package donation

import (
	"errors"
	"net/http"
)

type httpAPI struct{ service *Service }

func RegisterOwnerRoutes(registrar UserRouteRegistrar, service *Service) error {
	if nilDependency(registrar) || service == nil {
		return errors.New("donation: owner registrar and service are required")
	}
	api := &httpAPI{service: service}
	routes := []struct {
		method, pattern string
		handler         AuthorizedUserHandler
	}{
		{http.MethodGet, routeDonations, api.listOwner},
		{http.MethodPost, routeDonations, api.createOwner},
		{http.MethodGet, routeDonation, api.getOwner},
		{http.MethodPatch, routeDonation, api.editOwner},
		{http.MethodPost, routeWithdraw, api.withdrawOwner},
		{http.MethodPost, routeTerminate, api.terminateOwner},
	}
	for _, route := range routes {
		if err := registrar.RegisterUserRoute(route.method, route.pattern, route.handler); err != nil {
			return registerError("donation", route.method, route.pattern, err)
		}
	}
	return nil
}

func RegisterAdminRoutes(registrar AdminRouteRegistrar, service *Service) error {
	if nilDependency(registrar) || service == nil {
		return errors.New("donation: admin registrar and service are required")
	}
	api := &httpAPI{service: service}
	routes := []struct {
		method, pattern string
		handler         http.HandlerFunc
	}{
		{http.MethodGet, routeAdminDonations, api.listAdmin},
		{http.MethodGet, routeAdminDonation, api.getAdmin},
		{http.MethodPost, routeAdminReview, api.reviewAdmin},
		{http.MethodPatch, routeAdminKey, api.manageKeyAdmin},
	}
	for _, route := range routes {
		if err := registrar.RegisterAdminRoute(route.method, route.pattern, route.handler); err != nil {
			return registerError("donation", route.method, route.pattern, err)
		}
	}
	return nil
}

func RegisterStewardRoutes(registrar UserRouteRegistrar, service *Service) error {
	if nilDependency(registrar) || service == nil {
		return errors.New("donation: steward registrar and service are required")
	}
	api := &httpAPI{service: service}
	routes := []struct {
		method, pattern string
		handler         AuthorizedUserHandler
	}{
		{http.MethodGet, routeStewardDonations, api.listSteward},
		{http.MethodGet, routeStewardDonation, api.getSteward},
		{http.MethodPost, routeStewardReview, api.reviewSteward},
		{http.MethodPatch, routeStewardKey, api.manageKeySteward},
	}
	for _, route := range routes {
		if err := registrar.RegisterUserRoute(route.method, route.pattern, route.handler); err != nil {
			return registerError("donation", route.method, route.pattern, err)
		}
	}
	return nil
}

func (api *httpAPI) listOwner(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	if !requireNoBody(writer, request) {
		return
	}
	values, ok := requestQuery(writer, request)
	if !ok {
		return
	}
	limit, cursor, ok := parsePage(values, "cursor", "limit")
	if !ok {
		writeDonationError(writer, ErrInvalidRequest)
		return
	}
	now, err := api.service.nowUnix()
	if err != nil {
		writeDonationError(writer, err)
		return
	}
	owner := pageOwner(principal.UserID, "owner")
	after, err := api.service.decodeCursor(cursor, "owner-donations", owner, now)
	if err != nil {
		writeDonationError(writer, err)
		return
	}
	items, next, err := api.service.ListOwner(request.Context(), principal.UserID, after, limit)
	if err != nil {
		writeDonationError(writer, err)
		return
	}
	page := Page[Donation]{Data: items}
	if next > 0 {
		token, err := api.service.encodeCursor("owner-donations", owner, now, next)
		if err != nil {
			writeDonationError(writer, err)
			return
		}
		page.NextCursor = &token
	}
	writeJSON(writer, page)
}

func (api *httpAPI) getOwner(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	id, ok := parsePathID(writer, request, "id")
	if !ok || !requireEmptyQuery(writer, request) || !requireNoBody(writer, request) {
		return
	}
	value, err := api.service.GetOwner(request.Context(), principal.UserID, id)
	if err != nil {
		writeDonationError(writer, err)
		return
	}
	writeJSON(writer, value)
}

type createKeyWire struct {
	EndpointKeyID requiredField[string] `json:"endpoint_key_id"`
	ExpiresAt     nullableField[int64]  `json:"expires_at"`
}

type createWire struct {
	Description         requiredField[string]          `json:"description"`
	Keys                requiredField[[]createKeyWire] `json:"keys"`
	OwnershipAuthorized requiredField[bool]            `json:"ownership_authorized"`
}

