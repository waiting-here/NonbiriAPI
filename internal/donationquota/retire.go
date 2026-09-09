package donationquota

import (
	"context"
	"database/sql"
)

// RetireDonation prepares a terminal donation for bounded garbage collection.
// The caller has already checked its retention deadline and legal holds. The
// parent must remain until its rules and aggregate rows have been collected.
func RetireDonation(ctx context.Context, tx *sql.Tx, donationID, now int64, budget int) (bool, int, error) {
	if donationID <= 0 || !validNow(now) || budget < 1 || budget > CleanupBatch/2 {
		return false, 0, ErrInvalid
	}
	var active bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM donation_quota_receipts x JOIN donation_quota_rules r ON r.id=x.rule_id JOIN donation_keys k ON k.id=r.donation_key_id WHERE k.donation_id=? AND x.state<>'settled')`, donationID).Scan(&active); err != nil {
		return false, 0, err
	}
	if active {
		return false, 0, ErrConflict
	}
	rows, err := tx.QueryContext(ctx, `SELECT r.id,r.current_epoch FROM donation_quota_rules r JOIN donation_keys k ON k.id=r.donation_key_id WHERE k.donation_id=? AND r.current_epoch IS NOT NULL ORDER BY r.id LIMIT ?`, donationID, budget)
	if err != nil {
		return false, 0, err
	}
	type target struct {
		id     string
		number int64
	}
	var targets []target
	for rows.Next() {
		var value target
		if err := rows.Scan(&value.id, &value.number); err != nil {
			rows.Close()
			return false, 0, err
		}
		targets = append(targets, value)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return false, 0, err
	}
	for _, value := range targets {
		if err := oneRow(tx.ExecContext(ctx, `UPDATE donation_quota_epochs SET retired_at=max(?,effective_at,last_observed_at) WHERE rule_id=? AND epoch=? AND retired_at IS NULL`, now, value.id, value.number)); err != nil {
			return false, 0, err
		}
		if err := oneRow(tx.ExecContext(ctx, `UPDATE donation_quota_rules SET current_epoch=NULL,display_order=NULL WHERE id=? AND current_epoch=?`, value.id, value.number)); err != nil {
			return false, 0, err
		}
	}
	var remaining bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM donation_quota_rules r JOIN donation_keys k ON k.id=r.donation_key_id WHERE k.donation_id=?)`, donationID).Scan(&remaining)
	return !remaining, len(targets), err
}
