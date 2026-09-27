package antiabuse

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"

	"github.com/waiting-here/NonbiriAPI/internal/charityrouting"
	"github.com/waiting-here/NonbiriAPI/internal/continuity"
)

type continuationFlags struct {
	BanDone     bool `json:"ban_done"`
	SuspendDone bool `json:"suspend_done"`
}

func (k windowKey) continuityKind() continuity.WindowKind {
	if k.charity {
		return continuity.AbuseShortContent
	}
	return continuity.AbuseRPM
}

// PreserveWindowTx reads the authoritative rows without taking the memory
// gate inside SQLite. Cleanup takes the opposite order, so acquiring it here
// would deadlock. The outer identity gate has already drained user requests.
func (s *Service) PreserveWindowTx(ctx context.Context, tx *sql.Tx, userID, now int64) error {
	if s == nil || s.config.Continuity == nil {
		return charityrouting.ErrUnavailable
	}
	identity, err := s.config.Continuity.UserKeyTx(ctx, tx, userID)
	if err != nil {
		return err
	}
	reader := &transactionConfig{ctx: ctx, tx: tx}
	cfg := readConfig(reader)
	if reader.err != nil {
		return reader.err
	}
	for _, key := range []windowKey{{userID: userID}, {userID: userID, charity: true}} {
		var flags continuationFlags
		err := tx.QueryRowContext(ctx, `SELECT ban_done,suspend_done FROM abuse_windows WHERE user_id=? AND violation_kind=?`, userID, key.kind()).Scan(&flags.BanDone, &flags.SuspendDone)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		duration := windowDuration(key, cfg)
		rows, err := tx.QueryContext(ctx, `SELECT request_id,occurred_at,COALESCE(expires_at,MIN(253402300799,occurred_at+?)),content_chars FROM abuse_window_events WHERE user_id=? AND violation_kind=? AND occurred_at>? AND (expires_at IS NULL OR expires_at>?) ORDER BY occurred_at,seq LIMIT ?`, duration, userID, key.kind(), now-duration, now, MaxViolationThreshold+1)
		if err != nil {
			return err
		}
		events := []continuity.WindowEvent{}
		var expiry int64
		for rows.Next() {
			var event continuity.WindowEvent
			var at, end int64
			if err := rows.Scan(&event.ID, &at, &end, &event.Value); err != nil {
				rows.Close()
				return err
			}
			if len(events) == MaxViolationThreshold {
				rows.Close()
				return charityrouting.ErrResourceLimit
			}
			event.AtMillis, event.ExpiresMillis, event.Count = at*1000, end*1000, 1
			events = append(events, event)
			expiry = max(expiry, end)
		}
		err = rows.Err()
		if closeErr := rows.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			return err
		}
		if len(events) == 0 {
			continue
		}
		if err := continuity.SaveWindowTx(ctx, tx, identity, key.continuityKind(), "v1", now*1000, events); err != nil {
			return err
		}
		raw, err := json.Marshal(flags)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO identity_continuity_facts(identity_key,kind,scope,window_key,fact_json,occurred_at,expires_at) VALUES(?,'abuse_state',?,'v1',?,?,?) ON CONFLICT(identity_key,kind,scope,window_key) DO UPDATE SET fact_json=excluded.fact_json,occurred_at=excluded.occurred_at,expires_at=excluded.expires_at`, identity[:], key.kind(), string(raw), now, expiry)
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) mergeContinuityWindow(ctx context.Context, tx *sql.Tx, key windowKey, window *violationWindow, now int64, cfg Config) (int, error) {
	if s.config.Continuity == nil {
		return 0, nil
	}
	identity, err := s.config.Continuity.UserKeyTx(ctx, tx, key.userID)
	if err != nil {
		return 0, err
	}
	events, err := continuity.LoadWindowTx(ctx, tx, identity, key.continuityKind(), "v1", now*1000, MaxViolationThreshold)
	if err != nil {
		return 0, err
	}
	if len(events) == 0 {
		return 0, nil
	}
	seen := make(map[string]windowEvent, len(window.facts))
	for _, event := range window.facts {
		seen[event.RequestID] = event
	}
	added := []windowEvent{}
	for _, event := range events {
		if event.Count != 1 || event.AtMillis%1000 != 0 || event.ExpiresMillis%1000 != 0 || !key.charity && event.Value != nil || event.Value != nil && *event.Value > MaxCharityContentRuneCount {
			return 0, continuity.ErrInvariant
		}
		if event.AtMillis/1000 <= now-windowDuration(key, cfg) {
			continue
		}
		if prior, found := seen[event.ID]; found {
			if prior.At != event.AtMillis/1000 || prior.Expires != event.ExpiresMillis/1000 {
				return 0, continuity.ErrInvariant
			}
			continue
		}
		fact := windowEvent{At: event.AtMillis / 1000, Expires: event.ExpiresMillis / 1000, RequestID: event.ID}
		if event.Value != nil {
			fact.Chars = new(int(*event.Value))
		}
		added = append(added, fact)
		seen[event.ID] = fact
	}
	if len(added)+len(window.events) > MaxViolationThreshold {
		return 0, charityrouting.ErrResourceLimit
	}
	if len(added) == 0 {
		return 0, nil
	}
	var raw string
	err = tx.QueryRowContext(ctx, `SELECT fact_json FROM identity_continuity_facts WHERE identity_key=? AND kind='abuse_state' AND scope=? AND window_key='v1' AND expires_at>?`, identity[:], key.kind(), now).Scan(&raw)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	if err == nil {
		var flags continuationFlags
		if err := json.Unmarshal([]byte(raw), &flags); err != nil {
			return 0, err
		}
		window.banDone = window.banDone || flags.BanDone
		window.suspendDone = window.suspendDone || flags.SuspendDone
	}
	window.events = append([]int64(nil), window.events...)
	window.facts = append([]windowEvent(nil), window.facts...)
	for _, event := range added {
		window.events = append(window.events, event.At)
		window.facts = append(window.facts, event)
		if err := persistWindow(ctx, tx, key, window, now); err != nil {
			return 0, err
		}
	}
	// Evidence and subsequent cleanup retain chronological event order.
	sort.Slice(window.facts, func(i, j int) bool {
		if window.facts[i].At == window.facts[j].At {
			return window.facts[i].RequestID < window.facts[j].RequestID
		}
		return window.facts[i].At < window.facts[j].At
	})
	for i, event := range window.facts {
		window.events[i] = event.At
	}
	resetDone(key, window, now, cfg)
	return len(added), nil
}