func (api *httpAPI) createOwner(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	if !requireEmptyQuery(writer, request) {
		return
	}
	var wire createWire
	if !decodeStrictObject(writer, request, &wire) {
		return
	}
	if !wire.Description.Set || !wire.Keys.Set || !wire.OwnershipAuthorized.Set {
		writeDonationError(writer, ErrInvalidRequest)
		return
	}
	keys := make([]CreateKeyInput, len(wire.Keys.Value))
	canonicalKeys := make([]map[string]any, len(wire.Keys.Value))
	for index, key := range wire.Keys.Value {
		id, err := requiredID(key.EndpointKeyID)
		if err != nil || !key.ExpiresAt.Set {
			writeDonationError(writer, ErrInvalidRequest)
			return
		}
		keys[index] = CreateKeyInput{EndpointKeyID: id, ExpiresAt: key.ExpiresAt.Value}
		canonicalKeys[index] = map[string]any{"endpoint_key_id": key.EndpointKeyID.Value, "expires_at": key.ExpiresAt.Value}
	}
	canonical := map[string]any{"description": wire.Description.Value, "keys": canonicalKeys,
		"ownership_authorized": wire.OwnershipAuthorized.Value}
	mutation, ok := mutationFor(writer, request, routeDonations, nil, canonical)
	if !ok {
		return
	}
	result, err := api.service.Create(request.Context(), principal.UserID, mutation, CreateInput{
		Description: wire.Description.Value, Keys: keys, OwnershipAuthorized: wire.OwnershipAuthorized.Value,
	})
	if err != nil {
		writeDonationError(writer, err)
		return
	}
	writeMutation(writer, result)
}

type editWire struct {
	Description      requiredField[string] `json:"description"`
	ExpectedRevision requiredField[string] `json:"expected_revision"`
}

func (api *httpAPI) editOwner(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	id, ok := parsePathID(writer, request, "id")
	if !ok || !requireEmptyQuery(writer, request) {
		return
	}
	var wire editWire
	if !decodeStrictObject(writer, request, &wire) {
		return
	}
	revision, err := requiredRevision(wire.ExpectedRevision)
	if !wire.Description.Set || err != nil {
		writeDonationError(writer, ErrInvalidRequest)
		return
	}
	canonical := map[string]any{"description": wire.Description.Value, "expected_revision": wire.ExpectedRevision.Value}
	mutation, ok := mutationFor(writer, request, routeDonation, []int64{id}, canonical)
	if !ok {
		return
	}
	result, err := api.service.Edit(request.Context(), principal.UserID, id, mutation,
		EditInput{Description: wire.Description.Value, ExpectedRevision: revision})
	if err != nil {
		writeDonationError(writer, err)
		return
	}
	writeMutation(writer, result)
}

type revisionWire struct {
	ExpectedRevision requiredField[string] `json:"expected_revision"`
}

func (api *httpAPI) withdrawOwner(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	id, ok := parsePathID(writer, request, "id")
	if !ok || !requireEmptyQuery(writer, request) {
		return
	}
	var wire revisionWire
	if !decodeStrictObject(writer, request, &wire) {
		return
	}
	revision, err := requiredRevision(wire.ExpectedRevision)
	if err != nil {
		writeDonationError(writer, ErrInvalidRequest)
		return
	}
	canonical := map[string]any{"expected_revision": wire.ExpectedRevision.Value}
	mutation, ok := mutationFor(writer, request, routeWithdraw, []int64{id}, canonical)
	if !ok {
		return
	}
	result, err := api.service.Withdraw(request.Context(), principal.UserID, id, mutation, RevisionInput{ExpectedRevision: revision})
	if err != nil {
		writeDonationError(writer, err)
		return
	}
	writeMutation(writer, result)
}

type terminateWire struct {
	ExpectedRevision requiredField[string] `json:"expected_revision"`
	Confirmation     requiredField[string] `json:"confirmation"`
}

