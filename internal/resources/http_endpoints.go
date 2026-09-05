package resources

import (
	"net/http"
)

type createEndpointRequest struct {
	Source        requestField[string] `json:"source"`
	ChannelID     requestField[string] `json:"channel_id"`
	ConnectorType requestField[string] `json:"connector_type"`
	BaseURL       requestField[string] `json:"base_url"`
	Note          requestField[string] `json:"note"`
	Enabled       requestField[bool]   `json:"enabled"`
}

type createEndpointCanonical struct {
	Source        string `json:"source"`
	ChannelID     string `json:"channel_id,omitempty"`
	ConnectorType string `json:"connector_type,omitempty"`
	BaseURL       string `json:"base_url,omitempty"`
	Note          string `json:"note"`
	Enabled       bool   `json:"enabled"`
}

type patchEndpointRequest struct {
	Note             requestField[string] `json:"note"`
	Enabled          requestField[bool]   `json:"enabled"`
	ExpectedRevision requestField[string] `json:"expected_revision"`
}

type patchEndpointCanonical struct {
	Note             *string `json:"note,omitempty"`
	Enabled          *bool   `json:"enabled,omitempty"`
	ExpectedRevision string  `json:"expected_revision"`
}

type expectedRevisionRequest struct {
	ExpectedRevision requestField[string] `json:"expected_revision"`
}

type expectedRevisionCanonical struct {
	ExpectedRevision string `json:"expected_revision"`
}

type createEndpointKeyRequest struct {
	Secret             requestField[string] `json:"secret"`
	Note               requestField[string] `json:"note"`
	Enabled            requestField[bool]   `json:"enabled"`
	ForceStoreFalse    requestField[bool]   `json:"force_store_false"`
	OwnershipConfirmed requestField[bool]   `json:"ownership_confirmed"`
	MaxConcurrency     requestField[int64]  `json:"max_concurrency"`
	MaxRPM             requestField[int64]  `json:"max_rpm"`
}

type createEndpointKeyCanonical struct {
	Secret             string `json:"secret"`
	Note               string `json:"note"`
	Enabled            bool   `json:"enabled"`
	ForceStoreFalse    bool   `json:"force_store_false"`
	OwnershipConfirmed bool   `json:"ownership_confirmed"`
	MaxConcurrency     int64  `json:"max_concurrency,omitempty"`
	MaxRPM             int64  `json:"max_rpm,omitempty"`
}

type patchEndpointKeyRequest struct {
	Note             requestField[string] `json:"note"`
	Enabled          requestField[bool]   `json:"enabled"`
	ForceStoreFalse  requestField[bool]   `json:"force_store_false"`
	ExpectedRevision requestField[string] `json:"expected_revision"`
	MaxConcurrency   requestField[int64]  `json:"max_concurrency"`
	MaxRPM           requestField[int64]  `json:"max_rpm"`
}

type patchEndpointKeyCanonical struct {
	Note             *string `json:"note,omitempty"`
	Enabled          *bool   `json:"enabled,omitempty"`
	ForceStoreFalse  *bool   `json:"force_store_false,omitempty"`
	ExpectedRevision string  `json:"expected_revision"`
	MaxConcurrency   *int64  `json:"max_concurrency,omitempty"`
	MaxRPM           *int64  `json:"max_rpm,omitempty"`
}

func (api *httpAPI) listEndpoints(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	if !requireNoBody(writer, request) {
		return
	}
	limit, cursor, ok := parsePageQuery(writer, request)
	if !ok {
		return
	}
	page, err := api.repository.ListEndpoints(request.Context(), principal.UserID, limit, cursor)
	if err != nil {
		writeResourceError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, page)
}

