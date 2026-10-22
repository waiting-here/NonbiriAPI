package charityrouting

import (
	"errors"
	"net/http"

	"github.com/waiting-here/NonbiriAPI/internal/donationstate"
	"github.com/waiting-here/NonbiriAPI/internal/httpapi"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
	"github.com/waiting-here/NonbiriAPI/internal/strictjson"
)

func optionalQueryID(values map[string][]string, key string) (int64, error) {
	raw, exists := values[key]
	if !exists {
		return 0, nil
	}
	if len(raw) != 1 {
		return 0, ErrInvalidRequest
	}
	return parsePositiveID(raw[0])
}

func (api *httpAPI) adminCandidates(writer http.ResponseWriter, request *http.Request) {
	api.candidates(writer, request, roleAdmin, 0)
}

func (api *httpAPI) stewardCandidates(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	api.candidates(writer, request, roleSteward, principal.UserID)
}

func (api *httpAPI) candidates(writer http.ResponseWriter, request *http.Request, role roleKind, actorID int64) {
	modelID, ok := parsePathID(writer, request, "id")
	if !ok || !requireNoBody(writer, request) {
		return
	}
	values, ok := requestQuery(writer, request)
	if !ok {
		return
	}
	numberedPage, numbered, pageErr := parseNumberedQuery(values, "donation_id", "donation_key_id", "source", "q", "key_state", "include_bound")
	if pageErr != nil {
		writeRoutingError(writer, pageErr)
		return
	}
	if values.Has("include_bound") && values.Get("include_bound") != "true" && values.Get("include_bound") != "false" {
		writeRoutingError(writer, ErrInvalidRequest)
		return
	}
	keyState := values.Get("key_state")
	if !donationstate.Valid(keyState) || values.Has("key_state") && keyState == "" {
		writeRoutingError(writer, ErrInvalidRequest)
		return
	}
	if numbered {
		api.numberedCandidates(writer, request, role, actorID, modelID, values, numberedPage)
		return
	}
	limit, cursor, ok := parsePage(values, "donation_id", "donation_key_id", "source", "q", "cursor", "limit", "key_state", "include_bound")
	if !ok {
		writeRoutingError(writer, ErrInvalidRequest)
		return
	}
	donationID, err := optionalQueryID(values, "donation_id")
	if err != nil {
		writeRoutingError(writer, err)
		return
	}
	donationKeyID, err := optionalQueryID(values, "donation_key_id")
	if err != nil {
		writeRoutingError(writer, err)
		return
	}
	source, query := values.Get("source"), values.Get("q")
	if source != "" && source != "automatic" && source != "manual" {
		writeRoutingError(writer, ErrInvalidRequest)
		return
	}
	now, err := api.service.nowUnix()
	if err != nil {
		writeRoutingError(writer, err)
		return
	}
	scope := string(role) + "-charity-binding-candidates"
	filterOwner := candidateFilterOwner(role, actorID, modelID, donationID, donationKeyID, source, query)
	if donationstate.Filtered(keyState) {
		filterOwner += ":key_state=" + keyState
	}
	if values.Get("include_bound") == "true" {
		filterOwner += ":include_bound=true"
	}
	owner, err := api.service.managementCursorOwner(request.Context(), role, actorID, modelID, filterOwner)
	if err != nil {
		writeRoutingError(writer, err)
		return
	}
	afterKey, afterModel, err := api.service.decodeCandidateCursor(cursor, scope, owner, now)
	if err != nil {
		writeRoutingError(writer, err)
		return
	}
	input := CandidateQuery{IncludeBound: values.Get("include_bound") == "true", DonationID: donationID, DonationKeyID: donationKeyID, Source: source, Query: query, KeyState: keyState,
		AfterKeyID: afterKey, AfterModelID: afterModel, Limit: limit}
	if role == roleAdmin {
		items, nextKey, nextModel, err := api.service.BindingCandidatesAdmin(request.Context(), modelID, input)
		if err != nil {
			writeRoutingError(writer, err)
			return
		}
		page := Page[AdminBindingCandidate]{Data: items}
		if !api.setCandidateNext(writer, scope, owner, now, nextKey, nextModel, &page.NextCursor) {
			return
		}
		writeJSON(writer, page)
		return
	}
	items, nextKey, nextModel, err := api.service.BindingCandidatesSteward(request.Context(), actorID, modelID, input)
	if err != nil {
		writeRoutingError(writer, err)
		return
	}
	page := Page[StewardBindingCandidate]{Data: items}
	if !api.setCandidateNext(writer, scope, owner, now, nextKey, nextModel, &page.NextCursor) {
		return
	}
	writeJSON(writer, page)
}

