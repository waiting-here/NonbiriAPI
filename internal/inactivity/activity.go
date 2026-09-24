package inactivity

import (
	"context"
	"database/sql"
	"errors"

	"github.com/waiting-here/NonbiriAPI/internal/useractivity"
)

type ActiveEvent = useractivity.ActiveEvent

func activityError(err error) error {
	if errors.Is(err, useractivity.ErrInvalid) {
		return ErrInvalid
	}
	return err
}

func InitializeTx(ctx context.Context, tx *sql.Tx, userID, at int64) error {
	return activityError(useractivity.InitializeTx(ctx, tx, userID, at))
}
func RecordActiveTx(ctx context.Context, tx *sql.Tx, event ActiveEvent) error {
	return activityError(useractivity.RecordActiveTx(ctx, tx, event))
}
func ResetObservationTx(ctx context.Context, tx *sql.Tx, userID, at int64) error {
	return activityError(useractivity.ResetObservationTx(ctx, tx, userID, at))
}
func RescheduleTx(ctx context.Context, tx *sql.Tx, userID int64) error {
	return activityError(useractivity.RescheduleTx(ctx, tx, userID))
}

// DonorAllowedTx is only for donor eligibility. It must never authorize the
// protected account's own login, CallerKey, or API requests.
func DonorAllowedTx(ctx context.Context, tx *sql.Tx, userID, at int64) (bool, error) {
	if tx == nil || userID <= 0 || !validTime(at) {
		return false, ErrInvalid
	}
	var allowed bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id=? AND is_admin=0 AND (is_banned=0 OR (banned_until IS NOT NULL AND banned_until<=?) OR (ban_kind='protective_inactivity' AND is_banned=1 AND banned_until IS NULL)))`, userID, at).Scan(&allowed)
	return allowed, err
}
