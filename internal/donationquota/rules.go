package donationquota

import (
	"context"
	"database/sql"
	"math"

	"github.com/waiting-here/NonbiriAPI/internal/calendar"
	"github.com/waiting-here/NonbiriAPI/internal/db"
)

const epochColumns = `r.id,e.epoch,r.donation_key_id,COALESCE(r.display_order,0),e.mode,e.interval,e.alignment,e.time_zone,e.week_starts_on,e.metric,e.limit_mag,e.effective_at,e.retired_at,e.last_observed_at,e.current_period_start,e.window_left,e.window_at,e.window_used,e.window_reserved,e.pending_reserved`

type scanner interface{ Scan(...any) error }

func scanEpoch(row scanner) (epoch, error) {
	var e epoch
	var limit, used, reserved, pending []byte
	var alignment sql.NullString
	var week sql.NullInt64
	err := row.Scan(&e.id, &e.number, &e.keyID, &e.order, &e.rule.Mode, &e.rule.Interval, &alignment, &e.rule.TimeZone, &week, &e.rule.Metric, &limit, &e.effective, &e.retired, &e.observed, &e.period, &e.left, &e.at, &used, &reserved, &pending)
	if err != nil {
		return e, err
	}
	e.rule.ID = &e.id
	if alignment.Valid {
		e.rule.Alignment = &alignment.String
	}
	if week.Valid {
		n := int(week.Int64)
		e.rule.WeekStartsOn = &n
	}
	if e.limit, err = magnitude(limit); err != nil {
		return e, err
	}
	if e.pending, err = magnitude(pending); err != nil {
		return e, err
	}
	if e.left.Valid != e.at.Valid || e.at.Valid != (used != nil) || e.at.Valid != (reserved != nil) {
		return e, ErrInvariant
	}
	if e.at.Valid {
		if e.used, err = magnitude(used); err != nil {
			return e, err
		}
		if e.reserved, err = magnitude(reserved); err != nil {
			return e, err
		}
	}
	e.rule.Limit = formatMagnitude(e.rule.Metric, e.limit)
	if Validate(e.rule) != nil {
		return e, ErrInvariant
	}
	return e, nil
}

