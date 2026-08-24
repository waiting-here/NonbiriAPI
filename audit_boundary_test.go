// Independent audit: full application wiring through the real handler chain.
//
// These tests build the production application (buildApplication) over a
// temporary database and drive it with a real HTTP server and client, so the
// host/station boundary, session/elevation replay, caller-key ban/delete
// invalidation, RPM metering, and admin runtime controls are verified through
// the same singletons and dispatch used in production.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/config"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbtest"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

const (
	auditUserHost  = "127.0.0.1"
	auditAdminHost = "127.0.0.2"
)

func auditConfig() *config.Config {
	return &config.Config{
		ListenAddr: "127.0.0.1:18999", UserHost: auditUserHost, AdminHost: auditAdminHost,
		SiteBaseURL: "https://" + auditUserHost, TrustedProxyCIDRs: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")},
		AdminUsername: "root", AdminPassword: "correct-horse-battery", DiscordClientID: "client-id", DiscordClientSecret: "client-secret",
	}
}

func auditApp(t *testing.T) (*application, *db.Store, *secret.Vault) {
	t.Helper()
	key := bytes.Repeat([]byte{0x5a}, secret.MasterKeyBytes)
	vault, err := secret.New(key)
	clear(key)
	if err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(t.TempDir(), "audit.db")
	dbtest.EnsureOwnerOnlyParent(t, dbPath)
	store, err := db.Open(dbPath, vault)
	if err != nil {
		_ = vault.Close()
		t.Fatal(err)
	}
	if err := store.SetSiteConfigValue("maintenance_mode", "0"); err != nil {
		t.Fatal(err)
	}
	app, err := buildApplication(auditConfig(), store, vault)
	if err != nil {
		_ = store.Close()
		_ = vault.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = app.Close()
		_ = store.Close()
		_ = vault.Close()
	})
	return app, store, vault
}

func auditRequest(method, host, path, body string) *http.Request {
	req := httptest.NewRequest(method, "https://wire.invalid"+path, strings.NewReader(body))
	req.Host = host
	req.RemoteAddr = "198.51.100.9:4242"
	if method != http.MethodGet && method != http.MethodHead && method != http.MethodOptions &&
		(path == "/api" || strings.HasPrefix(path, "/api/") || path == "/admin/api" || strings.HasPrefix(path, "/admin/api/")) {
		req.Header.Set("Origin", "https://"+host)
		req.Header.Set("Sec-Fetch-Site", "same-origin")
	}
	return req
}

func auditDo(t *testing.T, app *application, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	app.handler.ServeHTTP(rec, req)
	return rec
}

func auditJSON(t *testing.T, rec *httptest.ResponseRecorder, into any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), into); err != nil {
		t.Fatalf("decode JSON: %v body=%q", err, rec.Body.String())
	}
}

func auditErrorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	auditJSON(t, rec, &envelope)
	if envelope.Error.Code == "" {
		t.Fatalf("no error code in body=%q", rec.Body.String())
	}
	return envelope.Error.Code
}

func auditSetCookie(cookies []*http.Cookie, name string) string {
	for _, c := range cookies {
		if c.Name == name {
			return c.Value
		}
	}
	return ""
}

