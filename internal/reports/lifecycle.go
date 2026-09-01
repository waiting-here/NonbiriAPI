package reports

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

type pendingCapture struct {
	caseRow      activeCase
	snapshot     endpointKeySnapshot
	keyCreatedAt int64
}

// DetachUserForDeletion prepares report facts inside the account-deletion
// transaction. It captures any not-yet-projected matching keys, removes live
// account identity, and records protected targets as deleted by account. It
// never authorizes, commits, deletes the user, or closes a report case.
func (repository *Repository) DetachUserForDeletion(
	ctx context.Context,
	tx *sql.Tx,
	userID int64,
	decisionNow int64,
) error {
	if err := repository.admit(); err != nil {
		return err
	}
	defer repository.release()
	if ctx == nil || tx == nil || userID <= 0 || decisionNow < 0 || decisionNow > maxUnixSecond {
		return ErrInvalidRequest
	}
	captures, err := readUserPendingCapturesTx(ctx, tx, userID)
	if err != nil {
		return err
	}
	expired := make(map[string]struct{})
	for _, capture := range captures {
		if capture.caseRow.status != "approved_processing" && capture.caseRow.deadline <= decisionNow {
			if _, alreadyExpired := expired[capture.caseRow.id]; !alreadyExpired {
				didExpire, err := repository.expireActiveCaseTx(
					ctx, tx, capture.caseRow.id, capture.caseRow.status, capture.snapshot.fingerprint[:],
					capture.caseRow.materialVersion, capture.caseRow.targetVersion, capture.caseRow.deadline, decisionNow,
				)
				if err != nil {
					return err
				}
				if !didExpire {
					return ErrConflict
				}
				expired[capture.caseRow.id] = struct{}{}
			}
			if capture.caseRow.status == "pending_indexing" && capture.keyCreatedAt < capture.caseRow.deadline {
				if _, err := repository.captureReleasedEndpointKeyTx(
					ctx, tx, capture.caseRow.id, capture.snapshot, decisionNow,
				); err != nil {
					return err
				}
			}
			continue
		}
		if _, err := repository.captureEndpointKeyTx(ctx, tx, capture.caseRow.id, capture.snapshot, decisionNow); err != nil {
			return err
		}
	}
	if err := repository.detachReporterIdentityTx(ctx, tx, userID); err != nil {
		return err
	}
	if err := repository.detachProtectedTargetsTx(ctx, tx, userID, decisionNow); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE report_targets SET
 endpoint_key_id=NULL,owner_user_id=NULL,owner_discord_id=NULL,owner_display_name=NULL,updated_at=?
WHERE owner_user_id=?`, decisionNow, userID); err != nil {
		return fmt.Errorf("reports: detach target identity: %w", err)
	}
	rateHash, err := repository.keys.rateDigest("account", []byte(strconv.FormatInt(userID, 10)))
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM report_rate_buckets WHERE scope='account' AND scope_hash=?`, rateHash[:]); err != nil {
		return fmt.Errorf("reports: clear account rate scope: %w", err)
	}
	return nil
}

func readUserPendingCapturesTx(ctx context.Context, tx *sql.Tx, userID int64) ([]pendingCapture, error) {
	rows, err := tx.QueryContext(ctx, `
SELECT c.id,c.connector_type,c.canonical_base_url,c.status,c.material_version,c.target_version,c.created_at,c.deadline,
 k.id,e.user_id,u.discord_id,
 CASE WHEN u.guild_nick<>'' THEN u.guild_nick ELSE u.username END,
 e.connector_type,e.base_url,k.display_head,k.display_tail,k.secret_fingerprint,k.created_at
FROM endpoint_keys k
JOIN endpoints e ON e.id=k.endpoint_id
JOIN users u ON u.id=e.user_id
JOIN report_cases c ON c.fingerprint=k.secret_fingerprint
 AND c.connector_type=e.connector_type AND c.canonical_base_url=e.base_url
WHERE e.user_id=? AND c.status IN ('pending_indexing','pending_review','approved_processing')
ORDER BY c.id,k.id`, userID)
	if err != nil {
		return nil, fmt.Errorf("reports: enumerate account targets: %w", err)
	}
	defer rows.Close()
	var captures []pendingCapture
	for rows.Next() {
		var capture pendingCapture
		var fingerprint []byte
		if err := rows.Scan(
			&capture.caseRow.id, &capture.caseRow.connector, &capture.caseRow.baseURL, &capture.caseRow.status,
			&capture.caseRow.materialVersion, &capture.caseRow.targetVersion, &capture.caseRow.createdAt, &capture.caseRow.deadline,
			&capture.snapshot.keyID, &capture.snapshot.ownerUserID, &capture.snapshot.ownerDiscord,
			&capture.snapshot.displayName, &capture.snapshot.connector, &capture.snapshot.baseURL,
			&capture.snapshot.displayHead, &capture.snapshot.displayTail, &fingerprint, &capture.keyCreatedAt); err != nil {
			return nil, fmt.Errorf("reports: scan account target: %w", err)
		}
		if len(fingerprint) != len(capture.snapshot.fingerprint) ||
			!db.ValidateOpaqueID(capture.caseRow.id, "rpc_") || capture.caseRow.materialVersion < 1 ||
			capture.caseRow.targetVersion < 1 || capture.caseRow.deadline < 0 || capture.keyCreatedAt < 0 ||
			capture.caseRow.connector != capture.snapshot.connector || capture.caseRow.baseURL != capture.snapshot.baseURL {
			clear(fingerprint)
			return nil, ErrInvariant
		}
		copy(capture.snapshot.fingerprint[:], fingerprint)
		clear(fingerprint)
		captures = append(captures, capture)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reports: enumerate account targets: %w", err)
	}
	return captures, nil
}

