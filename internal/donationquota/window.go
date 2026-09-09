package donationquota

import (
	"context"
	"database/sql"
	"errors"
	"math/big"

	"github.com/waiting-here/NonbiriAPI/internal/calendar"
	"github.com/waiting-here/NonbiriAPI/internal/db"
)

type aggregate struct {
	used, reserved db.U128
	start, end     int64
	exists         bool
}

func readPeriod(ctx context.Context, q Reader, e epoch, start int64) (aggregate, error) {
	a := aggregate{start: start}
	var u, r []byte
	err := q.QueryRowContext(ctx, `SELECT end_at,used_mag,reserved_mag FROM donation_quota_periods WHERE rule_id=? AND epoch=? AND start_at=?`, e.id, e.number, start).Scan(&a.end, &u, &r)
	if errors.Is(err, sql.ErrNoRows) {
		return a, nil
	}
	if err != nil {
		return a, err
	}
	a.exists = true
	if a.used, err = magnitude(u); err != nil {
		return a, err
	}
	a.reserved, err = magnitude(r)
	return a, err
}

func periodAt(ctx context.Context, q Reader, e epoch, now int64) (aggregate, error) {
	if e.period.Valid {
		a, err := readPeriod(ctx, q, e, e.period.Int64)
		if err != nil {
			return a, err
		}
		if !a.exists {
			return a, ErrInvariant
		}
		if a.start > now {
			return a, ErrInvariant
		}
		if now < a.end {
			return a, nil
		}
	}
	if *e.rule.Alignment == "first_success" {
		return aggregate{}, nil
	}
	week := 0
	if e.rule.WeekStartsOn != nil {
		week = *e.rule.WeekStartsOn
	}
	p, err := calendar.NaturalPeriod(now, e.rule.Interval, e.rule.TimeZone, week)
	if err != nil {
		return aggregate{}, ErrInvariant
	}
	a, err := readPeriod(ctx, q, e, p.Start)
	if err != nil {
		return a, err
	}
	if a.exists && a.end != p.End {
		return a, ErrInvariant
	}
	a.end = p.End
	return a, nil
}

func sumBuckets(ctx context.Context, q Reader, e epoch, left, right int64) (db.U128, db.U128, error) {
	var used, reserved db.U128
	if left >= right {
		return used, reserved, nil
	}
	rows, err := q.QueryContext(ctx, `SELECT used_mag,reserved_mag FROM donation_quota_buckets WHERE rule_id=? AND epoch=? AND success_at>? AND success_at<=?`, e.id, e.number, left, right)
	if err != nil {
		return used, reserved, err
	}
	defer rows.Close()
	for rows.Next() {
		var ub, rb []byte
		if err := rows.Scan(&ub, &rb); err != nil {
			return used, reserved, err
		}
		u, err := magnitude(ub)
		if err != nil {
			return used, reserved, err
		}
		r, err := magnitude(rb)
		if err != nil {
			return used, reserved, err
		}
		if used, err = add(used, u); err != nil {
			return used, reserved, err
		}
		if reserved, err = add(reserved, r); err != nil {
			return used, reserved, err
		}
	}
	return used, reserved, rows.Err()
}

// moveWindow reads only the symmetric difference through the bucket time
// index. A calendar left boundary may move backwards, so both entry and exit
// ranges are handled; retained facts are never permanently dequeued.
func moveWindow(ctx context.Context, q Reader, e *epoch, now int64) error {
	left, err := calendar.Subtract(now, e.rule.Interval, e.rule.TimeZone)
	if err != nil || left >= now {
		return ErrInvariant
	}
	if e.at.Valid && (now < e.at.Int64 || e.left.Int64 >= e.at.Int64) {
		return ErrInvariant
	}
	if !e.at.Valid || left >= e.at.Int64 {
		// Disjoint windows share no facts. Read the new range directly instead
		// of scanning and subtracting every expired bucket from the old cache.
		e.used, e.reserved, err = sumBuckets(ctx, q, *e, left, now)
	} else {
		adjust := func(l, r int64, incoming bool) error {
			u, res, err := sumBuckets(ctx, q, *e, l, r)
			if err != nil {
				return err
			}
			operation := subtract
			if incoming {
				operation = add
			}
			if e.used, err = operation(e.used, u); err != nil {
				return err
			}
			e.reserved, err = operation(e.reserved, res)
			return err
		}
		if left > e.left.Int64 {
			if err := adjust(e.left.Int64, min(left, e.at.Int64), false); err != nil {
				return err
			}
		}
		if left < e.left.Int64 {
			if err := adjust(left, e.left.Int64, true); err != nil {
				return err
			}
		}
		if err := adjust(max(left, e.at.Int64), now, true); err != nil {
			return err
		}
	}
	if err != nil {
		return err
	}
	e.left = sql.NullInt64{Int64: left, Valid: true}
	e.at = sql.NullInt64{Int64: now, Valid: true}
	return nil
}