func (api *httpAPI) setCandidateNext(writer http.ResponseWriter, scope, owner string, now, keyID int64, modelID string, target **string) bool {
	if keyID == 0 {
		return true
	}
	token, err := api.service.encodeCandidateCursor(scope, owner, now, keyID, modelID)
	if err != nil {
		writeRoutingError(writer, err)
		return false
	}
	*target = &token
	return true
}

func (api *httpAPI) getAdminBindings(writer http.ResponseWriter, request *http.Request) {
	api.getBindings(writer, request, roleAdmin, 0)
}

func (api *httpAPI) getStewardBindings(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	api.getBindings(writer, request, roleSteward, principal.UserID)
}

func (api *httpAPI) getBindings(writer http.ResponseWriter, request *http.Request, role roleKind, actorID int64) {
	modelID, ok := parsePathID(writer, request, "id")
	if !ok || !requireEmptyQuery(writer, request) || !requireNoBody(writer, request) {
		return
	}
	if role == roleAdmin {
		value, err := api.service.GetBindingsAdmin(request.Context(), modelID)
		if err != nil {
			writeRoutingError(writer, err)
			return
		}
		writeJSON(writer, value)
		return
	}
	value, err := api.service.GetBindingsSteward(request.Context(), actorID, modelID)
	if err != nil {
		writeRoutingError(writer, err)
		return
	}
	writeJSON(writer, value)
}

type bindingSelectionWire struct {
	Targets         requiredField[[]bindingTargetWire] `json:"targets"`
	DonationKeyID   requiredField[string]              `json:"donation_key_id"`
	UpstreamModelID requiredField[string]              `json:"upstream_model_id"`
}

type bindingBatchWire struct {
	ExpectedBindingRevision requiredField[string]                 `json:"expected_binding_revision"`
	Selections              requiredField[[]bindingSelectionWire] `json:"selections"`
}

func decodeBindingBatchReceipt(w http.ResponseWriter, r *http.Request, wire *bindingBatchWire) bool {
	body, err := httpapi.ReadJSON(w, r, wire, httpapi.BodyOptions{MaxBytes: idempotency.MaxControlBodyBytes,
		Validate: func(body []byte) error { return strictjson.ValidateObjectWithFieldLimit(body, 2+4*maxBindingBatch) }})
	if err != nil {
		if errors.Is(err, httpapi.ErrTooLarge) {
			httperr.WriteError(w, httperr.New(httperr.CodePayloadTooLarge, "request body is too large"))
		} else {
			writeRoutingError(w, ErrInvalidRequest)
		}
		return false
	}
	clear(body)
	return true
}

func parseBindingBatch(wire bindingBatchWire) (BindingBatch, map[string]any, error) {
	if !wire.ExpectedBindingRevision.Set || !wire.Selections.Set {
		return BindingBatch{}, nil, ErrInvalidRequest
	}
	if _, err := parseNonnegativeRevision(wire.ExpectedBindingRevision.Value); err != nil {
		return BindingBatch{}, nil, err
	}
	selections := make([]BindingSelection, len(wire.Selections.Value))
	canonicalSelections := make([]map[string]any, len(wire.Selections.Value))
	for index, item := range wire.Selections.Value {
		if item.Targets.Set {
			if item.DonationKeyID.Set || item.UpstreamModelID.Set {
				return BindingBatch{}, nil, ErrInvalidRequest
			}
			targets, err := parseBindingTargetWires(item.Targets.Value)
			if err != nil {
				return BindingBatch{}, nil, err
			}
			selections[index] = BindingSelection{Targets: targets}
			canonicalSelections[index] = map[string]any{"targets": targets}
			continue
		}
		if !item.DonationKeyID.Set || !item.UpstreamModelID.Set {
			return BindingBatch{}, nil, ErrInvalidRequest
		}
		if _, err := parsePositiveID(item.DonationKeyID.Value); err != nil {
			return BindingBatch{}, nil, err
		}
		selections[index] = BindingSelection{DonationKeyID: item.DonationKeyID.Value, UpstreamModelID: item.UpstreamModelID.Value}
		canonicalSelections[index] = map[string]any{"donation_key_id": item.DonationKeyID.Value,
			"upstream_model_id": item.UpstreamModelID.Value}
	}
	input := BindingBatch{ExpectedBindingRevision: wire.ExpectedBindingRevision.Value, Selections: selections}
	return input, map[string]any{"expected_binding_revision": input.ExpectedBindingRevision,
		"selections": canonicalSelections}, nil
}

