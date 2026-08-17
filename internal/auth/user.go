package auth

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"nonbiriapi/internal/db"
	"nonbiriapi/internal/host"
	"nonbiriapi/internal/httperr"
	"nonbiriapi/internal/httpmw"
)

// UserAuthConfig wires the user station identity boundary. RedirectURI and
// UserRedirectPath are fixed configuration; neither is derived from request
// Host or forwarding headers.
type UserAuthConfig struct {
	Store            *db.Store
	Provider         DiscordProvider
	ClientID         string
	State            *StateManager
	SiteBaseURL      string
	RedirectURI      string
	UserRedirectPath string
	RegistrationGate RegistrationGateFunc
}

// UserAuth exposes handlers, a mountable auth route tree, and middleware for
// Discord login and user sessions. It does not register process-wide routes.
type UserAuth struct {
	store            *db.Store
	provider         DiscordProvider
	clientID         string
	state            *StateManager
	ownsState        bool
	siteBaseURL      string
	redirectURI      string
	userRedirectPath string
	registrationGate RegistrationGateFunc
}

// NewUserAuth validates fixed station configuration and returns a mountable
// user auth service.
func NewUserAuth(config UserAuthConfig) (*UserAuth, error) {
	if config.Store == nil || config.Provider == nil || !validateBoundedText(config.ClientID, 512, false) {
		return nil, ErrProviderUnavailable
	}
	base, err := fixedOrigin(config.SiteBaseURL)
	if err != nil {
		return nil, err
	}
	ownsState := false
	if config.State == nil {
		config.State, err = NewStateManager(DefaultOAuthStateTTL)
		if err != nil {
			return nil, err
		}
		ownsState = true
	}
	redirectURI := strings.TrimSpace(config.RedirectURI)
	if redirectURI == "" {
		redirectURI = base + "/api/auth/discord/callback"
	}
	if !validRedirectURI(base, redirectURI) {
		if ownsState {
			_ = config.State.Close()
		}
		return nil, ErrProviderUnavailable
	}
	path := config.UserRedirectPath
	if path == "" {
		path = "/"
	}
	if !validLocalRedirectPath(path) {
		if ownsState {
			_ = config.State.Close()
		}
		return nil, ErrProviderUnavailable
	}
	gate := config.RegistrationGate
	if gate == nil {
		gate = func(context.Context) (RegistrationGate, error) {
			guildID, roleID, err := config.Store.DiscordRegistrationGate()
			return RegistrationGate{GuildID: guildID, RoleID: roleID}, err
		}
	}
	return &UserAuth{
		store: config.Store, provider: config.Provider, clientID: config.ClientID, state: config.State,
		ownsState: ownsState, siteBaseURL: base, redirectURI: redirectURI, userRedirectPath: path,
		registrationGate: gate,
	}, nil
}

func validLocalRedirectPath(path string) bool {
	if path == "" || !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") || strings.ContainsAny(path, "\x00\r\n") {
		return false
	}
	parsed, err := url.Parse(path)
	return err == nil && !parsed.IsAbs() && parsed.Host == "" && parsed.User == nil && parsed.RawQuery == "" && parsed.Fragment == ""
}

// validRedirectURI binds the OAuth callback to the configured user origin. A
// redirect URI is startup configuration, not request data, but accepting a
// different authority here would turn a provider callback into a credential
// delivery redirect. Query strings and fragments are rejected so the callback
// URL remains one fixed protocol endpoint.
func validRedirectURI(base, raw string) bool {
	if !validateBoundedText(raw, 2048, false) {
		return false
	}
	baseURL, err := url.Parse(base)
	if err != nil {
		return false
	}
	redirect, err := url.Parse(raw)
	if err != nil || !redirect.IsAbs() || redirect.Host == "" || redirect.User != nil || redirect.RawQuery != "" || redirect.Fragment != "" {
		return false
	}
	if !strings.EqualFold(baseURL.Scheme, redirect.Scheme) {
		return false
	}
	baseHost, err := host.ParseConfigured(baseURL.Host)
	if err != nil {
		return false
	}
	redirectHost, err := host.ParseConfigured(redirect.Host)
	if err != nil || redirectHost.Authority != baseHost.Authority {
		return false
	}
	return redirect.Path != "" && strings.HasPrefix(redirect.Path, "/") && !strings.HasPrefix(redirect.Path, "//")
}

// Start handles GET /api/auth/discord/start. The intent is signed into state
// so later elevation flows cannot be confused with login.
func (a *UserAuth) Start(w http.ResponseWriter, r *http.Request) {
	a.StartWithIntent(w, r, OAuthIntentLogin)
}

