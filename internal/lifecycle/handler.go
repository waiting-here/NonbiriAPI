package lifecycle

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/auth"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/host"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/httpmw"
)

const (
	elevatedTokenHeader = "X-Elevated-Token"
	maxElevatedTokenLen = 512
	maxElevateBodyBytes = 16 * 1024
	maxPasswordBytes    = 4096
	confirmDeleteValue  = "DELETE"
)

// writeErr maps a lifecycle/repository error to the stable error envelope. No
// raw credential, token, or upstream text is ever placed in the message.
func writeErr(w http.ResponseWriter, err error) {
	switch {
	case err == nil:
		return
	case errors.Is(err, ErrElevationRequired):
		httperr.WriteError(w, httperr.New(httperr.CodeElevationRequired, "elevated authorization required"))
	case errors.Is(err, ErrInvalidCredentials):
		httperr.WriteError(w, httperr.New(httperr.CodeForbidden, "invalid administrator credentials"))
	case errors.Is(err, ErrAccountGone):
		httperr.WriteError(w, httperr.New(httperr.CodeConflict, "account no longer exists"))
	case errors.Is(err, db.ErrAdminProtected):
		httperr.WriteError(w, httperr.New(httperr.CodeForbidden, "administrator identity is protected"))
	case errors.Is(err, db.ErrNotFound):
		httperr.WriteError(w, httperr.New(httperr.CodeNotFound, "account not found"))
	default:
		httperr.WriteError(w, httperr.New(httperr.CodeInternal, "account operation unavailable"))
	}
}

func requireMethod(w http.ResponseWriter, r *http.Request, method string) bool {
	if r != nil && r.Method == method {
		return true
	}
	httperr.WriteError(w, httperr.New(httperr.CodeMethodNotAllowed, "method not allowed"))
	return false
}

func requireStation(w http.ResponseWriter, r *http.Request, want host.Station) bool {
	if r != nil && httpmw.StationOf(r) == want {
		return true
	}
	httperr.WriteError(w, httperr.New(httperr.CodeForbidden, "station authorization required"))
	return false
}

func elevatedTokenFromRequest(r *http.Request) string {
	if r == nil {
		return ""
	}
	values := r.Header.Values(elevatedTokenHeader)
	if len(values) != 1 || len(values[0]) > maxElevatedTokenLen {
		return ""
	}
	token := strings.TrimSpace(values[0])
	if token == "" || strings.ContainsAny(token, "\x00\r\n\t") {
		return ""
	}
	return token
}

// decodeJSONBody is the local bounded-JSON helper: it rejects unknown fields,
// trailing tokens, and oversized bodies, mirroring internal/auth.decodeJSONBody
// so the lifecycle package does not depend on auth's unexported helpers.
func decodeJSONBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	if r == nil || r.Body == nil {
		httperr.WriteError(w, httperr.New(httperr.CodeInvalidRequest, "invalid request"))
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxElevateBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			httperr.WriteError(w, httperr.New(httperr.CodeInvalidRequest, "invalid request"))
		} else {
			var maxErr *http.MaxBytesError
			if errors.As(err, &maxErr) {
				httperr.WriteError(w, httperr.New(httperr.CodePayloadTooLarge, "request body too large"))
			} else {
				httperr.WriteError(w, httperr.New(httperr.CodeInvalidRequest, "invalid request"))
			}
		}
		return false
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			httperr.WriteError(w, httperr.New(httperr.CodePayloadTooLarge, "request body too large"))
		} else {
			httperr.WriteError(w, httperr.New(httperr.CodeInvalidRequest, "invalid request"))
		}
		return false
	}
	return true
}

// ElevateAdminHandler handles POST /admin/api/auth/elevate. The administrator
// re-inputs the password as the second factor; on success a single-use
// elevated-action capability is returned and must be sent back as
// X-Elevated-Token by the destructive call. The response is no-store.
func (s *Service) ElevateAdminHandler(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) || !requireStation(w, r, host.StationAdmin) {
		return
	}
	if s == nil || s.store == nil || s.elevation == nil || s.adminVerifier == nil {
		httperr.WriteError(w, httperr.New(httperr.CodeServiceUnavailable, "service unavailable"))
		return
	}
	admin, ok := auth.AdminFromContext(r.Context())
	if !ok {
		httperr.WriteError(w, httperr.New(httperr.CodeUnauthorized, "administrator authentication required"))
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	if !decodeJSONBody(w, r, &body) {
		return
	}
	if len(body.Password) == 0 || len(body.Password) > maxPasswordBytes {
		httperr.WriteError(w, httperr.New(httperr.CodeInvalidRequest, "invalid request"))
		return
	}
	identity := httpmw.ClientIP(r)
	if identity == "" {
		identity = "unknown"
	}
	if s.throttle != nil {
		decision, err := s.throttle.Check(identity, admin.Username)
		if err != nil {
			httperr.WriteError(w, httperr.New(httperr.CodeRateLimited, "elevation temporarily unavailable"))
			return
		}
		if decision.Locked || !decision.Allowed {
			if decision.RetryAfterSeconds > 0 {
				w.Header().Set("Retry-After", strconv.Itoa(decision.RetryAfterSeconds))
			}
			httperr.WriteError(w, httperr.New(httperr.CodeRateLimited, "elevation temporarily unavailable"))
			return
		}
	}

	token, expires, err := s.ElevateAdminBound(r.Context(), admin, body.Password, db.SessionHash(auth.AdminSessionToken(r)))
	if err != nil {
		if errors.Is(err, ErrInvalidCredentials) {
			if s.throttle != nil {
				if failure, ferr := s.throttle.Failure(identity, admin.Username); ferr == nil && failure.Locked {
					if failure.RetryAfterSeconds > 0 {
						w.Header().Set("Retry-After", strconv.Itoa(failure.RetryAfterSeconds))
					}
					httperr.WriteError(w, httperr.New(httperr.CodeRateLimited, "elevation temporarily unavailable"))
					return
				}
			}
			httperr.WriteError(w, httperr.New(httperr.CodeForbidden, "invalid administrator credentials"))
			return
		}
		httperr.WriteError(w, httperr.New(httperr.CodeInternal, "elevation unavailable"))
		return
	}
	if s.throttle != nil {
		_ = s.throttle.Success(identity, admin.Username)
	}
	httperr.WriteJSON(w, http.StatusOK, elevateResponse{Token: token, ExpiresAt: expires.UTC().Format(time.RFC3339)})
}

