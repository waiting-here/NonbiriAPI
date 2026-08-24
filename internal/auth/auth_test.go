package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbtest"
	"github.com/waiting-here/NonbiriAPI/internal/host"
	"github.com/waiting-here/NonbiriAPI/internal/lifecyclegate"
	"github.com/waiting-here/NonbiriAPI/internal/ratelimit"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

type fakeDiscordProvider struct {
	mu          sync.Mutex
	login       DiscordLogin
	exchangeErr error
	calls       []string
}

func (p *fakeDiscordProvider) AuthorizationURL(_ context.Context, request DiscordAuthorizeRequest) (string, error) {
	return "https://discord.example/authorize?state=" + url.QueryEscape(request.State), nil
}

func (p *fakeDiscordProvider) Exchange(_ context.Context, code, _ string) (DiscordLogin, error) {
	p.mu.Lock()
	p.calls = append(p.calls, code)
	p.mu.Unlock()
	if p.exchangeErr != nil {
		return DiscordLogin{}, p.exchangeErr
	}
	return p.login, nil
}

type recordingThrottle struct {
	mu         sync.Mutex
	checks     int
	failures   int
	successes  int
	decision   ratelimit.LoginDecision
	checkErr   error
	failureErr error
	successErr error
}

func (t *recordingThrottle) Check(string, string) (ratelimit.LoginDecision, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.checks++
	if t.checkErr != nil {
		return ratelimit.LoginDecision{}, t.checkErr
	}
	if t.decision == (ratelimit.LoginDecision{}) {
		return ratelimit.LoginDecision{Allowed: true}, nil
	}
	return t.decision, nil
}
func (t *recordingThrottle) Failure(string, string) (ratelimit.LoginDecision, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.failures++
	if t.failureErr != nil {
		return ratelimit.LoginDecision{}, t.failureErr
	}
	return ratelimit.LoginDecision{Allowed: true}, nil
}
func (t *recordingThrottle) Success(string, string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.successes++
	return t.successErr
}

func authTestStore(t *testing.T) *db.Store {
	t.Helper()
	key := bytes.Repeat([]byte{0x47}, secret.MasterKeyBytes)
	vault, err := secret.New(key)
	clear(key)
	if err != nil {
		t.Fatalf("secret.New: %v", err)
	}
	dbPath := filepath.Join(t.TempDir(), "auth.db")
	dbtest.EnsureOwnerOnlyParent(t, dbPath)
	st, err := db.Open(dbPath, vault)
	if err != nil {
		_ = vault.Close()
		t.Fatalf("db.Open: %v", err)
	}
	if err := st.SetSiteConfigValue("registration_open", "1"); err != nil {
		t.Fatalf("open registration for auth fixture: %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close()
		_ = vault.Close()
	})
	return st
}

func stationRequest(method, target string, station host.Station, body io.Reader) *http.Request {
	r := httptest.NewRequest(method, target, body)
	r.RemoteAddr = "198.51.100.20:4000"
	return r.WithContext(host.WithStation(r.Context(), station))
}

func cookieFromResponse(t *testing.T, rec *httptest.ResponseRecorder, name string) *http.Cookie {
	t.Helper()
	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == name {
			return cookie
		}
	}
	t.Fatalf("cookie %q not found in response: %v", name, rec.Result().Cookies())
	return nil
}

func newTestUserAuth(t *testing.T, st *db.Store, provider DiscordProvider, gate RegistrationGateFunc) *UserAuth {
	t.Helper()
	key := bytes.Repeat([]byte{0x92}, 32)
	state, err := NewStateManagerWithKey(key, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.Close() })
	auth, err := NewUserAuth(UserAuthConfig{
		Store: st, Provider: provider, ClientID: "client-id", State: state,
		SiteBaseURL: "https://example.com", RegistrationGate: gate,
	})
	if err != nil {
		t.Fatalf("NewUserAuth: %v", err)
	}
	return auth
}

func startOAuth(t *testing.T, service *UserAuth) (*httptest.ResponseRecorder, *http.Request, string) {
	t.Helper()
	req := stationRequest(http.MethodGet, "https://example.com/api/auth/discord/start", host.StationUser, nil)
	rec := httptest.NewRecorder()
	service.Start(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("start status=%d body=%s", rec.Code, rec.Body.String())
	}
	stateCookie := cookieFromResponse(t, rec, OAuthStateCookieName)
	location := rec.Header().Get("Location")
	state := locationState(t, location)
	if stateCookie.Value != state {
		t.Fatalf("state cookie and URL differ: cookie=%q url=%q", stateCookie.Value, state)
	}
	return rec, req, state
}

func locationState(t *testing.T, location string) string {
	t.Helper()
	u, err := url.Parse(location)
	if err != nil {
		t.Fatalf("parse location: %v", err)
	}
	values := u.Query()["state"]
	if len(values) != 1 || values[0] == "" {
		t.Fatalf("location has invalid state: %q", location)
	}
	return values[0]
}

func callbackRequest(state, code string, cookie *http.Cookie) *http.Request {
	r := stationRequest(http.MethodGet, "https://example.com/api/auth/discord/callback?state="+url.QueryEscape(state)+"&code="+url.QueryEscape(code), host.StationUser, nil)
	r.AddCookie(cookie)
	return r
}

