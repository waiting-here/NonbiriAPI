package resources

import (
	"net/http"
	"strconv"
)

type createModelRequest struct {
	Provider         requestField[string] `json:"provider"`
	Model            requestField[string] `json:"model"`
	RouteStrategy    requestField[string] `json:"route_strategy"`
	SilentRetry      requestField[bool]   `json:"silent_retry"`
	FlattenToolCalls requestField[bool]   `json:"flatten_tool_calls"`
}

type createModelCanonical struct {
	Provider         string  `json:"provider"`
	Model            string  `json:"model"`
	RouteStrategy    *string `json:"route_strategy,omitempty"`
	SilentRetry      *bool   `json:"silent_retry,omitempty"`
	FlattenToolCalls *bool   `json:"flatten_tool_calls,omitempty"`
}

type patchModelRequest struct {
	Provider         requestField[string] `json:"provider"`
	Model            requestField[string] `json:"model"`
	RouteStrategy    requestField[string] `json:"route_strategy"`
	SilentRetry      requestField[bool]   `json:"silent_retry"`
	FlattenToolCalls requestField[bool]   `json:"flatten_tool_calls"`
	ExpectedRevision requestField[string] `json:"expected_revision"`
}

type patchModelCanonical struct {
	Provider         *string `json:"provider,omitempty"`
	Model            *string `json:"model,omitempty"`
	RouteStrategy    *string `json:"route_strategy,omitempty"`
	SilentRetry      *bool   `json:"silent_retry,omitempty"`
	FlattenToolCalls *bool   `json:"flatten_tool_calls,omitempty"`
	ExpectedRevision string  `json:"expected_revision"`
}

type bindingSelectionRequest struct {
	EndpointKeyID   requestField[string] `json:"endpoint_key_id"`
	UpstreamModelID requestField[string] `json:"upstream_model_id"`
}

type bindingSelectionCanonical struct {
	EndpointKeyID   string `json:"endpoint_key_id"`
	UpstreamModelID string `json:"upstream_model_id"`
}

type addBindingsRequest struct {
	ExpectedBindingRevision requestField[string]                    `json:"expected_binding_revision"`
	Selections              requestField[[]bindingSelectionRequest] `json:"selections"`
}

type addBindingsCanonical struct {
	ExpectedBindingRevision string                      `json:"expected_binding_revision"`
	Selections              []bindingSelectionCanonical `json:"selections"`
}

type orderBindingsRequest struct {
	ExpectedBindingRevision requestField[string]   `json:"expected_binding_revision"`
	Order                   requestField[[]string] `json:"order"`
}

type orderBindingsCanonical struct {
	ExpectedBindingRevision string   `json:"expected_binding_revision"`
	Order                   []string `json:"order"`
}

type expectedBindingRevisionRequest struct {
	ExpectedBindingRevision requestField[string] `json:"expected_binding_revision"`
}

type expectedBindingRevisionCanonical struct {
	ExpectedBindingRevision string `json:"expected_binding_revision"`
}

func (api *httpAPI) listModels(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	if !requireNoBody(writer, request) {
		return
	}
	limit, cursor, ok := parsePageQuery(writer, request)
	if !ok {
		return
	}
	page, err := api.repository.ListModels(request.Context(), principal.UserID, limit, cursor)
	if err != nil {
		writeResourceError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, page)
}

func (api *httpAPI) createModel(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	if !requireEmptyQuery(writer, request) {
		return
	}
	var body createModelRequest
	if _, ok := decodeStrictObject(writer, request, &body); !ok {
		return
	}
	if !body.Provider.Set || !body.Model.Set {
		writeResourceError(writer, ErrInvalidRequest)
		return
	}
	canonical := createModelCanonical{
		Provider: body.Provider.Value, Model: body.Model.Value,
		RouteStrategy: optionalPointer(body.RouteStrategy), SilentRetry: optionalPointer(body.SilentRetry),
		FlattenToolCalls: optionalPointer(body.FlattenToolCalls),
	}
	mutation, ok := controlMutation(writer, request, routeModels, nil, canonical)
	if !ok {
		return
	}
	input := CreateModelInput{Provider: canonical.Provider, Model: canonical.Model}
	if canonical.RouteStrategy != nil {
		input.RouteStrategy = *canonical.RouteStrategy
	}
	if canonical.SilentRetry != nil {
		input.SilentRetry = *canonical.SilentRetry
	}
	if canonical.FlattenToolCalls != nil {
		input.FlattenToolCalls = *canonical.FlattenToolCalls
	}
	result, err := api.repository.CreateModel(request.Context(), principal.UserID, mutation, input)
	if err != nil {
		writeResourceError(writer, err)
		return
	}
	writeMutation(writer, result)
}

