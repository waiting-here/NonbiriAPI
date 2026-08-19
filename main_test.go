package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/config"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
	"github.com/waiting-here/NonbiriAPI/internal/usage"
)

func testHTTPConfig() *config.Config {
	return &config.Config{
		UserHost:          "example.com",
		AdminHost:         "admin.example.com",
		SiteBaseURL:       "https://example.com",
		TrustedProxyCIDRs: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")},
	}
}

func testHTTPResponse(t *testing.T, h http.Handler, method, host, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, "http://wire.invalid"+path, nil)
	req.Host = host
	req.RemoteAddr = "198.51.100.20:4242"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestHTTPHandlerStationAndPathMatrix(t *testing.T) {
	h, err := newHTTPHandler(testHTTPConfig())
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name       string
		host       string
		path       string
		wantStatus int
		wantBody   string
		wantHTML   bool
	}{
		{name: "user health", host: "example.com", path: "/healthz", wantStatus: http.StatusOK, wantBody: `"status":"ok"`},
		{name: "admin health", host: "admin.example.com", path: "/healthz", wantStatus: http.StatusOK, wantBody: `"status":"ok"`},
		{name: "user spa root", host: "example.com", path: "/", wantStatus: http.StatusOK, wantBody: "user placeholder", wantHTML: true},
		{name: "admin spa deep path", host: "admin.example.com", path: "/settings/profile", wantStatus: http.StatusOK, wantBody: "admin placeholder", wantHTML: true},
		{name: "user api not found", host: "example.com", path: "/api/unknown", wantStatus: http.StatusNotFound, wantBody: `"code":"not_found"`},
		{name: "user v1 not found", host: "example.com", path: "/v1/models", wantStatus: http.StatusNotFound, wantBody: `"code":"not_found"`},
		{name: "admin api not found", host: "admin.example.com", path: "/admin/api/unknown", wantStatus: http.StatusNotFound, wantBody: `"code":"not_found"`},
		{name: "admin cannot reach user api", host: "admin.example.com", path: "/api/unknown", wantStatus: http.StatusNotFound, wantBody: `"code":"not_found"`},
		{name: "admin cannot reach user config", host: "admin.example.com", path: "/api/config", wantStatus: http.StatusNotFound, wantBody: `"code":"not_found"`},
		{name: "admin cannot reach user v1", host: "admin.example.com", path: "/v1/models", wantStatus: http.StatusNotFound, wantBody: `"code":"not_found"`},
		{name: "user cannot reach admin api", host: "example.com", path: "/admin/api/unknown", wantStatus: http.StatusNotFound, wantBody: `"code":"not_found"`},
		{name: "unknown host is not user", host: "other.example.com", path: "/", wantStatus: http.StatusBadRequest, wantBody: `"code":"invalid_request"`},
		{name: "unknown health is not public", host: "other.example.com", path: "/healthz", wantStatus: http.StatusBadRequest, wantBody: `"code":"invalid_request"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := testHTTPResponse(t, h, http.MethodGet, tc.host, tc.path)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status=%d want=%d body=%s", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tc.wantBody) {
				t.Fatalf("body=%q does not contain %q", rec.Body.String(), tc.wantBody)
			}
			if tc.wantHTML {
				if !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/html") {
					t.Fatalf("content-type=%q", rec.Header().Get("Content-Type"))
				}
				if rec.Header().Get("Cache-Control") != "no-store" {
					t.Fatalf("SPA cache-control=%q", rec.Header().Get("Cache-Control"))
				}
			} else if tc.wantStatus >= 400 || strings.HasPrefix(tc.path, "/api") || strings.HasPrefix(tc.path, "/v1") || strings.HasPrefix(tc.path, "/admin/api") || tc.path == "/healthz" {
				if rec.Header().Get("Cache-Control") != "no-store" {
					t.Fatalf("dynamic cache-control=%q", rec.Header().Get("Cache-Control"))
				}
			}
		})
	}

	unknown := testHTTPResponse(t, h, http.MethodGet, "evil.example", "/healthz?next=1")
	if unknown.Header().Get("Location") != "" {
		t.Fatalf("unknown host got redirect %q", unknown.Header().Get("Location"))
	}
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(unknown.Body.Bytes(), &envelope); err != nil || envelope.Error.Code != "invalid_request" {
		t.Fatalf("unknown response=%s err=%v", unknown.Body.String(), err)
	}
}

func TestApplicationWiringProtectsAllEntryPoints(t *testing.T) {
	key := bytes.Repeat([]byte{0x6a}, secret.MasterKeyBytes)
	vault, err := secret.New(key)
	clear(key)
	if err != nil {
		t.Fatal(err)
	}
	store, err := db.Open(filepath.Join(t.TempDir(), "integration.db"), vault)
	if err != nil {
		_ = vault.Close()
		t.Fatal(err)
	}
	defer func() {
		_ = store.Close()
		_ = vault.Close()
	}()
	cfg := &config.Config{
		ListenAddr: "127.0.0.1:18080", UserHost: "127.0.0.1", AdminHost: "127.0.0.2",
		SiteBaseURL: "https://127.0.0.1", TrustedProxyCIDRs: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")},
		AdminUsername: "root", AdminPassword: "password", DiscordClientID: "client-id", DiscordClientSecret: "client-secret",
	}
	app, err := buildApplication(cfg, store, vault)
	if err != nil {
		t.Fatalf("buildApplication: %v", err)
	}
	defer app.Close()

	for _, tc := range []struct {
		name, host, path, want string
	}{
		{"user health", "127.0.0.1", "/healthz", `"status":"ok"`},
		{"user management requires session", "127.0.0.1", "/api/endpoints", `"code":"unauthorized"`},
		{"caller exit requires bearer", "127.0.0.1", "/v1/models", `"code":"unauthorized"`},
		{"admin session requires cookie", "127.0.0.2", "/admin/api/session", `"code":"unauthorized"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := testHTTPResponse(t, app.handler, http.MethodGet, tc.host, tc.path)
			if !strings.Contains(rec.Body.String(), tc.want) {
				t.Fatalf("status=%d body=%q want=%q", rec.Code, rec.Body.String(), tc.want)
			}
			if rec.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("cache-control=%q", rec.Header().Get("Cache-Control"))
			}
		})
	}
	spa := testHTTPResponse(t, app.handler, http.MethodGet, "127.0.0.1", "/")
	if spa.Code != http.StatusOK || !strings.HasPrefix(spa.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("user SPA status=%d content-type=%q", spa.Code, spa.Header().Get("Content-Type"))
	}
	unknown := testHTTPResponse(t, app.handler, http.MethodGet, "198.51.100.10", "/")
	if unknown.Code != http.StatusBadRequest || strings.Contains(unknown.Body.String(), "user placeholder") {
		t.Fatalf("unknown host response status=%d body=%q", unknown.Code, unknown.Body.String())
	}

	// The public config bootstrap is unauthenticated but projects only the
	// display allowlist: site_name / site_logo_url / default_locale. Operational
	// and secret keys must never appear, and the response is no-store.
	cfgResp := testHTTPResponse(t, app.handler, http.MethodGet, "127.0.0.1", "/api/config")
	if cfgResp.Code != http.StatusOK {
		t.Fatalf("/api/config status=%d body=%s", cfgResp.Code, cfgResp.Body.String())
	}
	if cfgResp.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("/api/config cache-control=%q", cfgResp.Header().Get("Cache-Control"))
	}
	var cfgBody map[string]any
	if err := json.Unmarshal(cfgResp.Body.Bytes(), &cfgBody); err != nil {
		t.Fatalf("/api/config body=%s err=%v", cfgResp.Body.String(), err)
	}
	for _, k := range []string{"site_name", "site_logo_url", "default_locale", "legal_privacy_override_zh", "legal_privacy_override_en", "legal_terms_override_zh", "legal_terms_override_en", "legal_authoritative_locale"} {
		if _, ok := cfgBody[k]; !ok {
			t.Fatalf("/api/config missing %q: %s", k, cfgResp.Body.String())
		}
	}
	for _, secret := range []string{"default_rpm_per_user", "global_rpm", "discord_guild_id", "discord_role_id", "oauth_start_rate_limit"} {
		if _, ok := cfgBody[secret]; ok {
			t.Fatalf("/api/config leaked %q: %s", secret, cfgResp.Body.String())
		}
	}

	// The admin station lives on a separate host and the host boundary blocks
	// user API paths there, so the admin shell fetches the same display-only
	// public config from /admin/api/config. It must be unauthenticated (the
	// admin login screen needs the site name/logo before signing in), project
	// the same allowlist, reject secrets, and be no-store.
	adminCfgResp := testHTTPResponse(t, app.handler, http.MethodGet, "127.0.0.2", "/admin/api/config")
	if adminCfgResp.Code != http.StatusOK {
		t.Fatalf("/admin/api/config status=%d body=%s", adminCfgResp.Code, adminCfgResp.Body.String())
	}
	if adminCfgResp.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("/admin/api/config cache-control=%q", adminCfgResp.Header().Get("Cache-Control"))
	}
	var adminCfgBody map[string]any
	if err := json.Unmarshal(adminCfgResp.Body.Bytes(), &adminCfgBody); err != nil {
		t.Fatalf("/admin/api/config body=%s err=%v", adminCfgResp.Body.String(), err)
	}
	for _, k := range []string{"site_name", "site_logo_url", "default_locale", "legal_privacy_override_zh", "legal_privacy_override_en", "legal_terms_override_zh", "legal_terms_override_en", "legal_authoritative_locale"} {
		if _, ok := adminCfgBody[k]; !ok {
			t.Fatalf("/admin/api/config missing %q: %s", k, adminCfgResp.Body.String())
		}
	}
	for _, secret := range []string{"default_rpm_per_user", "global_rpm", "discord_guild_id", "discord_role_id", "oauth_start_rate_limit"} {
		if _, ok := adminCfgBody[secret]; ok {
			t.Fatalf("/admin/api/config leaked %q: %s", secret, adminCfgResp.Body.String())
		}
	}
}

