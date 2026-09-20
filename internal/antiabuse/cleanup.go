package antiabuse

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/charityrouting"
)

func DeleteTx(ctx context.Context, tx *sql.Tx, user int64) error {
	for _, table := range []string{"abuse_cases", "abuse_windows"} {
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE user_id=?`, user); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) runCleanup(ctx context.Context) {
	timer := time.NewTicker(time.Minute)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			batch, cancel := context.WithTimeout(ctx, 5*time.Second)
			err := s.Cleanup(batch)
			cancel()
			if err != nil && ctx.Err() == nil {
				slog.Error("abuse retention cleanup failed", "error", err)
			}
		}
	}
}

// Cleanup follows the same window-gate/transaction order as requests, removes
// only expired facts, and never repeats or changes a user's punishment.
func (s *Service) Cleanup(ctx context.Context) error {
	select {
	case s.gate <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-s.gate }()
	if s.closed {
		return charityrouting.ErrUnavailable
	}
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
	windows, events, err := s.cleanupCopy(ctx, tx, now, cfg, windowKey{})
	if err != nil {
		return err
	}
	if err := PruneTx(ctx, tx, now, 512); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.windows, s.events = windows, events
	return nil
}