func (api *httpAPI) getModel(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	modelID, ok := parsePathID(writer, request, "id")
	if !ok || !requireEmptyQuery(writer, request) || !requireNoBody(writer, request) {
		return
	}
	model, err := api.repository.GetModel(request.Context(), principal.UserID, modelID)
	if err != nil {
		writeResourceError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, model)
}

func (api *httpAPI) patchModel(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	modelID, ok := parsePathID(writer, request, "id")
	if !ok || !requireEmptyQuery(writer, request) {
		return
	}
	var body patchModelRequest
	if _, ok := decodeStrictObject(writer, request, &body); !ok {
		return
	}
	revision, revisionOK := canonicalExpectedRevision(body.ExpectedRevision)
	if !revisionOK || (!body.Provider.Set && !body.Model.Set && !body.RouteStrategy.Set && !body.SilentRetry.Set && !body.FlattenToolCalls.Set) {
		writeResourceError(writer, ErrInvalidRequest)
		return
	}
	canonical := patchModelCanonical{
		Provider: optionalPointer(body.Provider), Model: optionalPointer(body.Model),
		RouteStrategy: optionalPointer(body.RouteStrategy), SilentRetry: optionalPointer(body.SilentRetry),
		FlattenToolCalls: optionalPointer(body.FlattenToolCalls), ExpectedRevision: body.ExpectedRevision.Value,
	}
	mutation, ok := controlMutation(writer, request, routeModel, []int64{modelID}, canonical)
	if !ok {
		return
	}
	result, err := api.repository.PatchModel(request.Context(), principal.UserID, modelID, mutation, PatchModelInput{
		Provider: canonical.Provider, Model: canonical.Model, RouteStrategy: canonical.RouteStrategy,
		SilentRetry: canonical.SilentRetry, FlattenToolCalls: canonical.FlattenToolCalls, ExpectedRevision: revision,
	})
	if err != nil {
		writeResourceError(writer, err)
		return
	}
	writeMutation(writer, result)
}

func (api *httpAPI) deleteModel(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	modelID, ok := parsePathID(writer, request, "id")
	if !ok || !requireEmptyQuery(writer, request) {
		return
	}
	var body expectedRevisionRequest
	if _, ok := decodeStrictObject(writer, request, &body); !ok {
		return
	}
	revision, ok := canonicalExpectedRevision(body.ExpectedRevision)
	if !ok {
		writeResourceError(writer, ErrInvalidRequest)
		return
	}
	canonical := expectedRevisionCanonical{ExpectedRevision: body.ExpectedRevision.Value}
	mutation, ok := controlMutation(writer, request, routeModel, []int64{modelID}, canonical)
	if !ok {
		return
	}
	result, err := api.repository.DeleteModel(request.Context(), principal.UserID, modelID, mutation, revision)
	if err != nil {
		writeResourceError(writer, err)
		return
	}
	writeMutation(writer, result)
}

func (api *httpAPI) bindingCandidates(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	modelID, ok := parsePathID(writer, request, "id")
	if !ok || !requireNoBody(writer, request) {
		return
	}
	values, parsed := requestQuery(writer, request)
	if !parsed {
		return
	}
	if !exactQuery(values, "endpoint_id", "key_id", "source", "q", "cursor", "limit") {
		writeResourceError(writer, ErrInvalidRequest)
		return
	}
	query := CandidateQuery{}
	if entries, present := values["endpoint_id"]; present {
		query.EndpointID, _ = parseDecimalID(entries[0])
		if query.EndpointID <= 0 {
			writeResourceError(writer, ErrInvalidRequest)
			return
		}
	}
	if entries, present := values["key_id"]; present {
		query.KeyID, _ = parseDecimalID(entries[0])
		if query.KeyID <= 0 {
			writeResourceError(writer, ErrInvalidRequest)
			return
		}
	}
	if entries, present := values["source"]; present {
		query.Source = entries[0]
		if query.Source != "automatic" && query.Source != "manual" {
			writeResourceError(writer, ErrInvalidRequest)
			return
		}
	}
	if entries, present := values["q"]; present {
		query.Query = entries[0]
	}
	if entries, present := values["cursor"]; present {
		query.Cursor = entries[0]
		if query.Cursor == "" {
			writeResourceError(writer, ErrInvalidRequest)
			return
		}
	}
	if entries, present := values["limit"]; present {
		query.Limit, _ = strconv.Atoi(entries[0])
		if !validPageLimit(query.Limit) {
			writeResourceError(writer, ErrInvalidRequest)
			return
		}
	}
	page, err := api.repository.BindingCandidates(request.Context(), principal.UserID, modelID, query)
	if err != nil {
		writeResourceError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, page)
}

