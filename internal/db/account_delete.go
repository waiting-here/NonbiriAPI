package db

// Account deletion and late-callback linearization.
//
// This file implements the Phase 4 track T lifecycle repository:
//
//   - DeleteUserAccount removes a normal user and every user-associated row in
//     a single transaction. Most children are removed by the schema's
//     ON DELETE CASCADE (sessions, caller_keys, endpoints -> endpoint_keys ->
//     fetched_models / model_bindings, models -> model_bindings, request_logs,
//     user_issues). admin_alerts.subject_user_id has NO foreign key on purpose
//     (see schema.go): existing orphan alerts for the deleted user are removed
//     explicitly here, and FUTURE late callbacks are suppressed atomically by
//     RecordAdminAlert's INSERT ... SELECT ... WHERE EXISTS users so no new
//     orphan can ever be written.
//   - RecordAdminAlert is the atomic late-callback suppression hook for the
//     administrator alert center (track V and any other late writer). A
//     user-subject alert is inserted only while the subject user still exists;
//     after deletion the insert no-ops (inserted=false) rather than failing or
//     resurrecting an account. A NULL-subject alert (site-wide, no user) is
//     inserted unconditionally because there is no subject to suppress against.
//   - RecordUserIssue is the matching atomic suppression hook for late user
//     issue writes (track U and any other late writer). user_issues has a FK
//     CASCADE to users, so a plain INSERT against a deleted user would fail the
//     FK; the INSERT ... SELECT ... WHERE EXISTS users makes a late issue write
//     an atomic no-op instead.
//
// Linearization proof (why no read-then-write):
//
//   - The delete itself is a single conditional DELETE ... WHERE id=? AND
//     is_admin=0; the is_admin guard is part of the WHERE, so administrator
//     protection is atomic. The post-delete read only classifies a 0-affected
//     result (admin vs already-gone) for the right error code; it never gates
//     the delete.
//   - A late callback (RecordRequest / RecordAdminAlert / RecordUserIssue) and
//     a concurrent delete serialize through the single SQLite writer connection
//     (Store.DB has SetMaxOpenConns(1) + WAL + busy_timeout). Whichever wins
//     the writer lock commits first; the loser sees the committed state:
//     the callback's INSERT ... SELECT ... WHERE EXISTS users inserts zero
//     rows (no-op) when the delete won, and the delete's conditional DELETE
//     matches zero rows (ErrNotFound) when a prior delete already removed the
//     user. There is no interleaving that produces an orphan, a resurrection,
//     or a cross-user write.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/diagnostic"
)

// Late-callback text bounds. Every persisted alert/issue field is bounded and
// sanitized through diagnostic.BoundTo as the final sink policy, matching the
// fetch-rail issue writes.
const (
	maxAlertKindRunes    = 128
	maxAlertMessageRunes = 1024
	maxAlertRefRunes     = 256
	maxIssueKindRunes    = 128
	maxIssueMessageRunes = 1024
	maxIssueRefRunes     = 256
)

// DeleteUserAccount removes a normal user and all user-associated data in one
// transaction. It returns:
//
//   - nil on success;
//   - ErrAdminProtected if the row is the environment-owned administrator (it
//     can never be deleted through this path);
//   - ErrNotFound if the user was already absent (a concurrent or prior delete
//     won the writer lock).
//
// The current caller session is invalidated by the sessions cascade; the HTTP
// layer additionally clears the session cookie. No plaintext credential,
// ciphertext, or secret is accepted or returned here.
func (s *Store) DeleteUserAccount(ctx context.Context, userID int64) error {
	if userID <= 0 {
		return ErrNotFound
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("delete account: begin: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	// 1. Remove orphan admin alerts for this user. subject_user_id has no FK by
	//    design, so the user cascade does not touch admin_alerts; this explicit
	//    delete is the only way existing alerts about the user are cleaned.
	if _, err := tx.ExecContext(ctx, `DELETE FROM admin_alerts WHERE subject_user_id = ?`, userID); err != nil {
		return fmt.Errorf("delete account: clean orphan alerts: %w", err)
	}

	// 2. Atomic conditional delete of the user. is_admin=0 in the WHERE is the
	//    administrator-protection backstop; the cascade removes sessions,
	//    caller_keys, endpoints (+keys+fetched_models+bindings), models
	//    (+bindings), request_logs, and user_issues.
	res, err := tx.ExecContext(ctx, `DELETE FROM users WHERE id = ? AND is_admin = 0`, userID)
	if err != nil {
		return fmt.Errorf("delete account: delete user: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete account: rows affected: %w", err)
	}
	if affected == 1 {
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("delete account: commit: %w", err)
		}
		committed = true
		return nil
	}

	// 3. Zero rows affected: either the row is the administrator or it is
	//    already gone. Classify for the right stable error. This read never
	//    gates the delete (the delete already ran); it only reports.
	var isAdmin int
	err = tx.QueryRowContext(ctx, `SELECT is_admin FROM users WHERE id = ?`, userID).Scan(&isAdmin)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("delete account: classify: %w", err)
	}
	if isAdmin != 0 {
		return ErrAdminProtected
	}
	// The row exists, is not admin, but the conditional delete matched zero
	// rows. This is unreachable under the schema; treat it as already-gone.
	return ErrNotFound
}

