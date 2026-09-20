package antiabuse

import (
	"context"
	"database/sql"
	"math"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/charityrouting"
)

type windowEvent struct {
	At        int64  `json:"occurred_at"`
	RequestID string `json:"request_id"`
	Chars     *int   `json:"content_chars,omitempty"`
}

func (k windowKey) kind() string {
	if k.charity {
		return "short_content"
	}
	return "rpm"
}
func windowDuration(k windowKey, c Config) int64 {
	if k.charity {
		return max(c.CharityViolationWindowSeconds, c.CharitySuspendWindowSeconds)
	}
	return c.RPMBanWindowSeconds
}
func resetDone(k windowKey, w *violationWindow, now int64, c Config) {
	if k.charity {
		if c.CharityViolationBanThreshold == 0 || c.CharityViolationWindowBanSeconds == 0 || countEvents(w.events, now, c.CharityViolationWindowSeconds) < c.CharityViolationBanThreshold {
			w.banDone = false
		}
		if c.CharitySuspendThreshold == 0 || c.CharitySuspendDurationSeconds == 0 || countEvents(w.events, now, c.CharitySuspendWindowSeconds) < c.CharitySuspendThreshold {
			w.suspendDone = false
		}
	} else if c.RPMBanThreshold == 0 || countEvents(w.events, now, c.RPMBanWindowSeconds) < c.RPMBanThreshold {
		w.banDone = false
	}
}

