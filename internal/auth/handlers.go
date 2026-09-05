package auth

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/elevation"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/httpmw"
	"github.com/waiting-here/NonbiriAPI/internal/ratelimit"
)

type routeSpec struct {
	path   string
	detail bool
}

var userReturnRoutes = map[string]routeSpec{
	"home":      {path: "/"},
	"endpoints": {path: "/endpoints"}, "endpoint-detail": {path: "/endpoints/%s", detail: true},
	"caller-key": {path: "/keys"}, "models": {path: "/models"},
	"charity": {path: "/charity"}, "donation-detail": {path: "/charity/donations/%s", detail: true},
	"activities": {path: "/activities"}, "games": {path: "/games"}, "game-fishing": {path: "/games/fishing"}, "game-linklink": {path: "/games/linklink"}, "game-rps": {path: "/games/rps"},
	"debug": {path: "/debug"}, "logs": {path: "/logs"}, "issues": {path: "/issues"}, "credential-report": {path: "/report"},
	"credits":       {path: "/credits"},
	"announcements": {path: "/announcements"}, "announcement-detail": {path: "/announcements/%s", detail: true},
	"account": {path: "/account"}, "steward": {path: "/steward"}, "privacy": {path: "/privacy"}, "terms": {path: "/terms"},
	"maintenance": {path: "/maintenance"}, "registration-closed": {path: "/registration-closed"},
}

func parseReturnIntent(req *http.Request) (string, string, error) {
	if req == nil || req.URL == nil || req.URL.ForceQuery || len(req.URL.RawQuery) > maxReturnQueryBytes {
		return "", "", ErrStateInvalid
	}
	q, err := url.ParseQuery(req.URL.RawQuery)
	if err != nil {
		return "", "", ErrStateInvalid
	}
	if len(q) == 0 {
		return "home", "", nil
	}
	for key, values := range q {
		if key != "route_id" && key != "resource_id" {
			return "", "", ErrStateInvalid
		}
		if len(values) != 1 {
			return "", "", ErrStateInvalid
		}
	}
	routes, ok := q["route_id"]
	if !ok || len(routes) != 1 {
		return "", "", ErrStateInvalid
	}
	route := routes[0]
	spec, ok := userReturnRoutes[route]
	if !ok {
		return "", "", ErrStateInvalid
	}
	resources, hasResource := q["resource_id"]
	if spec.detail {
		if !hasResource || len(resources) != 1 || !validateResourceID(resources[0]) {
			return "", "", ErrStateInvalid
		}
		return route, resources[0], nil
	}
	if hasResource {
		return "", "", ErrStateInvalid
	}
	return route, "", nil
}

func returnPath(routeID, resourceID string) (string, error) {
	spec, ok := userReturnRoutes[routeID]
	if !ok {
		return "", ErrStateInvalid
	}
	if spec.detail {
		if !validateResourceID(resourceID) {
			return "", ErrStateInvalid
		}
		return strings.Replace(spec.path, "%s", url.PathEscape(resourceID), 1), nil
	}
	if resourceID != "" {
		return "", ErrStateInvalid
	}
	return spec.path, nil
}

func exactCallbackQuery(req *http.Request) (string, string, bool) {
	if req == nil || req.URL == nil || len(req.URL.RawQuery) > maxCallbackQueryBytes {
		return "", "", false
	}
	q, err := url.ParseQuery(req.URL.RawQuery)
	if err != nil {
		return "", "", false
	}
	if len(q) != 2 {
		return "", "", false
	}
	codes, okCode := q["code"]
	states, okState := q["state"]
	if !okCode || !okState || len(codes) != 1 || len(states) != 1 || !validateOAuthCode(codes[0]) || !validateOAuthStateText(states[0]) {
		return "", "", false
	}
	return codes[0], states[0], true
}

func (r *Runtime) oauthStart(w http.ResponseWriter, req *http.Request) {
	if !requireEmptyBody(w, req) {
		return
	}
	routeID, resourceID, err := parseReturnIntent(req)
	if err != nil {
		writeStableError(w, httperr.CodeInvalidRequest, "invalid request")
		return
	}
	if !r.allowOAuthStart(w, req) {
		return
	}
	state, err := r.states.IssueForRoute(StationUser, OAuthIntentLogin, routeID, resourceID)
	if err != nil {
		writeAuthFailure(w, err)
		return
	}
	authorize, err := r.provider.AuthorizationURL(req.Context(), DiscordAuthorizeRequest{ClientID: r.clientID, RedirectURI: r.redirectURI, State: state, Intent: OAuthIntentLogin})
	if err != nil || !validAuthorizationLocation(authorize, state) {
		writeStableError(w, httperr.CodeServiceUnavailable, "authentication service unavailable")
		return
	}
	setOAuthStateCookie(w, state, secureCookieForRequest(req, r.siteOrigin), r.states.TTL(), r.now())
	noStoreRedirect(w, req, authorize)
}