func (api *httpAPI) listBindings(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	modelID, ok := parsePathID(writer, request, "id")
	if !ok || !requireEmptyQuery(writer, request) || !requireNoBody(writer, request) {
		return
	}
	bindings, err := api.repository.ListBindings(request.Context(), principal.UserID, modelID)
	if err != nil {
		writeResourceError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, bindings)
}

func (api *httpAPI) addBindings(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	modelID, ok := parsePathID(writer, request, "id")
	if !ok || !requireEmptyQuery(writer, request) {
		return
	}
	var body addBindingsRequest
	if _, ok := decodeStrictObject(writer, request, &body); !ok {
		return
	}
	revision, revisionOK := canonicalExpectedGeneration(body.ExpectedBindingRevision)
	if !revisionOK || !body.Selections.Set {
		writeResourceError(writer, ErrInvalidRequest)
		return
	}
	selections := make([]BindingSelection, len(body.Selections.Value))
	canonicalSelections := make([]bindingSelectionCanonical, len(body.Selections.Value))
	for index, selection := range body.Selections.Value {
		if !selection.EndpointKeyID.Set || !selection.UpstreamModelID.Set {
			writeResourceError(writer, ErrInvalidRequest)
			return
		}
		keyID, err := parseDecimalID(selection.EndpointKeyID.Value)
		if err != nil {
			writeResourceError(writer, ErrInvalidRequest)
			return
		}
		selections[index] = BindingSelection{EndpointKeyID: keyID, UpstreamModelID: selection.UpstreamModelID.Value}
		canonicalSelections[index] = bindingSelectionCanonical{EndpointKeyID: selection.EndpointKeyID.Value, UpstreamModelID: selection.UpstreamModelID.Value}
	}
	canonical := addBindingsCanonical{ExpectedBindingRevision: body.ExpectedBindingRevision.Value, Selections: canonicalSelections}
	mutation, ok := controlMutation(writer, request, routeBindingBatch, []int64{modelID}, canonical)
	if !ok {
		return
	}
	result, err := api.repository.AddBindings(request.Context(), principal.UserID, modelID, mutation, revision, selections)
	if err != nil {
		writeResourceError(writer, err)
		return
	}
	writeMutation(writer, result)
}

func (api *httpAPI) orderBindings(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	modelID, ok := parsePathID(writer, request, "id")
	if !ok || !requireEmptyQuery(writer, request) {
		return
	}
	var body orderBindingsRequest
	if _, ok := decodeStrictObject(writer, request, &body); !ok {
		return
	}
	revision, revisionOK := canonicalExpectedGeneration(body.ExpectedBindingRevision)
	if !revisionOK || !body.Order.Set {
		writeResourceError(writer, ErrInvalidRequest)
		return
	}
	order := make([]int64, len(body.Order.Value))
	for index, value := range body.Order.Value {
		id, err := parseDecimalID(value)
		if err != nil {
			writeResourceError(writer, ErrInvalidRequest)
			return
		}
		order[index] = id
	}
	canonical := orderBindingsCanonical{ExpectedBindingRevision: body.ExpectedBindingRevision.Value, Order: append([]string(nil), body.Order.Value...)}
	mutation, ok := controlMutation(writer, request, routeBindingOrder, []int64{modelID}, canonical)
	if !ok {
		return
	}
	result, err := api.repository.OrderBindings(request.Context(), principal.UserID, modelID, mutation, revision, order)
	if err != nil {
		writeResourceError(writer, err)
		return
	}
	writeMutation(writer, result)
}

func (api *httpAPI) deleteBinding(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	modelID, ok := parsePathID(writer, request, "id")
	if !ok {
		return
	}
	bindingID, ok := parsePathID(writer, request, "bId")
	if !ok || !requireEmptyQuery(writer, request) {
		return
	}
	var body expectedBindingRevisionRequest
	if _, ok := decodeStrictObject(writer, request, &body); !ok {
		return
	}
	revision, revisionOK := canonicalExpectedGeneration(body.ExpectedBindingRevision)
	if !revisionOK {
		writeResourceError(writer, ErrInvalidRequest)
		return
	}
	canonical := expectedBindingRevisionCanonical{ExpectedBindingRevision: body.ExpectedBindingRevision.Value}
	mutation, ok := controlMutation(writer, request, routeBinding, []int64{modelID, bindingID}, canonical)
	if !ok {
		return
	}
	result, err := api.repository.DeleteBinding(request.Context(), principal.UserID, modelID, bindingID, mutation, revision)
	if err != nil {
		writeResourceError(writer, err)
		return
	}
	writeMutation(writer, result)
}
