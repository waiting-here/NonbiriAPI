package charityrouting

import (
	"errors"
	"net/http"
)

type httpAPI struct{ service *Service }

func RegisterOwnerRoutes(registrar UserRouteRegistrar, service *Service) error {
	if nilDependency(registrar) || service == nil {
		return errors.New("charity routing: owner registrar and service are required")
	}
	api := &httpAPI{service: service}
	if err := registrar.RegisterUserRoute(http.MethodGet, routeCapability, api.capability); err != nil {
		return registerError(http.MethodGet, routeCapability, err)
	}
	return nil
}

func RegisterAdminRoutes(registrar AdminRouteRegistrar, service *Service) error {
	if nilDependency(registrar) || service == nil {
		return errors.New("charity routing: admin registrar and service are required")
	}
	api := &httpAPI{service: service}
	routes := []struct {
		method, pattern string
		handler         http.HandlerFunc
	}{
		{http.MethodGet, routeAdminModels, api.listAdminModels},
		{http.MethodPost, routeAdminModels, api.createAdminModel},
		{http.MethodGet, routeAdminModel, api.getAdminModel},
		{http.MethodPatch, routeAdminModel, api.patchAdminModel},
		{http.MethodDelete, routeAdminModel, api.deleteAdminModel},
		{http.MethodGet, routeAdminCandidates, api.adminCandidates},
		{http.MethodGet, routeAdminBindings, api.getAdminBindings},
		{http.MethodPost, routeAdminBindingBatch, api.addAdminBindings},
		{http.MethodPut, routeAdminBindingOrder, api.orderAdminBindings},
		{http.MethodDelete, routeAdminBinding, api.deleteAdminBinding},
	}
	for _, route := range routes {
		if err := registrar.RegisterAdminRoute(route.method, route.pattern, route.handler); err != nil {
			return registerError(route.method, route.pattern, err)
		}
	}
	return nil
}

func RegisterStewardRoutes(registrar UserRouteRegistrar, service *Service) error {
	if nilDependency(registrar) || service == nil {
		return errors.New("charity routing: steward registrar and service are required")
	}
	api := &httpAPI{service: service}
	routes := []struct {
		method, pattern string
		handler         AuthorizedUserHandler
	}{
		{http.MethodGet, routeStewardModels, api.listStewardModels},
		{http.MethodPost, routeStewardModels, api.createStewardModel},
		{http.MethodGet, routeStewardModel, api.getStewardModel},
		{http.MethodPatch, routeStewardModel, api.patchStewardModel},
		{http.MethodDelete, routeStewardModel, api.deleteStewardModel},
		{http.MethodGet, routeStewardCandidates, api.stewardCandidates},
		{http.MethodGet, routeStewardBindings, api.getStewardBindings},
		{http.MethodPost, routeStewardBindingBatch, api.addStewardBindings},
		{http.MethodPut, routeStewardBindingOrder, api.orderStewardBindings},
		{http.MethodDelete, routeStewardBinding, api.deleteStewardBinding},
	}
	for _, route := range routes {
		if err := registrar.RegisterUserRoute(route.method, route.pattern, route.handler); err != nil {
			return registerError(route.method, route.pattern, err)
		}
	}
	return nil
}

func (api *httpAPI) capability(writer http.ResponseWriter, request *http.Request, _ UserPrincipal) {
	if !requireEmptyQuery(writer, request) || !requireNoBody(writer, request) {
		return
	}
	now, err := api.service.nowUnix()
	if err != nil {
		writeRoutingError(writer, err)
		return
	}
	value, err := api.service.Capability(request.Context(), now)
	if err != nil {
		writeRoutingError(writer, err)
		return
	}
	writeJSON(writer, value)
}

func parseEnabledFilter(raw string, present bool) (*bool, error) {
	if !present {
		return nil, nil
	}
	if raw != "true" && raw != "false" {
		return nil, ErrInvalidRequest
	}
	value := raw == "true"
	return &value, nil
}

func (api *httpAPI) listAdminModels(writer http.ResponseWriter, request *http.Request) {
	api.listModels(writer, request, roleAdmin, 0)
}

func (api *httpAPI) listStewardModels(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	api.listModels(writer, request, roleSteward, principal.UserID)
}