func (a *UserAuth) StartWithIntent(w http.ResponseWriter, r *http.Request, intent string) {
	if !requireMethod(w, r, http.MethodGet) || !requireStation(w, r, host.StationUser) {
		return
	}
	if a == nil || a.store == nil || a.provider == nil || a.state == nil {
		writeStableError(w, httperr.CodeServiceUnavailable, "authentication service unavailable")
		return
	}
	if !validateIntent(intent) {
		writeStableError(w, httperr.CodeInvalidRequest, "invalid request")
		return
	}
	state, err := a.state.Issue(StationUser, intent)
	if err != nil {
		if errors.Is(err, ErrStateCapacity) {
			writeStableError(w, httperr.CodeServiceUnavailable, "authentication service unavailable")
		} else {
			writeStableError(w, httperr.CodeInternal, "authentication service unavailable")
		}
		return
	}
	location, err := a.provider.AuthorizationURL(r.Context(), DiscordAuthorizeRequest{
		ClientID: a.clientID, RedirectURI: a.redirectURI, State: state, Intent: intent,
	})
	if err != nil || !validAuthorizationLocation(location, state) {
		// The state remains pending only until its short TTL; no raw state is
		// returned. A provider URL failure is not a client-visible detail.
		writeStableError(w, httperr.CodeServiceUnavailable, "authentication service unavailable")
		return
	}
	SetOAuthStateCookie(w, state, secureCookieForRequest(r, a.siteBaseURL), a.state.ttl)
	noStoreRedirect(w, r, location)
}

func validAuthorizationLocation(location, state string) bool {
	if !validateBoundedText(location, 4096, false) || !validateOAuthStateText(state) {
		return false
	}
	parsed, err := url.Parse(location)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" || (!strings.EqualFold(parsed.Scheme, "https") && !strings.EqualFold(parsed.Scheme, "http")) {
		return false
	}
	// A custom provider must carry the exact state issued by this service. This
	// keeps the login-CSRF binding intact even when the provider adapter is
	// replaced in tests or by a future implementation.
	values, ok := parsed.Query()["state"]
	return ok && len(values) == 1 && values[0] == state
}

func fixedExternalURL(raw string) (string, error) {
	if !validateBoundedText(raw, 4096, false) {
		return "", ErrProviderUnavailable
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" || (!strings.EqualFold(parsed.Scheme, "https") && !strings.EqualFold(parsed.Scheme, "http")) {
		return "", ErrProviderUnavailable
	}
	return raw, nil
}

// Callback handles GET /api/auth/discord/callback. It consumes state before
// exchanging the code, preventing concurrent callback replay.
func (a *UserAuth) Callback(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) || !requireStation(w, r, host.StationUser) {
		return
	}
	if a == nil || a.store == nil || a.provider == nil || a.state == nil {
		writeStableError(w, httperr.CodeServiceUnavailable, "authentication service unavailable")
		return
	}
	state, stateOK := singleQueryValue(r, "state")
	code, codeOK := singleQueryValue(r, "code")
	cookieState := OAuthStateFromRequest(r)
	// Always clear the browser binding on a callback attempt. The state/code
	// values are never copied into an error or diagnostic.
	defer ClearOAuthStateCookie(w, secureCookieForRequest(r, a.siteBaseURL))
	if !stateOK || !codeOK || !validateOAuthStateText(state) || !validateOAuthStateText(cookieState) || !validateOAuthCode(code) {
		writeStableError(w, httperr.CodeInvalidRequest, "invalid authentication callback")
		return
	}
	if err := a.state.Consume(state, cookieState, StationUser, OAuthIntentLogin); err != nil {
		if errors.Is(err, ErrStateExpired) || errors.Is(err, ErrStateReplay) {
			writeStableError(w, httperr.CodeUnauthorized, "authentication failed")
		} else {
			writeStableError(w, httperr.CodeInvalidRequest, "invalid authentication callback")
		}
		return
	}
	login, err := a.provider.Exchange(r.Context(), code, a.redirectURI)
	if err != nil {
		writeAuthFailure(w, err)
		return
	}
	identity := login.Identity
	if !validateBoundedText(identity.ID, 128, false) || !validateBoundedText(identity.Username, maxUsernameBytes, false) || !validateBoundedText(identity.Avatar, 1024, true) {
		writeStableError(w, httperr.CodeServiceUnavailable, "authentication service unavailable")
		return
	}

	user, err := a.store.GetUserByDiscordID(identity.ID)
	if err != nil {
		writeStableError(w, httperr.CodeInternal, "authentication failed")
		return
	}
	if user == nil {
		user, err = a.register(identity, login, r.Context())
		if err != nil {
			writeAuthFailure(w, err)
			return
		}
	} else {
		if user.IsBanned {
			writeStableError(w, httperr.CodeUnauthorized, "authentication failed")
			return
		}
		if err := a.store.UpdateUserProfile(user.ID, identity.Username, identity.Avatar); err != nil {
			writeStableError(w, httperr.CodeInternal, "authentication failed")
			return
		}
		user.Username = identity.Username
		user.Avatar = identity.Avatar
	}

	token, expiry, err := a.store.CreateUserSession(user.ID)
	if err != nil {
		if errors.Is(err, db.ErrBanned) {
			writeStableError(w, httperr.CodeUnauthorized, "authentication failed")
		} else {
			writeStableError(w, httperr.CodeInternal, "authentication failed")
		}
		return
	}
	SetUserSessionCookie(w, token, expiry.Idle, secureCookieForRequest(r, a.siteBaseURL))
	noStoreRedirect(w, r, a.userRedirectPath)
}

