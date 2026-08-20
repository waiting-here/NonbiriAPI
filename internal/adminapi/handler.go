// Package adminapi serves the administrator user-management and site
// configuration surface:
//
//	GET    /admin/api/users                  list (bounded page + filters)
//	GET    /admin/api/users/{id}             detail with usage metadata
//	PATCH  /admin/api/users/{id}             endpoint_limit / rpm_limit / lang
//	POST   /admin/api/users/{id}/ban         ban (same-transaction session and
//	                                        caller-key invalidation)
//	POST   /admin/api/users/{id}/unban       unban
//	GET    /admin/api/site-config            known keys -> typed values
//	PATCH  /admin/api/site-config/{key}      update one known value
//
// Authorization is admin-session-only by construction: every route checks the
// admin station and the admin principal itself, so a user session, caller
// key, or header-carried identity never authorizes a route here. All queries
// are parameterized, page sizes are clamped, values are typed/bounded/
// control-character-checked, and every response is no-store through the
// shared httpmw.API / httperr boundary. Caller-key and session material is
// never selected, decrypted, or projected. Account deletion stays with the
// lifecycle rail (elevated DELETE /admin/api/users/{id}); this tree
// deliberately has no DELETE route.
package adminapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/waiting-here/NonbiriAPI/internal/auth"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/host"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/httpmw"
	"github.com/waiting-here/NonbiriAPI/internal/ratelimit"
)

const maxAdminBodyBytes = 16 * 1024

// HandlerDeps wires the repository and the optional runtime applier. A nil
// Runtime keeps the routes working with DB-only persistence (values take
// effect on restart); the integration rail injects the shared singletons.
type HandlerDeps struct {
	Store   *db.Store
	Runtime RuntimeApplier
}

// Handler is the mountable admin-controls route tree, wrapped in the shared
// no-store API boundary. A nil store fails every route closed. The
// siteConfigMu serializes the site-config PATCH read→runtime apply→persist→
// revert step per handler so concurrent patches to the same key cannot
// interleave their apply and persist orders and leave the database and the
// runtime singleton drifted apart.
type Handler struct {
	store        *db.Store
	runtime      RuntimeApplier
	mux          *http.ServeMux
	siteConfigMu sync.Mutex
}

// NewHandler builds the route tree. It is meant to be wrapped by the admin
// session middleware at the integration rail; the routes re-check station and
// principal as defense in depth.
func NewHandler(deps HandlerDeps) http.Handler {
	h := &Handler{store: deps.Store, runtime: deps.Runtime, mux: http.NewServeMux()}
	h.mux.HandleFunc("GET /admin/api/users", h.listUsers)
	h.mux.HandleFunc("GET /admin/api/users/{id}", h.getUser)
	h.mux.HandleFunc("PATCH /admin/api/users/{id}", h.patchUser)
	h.mux.HandleFunc("POST /admin/api/users/{id}/ban", h.banUser)
	h.mux.HandleFunc("POST /admin/api/users/{id}/unban", h.unbanUser)
	h.mux.HandleFunc("GET /admin/api/site-config", h.getSiteConfig)
	h.mux.HandleFunc("PATCH /admin/api/site-config/{key}", h.patchSiteConfig)
	return httpmw.API(h.mux)
}

// requireAdmin enforces the admin-station boundary and the admin-session
// principal. A valid identity of the wrong kind (a logged-in normal user or
// a caller key) is forbidden; a missing identity is unauthorized.
func (h *Handler) requireAdmin(w http.ResponseWriter, r *http.Request) (*db.User, bool) {
	if r == nil || httpmw.StationOf(r) != host.StationAdmin {
		writeErr(w, httperr.New(httperr.CodeForbidden, "station authorization required"))
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

// pathUserID parses the {id} path value. A missing or non-positive id is an
// indistinguishable not_found.
func pathUserID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeErr(w, httperr.New(httperr.CodeNotFound, "user not found"))
		return 0, false
	}
	return id, true
}

