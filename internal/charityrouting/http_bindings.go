package charityrouting

import (
	"net/http"
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
	limit, cursor, ok := parsePage(values, "donation_id", "donation_key_id", "source", "q", "cursor", "limit")
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
	owner := candidateFilterOwner(role, actorID, modelID, donationID, donationKeyID, source, query)
	afterKey, afterModel, err := api.service.decodeCandidateCursor(cursor, scope, owner, now)
	if err != nil {
		writeRoutingError(writer, err)
		return
	}
	input := CandidateQuery{DonationID: donationID, DonationKeyID: donationKeyID, Source: source, Query: query,
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
	DonationKeyID   requiredField[string] `json:"donation_key_id"`
	UpstreamModelID requiredField[string] `json:"upstream_model_id"`
}

type bindingBatchWire struct {
	ExpectedBindingRevision requiredField[string]                 `json:"expected_binding_revision"`
	Selections              requiredField[[]bindingSelectionWire] `json:"selections"`
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
	api.addBindings(writer, request, roleAdmin, 0)
}

func (api *httpAPI) addStewardBindings(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	api.addBindings(writer, request, roleSteward, principal.UserID)
}

func (api *httpAPI) addBindings(writer http.ResponseWriter, request *http.Request, role roleKind, actorID int64) {
	modelID, ok := parsePathID(writer, request, "id")
	if !ok || !requireEmptyQuery(writer, request) {
		return
	}
	var wire bindingBatchWire
	if !decodeStrictObject(writer, request, &wire) {
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
	mutation, ok := mutationFor(writer, request, route, []int64{modelID}, canonical)
	if !ok {
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
	ExpectedBindingRevision requiredField[string]   `json:"expected_binding_revision"`
	Order                   requiredField[[]string] `json:"order"`
}

func parseBindingOrder(wire bindingOrderWire) (BindingOrder, map[string]any, error) {
	if !wire.ExpectedBindingRevision.Set || !wire.Order.Set {
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
	input := BindingOrder{ExpectedBindingRevision: wire.ExpectedBindingRevision.Value, Order: wire.Order.Value}
	return input, map[string]any{"expected_binding_revision": input.ExpectedBindingRevision, "order": input.Order}, nil
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
	if !wire.ExpectedBindingRevision.Set {
		writeRoutingError(writer, ErrInvalidRequest)
		return
	}
	if _, err := parseNonnegativeRevision(wire.ExpectedBindingRevision.Value); err != nil {
		writeRoutingError(writer, err)
		return
	}
	input := BindingDelete{ExpectedBindingRevision: wire.ExpectedBindingRevision.Value}
	canonical := map[string]any{"expected_binding_revision": input.ExpectedBindingRevision}
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