func (api *httpAPI) listModels(writer http.ResponseWriter, request *http.Request, role roleKind, actorID int64) {
	if !requireNoBody(writer, request) {
		return
	}
	values, ok := requestQuery(writer, request)
	if !ok {
		return
	}
	limit, cursor, ok := parsePage(values, "q", "enabled", "cursor", "limit")
	if !ok {
		writeRoutingError(writer, ErrInvalidRequest)
		return
	}
	enabledRaw, enabledPresent := values["enabled"]
	raw := ""
	if enabledPresent {
		raw = enabledRaw[0]
	}
	enabled, err := parseEnabledFilter(raw, enabledPresent)
	if err != nil {
		writeRoutingError(writer, err)
		return
	}
	query := values.Get("q")
	now, err := api.service.nowUnix()
	if err != nil {
		writeRoutingError(writer, err)
		return
	}
	scope := string(role) + "-charity-models"
	owner := paginationOwner(role, actorID, query, boolFilter(enabled))
	after, err := api.service.decodeModelCursor(cursor, scope, owner, now)
	if err != nil {
		writeRoutingError(writer, err)
		return
	}
	if role == roleAdmin {
		items, next, err := api.service.ListAdmin(request.Context(), query, enabled, after, limit)
		if err != nil {
			writeRoutingError(writer, err)
			return
		}
		page := Page[AdminCharityModel]{Data: items}
		if !api.setModelNext(writer, scope, owner, now, next, &page.NextCursor) {
			return
		}
		writeJSON(writer, page)
		return
	}
	items, next, err := api.service.ListSteward(request.Context(), actorID, query, enabled, after, limit)
	if err != nil {
		writeRoutingError(writer, err)
		return
	}
	page := Page[StewardCharityModel]{Data: items}
	if !api.setModelNext(writer, scope, owner, now, next, &page.NextCursor) {
		return
	}
	writeJSON(writer, page)
}

func (api *httpAPI) setModelNext(writer http.ResponseWriter, scope, owner string, now, next int64, target **string) bool {
	if next == 0 {
		return true
	}
	token, err := api.service.encodeModelCursor(scope, owner, now, next)
	if err != nil {
		writeRoutingError(writer, err)
		return false
	}
	*target = &token
	return true
}

func (api *httpAPI) getAdminModel(writer http.ResponseWriter, request *http.Request) {
	api.getModel(writer, request, roleAdmin, 0)
}

func (api *httpAPI) getStewardModel(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	api.getModel(writer, request, roleSteward, principal.UserID)
}

func (api *httpAPI) getModel(writer http.ResponseWriter, request *http.Request, role roleKind, actorID int64) {
	id, ok := parsePathID(writer, request, "id")
	if !ok || !requireEmptyQuery(writer, request) || !requireNoBody(writer, request) {
		return
	}
	if role == roleAdmin {
		value, err := api.service.GetAdmin(request.Context(), id)
		if err != nil {
			writeRoutingError(writer, err)
			return
		}
		writeJSON(writer, value)
		return
	}
	value, err := api.service.GetSteward(request.Context(), actorID, id)
	if err != nil {
		writeRoutingError(writer, err)
		return
	}
	writeJSON(writer, value)
}

type tokenPricesWire struct {
	UncachedInput   requiredField[string] `json:"uncached_input"`
	CacheWriteInput requiredField[string] `json:"cache_write_input"`
	CacheReadInput  requiredField[string] `json:"cache_read_input"`
	Output          requiredField[string] `json:"output"`
}

type pricingWire struct {
	Mode         requiredField[string]          `json:"mode"`
	UserPrice    requiredField[string]          `json:"user_price"`
	DonorReward  requiredField[string]          `json:"donor_reward"`
	UserPrices   requiredField[tokenPricesWire] `json:"user_prices"`
	DonorRewards requiredField[tokenPricesWire] `json:"donor_rewards"`
}