func (api *httpAPI) terminateOwner(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	id, ok := parsePathID(writer, request, "id")
	if !ok || !requireEmptyQuery(writer, request) {
		return
	}
	var wire terminateWire
	if !decodeStrictObject(writer, request, &wire) {
		return
	}
	revision, err := requiredRevision(wire.ExpectedRevision)
	if err != nil || !wire.Confirmation.Set {
		writeDonationError(writer, ErrInvalidRequest)
		return
	}
	canonical := map[string]any{"expected_revision": wire.ExpectedRevision.Value, "confirmation": wire.Confirmation.Value}
	mutation, ok := mutationFor(writer, request, routeTerminate, []int64{id}, canonical)
	if !ok {
		return
	}
	result, err := api.service.Terminate(request.Context(), principal.UserID, id, mutation,
		TerminateInput{ExpectedRevision: revision, Confirmation: wire.Confirmation.Value})
	if err != nil {
		writeDonationError(writer, err)
		return
	}
	writeMutation(writer, result)
}

func (api *httpAPI) listAdmin(writer http.ResponseWriter, request *http.Request) {
	api.listRole(writer, request, UserPrincipal{}, reviewerAdmin)
}

func (api *httpAPI) listSteward(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	api.listRole(writer, request, principal, reviewerSteward)
}

func (api *httpAPI) listRole(writer http.ResponseWriter, request *http.Request, principal UserPrincipal, role reviewerRole) {
	if !requireNoBody(writer, request) {
		return
	}
	values, ok := requestQuery(writer, request)
	if !ok {
		return
	}
	limit, cursor, ok := parsePage(values, "status", "cursor", "limit")
	if !ok {
		writeDonationError(writer, ErrInvalidRequest)
		return
	}
	status := values.Get("status")
	if !validStatusFilter(status) {
		writeDonationError(writer, ErrInvalidRequest)
		return
	}
	now, err := api.service.nowUnix()
	if err != nil {
		writeDonationError(writer, err)
		return
	}
	scope, owner := "admin-donations", "admin:status="+status
	if role == reviewerSteward {
		scope = "steward-donations"
		owner = pageOwner(principal.UserID, "steward:status="+status)
	}
	after, err := api.service.decodeCursor(cursor, scope, owner, now)
	if err != nil {
		writeDonationError(writer, err)
		return
	}
	if role == reviewerAdmin {
		items, next, err := api.service.ListAdmin(request.Context(), status, after, limit)
		if err != nil {
			writeDonationError(writer, err)
			return
		}
		page := Page[AdminDonation]{Data: items}
		if !api.setNextCursor(writer, scope, owner, now, next, &page.NextCursor) {
			return
		}
		writeJSON(writer, page)
		return
	}
	items, next, err := api.service.ListSteward(request.Context(), principal.UserID, status, after, limit)
	if err != nil {
		writeDonationError(writer, err)
		return
	}
	page := Page[StewardDonation]{Data: items}
	if !api.setNextCursor(writer, scope, owner, now, next, &page.NextCursor) {
		return
	}
	writeJSON(writer, page)
}

func (api *httpAPI) setNextCursor(writer http.ResponseWriter, scope, owner string, now, next int64, target **string) bool {
	if next == 0 {
		return true
	}
	token, err := api.service.encodeCursor(scope, owner, now, next)
	if err != nil {
		writeDonationError(writer, err)
		return false
	}
	*target = &token
	return true
}

func (api *httpAPI) getAdmin(writer http.ResponseWriter, request *http.Request) {
	id, ok := parsePathID(writer, request, "id")
	if !ok || !requireEmptyQuery(writer, request) || !requireNoBody(writer, request) {
		return
	}
	value, err := api.service.GetAdmin(request.Context(), id)
	if err != nil {
		writeDonationError(writer, err)
		return
	}
	writeJSON(writer, value)
}

func (api *httpAPI) getSteward(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	id, ok := parsePathID(writer, request, "id")
	if !ok || !requireEmptyQuery(writer, request) || !requireNoBody(writer, request) {
		return
	}
	value, err := api.service.GetSteward(request.Context(), principal.UserID, id)
	if err != nil {
		writeDonationError(writer, err)
		return
	}
	writeJSON(writer, value)
}

type reviewKeyWire struct {
	DonationKeyID requiredField[string] `json:"donation_key_id"`
	PriceLimit    nullableField[string] `json:"price_limit"`
	CallsLimit    nullableField[string] `json:"calls_limit"`
	TokensLimit   nullableField[string] `json:"tokens_limit"`
	TokenReserve  requiredField[int64]  `json:"token_reserve"`
	Enabled       requiredField[bool]   `json:"enabled"`
	SafeNote      requiredField[string] `json:"safe_note"`
	ExpiresAt     nullableField[int64]  `json:"expires_at"`
}

type reviewWire struct {
	Decision         requiredField[string]          `json:"decision"`
	ExpectedRevision requiredField[string]          `json:"expected_revision"`
	Reason           requiredField[string]          `json:"reason"`
	KeySettings      requiredField[[]reviewKeyWire] `json:"key_settings"`
}

