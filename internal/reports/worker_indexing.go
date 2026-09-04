package reports

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

const (
	indexPhaseEndpoint = "endpoint"
	indexPhaseDonation = "donation"
)

type indexingCase struct {
	id          string
	status      string
	progress    string
	fingerprint [32]byte
	connector   string
	baseURL     string
	deadline    sql.NullInt64
	createdAt   int64
	cursorPhase string
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
	var cursorSource sql.NullString
	var cursorID sql.NullInt64
	err := tx.QueryRowContext(ctx, `SELECT id,status,progress_state,fingerprint,connector_type,
canonical_base_url,deadline,created_at,cursor_source,cursor_id FROM report_cases WHERE id=?`, caseID).Scan(
		&row.id, &row.status, &row.progress, &fingerprint, &row.connector,
		&row.baseURL, &row.deadline, &row.createdAt, &cursorSource, &cursorID)
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
	if !cursorSource.Valid || cursorSource.String == "" {
		if cursorID.Valid {
			return indexingCase{}, ErrInvariant
		}
		row.cursorPhase = indexPhaseEndpoint
		row.cursor = 0
		return row, nil
	}
	if !cursorID.Valid || cursorID.Int64 < 0 {
		return indexingCase{}, ErrInvariant
	}
	switch cursorSource.String {
	case indexPhaseEndpoint, indexPhaseDonation:
		row.cursorPhase = cursorSource.String
	default:
		return indexingCase{}, ErrInvariant
	}
	row.cursor = cursorID.Int64
	return row, nil
}

