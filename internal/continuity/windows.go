package continuity

import (
	"context"
	"database/sql"
)

type WindowKind string

const (
	UserRPM           WindowKind = "user_rpm"
	GameStart         WindowKind = "game_start"
	AbuseRPM          WindowKind = "abuse_rpm"
	AbuseShortContent WindowKind = "abuse_short_content"
	MaxWindowEvents              = 10000
)

type WindowEvent struct {
	ID            string
	AtMillis      int64
	ExpiresMillis int64
	Count         int64
	Value         *int64
}

type WindowPreserver interface {
	PreserveWindowTx(context.Context, *sql.Tx, int64, int64) error
}

// AttachWindowPreservers is a one-time startup composition step. Each owner
// copies only its still-live facts inside the account coordinator's TX.
func (service *Service) AttachWindowPreservers(owners ...WindowPreserver) error {
	if service == nil || len(owners) == 0 || len(owners) > 4 {
		return ErrInvalid
	}
	for _, owner := range owners {
		if owner == nil {
			return ErrInvalid
		}
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	if service.closed || len(service.windows) != 0 {
		return ErrInvalid
	}
	service.windows = append([]WindowPreserver(nil), owners...)
	return nil
}

func validWindow(kind WindowKind, scope string, nowMillis int64, limit int) bool {
	return (kind == UserRPM || kind == GameStart || kind == AbuseRPM || kind == AbuseShortContent) && validLabel(scope) && nowMillis >= 0 && nowMillis <= maxUnixSecond*1000 && limit > 0 && limit <= MaxWindowEvents
}

func (service *Service) LoadWindow(ctx context.Context, userID int64, kind WindowKind, scope string, nowMillis int64, limit int) (Key, []WindowEvent, error) {
	if service == nil || ctx == nil || !validWindow(kind, scope, nowMillis, limit) {
		return Key{}, nil, ErrInvalid
	}
	tx, err := service.database.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Key{}, nil, err
	}
	defer tx.Rollback()
	key, err := service.UserKeyTx(ctx, tx, userID)
	if err != nil {
		return Key{}, nil, err
	}
	events, err := LoadWindowTx(ctx, tx, key, kind, scope, nowMillis, limit)
	if err != nil {
		return Key{}, nil, err
	}
	return key, events, tx.Commit()
}

func LoadWindowTx(ctx context.Context, tx *sql.Tx, key Key, kind WindowKind, scope string, nowMillis int64, limit int) ([]WindowEvent, error) {
	if ctx == nil || tx == nil || !validWindow(kind, scope, nowMillis, limit) {
		return nil, ErrInvalid
	}
	rows, err := tx.QueryContext(ctx, `SELECT event_key,occurred_at_ms,expires_at_ms,count,value FROM identity_window_events WHERE identity_key=? AND kind=? AND scope=? AND expires_at_ms>? ORDER BY occurred_at_ms,event_key LIMIT ?`, key[:], string(kind), scope, nowMillis, limit+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := []WindowEvent{}
	for rows.Next() {
		var event WindowEvent
		if err := rows.Scan(&event.ID, &event.AtMillis, &event.ExpiresMillis, &event.Count, &event.Value); err != nil {
			return nil, err
		}
		if event.AtMillis > nowMillis || len(events) == limit {
			return nil, ErrInvariant
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

// SaveWindowTx merges by immutable event identity. Repeated deletion and
// re-registration cannot double count an imported event or extend its expiry.
func SaveWindowTx(ctx context.Context, tx *sql.Tx, key Key, kind WindowKind, scope string, nowMillis int64, events []WindowEvent) error {
	if ctx == nil || tx == nil || !validWindow(kind, scope, nowMillis, MaxWindowEvents) || len(events) > MaxWindowEvents {
		return ErrInvalid
	}
	for _, event := range events {
		if !validLabel(event.ID) || event.AtMillis < 0 || event.AtMillis > nowMillis || event.ExpiresMillis <= event.AtMillis || event.ExpiresMillis > maxUnixSecond*1000 || event.Count < 1 || event.Value != nil && *event.Value < 0 {
			return ErrInvalid
		}
		if event.ExpiresMillis <= nowMillis {
			continue
		}
		result, err := tx.ExecContext(ctx, `INSERT INTO identity_window_events(identity_key,kind,scope,event_key,occurred_at_ms,expires_at_ms,count,value) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(identity_key,kind,scope,event_key) DO UPDATE SET event_key=excluded.event_key WHERE occurred_at_ms=excluded.occurred_at_ms AND expires_at_ms=excluded.expires_at_ms AND count=excluded.count AND value IS excluded.value`, key[:], string(kind), scope, event.ID, event.AtMillis, event.ExpiresMillis, event.Count, event.Value)
		if err != nil {
			return err
		}
		count, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if count != 1 {
			return ErrInvariant
		}
	}
	return nil
}