func parseReviewWire(wire reviewWire) (ReviewInput, map[string]any, error) {
	if !wire.Decision.Set || !wire.Reason.Set {
		return ReviewInput{}, nil, ErrInvalidRequest
	}
	revision, err := requiredRevision(wire.ExpectedRevision)
	if err != nil {
		return ReviewInput{}, nil, err
	}
	canonical := map[string]any{"decision": wire.Decision.Value, "expected_revision": wire.ExpectedRevision.Value,
		"reason": wire.Reason.Value}
	input := ReviewInput{Decision: wire.Decision.Value, ExpectedRevision: revision, Reason: wire.Reason.Value}
	switch wire.Decision.Value {
	case "reject":
		if wire.KeySettings.Set {
			return ReviewInput{}, nil, ErrInvalidRequest
		}
	case "approve":
		if !wire.KeySettings.Set {
			return ReviewInput{}, nil, ErrInvalidRequest
		}
		settings := make([]KeySetting, len(wire.KeySettings.Value))
		canonicalSettings := make([]map[string]any, len(wire.KeySettings.Value))
		for index, setting := range wire.KeySettings.Value {
			id, err := requiredID(setting.DonationKeyID)
			if err != nil || !setting.PriceLimit.Set || !setting.CallsLimit.Set || !setting.TokensLimit.Set ||
				!setting.TokenReserve.Set || !setting.Enabled.Set || !setting.SafeNote.Set || !setting.ExpiresAt.Set {
				return ReviewInput{}, nil, ErrInvalidRequest
			}
			settings[index] = KeySetting{DonationKeyID: id, PriceLimit: setting.PriceLimit.Value,
				CallsLimit: setting.CallsLimit.Value, TokensLimit: setting.TokensLimit.Value,
				TokenReserve: setting.TokenReserve.Value, Enabled: setting.Enabled.Value, SafeNote: setting.SafeNote.Value,
				ExpiresAt: setting.ExpiresAt.Value}
			canonicalSettings[index] = map[string]any{"donation_key_id": setting.DonationKeyID.Value,
				"price_limit": setting.PriceLimit.Value, "calls_limit": setting.CallsLimit.Value,
				"tokens_limit": setting.TokensLimit.Value, "token_reserve": setting.TokenReserve.Value,
				"enabled": setting.Enabled.Value, "safe_note": setting.SafeNote.Value, "expires_at": setting.ExpiresAt.Value}
		}
		input.KeySettings = settings
		canonical["key_settings"] = canonicalSettings
	default:
		return ReviewInput{}, nil, ErrInvalidRequest
	}
	return input, canonical, nil
}

func (api *httpAPI) reviewAdmin(writer http.ResponseWriter, request *http.Request) {
	api.reviewRole(writer, request, UserPrincipal{}, reviewerAdmin)
}

func (api *httpAPI) reviewSteward(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	api.reviewRole(writer, request, principal, reviewerSteward)
}

func (api *httpAPI) reviewRole(writer http.ResponseWriter, request *http.Request, principal UserPrincipal, role reviewerRole) {
	id, ok := parsePathID(writer, request, "id")
	if !ok || !requireEmptyQuery(writer, request) {
		return
	}
	var wire reviewWire
	if !decodeStrictObject(writer, request, &wire) {
		return
	}
	input, canonical, err := parseReviewWire(wire)
	if err != nil {
		writeDonationError(writer, err)
		return
	}
	route := routeAdminReview
	if role == reviewerSteward {
		route = routeStewardReview
	}
	mutation, ok := mutationFor(writer, request, route, []int64{id}, canonical)
	if !ok {
		return
	}
	if role == reviewerAdmin {
		result, err := api.service.ReviewAdmin(request.Context(), mutation, id, input)
		if err != nil {
			writeDonationError(writer, err)
			return
		}
		writeMutation(writer, result)
		return
	}
	result, err := api.service.ReviewSteward(request.Context(), principal.UserID, id, mutation, input)
	if err != nil {
		writeDonationError(writer, err)
		return
	}
	writeMutation(writer, result)
}

