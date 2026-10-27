package resources

import (
	"context"
	"net/http"
	"net/url"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/httpapi"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

type createEndpointRequest struct {
	ConnectionID    requestField[string]                   `json:"connection_id"`
	ChannelEntryID  requestField[string]                   `json:"channel_entry_id"`
	ChannelRevision requestField[string]                   `json:"channel_revision"`
	NativeServices  requestField[[]contract.NativeService] `json:"native_services"`
	RerankFormat    requestField[contract.RerankFormat]    `json:"rerank_format"`
	Source          requestField[string]                   `json:"source"`
	ChannelID       requestField[string]                   `json:"channel_id"`
	ConnectorType   requestField[string]                   `json:"connector_type"`
	BaseURL         requestField[string]                   `json:"base_url"`
	Note            requestField[string]                   `json:"note"`
	Enabled         requestField[bool]                     `json:"enabled"`
}

type createEndpointCanonical struct {
	ConnectionID    string                    `json:"connection_id,omitempty"`
	ChannelEntryID  string                    `json:"channel_entry_id,omitempty"`
	ChannelRevision string                    `json:"channel_revision,omitempty"`
	NativeServices  *[]contract.NativeService `json:"native_services,omitempty"`
	RerankFormat    contract.RerankFormat     `json:"rerank_format,omitempty"`
	Source          string                    `json:"source"`
	ChannelID       string                    `json:"channel_id,omitempty"`
	ConnectorType   string                    `json:"connector_type,omitempty"`
	BaseURL         string                    `json:"base_url,omitempty"`
	Note            string                    `json:"note"`
	Enabled         bool                      `json:"enabled"`
}

type patchEndpointRequest struct {
	ConnectionID     requestField[string]                   `json:"connection_id"`
	NativeServices   requestField[[]contract.NativeService] `json:"native_services"`
	RerankFormat     requestField[contract.RerankFormat]    `json:"rerank_format"`
	Note             requestField[string]                   `json:"note"`
	Enabled          requestField[bool]                     `json:"enabled"`
	ExpectedRevision requestField[string]                   `json:"expected_revision"`
}

type patchEndpointCanonical struct {
	ConnectionID     *string                   `json:"connection_id,omitempty"`
	NativeServices   *[]contract.NativeService `json:"native_services,omitempty"`
	RerankFormat     *contract.RerankFormat    `json:"rerank_format,omitempty"`
	Note             *string                   `json:"note,omitempty"`
	Enabled          *bool                     `json:"enabled,omitempty"`
	ExpectedRevision string                    `json:"expected_revision"`
}

type expectedRevisionRequest struct {
	ExpectedRevision requestField[string] `json:"expected_revision"`
}

type expectedRevisionCanonical struct {
	ExpectedRevision string `json:"expected_revision"`
}

type createEndpointKeyRequest struct {
	CredentialID       requestField[string]                   `json:"credential_id"`
	NativeServices     requestField[[]contract.NativeService] `json:"native_services"`
	DiscoverySourceID  httpapi.NullableField[string]          `json:"discovery_source_id"`
	Secret             requestField[string]                   `json:"secret"`
	Note               requestField[string]                   `json:"note"`
	Enabled            requestField[bool]                     `json:"enabled"`
	ForceStoreFalse    requestField[bool]                     `json:"force_store_false"`
	OwnershipConfirmed requestField[bool]                     `json:"ownership_confirmed"`
	MaxConcurrency     requestField[int64]                    `json:"max_concurrency"`
	MaxRPM             requestField[int64]                    `json:"max_rpm"`
}

type createEndpointKeyCanonical struct {
	CredentialID       *string                   `json:"credential_id,omitempty"`
	NativeServices     *[]contract.NativeService `json:"native_services,omitempty"`
	DiscoverySourceID  **string                  `json:"discovery_source_id,omitempty"`
	Secret             string                    `json:"secret"`
	Note               string                    `json:"note"`
	Enabled            bool                      `json:"enabled"`
	ForceStoreFalse    bool                      `json:"force_store_false"`
	OwnershipConfirmed bool                      `json:"ownership_confirmed"`
	MaxConcurrency     int64                     `json:"max_concurrency,omitempty"`
	MaxRPM             int64                     `json:"max_rpm,omitempty"`
}

type patchEndpointKeyRequest struct {
	CredentialID      requestField[string]                   `json:"credential_id"`
	NativeServices    requestField[[]contract.NativeService] `json:"native_services"`
	DiscoverySourceID httpapi.NullableField[string]          `json:"discovery_source_id"`
	Note              requestField[string]                   `json:"note"`
	Enabled           requestField[bool]                     `json:"enabled"`
	ForceStoreFalse   requestField[bool]                     `json:"force_store_false"`
	ExpectedRevision  requestField[string]                   `json:"expected_revision"`
	MaxConcurrency    requestField[int64]                    `json:"max_concurrency"`
	MaxRPM            requestField[int64]                    `json:"max_rpm"`
}

type patchEndpointKeyCanonical struct {
	CredentialID      *string                   `json:"credential_id,omitempty"`
	NativeServices    *[]contract.NativeService `json:"native_services,omitempty"`
	DiscoverySourceID **string                  `json:"discovery_source_id,omitempty"`
	Note              *string                   `json:"note,omitempty"`
	Enabled           *bool                     `json:"enabled,omitempty"`
	ForceStoreFalse   *bool                     `json:"force_store_false,omitempty"`
	ExpectedRevision  string                    `json:"expected_revision"`
	MaxConcurrency    *int64                    `json:"max_concurrency,omitempty"`
	MaxRPM            *int64                    `json:"max_rpm,omitempty"`
}

func (api *httpAPI) listEndpoints(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	if !requireNoBody(writer, request) {
		return
	}
	if serveNumberedPage(writer, request, []string{"q", "connector_type", "source", "state"}, func(ctx context.Context, values url.Values, page pagination.Request) (Page[Endpoint], error) {
		if !nonemptyPageFilters(values, "connector_type", "source", "state") {
			return Page[Endpoint]{}, ErrInvalidRequest
		}
		return api.repository.FilterEndpointsPage(ctx, principal.UserID, EndpointPageFilters{Query: values.Get("q"), ConnectorType: values.Get("connector_type"), Source: values.Get("source"), State: values.Get("state")}, page)
	}) {
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
		ConnectionID: body.ConnectionID.Value, ChannelEntryID: body.ChannelEntryID.Value, ChannelRevision: body.ChannelRevision.Value,
		NativeServices: optionalPointer(body.NativeServices), RerankFormat: body.RerankFormat.Value,
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
		if !body.ConnectorType.Set || !body.BaseURL.Set || body.ChannelID.Set || body.ChannelEntryID.Set || body.ChannelRevision.Set || canonical.ConnectorType == "" || canonical.BaseURL == "" {
			writeResourceError(writer, ErrInvalidRequest)
			return
		}
	default:
		writeResourceError(writer, ErrInvalidRequest)
		return
	}
	connectionID, validConnection := optionalResourceID(body.ConnectionID)
	entryID, validEntry := optionalResourceID(body.ChannelEntryID)
	channelRevision, validChannel := optionalResourceID(body.ChannelRevision)
	if !validConnection || !validEntry || !validChannel || body.ChannelEntryID.Set != body.ChannelRevision.Set {
		writeResourceError(writer, ErrInvalidRequest)
		return
	}
	mutation, ok := controlMutation(writer, request, routeEndpoints, nil, canonical)
	if !ok {
		return
	}
	result, err := api.repository.CreateEndpoint(request.Context(), principal.UserID, mutation, CreateEndpointInput{
		ConnectionID: optionalIDValue(connectionID), ChannelEntryID: optionalIDValue(entryID), ChannelRevision: optionalIDValue(channelRevision),
		NativeServices: canonical.NativeServices, RerankFormat: canonical.RerankFormat,
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
	endpoint, err := api.repository.GetEndpointWithBrowse(request.Context(), principal.UserID, endpointID)
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
	if !ok || (!body.Note.Set && !body.Enabled.Set && !body.ConnectionID.Set && !body.NativeServices.Set && !body.RerankFormat.Set) {
		writeResourceError(writer, ErrInvalidRequest)
		return
	}
	canonical := patchEndpointCanonical{
		ConnectionID: optionalPointer(body.ConnectionID), NativeServices: optionalPointer(body.NativeServices), RerankFormat: optionalPointer(body.RerankFormat),
		Note: optionalPointer(body.Note), Enabled: optionalPointer(body.Enabled),
		ExpectedRevision: body.ExpectedRevision.Value,
	}
	connectionID, validConnection := optionalResourceID(body.ConnectionID)
	if !validConnection {
		writeResourceError(writer, ErrInvalidRequest)
		return
	}
	mutation, ok := controlMutation(writer, request, routeEndpoint, []int64{endpointID}, canonical)
	if !ok {
		return
	}
	result, err := api.repository.PatchEndpoint(request.Context(), principal.UserID, endpointID, mutation, PatchEndpointInput{
		ConnectionID: connectionID, NativeServices: canonical.NativeServices, RerankFormat: canonical.RerankFormat,
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
	if serveNumberedPage(writer, request, []string{"q", "enabled", "donated", "suspension_state"}, func(ctx context.Context, values url.Values, page pagination.Request) (Page[EndpointKey], error) {
		if !nonemptyPageFilters(values, "enabled", "donated", "suspension_state") {
			return Page[EndpointKey]{}, ErrInvalidRequest
		}
		return api.repository.FilterEndpointKeysPage(ctx, principal.UserID, endpointID, EndpointKeyPageFilters{Query: values.Get("q"), Enabled: values.Get("enabled"), Donated: values.Get("donated"), SuspensionState: values.Get("suspension_state")}, page)
	}) {
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
	if body.Secret.Set == body.CredentialID.Set || !body.Note.Set || !body.Enabled.Set || !body.ForceStoreFalse.Set ||
		!body.OwnershipConfirmed.Set || !body.OwnershipConfirmed.Value {
		writeResourceError(writer, ErrInvalidRequest)
		return
	}
	credentialID, validCredential := optionalResourceID(body.CredentialID)
	sourceID, validSource := nullableResourceID(body.DiscoverySourceID)
	if !validCredential || !validSource {
		writeResourceError(writer, ErrInvalidRequest)
		return
	}
	canonical := createEndpointKeyCanonical{
		CredentialID: optionalPointer(body.CredentialID), NativeServices: optionalPointer(body.NativeServices),
		Secret: body.Secret.Value, Note: body.Note.Value, Enabled: body.Enabled.Value,
		ForceStoreFalse: body.ForceStoreFalse.Value, OwnershipConfirmed: body.OwnershipConfirmed.Value,
		MaxConcurrency: body.MaxConcurrency.Value, MaxRPM: body.MaxRPM.Value,
	}
	if body.DiscoverySourceID.Set {
		canonical.DiscoverySourceID = &body.DiscoverySourceID.Value
	}
	mutation, ok := controlMutation(writer, request, routeEndpointKeys, []int64{endpointID}, canonical)
	if !ok {
		return
	}
	secretBytes := []byte(canonical.Secret)
	body.Secret.Value, canonical.Secret = "", ""
	defer clear(secretBytes)
	defer clear(mutation.CanonicalBody)
	result, err := api.repository.CreateEndpointKey(request.Context(), principal.UserID, endpointID, mutation, CreateEndpointKeyInput{
		CredentialID: optionalIDValue(credentialID), NativeServices: canonical.NativeServices, DiscoverySourceID: sourceID,
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
	if !ok || (!body.Note.Set && !body.Enabled.Set && !body.ForceStoreFalse.Set && !body.MaxConcurrency.Set && !body.MaxRPM.Set && !body.CredentialID.Set && !body.NativeServices.Set && !body.DiscoverySourceID.Set) {
		writeResourceError(writer, ErrInvalidRequest)
		return
	}
	credentialID, validCredential := optionalResourceID(body.CredentialID)
	sourceID, validSource := nullableResourceID(body.DiscoverySourceID)
	if !validCredential || !validSource {
		writeResourceError(writer, ErrInvalidRequest)
		return
	}
	var credentialPointer *int64
	if body.CredentialID.Set {
		credentialPointer = credentialID
	}
	canonical := patchEndpointKeyCanonical{
		CredentialID: optionalPointer(body.CredentialID), NativeServices: optionalPointer(body.NativeServices),
		Note: optionalPointer(body.Note), Enabled: optionalPointer(body.Enabled),
		ForceStoreFalse: optionalPointer(body.ForceStoreFalse), ExpectedRevision: body.ExpectedRevision.Value,
		MaxConcurrency: optionalPointer(body.MaxConcurrency), MaxRPM: optionalPointer(body.MaxRPM),
	}
	if body.DiscoverySourceID.Set {
		canonical.DiscoverySourceID = &body.DiscoverySourceID.Value
	}
	mutation, ok := controlMutation(writer, request, routeEndpointKey, []int64{endpointID, keyID}, canonical)
	if !ok {
		return
	}
	result, err := api.repository.PatchEndpointKey(request.Context(), principal.UserID, endpointID, keyID, mutation, PatchEndpointKeyInput{
		CredentialID: credentialPointer, NativeServices: canonical.NativeServices, DiscoverySourceID: sourceID,
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

func nullableResourceID(field httpapi.NullableField[string]) (**int64, bool) {
	if !field.Set {
		return nil, true
	}
	var value *int64
	if field.Value != nil {
		id, err := parseDecimalID(*field.Value)
		if err != nil {
			return nil, false
		}
		value = &id
	}
	return &value, true
}
