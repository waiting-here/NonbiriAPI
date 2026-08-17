// Package logapi serves the request-log, usage, and overview API surface of
// both stations:
//
//   - GET /api/me/usage                  (user session only)
//   - GET /admin/api/logs                (admin session only)
//   - GET /admin/api/usage               (admin session only)
//   - GET /admin/api/overview/endpoints  (admin session only)
//   - GET /admin/api/overview/models     (admin session only)
//
// Authorization is session-only by construction: a user-session principal can
// read exactly its own usage; an admin-session principal the global metadata
// projections. Caller keys, header-carried identities, and front-end
// permissions never authorize a route here. Every query is parameterized and
// pre-validated, page sizes are clamped, and responses are metadata only —
// no request/response content, upstream secret, ciphertext, or unbounded raw
// diagnostic is ever projected. Success and error responses carry no-store
// through the shared httpmw.API / httperr boundary.
package logapi

import (
	"errors"
	"net/http"

	"nonbiriapi/internal/auth"
	"nonbiriapi/internal/db"
	"nonbiriapi/internal/host"
	"nonbiriapi/internal/httperr"
	"nonbiriapi/internal/httpmw"
)

// HandlerDeps wires the repository backing these read-only routes.
type HandlerDeps struct {
	Store *db.Store
}

// Handler is the mountable log/usage/overview route tree. It is safe to mount
// on either station: each route family enforces its own station and principal
// kind, so a single handler can be wrapped by both session middlewares at the
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
	h.mux.HandleFunc("GET /api/me/usage", h.userUsage)
	h.mux.HandleFunc("GET /admin/api/logs", h.adminLogs)
	h.mux.HandleFunc("GET /admin/api/usage", h.adminUsage)
	h.mux.HandleFunc("GET /admin/api/overview/endpoints", h.adminOverviewEndpoints)
	h.mux.HandleFunc("GET /admin/api/overview/models", h.adminOverviewModels)
	return httpmw.API(h.mux)
}

// requireUserSession enforces the user-station boundary and the user-session
// principal. Any other identity (admin session, caller key, header id, or no
// identity) is unauthorized: reading one's own usage is session-only by
// construction.
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

// userUsage handles GET /api/me/usage: the server-authoritative accumulator
// totals of the authenticated user, shape fixed by the API contract.
func (h *Handler) userUsage(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		writeErr(w, httperr.New(httperr.CodeServiceUnavailable, "usage service unavailable"))
		return
	}
	user, ok := h.requireUserSession(w, r)
	if !ok {
		return
	}
	totals, err := h.store.GetUserUsage(r.Context(), user.ID)
	if err != nil {
		writeRepoErr(w, err)
		return
	}
	httperr.WriteJSON(w, http.StatusOK, usageTotalsResponse(totals))
}

// adminLogs handles GET /admin/api/logs: a bounded, keyset-paginated page of
// metadata-only request-log rows across all users, newest first.
func (h *Handler) adminLogs(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		writeErr(w, httperr.New(httperr.CodeServiceUnavailable, "usage service unavailable"))
		return
	}
	if _, ok := h.requireAdminSession(w, r); !ok {
		return
	}
	query, derr := parseLogQuery(r)
	if derr.Code != "" {
		writeErr(w, derr)
		return
	}
	logs, err := h.store.QueryRequestLogs(r.Context(), query)
	if err != nil {
		writeRepoErr(w, err)
		return
	}
	httperr.WriteJSON(w, http.StatusOK, logListResponse(logs))
}

// adminUsage handles GET /admin/api/usage. group_by=site (default) returns
// site-wide accumulator totals across all normal users; group_by=user returns
// per-user totals ordered by total requests descending. The administrator row
// is excluded from both aggregations by the repository.
func (h *Handler) adminUsage(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		writeErr(w, httperr.New(httperr.CodeServiceUnavailable, "usage service unavailable"))
		return
	}
	if _, ok := h.requireAdminSession(w, r); !ok {
		return
	}
	groupBy, limit, derr := parseUsageQuery(r)
	if derr.Code != "" {
		writeErr(w, derr)
		return
	}
	if groupBy == "user" {
		rows, err := h.store.AggregateUsageByUser(r.Context(), limit)
		if err != nil {
			writeRepoErr(w, err)
			return
		}
		httperr.WriteJSON(w, http.StatusOK, usageByUserResponse(rows))
		return
	}
	totals, err := h.store.AggregateUsage(r.Context())
	if err != nil {
		writeRepoErr(w, err)
		return
	}
	httperr.WriteJSON(w, http.StatusOK, usageTotalsResponse(totals))
}

// adminOverviewEndpoints handles GET /admin/api/overview/endpoints: endpoint
// metadata across all users plus live key counts. The projection never
// selects or decrypts a key secret.
func (h *Handler) adminOverviewEndpoints(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		writeErr(w, httperr.New(httperr.CodeServiceUnavailable, "usage service unavailable"))
		return
	}
	if _, ok := h.requireAdminSession(w, r); !ok {
		return
	}
	if derr := rejectUnknownParams(r); derr.Code != "" {
		writeErr(w, derr)
		return
	}
	eps, err := h.store.ListEndpointsOverview(r.Context())
	if err != nil {
		writeRepoErr(w, err)
		return
	}
	httperr.WriteJSON(w, http.StatusOK, endpointOverviewResponse(eps))
}

// adminOverviewModels handles GET /admin/api/overview/models: platform model
// metadata across all users plus live binding counts.
func (h *Handler) adminOverviewModels(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		writeErr(w, httperr.New(httperr.CodeServiceUnavailable, "usage service unavailable"))
		return
	}
	if _, ok := h.requireAdminSession(w, r); !ok {
		return
	}
	if derr := rejectUnknownParams(r); derr.Code != "" {
		writeErr(w, derr)
		return
	}
	models, err := h.store.ListModelsOverview(r.Context())
	if err != nil {
		writeRepoErr(w, err)
		return
	}
	httperr.WriteJSON(w, http.StatusOK, modelOverviewResponse(models))
}

// writeRepoErr maps repository failures to the stable envelope. These
// read-only queries are parameterized and pre-validated at the handler, so a
// repository error is a server-side condition; no SQL, path, or secret
// material is ever echoed.
func writeRepoErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, db.ErrNotFound):
		writeErr(w, httperr.New(httperr.CodeNotFound, "not found"))
	case errors.Is(err, db.ErrOverviewLimit):
		writeErr(w, httperr.New(httperr.CodePayloadTooLarge, "overview is too large"))
	default:
		writeErr(w, httperr.New(httperr.CodeInternal, "internal error"))
	}
}

func writeErr(w http.ResponseWriter, e httperr.Error) {
	httperr.WriteError(w, e)
}