func (api *httpAPI) addAdminBindings(writer http.ResponseWriter, request *http.Request) {
	api.addBindings(writer, request, roleAdmin, 0, false)
}

func (api *httpAPI) addStewardBindings(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	api.addBindings(writer, request, roleSteward, principal.UserID, false)
}

func (api *httpAPI) addAdminBindingsReceipt(writer http.ResponseWriter, request *http.Request) {
	api.addBindings(writer, request, roleAdmin, 0, true)
}

func (api *httpAPI) addStewardBindingsReceipt(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	api.addBindings(writer, request, roleSteward, principal.UserID, true)
}

func (api *httpAPI) addBindings(writer http.ResponseWriter, request *http.Request, role roleKind, actorID int64, compact bool) {
	modelID, ok := parsePathID(writer, request, "id")
	if !ok || !requireEmptyQuery(writer, request) {
		return
	}
	var wire bindingBatchWire
	if compact {
		if !decodeBindingBatchReceipt(writer, request, &wire) {
			return
		}
	} else if !decodeStrictObject(writer, request, &wire) {
		return
	}
	input, canonical, err := parseBindingBatch(wire)
	if err != nil {
		writeRoutingError(writer, err)
		return
	}
	route := routeAdminBindingBatch
	if role == roleSteward {
		route = routeStewardBindingBatch
	}
	if compact {
		if role == roleAdmin {
			route = routeAdminBindingReceipt
		} else {
			route = routeStewardBindingReceipt
		}
	}
	mutation, ok := mutationFor(writer, request, route, []int64{modelID}, canonical)
	if !ok {
		return
	}
	if compact {
		var result resources.MutationResult[BindingBatchReceipt]
		if role == roleAdmin {
			result, err = api.service.AddBindingsReceiptAdmin(request.Context(), modelID, mutation, input)
		} else {
			result, err = api.service.AddBindingsReceiptSteward(request.Context(), actorID, modelID, mutation, input)
		}
		if err != nil {
			writeRoutingError(writer, err)
			return
		}
		writeMutation(writer, result)
		return
	}
	if role == roleAdmin {
		result, err := api.service.AddBindingsAdmin(request.Context(), modelID, mutation, input)
		if err != nil {
			writeRoutingError(writer, err)
			return
		}
		writeMutation(writer, result)
		return
	}
	result, err := api.service.AddBindingsSteward(request.Context(), actorID, modelID, mutation, input)
	if err != nil {
		writeRoutingError(writer, err)
		return
	}
	writeMutation(writer, result)
}

type bindingOrderWire struct {
	Response                requiredField[string]   `json:"response"`
	ExpectedBindingRevision requiredField[string]   `json:"expected_binding_revision"`
	Order                   requiredField[[]string] `json:"order"`
}

func parseBindingOrder(wire bindingOrderWire) (BindingOrder, map[string]any, error) {
	if !wire.ExpectedBindingRevision.Set || !wire.Order.Set || wire.Response.Set && wire.Response.Value != "receipt" {
		return BindingOrder{}, nil, ErrInvalidRequest
	}
	if _, err := parseNonnegativeRevision(wire.ExpectedBindingRevision.Value); err != nil {
		return BindingOrder{}, nil, err
	}
	for _, id := range wire.Order.Value {
		if _, err := parsePositiveID(id); err != nil {
			return BindingOrder{}, nil, err
		}
	}
	input := BindingOrder{ExpectedBindingRevision: wire.ExpectedBindingRevision.Value, Order: wire.Order.Value, Response: wire.Response.Value}
	canonical := map[string]any{"expected_binding_revision": input.ExpectedBindingRevision, "order": input.Order}
	if wire.Response.Set {
		canonical["response"] = input.Response
	}
	return input, canonical, nil
}

