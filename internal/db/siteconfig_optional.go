package db

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// DeleteSiteConfigValueWithActivity removes one optional site_config
// override and records the administrator's console write in the same
// transaction. A missing row is an idempotent successful reset. The activity
// half follows the same timezone-unavailable behavior as the upsert path.
func (s *Store) DeleteSiteConfigValueWithActivity(ctx context.Context, key string, actorUserID int64, at time.Time) error {
	if err := validateSiteConfigText(key, maxSiteConfigKeyBytes, false, false); err != nil {
		return ErrConflict
	}
	if actorUserID <= 0 || at.IsZero() {
		return fmt.Errorf("delete site configuration with activity: actor and time are required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("delete site configuration: begin: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if _, err := tx.ExecContext(ctx, `DELETE FROM site_config WHERE key=?`, key); err != nil {
		return fmt.Errorf("delete site configuration: %w", err)
	}
	dayKey, err := siteDayKeyAtTx(tx, at.Unix())
	if errors.Is(err, ErrTimezoneUnavailable) {
		// Activity is disabled until the site timezone is configured.
	} else if err != nil {
		return fmt.Errorf("delete site configuration: resolve day key: %w", err)
	} else if _, err := recordActivityTx(ctx, tx, actorUserID, dayKey, ActivityDelta{ConsoleWrites: 1}, at.Unix()); err != nil {
		return fmt.Errorf("delete site configuration: record activity: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("delete site configuration: commit: %w", err)
	}
	committed = true
	return nil
}