func (api *httpAPI) createEndpoint(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	if !requireEmptyQuery(writer, request) {
		return
	}
	var body createEndpointRequest
	if _, ok := decodeStrictObject(writer, request, &body); !ok {
		return
	}
	if !body.Source.Set || !body.Note.Set || !body.Enabled.Set {
		writeResourceError(writer, ErrInvalidRequest)
		return
	}
	canonical := createEndpointCanonical{
		Source: body.Source.Value, ChannelID: body.ChannelID.Value,
		ConnectorType: body.ConnectorType.Value, BaseURL: body.BaseURL.Value,
		Note: body.Note.Value, Enabled: body.Enabled.Value,
	}
	switch canonical.Source {
	case "mainstream":
		if !body.ChannelID.Set || body.ConnectorType.Set || body.BaseURL.Set || canonical.ChannelID == "" {
			writeResourceError(writer, ErrInvalidRequest)
			return
		}
	case "custom":
		if !body.ConnectorType.Set || !body.BaseURL.Set || body.ChannelID.Set || canonical.ConnectorType == "" || canonical.BaseURL == "" {
			writeResourceError(writer, ErrInvalidRequest)
			return
		}
	default:
		writeResourceError(writer, ErrInvalidRequest)
		return
	}
	mutation, ok := controlMutation(writer, request, routeEndpoints, nil, canonical)
	if !ok {
		return
	}
	result, err := api.repository.CreateEndpoint(request.Context(), principal.UserID, mutation, CreateEndpointInput{
		Source: canonical.Source, ChannelID: canonical.ChannelID,
		ConnectorType: canonical.ConnectorType, BaseURL: canonical.BaseURL,
		Note: canonical.Note, Enabled: canonical.Enabled,
	})
	if err != nil {
		writeResourceError(writer, err)
		return
	}
	writeMutation(writer, result)
}

func (api *httpAPI) getEndpoint(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	endpointID, ok := parsePathID(writer, request, "id")
	if !ok || !requireEmptyQuery(writer, request) || !requireNoBody(writer, request) {
		return
	}
	endpoint, err := api.repository.GetEndpoint(request.Context(), principal.UserID, endpointID)
	if err != nil {
		writeResourceError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, endpoint)
}

func (api *httpAPI) patchEndpoint(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	endpointID, ok := parsePathID(writer, request, "id")
	if !ok || !requireEmptyQuery(writer, request) {
		return
	}
	var body patchEndpointRequest
	if _, ok := decodeStrictObject(writer, request, &body); !ok {
		return
	}
	revision, ok := canonicalExpectedRevision(body.ExpectedRevision)
	if !ok || (!body.Note.Set && !body.Enabled.Set) {
		writeResourceError(writer, ErrInvalidRequest)
		return
	}
	canonical := patchEndpointCanonical{
		Note: optionalPointer(body.Note), Enabled: optionalPointer(body.Enabled),
		ExpectedRevision: body.ExpectedRevision.Value,
	}
	mutation, ok := controlMutation(writer, request, routeEndpoint, []int64{endpointID}, canonical)
	if !ok {
		return
	}
	result, err := api.repository.PatchEndpoint(request.Context(), principal.UserID, endpointID, mutation, PatchEndpointInput{
		Note: canonical.Note, Enabled: canonical.Enabled, ExpectedRevision: revision,
	})
	if err != nil {
		writeResourceError(writer, err)
		return
	}
	writeMutation(writer, result)
}

func (api *httpAPI) deleteEndpoint(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	endpointID, ok := parsePathID(writer, request, "id")
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
	mutation, ok := controlMutation(writer, request, routeEndpoint, []int64{endpointID}, canonical)
	if !ok {
		return
	}
	result, err := api.repository.DeleteEndpoint(request.Context(), principal.UserID, endpointID, mutation, revision)
	if err != nil {
		writeResourceError(writer, err)
		return
	}
	writeMutation(writer, result)
}

func (api *httpAPI) listEndpointKeys(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	endpointID, ok := parsePathID(writer, request, "id")
	if !ok || !requireNoBody(writer, request) {
		return
	}
	limit, cursor, ok := parsePageQuery(writer, request)
	if !ok {
		return
	}
	page, err := api.repository.ListEndpointKeys(request.Context(), principal.UserID, endpointID, limit, cursor)
	if err != nil {
		writeResourceError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, page)
}

