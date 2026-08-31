package reports

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

type approvalCase struct {
	id            string
	targetVersion int64
	targetCount   int64
	processed     int64
	deleted       int64
	createdAt     int64
	cursor        int64
}

type approvalTarget struct {
	id            string
	sequence      int64
	endpointKeyID sql.NullInt64
	ownerUserID   sql.NullInt64
}

func (repository *Repository) processApprovalCases(ctx context.Context, now int64, limit int) (int, bool, error) {
	ids, more, err := dueCaseIDs(ctx, repository.db, `SELECT id FROM report_cases
WHERE status='approved_processing' AND (next_retry_at IS NULL OR next_retry_at<=?1)
ORDER BY COALESCE(next_retry_at,0),created_at,id LIMIT ?2`, now, limit)
	if err != nil {
		return 0, false, fmt.Errorf("reports: list approval cases: %w", err)
	}
	processed := 0
	for _, caseID := range ids {
		if ctx.Err() != nil {
			return processed, true, context.Cause(ctx)
		}
		batchCtx, cancel := repository.boundedWorkerContext(ctx)
		worked, caseMore, workErr := repository.processApprovalCase(batchCtx, caseID, now)
		cancel()
		if workErr != nil {
			if ctx.Err() != nil {
				return processed, true, context.Cause(ctx)
			}
			if err := repository.recordCaseFailure(ctx, caseID, "report_approved_processing", classifyWorkerError(workErr), now); err != nil {
				return processed, true, err
			}
			processed++
			continue
		}
		if worked {
			processed++
		}
		more = more || caseMore
	}
	return processed, more, nil
}

func readApprovalCaseTx(ctx context.Context, tx *sql.Tx, caseID string) (approvalCase, error) {
	var row approvalCase
	var cursor sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT id,target_version,target_count,processed_target_count,
deleted_target_count,created_at,cursor_text FROM report_cases
WHERE id=? AND status='approved_processing' AND progress_state='in_progress'`, caseID).Scan(
		&row.id, &row.targetVersion, &row.targetCount, &row.processed, &row.deleted, &row.createdAt, &cursor)
	if errors.Is(err, sql.ErrNoRows) {
		return approvalCase{}, ErrNotFound
	}
	if err != nil {
		return approvalCase{}, fmt.Errorf("reports: read approval case: %w", err)
	}
	if !db.ValidateOpaqueID(row.id, "rpc_") || row.targetVersion < 1 || row.targetCount < 0 ||
		row.processed < 0 || row.processed > row.targetCount || row.deleted < 0 || row.deleted > row.targetCount {
		return approvalCase{}, ErrInvariant
	}
	if cursor.Valid && cursor.String != "" {
		row.cursor, err = strconv.ParseInt(cursor.String, 10, 64)
		if err != nil || row.cursor < 0 || strconv.FormatInt(row.cursor, 10) != cursor.String {
			return approvalCase{}, ErrInvariant
		}
	}
	return row, nil
}

func readApprovalTargetsTx(ctx context.Context, tx *sql.Tx, row approvalCase) ([]approvalTarget, bool, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id,target_seq,endpoint_key_id,owner_user_id
FROM report_targets WHERE case_id=? AND state='protected' AND target_seq>?
ORDER BY target_seq,id LIMIT ?`, row.id, row.cursor, workerBatchLimit+1)
	if err != nil {
		return nil, false, fmt.Errorf("reports: enumerate approval targets: %w", err)
	}
	defer rows.Close()
	targets := make([]approvalTarget, 0, workerBatchLimit+1)
	for rows.Next() {
		var target approvalTarget
		if err := rows.Scan(&target.id, &target.sequence, &target.endpointKeyID, &target.ownerUserID); err != nil {
			return nil, false, fmt.Errorf("reports: scan approval target: %w", err)
		}
		if !db.ValidateOpaqueID(target.id, "rpt_") || target.sequence < 1 {
			return nil, false, ErrInvariant
		}
		targets = append(targets, target)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("reports: enumerate approval targets: %w", err)
	}
	more := len(targets) > workerBatchLimit
	if more {
		targets = targets[:workerBatchLimit]
	}
	return targets, more, nil
}

