package charityrouting

import (
	"net/http"

	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

func (api *httpAPI) adminEndpointBackoffOptions(w http.ResponseWriter, r *http.Request) {
	api.getEndpointBackoffOptions(w, r, roleAdmin, 0)
}
func (api *httpAPI) stewardEndpointBackoffOptions(w http.ResponseWriter, r *http.Request, p UserPrincipal) {
	api.getEndpointBackoffOptions(w, r, roleSteward, p.UserID)
}
func (api *httpAPI) getEndpointBackoffOptions(w http.ResponseWriter, r *http.Request, role roleKind, actor int64) {
	if !requireNoBody(w, r) || !requireEmptyQuery(w, r) {
		return
	}
	out, err := api.service.endpointBackoffOptions(r.Context(), role, actor)
	if err != nil {
		writeRoutingError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, out)
}

func (api *httpAPI) adminEndpointBackoffPolicies(w http.ResponseWriter, r *http.Request) {
	api.listEndpointBackoffPolicies(w, r, roleAdmin, 0)
}
func (api *httpAPI) stewardEndpointBackoffPolicies(w http.ResponseWriter, r *http.Request, p UserPrincipal) {
	api.listEndpointBackoffPolicies(w, r, roleSteward, p.UserID)
}
func (api *httpAPI) adminEndpointBackoffPolicy(w http.ResponseWriter, r *http.Request) {
	api.getEndpointBackoffPolicy(w, r, roleAdmin, 0)
}
func (api *httpAPI) stewardEndpointBackoffPolicy(w http.ResponseWriter, r *http.Request, p UserPrincipal) {
	api.getEndpointBackoffPolicy(w, r, roleSteward, p.UserID)
}
func (api *httpAPI) adminSaveEndpointBackoffPolicy(w http.ResponseWriter, r *http.Request) {
	api.saveEndpointBackoffPolicy(w, r, roleAdmin, 0)
}
func (api *httpAPI) stewardSaveEndpointBackoffPolicy(w http.ResponseWriter, r *http.Request, p UserPrincipal) {
	api.saveEndpointBackoffPolicy(w, r, roleSteward, p.UserID)
}

func (api *httpAPI) listEndpointBackoffPolicies(w http.ResponseWriter, r *http.Request, role roleKind, actor int64) {
	if !requireNoBody(w, r) {
		return
	}
	values, ok := requestQuery(w, r)
	if !ok {
		return
	}
	if !exactQuery(values, "q", "page", "page_size") {
		writeRoutingError(w, ErrInvalidRequest)
		return
	}
	page, _, err := pagination.Parse(values)
	if err != nil {
		writeRoutingError(w, ErrInvalidRequest)
		return
	}
	out, err := api.service.endpointBackoffPolicies(r.Context(), role, actor, values.Get("q"), page)
	if err != nil {
		writeRoutingError(w, err)
		return
	}
	writeJSON(w, out)
}
func (api *httpAPI) getEndpointBackoffPolicy(w http.ResponseWriter, r *http.Request, role roleKind, actor int64) {
	if !requireNoBody(w, r) {
		return
	}
	values, ok := requestQuery(w, r)
	if !ok {
		return
	}
	if !exactQuery(values, "connector_type", "base_url") || len(values) != 2 {
		writeRoutingError(w, ErrInvalidRequest)
		return
	}
	out, err := api.service.endpointBackoffPolicy(r.Context(), role, actor, values.Get("connector_type"), values.Get("base_url"))
	if err != nil {
		writeRoutingError(w, err)
		return
	}
	writeJSON(w, out)
}
func (api *httpAPI) saveEndpointBackoffPolicy(w http.ResponseWriter, r *http.Request, role roleKind, actor int64) {
	if !requireEmptyQuery(w, r) {
		return
	}
	var input EndpointBackoffPolicyInput
	if !decodeStrictObjectLimit(w, r, &input, 36<<10) {
		return
	}
	route := routeAdminEndpointBackoffPolicy
	if role == roleSteward {
		route = routeStewardEndpointBackoffPolicy
	}
	mutation, ok := mutationFor(w, r, route, nil, input)
	if !ok {
		return
	}
	out, err := api.service.saveEndpointBackoffPolicy(r.Context(), role, actor, mutation, input)
	if err != nil {
		writeRoutingError(w, err)
		return
	}
	writeMutation(w, out)
}
