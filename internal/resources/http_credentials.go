package resources

import "net/http"

func (api *httpAPI) listConnectionCredentials(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	id, ok := parsePathID(writer, request, "id")
	if !ok || !requireNoBody(writer, request) {
		return
	}
	values, window, ok := parseConnectionList(writer, request, "q", "enabled")
	if !ok {
		return
	}
	if !nonemptyPageFilters(values, "enabled") {
		writeResourceError(writer, ErrInvalidRequest)
		return
	}
	page, err := api.repository.listConnectionCredentials(request.Context(), principal.UserID, id, values.Get("q"), values.Get("enabled"), window)
	if err != nil {
		writeResourceError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, page)
}

func (api *httpAPI) getCredential(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	id, ok := parsePathID(writer, request, "id")
	if !ok || !requireNoBody(writer, request) || !requireEmptyQuery(writer, request) {
		return
	}
	item, err := api.repository.GetCredential(request.Context(), principal.UserID, id)
	if err != nil {
		writeResourceError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, item)
}

func (api *httpAPI) createCredential(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	id, ok := parsePathID(writer, request, "id")
	if !ok || !requireEmptyQuery(writer, request) {
		return
	}
	var body struct {
		Secret             requestField[string] `json:"secret"`
		Note               requestField[string] `json:"note"`
		OwnershipConfirmed requestField[bool]   `json:"ownership_confirmed"`
	}
	if _, ok := decodeStrictObject(writer, request, &body); !ok {
		return
	}
	if !body.Secret.Set || !body.OwnershipConfirmed.Set || !body.OwnershipConfirmed.Value {
		writeResourceError(writer, ErrInvalidRequest)
		return
	}
	canonical := struct {
		Secret             string `json:"secret"`
		Note               string `json:"note"`
		OwnershipConfirmed bool   `json:"ownership_confirmed"`
	}{body.Secret.Value, body.Note.Value, true}
	mutation, ok := controlMutation(writer, request, routeConnectionCredentials, []int64{id}, canonical)
	if !ok {
		return
	}
	plaintext := []byte(body.Secret.Value)
	body.Secret.Value, canonical.Secret = "", ""
	defer clear(plaintext)
	defer clear(mutation.CanonicalBody)
	result, err := api.repository.CreateCredential(request.Context(), principal.UserID, id, mutation, CreateCredentialInput{
		Secret: plaintext, Note: canonical.Note, OwnershipConfirmed: true,
	})
	if err != nil {
		writeResourceError(writer, err)
		return
	}
	writeMutation(writer, result)
}

func (api *httpAPI) patchCredential(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	id, ok := parsePathID(writer, request, "id")
	if !ok || !requireEmptyQuery(writer, request) {
		return
	}
	var body struct {
		Note             requestField[string] `json:"note"`
		Enabled          requestField[bool]   `json:"enabled"`
		ExpectedRevision requestField[string] `json:"expected_revision"`
	}
	if _, ok := decodeStrictObject(writer, request, &body); !ok {
		return
	}
	revision, ok := canonicalExpectedRevision(body.ExpectedRevision)
	if !ok || (!body.Note.Set && !body.Enabled.Set) {
		writeResourceError(writer, ErrInvalidRequest)
		return
	}
	canonical := struct {
		Note             *string `json:"note,omitempty"`
		Enabled          *bool   `json:"enabled,omitempty"`
		ExpectedRevision string  `json:"expected_revision"`
	}{optionalPointer(body.Note), optionalPointer(body.Enabled), body.ExpectedRevision.Value}
	mutation, ok := controlMutation(writer, request, routeCredential, []int64{id}, canonical)
	if !ok {
		return
	}
	result, err := api.repository.PatchCredential(request.Context(), principal.UserID, id, mutation, PatchCredentialInput{
		Note: canonical.Note, Enabled: canonical.Enabled, ExpectedRevision: revision,
	})
	if err != nil {
		writeResourceError(writer, err)
		return
	}
	writeMutation(writer, result)
}

