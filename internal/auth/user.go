package auth

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/elevation"
	"github.com/waiting-here/NonbiriAPI/internal/host"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/httpmw"
	"github.com/waiting-here/NonbiriAPI/internal/ratelimit"
)

// DefaultElevatedCapabilityTTL mirrors the shared elevation manager default
// while keeping the auth configuration API stable.
const DefaultElevatedCapabilityTTL = elevation.DefaultTTL

// UserAuthConfig wires the user station identity boundary. RedirectURI and
// UserRedirectPath are fixed configuration; neither is derived from request
// Host or forwarding headers.
type UserAuthConfig struct {
	Store                  *db.Store
	Provider               DiscordProvider
	ClientID               string
	State                  *StateManager
	SiteBaseURL            string
	RedirectURI            string
	UserRedirectPath       string
	RegistrationClosedPath string
	RegistrationGate       RegistrationGateFunc
	// ElevatedCapabilityTTL bounds a completed elevation (the second factor
	// for account self-service). Zero selects DefaultElevatedCapabilityTTL.
	ElevatedCapabilityTTL time.Duration
	// Elevation optionally supplies the shared user/admin capability manager.
	// Nil creates an owned manager for a standalone auth service; production
	// should inject one instance shared with lifecycle.
	Elevation *elevation.Manager
	// OAuthStartThrottle optionally supplies the per-client-IP admission
	// throttle applied immediately before an OAuth state is issued for login
	// (GET /api/auth/discord/start) or elevation (POST /api/auth/elevate). It
	// is the second layer behind the reverse-proxy per-IP limit and stops one
	// client from exhausting the shared, fail-closed OAuth state pool. Nil
	// disables application-level admission; a throttle with Limit == 0 admits
	// all callers and only bounds the identity. The same instance is shared by
	// both flows and keyed by the trusted-edge ClientIP, and is not owned by
	// UserAuth (the integration rail closes it alongside the runtime applier).
	OAuthStartThrottle *ratelimit.IPThrottle
}

