package resources

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
)

const (
	routeModels            = "/api/models"
	routeModel             = "/api/models/{id}"
	routeBindingCandidates = "/api/models/{id}/binding-candidates"
	routeBindings          = "/api/models/{id}/bindings"
	routeBindingBatch      = "/api/models/{id}/bindings/batch"
	routeBindingOrder      = "/api/models/{id}/bindings/order"
	routeBinding           = "/api/models/{id}/bindings/{bId}"
)

type modelRow struct {
	id, revision, bindingRevision, bindingCount, createdAt, updatedAt int64
	provider, model, fullName, routeStrategy                          string
	silentRetry, flattenToolCalls                                     int
}

func (row modelRow) dto() (Model, error) {
	id, err := decimalID(row.id)
	if err != nil || row.revision < 1 || row.bindingRevision < 0 || row.bindingCount < 0 ||
		row.silentRetry < 0 || row.silentRetry > 1 || row.flattenToolCalls < 0 || row.flattenToolCalls > 1 ||
		(row.routeStrategy != "ordered" && row.routeStrategy != "random") || row.fullName != row.provider+"/"+row.model {
		return Model{}, ErrUnavailable
	}
	return Model{
		ID: id, Provider: row.provider, Model: row.model, FullName: row.fullName,
		RouteStrategy: row.routeStrategy, SilentRetry: row.silentRetry == 1,
		FlattenToolCalls: row.flattenToolCalls == 1, Revision: strconv.FormatInt(row.revision, 10),
		BindingRevision: strconv.FormatInt(row.bindingRevision, 10), BindingCount: strconv.FormatInt(row.bindingCount, 10),
		CreatedAt: row.createdAt, UpdatedAt: row.updatedAt,
	}, nil
}

func scanModel(scanner interface{ Scan(...any) error }) (Model, error) {
	var row modelRow
	if err := scanner.Scan(&row.id, &row.provider, &row.model, &row.fullName, &row.routeStrategy,
		&row.silentRetry, &row.flattenToolCalls, &row.revision, &row.bindingRevision, &row.bindingCount,
		&row.createdAt, &row.updatedAt); err != nil {
		return Model{}, err
	}
	return row.dto()
}

const modelSelect = `
SELECT m.id,m.provider,m.model,m.full_name,m.route_strategy,m.silent_retry,m.flatten_tool_calls,
       m.revision,m.binding_revision,(SELECT count(*) FROM model_bindings b WHERE b.model_id=m.id),
       m.created_at,m.updated_at
FROM models m`

