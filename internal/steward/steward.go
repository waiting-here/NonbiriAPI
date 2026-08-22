// Package steward serves the level-5 co-management prefix /api/steward/ on
// the user station (frozen §G, accepted clarifications 6–8, implementation
// contract §6.3).
//
// Boundary rules:
//
//   - the station is and stays the USER station: requests that arrive on the
//     admin host (or with an unknown station) are refused before any identity
//     is consulted, and an administrator session is never accepted — an
//     administrator cannot borrow the level-5 prefix to gain anything, and a
//     level-5 user gains nothing on the admin station;
//   - authorization re-resolves the server-authoritative effective level >= 5
//     on EVERY request (no caching): a demotion or a manual-level reset takes
//     effect on the very next request of an existing session;
//   - sub-handlers receive only an opaque steward identity (user id + role),
//     never a *db.User or any session material;
//   - this package provides the mountable middleware/mux frame only. It
//     registers NO business route itself: business rails (donation reviews,
//     charity model management, full-site logs) attach through Handle at the
//     integration rail, and an unregistered sub-path answers with the stable
//     not_found envelope — but only AFTER the level-5 gate, so an
//     under-privileged caller learns nothing beyond forbidden.
package steward

import (
	"net/http"

	"github.com/waiting-here/NonbiriAPI/internal/auth"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/host"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/httpmw"
)

// Prefix is the frozen user-station co-management prefix.
const Prefix = "/api/steward/"

// RoleLevel5 is the only role this frame hands to sub-handlers.
const RoleLevel5 = "level5"

// Principal is the entire identity a steward sub-handler receives: the
// session user's id and the steward role marker. It deliberately carries no
// user row, no session token, and no administrator capability.
type Principal struct {
	UserID int64
	Role   string
}

// SubHandler is one steward business route. The level-5 gate has already run
// when it is invoked.
type SubHandler func(w http.ResponseWriter, r *http.Request, p Principal)

// Deps wires the user-session middleware and the authoritative store.
type Deps struct {
	UserAuth *auth.UserAuth
	Store    *db.Store
}

// Handler is the mountable /api/steward/ frame.
type Handler struct {
	userAuth *auth.UserAuth
	store    *db.Store
	mux      *http.ServeMux
}

// New builds the frame with an empty sub-route table.
func New(deps Deps) *Handler {
	return &Handler{userAuth: deps.UserAuth, store: deps.Store, mux: http.NewServeMux()}
}

// Handle registers one business sub-route (for example the frozen
// logapi.StewardLogsPath) behind the level-5 gate. The method/pattern follow
// net/http ServeMux conventions ("GET /api/steward/logs").
func (h *Handler) Handle(method, pattern string, sub SubHandler) {
	if h == nil || sub == nil {
		return
	}
	h.mux.HandleFunc(method+" "+pattern, func(w http.ResponseWriter, r *http.Request) {
		user, ok := auth.UserFromContext(r.Context())
		if !ok || user == nil || user.ID <= 0 {
			// Unreachable behind the gate; kept fail-closed anyway.
			httperr.WriteError(w, httperr.New(httperr.CodeForbidden, "steward authorization required"))
			return
		}
		sub(w, r, Principal{UserID: user.ID, Role: RoleLevel5})
	})
}

// Handler returns the mountable route tree:
// user-station boundary + user-session authentication (the shared
// auth.UserAuth middleware) → the per-request live level-5 gate → the sub
// mux, whose unmatched paths answer with the stable not_found envelope. The
// shared httpmw.API wrapper turns the mux's own 404/405 fallbacks into the
// stable JSON envelope with no-store.
func (h *Handler) Handler() http.Handler {
	if h == nil || h.userAuth == nil || h.store == nil {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			httperr.WriteError(w, httperr.New(httperr.CodeServiceUnavailable, "steward service unavailable"))
		})
	}
	// The sub-route table is wrapped once: its unmatched 404/405 fallbacks
	// become the stable no-store JSON envelope.
	subTree := httpmw.API(h.mux)
	gate := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Station re-check inside the gate (defense in depth; the user-session
		// middleware already refuses non-user stations): the user API tree is
		// reachable from both hosts on the root mux, so the steward prefix
		// must positively confirm the user station itself.
		if httpmw.StationOf(r) != host.StationUser {
			httperr.WriteError(w, httperr.New(httperr.CodeForbidden, "station authorization required"))
			return
		}
		user, ok := auth.UserFromContext(r.Context())
		if !ok || user == nil || user.ID <= 0 || !user.IsActive() {
			httperr.WriteError(w, httperr.New(httperr.CodeForbidden, "steward authorization required"))
			return
		}
		// Live, per-request, server-authoritative level resolution. No cached
		// value: a demotion or a manual reset revokes the existing session on
		// its very next request. The administrator row is excluded by the
		// resolver itself (and could not hold a user session anyway).
		level, err := h.store.ResolveEffectiveLevel(r.Context(), user.ID)
		if err != nil || !db.EffectiveLevelAtLeast(level, db.LevelSteward) {
			httperr.WriteError(w, httperr.New(httperr.CodeForbidden, "steward authorization required"))
			return
		}
		subTree.ServeHTTP(w, r)
	})
	return h.userAuth.Middleware(gate)
}
