package antiabuse

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/waiting-here/NonbiriAPI/internal/charityrouting"
	"github.com/waiting-here/NonbiriAPI/internal/db"
)

const RetentionSeconds = int64(90 * 24 * 60 * 60)

type evidenceStatistics struct {
	Rule          string `json:"rule"`
	WindowStart   int64  `json:"window_start"`
	WindowEnd     int64  `json:"window_end"`
	Threshold     int    `json:"threshold"`
	Count         int    `json:"count"`
	Actual        *int   `json:"actual_chars,omitempty"`
	Minimum       *int   `json:"minimum_chars,omitempty"`
	DirectSeconds int64  `json:"direct_seconds,omitempty"`
}

func rulesJSON(c Config) ([]byte, error) {
	return json.Marshal(map[string]any{"version": 1, "rpm_ban_threshold": c.RPMBanThreshold, "rpm_ban_window_seconds": c.RPMBanWindowSeconds, "rpm_ban_duration_seconds": c.RPMBanDurationSeconds,
		"charity_min_chars": c.CharityMinChars, "charity_violation_deduct_milli": c.CharityViolationDeductMilli, "charity_violation_ban_seconds": c.CharityViolationBanSeconds,
		"charity_violation_window_seconds": c.CharityViolationWindowSeconds, "charity_violation_ban_threshold": c.CharityViolationBanThreshold, "charity_violation_window_ban_seconds": c.CharityViolationWindowBanSeconds,
		"charity_suspend_window_seconds": c.CharitySuspendWindowSeconds, "charity_suspend_threshold": c.CharitySuspendThreshold, "charity_suspend_duration_seconds": c.CharitySuspendDurationSeconds})
}
func matchingFacts(w violationWindow, now, duration int64) []windowEvent {
	result := make([]windowEvent, 0, len(w.facts))
	for _, e := range w.facts {
		if e.At > now-duration {
			result = append(result, e)
		}
	}
	return result
}
func appendAction(ctx context.Context, tx *sql.Tx, id, action, reason, request, operation string, actor *int64, at int64, previous, ends *int64, rules, stats []byte, facts []windowEvent, kind string) error {
	evidence, err := json.Marshal(facts)
	if err != nil {
		return err
	}
	if len(rules) > 4096 || len(stats) > 4096 || len(facts) > 4096 || len(evidence) > 1<<20 {
		return charityrouting.ErrResourceLimit
	}
	var requestValue, operationValue any
	if request != "" {
		requestValue = request
	}
	if operation != "" {
		operationValue = operation
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO abuse_actions(case_id,action,occurred_at,request_id,operation_id,actor_user_id,previous_ends_at,ends_at,reason_code,rules_json,statistics_json,evidence_count,evidence_bytes) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, id, action, at, requestValue, operationValue, actor, previous, ends, reason, string(rules), string(stats), len(facts), len(evidence))
	if err != nil {
		return err
	}
	seq, err := result.LastInsertId()
	if err != nil {
		return err
	}
	for i, e := range facts {
		if _, err := tx.ExecContext(ctx, `INSERT INTO abuse_evidence(action_seq,ordinal,occurred_at,request_id,violation_kind,content_chars) VALUES(?,?,?,?,?,?)`, seq, i, e.At, e.RequestID, kind, e.Chars); err != nil {
			return err
		}
	}
	return nil
}