type elevateResponse struct {
	Token     string `json:"token"`
	ExpiresAt string `json:"expires_at"`
}

// DeleteOwnAccountHandler handles POST /api/account/delete. It requires a
// normal-user session, an active elevated-action capability
// (X-Elevated-Token) bound to that user, and {confirm:"DELETE"}. On success
// the account and all linked data are gone and the current session is
// invalidated (the cascade removed it; the cookie is cleared here).
func (s *Service) DeleteOwnAccountHandler(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) || !requireStation(w, r, host.StationUser) {
		return
	}
	if s == nil || s.store == nil || s.elevation == nil {
		httperr.WriteError(w, httperr.New(httperr.CodeServiceUnavailable, "service unavailable"))
		return
	}
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		httperr.WriteError(w, httperr.New(httperr.CodeUnauthorized, "authentication required"))
		return
	}
	token := elevatedTokenFromRequest(r)
	if token == "" {
		httperr.WriteError(w, httperr.New(httperr.CodeElevationRequired, "elevated authorization required"))
		return
	}
	var body struct {
		Confirm string `json:"confirm"`
	}
	if !decodeJSONBody(w, r, &body) {
		return
	}
	if body.Confirm != confirmDeleteValue {
		httperr.WriteError(w, httperr.New(httperr.CodeInvalidRequest, "confirmation does not match"))
		return
	}
	if err := s.DeleteOwnAccountBound(r.Context(), user, token, db.SessionHash(auth.UserSessionToken(r))); err != nil {
		// A capability is single-use; clear the browser-side carrier on every
		// consume attempt so a stale token cannot linger in the SPA.
		auth.ClearElevatedCookie(w, httpmw.RequestIsHTTPS(r))
		writeErr(w, err)
		return
	}
	// The cascade already removed every session for this user (including the
	// current one); clear both browser carriers so the client drops them too.
	auth.ClearElevatedCookie(w, httpmw.RequestIsHTTPS(r))
	auth.ClearUserSessionCookie(w, httpmw.RequestIsHTTPS(r))
	w.WriteHeader(http.StatusNoContent)
}

// DeleteUserHandler handles DELETE /admin/api/users/{id}. It requires an
// administrator session and an active elevated-action capability
// (X-Elevated-Token) bound to that administrator. The administrator row is
// protected and refused with forbidden.
func (s *Service) DeleteUserHandler(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodDelete) || !requireStation(w, r, host.StationAdmin) {
		return
	}
	if s == nil || s.store == nil || s.elevation == nil {
		httperr.WriteError(w, httperr.New(httperr.CodeServiceUnavailable, "service unavailable"))
		return
	}
	admin, ok := auth.AdminFromContext(r.Context())
	if !ok {
		httperr.WriteError(w, httperr.New(httperr.CodeUnauthorized, "administrator authentication required"))
		return
	}
	token := elevatedTokenFromRequest(r)
	if token == "" {
		httperr.WriteError(w, httperr.New(httperr.CodeElevationRequired, "elevated authorization required"))
		return
	}
	targetRaw := r.PathValue("id")
	targetID, err := strconv.ParseInt(targetRaw, 10, 64)
	if err != nil || targetID <= 0 {
		httperr.WriteError(w, httperr.New(httperr.CodeNotFound, "account not found"))
		return
	}
	if err := s.DeleteUserAsAdminBound(r.Context(), admin, targetID, token, db.SessionHash(auth.AdminSessionToken(r))); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Mount registers the lifecycle routes on mux behind the supplied session
// middlewares. userSessionMw wraps user-station routes; adminSessionMw wraps
// admin-station routes. Both middlewares are expected to set the auth
// principal in context (internal/auth UserAuth.Middleware / AdminAuth.Middleware).
func (s *Service) Mount(mux *http.ServeMux, userSessionMw, adminSessionMw func(http.Handler) http.Handler) {
	if mux == nil || userSessionMw == nil || adminSessionMw == nil {
		return
	}
	mux.Handle("POST /api/account/delete", userSessionMw(httpmw.API(http.HandlerFunc(s.DeleteOwnAccountHandler))))
	mux.Handle("POST /admin/api/auth/elevate", adminSessionMw(httpmw.API(http.HandlerFunc(s.ElevateAdminHandler))))
	mux.Handle("DELETE /admin/api/users/{id}", adminSessionMw(httpmw.API(http.HandlerFunc(s.DeleteUserHandler))))
}

// Verify at build time that *auth.AdminAuth satisfies AdminPasswordVerifier
// through its constant-time AdminCredentialCheck method.
var _ AdminPasswordVerifier = (*auth.AdminAuth)(nil)