func TestUserOAuthGateSessionAndReplay(t *testing.T) {
	st := authTestStore(t)
	provider := &fakeDiscordProvider{login: DiscordLogin{
		Identity: DiscordIdentity{ID: "discord-1", Username: "alice", Avatar: "avatar"},
		GuildMember: func(_ context.Context, guildID string) (GuildMember, error) {
			if guildID == "guild-1" {
				return GuildMember{Roles: []string{"role-1"}, Nick: "alice-server"}, nil
			}
			return GuildMember{}, nil
		},
	}}
	gate := func(context.Context) (RegistrationGate, error) {
		return RegistrationGate{GuildID: "guild-1", RoleID: "role-1"}, nil
	}
	service := newTestUserAuth(t, st, provider, gate)

	startRec, _, state := startOAuth(t, service)
	stateCookie := cookieFromResponse(t, startRec, OAuthStateCookieName)
	callback := httptest.NewRecorder()
	service.Callback(callback, callbackRequest(state, "code-1", stateCookie))
	if callback.Code != http.StatusFound || callback.Header().Get("Location") != "/" {
		t.Fatalf("callback status=%d location=%q body=%s", callback.Code, callback.Header().Get("Location"), callback.Body.String())
	}
	sessionCookie := cookieFromResponse(t, callback, UserSessionCookieName)
	if !sessionCookie.Secure || !sessionCookie.HttpOnly || sessionCookie.SameSite != http.SameSiteLaxMode || sessionCookie.Path != "/api" {
		t.Fatalf("unsafe user session cookie: %#v", sessionCookie)
	}
	user, err := st.GetUserByDiscordID("discord-1")
	if err != nil || user == nil {
		t.Fatalf("registered user=%#v err=%v", user, err)
	}
	if _, err := st.AuthenticateUserSession(sessionCookie.Value); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(callback.Body.String(), "code-1") || strings.Contains(callback.Body.String(), "avatar") {
		t.Fatalf("callback response echoed provider input: %q", callback.Body.String())
	}

	// A second callback with the consumed state must fail and cannot mint a
	// second session, even though the provider would otherwise accept the code.
	replay := httptest.NewRecorder()
	service.Callback(replay, callbackRequest(state, "code-1", stateCookie))
	if replay.Code != http.StatusUnauthorized || strings.Contains(replay.Body.String(), state) || strings.Contains(replay.Body.String(), "code-1") {
		t.Fatalf("replay response status=%d body=%q", replay.Code, replay.Body.String())
	}
}