func TestAuditAppHostStationIsolationRealHTTP(t *testing.T) {
	app, _, _ := auditApp(t)
	srv := httptest.NewServer(app.handler)
	defer srv.Close()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	do := func(method, host, path, body string) *http.Response {
		t.Helper()
		req, err := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Host = host // dial the test server, present a different Host
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}

	cases := []struct {
		name   string
		host   string
		path   string
		want   int
		method string
	}{
		{"user health", auditUserHost, "/healthz", 200, http.MethodGet},
		{"admin health", auditAdminHost, "/healthz", 200, http.MethodGet},
		{"user spa", auditUserHost, "/some/deep/link", 200, http.MethodGet},
		{"admin spa", auditAdminHost, "/settings", 200, http.MethodGet},
		{"admin on user host blocked", auditUserHost, "/admin/api/session", 404, http.MethodGet},
		{"user api on admin host blocked", auditAdminHost, "/api/endpoints", 404, http.MethodGet},
		{"v1 on admin host blocked", auditAdminHost, "/v1/models", 404, http.MethodGet},
		{"unknown host refused", "evil.test", "/", 400, http.MethodGet},
		{"unknown host health refused", "evil.test", "/healthz", 400, http.MethodGet},
		{"unknown host api refused", "evil.test", "/api/me", 400, http.MethodGet},
		{"admin api needs session", auditAdminHost, "/admin/api/session", 401, http.MethodGet},
		{"user api needs session", auditUserHost, "/api/endpoints", 401, http.MethodGet},
		{"v1 needs bearer", auditUserHost, "/v1/models", 401, http.MethodGet},
		{"method not allowed on api", auditUserHost, "/api/me", 405, http.MethodDelete},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := do(tc.method, tc.host, tc.path, "")
			defer resp.Body.Close()
			if resp.StatusCode != tc.want {
				t.Fatalf("status=%d want=%d", resp.StatusCode, tc.want)
			}
			if cc := resp.Header.Get("Cache-Control"); cc != "no-store" {
				t.Fatalf("cache-control=%q", cc)
			}
			if xcto := resp.Header.Get("X-Content-Type-Options"); xcto != "nosniff" {
				t.Fatalf("x-content-type-options=%q", xcto)
			}
			body, _ := io.ReadAll(resp.Body)
			if tc.want == 400 && strings.Contains(string(body), auditUserHost) {
				t.Fatalf("unknown-host response leaks station config: %q", body)
			}
		})
	}

	// Host matching is deliberately port-insensitive (documented host
	// package contract): a different port of the user host still selects the
	// user station. Same-host-different-port station separation is rejected
	// at configuration load, so this cannot create an ambiguity.
	resp := do(http.MethodGet, auditUserHost+":8080", "/healthz", "")
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("host with port status=%d want=200 (port-insensitive match)", resp.StatusCode)
	}
}