func currentEpochs(ctx context.Context, q Reader, keyID int64) ([]epoch, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+epochColumns+` FROM donation_quota_rules r JOIN donation_quota_epochs e ON e.rule_id=r.id AND e.epoch=r.current_epoch WHERE r.donation_key_id=? ORDER BY r.display_order`, keyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]epoch, 0)
	for rows.Next() {
		e, err := scanEpoch(rows)
		if err != nil {
			return nil, err
		}
		if e.retired.Valid || len(out) >= MaxRules {
			return nil, ErrInvariant
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func readEpoch(ctx context.Context, q Reader, id string, number int64) (epoch, error) {
	return scanEpoch(q.QueryRowContext(ctx, `SELECT `+epochColumns+` FROM donation_quota_rules r JOIN donation_quota_epochs e ON e.rule_id=r.id WHERE r.id=? AND e.epoch=?`, id, number))
}

// Replace changes the complete current set. Its caller must authorize the key,
// compare the donation revision, and commit the idempotent command in this tx.
func Replace(ctx context.Context, tx *sql.Tx, keyID, now int64, input []RuleInput) error {
	if ctx == nil || tx == nil || keyID <= 0 || !validNow(now) || len(input) > MaxRules {
		return ErrInvalid
	}
	seen := make(map[string]bool)
	split := false
	for _, rule := range input {
		if err := Validate(rule); err != nil {
			return err
		}
		split = split || rule.Metric == "input_tokens" || rule.Metric == "output_tokens"
		if rule.ID != nil {
			if seen[*rule.ID] {
				return ErrInvalid
			}
			seen[*rule.ID] = true
		}
	}
	if split {
		budget, err := ReadTokenBudget(ctx, tx, keyID)
		if err != nil {
			return err
		}
		if budget.Reservation.Input == nil {
			return ErrInvalid
		}
	}
	old, err := currentEpochs(ctx, tx, keyID)
	if err != nil {
		return err
	}
	byID := make(map[string]epoch, len(old))
	for _, e := range old {
		byID[e.id] = e
	}
	for id := range seen {
		if _, ok := byID[id]; !ok {
			return ErrInvalid
		}
	}
	// Clear both nullable fields before reordering, avoiding transient collisions
	// in the partial unique index even for a complete reversal of sixteen rules.
	if _, err := tx.ExecContext(ctx, `UPDATE donation_quota_rules SET current_epoch=NULL,display_order=NULL WHERE donation_key_id=?`, keyID); err != nil {
		return err
	}
	for _, e := range old {
		if !seen[e.id] {
			at := max(now, e.effective, e.observed.Int64)
			if err := oneRow(tx.ExecContext(ctx, `UPDATE donation_quota_epochs SET retired_at=? WHERE rule_id=? AND epoch=? AND retired_at IS NULL`, at, e.id, e.number)); err != nil {
				return err
			}
		}
	}
	for order, rule := range input {
		id := ""
		number := int64(1)
		effective := now
		fresh := true
		if rule.ID == nil {
			id, err = db.GenerateOpaqueID("qlr_")
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO donation_quota_rules(id,donation_key_id) VALUES(?,?)`, id, keyID); err != nil {
				return err
			}
		} else {
			id = *rule.ID
			previous := byID[id]
			number = previous.number
			fresh = !sameStructure(previous.rule, rule)
			if fresh {
				if number == math.MaxInt64 {
					return ErrConflict
				}
				effective = max(now, previous.effective, previous.observed.Int64)
				if err := oneRow(tx.ExecContext(ctx, `UPDATE donation_quota_epochs SET retired_at=? WHERE rule_id=? AND epoch=? AND retired_at IS NULL`, effective, id, number)); err != nil {
					return err
				}
				number++
			}
		}
		limit, err := parseMagnitude(rule.Metric, rule.Limit)
		if err != nil {
			return err
		}
		if fresh {
			var left, at, used, reserved any
			zero := db.EncodeU128(db.U128{})
			if rule.Mode == "sliding" {
				l, err := calendar.Subtract(effective, rule.Interval, rule.TimeZone)
				if err != nil || l >= effective {
					return ErrInvalid
				}
				left, at, used, reserved = l, effective, zero, zero
			} else if *rule.Alignment == "calendar" {
				week := 0
				if rule.WeekStartsOn != nil {
					week = *rule.WeekStartsOn
				}
				if _, err := calendar.NaturalPeriod(effective, rule.Interval, rule.TimeZone, week); err != nil {
					return ErrInvalid
				}
			} else if _, err := calendar.Add(effective, rule.Interval, rule.TimeZone); err != nil {
				return ErrInvalid
			}
			_, err := tx.ExecContext(ctx, `INSERT INTO donation_quota_epochs(rule_id,epoch,mode,interval,alignment,time_zone,week_starts_on,metric,limit_mag,effective_at,last_observed_at,window_left,window_at,window_used,window_reserved,pending_reserved) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, id, number, rule.Mode, rule.Interval, rule.Alignment, rule.TimeZone, rule.WeekStartsOn, rule.Metric, db.EncodeU128(limit), effective, effective, left, at, used, reserved, zero)
			if err != nil {
				return err
			}
		} else if err := oneRow(tx.ExecContext(ctx, `UPDATE donation_quota_epochs SET limit_mag=? WHERE rule_id=? AND epoch=? AND retired_at IS NULL`, db.EncodeU128(limit), id, number)); err != nil {
			return err
		}
		if err := oneRow(tx.ExecContext(ctx, `UPDATE donation_quota_rules SET current_epoch=?,display_order=? WHERE id=? AND donation_key_id=?`, number, order, id, keyID)); err != nil {
			return err
		}
	}
	return nil
}