func (repository *Repository) processApprovalCase(ctx context.Context, caseID string, now int64) (bool, bool, error) {
	tx, err := repository.beginTx(ctx)
	if err != nil {
		return false, false, err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)
	row, err := readApprovalCaseTx(ctx, tx, caseID)
	if errors.Is(err, ErrNotFound) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	if err := markReportOperationRunningTx(ctx, tx, "report_approved_processing", caseID); err != nil {
		return false, false, err
	}
	targets, more, err := readApprovalTargetsTx(ctx, tx, row)
	if err != nil {
		return false, false, err
	}
	lastCursor := row.cursor
	for _, target := range targets {
		if !target.endpointKeyID.Valid || target.endpointKeyID.Int64 <= 0 ||
			!target.ownerUserID.Valid || target.ownerUserID.Int64 <= 0 {
			return false, false, ErrInvariant
		}
		var owned int
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM endpoint_keys k
JOIN endpoints e ON e.id=k.endpoint_id WHERE k.id=? AND e.user_id=?)`,
			target.endpointKeyID.Int64, target.ownerUserID.Int64).Scan(&owned); err != nil {
			return false, false, fmt.Errorf("reports: verify approval target: %w", err)
		}
		if owned != 1 {
			return false, false, ErrInvariant
		}
		if err := repository.deleteKey(ctx, tx, target.ownerUserID.Int64, target.endpointKeyID.Int64, now); err != nil {
			return false, false, fmt.Errorf("reports: delete approved endpoint key: %w", err)
		}
		var remains int
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM endpoint_keys WHERE id=?)`,
			target.endpointKeyID.Int64).Scan(&remains); err != nil {
			return false, false, fmt.Errorf("reports: verify approved deletion: %w", err)
		}
		if remains != 0 {
			return false, false, ErrInvariant
		}
		result, err := tx.ExecContext(ctx, `UPDATE report_targets SET
state='deleted_by_approval',endpoint_key_id=NULL,decided_version=?,updated_at=?
WHERE id=? AND case_id=? AND state='protected'`, row.targetVersion, now, target.id, caseID)
		if err != nil {
			return false, false, fmt.Errorf("reports: complete approved target: %w", err)
		}
		changed, _ := result.RowsAffected()
		if changed != 1 {
			return false, false, ErrInvariant
		}
		lastCursor = target.sequence
	}
	processedIncrement := int64(len(targets))
	if more {
		result, err := tx.ExecContext(ctx, `UPDATE report_cases SET
cursor_text=?,processed_target_count=processed_target_count+?,deleted_target_count=deleted_target_count+?,
retry_attempt_count=0,next_retry_at=NULL,last_error_class=NULL
WHERE id=? AND status='approved_processing' AND target_version=?`,
			strconv.FormatInt(lastCursor, 10), processedIncrement, processedIncrement, caseID, row.targetVersion)
		if err != nil {
			return false, false, fmt.Errorf("reports: advance approval cursor: %w", err)
		}
		changed, _ := result.RowsAffected()
		if changed != 1 {
			return false, false, ErrConflict
		}
	} else {
		var stranded int64
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM report_targets
WHERE case_id=? AND state='protected'`, caseID).Scan(&stranded); err != nil {
			return false, false, fmt.Errorf("reports: inspect stranded approval targets: %w", err)
		}
		if stranded != 0 {
			return false, false, ErrInvariant
		}
		result, err := tx.ExecContext(ctx, `UPDATE report_cases SET
status='approved',progress_state='complete',cursor_text=NULL,
processed_target_count=processed_target_count+?,deleted_target_count=deleted_target_count+?,
retry_attempt_count=0,next_retry_at=NULL,last_error_class=NULL,terminal_at=?
WHERE id=? AND status='approved_processing' AND target_version=?
 AND processed_target_count+?=target_count`,
			processedIncrement, processedIncrement, now, caseID, row.targetVersion, processedIncrement)
		if err != nil {
			return false, false, fmt.Errorf("reports: complete approval: %w", err)
		}
		changed, _ := result.RowsAffected()
		if changed != 1 {
			return false, false, ErrInvariant
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM endpoint_key_suspensions
WHERE reason_type='report_case' AND report_case_id=?`, caseID); err != nil {
			return false, false, fmt.Errorf("reports: clear approval suspension: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM accepted_operations
WHERE kind='report_approved_processing' AND checkpoint=?`, caseID); err != nil {
			return false, false, fmt.Errorf("reports: clear approval operation: %w", err)
		}
		if row.createdAt+caseRetentionSeconds <= now {
			var held int
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM legal_holds
WHERE object_kind='report_case' AND object_ref=? AND state='active')`, caseID).Scan(&held); err != nil {
				return false, false, fmt.Errorf("reports: inspect report legal hold: %w", err)
			}
			if held == 0 {
				if _, err := tx.ExecContext(ctx, `DELETE FROM report_cases WHERE id=? AND status='approved'`, caseID); err != nil {
					return false, false, fmt.Errorf("reports: delete completed retained approval: %w", err)
				}
			}
		}
	}
	if err := commit(tx, &committed); err != nil {
		return false, false, err
	}
	return true, more, nil
}