func TestUserOAuthStoresAndRefreshesGuildSnapshot(t *testing.T) {
	st := authTestStore(t)
	// Registration: the guild member carries a server nickname and a server
	// avatar hash; both should be stored as a snapshot on the new user.
	provider := &fakeDiscordProvider{login: DiscordLogin{
		Identity: DiscordIdentity{ID: "discord-guild", Username: "global-name", Avatar: "global-avatar"},
		GuildMember: func(_ context.Context, guildID string) (GuildMember, error) {
			if guildID == "guild-1" {
				return GuildMember{Roles: []string{"role-1"}, Nick: "server-name", Avatar: "guild-avatar-hash"}, nil
			}
			return GuildMember{}, nil
		},
	}}
	service := newTestUserAuth(t, st, provider, func(context.Context) (RegistrationGate, error) {
		return RegistrationGate{GuildID: "guild-1", RoleID: "role-1"}, nil
	})

	startRec, _, state := startOAuth(t, service)
	stateCookie := cookieFromResponse(t, startRec, OAuthStateCookieName)
	rec := httptest.NewRecorder()
	service.Callback(rec, callbackRequest(state, "code-guild", stateCookie))
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/" {
		t.Fatalf("register status=%d location=%q body=%s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	user, err := st.GetUserByDiscordID("discord-guild")
	if err != nil || user == nil {
		t.Fatalf("registered user=%#v err=%v", user, err)
	}
	if user.GuildNick != "server-name" {
		t.Fatalf("guild nick after register=%q want server-name", user.GuildNick)
	}
	if user.GuildAvatarURL == "" || !strings.Contains(user.GuildAvatarURL, "guild-avatar-hash") {
		t.Fatalf("guild avatar url after register=%q want guild-avatar-hash", user.GuildAvatarURL)
	}

	// Login (existing user): a fresh OAuth start refreshes the snapshot with
	// the member's current nickname and avatar.
	provider.login = DiscordLogin{
		Identity: DiscordIdentity{ID: "discord-guild", Username: "global-name", Avatar: "global-avatar"},
		GuildMember: func(_ context.Context, guildID string) (GuildMember, error) {
			if guildID == "guild-1" {
				return GuildMember{Roles: []string{"role-1"}, Nick: "updated-name", Avatar: "updated-hash"}, nil
			}
			return GuildMember{}, nil
		},
	}
	startRec2, _, state2 := startOAuth(t, service)
	stateCookie2 := cookieFromResponse(t, startRec2, OAuthStateCookieName)
	login := httptest.NewRecorder()
	service.Callback(login, callbackRequest(state2, "code-guild-2", stateCookie2))
	if login.Code != http.StatusFound || login.Header().Get("Location") != "/" {
		t.Fatalf("login status=%d location=%q body=%s", login.Code, login.Header().Get("Location"), login.Body.String())
	}
	user, err = st.GetUserByDiscordID("discord-guild")
	if err != nil || user == nil {
		t.Fatalf("user after login=%#v err=%v", user, err)
	}
	if user.GuildNick != "updated-name" {
		t.Fatalf("guild nick after login=%q want updated-name", user.GuildNick)
	}
	if user.GuildAvatarURL == "" || !strings.Contains(user.GuildAvatarURL, "updated-hash") {
		t.Fatalf("guild avatar url after login=%q want updated-hash", user.GuildAvatarURL)
	}
}

func TestUserOAuthRegistrationPauseAndExistingUserLogin(t *testing.T) {
	st := authTestStore(t)
	provider := &fakeDiscordProvider{login: DiscordLogin{Identity: DiscordIdentity{ID: "discord-existing", Username: "before"}}}
	gateValue := RegistrationGate{GuildID: "", RoleID: ""}
	service := newTestUserAuth(t, st, provider, func(context.Context) (RegistrationGate, error) { return gateValue, nil })

	startRec, _, state := startOAuth(t, service)
	cookie := cookieFromResponse(t, startRec, OAuthStateCookieName)
	paused := httptest.NewRecorder()
	service.Callback(paused, callbackRequest(state, "code-paused", cookie))
	if paused.Code != http.StatusServiceUnavailable || strings.Contains(paused.Body.String(), "code-paused") {
		t.Fatalf("paused registration status=%d body=%q", paused.Code, paused.Body.String())
	}
	if user, err := st.GetUserByDiscordID("discord-existing"); err != nil || user != nil {
		t.Fatalf("paused registration created user=%#v err=%v", user, err)
	}

	user, err := st.CreateDiscordUser("discord-existing", "old", "")
	if err != nil {
		t.Fatal(err)
	}
	if user == nil {
		t.Fatal("CreateDiscordUser returned nil")
	}
	startRec, _, state = startOAuth(t, service)
	cookie = cookieFromResponse(t, startRec, OAuthStateCookieName)
	login := httptest.NewRecorder()
	service.Callback(login, callbackRequest(state, "code-existing", cookie))
	if login.Code != http.StatusFound {
		t.Fatalf("existing user login status=%d body=%s", login.Code, login.Body.String())
	}
	updated, err := st.GetUserByDiscordID("discord-existing")
	if err != nil || updated == nil || updated.Username != "before" {
		t.Fatalf("existing profile was not refreshed: %#v err=%v", updated, err)
	}
}

// TestUserOAuthRegistrationClosedRedirectsNewUserButAllowsExisting verifies the
// registration_open site toggle: a closed registration redirects a brand-new
// identity to the registration-closed notice without creating a user, while an
// existing identity still signs in (the gate is evaluated only on the
// new-identity branch).
func TestUserOAuthRegistrationClosedRedirectsNewUserButAllowsExisting(t *testing.T) {
	st := authTestStore(t)
	if err := st.SetSiteConfigValue("registration_open", "0"); err != nil {
		t.Fatalf("set registration_open: %v", err)
	}
	provider := &fakeDiscordProvider{login: DiscordLogin{
		Identity: DiscordIdentity{ID: "discord-closed", Username: "newcomer", Avatar: "avatar"},
		GuildMember: func(context.Context, string) (GuildMember, error) {
			return GuildMember{Roles: []string{"role-1"}}, nil
		},
	}}
	service := newTestUserAuth(t, st, provider, func(context.Context) (RegistrationGate, error) {
		return RegistrationGate{GuildID: "guild-1", RoleID: "role-1"}, nil
	})

	startRec, _, state := startOAuth(t, service)
	cookie := cookieFromResponse(t, startRec, OAuthStateCookieName)
	closed := httptest.NewRecorder()
	service.Callback(closed, callbackRequest(state, "code-closed", cookie))
	if closed.Code != http.StatusFound || closed.Header().Get("Location") != "/registration-closed" {
		t.Fatalf("closed registration status=%d location=%q body=%q", closed.Code, closed.Header().Get("Location"), closed.Body.String())
	}
	if user, err := st.GetUserByDiscordID("discord-closed"); err != nil || user != nil {
		t.Fatalf("closed registration created user=%#v err=%v", user, err)
	}

	if _, err := st.CreateDiscordUser("discord-closed", "old", ""); err != nil {
		t.Fatal(err)
	}
	startRec2, _, state2 := startOAuth(t, service)
	cookie2 := cookieFromResponse(t, startRec2, OAuthStateCookieName)
	login := httptest.NewRecorder()
	service.Callback(login, callbackRequest(state2, "code-login", cookie2))
	if login.Code != http.StatusFound || login.Header().Get("Location") != "/" {
		t.Fatalf("existing login status=%d location=%q body=%q", login.Code, login.Header().Get("Location"), login.Body.String())
	}
	if c := cookieFromResponse(t, login, UserSessionCookieName); c.Value == "" {
		t.Fatalf("existing login did not mint a session cookie")
	}
}

func TestUserOAuthGuildRoleRequiresBothExactValues(t *testing.T) {
	st := authTestStore(t)
	provider := &fakeDiscordProvider{login: DiscordLogin{
		Identity: DiscordIdentity{ID: "discord-mismatch", Username: "alice"},
		GuildMember: func(_ context.Context, guildID string) (GuildMember, error) {
			if guildID == "guild-good" {
				return GuildMember{Roles: []string{"role-good"}}, nil
			}
			return GuildMember{}, nil
		},
	}}
	service := newTestUserAuth(t, st, provider, func(context.Context) (RegistrationGate, error) {
		return RegistrationGate{GuildID: "guild-bad", RoleID: "role-good"}, nil
	})
	startRec, _, state := startOAuth(t, service)
	callback := httptest.NewRecorder()
	service.Callback(callback, callbackRequest(state, "code-mismatch", cookieFromResponse(t, startRec, OAuthStateCookieName)))
	if callback.Code != http.StatusUnauthorized || strings.Contains(callback.Body.String(), "guild-bad") || strings.Contains(callback.Body.String(), "role-good") {
		t.Fatalf("mismatch status=%d body=%q", callback.Code, callback.Body.String())
	}
	if user, err := st.GetUserByDiscordID("discord-mismatch"); err != nil || user != nil {
		t.Fatalf("mismatch created user=%#v err=%v", user, err)
	}
}

func TestUserAndAdminSessionMiddlewareAreStationAndRoleIsolated(t *testing.T) {
	st := authTestStore(t)
	user, err := st.CreateDiscordUser("discord-isolation", "alice", "")
	if err != nil {
		t.Fatal(err)
	}
	userToken, _, err := st.CreateUserSession(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := st.EnsureAdminUser("root")
	if err != nil {
		t.Fatal(err)
	}
	adminToken, _, err := st.CreateAdminSession(admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	provider := &fakeDiscordProvider{}
	userService := newTestUserAuth(t, st, provider, func(context.Context) (RegistrationGate, error) { return RegistrationGate{}, nil })
	adminService, err := NewAdminAuth(AdminAuthConfig{Store: st, Username: "root", Password: "password", SiteBaseURL: "https://example.com"})
	if err != nil {
		t.Fatal(err)
	}
	defer adminService.Close()

	var userPrincipal Principal
	userNext := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var ok bool
		userPrincipal, ok = PrincipalFromContext(r.Context())
		if !ok {
			t.Error("missing user principal")
		}
		w.WriteHeader(http.StatusNoContent)
	})
	userRequest := stationRequest(http.MethodGet, "https://example.com/api/me", host.StationUser, nil)
	userRequest.AddCookie(&http.Cookie{Name: UserSessionCookieName, Value: userToken})
	userRec := httptest.NewRecorder()
	userService.Middleware(userNext).ServeHTTP(userRec, userRequest)
	if userRec.Code != http.StatusNoContent || userPrincipal.Kind != PrincipalUserSession || userPrincipal.User.ID != user.ID {
		t.Fatalf("user middleware status=%d principal=%#v", userRec.Code, userPrincipal)
	}

	adminRequest := stationRequest(http.MethodGet, "https://admin.example.com/admin/api/users", host.StationAdmin, nil)
	adminRequest.AddCookie(&http.Cookie{Name: AdminSessionCookieName, Value: adminToken})
	adminRec := httptest.NewRecorder()
	adminService.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal, ok := AdminFromContext(r.Context())
		if !ok || principal.ID != admin.ID {
			t.Errorf("missing admin principal: %#v %v", principal, ok)
		}
		w.WriteHeader(http.StatusNoContent)
	})).ServeHTTP(adminRec, adminRequest)
	if adminRec.Code != http.StatusNoContent {
		t.Fatalf("admin middleware status=%d body=%s", adminRec.Code, adminRec.Body.String())
	}

	// Crossing either cookie into the other station/domain never authorizes.
	cross := stationRequest(http.MethodGet, "https://admin.example.com/admin/api/users", host.StationAdmin, nil)
	cross.AddCookie(&http.Cookie{Name: UserSessionCookieName, Value: userToken})
	crossRec := httptest.NewRecorder()
	adminService.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("user session crossed into admin") })).ServeHTTP(crossRec, cross)
	if crossRec.Code != http.StatusUnauthorized {
		t.Fatalf("cross-station user session status=%d body=%s", crossRec.Code, crossRec.Body.String())
	}
	cross = stationRequest(http.MethodGet, "https://example.com/api/session", host.StationUser, nil)
	cross.AddCookie(&http.Cookie{Name: AdminSessionCookieName, Value: adminToken})
	crossRec = httptest.NewRecorder()
	userService.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("admin session crossed into user") })).ServeHTTP(crossRec, cross)
	if crossRec.Code != http.StatusUnauthorized {
		t.Fatalf("cross-station admin session status=%d body=%s", crossRec.Code, crossRec.Body.String())
	}
}

