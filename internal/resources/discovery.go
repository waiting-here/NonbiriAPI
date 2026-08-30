package resources

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"time"
	"unicode"
	"unicode/utf8"

	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
)

const (
	routeCatalog            = "/api/endpoints/{id}/keys/{keyId}/models"
	routeDiscovery          = "/api/endpoints/{id}/keys/{keyId}/models/refresh"
	routeManualCatalog      = "/api/endpoints/{id}/keys/{keyId}/models/manual"
	routeManualEntry        = "/api/endpoints/{id}/keys/{keyId}/models/manual/{entryId}"
	discoveryCleanupTimeout = 5 * time.Second
)

type discoveryRow struct {
	endpointID, keyID      int64
	connectorType, baseURL string
	state                  string
	revision               int64
	operationHash          []byte
	safeClass, safeDiag    string
	startedAt, completedAt sql.NullInt64
	fetchedCount           int64
}

type discoveryJob struct {
	input    DiscoveryClaimInput
	revision int64
}

func (row discoveryRow) evidence() (DiscoveryEvidence, error) {
	if row.revision < 1 || row.fetchedCount < 0 {
		return DiscoveryEvidence{}, ErrUnavailable
	}
	evidence := DiscoveryEvidence{
		State: row.state, Revision: strconv.FormatInt(row.revision, 10),
		SafeClass: row.safeClass,
	}
	switch row.state {
	case "unknown":
		if row.operationHash != nil || row.safeClass != "none" || row.startedAt.Valid || row.completedAt.Valid || row.fetchedCount != 0 {
			return DiscoveryEvidence{}, ErrUnavailable
		}
	case "checking":
		if len(row.operationHash) != sha256.Size || row.safeClass != "none" || !row.startedAt.Valid || row.completedAt.Valid {
			return DiscoveryEvidence{}, ErrUnavailable
		}
		observed := row.startedAt.Int64
		evidence.ObservedAt = &observed
	case "succeeded":
		if row.operationHash != nil || row.safeClass != "none" || !row.completedAt.Valid {
			return DiscoveryEvidence{}, ErrUnavailable
		}
		result := "nonempty"
		if row.fetchedCount == 0 {
			result = "empty"
		}
		count := strconv.FormatInt(row.fetchedCount, 10)
		observed := row.completedAt.Int64
		evidence.Result, evidence.Count, evidence.ObservedAt = &result, &count, &observed
	case "failed":
		if row.operationHash != nil || !validFailureClass(DiscoveryFailureClass(row.safeClass)) || !row.completedAt.Valid {
			return DiscoveryEvidence{}, ErrUnavailable
		}
		observed := row.completedAt.Int64
		evidence.ObservedAt = &observed
	default:
		return DiscoveryEvidence{}, ErrUnavailable
	}
	return evidence, nil
}

func scanDiscovery(scanner interface{ Scan(...any) error }) (discoveryRow, error) {
	var row discoveryRow
	err := scanner.Scan(&row.endpointID, &row.keyID, &row.connectorType, &row.baseURL,
		&row.state, &row.revision, &row.operationHash, &row.safeClass, &row.safeDiag,
		&row.startedAt, &row.completedAt, &row.fetchedCount)
	return row, err
}

func discoveryOwnerTx(ctx context.Context, tx *sql.Tx, userID, endpointID, keyID int64) (discoveryRow, error) {
	row, err := scanDiscovery(tx.QueryRowContext(ctx, `
SELECT e.id,k.id,e.connector_type,e.base_url,
       d.state,d.revision,d.operation_hash,d.safe_class,d.safe_diag,d.started_at,d.completed_at,d.fetched_count
FROM endpoints e
JOIN endpoint_keys k ON k.endpoint_id=e.id
JOIN model_discovery_evidence d ON d.endpoint_key_id=k.id
WHERE e.user_id=? AND e.id=? AND k.id=?`, userID, endpointID, keyID))
	if errors.Is(err, sql.ErrNoRows) {
		return discoveryRow{}, ErrNotFound
	}
	if err != nil {
		return discoveryRow{}, fmt.Errorf("resources: read discovery evidence: %w", err)
	}
	return row, nil
}

