// Package alertapi serves the administrator alert-center API surface:
//
//   - GET /admin/api/alerts                 (admin session only)
//   - POST /admin/api/alerts/{id}/resolve   (admin session only)
//
// Authorization is admin-session-only by construction: a user-session
// principal, a caller key, a header-carried identity, or no identity never
// authorizes a route here (a wrong-kind identity is forbidden, a missing one
// unauthorized). Every query parameter and the resolve body are
// pre-validated and bounded before they reach the repository; page sizes are
// clamped; responses are metadata only — no credential, ciphertext, or
// unbounded raw diagnostic is ever projected. Success and error responses
// carry no-store through the shared httpmw.API / httperr boundary.
package alertapi

import (
	"errors"
	"net/http"

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

// Handler is the mountable admin alert-center route tree. It is safe to mount
// on either station: each route enforces its own station and principal kind,
// so a single handler can be wrapped by the admin-session middleware at the
// integration rail.
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
	h.mux.HandleFunc("GET /admin/api/alerts", h.adminAlerts)
	h.mux.HandleFunc("POST /admin/api/alerts/{id}/resolve", h.resolveAlert)
	return httpmw.API(h.mux)
}

// requireAdminSession enforces the admin-station boundary and the admin-session
// principal. A valid identity of the wrong kind (a logged-in normal user or a
// caller key) is forbidden; a missing identity is unauthorized.
func (h *Handler) requireAdminSession(w http.ResponseWriter, r *http.Request) (*db.User, bool) {
	if !stationIs(w, r, host.StationAdmin) {
		return nil, false
	}
	admin, ok := auth.AdminFromContext(r.Context())
	if ok && admin != nil && admin.ID > 0 && admin.IsAdmin {
		return admin, true
	}
	if _, any := auth.PrincipalFromContext(r.Context()); any {
		writeErr(w, httperr.New(httperr.CodeForbidden, "administrator authorization required"))
	} else {
		writeErr(w, httperr.New(httperr.CodeUnauthorized, "authentication required"))
	}
	return nil, false
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

// adminAlerts handles GET /admin/api/alerts: a bounded, offset-paginated page
// of alert rows, newest first, with an optional resolved filter.
func (h *Handler) adminAlerts(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		writeErr(w, httperr.New(httperr.CodeServiceUnavailable, "alert service unavailable"))
		return
	}
	if _, ok := h.requireAdminSession(w, r); !ok {
		return
	}
	query, derr := parseAlertsQuery(r)
	if derr.Code != "" {
		writeErr(w, derr)
		return
	}
	alerts, hasMore, err := h.store.ListAdminAlerts(r.Context(), query)
	if err != nil {
		writeRepoErr(w, err)
		return
	}
	httperr.WriteJSON(w, http.StatusOK, alertListResponse(alerts, hasMore))
}

// resolveAlert handles POST /admin/api/alerts/{id}/resolve: the idempotent
// resolve/ack (resolved=true, the default) or reopen (resolved=false) of one
// alert. Repeating the same transition succeeds and returns the current
// alert; a missing alert is not_found.
func (h *Handler) resolveAlert(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		writeErr(w, httperr.New(httperr.CodeServiceUnavailable, "alert service unavailable"))
		return
	}
	if _, ok := h.requireAdminSession(w, r); !ok {
		return
	}
	id, derr := parseAlertID(r)
	if derr.Code != "" {
		writeErr(w, derr)
		return
	}
	resolved, derr := parseResolveBody(r)
	if derr.Code != "" {
		writeErr(w, derr)
		return
	}
	alert, err := h.store.SetAdminAlertResolved(r.Context(), id, resolved, nowUnix())
	if err != nil {
		writeRepoErr(w, err)
		return
	}
	httperr.WriteJSON(w, http.StatusOK, alertResponse(alert))
}

// writeRepoErr maps repository failures to the stable envelope. These
// queries and updates are parameterized and pre-validated at the handler, so
// a repository error is a server-side condition; no SQL, path, or secret
// material is ever echoed.
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
