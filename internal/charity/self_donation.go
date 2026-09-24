package charity

import (
	"context"
	"database/sql"

	"github.com/waiting-here/NonbiriAPI/internal/claim"
	"github.com/waiting-here/NonbiriAPI/internal/inactivity"
)

func requireDonationCallerTx(ctx context.Context, tx *sql.Tx, callerID, donorID, at int64) error {
	allowed, err := inactivity.DonorAllowedTx(ctx, tx, donorID, at)
	if err != nil {
		return err
	}
	if !allowed {
		return claim.ErrNotFound
	}
	if callerID != donorID {
		return nil
	}
	var exempt bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id=? AND is_admin=0 AND COALESCE(level,auto_level)=6)`, callerID).Scan(&exempt); err != nil {
		return err
	}
	if !exempt {
		return claim.ErrNotFound
	}
	return nil
}
