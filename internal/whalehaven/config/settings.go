// Package config owns Whale Haven's versioned economic settings.
package config

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

type Settings struct {
	Enabled         bool             `json:"enabled"`
	ExchangeEnabled bool             `json:"exchange_enabled"`
	Values          map[string]int64 `json:"values"`
}

func Defaults() Settings {
	s := Settings{Values: make(map[string]int64, len(fields))}
	for _, spec := range fields {
		s.Values[spec.Key] = spec.Initial
	}
	return s
}

func (s Settings) Value(key string) int64 { return s.Values[key] }

type Epoch struct {
	AtMS     int64
	Settings Settings
	Master   bool
}

// New fields take their defaults when replaying a document written by an older version.
func decode(raw string) (Settings, error) {
	s := Defaults()
	err := json.Unmarshal([]byte(raw), &s)
	if err == nil {
		err = s.validate()
	}
	return s, err
}

func Read(ctx context.Context, tx *sql.Tx) (Settings, int64, error) {
	var revision int64
	var raw string
	err := tx.QueryRowContext(ctx, "SELECT revision, document FROM whale_haven_settings ORDER BY revision DESC LIMIT 1").Scan(&revision, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return Defaults(), 1, nil
	}
	if err != nil {
		return Settings{}, 0, err
	}
	s, err := decode(raw)
	return s, revision, err
}

// History returns the state at fromMS followed by effective changes through toMS.
// Equal timestamps are coalesced, including back-to-back edits in the same millisecond.
func History(ctx context.Context, tx *sql.Tx, fromMS, toMS int64) ([]Epoch, error) {
	if fromMS > toMS {
		return nil, fmt.Errorf("invalid whale history interval")
	}
	cur := Epoch{AtMS: fromMS, Settings: Defaults()}
	var raw string
	err := tx.QueryRowContext(ctx, "SELECT document FROM whale_haven_settings WHERE effective_at<=? ORDER BY effective_at DESC, revision DESC LIMIT 1", fromMS).Scan(&raw)
	if err == nil {
		cur.Settings, err = decode(raw)
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	err = tx.QueryRowContext(ctx, "SELECT enabled FROM whale_haven_master_history WHERE effective_at<=? ORDER BY effective_at DESC LIMIT 1", fromMS).Scan(&cur.Master)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	result := []Epoch{cur}
	rows, err := tx.QueryContext(ctx, `SELECT effective_at, revision, document, NULL FROM whale_haven_settings WHERE effective_at>? AND effective_at<=?
 UNION ALL SELECT effective_at, 0, NULL, enabled FROM whale_haven_master_history WHERE effective_at>? AND effective_at<=?
 ORDER BY 1,2`, fromMS, toMS, fromMS, toMS)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var revision int64
		var doc sql.NullString
		var master sql.NullBool
		if err := rows.Scan(&cur.AtMS, &revision, &doc, &master); err != nil {
			return nil, err
		}
		if master.Valid {
			cur.Master = master.Bool
		} else {
			cur.Settings, err = decode(doc.String)
			if err != nil {
				return nil, err
			}
		}
		if result[len(result)-1].AtMS == cur.AtMS {
			result[len(result)-1] = cur
		} else {
			result = append(result, cur)
		}
	}
	return result, rows.Err()
}
