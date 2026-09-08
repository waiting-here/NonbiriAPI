package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/auth"
	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbfixture"
	"github.com/waiting-here/NonbiriAPI/internal/maintenance"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
	"github.com/waiting-here/NonbiriAPI/internal/timeapi"
)

type timeLoginProvider struct{}

func (timeLoginProvider) AuthorizationURL(_ context.Context, request auth.DiscordAuthorizeRequest) (string, error) {
	return "https://identity.example/authorize?" + url.Values{"state": {request.State}}.Encode(), nil
}

func (timeLoginProvider) Exchange(context.Context, string, string) (auth.DiscordLogin, error) {
	return auth.DiscordLogin{
		Identity: auth.DiscordIdentity{ID: "time-test-user", Username: "Time test user"},
		GuildMember: func(context.Context, string) (auth.GuildMember, error) {
			return auth.GuildMember{Nick: "Time test member", Roles: []string{"test-role"}}, nil
		},
	}, nil
}

type timeMaintenanceGate struct{ enabled bool }

func (gate *timeMaintenanceGate) State() (maintenance.State, bool) {
	return maintenance.State{Enabled: gate.enabled, Revision: 1}, true
}

func TestTimeRoutesThroughProductionStationAndSessionBoundary(t *testing.T) {
	key := bytes.Repeat([]byte{0x53}, secret.MasterKeyBytes)
	vault, err := secret.New(key)
	clear(key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = vault.Close() })
	path := filepath.Join(t.TempDir(), "time-http.db")
	dbfixture.Materialize(t, path)
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	for key, value := range map[string]string{"registration_open": "1", "maintenance_mode": "0", "discord_guild_id": "test-guild", "discord_role_id": "test-role"} {
		if _, err := store.DB().Exec(`UPDATE site_config SET value=? WHERE key=?`, value, key); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Unix(1781524800, 0)
	clock := func() time.Time { return now }
	gate := &timeMaintenanceGate{}
	cfg := testHTTPConfig()
	runtime, err := auth.NewRuntime(auth.RuntimeConfig{
		Store: store, Provider: timeLoginProvider{}, DiscordClientID: "test-client", UserSiteBaseURL: cfg.SiteBaseURL,
		AdminUsername: "operator", AdminPassword: "synthetic time test password", CredentialKeyDeriver: vault,
		Authorizer: authz.New(authz.Options{Now: clock}), Maintenance: gate, Now: clock,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	if err := timeapi.RegisterRoutes(runtime, resourceAdminRouteRegistrar{runtime: runtime}); err != nil {
		t.Fatal(err)
	}
	mux, err := generationTwoMux(cfg, store, runtime, http.NotFoundHandler())
	if err != nil {
		t.Fatal(err)
	}
	handler, err := stationBoundary(cfg, mux)
	if err != nil {
		t.Fatal(err)
	}
	request := func(method, hostname, path, body string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, "https://"+hostname+path, bytes.NewBufferString(body))
		req.RemoteAddr = "192.0.2.41:1234"
		if method != http.MethodGet {
			req.Header.Set("Origin", "https://"+hostname)
		}
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		for _, cookie := range cookies {
			req.AddCookie(cookie)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}
	cookie := func(response *httptest.ResponseRecorder, name string) *http.Cookie {
		t.Helper()
		for _, cookie := range response.Result().Cookies() {
			if cookie.Name == name {
				return cookie
			}
		}
		t.Fatalf("missing %s cookie, status=%d", name, response.Code)
		return nil
	}
	start := request(http.MethodGet, cfg.UserHost, "/api/auth/discord/start", "")
	if start.Code != http.StatusFound {
		t.Fatalf("login start: %d %s", start.Code, start.Body.String())
	}
	location, err := url.Parse(start.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	callback := request(http.MethodGet, cfg.UserHost, "/api/auth/discord/callback?"+url.Values{"code": {"time-login"}, "state": {location.Query().Get("state")}}.Encode(), "", cookie(start, auth.OAuthStateCookieName))
	if callback.Code != http.StatusFound {
		t.Fatalf("login callback: %d %s", callback.Code, callback.Body.String())
	}
	userCookie := cookie(callback, auth.UserSessionCookieName)
	adminLogin := request(http.MethodPost, cfg.AdminHost, "/admin/api/login", `{"username":"operator","password":"synthetic time test password"}`)
	if adminLogin.Code != http.StatusOK {
		t.Fatalf("admin login: %d %s", adminLogin.Code, adminLogin.Body.String())
	}
	adminCookie := cookie(adminLogin, auth.AdminSessionCookieName)
	for _, station := range []struct {
		name, hostname, prefix string
		cookie                 *http.Cookie
	}{
		{"user", cfg.UserHost, "/api", userCookie}, {"admin", cfg.AdminHost, "/admin/api", adminCookie},
	} {
		for _, endpoint := range []string{"/time-zones", "/time/resolve?local=2026-11-01T01:30:00&time_zone=America/New_York"} {
			t.Run(station.name+endpoint, func(t *testing.T) {
				for _, invalidCookies := range [][]*http.Cookie{nil, {{Name: station.cookie.Name, Value: "invalid"}}, {userCookie, userCookie, adminCookie, adminCookie}} {
					response := request(http.MethodGet, station.hostname, station.prefix+endpoint, "", invalidCookies...)
					if response.Code != http.StatusUnauthorized {
						t.Fatalf("invalid session: %d %s", response.Code, response.Body.String())
					}
				}
				response := request(http.MethodGet, station.hostname, station.prefix+endpoint, "", station.cookie)
				if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" {
					t.Fatalf("authenticated: %d %s", response.Code, response.Body.String())
				}
				if endpoint != "/time-zones" {
					var result struct {
						Instant    int64  `json:"instant"`
						Adjustment string `json:"adjustment"`
					}
					if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
						t.Fatal(err)
					}
					if result.Instant != 1793514600 || result.Adjustment != "fold_later" {
						t.Fatalf("resolved=%+v", result)
					}
				}
			})
		}
	}
	for _, test := range []struct {
		method, hostname, path string
		cookie                 *http.Cookie
		want                   int
	}{
		{http.MethodGet, cfg.UserHost, "/admin/api/time-zones", adminCookie, 404},
		{http.MethodGet, cfg.AdminHost, "/api/time-zones", userCookie, 404},
		{http.MethodGet, cfg.UserHost, "/api/time-zones", adminCookie, 401},
		{http.MethodGet, cfg.AdminHost, "/admin/api/time-zones", userCookie, 401},
		{http.MethodPost, cfg.UserHost, "/api/time-zones", userCookie, 405},
		{http.MethodPost, cfg.AdminHost, "/admin/api/time/resolve", adminCookie, 405},
		{http.MethodGet, "unrecognized.example", "/api/time-zones", userCookie, 400},
	} {
		response := request(test.method, test.hostname, test.path, "", test.cookie)
		if response.Code != test.want {
			t.Fatalf("%s %s%s=%d want=%d: %s", test.method, test.hostname, test.path, response.Code, test.want, response.Body.String())
		}
	}
	if _, err := store.DB().Exec(`UPDATE users SET level=5 WHERE discord_id='time-test-user'`); err != nil {
		t.Fatal(err)
	}
	if response := request(http.MethodGet, cfg.UserHost, "/api/time-zones", "", userCookie); response.Code != 200 {
		t.Fatalf("steward same-station route: %d", response.Code)
	}
	if _, err := store.DB().Exec(`UPDATE users SET is_banned=1 WHERE discord_id='time-test-user'`); err != nil {
		t.Fatal(err)
	}
	if response := request(http.MethodGet, cfg.UserHost, "/api/time-zones", "", userCookie); response.Code != 403 {
		t.Fatalf("banned route: %d", response.Code)
	}
	if _, err := store.DB().Exec(`UPDATE users SET is_banned=0 WHERE discord_id='time-test-user'`); err != nil {
		t.Fatal(err)
	}
	gate.enabled = true
	if response := request(http.MethodGet, cfg.UserHost, "/api/time-zones", "", userCookie); response.Code != 503 {
		t.Fatalf("maintenance user: %d", response.Code)
	}
	if response := request(http.MethodGet, cfg.AdminHost, "/admin/api/time-zones", "", adminCookie); response.Code != 200 {
		t.Fatalf("maintenance admin: %d", response.Code)
	}
	gate.enabled = false
	if _, err := store.DB().Exec(`UPDATE sessions SET expires_at=last_seen_at`); err != nil {
		t.Fatal(err)
	}
	for _, target := range []struct {
		hostname, path string
		cookie         *http.Cookie
	}{
		{cfg.UserHost, "/api/time-zones", userCookie}, {cfg.AdminHost, "/admin/api/time-zones", adminCookie},
	} {
		if response := request(http.MethodGet, target.hostname, target.path, "", target.cookie); response.Code != 401 {
			t.Fatalf("expired route %s: %d", target.hostname, response.Code)
		}
	}
}
