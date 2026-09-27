package resources

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/requestadaptation"
)

func resourceAdaptationError(err error) error {
	switch {
	case errors.Is(err, requestadaptation.ErrInvalid):
		return ErrInvalidRequest
	case errors.Is(err, requestadaptation.ErrConflict):
		return ErrConflict
	default:
		return ErrUnavailable
	}
}

func (r *Repository) GetEndpointAdaptation(ctx context.Context, userID, endpointID int64) (requestadaptation.Projection, error) {
	if r == nil || r.adaptations == nil || ctx == nil || userID <= 0 || endpointID <= 0 {
		return requestadaptation.Projection{}, ErrInvalidRequest
	}
	tx, err := beginTx(ctx, r.db)
	if err != nil {
		return requestadaptation.Projection{}, err
	}
	defer tx.Rollback()
	if _, err := getEndpointTx(ctx, tx, userID, endpointID); err != nil {
		return requestadaptation.Projection{}, err
	}
	snapshot, err := r.adaptations.LoadTx(ctx, tx, requestadaptation.Ref{Scope: requestadaptation.ScopeEndpoint, ID: endpointID})
	if err != nil {
		return requestadaptation.Projection{}, resourceAdaptationError(err)
	}
	defer snapshot.Clear()
	projection := snapshot.Projection()
	if err := tx.Commit(); err != nil {
		return requestadaptation.Projection{}, ErrUnavailable
	}
	return projection, nil
}

func (r *Repository) PutEndpointAdaptation(ctx context.Context, userID, endpointID int64, mutation ControlMutation, patch requestadaptation.Patch) (MutationResult[requestadaptation.Projection], error) {
	if r == nil || r.adaptations == nil || ctx == nil || userID <= 0 || endpointID <= 0 || mutation.Method != http.MethodPut || mutation.Route != routeEndpointAdaptation || !mutationPathIDs(mutation, endpointID) || mutation.Query != "" {
		return MutationResult[requestadaptation.Projection]{}, ErrInvalidRequest
	}
	now, err := r.nowUnix()
	if err != nil {
		return MutationResult[requestadaptation.Projection]{}, err
	}
	tx, err := r.beginAuthorizedTx(ctx, userID)
	if err != nil {
		return MutationResult[requestadaptation.Projection]{}, err
	}
	committed := false
	defer finishTx(tx, &committed)
	decision, err := beginControlMutation(ctx, tx, userID, mutation, now)
	if err != nil {
		return MutationResult[requestadaptation.Projection]{}, err
	}
	if decision.Kind == idempotency.Replay {
		return replayMutation[requestadaptation.Projection](decision)
	}
	if _, err := getEndpointTx(ctx, tx, userID, endpointID); err != nil {
		return MutationResult[requestadaptation.Projection]{}, err
	}
	ref := requestadaptation.Ref{Scope: requestadaptation.ScopeEndpoint, ID: endpointID}
	previous, err := r.adaptations.LoadTx(ctx, tx, ref)
	if err != nil {
		return MutationResult[requestadaptation.Projection]{}, resourceAdaptationError(err)
	}
	defer previous.Clear()
	previousRevision, _ := parseDecimalID(previous.Revision)
	if previousRevision != patch.ExpectedRevision {
		return MutationResult[requestadaptation.Projection]{}, ErrConflict
	}
	next, err := requestadaptation.ApplyPatch(previous.Document, patch, ref.Scope)
	if err != nil {
		return MutationResult[requestadaptation.Projection]{}, resourceAdaptationError(err)
	}
	defer next.Clear()
	projection, err := r.adaptations.SaveTx(ctx, tx, ref, patch.ExpectedRevision, next, userID, now)
	if err != nil {
		return MutationResult[requestadaptation.Projection]{}, resourceAdaptationError(err)
	}
	out, err := finishJSONMutation(ctx, tx, decision, http.StatusOK, projection)
	if err != nil {
		return MutationResult[requestadaptation.Projection]{}, err
	}
	if err := commitTx(tx, &committed); err != nil {
		return MutationResult[requestadaptation.Projection]{}, err
	}
	return out, nil
}

func (api *httpAPI) getEndpointAdaptation(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	endpointID, ok := parsePathID(writer, request, "id")
	if !ok || !requireEmptyQuery(writer, request) || !requireNoBody(writer, request) {
		return
	}
	projection, err := api.repository.GetEndpointAdaptation(request.Context(), principal.UserID, endpointID)
	if err != nil {
		writeResourceError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, projection)
}

func (api *httpAPI) putEndpointAdaptation(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	endpointID, ok := parsePathID(writer, request, "id")
	if !ok || !requireEmptyQuery(writer, request) {
		return
	}
	data, err := io.ReadAll(io.LimitReader(request.Body, requestadaptation.MaxConfigurationBytes+1))
	if err != nil || len(data) > requestadaptation.MaxConfigurationBytes {
		writeResourceError(writer, ErrInvalidRequest)
		return
	}
	defer clear(data)
	patch, err := requestadaptation.ParsePatch(data)
	if err != nil {
		writeResourceError(writer, resourceAdaptationError(err))
		return
	}
	mutation, ok := controlMutation(writer, request, routeEndpointAdaptation, []int64{endpointID}, json.RawMessage(data))
	if !ok {
		return
	}
	result, err := api.repository.PutEndpointAdaptation(request.Context(), principal.UserID, endpointID, mutation, patch)
	if err != nil {
		writeResourceError(writer, err)
		return
	}
	writeMutation(writer, result)
}