func (a *UserAuth) register(identity DiscordIdentity, login DiscordLogin, ctx context.Context) (*db.User, error) {
	if a == nil || a.registrationGate == nil {
		return nil, ErrProviderUnavailable
	}
	gate, err := a.registrationGate(ctx)
	if err != nil {
		return nil, ErrProviderUnavailable
	}
	gate.GuildID = strings.TrimSpace(gate.GuildID)
	gate.RoleID = strings.TrimSpace(gate.RoleID)
	if !validateBoundedText(gate.GuildID, 128, false) || !validateBoundedText(gate.RoleID, 128, false) {
		return nil, ErrRegistrationPaused
	}
	var matches bool
	if login.HasGuildRole != nil {
		matches, err = login.HasGuildRole(ctx, gate.GuildID, gate.RoleID)
	} else {
		if !validateMembershipMetadata(identity) {
			return nil, ErrInvalidIdentity
		}
		matches = identity.GuildID == gate.GuildID && containsString(identity.RoleIDs, gate.RoleID)
	}
	if err != nil {
		return nil, ErrProviderUnavailable
	}
	if !matches {
		return nil, ErrGuildRoleMismatch
	}
	user, _, err := a.store.FindOrCreateDiscordUser(identity.ID, identity.Username, identity.Avatar)
	if errors.Is(err, db.ErrConflict) {
		// Another callback may have won the unique Discord id race. Resolve the
		// committed row, then let the caller apply the normal ban check.
		user, err = a.store.GetUserByDiscordID(identity.ID)
	}
	if err != nil || user == nil {
		if err == nil {
			err = db.ErrConflict
		}
		return nil, err
	}
	if user.IsBanned {
		return nil, db.ErrBanned
	}
	return user, nil
}

func validateMembershipMetadata(identity DiscordIdentity) bool {
	if !validateBoundedText(identity.GuildID, 128, true) || len(identity.RoleIDs) > maxDiscordRoleIDs {
		return false
	}
	for _, role := range identity.RoleIDs {
		if !validateBoundedText(role, 128, false) {
			return false
		}
	}
	return true
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// Session handles GET /api/session.
func (a *UserAuth) Session(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) || !requireStation(w, r, host.StationUser) {
		return
	}
	user, ok := a.authenticateUserRequest(r)
	if !ok {
		writeStableError(w, httperr.CodeUnauthorized, "authentication required")
		return
	}
	httperr.WriteJSON(w, http.StatusOK, userEnvelope{User: publicUser(user)})
}

// Me is the contract-compatible alias for the user profile probe.
func (a *UserAuth) Me(w http.ResponseWriter, r *http.Request) { a.Session(w, r) }

// Logout handles POST /api/auth/logout and atomically removes the presented
// user session. Invalid/missing cookies are not echoed.
func (a *UserAuth) Logout(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) || !requireStation(w, r, host.StationUser) {
		return
	}
	if a == nil || a.store == nil {
		writeStableError(w, httperr.CodeServiceUnavailable, "authentication service unavailable")
		return
	}
	token := UserSessionToken(r)
	if token == "" {
		writeStableError(w, httperr.CodeUnauthorized, "authentication required")
		return
	}
	user, err := a.store.AuthenticateUserSession(token)
	if err != nil || user == nil {
		writeStableError(w, httperr.CodeUnauthorized, "authentication required")
		return
	}
	if err := a.store.DeleteSession(token); err != nil {
		writeStableError(w, httperr.CodeInternal, "authentication failed")
		return
	}
	ClearUserSessionCookie(w, secureCookieForRequest(r, a.siteBaseURL))
	w.WriteHeader(http.StatusNoContent)
}

