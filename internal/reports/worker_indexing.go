package reports

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

type indexingCase struct {
	id          string
	status      string
	progress    string
	fingerprint [32]byte
	connector   string
	baseURL     string
	deadline    int64
	createdAt   int64
	cursor      int64
}

func (repository *Repository) processIndexingCases(ctx context.Context, now int64, limit int) (int, bool, error) {
	ids, more, err := dueCaseIDs(ctx, repository.db, `SELECT id FROM report_cases
WHERE ((status='pending_indexing' AND deadline>?1) OR
       (status='expired' AND progress_state='in_progress' AND created_at+7776000>?1))
  AND (next_retry_at IS NULL OR next_retry_at<=?1)
ORDER BY COALESCE(next_retry_at,0),created_at,id LIMIT ?2`, now, limit)
	if err != nil {
		return 0, false, fmt.Errorf("reports: list indexing cases: %w", err)
	}
	processed := 0
	for _, caseID := range ids {
		if ctx.Err() != nil {
			return processed, true, context.Cause(ctx)
		}
		batchCtx, cancel := repository.boundedWorkerContext(ctx)
		worked, caseMore, workErr := repository.processIndexingCase(batchCtx, caseID, now)
		cancel()
		if workErr != nil {
			if ctx.Err() != nil {
				return processed, true, context.Cause(ctx)
			}
			if err := repository.recordCaseFailure(ctx, caseID, "report_indexing", classifyWorkerError(workErr), now); err != nil {
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

func readIndexingCaseTx(ctx context.Context, tx *sql.Tx, caseID string) (indexingCase, error) {
	var row indexingCase
	var fingerprint []byte
	var cursor sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT id,status,progress_state,fingerprint,connector_type,
canonical_base_url,deadline,created_at,cursor_text FROM report_cases WHERE id=?`, caseID).Scan(
		&row.id, &row.status, &row.progress, &fingerprint, &row.connector,
		&row.baseURL, &row.deadline, &row.createdAt, &cursor)
	if errors.Is(err, sql.ErrNoRows) {
		return indexingCase{}, ErrNotFound
	}
	if err != nil {
		return indexingCase{}, fmt.Errorf("reports: read indexing case: %w", err)
	}
	if len(fingerprint) != len(row.fingerprint) || !db.ValidateOpaqueID(row.id, "rpc_") {
		clear(fingerprint)
		return indexingCase{}, ErrInvariant
	}
	copy(row.fingerprint[:], fingerprint)
	clear(fingerprint)
	if cursor.Valid && cursor.String != "" {
		row.cursor, err = strconv.ParseInt(cursor.String, 10, 64)
		if err != nil || row.cursor < 0 || strconv.FormatInt(row.cursor, 10) != cursor.String {
			return indexingCase{}, ErrInvariant
		}
	}
	return row, nil
}

func readIndexingSnapshotsTx(ctx context.Context, tx *sql.Tx, row indexingCase) ([]endpointKeySnapshot, bool, error) {
	query := `SELECT k.id,e.user_id,u.discord_id,
CASE WHEN u.guild_nick<>'' THEN u.guild_nick ELSE u.username END,
e.connector_type,e.base_url,k.display_head,k.display_tail,k.secret_fingerprint
FROM endpoint_keys k JOIN endpoints e ON e.id=k.endpoint_id JOIN users u ON u.id=e.user_id
WHERE k.id>? AND k.secret_fingerprint=? AND e.connector_type=? AND e.base_url=?`
	args := []any{row.cursor, row.fingerprint[:], row.connector, row.baseURL}
	if row.status == "expired" {
		query += ` AND k.created_at<?`
		args = append(args, row.deadline)
	}
	query += ` ORDER BY k.id LIMIT ?`
	args = append(args, workerBatchLimit+1)
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, false, fmt.Errorf("reports: enumerate indexing targets: %w", err)
	}
	defer rows.Close()
	snapshots := make([]endpointKeySnapshot, 0, workerBatchLimit+1)
	for rows.Next() {
		var snapshot endpointKeySnapshot
		var fingerprint []byte
		if err := rows.Scan(&snapshot.keyID, &snapshot.ownerUserID, &snapshot.ownerDiscord, &snapshot.displayName,
			&snapshot.connector, &snapshot.baseURL, &snapshot.displayHead, &snapshot.displayTail, &fingerprint); err != nil {
			return nil, false, fmt.Errorf("reports: scan indexing target: %w", err)
		}
		if len(fingerprint) != 32 {
			clear(fingerprint)
			return nil, false, ErrInvariant
		}
		copy(snapshot.fingerprint[:], fingerprint)
		clear(fingerprint)
		snapshots = append(snapshots, snapshot)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("reports: enumerate indexing targets: %w", err)
	}
	more := len(snapshots) > workerBatchLimit
	if more {
		snapshots = snapshots[:workerBatchLimit]
	}
	return snapshots, more, nil
}

func (repository *Repository) processIndexingCase(ctx context.Context, caseID string, now int64) (bool, bool, error) {
	tx, err := repository.beginTx(ctx)
	if err != nil {
		return false, false, err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)
	row, err := readIndexingCaseTx(ctx, tx, caseID)
	if errors.Is(err, ErrNotFound) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	if row.status == "pending_indexing" {
		if row.progress != "in_progress" || row.deadline <= now {
			return false, false, nil
		}
	} else if row.status == "expired" {
		if row.progress != "in_progress" || row.createdAt+caseRetentionSeconds <= now {
			return false, false, nil
		}
	} else {
		return false, false, nil
	}
	if err := markReportOperationRunningTx(ctx, tx, "report_indexing", caseID); err != nil {
		return false, false, err
	}
	snapshots, more, err := readIndexingSnapshotsTx(ctx, tx, row)
	if err != nil {
		return false, false, err
	}
	lastCursor := row.cursor
	for _, snapshot := range snapshots {
		if snapshot.fingerprint != row.fingerprint || snapshot.connector != row.connector || snapshot.baseURL != row.baseURL {
			return false, false, ErrInvariant
		}
		if row.status == "pending_indexing" {
			if _, err := repository.captureEndpointKeyTx(ctx, tx, caseID, snapshot, now); err != nil {
				return false, false, err
			}
		} else {
			if _, err := repository.captureReleasedEndpointKeyTx(ctx, tx, caseID, snapshot, now); err != nil {
				return false, false, err
			}
		}
		lastCursor = snapshot.keyID
	}
	if more {
		result, err := tx.ExecContext(ctx, `UPDATE report_cases SET cursor_text=?,retry_attempt_count=0,
next_retry_at=NULL,last_error_class=NULL WHERE id=? AND status=? AND progress_state='in_progress'`,
			strconv.FormatInt(lastCursor, 10), caseID, row.status)
		if err != nil {
			return false, false, fmt.Errorf("reports: advance indexing cursor: %w", err)
		}
		changed, _ := result.RowsAffected()
		if changed != 1 {
			return false, false, ErrConflict
		}
	} else {
		var targetCount, processedCount int64
		if err := tx.QueryRowContext(ctx, `SELECT target_count,processed_target_count
FROM report_cases WHERE id=? AND status=?`, caseID, row.status).Scan(&targetCount, &processedCount); err != nil {
			return false, false, fmt.Errorf("reports: verify indexing counts: %w", err)
		}
		if targetCount != processedCount {
			return false, false, ErrInvariant
		}
		if row.status == "pending_indexing" {
			result, err := tx.ExecContext(ctx, `UPDATE report_cases SET
status='pending_review',progress_state='complete',cursor_text=NULL,retry_attempt_count=0,
next_retry_at=NULL,last_error_class=NULL
WHERE id=? AND status='pending_indexing' AND progress_state='in_progress'`, caseID)
			if err != nil {
				return false, false, fmt.Errorf("reports: complete pending indexing: %w", err)
			}
			changed, _ := result.RowsAffected()
			if changed != 1 {
				return false, false, ErrConflict
			}
		} else {
			result, err := tx.ExecContext(ctx, `UPDATE report_cases SET
progress_state='complete',cursor_text=NULL,retry_attempt_count=0,next_retry_at=NULL,last_error_class=NULL
WHERE id=? AND status='expired' AND progress_state='in_progress'`, caseID)
			if err != nil {
				return false, false, fmt.Errorf("reports: complete expired indexing: %w", err)
			}
			changed, _ := result.RowsAffected()
			if changed != 1 {
				return false, false, ErrConflict
			}
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM accepted_operations
WHERE kind='report_indexing' AND checkpoint=?`, caseID); err != nil {
			return false, false, fmt.Errorf("reports: clear indexing operation: %w", err)
		}
	}
	if err := commit(tx, &committed); err != nil {
		return false, false, err
	}
	return true, more, nil
}

func markReportOperationRunningTx(ctx context.Context, tx *sql.Tx, kind, caseID string) error {
	var operationID, state string
	err := tx.QueryRowContext(ctx, `SELECT id,state FROM accepted_operations
WHERE kind=? AND checkpoint=? AND state IN ('accepted','running','failed_retryable')
ORDER BY created_at DESC,id DESC LIMIT 1`, kind, caseID).Scan(&operationID, &state)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrInvariant
	}
	if err != nil {
		return fmt.Errorf("reports: read operation: %w", err)
	}
	switch state {
	case "running":
		return nil
	case "accepted", "failed_retryable":
		result, err := tx.ExecContext(ctx, `UPDATE accepted_operations SET
state='running',last_error_class=NULL,terminal_at=NULL WHERE id=? AND state=?`, operationID, state)
		if err != nil {
			return fmt.Errorf("reports: run operation: %w", err)
		}
		changed, _ := result.RowsAffected()
		if changed != 1 {
			return ErrConflict
		}
		return nil
	default:
		return ErrInvariant
	}
}