// UserAuth exposes handlers, a mountable auth route tree, and middleware for
// Discord login and user sessions. It does not register process-wide routes.
type UserAuth struct {
	store                  *db.Store
	provider               DiscordProvider
	clientID               string
	state                  *StateManager
	elevation              *elevation.Manager
	ownsElevation          bool
	ownsState              bool
	siteBaseURL            string
	redirectURI            string
	userRedirectPath       string
	registrationClosedPath string
	registrationGate       RegistrationGateFunc
	oauthStartThrottle     *ratelimit.IPThrottle
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
	registrationClosedPath := strings.TrimSpace(config.RegistrationClosedPath)
	if registrationClosedPath == "" {
		registrationClosedPath = "/registration-closed"
	}
	if !validLocalRedirectPath(registrationClosedPath) {
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
	capabilityTTL := config.ElevatedCapabilityTTL
	if capabilityTTL <= 0 {
		capabilityTTL = DefaultElevatedCapabilityTTL
	}
	manager := config.Elevation
	ownsElevation := false
	if manager == nil {
		manager, err = elevation.NewManagerWithTTL(capabilityTTL)
		if err != nil {
			if ownsState {
				_ = config.State.Close()
			}
			return nil, ErrProviderUnavailable
		}
		ownsElevation = true
	}
	return &UserAuth{
		store: config.Store, provider: config.Provider, clientID: config.ClientID, state: config.State,
		ownsState: ownsState, siteBaseURL: base, redirectURI: redirectURI, userRedirectPath: path,
		registrationClosedPath: registrationClosedPath, registrationGate: gate, elevation: manager,
		ownsElevation: ownsElevation, oauthStartThrottle: config.OAuthStartThrottle,
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
	if !a.admitOAuthStart(w, r) {
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

// admitOAuthStart applies the optional per-client-IP admission throttle that
// guards the shared, fail-closed OAuth state pool. It runs immediately before
// a state is issued for login or elevation, on the same throttle instance and
// the same trusted-edge ClientIP key for both flows. A penalized caller gets
// 429 with a Retry-After header; any throttle error (a full bounded entry
// store, a closed throttle, or an overlong identity) fails closed as 503 so a
// live attack can never evict a real pending state. A nil throttle (or one
// configured with Limit == 0) admits the caller and the configured
// reverse-proxy limit remains the outer boundary.
func (a *UserAuth) admitOAuthStart(w http.ResponseWriter, r *http.Request) bool {
	if a == nil || a.oauthStartThrottle == nil {
		return true
	}
	identity := httpmw.ClientIP(r)
	if identity == "" {
		identity = "unknown"
	}
	decision, err := a.oauthStartThrottle.Allow(identity)
	if err != nil {
		writeStableError(w, httperr.CodeServiceUnavailable, "authentication service unavailable")
		return false
	}
	if !decision.Allowed {
		setRetryAfter(w, decision.RetryAfterSeconds)
		writeStableError(w, httperr.CodeRateLimited, "authentication temporarily unavailable")
		return false
	}
	return true
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
	// ConsumeAny verifies the signature, cookie equality, station, expiry and
	// one-use nonce in one atomic step and reveals the signed intent, so the
	// callback can branch without ever trusting an intent supplied by the
	// client or mixing login and elevation states.
	intent, binding, err := a.state.ConsumeAny(state, cookieState, StationUser)
	if err != nil {
		if errors.Is(err, ErrStateExpired) || errors.Is(err, ErrStateReplay) {
			writeStableError(w, httperr.CodeUnauthorized, "authentication failed")
		} else {
			writeStableError(w, httperr.CodeInvalidRequest, "invalid authentication callback")
		}
		return
	}
	switch intent {
	case OAuthIntentLogin:
		if binding != "" {
			// A login callback must never complete an elevation-bound state.
			writeStableError(w, httperr.CodeInvalidRequest, "invalid authentication callback")
			return
		}
		a.finishLoginCallback(w, r, code)
	case OAuthIntentElevate:
		a.finishElevationCallback(w, r, code, binding)
	default:
		writeStableError(w, httperr.CodeInvalidRequest, "invalid authentication callback")
	}
}

// finishLoginCallback completes a normal Discord login: exchange, gate,
// registration/profile refresh, and session minting.
func (a *UserAuth) finishLoginCallback(w http.ResponseWriter, r *http.Request, code string) {
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
		open, err := a.store.RegistrationOpen()
		if err != nil {
			writeStableError(w, httperr.CodeServiceUnavailable, "authentication service unavailable")
			return
		}
		if !open {
			noStoreRedirect(w, r, a.registrationClosedPath)
			return
		}
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
		// Refresh the server-nickname and server-avatar snapshot on login. The
		// guild member is fetched only when a registration guild is configured;
		// a definitive non-membership (404) returns an empty member and clears
		// the snapshot so the chip falls back to the global profile, while a
		// transport or server error keeps the existing snapshot.
		guildNick, guildAvatarURL := user.GuildNick, user.GuildAvatarURL
		if login.GuildMember != nil {
			if gate, gerr := a.registrationGate(r.Context()); gerr == nil && validateBoundedText(gate.GuildID, 128, false) {
				if member, merr := login.GuildMember(r.Context(), gate.GuildID); merr == nil {
					guildNick = member.Nick
					guildAvatarURL = discordGuildAvatarURL(gate.GuildID, identity.ID, member.Avatar)
				}
			}
		}
		if err := a.store.UpdateUserProfile(user.ID, identity.Username, identity.Avatar, guildNick, guildAvatarURL); err != nil {
			writeStableError(w, httperr.CodeInternal, "authentication failed")
			return
		}
		user.Username = identity.Username
		user.Avatar = identity.Avatar
		user.GuildNick = guildNick
		user.GuildAvatarURL = guildAvatarURL
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
	var member GuildMember
	var matches bool
	if login.GuildMember != nil {
		member, err = login.GuildMember(ctx, gate.GuildID)
		if err != nil {
			return nil, ErrProviderUnavailable
		}
		matches = containsString(member.Roles, gate.RoleID)
	} else {
		if !validateMembershipMetadata(identity) {
			return nil, ErrInvalidIdentity
		}
		matches = identity.GuildID == gate.GuildID && containsString(identity.RoleIDs, gate.RoleID)
	}
	if !matches {
		return nil, ErrGuildRoleMismatch
	}
	guildAvatarURL := discordGuildAvatarURL(gate.GuildID, identity.ID, member.Avatar)
	user, _, err := a.store.FindOrCreateDiscordUser(identity.ID, identity.Username, identity.Avatar, member.Nick, guildAvatarURL)
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

// PatchMe handles PATCH /api/me: a session-only self-service profile
// update limited to lang. endpoint_limit, rpm_limit, admin/ban state,
// usage, and the body user id are never accepted (unknown fields are
// rejected by the strict decoder). The response is the same no-store user
// envelope as GET /api/me. Per-user RPM limits are administrator-set only;
// alpha.1 removed the self-service rpm_limit field after trial-run feedback
// that exposing per-minute rate configuration to end users was misleading.
func (a *UserAuth) PatchMe(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPatch) || !requireStation(w, r, host.StationUser) {
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
	var body struct {
		Lang *string `json:"lang"`
	}
	if !decodeJSONBody(w, r, &body) {
		return
	}
	if body.Lang == nil {
		writeStableError(w, httperr.CodeInvalidRequest, "invalid request")
		return
	}
	patch := db.UserLimitPatch{}
	if *body.Lang != "zh" && *body.Lang != "en" {
		writeStableError(w, httperr.CodeInvalidRequest, "invalid request")
		return
	}
	patch.LangSet = true
	patch.Lang = *body.Lang
	updated, err := a.store.UpdateUserLimits(user.ID, patch)
	if err != nil {
		switch {
		case errors.Is(err, db.ErrNotFound), errors.Is(err, db.ErrAdminProtected):
			// The session raced an account deletion; never distinguish it.
			writeStableError(w, httperr.CodeUnauthorized, "authentication required")
		case errors.Is(err, db.ErrConflict):
			writeStableError(w, httperr.CodeInvalidRequest, "invalid request")
		default:
			writeStableError(w, httperr.CodeInternal, "profile update unavailable")
		}
		return
	}
	httperr.WriteJSON(w, http.StatusOK, userEnvelope{User: publicUser(updated)})
}

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

// Elevate handles POST /api/auth/elevate: the first step of the two-step
// elevation. It requires an active user session, issues a short-lived,
// single-use OAuth state bound to that exact session, and returns the Discord
// authorization URL. The browser carries the state cookie; the callback mints
// the elevated capability only after the same identity re-authorizes. No
// capability exists until the callback completes, so a leaked state can only
// start (not finish) the flow.
func (a *UserAuth) Elevate(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) || !requireStation(w, r, host.StationUser) {
		return
	}
	if a == nil || a.store == nil || a.provider == nil || a.state == nil || a.elevation == nil {
		writeStableError(w, httperr.CodeServiceUnavailable, "authentication service unavailable")
		return
	}
	if _, ok := a.authenticateUserRequest(r); !ok {
		writeStableError(w, httperr.CodeUnauthorized, "authentication required")
		return
	}
	sessionToken := UserSessionToken(r)
	if sessionToken == "" {
		writeStableError(w, httperr.CodeUnauthorized, "authentication required")
		return
	}
	if !a.admitOAuthStart(w, r) {
		return
	}
	binding := db.SessionHash(sessionToken)
	state, err := a.state.IssueBound(StationUser, OAuthIntentElevate, binding)
	if err != nil {
		writeStableError(w, httperr.CodeServiceUnavailable, "authentication service unavailable")
		return
	}
	location, err := a.provider.AuthorizationURL(r.Context(), DiscordAuthorizeRequest{
		ClientID: a.clientID, RedirectURI: a.redirectURI, State: state, Intent: OAuthIntentElevate,
	})
	if err != nil || !validAuthorizationLocation(location, state) {
		writeStableError(w, httperr.CodeServiceUnavailable, "authentication service unavailable")
		return
	}
	SetOAuthStateCookie(w, state, secureCookieForRequest(r, a.siteBaseURL), a.state.ttl)
	httperr.WriteJSON(w, http.StatusOK, map[string]string{"authorization_url": location})
}

// finishElevationCallback completes the second factor: the OAuth state was
// already consumed and the provider code exchanged. The re-authorized Discord
// identity must be the same account that holds the presented session; only
// then is a short-lived, single-use elevated capability minted and handed to
// the browser via the elevated cookie. The Discord access token stays inside
// the provider stack and never reaches a response, log, error, or URL.
func (a *UserAuth) finishElevationCallback(w http.ResponseWriter, r *http.Request, code, binding string) {
	sessionToken := UserSessionToken(r)
	if sessionToken == "" || binding != db.SessionHash(sessionToken) {
		writeStableError(w, httperr.CodeUnauthorized, "authentication failed")
		return
	}
	user, err := a.store.AuthenticateUserSession(sessionToken)
	if err != nil || user == nil {
		writeStableError(w, httperr.CodeUnauthorized, "authentication failed")
		return
	}
	login, err := a.provider.Exchange(r.Context(), code, a.redirectURI)
	if err != nil {
		writeAuthFailure(w, err)
		return
	}
	identity := login.Identity
	if !validateBoundedText(identity.ID, 128, false) {
		writeStableError(w, httperr.CodeServiceUnavailable, "authentication service unavailable")
		return
	}
	// The second factor is only meaningful when the re-authorized identity is
	// the same account that owns the session. A different Discord account is a
	// stable forbidden failure, never a session change.
	if user.DiscordID != identity.ID {
		writeStableError(w, httperr.CodeForbidden, "identity does not match the session")
		return
	}
	token, _, err := a.elevation.IssueBound(user.ID, elevation.KindUser, binding)
	if err != nil {
		writeStableError(w, httperr.CodeServiceUnavailable, "authentication service unavailable")
		return
	}
	SetElevatedCookie(w, token, secureCookieForRequest(r, a.siteBaseURL), a.elevation.TTL())
	noStoreRedirect(w, r, a.userRedirectPath)
}

// ConsumeElevated atomically validates and consumes the X-Elevated-Token
// capability bound to the session presented in the request. Any failure
// (missing header, malformed/expired/replayed token, session or user binding
// mismatch) collapses to ErrElevationRequired so the wire never distinguishes
// which check failed. The raw token never enters an error value or log.
func (a *UserAuth) ConsumeElevated(w http.ResponseWriter, r *http.Request, user *db.User) error {
	if a == nil || a.elevation == nil || r == nil || user == nil || user.ID <= 0 {
		return ErrElevationRequired
	}
	token := r.Header.Get("X-Elevated-Token")
	if token == "" {
		return ErrElevationRequired
	}
	sessionToken := UserSessionToken(r)
	if sessionToken == "" {
		return ErrElevationRequired
	}
	if err := a.elevation.ConsumeBound(token, user.ID, elevation.KindUser, db.SessionHash(sessionToken)); err != nil {
		return ErrElevationRequired
	}
	a.ClearElevatedCookie(w, r)
	return nil
}

// ElevationManager returns the shared capability manager so lifecycle export
// and deletion can use the exact same one-use/session-bound token domain.
func (a *UserAuth) ElevationManager() *elevation.Manager {
	if a == nil {
		return nil
	}
	return a.elevation
}

// ClearElevatedCookie removes the browser-side elevated capability cookie.
// It is exposed so account self-service handlers can drop the single-use
// token after a consume attempt.
func (a *UserAuth) ClearElevatedCookie(w http.ResponseWriter, r *http.Request) {
	if w == nil || r == nil {
		return
	}
	ClearElevatedCookie(w, secureCookieForRequest(r, a.siteBaseURL))
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

// Close releases an internally-created ephemeral OAuth state signer and the
// elevated-capability store. A caller-supplied StateManager remains owned by
// that caller.
func (a *UserAuth) Close() error {
	if a == nil {
		return nil
	}
	if a.ownsElevation && a.elevation != nil {
		_ = a.elevation.Close()
	}
	if !a.ownsState || a.state == nil {
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
	mux.HandleFunc("POST /api/auth/elevate", a.Elevate)
	mux.HandleFunc("GET /api/session", a.Session)
	mux.HandleFunc("GET /api/me", a.Me)
	mux.HandleFunc("PATCH /api/me", a.PatchMe)
	mux.HandleFunc("POST /api/auth/logout", a.Logout)
	mux.HandleFunc("GET /api/caller-key", a.UserCallerKeyMetadata)
	mux.HandleFunc("POST /api/caller-key/regenerate", a.RegenerateCallerKey)
	return httpmw.API(mux)
}
