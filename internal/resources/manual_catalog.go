package resources

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
)

func scanCatalogEntry(scanner interface{ Scan(...any) error }) (CatalogEntry, error) {
	var id, sourceRevision, pairRevision int64
	var entry CatalogEntry
	if err := scanner.Scan(&id, &entry.SourceType, &entry.UpstreamModelID, &entry.Provider,
		&sourceRevision, &pairRevision, &entry.CreatedAt, &entry.UpdatedAt); err != nil {
		return CatalogEntry{}, err
	}
	entryID, err := decimalID(id)
	if err != nil || sourceRevision < 1 || pairRevision < 1 ||
		(entry.SourceType != "automatic" && entry.SourceType != "manual") {
		return CatalogEntry{}, ErrUnavailable
	}
	entry.ID = entryID
	entry.SourceRevision = strconv.FormatInt(sourceRevision, 10)
	entry.PairRevision = strconv.FormatInt(pairRevision, 10)
	return entry, nil
}

type pairState struct {
	automaticSupports int64
	manualSupports    int64
	automaticRevision int64
	pairRevision      int64
	evidenceState     string
	evidenceRevision  int64
}

func readPairStateTx(ctx context.Context, tx *sql.Tx, keyID int64, modelID string) (pairState, bool, error) {
	var state pairState
	err := tx.QueryRowContext(ctx, `
SELECT p.automatic_supports,p.manual_supports,p.automatic_revision,p.pair_revision,d.state,d.revision
FROM model_pair_catalog p JOIN model_discovery_evidence d ON d.endpoint_key_id=p.endpoint_key_id
WHERE p.endpoint_key_id=? AND p.normalized_model_id=?`, keyID, modelID).Scan(
		&state.automaticSupports, &state.manualSupports, &state.automaticRevision, &state.pairRevision,
		&state.evidenceState, &state.evidenceRevision)
	if errors.Is(err, sql.ErrNoRows) {
		return pairState{}, false, nil
	}
	if err != nil {
		return pairState{}, false, fmt.Errorf("resources: read catalog pair: %w", err)
	}
	return state, true, nil
}

func (state pairState) eligible() bool {
	return state.manualSupports > 0 ||
		(state.automaticSupports > 0 && state.evidenceState == "succeeded" && state.automaticRevision == state.evidenceRevision)
}

func (r *Repository) CreateManualEntries(ctx context.Context, userID, endpointID, keyID int64, mutation ControlMutation, entries []ManualCatalogInput) (MutationResult[ManualEntriesResponse], error) {
	if r == nil || userID <= 0 || endpointID <= 0 || keyID <= 0 || mutation.Route != routeManualCatalog || mutation.Method != http.MethodPost || !mutationPathIDs(mutation, endpointID, keyID) || mutation.Query != "" || len(entries) == 0 || len(entries) > 100 {
		return MutationResult[ManualEntriesResponse]{}, ErrInvalidRequest
	}
	seen := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		if !validateCatalogText(entry.UpstreamModelID, 1, 512) || !validateCatalogText(entry.Provider, 0, 128) {
			return MutationResult[ManualEntriesResponse]{}, ErrInvalidRequest
		}
		if _, duplicate := seen[entry.UpstreamModelID]; duplicate {
			return MutationResult[ManualEntriesResponse]{}, ErrConflict
		}
		seen[entry.UpstreamModelID] = struct{}{}
	}
	now, err := r.nowUnix()
	if err != nil {
		return MutationResult[ManualEntriesResponse]{}, err
	}
	tx, err := r.beginAuthorizedTx(ctx, userID)
	if err != nil {
		return MutationResult[ManualEntriesResponse]{}, err
	}
	committed := false
	defer finishTx(tx, &committed)
	decision, err := beginControlMutation(ctx, tx, userID, mutation, now)
	if err != nil {
		return MutationResult[ManualEntriesResponse]{}, err
	}
	if decision.Kind == idempotency.Replay {
		return replayMutation[ManualEntriesResponse](decision)
	}
	if _, err := discoveryOwnerTx(ctx, tx, userID, endpointID, keyID); err != nil {
		return MutationResult[ManualEntriesResponse]{}, err
	}
	locked, err := endpointKeyLockedTx(ctx, tx, keyID)
	if err != nil {
		return MutationResult[ManualEntriesResponse]{}, err
	}
	if locked {
		return MutationResult[ManualEntriesResponse]{}, ErrResourceLocked
	}
	created := make([]CatalogEntry, 0, len(entries))
	for _, input := range entries {
		pairRevision, err := addManualSupportTx(ctx, tx, keyID, input.UpstreamModelID, now)
		if err != nil {
			return MutationResult[ManualEntriesResponse]{}, err
		}
		result, err := tx.ExecContext(ctx, `
INSERT INTO model_catalog_entries(endpoint_key_id,source_type,source_identity,normalized_model_id,provider,source_revision,created_at,updated_at)
VALUES(?,'manual',?,?,?,?,?,?)`, keyID, input.UpstreamModelID, input.UpstreamModelID, input.Provider, pairRevision, now, now)
		if err != nil {
			return MutationResult[ManualEntriesResponse]{}, conflictOrError("create manual catalog entry", err)
		}
		entryID, err := result.LastInsertId()
		if err != nil {
			return MutationResult[ManualEntriesResponse]{}, fmt.Errorf("resources: create manual catalog identity: %w", err)
		}
		entry, err := catalogEntryTx(ctx, tx, keyID, entryID)
		if err != nil {
			return MutationResult[ManualEntriesResponse]{}, err
		}
		created = append(created, entry)
	}
	response := ManualEntriesResponse{Entries: created}
	out, err := finishJSONMutation(ctx, tx, decision, http.StatusCreated, response)
	if err != nil {
		return MutationResult[ManualEntriesResponse]{}, err
	}
	if err := commitTx(tx, &committed); err != nil {
		return MutationResult[ManualEntriesResponse]{}, err
	}
	return out, nil
}