// readEndpointSnapshotsTx enumerates live endpoint keys whose secret
// fingerprint matches the case, resuming strictly after the stored cursor id.
func (repository *Repository) readEndpointSnapshotsTx(ctx context.Context, tx *sql.Tx, row indexingCase) ([]endpointKeySnapshot, bool, error) {
	if row.cursorPhase != indexPhaseEndpoint {
		return nil, false, nil
	}
	query := `SELECT k.id,e.user_id,u.discord_id,
CASE WHEN u.guild_nick<>'' THEN u.guild_nick ELSE u.username END,
e.connector_type,e.base_url,k.display_head,k.display_tail,k.secret_fingerprint
FROM endpoint_keys k JOIN endpoints e ON e.id=k.endpoint_id JOIN users u ON u.id=e.user_id
WHERE k.id>? AND k.secret_fingerprint=? AND e.connector_type=? AND e.base_url=?`
	args := []any{row.cursor, row.fingerprint[:], row.connector, row.baseURL}
	if row.status == "expired" {
		if !row.deadline.Valid {
			return nil, false, ErrInvariant
		}
		query += ` AND k.created_at<?`
		args = append(args, row.deadline.Int64)
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

// readDonationTombstoneSnapshotsTx enumerates donation-key rows whose physical
// endpoint key is gone but whose report fingerprint is still inside its
// match window. Only donations still inside ordinary 400-day retention are
// surfaced; fully expired aggregates are unreachable by design.
func (repository *Repository) readDonationTombstoneSnapshotsTx(ctx context.Context, tx *sql.Tx, row indexingCase, now int64) ([]donationTombstoneSnapshot, bool, error) {
	if row.cursorPhase != indexPhaseDonation {
		return nil, false, nil
	}
	query := `WITH candidates AS (
SELECT dk.id,dk.source_endpoint_key_id,dk.ended_at,d.user_id,u.discord_id,
CASE WHEN u.guild_nick<>'' THEN u.guild_nick ELSE u.username END AS owner_display_name,
dk.connector_type,dk.canonical_base_url,dk.display_head,dk.display_tail,
ROW_NUMBER() OVER (
 PARTITION BY dk.source_endpoint_key_id
 ORDER BY dk.ended_at DESC,dk.id DESC
) AS snapshot_rank
FROM donation_keys dk
JOIN donations d ON d.id=dk.donation_id
LEFT JOIN users u ON u.id=d.user_id
WHERE dk.report_fingerprint=? AND dk.connector_type=? AND dk.canonical_base_url=?
AND dk.ended_at IS NOT NULL AND dk.report_match_until>?
AND (d.status IN ('pending','approved') OR
     (d.status IN ('rejected','deleted','expired') AND d.terminal_at IS NOT NULL AND d.terminal_at>?))`
	args := []any{row.fingerprint[:], row.connector, row.baseURL, now, now - donationRetentionSeconds}
	if row.status == "expired" {
		if !row.deadline.Valid {
			return nil, false, ErrInvariant
		}
		query += ` AND dk.created_at<?`
		args = append(args, row.deadline.Int64)
	}
	query += `)
SELECT id,source_endpoint_key_id,ended_at,user_id,discord_id,owner_display_name,
connector_type,canonical_base_url,display_head,display_tail
FROM candidates WHERE snapshot_rank=1 AND id>? ORDER BY id LIMIT ?`
	args = append(args, row.cursor, workerBatchLimit+1)
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, false, fmt.Errorf("reports: enumerate donation tombstones: %w", err)
	}
	defer rows.Close()
	snapshots := make([]donationTombstoneSnapshot, 0, workerBatchLimit+1)
	for rows.Next() {
		var snapshot donationTombstoneSnapshot
		if err := rows.Scan(&snapshot.donationKeyID, &snapshot.sourceKeyID, &snapshot.endedAt,
			&snapshot.ownerUserID, &snapshot.ownerDiscord,
			&snapshot.displayName, &snapshot.connector, &snapshot.baseURL,
			&snapshot.displayHead, &snapshot.displayTail); err != nil {
			return nil, false, fmt.Errorf("reports: scan donation tombstone: %w", err)
		}
		snapshots = append(snapshots, snapshot)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("reports: iterate donation tombstones: %w", err)
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
		if row.progress != "in_progress" || !row.deadline.Valid || row.deadline.Int64 <= now {
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
	// Phase one scans live endpoint keys. Both phases are restart-safe: the
	// UNIQUE(case_id,key_ref) contract plus the strictly advancing cursor
	// makes re-running any pass idempotent.
	if row.cursorPhase == indexPhaseEndpoint {
		snapshots, endpointMore, err := repository.readEndpointSnapshotsTx(ctx, tx, row)
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
		if endpointMore {
			if err := writeIndexingCursorTx(ctx, tx, caseID, row.status, indexPhaseEndpoint, lastCursor); err != nil {
				return false, false, err
			}
			if err := commit(tx, &committed); err != nil {
				return false, false, err
			}
			return true, true, nil
		}
		// The phase marker is a committed checkpoint, not an in-memory
		// fallthrough. A crash after this commit therefore resumes donation
		// scanning from its own ID domain at the reserved zero cursor.
		if err := writeIndexingCursorTx(ctx, tx, caseID, row.status, indexPhaseDonation, 0); err != nil {
			return false, false, err
		}
		if err := commit(tx, &committed); err != nil {
			return false, false, err
		}
		return true, true, nil
	}
	// Phase two scans donation-key tombstones. The physical key is already
	// gone, so a tombstone target is terminal by construction and is inserted
	// directly into 'released'.
	tombstones, donationMore, err := repository.readDonationTombstoneSnapshotsTx(ctx, tx, row, now)
	if err != nil {
		return false, false, err
	}
	lastTombstone := row.cursor
	for _, snapshot := range tombstones {
		if snapshot.donationKeyID <= lastTombstone || snapshot.connector != row.connector || snapshot.baseURL != row.baseURL {
			return false, false, ErrInvariant
		}
		if _, err := repository.captureDonationTombstoneTx(ctx, tx, caseID, row.status, snapshot, now); err != nil {
			return false, false, err
		}
		lastTombstone = snapshot.donationKeyID
	}
	if donationMore {
		if err := writeIndexingCursorTx(ctx, tx, caseID, row.status, indexPhaseDonation, lastTombstone); err != nil {
			return false, false, err
		}
		if err := commit(tx, &committed); err != nil {
			return false, false, err
		}
		return true, true, nil
	}
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
status='pending_review',progress_state='complete',cursor_source=NULL,cursor_id=NULL,retry_attempt_count=0,
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
progress_state='complete',cursor_source=NULL,cursor_id=NULL,retry_attempt_count=0,next_retry_at=NULL,last_error_class=NULL
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
	if err := commit(tx, &committed); err != nil {
		return false, false, err
	}
	return true, false, nil
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
