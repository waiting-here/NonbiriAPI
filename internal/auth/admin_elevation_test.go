package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/elevation"
	"github.com/waiting-here/NonbiriAPI/internal/host"
	"github.com/waiting-here/NonbiriAPI/internal/ratelimit"
)

type throttleCall struct {
	operation string
	identity  string
	username  string
}

type recordingLoginThrottle struct {
	calls         []throttleCall
	checkDecision ratelimit.LoginDecision
}

func (t *recordingLoginThrottle) Check(identity, username string) (ratelimit.LoginDecision, error) {
	t.calls = append(t.calls, throttleCall{operation: "check", identity: identity, username: username})
	return t.checkDecision, nil
}

func (t *recordingLoginThrottle) Failure(identity, username string) (ratelimit.LoginDecision, error) {
	t.calls = append(t.calls, throttleCall{operation: "failure", identity: identity, username: username})
	return ratelimit.LoginDecision{Allowed: true, Reason: ratelimit.LoginAllowed}, nil
}

func (t *recordingLoginThrottle) Success(identity, username string) error {
	t.calls = append(t.calls, throttleCall{operation: "success", identity: identity, username: username})
	return nil
}

func TestAdminAuthenticationMaintenanceAndLogoutSessionRequirements(t *testing.T) {
	f := newRuntimeFixture(t, nil)
	f.gate.enabled = true
	login := request(t, f.runtime.AdminHandler(), host.StationAdmin, http.MethodPost, "https://admin.example/admin/api/login", `{"username":"operator","password":"correct horse battery staple"}`, nil, map[string]string{"Content-Type": "application/json"})
	if login.Code != http.StatusOK {
		t.Fatalf("login status=%d body=%s", login.Code, login.Body.String())
	}
	cookie := responseCookie(t, login, AdminSessionCookieName)
	if cookie.Path != "/admin" || !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("admin cookie=%+v", cookie)
	}
	session := request(t, f.runtime.AdminHandler(), host.StationAdmin, http.MethodGet, "https://admin.example/admin/api/session", "", []*http.Cookie{cookie}, nil)
	if session.Code != http.StatusOK || session.Body.String() != `{"admin":{"username":"operator"}}`+"\n" {
		t.Fatalf("session=%d %s", session.Code, session.Body.String())
	}
	var adminID, wallets, keys int
	if err := f.store.DB().QueryRow(`SELECT id FROM users WHERE is_admin=1 AND discord_id IS NULL AND username='operator'`).Scan(&adminID); err != nil {
		t.Fatal(err)
	}
	if err := f.store.DB().QueryRow(`SELECT COUNT(*) FROM credit_accounts WHERE user_id=?`, adminID).Scan(&wallets); err != nil {
		t.Fatal(err)
	}
	if err := f.store.DB().QueryRow(`SELECT COUNT(*) FROM caller_keys WHERE user_id=?`, adminID).Scan(&keys); err != nil {
		t.Fatal(err)
	}
	if wallets != 0 || keys != 0 {
		t.Fatalf("admin wallet=%d caller_keys=%d", wallets, keys)
	}
	missing := request(t, f.runtime.AdminHandler(), host.StationAdmin, http.MethodPost, "https://admin.example/admin/api/logout", "", nil, nil)
	if missing.Code != http.StatusUnauthorized {
		t.Fatalf("missing logout=%d %s", missing.Code, missing.Body.String())
	}
	wrongStation := request(t, f.runtime.AdminHandler(), host.StationUser, http.MethodPost, "https://admin.example/admin/api/logout", "", []*http.Cookie{cookie}, nil)
	if wrongStation.Code != http.StatusForbidden {
		t.Fatalf("wrong station=%d %s", wrongStation.Code, wrongStation.Body.String())
	}
	logout := request(t, f.runtime.AdminHandler(), host.StationAdmin, http.MethodPost, "https://admin.example/admin/api/logout", "", []*http.Cookie{cookie}, nil)
	if logout.Code != http.StatusNoContent {
		t.Fatalf("logout=%d %s", logout.Code, logout.Body.String())
	}
	after := request(t, f.runtime.AdminHandler(), host.StationAdmin, http.MethodGet, "https://admin.example/admin/api/session", "", []*http.Cookie{cookie}, nil)
	if after.Code != http.StatusUnauthorized {
		t.Fatalf("after logout=%d %s", after.Code, after.Body.String())
	}
	expiredLogin := request(t, f.runtime.AdminHandler(), host.StationAdmin, http.MethodPost, "https://admin.example/admin/api/login", `{"username":"operator","password":"correct horse battery staple"}`, nil, map[string]string{"Content-Type": "application/json"})
	expiredCookie := responseCookie(t, expiredLogin, AdminSessionCookieName)
	if _, err := f.store.DB().Exec(`UPDATE sessions SET expires_at=last_seen_at WHERE token_hash=?`, sessionLookupHash(expiredCookie.Value)); err != nil {
		t.Fatal(err)
	}
	expiredLogout := request(t, f.runtime.AdminHandler(), host.StationAdmin, http.MethodPost, "https://admin.example/admin/api/logout", "", []*http.Cookie{expiredCookie}, nil)
	if expiredLogout.Code != http.StatusUnauthorized {
		t.Fatalf("expired logout=%d %s", expiredLogout.Code, expiredLogout.Body.String())
	}
}