func observedNow(e epoch, now int64) int64 { return max(now, e.effective, e.observed.Int64) }

func writeWindow(ctx context.Context, tx *sql.Tx, e epoch) error {
	return oneRow(tx.ExecContext(ctx, `UPDATE donation_quota_epochs SET window_used=?,window_reserved=? WHERE rule_id=? AND epoch=?`, db.EncodeU128(e.used), db.EncodeU128(e.reserved), e.id, e.number))
}

func advance(ctx context.Context, tx *sql.Tx, e *epoch, now int64) error {
	now = observedNow(*e, now)
	if !validNow(now) {
		return ErrInvariant
	}
	if e.rule.Mode == "sliding" {
		if err := moveWindow(ctx, tx, e, now); err != nil {
			return err
		}
		if err := oneRow(tx.ExecContext(ctx, `UPDATE donation_quota_epochs SET last_observed_at=?,window_left=?,window_at=?,window_used=?,window_reserved=? WHERE rule_id=? AND epoch=?`, now, e.left.Int64, now, db.EncodeU128(e.used), db.EncodeU128(e.reserved), e.id, e.number)); err != nil {
			return err
		}
	} else {
		a, err := periodAt(ctx, tx, *e, now)
		if err != nil {
			return err
		}
		e.period = sql.NullInt64{Int64: a.start, Valid: a.exists}
		if err := oneRow(tx.ExecContext(ctx, `UPDATE donation_quota_epochs SET last_observed_at=?,current_period_start=? WHERE rule_id=? AND epoch=?`, now, e.period, e.id, e.number)); err != nil {
			return err
		}
	}
	e.observed = sql.NullInt64{Int64: now, Valid: true}
	return nil
}

func view(ctx context.Context, q Reader, e epoch, now int64) (RuleView, error) {
	out := RuleView{RuleInput: e.rule, State: "available"}
	now = observedNow(e, now)
	var used, reserved db.U128
	if e.rule.Mode == "sliding" {
		if err := moveWindow(ctx, q, &e, now); err != nil {
			return out, err
		}
		used, reserved = e.used, e.reserved
	} else {
		a, err := periodAt(ctx, q, e, now)
		if err != nil {
			return out, err
		}
		used, reserved = a.used, a.reserved
		if a.exists || *e.rule.Alignment == "calendar" {
			start, end := max(a.start, e.effective), a.end
			out.PeriodStart, out.PeriodEnd, out.NextTransitionAt = &start, &end, &end
		} else {
			out.State = "waiting_first_success"
		}
	}
	var err error
	reserved, err = add(reserved, e.pending)
	if err != nil {
		return out, err
	}
	remaining := new(big.Int).Sub(e.limit.Big(), new(big.Int).Add(used.Big(), reserved.Big()))
	if remaining.Sign() <= 0 {
		remaining.SetInt64(0)
		out.State = "limited"
	}
	rem, err := db.U128FromBig(remaining)
	if err != nil {
		return out, ErrInvariant
	}
	out.Used, out.Reserved, out.Remaining = formatMagnitude(e.rule.Metric, used), formatMagnitude(e.rule.Metric, reserved), formatMagnitude(e.rule.Metric, rem)
	return out, nil
}

// Views is read-only and uses the caller's snapshot and one decision time.
func Views(ctx context.Context, q Reader, keyID, now int64) ([]RuleView, error) {
	if ctx == nil || q == nil || keyID <= 0 || !validNow(now) {
		return nil, ErrInvalid
	}
	epochs, err := currentEpochs(ctx, q, keyID)
	if err != nil {
		return nil, err
	}
	out := make([]RuleView, 0, len(epochs))
	for _, e := range epochs {
		v, err := view(ctx, q, e, now)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}