func parseTokenPrices(wire tokenPricesWire) (*TokenPricesInput, map[string]any, error) {
	if !wire.UncachedInput.Set || !wire.CacheWriteInput.Set || !wire.CacheReadInput.Set || !wire.Output.Set {
		return nil, nil, ErrInvalidRequest
	}
	value := &TokenPricesInput{UncachedInput: wire.UncachedInput.Value, CacheWriteInput: wire.CacheWriteInput.Value,
		CacheReadInput: wire.CacheReadInput.Value, Output: wire.Output.Value}
	canonical := map[string]any{"uncached_input": value.UncachedInput, "cache_write_input": value.CacheWriteInput,
		"cache_read_input": value.CacheReadInput, "output": value.Output}
	return value, canonical, nil
}

func parsePricing(wire pricingWire) (PricingInput, map[string]any, error) {
	if !wire.Mode.Set {
		return PricingInput{}, nil, ErrInvalidRequest
	}
	input := PricingInput{Mode: wire.Mode.Value}
	canonical := map[string]any{"mode": wire.Mode.Value}
	switch wire.Mode.Value {
	case "per_request":
		if !wire.UserPrice.Set || !wire.DonorReward.Set || wire.UserPrices.Set || wire.DonorRewards.Set {
			return PricingInput{}, nil, ErrInvalidRequest
		}
		input.UserPrice, input.DonorReward = &wire.UserPrice.Value, &wire.DonorReward.Value
		canonical["user_price"], canonical["donor_reward"] = wire.UserPrice.Value, wire.DonorReward.Value
	case "per_token":
		if wire.UserPrice.Set || wire.DonorReward.Set || !wire.UserPrices.Set || !wire.DonorRewards.Set {
			return PricingInput{}, nil, ErrInvalidRequest
		}
		var err error
		input.UserPrices, canonical["user_prices"], err = parseTokenPrices(wire.UserPrices.Value)
		if err != nil {
			return PricingInput{}, nil, err
		}
		input.DonorRewards, canonical["donor_rewards"], err = parseTokenPrices(wire.DonorRewards.Value)
		if err != nil {
			return PricingInput{}, nil, err
		}
	default:
		return PricingInput{}, nil, ErrInvalidRequest
	}
	return input, canonical, nil
}

type discountWire struct {
	Enabled requiredField[bool]  `json:"enabled"`
	Percent requiredField[int]   `json:"percent"`
	StartAt nullableField[int64] `json:"start_at"`
	EndAt   nullableField[int64] `json:"end_at"`
}

func parseDiscount(wire discountWire) (DiscountInput, map[string]any, error) {
	if !wire.Enabled.Set || !wire.Percent.Set || !wire.StartAt.Set || !wire.EndAt.Set {
		return DiscountInput{}, nil, ErrInvalidRequest
	}
	input := DiscountInput{Enabled: wire.Enabled.Value, Percent: wire.Percent.Value,
		StartAt: wire.StartAt.Value, EndAt: wire.EndAt.Value}
	canonical := map[string]any{"enabled": input.Enabled, "percent": input.Percent,
		"start_at": input.StartAt, "end_at": input.EndAt}
	return input, canonical, nil
}

type modelCreateWire struct {
	Provider         requiredField[string]       `json:"provider"`
	Model            requiredField[string]       `json:"model"`
	Enabled          requiredField[bool]         `json:"enabled"`
	Pricing          requiredField[pricingWire]  `json:"pricing"`
	Discount         requiredField[discountWire] `json:"discount"`
	FlattenToolCalls requiredField[bool]         `json:"flatten_tool_calls"`
}

func parseModelCreate(wire modelCreateWire) (ModelCreate, map[string]any, error) {
	if !wire.Provider.Set || !wire.Model.Set || !wire.Enabled.Set || !wire.Pricing.Set || !wire.Discount.Set || !wire.FlattenToolCalls.Set {
		return ModelCreate{}, nil, ErrInvalidRequest
	}
	pricing, pricingJSON, err := parsePricing(wire.Pricing.Value)
	if err != nil {
		return ModelCreate{}, nil, err
	}
	discount, discountJSON, err := parseDiscount(wire.Discount.Value)
	if err != nil {
		return ModelCreate{}, nil, err
	}
	input := ModelCreate{Provider: wire.Provider.Value, Model: wire.Model.Value, Enabled: wire.Enabled.Value,
		Pricing: pricing, Discount: discount, FlattenToolCalls: wire.FlattenToolCalls.Value}
	canonical := map[string]any{"provider": input.Provider, "model": input.Model, "enabled": input.Enabled,
		"pricing": pricingJSON, "discount": discountJSON, "flatten_tool_calls": input.FlattenToolCalls}
	return input, canonical, nil
}

