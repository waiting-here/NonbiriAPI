package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/elevation"
	"github.com/waiting-here/NonbiriAPI/internal/host"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/httpmw"
	"github.com/waiting-here/NonbiriAPI/internal/ratelimit"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

var adminCredentialPurpose = []byte("NonbiriAPI/admin-session-credential-generation/v1")

type Runtime struct {
	db                                *sql.DB
	provider                          DiscordProvider
	clientID, redirectURI, siteOrigin string
	adminUsername                     string
	adminPasswordDigest               [32]byte
	adminCredentialGeneration         string
	authorizer                        *authz.Authorizer
	maintenance                       MaintenanceGate
	states                            *StateManager
	elevation                         *elevation.Manager
	oauthThrottle                     *ratelimit.IPThrottle
	adminThrottle                     LoginThrottle
	now                               func() time.Time
	idleTTL, absoluteTTL              time.Duration

	mu                        sync.Mutex
	userMux, adminMux         *http.ServeMux
	routes                    map[string]struct{}
	frozen, closed            bool
	userHandler, adminHandler http.Handler
}

var _ resources.UserRouteRegistrar = (*Runtime)(nil)
var _ resources.FinalTxAuthorizer = (*Runtime)(nil)

func NewRuntime(c RuntimeConfig) (*Runtime, error) {
	if c.Store == nil || c.Store.DB() == nil || c.Provider == nil || c.Authorizer == nil || c.Maintenance == nil || c.CredentialKeyDeriver == nil {
		return nil, errors.New("authentication runtime configuration is incomplete")
	}
	origin, err := fixedOrigin(c.UserSiteBaseURL)
	if err != nil {
		return nil, err
	}
	redirect := c.DiscordRedirectURI
	if redirect == "" {
		redirect = origin + "/api/auth/discord/callback"
	}
	if !validRedirectURI(redirect, origin) {
		return nil, errors.New("discord redirect URI is invalid")
	}
	if !validateBoundedText(c.DiscordClientID, 512, false) || !validateBoundedText(c.AdminUsername, maxUsernameBytes, false) || !validateBoundedText(c.AdminPassword, maxPasswordBytes, false) {
		return nil, errors.New("authentication credential configuration is invalid")
	}
	idle := c.SessionIdleTTL
	if idle == 0 {
		idle = DefaultSessionIdleTTL
	}
	absolute := c.SessionAbsoluteTTL
	if absolute == 0 {
		absolute = DefaultSessionAbsoluteTTL
	}
	if idle <= 0 || absolute <= 0 || idle > absolute || absolute > 366*24*time.Hour {
		return nil, errors.New("session lifetime configuration is invalid")
	}
	states := c.OAuthStates
	createdStates := false
	if states == nil {
		states, err = NewStateManager(DefaultOAuthStateTTL)
		if err != nil {
			return nil, err
		}
		createdStates = true
	}
	elev := c.Elevation
	createdElevation := false
	if elev == nil {
		elev, err = elevation.NewManager()
		if err != nil {
			if createdStates {
				_ = states.Close()
			}
			return nil, err
		}
		createdElevation = true
	}
	throttleConfig := ratelimit.DefaultIPThrottleConfig()
	throttleConfig.MaxHitsPerKey = 1000
	oauthThrottle, err := ratelimit.NewIPThrottle(throttleConfig)
	if err != nil {
		if createdStates {
			_ = states.Close()
		}
		if createdElevation {
			_ = elev.Close()
		}
		return nil, err
	}
	adminThrottle := c.AdminLoginThrottle
	if adminThrottle == nil {
		adminThrottle, err = ratelimit.NewLoginThrottle(ratelimit.DefaultLoginThrottleConfig())
		if err != nil {
			_ = oauthThrottle.Close()
			if createdStates {
				_ = states.Close()
			}
			if createdElevation {
				_ = elev.Close()
			}
			return nil, err
		}
	}
	subkey, err := c.CredentialKeyDeriver.DeriveGenerationTwoSubkey(adminCredentialPurpose)
	if err != nil || len(subkey) < 32 {
		clear(subkey)
		_ = oauthThrottle.Close()
		if createdStates {
			_ = states.Close()
		}
		if createdElevation {
			_ = elev.Close()
		}
		return nil, errors.New("admin credential generation unavailable")
	}
	mac := hmac.New(sha256.New, subkey)
	_, _ = mac.Write([]byte(c.AdminPassword))
	generation := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	clear(subkey)
	now := c.Now
	if now == nil {
		now = time.Now
	}
	r := &Runtime{db: c.Store.DB(), provider: c.Provider, clientID: c.DiscordClientID, redirectURI: redirect, siteOrigin: origin, adminUsername: c.AdminUsername, adminPasswordDigest: sha256.Sum256([]byte(c.AdminPassword)), adminCredentialGeneration: generation, authorizer: c.Authorizer, maintenance: c.Maintenance, states: states, elevation: elev, oauthThrottle: oauthThrottle, adminThrottle: adminThrottle, now: now, idleTTL: idle, absoluteTTL: absolute, userMux: http.NewServeMux(), adminMux: http.NewServeMux(), routes: make(map[string]struct{})}
	if err := r.registerBuiltins(); err != nil {
		_ = r.Close()
		return nil, err
	}
	return r, nil
}

