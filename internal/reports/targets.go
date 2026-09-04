package reports

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"unicode/utf8"

	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/db"
)

type endpointKeySnapshot struct {
	keyID        int64
	ownerUserID  int64
	ownerDiscord sql.NullString
	displayName  string
	connector    string
	baseURL      string
	displayHead  string
	displayTail  string
	fingerprint  [32]byte
}

type targetCaseState struct {
	id                   string
	status               string
	targetVersion        int64
	targetCount          int64
	distinctOwnerCount   int64
	processedTargetCount int64
}

// donationTombstoneSnapshot is the report-owned projection of a terminal
// donation-key row. It carries no secret material: only the immutable source
// identity and the safe display facts copied when the donation key was made.
type donationTombstoneSnapshot struct {
	donationKeyID int64
	sourceKeyID   int64
	endedAt       int64
	ownerUserID   sql.NullInt64
	ownerDiscord  sql.NullString
	displayName   sql.NullString
	connector     string
	baseURL       string
	displayHead   string
	displayTail   string
}

func (repository *Repository) validateDonationTombstoneSnapshot(snapshot donationTombstoneSnapshot) error {
	if snapshot.donationKeyID <= 0 || snapshot.sourceKeyID <= 0 || snapshot.endedAt < 0 ||
		snapshot.endedAt > maxUnixSecond || len(snapshot.displayHead) > 16 || len(snapshot.displayTail) > 16 ||
		!utf8.ValidString(snapshot.displayHead) || !utf8.ValidString(snapshot.displayTail) {
		return ErrInvariant
	}
	for _, value := range snapshot.displayHead + snapshot.displayTail {
		if value < 0x20 || value > 0x7e {
			return ErrInvariant
		}
	}
	if snapshot.ownerUserID.Valid {
		if snapshot.ownerUserID.Int64 <= 0 || !snapshot.ownerDiscord.Valid || snapshot.ownerDiscord.String == "" ||
			!snapshot.displayName.Valid || !utf8.ValidString(snapshot.displayName.String) ||
			utf8.RuneCountInString(snapshot.displayName.String) > 128 {
			return ErrInvariant
		}
	} else if snapshot.ownerDiscord.Valid || snapshot.displayName.Valid {
		return ErrInvariant
	}
	return repository.validateEndpointKeySnapshot(endpointKeySnapshot{
		keyID: snapshot.sourceKeyID, ownerUserID: 1,
		connector: snapshot.connector, baseURL: snapshot.baseURL,
		displayHead: snapshot.displayHead, displayTail: snapshot.displayTail,
	})
}