func (r *Runtime) oauthCallback(w http.ResponseWriter, req *http.Request) {
	clearOAuthStateCookie(w, secureCookieForRequest(req, r.siteOrigin))
	if !requireEmptyBody(w, req) {
		return
	}
	code, state, ok := exactCallbackQuery(req)
	if !ok {
		writeStableError(w, httperr.CodeInvalidRequest, "invalid request")
		return
	}
	cookie, ok := cookieValue(req, OAuthStateCookieName)
	if !ok {
		writeAuthFailure(w, ErrStateInvalid)
		return
	}
	claims, err := r.states.ConsumeClaims(state, cookie, StationUser)
	if err != nil {
		writeAuthFailure(w, err)
		return
	}
	target, err := returnPath(claims.RouteID, claims.ResourceID)
	if err != nil {
		writeAuthFailure(w, ErrStateInvalid)
		return
	}
	login, err := r.provider.Exchange(req.Context(), code, r.redirectURI)
	if err != nil {
		writeAuthFailure(w, err)
		return
	}
	if !validDiscordIdentity(login.Identity) {
		writeAuthFailure(w, ErrInvalidIdentity)
		return
	}
	switch claims.Intent {
	case OAuthIntentLogin:
		r.completeLogin(w, req, login, target)
	case OAuthIntentElevate:
		r.completeUserElevation(w, req, login, claims, target)
	default:
		writeAuthFailure(w, ErrStateInvalid)
	}
}

func validDiscordIdentity(identity DiscordIdentity) bool {
	return validateBoundedText(identity.ID, 128, false) && validateBoundedText(identity.Username, maxUsernameBytes, false) && validateBoundedText(identity.Avatar, 1024, true)
}

func (r *Runtime) registrationConfig(ctx context.Context) (map[string]string, error) {
	values := make(map[string]string, 2)
	for _, key := range []string{"discord_guild_id", "discord_role_id"} {
		var value string
		if err := r.db.QueryRowContext(ctx, `SELECT value FROM site_config WHERE key=?`, key).Scan(&value); err != nil || !validateBoundedText(value, 128, false) {
			return nil, ErrProviderUnavailable
		}
		values[key] = value
	}
	return values, nil
}

func (r *Runtime) registrationOpen(ctx context.Context) (bool, error) {
	var value string
	if err := r.db.QueryRowContext(ctx, `SELECT value FROM site_config WHERE key='registration_open'`).Scan(&value); err != nil {
		return false, ErrProviderUnavailable
	}
	switch value {
	case "0":
		return false, nil
	case "1":
		return true, nil
	default:
		return false, ErrProviderUnavailable
	}
}

func (r *Runtime) registrationGuild(ctx context.Context) (string, error) {
	var guild string
	if err := r.db.QueryRowContext(ctx, `SELECT value FROM site_config WHERE key='discord_guild_id'`).Scan(&guild); err != nil || !validateBoundedText(guild, 128, false) {
		return "", ErrProviderUnavailable
	}
	return guild, nil
}

func memberFor(ctx context.Context, login DiscordLogin, guild string) (GuildMember, error) {
	if guild == "" {
		return GuildMember{}, nil
	}
	if login.GuildMember == nil {
		return GuildMember{}, ErrProviderUnavailable
	}
	member, err := login.GuildMember(ctx, guild)
	if err != nil {
		return GuildMember{}, err
	}
	if !validateBoundedText(member.Nick, maxUsernameBytes, true) || !validateBoundedText(member.Avatar, 1024, true) || len(member.Roles) > maxDiscordRoleIDs {
		return GuildMember{}, ErrInvalidIdentity
	}
	for _, role := range member.Roles {
		if !validateBoundedText(role, 128, false) {
			return GuildMember{}, ErrInvalidIdentity
		}
	}
	return member, nil
}

