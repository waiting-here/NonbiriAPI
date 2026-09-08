package donationquota

import (
	"context"
	"database/sql"
	"errors"
	"math/big"

	"github.com/waiting-here/NonbiriAPI/internal/calendar"
	"github.com/waiting-here/NonbiriAPI/internal/db"
)

type receipt struct {
	claimID, ruleID, state, capacity string
	number                           int64
	reserved, remaining, actual      db.U128
	success, period                  sql.NullInt64
}

func readReceipts(ctx context.Context, q Reader, claimID string) ([]receipt, error) {
	rows, err := q.QueryContext(ctx, `SELECT rule_id,epoch,state,reserved_mag,remaining_reserved_mag,actual_mag,success_at,period_start,capacity_state FROM donation_quota_receipts WHERE claim_id=? ORDER BY rule_id,epoch`, claimID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]receipt, 0)
	for rows.Next() {
		r := receipt{claimID: claimID}
		var reserve, remaining, actual []byte
		if err := rows.Scan(&r.ruleID, &r.number, &r.state, &reserve, &remaining, &actual, &r.success, &r.period, &r.capacity); err != nil {
			return nil, err
		}
		if reserve != nil {
			if r.reserved, err = magnitude(reserve); err != nil {
				return nil, err
			}
		}
		if remaining != nil {
			if r.remaining, err = magnitude(remaining); err != nil {
				return nil, err
			}
		}
		if actual != nil {
			if r.actual, err = magnitude(actual); err != nil {
				return nil, err
			}
		}
		if len(out) >= MaxRules {
			return nil, ErrInvariant
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

type Amounts struct{ Calls, Tokens, Credits db.U128 }

func amountFor(metric string, a Amounts) db.U128 {
	switch metric {
	case "calls":
		return a.Calls
	case "tokens":
		return a.Tokens
	default:
		return a.Credits
	}
}

func claimReservation(ctx context.Context, tx *sql.Tx, claimID string) (int64, Amounts, error) {
	var keyID, price, calls, tokens int64
	err := tx.QueryRowContext(ctx, `SELECT donation_key_id,reserved_price_milli,reserved_calls,reserved_tokens FROM dispatch_claims WHERE id=? AND state='claimed' AND purpose='charity'`, claimID).Scan(&keyID, &price, &calls, &tokens)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, Amounts{}, ErrConflict
	}
	if err != nil {
		return 0, Amounts{}, err
	}
	if keyID <= 0 || price < 0 || calls != 1 || tokens < 0 {
		return 0, Amounts{}, ErrInvariant
	}
	p, _ := db.U128FromBig(big.NewInt(price))
	c, _ := db.U128FromBig(big.NewInt(calls))
	t, _ := db.U128FromBig(big.NewInt(tokens))
	return keyID, Amounts{Calls: c, Tokens: t, Credits: p}, nil
}

// Reserve runs after the dispatch_claims row is inserted in its original tx.
// All rules and all future aggregate positions are admitted atomically.
func Reserve(ctx context.Context, tx *sql.Tx, claimID string, now int64) error {
	if ctx == nil || tx == nil || !db.ValidateOpaqueID(claimID, "clm_") || !validNow(now) {
		return ErrInvalid
	}
	keyID, amounts, err := claimReservation(ctx, tx, claimID)
	if err != nil {
		return err
	}
	existing, err := readReceipts(ctx, tx, claimID)
	if err != nil {
		return err
	}
	if len(existing) != 0 {
		return ErrConflict
	}
	epochs, err := currentEpochs(ctx, tx, keyID)
	if err != nil {
		return err
	}
	for i := range epochs {
		e := &epochs[i]
		if err := advance(ctx, tx, e, now); err != nil {
			return err
		}
		v, err := view(ctx, tx, *e, now)
		if err != nil {
			return err
		}
		remaining, err := parseMagnitude(e.rule.Metric, v.Remaining)
		if err != nil {
			return ErrInvariant
		}
		if remaining == (db.U128{}) || amountFor(e.rule.Metric, amounts).Big().Cmp(remaining.Big()) > 0 {
			return ErrLimited
		}
	}
	if err := holdCapacity(ctx, tx, len(epochs), now); err != nil {
		return err
	}
	for _, e := range epochs {
		amount := amountFor(e.rule.Metric, amounts)
		pending, err := add(e.pending, amount)
		if err != nil {
			return err
		}
		if err := oneRow(tx.ExecContext(ctx, `UPDATE donation_quota_epochs SET pending_reserved=? WHERE rule_id=? AND epoch=?`, db.EncodeU128(pending), e.id, e.number)); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO donation_quota_receipts(claim_id,rule_id,epoch,state,reserved_mag,remaining_reserved_mag,capacity_state) VALUES(?,?,?,'reserved',?,?,'held')`, claimID, e.id, e.number, db.EncodeU128(amount), db.EncodeU128(amount))
		if err != nil {
			return err
		}
	}
	return nil
}

// Reconcile removes only this unsent attempt's occupancy and then checks the
// complete current set. Rolling back on failure retains the original receipt
// for the existing undispatched-release path; no stale admission survives.
func Reconcile(ctx context.Context, tx *sql.Tx, claimID string, now int64) error {
	if _, _, err := claimReservation(ctx, tx, claimID); err != nil {
		return err
	}
	receipts, err := readReceipts(ctx, tx, claimID)
	if err != nil {
		return err
	}
	for _, r := range receipts {
		if r.state != "reserved" || r.capacity != "held" || r.success.Valid {
			return ErrInvariant
		}
		e, err := readEpoch(ctx, tx, r.ruleID, r.number)
		if err != nil {
			return err
		}
		if err := advance(ctx, tx, &e, now); err != nil {
			return err
		}
		pending, err := subtract(e.pending, r.remaining)
		if err != nil {
			return err
		}
		if err := oneRow(tx.ExecContext(ctx, `UPDATE donation_quota_epochs SET pending_reserved=? WHERE rule_id=? AND epoch=?`, db.EncodeU128(pending), e.id, e.number)); err != nil {
			return err
		}
	}
	if len(receipts) > 0 {
		if err := changeCapacity(ctx, tx, 0, -len(receipts)); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM donation_quota_receipts WHERE claim_id=? AND state='reserved'`, claimID); err != nil {
			return err
		}
	}
	return Reserve(ctx, tx, claimID, now)
}

