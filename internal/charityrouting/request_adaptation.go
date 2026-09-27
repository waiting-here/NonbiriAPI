package charityrouting

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/waiting-here/NonbiriAPI/internal/charityreserve"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/requestadaptation"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

type adaptationView struct {
	requestadaptation.Projection
	Effective *requestadaptation.Projection `json:"effective,omitempty"`
}

func (s *Service) ReserveForOutput(ctx context.Context, modelID, currentReserve, outputFloor int64) (int64, error) {
	if s == nil || ctx == nil || modelID <= 0 || currentReserve < 0 || outputFloor < 0 || outputFloor > 2147483647 {
		return 0, ErrInvalidRequest
	}
	if outputFloor == 0 {
		return currentReserve, nil
	}
	var mode string
	var unit int64
	if err := s.db.QueryRowContext(ctx, `SELECT pricing_mode,output_user_price FROM charity_models WHERE id=? AND enabled=1`, modelID).Scan(&mode, &unit); errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	} else if err != nil {
		return 0, ErrUnavailable
	}
	if mode == "per_request" {
		return currentReserve, nil
	}
	if mode != "per_token" {
		return 0, ErrInvariant
	}
	reserve, err := charityreserve.WithOutputFloor(currentReserve, unit, outputFloor)
	if err != nil {
		return 0, ErrResourceLimit
	}
	return reserve, nil
}

func routingAdaptationError(err error) error {
	switch {
	case errors.Is(err, requestadaptation.ErrInvalid):
		return ErrInvalidRequest
	case errors.Is(err, requestadaptation.ErrConflict):
		return ErrConflict
	default:
		return ErrUnavailable
	}
}

