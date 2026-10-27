package httpmw

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAPILegacyHTMLFallbackRemainsJSONForClients(t *testing.T) {
	h := API(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("<h1>not found</h1>"))
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/missing", nil))
	if rec.Code != 404 || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") || !strings.Contains(rec.Body.String(), `"code":"not_found"`) {
		t.Fatalf("legacy API fallback changed: status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestBrowserBoundaryErrors(t *testing.T) {
	for _, tc := range []struct {
		name, host, path, method string
		status                   int
		home                     string
	}{
		{"unknown host", "evil.example", "/?return=https://evil.example", http.MethodGet, 400, `href="https://example.com/"`},
		{"wrong station", "admin.example.com", "/api/session", http.MethodGet, 404, `href="/"`},
		{"cross origin form", "example.com", "/api/auth/logout", http.MethodPost, 403, `href="/"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := makeRequest(tc.method, "https://example.com"+tc.path, tc.host, "192.0.2.1:1234")
			req.Header.Set("Accept", "text/html")
			req.Header.Set("Sec-Fetch-Dest", "document")
			req.Header.Set("Sec-Fetch-Mode", "navigate")
			if tc.method == http.MethodPost {
				req.Header.Set("Origin", "https://untrusted.example")
			}
			rec := runBoundary(t, testConfig(false), recordingHandler(), req)
			if rec.Code != tc.status || !strings.Contains(rec.Body.String(), "<!doctype html>") || !strings.Contains(rec.Body.String(), tc.home) {
				t.Fatalf("status=%d", rec.Code)
			}
			if strings.Contains(rec.Body.String(), "evil.example") || strings.Contains(rec.Body.String(), "untrusted.example") {
				t.Fatal("request origin or return URL reflected into page")
			}
			if !strings.Contains(rec.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") {
				t.Fatal("security policy lost")
			}
		})
	}
}
