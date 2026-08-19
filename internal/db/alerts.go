package db

// Administrator alert-center repository (Phase 4, track V): bounded reads,
// the idempotent resolve/ack update, and the flood-safe event-write hook.
//
// Read path:
//
//   - ListAdminAlerts returns one offset-paginated page (id DESC) with an
//     optional resolved filter. The page size is clamped and the table is
//     never loaded in full: hasMore is derived from one extra fetched row.
//   - kind/message/ref are re-bounded through diagnostic.BoundTo on the read
//     side as the final sink policy, so even a future writer that bypasses
//     the write boundary cannot project an unbounded or line-forging value.
//   - The projection carries no credential, ciphertext, request/response
//     content, or unbounded diagnostic: an alert stores only the bounded
//     metadata an administrator needs to act.
//
// Write path:
//
//   - RecordAdminAlertBounded is the production event hook. It applies a
//     closed kind registry (single authority, like the connector registry),
//     the shared text bounds, and two flood controls in one atomic statement:
//
//     - dedupe: an unresolved alert with the same (kind, ref, subject) is
//       suppressed, so a repeating event source cannot pile up identical
//       pending alerts;
//     - per-kind cap: at most MaxAdminAlertsPerKind unresolved alerts of one
//       kind may exist; a saturated kind rejects new events until an
//       administrator resolves some. With the closed registry this bounds
//       total pending alerts, so no event loop can flood the table.
//
//     The subject guard is the track-T linearization pattern
//     (INSERT ... SELECT ... WHERE EXISTS users) inlined: a late callback
//     against a deleted user is an atomic no-op, never an orphan row and
//     never a FK failure (admin_alerts.subject_user_id has no FK on purpose,
//     see schema.go).
//
//   - SetAdminAlertResolved is the idempotent resolve/ack (and reopen)
//     update. Repeating the same transition is a true no-op that keeps the
//     original resolved_at; a missing alert is ErrNotFound. The conditional
//     UPDATE and the read-back SELECT serialize on the single writer
//     connection, so the classification cannot race a concurrent change.
//
// The base primitive RecordAdminAlert (track T) remains the raw bounded
// insert; event writers must use RecordAdminAlertBounded so the flood
// controls are never bypassed.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/diagnostic"
)

const (
	// MaxAdminAlertsPageLimit bounds one admin-alert page. Page sizes are
	// clamped into [1, MaxAdminAlertsPageLimit] at the handler and
	// re-validated here.
	MaxAdminAlertsPageLimit = 100
	// MaxAdminAlertsPerKind bounds the pending (unresolved) alerts of one
	// kind. A new event of a saturated kind is suppressed (inserted=false)
	// until an administrator resolves pending alerts of that kind.
	MaxAdminAlertsPerKind = 100
	// MaxAdminAlertsTotal bounds retained alert history. It is a hard fail-closed
	// cap; the resolved-alert retention policy (CleanupResolvedAlerts, see below)
	// removes old resolved rows periodically so the cap stays available for new
	// events instead of being consumed by history.
	MaxAdminAlertsTotal = 10000
	// ResolvedAlertRetention is the policy retention window for resolved admin
	// alerts: a resolved row whose resolved_at is older than now minus this
	// window is removed by CleanupResolvedAlerts. Pending (unresolved) alerts
	// are never removed by retention — an administrator keeps the chance to act
	// on them. The value is the frozen 30-day resolved-alert retention policy.
	ResolvedAlertRetention = 30 * 24 * time.Hour
)

// Alert kind registry. This is the single authority for alert event kinds:
// RecordAdminAlertBounded rejects an unregistered kind with an error so a
// future rail learns immediately that its event type must be registered
// first (fail closed, never a silent drop into an unbounded kind space).
const (
	// AlertKindFetchFailed marks a failed upstream model-fetch attempt (the
	// fetch rail's FailFetch site of events, surfaced for administrator
	// attention in addition to the per-user issue).
	AlertKindFetchFailed = "fetch_failed"
	// AlertKindForwardError marks a forwarding-stage upstream error that
	// needs administrator attention.
	AlertKindForwardError = "forward_error"
	// AlertKindRegistrationRejected marks a registration attempt rejected by
	// the Discord guild/role gate.
	AlertKindRegistrationRejected = "registration_rejected"
)