func (r *Repository) GetCatalog(ctx context.Context, userID, endpointID, keyID int64, limit int, cursor string) (CatalogView, error) {
	limit = normalizePageLimit(limit)
	if r == nil || userID <= 0 || endpointID <= 0 || keyID <= 0 || !validPageLimit(limit) {
		return CatalogView{}, ErrInvalidRequest
	}
	tx, err := beginTx(ctx, r.db)
	if err != nil {
		return CatalogView{}, err
	}
	defer tx.Rollback()
	owner, err := discoveryOwnerTx(ctx, tx, userID, endpointID, keyID)
	if err != nil {
		return CatalogView{}, err
	}
	evidence, err := owner.evidence()
	if err != nil {
		return CatalogView{}, err
	}
	now, err := r.nowUnix()
	if err != nil {
		return CatalogView{}, err
	}
	var afterID uint64
	if cursor != "" {
		cursorOwner := catalogCursorOwner(userID, endpointID, keyID)
		atoms, err := r.cursors.decode(cursor, "endpoint-catalog", cursorOwner, uint64(now), db.CursorUint)
		if err != nil {
			return CatalogView{}, err
		}
		afterID = atoms[0].Uint
		if afterID == 0 || afterID > uint64(^uint64(0)>>1) {
			return CatalogView{}, ErrInvalidRequest
		}
	}
	rows, err := tx.QueryContext(ctx, `
SELECT c.id,c.source_type,c.normalized_model_id,c.provider,c.source_revision,p.pair_revision,c.created_at,c.updated_at
FROM model_catalog_entries c
JOIN model_pair_catalog p ON p.endpoint_key_id=c.endpoint_key_id AND p.normalized_model_id=c.normalized_model_id
WHERE c.endpoint_key_id=? AND (?=0 OR c.id>?)
ORDER BY c.id LIMIT ?`, keyID, afterID, afterID, limit+1)
	if err != nil {
		return CatalogView{}, fmt.Errorf("resources: list catalog: %w", err)
	}
	defer rows.Close()
	all := make([]CatalogEntry, 0, limit+1)
	for rows.Next() {
		entry, err := scanCatalogEntry(rows)
		if err != nil {
			return CatalogView{}, fmt.Errorf("resources: scan catalog: %w", err)
		}
		all = append(all, entry)
	}
	if err := rows.Err(); err != nil {
		return CatalogView{}, fmt.Errorf("resources: list catalog: %w", err)
	}
	view := CatalogView{Evidence: evidence, AutomaticEntries: []CatalogEntry{}, ManualEntries: []CatalogEntry{}}
	visible := all
	if len(all) > limit {
		visible = all[:limit]
		lastID, _ := parseDecimalID(visible[len(visible)-1].ID)
		next, err := r.cursors.encode("endpoint-catalog", catalogCursorOwner(userID, endpointID, keyID), uint64(now+cursorLifetime), []db.CursorAtom{{Kind: db.CursorUint, Uint: uint64(lastID)}})
		if err != nil {
			return CatalogView{}, err
		}
		view.NextCursor = &next
	}
	for _, entry := range visible {
		if entry.SourceType == "automatic" {
			view.AutomaticEntries = append(view.AutomaticEntries, entry)
		} else {
			view.ManualEntries = append(view.ManualEntries, entry)
		}
	}
	return view, nil
}

func catalogCursorOwner(userID, endpointID, keyID int64) string {
	return strconv.FormatInt(userID, 10) + ":" + strconv.FormatInt(endpointID, 10) + ":" + strconv.FormatInt(keyID, 10)
}