func addManualSupportTx(ctx context.Context, tx *sql.Tx, keyID int64, modelID string, now int64) (int64, error) {
	state, exists, err := readPairStateTx(ctx, tx, keyID, modelID)
	if err != nil {
		return 0, err
	}
	if !exists {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO model_pair_catalog(endpoint_key_id,normalized_model_id,automatic_supports,manual_supports,automatic_revision,pair_revision,updated_at)
VALUES(?,?,0,1,0,1,?)`, keyID, modelID, now); err != nil {
			return 0, conflictOrError("create manual catalog pair", err)
		}
		return 1, nil
	}
	if state.manualSupports != 0 {
		return 0, ErrConflict
	}
	if state.pairRevision == int64(^uint64(0)>>1) || state.automaticSupports+1 > maxCatalogPairRows {
		return 0, ErrResourceLimit
	}
	revision := state.pairRevision + 1
	result, err := tx.ExecContext(ctx, `
UPDATE model_pair_catalog SET manual_supports=1,pair_revision=?,updated_at=?
WHERE endpoint_key_id=? AND normalized_model_id=? AND pair_revision=? AND manual_supports=0`,
		revision, now, keyID, modelID, state.pairRevision)
	if err != nil {
		return 0, fmt.Errorf("resources: add manual catalog support: %w", err)
	}
	updated, _ := result.RowsAffected()
	if updated != 1 {
		return 0, ErrConflict
	}
	return revision, nil
}

func catalogEntryTx(ctx context.Context, tx *sql.Tx, keyID, entryID int64) (CatalogEntry, error) {
	entry, err := scanCatalogEntry(tx.QueryRowContext(ctx, `
SELECT c.id,c.source_type,c.normalized_model_id,c.provider,c.source_revision,p.pair_revision,c.created_at,c.updated_at
FROM model_catalog_entries c
JOIN model_pair_catalog p ON p.endpoint_key_id=c.endpoint_key_id AND p.normalized_model_id=c.normalized_model_id
WHERE c.endpoint_key_id=? AND c.id=?`, keyID, entryID))
	if errors.Is(err, sql.ErrNoRows) {
		return CatalogEntry{}, ErrNotFound
	}
	if err != nil {
		return CatalogEntry{}, fmt.Errorf("resources: read catalog entry: %w", err)
	}
	return entry, nil
}

type manualEntryRow struct {
	id, sourceRevision, pairRevision int64
	modelID, provider                string
}

func manualEntryOwnerTx(ctx context.Context, tx *sql.Tx, userID, endpointID, keyID, entryID int64) (manualEntryRow, error) {
	var row manualEntryRow
	err := tx.QueryRowContext(ctx, `
SELECT c.id,c.normalized_model_id,c.provider,c.source_revision,p.pair_revision
FROM endpoints e
JOIN endpoint_keys k ON k.endpoint_id=e.id
JOIN model_catalog_entries c ON c.endpoint_key_id=k.id AND c.source_type='manual'
JOIN model_pair_catalog p ON p.endpoint_key_id=c.endpoint_key_id AND p.normalized_model_id=c.normalized_model_id
WHERE e.user_id=? AND e.id=? AND k.id=? AND c.id=?`, userID, endpointID, keyID, entryID).Scan(
		&row.id, &row.modelID, &row.provider, &row.sourceRevision, &row.pairRevision)
	if errors.Is(err, sql.ErrNoRows) {
		return manualEntryRow{}, ErrNotFound
	}
	if err != nil {
		return manualEntryRow{}, fmt.Errorf("resources: read manual catalog entry: %w", err)
	}
	return row, nil
}

func (r *Repository) UpdateManualEntry(ctx context.Context, userID, endpointID, keyID, entryID int64, mutation ControlMutation, input UpdateManualInput) (MutationResult[ManualUpdateResponse], error) {
	if r == nil || userID <= 0 || endpointID <= 0 || keyID <= 0 || entryID <= 0 || mutation.Route != routeManualEntry || mutation.Method != http.MethodPatch || !mutationPathIDs(mutation, endpointID, keyID, entryID) || mutation.Query != "" ||
		input.ExpectedPairRevision < 1 || !validateCatalogText(input.UpstreamModelID, 1, 512) || !validateCatalogText(input.Provider, 0, 128) || !validReplacements(input.Replacements) {
		return MutationResult[ManualUpdateResponse]{}, ErrInvalidRequest
	}
	now, err := r.nowUnix()
	if err != nil {
		return MutationResult[ManualUpdateResponse]{}, err
	}
	tx, err := r.beginAuthorizedTx(ctx, userID)
	if err != nil {
		return MutationResult[ManualUpdateResponse]{}, err
	}
	committed := false
	defer finishTx(tx, &committed)
	decision, err := beginControlMutation(ctx, tx, userID, mutation, now)
	if err != nil {
		return MutationResult[ManualUpdateResponse]{}, err
	}
	if decision.Kind == idempotency.Replay {
		return replayMutation[ManualUpdateResponse](decision)
	}
	current, err := manualEntryOwnerTx(ctx, tx, userID, endpointID, keyID, entryID)
	if err != nil {
		return MutationResult[ManualUpdateResponse]{}, err
	}
	if current.pairRevision != input.ExpectedPairRevision {
		return MutationResult[ManualUpdateResponse]{}, ErrConflict
	}
	locked, err := endpointKeyLockedTx(ctx, tx, keyID)
	if err != nil {
		return MutationResult[ManualUpdateResponse]{}, err
	}
	if locked {
		return MutationResult[ManualUpdateResponse]{}, ErrResourceLocked
	}
	oldState, exists, err := readPairStateTx(ctx, tx, keyID, current.modelID)
	if err != nil || !exists || oldState.manualSupports != 1 {
		if err != nil {
			return MutationResult[ManualUpdateResponse]{}, err
		}
		return MutationResult[ManualUpdateResponse]{}, ErrUnavailable
	}
	var newPairRevision int64
	if input.UpstreamModelID == current.modelID {
		if oldState.pairRevision == int64(^uint64(0)>>1) {
			return MutationResult[ManualUpdateResponse]{}, ErrResourceLimit
		}
		newPairRevision = oldState.pairRevision + 1
		result, err := tx.ExecContext(ctx, `
UPDATE model_pair_catalog SET pair_revision=?,updated_at=?
WHERE endpoint_key_id=? AND normalized_model_id=? AND pair_revision=? AND manual_supports=1`,
			newPairRevision, now, keyID, current.modelID, oldState.pairRevision)
		if err != nil {
			return MutationResult[ManualUpdateResponse]{}, fmt.Errorf("resources: advance manual pair revision: %w", err)
		}
		updated, _ := result.RowsAffected()
		if updated != 1 {
			return MutationResult[ManualUpdateResponse]{}, ErrConflict
		}
	} else {
		if err := removeManualSupportTx(ctx, tx, keyID, current.modelID, oldState.pairRevision, now); err != nil {
			return MutationResult[ManualUpdateResponse]{}, err
		}
		newPairRevision, err = addManualSupportTx(ctx, tx, keyID, input.UpstreamModelID, now)
		if err != nil {
			return MutationResult[ManualUpdateResponse]{}, err
		}
	}
	result, err := tx.ExecContext(ctx, `
UPDATE model_catalog_entries
SET source_identity=?,normalized_model_id=?,provider=?,source_revision=?,updated_at=?
WHERE id=? AND endpoint_key_id=? AND source_type='manual'`, input.UpstreamModelID, input.UpstreamModelID,
		input.Provider, newPairRevision, now, entryID, keyID)
	if err != nil {
		return MutationResult[ManualUpdateResponse]{}, conflictOrError("update manual catalog entry", err)
	}
	updated, _ := result.RowsAffected()
	if updated != 1 {
		return MutationResult[ManualUpdateResponse]{}, ErrConflict
	}
	affectedIDs, err := applyBindingReplacementsTx(ctx, tx, userID, keyID, current.modelID, input.Replacements, now)
	if err != nil {
		return MutationResult[ManualUpdateResponse]{}, err
	}
	if err := cleanCatalogPairTx(ctx, tx, keyID, current.modelID); err != nil {
		return MutationResult[ManualUpdateResponse]{}, err
	}
	entry, err := catalogEntryTx(ctx, tx, keyID, entryID)
	if err != nil {
		return MutationResult[ManualUpdateResponse]{}, err
	}
	affected, err := affectedModelsTx(ctx, tx, userID, affectedIDs)
	if err != nil {
		return MutationResult[ManualUpdateResponse]{}, err
	}
	response := ManualUpdateResponse{Entries: []CatalogEntry{entry}, AffectedModels: affected}
	out, err := finishJSONMutation(ctx, tx, decision, http.StatusOK, response)
	if err != nil {
		return MutationResult[ManualUpdateResponse]{}, err
	}
	if err := commitTx(tx, &committed); err != nil {
		return MutationResult[ManualUpdateResponse]{}, err
	}
	return out, nil
}

func (r *Repository) DeleteManualEntry(ctx context.Context, userID, endpointID, keyID, entryID int64, mutation ControlMutation, input DeleteManualInput) (MutationResult[struct{}], error) {
	if r == nil || userID <= 0 || endpointID <= 0 || keyID <= 0 || entryID <= 0 || mutation.Route != routeManualEntry || mutation.Method != http.MethodDelete || !mutationPathIDs(mutation, endpointID, keyID, entryID) || mutation.Query != "" ||
		input.ExpectedPairRevision < 1 || !validReplacements(input.Replacements) {
		return MutationResult[struct{}]{}, ErrInvalidRequest
	}
	now, err := r.nowUnix()
	if err != nil {
		return MutationResult[struct{}]{}, err
	}
	tx, err := r.beginAuthorizedTx(ctx, userID)
	if err != nil {
		return MutationResult[struct{}]{}, err
	}
	committed := false
	defer finishTx(tx, &committed)
	decision, err := beginControlMutation(ctx, tx, userID, mutation, now)
	if err != nil {
		return MutationResult[struct{}]{}, err
	}
	if decision.Kind == idempotency.Replay {
		return replayMutation[struct{}](decision)
	}
	current, err := manualEntryOwnerTx(ctx, tx, userID, endpointID, keyID, entryID)
	if err != nil {
		return MutationResult[struct{}]{}, err
	}
	if current.pairRevision != input.ExpectedPairRevision {
		return MutationResult[struct{}]{}, ErrConflict
	}
	locked, err := endpointKeyLockedTx(ctx, tx, keyID)
	if err != nil {
		return MutationResult[struct{}]{}, err
	}
	if locked {
		return MutationResult[struct{}]{}, ErrResourceLocked
	}
	if err := removeManualSupportTx(ctx, tx, keyID, current.modelID, current.pairRevision, now); err != nil {
		return MutationResult[struct{}]{}, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM model_catalog_entries WHERE id=? AND endpoint_key_id=? AND source_type='manual'`, entryID, keyID); err != nil {
		return MutationResult[struct{}]{}, fmt.Errorf("resources: delete manual catalog entry: %w", err)
	}
	if _, err := applyBindingReplacementsTx(ctx, tx, userID, keyID, current.modelID, input.Replacements, now); err != nil {
		return MutationResult[struct{}]{}, err
	}
	if err := cleanCatalogPairTx(ctx, tx, keyID, current.modelID); err != nil {
		return MutationResult[struct{}]{}, err
	}
	out, err := finishEmptyMutation(ctx, tx, decision, http.StatusNoContent)
	if err != nil {
		return MutationResult[struct{}]{}, err
	}
	if err := commitTx(tx, &committed); err != nil {
		return MutationResult[struct{}]{}, err
	}
	return out, nil
}