func TestUserSessionLeaseCancelsARealHandlerOnRetirement(t *testing.T) {
	st := authTestStore(t)
	user, err := st.CreateDiscordUser("discord-gated-handler", "gated-handler", "")
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := st.CreateUserSession(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	gate, err := lifecyclegate.New(lifecyclegate.Config{MaxUsers: 4})
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Close()
	service, err := NewUserAuth(UserAuthConfig{
		Store: st, Provider: &fakeDiscordProvider{}, ClientID: "client-id",
		SiteBaseURL: "https://example.com",
		UserRequestGate: func(ctx context.Context, userID int64, binding string) (context.Context, func(), error) {
			return gate.Admit(ctx, userID, binding, st.ValidateUserSessionBinding)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()

	entered := make(chan struct{})
	finished := make(chan struct{})
	handler := service.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-r.Context().Done()
		if !errors.Is(r.Context().Err(), context.Canceled) {
			t.Errorf("handler context error=%v, want context.Canceled", r.Context().Err())
		}
		close(finished)
	}))
	req := stationRequest(http.MethodGet, "https://example.com/api/me", host.StationUser, nil)
	req.AddCookie(&http.Cookie{Name: UserSessionCookieName, Value: token})
	rec := httptest.NewRecorder()
	serveDone := make(chan struct{})
	go func() {
		handler.ServeHTTP(rec, req)
		close(serveDone)
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not acquire a user lease")
	}
	retirementDone := make(chan error, 1)
	go func() {
		retirement, beginErr := gate.BeginUserRetirement(user.ID)
		if beginErr != nil {
			retirementDone <- beginErr
			return
		}
		retirement.Commit()
		retirementDone <- nil
	}()
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("retirement did not cancel the real handler")
	}
	select {
	case <-serveDone:
	case <-time.After(2 * time.Second):
		t.Fatal("real handler did not release its user lease")
	}
	select {
	case err := <-retirementDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("retirement did not drain the real handler")
	}
}

