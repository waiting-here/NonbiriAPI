package charityrouting

import (
	"errors"
	"net/http"

	"github.com/waiting-here/NonbiriAPI/internal/charityscope"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/httpapi"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
	"github.com/waiting-here/NonbiriAPI/internal/strictjson"
)

func (api *httpAPI) adminKeyBindingOptions(w http.ResponseWriter, r *http.Request) {
	api.bindingOptions(w, r, roleAdmin, 0)
}
func (api *httpAPI) stewardKeyBindingOptions(w http.ResponseWriter, r *http.Request, p UserPrincipal) {
	api.bindingOptions(w, r, roleSteward, p.UserID)
}
func (api *httpAPI) bindingOptions(w http.ResponseWriter, r *http.Request, role roleKind, actor int64) {
	selected, err := charityscope.SelectRequest(r)
	if err != nil {
		writeRoutingError(w, ErrInvalidRequest)
		return
	}
	r = selected
	id, ok := parsePathID(w, r, "id")
	if !ok {
		return
	}
	key, ok := parsePathID(w, r, "keyId")
	if !ok || !requireNoBody(w, r) {
		return
	}
	values, ok := requestQuery(w, r)
	if !ok {
		return
	}
	for name, v := range values {
		if len(v) != 1 || (name != "q" && name != "page" && name != "page_size") {
			writeRoutingError(w, ErrInvalidRequest)
			return
		}
	}
	page, _, err := pagination.Parse(values)
	if err != nil {
		writeRoutingError(w, ErrInvalidRequest)
		return
	}
	out, err := api.service.keyBindingOptions(r.Context(), role, actor, id, key, values.Get("q"), page)
	if err != nil {
		writeRoutingError(w, err)
		return
	}
	writeJSON(w, out)
}
func (api *httpAPI) adminKeyBindingBatch(w http.ResponseWriter, r *http.Request) {
	api.keyBindingBatch(w, r, roleAdmin, 0)
}
func (api *httpAPI) stewardKeyBindingBatch(w http.ResponseWriter, r *http.Request, p UserPrincipal) {
	api.keyBindingBatch(w, r, roleSteward, p.UserID)
}
func (api *httpAPI) keyBindingBatch(w http.ResponseWriter, r *http.Request, role roleKind, actor int64) {
	id, ok := parsePathID(w, r, "id")
	if !ok {
		return
	}
	key, ok := parsePathID(w, r, "keyId")
	if !ok || !requireEmptyQuery(w, r) {
		return
	}
	var input KeyBindingBatch
	if !decodeKeyBindingBatch(w, r, &input) {
		return
	}
	route := routeAdminKeyBindingBatch
	if role == roleSteward {
		route = routeStewardKeyBindingBatch
	}
	mutation, ok := mutationFor(w, r, route, []int64{id, key}, input)
	if !ok {
		return
	}
	out, err := api.service.bindKeyTargets(r.Context(), role, actor, id, key, mutation, input)
	if err != nil {
		writeRoutingError(w, err)
		return
	}
	writeMutation(w, out)
}
func decodeKeyBindingBatch(w http.ResponseWriter, r *http.Request, input *KeyBindingBatch) bool {
	body, err := httpapi.ReadJSON(w, r, input, httpapi.BodyOptions{MaxBytes: idempotency.MaxControlBodyBytes,
		Validate: func(body []byte) error {
			return strictjson.ValidateObjectWithFieldLimit(body, 2+3*maxKeyBindingTargets)
		}})
	if err != nil {
		if errors.Is(err, httpapi.ErrTooLarge) {
			httperr.WriteError(w, httperr.New(httperr.CodePayloadTooLarge, "request body is too large"))
		} else {
			writeRoutingError(w, ErrInvalidRequest)
		}
		return false
	}
	clear(body)
	return true
}
func (api *httpAPI) adminApplyBindingPreset(w http.ResponseWriter, r *http.Request) {
	api.applyBindingPreset(w, r, roleAdmin, 0)
}
func (api *httpAPI) stewardApplyBindingPreset(w http.ResponseWriter, r *http.Request, p UserPrincipal) {
	api.applyBindingPreset(w, r, roleSteward, p.UserID)
}
func (api *httpAPI) applyBindingPreset(w http.ResponseWriter, r *http.Request, role roleKind, actor int64) {
	id, ok := parsePathID(w, r, "id")
	if !ok || !requireEmptyQuery(w, r) {
		return
	}
	var input ApplyBindingPreset
	if !decodeStrictObjectLimit(w, r, &input, 16<<10) {
		return
	}
	route := routeAdminApplyBindingPreset
	if role == roleSteward {
		route = routeStewardApplyBindingPreset
	}
	mutation, ok := mutationFor(w, r, route, []int64{id}, input)
	if !ok {
		return
	}
	out, err := api.service.applyBindingPreset(r.Context(), role, actor, id, mutation, input)
	if err != nil {
		writeRoutingError(w, err)
		return
	}
	writeMutation(w, out)
}
func presetTextMutation(w http.ResponseWriter, r *http.Request, route, id string, input any) (resources.ControlMutation, bool) {
	if !db.ValidateOpaqueID(id, "mch_") {
		writeRoutingError(w, ErrInvalidRequest)
		return resources.ControlMutation{}, false
	}
	mutation, ok := mutationFor(w, r, route, nil, input)
	if ok {
		mutation.PathIDs = []string{id}
	}
	return mutation, ok
}