func TestAdminThrottleUsesFixedSingletonAccountKey(t *testing.T) {
	throttle := &recordingLoginThrottle{checkDecision: ratelimit.LoginDecision{Allowed: true, Reason: ratelimit.LoginAllowed}}
	f := newRuntimeFixture(t, func(c *RuntimeConfig) { c.AdminLoginThrottle = throttle })
	wrong := request(t, f.runtime.AdminHandler(), host.StationAdmin, http.MethodPost, "https://admin.example/admin/api/login", `{"username":"attacker-selected","password":"wrong password"}`, nil, map[string]string{"Content-Type": "application/json"})
	if wrong.Code != http.StatusUnauthorized {
		t.Fatalf("wrong credentials=%d body=%s", wrong.Code, wrong.Body.String())
	}
	if len(throttle.calls) != 2 {
		t.Fatalf("calls=%+v", throttle.calls)
	}
	for _, call := range throttle.calls {
		if call.identity != "192.0.2.10" || call.username != "operator" {
			t.Fatalf("throttle call=%+v", call)
		}
	}

	throttle.calls = nil
	throttle.checkDecision = ratelimit.LoginDecision{Allowed: false, Locked: true, Reason: ratelimit.LoginLocked, RetryAfterSeconds: 17}
	locked := request(t, f.runtime.AdminHandler(), host.StationAdmin, http.MethodPost, "https://admin.example/admin/api/login", `{"username":"operator","password":"correct horse battery staple"}`, nil, map[string]string{"Content-Type": "application/json"})
	if locked.Code != http.StatusTooManyRequests || locked.Header().Get("Retry-After") != "17" {
		t.Fatalf("locked=%d retry=%q body=%s", locked.Code, locked.Header().Get("Retry-After"), locked.Body.String())
	}
}

func TestUserLogoutIsAuthenticatedButMaintenanceExempt(t *testing.T) {
	f := newRuntimeFixture(t, nil)
	cookie := loginUser(t, f, "logout", "")
	f.gate.enabled = true
	missing := request(t, f.runtime.UserHandler(), host.StationUser, http.MethodPost, "https://user.example/api/auth/logout", "", nil, nil)
	if missing.Code != http.StatusUnauthorized {
		t.Fatalf("missing=%d %s", missing.Code, missing.Body.String())
	}
	wrong := request(t, f.runtime.UserHandler(), host.StationAdmin, http.MethodPost, "https://user.example/api/auth/logout", "", []*http.Cookie{cookie}, nil)
	if wrong.Code != http.StatusForbidden {
		t.Fatalf("wrong station=%d %s", wrong.Code, wrong.Body.String())
	}
	valid := request(t, f.runtime.UserHandler(), host.StationUser, http.MethodPost, "https://user.example/api/auth/logout", "", []*http.Cookie{cookie}, nil)
	if valid.Code != http.StatusNoContent {
		t.Fatalf("maintenance logout=%d %s", valid.Code, valid.Body.String())
	}
	f.gate.enabled = false
	expired := loginUser(t, f, "logout", "")
	if _, err := f.store.DB().Exec(`UPDATE sessions SET expires_at=last_seen_at WHERE token_hash=?`, sessionLookupHash(expired.Value)); err != nil {
		t.Fatal(err)
	}
	expiredResult := request(t, f.runtime.UserHandler(), host.StationUser, http.MethodPost, "https://user.example/api/auth/logout", "", []*http.Cookie{expired}, nil)
	if expiredResult.Code != http.StatusUnauthorized {
		t.Fatalf("expired=%d %s", expiredResult.Code, expiredResult.Body.String())
	}
}

