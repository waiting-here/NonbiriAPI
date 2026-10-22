package resources

import (
	"context"
	"net/http"
)

type ownerResourceDeleter func(context.Context, int64, int64, ControlMutation, int64) (MutationResult[struct{}], error)

func deleteConnectionResource(writer http.ResponseWriter, request *http.Request, principal UserPrincipal, route string, remove ownerResourceDeleter) {
	id, ok := parsePathID(writer, request, "id")
	if !ok || !requireEmptyQuery(writer, request) {
		return
	}
	var body struct {
		ExpectedRevision requestField[string] `json:"expected_revision"`
	}
	if _, ok := decodeStrictObject(writer, request, &body); !ok {
		return
	}
	revision, ok := canonicalExpectedRevision(body.ExpectedRevision)
	if !ok {
		writeResourceError(writer, ErrInvalidRequest)
		return
	}
	canonical := struct {
		ExpectedRevision string `json:"expected_revision"`
	}{body.ExpectedRevision.Value}
	mutation, ok := controlMutation(writer, request, route, []int64{id}, canonical)
	if !ok {
		return
	}
	result, err := remove(request.Context(), principal.UserID, id, mutation, revision)
	if err != nil {
		writeResourceError(writer, err)
		return
	}
	writeMutation(writer, result)
}

func (api *httpAPI) deleteConnection(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	deleteConnectionResource(writer, request, principal, routeConnection, api.repository.DeleteConnection)
}
func (api *httpAPI) deleteCredential(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	deleteConnectionResource(writer, request, principal, routeCredential, api.repository.DeleteCredential)
}
func (api *httpAPI) deleteDiscoverySource(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	deleteConnectionResource(writer, request, principal, routeDiscoverySource, api.repository.DeleteDiscoverySource)
}
