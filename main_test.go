package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/config"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
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
