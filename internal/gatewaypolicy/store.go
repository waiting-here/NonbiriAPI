package gatewaypolicy

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type Store struct{ database *sql.DB }

func NewStore(database *sql.DB) *Store { return &Store{database: database} }

// Initialize imports the legacy startup configuration exactly once, including
// an empty configuration. A later environment value cannot restore deleted rows.
func (s *Store) Initialize(ctx context.Context, raw string, fallback Config) error {
	tx, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var exists int
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM gateway_model_capabilities_state WHERE singleton=1").Scan(&exists); err != nil {
		return err
	}
	if exists != 0 {
		return tx.Commit()
	}
	config := fallback
	if strings.TrimSpace(raw) != "" {
		config, err = Parse(raw)
		if err != nil {
			return err
		}
	}
	now := time.Now().UTC().Unix()
	for _, entry := range config.Entries() {
		policy, err := json.Marshal(entry.Policy)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO gateway_model_capabilities(base_url,model,policy_json,revision,updated_at) VALUES(?,?,?,1,?)", entry.BaseURL, entry.Model, string(policy), now); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO gateway_model_capabilities_state(singleton,initialized_at) VALUES(1,?)", now); err != nil {
		return err
	}
	return tx.Commit()
}

type Record struct {
	ID        string `json:"id"`
	Revision  string `json:"revision"`
	UpdatedAt int64  `json:"updated_at"`
	Entry
}
type Queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func ReadRecords(ctx context.Context, q Queryer) ([]Record, error) {
	rows, err := q.QueryContext(ctx, "SELECT id,base_url,model,policy_json,revision,updated_at FROM gateway_model_capabilities ORDER BY base_url,model")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Record{}
	for rows.Next() {
		var r Record
		var id, rev int64
		var policy string
		if err := rows.Scan(&id, &r.BaseURL, &r.Model, &policy, &rev, &r.UpdatedAt); err != nil {
			return nil, err
		}
		r.Policy, err = DecodePolicy([]byte(policy))
		if err != nil {
			return nil, err
		}
		r.ID, r.Revision = strconv.FormatInt(id, 10), strconv.FormatInt(rev, 10)
		result = append(result, r)
	}
	return result, rows.Err()
}

// LoadMany uses one SELECT so all route candidates observe one database snapshot.
func (s *Store) LoadMany(ctx context.Context, targets []Target) (map[Target]Model, error) {
	result := make(map[Target]Model, len(targets))
	if len(targets) == 0 {
		return result, nil
	}
	unique := make(map[Target]bool, len(targets))
	args := []any{}
	clauses := []string{}
	for _, target := range targets {
		canonical := target.canonical()
		if unique[canonical] {
			continue
		}
		unique[canonical] = true
		clauses = append(clauses, "(base_url=? AND model=?)")
		args = append(args, canonical.BaseURL, canonical.Model)
	}
	rows, err := s.database.QueryContext(ctx, "SELECT base_url,model,policy_json FROM gateway_model_capabilities WHERE "+strings.Join(clauses, " OR "), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	resolved := make(map[Target]Model, len(unique))
	for rows.Next() {
		var e Entry
		var raw string
		if err := rows.Scan(&e.BaseURL, &e.Model, &raw); err != nil {
			return nil, err
		}
		e.Policy, err = DecodePolicy([]byte(raw))
		if err != nil {
			return nil, errors.New("invalid stored Gateway policy")
		}
		normalized, m, err := Validate(e)
		if err != nil {
			return nil, fmt.Errorf("stored Gateway policy: %w", err)
		}
		resolved[Target{normalized.BaseURL, normalized.Model}] = m
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, t := range targets {
		result[t] = resolved[t.canonical()]
	}
	return result, nil
}
