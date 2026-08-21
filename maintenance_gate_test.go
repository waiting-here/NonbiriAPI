package main

// Integration coverage for the server-side maintenance gate and the error
// source attribution. These build the real application over a temporary
// database and drive the full handler chain — host edge, maintenance gate,
// auth, business — so the route matrix, live apply, restart-from-DB and
// runtime revert are verified through the same singletons used in production.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/adminapi"
	"github.com/waiting-here/NonbiriAPI/internal/config"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbtest"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

// maintenanceApp builds a real application and provisions a user (with a
// fresh session and caller key minted on demand by callers) plus an admin
// session cookie, so the route matrix can exercise already-issued credentials
// under both gate states.
func maintenanceApp(t *testing.T) (app *application, store *db.Store, user *db.User, adminSession string) {
	t.Helper()
	key := bytes.Repeat([]byte{0x71}, secret.MasterKeyBytes)
	vault, err := secret.New(key)
	clear(key)
	if err != nil {
		t.Fatalf("secret.New: %v", err)
	}
	cfg := &config.Config{
		ListenAddr: "127.0.0.1:19077", UserHost: auditUserHost, AdminHost: auditAdminHost,
		SiteBaseURL: "https://" + auditUserHost, TrustedProxyCIDRs: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")},
		AdminUsername: "root", AdminPassword: "correct-horse-battery", DiscordClientID: "client-id", DiscordClientSecret: "client-secret",
	}
	dbPath := filepath.Join(t.TempDir(), "maintenance-gate.db")
	dbtest.EnsureOwnerOnlyParent(t, dbPath)
	store, err = db.Open(dbPath, vault)
	if err != nil {
		_ = vault.Close()
		t.Fatalf("db.Open: %v", err)
	}
	app, err = buildApplication(cfg, store, vault)
	if err != nil {
		_ = store.Close()
		_ = vault.Close()
		t.Fatalf("buildApplication: %v", err)
	}
	t.Cleanup(func() {
		_ = app.Close()
		_ = store.Close()
		_ = vault.Close()
	})

	user, err = store.CreateUser("discord-maintenance", "maintenance-user", "")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	adminSession = loginAdmin(t, app)
	return app, store, user, adminSession
}

// freshSession mints a brand-new user session for one request, so a case that
// invalidates credentials (logout, caller-key regenerate) cannot perturb later
// cases that share the same user.
func freshSession(t *testing.T, store *db.Store, userID int64) string {
	t.Helper()
	token, _, err := store.CreateUserSession(userID)
	if err != nil {
		t.Fatalf("CreateUserSession: %v", err)
	}
	return token
}

// freshCallerKey mints a brand-new caller key for one request.
func freshCallerKey(t *testing.T, store *db.Store, userID int64) string {
	t.Helper()
	gen, err := store.RegenerateCallerKey(userID)
	if err != nil {
		t.Fatalf("RegenerateCallerKey: %v", err)
	}
	return gen.Secret
}

func loginAdmin(t *testing.T, app *application) string {
	t.Helper()
	req := auditRequest(http.MethodPost, auditAdminHost, "/admin/api/login", `{"username":"root","password":"correct-horse-battery"}`)
	rec := auditDo(t, app, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin login status=%d body=%q", rec.Code, rec.Body.String())
	}
	return auditSetCookie(rec.Result().Cookies(), "nb_admin_session")
}

func patchSiteConfig(t *testing.T, app *application, adminSession, keyName string, value any) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"value": value})
	req := auditRequest(http.MethodPatch, auditAdminHost, "/admin/api/site-config/"+keyName, string(body))
	req.AddCookie(&http.Cookie{Name: "nb_admin_session", Value: adminSession, Path: "/admin"})
	return auditDo(t, app, req)
}