func validRedirectURI(raw, origin string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "/api/auth/discord/callback" {
		return false
	}
	return strings.EqualFold(u.Scheme+"://"+u.Host, origin)
}

func (r *Runtime) registerBuiltins() error {
	for _, route := range []struct {
		method, path string
		handler      http.HandlerFunc
	}{
		{http.MethodGet, "/api/auth/discord/start", r.oauthStart}, {http.MethodGet, "/api/auth/discord/callback", r.oauthCallback},
		{http.MethodGet, "/api/session", r.userSession}, {http.MethodPost, "/api/auth/elevate", r.userElevate}, {http.MethodPost, "/api/auth/logout", r.userLogout},
		{http.MethodGet, "/api/me", r.userMe}, {http.MethodPatch, "/api/me", r.patchMe}, {http.MethodGet, "/api/me/usage", r.userUsage},
		{http.MethodPost, "/admin/api/login", r.adminLogin}, {http.MethodPost, "/admin/api/logout", r.adminLogout}, {http.MethodGet, "/admin/api/session", r.adminSession}, {http.MethodPost, "/admin/api/auth/elevate", r.adminElevate},
	} {
		if strings.HasPrefix(route.path, "/admin/") {
			anonymous := route.path == "/admin/api/login"
			logout := route.method == http.MethodPost && route.path == "/admin/api/logout"
			if err := r.registerAdminInternal(route.method, route.path, route.handler, anonymous, logout); err != nil {
				return err
			}
		} else {
			anonymous := route.path == "/api/auth/discord/start" || route.path == "/api/auth/discord/callback"
			maintenanceExempt := route.method == http.MethodPost && route.path == "/api/auth/logout"
			if err := r.registerUserInternal(route.method, route.path, route.handler, anonymous, maintenanceExempt); err != nil {
				return err
			}
		}
	}
	return nil
}

func routeKey(method, pattern string) (string, error) {
	if method == "" || method != strings.ToUpper(method) || strings.ContainsAny(method, " \t\r\n") || !strings.HasPrefix(pattern, "/") || strings.ContainsAny(pattern, "?#") {
		return "", ErrInvalidRoute
	}
	key := method + " " + pattern
	if !validServeMuxPattern(key) {
		return "", ErrInvalidRoute
	}
	return key, nil
}

func validServeMuxPattern(pattern string) (valid bool) {
	defer func() {
		if recover() != nil {
			valid = false
		}
	}()
	http.NewServeMux().Handle(pattern, http.NotFoundHandler())
	return true
}

