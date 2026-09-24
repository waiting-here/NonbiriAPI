package inactivity

import (
	"context"
	"database/sql"
	"math"
)

type ActiveEvent struct {
	UserID, At int64
	Kind       string
	Fresh      bool
}

func InitializeTx(ctx context.Context, tx *sql.Tx, userID, at int64) error {
	if tx == nil || userID <= 0 || !validTime(at) {
		return ErrInvalid
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO user_activity_state(user_id,observation_started_at) SELECT id,? FROM users WHERE id=? ON CONFLICT(user_id) DO NOTHING`, at, userID)
	return err
}

// RecordActiveTx belongs inside the winning business transaction. Replay,
// failed actions, passive income and status polling cannot refresh activity.
func RecordActiveTx(ctx context.Context, tx *sql.Tx, event ActiveEvent) error {
	if !event.Fresh {
		return nil
	}
	if tx == nil || event.UserID <= 0 || !validTime(event.At) {
		return ErrInvalid
	}
	switch event.Kind {
	case "login", "api", "checkin", "welfare", "game", "activity":
	default:
		return ErrInvalid
	}
	if err := InitializeTx(ctx, tx, event.UserID, event.At); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE user_activity_state SET last_active_at=max(observation_started_at,COALESCE(last_active_at,0),?),activity_seq=activity_seq+1,activity_epoch=activity_epoch+1,last_decay_at=NULL,schedule_revision=0,next_due_at=NULL WHERE user_id=? AND activity_seq<? AND activity_epoch<? AND EXISTS(SELECT 1 FROM users WHERE id=user_id AND (is_banned=0 OR (banned_until IS NOT NULL AND banned_until<=?)))`, event.At, event.UserID, int64(math.MaxInt64), int64(math.MaxInt64), event.At)
	return err
}

// ResetObservationTx must run before clearing a protective ban marker.
func ResetObservationTx(ctx context.Context, tx *sql.Tx, userID, at int64) error {
	if tx == nil || userID <= 0 || !validTime(at) {
		return ErrInvalid
	}
	_, err := tx.ExecContext(ctx, `UPDATE user_activity_state SET observation_started_at=?,last_active_at=NULL,activity_seq=activity_seq+1,activity_epoch=activity_epoch+1,last_decay_at=NULL,schedule_revision=0,next_due_at=NULL WHERE user_id=? AND activity_seq<? AND activity_epoch<? AND EXISTS(SELECT 1 FROM users WHERE id=user_id AND ban_kind='protective_inactivity')`, at, userID, int64(math.MaxInt64), int64(math.MaxInt64))
	return err
}
func RescheduleTx(ctx context.Context, tx *sql.Tx, userID int64) error {
	if tx == nil || userID <= 0 {
		return ErrInvalid
	}
	_, err := tx.ExecContext(ctx, `UPDATE user_activity_state SET schedule_revision=0,next_due_at=NULL WHERE user_id=?`, userID)
	return err
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