func removeManualSupportTx(ctx context.Context, tx *sql.Tx, keyID int64, modelID string, expectedRevision, now int64) error {
	if expectedRevision == int64(^uint64(0)>>1) {
		return ErrResourceLimit
	}
	result, err := tx.ExecContext(ctx, `
UPDATE model_pair_catalog SET manual_supports=0,pair_revision=pair_revision+1,updated_at=?
WHERE endpoint_key_id=? AND normalized_model_id=? AND pair_revision=? AND manual_supports=1`,
		now, keyID, modelID, expectedRevision)
	if err != nil {
		return fmt.Errorf("resources: remove manual catalog support: %w", err)
	}
	updated, _ := result.RowsAffected()
	if updated != 1 {
		return ErrConflict
	}
	return nil
}

func cleanCatalogPairTx(ctx context.Context, tx *sql.Tx, keyID int64, modelID string) error {
	_, err := tx.ExecContext(ctx, `
DELETE FROM model_pair_catalog
WHERE endpoint_key_id=? AND normalized_model_id=? AND automatic_supports=0 AND manual_supports=0
  AND NOT EXISTS(SELECT 1 FROM model_bindings b WHERE b.endpoint_key_id=model_pair_catalog.endpoint_key_id AND b.upstream_model_id=model_pair_catalog.normalized_model_id)
  AND NOT EXISTS(SELECT 1 FROM charity_model_bindings b WHERE b.endpoint_key_id=model_pair_catalog.endpoint_key_id AND b.upstream_model_id=model_pair_catalog.normalized_model_id)`, keyID, modelID)
	if err != nil {
		return fmt.Errorf("resources: clean catalog pair: %w", err)
	}
	return nil
}

