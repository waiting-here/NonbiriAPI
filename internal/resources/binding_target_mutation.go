package resources

import (
	"context"
	"database/sql"
	"math"
	"net/http"
	"strconv"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

const routeBindingTargets = "/api/models/{id}/bindings/{bId}/targets"

type storedBindingTarget struct {
	id, revision, mask int64
	ord                int
}

func (r *Repository) ReplaceBindingTargets(ctx context.Context, userID, modelID, bindingID int64, mutation ControlMutation, expectedBindingRevision int64, targets []BindingTargetSelection) (MutationResult[BindingTargetsReceipt], error) {
	if r == nil || userID <= 0 || modelID <= 0 || bindingID <= 0 || expectedBindingRevision < 0 || !validTargetSelections(targets) || !validResourceMutation(mutation, http.MethodPut, routeBindingTargets, modelID, bindingID) {
		return MutationResult[BindingTargetsReceipt]{}, ErrInvalidRequest
	}
	return ownerResourceMutation(ctx, r, userID, mutation, http.StatusOK, "", func(ctx context.Context, tx *sql.Tx, now int64) (BindingTargetsReceipt, ResourceOperationResult, error) {
		model, err := r.getModelTx(ctx, tx, userID, modelID)
		if err != nil {
			return BindingTargetsReceipt{}, ResourceOperationResult{}, err
		}
		if model.BindingRevision != strconv.FormatInt(expectedBindingRevision, 10) || expectedBindingRevision == math.MaxInt64 {
			return BindingTargetsReceipt{}, ResourceOperationResult{}, ErrConflict
		}
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM model_bindings WHERE id=? AND model_id=?)`, bindingID, modelID).Scan(&exists); err != nil {
			return BindingTargetsReceipt{}, ResourceOperationResult{}, err
		}
		if !exists {
			return BindingTargetsReceipt{}, ResourceOperationResult{}, ErrNotFound
		}
		limit, err := readSiteLimitTx(ctx, tx, "default_binding_limit", 1, 10000)
		if err != nil {
			return BindingTargetsReceipt{}, ResourceOperationResult{}, err
		}
		count, err := countTx(ctx, tx, `SELECT count(*) FROM model_binding_targets t JOIN model_bindings b ON b.id=t.binding_id WHERE b.model_id=? AND b.id<>?`, modelID, bindingID)
		if err != nil {
			return BindingTargetsReceipt{}, ResourceOperationResult{}, err
		}
		if count+int64(len(targets)) > minBindingCapacity(limit) {
			return BindingTargetsReceipt{}, ResourceOperationResult{}, ErrResourceLimit
		}
		if err := replaceBindingTargetsTx(ctx, tx, userID, modelID, bindingID, targets, now); err != nil {
			return BindingTargetsReceipt{}, ResourceOperationResult{}, err
		}
		if err := advanceBindingRevisionTx(ctx, tx, userID, modelID, expectedBindingRevision, now); err != nil {
			return BindingTargetsReceipt{}, ResourceOperationResult{}, err
		}
		if err := r.projection.ReconcileRoutingProjection(ctx, tx, userID, modelID); err != nil {
			return BindingTargetsReceipt{}, ResourceOperationResult{}, err
		}
		receipt := BindingTargetsReceipt{BindingID: strconv.FormatInt(bindingID, 10), BindingRevision: strconv.FormatInt(expectedBindingRevision+1, 10), Targets: []BindingTargetReceipt{}}
		rows, err := tx.QueryContext(ctx, "SELECT id,revision FROM model_binding_targets WHERE binding_id=? ORDER BY ord,id", bindingID)
		if err != nil {
			return receipt, ResourceOperationResult{}, err
		}
		defer rows.Close()
		for rows.Next() {
			var id, revision int64
			if err := rows.Scan(&id, &revision); err != nil {
				return receipt, ResourceOperationResult{}, err
			}
			receipt.Targets = append(receipt.Targets, BindingTargetReceipt{ID: strconv.FormatInt(id, 10), Revision: strconv.FormatInt(revision, 10)})
		}
		return receipt, ResourceOperationResult{}, rows.Err()
	})
}

func replaceBindingTargetsTx(ctx context.Context, tx *sql.Tx, userID, modelID, bindingID int64, targets []BindingTargetSelection, now int64) error {
	rows, err := tx.QueryContext(ctx, `SELECT id,revision,endpoint_key_id,upstream_model_id,native_service_mask,ord FROM model_binding_targets WHERE binding_id=? ORDER BY ord,id`, bindingID)
	if err != nil {
		return err
	}
	previous := map[string]storedBindingTarget{}
	for rows.Next() {
		var item storedBindingTarget
		var keyID int64
		var upstream string
		if err := rows.Scan(&item.id, &item.revision, &keyID, &upstream, &item.mask, &item.ord); err != nil {
			rows.Close()
			return err
		}
		previous[strconv.FormatInt(keyID, 10)+"/"+upstream] = item
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if len(previous) == 0 {
		return ErrUnavailable
	}
	if _, err := tx.ExecContext(ctx, `UPDATE model_binding_targets SET ord=ord+256 WHERE binding_id=?`, bindingID); err != nil {
		return err
	}
	for ord, target := range targets {
		key := strconv.FormatInt(target.EndpointKeyID, 10) + "/" + target.UpstreamModelID
		old, exists := previous[key]
		if !exists {
			if err := insertBindingTargetTx(ctx, tx, userID, modelID, bindingID, target, ord, now); err != nil {
				return err
			}
			continue
		}
		selected, _ := contract.ParseNativeServices(target.NativeServices)
		changed := old.mask != selected.Mask() || old.ord != ord
		if changed && old.revision == math.MaxInt64 {
			return ErrConflict
		}
		if selected.Mask()&^old.mask != 0 {
			if _, err := verifyBindingTargetTx(ctx, tx, userID, target); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE model_binding_targets SET native_service_mask=?,ord=?,revision=revision+?,updated_at=CASE WHEN ? THEN ? ELSE updated_at END,selection_origin=CASE WHEN ? THEN 'user' ELSE selection_origin END,selection_revision=CASE WHEN ? THEN NULL ELSE selection_revision END WHERE id=? AND binding_id=?`, selected.Mask(), ord, boolInt(changed), changed, now, old.mask != selected.Mask(), old.mask != selected.Mask(), old.id, bindingID); err != nil {
			return err
		}
		delete(previous, key)
	}
	for _, old := range previous {
		if _, err := tx.ExecContext(ctx, `DELETE FROM model_binding_targets WHERE id=? AND binding_id=?`, old.id, bindingID); err != nil {
			return err
		}
	}
	return nil
}

func minBindingCapacity(limit int64) int64 {
	if limit > maxBindingBatch {
		return maxBindingBatch
	}
	return limit
}

type BindingTargetReceipt struct {
	ID       string `json:"id"`
	Revision string `json:"revision"`
}
type BindingTargetsReceipt struct {
	BindingRevision string                 `json:"binding_revision"`
	BindingID       string                 `json:"binding_id"`
	Targets         []BindingTargetReceipt `json:"targets"`
}