func (r *Runtime) registerUserInternal(method, pattern string, handler http.Handler, anonymous, maintenanceExempt bool) error {
	wrapped := r.wrapUser(handler, anonymous, maintenanceExempt)
	return r.mountRoute(r.userMux, method, pattern, wrapped)
}
func (r *Runtime) registerAdminInternal(method, pattern string, handler http.Handler, anonymous, logout bool) error {
	return r.mountRoute(r.adminMux, method, pattern, r.wrapAdmin(handler, anonymous, logout))
}

func (r *Runtime) mountRoute(mux *http.ServeMux, method, pattern string, handler http.Handler) (err error) {
	key, err := routeKey(method, pattern)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return ErrClosed
	}
	if r.frozen {
		return ErrFrozen
	}
	if _, ok := r.routes[key]; ok {
		return ErrDuplicateRoute
	}
	defer func() {
		if recover() != nil {
			err = ErrInvalidRoute
		}
	}()
	mux.Handle(key, handler)
	r.routes[key] = struct{}{}
	return nil
}

func (r *Runtime) RegisterAnonymousUserRoute(method, pattern string, handler AnonymousUserHandler) error {
	if handler == nil {
		return ErrInvalidRoute
	}
	return r.registerUserInternal(method, pattern, http.HandlerFunc(handler), true, false)
}

func (r *Runtime) RegisterOptionalUserRoute(method, pattern string, handler OptionalUserHandler) error {
	if handler == nil {
		return ErrInvalidRoute
	}
	return r.mountRoute(r.userMux, method, pattern, r.wrapOptionalUser(handler))
}

func (r *Runtime) RegisterUserRoute(method, pattern string, handler resources.AuthorizedUserHandler) error {
	if handler == nil {
		return ErrInvalidRoute
	}
	adapter := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		actor, ok := ActorFromContext(req.Context())
		if !ok || actor.Kind != authz.ActorUserSession {
			writeStableError(w, httperr.CodeUnauthorized, "authentication required")
			return
		}
		handler(w, req, resources.UserPrincipal{UserID: actor.UserID})
	})
	return r.registerUserInternal(method, pattern, adapter, false, false)
}

func (r *Runtime) RegisterAdminRoute(method, pattern string, handler http.Handler) error {
	if handler == nil {
		return ErrInvalidRoute
	}
	return r.registerAdminInternal(method, pattern, handler, false, false)
}

func (r *Runtime) UserRouteRegistrar() resources.UserRouteRegistrar { return r }
func (r *Runtime) FinalTxAuthorizer() resources.FinalTxAuthorizer   { return r }
func (r *Runtime) ElevationManager() *elevation.Manager             { return r.elevation }

// ClearUserElevationCookie removes the browser handoff after a user
// capability consume attempt. It deliberately does not affect admin
// elevation, whose wire remains a JSON token response.
func (r *Runtime) ClearUserElevationCookie(w http.ResponseWriter, req *http.Request) {
	if r == nil || w == nil {
		return
	}
	clearElevatedCookie(w, secureCookieForRequest(req, r.siteOrigin))
}

func (r *Runtime) freeze() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return ErrClosed
	}
	r.frozen = true
	return nil
}

func (r *Runtime) UserHandler() http.Handler {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.userHandler == nil {
		r.frozen = true
		r.userHandler = httpmw.API(r.userMux)
	}
	return r.userHandler
}
func (r *Runtime) AdminHandler() http.Handler {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.adminHandler == nil {
		r.frozen = true
		r.adminHandler = httpmw.API(r.adminMux)
	}
	return r.adminHandler
}

func (r *Runtime) maintenanceAllowed(w http.ResponseWriter, exempt bool) bool {
	state, ready := r.maintenance.State()
	if !ready {
		writeStableError(w, httperr.CodeServiceUnavailable, "service unavailable")
		return false
	}
	if state.Enabled && !exempt {
		writeStableError(w, httperr.CodeMaintenance, "maintenance in progress")
		return false
	}
	return true
}