func (r *Repository) RefreshDiscovery(ctx context.Context, userID, endpointID, keyID int64, mutation ControlMutation) (MutationResult[DiscoveryAccepted], error) {
	if r == nil || userID <= 0 || endpointID <= 0 || keyID <= 0 || mutation.Route != routeDiscovery || mutation.Method != http.MethodPost || !mutationPathIDs(mutation, endpointID, keyID) || mutation.Query != "" {
		return MutationResult[DiscoveryAccepted]{}, ErrInvalidRequest
	}
	now, err := r.nowUnix()
	if err != nil {
		return MutationResult[DiscoveryAccepted]{}, err
	}
	actor, err := actorHash(userID)
	if err != nil {
		return MutationResult[DiscoveryAccepted]{}, err
	}
	digest, err := idempotency.RequestDigest(idempotency.DigestInput{
		ActorScopeHash: actor, Method: mutation.Method, Route: mutation.Route,
		PathResourceIDs: mutation.PathIDs, Query: mutation.Query, Body: mutation.CanonicalBody,
	})
	if err != nil {
		return MutationResult[DiscoveryAccepted]{}, ErrInvalidRequest
	}
	tx, err := r.beginAuthorizedTx(ctx, userID)
	if err != nil {
		return MutationResult[DiscoveryAccepted]{}, err
	}
	committed := false
	defer finishTx(tx, &committed)
	decision, err := idempotency.Begin(ctx, tx, idempotency.BeginInput{
		Scope: idempotency.ScopeModelDiscovery, ActorHash: actor, Key: mutation.IdempotencyKey,
		RequestHash: digest, DecisionNow: now,
	})
	if errors.Is(err, idempotency.ErrConflict) || errors.Is(err, idempotency.ErrInProgress) {
		return MutationResult[DiscoveryAccepted]{}, ErrConflict
	}
	if err != nil {
		return MutationResult[DiscoveryAccepted]{}, fmt.Errorf("resources: accept discovery replay: %w", err)
	}
	if decision.Kind == idempotency.Replay {
		return replayMutation[DiscoveryAccepted](decision)
	}
	owner, err := discoveryOwnerTx(ctx, tx, userID, endpointID, keyID)
	if err != nil {
		return MutationResult[DiscoveryAccepted]{}, err
	}
	if owner.state == "checking" || owner.revision == int64(^uint64(0)>>1) {
		return MutationResult[DiscoveryAccepted]{}, ErrConflict
	}
	locked, err := endpointKeyLockedTx(ctx, tx, keyID)
	if err != nil {
		return MutationResult[DiscoveryAccepted]{}, err
	}
	if locked {
		return MutationResult[DiscoveryAccepted]{}, ErrResourceLocked
	}
	descriptor, ok := r.connectors.Descriptor(connectorcontract.Type(owner.connectorType))
	if !ok || descriptor.Discoverer == nil {
		return MutationResult[DiscoveryAccepted]{}, ErrInvalidRequest
	}
	operationID, err := r.operationID()
	if err != nil || !db.ValidateOpaqueID(operationID, "op_") {
		return MutationResult[DiscoveryAccepted]{}, ErrUnavailable
	}
	operationHash := sha256.Sum256([]byte(operationID))
	newRevision := owner.revision + 1
	job := discoveryJob{
		input: DiscoveryClaimInput{
			OperationID: operationID, OwnerUserID: userID, EndpointID: endpointID,
			EndpointKeyID: keyID,
			ConnectorType: connectorcontract.Type(owner.connectorType), CanonicalBaseURL: owner.baseURL,
			Discoverer: descriptor.Discoverer,
		},
		revision: newRevision,
	}
	reservation, admitted := r.discoveryWorker.ReserveDiscovery()
	if !admitted {
		return MutationResult[DiscoveryAccepted]{}, errDiscoveryWorkerUnavailable
	}
	reservationStarted := false
	defer func() {
		if !reservationStarted {
			reservation.Release()
		}
	}()
	checkpoint := strconv.FormatInt(keyID, 10) + ":" + strconv.FormatInt(newRevision, 10)
	if _, err := tx.ExecContext(ctx, `
INSERT INTO accepted_operations(
 id,kind,actor_user_id,actor_role,payload_hash,state,checkpoint,last_error_class,created_at,terminal_at
) VALUES(?,'model_discovery',?,'user',?,'accepted',?,NULL,?,NULL)`,
		operationID, userID, digest[:], checkpoint, now); err != nil {
		return MutationResult[DiscoveryAccepted]{}, fmt.Errorf("resources: accept discovery operation: %w", err)
	}
	result, err := tx.ExecContext(ctx, `
UPDATE model_discovery_evidence
SET state='checking',revision=?,operation_hash=?,safe_class='none',safe_diag='',started_at=?,completed_at=NULL,fetched_count=0
WHERE endpoint_key_id=? AND revision=? AND state<>'checking'`, newRevision, operationHash[:], now, keyID, owner.revision)
	if err != nil {
		return MutationResult[DiscoveryAccepted]{}, fmt.Errorf("resources: start discovery: %w", err)
	}
	updated, _ := result.RowsAffected()
	if updated != 1 {
		return MutationResult[DiscoveryAccepted]{}, ErrConflict
	}
	checking := DiscoveryEvidence{State: "checking", Revision: strconv.FormatInt(newRevision, 10), SafeClass: "none", ObservedAt: &now}
	accepted := DiscoveryAccepted{OperationID: operationID, Evidence: checking}
	out, err := finishJSONMutation(ctx, tx, decision, http.StatusAccepted, accepted)
	if err != nil {
		return MutationResult[DiscoveryAccepted]{}, err
	}
	if err := commitTx(tx, &committed); err != nil {
		return MutationResult[DiscoveryAccepted]{}, err
	}
	reservationStarted = true
	reservation.Start(func(workerContext context.Context) {
		r.runDiscovery(workerContext, job)
	})
	return out, nil
}