// Middleware authenticates a user session and places a server-authoritative
// principal in context. It rejects admin sessions and banned users.
func (a *UserAuth) Middleware(next http.Handler) http.Handler {
	if next == nil {
		next = http.NotFoundHandler()
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !requireStation(w, r, host.StationUser) {
			return
		}
		user, ok := a.authenticateUserRequest(r)
		if !ok {
			writeStableError(w, httperr.CodeUnauthorized, "authentication required")
			return
		}
		request := r.WithContext(withPrincipal(r.Context(), Principal{User: user, Kind: PrincipalUserSession}))
		next.ServeHTTP(w, request)
	})
}

func (a *UserAuth) authenticateUserRequest(r *http.Request) (*db.User, bool) {
	if a == nil || a.store == nil || r == nil {
		return nil, false
	}
	token := UserSessionToken(r)
	if token == "" {
		return nil, false
	}
	user, err := a.store.AuthenticateUserSession(token)
	return user, err == nil && user != nil && !user.IsAdmin && !user.IsBanned
}

// UserCallerKeyMetadata returns the safe metadata representation for the
// authenticated user. The full key is never returned by this helper.
func (a *UserAuth) UserCallerKeyMetadata(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) || !requireStation(w, r, host.StationUser) {
		return
	}
	if a == nil || a.store == nil {
		writeStableError(w, httperr.CodeServiceUnavailable, "authentication service unavailable")
		return
	}
	user, ok := a.authenticateUserRequest(r)
	if !ok {
		writeStableError(w, httperr.CodeUnauthorized, "authentication required")
		return
	}
	key, err := a.store.GetCallerKey(user.ID)
	if err != nil {
		writeStableError(w, httperr.CodeInternal, "caller key unavailable")
		return
	}
	if key == nil {
		writeStableError(w, httperr.CodeNotFound, "caller key not found")
		return
	}
	httperr.WriteJSON(w, http.StatusOK, map[string]any{
		"display": CallerKeyDisplay(key), "created_at": key.CreatedAt, "updated_at": key.UpdatedAt,
	})
}

// RegenerateCallerKey handles the one-time plaintext response. The response is
// explicitly no-store and carries no cacheable HTML or diagnostic.
func (a *UserAuth) RegenerateCallerKey(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) || !requireStation(w, r, host.StationUser) {
		return
	}
	if a == nil || a.store == nil {
		writeStableError(w, httperr.CodeServiceUnavailable, "authentication service unavailable")
		return
	}
	user, ok := a.authenticateUserRequest(r)
	if !ok {
		writeStableError(w, httperr.CodeUnauthorized, "authentication required")
		return
	}
	generation, err := a.store.RegenerateCallerKey(user.ID)
	if err != nil {
		if errors.Is(err, db.ErrBanned) {
			writeStableError(w, httperr.CodeUnauthorized, "authentication required")
		} else {
			writeStableError(w, httperr.CodeInternal, "caller key unavailable")
		}
		return
	}
	httperr.WriteJSON(w, http.StatusOK, map[string]string{"secret": generation.Secret})
}

// CallerKeyDisplay formats stored four-character fragments without requiring
// the secret.
func CallerKeyDisplay(key *db.CallerKey) string {
	if key == nil {
		return ""
	}
	return CallerKeyPrefixForDisplay + key.DisplayHead + "…" + key.DisplayTail
}

const CallerKeyPrefixForDisplay = db.CallerKeyPrefix

// Close releases an internally-created ephemeral OAuth state signer. A
// caller-supplied StateManager remains owned by that caller.
func (a *UserAuth) Close() error {
	if a == nil || !a.ownsState || a.state == nil {
		return nil
	}
	return a.state.Close()
}

// Handler returns the user-station authentication route tree. It is safe to
// mount under the shared httpmw edge: every route still checks the validated
// station context, and the API wrapper turns fallback 404/405 responses into
// the stable no-store error envelope.
func (a *UserAuth) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/auth/discord/start", a.Start)
	mux.HandleFunc("GET /api/auth/discord/callback", a.Callback)
	mux.HandleFunc("GET /api/session", a.Session)
	mux.HandleFunc("GET /api/me", a.Me)
	mux.HandleFunc("POST /api/auth/logout", a.Logout)
	mux.HandleFunc("GET /api/caller-key", a.UserCallerKeyMetadata)
	mux.HandleFunc("POST /api/caller-key/regenerate", a.RegenerateCallerKey)
	return httpmw.API(mux)
}