// listUsers handles GET /admin/api/users.
func (h *Handler) listUsers(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		writeErr(w, httperr.New(httperr.CodeServiceUnavailable, "service unavailable"))
		return
	}
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	query, derr := parseUserListQuery(r)
	if derr.Code != "" {
		writeErr(w, derr)
		return
	}
	users, hasMore, err := h.store.ListUsers(r.Context(), query)
	if err != nil {
		writeRepoErr(w, err)
		return
	}
	httperr.WriteJSON(w, http.StatusOK, userListResponse(users, hasMore))
}

// getUser handles GET /admin/api/users/{id}. The administrator row is not a
// user: it is indistinguishable not_found.
func (h *Handler) getUser(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		writeErr(w, httperr.New(httperr.CodeServiceUnavailable, "service unavailable"))
		return
	}
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	id, ok := pathUserID(w, r)
	if !ok {
		return
	}
	user, err := h.store.GetUserByID(id)
	if err != nil {
		writeRepoErr(w, err)
		return
	}
	if user == nil || user.IsAdmin {
		writeErr(w, httperr.New(httperr.CodeNotFound, "user not found"))
		return
	}
	httperr.WriteJSON(w, http.StatusOK, userResponse(user))
}

// patchUser handles PATCH /admin/api/users/{id}: endpoint_limit / rpm_limit
// (nullable; NULL restores the global default) and lang. rpm_limit is
// rejected above the current global per-user cap; the administrator raises
// that cap via default_rpm_per_user first.
func (h *Handler) patchUser(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		writeErr(w, httperr.New(httperr.CodeServiceUnavailable, "service unavailable"))
		return
	}
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	id, ok := pathUserID(w, r)
	if !ok {
		return
	}
	var body struct {
		EndpointLimit json.RawMessage `json:"endpoint_limit"`
		RPMLimit      json.RawMessage `json:"rpm_limit"`
		Lang          *string         `json:"lang"`
	}
	if !decodeJSONBody(w, r, &body) {
		return
	}
	if body.EndpointLimit == nil && body.RPMLimit == nil && body.Lang == nil {
		writeErr(w, httperr.New(httperr.CodeInvalidRequest, "no fields to update"))
		return
	}
	patch := db.UserLimitPatch{}
	if present, value, ok := nullableInt(body.EndpointLimit); present {
		if !ok {
			writeErr(w, httperr.New(httperr.CodeInvalidRequest, "invalid endpoint limit"))
			return
		}
		patch.EndpointLimitSet = true
		patch.EndpointLimit = value
	}
	if present, value, ok := nullableInt(body.RPMLimit); present {
		if !ok {
			writeErr(w, httperr.New(httperr.CodeInvalidRequest, "invalid rpm limit"))
			return
		}
		if value != nil {
			if *value < 1 {
				writeErr(w, httperr.New(httperr.CodeInvalidRequest, "invalid rpm limit"))
				return
			}
			cap, err := h.currentRPMLimitCap(r.Context())
			if err != nil {
				writeRepoErr(w, err)
				return
			}
			if *value > cap {
				writeErr(w, httperr.New(httperr.CodeInvalidRequest, "rpm limit exceeds the global cap"))
				return
			}
		}
		patch.RPMLimitSet = true
		patch.RPMLimit = value
	}
	if body.Lang != nil {
		if *body.Lang != "zh" && *body.Lang != "en" {
			writeErr(w, httperr.New(httperr.CodeInvalidRequest, "invalid language"))
			return
		}
		patch.LangSet = true
		patch.Lang = *body.Lang
	}
	updated, err := h.store.UpdateUserLimits(id, patch)
	if err != nil {
		writeLimitErr(w, err)
		return
	}
	httperr.WriteJSON(w, http.StatusOK, userResponse(updated))
}

// currentRPMLimitCap resolves the global per-user RPM ceiling: the
// default_rpm_per_user site_config value, or the ratelimit default when
// unset. This is the same ceiling the flow-control controller clamps user
// limits to at admission (the integration rail keeps the runtime controller
// in sync through the runtime applier).
func (h *Handler) currentRPMLimitCap(ctx context.Context) (int, error) {
	if h.store != nil {
		if raw, err := h.store.GetSiteConfigValue("default_rpm_per_user"); err == nil {
			raw = strings.TrimSpace(raw)
			if raw != "" {
				if n, perr := strconv.Atoi(raw); perr == nil && n >= 1 {
					return n, nil
				}
			}
		}
	}
	return ratelimit.DefaultRPMPerUserLimit, nil
}

