package logapi

import (
	"net/http"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/steward"
)

// StewardLogsSub returns the level-5 steward sub-handler for the frozen
// full-site log route GET /api/steward/logs (frozen §G, clarification §1.8: a
// user-station co-management API served with the minimal de-privacy
// projection, never the administrator host/session). The steward frame's
// per-request live level>=5 gate has already run when it is invoked, so this
// handler only serves the business projection.
//
// The projection is db.QueryStewardRequestLogs: it reuses the administrator
// log shape and bounded filter, but blanks the donor's endpoint key id, base
// URL and upstream model id on charity rows (route_kind='charity'), because a
// level-5 steward co-manages site-wide log/activity, not donor resources. The
// steward therefore sees the consumer's user id, route kind, status, tokens
// and bounded diagnostic for every row, and never a user-chosen platform
// model name, an endpoint/key note, a Discord identity, or any donated
// resource. No bulk CSV/JSON export surface is exposed here.
//
// The returned handler is registered through steward.Handler.Handle (see
// main.go), which wraps it in the user-session middleware and the live
// level-5 gate; mounting it on any other mux would bypass that boundary.
func StewardLogsSub(store *db.Store) func(http.ResponseWriter, *http.Request, steward.Principal) {
	return func(w http.ResponseWriter, r *http.Request, p steward.Principal) {
		if store == nil {
			writeErr(w, httperr.New(httperr.CodeServiceUnavailable, "log service unavailable"))
			return
		}
		// Defense in depth: the level-5 gate already resolved a real,
		// active, level>=5 user. A principal without a user id must never
		// reach the projection.
		if p.UserID <= 0 {
			writeErr(w, httperr.New(httperr.CodeForbidden, "steward authorization required"))
			return
		}
		query, derr := parseAdminLogsQuery(r)
		if derr.Code != "" {
			writeErr(w, derr)
			return
		}
		logs, hasMore, err := store.QueryStewardRequestLogs(r.Context(), query)
		if err != nil {
			writeRepoErr(w, err)
			return
		}
		httperr.WriteJSON(w, http.StatusOK, logListResponse(logs, hasMore))
	}
}