func requireBindingTx(ctx context.Context, tx *sql.Tx, modelID, bindingID int64) error {
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM charity_model_bindings WHERE id=? AND charity_model_id=?)`, bindingID, modelID).Scan(&exists); err != nil {
		return ErrUnavailable
	}
	if exists != 1 {
		return ErrNotFound
	}
	return nil
}

func (s *Service) adaptationViewTx(ctx context.Context, tx *sql.Tx, modelID, bindingID int64) (adaptationView, error) {
	ref := requestadaptation.Ref{Scope: requestadaptation.ScopeCharityModel, ID: modelID}
	if bindingID > 0 {
		ref = requestadaptation.Ref{Scope: requestadaptation.ScopeBinding, ID: bindingID}
	}
	current, err := s.adaptations.LoadTx(ctx, tx, ref)
	if err != nil {
		return adaptationView{}, routingAdaptationError(err)
	}
	defer current.Clear()
	view := adaptationView{Projection: current.Projection()}
	if bindingID > 0 {
		model, err := s.adaptations.LoadTx(ctx, tx, requestadaptation.Ref{Scope: requestadaptation.ScopeCharityModel, ID: modelID})
		if err != nil {
			return adaptationView{}, routingAdaptationError(err)
		}
		defer model.Clear()
		effective := requestadaptation.Effective(model, current)
		projection := effective.Projection()
		effective.Clear()
		view.Effective = &projection
	}
	return view, nil
}

func (s *Service) GetAdaptation(ctx context.Context, role roleKind, actorID, modelID, bindingID int64) (adaptationView, error) {
	if s == nil || s.adaptations == nil || ctx == nil || modelID <= 0 || bindingID < 0 {
		return adaptationView{}, ErrInvalidRequest
	}
	tx, scope, err := s.beginManagementTx(ctx, role, actorID, modelID, false, true)
	if err != nil {
		return adaptationView{}, err
	}
	defer tx.Rollback()
	if scope.Trainee {
		return adaptationView{}, ErrForbidden
	}
	if _, err := readStoredModelTx(ctx, tx, modelID); err != nil {
		return adaptationView{}, err
	}
	if bindingID > 0 {
		if err := requireBindingTx(ctx, tx, modelID, bindingID); err != nil {
			return adaptationView{}, err
		}
	}
	view, err := s.adaptationViewTx(ctx, tx, modelID, bindingID)
	if err != nil {
		return adaptationView{}, err
	}
	if err := tx.Commit(); err != nil {
		return adaptationView{}, ErrUnavailable
	}
	return view, nil
}

func (s *Service) validateModelAdaptationsTx(ctx context.Context, tx *sql.Tx, modelID int64, excluded []string, modelDoc requestadaptation.Document) error {
	if requestadaptation.ExcludedConflict(modelDoc, excluded) {
		return ErrConflict
	}
	rows, err := tx.QueryContext(ctx, `SELECT id FROM charity_model_bindings WHERE charity_model_id=? ORDER BY id LIMIT 257`, modelID)
	if err != nil {
		return ErrUnavailable
	}
	var bindingIDs []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) != nil {
			rows.Close()
			return ErrUnavailable
		}
		bindingIDs = append(bindingIDs, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return ErrUnavailable
	}
	rows.Close()
	if len(bindingIDs) > 256 {
		return ErrResourceLimit
	}
	for _, id := range bindingIDs {
		binding, err := s.adaptations.LoadTx(ctx, tx, requestadaptation.Ref{Scope: requestadaptation.ScopeBinding, ID: id})
		if err != nil {
			return routingAdaptationError(err)
		}
		effective := requestadaptation.Effective(requestadaptation.Snapshot{Document: modelDoc}, binding)
		conflict := requestadaptation.ValidateDocument(effective.Document, requestadaptation.ScopeCharityModel) != nil || requestadaptation.ExcludedConflict(effective.Document, excluded)
		effective.Clear()
		binding.Clear()
		if conflict {
			return ErrConflict
		}
	}
	return nil
}

func (s *Service) PutAdaptation(ctx context.Context, modelID, bindingID int64, mutation resources.ControlMutation, patch requestadaptation.Patch) (resources.MutationResult[adaptationView], error) {
	if s == nil || s.adaptations == nil || ctx == nil || modelID <= 0 || bindingID < 0 {
		return resources.MutationResult[adaptationView]{}, ErrInvalidRequest
	}
	route := routeAdminModelAdaptation
	ids := []int64{modelID}
	if bindingID > 0 {
		route = routeAdminBindingAdaptation
		ids = append(ids, bindingID)
	}
	if !validMutation(mutation, http.MethodPut, route, ids...) {
		return resources.MutationResult[adaptationView]{}, ErrInvalidRequest
	}
	now, err := s.nowUnix()
	if err != nil {
		return resources.MutationResult[adaptationView]{}, err
	}
	tx, actorID, err := s.beginRoleTx(ctx, roleAdmin, 0)
	if err != nil {
		return resources.MutationResult[adaptationView]{}, err
	}
	committed := false
	defer finishTx(tx, &committed)
	decision, err := beginMutation(ctx, tx, roleAdmin, actorID, mutation, now)
	if err != nil {
		return resources.MutationResult[adaptationView]{}, err
	}
	if decision.Kind == idempotency.Replay {
		return replay[adaptationView](decision)
	}
	model, err := readStoredModelTx(ctx, tx, modelID)
	if err != nil {
		return resources.MutationResult[adaptationView]{}, err
	}
	if bindingID > 0 {
		if err := requireBindingTx(ctx, tx, modelID, bindingID); err != nil {
			return resources.MutationResult[adaptationView]{}, err
		}
	}
	ref := requestadaptation.Ref{Scope: requestadaptation.ScopeCharityModel, ID: modelID}
	if bindingID > 0 {
		ref = requestadaptation.Ref{Scope: requestadaptation.ScopeBinding, ID: bindingID}
	}
	previous, err := s.adaptations.LoadTx(ctx, tx, ref)
	if err != nil {
		return resources.MutationResult[adaptationView]{}, routingAdaptationError(err)
	}
	defer previous.Clear()
	previousRevision, _ := parseNonNegativeRevision(previous.Revision)
	if previousRevision != patch.ExpectedRevision {
		return resources.MutationResult[adaptationView]{}, ErrConflict
	}
	next, err := requestadaptation.ApplyPatch(previous.Document, patch, ref.Scope)
	if err != nil {
		return resources.MutationResult[adaptationView]{}, routingAdaptationError(err)
	}
	defer next.Clear()
	excluded, err := decodeExcludedFields(model.excludedFields)
	if err != nil {
		return resources.MutationResult[adaptationView]{}, err
	}
	if bindingID == 0 {
		if err := s.validateModelAdaptationsTx(ctx, tx, modelID, excluded, next); err != nil {
			return resources.MutationResult[adaptationView]{}, err
		}
	} else {
		parent, err := s.adaptations.LoadTx(ctx, tx, requestadaptation.Ref{Scope: requestadaptation.ScopeCharityModel, ID: modelID})
		if err != nil {
			return resources.MutationResult[adaptationView]{}, routingAdaptationError(err)
		}
		effective := requestadaptation.Effective(parent, requestadaptation.Snapshot{Document: next})
		conflict := requestadaptation.ValidateDocument(effective.Document, requestadaptation.ScopeCharityModel) != nil || requestadaptation.ExcludedConflict(effective.Document, excluded)
		effective.Clear()
		parent.Clear()
		if conflict {
			return resources.MutationResult[adaptationView]{}, ErrConflict
		}
	}
	if _, err := s.adaptations.SaveTx(ctx, tx, ref, patch.ExpectedRevision, next, actorID, now); err != nil {
		return resources.MutationResult[adaptationView]{}, routingAdaptationError(err)
	}
	view, err := s.adaptationViewTx(ctx, tx, modelID, bindingID)
	if err != nil {
		return resources.MutationResult[adaptationView]{}, err
	}
	out, err := finishJSON(ctx, tx, decision, http.StatusOK, view)
	if err != nil {
		return resources.MutationResult[adaptationView]{}, err
	}
	if err := commitTx(tx, &committed); err != nil {
		return resources.MutationResult[adaptationView]{}, err
	}
	return out, nil
}

func parseNonNegativeRevision(value string) (int64, error) {
	if value == "0" {
		return 0, nil
	}
	return parsePositiveID(value)
}

func (api *httpAPI) getAdminModelAdaptation(w http.ResponseWriter, r *http.Request) {
	api.getAdaptation(w, r, roleAdmin, 0, false)
}
func (api *httpAPI) getAdminBindingAdaptation(w http.ResponseWriter, r *http.Request) {
	api.getAdaptation(w, r, roleAdmin, 0, true)
}
func (api *httpAPI) getStewardModelAdaptation(w http.ResponseWriter, r *http.Request, p UserPrincipal) {
	api.getAdaptation(w, r, roleSteward, p.UserID, false)
}
func (api *httpAPI) getStewardBindingAdaptation(w http.ResponseWriter, r *http.Request, p UserPrincipal) {
	api.getAdaptation(w, r, roleSteward, p.UserID, true)
}

func (api *httpAPI) getAdaptation(w http.ResponseWriter, r *http.Request, role roleKind, actorID int64, binding bool) {
	modelID, ok := parsePathID(w, r, "id")
	if !ok || !requireEmptyQuery(w, r) || !requireNoBody(w, r) {
		return
	}
	bindingID := int64(0)
	if binding {
		bindingID, ok = parsePathID(w, r, "bindingId")
		if !ok {
			return
		}
	}
	view, err := api.service.GetAdaptation(r.Context(), role, actorID, modelID, bindingID)
	if err != nil {
		writeRoutingError(w, err)
		return
	}
	writeJSON(w, view)
}

func (api *httpAPI) putAdminModelAdaptation(w http.ResponseWriter, r *http.Request) {
	api.putAdaptation(w, r, false)
}
func (api *httpAPI) putAdminBindingAdaptation(w http.ResponseWriter, r *http.Request) {
	api.putAdaptation(w, r, true)
}

func (api *httpAPI) putAdaptation(w http.ResponseWriter, r *http.Request, binding bool) {
	modelID, ok := parsePathID(w, r, "id")
	if !ok || !requireEmptyQuery(w, r) {
		return
	}
	bindingID := int64(0)
	route := routeAdminModelAdaptation
	ids := []int64{modelID}
	if binding {
		bindingID, ok = parsePathID(w, r, "bindingId")
		if !ok {
			return
		}
		route = routeAdminBindingAdaptation
		ids = append(ids, bindingID)
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, requestadaptation.MaxConfigurationBytes+1))
	if err != nil || len(data) > requestadaptation.MaxConfigurationBytes {
		writeRoutingError(w, ErrInvalidRequest)
		return
	}
	defer clear(data)
	patch, err := requestadaptation.ParsePatch(data)
	if err != nil {
		writeRoutingError(w, routingAdaptationError(err))
		return
	}
	mutation, ok := mutationFor(w, r, route, ids, json.RawMessage(data))
	if !ok {
		return
	}
	result, err := api.service.PutAdaptation(r.Context(), modelID, bindingID, mutation, patch)
	if err != nil {
		writeRoutingError(w, err)
		return
	}
	writeMutation(w, result)
}