// writeIndexingCursorTx advances the two-phase indexing cursor inside the
// caller's transaction. Phase 'endpoint' stores the last live endpoint-key id;
// phase 'donation' stores the last donation-key id, with id zero as the
// reserved phase-switch marker.
func writeIndexingCursorTx(
	ctx context.Context,
	tx *sql.Tx,
	caseID string,
	caseStatus string,
	phase string,
	lastID int64,
) error {
	if ctx == nil || tx == nil || !db.ValidateOpaqueID(caseID, "rpc_") ||
		(phase != indexPhaseEndpoint && phase != indexPhaseDonation) || lastID < 0 ||
		(phase == indexPhaseEndpoint && lastID == 0) {
		return ErrInvariant
	}
	result, err := tx.ExecContext(ctx, `UPDATE report_cases SET cursor_source=?,cursor_id=?,retry_attempt_count=0,
next_retry_at=NULL,last_error_class=NULL WHERE id=? AND status=? AND progress_state='in_progress'`,
		phase, lastID, caseID, caseStatus)
	if err != nil {
		return fmt.Errorf("reports: advance indexing cursor: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil || changed != 1 {
		return ErrConflict
	}
	return nil
}

// ProtectNewEndpointKey is the C1 creation-transaction seam. It never starts,
// authorizes, commits, or rolls back the supplied transaction.
func (repository *Repository) ProtectNewEndpointKey(
	ctx context.Context,
	tx *sql.Tx,
	ownerUserID int64,
	endpointKeyID int64,
	decisionNow int64,
) error {
	if err := repository.admit(); err != nil {
		return err
	}
	defer repository.release()
	if ctx == nil || tx == nil || ownerUserID <= 0 || endpointKeyID <= 0 || decisionNow < 0 || decisionNow > maxUnixSecond {
		return ErrInvalidRequest
	}
	snapshot, err := readEndpointKeySnapshotTx(ctx, tx, ownerUserID, endpointKeyID)
	if err != nil {
		return err
	}
	caseRow, found, err := findActiveCaseTx(ctx, tx, snapshot.fingerprint)
	if err != nil || !found {
		return err
	}
	if caseRow.connector != snapshot.connector || caseRow.baseURL != snapshot.baseURL {
		return ErrInvariant
	}
	if caseRow.status != "approved_processing" && caseRow.deadline <= decisionNow {
		expired, err := repository.expireActiveCaseTx(
			ctx, tx, caseRow.id, caseRow.status, snapshot.fingerprint[:], caseRow.materialVersion,
			caseRow.targetVersion, caseRow.deadline, decisionNow,
		)
		if err != nil {
			return err
		}
		if !expired {
			return ErrConflict
		}
		return nil
	}
	_, err = repository.captureEndpointKeyTx(ctx, tx, caseRow.id, snapshot, decisionNow)
	return err
}

func readEndpointKeySnapshotTx(
	ctx context.Context,
	tx *sql.Tx,
	ownerUserID int64,
	endpointKeyID int64,
) (endpointKeySnapshot, error) {
	var snapshot endpointKeySnapshot
	var fingerprint []byte
	err := tx.QueryRowContext(ctx, `
SELECT k.id,e.user_id,u.discord_id,
 CASE WHEN u.guild_nick<>'' THEN u.guild_nick ELSE u.username END,
 e.connector_type,e.base_url,k.display_head,k.display_tail,k.secret_fingerprint
FROM endpoint_keys k
JOIN endpoints e ON e.id=k.endpoint_id
JOIN users u ON u.id=e.user_id
WHERE k.id=? AND e.user_id=?`, endpointKeyID, ownerUserID).Scan(
		&snapshot.keyID, &snapshot.ownerUserID, &snapshot.ownerDiscord, &snapshot.displayName,
		&snapshot.connector, &snapshot.baseURL, &snapshot.displayHead, &snapshot.displayTail, &fingerprint)
	if errors.Is(err, sql.ErrNoRows) {
		return endpointKeySnapshot{}, ErrNotFound
	}
	if err != nil {
		return endpointKeySnapshot{}, fmt.Errorf("reports: read endpoint key snapshot: %w", err)
	}
	if len(fingerprint) != len(snapshot.fingerprint) || snapshot.keyID <= 0 || snapshot.ownerUserID <= 0 ||
		!utf8.ValidString(snapshot.displayName) || utf8.RuneCountInString(snapshot.displayName) > 128 ||
		len(snapshot.displayHead) > 16 || len(snapshot.displayTail) > 16 {
		return endpointKeySnapshot{}, ErrInvariant
	}
	copy(snapshot.fingerprint[:], fingerprint)
	clear(fingerprint)
	return snapshot, nil
}

func (repository *Repository) validateEndpointKeySnapshot(snapshot endpointKeySnapshot) error {
	validatedConnector, err := repository.connectors.MustValidate(connectorcontract.Type(snapshot.connector))
	if err != nil || string(validatedConnector) != snapshot.connector || snapshot.baseURL == "" || len(snapshot.baseURL) > maxBaseURLBytes {
		return ErrInvariant
	}
	canonical, err := repository.baseURLs.ValidateBaseURL(snapshot.baseURL)
	if err != nil || canonical != snapshot.baseURL {
		return ErrInvariant
	}
	return nil
}

// captureDonationTombstoneTx records one terminal report target for a
// donation-key tombstone. The physical endpoint key is already gone, so the
// target is inserted directly into 'released' with endpoint_key_id=NULL; no
// suspension row is created and no deletable entity is fabricated.
func (repository *Repository) captureDonationTombstoneTx(
	ctx context.Context,
	tx *sql.Tx,
	caseID string,
	caseStatus string,
	snapshot donationTombstoneSnapshot,
	now int64,
) (bool, error) {
	if ctx == nil || tx == nil || !db.ValidateOpaqueID(caseID, "rpc_") ||
		snapshot.sourceKeyID <= 0 || snapshot.donationKeyID <= 0 || now < 0 || now > maxUnixSecond {
		return false, ErrInvalidRequest
	}
	if err := repository.validateDonationTombstoneSnapshot(snapshot); err != nil {
		return false, err
	}
	var caseState targetCaseState
	if caseStatus == "expired" {
		err := tx.QueryRowContext(ctx, `SELECT id,status,target_version,target_count,distinct_owner_count,processed_target_count
FROM report_cases WHERE id=? AND status='expired' AND progress_state='in_progress'`, caseID).Scan(
			&caseState.id, &caseState.status, &caseState.targetVersion, &caseState.targetCount,
			&caseState.distinctOwnerCount, &caseState.processedTargetCount)
		if errors.Is(err, sql.ErrNoRows) {
			return false, ErrConflict
		}
		if err != nil {
			return false, fmt.Errorf("reports: read expired tombstone case: %w", err)
		}
	} else {
		state, err := readTargetCaseStateTx(ctx, tx, caseID)
		if err != nil {
			return false, err
		}
		caseState = state
	}
	keyRef, err := repository.keys.targetDigest(caseID, snapshot.sourceKeyID)
	if err != nil {
		return false, err
	}
	var existing int
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(
SELECT 1 FROM report_targets WHERE case_id=? AND key_ref=?)`, caseID, keyRef[:]).Scan(&existing); err != nil {
		return false, fmt.Errorf("reports: inspect tombstone duplicate: %w", err)
	}
	if existing == 1 {
		return false, nil
	}
	if caseState.targetVersion == math.MaxInt64 || caseState.targetCount == math.MaxInt64 ||
		caseState.distinctOwnerCount == math.MaxInt64 || caseState.processedTargetCount == math.MaxInt64 {
		return false, ErrInvariant
	}
	targetID, err := repository.generateID("rpt_")
	if err != nil || !db.ValidateOpaqueID(targetID, "rpt_") {
		return false, ErrUnavailable
	}
	var ownerAlreadySeen int
	if snapshot.ownerUserID.Valid {
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(
SELECT 1 FROM report_targets WHERE case_id=? AND owner_user_id=?)`, caseID, snapshot.ownerUserID.Int64).Scan(&ownerAlreadySeen); err != nil {
			return false, fmt.Errorf("reports: inspect tombstone owner: %w", err)
		}
	}
	newVersion := caseState.targetVersion + 1
	newSequence := caseState.targetCount + 1
	var ownerUser any
	var ownerDiscord any
	var displayName any
	if snapshot.ownerUserID.Valid {
		ownerUser = snapshot.ownerUserID.Int64
	}
	if snapshot.ownerDiscord.Valid {
		ownerDiscord = snapshot.ownerDiscord.String
	}
	if snapshot.displayName.Valid {
		displayName = snapshot.displayName.String
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO report_targets(
 id,case_id,target_seq,endpoint_key_id,source_endpoint_key_id,key_ref,owner_user_id,owner_discord_id,owner_display_name,
 connector_type,canonical_base_url,key_display_head,key_display_tail,state,discovered_version,decided_version,created_at,updated_at)
VALUES(?,?,?,NULL,?,?,?,?,?,?,?,?,?,'released',?,?,?,?)`,
		targetID, caseID, newSequence, snapshot.sourceKeyID, keyRef[:], ownerUser, ownerDiscord, displayName,
		snapshot.connector, snapshot.baseURL, snapshot.displayHead, snapshot.displayTail,
		newVersion, newVersion, now, now); err != nil {
		return false, fmt.Errorf("reports: insert tombstone target: %w", err)
	}
	distinctIncrement := int64(0)
	if snapshot.ownerUserID.Valid && ownerAlreadySeen == 0 {
		distinctIncrement = 1
	}
	result, err := tx.ExecContext(ctx, `UPDATE report_cases SET
 target_version=?,target_count=target_count+1,distinct_owner_count=distinct_owner_count+?,
 processed_target_count=processed_target_count+1,released_target_count=released_target_count+1
WHERE id=? AND status=? AND target_version=? AND target_count=?`,
		newVersion, distinctIncrement, caseID, caseStatus, caseState.targetVersion, caseState.targetCount)
	if err != nil {
		return false, fmt.Errorf("reports: advance tombstone target version: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil || changed != 1 {
		return false, ErrConflict
	}
	return true, nil
}

func readTargetCaseStateTx(ctx context.Context, tx *sql.Tx, caseID string) (targetCaseState, error) {
	var state targetCaseState
	err := tx.QueryRowContext(ctx, `SELECT id,status,target_version,target_count,distinct_owner_count,processed_target_count
FROM report_cases WHERE id=? AND status IN ('pending_indexing','pending_review','approved_processing')`, caseID).Scan(
		&state.id, &state.status, &state.targetVersion, &state.targetCount, &state.distinctOwnerCount, &state.processedTargetCount)
	if errors.Is(err, sql.ErrNoRows) {
		return targetCaseState{}, ErrConflict
	}
	if err != nil {
		return targetCaseState{}, fmt.Errorf("reports: read target case state: %w", err)
	}
	if !db.ValidateOpaqueID(state.id, "rpc_") || state.targetVersion < 1 || state.targetCount < 0 ||
		state.distinctOwnerCount < 0 || state.processedTargetCount < 0 || state.processedTargetCount > state.targetCount {
		return targetCaseState{}, ErrInvariant
	}
	return state, nil
}

func (repository *Repository) captureEndpointKeyTx(
	ctx context.Context,
	tx *sql.Tx,
	caseID string,
	snapshot endpointKeySnapshot,
	now int64,
) (bool, error) {
	if ctx == nil || tx == nil || !db.ValidateOpaqueID(caseID, "rpc_") || snapshot.keyID <= 0 ||
		snapshot.ownerUserID <= 0 || now < 0 || now > maxUnixSecond {
		return false, ErrInvalidRequest
	}
	if err := repository.validateEndpointKeySnapshot(snapshot); err != nil {
		return false, err
	}
	caseState, err := readTargetCaseStateTx(ctx, tx, caseID)
	if err != nil {
		return false, err
	}
	issueTransitions := make(reportIssueTransitions)
	if err := snapshotReportIssueTargetsTx(ctx, tx, issueTransitions, []reportIssueTarget{{
		ownerUserID: snapshot.ownerUserID, endpointKeyID: snapshot.keyID,
	}}); err != nil {
		return false, err
	}
	keyRef, err := repository.keys.targetDigest(caseID, snapshot.keyID)
	if err != nil {
		return false, err
	}
	var existing int
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(
SELECT 1 FROM report_targets WHERE case_id=? AND key_ref=?)`, caseID, keyRef[:]).Scan(&existing); err != nil {
		return false, fmt.Errorf("reports: inspect target duplicate: %w", err)
	}
	if existing == 1 {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO endpoint_key_suspensions(
endpoint_key_id,reason_type,report_case_id,created_at) VALUES(?,'report_case',?,?)`, snapshot.keyID, caseID, now); err != nil {
			return false, fmt.Errorf("reports: restore target suspension: %w", err)
		}
		if err := repository.reconcileReportIssueTransitionsTx(ctx, tx, issueTransitions); err != nil {
			return false, err
		}
		return false, nil
	}
	if caseState.targetVersion == math.MaxInt64 || caseState.targetCount == math.MaxInt64 ||
		caseState.distinctOwnerCount == math.MaxInt64 || caseState.processedTargetCount == math.MaxInt64 {
		return false, ErrInvariant
	}
	targetID, err := repository.generateID("rpt_")
	if err != nil || !db.ValidateOpaqueID(targetID, "rpt_") {
		return false, ErrUnavailable
	}
	var ownerAlreadySeen int
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(
SELECT 1 FROM report_targets WHERE case_id=? AND owner_user_id=?)`, caseID, snapshot.ownerUserID).Scan(&ownerAlreadySeen); err != nil {
		return false, fmt.Errorf("reports: inspect target owner: %w", err)
	}
	newVersion := caseState.targetVersion + 1
	newSequence := caseState.targetCount + 1
	var ownerDiscord any
	if snapshot.ownerDiscord.Valid {
		ownerDiscord = snapshot.ownerDiscord.String
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO report_targets(
 id,case_id,target_seq,endpoint_key_id,source_endpoint_key_id,key_ref,owner_user_id,owner_discord_id,owner_display_name,
 connector_type,canonical_base_url,key_display_head,key_display_tail,state,discovered_version,decided_version,created_at,updated_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,'protected',?,NULL,?,?)`,
		targetID, caseID, newSequence, snapshot.keyID, snapshot.keyID, keyRef[:], snapshot.ownerUserID, ownerDiscord, snapshot.displayName,
		snapshot.connector, snapshot.baseURL, snapshot.displayHead, snapshot.displayTail, newVersion, now, now); err != nil {
		return false, fmt.Errorf("reports: insert target: %w", err)
	}
	processedIncrement := int64(1)
	if caseState.status == "approved_processing" {
		processedIncrement = 0
	}
	distinctIncrement := int64(0)
	if ownerAlreadySeen == 0 {
		distinctIncrement = 1
	}
	result, err := tx.ExecContext(ctx, `UPDATE report_cases SET
 target_version=?,target_count=target_count+1,distinct_owner_count=distinct_owner_count+?,
 processed_target_count=processed_target_count+?
WHERE id=? AND status=? AND target_version=? AND target_count=?`,
		newVersion, distinctIncrement, processedIncrement, caseID, caseState.status, caseState.targetVersion, caseState.targetCount)
	if err != nil {
		return false, fmt.Errorf("reports: advance target version: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil || changed != 1 {
		return false, ErrConflict
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO endpoint_key_suspensions(
endpoint_key_id,reason_type,report_case_id,created_at) VALUES(?,'report_case',?,?)`, snapshot.keyID, caseID, now); err != nil {
		return false, fmt.Errorf("reports: apply target suspension: %w", err)
	}
	if err := repository.reconcileReportIssueTransitionsTx(ctx, tx, issueTransitions); err != nil {
		return false, err
	}
	return true, nil
}

