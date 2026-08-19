// Package issues serves the user issue center:
//
//   - GET  /api/issues              (user session only)
//   - POST /api/issues/{id}/resolve (user session only)
//
// Authorization is session-only by construction: a browser user-session
// principal reads exactly its own issues and can ack exactly its own rows.
// Caller keys, header-carried identities, admin sessions, and front-end
// permissions never authorize a route here. Every query is parameterized and
// pre-validated, page sizes are clamped, and the projection is bounded and
// sanitized at the repository: kind/message/ref never carry an upstream
// secret, ciphertext, request content, raw body, or full endpoint URL, and
// they are rendered as plain text only. Success and error responses carry
// no-store through the shared httpmw.API / httperr boundary. There is no
// admin projection here: the administrator alert center (admin_alerts) is a
// separate surface and this package never touches it.
package issues

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/auth"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/host"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/httpmw"
)

// HandlerDeps wires the repository backing these routes.
type HandlerDeps struct {
	Store *db.Store
}

// Handler is the mountable user-station issue route tree. It mounts nothing
// by itself; the integration rail wraps it with the user-session middleware
// and the httpmw edge.
type Handler struct {
	store *db.Store
	mux   *http.ServeMux
}

// NewHandler builds the route tree wrapped in the shared no-store API
// boundary, so fallback 404/405 responses are stable JSON envelopes. A nil
// store fails every route closed (service_unavailable), keeping the boundary
// safe until the integration rail wires the repository.
func NewHandler(deps HandlerDeps) http.Handler {
	h := &Handler{store: deps.Store, mux: http.NewServeMux()}
	h.mux.HandleFunc("GET /api/issues", h.listIssues)
	h.mux.HandleFunc("POST /api/issues/{id}/resolve", h.resolveIssue)
	return httpmw.API(h.mux)
}

// requireUserSession enforces the user-station boundary and the user-session
// principal. Any other identity (admin session, caller key, header id, or no
// identity) is unauthorized: the issue center is session-only by
// construction, mirroring the log/usage routes.
func (h *Handler) requireUserSession(w http.ResponseWriter, r *http.Request) (*db.User, bool) {
	if !stationIs(w, r, host.StationUser) {
		return nil, false
	}
	user, ok := auth.UserFromContext(r.Context())
	if !ok || user == nil || user.ID <= 0 || !user.IsActive() {
		writeErr(w, httperr.New(httperr.CodeUnauthorized, "authentication required"))
		return nil, false
	}
	return user, true
}

// stationIs mirrors the auth boundary: a request that reached the wrong
// station is refused before any identity is consulted.
func stationIs(w http.ResponseWriter, r *http.Request, expected host.Station) bool {
	if r != nil && httpmw.StationOf(r) == expected {
		return true
	}
	writeErr(w, httperr.New(httperr.CodeForbidden, "station authorization required"))
	return false
}

// listIssues handles GET /api/issues: one bounded, ownership-scoped page of
// the caller's issues, newest first, with an optional resolved filter and
// offset pagination. The shape is fixed by the API contract; cross-user rows
// are excluded in SQL and never enter the projection.
func (h *Handler) listIssues(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		writeErr(w, httperr.New(httperr.CodeServiceUnavailable, "issue service unavailable"))
		return
	}
	user, ok := h.requireUserSession(w, r)
	if !ok {
		return
	}
	query, derr := parseIssueQuery(r)
	if derr.Code != "" {
		writeErr(w, derr)
		return
	}
	query.UserID = user.ID
	issues, hasMore, err := h.store.QueryUserIssues(r.Context(), query)
	if err != nil {
		writeRepoErr(w, err)
		return
	}
	httperr.WriteJSON(w, http.StatusOK, issueListResponse(issues, hasMore))
}

// resolveIssue handles POST /api/issues/{id}/resolve: the one-way, idempotent
// ack. The update is ownership-scoped in SQL, so a cross-user or missing id
// is an indistinguishable not_found; a repeated ack succeeds and keeps the
// original resolved_at.
func (h *Handler) resolveIssue(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		writeErr(w, httperr.New(httperr.CodeServiceUnavailable, "issue service unavailable"))
		return
	}
	user, ok := h.requireUserSession(w, r)
	if !ok {
		return
	}
	issueID, ok := parsePathID(w, r, "id")
	if !ok {
		return
	}
	now := time.Now().Unix()
	if err := h.store.ResolveUserIssue(r.Context(), user.ID, issueID, now); err != nil {
		writeRepoErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// parsePathID reads a positive int64 path parameter and writes an error
// envelope on failure, mirroring the endpoint-handler convention.
func parsePathID(w http.ResponseWriter, r *http.Request, name string) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil || id <= 0 {
		writeErr(w, httperr.New(httperr.CodeInvalidRequest, "invalid id"))
		return 0, false
	}
	return id, true
}

// writeRepoErr maps repository failures to the stable envelope. These
// parameterized queries and ownership-scoped updates cannot leak SQL, path,
// or secret material; a repository error is a server-side condition.
func writeRepoErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, db.ErrNotFound):
		writeErr(w, httperr.New(httperr.CodeNotFound, "not found"))
	default:
		writeErr(w, httperr.New(httperr.CodeInternal, "internal error"))
	}
}

func writeErr(w http.ResponseWriter, e httperr.Error) {
	httperr.WriteError(w, e)
}
