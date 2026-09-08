package donationquota

import (
	"context"
	"database/sql"

	"github.com/waiting-here/NonbiriAPI/internal/calendar"
)

// Cleanup removes at most budget quota rows. Unfinished receipts and their
// aggregate rows remain until normal recovery completes their accounting.
func Cleanup(ctx context.Context, tx *sql.Tx, now int64, budget int) (int, error) {
	if !validNow(now) || budget < 1 || budget > CleanupBatch {
		return 0, ErrInvalid
	}
	deleted := 0
	result, err := tx.ExecContext(ctx, `DELETE FROM donation_quota_receipts WHERE (claim_id,rule_id,epoch) IN (
SELECT r.claim_id,r.rule_id,r.epoch FROM donation_quota_receipts r JOIN dispatch_claims c ON c.id=r.claim_id
WHERE r.state='settled' AND c.state IN ('committed','released') ORDER BY r.claim_id,r.rule_id,r.epoch LIMIT ?)`, budget)
	if err != nil {
		return deleted, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return deleted, err
	}
	deleted += int(n)
	type candidate struct {
		id         string
		number, at int64
	}
	for _, kind := range []string{"period", "bucket"} {
		if deleted == budget {
			return deleted, nil
		}
		table, column, age := "donation_quota_periods", "start_at", `a.end_at<=max(?,COALESCE(e.last_observed_at,e.effective_at))`
		cutoff := now
		if kind == "bucket" {
			table, column, age = "donation_quota_buckets", "success_at", `a.success_at<=?`
			cutoff = now - calendar.MaxLookbackSeconds
		}
		query := `SELECT a.rule_id,a.epoch,a.` + column + ` FROM ` + table + ` a JOIN donation_quota_epochs e ON e.rule_id=a.rule_id AND e.epoch=a.epoch
WHERE (` + age + ` OR (e.retired_at IS NOT NULL AND NOT EXISTS(SELECT 1 FROM donation_quota_receipts r WHERE r.rule_id=e.rule_id AND r.epoch=e.epoch)))
AND NOT EXISTS(SELECT 1 FROM donation_quota_receipts r WHERE r.rule_id=a.rule_id AND r.epoch=a.epoch AND `
		if kind == "period" {
			query += `r.period_start=a.start_at)`
		} else {
			query += `r.success_at=a.success_at)`
		}
		query += ` ORDER BY a.rule_id,a.epoch,a.` + column + ` LIMIT ?`
		rows, err := tx.QueryContext(ctx, query, cutoff, budget-deleted)
		if err != nil {
			return deleted, err
		}
		var candidates []candidate
		for rows.Next() {
			var c candidate
			if err := rows.Scan(&c.id, &c.number, &c.at); err != nil {
				rows.Close()
				return deleted, err
			}
			candidates = append(candidates, c)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return deleted, err
		}
		for _, c := range candidates {
			e, err := readEpoch(ctx, tx, c.id, c.number)
			if err != nil {
				return deleted, err
			}
			// Cleanup is also an authoritative clock observation. Persist it
			// before discarding an expired period so a later clock rollback
			// cannot reopen capacity at a time whose usage was just collected.
			if err := advance(ctx, tx, &e, now); err != nil {
				return deleted, err
			}
			if kind == "bucket" {
				// Early removal of a retired epoch's last facts must also remove
				// them from its derived cache while the parent still exists.
				if c.at > e.left.Int64 && c.at <= e.at.Int64 {
					u, r, err := sumBuckets(ctx, tx, e, c.at-1, c.at)
					if err != nil {
						return deleted, err
					}
					if e.used, err = subtract(e.used, u); err != nil {
						return deleted, err
					}
					if e.reserved, err = subtract(e.reserved, r); err != nil {
						return deleted, err
					}
					if err := writeWindow(ctx, tx, e); err != nil {
						return deleted, err
					}
				}
			} else if e.period.Valid && e.period.Int64 == c.at {
				if err := oneRow(tx.ExecContext(ctx, `UPDATE donation_quota_epochs SET current_period_start=NULL WHERE rule_id=? AND epoch=?`, c.id, c.number)); err != nil {
					return deleted, err
				}
			}
			if err := oneRow(tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE rule_id=? AND epoch=? AND `+column+`=?`, c.id, c.number, c.at)); err != nil {
				return deleted, err
			}
			if err := changeCapacity(ctx, tx, -1, 0); err != nil {
				return deleted, err
			}
			deleted++
		}
	}
	if deleted < budget {
		result, err := tx.ExecContext(ctx, `DELETE FROM donation_quota_epochs WHERE (rule_id,epoch) IN (
SELECT e.rule_id,e.epoch FROM donation_quota_epochs e JOIN donation_quota_rules q ON q.id=e.rule_id
WHERE e.retired_at IS NOT NULL AND (q.current_epoch IS NULL OR q.current_epoch<>e.epoch)
AND NOT EXISTS(SELECT 1 FROM donation_quota_receipts r WHERE r.rule_id=e.rule_id AND r.epoch=e.epoch)
AND NOT EXISTS(SELECT 1 FROM donation_quota_periods p WHERE p.rule_id=e.rule_id AND p.epoch=e.epoch)
AND NOT EXISTS(SELECT 1 FROM donation_quota_buckets b WHERE b.rule_id=e.rule_id AND b.epoch=e.epoch)
ORDER BY e.rule_id,e.epoch LIMIT ?)`, budget-deleted)
		if err != nil {
			return deleted, err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return deleted, err
		}
		deleted += int(n)
	}
	if deleted < budget {
		result, err := tx.ExecContext(ctx, `DELETE FROM donation_quota_rules WHERE id IN (SELECT q.id FROM donation_quota_rules q WHERE q.current_epoch IS NULL AND NOT EXISTS(SELECT 1 FROM donation_quota_epochs e WHERE e.rule_id=q.id) ORDER BY q.id LIMIT ?)`, budget-deleted)
		if err != nil {
			return deleted, err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return deleted, err
		}
		deleted += int(n)
	}
	return deleted, nil
}
