package resources

import (
	"net/http"
	"strconv"
)

type createMainstreamChannelRequest struct {
	Name          requestField[string] `json:"name"`
	Category      requestField[string] `json:"category"`
	ConnectorType requestField[string] `json:"connector_type"`
	BaseURL       requestField[string] `json:"base_url"`
	Enabled       requestField[bool]   `json:"enabled"`
}

type createMainstreamChannelCanonical struct {
	Name          string `json:"name"`
	Category      string `json:"category"`
	ConnectorType string `json:"connector_type"`
	BaseURL       string `json:"base_url"`
	Enabled       bool   `json:"enabled"`
}

type patchMainstreamChannelRequest struct {
	Name             requestField[string] `json:"name"`
	Category         requestField[string] `json:"category"`
	ConnectorType    requestField[string] `json:"connector_type"`
	BaseURL          requestField[string] `json:"base_url"`
	Enabled          requestField[bool]   `json:"enabled"`
	ExpectedRevision requestField[string] `json:"expected_revision"`
}

type patchMainstreamChannelCanonical struct {
	Name             *string `json:"name,omitempty"`
	Category         *string `json:"category,omitempty"`
	ConnectorType    *string `json:"connector_type,omitempty"`
	BaseURL          *string `json:"base_url,omitempty"`
	Enabled          *bool   `json:"enabled,omitempty"`
	ExpectedRevision string  `json:"expected_revision"`
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
	if !body.Name.Set || !body.Category.Set || !body.ConnectorType.Set || !body.BaseURL.Set || !body.Enabled.Set {
		writeResourceError(writer, ErrInvalidRequest)
		return
	}
	canonical := createMainstreamChannelCanonical{
		Name: body.Name.Value, Category: body.Category.Value, ConnectorType: body.ConnectorType.Value,
		BaseURL: body.BaseURL.Value, Enabled: body.Enabled.Value,
	}
	mutation, ok := adminMutationForTextPath(writer, request, routeMainstreamChannels, nil, canonical)
	if !ok {
		return
	}
	result, err := api.repository.CreateMainstreamChannel(request.Context(), principal.UserID, mutation, CreateMainstreamChannelInput{
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
	if !ok || (!body.Name.Set && !body.Category.Set && !body.ConnectorType.Set && !body.BaseURL.Set && !body.Enabled.Set) {
		writeResourceError(writer, ErrInvalidRequest)
		return
	}
	canonical := patchMainstreamChannelCanonical{
		Name: optionalPointer(body.Name), Category: optionalPointer(body.Category),
		ConnectorType: optionalPointer(body.ConnectorType), BaseURL: optionalPointer(body.BaseURL),
		Enabled: optionalPointer(body.Enabled), ExpectedRevision: body.ExpectedRevision.Value,
	}
	mutation, ok := adminMutationForTextPath(writer, request, routeMainstreamChannel, []string{id}, canonical)
	if !ok {
		return
	}
	result, err := api.repository.PatchMainstreamChannel(request.Context(), principal.UserID, id, mutation, PatchMainstreamChannelInput{
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
	if !requireEmptyQuery(writer, request) || !requireNoBody(writer, request) {
		return
	}
	options, err := api.repository.GetEndpointCreateOptions(request.Context(), principal.UserID)
	if err != nil {
		writeResourceError(writer, err)
		return
	}
	writer.Header().Set("Cache-Control", "no-store")
	writeJSON(writer, http.StatusOK, options)
}