func (r *Runtime) completeLogin(w http.ResponseWriter, req *http.Request, login DiscordLogin, target string) {
	userID, exists, err := r.findDiscordUser(req.Context(), login.Identity.ID)
	if err != nil {
		writeAuthFailure(w, err)
		return
	}
	if exists {
		var member *GuildMember
		if guild, guildErr := r.registrationGuild(req.Context()); guildErr == nil {
			resolved, memberErr := memberFor(req.Context(), login, guild)
			if memberErr == nil {
				resolved.Avatar = dereference(discordGuildAvatarURL(guild, login.Identity.ID, resolved.Avatar))
				member = &resolved
			}
		}
		token, expiry, err := r.refreshExistingUser(req.Context(), userID, login.Identity, member)
		if err != nil {
			r.writeSessionFailure(w, err)
			return
		}
		setUserSessionCookie(w, token, timeFromUnix(expiry), r.now(), secureCookieForRequest(req, r.siteOrigin))
		clearElevatedCookie(w, secureCookieForRequest(req, r.siteOrigin))
		noStoreRedirect(w, req, target)
		return
	}
	open, err := r.registrationOpen(req.Context())
	if err != nil {
		writeAuthFailure(w, err)
		return
	}
	if !open {
		noStoreRedirect(w, req, "/registration-closed")
		return
	}
	values, configErr := r.registrationConfig(req.Context())
	if configErr != nil {
		writeAuthFailure(w, ErrProviderUnavailable)
		return
	}
	guild, role := values["discord_guild_id"], values["discord_role_id"]
	member, err := memberFor(req.Context(), login, guild)
	if err != nil {
		writeAuthFailure(w, err)
		return
	}
	member.Avatar = dereference(discordGuildAvatarURL(guild, login.Identity.ID, member.Avatar))
	userID, token, expiry, err := r.registerUser(req.Context(), login.Identity, member, guild, role)
	if errors.Is(err, ErrRegistrationPaused) {
		noStoreRedirect(w, req, "/registration-closed")
		return
	}
	if errors.Is(err, errIdentityConflict) && userID > 0 {
		token, expiry, err = r.refreshExistingUser(req.Context(), userID, login.Identity, &member)
	}
	if err != nil {
		writeAuthFailure(w, err)
		return
	}
	setUserSessionCookie(w, token, timeFromUnix(expiry), r.now(), secureCookieForRequest(req, r.siteOrigin))
	clearElevatedCookie(w, secureCookieForRequest(req, r.siteOrigin))
	noStoreRedirect(w, req, target)
}

func (r *Runtime) completeUserElevation(w http.ResponseWriter, req *http.Request, login DiscordLogin, claims StateClaims, target string) {
	raw, ok := cookieValue(req, UserSessionCookieName)
	if !ok {
		writeAuthFailure(w, ErrStateInvalid)
		return
	}
	principal, err := r.authenticate(req.Context(), raw, authz.ActorUserSession, "")
	if err != nil || !hmacStringEqual(principal.actor.SessionTokenHash, claims.Binding) {
		writeAuthFailure(w, ErrStateInvalid)
		return
	}
	var discordID string
	err = r.db.QueryRowContext(req.Context(), `SELECT discord_id FROM users WHERE id=? AND is_admin=0`, principal.actor.UserID).Scan(&discordID)
	if err != nil || !hmacStringEqual(discordID, login.Identity.ID) {
		writeStableError(w, httperr.CodeForbidden, "identity does not match the session")
		return
	}
	token, expiry, err := r.elevation.IssueBound(principal.actor.UserID, elevation.KindUser, principal.actor.SessionTokenHash)
	if err != nil {
		writeStableError(w, httperr.CodeServiceUnavailable, "elevation unavailable")
		return
	}
	setUserSessionCookie(w, raw, timeFromUnix(principal.expiresAt), r.now(), secureCookieForRequest(req, r.siteOrigin))
	setElevatedCookie(w, token, secureCookieForRequest(req, r.siteOrigin), expiry, r.now())
	noStoreRedirect(w, req, target)
}

func hmacStringEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func dereference(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
func timeFromUnix(v int64) time.Time { return time.Unix(v, 0) }

func (r *Runtime) userSession(w http.ResponseWriter, req *http.Request) {
	if !requireEmptyQuery(w, req) || !requireEmptyBody(w, req) {
		return
	}
	actor, ok := ActorFromContext(req.Context())
	if !ok {
		writeStableError(w, httperr.CodeUnauthorized, "authentication required")
		return
	}
	envelope, err := r.readUserEnvelope(req.Context(), actor.UserID)
	if err != nil {
		writeStableError(w, httperr.CodeInternal, "user unavailable")
		return
	}
	httpmwWriteJSON(w, http.StatusOK, envelope)
}
func (r *Runtime) userMe(w http.ResponseWriter, req *http.Request) { r.userSession(w, req) }
func (r *Runtime) userUsage(w http.ResponseWriter, req *http.Request) {
	if !requireEmptyQuery(w, req) || !requireEmptyBody(w, req) {
		return
	}
	actor, ok := ActorFromContext(req.Context())
	if !ok {
		writeStableError(w, httperr.CodeUnauthorized, "authentication required")
		return
	}
	envelope, err := r.readUserEnvelope(req.Context(), actor.UserID)
	if err != nil {
		writeStableError(w, httperr.CodeInternal, "usage unavailable")
		return
	}
	httpmwWriteJSON(w, http.StatusOK, envelope.User.Usage)
}

func (r *Runtime) userElevate(w http.ResponseWriter, req *http.Request) {
	if !requireEmptyQuery(w, req) || !requireEmptyBody(w, req) {
		return
	}
	if !r.allowOAuthStart(w, req) {
		return
	}
	actor, ok := ActorFromContext(req.Context())
	if !ok {
		writeStableError(w, httperr.CodeUnauthorized, "authentication required")
		return
	}
	state, err := r.states.IssueBoundForRoute(StationUser, OAuthIntentElevate, actor.SessionTokenHash, "account", "")
	if err != nil {
		writeAuthFailure(w, err)
		return
	}
	authorize, err := r.provider.AuthorizationURL(req.Context(), DiscordAuthorizeRequest{ClientID: r.clientID, RedirectURI: r.redirectURI, State: state, Intent: OAuthIntentElevate})
	if err != nil || !validAuthorizationLocation(authorize, state) {
		writeStableError(w, httperr.CodeServiceUnavailable, "authentication service unavailable")
		return
	}
	setOAuthStateCookie(w, state, secureCookieForRequest(req, r.siteOrigin), r.states.TTL(), r.now())
	httpmwWriteJSON(w, http.StatusOK, AuthorizationURLResponse{AuthorizationURL: authorize})
}

func validAuthorizationLocation(location, state string) bool {
	if !validateBoundedText(location, 4096, false) || !validateOAuthStateText(state) {
		return false
	}
	parsed, err := url.Parse(location)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" || (!strings.EqualFold(parsed.Scheme, "https") && !strings.EqualFold(parsed.Scheme, "http")) {
		return false
	}
	values, err := url.ParseQuery(parsed.RawQuery)
	if err != nil {
		return false
	}
	states, ok := values["state"]
	return ok && len(states) == 1 && states[0] == state
}

func (r *Runtime) userLogout(w http.ResponseWriter, req *http.Request) {
	if !requireEmptyQuery(w, req) || !requireEmptyBody(w, req) {
		return
	}
	clearElevatedCookie(w, secureCookieForRequest(req, r.siteOrigin))
	if raw, ok := cookieValue(req, UserSessionCookieName); ok {
		deleted, err := r.deleteSession(req.Context(), raw)
		if err != nil {
			writeStableError(w, httperr.CodeInternal, "logout failed")
			return
		}
		if deleted {
			if actor, present := ActorFromContext(req.Context()); present && actor.Kind == authz.ActorUserSession {
				r.notifyUserSessionInvalidated(actor.UserID)
			}
		}
	}
	clearUserSessionCookie(w, secureCookieForRequest(req, r.siteOrigin))
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

type adminLoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}
type adminElevationRequest struct {
	Password string `json:"password"`
}