type keyManagementWire struct {
	ExpectedRevision   requiredField[string] `json:"expected_revision"`
	Enabled            requiredField[bool]   `json:"enabled"`
	PriceLimit         nullableField[string] `json:"price_limit"`
	CallsLimit         nullableField[string] `json:"calls_limit"`
	TokensLimit        nullableField[string] `json:"tokens_limit"`
	TokenReserve       requiredField[int64]  `json:"token_reserve"`
	SafeNote           requiredField[string] `json:"safe_note"`
	ExpiresAt          nullableField[int64]  `json:"expires_at"`
	ResetFailureStreak requiredField[bool]   `json:"reset_failure_streak"`
}

func parseKeyManagementWire(wire keyManagementWire) (KeyManagementInput, map[string]any, error) {
	revision, err := requiredRevision(wire.ExpectedRevision)
	if err != nil || !wire.Enabled.Set && !wire.PriceLimit.Set && !wire.CallsLimit.Set && !wire.TokensLimit.Set &&
		!wire.TokenReserve.Set && !wire.SafeNote.Set && !wire.ExpiresAt.Set && !wire.ResetFailureStreak.Set ||
		wire.ResetFailureStreak.Set && !wire.ResetFailureStreak.Value {
		return KeyManagementInput{}, nil, ErrInvalidRequest
	}
	input := KeyManagementInput{ExpectedRevision: revision, ResetFailureStreak: wire.ResetFailureStreak.Set}
	canonical := map[string]any{"expected_revision": wire.ExpectedRevision.Value}
	if wire.Enabled.Set {
		value := wire.Enabled.Value
		input.Enabled = &value
		canonical["enabled"] = value
	}
	if wire.PriceLimit.Set {
		input.PriceLimit = &wire.PriceLimit.Value
		canonical["price_limit"] = wire.PriceLimit.Value
	}
	if wire.CallsLimit.Set {
		input.CallsLimit = &wire.CallsLimit.Value
		canonical["calls_limit"] = wire.CallsLimit.Value
	}
	if wire.TokensLimit.Set {
		input.TokensLimit = &wire.TokensLimit.Value
		canonical["tokens_limit"] = wire.TokensLimit.Value
	}
	if wire.TokenReserve.Set {
		value := wire.TokenReserve.Value
		input.TokenReserve = &value
		canonical["token_reserve"] = value
	}
	if wire.SafeNote.Set {
		value := wire.SafeNote.Value
		input.SafeNote = &value
		canonical["safe_note"] = value
	}
	if wire.ExpiresAt.Set {
		input.ExpiresAt = &wire.ExpiresAt.Value
		canonical["expires_at"] = wire.ExpiresAt.Value
	}
	if wire.ResetFailureStreak.Set {
		canonical["reset_failure_streak"] = true
	}
	return input, canonical, nil
}

func (api *httpAPI) manageKeyAdmin(writer http.ResponseWriter, request *http.Request) {
	api.manageKeyRole(writer, request, UserPrincipal{}, reviewerAdmin)
}

func (api *httpAPI) manageKeySteward(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	api.manageKeyRole(writer, request, principal, reviewerSteward)
}

func (api *httpAPI) manageKeyRole(writer http.ResponseWriter, request *http.Request, principal UserPrincipal, role reviewerRole) {
	donationID, ok := parsePathID(writer, request, "id")
	if !ok {
		return
	}
	keyID, ok := parsePathID(writer, request, "keyId")
	if !ok || !requireEmptyQuery(writer, request) {
		return
	}
	var wire keyManagementWire
	if !decodeStrictObject(writer, request, &wire) {
		return
	}
	input, canonical, err := parseKeyManagementWire(wire)
	if err != nil {
		writeDonationError(writer, err)
		return
	}
	route := routeAdminKey
	if role == reviewerSteward {
		route = routeStewardKey
	}
	mutation, ok := mutationFor(writer, request, route, []int64{donationID, keyID}, canonical)
	if !ok {
		return
	}
	if role == reviewerAdmin {
		result, err := api.service.ManageKeyAdmin(request.Context(), donationID, keyID, mutation, input)
		if err != nil {
			writeDonationError(writer, err)
			return
		}
		writeMutation(writer, result)
		return
	}
	result, err := api.service.ManageKeySteward(request.Context(), principal.UserID, donationID, keyID, mutation, input)
	if err != nil {
		writeDonationError(writer, err)
		return
	}
	writeMutation(writer, result)
}

func requiredRevision(field requiredField[string]) (int64, error) {
	if !field.Set {
		return 0, ErrInvalidRequest
	}
	return parseCanonicalID(field.Value)
}

func requiredID(field requiredField[string]) (int64, error) {
	if !field.Set {
		return 0, ErrInvalidRequest
	}
	return parseCanonicalID(field.Value)
}