func TestMaintenanceSweepPurgesExpiredSessions(t *testing.T) {
	key := bytes.Repeat([]byte{0x4c}, secret.MasterKeyBytes)
	vault, err := secret.New(key)
	clear(key)
	if err != nil {
		t.Fatal(err)
	}
	store, err := db.Open(filepath.Join(t.TempDir(), "maintenance.db"), vault)
	if err != nil {
		_ = vault.Close()
		t.Fatal(err)
	}
	defer func() {
		_ = store.Close()
		_ = vault.Close()
	}()

	now := time.Now().UTC()
	result, err := store.DB().Exec(`INSERT INTO users (discord_id, username, created_at, updated_at) VALUES ('maintenance-user', 'maintenance-user', ?, ?)`, now.Unix(), now.Unix())
	if err != nil {
		t.Fatal(err)
	}
	userID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().Exec(`INSERT INTO sessions
		(token_hash, user_id, oauth_state, last_seen_at, expires_at, absolute_expires_at, created_at)
		VALUES ('expired-session', ?, '', ?, ?, ?, ?), ('live-session', ?, '', ?, ?, ?, ?)`,
		userID, now.Add(-time.Hour).Unix(), now.Add(-time.Minute).Unix(), now.Add(time.Hour).Unix(), now.Add(-time.Hour).Unix(),
		userID, now.Unix(), now.Add(time.Hour).Unix(), now.Add(2*time.Hour).Unix(), now.Unix()); err != nil {
		t.Fatal(err)
	}
	usageService, err := usage.NewService(usage.Config{Store: store})
	if err != nil {
		t.Fatal(err)
	}

	runMaintenanceSweep(context.Background(), store, usageService)
	var count int
	if err := store.DB().QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("session rows after maintenance=%d, want 1", count)
	}
	var tokenHash string
	if err := store.DB().QueryRow(`SELECT token_hash FROM sessions`).Scan(&tokenHash); err != nil {
		t.Fatal(err)
	}
	if tokenHash != "live-session" {
		t.Fatalf("remaining session=%q, want live-session", tokenHash)
	}
}

func TestHTTPHandlerSecurityHeaders(t *testing.T) {
	h, err := newHTTPHandler(testHTTPConfig())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		host string
		path string
	}{
		{host: "example.com", path: "/"},
		{host: "admin.example.com", path: "/admin/api/missing"},
		{host: "other.example.com", path: "/"},
	} {
		rec := testHTTPResponse(t, h, http.MethodGet, tc.host, tc.path)
		for name, want := range map[string]string{
			"X-Content-Type-Options": "nosniff",
			"X-Frame-Options":        "DENY",
			"Referrer-Policy":        "no-referrer",
		} {
			if got := rec.Header().Get(name); got != want {
				t.Errorf("%s %s=%q want=%q", tc.path, name, got, want)
			}
		}
		csp := rec.Header().Get("Content-Security-Policy")
		if !strings.Contains(csp, "frame-ancestors 'none'") || !strings.Contains(csp, "script-src 'self'") {
			t.Errorf("%s CSP=%q", tc.path, csp)
		}
	}
}