func (r *Runtime) adminLogin(w http.ResponseWriter, req *http.Request) {
	if !requireEmptyQuery(w, req) {
		return
	}
	var body adminLoginRequest
	if !decodeJSONBody(w, req, &body, true) {
		return
	}
	if !validateBoundedText(body.Username, maxUsernameBytes, false) || !validateBoundedText(body.Password, maxPasswordBytes, false) {
		writeStableError(w, httperr.CodeInvalidRequest, "invalid request")
		return
	}
	identity := httpmw.ClientIP(req)
	decision, err := r.adminThrottle.Check(identity, r.adminUsername)
	if err != nil {
		writeStableError(w, httperr.CodeServiceUnavailable, "authentication service unavailable")
		return
	}
	if !decision.Allowed {
		setRetryAfter(w, decision.RetryAfterSeconds)
		writeStableError(w, httperr.CodeRateLimited, "too many requests")
		return
	}
	if body.Username != r.adminUsername || !r.passwordMatches(body.Password) {
		decision, err = r.adminThrottle.Failure(identity, r.adminUsername)
		if err != nil || decision.Reason == ratelimit.LoginCapacity {
			writeStableError(w, httperr.CodeServiceUnavailable, "authentication service unavailable")
			return
		}
		if decision.Locked {
			setRetryAfter(w, decision.RetryAfterSeconds)
			writeStableError(w, httperr.CodeRateLimited, "too many requests")
			return
		}
		writeStableError(w, httperr.CodeUnauthorized, "invalid credentials")
		return
	}
	token, expiry, err := r.ensureAdminAndSession(req.Context())
	if err != nil {
		writeStableError(w, httperr.CodeServiceUnavailable, "authentication service unavailable")
		return
	}
	if err := r.adminThrottle.Success(identity, r.adminUsername); err != nil {
		writeStableError(w, httperr.CodeServiceUnavailable, "authentication service unavailable")
		return
	}
	setAdminSessionCookie(w, token, timeFromUnix(expiry), r.now(), secureCookieForRequest(req, r.siteOrigin))
	httpmwWriteJSON(w, http.StatusOK, AdminEnvelope{Admin: AdminResponse{Username: r.adminUsername}})
}

func (r *Runtime) adminSession(w http.ResponseWriter, req *http.Request) {
	if !requireEmptyQuery(w, req) || !requireEmptyBody(w, req) {
		return
	}
	httpmwWriteJSON(w, http.StatusOK, AdminEnvelope{Admin: AdminResponse{Username: r.adminUsername}})
}
func (r *Runtime) adminLogout(w http.ResponseWriter, req *http.Request) {
	if !requireEmptyQuery(w, req) || !requireEmptyBody(w, req) {
		return
	}
	if raw, ok := cookieValue(req, AdminSessionCookieName); ok {
		if _, err := r.deleteSession(req.Context(), raw); err != nil {
			writeStableError(w, httperr.CodeInternal, "logout failed")
			return
		}
	}
	clearAdminSessionCookie(w, secureCookieForRequest(req, r.siteOrigin))
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

func (r *Runtime) adminElevate(w http.ResponseWriter, req *http.Request) {
	if !requireEmptyQuery(w, req) {
		return
	}
	var body adminElevationRequest
	if !decodeJSONBody(w, req, &body, true) {
		return
	}
	if !validateBoundedText(body.Password, maxPasswordBytes, false) {
		writeStableError(w, httperr.CodeInvalidRequest, "invalid request")
		return
	}
	actor, ok := ActorFromContext(req.Context())
	if !ok || actor.Kind != authz.ActorAdminSession {
		writeStableError(w, httperr.CodeUnauthorized, "authentication required")
		return
	}
	identity := httpmw.ClientIP(req)
	decision, err := r.adminThrottle.Check(identity, r.adminUsername)
	if err != nil {
		writeStableError(w, httperr.CodeServiceUnavailable, "authentication service unavailable")
		return
	}
	if !decision.Allowed {
		setRetryAfter(w, decision.RetryAfterSeconds)
		writeStableError(w, httperr.CodeRateLimited, "too many requests")
		return
	}
	if !r.passwordMatches(body.Password) {
		decision, err = r.adminThrottle.Failure(identity, r.adminUsername)
		if err != nil || decision.Reason == ratelimit.LoginCapacity {
			writeStableError(w, httperr.CodeServiceUnavailable, "authentication service unavailable")
			return
		}
		if decision.Locked {
			setRetryAfter(w, decision.RetryAfterSeconds)
			writeStableError(w, httperr.CodeRateLimited, "too many requests")
			return
		}
		writeStableError(w, httperr.CodeForbidden, "invalid credentials")
		return
	}
	if err := r.adminThrottle.Success(identity, r.adminUsername); err != nil {
		writeStableError(w, httperr.CodeServiceUnavailable, "authentication service unavailable")
		return
	}
	token, expiry, err := r.elevation.IssueBound(actor.UserID, elevation.KindAdmin, actor.SessionTokenHash)
	if err != nil {
		writeStableError(w, httperr.CodeServiceUnavailable, "elevation unavailable")
		return
	}
	httpmwWriteJSON(w, http.StatusOK, ElevationResponse{Token: token, ExpiresAt: expiry.Unix()})
}

func httpmwWriteJSON(w http.ResponseWriter, status int, value any) {
	data, err := json.Marshal(value)
	if err != nil {
		writeStableError(w, httperr.CodeInternal, "response unavailable")
		return
	}
	writeJSONBytes(w, status, append(data, '\n'))
}
