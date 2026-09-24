package adminusers

import (
	"net/http"

	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

const routeBlacklist = "/admin/api/blacklist"
const routeBlacklistRemove = "/admin/api/blacklist/{discordID}/remove"

func (api *httpAPI) listBlacklist(w http.ResponseWriter, r *http.Request, p AdminPrincipal) {
	if !requireNoBody(w, r) {
		return
	}
	values, ok := strictQuery(w, r, "q", "page", "page_size")
	if !ok {
		return
	}
	page, _, err := pagination.Parse(values)
	if err != nil {
		writeError(w, ErrInvalidRequest)
		return
	}
	q := values.Get("q")
	if _, set := values["q"]; set && !validDiscordID(q) {
		writeError(w, ErrInvalidRequest)
		return
	}
	result, err := api.service.ListBlacklist(r.Context(), p.UserID, q, page)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (api *httpAPI) addBlacklist(w http.ResponseWriter, r *http.Request, p AdminPrincipal) {
	object, ok := decodeStrictObject(w, r)
	if !ok {
		return
	}
	if !exactObject(object, "discord_id", "reason") {
		writeError(w, ErrInvalidRequest)
		return
	}
	id, validID := requiredString(object, "discord_id")
	reason, validReasonField := requiredString(object, "reason")
	if !validID || !validReasonField || !validDiscordID(id) || !validReason(reason) {
		writeError(w, ErrInvalidRequest)
		return
	}
	control, ok := makeControl(w, r, routeBlacklist, 0, map[string]string{"discord_id": id, "reason": reason})
	if !ok {
		return
	}
	control.PathIDs = []string{}
	result, err := api.service.SetBlacklist(r.Context(), p.UserID, control, id, reason, true)
	if err != nil {
		writeError(w, err)
		return
	}
	writeMutation(w, result.Status, result.Body)
}

func (api *httpAPI) removeBlacklist(w http.ResponseWriter, r *http.Request, p AdminPrincipal) {
	if !requireReadRequest(w, r) {
		return
	}
	id := r.PathValue("discordID")
	if !validDiscordID(id) {
		writeError(w, ErrInvalidRequest)
		return
	}
	control, ok := makeControl(w, r, routeBlacklistRemove, 0, map[string]string{})
	if !ok {
		return
	}
	control.PathIDs = []string{id}
	result, err := api.service.SetBlacklist(r.Context(), p.UserID, control, id, "", false)
	if err != nil {
		writeError(w, err)
		return
	}
	writeMutation(w, result.Status, result.Body)
}