// banUser handles POST /admin/api/users/{id}/ban. The repository performs the
// ban, session deletion, and caller-key deletion in one transaction, so
// request-time auth and platform-exit auth are invalidated atomically.
func (h *Handler) banUser(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		writeErr(w, httperr.New(httperr.CodeServiceUnavailable, "service unavailable"))
		return
	}
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	id, ok := pathUserID(w, r)
	if !ok {
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	if !decodeOptionalJSONBody(w, r, &body) {
		return
	}
	if err := h.store.BanUser(id, body.Reason); err != nil {
		writeLimitErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// unbanUser handles POST /admin/api/users/{id}/unban. No body is accepted; a
// caller key is not recreated (the user must generate a new one).
func (h *Handler) unbanUser(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		writeErr(w, httperr.New(httperr.CodeServiceUnavailable, "service unavailable"))
		return
	}
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	id, ok := pathUserID(w, r)
	if !ok {
		return
	}
	if r.ContentLength > 0 || (r.ContentLength == -1 && r.Body != nil) {
		writeErr(w, httperr.New(httperr.CodeInvalidRequest, "body is not accepted"))
		return
	}
	if err := h.store.UnbanUser(id); err != nil {
		writeLimitErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// getSiteConfig handles GET /admin/api/site-config: a flat object of known
// keys -> typed values (effective defaults when unset). Unknown stored rows
// are never projected.
func (h *Handler) getSiteConfig(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		writeErr(w, httperr.New(httperr.CodeServiceUnavailable, "service unavailable"))
		return
	}
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	if derr := rejectUnknownParams(r); derr.Code != "" {
		writeErr(w, derr)
		return
	}
	values, err := h.store.GetAllSiteConfigValues()
	if err != nil {
		writeRepoErr(w, err)
		return
	}
	out := make(map[string]any, len(knownSiteConfig)+4)
	for key := range knownSiteConfig {
		out[key] = typedSiteConfigValue(key, values[key])
	}
	for key, stored := range values {
		if strings.HasPrefix(key, alertPrefsPrefix) {
			if _, exact := knownSiteConfig[key]; !exact {
				out[key] = typedSiteConfigValue(key, stored)
			}
		}
	}
	httperr.WriteJSON(w, http.StatusOK, out)
}

// patchSiteConfig handles PATCH /admin/api/site-config/{key}. The value is
// validated against the key's typed spec, then the read→runtime apply→persist→
// revert step runs under the handler's site-config lock so concurrent patches to
// the same key cannot interleave. The runtime singleton is applied first (fail
// closed: a failed apply leaves runtime and DB untouched), then the value is
// persisted; a persistence failure reverts the runtime singleton to its previous
// value (or the frozen canonical default when the prior row was missing) so DB
// and runtime cannot drift.
func (h *Handler) patchSiteConfig(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		writeErr(w, httperr.New(httperr.CodeServiceUnavailable, "service unavailable"))
		return
	}
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	key := r.PathValue("key")
	if !knownSiteConfigKey(key) {
		writeErr(w, httperr.New(httperr.CodeNotFound, "configuration key not found"))
		return
	}
	var body struct {
		Value json.RawMessage `json:"value"`
	}
	if !decodeJSONBody(w, r, &body) {
		return
	}
	if body.Value == nil || string(body.Value) == "null" {
		writeErr(w, httperr.New(httperr.CodeInvalidRequest, "invalid configuration value"))
		return
	}
	value, derr := validateSiteConfigValue(key, body.Value)
	if derr.Code != "" {
		writeErr(w, derr)
		return
	}
	// Serialize the read→apply→persist→revert step per handler so concurrent
	// patches to the same key cannot interleave: without this lock one patch's
	// runtime apply could land between another's apply and persist (or a revert
	// could use a stale previous), leaving the persisted value and the live
	// runtime singleton disagreed.
	h.siteConfigMu.Lock()
	defer h.siteConfigMu.Unlock()
	previous, err := h.store.GetSiteConfigValue(key)
	if err != nil {
		writeRepoErr(w, err)
		return
	}
	if err := applyThenPersist(r.Context(), h.runtime, key, value, previous, func() error {
		return h.store.SetSiteConfigValue(key, value)
	}); err != nil {
		slog.Error("site config update failed", "key", key, "err", err)
		writeErr(w, httperr.New(httperr.CodeInternal, "configuration update failed"))
		return
	}
	httperr.WriteJSON(w, http.StatusOK, siteConfigPatchResp{Key: key, Value: typedSiteConfigValue(key, value)})
}

// applyThenPersist is the DB/runtime consistency step for a validated
// site_config change: the runtime singleton is applied first (fail closed — a
// failed apply leaves runtime and DB untouched), then the value is persisted;
// a persistence failure reverts the runtime singleton to its previous value
// so DB and runtime cannot drift. Revert failures are logged and reported
// through the returned error.
func applyThenPersist(ctx context.Context, runtime RuntimeApplier, key, value, previous string, persist func() error) error {
	if persist == nil {
		return errors.New("site config persist step is required")
	}
	if runtime != nil {
		if err := runtime.ApplySiteConfig(ctx, key, value); err != nil {
			return fmt.Errorf("runtime apply: %w", err)
		}
	}
	if err := persist(); err != nil {
		if runtime != nil {
			if rerr := runtime.RevertSiteConfig(ctx, key, previous); rerr != nil {
				slog.Error("site config runtime revert failed", "key", key, "err", rerr)
				return errors.Join(err, fmt.Errorf("runtime revert: %w", rerr))
			}
		}
		return err
	}
	return nil
}

// nullableInt parses a tri-state JSON field: absent (raw == nil), explicit
// null (value == nil), or an integer. A non-integer, non-null literal is not
// ok.
func nullableInt(raw json.RawMessage) (present bool, value *int, ok bool) {
	if raw == nil {
		return false, nil, true
	}
	if string(raw) == "null" {
		return true, nil, true
	}
	var n int
	if err := json.Unmarshal(raw, &n); err != nil {
		return true, nil, false
	}
	return true, &n, true
}

// decodeJSONBody is the local bounded-JSON helper: it rejects unknown fields,
// trailing tokens, and oversized bodies, mirroring the shared helpers so this
// package does not depend on auth/lifecycle internals.
func decodeJSONBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	if r == nil || r.Body == nil {
		writeErr(w, httperr.New(httperr.CodeInvalidRequest, "invalid request"))
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxAdminBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		writeDecodeErr(w, err)
		return false
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		writeDecodeErr(w, err)
		return false
	}
	return true
}

