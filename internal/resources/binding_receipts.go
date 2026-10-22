package resources

import (
	"context"
	"database/sql"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"net/http"
)

const routeBindingBatchReceipt = "/api/models/{id}/bindings/batch-receipt"

type BindingBatchReceipt struct {
	ModelID         string `json:"model_id"`
	BindingRevision string `json:"binding_revision"`
	AddedCount      string `json:"added_count"`
	BindingCount    string `json:"binding_count"`
}
type BindingRevisionReceipt struct {
	BindingRevision string `json:"binding_revision"`
}

func finishBindingReceipt(ctx context.Context, tx *sql.Tx, decision idempotency.Decision, status int, revision string, receipt any) (MutationResult[BindingsResponse], error) {
	out, err := finishJSONMutation(ctx, tx, decision, status, receipt)
	return MutationResult[BindingsResponse]{Value: BindingsResponse{BindingRevision: revision}, Status: out.Status, Body: out.Body}, err
}
func (api *httpAPI) addBindings(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	api.addBindingsWithReceipt(writer, request, principal, false)
}
func (api *httpAPI) addBindingsReceipt(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	api.addBindingsWithReceipt(writer, request, principal, true)
}
