package donationquota

import (
	"context"
	"database/sql"
)

func changeCapacity(ctx context.Context, tx *sql.Tx, used, held int) error {
	return oneRow(tx.ExecContext(ctx, `UPDATE donation_quota_capacity SET rows_used=rows_used+?,rows_held=rows_held+? WHERE id=1 AND rows_used+?>=0 AND rows_held+?>=0 AND rows_used+rows_held+?+?<=?`, used, held, used, held, used, held, MaxRows))
}

func holdCapacity(ctx context.Context, tx *sql.Tx, n int, now int64) error {
	if n == 0 {
		return nil
	}
	for attempt := 0; attempt < 2; attempt++ {
		var used, held int
		if err := tx.QueryRowContext(ctx, `SELECT rows_used,rows_held FROM donation_quota_capacity WHERE id=1`).Scan(&used, &held); err != nil {
			return err
		}
		if used < 0 || held < 0 || used+held > MaxRows {
			return ErrInvariant
		}
		if n <= MaxRows-used-held {
			return changeCapacity(ctx, tx, 0, n)
		}
		if attempt == 0 {
			if _, err := Cleanup(ctx, tx, now, CleanupBatch); err != nil {
				return err
			}
		}
	}
	return ErrCapacity
}