func TestCallerKeyLeaseCancelsARealRunnerOnRetirement(t *testing.T) {
	st := authTestStore(t)
	user, err := st.CreateDiscordUser("discord-gated-caller", "gated-caller", "")
	if err != nil {
		t.Fatal(err)
	}
	generation, err := st.RegenerateCallerKey(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	gate, err := lifecyclegate.New(lifecyclegate.Config{MaxUsers: 4})
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Close()
	entered := make(chan struct{})
	finished := make(chan struct{})
	runner := CallerKeyMiddlewareWithGate(st, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-r.Context().Done()
		if !errors.Is(r.Context().Err(), context.Canceled) {
			t.Errorf("runner context error=%v, want context.Canceled", r.Context().Err())
		}
		close(finished)
	}), func(ctx context.Context, userID int64, binding string) (context.Context, func(), error) {
		return gate.Admit(ctx, userID, binding, st.ValidateCallerKeyBinding)
	})
	req := stationRequest(http.MethodPost, "https://example.com/v1/chat/completions", host.StationUser, nil)
	req.Header.Set("Authorization", "Bearer "+generation.Secret)
	rec := httptest.NewRecorder()
	serveDone := make(chan struct{})
	go func() {
		runner.ServeHTTP(rec, req)
		close(serveDone)
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("runner did not acquire a caller lease")
	}
	retirementDone := make(chan error, 1)
	go func() {
		retirement, beginErr := gate.BeginUserRetirement(user.ID)
		if beginErr != nil {
			retirementDone <- beginErr
			return
		}
		retirement.Commit()
		retirementDone <- nil
	}()
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("retirement did not cancel the real runner")
	}
	select {
	case <-serveDone:
	case <-time.After(2 * time.Second):
		t.Fatal("real runner did not release its caller lease")
	}
	select {
	case err := <-retirementDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("retirement did not drain the real runner")
	}
}