// AdminAlertInput is the bounded, sanitized input for one administrator alert.
// No request/response content, credential, base URL, or ciphertext may ever be
// passed here; the repository applies diagnostic.BoundTo as the final sink.
type AdminAlertInput struct {
	Kind          string
	Message       string
	Ref           string
	SubjectUserID int64 // 0/NULL means a site-wide alert with no subject to suppress against
	CreatedAt     time.Time
}

func (i AdminAlertInput) validate() error {
	if i.Kind == "" {
		return fmt.Errorf("admin alert: kind is required")
	}
	if i.SubjectUserID < 0 {
		return fmt.Errorf("admin alert: subject user id is invalid")
	}
	if i.CreatedAt.IsZero() {
		return fmt.Errorf("admin alert: created at is required")
	}
	return nil
}

// RecordAdminAlert inserts one administrator alert atomically. When
// SubjectUserID > 0, the insert uses
//
//	INSERT INTO admin_alerts (...) SELECT ... WHERE EXISTS (SELECT 1 FROM users WHERE id=?)
//
// so a late callback against a deleted user is an atomic no-op (inserted=false)
// rather than a FK failure or an orphan row. When SubjectUserID == 0 the alert
// has no subject and is inserted unconditionally. The return is (true, nil) on
// an inserted row and (false, nil) on a suppressed no-op.
func (s *Store) RecordAdminAlert(ctx context.Context, input AdminAlertInput) (bool, error) {
	if err := input.validate(); err != nil {
		return false, err
	}
	kind := diagnostic.BoundTo(input.Kind, maxAlertKindRunes)
	message := diagnostic.BoundTo(input.Message, maxAlertMessageRunes)
	ref := diagnostic.BoundTo(input.Ref, maxAlertRefRunes)
	if kind == "" {
		return false, fmt.Errorf("admin alert: kind is invalid")
	}
	created := input.CreatedAt.Unix()

	if input.SubjectUserID == 0 {
		result, err := s.db.ExecContext(ctx, `
INSERT INTO admin_alerts (kind, message, ref, subject_user_id, created_at)
SELECT ?, ?, ?, NULL, ?
WHERE (SELECT COUNT(*) FROM admin_alerts) < ?`,
			kind, message, ref, created, MaxAdminAlertsTotal)
		if err != nil {
			return false, fmt.Errorf("record admin alert: insert: %w", err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return false, fmt.Errorf("record admin alert: rows affected: %w", err)
		}
		return affected == 1, nil
	}

	// Subject-bound alert: atomic conditional insert. Zero rows are inserted
	// when the subject user has been deleted (late-callback linearization).
	result, err := s.db.ExecContext(ctx, `
INSERT INTO admin_alerts (kind, message, ref, subject_user_id, created_at)
SELECT ?, ?, ?, ?, ?
WHERE EXISTS (SELECT 1 FROM users WHERE id = ?)
  AND (SELECT COUNT(*) FROM admin_alerts) < ?`,
		kind, message, ref, input.SubjectUserID, created, input.SubjectUserID, MaxAdminAlertsTotal)
	if err != nil {
		return false, fmt.Errorf("record admin alert: insert: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("record admin alert: rows affected: %w", err)
	}
	return affected == 1, nil
}

// UserIssueInput is the bounded, sanitized input for one user issue. No
// request/response content, credential, base URL, or ciphertext may ever be
// passed here.
type UserIssueInput struct {
	UserID    int64
	Kind      string
	Message   string
	Ref       string
	CreatedAt time.Time
}

func (i UserIssueInput) validate() error {
	if i.UserID <= 0 {
		return fmt.Errorf("user issue: user id is required")
	}
	if i.Kind == "" {
		return fmt.Errorf("user issue: kind is required")
	}
	if i.CreatedAt.IsZero() {
		return fmt.Errorf("user issue: created at is required")
	}
	return nil
}

// RecordUserIssue inserts one user issue atomically. The insert uses
//
//	INSERT INTO user_issues (...) SELECT ... WHERE EXISTS (SELECT 1 FROM users WHERE id=?)
//
// so a late issue callback against a deleted user is an atomic no-op
// (inserted=false) instead of failing the user_issues foreign key. This is the
// shared linearization helper for track U and any other late issue writer; the
// fetch rail's FailFetch uses the same pattern inline.
func (s *Store) RecordUserIssue(ctx context.Context, input UserIssueInput) (bool, error) {
	if err := input.validate(); err != nil {
		return false, err
	}
	kind := diagnostic.BoundTo(input.Kind, maxIssueKindRunes)
	message := diagnostic.BoundTo(input.Message, maxIssueMessageRunes)
	ref := diagnostic.BoundTo(input.Ref, maxIssueRefRunes)
	if kind == "" {
		return false, fmt.Errorf("user issue: kind is invalid")
	}
	created := input.CreatedAt.Unix()

	result, err := s.db.ExecContext(ctx, `
INSERT INTO user_issues (user_id, kind, message, ref, created_at)
SELECT ?, ?, ?, ?, ?
WHERE EXISTS (SELECT 1 FROM users WHERE id = ?)
  AND (SELECT COUNT(*) FROM user_issues WHERE user_id = ?) < ?`,
		input.UserID, kind, message, ref, created, input.UserID, input.UserID, MaxUserIssuesPerUser)
	if err != nil {
		return false, fmt.Errorf("record user issue: insert: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("record user issue: rows affected: %w", err)
	}
	return affected == 1, nil
}
