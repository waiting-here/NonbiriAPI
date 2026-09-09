package donationquota

import (
	"context"
	"database/sql"
	"fmt"
	"math/big"

	"github.com/waiting-here/NonbiriAPI/internal/calendar"
	"github.com/waiting-here/NonbiriAPI/internal/db"
)

// Validate checks persistent counters before startup recovery or admission.
// It is read-only: inconsistent state is never repaired into a zero balance.
func ValidateState(ctx context.Context, tx *sql.Tx) error {
	var used, held, actualRows, actualHeld int64
	err := tx.QueryRowContext(ctx, `SELECT rows_used,rows_held,(SELECT COUNT(*) FROM donation_quota_periods)+(SELECT COUNT(*) FROM donation_quota_buckets),(SELECT COUNT(*) FROM donation_quota_receipts WHERE capacity_state='held') FROM donation_quota_capacity WHERE id=1`).Scan(&used, &held, &actualRows, &actualHeld)
	if err != nil {
		return err
	}
	if used != actualRows || held != actualHeld || used < 0 || held < 0 || used+held > MaxRows {
		return fmt.Errorf("%w: capacity accounting", ErrInvariant)
	}
	var inconsistent bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM donation_quota_epochs e JOIN donation_quota_rules r ON r.id=e.rule_id WHERE (e.retired_at IS NULL) <> (r.current_epoch IS NOT NULL AND r.current_epoch=e.epoch))`).Scan(&inconsistent)
	if err != nil {
		return err
	}
	if inconsistent {
		return fmt.Errorf("%w: current epoch", ErrInvariant)
	}
	// Read bounded batches so validation memory does not grow with archived
	// epochs. Aggregate rows themselves are streamed using their primary keys.
	lastID := ""
	lastNumber := int64(0)
	for {
		rows, err := tx.QueryContext(ctx, `SELECT `+epochColumns+` FROM donation_quota_epochs e JOIN donation_quota_rules r ON r.id=e.rule_id WHERE (e.rule_id,e.epoch)>(?,?) ORDER BY e.rule_id,e.epoch LIMIT 128`, lastID, lastNumber)
		if err != nil {
			return err
		}
		var batch []epoch
		for rows.Next() {
			e, err := scanEpoch(rows)
			if err != nil {
				rows.Close()
				return err
			}
			batch = append(batch, e)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, e := range batch {
			if err := validateEpoch(ctx, tx, e); err != nil {
				return err
			}
			lastID, lastNumber = e.id, e.number
		}
		if len(batch) < 128 {
			return nil
		}
	}
}

type expectedAggregate struct{ used, reserved db.U128 }

func validateEpoch(ctx context.Context, tx *sql.Tx, e epoch) error {
	if !e.observed.Valid || e.observed.Int64 < e.effective {
		return fmt.Errorf("%w: observed time", ErrInvariant)
	}
	if e.rule.Mode == "sliding" {
		left, err := calendar.Subtract(e.at.Int64, e.rule.Interval, e.rule.TimeZone)
		if err != nil || !e.at.Valid || e.at.Int64 != e.observed.Int64 || left != e.left.Int64 {
			return fmt.Errorf("%w: window boundary", ErrInvariant)
		}
		u, r, err := sumBuckets(ctx, tx, e, left, e.at.Int64)
		if err != nil {
			return err
		}
		if u != e.used || r != e.reserved {
			return fmt.Errorf("%w: window cache", ErrInvariant)
		}
	} else if _, err := periodAt(ctx, tx, e, e.observed.Int64); err != nil {
		return err
	}

	rows, err := tx.QueryContext(ctx, `SELECT r.state,r.reserved_mag,r.remaining_reserved_mag,r.actual_mag,r.success_at,r.period_start,r.capacity_state,c.state,c.purpose,c.donation_key_id,c.dispatched_at,m.started_at,c.reserved_calls,c.reserved_tokens,c.reserved_price_milli