func (api *httpAPI) createEndpointKey(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	endpointID, ok := parsePathID(writer, request, "id")
	if !ok || !requireEmptyQuery(writer, request) {
		return
	}
	var body createEndpointKeyRequest
	if _, ok := decodeStrictObject(writer, request, &body); !ok {
		return
	}
	if !body.Secret.Set || !body.Note.Set || !body.Enabled.Set || !body.ForceStoreFalse.Set ||
		!body.OwnershipConfirmed.Set || !body.OwnershipConfirmed.Value {
		writeResourceError(writer, ErrInvalidRequest)
		return
	}
	canonical := createEndpointKeyCanonical{
		Secret: body.Secret.Value, Note: body.Note.Value, Enabled: body.Enabled.Value,
		ForceStoreFalse: body.ForceStoreFalse.Value, OwnershipConfirmed: body.OwnershipConfirmed.Value,
		MaxConcurrency: body.MaxConcurrency.Value, MaxRPM: body.MaxRPM.Value,
	}
	mutation, ok := controlMutation(writer, request, routeEndpointKeys, []int64{endpointID}, canonical)
	if !ok {
		return
	}
	secretBytes := []byte(canonical.Secret)
	defer clear(secretBytes)
	result, err := api.repository.CreateEndpointKey(request.Context(), principal.UserID, endpointID, mutation, CreateEndpointKeyInput{
		Secret: secretBytes, Note: canonical.Note, Enabled: canonical.Enabled,
		ForceStoreFalse: canonical.ForceStoreFalse, OwnershipConfirmed: canonical.OwnershipConfirmed,
		MaxConcurrency: canonical.MaxConcurrency, MaxRPM: canonical.MaxRPM,
	})
	if err != nil {
		writeResourceError(writer, err)
		return
	}
	writeMutation(writer, result)
}

func (api *httpAPI) patchEndpointKey(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	endpointID, ok := parsePathID(writer, request, "id")
	if !ok {
		return
	}
	keyID, ok := parsePathID(writer, request, "keyId")
	if !ok || !requireEmptyQuery(writer, request) {
		return
	}
	var body patchEndpointKeyRequest
	if _, ok := decodeStrictObject(writer, request, &body); !ok {
		return
	}
	revision, ok := canonicalExpectedRevision(body.ExpectedRevision)
	if !ok || (!body.Note.Set && !body.Enabled.Set && !body.ForceStoreFalse.Set && !body.MaxConcurrency.Set && !body.MaxRPM.Set) {
		writeResourceError(writer, ErrInvalidRequest)
		return
	}
	canonical := patchEndpointKeyCanonical{
		Note: optionalPointer(body.Note), Enabled: optionalPointer(body.Enabled),
		ForceStoreFalse: optionalPointer(body.ForceStoreFalse), ExpectedRevision: body.ExpectedRevision.Value,
		MaxConcurrency: optionalPointer(body.MaxConcurrency), MaxRPM: optionalPointer(body.MaxRPM),
	}
	mutation, ok := controlMutation(writer, request, routeEndpointKey, []int64{endpointID, keyID}, canonical)
	if !ok {
		return
	}
	result, err := api.repository.PatchEndpointKey(request.Context(), principal.UserID, endpointID, keyID, mutation, PatchEndpointKeyInput{
		Note: canonical.Note, Enabled: canonical.Enabled, ForceStoreFalse: canonical.ForceStoreFalse,
		MaxConcurrency: canonical.MaxConcurrency, MaxRPM: canonical.MaxRPM,
		ExpectedRevision: revision,
	})
	if err != nil {
		writeResourceError(writer, err)
		return
	}
	writeMutation(writer, result)
}

func (api *httpAPI) deleteEndpointKey(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	endpointID, ok := parsePathID(writer, request, "id")
	if !ok {
		return
	}
	keyID, ok := parsePathID(writer, request, "keyId")
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
	mutation, ok := controlMutation(writer, request, routeEndpointKey, []int64{endpointID, keyID}, canonical)
	if !ok {
		return
	}
	result, err := api.repository.DeleteEndpointKey(request.Context(), principal.UserID, endpointID, keyID, mutation, revision)
	if err != nil {
		writeResourceError(writer, err)
		return
	}
	writeMutation(writer, result)
}