func (api *httpAPI) orderAdminBindings(writer http.ResponseWriter, request *http.Request) {
	api.orderBindings(writer, request, roleAdmin, 0)
}

func (api *httpAPI) orderStewardBindings(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	api.orderBindings(writer, request, roleSteward, principal.UserID)
}

func (api *httpAPI) orderBindings(writer http.ResponseWriter, request *http.Request, role roleKind, actorID int64) {
	modelID, ok := parsePathID(writer, request, "id")
	if !ok || !requireEmptyQuery(writer, request) {
		return
	}
	var wire bindingOrderWire
	if !decodeStrictObject(writer, request, &wire) {
		return
	}
	input, canonical, err := parseBindingOrder(wire)
	if err != nil {
		writeRoutingError(writer, err)
		return
	}
	route := routeAdminBindingOrder
	if role == roleSteward {
		route = routeStewardBindingOrder
	}
	mutation, ok := mutationFor(writer, request, route, []int64{modelID}, canonical)
	if !ok {
		return
	}
	if role == roleAdmin {
		result, err := api.service.OrderBindingsAdmin(request.Context(), modelID, mutation, input)
		if err != nil {
			writeRoutingError(writer, err)
			return
		}
		writeMutation(writer, result)
		return
	}
	result, err := api.service.OrderBindingsSteward(request.Context(), actorID, modelID, mutation, input)
	if err != nil {
		writeRoutingError(writer, err)
		return
	}
	writeMutation(writer, result)
}

type bindingDeleteWire struct {
	Response                requiredField[string] `json:"response"`
	ExpectedBindingRevision requiredField[string] `json:"expected_binding_revision"`
}

func (api *httpAPI) deleteAdminBinding(writer http.ResponseWriter, request *http.Request) {
	api.deleteBinding(writer, request, roleAdmin, 0)
}

func (api *httpAPI) deleteStewardBinding(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	api.deleteBinding(writer, request, roleSteward, principal.UserID)
}

func (api *httpAPI) deleteBinding(writer http.ResponseWriter, request *http.Request, role roleKind, actorID int64) {
	modelID, ok := parsePathID(writer, request, "id")
	if !ok {
		return
	}
	bindingID, ok := parsePathID(writer, request, "bindingId")
	if !ok || !requireEmptyQuery(writer, request) {
		return
	}
	var wire bindingDeleteWire
	if !decodeStrictObject(writer, request, &wire) {
		return
	}
	if !wire.ExpectedBindingRevision.Set || wire.Response.Set && wire.Response.Value != "receipt" {
		writeRoutingError(writer, ErrInvalidRequest)
		return
	}
	if _, err := parseNonnegativeRevision(wire.ExpectedBindingRevision.Value); err != nil {
		writeRoutingError(writer, err)
		return
	}
	input := BindingDelete{ExpectedBindingRevision: wire.ExpectedBindingRevision.Value, Response: wire.Response.Value}
	canonical := map[string]any{"expected_binding_revision": input.ExpectedBindingRevision}
	if wire.Response.Set {
		canonical["response"] = input.Response
	}
	route := routeAdminBinding
	if role == roleSteward {
		route = routeStewardBinding
	}
	mutation, ok := mutationFor(writer, request, route, []int64{modelID, bindingID}, canonical)
	if !ok {
		return
	}
	if role == roleAdmin {
		result, err := api.service.DeleteBindingAdmin(request.Context(), modelID, bindingID, mutation, input)
		if err != nil {
			writeRoutingError(writer, err)
			return
		}
		writeMutation(writer, result)
		return
	}
	result, err := api.service.DeleteBindingSteward(request.Context(), actorID, modelID, bindingID, mutation, input)
	if err != nil {
		writeRoutingError(writer, err)
		return
	}
	writeMutation(writer, result)
}
