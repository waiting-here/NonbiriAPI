package resources

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/httpapi"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

type createMainstreamChannelRequest struct {
	Entries         requestField[[]MainstreamEntry]                  `json:"entries"`
	DiscoverySource httpapi.NullableField[MainstreamDiscoveryPreset] `json:"discovery_source"`
	ModelMappings   requestField[[]MainstreamModelMapping]           `json:"model_mappings"`
	Name            requestField[string]                             `json:"name"`
	Category        requestField[string]                             `json:"category"`
	ConnectorType   requestField[string]                             `json:"connector_type"`
	BaseURL         requestField[string]                             `json:"base_url"`
	Enabled         requestField[bool]                               `json:"enabled"`
}

type createMainstreamChannelCanonical struct {
	Entries         *[]MainstreamEntry         `json:"entries,omitempty"`
	DiscoverySource *MainstreamDiscoveryPreset `json:"discovery_source,omitempty"`
	ModelMappings   []MainstreamModelMapping   `json:"model_mappings,omitempty"`
	Name            string                     `json:"name"`
	Category        string                     `json:"category"`
	ConnectorType   string                     `json:"connector_type"`
	BaseURL         string                     `json:"base_url"`
	Enabled         bool                       `json:"enabled"`
}

type patchMainstreamChannelRequest struct {
	Entries          requestField[[]MainstreamEntry]                  `json:"entries"`
	DiscoverySource  httpapi.NullableField[MainstreamDiscoveryPreset] `json:"discovery_source"`
	ModelMappings    requestField[[]MainstreamModelMapping]           `json:"model_mappings"`
	Name             requestField[string]                             `json:"name"`
	Category         requestField[string]                             `json:"category"`
	ConnectorType    requestField[string]                             `json:"connector_type"`
	BaseURL          requestField[string]                             `json:"base_url"`
	Enabled          requestField[bool]                               `json:"enabled"`
	ExpectedRevision requestField[string]                             `json:"expected_revision"`
}

type patchMainstreamChannelCanonical struct {
	Entries          *[]MainstreamEntry          `json:"entries,omitempty"`
	DiscoverySource  **MainstreamDiscoveryPreset `json:"discovery_source,omitempty"`
	ModelMappings    *[]MainstreamModelMapping   `json:"model_mappings,omitempty"`
	Name             *string                     `json:"name,omitempty"`
	Category         *string                     `json:"category,omitempty"`
	ConnectorType    *string                     `json:"connector_type,omitempty"`
	BaseURL          *string                     `json:"base_url,omitempty"`
	Enabled          *bool                       `json:"enabled,omitempty"`
	ExpectedRevision string                      `json:"expected_revision"`
}

type retireMainstreamChannelRequest struct {
	ExpectedRevision requestField[string] `json:"expected_revision"`
	Confirmation     requestField[string] `json:"confirmation"`
}

type retireMainstreamChannelCanonical struct {
	ExpectedRevision string `json:"expected_revision"`
	Confirmation     string `json:"confirmation"`
}

func RegisterAdminRoutes(registrar AdminRouteRegistrar, repository *Repository) error {
	if isNilInterface(registrar) || repository == nil || repository.db == nil || isNilInterface(repository.adminFinalAuth) {
		return ErrInvalidRequest
	}
	api := &httpAPI{repository: repository}
	routes := []struct {
		method  string
		pattern string
		handler AuthorizedAdminHandler
	}{
		{http.MethodGet, "/admin/api/endpoint-create-options", api.adminEndpointCreateOptions},
		{http.MethodGet, routeMainstreamChannels, api.listMainstreamChannels},
		{http.MethodPost, routeMainstreamChannels, api.createMainstreamChannel},
		{http.MethodGet, routeMainstreamChannel, api.getMainstreamChannel},
		{http.MethodPatch, routeMainstreamChannel, api.patchMainstreamChannel},
		{http.MethodDelete, routeMainstreamChannel, api.retireMainstreamChannel},
	}
	for _, route := range routes {
		if err := registrar.RegisterAdminRoute(route.method, route.pattern, route.handler); err != nil {
			return err
		}
	}
	return nil
}

