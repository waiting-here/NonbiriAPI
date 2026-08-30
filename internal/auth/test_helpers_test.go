package auth

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbtest"
	"github.com/waiting-here/NonbiriAPI/internal/elevation"
	"github.com/waiting-here/NonbiriAPI/internal/host"
	"github.com/waiting-here/NonbiriAPI/internal/maintenance"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

const authTestNow = int64(1700000000)

type testClock struct {
	mu    sync.Mutex
	value time.Time
}

func (c *testClock) Now() time.Time      { c.mu.Lock(); defer c.mu.Unlock(); return c.value }
func (c *testClock) Add(d time.Duration) { c.mu.Lock(); c.value = c.value.Add(d); c.mu.Unlock() }

type testGate struct {
	enabled bool
	ready   bool
}

func (g *testGate) State() (maintenance.State, bool) {
	return maintenance.State{Enabled: g.enabled, Revision: 1, ChangedAt: authTestNow}, g.ready
}

type fakeProvider struct {
	mu               sync.Mutex
	requests         []DiscordAuthorizeRequest
	logins           map[string]DiscordLogin
	authorizationURL string
	authorizationErr error
	exchangeErr      error
}

func (p *fakeProvider) AuthorizationURL(_ context.Context, r DiscordAuthorizeRequest) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.requests = append(p.requests, r)
	if p.authorizationErr != nil {
		return "", p.authorizationErr
	}
	if p.authorizationURL != "" {
		return p.authorizationURL, nil
	}
	v := url.Values{"state": []string{r.State}}
	return "https://identity.example/authorize?" + v.Encode(), nil
}
func (p *fakeProvider) Exchange(_ context.Context, code, _ string) (DiscordLogin, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.exchangeErr != nil {
		return DiscordLogin{}, p.exchangeErr
	}
	login, ok := p.logins[code]
	if !ok {
		return DiscordLogin{}, ErrProviderUnauthorized
	}
	return login, nil
}

type runtimeFixture struct {
	runtime  *Runtime
	store    *db.Store
	vault    *secret.Vault
	provider *fakeProvider
	gate     *testGate
	clock    *testClock
}

func newRuntimeFixture(t *testing.T, configure func(*RuntimeConfig)) *runtimeFixture {
	t.Helper()
	key := bytes.Repeat([]byte{0x43}, secret.MasterKeyBytes)
	vault, err := secret.New(key)
	clear(key)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "auth.db")
	dbtest.EnsureOwnerOnlyParent(t, path)
	store, err := db.Open(path, vault)
	if err != nil {
		_ = vault.Close()
		t.Fatalf("open db: %v", err)
	}
	for key, value := range map[string]string{"maintenance_mode": "0", "registration_open": "1", "discord_guild_id": "guild-1", "discord_role_id": "role-1"} {
		if _, err := store.DB().Exec(`UPDATE site_config SET value=?,updated_at=? WHERE key=?`, value, authTestNow, key); err != nil {
			t.Fatalf("configure %s: %v", key, err)
		}
	}
	clock := &testClock{value: time.Unix(authTestNow, 0)}
	states, err := NewStateManagerWithKey(bytes.Repeat([]byte{0x31}, 32), DefaultOAuthStateTTL)
	if err != nil {
		t.Fatal(err)
	}
	if err := states.SetClock(clock.Now); err != nil {
		t.Fatal(err)
	}
	elev, err := elevation.NewManagerWithKey(bytes.Repeat([]byte{0x52}, 32), 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := elev.SetClock(clock.Now); err != nil {
		t.Fatal(err)
	}
	provider := &fakeProvider{logins: map[string]DiscordLogin{}}
	gate := &testGate{ready: true}
	config := RuntimeConfig{Store: store, Provider: provider, DiscordClientID: "client", UserSiteBaseURL: "https://user.example", AdminUsername: "operator", AdminPassword: "correct horse battery staple", CredentialKeyDeriver: vault, Maintenance: gate, OAuthStates: states, Elevation: elev, Now: clock.Now}
	config.Authorizer = authz.New(authz.Options{Now: clock.Now, Elevation: elev})
	if configure != nil {
		configure(&config)
	}
	runtime, err := NewRuntime(config)
	if err != nil {
		_ = states.Close()
		_ = elev.Close()
		_ = store.Close()
		_ = vault.Close()
		t.Fatalf("new runtime: %v", err)
	}
	fixture := &runtimeFixture{runtime: runtime, store: store, vault: vault, provider: provider, gate: gate, clock: clock}
	t.Cleanup(func() { _ = runtime.Close(); _ = store.Close(); _ = vault.Close() })
	return fixture
}

func request(t *testing.T, handler http.Handler, station host.Station, method, target, body string, cookies []*http.Cookie, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, bytes.NewBufferString(body))
	req.RemoteAddr = "192.0.2.10:4567"
	req = req.WithContext(host.WithStation(req.Context(), station))
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func responseCookie(t *testing.T, rec *httptest.ResponseRecorder, name string) *http.Cookie {
	t.Helper()
	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == name {
			return cookie
		}
	}
	t.Fatalf("response lacks cookie %s: %s", name, rec.Body.String())
	return nil
}

func addLogin(p *fakeProvider, code, id string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, exists := p.logins[code]; exists {
		return
	}
	p.logins[code] = DiscordLogin{Identity: DiscordIdentity{ID: id, Username: "Alice", Avatar: "avatar-hash"}, GuildMember: func(context.Context, string) (GuildMember, error) {
		return GuildMember{Nick: "Guild Alice", Avatar: "guild-avatar", Roles: []string{"role-1"}}, nil
	}}
}

func loginUser(t *testing.T, f *runtimeFixture, code, target string) *http.Cookie {
	t.Helper()
	addLogin(f.provider, code, "discord-1")
	start := request(t, f.runtime.UserHandler(), host.StationUser, http.MethodGet, "https://user.example/api/auth/discord/start"+target, "", nil, nil)
	if start.Code != http.StatusFound {
		t.Fatalf("start status=%d body=%s", start.Code, start.Body.String())
	}
	stateCookie := responseCookie(t, start, OAuthStateCookieName)
	location, err := url.Parse(start.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	state := location.Query().Get("state")
	callback := request(t, f.runtime.UserHandler(), host.StationUser, http.MethodGet, "https://user.example/api/auth/discord/callback?code="+url.QueryEscape(code)+"&state="+url.QueryEscape(state), "", []*http.Cookie{stateCookie}, nil)
	if callback.Code != http.StatusFound {
		t.Fatalf("callback status=%d body=%s", callback.Code, callback.Body.String())
	}
	return responseCookie(t, callback, UserSessionCookieName)
}