func (r *Repository) ListModels(ctx context.Context, userID int64, limit int, cursor string) (Page[Model], error) {
	limit = normalizePageLimit(limit)
	if r == nil || userID <= 0 || !validPageLimit(limit) {
		return Page[Model]{}, ErrInvalidRequest
	}
	now, err := r.nowUnix()
	if err != nil {
		return Page[Model]{}, err
	}
	var afterUpdated, afterID uint64
	if cursor != "" {
		atoms, err := r.cursors.decode(cursor, "user-models", strconv.FormatInt(userID, 10), uint64(now), db.CursorUint, db.CursorUint)
		if err != nil {
			return Page[Model]{}, err
		}
		afterUpdated, afterID = atoms[0].Uint, atoms[1].Uint
		if afterID == 0 || afterID > uint64(^uint64(0)>>1) || afterUpdated > uint64(maxUnixSecond) {
			return Page[Model]{}, ErrInvalidRequest
		}
	}
	rows, err := r.db.QueryContext(ctx, modelSelect+`
WHERE m.user_id=? AND (?=0 OR m.updated_at<? OR (m.updated_at=? AND m.id<?))
ORDER BY m.updated_at DESC,m.id DESC LIMIT ?`, userID, afterID, afterUpdated, afterUpdated, afterID, limit+1)
	if err != nil {
		return Page[Model]{}, fmt.Errorf("resources: list models: %w", err)
	}
	defer rows.Close()
	items := make([]Model, 0, limit+1)
	for rows.Next() {
		item, err := scanModel(rows)
		if err != nil {
			return Page[Model]{}, fmt.Errorf("resources: scan model: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return Page[Model]{}, fmt.Errorf("resources: list models: %w", err)
	}
	page := Page[Model]{Data: items}
	if len(items) > limit {
		last := items[limit-1]
		lastID, _ := parseDecimalID(last.ID)
		next, err := r.cursors.encode("user-models", strconv.FormatInt(userID, 10), uint64(now+cursorLifetime), []db.CursorAtom{
			{Kind: db.CursorUint, Uint: uint64(last.UpdatedAt)}, {Kind: db.CursorUint, Uint: uint64(lastID)},
		})
		if err != nil {
			return Page[Model]{}, err
		}
		page.Data = items[:limit]
		page.NextCursor = &next
	}
	return page, nil
}

func (r *Repository) GetModel(ctx context.Context, userID, modelID int64) (Model, error) {
	if r == nil || userID <= 0 || modelID <= 0 {
		return Model{}, ErrNotFound
	}
	model, err := scanModel(r.db.QueryRowContext(ctx, modelSelect+` WHERE m.user_id=? AND m.id=?`, userID, modelID))
	if errors.Is(err, sql.ErrNoRows) {
		return Model{}, ErrNotFound
	}
	if err != nil {
		return Model{}, fmt.Errorf("resources: get model: %w", err)
	}
	return model, nil
}

func getModelTx(ctx context.Context, tx *sql.Tx, userID, modelID int64) (Model, error) {
	model, err := scanModel(tx.QueryRowContext(ctx, modelSelect+` WHERE m.user_id=? AND m.id=?`, userID, modelID))
	if errors.Is(err, sql.ErrNoRows) {
		return Model{}, ErrNotFound
	}
	if err != nil {
		return Model{}, fmt.Errorf("resources: read model: %w", err)
	}
	return model, nil
}

const reservedCharityProviderPrefix = "[公益]"

func validPersonalModelProvider(provider string) bool {
	return validateExactText(provider, 1, maxModelNameRunes) && !strings.HasPrefix(provider, reservedCharityProviderPrefix)
}

func validModelIdentity(provider, model string) bool {
	return validPersonalModelProvider(provider) && validateExactText(model, 1, maxModelNameRunes)
}

func validRouteStrategy(strategy string) bool {
	return strategy == "ordered" || strategy == "random"
}

func (r *Repository) CreateModel(ctx context.Context, userID int64, mutation ControlMutation, input CreateModelInput) (MutationResult[Model], error) {
	if input.RouteStrategy == "" {
		input.RouteStrategy = "ordered"
	}
	if r == nil || userID <= 0 || mutation.Route != routeModels || mutation.Method != http.MethodPost || !mutationPathIDs(mutation) || mutation.Query != "" ||
		!validModelIdentity(input.Provider, input.Model) || !validRouteStrategy(input.RouteStrategy) {
		return MutationResult[Model]{}, ErrInvalidRequest
	}
	now, err := r.nowUnix()
	if err != nil {
		return MutationResult[Model]{}, err
	}
	tx, err := r.beginAuthorizedTx(ctx, userID)
	if err != nil {
		return MutationResult[Model]{}, err
	}
	committed := false
	defer finishTx(tx, &committed)
	decision, err := beginControlMutation(ctx, tx, userID, mutation, now)
	if err != nil {
		return MutationResult[Model]{}, err
	}
	if decision.Kind == idempotency.Replay {
		return replayMutation[Model](decision)
	}
	modelLimit, err := readSiteLimitTx(ctx, tx, "default_model_limit", 1, 10000)
	if err != nil {
		return MutationResult[Model]{}, err
	}
	modelCount, err := countTx(ctx, tx, `SELECT count(*) FROM models WHERE user_id=?`, userID)
	if err != nil {
		return MutationResult[Model]{}, fmt.Errorf("resources: count models: %w", err)
	}
	if modelCount >= modelLimit {
		return MutationResult[Model]{}, ErrResourceLimit
	}
	result, err := tx.ExecContext(ctx, `
INSERT INTO models(user_id,provider,model,full_name,route_strategy,silent_retry,flatten_tool_calls,revision,binding_revision,created_at,updated_at)
SELECT ?,?,?,?,?,?,?,1,0,?,? WHERE EXISTS(SELECT 1 FROM users WHERE id=? AND is_admin=0)`,
		userID, input.Provider, input.Model, input.Provider+"/"+input.Model, input.RouteStrategy,
		boolInt(input.SilentRetry), boolInt(input.FlattenToolCalls), now, now, userID)
	if err != nil {
		return MutationResult[Model]{}, conflictOrError("create model", err)
	}
	inserted, _ := result.RowsAffected()
	if inserted != 1 {
		return MutationResult[Model]{}, ErrNotFound
	}
	modelID, err := result.LastInsertId()
	if err != nil {
		return MutationResult[Model]{}, fmt.Errorf("resources: create model identity: %w", err)
	}
	if input.FlattenToolCalls {
		if err := writePolicyAudit(ctx, tx, userID, "model", modelID, "flatten_tool_calls", false, true, now); err != nil {
			return MutationResult[Model]{}, err
		}
	}
	model, err := getModelTx(ctx, tx, userID, modelID)
	if err != nil {
		return MutationResult[Model]{}, err
	}
	if err := r.projection.ReconcileRoutingProjection(ctx, tx, userID, modelID); err != nil {
		return MutationResult[Model]{}, fmt.Errorf("resources: project new model routing: %w", err)
	}
	out, err := finishJSONMutation(ctx, tx, decision, http.StatusCreated, model)
	if err != nil {
		return MutationResult[Model]{}, err
	}
	if err := commitTx(tx, &committed); err != nil {
		return MutationResult[Model]{}, err
	}
	return out, nil
}

func (r *Repository) PatchModel(ctx context.Context, userID, modelID int64, mutation ControlMutation, input PatchModelInput) (MutationResult[Model], error) {
	if r == nil || userID <= 0 || modelID <= 0 || mutation.Route != routeModel || mutation.Method != http.MethodPatch || !mutationPathIDs(mutation, modelID) || mutation.Query != "" || input.ExpectedRevision < 1 ||
		(input.Provider == nil && input.Model == nil && input.RouteStrategy == nil && input.SilentRetry == nil && input.FlattenToolCalls == nil) ||
		(input.Provider != nil && !validPersonalModelProvider(*input.Provider)) ||
		(input.Model != nil && !validateExactText(*input.Model, 1, maxModelNameRunes)) ||
		(input.RouteStrategy != nil && !validRouteStrategy(*input.RouteStrategy)) {
		return MutationResult[Model]{}, ErrInvalidRequest
	}
	now, err := r.nowUnix()
	if err != nil {
		return MutationResult[Model]{}, err
	}
	tx, err := r.beginAuthorizedTx(ctx, userID)
	if err != nil {
		return MutationResult[Model]{}, err
	}
	committed := false
	defer finishTx(tx, &committed)
	decision, err := beginControlMutation(ctx, tx, userID, mutation, now)
	if err != nil {
		return MutationResult[Model]{}, err
	}
	if decision.Kind == idempotency.Replay {
		return replayMutation[Model](decision)
	}
	current, err := getModelTx(ctx, tx, userID, modelID)
	if err != nil {
		return MutationResult[Model]{}, err
	}
	revision, _ := strconv.ParseInt(current.Revision, 10, 64)
	if revision != input.ExpectedRevision || revision == int64(^uint64(0)>>1) {
		return MutationResult[Model]{}, ErrConflict
	}
	provider, modelName, strategy := current.Provider, current.Model, current.RouteStrategy
	silentRetry, flatten := current.SilentRetry, current.FlattenToolCalls
	if input.Provider != nil {
		provider = *input.Provider
	}
	if input.Model != nil {
		modelName = *input.Model
	}
	if input.RouteStrategy != nil {
		strategy = *input.RouteStrategy
	}
	if input.SilentRetry != nil {
		silentRetry = *input.SilentRetry
	}
	if input.FlattenToolCalls != nil {
		flatten = *input.FlattenToolCalls
	}
	if !current.FlattenToolCalls && flatten {
		incompatible, err := modelHasNonOpenAIBindingTx(ctx, tx, modelID)
		if err != nil {
			return MutationResult[Model]{}, err
		}
		if incompatible {
			return MutationResult[Model]{}, ErrConflict
		}
	}
	result, err := tx.ExecContext(ctx, `
UPDATE models SET provider=?,model=?,full_name=?,route_strategy=?,silent_retry=?,flatten_tool_calls=?,revision=revision+1,updated_at=?
WHERE id=? AND user_id=? AND revision=?`, provider, modelName, provider+"/"+modelName, strategy,
		boolInt(silentRetry), boolInt(flatten), now, modelID, userID, input.ExpectedRevision)
	if err != nil {
		return MutationResult[Model]{}, conflictOrError("patch model", err)
	}
	updated, _ := result.RowsAffected()
	if updated != 1 {
		return MutationResult[Model]{}, ErrConflict
	}
	if current.FlattenToolCalls != flatten {
		if err := writePolicyAudit(ctx, tx, userID, "model", modelID, "flatten_tool_calls", current.FlattenToolCalls, flatten, now); err != nil {
			return MutationResult[Model]{}, err
		}
	}
	model, err := getModelTx(ctx, tx, userID, modelID)
	if err != nil {
		return MutationResult[Model]{}, err
	}
	out, err := finishJSONMutation(ctx, tx, decision, http.StatusOK, model)
	if err != nil {
		return MutationResult[Model]{}, err
	}
	if err := commitTx(tx, &committed); err != nil {
		return MutationResult[Model]{}, err
	}
	return out, nil
}

func modelHasNonOpenAIBindingTx(ctx context.Context, tx *sql.Tx, modelID int64) (bool, error) {
	var incompatible int
	if err := tx.QueryRowContext(ctx, `
SELECT EXISTS(
 SELECT 1 FROM model_bindings b
 JOIN endpoint_keys k ON k.id=b.endpoint_key_id
 JOIN endpoints e ON e.id=k.endpoint_id
 WHERE b.model_id=? AND e.connector_type<>?
)`, modelID, string(connectorcontract.TypeOpenAICompatible)).Scan(&incompatible); err != nil {
		return false, fmt.Errorf("resources: validate model flatten bindings: %w", err)
	}
	return incompatible == 1, nil
}

func (r *Repository) DeleteModel(ctx context.Context, userID, modelID int64, mutation ControlMutation, expectedRevision int64) (MutationResult[struct{}], error) {
	if r == nil || userID <= 0 || modelID <= 0 || expectedRevision < 1 || mutation.Route != routeModel || mutation.Method != http.MethodDelete || !mutationPathIDs(mutation, modelID) || mutation.Query != "" {
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
	if _, err := getModelTx(ctx, tx, userID, modelID); err != nil {
		return MutationResult[struct{}]{}, err
	}
	if err := r.projection.PrepareModelDeletion(ctx, tx, userID, modelID, now); err != nil {
		return MutationResult[struct{}]{}, fmt.Errorf("resources: prepare model projection deletion: %w", err)
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM models WHERE id=? AND user_id=? AND revision=?`, modelID, userID, expectedRevision)
	if err != nil {
		return MutationResult[struct{}]{}, fmt.Errorf("resources: delete model: %w", err)
	}
	deleted, _ := result.RowsAffected()
	if deleted != 1 {
		return MutationResult[struct{}]{}, ErrConflict
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

func (r *Repository) ListBindings(ctx context.Context, userID, modelID int64) (BindingsResponse, error) {
	if r == nil || userID <= 0 || modelID <= 0 {
		return BindingsResponse{}, ErrNotFound
	}
	tx, err := beginTx(ctx, r.db)
	if err != nil {
		return BindingsResponse{}, err
	}
	defer tx.Rollback()
	model, err := getModelTx(ctx, tx, userID, modelID)
	if err != nil {
		return BindingsResponse{}, err
	}
	bindings, err := listBindingsTx(ctx, tx, userID, modelID)
	if err != nil {
		return BindingsResponse{}, err
	}
	return BindingsResponse{Bindings: bindings, BindingRevision: model.BindingRevision}, nil
}

func listBindingsTx(ctx context.Context, tx *sql.Tx, userID, modelID int64) ([]Binding, error) {
	rows, err := tx.QueryContext(ctx, `
SELECT b.id,b.endpoint_key_id,e.base_url,e.connector_type,e.note,k.display_head,k.display_tail,k.note,b.upstream_model_id,b.ord
FROM model_bindings b
JOIN models m ON m.id=b.model_id
JOIN endpoint_keys k ON k.id=b.endpoint_key_id
JOIN endpoints e ON e.id=k.endpoint_id
WHERE m.user_id=? AND m.id=? AND e.user_id=?
ORDER BY b.ord,b.id`, userID, modelID, userID)
	if err != nil {
		return nil, fmt.Errorf("resources: list bindings: %w", err)
	}
	defer rows.Close()
	items := make([]Binding, 0)
	for rows.Next() {
		var item Binding
		var id, keyID int64
		if err := rows.Scan(&id, &keyID, &item.EndpointBaseURL, &item.ConnectorType, &item.EndpointNote,
			&item.EndpointKeyDisplayHead, &item.EndpointKeyDisplayTail, &item.EndpointKeyNote,
			&item.UpstreamModelID, &item.Ord); err != nil {
			return nil, fmt.Errorf("resources: scan binding: %w", err)
		}
		var err error
		item.ID, err = decimalID(id)
		if err != nil {
			return nil, err
		}
		item.EndpointKeyID, err = decimalID(keyID)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("resources: list bindings: %w", err)
	}
	return items, nil
}

func (r *Repository) AddBindings(ctx context.Context, userID, modelID int64, mutation ControlMutation, expectedBindingRevision int64, selections []BindingSelection) (MutationResult[BindingsResponse], error) {
	if r == nil || userID <= 0 || modelID <= 0 || expectedBindingRevision < 0 || mutation.Route != routeBindingBatch || mutation.Method != http.MethodPost || !mutationPathIDs(mutation, modelID) || mutation.Query != "" || len(selections) == 0 || len(selections) > maxBindingBatch || !validBindingSelections(selections) {
		return MutationResult[BindingsResponse]{}, ErrInvalidRequest
	}
	now, err := r.nowUnix()
	if err != nil {
		return MutationResult[BindingsResponse]{}, err
	}
	tx, err := r.beginAuthorizedTx(ctx, userID)
	if err != nil {
		return MutationResult[BindingsResponse]{}, err
	}
	committed := false
	defer finishTx(tx, &committed)
	decision, err := beginControlMutation(ctx, tx, userID, mutation, now)
	if err != nil {
		return MutationResult[BindingsResponse]{}, err
	}
	if decision.Kind == idempotency.Replay {
		return replayMutation[BindingsResponse](decision)
	}
	model, err := getModelTx(ctx, tx, userID, modelID)
	if err != nil {
		return MutationResult[BindingsResponse]{}, err
	}
	currentRevision, _ := strconv.ParseInt(model.BindingRevision, 10, 64)
	currentCount, _ := strconv.ParseInt(model.BindingCount, 10, 64)
	if currentRevision != expectedBindingRevision || currentRevision == int64(^uint64(0)>>1) {
		return MutationResult[BindingsResponse]{}, ErrConflict
	}
	bindingLimit, err := readSiteLimitTx(ctx, tx, "default_binding_limit", 1, 10000)
	if err != nil {
		return MutationResult[BindingsResponse]{}, err
	}
	if bindingLimit > maxBindingBatch {
		bindingLimit = maxBindingBatch
	}
	if currentCount+int64(len(selections)) > bindingLimit {
		return MutationResult[BindingsResponse]{}, ErrResourceLimit
	}
	for index, selection := range selections {
		connectorType, eligible, err := bindingSelectionConnectorTx(ctx, tx, userID, selection.EndpointKeyID, selection.UpstreamModelID)
		if err != nil {
			return MutationResult[BindingsResponse]{}, err
		}
		if !eligible {
			return MutationResult[BindingsResponse]{}, ErrNotFound
		}
		if model.FlattenToolCalls && connectorType != string(connectorcontract.TypeOpenAICompatible) {
			return MutationResult[BindingsResponse]{}, ErrConflict
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO model_bindings(model_id,endpoint_key_id,upstream_model_id,ord,created_at,updated_at)
VALUES(?,?,?,?,?,?)`, modelID, selection.EndpointKeyID, selection.UpstreamModelID, int(currentCount)+index, now, now); err != nil {
			return MutationResult[BindingsResponse]{}, conflictOrError("create binding", err)
		}
	}
	if err := advanceBindingRevisionTx(ctx, tx, userID, modelID, expectedBindingRevision, now); err != nil {
		return MutationResult[BindingsResponse]{}, err
	}
	if err := r.projection.ReconcileRoutingProjection(ctx, tx, userID, modelID); err != nil {
		return MutationResult[BindingsResponse]{}, fmt.Errorf("resources: project added bindings: %w", err)
	}
	bindings, err := listBindingsTx(ctx, tx, userID, modelID)
	if err != nil {
		return MutationResult[BindingsResponse]{}, err
	}
	response := BindingsResponse{Bindings: bindings, BindingRevision: strconv.FormatInt(expectedBindingRevision+1, 10)}
	out, err := finishJSONMutation(ctx, tx, decision, http.StatusCreated, response)
	if err != nil {
		return MutationResult[BindingsResponse]{}, err
	}
	if err := commitTx(tx, &committed); err != nil {
		return MutationResult[BindingsResponse]{}, err
	}
	return out, nil
}

func validBindingSelections(selections []BindingSelection) bool {
	seen := make(map[string]struct{}, len(selections))
	for _, selection := range selections {
		if selection.EndpointKeyID <= 0 || !validateCatalogText(selection.UpstreamModelID, 1, 512) {
			return false
		}
		identity := strconv.FormatInt(selection.EndpointKeyID, 10) + "\x00" + selection.UpstreamModelID
		if _, duplicate := seen[identity]; duplicate {
			return false
		}
		seen[identity] = struct{}{}
	}
	return true
}

func bindingSelectionConnectorTx(ctx context.Context, tx *sql.Tx, userID, keyID int64, upstreamModelID string) (string, bool, error) {
	var connectorType string
	err := tx.QueryRowContext(ctx, `
SELECT e.connector_type
FROM model_pair_catalog p
JOIN endpoint_keys k ON k.id=p.endpoint_key_id
JOIN endpoints e ON e.id=k.endpoint_id
JOIN model_discovery_evidence d ON d.endpoint_key_id=k.id
WHERE e.user_id=? AND k.id=? AND p.normalized_model_id=?
  AND e.enabled=1 AND k.enabled=1
  AND NOT EXISTS(SELECT 1 FROM endpoint_key_suspensions s WHERE s.endpoint_key_id=k.id)
  AND (p.manual_supports>0 OR (p.automatic_supports>0 AND d.state='succeeded' AND d.revision=p.automatic_revision))`,
		userID, keyID, upstreamModelID).Scan(&connectorType)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("resources: verify binding candidate: %w", err)
	}
	return connectorType, true, nil
}

func advanceBindingRevisionTx(ctx context.Context, tx *sql.Tx, userID, modelID, expectedRevision, now int64) error {
	result, err := tx.ExecContext(ctx, `
UPDATE models SET binding_revision=binding_revision+1,updated_at=?
WHERE id=? AND user_id=? AND binding_revision=? AND binding_revision<9223372036854775807`,
		now, modelID, userID, expectedRevision)
	if err != nil {
		return fmt.Errorf("resources: advance model binding revision: %w", err)
	}
	updated, _ := result.RowsAffected()
	if updated != 1 {
		return ErrConflict
	}
	return nil
}

type bindingStorageRow struct {
	id, keyID, ord, createdAt, updatedAt int64
	upstreamModelID                      string
}

func readBindingStorageTx(ctx context.Context, tx *sql.Tx, userID, modelID int64) ([]bindingStorageRow, error) {
	rows, err := tx.QueryContext(ctx, `
SELECT b.id,b.endpoint_key_id,b.upstream_model_id,b.ord,b.created_at,b.updated_at
FROM model_bindings b JOIN models m ON m.id=b.model_id
WHERE m.user_id=? AND m.id=? ORDER BY b.ord,b.id`, userID, modelID)
	if err != nil {
		return nil, fmt.Errorf("resources: read binding storage: %w", err)
	}
	defer rows.Close()
	var items []bindingStorageRow
	for rows.Next() {
		var item bindingStorageRow
		if err := rows.Scan(&item.id, &item.keyID, &item.upstreamModelID, &item.ord, &item.createdAt, &item.updatedAt); err != nil {
			return nil, fmt.Errorf("resources: scan binding storage: %w", err)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func rebuildBindingOrderTx(ctx context.Context, tx *sql.Tx, modelID int64, rows []bindingStorageRow, order []int64, now int64) error {
	byID := make(map[int64]bindingStorageRow, len(rows))
	for _, row := range rows {
		byID[row.id] = row
	}
	if len(order) != len(rows) {
		return ErrConflict
	}
	seen := make(map[int64]struct{}, len(order))
	for _, id := range order {
		if _, duplicate := seen[id]; duplicate {
			return ErrConflict
		}
		if _, exists := byID[id]; !exists {
			return ErrConflict
		}
		seen[id] = struct{}{}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM model_bindings WHERE model_id=?`, modelID); err != nil {
		return fmt.Errorf("resources: clear binding order: %w", err)
	}
	for ordinal, id := range order {
		row := byID[id]
		if _, err := tx.ExecContext(ctx, `
INSERT INTO model_bindings(id,model_id,endpoint_key_id,upstream_model_id,ord,created_at,updated_at)
VALUES(?,?,?,?,?,?,?)`, row.id, modelID, row.keyID, row.upstreamModelID, ordinal, row.createdAt, now); err != nil {
			return fmt.Errorf("resources: rebuild binding order: %w", err)
		}
	}
	return nil
}

func (r *Repository) OrderBindings(ctx context.Context, userID, modelID int64, mutation ControlMutation, expectedBindingRevision int64, order []int64) (MutationResult[BindingsResponse], error) {
	if r == nil || userID <= 0 || modelID <= 0 || expectedBindingRevision < 0 || mutation.Route != routeBindingOrder || mutation.Method != http.MethodPut || !mutationPathIDs(mutation, modelID) || mutation.Query != "" || len(order) > maxBindingBatch {
		return MutationResult[BindingsResponse]{}, ErrInvalidRequest
	}
	for _, id := range order {
		if id <= 0 {
			return MutationResult[BindingsResponse]{}, ErrInvalidRequest
		}
	}
	now, err := r.nowUnix()
	if err != nil {
		return MutationResult[BindingsResponse]{}, err
	}
	tx, err := r.beginAuthorizedTx(ctx, userID)
	if err != nil {
		return MutationResult[BindingsResponse]{}, err
	}
	committed := false
	defer finishTx(tx, &committed)
	decision, err := beginControlMutation(ctx, tx, userID, mutation, now)
	if err != nil {
		return MutationResult[BindingsResponse]{}, err
	}
	if decision.Kind == idempotency.Replay {
		return replayMutation[BindingsResponse](decision)
	}
	model, err := getModelTx(ctx, tx, userID, modelID)
	if err != nil {
		return MutationResult[BindingsResponse]{}, err
	}
	currentRevision, _ := strconv.ParseInt(model.BindingRevision, 10, 64)
	if currentRevision != expectedBindingRevision || currentRevision == int64(^uint64(0)>>1) {
		return MutationResult[BindingsResponse]{}, ErrConflict
	}
	rows, err := readBindingStorageTx(ctx, tx, userID, modelID)
	if err != nil {
		return MutationResult[BindingsResponse]{}, err
	}
	if err := rebuildBindingOrderTx(ctx, tx, modelID, rows, order, now); err != nil {
		return MutationResult[BindingsResponse]{}, err
	}
	if err := advanceBindingRevisionTx(ctx, tx, userID, modelID, expectedBindingRevision, now); err != nil {
		return MutationResult[BindingsResponse]{}, err
	}
	bindings, err := listBindingsTx(ctx, tx, userID, modelID)
	if err != nil {
		return MutationResult[BindingsResponse]{}, err
	}
	response := BindingsResponse{Bindings: bindings, BindingRevision: strconv.FormatInt(expectedBindingRevision+1, 10)}
	out, err := finishJSONMutation(ctx, tx, decision, http.StatusOK, response)
	if err != nil {
		return MutationResult[BindingsResponse]{}, err
	}
	if err := commitTx(tx, &committed); err != nil {
		return MutationResult[BindingsResponse]{}, err
	}
	return out, nil
}

func (r *Repository) DeleteBinding(ctx context.Context, userID, modelID, bindingID int64, mutation ControlMutation, expectedBindingRevision int64) (MutationResult[BindingsResponse], error) {
	if r == nil || userID <= 0 || modelID <= 0 || bindingID <= 0 || expectedBindingRevision < 0 || mutation.Route != routeBinding || mutation.Method != http.MethodDelete || !mutationPathIDs(mutation, modelID, bindingID) || mutation.Query != "" {
		return MutationResult[BindingsResponse]{}, ErrInvalidRequest
	}
	now, err := r.nowUnix()
	if err != nil {
		return MutationResult[BindingsResponse]{}, err
	}
	tx, err := r.beginAuthorizedTx(ctx, userID)
	if err != nil {
		return MutationResult[BindingsResponse]{}, err
	}
	committed := false
	defer finishTx(tx, &committed)
	decision, err := beginControlMutation(ctx, tx, userID, mutation, now)
	if err != nil {
		return MutationResult[BindingsResponse]{}, err
	}
	if decision.Kind == idempotency.Replay {
		return replayMutation[BindingsResponse](decision)
	}
	model, err := getModelTx(ctx, tx, userID, modelID)
	if err != nil {
		return MutationResult[BindingsResponse]{}, err
	}
	currentRevision, _ := strconv.ParseInt(model.BindingRevision, 10, 64)
	if currentRevision != expectedBindingRevision || currentRevision == int64(^uint64(0)>>1) {
		return MutationResult[BindingsResponse]{}, ErrConflict
	}
	rows, err := readBindingStorageTx(ctx, tx, userID, modelID)
	if err != nil {
		return MutationResult[BindingsResponse]{}, err
	}
	found := false
	remainingCapacity := len(rows)
	if remainingCapacity > 0 {
		remainingCapacity--
	}
	remaining := make([]bindingStorageRow, 0, remainingCapacity)
	order := make([]int64, 0, remainingCapacity)
	for _, row := range rows {
		if row.id == bindingID {
			found = true
			continue
		}
		remaining = append(remaining, row)
		order = append(order, row.id)
	}
	if !found {
		return MutationResult[BindingsResponse]{}, ErrNotFound
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM model_bindings WHERE id=? AND model_id=?`, bindingID, modelID); err != nil {
		return MutationResult[BindingsResponse]{}, fmt.Errorf("resources: delete binding: %w", err)
	}
	if err := rebuildBindingOrderTx(ctx, tx, modelID, remaining, order, now); err != nil {
		return MutationResult[BindingsResponse]{}, err
	}
	if err := advanceBindingRevisionTx(ctx, tx, userID, modelID, expectedBindingRevision, now); err != nil {
		return MutationResult[BindingsResponse]{}, err
	}
	if err := r.projection.ReconcileRoutingProjection(ctx, tx, userID, modelID); err != nil {
		return MutationResult[BindingsResponse]{}, fmt.Errorf("resources: project deleted binding: %w", err)
	}
	bindings, err := listBindingsTx(ctx, tx, userID, modelID)
	if err != nil {
		return MutationResult[BindingsResponse]{}, err
	}
	response := BindingsResponse{Bindings: bindings, BindingRevision: strconv.FormatInt(expectedBindingRevision+1, 10)}
	out, err := finishJSONMutation(ctx, tx, decision, http.StatusOK, response)
	if err != nil {
		return MutationResult[BindingsResponse]{}, err
	}
	if err := commitTx(tx, &committed); err != nil {
		return MutationResult[BindingsResponse]{}, err
	}
	return out, nil
}

func (r *Repository) BindingCandidates(ctx context.Context, userID, modelID int64, query CandidateQuery) (Page[BindingCandidate], error) {
	query.Limit = normalizePageLimit(query.Limit)
	if r == nil || userID <= 0 || modelID <= 0 || !validPageLimit(query.Limit) ||
		(query.Source != "" && query.Source != "automatic" && query.Source != "manual") ||
		!validateSearchText(query.Query) {
		return Page[BindingCandidate]{}, ErrInvalidRequest
	}
	if _, err := r.GetModel(ctx, userID, modelID); err != nil {
		return Page[BindingCandidate]{}, err
	}
	if err := r.validateCandidateParents(ctx, userID, query.EndpointID, query.KeyID); err != nil {
		return Page[BindingCandidate]{}, err
	}
	now, err := r.nowUnix()
	if err != nil {
		return Page[BindingCandidate]{}, err
	}
	var afterEndpoint, afterKey, afterPair uint64
	if query.Cursor != "" {
		atoms, err := r.cursors.decode(query.Cursor, "binding-candidates", candidateCursorOwner(userID, modelID, query), uint64(now), db.CursorUint, db.CursorUint, db.CursorUint)
		if err != nil {
			return Page[BindingCandidate]{}, err
		}
		afterEndpoint, afterKey, afterPair = atoms[0].Uint, atoms[1].Uint, atoms[2].Uint
		if afterEndpoint == 0 || afterKey == 0 || afterPair == 0 || afterEndpoint > uint64(^uint64(0)>>1) || afterKey > uint64(^uint64(0)>>1) || afterPair > uint64(^uint64(0)>>1) {
			return Page[BindingCandidate]{}, ErrInvalidRequest
		}
	}
	rows, err := r.db.QueryContext(ctx, `
SELECT e.id,k.id,p.rowid,e.base_url,e.connector_type,e.note,k.display_head,k.display_tail,k.note,p.normalized_model_id,
       CASE WHEN p.automatic_supports>0 AND d.state='succeeded' AND d.revision=p.automatic_revision THEN 1 ELSE 0 END,
       CASE WHEN p.manual_supports>0 THEN 1 ELSE 0 END
FROM model_pair_catalog p
JOIN endpoint_keys k ON k.id=p.endpoint_key_id
JOIN endpoints e ON e.id=k.endpoint_id
JOIN model_discovery_evidence d ON d.endpoint_key_id=k.id
WHERE e.user_id=? AND e.enabled=1 AND k.enabled=1
  AND NOT EXISTS(SELECT 1 FROM endpoint_key_suspensions s WHERE s.endpoint_key_id=k.id)
  AND (p.manual_supports>0 OR (p.automatic_supports>0 AND d.state='succeeded' AND d.revision=p.automatic_revision))
  AND (?=0 OR e.id=?) AND (?=0 OR k.id=?)
  AND (?='' OR instr(p.normalized_model_id,?)>0)
  AND (?='' OR (?='automatic' AND p.automatic_supports>0 AND d.state='succeeded' AND d.revision=p.automatic_revision) OR (?='manual' AND p.manual_supports>0))
  AND (?=0 OR e.id>? OR (e.id=? AND (k.id>? OR (k.id=? AND p.rowid>?))))
ORDER BY e.id,k.id,p.rowid LIMIT ?`,
		userID, query.EndpointID, query.EndpointID, query.KeyID, query.KeyID,
		query.Query, query.Query, query.Source, query.Source, query.Source,
		afterPair, afterEndpoint, afterEndpoint, afterKey, afterKey, afterPair, query.Limit+1)
	if err != nil {
		return Page[BindingCandidate]{}, fmt.Errorf("resources: list binding candidates: %w", err)
	}
	defer rows.Close()
	type candidateWithSort struct {
		candidate         BindingCandidate
		endpointID, keyID int64
		pairRowID         int64
	}
	items := make([]candidateWithSort, 0, query.Limit+1)
	for rows.Next() {
		var item candidateWithSort
		var automatic, manual int
		if err := rows.Scan(&item.endpointID, &item.keyID, &item.pairRowID, &item.candidate.EndpointBaseURL, &item.candidate.ConnectorType,
			&item.candidate.EndpointNote, &item.candidate.EndpointKeyDisplayHead, &item.candidate.EndpointKeyDisplayTail,
			&item.candidate.EndpointKeyNote, &item.candidate.UpstreamModelID, &automatic, &manual); err != nil {
			return Page[BindingCandidate]{}, fmt.Errorf("resources: scan binding candidate: %w", err)
		}
		item.candidate.EndpointKeyID, err = decimalID(item.keyID)
		if err != nil {
			return Page[BindingCandidate]{}, err
		}
		item.candidate.SourceTypes = []string{}
		if automatic == 1 {
			item.candidate.SourceTypes = append(item.candidate.SourceTypes, "automatic")
		}
		if manual == 1 {
			item.candidate.SourceTypes = append(item.candidate.SourceTypes, "manual")
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return Page[BindingCandidate]{}, fmt.Errorf("resources: list binding candidates: %w", err)
	}
	page := Page[BindingCandidate]{Data: make([]BindingCandidate, 0, min(len(items), query.Limit))}
	visible := items
	if len(items) > query.Limit {
		visible = items[:query.Limit]
		last := visible[len(visible)-1]
		next, err := r.cursors.encode("binding-candidates", candidateCursorOwner(userID, modelID, query), uint64(now+cursorLifetime), []db.CursorAtom{
			{Kind: db.CursorUint, Uint: uint64(last.endpointID)}, {Kind: db.CursorUint, Uint: uint64(last.keyID)},
			{Kind: db.CursorUint, Uint: uint64(last.pairRowID)},
		})
		if err != nil {
			return Page[BindingCandidate]{}, err
		}
		page.NextCursor = &next
	}
	for _, item := range visible {
		page.Data = append(page.Data, item.candidate)
	}
	return page, nil
}

func (r *Repository) validateCandidateParents(ctx context.Context, userID, endpointID, keyID int64) error {
	if endpointID < 0 || keyID < 0 {
		return ErrInvalidRequest
	}
	if endpointID == 0 && keyID == 0 {
		return nil
	}
	var count int
	if keyID != 0 {
		query := `SELECT count(*) FROM endpoint_keys k JOIN endpoints e ON e.id=k.endpoint_id WHERE e.user_id=? AND k.id=?`
		args := []any{userID, keyID}
		if endpointID != 0 {
			query += ` AND e.id=?`
			args = append(args, endpointID)
		}
		if err := r.db.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
			return fmt.Errorf("resources: verify candidate key: %w", err)
		}
	} else {
		if err := r.db.QueryRowContext(ctx, `SELECT count(*) FROM endpoints WHERE user_id=? AND id=?`, userID, endpointID).Scan(&count); err != nil {
			return fmt.Errorf("resources: verify candidate endpoint: %w", err)
		}
	}
	if count != 1 {
		return ErrNotFound
	}
	return nil
}

func candidateCursorOwner(userID, modelID int64, query CandidateQuery) string {
	identity := strings.Join([]string{
		strconv.FormatInt(userID, 10), strconv.FormatInt(modelID, 10),
		strconv.FormatInt(query.EndpointID, 10), strconv.FormatInt(query.KeyID, 10), query.Source, query.Query,
	}, "\x00")
	digest := sha256.Sum256([]byte(identity))
	return hex.EncodeToString(digest[:])
}

func validateSearchText(value string) bool {
	return validateFreeText(value, 0, 512)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
