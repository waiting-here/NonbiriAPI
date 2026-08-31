package reports

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

func (repository *Repository) processExpiries(ctx context.Context, now int64, limit int) (int, bool, error) {
	ids, more, err := dueCaseIDs(ctx, repository.db, `SELECT id FROM report_cases
WHERE status IN ('pending_indexing','pending_review') AND deadline<=?
ORDER BY deadline,id LIMIT ?`, now, limit)
	if err != nil {
		return 0, false, fmt.Errorf("reports: list expiring cases: %w", err)
	}
	processed := 0
	for _, caseID := range ids {
		if ctx.Err() != nil {
			return processed, true, context.Cause(ctx)
		}
		batchCtx, cancel := repository.boundedWorkerContext(ctx)
		didExpire, err := repository.expireCase(batchCtx, caseID, now)
		cancel()
		if err != nil {
			return processed, true, err
		}
		if didExpire {
			processed++
		}
	}
	return processed, more, nil
}

func (repository *Repository) expireCase(ctx context.Context, caseID string, now int64) (bool, error) {
	tx, err := repository.beginTx(ctx)
	if err != nil {
		return false, err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)
	var (
		status                         string
		fingerprint                    []byte
		materialVersion, targetVersion int64
		deadline                       int64
	)
	err = tx.QueryRowContext(ctx, `SELECT status,fingerprint,material_version,target_version,deadline
FROM report_cases WHERE id=?`, caseID).Scan(&status, &fingerprint, &materialVersion, &targetVersion, &deadline)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("reports: read expiring case: %w", err)
	}
	defer clear(fingerprint)
	if status != "pending_indexing" && status != "pending_review" {
		return false, nil
	}
	if deadline > now {
		return false, nil
	}
	if len(fingerprint) != 32 || materialVersion < 1 || targetVersion < 1 {
		return false, ErrInvariant
	}
	didExpire, err := repository.expireActiveCaseTx(
		ctx, tx, caseID, status, fingerprint, materialVersion, targetVersion, deadline, now,
	)
	if err != nil || !didExpire {
		return didExpire, err
	}
	if err := commit(tx, &committed); err != nil {
		return false, err
	}
	return true, nil
}

func (repository *Repository) expireActiveCaseTx(
	ctx context.Context,
	tx *sql.Tx,
	caseID string,
	status string,
	fingerprint []byte,
	materialVersion int64,
	targetVersion int64,
	deadline int64,
	now int64,
) (bool, error) {
	return repository.expireActiveCaseCoreTx(
		ctx, tx, caseID, status, fingerprint, materialVersion, targetVersion, deadline, now, true,
	)
}

func (repository *Repository) expireActiveCaseWithoutIssueProjectionTx(
	ctx context.Context,
	tx *sql.Tx,
	caseID string,
	status string,
	fingerprint []byte,
	materialVersion int64,
	targetVersion int64,
	deadline int64,
	now int64,
) (bool, error) {
	return repository.expireActiveCaseCoreTx(
		ctx, tx, caseID, status, fingerprint, materialVersion, targetVersion, deadline, now, false,
	)
}