func TestUserElevationJSONHandoffAndBoundCallback(t *testing.T) {
	f := newRuntimeFixture(t, nil)
	cookie := loginUser(t, f, "base", "")
	addLogin(f.provider, "elevate", "discord-1")
	start := request(t, f.runtime.UserHandler(), host.StationUser, http.MethodPost, "https://user.example/api/auth/elevate", "", []*http.Cookie{cookie}, nil)
	if start.Code != http.StatusOK {
		t.Fatalf("elevate start=%d %s", start.Code, start.Body.String())
	}
	var handoff AuthorizationURLResponse
	if err := json.Unmarshal(start.Body.Bytes(), &handoff); err != nil || handoff.AuthorizationURL == "" {
		t.Fatalf("handoff=%+v err=%v body=%s", handoff, err, start.Body.String())
	}
	stateCookie := responseCookie(t, start, OAuthStateCookieName)
	authorization, err := url.Parse(handoff.AuthorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	state := authorization.Query().Get("state")
	callback := request(t, f.runtime.UserHandler(), host.StationUser, http.MethodGet, "https://user.example/api/auth/discord/callback?code=elevate&state="+url.QueryEscape(state), "", []*http.Cookie{stateCookie, cookie}, nil)
	if callback.Code != http.StatusFound || callback.Header().Get("Location") != "/account" {
		t.Fatalf("callback=%d location=%q body=%s", callback.Code, callback.Header().Get("Location"), callback.Body.String())
	}
	renewed := responseCookie(t, callback, UserSessionCookieName)
	if renewed.Value != cookie.Value {
		t.Fatalf("renewed session cookie=%+v", renewed)
	}
	elevated := responseCookie(t, callback, ElevatedCookieName)
	if elevated.HttpOnly || elevated.SameSite != http.SameSiteLaxMode || !elevated.Secure {
		t.Fatalf("elevated cookie=%+v", elevated)
	}
	if err := f.runtime.ElevationManager().ConsumeBound(elevated.Value, 1, elevation.KindUser, sessionLookupHash(cookie.Value)); err != nil {
		t.Fatalf("consume bound elevation: %v", err)
	}
	if err := f.runtime.ElevationManager().ConsumeBound(elevated.Value, 1, elevation.KindUser, sessionLookupHash(cookie.Value)); !errors.Is(err, elevation.ErrReplay) {
		t.Fatalf("replay=%v", err)
	}
}

func TestUserElevationRejectsDifferentDiscordIdentity(t *testing.T) {
	f := newRuntimeFixture(t, nil)
	session := loginUser(t, f, "identity-base", "")
	addLogin(f.provider, "identity-other", "discord-other")
	start := request(t, f.runtime.UserHandler(), host.StationUser, http.MethodPost, "https://user.example/api/auth/elevate", "", []*http.Cookie{session}, nil)
	if start.Code != http.StatusOK {
		t.Fatalf("start=%d body=%s", start.Code, start.Body.String())
	}
	var handoff AuthorizationURLResponse
	if err := json.Unmarshal(start.Body.Bytes(), &handoff); err != nil {
		t.Fatal(err)
	}
	authorization, err := url.Parse(handoff.AuthorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	callback := request(t, f.runtime.UserHandler(), host.StationUser, http.MethodGet, "https://user.example/api/auth/discord/callback?code=identity-other&state="+url.QueryEscape(authorization.Query().Get("state")), "", []*http.Cookie{responseCookie(t, start, OAuthStateCookieName), session}, nil)
	if callback.Code != http.StatusForbidden {
		t.Fatalf("callback=%d body=%s", callback.Code, callback.Body.String())
	}
	for _, cookie := range callback.Result().Cookies() {
		if cookie.Name == ElevatedCookieName && cookie.Value != "" {
			t.Fatalf("mismatched identity received elevation cookie: %+v", cookie)
		}
	}
}

func TestUserElevationCookieClearsOnReplacementLogoutAndConsumeAttempt(t *testing.T) {
	f := newRuntimeFixture(t, nil)
	session := loginUser(t, f, "cookie-base", "")
	addLogin(f.provider, "cookie-elevate", "discord-1")
	elevateStart := request(t, f.runtime.UserHandler(), host.StationUser, http.MethodPost, "https://user.example/api/auth/elevate", "", []*http.Cookie{session}, nil)
	var handoff AuthorizationURLResponse
	if err := json.Unmarshal(elevateStart.Body.Bytes(), &handoff); err != nil {
		t.Fatal(err)
	}
	authorization, err := url.Parse(handoff.AuthorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	stateCookie := responseCookie(t, elevateStart, OAuthStateCookieName)
	elevationCallback := request(t, f.runtime.UserHandler(), host.StationUser, http.MethodGet, "https://user.example/api/auth/discord/callback?code=cookie-elevate&state="+url.QueryEscape(authorization.Query().Get("state")), "", []*http.Cookie{stateCookie, session}, nil)
	elevated := responseCookie(t, elevationCallback, ElevatedCookieName)
	if elevated.Value == "" || elevated.MaxAge <= 0 {
		t.Fatalf("issued elevation cookie=%+v", elevated)
	}

	addLogin(f.provider, "cookie-replacement", "discord-1")
	loginStart := request(t, f.runtime.UserHandler(), host.StationUser, http.MethodGet, "https://user.example/api/auth/discord/start", "", []*http.Cookie{session, elevated}, nil)
	loginStateCookie := responseCookie(t, loginStart, OAuthStateCookieName)
	loginAuthorization, err := url.Parse(loginStart.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	replacement := request(t, f.runtime.UserHandler(), host.StationUser, http.MethodGet, "https://user.example/api/auth/discord/callback?code=cookie-replacement&state="+url.QueryEscape(loginAuthorization.Query().Get("state")), "", []*http.Cookie{loginStateCookie, session, elevated}, nil)
	newSession := responseCookie(t, replacement, UserSessionCookieName)
	clearedByReplacement := responseCookie(t, replacement, ElevatedCookieName)
	assertClearedElevationCookie(t, replacement, clearedByReplacement)

	logout := request(t, f.runtime.UserHandler(), host.StationUser, http.MethodPost, "https://user.example/api/auth/logout", "", []*http.Cookie{newSession, elevated}, nil)
	if logout.Code != http.StatusNoContent {
		t.Fatalf("logout=%d body=%s", logout.Code, logout.Body.String())
	}
	assertClearedElevationCookie(t, logout, responseCookie(t, logout, ElevatedCookieName))

	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://user.example/api/lifecycle", nil)
	f.runtime.ClearUserElevationCookie(recorder, req)
	assertClearedElevationCookie(t, recorder, responseCookie(t, recorder, ElevatedCookieName))
}

func assertClearedElevationCookie(t *testing.T, recorder *httptest.ResponseRecorder, cookie *http.Cookie) {
	t.Helper()
	if cookie.Value != "" || cookie.Path != "/" || cookie.MaxAge != -1 || !cookie.Secure || recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("clear cookie=%+v cache-control=%q", cookie, recorder.Header().Get("Cache-Control"))
	}
}

func TestAdminElevationRequiresPasswordAndBindsSession(t *testing.T) {
	f := newRuntimeFixture(t, nil)
	login := request(t, f.runtime.AdminHandler(), host.StationAdmin, http.MethodPost, "https://admin.example/admin/api/login", `{"username":"operator","password":"correct horse battery staple"}`, nil, map[string]string{"Content-Type": "application/json"})
	cookie := responseCookie(t, login, AdminSessionCookieName)
	bad := request(t, f.runtime.AdminHandler(), host.StationAdmin, http.MethodPost, "https://admin.example/admin/api/auth/elevate", `{"password":"wrong"}`, []*http.Cookie{cookie}, map[string]string{"Content-Type": "application/json"})
	if bad.Code != http.StatusForbidden {
		t.Fatalf("bad=%d %s", bad.Code, bad.Body.String())
	}
	good := request(t, f.runtime.AdminHandler(), host.StationAdmin, http.MethodPost, "https://admin.example/admin/api/auth/elevate", `{"password":"correct horse battery staple"}`, []*http.Cookie{cookie}, map[string]string{"Content-Type": "application/json"})
	if good.Code != http.StatusOK {
		t.Fatalf("good=%d %s", good.Code, good.Body.String())
	}
	for _, cookie := range good.Result().Cookies() {
		if cookie.Name == ElevatedCookieName {
			t.Fatalf("admin elevation unexpectedly set user elevation cookie: %+v", cookie)
		}
	}
	var response ElevationResponse
	if err := json.Unmarshal(good.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Token == "" || response.ExpiresAt != authTestNow+int64((10*time.Minute)/time.Second) {
		t.Fatalf("response=%+v", response)
	}
	var userID int64
	if err := f.store.DB().QueryRow(`SELECT id FROM users WHERE is_admin=1`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if err := f.runtime.ElevationManager().ConsumeBound(response.Token, userID, elevation.KindAdmin, sessionLookupHash(cookie.Value)); err != nil {
		t.Fatal(err)
	}
}

func TestExistingIdentityIgnoresRegistrationAndMemberRefreshFailures(t *testing.T) {
	f := newRuntimeFixture(t, nil)
	loginUser(t, f, "initial", "")

	setConfig := func(key, value string) {
		t.Helper()
		if _, err := f.store.DB().Exec(`UPDATE site_config SET value=? WHERE key=?`, value, key); err != nil {
			t.Fatalf("set %s: %v", key, err)
		}
	}
	addExistingLogin := func(code, username string, lookup func(context.Context, string) (GuildMember, error)) {
		t.Helper()
		f.provider.mu.Lock()
		f.provider.logins[code] = DiscordLogin{
			Identity:    DiscordIdentity{ID: "discord-1", Username: username, Avatar: "new-avatar"},
			GuildMember: lookup,
		}
		f.provider.mu.Unlock()
	}
	assertSnapshot := func(username, guildNick string, guildAvatarPresent bool) {
		t.Helper()
		var gotUsername, gotGuildNick, gotGuildAvatar string
		if err := f.store.DB().QueryRow(`SELECT username,guild_nick,guild_avatar_url FROM users WHERE discord_id='discord-1'`).Scan(&gotUsername, &gotGuildNick, &gotGuildAvatar); err != nil {
			t.Fatal(err)
		}
		if gotUsername != username || gotGuildNick != guildNick || (gotGuildAvatar != "") != guildAvatarPresent {
			t.Fatalf("profile username=%q guild_nick=%q guild_avatar=%q", gotUsername, gotGuildNick, gotGuildAvatar)
		}
	}

	setConfig("registration_open", "0")
	addExistingLogin("closed-registration", "Alice Closed", func(context.Context, string) (GuildMember, error) {
		return GuildMember{Nick: "Closed Gate Nick", Avatar: "closed-avatar", Roles: []string{"role-1"}}, nil
	})
	loginUser(t, f, "closed-registration", "")
	assertSnapshot("Alice Closed", "Closed Gate Nick", true)

	setConfig("registration_open", "invalid")
	addExistingLogin("invalid-registration", "Alice Invalid Registration", func(context.Context, string) (GuildMember, error) {
		return GuildMember{Nick: "Invalid Registration Nick", Avatar: "invalid-registration-avatar"}, nil
	})
	loginUser(t, f, "invalid-registration", "")
	assertSnapshot("Alice Invalid Registration", "Invalid Registration Nick", true)

	setConfig("registration_open", "1")
	setConfig("discord_role_id", "bad\nrole")
	addExistingLogin("invalid-role", "Alice Invalid Role", func(context.Context, string) (GuildMember, error) {
		return GuildMember{Nick: "Invalid Role Nick", Avatar: "invalid-role-avatar"}, nil
	})
	loginUser(t, f, "invalid-role", "")
	assertSnapshot("Alice Invalid Role", "Invalid Role Nick", true)

	setConfig("discord_role_id", "role-1")
	setConfig("discord_guild_id", "bad\nguild")
	addExistingLogin("invalid-guild", "Alice Invalid Guild", func(context.Context, string) (GuildMember, error) {
		return GuildMember{}, errors.New("member lookup must not run with invalid registration config")
	})
	loginUser(t, f, "invalid-guild", "")
	assertSnapshot("Alice Invalid Guild", "Invalid Role Nick", true)

	setConfig("discord_guild_id", "guild-1")
	addExistingLogin("member-down", "Alice Member Down", func(context.Context, string) (GuildMember, error) {
		return GuildMember{}, ErrProviderUnavailable
	})
	loginUser(t, f, "member-down", "")
	assertSnapshot("Alice Member Down", "Invalid Role Nick", true)

	addExistingLogin("definite-nonmember", "Alice Nonmember", func(context.Context, string) (GuildMember, error) {
		return GuildMember{}, nil
	})
	loginUser(t, f, "definite-nonmember", "")
	assertSnapshot("Alice Nonmember", "", false)
}

func TestNewIdentityStillRequiresLiveRegistrationConfigAndMembership(t *testing.T) {
	tests := []struct {
		name       string
		configKey  string
		config     string
		member     GuildMember
		memberErr  error
		wantStatus int
	}{
		{name: "invalid registration open", configKey: "registration_open", config: "invalid", member: GuildMember{Roles: []string{"role-1"}}, wantStatus: http.StatusServiceUnavailable},
		{name: "invalid guild", configKey: "discord_guild_id", config: "bad\nguild", member: GuildMember{Roles: []string{"role-1"}}, wantStatus: http.StatusServiceUnavailable},
		{name: "invalid role", configKey: "discord_role_id", config: "bad\nrole", member: GuildMember{Roles: []string{"role-1"}}, wantStatus: http.StatusServiceUnavailable},
		{name: "membership unavailable", memberErr: ErrProviderUnavailable, wantStatus: http.StatusServiceUnavailable},
		{name: "missing membership", member: GuildMember{}, wantStatus: http.StatusUnauthorized},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newRuntimeFixture(t, nil)
			if tc.configKey != "" {
				if _, err := f.store.DB().Exec(`UPDATE site_config SET value=? WHERE key=?`, tc.config, tc.configKey); err != nil {
					t.Fatal(err)
				}
			}
			f.provider.mu.Lock()
			f.provider.logins["new"] = DiscordLogin{
				Identity: DiscordIdentity{ID: "discord-new", Username: "New User"},
				GuildMember: func(context.Context, string) (GuildMember, error) {
					return tc.member, tc.memberErr
				},
			}
			f.provider.mu.Unlock()

			start := request(t, f.runtime.UserHandler(), host.StationUser, http.MethodGet, "https://user.example/api/auth/discord/start", "", nil, nil)
			stateCookie := responseCookie(t, start, OAuthStateCookieName)
			parsed, err := url.Parse(start.Header().Get("Location"))
			if err != nil {
				t.Fatal(err)
			}
			callback := request(t, f.runtime.UserHandler(), host.StationUser, http.MethodGet, "https://user.example/api/auth/discord/callback?code=new&state="+url.QueryEscape(parsed.Query().Get("state")), "", []*http.Cookie{stateCookie}, nil)
			if callback.Code != tc.wantStatus {
				t.Fatalf("status=%d body=%s", callback.Code, callback.Body.String())
			}
			var users int
			if err := f.store.DB().QueryRow(`SELECT COUNT(*) FROM users WHERE discord_id='discord-new'`).Scan(&users); err != nil {
				t.Fatal(err)
			}
			if users != 0 {
				t.Fatalf("created users=%d", users)
			}
		})
	}
}

func TestAdminPasswordRotationInvalidatesPersistedSessionGeneration(t *testing.T) {
	f := newRuntimeFixture(t, nil)
	login := request(t, f.runtime.AdminHandler(), host.StationAdmin, http.MethodPost, "https://admin.example/admin/api/login", `{"username":"operator","password":"correct horse battery staple"}`, nil, map[string]string{"Content-Type": "application/json"})
	oldCookie := responseCookie(t, login, AdminSessionCookieName)
	states, err := NewStateManagerWithKey(bytes.Repeat([]byte{0x61}, 32), DefaultOAuthStateTTL)
	if err != nil {
		t.Fatal(err)
	}
	if err := states.SetClock(f.clock.Now); err != nil {
		t.Fatal(err)
	}
	elev, err := elevation.NewManagerWithKey(bytes.Repeat([]byte{0x62}, 32), 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := elev.SetClock(f.clock.Now); err != nil {
		t.Fatal(err)
	}
	second, err := NewRuntime(RuntimeConfig{Store: f.store, Provider: f.provider, DiscordClientID: "client", UserSiteBaseURL: "https://user.example", AdminUsername: "operator", AdminPassword: "rotated password", CredentialKeyDeriver: f.vault, Authorizer: authz.New(authz.Options{Now: f.clock.Now, Elevation: elev}), Maintenance: f.gate, OAuthStates: states, Elevation: elev, Now: f.clock.Now})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })
	stale := request(t, second.AdminHandler(), host.StationAdmin, http.MethodGet, "https://admin.example/admin/api/session", "", []*http.Cookie{oldCookie}, nil)
	if stale.Code != http.StatusUnauthorized {
		t.Fatalf("stale session=%d %s", stale.Code, stale.Body.String())
	}
	newLogin := request(t, second.AdminHandler(), host.StationAdmin, http.MethodPost, "https://admin.example/admin/api/login", `{"username":"operator","password":"rotated password"}`, nil, map[string]string{"Content-Type": "application/json"})
	if newLogin.Code != http.StatusOK {
		t.Fatalf("rotated login=%d %s", newLogin.Code, newLogin.Body.String())
	}
}
