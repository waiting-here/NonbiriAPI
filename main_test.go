package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/config"
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
		wantCode   string
		wantHTML   bool
	}{
		{name: "user health", host: "example.com", path: "/healthz", wantStatus: http.StatusOK},
		{name: "admin health", host: "admin.example.com", path: "/healthz", wantStatus: http.StatusOK},
		{name: "user spa root", host: "example.com", path: "/", wantStatus: http.StatusOK, wantHTML: true},
		{name: "admin spa deep path", host: "admin.example.com", path: "/settings/profile", wantStatus: http.StatusOK, wantHTML: true},
		{name: "boundary-only user config is absent", host: "example.com", path: "/api/config", wantStatus: http.StatusNotFound, wantCode: "not_found"},
		{name: "user api not found", host: "example.com", path: "/api/unknown", wantStatus: http.StatusNotFound, wantCode: "not_found"},
		{name: "user v1 not found", host: "example.com", path: "/v1/models", wantStatus: http.StatusNotFound, wantCode: "not_found"},
		{name: "admin api not found", host: "admin.example.com", path: "/admin/api/config", wantStatus: http.StatusNotFound, wantCode: "not_found"},
		{name: "admin cannot reach user api", host: "admin.example.com", path: "/api/config", wantStatus: http.StatusNotFound, wantCode: "not_found"},
		{name: "user cannot reach admin api", host: "example.com", path: "/admin/api/config", wantStatus: http.StatusNotFound, wantCode: "not_found"},
		{name: "unknown host is rejected", host: "other.example.com", path: "/", wantStatus: http.StatusBadRequest, wantCode: "invalid_request"},
		{name: "unknown health is rejected", host: "other.example.com", path: "/healthz", wantStatus: http.StatusBadRequest, wantCode: "invalid_request"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := testHTTPResponse(t, h, http.MethodGet, tc.host, tc.path)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status=%d want=%d body=%s", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if rec.Code == http.StatusServiceUnavailable {
				t.Fatalf("fresh-safe baseline returned forbidden temporary 503: %s", rec.Body.String())
			}
			if tc.wantHTML {
				if !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/html") {
					t.Fatalf("content-type=%q", rec.Header().Get("Content-Type"))
				}
			} else if tc.wantCode != "" {
				var envelope struct {
					Error struct {
						Code string `json:"code"`
					} `json:"error"`
				}
				if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil || envelope.Error.Code != tc.wantCode {
					t.Fatalf("response=%s code=%q err=%v", rec.Body.String(), envelope.Error.Code, err)
				}
			}
			if rec.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("cache-control=%q", rec.Header().Get("Cache-Control"))
			}
		})
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

func TestHTTPHandlerRejectsMissingConfiguration(t *testing.T) {
	if _, err := newHTTPHandler(nil); err == nil {
		t.Fatal("nil configuration was accepted")
	}
}

func TestHealthRejectsQueryAndBody(t *testing.T) {
	h, err := newHTTPHandler(testHTTPConfig())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		path string
		body string
	}{
		{name: "query", path: "/healthz?unexpected=1"},
		{name: "bare query delimiter", path: "/healthz?"},
		{name: "body", path: "/healthz", body: "{}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "http://wire.invalid"+tc.path, strings.NewReader(tc.body))
			req.Host = "example.com"
			req.RemoteAddr = "198.51.100.20:4242"
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status=%d want=%d body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
			}
		})
	}
}