func (repository *Repository) expireActiveCaseCoreTx(
	ctx context.Context,
	tx *sql.Tx,
	caseID string,
	status string,
	fingerprint []byte,
	materialVersion int64,
	targetVersion int64,
	deadline int64,
	now int64,
	reconcileIssues bool,
) (bool, error) {
	if ctx == nil || tx == nil || len(fingerprint) != 32 || materialVersion < 1 || targetVersion < 1 ||
		deadline < 0 || now < deadline || (status != "pending_indexing" && status != "pending_review") {
		return false, ErrInvariant
	}
	var protectedCount int64
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM report_targets
WHERE case_id=? AND state='protected'`, caseID).Scan(&protectedCount); err != nil {
		return false, fmt.Errorf("reports: count expiring targets: %w", err)
	}
	progress := "complete"
	if status == "pending_indexing" {
		progress = "in_progress"
	}
	updated, err := tx.ExecContext(ctx, `UPDATE report_cases SET
status='expired',progress_state=?,decision_reason='deadline_expired',decision_actor_user_id=NULL,
decision_at=?,terminal_at=?,released_target_count=released_target_count+?,retry_attempt_count=0,
next_retry_at=NULL,last_error_class=NULL
WHERE id=? AND status=? AND deadline<=? AND material_version=? AND target_version=?`,
		progress, now, now, protectedCount, caseID, status, now, materialVersion, targetVersion)
	if err != nil {
		return false, fmt.Errorf("reports: expire case: %w", err)
	}
	changed, _ := updated.RowsAffected()
	if changed != 1 {
		return false, nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE report_targets SET
state='released',decided_version=?,updated_at=? WHERE case_id=? AND state='protected'`,
		targetVersion, now, caseID); err != nil {
		return false, fmt.Errorf("reports: release expired targets: %w", err)
	}
	issueTransitions := make(reportIssueTransitions)
	if reconcileIssues {
		issueTargets, err := readCaseIssueTargetsTx(ctx, tx, caseID)
		if err != nil {
			return false, err
		}
		if err := snapshotReportIssueTargetsTx(ctx, tx, issueTransitions, issueTargets); err != nil {
			return false, err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM endpoint_key_suspensions
WHERE reason_type='report_case' AND report_case_id=?`, caseID); err != nil {
		return false, fmt.Errorf("reports: release expired suspension: %w", err)
	}
	if reconcileIssues {
		if err := repository.reconcileReportIssueTransitionsTx(ctx, tx, issueTransitions); err != nil {
			return false, err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO report_decisions(
case_id,material_version,target_version,actor_user_id,action,reason,created_at)
VALUES(?,?,?,NULL,'expire','deadline_expired',?)`, caseID, materialVersion, targetVersion, now); err != nil {
		return false, fmt.Errorf("reports: append expiry decision: %w", err)
	}
	if status == "pending_indexing" {
		if err := repository.ensureIndexingOperationTx(ctx, tx, caseID, fingerprint, now); err != nil {
			return false, err
		}
	} else {
		if _, err := tx.ExecContext(ctx, `DELETE FROM accepted_operations
WHERE kind='report_indexing' AND checkpoint=? AND state='completed'`, caseID); err != nil {
			return false, fmt.Errorf("reports: clear completed indexing operation: %w", err)
		}
	}
	return true, nil
}

func (repository *Repository) ensureIndexingOperationTx(
	ctx context.Context,
	tx *sql.Tx,
	caseID string,
	fingerprint []byte,
	now int64,
) error {
	var operationID, state string
	err := tx.QueryRowContext(ctx, `SELECT id,state FROM accepted_operations
WHERE kind='report_indexing' AND checkpoint=? AND state IN ('accepted','running','failed_retryable')
ORDER BY created_at DESC,id DESC LIMIT 1`, caseID).Scan(&operationID, &state)
	if errors.Is(err, sql.ErrNoRows) {
		newID, idErr := repository.generateID("op_")
		if idErr != nil {
			return ErrUnavailable
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO accepted_operations(
id,kind,actor_user_id,actor_role,payload_hash,state,checkpoint,last_error_class,created_at,terminal_at)
VALUES(?,'report_indexing',NULL,'',?,'accepted',?,NULL,?,NULL)`, newID, fingerprint, caseID, now); err != nil {
			return fmt.Errorf("reports: accept expiry indexing operation: %w", err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("reports: read indexing operation: %w", err)
	}
	if state == "accepted" {
		return nil
	}
	if state != "running" && state != "failed_retryable" {
		return ErrInvariant
	}
	result, err := tx.ExecContext(ctx, `UPDATE accepted_operations SET
state='accepted',last_error_class=NULL,terminal_at=NULL WHERE id=? AND state=?`, operationID, state)
	if err != nil {
		return fmt.Errorf("reports: recover expiry indexing operation: %w", err)
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return ErrConflict
	}
	return nil
}