func recordCase(ctx context.Context, tx *sql.Tx, user, now int64, kind, reason, request, operation string, ends int64, cfg Config, stats evidenceStatistics, facts []windowEvent) error {
	var id string
	var old sql.NullInt64
	err := tx.QueryRowContext(ctx, `SELECT id,ends_at FROM abuse_cases WHERE user_id=? AND kind=? AND state='active'`, user, kind).Scan(&id, &old)
	var previous *int64
	action := "trigger"
	if err == nil {
		action = "extend"
		if old.Valid {
			previous = new(old.Int64)
			ends = max(ends, old.Int64)
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var end *int64 = new(ends)
	if action == "extend" && !old.Valid {
		end = nil
	}
	if action == "trigger" {
		id, err = db.GenerateOpaqueID("abc_")
		if err != nil {
			return err
		}
		state, result := "active", "applied"
		var ended *int64
		if kind == "deduction" {
			state = "ended"
			ended = new(now)
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO abuse_cases(id,user_id,kind,reason_code,started_at,ends_at,ended_at,state,result) VALUES(?,?,?,?,?,?,?,?,?)`, id, user, kind, reason, now, end, ended, state, result)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE abuse_cases SET ends_at=?,reason_code=?,result='extended' WHERE id=?`, end, reason, id)
	}
	if err != nil {
		return err
	}
	rules, err := rulesJSON(cfg)
	if err != nil {
		return err
	}
	summary, err := json.Marshal(stats)
	if err != nil {
		return err
	}
	violation := "short_content"
	if reason == "charity_rpm" {
		violation = "rpm"
	}
	return appendAction(ctx, tx, id, action, reason, request, operation, nil, now, previous, end, rules, summary, facts, violation)
}

// ExpireTx records the authoritative end second, even when a worker notices
// it later. It participates in its caller's transaction and has a row budget.
func ExpireTx(ctx context.Context, tx *sql.Tx, now int64, limit int) error {
	return expireCasesTx(ctx, tx, now, limit, 0)
}
func expireUserTx(ctx context.Context, tx *sql.Tx, user, now int64) error {
	return expireCasesTx(ctx, tx, now, 2, user)
}
func expireCasesTx(ctx context.Context, tx *sql.Tx, now int64, limit int, user int64) error {
	query := `SELECT id,ends_at FROM abuse_cases WHERE state='active' AND ends_at<=?`
	args := []any{now}
	if user > 0 {
		query += ` AND user_id=?`
		args = append(args, user)
	}
	query += ` ORDER BY ends_at,id LIMIT ?`
	args = append(args, limit)
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	type due struct {
		id string
		at int64
	}
	var cases []due
	for rows.Next() {
		var c due
		if err = rows.Scan(&c.id, &c.at); err != nil {
			rows.Close()
			return err
		}
		cases = append(cases, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, c := range cases {
		if err := appendAction(ctx, tx, c.id, "expire", "expired", "", "", nil, c.at, new(c.at), new(c.at), []byte(`{}`), []byte(`{}`), nil, ""); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE abuse_cases SET state='ended',ended_at=ends_at,result='expired' WHERE id=? AND state='active'`, c.id); err != nil {
			return err
		}
	}
	return nil
}

// EndAutomaticTx closes the old automatic case when a manager takes over or
// releases a restriction. A subsequent automatic trigger starts a new case.
func EndAutomaticTx(ctx context.Context, tx *sql.Tx, user int64, kind string, actor, now int64, adjust bool, newEnd *int64) error {
	if err := expireUserTx(ctx, tx, user, now); err != nil {
		return err
	}
	var id string
	var old sql.NullInt64
	err := tx.QueryRowContext(ctx, `SELECT id,ends_at FROM abuse_cases WHERE user_id=? AND kind=? AND state='active'`, user, kind).Scan(&id, &old)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	action, reason, result := "release", "manual_release", "released"
	if adjust {
		action, reason, result = "adjust", "manual_adjustment", "adjusted"
	}
	var previous *int64
	if old.Valid {
		previous = new(old.Int64)
	}
	if err := appendAction(ctx, tx, id, action, reason, "", "", new(actor), now, previous, newEnd, []byte(`{}`), []byte(`{}`), nil, ""); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE abuse_cases SET state='ended',ended_at=?,result=? WHERE id=?`, now, result, id)
	return err
}

func PruneTx(ctx context.Context, tx *sql.Tx, now int64, limit int) error {
	if err := ExpireTx(ctx, tx, now, limit); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM abuse_cases WHERE id IN (SELECT id FROM abuse_cases WHERE state='ended' AND ended_at<=? ORDER BY ended_at,id LIMIT ?)`, now-RetentionSeconds, limit)
	return err
}