func (repository *Repository) detachReporterIdentityTx(ctx context.Context, tx *sql.Tx, userID int64) error {
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT c.id,c.material_version
FROM report_cases c JOIN report_materials m ON m.case_id=c.id
WHERE m.reporter_user_id=? AND c.status IN ('pending_indexing','pending_review')
ORDER BY c.id`, userID)
	if err != nil {
		return fmt.Errorf("reports: enumerate reporter identity: %w", err)
	}
	type affectedCase struct {
		id      string
		version int64
	}
	var affected []affectedCase
	for rows.Next() {
		var item affectedCase
		if err := rows.Scan(&item.id, &item.version); err != nil {
			_ = rows.Close()
			return fmt.Errorf("reports: scan reporter identity: %w", err)
		}
		if item.version == math.MaxInt64 {
			_ = rows.Close()
			return ErrInvariant
		}
		affected = append(affected, item)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("reports: close reporter identity cursor: %w", err)
	}
	for _, item := range affected {
		result, err := tx.ExecContext(ctx, `UPDATE report_cases SET material_version=material_version+1
WHERE id=? AND material_version=? AND status IN ('pending_indexing','pending_review')`, item.id, item.version)
		if err != nil {
			return fmt.Errorf("reports: advance reporter material version: %w", err)
		}
		changed, _ := result.RowsAffected()
		if changed != 1 {
			return ErrConflict
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE report_materials
SET reporter_user_id=NULL,reporter_discord_id=NULL WHERE reporter_user_id=?`, userID); err != nil {
		return fmt.Errorf("reports: detach reporter identity: %w", err)
	}
	return nil
}

func (repository *Repository) detachProtectedTargetsTx(ctx context.Context, tx *sql.Tx, userID, now int64) error {
	rows, err := tx.QueryContext(ctx, `SELECT c.id,c.status,c.target_version,COUNT(t.id)
FROM report_cases c JOIN report_targets t ON t.case_id=c.id
WHERE t.owner_user_id=? AND t.state='protected'
 AND c.status IN ('pending_indexing','pending_review','approved_processing')
GROUP BY c.id,c.status,c.target_version ORDER BY c.id`, userID)
	if err != nil {
		return fmt.Errorf("reports: enumerate protected account targets: %w", err)
	}
	type affectedCase struct {
		id      string
		status  string
		version int64
		count   int64
	}
	var affected []affectedCase
	for rows.Next() {
		var item affectedCase
		if err := rows.Scan(&item.id, &item.status, &item.version, &item.count); err != nil {
			_ = rows.Close()
			return fmt.Errorf("reports: scan protected account targets: %w", err)
		}
		if item.version == math.MaxInt64 || item.count <= 0 {
			_ = rows.Close()
			return ErrInvariant
		}
		affected = append(affected, item)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("reports: close protected account targets: %w", err)
	}
	for _, item := range affected {
		newVersion := item.version + 1
		result, err := tx.ExecContext(ctx, `UPDATE report_targets SET
 state='deleted_by_account',endpoint_key_id=NULL,owner_user_id=NULL,owner_discord_id=NULL,owner_display_name=NULL,
 decided_version=?,updated_at=?
WHERE case_id=? AND owner_user_id=? AND state='protected'`, newVersion, now, item.id, userID)
		if err != nil {
			return fmt.Errorf("reports: mark account targets deleted: %w", err)
		}
		changed, _ := result.RowsAffected()
		if changed != item.count {
			return ErrConflict
		}
		processedIncrement := int64(0)
		if item.status == "approved_processing" {
			processedIncrement = item.count
		}
		result, err = tx.ExecContext(ctx, `UPDATE report_cases SET
 target_version=?,processed_target_count=processed_target_count+?,deleted_target_count=deleted_target_count+?
WHERE id=? AND status=? AND target_version=?`,
			newVersion, processedIncrement, item.count, item.id, item.status, item.version)
		if err != nil {
			return fmt.Errorf("reports: advance account target version: %w", err)
		}
		changed, _ = result.RowsAffected()
		if changed != 1 {
			return ErrConflict
		}
	}
	return nil
}