var validAlertKinds = map[string]bool{
	AlertKindFetchFailed:          true,
	AlertKindForwardError:         true,
	AlertKindRegistrationRejected: true,
}

// ValidAdminAlertKind reports whether kind is a registered alert kind.
func ValidAdminAlertKind(kind string) bool {
	return validAlertKinds[kind]
}

// AdminAlert is one bounded, sanitized alert row. CreatedAt and ResolvedAt
// are unix seconds; ResolvedAt is the zero time while the alert is
// unresolved. SubjectUserID is 0 when the alert is site-wide (no subject).
type AdminAlert struct {
	ID            int64
	Kind          string
	Message       string
	Ref           string
	SubjectUserID int64
	CreatedAt     time.Time
	Resolved      bool
	ResolvedAt    time.Time
}

// AlertQuery is the bounded, parameterized admin-alert filter. Every filter
// is optional. Results are ordered id DESC and offset-paginated with
// Page/PageSize (LIMIT page_size+1 OFFSET (page-1)*page_size), so a client
// never infers pagination from a raw page size.
type AlertQuery struct {
	Resolved *bool // nil = both states; true/false filters one state
	Page     int   // 1-based; values below 1 are treated as 1
	PageSize int   // clamped to 1..MaxAdminAlertsPageLimit; 0 selects the default
}

// ListAdminAlerts returns at most one page of alerts matching the filter,
// newest first. hasMore reports whether another page exists past the
// returned rows; it is computed from one extra fetched row, so no second
// query is needed and no full-table scan ever happens. kind/message/ref are
// re-bounded through diagnostic.BoundTo as the final read-side sink.
func (s *Store) ListAdminAlerts(ctx context.Context, query AlertQuery) (alerts []AdminAlert, hasMore bool, err error) {
	page := query.Page
	if page < 1 {
		page = 1
	}
	pageSize := query.PageSize
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > MaxAdminAlertsPageLimit {
		pageSize = MaxAdminAlertsPageLimit
	}

	var clauses []string
	var args []any
	if query.Resolved != nil {
		clauses = append(clauses, "resolved = ?")
		args = append(args, boolInt(*query.Resolved))
	}
	sqlText := `
SELECT id, kind, message, ref, subject_user_id, created_at, resolved, resolved_at
FROM admin_alerts`
	if len(clauses) > 0 {
		// #nosec G202 -- clauses contains only fixed resolved predicates;
		// every filter value is a bound argument.
		sqlText += ` WHERE ` + strings.Join(clauses, " AND ")
	}
	sqlText += `
ORDER BY id DESC
LIMIT ? OFFSET ?`
	args = append(args, pageSize+1, (page-1)*pageSize)

	rows, err := s.db.QueryContext(ctx, sqlText, args...)
	if err != nil {
		return nil, false, fmt.Errorf("query admin alerts: %w", err)
	}
	defer rows.Close()

	alerts = make([]AdminAlert, 0, min(pageSize, 32))
	for rows.Next() {
		var (
			row        AdminAlert
			subjectID  sql.NullInt64
			createdAt  int64
			resolved   int
			resolvedAt sql.NullInt64
		)
		if err := rows.Scan(&row.ID, &row.Kind, &row.Message, &row.Ref, &subjectID, &createdAt, &resolved, &resolvedAt); err != nil {
			return nil, false, fmt.Errorf("scan admin alert: %w", err)
		}
		row.Kind = diagnostic.BoundTo(row.Kind, maxAlertKindRunes)
		row.Message = diagnostic.BoundTo(row.Message, maxAlertMessageRunes)
		row.Ref = diagnostic.BoundTo(row.Ref, maxAlertRefRunes)
		if subjectID.Valid {
			row.SubjectUserID = subjectID.Int64
		}
		row.CreatedAt = time.Unix(createdAt, 0).UTC()
		row.Resolved = resolved != 0
		if resolvedAt.Valid {
			row.ResolvedAt = time.Unix(resolvedAt.Int64, 0).UTC()
		}
		alerts = append(alerts, row)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("iterate admin alerts: %w", err)
	}
	hasMore = len(alerts) > pageSize
	if hasMore {
		alerts = alerts[:pageSize]
	}
	return alerts, hasMore, nil
}