func TestAdminEnvironmentCredentialsThrottleAndNoPasswordPersistence(t *testing.T) {
	st := authTestStore(t)
	throttle := &recordingThrottle{}
	service, err := NewAdminAuth(AdminAuthConfig{
		Store: st, Username: "root-user", Password: "correct-password", Throttle: throttle,
		SiteBaseURL: "https://example.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()

	badBody := bytes.NewReader([]byte(`{"username":"root-user","password":"wrong-password"}`))
	bad := httptest.NewRecorder()
	service.Login(bad, stationRequest(http.MethodPost, "https://admin.example.com/admin/api/login", host.StationAdmin, badBody))
	if bad.Code != http.StatusUnauthorized || strings.Contains(bad.Body.String(), "wrong-password") || strings.Contains(bad.Body.String(), "correct-password") {
		t.Fatalf("bad login status=%d body=%q", bad.Code, bad.Body.String())
	}
	throttle.mu.Lock()
	checks, failures := throttle.checks, throttle.failures
	throttle.mu.Unlock()
	if checks != 1 || failures != 1 {
		t.Fatalf("throttle calls check=%d failure=%d, want 1/1", checks, failures)
	}

	goodBody := bytes.NewReader([]byte(`{"username":"root-user","password":"correct-password"}`))
	good := httptest.NewRecorder()
	service.Login(good, stationRequest(http.MethodPost, "https://admin.example.com/admin/api/login", host.StationAdmin, goodBody))
	if good.Code != http.StatusOK || strings.Contains(good.Body.String(), "correct-password") {
		t.Fatalf("good login status=%d body=%q", good.Code, good.Body.String())
	}
	cookie := cookieFromResponse(t, good, AdminSessionCookieName)
	if !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode || cookie.Path != "/admin" {
		t.Fatalf("unsafe admin cookie: %#v", cookie)
	}
	throttle.mu.Lock()
	successes := throttle.successes
	throttle.mu.Unlock()
	if successes != 1 {
		t.Fatalf("throttle successes=%d, want 1", successes)
	}
	var admins int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM users WHERE is_admin=1`).Scan(&admins); err != nil {
		t.Fatal(err)
	}
	if admins != 1 {
		t.Fatalf("admin rows=%d, want one", admins)
	}
	var raw string
	if err := st.DB().QueryRow(`SELECT username || ':' || COALESCE(discord_id, '') FROM users WHERE is_admin=1`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, "correct-password") || strings.Contains(good.Header().Get("Set-Cookie"), "correct-password") {
		t.Fatalf("password crossed a persistence/response boundary: raw=%q cookie=%q", raw, good.Header().Get("Set-Cookie"))
	}
	if !service.AdminCredentialCheck("root-user", "correct-password") || service.AdminCredentialCheck("root-user", "wrong-password") {
		t.Fatal("AdminCredentialCheck result incorrect")
	}
}

func TestAdminThrottleCannotBeFilledWithCandidateUsernameVariants(t *testing.T) {
	st := authTestStore(t)
	config := ratelimit.DefaultLoginThrottleConfig()
	config.MaxEntries = 1
	throttle, err := ratelimit.NewLoginThrottle(config)
	if err != nil {
		t.Fatal(err)
	}
	defer throttle.Close()
	service, err := NewAdminAuth(AdminAuthConfig{
		Store: st, Username: "root-user", Password: "correct-password", Throttle: throttle,
		SiteBaseURL: "https://example.com",
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, candidate := range []string{"attacker-name-one", "attacker-name-two"} {
		body := bytes.NewReader([]byte(`{"username":"` + candidate + `","password":"wrong-password"}`))
		recorder := httptest.NewRecorder()
		service.Login(recorder, stationRequest(http.MethodPost, "https://admin.example.com/admin/api/login", host.StationAdmin, body))
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("candidate %q status=%d body=%q; username variation consumed throttle capacity", candidate, recorder.Code, recorder.Body.String())
		}
	}
}

func TestCallerKeyMiddlewareAndBannedImmediateFailure(t *testing.T) {
	st := authTestStore(t)
	user, err := st.CreateDiscordUser("discord-caller", "caller", "")
	if err != nil {
		t.Fatal(err)
	}
	generation, err := st.RegenerateCallerKey(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	var got Principal
	var callerContext context.Context
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callerContext = r.Context()
		var ok bool
		got, ok = PrincipalFromContext(r.Context())
		if !ok {
			t.Error("caller principal missing")
		}
		w.WriteHeader(http.StatusNoContent)
	})
	req := stationRequest(http.MethodGet, "https://example.com/v1/models", host.StationUser, nil)
	req.Header.Set("Authorization", "Bearer "+generation.Secret)
	rec := httptest.NewRecorder()
	CallerKeyMiddleware(st, next).ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent || got.Kind != PrincipalCallerKey || got.User.ID != user.ID {
		t.Fatalf("caller middleware status=%d principal=%#v body=%s", rec.Code, got, rec.Body.String())
	}
	if _, ok := UserFromContext(callerContext); ok {
		t.Fatal("session-only UserFromContext accepted a caller-key request context")
	}
	if caller, ok := CallerUserFromContext(callerContext); !ok || caller.ID != user.ID {
		t.Fatal("CallerUserFromContext did not expose the caller principal")
	}
	for _, authorization := range []string{"", "Basic " + generation.Secret, "Bearer " + generation.Secret + "\n", "Bearer nbk_bad"} {
		req = stationRequest(http.MethodGet, "https://example.com/v1/models", host.StationUser, nil)
		if authorization != "" {
			req.Header.Set("Authorization", authorization)
		}
		rec = httptest.NewRecorder()
		CallerKeyMiddleware(st, next).ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("authorization %q status=%d body=%s", authorization, rec.Code, rec.Body.String())
		}
	}
	if err := st.BanUser(user.ID, "security review"); err != nil {
		t.Fatal(err)
	}
	req = stationRequest(http.MethodGet, "https://example.com/v1/models", host.StationUser, nil)
	req.Header.Set("Authorization", "Bearer "+generation.Secret)
	rec = httptest.NewRecorder()
	CallerKeyMiddleware(st, next).ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("banned caller key status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestCallerKeyHandlersReturnPlaintextOnlyOnRegeneration(t *testing.T) {
	st := authTestStore(t)
	user, err := st.CreateDiscordUser("discord-key-handler", "caller", "")
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := st.CreateUserSession(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	service := newTestUserAuth(t, st, &fakeDiscordProvider{}, func(context.Context) (RegistrationGate, error) { return RegistrationGate{}, nil })
	request := stationRequest(http.MethodPost, "https://example.com/api/caller-key/regenerate", host.StationUser, nil)
	request.AddCookie(&http.Cookie{Name: UserSessionCookieName, Value: token})
	regenerated := httptest.NewRecorder()
	service.RegenerateCallerKey(regenerated, request)
	if regenerated.Code != http.StatusOK || regenerated.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("regenerate status=%d cache=%q body=%s", regenerated.Code, regenerated.Header().Get("Cache-Control"), regenerated.Body.String())
	}
	var payload struct {
		Secret string `json:"secret"`
	}
	if err := json.Unmarshal(regenerated.Body.Bytes(), &payload); err != nil || !strings.HasPrefix(payload.Secret, db.CallerKeyPrefix) {
		t.Fatalf("regenerate payload=%s err=%v", regenerated.Body.String(), err)
	}
	metadataRequest := stationRequest(http.MethodGet, "https://example.com/api/caller-key", host.StationUser, nil)
	metadataRequest.AddCookie(&http.Cookie{Name: UserSessionCookieName, Value: token})
	metadata := httptest.NewRecorder()
	service.UserCallerKeyMetadata(metadata, metadataRequest)
	if metadata.Code != http.StatusOK || strings.Contains(metadata.Body.String(), payload.Secret) || !strings.Contains(metadata.Body.String(), "…") {
		t.Fatalf("metadata leaked secret or omitted fragments: status=%d body=%s", metadata.Code, metadata.Body.String())
	}
}

func TestAuthHandlersUseNoStoreAndStableErrors(t *testing.T) {
	st := authTestStore(t)
	service := newTestUserAuth(t, st, &fakeDiscordProvider{}, func(context.Context) (RegistrationGate, error) {
		return RegistrationGate{}, nil
	})
	userHandler := service.Handler()
	req := stationRequest(http.MethodGet, "https://example.com/api/session", host.StationUser, nil)
	rec := httptest.NewRecorder()
	userHandler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("user handler status=%d cache=%q body=%s", rec.Code, rec.Header().Get("Cache-Control"), rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"code":"unauthorized"`) {
		t.Fatalf("user handler did not use stable error: %s", rec.Body.String())
	}
	method := stationRequest(http.MethodPost, "https://example.com/api/session", host.StationUser, nil)
	methodRec := httptest.NewRecorder()
	userHandler.ServeHTTP(methodRec, method)
	if methodRec.Code != http.StatusMethodNotAllowed || methodRec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("user method fallback status=%d cache=%q body=%s", methodRec.Code, methodRec.Header().Get("Cache-Control"), methodRec.Body.String())
	}

	admin, err := NewAdminAuth(AdminAuthConfig{Store: st, Username: "root", Password: "password", SiteBaseURL: "https://example.com"})
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	wrongStation := stationRequest(http.MethodPost, "https://example.com/admin/api/login", host.StationUser, bytes.NewReader([]byte(`{"username":"root","password":"password"}`)))
	wrongRec := httptest.NewRecorder()
	admin.Handler().ServeHTTP(wrongRec, wrongStation)
	if wrongRec.Code != http.StatusForbidden || strings.Contains(wrongRec.Body.String(), "password") {
		t.Fatalf("admin station boundary status=%d body=%q", wrongRec.Code, wrongRec.Body.String())
	}
}