// LifecycleHeldCaseState is the report-owned legal-hold root projection. It
// exposes only existence, the original 90-day deadline, and the irreversible
// marker; report materials, targets, and decisions remain in this package.
type LifecycleHeldCaseState struct {
	Exists            bool
	OrdinaryDeadline  int64
	LegalHoldConsumed bool
}

func (repository *Repository) InspectLifecycleHeldCase(
	ctx context.Context,
	tx *sql.Tx,
	caseID string,
	decisionNow int64,
) (LifecycleHeldCaseState, error) {
	if err := repository.admit(); err != nil {
		return LifecycleHeldCaseState{}, err
	}
	defer repository.release()
	if ctx == nil || tx == nil || !db.ValidateOpaqueID(caseID, "rpc_") ||
		decisionNow < 0 || decisionNow > maxUnixSecond {
		return LifecycleHeldCaseState{}, ErrInvalidRequest
	}
	return readLifecycleHeldCaseState(ctx, tx, caseID)
}

func readLifecycleHeldCaseState(
	ctx context.Context,
	tx *sql.Tx,
	caseID string,
) (LifecycleHeldCaseState, error) {
	var status string
	var createdAt int64
	var marker int
	err := tx.QueryRowContext(ctx, `SELECT status,created_at,legal_hold_consumed
FROM report_cases WHERE id=?`, caseID).Scan(&status, &createdAt, &marker)
	if err == sql.ErrNoRows {
		return LifecycleHeldCaseState{}, nil
	}
	if err != nil {
		return LifecycleHeldCaseState{}, fmt.Errorf("reports: inspect lifecycle held case: %w", err)
	}
	if !validCaseStatus(status, false) || createdAt < 0 ||
		createdAt > maxUnixSecond-caseRetentionSeconds || (marker != 0 && marker != 1) {
		return LifecycleHeldCaseState{}, ErrInvariant
	}
	return LifecycleHeldCaseState{
		Exists: true, OrdinaryDeadline: createdAt + caseRetentionSeconds,
		LegalHoldConsumed: marker == 1,
	}, nil
}

func (repository *Repository) ConsumeLifecycleHeldCaseMarker(
	ctx context.Context,
	tx *sql.Tx,
	caseID string,
) error {
	if err := repository.admit(); err != nil {
		return err
	}
	defer repository.release()
	if ctx == nil || tx == nil || !db.ValidateOpaqueID(caseID, "rpc_") {
		return ErrInvalidRequest
	}
	result, err := tx.ExecContext(ctx, `UPDATE report_cases SET legal_hold_consumed=1
WHERE id=? AND legal_hold_consumed=0`, caseID)
	if err != nil {
		return fmt.Errorf("reports: consume lifecycle held case marker: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("reports: count lifecycle held case marker: %w", err)
	}
	if changed != 1 {
		return ErrConflict
	}
	return nil
}

// ReadLifecycleHeldCase confirms only the report aggregate root. Existing
// report admin detail paths remain responsible for projecting the case plus
// its materials, targets, and decisions; no rate, operation, or secret rows
// are widened by this seam.
func (repository *Repository) ReadLifecycleHeldCase(
	ctx context.Context,
	tx *sql.Tx,
	caseID string,
	decisionNow int64,
) (bool, error) {
	state, err := repository.InspectLifecycleHeldCase(ctx, tx, caseID, decisionNow)
	return state.Exists, err
}