// SetAdminAlertResolved is the idempotent resolve/ack (resolved=true) and
// reopen (resolved=false) for one alert. The first transition writes the
// state; repeating the same transition is a true no-op that keeps the
// original resolved_at, so a double click or a stale page never errors or
// churns the row. A missing alert is ErrNotFound; no other information about
// the row is revealed. now is a unix-seconds timestamp used only when
// resolving.
func (s *Store) SetAdminAlertResolved(ctx context.Context, alertID int64, resolved bool, now int64) (AdminAlert, error) {
	if alertID <= 0 {
		return AdminAlert{}, ErrNotFound
	}
	var sqlText string
	var args []any
	if resolved {
		sqlText = `UPDATE admin_alerts SET resolved = 1, resolved_at = ? WHERE id = ? AND resolved = 0`
		args = []any{now, alertID}
	} else {
		sqlText = `UPDATE admin_alerts SET resolved = 0, resolved_at = NULL WHERE id = ? AND resolved = 1`
		args = []any{alertID}
	}
	if _, err := s.db.ExecContext(ctx, sqlText, args...); err != nil {
		return AdminAlert{}, fmt.Errorf("set admin alert resolved: %w", err)
	}

	// Read back the alert for the response. The conditional UPDATE and this
	// SELECT serialize on the single writer connection (Store.DB has
	// SetMaxOpenConns(1)), so the classification below is stable: zero
	// affected rows means the alert is absent or was already in the target
	// state, and the SELECT decides which.
	var row AdminAlert
	if err := scanAdminAlertByID(ctx, s.db, alertID, &row); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AdminAlert{}, ErrNotFound
		}
		return AdminAlert{}, err
	}
	return row, nil
}

func scanAdminAlertByID(ctx context.Context, db *sql.DB, alertID int64, dst *AdminAlert) error {
	var (
		subjectID  sql.NullInt64
		createdAt  int64
		resolved   int
		resolvedAt sql.NullInt64
	)
	err := db.QueryRowContext(ctx, `
SELECT id, kind, message, ref, subject_user_id, created_at, resolved, resolved_at
FROM admin_alerts WHERE id = ?`, alertID).
		Scan(&dst.ID, &dst.Kind, &dst.Message, &dst.Ref, &subjectID, &createdAt, &resolved, &resolvedAt)
	if err != nil {
		return err
	}
	dst.Kind = diagnostic.BoundTo(dst.Kind, maxAlertKindRunes)
	dst.Message = diagnostic.BoundTo(dst.Message, maxAlertMessageRunes)
	dst.Ref = diagnostic.BoundTo(dst.Ref, maxAlertRefRunes)
	if subjectID.Valid {
		dst.SubjectUserID = subjectID.Int64
	}
	dst.CreatedAt = time.Unix(createdAt, 0).UTC()
	dst.Resolved = resolved != 0
	if resolvedAt.Valid {
		dst.ResolvedAt = time.Unix(resolvedAt.Int64, 0).UTC()
	}
	return nil
}