func TestUserLogoutInvokesProcessLocalBindingHookAfterSessionDeletion(t *testing.T) {
	st := authTestStore(t)
	user, err := st.CreateDiscordUser("discord-logout-hook", "logout-hook", "")
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := st.CreateUserSession(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	var hookUserID int64
	var hookBinding string
	service, err := NewUserAuth(UserAuthConfig{
		Store: st, Provider: &fakeDiscordProvider{}, ClientID: "client-id",
		SiteBaseURL: "https://example.com",
		OnLogout: func(userID int64, sessionBinding string) {
			hookUserID = userID
			hookBinding = sessionBinding
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	req := stationRequest(http.MethodPost, "https://example.com/api/auth/logout", host.StationUser, nil)
	req.AddCookie(&http.Cookie{Name: UserSessionCookieName, Value: token})
	rec := httptest.NewRecorder()
	service.Logout(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("logout status=%d body=%s", rec.Code, rec.Body.String())
	}
	if hookUserID != user.ID || hookBinding != db.SessionHash(token) || strings.Contains(hookBinding, token) {
		t.Fatalf("logout hook user=%d binding shape=%d", hookUserID, len(hookBinding))
	}
	if got, err := st.AuthenticateUserSession(token); err != nil || got != nil {
		t.Fatalf("logged-out session = %#v, %v", got, err)
	}
}

func TestCookieHelpersSetSecurityAttributes(t *testing.T) {
	expires := time.Now().Add(time.Hour)
	rec := httptest.NewRecorder()
	SetUserSessionCookie(rec, "opaque", expires, true)
	cookie := cookieFromResponse(t, rec, UserSessionCookieName)
	if cookie.Value != "opaque" || cookie.Path != "/api" || !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode || cookie.MaxAge <= 0 {
		t.Fatalf("user cookie attributes=%#v", cookie)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("session cookie response is cacheable")
	}
	rec = httptest.NewRecorder()
	SetOAuthStateCookie(rec, "signed-state", true, 10*time.Minute)
	stateCookie := cookieFromResponse(t, rec, OAuthStateCookieName)
	if stateCookie.Path != "/api/auth/discord" || !stateCookie.Secure || !stateCookie.HttpOnly || stateCookie.SameSite != http.SameSiteLaxMode || stateCookie.MaxAge != 600 {
		t.Fatalf("oauth cookie attributes=%#v", stateCookie)
	}
}

func TestPublicUserResponseDoesNotContainSecurityMaterial(t *testing.T) {
	st := authTestStore(t)
	user, err := st.CreateDiscordUser("discord-public", "public-user", "")
	if err != nil {
		t.Fatal(err)
	}
	service := newTestUserAuth(t, st, &fakeDiscordProvider{}, func(context.Context) (RegistrationGate, error) { return RegistrationGate{}, nil })
	token, _, err := st.CreateUserSession(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	req := stationRequest(http.MethodGet, "https://example.com/api/session", host.StationUser, nil)
	req.AddCookie(&http.Cookie{Name: UserSessionCookieName, Value: token})
	rec := httptest.NewRecorder()
	service.Session(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("session status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(body)
	for _, forbidden := range []string{"password", "token_hash", "key_hash", "access_token", "refresh_token"} {
		if bytes.Contains(encoded, []byte(forbidden)) {
			t.Fatalf("public session response contains security field %q: %s", forbidden, encoded)
		}
	}
}

// TestAdminSessionRevokedOnPasswordRotation verifies the administrator
// password-rotation revocation property: an admin session is stamped with the
// current password fingerprint, so rotating the password (env change +
// restart) revokes the pre-rotation long-lived admin session at the next
// request and deletes the stale row. The opaque fingerprint never appears in
// a response body or header.
func TestAdminSessionRevokedOnPasswordRotation(t *testing.T) {
	st := authTestStore(t)
	subkey := bytes.Repeat([]byte{0x71}, secret.SubkeyBytes)
	t.Cleanup(func() { clear(subkey) })

	first, err := NewAdminAuth(AdminAuthConfig{
		Store: st, Username: "root", Password: "first-password",
		CredGenSubkey: subkey, SiteBaseURL: "https://example.com",
	})
	if err != nil {
		t.Fatalf("NewAdminAuth (first): %v", err)
	}
	defer first.Close()
	if first.credGen == "" {
		t.Fatal("admin auth with a subkey did not derive a credential-generation fingerprint")
	}

	login := httptest.NewRecorder()
	first.Login(login, stationRequest(http.MethodPost, "https://admin.example.com/admin/api/login",
		host.StationAdmin, bytes.NewReader([]byte(`{"username":"root","password":"first-password"}`))))
	if login.Code != http.StatusOK {
		t.Fatalf("login status=%d body=%s", login.Code, login.Body.String())
	}
	cookie := cookieFromResponse(t, login, AdminSessionCookieName)
	if strings.Contains(login.Body.String(), first.credGen) {
		t.Fatal("credential-generation fingerprint leaked into the login response body")
	}
	if strings.Contains(login.Header().Get("Set-Cookie"), first.credGen) {
		t.Fatal("credential-generation fingerprint leaked into the login Set-Cookie header")
	}

	// The session authenticates under the same credential generation.
	admin, err := st.AuthenticateAdminSessionWithCredGen(cookie.Value, first.credGen)
	if err != nil || admin == nil || !admin.IsAdmin {
		t.Fatalf("same-generation AuthenticateAdminSessionWithCredGen = %#v err=%v", admin, err)
	}

	// Rotating the password (env change + restart) builds a new AdminAuth with
	// the same subkey but a different password; its fingerprint differs.
	rotated, err := NewAdminAuth(AdminAuthConfig{
		Store: st, Username: "root", Password: "second-password",
		CredGenSubkey: subkey, SiteBaseURL: "https://example.com",
	})
	if err != nil {
		t.Fatalf("NewAdminAuth (rotated): %v", err)
	}
	defer rotated.Close()
	if rotated.credGen == first.credGen {
		t.Fatal("password rotation did not change the credential-generation fingerprint")
	}

	// The pre-rotation session is revoked at the next request and the stale
	// row is deleted, not merely rejected.
	req := stationRequest(http.MethodGet, "https://admin.example.com/admin/api/session", host.StationAdmin, nil)
	req.AddCookie(&http.Cookie{Name: AdminSessionCookieName, Value: cookie.Value})
	rec := httptest.NewRecorder()
	rotated.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("rotated password authorized a pre-rotation admin session")
	})).ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("rotated password status=%d body=%s; want 401", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), first.credGen) || strings.Contains(rec.Body.String(), rotated.credGen) {
		t.Fatal("credential-generation fingerprint leaked into the unauthorized response body")
	}
	if count, err := st.SessionRowCount(); err != nil || count != 0 {
		t.Fatalf("stale admin session row lingered after rotation: count=%d err=%v", count, err)
	}
}

// TestAdminSessionSurvivesSamePasswordRestart verifies the counterpart
// property: a plain restart with the SAME password keeps admin sessions
// alive, because the fingerprint is unchanged. Only an actual password
// rotation revokes them.
func TestAdminSessionSurvivesSamePasswordRestart(t *testing.T) {
	st := authTestStore(t)
	subkey := bytes.Repeat([]byte{0x72}, secret.SubkeyBytes)
	t.Cleanup(func() { clear(subkey) })

	first, err := NewAdminAuth(AdminAuthConfig{
		Store: st, Username: "root", Password: "stable-password",
		CredGenSubkey: subkey, SiteBaseURL: "https://example.com",
	})
	if err != nil {
		t.Fatalf("NewAdminAuth (first): %v", err)
	}
	defer first.Close()

	login := httptest.NewRecorder()
	first.Login(login, stationRequest(http.MethodPost, "https://admin.example.com/admin/api/login",
		host.StationAdmin, bytes.NewReader([]byte(`{"username":"root","password":"stable-password"}`))))
	if login.Code != http.StatusOK {
		t.Fatalf("login status=%d body=%s", login.Code, login.Body.String())
	}
	cookie := cookieFromResponse(t, login, AdminSessionCookieName)

	// Simulate a plain restart: a freshly built AdminAuth with the same
	// subkey and the same password derives the same fingerprint.
	restarted, err := NewAdminAuth(AdminAuthConfig{
		Store: st, Username: "root", Password: "stable-password",
		CredGenSubkey: subkey, SiteBaseURL: "https://example.com",
	})
	if err != nil {
		t.Fatalf("NewAdminAuth (restarted): %v", err)
	}
	defer restarted.Close()
	if restarted.credGen != first.credGen {
		t.Fatal("same password produced a different credential-generation fingerprint")
	}

	req := stationRequest(http.MethodGet, "https://admin.example.com/admin/api/session", host.StationAdmin, nil)
	req.AddCookie(&http.Cookie{Name: AdminSessionCookieName, Value: cookie.Value})
	rec := httptest.NewRecorder()
	restarted.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})).ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("same-password restart status=%d body=%s; want 204", rec.Code, rec.Body.String())
	}
}