func (r *Runtime) isClosed() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.closed
}

func (r *Runtime) wrapUser(next http.Handler, anonymous, maintenanceExempt bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if !requireStation(w, req, host.StationUser) {
			return
		}
		if r.isClosed() {
			writeStableError(w, httperr.CodeServiceUnavailable, "service unavailable")
			return
		}
		if !r.maintenanceAllowed(w, maintenanceExempt) {
			return
		}
		if anonymous {
			next.ServeHTTP(w, req)
			return
		}
		raw, ok := cookieValue(req, UserSessionCookieName)
		if !ok {
			writeStableError(w, httperr.CodeUnauthorized, "authentication required")
			return
		}
		elevated, ok := singleHeader(req, "X-Elevated-Token", false)
		if !ok {
			writeStableError(w, httperr.CodeInvalidRequest, "invalid request")
			return
		}
		p, err := r.authenticate(req.Context(), raw, authz.ActorUserSession, elevated)
		if err != nil {
			r.writeSessionFailure(w, err)
			return
		}
		if !maintenanceExempt {
			setUserSessionCookie(w, raw, timeFromUnix(p.expiresAt), r.now(), secureCookieForRequest(req, r.siteOrigin))
		}
		next.ServeHTTP(w, req.WithContext(withActor(req.Context(), p.actor)))
	})
}

func (r *Runtime) wrapOptionalUser(next OptionalUserHandler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if !requireStation(w, req, host.StationUser) {
			return
		}
		if r.isClosed() {
			writeStableError(w, httperr.CodeServiceUnavailable, "service unavailable")
			return
		}
		if !r.maintenanceAllowed(w, false) {
			return
		}
		raw, present, valid := uniqueCookieValue(req, UserSessionCookieName)
		if !present {
			next(w, req, nil)
			return
		}
		if !valid {
			writeStableError(w, httperr.CodeUnauthorized, "authentication required")
			return
		}
		elevated, ok := singleHeader(req, "X-Elevated-Token", false)
		if !ok {
			writeStableError(w, httperr.CodeInvalidRequest, "invalid request")
			return
		}
		p, err := r.authenticate(req.Context(), raw, authz.ActorUserSession, elevated)
		if err != nil {
			r.writeSessionFailure(w, err)
			return
		}
		setUserSessionCookie(w, raw, timeFromUnix(p.expiresAt), r.now(), secureCookieForRequest(req, r.siteOrigin))
		principal := &OptionalUserPrincipal{UserID: p.actor.UserID}
		next(w, req.WithContext(withActor(req.Context(), p.actor)), principal)
	})
}

func (r *Runtime) wrapAdmin(next http.Handler, anonymous, logout bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if !requireStation(w, req, host.StationAdmin) {
			return
		}
		if r.isClosed() {
			writeStableError(w, httperr.CodeServiceUnavailable, "service unavailable")
			return
		}
		if anonymous {
			next.ServeHTTP(w, req)
			return
		}
		raw, ok := cookieValue(req, AdminSessionCookieName)
		if !ok {
			writeStableError(w, httperr.CodeUnauthorized, "authentication required")
			return
		}
		elevated, ok := singleHeader(req, "X-Elevated-Token", false)
		if !ok {
			writeStableError(w, httperr.CodeInvalidRequest, "invalid request")
			return
		}
		p, err := r.authenticate(req.Context(), raw, authz.ActorAdminSession, elevated)
		if err != nil {
			r.writeSessionFailure(w, err)
			return
		}
		if !logout {
			setAdminSessionCookie(w, raw, timeFromUnix(p.expiresAt), r.now(), secureCookieForRequest(req, r.siteOrigin))
		}
		next.ServeHTTP(w, req.WithContext(withActor(req.Context(), p.actor)))
	})
}

