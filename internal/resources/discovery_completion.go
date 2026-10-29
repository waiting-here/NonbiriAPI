package resources

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"github.com/waiting-here/NonbiriAPI/internal/managementlog"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/connectionstore"
	"github.com/waiting-here/NonbiriAPI/internal/db"
)

type discoveryRunState struct {
	id, generation, sourceRevision, approvedRevision      int64
	sourceRevisionID, versionID, sourceID, ownerID, useID sql.NullInt64
	purpose, state, operationID                           string
	hash                                                  []byte
	currentRunID                                          sql.NullInt64
}

func loadDiscoveryRunTx(ctx context.Context, tx *sql.Tx, operationID string) (discoveryRunState, error) {
	var run discoveryRunState
	err := tx.QueryRowContext(ctx, `SELECT run.id,run.result_generation,run.source_revision_snapshot,COALESCE(run.authorization_use_revision_id,0),run.source_revision_id,run.credential_version_id,sr.source_id,sr.owner_user_id,run.authorization_use_id,run.authorization_kind,run.state,run.operation_id,run.operation_hash,st.current_run_id
 FROM discovery_runs run LEFT JOIN discovery_source_revisions sr ON sr.id=run.source_revision_id
 LEFT JOIN discovery_source_states st ON st.source_revision_id=run.source_revision_id
 WHERE run.operation_id=?`, operationID).Scan(&run.id, &run.generation, &run.sourceRevision, &run.approvedRevision, &run.sourceRevisionID, &run.versionID, &run.sourceID, &run.ownerID, &run.useID, &run.purpose, &run.state, &run.operationID, &run.hash, &run.currentRunID)
	return run, err
}

func (r *Repository) completeDiscoveryGuarded(ctx context.Context, _ int64, operationID string, revision int64, outcome DiscoveryClaimResult, authorize func(context.Context, *sql.Tx) error) error {
	if r == nil || !db.ValidateOpaqueID(operationID, "op_") || revision < 1 || !validDiscoveryOutcome(outcome) {
		return ErrInvalidRequest
	}
	now, err := r.nowUnix()
	if err != nil {
		return err
	}
	tx, err := beginTx(ctx, r.db)
	if err != nil {
		return err
	}
	committed := false
	defer finishTx(tx, &committed)
	run, err := loadDiscoveryRunTx(ctx, tx, operationID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	hash := sha256.Sum256([]byte(operationID))
	if run.generation != revision || run.state != "checking" || !equalDiscoveryHash(run.hash, hash) {
		return ErrConflict
	}
	var active bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM accepted_operations WHERE id=? AND kind='model_discovery' AND state IN ('accepted','running'))`, operationID).Scan(&active); err != nil {
		return err
	}
	if !active {
		return ErrConflict
	}
	valid := run.currentRunID.Valid && run.currentRunID.Int64 == run.id && run.sourceRevisionID.Valid && run.sourceID.Valid && run.ownerID.Valid && run.versionID.Valid
	if valid {
		ref := connectionstore.SourceAuthorizationRef{OwnerUserID: run.ownerID.Int64, SourceID: run.sourceID.Int64, Purpose: connectionstore.SourcePurpose(run.purpose), DonationUseID: run.useID.Int64, UseRevisionID: run.approvedRevision}
		source, authErr := connectionstore.LoadDiscoveryAuthorizationTx(ctx, tx, ref)
		valid = authErr == nil && source.SourceRevisionID == run.sourceRevisionID.Int64 && source.SourceRevision == run.sourceRevision && source.CredentialVersionID == run.versionID.Int64 && source.UseApprovedRevisionID == run.approvedRevision
	}
	if valid && authorize != nil {
		valid = authorize(ctx, tx) == nil
	}
	if !valid {
		outcome = DiscoveryClaimResult{FailureClass: DiscoveryFailureInterrupted}
	}
	if err := r.finishDiscoveryRunTx(ctx, tx, run, outcome, now); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE accepted_operations SET state='completed',checkpoint='',last_error_class=NULL,terminal_at=? WHERE id=? AND kind='model_discovery' AND state IN ('accepted','running')`, now, operationID)
	if err != nil {
		return err
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		return ErrConflict
	}
	return commitTx(tx, &committed)
}

func equalDiscoveryHash(raw []byte, expected [32]byte) bool {
	if len(raw) != len(expected) {
		return false
	}
	for i, b := range raw {
		if b != expected[i] {
			return false
		}
	}
	return true
}