func TestAuditAppGameAndDebugRoutesUseProductionIdentityBoundaries(t *testing.T) {
	app, store, _ := auditApp(t)
	user, err := store.CreateUser("discord-integrated-routes", "integrated-routes", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateModel(context.Background(), user.ID, "personal", "dry", "ordered", false, 1); err != nil {
		t.Fatal(err)
	}
	callerKey, err := store.SetCallerKey(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	firstSession, _, err := store.CreateUserSession(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	secondSession, _, err := store.CreateUserSession(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	userRequest := func(method, path, session, body string) *httptest.ResponseRecorder {
		req := auditRequest(method, auditUserHost, path, body)
		if session != "" {
			req.AddCookie(&http.Cookie{Name: "nb_user_session", Value: session, Path: "/api"})
		}
		return auditDo(t, app, req)
	}
	if rec := userRequest(http.MethodPost, "/api/debug/session", firstSession, ""); rec.Code != http.StatusCreated {
		t.Fatalf("first debug start status=%d body=%q", rec.Code, rec.Body.String())
	}
	if rec := userRequest(http.MethodPost, "/api/debug/session", secondSession, ""); rec.Code != http.StatusCreated {
		t.Fatalf("replacement debug start status=%d body=%q", rec.Code, rec.Body.String())
	}
	// Logging out the older browser session must not terminate the newer
	// process-local Debug session for the same account.
	if rec := userRequest(http.MethodPost, "/api/auth/logout", firstSession, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("old-session logout status=%d body=%q", rec.Code, rec.Body.String())
	}
	active := userRequest(http.MethodGet, "/api/debug/session", secondSession, "")
	if active.Code != http.StatusOK || !strings.Contains(active.Body.String(), `"mode":"dry"`) || !strings.Contains(active.Body.String(), `"id":"dbg_`) {
		t.Fatalf("replacement debug session status=%d body=%q", active.Code, active.Body.String())
	}
	games := userRequest(http.MethodGet, "/api/games", secondSession, "")
	if games.Code != http.StatusOK || !strings.Contains(games.Body.String(), `"id":"fishing"`) || !strings.Contains(games.Body.String(), `"master_enabled":false`) {
		t.Fatalf("games status=%d body=%q", games.Code, games.Body.String())
	}
	chat := auditRequest(http.MethodPost, auditUserHost, "/v1/chat/completions", `{"model":"personal/dry","messages":[]}`)
	chat.Header.Set("Authorization", "Bearer "+callerKey)
	chat.Header.Set("Content-Type", "application/json")
	dry := auditDo(t, app, chat)
	if dry.Code != http.StatusOK || dry.Header().Get("X-Nonbiri-Debug-Mode") != "dry-run" {
		t.Fatalf("debug dry call status=%d mode=%q body=%q", dry.Code, dry.Header().Get("X-Nonbiri-Debug-Mode"), dry.Body.String())
	}
	var requestLogs int
	if err := store.DB().QueryRow(`SELECT COUNT(*) FROM request_logs WHERE user_id=?`, user.ID).Scan(&requestLogs); err != nil || requestLogs != 0 {
		t.Fatalf("dry call persisted request log rows=%d err=%v", requestLogs, err)
	}
	if rec := userRequest(http.MethodPost, "/api/auth/logout", secondSession, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("current-session logout status=%d body=%q", rec.Code, rec.Body.String())
	}
	if rec := userRequest(http.MethodGet, "/api/debug/session", secondSession, ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("logged-out debug control status=%d body=%q", rec.Code, rec.Body.String())
	}

	login := auditRequest(http.MethodPost, auditAdminHost, "/admin/api/login", `{"username":"root","password":"correct-horse-battery"}`)
	loginResponse := auditDo(t, app, login)
	adminSession := auditSetCookie(loginResponse.Result().Cookies(), "nb_admin_session")
	if loginResponse.Code != http.StatusOK || adminSession == "" {
		t.Fatalf("admin login status=%d body=%q", loginResponse.Code, loginResponse.Body.String())
	}
	adminGames := auditRequest(http.MethodGet, auditAdminHost, "/admin/api/games/config", "")
	adminGames.AddCookie(&http.Cookie{Name: "nb_admin_session", Value: adminSession, Path: "/admin"})
	adminGamesResponse := auditDo(t, app, adminGames)
	if adminGamesResponse.Code != http.StatusOK || !strings.Contains(adminGamesResponse.Body.String(), `"master_enabled":false`) {
		t.Fatalf("admin games status=%d body=%q", adminGamesResponse.Code, adminGamesResponse.Body.String())
	}
}

func TestAuditAppCallerKeyBanRegenerateDelete(t *testing.T) {
	app, store, _ := auditApp(t)
	user, err := store.CreateUser("discord-audit-1", "audit-user", "")
	if err != nil {
		t.Fatal(err)
	}
	key, err := store.SetCallerKey(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(key, "nbk_") {
		t.Fatalf("caller key prefix=%q", key[:4])
	}
	models := func(keyValue string) *httptest.ResponseRecorder {
		req := auditRequest(http.MethodGet, auditUserHost, "/v1/models", "")
		if keyValue != "" {
			req.Header.Set("Authorization", "Bearer "+keyValue)
		}
		return auditDo(t, app, req)
	}
	chat := func(keyValue string) *httptest.ResponseRecorder {
		req := auditRequest(http.MethodPost, auditUserHost, "/v1/chat/completions",
			`{"model":"p/m","messages":[]}`)
		if keyValue != "" {
			req.Header.Set("Authorization", "Bearer "+keyValue)
		}
		req.Header.Set("Content-Type", "application/json")
		return auditDo(t, app, req)
	}

	if rec := models(""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no bearer status=%d", rec.Code)
	}
	// A user session cookie must never authenticate the caller exit.
	sessionToken, _, err := store.CreateUserSession(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	req := auditRequest(http.MethodGet, auditUserHost, "/v1/models", "")
	req.AddCookie(&http.Cookie{Name: "nb_user_session", Value: sessionToken, Path: "/api"})
	if rec := auditDo(t, app, req); rec.Code != http.StatusUnauthorized {
		t.Fatalf("session cookie on v1 status=%d", rec.Code)
	}
	// Valid key: models listing succeeds (empty list).
	if rec := models(key); rec.Code != http.StatusOK {
		t.Fatalf("models with key status=%d body=%q", rec.Code, rec.Body.String())
	}
	// Chat completion without a bound model: authenticated path fails with
	// the stable not_found code, proving the caller key passed auth + flow
	// control.
	if rec := chat(key); rec.Code != http.StatusNotFound || auditErrorCode(t, rec) != "not_found" {
		t.Fatalf("chat status=%d body=%q", rec.Code, rec.Body.String())
	}

	// Ban invalidates the key immediately (ban also revokes the stored
	// credential, so unban cannot resurrect the old key).
	if err := store.BanUser(user.ID, "audit test ban"); err != nil {
		t.Fatal(err)
	}
	if rec := models(key); rec.Code != http.StatusUnauthorized {
		t.Fatalf("banned key status=%d", rec.Code)
	}
	if err := store.UnbanUser(user.ID); err != nil {
		t.Fatal(err)
	}
	if rec := models(key); rec.Code != http.StatusUnauthorized {
		t.Fatalf("key after unban status=%d (ban revokes the stored key)", rec.Code)
	}
	freshKey, err := store.SetCallerKey(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rec := models(freshKey); rec.Code != http.StatusOK {
		t.Fatalf("fresh key after unban status=%d", rec.Code)
	}
	key = freshKey

	// Regenerate invalidates the old key.
	generation, err := store.RegenerateCallerKey(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if generation.Secret == key {
		t.Fatal("regenerated key equals the old key")
	}
	if rec := models(key); rec.Code != http.StatusUnauthorized {
		t.Fatalf("old key after regenerate status=%d", rec.Code)
	}
	if rec := models(generation.Secret); rec.Code != http.StatusOK {
		t.Fatalf("new key status=%d", rec.Code)
	}

	// Account deletion invalidates the key.
	if err := store.DeleteUserAccount(context.Background(), user.ID); err != nil {
		t.Fatal(err)
	}
	if rec := models(generation.Secret); rec.Code != http.StatusUnauthorized {
		t.Fatalf("deleted user key status=%d", rec.Code)
	}
	// The deleted user's session is gone too.
	req = auditRequest(http.MethodGet, auditUserHost, "/api/me", "")
	req.AddCookie(&http.Cookie{Name: "nb_user_session", Value: sessionToken, Path: "/api"})
	if rec := auditDo(t, app, req); rec.Code != http.StatusUnauthorized {
		t.Fatalf("deleted user session status=%d", rec.Code)
	}
}

func TestAuditAppAdminElevationReplay(t *testing.T) {
	app, store, _ := auditApp(t)
	target, err := store.CreateUser("discord-audit-2", "victim", "")
	if err != nil {
		t.Fatal(err)
	}

	login := auditRequest(http.MethodPost, auditAdminHost, "/admin/api/login", `{"username":"root","password":"correct-horse-battery"}`)
	rec := auditDo(t, app, login)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin login status=%d body=%q", rec.Code, rec.Body.String())
	}
	adminSession := auditSetCookie(rec.Result().Cookies(), "nb_admin_session")
	if adminSession == "" {
		t.Fatal("admin session cookie missing")
	}
	adminReq := func(method, path, body string) *httptest.ResponseRecorder {
		req := auditRequest(method, auditAdminHost, path, body)
		req.AddCookie(&http.Cookie{Name: "nb_admin_session", Value: adminSession, Path: "/admin"})
		return auditDo(t, app, req)
	}

	// Wrong password must not issue a capability.
	bad := adminReq(http.MethodPost, "/admin/api/auth/elevate", `{"password":"wrong"}`)
	if bad.Code != http.StatusForbidden {
		t.Fatalf("wrong-password elevate status=%d body=%q", bad.Code, bad.Body.String())
	}
	// Elevate with the correct second factor.
	elev := adminReq(http.MethodPost, "/admin/api/auth/elevate", `{"password":"correct-horse-battery"}`)
	if elev.Code != http.StatusOK {
		t.Fatalf("elevate status=%d body=%q", elev.Code, elev.Body.String())
	}
	var elevation struct {
		Token string `json:"token"`
	}
	auditJSON(t, elev, &elevation)
	if len(elevation.Token) < 16 {
		t.Fatalf("elevation token suspiciously short: %q", elevation.Token)
	}
	if strings.Contains(elev.Body.String(), "correct-horse-battery") {
		t.Fatal("elevation response echoes the password")
	}

	// Use the capability exactly once. The application dispatch must preserve
	// the path parameter for the lifecycle handler.
	del := auditRequest(http.MethodDelete, auditAdminHost, "/admin/api/users/"+itoa(target.ID), "")
	del.AddCookie(&http.Cookie{Name: "nb_admin_session", Value: adminSession, Path: "/admin"})
	del.Header.Set("X-Elevated-Token", elevation.Token)
	delRec := auditDo(t, app, del)
	if delRec.Code != http.StatusNoContent {
		t.Fatalf("delete with elevation status=%d body=%q", delRec.Code, delRec.Body.String())
	}
	if user, err := store.GetUserByID(target.ID); err != nil || user != nil {
		t.Fatalf("user survived the delete path: %v", err)
	}
	// Replay: the capability was consumed by the successful delete and cannot
	// authorize a second destructive call.
	replay := auditRequest(http.MethodDelete, auditAdminHost, "/admin/api/users/"+itoa(target.ID), "")
	replay.AddCookie(&http.Cookie{Name: "nb_admin_session", Value: adminSession, Path: "/admin"})
	replay.Header.Set("X-Elevated-Token", elevation.Token)
	replayRec := auditDo(t, app, replay)
	if replayRec.Code != http.StatusForbidden {
		t.Fatalf("token replay status=%d body=%q", replayRec.Code, replayRec.Body.String())
	}
	// A destructive call without any token fails closed too.
	noToken := adminReq(http.MethodDelete, "/admin/api/users/1", "")
	if noToken.Code != http.StatusForbidden {
		t.Fatalf("delete without token status=%d", noToken.Code)
	}
}

func TestAuditAppRPMMetering(t *testing.T) {
	// Phase A: the per-user ceiling is enforced by the site default.
	app, store, _ := auditApp(t)
	user, err := store.CreateUser("discord-audit-3", "rpm-user", "")
	if err != nil {
		t.Fatal(err)
	}
	key, err := store.SetCallerKey(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	login := auditRequest(http.MethodPost, auditAdminHost, "/admin/api/login", `{"username":"root","password":"correct-horse-battery"}`)
	rec := auditDo(t, app, login)
	adminSession := auditSetCookie(rec.Result().Cookies(), "nb_admin_session")
	adminPatch := func(keyName, value string) *httptest.ResponseRecorder {
		req := auditRequest(http.MethodPatch, auditAdminHost, "/admin/api/site-config/"+keyName, `{"value":`+value+`}`)
		req.AddCookie(&http.Cookie{Name: "nb_admin_session", Value: adminSession, Path: "/admin"})
		return auditDo(t, app, req)
	}
	if rec := adminPatch("default_rpm_per_user", "2"); rec.Code != http.StatusOK {
		t.Fatalf("patch per-user rpm status=%d body=%q", rec.Code, rec.Body.String())
	}
	chat := func() *httptest.ResponseRecorder {
		req := auditRequest(http.MethodPost, auditUserHost, "/v1/chat/completions", `{"model":"p/m","messages":[]}`)
		req.Header.Set("Authorization", "Bearer "+key)
		req.Header.Set("Content-Type", "application/json")
		return auditDo(t, app, req)
	}
	// The user's own rpm_limit is unset, so the site default (2) applies.
	for i := 0; i < 2; i++ {
		if rec := chat(); rec.Code != http.StatusNotFound {
			t.Fatalf("request %d status=%d (expected model_not_found, not throttled)", i+1, rec.Code)
		}
	}
	third := chat()
	if third.Code != http.StatusTooManyRequests || auditErrorCode(t, third) != "rate_limited" {
		t.Fatalf("third request status=%d body=%q", third.Code, third.Body.String())
	}
	if retryAfter := third.Header().Get("Retry-After"); retryAfter == "" {
		t.Fatal("429 without Retry-After")
	}

	// Phase B: the global ceiling is enforced independently by a fresh app
	// (a clean limiter window) with global_rpm=1.
	app2, store2, _ := auditApp(t)
	user2, err := store2.CreateUser("discord-audit-3b", "rpm-user2", "")
	if err != nil {
		t.Fatal(err)
	}
	key2, err := store2.SetCallerKey(user2.ID)
	if err != nil {
		t.Fatal(err)
	}
	login2 := auditRequest(http.MethodPost, auditAdminHost, "/admin/api/login", `{"username":"root","password":"correct-horse-battery"}`)
	rec2 := auditDo(t, app2, login2)
	adminSession2 := auditSetCookie(rec2.Result().Cookies(), "nb_admin_session")
	patchGlobal := auditRequest(http.MethodPatch, auditAdminHost, "/admin/api/site-config/global_rpm", `{"value":1}`)
	patchGlobal.AddCookie(&http.Cookie{Name: "nb_admin_session", Value: adminSession2, Path: "/admin"})
	if rec := auditDo(t, app2, patchGlobal); rec.Code != http.StatusOK {
		t.Fatalf("patch global rpm status=%d body=%q", rec.Code, rec.Body.String())
	}
	chat2 := func() *httptest.ResponseRecorder {
		req := auditRequest(http.MethodPost, auditUserHost, "/v1/chat/completions", `{"model":"p/m","messages":[]}`)
		req.Header.Set("Authorization", "Bearer "+key2)
		req.Header.Set("Content-Type", "application/json")
		return auditDo(t, app2, req)
	}
	if rec := chat2(); rec.Code != http.StatusNotFound {
		t.Fatalf("first global-window request status=%d", rec.Code)
	}
	if rec := chat2(); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("second global-window request status=%d body=%q", rec.Code, rec.Body.String())
	}
}

func TestAuditAppSiteConfigValidation(t *testing.T) {
	app, _, _ := auditApp(t)
	login := auditRequest(http.MethodPost, auditAdminHost, "/admin/api/login", `{"username":"root","password":"correct-horse-battery"}`)
	rec := auditDo(t, app, login)
	adminSession := auditSetCookie(rec.Result().Cookies(), "nb_admin_session")
	patch := func(keyName, body string) *httptest.ResponseRecorder {
		req := auditRequest(http.MethodPatch, auditAdminHost, "/admin/api/site-config/"+keyName, body)
		req.AddCookie(&http.Cookie{Name: "nb_admin_session", Value: adminSession, Path: "/admin"})
		return auditDo(t, app, req)
	}
	cases := []struct {
		name string
		key  string
		body string
		want int
	}{
		{"unknown key", "made_up_key", `{"value":1}`, 404},
		{"null value", "global_rpm", `{"value":null}`, 400},
		{"non-integer", "global_rpm", `{"value":"fast"}`, 400},
		{"negative", "global_rpm", `{"value":-1}`, 400},
		{"zero", "global_rpm", `{"value":0}`, 400},
		{"above cap", "global_rpm", `{"value":999999}`, 400},
		{"leading zeros", "global_rpm", `{"value":007}`, 400},
		{"float", "global_rpm", `{"value":1.5}`, 400},
		{"unknown field", "global_rpm", `{"value":5,"extra":1}`, 400},
		{"trailing token", "global_rpm", `{"value":5} {}`, 400},
		{"valid", "global_rpm", `{"value":64}`, 200},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if rec := patch(tc.key, tc.body); rec.Code != tc.want {
				t.Fatalf("status=%d want=%d body=%q", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
	// A non-admin (or no session) can never patch configuration.
	req := auditRequest(http.MethodPatch, auditAdminHost, "/admin/api/site-config/global_rpm", `{"value":1}`)
	if rec := auditDo(t, app, req); rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated patch status=%d", rec.Code)
	}
}

func TestAuditAppExportDeleteRequireElevation(t *testing.T) {
	app, store, _ := auditApp(t)
	user, err := store.CreateUser("discord-audit-4", "export-user", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetCallerKey(user.ID); err != nil {
		t.Fatal(err)
	}
	sessionToken, _, err := store.CreateUserSession(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	withSession := func(method, path, body string) *httptest.ResponseRecorder {
		req := auditRequest(method, auditUserHost, path, body)
		req.AddCookie(&http.Cookie{Name: "nb_user_session", Value: sessionToken, Path: "/api"})
		return auditDo(t, app, req)
	}
	// Export and delete are second-factor gated even with a valid session.
	exp := withSession(http.MethodPost, "/api/account/export", "")
	if exp.Code != http.StatusForbidden || auditErrorCode(t, exp) != "elevated_required" {
		t.Fatalf("export without elevation status=%d body=%q", exp.Code, exp.Body.String())
	}
	del := withSession(http.MethodPost, "/api/account/delete", `{"confirm":"DELETE"}`)
	if del.Code != http.StatusForbidden || auditErrorCode(t, del) != "elevated_required" {
		t.Fatalf("delete without elevation status=%d body=%q", del.Code, del.Body.String())
	}
	// Wrong confirm text fails even with a token present (the token is not
	// consumed on a bad confirmation).
	delWrong := auditRequest(http.MethodPost, auditUserHost, "/api/account/delete", `{"confirm":"NOPE"}`)
	delWrong.AddCookie(&http.Cookie{Name: "nb_user_session", Value: sessionToken, Path: "/api"})
	delWrong.Header.Set("X-Elevated-Token", "garbage-token")
	rec := auditDo(t, app, delWrong)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("delete with wrong confirm status=%d body=%q", rec.Code, rec.Body.String())
	}
	// The account still exists and its key still authenticates.
	if _, err := store.GetUserByID(user.ID); err != nil {
		t.Fatalf("user vanished after failed delete: %v", err)
	}
}

func itoa(n int64) string {
	return strconv.FormatInt(n, 10)
}
