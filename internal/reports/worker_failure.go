package reports

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"

	"github.com/waiting-here/NonbiriAPI/internal/worker"
)

func (repository *Repository) recordCaseFailure(
	ctx context.Context,
	caseID string,
	operationKind string,
	class worker.ErrorClass,
	now int64,
) error {
	if class != worker.ErrorDBBusy && class != worker.ErrorRetryable && class != worker.ErrorInvariant {
		return ErrInvariant
	}
	batchCtx, cancel := repository.boundedWorkerContext(ctx)
	defer cancel()
	tx, err := repository.beginTx(batchCtx)
	if err != nil {
		return fmt.Errorf("reports: begin failure record: %w", err)
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)
	var status, progress string
	var previousAttempt int64
	err = tx.QueryRowContext(batchCtx, `SELECT status,progress_state,retry_attempt_count
FROM report_cases WHERE id=?`, caseID).Scan(&status, &progress, &previousAttempt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reports: read failed case: %w", err)
	}
	valid := progress == "in_progress" &&
		(operationKind == "report_indexing" && (status == "pending_indexing" || status == "expired") ||
			operationKind == "report_approved_processing" && status == "approved_processing")
	if !valid {
		return nil
	}
	if previousAttempt == math.MaxInt64 {
		return ErrInvariant
	}
	attempt := previousAttempt + 1
	nextRetry, err := retryAt(now, attempt, class)
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(batchCtx, `UPDATE report_cases SET
retry_attempt_count=?,next_retry_at=?,last_error_class=?
WHERE id=? AND status=? AND progress_state='in_progress' AND retry_attempt_count=?`,
		attempt, nextRetry, string(class), caseID, status, previousAttempt)
	if err != nil {
		return fmt.Errorf("reports: record case retry: %w", err)
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return ErrConflict
	}
	var operationID, operationState string
	err = tx.QueryRowContext(batchCtx, `SELECT id,state FROM accepted_operations
WHERE kind=? AND checkpoint=? AND state IN ('accepted','running','failed_retryable')
ORDER BY created_at DESC,id DESC LIMIT 1`, operationKind, caseID).Scan(
		&operationID, &operationState)
	if err != nil {
		return fmt.Errorf("reports: read failed operation: %w", err)
	}
	operationNextState := "failed_retryable"
	var terminal any
	if class == worker.ErrorInvariant {
		operationNextState = "failed_blocked"
		terminal = now
	}
	result, err = tx.ExecContext(batchCtx, `UPDATE accepted_operations SET
state=?,last_error_class=?,terminal_at=? WHERE id=? AND state=?`,
		operationNextState, string(class), terminal, operationID, operationState)
	if err != nil {
		return fmt.Errorf("reports: record operation retry: %w", err)
	}
	changed, _ = result.RowsAffected()
	if changed != 1 {
		return ErrConflict
	}
	if attempt == 10 {
		if err := insertReportRetryAlertTx(batchCtx, tx, caseID, operationKind, class, now); err != nil {
			return err
		}
	}
	if class == worker.ErrorInvariant {
		if err := insertInvariantAlertTx(batchCtx, tx, caseID, "report_worker_invariant", now); err != nil {
			return err
		}
	}
	return commit(tx, &committed)
}

func insertReportRetryAlertTx(
	ctx context.Context,
	tx *sql.Tx,
	caseID string,
	operationKind string,
	class worker.ErrorClass,
	now int64,
) error {
	message := operationKind + ":" + string(class) + ":attempt_10"
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM admin_alerts
WHERE kind='report_retry_exhausted' AND ref=? AND resolved=0)`, caseID).Scan(&exists); err != nil {
		return fmt.Errorf("reports: inspect retry alert: %w", err)
	}
	if exists == 1 {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO admin_alerts(kind,message,ref,created_at,resolved)
VALUES('report_retry_exhausted',?,?,?,0)`, message, caseID, now); err != nil {
		return fmt.Errorf("reports: write retry alert: %w", err)
	}
	return nil
}