// RecordAdminAlertBounded is the flood-safe event-write hook of the
// administrator alert center. On top of the shared bounds and the atomic
// subject guard of RecordAdminAlert it enforces, in the same statement:
//
//   - the closed kind registry (an unregistered kind is rejected with an
//     error, never silently recorded);
//   - dedupe: while an unresolved alert with the same (kind, ref, subject)
//     already exists, the new event is suppressed (inserted=false);
//   - the per-kind pending cap MaxAdminAlertsPerKind: a saturated kind
//     suppresses new events (inserted=false).
//
// The return is (true, nil) on an inserted row and (false, nil) on a
// suppressed no-op (dedupe, flood cap, or deleted subject user).
func (s *Store) RecordAdminAlertBounded(ctx context.Context, input AdminAlertInput) (bool, error) {
	if err := input.validate(); err != nil {
		return false, err
	}
	if !ValidAdminAlertKind(input.Kind) {
		return false, fmt.Errorf("admin alert: kind %q is not registered", input.Kind)
	}
	kind := diagnostic.BoundTo(input.Kind, maxAlertKindRunes)
	message := diagnostic.BoundTo(input.Message, maxAlertMessageRunes)
	ref := diagnostic.BoundTo(input.Ref, maxAlertRefRunes)
	created := input.CreatedAt.Unix()

	var sqlText string
	var args []any
	if input.SubjectUserID == 0 {
		// Site-wide alert: no subject to guard against, so only dedupe and
		// the flood cap apply. subject_user_id IS NULL is matched explicitly
		// so a NULL-subject dedupe never collides with a subject-bound one.
		sqlText = `
INSERT INTO admin_alerts (kind, message, ref, subject_user_id, created_at)
SELECT ?, ?, ?, NULL, ?
WHERE NOT EXISTS (
        SELECT 1 FROM admin_alerts
        WHERE kind = ? AND ref = ? AND subject_user_id IS NULL AND resolved = 0)
  AND (SELECT COUNT(*) FROM admin_alerts WHERE kind = ? AND resolved = 0) < ?
  AND (SELECT COUNT(*) FROM admin_alerts) < ?`
		args = []any{kind, message, ref, created, kind, ref, kind, MaxAdminAlertsPerKind, MaxAdminAlertsTotal}
	} else {
		// Subject-bound alert: the track-T linearization guard makes a late
		// callback against a deleted user an atomic no-op, and dedupe/flood
		// apply to the same subject/kind.
		sqlText = `
INSERT INTO admin_alerts (kind, message, ref, subject_user_id, created_at)
SELECT ?, ?, ?, ?, ?
WHERE EXISTS (SELECT 1 FROM users WHERE id = ?)
  AND NOT EXISTS (
        SELECT 1 FROM admin_alerts
        WHERE kind = ? AND ref = ? AND subject_user_id = ? AND resolved = 0)
  AND (SELECT COUNT(*) FROM admin_alerts WHERE kind = ? AND resolved = 0) < ?
  AND (SELECT COUNT(*) FROM admin_alerts) < ?`
		args = []any{kind, message, ref, input.SubjectUserID, created, input.SubjectUserID,
			kind, ref, input.SubjectUserID, kind, MaxAdminAlertsPerKind, MaxAdminAlertsTotal}
	}
	result, err := s.db.ExecContext(ctx, sqlText, args...)
	if err != nil {
		return false, fmt.Errorf("record admin alert bounded: insert: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("record admin alert bounded: rows affected: %w", err)
	}
	return affected == 1, nil
}

// CleanupResolvedAlerts removes resolved admin alerts whose resolved_at is
// older than the retention window (now - retention). Pending (unresolved)
// alerts are never removed: an administrator must keep the chance to act on
// them, so the delete predicates on resolved = 1 and resolved_at alone. The
// age clock is resolved_at (when the alert was acked), not created_at — a
// long-pending alert that an administrator only just resolved stays for the
// full retention window counted from the resolve time.
//
// The retention is a parameter so tests and targeted sweeps can pass an
// explicit window; production passes ResolvedAlertRetention. The cutoff is
// derived from the service clock (now), never from client input. Only a
// bounded deleted-row count is returned; no alert content (kind/message/ref)
// is ever emitted in an error or log line.
func (s *Store) CleanupResolvedAlerts(ctx context.Context, retention time.Duration) (int64, error) {
	return s.cleanupResolvedAlertsAt(ctx, time.Now(), retention)
}

func (s *Store) cleanupResolvedAlertsAt(ctx context.Context, now time.Time, retention time.Duration) (int64, error) {
	if ctx == nil {
		return 0, fmt.Errorf("cleanup resolved alerts: context is required")
	}
	if retention < 0 {
		return 0, fmt.Errorf("cleanup resolved alerts: retention must be non-negative")
	}
	cutoff := now.UTC().Add(-retention).Unix()
	result, err := s.db.ExecContext(ctx, `DELETE FROM admin_alerts WHERE resolved = 1 AND resolved_at < ?`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("cleanup resolved alerts: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("cleanup resolved alerts: rows affected: %w", err)
	}
	return affected, nil
}
