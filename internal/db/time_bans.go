package db

// Temporal ban and charity-suspension lifecycle: deadline-based states are
// lifted lazily by an atomic conditional UPDATE on read instead of a cron
// sweep, so no scheduler race can revive or extend a ban and no read can
// observe a due-but-still-flagged state. A lift is recorded as a controlled
// structured log line; it never creates an administrator alert.
//
// Consistency rule: is_banned=1 with banned_until NULL is permanent and never
// lifts itself. is_banned=1 with banned_until <= now counts as not banned at
// every auth predicate AND triggers the same conditional lift UPDATE, so the
// SQL predicate and the stored row converge on the first read after expiry.

import (
	"database/sql"
	"fmt"
	"log/slog"
	"time"
)

// liftDueUserBanSQL clears a ban whose deadline has passed. The WHERE clause
// re-checks the deadline so a concurrent manual re-ban (which may set a new
// future deadline or a permanent one) can never be clobbered by a stale lift.
const liftDueUserBanSQL = `UPDATE users
	SET is_banned=0, banned_reason='', banned_until=NULL, updated_at=?
	WHERE id=? AND is_banned=1 AND banned_until IS NOT NULL AND banned_until<=?`

// clearDueCharitySuspensionSQL clears an expired charity-eligibility
// suspension deadline.
const clearDueCharitySuspensionSQL = `UPDATE users
	SET charity_suspended_until=NULL, updated_at=?
	WHERE id=? AND charity_suspended_until IS NOT NULL AND charity_suspended_until<=?`

// liftDueUserBanTx lifts one due ban inside an existing transaction and
// reports whether a row changed. The caller owns commit/rollback.
func liftDueUserBanTx(tx *sql.Tx, userID, nowUnix int64) (bool, error) {
	result, err := tx.Exec(liftDueUserBanSQL, nowUnix, userID, nowUnix)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return count == 1, nil
}

// clearDueCharitySuspensionTx clears one expired suspension inside an
// existing transaction and reports whether a row changed.
func clearDueCharitySuspensionTx(tx *sql.Tx, userID, nowUnix int64) (bool, error) {
	result, err := tx.Exec(clearDueCharitySuspensionSQL, nowUnix, userID, nowUnix)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return count == 1, nil
}

// reconcileUserTemporalState applies lazy on-read recovery for a single user
// row that was just read outside an open transaction: a due ban deadline or
// a due suspension deadline is lifted by its atomic conditional UPDATE and
// the in-memory projection is corrected to match. A permanent ban
// (banned_until NULL) is never touched. Failures are logged and left to the
// next read: every auth predicate already treats a due deadline as "not
// banned", so a failed lift cannot extend anyone's ban.
func (s *Store) reconcileUserTemporalState(u *User, now time.Time) {
	if u == nil || u.IsAdmin || s == nil {
		return
	}
	nowUnix := now.Unix()
	if u.IsBanned && u.BannedUntil != nil && u.BannedUntil.Unix() <= nowUnix {
		lifted, err := s.liftDueUserBan(u.ID, nowUnix)
		if err != nil {
			slog.Warn("lazy ban lift failed; treating deadline ban as expired on read", "user_id", u.ID, "err", err)
		} else if lifted {
			slog.Info("temporal user ban expired and was lifted lazily", "user_id", u.ID)
		}
		u.IsBanned = false
		u.BannedReason = ""
		u.BannedUntil = nil
	}
	if u.CharitySuspendedUntil != nil && u.CharitySuspendedUntil.Unix() <= nowUnix {
		cleared, err := s.clearDueCharitySuspension(u.ID, nowUnix)
		if err != nil {
			slog.Warn("lazy charity suspension clear failed", "user_id", u.ID, "err", err)
		} else if cleared {
			slog.Info("charity eligibility suspension expired and was cleared lazily", "user_id", u.ID)
		}
		u.CharitySuspendedUntil = nil
	}
}

func (s *Store) liftDueUserBan(userID, nowUnix int64) (bool, error) {
	result, err := s.db.Exec(liftDueUserBanSQL, nowUnix, userID, nowUnix)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count == 1, err
}

func (s *Store) clearDueCharitySuspension(userID, nowUnix int64) (bool, error) {
	result, err := s.db.Exec(clearDueCharitySuspensionSQL, nowUnix, userID, nowUnix)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count == 1, err
}

// SuspendCharityUntil atomically applies a charity-only suspension deadline.
// It never changes is_banned, sessions, or caller keys. A later deadline may
// replace an earlier one, but a concurrent stale action can never shorten an
// already stronger suspension. Due deadlines are treated as expired by the
// same conditional expression used by the lazy read path.
func (s *Store) SuspendCharityUntil(userID, nowUnix, durationSeconds int64) error {
	if userID <= 0 || nowUnix <= 0 || durationSeconds <= 0 || durationSeconds > MaxBanDurationSeconds {
		return ErrConflict
	}
	until := nowUnix + durationSeconds
	if until < nowUnix {
		return ErrConflict
	}
	result, err := s.db.Exec(`UPDATE users
		SET charity_suspended_until=CASE
			WHEN charity_suspended_until IS NULL OR charity_suspended_until<=? OR charity_suspended_until<? THEN ?
			ELSE charity_suspended_until END,
			updated_at=?
		WHERE id=? AND is_admin=0`, nowUnix, until, until, nowUnix, userID)
	if err != nil {
		return fmt.Errorf("suspend charity: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("suspend charity: %w", err)
	}
	if count != 1 {
		return ErrNotFound
	}
	return nil
}