// TestAdminAuthNeverDeletesUserSession verifies the cross-station safety of
// the credential-generation cleanup: a normal user's session token presented
// to the administrator station is rejected, and its row is never deleted by
// the administrator auth path's stale-cred_gen cleanup. User sessions carry
// the empty cred_gen sentinel, so the cleanup predicate excludes them.
func TestAdminAuthNeverDeletesUserSession(t *testing.T) {
	st := authTestStore(t)
	subkey := bytes.Repeat([]byte{0x74}, secret.SubkeyBytes)
	t.Cleanup(func() { clear(subkey) })

	user, err := st.CreateDiscordUser("discord-cross-station", "alice", "")
	if err != nil {
		t.Fatalf("CreateDiscordUser: %v", err)
	}
	userToken, _, err := st.CreateUserSession(user.ID)
	if err != nil {
		t.Fatalf("CreateUserSession: %v", err)
	}

	admin, err := NewAdminAuth(AdminAuthConfig{
		Store: st, Username: "root", Password: "admin-password",
		CredGenSubkey: subkey, SiteBaseURL: "https://example.com",
	})
	if err != nil {
		t.Fatalf("NewAdminAuth: %v", err)
	}
	defer admin.Close()

	req := stationRequest(http.MethodGet, "https://admin.example.com/admin/api/session", host.StationAdmin, nil)
	req.AddCookie(&http.Cookie{Name: AdminSessionCookieName, Value: userToken})
	rec := httptest.NewRecorder()
	admin.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("administrator station accepted a user session token")
	})).ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("cross-station status=%d body=%s; want 401", rec.Code, rec.Body.String())
	}

	// The user's own session must still authenticate normally afterwards.
	resolved, err := st.AuthenticateUserSession(userToken)
	if err != nil || resolved == nil || resolved.ID != user.ID {
		t.Fatalf("user session was invalidated by the admin auth path: %#v err=%v", resolved, err)
	}
}

// TestAdminAuthRejectsMalformedCredGenSubkey guards the wiring contract: a
// non-empty subkey of the wrong length fails closed instead of silently
// disabling the fingerprint check.
func TestAdminAuthRejectsMalformedCredGenSubkey(t *testing.T) {
	st := authTestStore(t)
	bad := bytes.Repeat([]byte{0x73}, secret.SubkeyBytes-1)
	t.Cleanup(func() { clear(bad) })
	if _, err := NewAdminAuth(AdminAuthConfig{
		Store: st, Username: "root", Password: "password",
		CredGenSubkey: bad, SiteBaseURL: "https://example.com",
	}); !errors.Is(err, ErrProviderUnavailable) {
		t.Fatalf("short subkey returned %v, want ErrProviderUnavailable", err)
	}
}
