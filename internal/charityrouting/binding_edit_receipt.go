package charityrouting

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"net/http"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/charityscope"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

type BindingRevisionReceipt struct {
	BindingRevision string `json:"binding_revision"`
}

func compactBindingResult(result resources.MutationResult[BindingRevisionReceipt]) resources.MutationResult[AdminBindings] {
	return resources.MutationResult[AdminBindings]{Value: AdminBindings{BindingRevision: result.Value.BindingRevision}, Status: result.Status, Body: result.Body, Replayed: result.Replayed}
}

func replayBindingEdit(ctx context.Context, tx *sql.Tx, decision idempotency.Decision, scope charityscope.Scope, now int64, response string) (resources.MutationResult[AdminBindings], error) {
	if response == "receipt" {
		result, err := replay[BindingRevisionReceipt](decision)
		return compactBindingResult(result), err
	}
	return replayScopedBindings(ctx, tx, decision, scope, now)
}

func finishBindingEdit(ctx context.Context, tx *sql.Tx, decision idempotency.Decision, modelID, revision int64, response string, scope charityscope.Scope, now int64, sources ...[]bindingReplaySource) (resources.MutationResult[AdminBindings], error) {
	if response == "receipt" {
		result, err := finishBindingEditJSON(ctx, tx, decision, BindingRevisionReceipt{strconv.FormatInt(revision, 10)}, sources...)
		return compactBindingResult(result), err
	}
	value, err := readAdminBindingsTx(ctx, tx, modelID)
	if err != nil {
		return resources.MutationResult[AdminBindings]{}, err
	}
	value, err = scopedBindingsTx(ctx, tx, scope, value, now)
	if err != nil {
		return resources.MutationResult[AdminBindings]{}, err
	}
	return finishBindingEditJSON(ctx, tx, decision, value, sources...)
}

// Delete receipts retain only the source identities needed to recheck a mapping
// that the successful request itself removed. The envelope is never sent to clients.
type bindingReplaySource struct {
	DonationID int64 `json:"donation_id"`
	KeyID      int64 `json:"key_id"`
}
type storedBindingDelete struct {
	Response json.RawMessage       `json:"binding_delete_response"`
	Sources  []bindingReplaySource `json:"source_keys"`
}

func bindingReplaySourcesTx(ctx context.Context, tx *sql.Tx, modelID, bindingID int64) ([]bindingReplaySource, error) {
	rows, err := tx.QueryContext(ctx, `SELECT dk.donation_id,t.donation_key_id FROM charity_model_bindings b JOIN charity_binding_targets t ON t.binding_id=b.id JOIN donation_keys dk ON dk.id=t.donation_key_id WHERE b.charity_model_id=? AND b.id=? ORDER BY t.id LIMIT ?`, modelID, bindingID, maxBindingBatch+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []bindingReplaySource{}
	for rows.Next() {
		var source bindingReplaySource
		if err := rows.Scan(&source.DonationID, &source.KeyID); err != nil {
			return nil, err
		}
		out = append(out, source)
	}
	if len(out) > maxBindingBatch {
		return nil, ErrInvariant
	}
	return out, rows.Err()
}
func finishBindingEditJSON[T any](ctx context.Context, tx *sql.Tx, decision idempotency.Decision, value T, sources ...[]bindingReplaySource) (resources.MutationResult[T], error) {
	if len(sources) == 0 {
		return finishJSON(ctx, tx, decision, http.StatusOK, value)
	}
	empty := resources.MutationResult[T]{}
	if len(sources) != 1 || len(sources[0]) == 0 || len(sources[0]) > maxBindingBatch {
		return empty, ErrInvariant
	}
	body, err := json.Marshal(value)
	if err != nil {
		return empty, ErrInvariant
	}
	stored, err := json.Marshal(storedBindingDelete{Response: body, Sources: sources[0]})
	if err != nil || len(stored) > idempotency.MaxResponseBytes {
		return empty, ErrResourceLimit
	}
	if err := idempotency.Complete(ctx, tx, decision, http.StatusOK, stored); err != nil {
		return empty, err
	}
	return resources.MutationResult[T]{Value: value, Status: http.StatusOK, Body: body}, nil
}
func replayDeletedBinding(ctx context.Context, tx *sql.Tx, decision idempotency.Decision, scope charityscope.Scope, now int64, response string) (resources.MutationResult[AdminBindings], error) {
	empty := resources.MutationResult[AdminBindings]{}
	var stored storedBindingDelete
	if err := json.Unmarshal(decision.ResponseBody, &stored); err != nil {
		return empty, ErrInvariant
	}
	if stored.Response == nil {
		// Old receipts lack the deleted relation and cannot establish restricted scope.
		if scope.RestrictedKeys() {
			return empty, authz.ErrPermissionDenied
		}
		return replayBindingEdit(ctx, tx, decision, scope, now, response)
	}
	if len(stored.Sources) == 0 || len(stored.Sources) > maxBindingBatch {
		return empty, ErrInvariant
	}
	if scope.RestrictedKeys() {
		if scope.DonationScope == authz.DonationScopeNone {
			return empty, authz.ErrPermissionDenied
		}
		var mainstream bool
		if err := tx.QueryRowContext(ctx, `SELECT is_mainstream FROM charity_models WHERE id=?`, scope.ModelID).Scan(&mainstream); err != nil {
			return empty, err
		}
		sourceScope := scope
		// The original mapping remains a valid read-only standard receipt anchor
		// while its model is still mainstream and its source still exists.
		if mainstream {
			sourceScope.DonationScope = authz.DonationScopeFull
		}
		for _, source := range stored.Sources {
			if err := sourceScope.RequireKey(ctx, tx, source.DonationID, source.KeyID, now, false); err != nil {
				return empty, managementScopeError(err)
			}
		}
	}
	decision.ResponseBody = stored.Response
	return replayBindingEdit(ctx, tx, decision, scope, now, response)
}
