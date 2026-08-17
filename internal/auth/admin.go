package auth

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"net/http"

	"nonbiriapi/internal/db"
	"nonbiriapi/internal/host"
	"nonbiriapi/internal/httperr"
	"nonbiriapi/internal/httpmw"
	"nonbiriapi/internal/ratelimit"
)

// AdminAuthConfig contains the environment-owned administrator credentials.
// Password is retained only in this service's process memory and is never
// passed to the database.
type AdminAuthConfig struct {
	Store       *db.Store
	Username    string
	Password    string
	Throttle    LoginThrottle
	SiteBaseURL string
}

// AdminAuth provides handlers, a mountable route tree, and middleware for the
// isolated administrator station. It does not register process-wide routes.
type AdminAuth struct {
	store         *db.Store
	username      string
	password      string
	throttle      LoginThrottle
	siteBaseURL   string
	ownedThrottle *ratelimit.LoginThrottle
}

// NewAdminAuth validates credentials and installs a bounded login throttle.
// A nil throttle receives a bounded in-memory default; callers may inject the
// shared rail implementation for process-wide policy.
func NewAdminAuth(config AdminAuthConfig) (*AdminAuth, error) {
	if config.Store == nil || !validateBoundedText(config.Username, maxUsernameBytes, false) || !validateBoundedText(config.Password, maxPasswordBytes, false) {
		return nil, ErrProviderUnavailable
	}
	base := ""
	if config.SiteBaseURL != "" {
		var err error
		base, err = fixedOrigin(config.SiteBaseURL)
		if err != nil {
			return nil, ErrProviderUnavailable
		}
	}
	service := &AdminAuth{store: config.Store, username: config.Username, password: config.Password, throttle: config.Throttle, siteBaseURL: base}
	if service.throttle == nil {
		throttle, err := ratelimit.NewLoginThrottle(ratelimit.DefaultLoginThrottleConfig())
		if err != nil {
			return nil, ErrProviderUnavailable
		}
		service.throttle = throttle
		service.ownedThrottle = throttle
	}
	return service, nil
}

// Close releases an internally-created bounded throttle. Injected throttles
// remain owned by their caller.
func (a *AdminAuth) Close() error {
	if a == nil || a.ownedThrottle == nil {
		return nil
	}
	return a.ownedThrottle.Close()
}

// Login handles POST /admin/api/login. It compares both username and password
// using fixed-size SHA-256 digests and uses the trusted edge ClientIP for the
// failure throttle.
func (a *AdminAuth) Login(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) || !requireStation(w, r, host.StationAdmin) {
		return
	}
	if a == nil || a.store == nil || a.throttle == nil {
		writeStableError(w, httperr.CodeServiceUnavailable, "authentication service unavailable")
		return
	}
	var request struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decodeJSONBody(w, r, &request) || !validateBoundedText(request.Username, maxUsernameBytes, false) || !validateBoundedText(request.Password, maxPasswordBytes, false) {
		writeStableError(w, httperr.CodeInvalidRequest, "invalid request")
		return
	}
	identity := httpmw.ClientIP(r)
	if identity == "" {
		identity = "unknown"
	}
	decision, err := a.throttle.Check(identity, request.Username)
	if err != nil {
		// A malformed/over-capacity throttle identity fails closed without
		// exposing implementation state.
		writeStableError(w, httperr.CodeRateLimited, "login temporarily unavailable")
		return
	}
	if decision.Locked || !decision.Allowed {
		setRetryAfter(w, decision.RetryAfterSeconds)
		writeStableError(w, httperr.CodeRateLimited, "login temporarily unavailable")
		return
	}

	usernameValid := constantTimeCredentialEqual(a.username, request.Username)
	passwordValid := constantTimeCredentialEqual(a.password, request.Password)
	valid := usernameValid && passwordValid
	if !valid {
		failure, failureErr := a.throttle.Failure(identity, request.Username)
		if failureErr != nil {
			writeStableError(w, httperr.CodeRateLimited, "login temporarily unavailable")
			return
		}
		if failure.Locked {
			setRetryAfter(w, failure.RetryAfterSeconds)
			writeStableError(w, httperr.CodeRateLimited, "login temporarily unavailable")
			return
		}
		writeStableError(w, httperr.CodeUnauthorized, "invalid administrator credentials")
		return
	}
	if err := a.throttle.Success(identity, request.Username); err != nil {
		writeStableError(w, httperr.CodeRateLimited, "login temporarily unavailable")
		return
	}
	admin, err := a.store.EnsureAdminUser(a.username)
	if err != nil || admin == nil || !admin.IsAdmin {
		writeStableError(w, httperr.CodeInternal, "administrator authentication unavailable")
		return
	}
	token, expiry, err := a.store.CreateAdminSession(admin.ID)
	if err != nil {
		writeStableError(w, httperr.CodeInternal, "administrator authentication unavailable")
		return
	}
	SetAdminSessionCookie(w, token, expiry.Idle, secureCookieForRequest(r, a.siteBaseURL))
	httperr.WriteJSON(w, http.StatusOK, adminEnvelope{Admin: AdminResponse{Username: admin.Username}})
}

