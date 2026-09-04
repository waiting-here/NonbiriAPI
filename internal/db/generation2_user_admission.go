package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

const (
	// MaxUserRPMLimit is the closed storage and runtime range for an explicit
	// per-user request-per-minute override.
	MaxUserRPMLimit = 4096
	// DefaultUserConcurrencyLimit is the effective value when a user has no
	// explicit in-flight request override.
	DefaultUserConcurrencyLimit = 5
	// MaxUserConcurrencyLimit is the closed storage and runtime range for an
	// explicit per-user in-flight request override.
	MaxUserConcurrencyLimit = 100000
)

// UserAdmissionLimits is the single live account snapshot consumed by the
// process-wide request admission boundary. RPMLimitSet preserves the
// distinction between an explicit override and the current site default.
type UserAdmissionLimits struct {
	RPMLimit         int
	RPMLimitSet      bool
	ConcurrencyLimit int
}

// GetUserAdmissionLimits resolves only a callable, non-administrator account.
// A missing, permanently banned, or still-temporarily-banned identity is
// indistinguishable from an unknown identity at this boundary.
func (s *Store) GetUserAdmissionLimits(ctx context.Context, userID int64) (UserAdmissionLimits, error) {
	if s == nil || s.db == nil || ctx == nil || userID <= 0 {
		return UserAdmissionLimits{}, ErrNotFound
	}
	var rpm, concurrency sql.NullInt64
	err := s.db.QueryRowContext(ctx, `
SELECT rpm_limit, concurrency_limit
FROM users
WHERE id=? AND is_admin=0
  AND (is_banned=0 OR (banned_until IS NOT NULL AND banned_until<=?))`, userID, time.Now().Unix()).Scan(&rpm, &concurrency)
	if errors.Is(err, sql.ErrNoRows) {
		return UserAdmissionLimits{}, ErrNotFound
	}
	if err != nil {
		return UserAdmissionLimits{}, fmt.Errorf("read user admission limits: %w", err)
	}

	out := UserAdmissionLimits{ConcurrencyLimit: DefaultUserConcurrencyLimit}
	if rpm.Valid {
		if rpm.Int64 < 1 || rpm.Int64 > MaxUserRPMLimit {
			return UserAdmissionLimits{}, errors.New("read user admission limits: invalid rpm limit")
		}
		out.RPMLimit = int(rpm.Int64)
		out.RPMLimitSet = true
	}
	if concurrency.Valid {
		if concurrency.Int64 < 1 || concurrency.Int64 > MaxUserConcurrencyLimit {
			return UserAdmissionLimits{}, errors.New("read user admission limits: invalid concurrency limit")
		}
		out.ConcurrencyLimit = int(concurrency.Int64)
	}
	return out, nil
}