func (api *httpAPI) listCredentialVersions(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	id, ok := parsePathID(writer, request, "id")
	if !ok || !requireNoBody(writer, request) {
		return
	}
	_, window, ok := parseConnectionList(writer, request)
	if !ok {
		return
	}
	page, err := api.repository.listCredentialVersions(request.Context(), principal.UserID, id, window)
	if err != nil {
		writeResourceError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, page)
}

func (api *httpAPI) patchCredentialVersion(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	id, validID := parsePathID(writer, request, "id")
	version, validVersion := parsePathID(writer, request, "version")
	if !validID || !validVersion || !requireEmptyQuery(writer, request) {
		return
	}
	var body struct {
		Enabled          requestField[bool]   `json:"enabled"`
		ExpectedRevision requestField[string] `json:"expected_revision"`
	}
	if _, ok := decodeStrictObject(writer, request, &body); !ok {
		return
	}
	revision, ok := canonicalExpectedRevision(body.ExpectedRevision)
	if !ok || !body.Enabled.Set {
		writeResourceError(writer, ErrInvalidRequest)
		return
	}
	canonical := struct {
		Enabled          bool   `json:"enabled"`
		ExpectedRevision string `json:"expected_revision"`
	}{body.Enabled.Value, body.ExpectedRevision.Value}
	mutation, ok := controlMutation(writer, request, routeCredentialVersion, []int64{id, version}, canonical)
	if !ok {
		return
	}
	result, err := api.repository.PatchCredentialVersion(request.Context(), principal.UserID, id, version, mutation, revision, canonical.Enabled)
	if err != nil {
		writeResourceError(writer, err)
		return
	}
	writeMutation(writer, result)
}

func (api *httpAPI) rotateCredential(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	id, ok := parsePathID(writer, request, "id")
	if !ok || !requireEmptyQuery(writer, request) {
		return
	}
	var body struct {
		Secret             requestField[string] `json:"secret"`
		OwnershipConfirmed requestField[bool]   `json:"ownership_confirmed"`
		ExpectedRevision   requestField[string] `json:"expected_revision"`
	}
	if _, ok := decodeStrictObject(writer, request, &body); !ok {
		return
	}
	revision, ok := canonicalExpectedRevision(body.ExpectedRevision)
	if !ok || !body.Secret.Set || !body.OwnershipConfirmed.Set || !body.OwnershipConfirmed.Value {
		writeResourceError(writer, ErrInvalidRequest)
		return
	}
	canonical := struct {
		Secret             string `json:"secret"`
		OwnershipConfirmed bool   `json:"ownership_confirmed"`
		ExpectedRevision   string `json:"expected_revision"`
	}{body.Secret.Value, true, body.ExpectedRevision.Value}
	mutation, ok := controlMutation(writer, request, routeCredentialVersions, []int64{id}, canonical)
	if !ok {
		return
	}
	plaintext := []byte(body.Secret.Value)
	body.Secret.Value, canonical.Secret = "", ""
	defer clear(plaintext)
	defer clear(mutation.CanonicalBody)
	result, err := api.repository.RotateCredential(request.Context(), principal.UserID, id, mutation, RotateCredentialInput{Secret: plaintext, OwnershipConfirmed: true, ExpectedRevision: revision})
	if err != nil {
		writeResourceError(writer, err)
		return
	}
	writeMutation(writer, result)
}

func (api *httpAPI) credentialImpact(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	id, ok := parsePathID(writer, request, "id")
	if !ok || !requireNoBody(writer, request) {
		return
	}
	values, window, ok := parseConnectionList(writer, request, "kind")
	if !ok {
		return
	}
	result, err := api.repository.listCredentialImpact(request.Context(), principal.UserID, id, values.Get("kind"), window)
	if err != nil {
		writeResourceError(writer, err)
		return
	}
	writer.Header().Set("Cache-Control", "no-store")
	writeJSON(writer, http.StatusOK, result)
}