func (r *Runtime) writeSessionFailure(w http.ResponseWriter, err error) {
	if errors.Is(err, errSessionForbidden) {
		writeStableError(w, httperr.CodeForbidden, "access forbidden")
		return
	}
	if errors.Is(err, errSessionUnauthorized) {
		writeStableError(w, httperr.CodeUnauthorized, "authentication required")
		return
	}
	writeStableError(w, httperr.CodeInternal, "authentication failed")
}

func (r *Runtime) AuthorizeUserMutation(ctx context.Context, tx *sql.Tx, userID int64) error {
	actor, ok := ActorFromContext(ctx)
	if !ok || actor.Kind != authz.ActorUserSession || actor.UserID != userID {
		return resources.ErrUnauthorized
	}
	_, err := r.authorizer.Authorize(ctx, tx, actor, authz.Requirement{Role: authz.RoleUser})
	switch {
	case err == nil:
		return nil
	case errors.Is(err, authz.ErrUnauthorized):
		return resources.ErrUnauthorized
	case errors.Is(err, authz.ErrForbidden):
		return resources.ErrForbidden
	case errors.Is(err, authz.ErrNotFound):
		return resources.ErrNotFound
	default:
		return fmt.Errorf("authorize user mutation: %w", err)
	}
}

func (r *Runtime) allowOAuthStart(w http.ResponseWriter, req *http.Request) bool {
	values := make(map[string]string, 3)
	for _, key := range []string{"oauth_start_rate_limit", "oauth_start_rate_window_seconds", "oauth_start_rate_penalty_seconds"} {
		var value string
		if err := r.db.QueryRowContext(req.Context(), `SELECT value FROM site_config WHERE key=?`, key).Scan(&value); err != nil {
			writeStableError(w, httperr.CodeServiceUnavailable, "authentication service unavailable")
			return false
		}
		values[key] = value
	}
	limit, err1 := strconv.Atoi(values["oauth_start_rate_limit"])
	window, err2 := strconv.Atoi(values["oauth_start_rate_window_seconds"])
	penalty, err3 := strconv.Atoi(values["oauth_start_rate_penalty_seconds"])
	if err1 != nil || err2 != nil || err3 != nil || limit < 0 || limit > 1000 || window < 1 || window > 3600 || penalty < 0 || penalty > 3600 {
		writeStableError(w, httperr.CodeServiceUnavailable, "authentication service unavailable")
		return false
	}
	if err := r.oauthThrottle.Reconfigure(ratelimit.IPThrottleConfig{Limit: limit, Window: time.Duration(window) * time.Second, Penalty: time.Duration(penalty) * time.Second}); err != nil {
		writeStableError(w, httperr.CodeServiceUnavailable, "authentication service unavailable")
		return false
	}
	decision, err := r.oauthThrottle.Allow(httpmw.ClientIP(req))
	if err != nil || !decision.Allowed {
		if decision.Reason == ratelimit.IPPenalty {
			setRetryAfter(w, decision.RetryAfterSeconds)
			writeStableError(w, httperr.CodeRateLimited, "too many requests")
		} else {
			writeStableError(w, httperr.CodeServiceUnavailable, "authentication service unavailable")
		}
		return false
	}
	return true
}

func (r *Runtime) passwordMatches(value string) bool {
	if !validateBoundedText(value, maxPasswordBytes, false) {
		return false
	}
	digest := sha256.Sum256([]byte(value))
	return hmac.Equal(digest[:], r.adminPasswordDigest[:])
}

func (r *Runtime) Close() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil
	}
	r.closed = true
	r.mu.Unlock()
	var first error
	for _, closeFn := range []func() error{r.states.Close, r.elevation.Close, r.oauthThrottle.Close} {
		if err := closeFn(); err != nil && first == nil {
			first = err
		}
	}
	if closer, ok := r.adminThrottle.(interface{ Close() error }); ok {
		if err := closer.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}