func validReplacements(replacements []BindingReplacement) bool {
	if len(replacements) > maxBindingBatch {
		return false
	}
	seen := make(map[int64]struct{}, len(replacements))
	for _, replacement := range replacements {
		if replacement.BindingID <= 0 || !validateCatalogText(replacement.ReplacementUpstreamModelID, 1, 512) {
			return false
		}
		if _, duplicate := seen[replacement.BindingID]; duplicate {
			return false
		}
		seen[replacement.BindingID] = struct{}{}
	}
	return true
}

type existingReplacementBinding struct {
	id, modelID int64
}

func applyBindingReplacementsTx(ctx context.Context, tx *sql.Tx, userID, keyID int64, oldModelID string, replacements []BindingReplacement, now int64) ([]int64, error) {
	rows, err := tx.QueryContext(ctx, `
SELECT b.id,b.model_id
FROM model_bindings b JOIN models m ON m.id=b.model_id
WHERE m.user_id=? AND b.endpoint_key_id=? AND b.upstream_model_id=?
ORDER BY b.id`, userID, keyID, oldModelID)
	if err != nil {
		return nil, fmt.Errorf("resources: list affected bindings: %w", err)
	}
	var current []existingReplacementBinding
	for rows.Next() {
		var binding existingReplacementBinding
		if err := rows.Scan(&binding.id, &binding.modelID); err != nil {
			rows.Close()
			return nil, fmt.Errorf("resources: scan affected binding: %w", err)
		}
		current = append(current, binding)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("resources: close affected bindings: %w", err)
	}
	state, exists, err := readPairStateTx(ctx, tx, keyID, oldModelID)
	if err != nil {
		return nil, err
	}
	oldEligible := exists && state.eligible()
	replacementByID := make(map[int64]string, len(replacements))
	for _, replacement := range replacements {
		replacementByID[replacement.BindingID] = replacement.ReplacementUpstreamModelID
	}
	currentByID := make(map[int64]existingReplacementBinding, len(current))
	for _, binding := range current {
		currentByID[binding.id] = binding
	}
	for bindingID := range replacementByID {
		if _, ok := currentByID[bindingID]; !ok {
			return nil, ErrConflict
		}
	}
	if !oldEligible && len(replacementByID) != len(current) {
		return nil, ErrConflict
	}
	affected := make(map[int64]struct{})
	for _, binding := range current {
		target, replace := replacementByID[binding.id]
		if !replace || target == oldModelID {
			if !oldEligible {
				return nil, ErrConflict
			}
			continue
		}
		targetState, exists, err := readPairStateTx(ctx, tx, keyID, target)
		if err != nil {
			return nil, err
		}
		if !exists || !targetState.eligible() {
			return nil, ErrConflict
		}
		var duplicate int
		if err := tx.QueryRowContext(ctx, `
SELECT EXISTS(SELECT 1 FROM model_bindings WHERE model_id=? AND endpoint_key_id=? AND upstream_model_id=? AND id<>?)`,
			binding.modelID, keyID, target, binding.id).Scan(&duplicate); err != nil {
			return nil, fmt.Errorf("resources: verify replacement binding: %w", err)
		}
		if duplicate == 1 {
			return nil, ErrConflict
		}
		if _, err := tx.ExecContext(ctx, `UPDATE model_bindings SET upstream_model_id=?,updated_at=? WHERE id=? AND model_id=?`, target, now, binding.id, binding.modelID); err != nil {
			return nil, conflictOrError("replace binding", err)
		}
		affected[binding.modelID] = struct{}{}
	}
	modelIDs := make([]int64, 0, len(affected))
	for modelID := range affected {
		result, err := tx.ExecContext(ctx, `
UPDATE models SET binding_revision=binding_revision+1,updated_at=?
WHERE id=? AND user_id=? AND binding_revision<9223372036854775807`, now, modelID, userID)
		if err != nil {
			return nil, fmt.Errorf("resources: advance binding revision: %w", err)
		}
		updated, _ := result.RowsAffected()
		if updated != 1 {
			return nil, ErrResourceLimit
		}
		modelIDs = append(modelIDs, modelID)
	}
	sort.Slice(modelIDs, func(i, j int) bool { return modelIDs[i] < modelIDs[j] })
	return modelIDs, nil
}

func affectedModelsTx(ctx context.Context, tx *sql.Tx, userID int64, modelIDs []int64) ([]AffectedModel, error) {
	items := make([]AffectedModel, 0, len(modelIDs))
	for _, modelID := range modelIDs {
		model, err := getModelTx(ctx, tx, userID, modelID)
		if err != nil {
			return nil, err
		}
		bindings, err := listBindingsTx(ctx, tx, userID, modelID)
		if err != nil {
			return nil, err
		}
		items = append(items, AffectedModel{Model: model, Bindings: bindings})
	}
	return items, nil
}

func conflictOrError(action string, err error) error {
	if err == nil {
		return nil
	}
	text := err.Error()
	if containsConstraint(text) {
		return ErrConflict
	}
	return fmt.Errorf("resources: %s: %w", action, err)
}

func containsConstraint(value string) bool {
	for _, fragment := range []string{"constraint failed", "UNIQUE constraint", "FOREIGN KEY constraint"} {
		if len(value) >= len(fragment) {
			for i := 0; i+len(fragment) <= len(value); i++ {
				if value[i:i+len(fragment)] == fragment {
					return true
				}
			}
		}
	}
	return false
}