func (r *Repository) runDiscovery(ctx context.Context, job discoveryJob) {
	markContext, cancelMark := context.WithTimeout(context.Background(), discoveryCleanupTimeout)
	markErr := r.markDiscoveryRunning(markContext, job.input.OperationID)
	cancelMark()

	claimResult := DiscoveryClaimResult{FailureClass: DiscoveryFailureInterrupted}
	if markErr == nil && ctx.Err() == nil {
		result, claimErr := r.discoveryRail.Discover(ctx, job.input)
		if claimErr == nil && ctx.Err() == nil {
			if validDiscoveryOutcome(result) {
				claimResult = result
			} else {
				// The rail is fed by an untrusted upstream. An invalid typed
				// projection is a protocol failure, not a reason to leave an
				// accepted operation checking until stale recovery.
				claimResult = DiscoveryClaimResult{FailureClass: DiscoveryFailureProtocol}
			}
		}
	}
	completionContext, cancelCompletion := context.WithTimeout(context.Background(), discoveryCleanupTimeout)
	defer cancelCompletion()
	if err := r.completeDiscovery(completionContext, job.input.EndpointKeyID, job.input.OperationID, job.revision, claimResult); err != nil {
		// No network retry is attempted; stale recovery fails closed.
		return
	}
}

func (r *Repository) markDiscoveryRunning(ctx context.Context, operationID string) error {
	if r == nil || !db.ValidateOpaqueID(operationID, "op_") {
		return ErrInvalidRequest
	}
	result, err := r.db.ExecContext(ctx, `
UPDATE accepted_operations SET state='running'
WHERE id=? AND kind='model_discovery' AND state='accepted'`, operationID)
	if err != nil {
		return fmt.Errorf("resources: start discovery operation: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("resources: observe discovery operation start: %w", err)
	}
	if updated != 1 {
		return ErrConflict
	}
	return nil
}

func (r *Repository) completeDiscovery(ctx context.Context, keyID int64, operationID string, revision int64, outcome DiscoveryClaimResult) error {
	if r == nil || keyID <= 0 || !db.ValidateOpaqueID(operationID, "op_") || revision < 1 || !validDiscoveryOutcome(outcome) {
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
	operationHash := sha256.Sum256([]byte(operationID))
	checkpoint := strconv.FormatInt(keyID, 10) + ":" + strconv.FormatInt(revision, 10)
	var operationCount int
	if err := tx.QueryRowContext(ctx, `
SELECT count(*) FROM accepted_operations
WHERE id=? AND kind='model_discovery' AND checkpoint=? AND state IN ('accepted','running')`,
		operationID, checkpoint).Scan(&operationCount); err != nil {
		return fmt.Errorf("resources: verify discovery continuation: %w", err)
	}
	if operationCount != 1 {
		return ErrConflict
	}
	var evidenceUpdated bool
	if outcome.Succeeded {
		updated, err := completeDiscoverySuccessTx(ctx, tx, keyID, revision, operationHash, outcome.Models, now)
		if err != nil {
			return err
		}
		evidenceUpdated = updated
	} else {
		result, err := tx.ExecContext(ctx, `
UPDATE model_discovery_evidence
SET state='failed',operation_hash=NULL,safe_class=?,safe_diag=?,completed_at=?,fetched_count=0
WHERE endpoint_key_id=? AND state='checking' AND revision=? AND operation_hash=?`,
			string(outcome.FailureClass), outcome.SafeDiagnostic, now, keyID, revision, operationHash[:])
		if err != nil {
			return fmt.Errorf("resources: fail discovery: %w", err)
		}
		updated, _ := result.RowsAffected()
		evidenceUpdated = updated == 1
	}
	if !evidenceUpdated {
		var evidenceExists int
		if err := tx.QueryRowContext(ctx, `
SELECT EXISTS(SELECT 1 FROM model_discovery_evidence WHERE endpoint_key_id=?)`, keyID).Scan(&evidenceExists); err != nil {
			return fmt.Errorf("resources: inspect discovery continuation target: %w", err)
		}
		if evidenceExists != 0 {
			return ErrConflict
		}
	}
	result, err := tx.ExecContext(ctx, `
UPDATE accepted_operations
SET state='completed',checkpoint='',last_error_class=NULL,terminal_at=?
WHERE id=? AND kind='model_discovery' AND checkpoint=? AND state IN ('accepted','running')`,
		now, operationID, checkpoint)
	if err != nil {
		return fmt.Errorf("resources: complete discovery operation: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("resources: observe discovery operation completion: %w", err)
	}
	if updated != 1 {
		return ErrConflict
	}
	return commitTx(tx, &committed)
}

func completeDiscoverySuccessTx(ctx context.Context, tx *sql.Tx, keyID, revision int64, operationHash [32]byte, models []DiscoveredModel, now int64) (bool, error) {
	result, err := tx.ExecContext(ctx, `
UPDATE model_discovery_evidence
SET state='succeeded',operation_hash=NULL,safe_class='none',safe_diag='',completed_at=?,fetched_count=?
WHERE endpoint_key_id=? AND state='checking' AND revision=? AND operation_hash=?`, now, len(models), keyID, revision, operationHash[:])
	if err != nil {
		return false, fmt.Errorf("resources: complete discovery evidence: %w", err)
	}
	updated, _ := result.RowsAffected()
	if updated == 0 {
		return false, nil
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM model_catalog_entries WHERE endpoint_key_id=? AND source_type='automatic'`, keyID); err != nil {
		return false, fmt.Errorf("resources: replace automatic catalog: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE model_pair_catalog SET automatic_supports=0,automatic_revision=?,updated_at=?
	WHERE endpoint_key_id=?`, revision, now, keyID); err != nil {
		return false, fmt.Errorf("resources: clear automatic pair support: %w", err)
	}
	counts := make(map[string]int64)
	for _, model := range models {
		counts[model.UpstreamModelID]++
	}
	modelIDs := make([]string, 0, len(counts))
	for modelID := range counts {
		modelIDs = append(modelIDs, modelID)
	}
	sort.Strings(modelIDs)
	for _, modelID := range modelIDs {
		count := counts[modelID]
		if count > maxCatalogPairRows {
			return false, ErrResourceLimit
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO model_pair_catalog(endpoint_key_id,normalized_model_id,automatic_supports,manual_supports,automatic_revision,pair_revision,updated_at)
VALUES(?,?,?,0,?,1,?)
ON CONFLICT(endpoint_key_id,normalized_model_id) DO UPDATE SET
 automatic_supports=excluded.automatic_supports,automatic_revision=excluded.automatic_revision,updated_at=excluded.updated_at`,
			keyID, modelID, count, revision, now); err != nil {
			return false, fmt.Errorf("resources: write automatic pair support: %w", err)
		}
	}
	for ordinal, model := range models {
		identity := strconv.FormatInt(revision, 10) + ":" + strconv.Itoa(ordinal)
		if _, err := tx.ExecContext(ctx, `
INSERT INTO model_catalog_entries(endpoint_key_id,source_type,source_identity,normalized_model_id,provider,source_revision,created_at,updated_at)
		VALUES(?,'automatic',?,?,?,?,?,?)`, keyID, identity, model.UpstreamModelID, model.Provider, revision, now, now); err != nil {
			return false, fmt.Errorf("resources: write automatic catalog entry: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `
DELETE FROM model_pair_catalog
WHERE endpoint_key_id=? AND automatic_supports=0 AND manual_supports=0
  AND NOT EXISTS(SELECT 1 FROM model_bindings b WHERE b.endpoint_key_id=model_pair_catalog.endpoint_key_id AND b.upstream_model_id=model_pair_catalog.normalized_model_id)
  AND NOT EXISTS(SELECT 1 FROM charity_model_bindings b WHERE b.endpoint_key_id=model_pair_catalog.endpoint_key_id AND b.upstream_model_id=model_pair_catalog.normalized_model_id)`, keyID); err != nil {
		return false, fmt.Errorf("resources: clean empty catalog pairs: %w", err)
	}
	return true, nil
}

func validDiscoveryOutcome(outcome DiscoveryClaimResult) bool {
	if outcome.Succeeded {
		if outcome.FailureClass != "" || outcome.SafeDiagnostic != "" || len(outcome.Models) > maxDiscoveryRows {
			return false
		}
		counts := make(map[string]int)
		for _, model := range outcome.Models {
			if !validateCatalogText(model.UpstreamModelID, 1, 512) || !validateCatalogText(model.Provider, 0, 128) {
				return false
			}
			counts[model.UpstreamModelID]++
			if counts[model.UpstreamModelID] > maxCatalogPairRows {
				return false
			}
		}
		return true
	}
	return len(outcome.Models) == 0 && validFailureClass(outcome.FailureClass) && validSafeDiagnostic(outcome.SafeDiagnostic)
}

func validFailureClass(value DiscoveryFailureClass) bool {
	switch value {
	case DiscoveryFailureAuth, DiscoveryFailureRateLimit, DiscoveryFailureTimeout,
		DiscoveryFailureProtocol, DiscoveryFailureTransport, DiscoveryFailureInterrupted:
		return true
	default:
		return false
	}
}

func validSafeDiagnostic(value string) bool {
	if !utf8.ValidString(value) || len(value) > 4096 {
		return false
	}
	for _, runeValue := range value {
		if unicode.IsControl(runeValue) || runeValue == 0x7f {
			return false
		}
	}
	return true
}

func (r *Repository) RecoverStaleDiscoveries(ctx context.Context) (int64, error) {
	if r == nil {
		return 0, ErrUnavailable
	}
	now, err := r.nowUnix()
	if err != nil {
		return 0, err
	}
	tx, err := beginTx(ctx, r.db)
	if err != nil {
		return 0, err
	}
	committed := false
	defer finishTx(tx, &committed)
	rows, err := tx.QueryContext(ctx, `
SELECT endpoint_key_id,revision FROM model_discovery_evidence
WHERE state='checking' AND started_at<=? ORDER BY endpoint_key_id`, now-staleDiscoverySecond)
	if err != nil {
		return 0, fmt.Errorf("resources: list stale discoveries: %w", err)
	}
	var checkpoints []string
	for rows.Next() {
		var keyID, revision int64
		if err := rows.Scan(&keyID, &revision); err != nil {
			rows.Close()
			return 0, fmt.Errorf("resources: scan stale discovery: %w", err)
		}
		checkpoints = append(checkpoints, strconv.FormatInt(keyID, 10)+":"+strconv.FormatInt(revision, 10))
	}
	if err := rows.Close(); err != nil {
		return 0, fmt.Errorf("resources: close stale discoveries: %w", err)
	}
	for _, checkpoint := range checkpoints {
		if _, err := tx.ExecContext(ctx, `
UPDATE accepted_operations
SET state='completed',checkpoint='',last_error_class=NULL,terminal_at=?
WHERE kind='model_discovery' AND checkpoint=? AND state IN ('accepted','running')`, now, checkpoint); err != nil {
			return 0, fmt.Errorf("resources: recover discovery operation: %w", err)
		}
	}
	result, err := tx.ExecContext(ctx, `
UPDATE model_discovery_evidence
SET state='failed',operation_hash=NULL,safe_class='interrupted',safe_diag='',completed_at=?,fetched_count=0
WHERE state='checking' AND started_at<=?`, now, now-staleDiscoverySecond)
	if err != nil {
		return 0, fmt.Errorf("resources: recover stale discoveries: %w", err)
	}
	evidenceCount, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("resources: observe stale discovery recovery: %w", err)
	}
	orphaned, err := tx.ExecContext(ctx, `
UPDATE accepted_operations AS op
SET state='completed',checkpoint='',last_error_class=NULL,terminal_at=?
WHERE op.kind='model_discovery' AND op.state IN ('accepted','running') AND op.created_at<=?
  AND NOT EXISTS(
    SELECT 1 FROM model_discovery_evidence d
    WHERE d.state='checking'
      AND op.checkpoint=CAST(d.endpoint_key_id AS TEXT)||':'||CAST(d.revision AS TEXT)
  )`, now, now-staleDiscoverySecond)
	if err != nil {
		return 0, fmt.Errorf("resources: recover orphaned discovery operations: %w", err)
	}
	orphanedCount, err := orphaned.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("resources: observe orphaned discovery recovery: %w", err)
	}
	if err := commitTx(tx, &committed); err != nil {
		return 0, err
	}
	return evidenceCount + orphanedCount, nil
}

func validateCatalogText(value string, minRunes, maxRunes int) bool {
	return validateExactText(value, minRunes, maxRunes)
}