// decodeOptionalJSONBody is decodeJSONBody with an empty body permitted (used
// by ban, whose reason is optional).
func decodeOptionalJSONBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	if r == nil || r.Body == nil {
		writeErr(w, httperr.New(httperr.CodeInvalidRequest, "invalid request"))
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxAdminBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) {
			return true // empty body: all fields keep their zero values
		}
		writeDecodeErr(w, err)
		return false
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		writeDecodeErr(w, err)
		return false
	}
	return true
}

func writeDecodeErr(w http.ResponseWriter, err error) {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		writeErr(w, httperr.New(httperr.CodeInvalidRequest, "invalid request"))
		return
	}
	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) {
		writeErr(w, httperr.New(httperr.CodePayloadTooLarge, "request body too large"))
		return
	}
	writeErr(w, httperr.New(httperr.CodeInvalidRequest, "invalid request"))
}

// writeLimitErr maps user-management repository failures to the stable
// envelope. No SQL, path, or secret material is ever echoed.
func writeLimitErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, db.ErrNotFound):
		writeErr(w, httperr.New(httperr.CodeNotFound, "user not found"))
	case errors.Is(err, db.ErrAdminProtected):
		writeErr(w, httperr.New(httperr.CodeForbidden, "administrator identity is protected"))
	case errors.Is(err, db.ErrConflict):
		writeErr(w, httperr.New(httperr.CodeInvalidRequest, "invalid request"))
	default:
		writeErr(w, httperr.New(httperr.CodeInternal, "internal error"))
	}
}

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