// Start assigns a single causal success time. The caller persists its response
// marker using the returned time in this same transaction, before delivery.
func Start(ctx context.Context, tx *sql.Tx, claimID string, now int64) (int64, error) {
	if !validNow(now) {
		return 0, ErrInvalid
	}
	receipts, err := readReceipts(ctx, tx, claimID)
	if err != nil {
		return 0, err
	}
	epochs := make([]epoch, len(receipts))
	for i, r := range receipts {
		if r.state != "reserved" || r.capacity != "held" || r.success.Valid {
			return 0, ErrInvariant
		}
		e, err := readEpoch(ctx, tx, r.ruleID, r.number)
		if err != nil {
			return 0, err
		}
		epochs[i] = e
		now = observedNow(e, now)
	}
	if !validNow(now) {
		return 0, ErrInvariant
	}
	for i, r := range receipts {
		e := epochs[i]
		if err := advance(ctx, tx, &e, now); err != nil {
			return 0, err
		}
		remaining, actual := r.remaining, db.U128{}
		var actualBlob any
		if e.rule.Metric == "calls" {
			actual, _ = db.U128FromBig(big.NewInt(1))
			remaining = db.U128{}
			actualBlob = db.EncodeU128(actual)
		}
		period, err := attach(ctx, tx, &e, now, actual, remaining)
		if err != nil {
			return 0, err
		}
		pending, err := subtract(e.pending, r.remaining)
		if err != nil {
			return 0, err
		}
		if err := oneRow(tx.ExecContext(ctx, `UPDATE donation_quota_epochs SET pending_reserved=? WHERE rule_id=? AND epoch=?`, db.EncodeU128(pending), e.id, e.number)); err != nil {
			return 0, err
		}
		if err := oneRow(tx.ExecContext(ctx, `UPDATE donation_quota_receipts SET state='started',remaining_reserved_mag=?,actual_mag=?,success_at=?,period_start=?,capacity_state='attached' WHERE claim_id=? AND rule_id=? AND epoch=? AND state='reserved'`, db.EncodeU128(remaining), actualBlob, now, period, claimID, e.id, e.number)); err != nil {
			return 0, err
		}
	}
	return now, nil
}

func attach(ctx context.Context, tx *sql.Tx, e *epoch, at int64, used, reserved db.U128) (sql.NullInt64, error) {
	period := sql.NullInt64{}
	created := false
	if e.rule.Mode == "reset" {
		a, err := periodAt(ctx, tx, *e, at)
		if err != nil {
			return period, err
		}
		if !a.exists {
			if *e.rule.Alignment == "first_success" {
				a.start = at
				a.end, err = calendar.Add(at, e.rule.Interval, e.rule.TimeZone)
				if err != nil || a.end <= a.start {
					return period, ErrInvariant
				}
			}
			_, err := tx.ExecContext(ctx, `INSERT INTO donation_quota_periods(rule_id,epoch,start_at,end_at,used_mag,reserved_mag) VALUES(?,?,?,?,?,?)`, e.id, e.number, a.start, a.end, db.EncodeU128(db.U128{}), db.EncodeU128(db.U128{}))
			if err != nil {
				return period, err
			}
			created = true
		}
		period = sql.NullInt64{Int64: a.start, Valid: true}
		if err := oneRow(tx.ExecContext(ctx, `UPDATE donation_quota_epochs SET current_period_start=? WHERE rule_id=? AND epoch=?`, a.start, e.id, e.number)); err != nil {
			return period, err
		}
		e.period = period
	} else {
		result, err := tx.ExecContext(ctx, `INSERT INTO donation_quota_buckets(rule_id,epoch,success_at,used_mag,reserved_mag) VALUES(?,?,?,?,?) ON CONFLICT(rule_id,epoch,success_at) DO NOTHING`, e.id, e.number, at, db.EncodeU128(db.U128{}), db.EncodeU128(db.U128{}))
		if err != nil {
			return period, err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return period, err
		}
		created = n == 1
	}
	usedDelta := 0
	if created {
		usedDelta = 1
	}
	if err := changeCapacity(ctx, tx, usedDelta, -1); err != nil {
		return period, err
	}
	return period, changeAggregate(ctx, tx, e, at, period, used, reserved, db.U128{})
}

