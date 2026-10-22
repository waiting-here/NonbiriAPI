package resources

import "net/http"

func (api *httpAPI) listConnectionSources(w http.ResponseWriter, req *http.Request, p UserPrincipal) {
	id, ok := parsePathID(w, req, "id")
	if !ok || !requireNoBody(w, req) {
		return
	}
	values, window, ok := parseConnectionList(w, req, "q", "enabled")
	if !ok {
		return
	}
	if !nonemptyPageFilters(values, "enabled") {
		writeResourceError(w, ErrInvalidRequest)
		return
	}
	page, err := api.repository.listConnectionSources(req.Context(), p.UserID, id, values.Get("q"), values.Get("enabled"), window)
	if err != nil {
		writeResourceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (api *httpAPI) getDiscoverySource(w http.ResponseWriter, req *http.Request, p UserPrincipal) {
	id, ok := parsePathID(w, req, "id")
	if !ok || !requireNoBody(w, req) || !requireEmptyQuery(w, req) {
		return
	}
	item, err := api.repository.GetDiscoverySource(req.Context(), p.UserID, id)
	if err != nil {
		writeResourceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (api *httpAPI) createDiscoverySource(w http.ResponseWriter, req *http.Request, p UserPrincipal) {
	id, ok := parsePathID(w, req, "id")
	if !ok || !requireEmptyQuery(w, req) {
		return
	}
	var body struct {
		Name         requestField[string] `json:"name"`
		BaseURL      requestField[string] `json:"base_url"`
		ListFormat   requestField[string] `json:"list_format"`
		CredentialID requestField[string] `json:"credential_id"`
		Enabled      requestField[bool]   `json:"enabled"`
	}
	if _, ok := decodeStrictObject(w, req, &body); !ok {
		return
	}
	credentialID, err := parseDecimalID(body.CredentialID.Value)
	if err != nil || !body.BaseURL.Set || !body.ListFormat.Set || !body.Enabled.Set {
		writeResourceError(w, ErrInvalidRequest)
		return
	}
	canonical := struct {
		Name         string `json:"name"`
		BaseURL      string `json:"base_url"`
		ListFormat   string `json:"list_format"`
		CredentialID string `json:"credential_id"`
		Enabled      bool   `json:"enabled"`
	}{body.Name.Value, body.BaseURL.Value, body.ListFormat.Value, body.CredentialID.Value, body.Enabled.Value}
	mutation, ok := controlMutation(w, req, routeConnectionSources, []int64{id}, canonical)
	if !ok {
		return
	}
	result, err := api.repository.CreateDiscoverySource(req.Context(), p.UserID, id, mutation, CreateDiscoverySourceInput{Name: canonical.Name, BaseURL: canonical.BaseURL, ListFormat: canonical.ListFormat, CredentialID: credentialID, Enabled: canonical.Enabled})
	if err != nil {
		writeResourceError(w, err)
		return
	}
	writeMutation(w, result)
}

func (api *httpAPI) patchDiscoverySource(w http.ResponseWriter, req *http.Request, p UserPrincipal) {
	id, ok := parsePathID(w, req, "id")
	if !ok || !requireEmptyQuery(w, req) {
		return
	}
	var body struct {
		Name             requestField[string] `json:"name"`
		BaseURL          requestField[string] `json:"base_url"`
		ListFormat       requestField[string] `json:"list_format"`
		CredentialID     requestField[string] `json:"credential_id"`
		Enabled          requestField[bool]   `json:"enabled"`
		ExpectedRevision requestField[string] `json:"expected_revision"`
	}
	if _, ok := decodeStrictObject(w, req, &body); !ok {
		return
	}
	revision, ok := canonicalExpectedRevision(body.ExpectedRevision)
	credentialID, validID := optionalResourceID(body.CredentialID)
	if !ok || !validID {
		writeResourceError(w, ErrInvalidRequest)
		return
	}
	canonical := struct {
		Name             *string `json:"name,omitempty"`
		BaseURL          *string `json:"base_url,omitempty"`
		ListFormat       *string `json:"list_format,omitempty"`
		CredentialID     *string `json:"credential_id,omitempty"`
		Enabled          *bool   `json:"enabled,omitempty"`
		ExpectedRevision string  `json:"expected_revision"`
	}{optionalPointer(body.Name), optionalPointer(body.BaseURL), optionalPointer(body.ListFormat), optionalPointer(body.CredentialID), optionalPointer(body.Enabled), body.ExpectedRevision.Value}
	mutation, ok := controlMutation(w, req, routeDiscoverySource, []int64{id}, canonical)
	if !ok {
		return
	}
	var credential *int64
	if body.CredentialID.Set {
		credential = credentialID
	}
	result, err := api.repository.PatchDiscoverySource(req.Context(), p.UserID, id, mutation, PatchDiscoverySourceInput{Name: canonical.Name, BaseURL: canonical.BaseURL, ListFormat: canonical.ListFormat, CredentialID: credential, Enabled: canonical.Enabled, ExpectedRevision: revision})
	if err != nil {
		writeResourceError(w, err)
		return
	}
	writeMutation(w, result)
}

func (api *httpAPI) listSourceModels(w http.ResponseWriter, req *http.Request, p UserPrincipal) {
	id, ok := parsePathID(w, req, "id")
	if !ok || !requireNoBody(w, req) {
		return
	}
	values, window, ok := parseConnectionList(w, req, "q", "provider", "native_service", "state")
	if !ok {
		return
	}
	if !nonemptyPageFilters(values, "native_service", "state") {
		writeResourceError(w, ErrInvalidRequest)
		return
	}
	page, err := api.repository.listSourceModels(req.Context(), p.UserID, id, values.Get("q"), values.Get("provider"), values.Get("native_service"), values.Get("state"), window)
	if err != nil {
		writeResourceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (api *httpAPI) refreshDiscoverySource(w http.ResponseWriter, req *http.Request, p UserPrincipal) {
	id, ok := parsePathID(w, req, "id")
	if !ok || !requireEmptyQuery(w, req) {
		return
	}
	var body expectedRevisionRequest
	if _, ok := decodeStrictObject(w, req, &body); !ok {
		return
	}
	revision, ok := canonicalExpectedRevision(body.ExpectedRevision)
	if !ok {
		writeResourceError(w, ErrInvalidRequest)
		return
	}
	mutation, ok := controlMutation(w, req, routeSourceRefresh, []int64{id}, expectedRevisionCanonical{ExpectedRevision: body.ExpectedRevision.Value})
	if !ok {
		return
	}
	result, err := api.repository.RefreshDiscoverySource(req.Context(), p.UserID, id, mutation, revision)
	if err != nil {
		writeResourceError(w, err)
		return
	}
	writeMutation(w, result)
}