func (api *httpAPI) listMainstreamChannels(writer http.ResponseWriter, request *http.Request, principal AdminPrincipal) {
	if !requireNoBody(writer, request) {
		return
	}
	writer.Header().Set("Cache-Control", "no-store")
	if serveNumberedPage(writer, request, []string{"state"}, func(ctx context.Context, values url.Values, page pagination.Request) (Page[MainstreamChannel], error) {
		if entries, present := values["state"]; present && !validMainstreamChannelState(entries[0]) {
			return Page[MainstreamChannel]{}, ErrInvalidRequest
		}
		return api.repository.ListMainstreamChannelsPage(ctx, principal.UserID, values.Get("state"), page)
	}) {
		return
	}
	values, ok := requestQuery(writer, request)
	if !ok || !exactQuery(values, "state", "cursor", "limit") {
		writeResourceError(writer, ErrInvalidRequest)
		return
	}
	state := mainstreamChannelStateActive
	if value, present := values["state"]; present {
		state = value[0]
		if !validMainstreamChannelState(state) {
			writeResourceError(writer, ErrInvalidRequest)
			return
		}
	}
	limit := 0
	if value, present := values["limit"]; present {
		parsed, err := strconv.Atoi(value[0])
		if err != nil || !validPageLimit(parsed) {
			writeResourceError(writer, ErrInvalidRequest)
			return
		}
		limit = parsed
	}
	cursor := ""
	if value, present := values["cursor"]; present {
		if value[0] == "" {
			writeResourceError(writer, ErrInvalidRequest)
			return
		}
		cursor = value[0]
	}
	page, err := api.repository.ListMainstreamChannels(request.Context(), principal.UserID, state, limit, cursor)
	if err != nil {
		writeResourceError(writer, err)
		return
	}
	writer.Header().Set("Cache-Control", "no-store")
	writeJSON(writer, http.StatusOK, page)
}

func mainstreamChannelPathID(writer http.ResponseWriter, request *http.Request) (string, bool) {
	if request == nil || !validMainstreamChannelID(request.PathValue("id")) {
		writeResourceError(writer, ErrNotFound)
		return "", false
	}
	return request.PathValue("id"), true
}

func (api *httpAPI) getMainstreamChannel(writer http.ResponseWriter, request *http.Request, principal AdminPrincipal) {
	id, ok := mainstreamChannelPathID(writer, request)
	if !ok || !requireEmptyQuery(writer, request) || !requireNoBody(writer, request) {
		return
	}
	item, err := api.repository.GetMainstreamChannel(request.Context(), principal.UserID, id)
	if err != nil {
		writeResourceError(writer, err)
		return
	}
	writer.Header().Set("Cache-Control", "no-store")
	writeJSON(writer, http.StatusOK, item)
}

func (api *httpAPI) createMainstreamChannel(writer http.ResponseWriter, request *http.Request, principal AdminPrincipal) {
	if !requireEmptyQuery(writer, request) {
		return
	}
	var body createMainstreamChannelRequest
	if _, ok := decodeStrictObject(writer, request, &body); !ok {
		return
	}
	if !body.Name.Set || !body.Category.Set || !body.Enabled.Set ||
		(!body.Entries.Set && (!body.ConnectorType.Set || !body.BaseURL.Set)) ||
		(body.Entries.Set && (body.ConnectorType.Set || body.BaseURL.Set)) {
		writeResourceError(writer, ErrInvalidRequest)
		return
	}
	canonical := createMainstreamChannelCanonical{
		Entries: optionalPointer(body.Entries), DiscoverySource: body.DiscoverySource.Value, ModelMappings: body.ModelMappings.Value,
		Name: body.Name.Value, Category: body.Category.Value, ConnectorType: body.ConnectorType.Value,
		BaseURL: body.BaseURL.Value, Enabled: body.Enabled.Value,
	}
	mutation, ok := adminMutationForTextPath(writer, request, routeMainstreamChannels, nil, canonical)
	if !ok {
		return
	}
	result, err := api.repository.CreateMainstreamChannel(request.Context(), principal.UserID, mutation, CreateMainstreamChannelInput{
		Entries: canonical.Entries, DiscoverySource: canonical.DiscoverySource, ModelMappings: canonical.ModelMappings,
		Name: canonical.Name, Category: canonical.Category, ConnectorType: canonical.ConnectorType,
		BaseURL: canonical.BaseURL, Enabled: canonical.Enabled,
	})
	if err != nil {
		writeResourceError(writer, err)
		return
	}
	writeMutation(writer, result)
}