func changeAggregate(ctx context.Context, tx *sql.Tx, e *epoch, at int64, period sql.NullInt64, usedAdd, reserveAdd, reserveRemove db.U128) error {
	table, column, key := "donation_quota_buckets", "success_at", at
	if e.rule.Mode == "reset" {
		if !period.Valid {
			return ErrInvariant
		}
		table, column, key = "donation_quota_periods", "start_at", period.Int64
	}
	var ub, rb []byte
	if err := tx.QueryRowContext(ctx, `SELECT used_mag,reserved_mag FROM `+table+` WHERE rule_id=? AND epoch=? AND `+column+`=?`, e.id, e.number, key).Scan(&ub, &rb); err != nil {
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
	if u, err = add(u, usedAdd); err != nil {
		return err
	}
	if r, err = subtract(r, reserveRemove); err != nil {
		return err
	}
	if r, err = add(r, reserveAdd); err != nil {
		return err
	}
	if err := oneRow(tx.ExecContext(ctx, `UPDATE `+table+` SET used_mag=?,reserved_mag=? WHERE rule_id=? AND epoch=? AND `+column+`=?`, db.EncodeU128(u), db.EncodeU128(r), e.id, e.number, key)); err != nil {
		return err
	}
	if e.rule.Mode == "sliding" && e.left.Valid && at > e.left.Int64 && at <= e.at.Int64 {
		if e.used, err = add(e.used, usedAdd); err != nil {
			return err
		}
		if e.reserved, err = subtract(e.reserved, reserveRemove); err != nil {
			return err
		}
		if e.reserved, err = add(e.reserved, reserveAdd); err != nil {
			return err
		}
		return oneRow(tx.ExecContext(ctx, `UPDATE donation_quota_epochs SET window_used=?,window_reserved=? WHERE rule_id=? AND epoch=?`, db.EncodeU128(e.used), db.EncodeU128(e.reserved), e.id, e.number))
	}
	return nil
}

// Settle records the same undiscounted actual amounts already calculated by
// charity. A successful receipt keeps its original period or success bucket.
func Settle(ctx context.Context, tx *sql.Tx, claimID string, now int64, amounts Amounts, started bool) error {
	if !validNow(now) {
		return ErrInvalid
	}
	receipts, err := readReceipts(ctx, tx, claimID)
	if err != nil {
		return err
	}
	for _, r := range receipts {
		e, err := readEpoch(ctx, tx, r.ruleID, r.number)
		if err != nil {
			return err
		}
		actual := amountFor(e.rule.Metric, amounts)
		if !started && actual != (db.U128{}) {
			return ErrInvariant
		}
		if r.state == "settled" {
			if r.actual != actual || r.success.Valid != started {
				return ErrConflict
			}
			continue
		}
		if r.success.Valid != started || (started && r.state != "started") || (!started && r.state != "reserved") {
			return ErrInvariant
		}
		if err := advance(ctx, tx, &e, now); err != nil {
			return err
		}
		if started {
			if r.capacity != "attached" {
				return ErrInvariant
			}
			usedAdd := actual
			if e.rule.Metric == "calls" {
				if actual != r.actual {
					return ErrConflict
				}
				usedAdd = db.U128{}
			}
			if err := changeAggregate(ctx, tx, &e, r.success.Int64, r.period, usedAdd, db.U128{}, r.remaining); err != nil {
				return err
			}
		} else {
			if r.capacity != "held" {
				return ErrInvariant
			}
			pending, err := subtract(e.pending, r.remaining)
			if err != nil {
				return err
			}
			if err := oneRow(tx.ExecContext(ctx, `UPDATE donation_quota_epochs SET pending_reserved=? WHERE rule_id=? AND epoch=?`, db.EncodeU128(pending), e.id, e.number)); err != nil {
				return err
			}
			if err := changeCapacity(ctx, tx, 0, -1); err != nil {
				return err
			}
		}
		if err := oneRow(tx.ExecContext(ctx, `UPDATE donation_quota_receipts SET state='settled',reserved_mag=NULL,remaining_reserved_mag=NULL,actual_mag=?,capacity_state='released' WHERE claim_id=? AND rule_id=? AND epoch=? AND state=?`, db.EncodeU128(actual), claimID, e.id, e.number, r.state)); err != nil {
			return err
		}
	}
	return nil
}