func (r *Repository) finishDiscoveryRunTx(ctx context.Context, tx *sql.Tx, run discoveryRunState, outcome DiscoveryClaimResult, now int64) error {
	if err := managementlog.StartRelatedCaptureTx(ctx, tx, "model_discovery", run.operationID); err != nil {
		return err
	}
	state, safeClass := "failed", string(outcome.FailureClass)
	if outcome.Succeeded {
		state, safeClass = "succeeded", "none"
	}
	result, err := tx.ExecContext(ctx, `UPDATE discovery_runs SET state=?,safe_class=?,safe_diagnostic=?,completed_at=?,model_count=? WHERE id=? AND state='checking'`, state, safeClass, outcome.SafeDiagnostic, now, len(outcome.Models), run.id)
	if err != nil {
		return err
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		return ErrConflict
	}
	if outcome.Succeeded {
		for ordinal, model := range outcome.Models {
			services := model.NativeServices
			identity := strconv.FormatInt(run.generation, 10) + ":" + strconv.Itoa(ordinal)
			if _, err := tx.ExecContext(ctx, `INSERT INTO model_catalog_entries(endpoint_key_id,discovery_run_id,source_type,source_identity,normalized_model_id,provider,source_revision,native_service_mask,evidence_source,created_at,updated_at) VALUES(NULL,?,'automatic',?,?,?,?,?,?,?,?)`, run.id, identity, model.UpstreamModelID, model.Provider, run.generation, services.Mask(), modelEvidenceOrigin(model), now, now); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO discovery_run_changes(run_id,added_count,removed_count)
 SELECT ?,
 (SELECT count(DISTINCT fresh.normalized_model_id) FROM model_catalog_entries fresh WHERE fresh.discovery_run_id=? AND NOT EXISTS(
 SELECT 1 FROM model_catalog_entries old WHERE old.discovery_run_id=st.last_successful_run_id AND old.normalized_model_id=fresh.normalized_model_id)),
 (SELECT count(DISTINCT old.normalized_model_id) FROM model_catalog_entries old WHERE old.discovery_run_id=st.last_successful_run_id AND NOT EXISTS(
 SELECT 1 FROM model_catalog_entries fresh WHERE fresh.discovery_run_id=? AND fresh.normalized_model_id=old.normalized_model_id))
 FROM discovery_source_states st WHERE st.source_revision_id=?`, run.id, run.id, run.id, run.sourceRevisionID.Int64); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE discovery_source_states SET last_successful_run_id=?,updated_at=? WHERE source_revision_id=? AND current_run_id=? AND result_generation=?`, run.id, now, run.sourceRevisionID.Int64, run.id, run.generation); err != nil {
			return err
		}
	}
	if run.sourceID.Valid && run.ownerID.Valid {
		if err := r.reconcileSourceKeysTx(ctx, tx, run.ownerID.Int64, run.sourceID.Int64, now); err != nil {
			return err
		}
	}
	if run.sourceRevisionID.Valid {
		if _, err := tx.ExecContext(ctx, `DELETE FROM discovery_runs WHERE source_revision_id=? AND state<>'checking'
 AND id NOT IN (SELECT current_run_id FROM discovery_source_states WHERE source_revision_id=? AND current_run_id IS NOT NULL)
 AND id NOT IN (SELECT last_successful_run_id FROM discovery_source_states WHERE source_revision_id=? AND last_successful_run_id IS NOT NULL)`, run.sourceRevisionID.Int64, run.sourceRevisionID.Int64, run.sourceRevisionID.Int64); err != nil {
			return err
		}
	}
	if run.versionID.Valid {
		if _, err := connectionstore.RetireUnreferencedVersionTx(ctx, tx, run.versionID.Int64, now); err != nil {
			return mapConnectionStoreError(err)
		}
	}
	resultOutcome := managementlog.Failed
	if outcome.Succeeded {
		resultOutcome = managementlog.Succeeded
	}
	return managementlog.AppendRelatedTx(ctx, tx, "model_discovery", run.operationID, managementlog.Item{Action: "discover", TargetKind: "discovery_job", TargetID: run.operationID, Outcome: resultOutcome, Code: string(outcome.FailureClass)}, true)
}

func modelEvidenceOrigin(model DiscoveredModel) string {
	if model.NativeServices.Empty() {
		return "unknown"
	}
	return "metadata"
}
