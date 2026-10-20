package charityrouting

import (
	"net/http"

	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

func (api *httpAPI) adminBindingPresets(w http.ResponseWriter, r *http.Request) {
	api.listBindingPresets(w, r, roleAdmin, 0)
}
func (api *httpAPI) stewardBindingPresets(w http.ResponseWriter, r *http.Request, p UserPrincipal) {
	api.listBindingPresets(w, r, roleSteward, p.UserID)
}
func (api *httpAPI) listBindingPresets(w http.ResponseWriter, r *http.Request, role roleKind, actor int64) {
	if !requireNoBody(w, r) {
		return
	}
	values, ok := requestQuery(w, r)
	if !ok {
		return
	}
	for name, v := range values {
		if len(v) != 1 || (name != "q" && name != "state" && name != "page" && name != "page_size") {
			writeRoutingError(w, ErrInvalidRequest)
			return
		}
	}
	page, _, err := pagination.Parse(values)
	if err != nil {
		writeRoutingError(w, ErrInvalidRequest)
		return
	}
	state := "active"
	if values.Has("state") {
		state = values.Get("state")
	}
	out, err := api.service.bindingPresets(r.Context(), role, actor, values.Get("q"), state, page)
	if err != nil {
		writeRoutingError(w, err)
		return
	}
	writeJSON(w, out)
}
func (api *httpAPI) adminBindingPreset(w http.ResponseWriter, r *http.Request) {
	api.getBindingPreset(w, r, roleAdmin, 0)
}
func (api *httpAPI) stewardBindingPreset(w http.ResponseWriter, r *http.Request, p UserPrincipal) {
	api.getBindingPreset(w, r, roleSteward, p.UserID)
}
func (api *httpAPI) getBindingPreset(w http.ResponseWriter, r *http.Request, role roleKind, actor int64) {
	if !requireNoBody(w, r) || !requireEmptyQuery(w, r) {
		return
	}
	out, err := api.service.bindingPreset(r.Context(), role, actor, r.PathValue("channelId"))
	if err != nil {
		writeRoutingError(w, err)
		return
	}
	writeJSON(w, out)
}
func (api *httpAPI) adminSaveBindingPreset(w http.ResponseWriter, r *http.Request) {
	api.saveBindingPreset(w, r, roleAdmin, 0)
}
func (api *httpAPI) stewardSaveBindingPreset(w http.ResponseWriter, r *http.Request, p UserPrincipal) {
	api.saveBindingPreset(w, r, roleSteward, p.UserID)
}
func (api *httpAPI) saveBindingPreset(w http.ResponseWriter, r *http.Request, role roleKind, actor int64) {
	if !requireEmptyQuery(w, r) {
		return
	}
	var input BindingPresetInput
	if !decodeStrictObjectLimit(w, r, &input, 64<<10) {
		return
	}
	id := r.PathValue("channelId")
	route := routeAdminBindingPreset
	if role == roleSteward {
		route = routeStewardBindingPreset
	}
	mutation, ok := presetTextMutation(w, r, route, id, input)
	if !ok {
		return
	}
	out, err := api.service.saveBindingPreset(r.Context(), role, actor, id, mutation, input)
	if err != nil {
		writeRoutingError(w, err)
		return
	}
	writeMutation(w, out)
}