func (api *httpAPI) createAdminModel(writer http.ResponseWriter, request *http.Request) {
	api.createModel(writer, request, roleAdmin, 0)
}

func (api *httpAPI) createStewardModel(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	api.createModel(writer, request, roleSteward, principal.UserID)
}

func (api *httpAPI) createModel(writer http.ResponseWriter, request *http.Request, role roleKind, actorID int64) {
	if !requireEmptyQuery(writer, request) {
		return
	}
	var wire modelCreateWire
	if !decodeStrictObject(writer, request, &wire) {
		return
	}
	input, canonical, err := parseModelCreate(wire)
	if err != nil {
		writeRoutingError(writer, err)
		return
	}
	route := routeAdminModels
	if role == roleSteward {
		route = routeStewardModels
	}
	mutation, ok := mutationFor(writer, request, route, nil, canonical)
	if !ok {
		return
	}
	if role == roleAdmin {
		result, err := api.service.CreateAdmin(request.Context(), mutation, input)
		if err != nil {
			writeRoutingError(writer, err)
			return
		}
		writeMutation(writer, result)
		return
	}
	result, err := api.service.CreateSteward(request.Context(), actorID, mutation, input)
	if err != nil {
		writeRoutingError(writer, err)
		return
	}
	writeMutation(writer, result)
}

type discountPatchWire struct {
	Enabled requiredField[bool]  `json:"enabled"`
	Percent requiredField[int]   `json:"percent"`
	StartAt nullableField[int64] `json:"start_at"`
	EndAt   nullableField[int64] `json:"end_at"`
}

func parseDiscountPatch(wire discountPatchWire) (*DiscountPatchInput, map[string]any, error) {
	if !wire.Enabled.Set && !wire.Percent.Set && !wire.StartAt.Set && !wire.EndAt.Set {
		return nil, nil, ErrInvalidRequest
	}
	input := &DiscountPatchInput{}
	canonical := make(map[string]any)
	if wire.Enabled.Set {
		input.Enabled = &wire.Enabled.Value
		canonical["enabled"] = wire.Enabled.Value
	}
	if wire.Percent.Set {
		input.Percent = &wire.Percent.Value
		canonical["percent"] = wire.Percent.Value
	}
	if wire.StartAt.Set {
		input.StartAt = &wire.StartAt.Value
		canonical["start_at"] = wire.StartAt.Value
	}
	if wire.EndAt.Set {
		input.EndAt = &wire.EndAt.Value
		canonical["end_at"] = wire.EndAt.Value
	}
	return input, canonical, nil
}

type modelPatchWire struct {
	ExpectedRevision requiredField[string]            `json:"expected_revision"`
	Provider         requiredField[string]            `json:"provider"`
	Model            requiredField[string]            `json:"model"`
	Enabled          requiredField[bool]              `json:"enabled"`
	Pricing          requiredField[pricingWire]       `json:"pricing"`
	Discount         requiredField[discountPatchWire] `json:"discount"`
	FlattenToolCalls requiredField[bool]              `json:"flatten_tool_calls"`
}

func parseModelPatch(wire modelPatchWire) (ModelPatch, map[string]any, error) {
	if !wire.ExpectedRevision.Set {
		return ModelPatch{}, nil, ErrInvalidRequest
	}
	if _, err := parsePositiveID(wire.ExpectedRevision.Value); err != nil {
		return ModelPatch{}, nil, err
	}
	input := ModelPatch{ExpectedRevision: wire.ExpectedRevision.Value}
	canonical := map[string]any{"expected_revision": wire.ExpectedRevision.Value}
	if wire.Provider.Set {
		input.Provider = &wire.Provider.Value
		canonical["provider"] = wire.Provider.Value
	}
	if wire.Model.Set {
		input.Model = &wire.Model.Value
		canonical["model"] = wire.Model.Value
	}
	if wire.Enabled.Set {
		input.Enabled = &wire.Enabled.Value
		canonical["enabled"] = wire.Enabled.Value
	}
	if wire.Pricing.Set {
		value, encoded, err := parsePricing(wire.Pricing.Value)
		if err != nil {
			return ModelPatch{}, nil, err
		}
		input.Pricing = &value
		canonical["pricing"] = encoded
	}
	if wire.Discount.Set {
		value, encoded, err := parseDiscountPatch(wire.Discount.Value)
		if err != nil {
			return ModelPatch{}, nil, err
		}
		input.Discount = value
		canonical["discount"] = encoded
	}
	if wire.FlattenToolCalls.Set {
		input.FlattenToolCalls = &wire.FlattenToolCalls.Value
		canonical["flatten_tool_calls"] = wire.FlattenToolCalls.Value
	}
	return input, canonical, nil
}

