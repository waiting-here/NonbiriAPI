package adminusers

import (
	"net/http"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/blacklist"

	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

const routeBlacklist = "/admin/api/blacklist"
const routeBlacklistRemove = "/admin/api/blacklist/{discordID}/remove"
const routeBlacklistEvents = "/admin/api/blacklist/{discordID}/events"

func (api *httpAPI) listBlacklist(w http.ResponseWriter, r *http.Request, p AdminPrincipal) {
	if !requireNoBody(w, r) {
		return
	}
	values, ok := strictQuery(w, r, "q", "discord_id", "actor_kind", "actor_user_id", "cursor", "limit", "page", "page_size")
	if !ok {
		return
	}
	query := BlacklistQuery{Q: values.Get("q"), DiscordID: values.Get("discord_id"), ActorKind: values.Get("actor_kind")}
	if raw, set := singleQuery(values, "actor_user_id"); set {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 || strconv.FormatInt(id, 10) != raw {
			writeError(w, ErrInvalidRequest)
			return
		}
		query.ActorUserID = id
	}
	if !parsePageQuery(w, values, &query.Cursor, &query.Limit, &query.Page) {
		return
	}
	if _, set := values["q"]; set && !validFilter(query.Q) {
		writeError(w, ErrInvalidRequest)
		return
	}
	if _, set := values["discord_id"]; set && !validDiscordID(query.DiscordID) {
		writeError(w, ErrInvalidRequest)
		return
	}
	result, err := api.service.listBlacklistFiltered(r.Context(), p.UserID, api.role, query)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (api *httpAPI) listBlacklistEvents(w http.ResponseWriter, r *http.Request, p AdminPrincipal) {
	if !requireNoBody(w, r) {
		return
	}
	values, ok := strictQuery(w, r, "page", "page_size")
	if !ok {
		return
	}
	page, _, err := pagination.Parse(values)
	if err != nil {
		writeError(w, ErrInvalidRequest)
		return
	}
	result, err := api.service.listBlacklistEvents(r.Context(), p.UserID, api.role, r.PathValue("discordID"), page)
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
	if !validID || !validReasonField || !validDiscordID(id) {
		writeError(w, ErrInvalidRequest)
		return
	}
	reason, err := blacklist.NormalizeNote(reason)
	if err != nil {
		writeError(w, ErrInvalidRequest)
		return
	}
	control, ok := makeControl(w, r, api.role.route(routeBlacklist), 0, map[string]string{"discord_id": id, "reason": reason})
	if !ok {
		return
	}
	control.PathIDs = []string{}
	result, err := api.service.setBlacklist(r.Context(), p.UserID, api.role, control, id, reason, true)
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