FROM donation_quota_receipts r JOIN dispatch_claims c ON c.id=r.claim_id LEFT JOIN dispatch_response_starts m ON m.claim_id=c.id WHERE r.rule_id=? AND r.epoch=?`, e.id, e.number)
	if err != nil {
		return err
	}
	var pending db.U128
	expected := make(map[int64]expectedAggregate)
	for rows.Next() {
		var state, capacity, claimState, purpose string
		var reserved, remaining, actual []byte
		var success, period, key, dispatched, marker sql.NullInt64
		var calls, tokens, credits int64
		if err := rows.Scan(&state, &reserved, &remaining, &actual, &success, &period, &capacity, &claimState, &purpose, &key, &dispatched, &marker, &calls, &tokens, &credits); err != nil {
			rows.Close()
			return err
		}
		bad := func() error { rows.Close(); return fmt.Errorf("%w: receipt and dispatch", ErrInvariant) }
		if purpose != "charity" || !key.Valid || key.Int64 != e.keyID || success.Valid != marker.Valid || (success.Valid && (success.Int64 != marker.Int64 || !dispatched.Valid || success.Int64 < dispatched.Int64 || success.Int64 < e.effective || success.Int64 > e.observed.Int64)) {
			return bad()
		}
		var rem, act db.U128
		if remaining != nil {
			rem, err = magnitude(remaining)
			if err != nil {
				return bad()
			}
		}
		if actual != nil {
			act, err = magnitude(actual)
			if err != nil {
				return bad()
			}
		}
		if state != "settled" {
			reserve, err := magnitude(reserved)
			if err != nil {
				return bad()
			}
			value := credits
			if e.rule.Metric == "calls" {
				value = calls
			} else if e.rule.Metric == "tokens" {
				value = tokens
			}
			want, err := db.U128FromBig(big.NewInt(value))
			if err != nil || reserve != want {
				return bad()
			}
			if state == "reserved" {
				if success.Valid || period.Valid || capacity != "held" || actual != nil || rem != reserve || (claimState != "claimed" && claimState != "dispatched") {
					return bad()
				}
				pending, err = add(pending, rem)
				if err != nil {
					rows.Close()
					return err
				}
				continue
			}
			if state != "started" || claimState != "dispatched" || !success.Valid || capacity != "attached" {
				return bad()
			}
			if e.rule.Metric == "calls" {
				one, _ := db.U128FromBig(big.NewInt(1))
				if act != one || rem != (db.U128{}) || actual == nil {
					return bad()
				}
			} else if actual != nil || rem != reserve {
				return bad()
			}
		} else if (claimState != "committed" && claimState != "released") || capacity != "released" || actual == nil || remaining != nil || reserved != nil {
			return bad()
		}
		if !success.Valid {
			if act != (db.U128{}) || period.Valid {
				return bad()
			}
			continue
		}
		if e.rule.Metric == "calls" {
			one, _ := db.U128FromBig(big.NewInt(1))
			if actual == nil || act != one {
				return bad()
			}
		}
		if (e.rule.Mode == "reset") != period.Valid {
			return bad()
		}
		index := success.Int64
		if period.Valid {
			index = period.Int64
		}
		a := expected[index]
		if a.used, err = add(a.used, act); err != nil {
			rows.Close()
			return err
		}
		if a.reserved, err = add(a.reserved, rem); err != nil {
			rows.Close()
			return err
		}
		expected[index] = a
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if pending != e.pending {
		return fmt.Errorf("%w: pending reservation", ErrInvariant)
	}
	return validateAggregates(ctx, tx, e, expected)
}

func validateAggregates(ctx context.Context, tx *sql.Tx, e epoch, expected map[int64]expectedAggregate) error {
	query := `SELECT success_at,NULL,used_mag,reserved_mag FROM donation_quota_buckets WHERE rule_id=? AND epoch=? ORDER BY success_at`
	if e.rule.Mode == "reset" {
		query = `SELECT start_at,end_at,used_mag,reserved_mag FROM donation_quota_periods WHERE rule_id=? AND epoch=? ORDER BY start_at`
	}
	rows, err := tx.QueryContext(ctx, query, e.id, e.number)
	if err != nil {
		return err
	}
	defer rows.Close()
	var active sql.NullInt64
	var previousEnd sql.NullInt64
	for rows.Next() {
		var at int64
		var end sql.NullInt64
		var ub, rb []byte
		if err := rows.Scan(&at, &end, &ub, &rb); err != nil {
			return err
		}
		u, err := magnitude(ub)
		if err != nil {
			return err
		}
		r, err := magnitude(rb)
		if err != nil {
			return err
		}
		a := expected[at]
		if at > e.observed.Int64 || u.Big().Cmp(a.used.Big()) < 0 || r != a.reserved {
			return fmt.Errorf("%w: aggregate accounting", ErrInvariant)
		}
		if e.rule.Mode == "reset" {
			if !end.Valid || end.Int64 <= at || (previousEnd.Valid && previousEnd.Int64 > at) {
				return fmt.Errorf("%w: period boundaries", ErrInvariant)
			}
			if *e.rule.Alignment == "first_success" {
				wantEnd, err := calendar.Add(at, e.rule.Interval, e.rule.TimeZone)
				if err != nil || at < e.effective || end.Int64 != wantEnd {
					return fmt.Errorf("%w: anchored period", ErrInvariant)
				}
			} else {
				week := 0
				if e.rule.WeekStartsOn != nil {
					week = *e.rule.WeekStartsOn
				}
				want, err := calendar.NaturalPeriod(max(at, e.effective), e.rule.Interval, e.rule.TimeZone, week)
				if err != nil || at != want.Start || end.Int64 != want.End {
					return fmt.Errorf("%w: calendar period", ErrInvariant)
				}
			}
			previousEnd = end
			if at <= e.observed.Int64 && e.observed.Int64 < end.Int64 {
				active = sql.NullInt64{Int64: at, Valid: true}
			}
			var mismatch bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM donation_quota_receipts WHERE rule_id=? AND epoch=? AND period_start=? AND (success_at<? OR success_at>=?))`, e.id, e.number, at, at, end.Int64).Scan(&mismatch); err != nil {
				return err
			}
			if mismatch {
				return fmt.Errorf("%w: period membership", ErrInvariant)
			}
		} else if at < e.effective {
			return ErrInvariant
		}
		delete(expected, at)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(expected) != 0 {
		return fmt.Errorf("%w: missing receipt aggregate", ErrInvariant)
	}
	if e.rule.Mode == "reset" && active != e.period {
		return fmt.Errorf("%w: current period pointer", ErrInvariant)
	}
	return nil
}
