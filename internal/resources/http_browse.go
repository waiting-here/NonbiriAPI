package resources

import (
	"net/http"

	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

const routeKeyBindings = "/api/endpoints/{id}/keys/{keyId}/bindings"

func (api *httpAPI) listKeyBindings(writer http.ResponseWriter, request *http.Request, principal UserPrincipal) {
	endpointID, ok := parsePathID(writer, request, "id")
	if !ok {
		return
	}
	keyID, ok := parsePathID(writer, request, "keyId")
	if !ok || !requireNoBody(writer, request) {
		return
	}
	values, ok := requestQuery(writer, request)
	if !ok {
		return
	}
	if !exactQuery(values, "page", "page_size", "upstream_model_id") {
		writeResourceError(writer, ErrInvalidRequest)
		return
	}
	if text, present := values["upstream_model_id"]; present && (len(text) != 1 || text[0] == "") {
		writeResourceError(writer, ErrInvalidRequest)
		return
	}
	page, _, err := pagination.Parse(values)
	if err != nil {
		writeResourceError(writer, ErrInvalidRequest)
		return
	}
	result, err := api.repository.ListKeyBindingsPage(request.Context(), principal.UserID, endpointID, keyID, values.Get("upstream_model_id"), page)
	if err != nil {
		writeResourceError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, result)
}