// restore uses durable facts without replaying any penalty. It removes only
// events outside the current windows and refuses an over-capacity live store.
func (s *Service) restore() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	tx, err := s.config.Database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	r := &transactionConfig{ctx: ctx, tx: tx}
	cfg := readConfig(r)
	if r.err != nil {
		return r.err
	}
	now := s.config.Now().Unix()
	if now < 0 || now > 253402300799 {
		return charityrouting.ErrInvariant
	}
	if err = pruneWindowRows(ctx, tx, now, cfg); err != nil {
		return err
	}
	windows := make(map[windowKey]violationWindow)
	rows, err := tx.QueryContext(ctx, `SELECT user_id,violation_kind,ban_done,suspend_done,next_event_seq FROM abuse_windows LIMIT ?`, MaxWindowUsers+1)
	if err != nil {
		return err
	}
	for rows.Next() {
		var k windowKey
		var w violationWindow
		var kind string
		if err = rows.Scan(&k.userID, &kind, &w.banDone, &w.suspendDone, &w.next); err != nil {
			rows.Close()
			return err
		}
		k.charity = kind == "short_content"
		windows[k] = w
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if len(windows) > MaxWindowUsers {
		return charityrouting.ErrResourceLimit
	}
	rows, err = tx.QueryContext(ctx, `SELECT user_id,violation_kind,seq,occurred_at,request_id,content_chars FROM abuse_window_events ORDER BY user_id,violation_kind,seq LIMIT ?`, MaxWindowUsers*MaxEventsPerUser+1)
	if err != nil {
		return err
	}
	count := 0
	for rows.Next() {
		var k windowKey
		var kind string
		var seq int64
		var e windowEvent
		var chars sql.NullInt64
		if err = rows.Scan(&k.userID, &kind, &seq, &e.At, &e.RequestID, &chars); err != nil {
			rows.Close()
			return err
		}
		k.charity = kind == "short_content"
		w, ok := windows[k]
		if !ok || seq >= w.next || len(w.events) >= MaxViolationThreshold || count >= MaxWindowUsers*MaxEventsPerUser {
			rows.Close()
			return charityrouting.ErrResourceLimit
		}
		if chars.Valid {
			e.Chars = new(int(chars.Int64))
		}
		w.events = append(w.events, e.At)
		w.facts = append(w.facts, e)
		windows[k] = w
		count++
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for k, w := range windows {
		resetDone(k, &w, now, cfg)
		windows[k] = w
		if err = saveWindowFlags(ctx, tx, k, w, now); err != nil {
			return err
		}
	}
	if err = ExpireTx(ctx, tx, now, 10000); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	s.windows, s.events = windows, count
	return nil
}

func pruneWindowRows(ctx context.Context, tx *sql.Tx, now int64, cfg Config) error {
	for _, k := range []windowKey{{charity: false}, {charity: true}} {
		if _, err := tx.ExecContext(ctx, `DELETE FROM abuse_window_events WHERE violation_kind=? AND occurred_at<=?`, k.kind(), now-windowDuration(k, cfg)); err != nil {
			return err
		}
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM abuse_windows WHERE NOT EXISTS(SELECT 1 FROM abuse_window_events e WHERE e.user_id=abuse_windows.user_id AND e.violation_kind=abuse_windows.violation_kind)`)
	return err
}
func saveWindowFlags(ctx context.Context, tx *sql.Tx, k windowKey, w violationWindow, now int64) error {
	_, err := tx.ExecContext(ctx, `UPDATE abuse_windows SET ban_done=?,suspend_done=?,updated_at=? WHERE user_id=? AND violation_kind=?`, w.banDone, w.suspendDone, now, k.userID, k.kind())
	return err
}

// cleanupCopy never mutates a published slice. Rollback therefore preserves
// the exact committed mirror, including flags and events removed by a shrink.
func (s *Service) cleanupCopy(ctx context.Context, tx *sql.Tx, now int64, cfg Config, target windowKey) (map[windowKey]violationWindow, int, error) {
	if err := pruneWindowRows(ctx, tx, now, cfg); err != nil {
		return nil, 0, err
	}
	result := make(map[windowKey]violationWindow, len(s.windows))
	count := 0
	for k, old := range s.windows {
		w := old
		cut := now - windowDuration(k, cfg)
		expired := false
		for _, at := range old.events {
			if at <= cut {
				expired = true
				break
			}
		}
		if expired {
			w.events = nil
			w.facts = nil
			for i, at := range old.events {
				if at > cut {
					w.events = append(w.events, at)
					w.facts = append(w.facts, old.facts[i])
				}
			}
		}
		if k == target {
			// The incoming event is included before the ordinary threshold
			// crossing decision. Disabled switches reset immediately.
			if !k.charity && cfg.RPMBanThreshold == 0 || k.charity && (cfg.CharityViolationBanThreshold == 0 || cfg.CharityViolationWindowBanSeconds == 0) {
				w.banDone = false
			}
			if k.charity && (cfg.CharitySuspendThreshold == 0 || cfg.CharitySuspendDurationSeconds == 0) {
				w.suspendDone = false
			}
		} else {
			resetDone(k, &w, now, cfg)
		}
		if len(w.events) == 0 {
			continue
		}
		if old.banDone != w.banDone || old.suspendDone != w.suspendDone {
			if err := saveWindowFlags(ctx, tx, k, w, now); err != nil {
				return nil, 0, err
			}
		}
		result[k] = w
		count += len(w.events)
	}
	return result, count, nil
}

func persistWindow(ctx context.Context, tx *sql.Tx, k windowKey, w *violationWindow, now int64) error {
	if w.next == 0 {
		w.next = 1
	}
	if w.next == math.MaxInt64 {
		return charityrouting.ErrResourceLimit
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO abuse_windows(user_id,violation_kind,ban_done,suspend_done,next_event_seq,updated_at) VALUES(?,?,?,?,?,?) ON CONFLICT(user_id,violation_kind) DO UPDATE SET ban_done=excluded.ban_done,suspend_done=excluded.suspend_done,next_event_seq=excluded.next_event_seq,updated_at=excluded.updated_at`, k.userID, k.kind(), w.banDone, w.suspendDone, w.next+1, now); err != nil {
		return err
	}
	e := w.facts[len(w.facts)-1]
	_, err := tx.ExecContext(ctx, `INSERT INTO abuse_window_events(user_id,violation_kind,seq,occurred_at,request_id,content_chars) VALUES(?,?,?,?,?,?)`, k.userID, k.kind(), w.next, e.At, e.RequestID, e.Chars)
	if err == nil {
		w.next++
	}
	return err
}
