package charityrouting

import (
	"context"
	"database/sql"
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

func finishBindingEdit(ctx context.Context, tx *sql.Tx, decision idempotency.Decision, modelID, revision int64, response string) (resources.MutationResult[AdminBindings], error) {
	if response == "receipt" {
		result, err := finishJSON(ctx, tx, decision, http.StatusOK, BindingRevisionReceipt{strconv.FormatInt(revision, 10)})
		return compactBindingResult(result), err
	}
	value, err := readAdminBindingsTx(ctx, tx, modelID)
	if err != nil {
		return resources.MutationResult[AdminBindings]{}, err
	}
	return finishJSON(ctx, tx, decision, http.StatusOK, value)
}