func (api *httpAPI) patchMainstreamChannel(writer http.ResponseWriter, request *http.Request, principal AdminPrincipal) {
	id, ok := mainstreamChannelPathID(writer, request)
	if !ok || !requireEmptyQuery(writer, request) {
		return
	}
	var body patchMainstreamChannelRequest
	if _, ok := decodeStrictObject(writer, request, &body); !ok {
		return
	}
	revision, ok := canonicalExpectedRevision(body.ExpectedRevision)
	if !ok || (!body.Name.Set && !body.Category.Set && !body.ConnectorType.Set && !body.BaseURL.Set && !body.Enabled.Set && !body.Entries.Set && !body.DiscoverySource.Set && !body.ModelMappings.Set) ||
		(body.Entries.Set && (body.ConnectorType.Set || body.BaseURL.Set)) {
		writeResourceError(writer, ErrInvalidRequest)
		return
	}
	canonical := patchMainstreamChannelCanonical{
		Entries: optionalPointer(body.Entries), ModelMappings: optionalPointer(body.ModelMappings),
		Name: optionalPointer(body.Name), Category: optionalPointer(body.Category),
		ConnectorType: optionalPointer(body.ConnectorType), BaseURL: optionalPointer(body.BaseURL),
		Enabled: optionalPointer(body.Enabled), ExpectedRevision: body.ExpectedRevision.Value,
	}
	if body.DiscoverySource.Set {
		canonical.DiscoverySource = &body.DiscoverySource.Value
	}
	mutation, ok := adminMutationForTextPath(writer, request, routeMainstreamChannel, []string{id}, canonical)
	if !ok {
		return
	}
	result, err := api.repository.PatchMainstreamChannel(request.Context(), principal.UserID, id, mutation, PatchMainstreamChannelInput{
		Entries: canonical.Entries, DiscoverySource: canonical.DiscoverySource, ModelMappings: canonical.ModelMappings,
		Name: canonical.Name, Category: canonical.Category, ConnectorType: canonical.ConnectorType,
		BaseURL: canonical.BaseURL, Enabled: canonical.Enabled, ExpectedRevision: revision,
	})
	if err != nil {
		writeResourceError(writer, err)
		return
	}
	writeMutation(writer, result)
}

func (api *httpAPI) retireMainstreamChannel(writer http.ResponseWriter, request *http.Request, principal AdminPrincipal) {
	id, ok := mainstreamChannelPathID(writer, request)
	if !ok || !requireEmptyQuery(writer, request) {
		return
	}
	var body retireMainstreamChannelRequest
	if _, ok := decodeStrictObject(writer, request, &body); !ok {
		return
	}
	revision, ok := canonicalExpectedRevision(body.ExpectedRevision)
	if !ok || !body.Confirmation.Set || body.Confirmation.Value != "retire" {
		writeResourceError(writer, ErrInvalidRequest)
		return
	}
	canonical := retireMainstreamChannelCanonical{ExpectedRevision: body.ExpectedRevision.Value, Confirmation: body.Confirmation.Value}
	mutation, ok := adminMutationForTextPath(writer, request, routeMainstreamChannel, []string{id}, canonical)
	if !ok {
		return
	}
	result, err := api.repository.RetireMainstreamChannel(request.Context(), principal.UserID, id, mutation, revision)
	if err != nil {
		writeResourceError(writer, err)
		return
	}
	writeMutation(writer, result)
}

func (api *httpAPI) endpointCreateOptions(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	values, ok := requestQuery(writer, request)
	if !ok {
		return
	}
	if !exactQuery(values, "match_base_url") || len(values.Get("match_base_url")) > 4096 || !safeMainstreamMatchInput(values.Get("match_base_url")) {
		writeResourceError(writer, ErrInvalidRequest)
		return
	}
	if !requireNoBody(writer, request) {
		return
	}
	options, err := api.repository.GetEndpointCreateOptions(request.Context(), principal.UserID)
	if err != nil {
		writeResourceError(writer, err)
		return
	}
	if values.Has("match_base_url") {
		matches := matchingMainstreamEntries(options.MainstreamChannels, values.Get("match_base_url"))
		options.MatchingMainstreamEntries = &matches
	}
	writer.Header().Set("Cache-Control", "no-store")
	writeJSON(writer, http.StatusOK, options)
}

func (api *httpAPI) adminEndpointCreateOptions(writer http.ResponseWriter, request *http.Request, principal AdminPrincipal) {
	if !requireEmptyQuery(writer, request) || !requireNoBody(writer, request) {
		return
	}
	options, err := api.repository.GetAdminEndpointCreateOptions(request.Context(), principal.UserID)
	if err != nil {
		writeResourceError(writer, err)
		return
	}
	writer.Header().Set("Cache-Control", "no-store")
	writeJSON(writer, http.StatusOK, options)
}