func constantTimeCredentialEqual(expected, candidate string) bool {
	// The HTTP decoder enforces this bound too; keeping it here prevents an
	// exported credential-check hook from hashing attacker-sized input.
	if len(expected) > maxPasswordBytes || len(candidate) > maxPasswordBytes {
		return false
	}
	expectedDigest := sha256.Sum256([]byte(expected))
	candidateDigest := sha256.Sum256([]byte(candidate))
	return subtle.ConstantTimeCompare(expectedDigest[:], candidateDigest[:]) == 1
}

// Session handles GET /admin/api/session.
func (a *AdminAuth) Session(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) || !requireStation(w, r, host.StationAdmin) {
		return
	}
	admin, ok := a.authenticateRequest(r)
	if !ok {
		writeStableError(w, httperr.CodeUnauthorized, "administrator authentication required")
		return
	}
	httperr.WriteJSON(w, http.StatusOK, adminEnvelope{Admin: AdminResponse{Username: admin.Username}})
}

// Logout handles POST /admin/api/logout and atomically consumes the current
// admin session.
func (a *AdminAuth) Logout(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) || !requireStation(w, r, host.StationAdmin) {
		return
	}
	if a == nil || a.store == nil {
		writeStableError(w, httperr.CodeServiceUnavailable, "authentication service unavailable")
		return
	}
	token := AdminSessionToken(r)
	if token == "" {
		writeStableError(w, httperr.CodeUnauthorized, "administrator authentication required")
		return
	}
	admin, err := a.store.AuthenticateAdminSession(token)
	if err != nil || admin == nil {
		writeStableError(w, httperr.CodeUnauthorized, "administrator authentication required")
		return
	}
	if err := a.store.DeleteSession(token); err != nil {
		writeStableError(w, httperr.CodeInternal, "administrator authentication failed")
		return
	}
	ClearAdminSessionCookie(w, secureCookieForRequest(r, a.siteBaseURL))
	w.WriteHeader(http.StatusNoContent)
}

// Middleware enforces the admin station and admin role independently of user
// session cookies. A normal user session cannot satisfy this middleware.
func (a *AdminAuth) Middleware(next http.Handler) http.Handler {
	if next == nil {
		next = http.NotFoundHandler()
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !requireStation(w, r, host.StationAdmin) {
			return
		}
		admin, ok := a.authenticateRequest(r)
		if !ok {
			writeStableError(w, httperr.CodeUnauthorized, "administrator authentication required")
			return
		}
		request := r.WithContext(withPrincipal(r.Context(), Principal{User: admin, Kind: PrincipalAdminSession}))
		next.ServeHTTP(w, request)
	})
}

func (a *AdminAuth) authenticateRequest(r *http.Request) (*db.User, bool) {
	if a == nil || a.store == nil || r == nil {
		return nil, false
	}
	token := AdminSessionToken(r)
	if token == "" {
		return nil, false
	}
	admin, err := a.store.AuthenticateAdminSession(token)
	return admin, err == nil && admin != nil && admin.IsAdmin
}

// AdminCredentialCheck is a direct constant-time credential hook for tests or
// a future two-step elevation handler. It does not query the database.
func (a *AdminAuth) AdminCredentialCheck(username, password string) bool {
	if a == nil {
		return false
	}
	usernameValid := constantTimeCredentialEqual(a.username, username)
	passwordValid := constantTimeCredentialEqual(a.password, password)
	return usernameValid && passwordValid
}

// LoginThrottleAdapter adapts a concrete login limiter when callers want an
// explicit named mount point instead of relying on interface assignment.
type LoginThrottleAdapter struct {
	Inner *ratelimit.LoginThrottle
}

func (a LoginThrottleAdapter) Check(identity, username string) (ratelimit.LoginDecision, error) {
	if a.Inner == nil {
		return ratelimit.LoginDecision{}, errors.New("login throttle unavailable")
	}
	return a.Inner.Check(identity, username)
}
func (a LoginThrottleAdapter) Failure(identity, username string) (ratelimit.LoginDecision, error) {
	if a.Inner == nil {
		return ratelimit.LoginDecision{}, errors.New("login throttle unavailable")
	}
	return a.Inner.Failure(identity, username)
}
func (a LoginThrottleAdapter) Success(identity, username string) error {
	if a.Inner == nil {
		return errors.New("login throttle unavailable")
	}
	return a.Inner.Success(identity, username)
}

// Handler returns the admin-station authentication route tree. The shared
// edge must still wrap this handler so httpmw.StationOf is authoritative.
func (a *AdminAuth) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /admin/api/login", a.Login)
	mux.HandleFunc("POST /admin/api/logout", a.Logout)
	mux.HandleFunc("GET /admin/api/session", a.Session)
	return httpmw.API(mux)
}