func (repository *Repository) captureReleasedEndpointKeyTx(
	ctx context.Context,
	tx *sql.Tx,
	caseID string,
	snapshot endpointKeySnapshot,
	now int64,
) (bool, error) {
	if ctx == nil || tx == nil || !db.ValidateOpaqueID(caseID, "rpc_") || snapshot.keyID <= 0 ||
		snapshot.ownerUserID <= 0 || now < 0 || now > maxUnixSecond {
		return false, ErrInvalidRequest
	}
	if err := repository.validateEndpointKeySnapshot(snapshot); err != nil {
		return false, err
	}
	var caseState targetCaseState
	err := tx.QueryRowContext(ctx, `SELECT id,status,target_version,target_count,distinct_owner_count,processed_target_count
FROM report_cases WHERE id=? AND status='expired' AND progress_state='in_progress'`, caseID).Scan(
		&caseState.id, &caseState.status, &caseState.targetVersion, &caseState.targetCount,
		&caseState.distinctOwnerCount, &caseState.processedTargetCount)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrConflict
	}
	if err != nil {
		return false, fmt.Errorf("reports: read expired target case: %w", err)
	}
	keyRef, err := repository.keys.targetDigest(caseID, snapshot.keyID)
	if err != nil {
		return false, err
	}
	var existing int
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(
SELECT 1 FROM report_targets WHERE case_id=? AND key_ref=?)`, caseID, keyRef[:]).Scan(&existing); err != nil {
		return false, fmt.Errorf("reports: inspect expired target duplicate: %w", err)
	}
	if existing == 1 {
		return false, nil
	}
	if caseState.targetVersion == math.MaxInt64 || caseState.targetCount == math.MaxInt64 ||
		caseState.distinctOwnerCount == math.MaxInt64 || caseState.processedTargetCount == math.MaxInt64 {
		return false, ErrInvariant
	}
	targetID, err := repository.generateID("rpt_")
	if err != nil || !db.ValidateOpaqueID(targetID, "rpt_") {
		return false, ErrUnavailable
	}
	var ownerAlreadySeen int
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(
SELECT 1 FROM report_targets WHERE case_id=? AND owner_user_id=?)`, caseID, snapshot.ownerUserID).Scan(&ownerAlreadySeen); err != nil {
		return false, fmt.Errorf("reports: inspect expired target owner: %w", err)
	}
	newVersion := caseState.targetVersion + 1
	newSequence := caseState.targetCount + 1
	var ownerDiscord any
	if snapshot.ownerDiscord.Valid {
		ownerDiscord = snapshot.ownerDiscord.String
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO report_targets(
 id,case_id,target_seq,endpoint_key_id,source_endpoint_key_id,key_ref,owner_user_id,owner_discord_id,owner_display_name,
 connector_type,canonical_base_url,key_display_head,key_display_tail,state,discovered_version,decided_version,created_at,updated_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,'released',?,?,?,?)`,
		targetID, caseID, newSequence, snapshot.keyID, snapshot.keyID, keyRef[:], snapshot.ownerUserID, ownerDiscord,
		snapshot.displayName, snapshot.connector, snapshot.baseURL, snapshot.displayHead, snapshot.displayTail,
		newVersion, newVersion, now, now); err != nil {
		return false, fmt.Errorf("reports: insert expired target: %w", err)
	}
	distinctIncrement := int64(0)
	if ownerAlreadySeen == 0 {
		distinctIncrement = 1
	}
	result, err := tx.ExecContext(ctx, `UPDATE report_cases SET
 target_version=?,target_count=target_count+1,distinct_owner_count=distinct_owner_count+?,
 processed_target_count=processed_target_count+1,released_target_count=released_target_count+1
WHERE id=? AND status='expired' AND progress_state='in_progress' AND target_version=? AND target_count=?`,
		newVersion, distinctIncrement, caseID, caseState.targetVersion, caseState.targetCount)
	if err != nil {
		return false, fmt.Errorf("reports: advance expired target version: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil || changed != 1 {
		return false, ErrConflict
	}
	return true, nil
}
