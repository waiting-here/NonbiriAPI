package resources

import "net/http"

func (api *httpAPI) listConnectionEntries(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	id, ok := parsePathID(writer, request, "id")
	if !ok || !requireNoBody(writer, request) {
		return
	}
	values, window, ok := parseConnectionList(writer, request, "q", "connector_type", "state")
	if !ok {
		return
	}
	if !nonemptyPageFilters(values, "connector_type", "state") {
		writeResourceError(writer, ErrInvalidRequest)
		return
	}
	page, err := api.repository.listConnectionEntries(request.Context(), principal.UserID, id, EndpointPageFilters{
		Query: values.Get("q"), ConnectorType: values.Get("connector_type"), State: values.Get("state"),
	}, window)
	if err != nil {
		writeResourceError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, page)
}

func (api *httpAPI) listConnections(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	if !requireNoBody(writer, request) {
		return
	}
	values, window, ok := parseConnectionList(writer, request, "q")
	if !ok {
		return
	}
	page, err := api.repository.listConnections(request.Context(), principal.UserID, values.Get("q"), window)
	if err != nil {
		writeResourceError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, page)
}

func (api *httpAPI) getConnection(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	id, ok := parsePathID(writer, request, "id")
	if !ok || !requireNoBody(writer, request) || !requireEmptyQuery(writer, request) {
		return
	}
	item, err := api.repository.GetConnection(request.Context(), principal.UserID, id)
	if err != nil {
		writeResourceError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, item)
}

func (api *httpAPI) createConnection(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	if !requireEmptyQuery(writer, request) {
		return
	}
	var body struct {
		Name requestField[string] `json:"name"`
		Note requestField[string] `json:"note"`
	}
	if _, ok := decodeStrictObject(writer, request, &body); !ok {
		return
	}
	if !body.Name.Set {
		writeResourceError(writer, ErrInvalidRequest)
		return
	}
	canonical := struct {
		Name string `json:"name"`
		Note string `json:"note"`
	}{body.Name.Value, body.Note.Value}
	mutation, ok := controlMutation(writer, request, routeConnections, nil, canonical)
	if !ok {
		return
	}
	result, err := api.repository.CreateConnection(request.Context(), principal.UserID, mutation, ConnectionInput{Name: canonical.Name, Note: canonical.Note})
	if err != nil {
		writeResourceError(writer, err)
		return
	}
	writeMutation(writer, result)
}

func (api *httpAPI) patchConnection(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	id, ok := parsePathID(writer, request, "id")
	if !ok || !requireEmptyQuery(writer, request) {
		return
	}
	var body struct {
		Name             requestField[string] `json:"name"`
		Note             requestField[string] `json:"note"`
		ExpectedRevision requestField[string] `json:"expected_revision"`
	}
	if _, ok := decodeStrictObject(writer, request, &body); !ok {
		return
	}
	revision, ok := canonicalExpectedRevision(body.ExpectedRevision)
	if !ok || (!body.Name.Set && !body.Note.Set) {
		writeResourceError(writer, ErrInvalidRequest)
		return
	}
	canonical := struct {
		Name             *string `json:"name,omitempty"`
		Note             *string `json:"note,omitempty"`
		ExpectedRevision string  `json:"expected_revision"`
	}{optionalPointer(body.Name), optionalPointer(body.Note), body.ExpectedRevision.Value}
	mutation, ok := controlMutation(writer, request, routeConnection, []int64{id}, canonical)
	if !ok {
		return
	}
	result, err := api.repository.PatchConnection(request.Context(), principal.UserID, id, mutation, PatchConnectionInput{
		Name: canonical.Name, Note: canonical.Note, ExpectedRevision: revision,
	})
	if err != nil {
		writeResourceError(writer, err)
		return
	}
	writeMutation(writer, result)
}