func assertMaintenanceRefusal(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d want 503; body=%q", rec.Code, rec.Body.String())
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("Cache-Control=%q want no-store", cc)
	}
	var env struct {
		Error struct {
			Code   string `json:"code"`
			Source string `json:"source"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v body=%q", err, rec.Body.String())
	}
	if env.Error.Code != "service_unavailable" {
		t.Fatalf("code=%q want service_unavailable", env.Error.Code)
	}
	if env.Error.Source != "platform" {
		t.Fatalf("source=%q want platform", env.Error.Source)
	}
}

// reqOn builds a request to the given user/admin station path with optional
// session cookie or caller-key bearer, matching the audit edge's browser
// metadata expectations for unsafe methods. body is sent verbatim (empty for
// GET / no-body POST).
func reqOn(host, method, path, userSession, callerKey, body string) *http.Request {
	req := auditRequest(method, host, path, body)
	if userSession != "" {
		req.AddCookie(&http.Cookie{Name: "nb_user_session", Value: userSession, Path: "/api"})
	}
	if callerKey != "" {
		req.Header.Set("Authorization", "Bearer "+callerKey)
	}
	if body != "" && method != http.MethodGet && method != http.MethodHead && method != http.MethodOptions {
		if strings.HasPrefix(path, "/v1") {
			req.Header.Set("Content-Type", "application/json")
		}
	}
	return req
}

// TestMaintenanceGateRouteMatrix drives the full route matrix with
// already-issued credentials in both gate states: off keeps the original
// behavior; on refuses every user-station /api and /v1 path except the public
// config and logout allowlist, while the admin station, healthz and public
// config remain available so an operator can toggle maintenance off.
func TestMaintenanceGateRouteMatrix(t *testing.T) {
	app, store, user, adminSession := maintenanceApp(t)

	// Cases that must keep their original behavior with the gate OFF, and be
	// refused (503) with the gate ON. allow=true marks the allowlist entries
	// that remain available even when the gate is ON. sess/bearer request a
	// fresh session cookie / caller key minted per request so a case that
	// invalidates credentials (logout, regenerate) cannot perturb others.
	cases := []struct {
		name      string
		host      string
		method    string
		path      string
		body      string
		sess      bool
		bearer    bool
		adminSess bool
		allow     bool
		offWant   int
	}{
		// healthz: mounted outside the gate, available on both stations.
		{`user healthz`, auditUserHost, http.MethodGet, "/healthz", "", false, false, false, true, http.StatusOK},
		{`admin healthz`, auditAdminHost, http.MethodGet, "/healthz", "", false, false, false, true, http.StatusOK},
		// public config: allowlist (the SPA must learn maintenance is on).
		{`user public config`, auditUserHost, http.MethodGet, "/api/config", "", false, false, false, true, http.StatusOK},
		{`admin public config`, auditAdminHost, http.MethodGet, "/admin/api/config", "", false, false, false, true, http.StatusOK},
		// logout: allowlist (an already-logged-in user can end their session).
		{`user logout`, auditUserHost, http.MethodPost, "/api/auth/logout", "", true, false, false, true, http.StatusNoContent},
		// already-issued user session: refused when on.
		{`user session`, auditUserHost, http.MethodGet, "/api/session", "", true, false, false, false, http.StatusOK},
		{`user me`, auditUserHost, http.MethodGet, "/api/me", "", true, false, false, false, http.StatusOK},
		{`user endpoints`, auditUserHost, http.MethodGet, "/api/endpoints", "", true, false, false, false, http.StatusOK},
		{`user me/usage`, auditUserHost, http.MethodGet, "/api/me/usage", "", true, false, false, false, http.StatusOK},
		// caller key (already issued): refused when on. The chat body is a valid
		// OpenAI request so the OFF path reaches model resolution (404) rather
		// than a body-decode 400; ON never reads the body (the gate refuses first).
		{`v1 models`, auditUserHost, http.MethodGet, "/v1/models", "", false, true, false, false, http.StatusOK},
		{`v1 chat completions`, auditUserHost, http.MethodPost, "/v1/chat/completions", `{"model":"p/m","messages":[]}`, false, true, false, false, http.StatusNotFound},
		// OAuth start/callback/elevate and caller-key management: refused when on.
		{`oauth start`, auditUserHost, http.MethodGet, "/api/auth/discord/start", "", false, false, false, false, -1},
		{`oauth callback`, auditUserHost, http.MethodGet, "/api/auth/discord/callback?code=x&state=y", "", false, false, false, false, http.StatusBadRequest},
		{`auth elevate`, auditUserHost, http.MethodPost, "/api/auth/elevate", "", true, false, false, false, -1},
		{`caller-key`, auditUserHost, http.MethodPost, "/api/caller-key/regenerate", "", true, false, false, false, http.StatusOK},
		// admin station: never routed through the gate (admin session required).
		{`admin session`, auditAdminHost, http.MethodGet, "/admin/api/session", "", false, false, true, true, http.StatusOK},
		{`admin site-config`, auditAdminHost, http.MethodGet, "/admin/api/site-config", "", false, false, true, true, http.StatusOK},
		{`admin users`, auditAdminHost, http.MethodGet, "/admin/api/users", "", false, false, true, true, http.StatusOK},
	}

	run := func(tc struct {
		name      string
		host      string
		method    string
		path      string
		body      string
		sess      bool
		bearer    bool
		adminSess bool
		allow     bool
		offWant   int
	}) *httptest.ResponseRecorder {
		session, bearer := "", ""
		if tc.sess {
			session = freshSession(t, store, user.ID)
		}
		if tc.bearer {
			bearer = freshCallerKey(t, store, user.ID)
		}
		req := reqOn(tc.host, tc.method, tc.path, session, bearer, tc.body)
		if tc.adminSess {
			req.AddCookie(&http.Cookie{Name: "nb_admin_session", Value: adminSession, Path: "/admin"})
		}
		return auditDo(t, app, req)
	}

	// OFF: assert the original behavior for every case.
	for _, tc := range cases {
		rec := run(tc)
		if tc.offWant == -1 {
			if rec.Code == http.StatusServiceUnavailable {
				t.Fatalf("off %s: unexpected 503 body=%q", tc.name, rec.Body.String())
			}
			continue
		}
		if rec.Code != tc.offWant {
			t.Fatalf("off %s: status=%d want=%d body=%q", tc.name, rec.Code, tc.offWant, rec.Body.String())
		}
	}

	// ON: live-apply via the admin PATCH path.
	if rec := patchSiteConfig(t, app, adminSession, adminapi.KeyMaintenanceMode, true); rec.Code != http.StatusOK {
		t.Fatalf("enable maintenance status=%d body=%q", rec.Code, rec.Body.String())
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := run(tc)
			if tc.allow {
				// Allowlist / admin / healthz keep their original behavior.
				if tc.offWant == -1 {
					if rec.Code == http.StatusServiceUnavailable {
						t.Fatalf("on allow %s: unexpectedly refused 503", tc.name)
					}
					return
				}
				if rec.Code != tc.offWant {
					t.Fatalf("on allow %s: status=%d want=%d body=%q", tc.name, rec.Code, tc.offWant, rec.Body.String())
				}
				return
			}
			// Everything else is a stable 503 platform envelope, regardless of
			// whether the request carried a valid session or caller key.
			assertMaintenanceRefusal(t, rec)
		})
	}

	// The admin station can still toggle maintenance off (the gate never
	// applies to /admin/api/*).
	if rec := patchSiteConfig(t, app, adminSession, adminapi.KeyMaintenanceMode, false); rec.Code != http.StatusOK {
		t.Fatalf("disable maintenance status=%d body=%q", rec.Code, rec.Body.String())
	}
	// OFF again: original behavior resumes immediately for already-issued
	// credentials (no restart needed).
	if rec := auditDo(t, app, reqOn(auditUserHost, http.MethodGet, "/api/session", freshSession(t, store, user.ID), "", "")); rec.Code != http.StatusOK {
		t.Fatalf("after disable session: status=%d body=%q", rec.Code, rec.Body.String())
	}
	if rec := auditDo(t, app, reqOn(auditUserHost, http.MethodGet, "/v1/models", "", freshCallerKey(t, store, user.ID), "")); rec.Code != http.StatusOK {
		t.Fatalf("after disable v1: status=%d body=%q", rec.Code, rec.Body.String())
	}
}

// TestMaintenanceGateLoadsFromDBOnStartup proves a restart observes the
// persisted maintenance_mode: a fresh application built over a store with
// maintenance_mode=1 refuses user-station API before any PATCH.
func TestMaintenanceGateLoadsFromDBOnStartup(t *testing.T) {
	key := bytes.Repeat([]byte{0x72}, secret.MasterKeyBytes)
	vault, err := secret.New(key)
	clear(key)
	if err != nil {
		t.Fatalf("secret.New: %v", err)
	}
	dbPath := filepath.Join(t.TempDir(), "maintenance-restart.db")
	dbtest.EnsureOwnerOnlyParent(t, dbPath)
	store, err := db.Open(dbPath, vault)
	if err != nil {
		_ = vault.Close()
		t.Fatalf("db.Open: %v", err)
	}
	if err := store.SetSiteConfigValue(adminapi.KeyMaintenanceMode, "1"); err != nil {
		_ = store.Close()
		_ = vault.Close()
		t.Fatalf("seed maintenance_mode: %v", err)
	}
	cfg := &config.Config{
		ListenAddr: "127.0.0.1:19078", UserHost: auditUserHost, AdminHost: auditAdminHost,
		SiteBaseURL: "https://" + auditUserHost, TrustedProxyCIDRs: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")},
		AdminUsername: "root", AdminPassword: "correct-horse-battery", DiscordClientID: "client-id", DiscordClientSecret: "client-secret",
	}
	app, err := buildApplication(cfg, store, vault)
	if err != nil {
		_ = store.Close()
		_ = vault.Close()
		t.Fatalf("buildApplication: %v", err)
	}
	t.Cleanup(func() {
		_ = app.Close()
		_ = store.Close()
		_ = vault.Close()
	})

	// Public config still works (allowlist) and reports maintenance on.
	rec := auditDo(t, app, auditRequest(http.MethodGet, auditUserHost, "/api/config", ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("/api/config status=%d body=%q", rec.Code, rec.Body.String())
	}
	var cfgBody map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &cfgBody); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	if cfgBody["maintenance_mode"] != true {
		t.Fatalf("maintenance_mode=%v want true", cfgBody["maintenance_mode"])
	}
	// A user-station business path is refused on the very first request.
	rec = auditDo(t, app, auditRequest(http.MethodGet, auditUserHost, "/api/session", ""))
	assertMaintenanceRefusal(t, rec)
	// /v1 is refused too.
	rec = auditDo(t, app, auditRequest(http.MethodGet, auditUserHost, "/v1/models", ""))
	assertMaintenanceRefusal(t, rec)
}

// TestMaintenanceGateAdminRecoveryWhenEnabled proves that with maintenance on,
// the admin station is fully usable (login, read config, toggle off) — the
// gate can never lock out the operator.
func TestMaintenanceGateAdminRecoveryWhenEnabled(t *testing.T) {
	app, _, _, adminSession := maintenanceApp(t)
	if rec := patchSiteConfig(t, app, adminSession, adminapi.KeyMaintenanceMode, true); rec.Code != http.StatusOK {
		t.Fatalf("enable status=%d body=%q", rec.Code, rec.Body.String())
	}
	// Admin login still works (fresh session, no pre-existing cookie).
	fresh := loginAdmin(t, app)
	// Admin can read config (with the fresh admin session) and toggle
	// maintenance off — the gate never applies to /admin/api/*.
	req := auditRequest(http.MethodGet, auditAdminHost, "/admin/api/site-config", "")
	req.AddCookie(&http.Cookie{Name: "nb_admin_session", Value: fresh, Path: "/admin"})
	rec := auditDo(t, app, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin site-config status=%d body=%q", rec.Code, rec.Body.String())
	}
	rec = patchSiteConfig(t, app, fresh, adminapi.KeyMaintenanceMode, false)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin disable status=%d body=%q", rec.Code, rec.Body.String())
	}
	// User station is reachable again.
	rec = auditDo(t, app, auditRequest(http.MethodGet, auditUserHost, "/api/config", ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("user config after recovery status=%d body=%q", rec.Code, rec.Body.String())
	}
}

// TestMaintenanceGateConcurrentToggleAndTraffic drives concurrent toggles and
// concurrent user-station traffic through the real gate so the -race detector
// covers the live toggle vs. inflight read. Every response is a valid terminal
// state (admitted or 503); the gate never panics or produces a partial envelope.
func TestMaintenanceGateConcurrentToggleAndTraffic(t *testing.T) {
	app, store, user, adminSession := maintenanceApp(t)

	userSession := freshSession(t, store, user.ID)
	callerKey := freshCallerKey(t, store, user.ID)

	// doReq is a non-asserting request runner (testing.T.Fatalf must only be
	// called from the test goroutine, so the workers use Errorf / status checks).
	doReq := func(method, path string) int {
		req := reqOn(auditUserHost, method, path, userSession, callerKey, "")
		rec := httptest.NewRecorder()
		app.handler.ServeHTTP(rec, req)
		return rec.Code
	}
	toggle := func(on bool) {
		body, _ := json.Marshal(map[string]any{"value": on})
		req := auditRequest(http.MethodPatch, auditAdminHost, "/admin/api/site-config/"+adminapi.KeyMaintenanceMode, string(body))
		req.AddCookie(&http.Cookie{Name: "nb_admin_session", Value: adminSession, Path: "/admin"})
		rec := httptest.NewRecorder()
		app.handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("toggle %v status=%d", on, rec.Code)
		}
	}

	var wg sync.WaitGroup
	// Toggler: flips maintenance on/off a bounded number of times through the
	// admin path. It is bounded (not channel-stopped) so wg.Wait() can observe
	// its completion deterministically alongside the traffic workers.
	wg.Add(1)
	go func() {
		defer wg.Done()
		on := false
		for k := 0; k < 400; k++ {
			on = !on
			toggle(on)
		}
	}()
	// Traffic: hammer user-station paths that cross the gate.
	paths := []struct {
		method, path string
	}{
		{http.MethodGet, "/api/session"},
		{http.MethodGet, "/v1/models"},
		{http.MethodGet, "/api/config"},
		{http.MethodPost, "/api/auth/logout"},
	}
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			for j := 0; j < 150; j++ {
				p := paths[(j+seed)%len(paths)]
				switch code := doReq(p.method, p.path); code {
				case http.StatusOK, http.StatusNoContent, http.StatusUnauthorized,
					http.StatusServiceUnavailable, http.StatusNotFound:
				default:
					t.Errorf("unexpected status %d on %s", code, p.path)
				}
			}
		}(i)
	}
	wg.Wait()
	// Final disable so cleanup is deterministic.
	toggle(false)
}
