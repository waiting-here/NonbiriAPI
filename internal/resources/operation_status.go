package resources

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/httpapi"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
)

const routeOperationStatus = "/api/resource-operation-status"
const maxOperationStatusBytes = 16 * 1024

// ResourceOperationResult contains only identifiers needed to read current resources.
type ResourceOperationResult struct {
	EndpointID      string   `json:"endpoint_id,omitempty"`
	EndpointKeyID   string   `json:"endpoint_key_id,omitempty"`
	ModelID         string   `json:"model_id,omitempty"`
	CatalogEntryIDs []string `json:"catalog_entry_ids,omitempty"`
	BindingIDs      []string `json:"binding_ids,omitempty"`
	OperationID     string   `json:"operation_id,omitempty"`
}

// ResourceOperationStatus is a receipt projection, not evidence of current success.
// Missing or expired receipts do not establish that a mutation rolled back.
type ResourceOperationStatus struct {
	Status string                   `json:"status"`
	Stage  string                   `json:"stage,omitempty"`
	Result *ResourceOperationResult `json:"result,omitempty"`
}

func recordResourceOperation(ctx context.Context, tx *sql.Tx, userID int64, mutation ControlMutation, scope idempotency.Scope, stage string, result ResourceOperationResult) error {
	actor, err := actorHash(userID)
	if err != nil {
		return err
	}
	key, err := idempotency.KeyHash(mutation.IdempotencyKey)
	if err != nil {
		return ErrInvalidRequest
	}
	body, err := json.Marshal(result)
	if err != nil || len(body) > maxOperationStatusBytes {
		return ErrResourceLimit
	}
	status := "recorded"
	if stage == "catalog_refresh" {
		status = "in_progress"
	}
	// Copy the original replay window. Key reuse after expiry may replace the old
	// projection; a live receipt is immutable even across replay namespaces.
	_, err = tx.ExecContext(ctx, `
INSERT INTO resource_operation_status(actor_scope_hash,key_hash,user_id,stage,status,result_json,created_at,expires_at)
SELECT actor_scope_hash,key_hash,?, ?, ?, ?,created_at,expires_at
FROM idempotency_records
WHERE scope=? AND actor_scope_hash=? AND key_hash=? AND state='completed'
ON CONFLICT(actor_scope_hash,key_hash) DO UPDATE SET
 user_id=excluded.user_id,stage=excluded.stage,status=excluded.status,
 result_json=excluded.result_json,created_at=excluded.created_at,expires_at=excluded.expires_at
WHERE resource_operation_status.expires_at<=excluded.created_at`, userID, stage, status, string(body), string(scope), actor[:], key[:])
	if err != nil {
		return fmt.Errorf("resources: record operation status: %w", err)
	}
	return nil
}

func (r *Repository) GetResourceOperationStatus(ctx context.Context, userID int64, operationKey string) (ResourceOperationStatus, error) {
	actor, err := actorHash(userID)
	if err != nil {
		return ResourceOperationStatus{}, ErrInvalidRequest
	}
	key, err := idempotency.KeyHash(operationKey)
	if err != nil {
		return ResourceOperationStatus{}, ErrInvalidRequest
	}
	tx, err := r.beginAuthorizedTx(ctx, userID)
	if err != nil {
		return ResourceOperationStatus{}, err
	}
	defer tx.Rollback()
	now, err := r.nowUnix()
	if err != nil {
		return ResourceOperationStatus{}, err
	}
	var stage, status, body string
	var expires int64
	err = tx.QueryRowContext(ctx, `SELECT stage,status,result_json,expires_at FROM resource_operation_status WHERE actor_scope_hash=? AND key_hash=? AND user_id=?`, actor[:], key[:], userID).Scan(&stage, &status, &body, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return ResourceOperationStatus{Status: "not_recorded"}, nil
	}
	if err != nil {
		return ResourceOperationStatus{}, fmt.Errorf("resources: read operation status: %w", err)
	}
	if expires <= now {
		return ResourceOperationStatus{Status: "expired"}, nil
	}
	// The public lookup has no scope selector. Preserve existing replay semantics
	// without attributing a key reused in two namespaces to the wrong operation.
	var liveScopes int
	err = tx.QueryRowContext(ctx, `SELECT count(*) FROM idempotency_records WHERE actor_scope_hash=? AND key_hash=? AND scope IN ('control_mutation','model_discovery') AND expires_at>?`, actor[:], key[:], now).Scan(&liveScopes)
	if err != nil {
		return ResourceOperationStatus{}, fmt.Errorf("resources: inspect operation identity: %w", err)
	}
	if liveScopes > 1 {
		return ResourceOperationStatus{}, ErrConflict
	}
	var result ResourceOperationResult
	if len(body) > maxOperationStatusBytes || json.Unmarshal([]byte(body), &result) != nil {
		return ResourceOperationStatus{}, ErrUnavailable
	}
	if stage == "catalog_refresh" && status == "in_progress" {
		if !db.ValidateOpaqueID(result.OperationID, "op_") {
			return ResourceOperationStatus{}, ErrUnavailable
		}
		var state string
		err = tx.QueryRowContext(ctx, `SELECT state FROM accepted_operations WHERE id=? AND kind='model_discovery' AND actor_user_id=? AND actor_role='user'`, result.OperationID, userID).Scan(&state)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return ResourceOperationStatus{}, fmt.Errorf("resources: read discovery operation status: %w", err)
		}
		// A missing continuation cannot establish completion. The committed
		// acceptance receipt remains available for current-resource checks.
		if err == nil && state != "accepted" && state != "running" {
			status = "recorded"
		}
	}
	return ResourceOperationStatus{Status: status, Stage: stage, Result: &result}, nil
}

func (api *httpAPI) resourceOperationStatus(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	writer.Header().Set("Cache-Control", "no-store")
	if !requireEmptyQuery(writer, request) {
		return
	}
	var input struct {
		OperationKey string `json:"operation_key"`
	}
	_, err := httpapi.ReadJSON(writer, request, &input, httpapi.BodyOptions{MaxBytes: maxOperationStatusBytes, Validate: validateResourceJSONObject})
	if err != nil {
		if errors.Is(err, httpapi.ErrTooLarge) {
			httperr.WriteError(writer, httperr.New(httperr.CodePayloadTooLarge, "request body is too large"))
		} else {
			writeResourceError(writer, ErrInvalidRequest)
		}
		return
	}
	result, err := api.repository.GetResourceOperationStatus(request.Context(), principal.UserID, input.OperationKey)
	if err != nil {
		writeResourceError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, result)
}

func catalogOperationIDs(entries []CatalogEntry) []string {
	ids := make([]string, len(entries))
	for i, entry := range entries {
		ids[i] = entry.ID
	}
	return ids
}

func bindingOperationIDs(bindings []Binding) []string {
	ids := make([]string, len(bindings))
	for i, binding := range bindings {
		ids[i] = binding.ID
	}
	return ids
}
