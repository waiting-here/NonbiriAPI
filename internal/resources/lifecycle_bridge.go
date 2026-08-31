package resources

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
)

func (r *Repository) reconcileRoutingTargetsTx(
	ctx context.Context,
	tx *sql.Tx,
	ownerUserID int64,
	targets []bindingRevisionTarget,
) error {
	for _, target := range targets {
		if err := r.projection.ReconcileRoutingProjection(ctx, tx, ownerUserID, target.modelID); err != nil {
			return fmt.Errorf("resources: reconcile routing projection for model %d: %w", target.modelID, err)
		}
	}
	return nil
}

func (r *Repository) reconcileRoutingModelIDsTx(
	ctx context.Context,
	tx *sql.Tx,
	ownerUserID int64,
	modelIDs []int64,
) error {
	unique := make(map[int64]struct{}, len(modelIDs))
	for _, modelID := range modelIDs {
		if modelID <= 0 {
			return ErrInvalidRequest
		}
		unique[modelID] = struct{}{}
	}
	ordered := make([]int64, 0, len(unique))
	for modelID := range unique {
		ordered = append(ordered, modelID)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	for _, modelID := range ordered {
		if err := r.projection.ReconcileRoutingProjection(ctx, tx, ownerUserID, modelID); err != nil {
			return fmt.Errorf("resources: reconcile routing projection for model %d: %w", modelID, err)
		}
	}
	return nil
}

func modelsUsingKeyPairsTx(
	ctx context.Context,
	tx *sql.Tx,
	ownerUserID, endpointKeyID int64,
	upstreamModelIDs []string,
) ([]int64, error) {
	if len(upstreamModelIDs) == 0 {
		return []int64{}, nil
	}
	unique := make(map[string]struct{}, len(upstreamModelIDs))
	for _, modelID := range upstreamModelIDs {
		if !validateCatalogText(modelID, 1, 512) {
			return nil, ErrInvalidRequest
		}
		unique[modelID] = struct{}{}
	}
	ordered := make([]string, 0, len(unique))
	for modelID := range unique {
		ordered = append(ordered, modelID)
	}
	sort.Strings(ordered)
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ordered)), ",")
	arguments := make([]any, 0, len(ordered)+2)
	arguments = append(arguments, ownerUserID, endpointKeyID)
	for _, modelID := range ordered {
		arguments = append(arguments, modelID)
	}
	rows, err := tx.QueryContext(ctx, `
SELECT DISTINCT m.id
FROM model_bindings b JOIN models m ON m.id=b.model_id
WHERE m.user_id=? AND b.endpoint_key_id=? AND b.upstream_model_id IN (`+placeholders+`)
ORDER BY m.id`, arguments...)
	if err != nil {
		return nil, fmt.Errorf("resources: list catalog-affected models: %w", err)
	}
	defer rows.Close()
	modelIDs := make([]int64, 0)
	for rows.Next() {
		var modelID int64
		if err := rows.Scan(&modelID); err != nil {
			return nil, fmt.Errorf("resources: scan catalog-affected model: %w", err)
		}
		if modelID <= 0 {
			return nil, ErrUnavailable
		}
		modelIDs = append(modelIDs, modelID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("resources: iterate catalog-affected models: %w", err)
	}
	return modelIDs, nil
}

func (r *Repository) reconcileDiscoveryKeyTx(ctx context.Context, tx *sql.Tx, endpointKeyID int64) error {
	var ownerUserID int64
	if err := tx.QueryRowContext(ctx, `
SELECT e.user_id FROM endpoint_keys k JOIN endpoints e ON e.id=k.endpoint_id WHERE k.id=?`, endpointKeyID).Scan(&ownerUserID); err != nil {
		if err == sql.ErrNoRows {
			return ErrNotFound
		}
		return fmt.Errorf("resources: read discovery projection owner: %w", err)
	}
	if err := r.projection.ReconcileModelDiscovery(ctx, tx, ownerUserID, endpointKeyID); err != nil {
		return fmt.Errorf("resources: reconcile model discovery projection: %w", err)
	}
	targets, err := bindingRevisionTargetsForKeysTx(ctx, tx, ownerUserID, []int64{endpointKeyID})
	if err != nil {
		return err
	}
	return r.reconcileRoutingTargetsTx(ctx, tx, ownerUserID, targets)
}

// DeleteEndpointKeyForReport is the narrow transaction capability used by an
// already-approved report worker. The report owner supplies the transaction
// and frozen owner/key identity; this method performs integrated cleanup without
// starting a second authorization or transaction and never commits.
func (r *Repository) DeleteEndpointKeyForReport(
	ctx context.Context,
	tx *sql.Tx,
	ownerUserID, endpointKeyID, decisionNow int64,
) error {
	if r == nil || ctx == nil || tx == nil || ownerUserID <= 0 || endpointKeyID <= 0 ||
		decisionNow < 0 || decisionNow > maxUnixSecond {
		return ErrInvalidRequest
	}
	var endpointID, secretRef int64
	if err := tx.QueryRowContext(ctx, `
SELECT k.endpoint_id,k.secret_ref_id
FROM endpoint_keys k JOIN endpoints e ON e.id=k.endpoint_id
WHERE k.id=? AND e.user_id=?`, endpointKeyID, ownerUserID).Scan(&endpointID, &secretRef); err != nil {
		if err == sql.ErrNoRows {
			return ErrNotFound
		}
		return fmt.Errorf("resources: read approved endpoint key: %w", err)
	}
	targets, err := bindingRevisionTargetsForKeysTx(ctx, tx, ownerUserID, []int64{endpointKeyID})
	if err != nil {
		return err
	}
	if err := validateBindingRevisionTargets(targets); err != nil {
		return err
	}
	if err := r.projection.PrepareEndpointKeyDeletion(ctx, tx, ownerUserID, []int64{endpointKeyID}, decisionNow); err != nil {
		return fmt.Errorf("resources: prepare approved endpoint key projection deletion: %w", err)
	}
	if err := r.keyDeletion.PrepareEndpointKeyDeletion(ctx, tx, ownerUserID, []int64{endpointKeyID}, decisionNow); err != nil {
		return fmt.Errorf("resources: prepare approved endpoint key deletion: %w", err)
	}
	if err := advanceBindingRevisionsForDeletionTx(ctx, tx, ownerUserID, targets, decisionNow); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM endpoint_keys WHERE id=? AND endpoint_id=?`, endpointKeyID, endpointID)
	if err != nil {
		return fmt.Errorf("resources: delete approved endpoint key: %w", err)
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("resources: observe approved endpoint key deletion: %w", err)
	}
	if deleted != 1 {
		return ErrConflict
	}
	if err := r.secrets.MarkEndpointSecretOrphaned(ctx, tx, secretRef, decisionNow); err != nil {
		return fmt.Errorf("resources: orphan approved endpoint secret: %w", err)
	}
	return r.reconcileRoutingTargetsTx(ctx, tx, ownerUserID, targets)
}
