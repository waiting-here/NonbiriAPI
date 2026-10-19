package claim

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/donationquota"
)

func (s *Service) quotaCapacityFailure(ctx context.Context, tx *sql.Tx, at int64, cause error, finish func(error)) error {
	if !errors.Is(cause, donationquota.ErrCapacity) {
		return cause
	}
	if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
		return errors.Join(cause, err)
	}
	// A measured transaction owns its connection until finish, even after rollback.
	if finish != nil {
		finish(cause)
	}
	alertCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	_, err := s.db.ExecContext(alertCtx, `INSERT INTO admin_alerts(kind,message,ref,created_at,resolved)
SELECT 'forward_error','Recurring charity quota storage is at capacity. New calls are paused; accepted calls retain their reserved settlement space.','charity_recurring_capacity',?,0
WHERE NOT EXISTS(SELECT 1 FROM admin_alerts WHERE kind='forward_error' AND ref='charity_recurring_capacity' AND resolved=0)`, at)
	if err != nil {
		return errors.Join(cause, fmt.Errorf("claim: record quota capacity alert: %w", err))
	}
	return cause
}