func (api *httpAPI) patchAdminModel(writer http.ResponseWriter, request *http.Request) {
	api.patchModel(writer, request, roleAdmin, 0)
}

func (api *httpAPI) patchStewardModel(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	api.patchModel(writer, request, roleSteward, principal.UserID)
}

func (api *httpAPI) patchModel(writer http.ResponseWriter, request *http.Request, role roleKind, actorID int64) {
	id, ok := parsePathID(writer, request, "id")
	if !ok || !requireEmptyQuery(writer, request) {
		return
	}
	var wire modelPatchWire
	if !decodeStrictObject(writer, request, &wire) {
		return
	}
	input, canonical, err := parseModelPatch(wire)
	if err != nil {
		writeRoutingError(writer, err)
		return
	}
	route := routeAdminModel
	if role == roleSteward {
		route = routeStewardModel
	}
	mutation, ok := mutationFor(writer, request, route, []int64{id}, canonical)
	if !ok {
		return
	}
	if role == roleAdmin {
		result, err := api.service.PatchAdmin(request.Context(), id, mutation, input)
		if err != nil {
			writeRoutingError(writer, err)
			return
		}
		writeMutation(writer, result)
		return
	}
	result, err := api.service.PatchSteward(request.Context(), actorID, id, mutation, input)
	if err != nil {
		writeRoutingError(writer, err)
		return
	}
	writeMutation(writer, result)
}

type modelDeleteWire struct {
	ExpectedRevision requiredField[string] `json:"expected_revision"`
	Confirmation     requiredField[string] `json:"confirmation"`
}

func (api *httpAPI) deleteAdminModel(writer http.ResponseWriter, request *http.Request) {
	api.deleteModel(writer, request, roleAdmin, 0)
}

func (api *httpAPI) deleteStewardModel(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	api.deleteModel(writer, request, roleSteward, principal.UserID)
}

func (api *httpAPI) deleteModel(writer http.ResponseWriter, request *http.Request, role roleKind, actorID int64) {
	id, ok := parsePathID(writer, request, "id")
	if !ok || !requireEmptyQuery(writer, request) {
		return
	}
	var wire modelDeleteWire
	if !decodeStrictObject(writer, request, &wire) {
		return
	}
	if !wire.ExpectedRevision.Set || !wire.Confirmation.Set {
		writeRoutingError(writer, ErrInvalidRequest)
		return
	}
	if _, err := parsePositiveID(wire.ExpectedRevision.Value); err != nil {
		writeRoutingError(writer, err)
		return
	}
	input := ModelDelete{ExpectedRevision: wire.ExpectedRevision.Value, Confirmation: wire.Confirmation.Value}
	canonical := map[string]any{"expected_revision": input.ExpectedRevision, "confirmation": input.Confirmation}
	route := routeAdminModel
	if role == roleSteward {
		route = routeStewardModel
	}
	mutation, ok := mutationFor(writer, request, route, []int64{id}, canonical)
	if !ok {
		return
	}
	if role == roleAdmin {
		result, err := api.service.DeleteAdmin(request.Context(), id, mutation, input)
		if err != nil {
			writeRoutingError(writer, err)
			return
		}
		writeMutation(writer, result)
		return
	}
	result, err := api.service.DeleteSteward(request.Context(), actorID, id, mutation, input)
	if err != nil {
		writeRoutingError(writer, err)
		return
	}
	writeMutation(writer, result)
}
