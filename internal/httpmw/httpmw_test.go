package httpmw

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/host"
)

func testConfig(forceHTTPS bool) Config {
	return Config{
		UserHost:          "example.com",
		AdminHost:         "admin.example.com",
		SiteBaseURL:       "https://example.com",
		TrustedProxyCIDRs: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8"), netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("::1/128")},
		ForceHTTPS:        forceHTTPS,
	}
}

func recordingHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "station=%s client=%s peer=%s https=%t trusted=%t xff=%q xfp=%q xri=%q", StationOf(r), ClientIP(r), PeerIP(r), RequestIsHTTPS(r), TrustedProxy(r), r.Header.Get("X-Forwarded-For"), r.Header.Get("X-Forwarded-Proto"), r.Header.Get("X-Real-IP"))
	})
}

func makeRequest(method, target, hostHeader, remote string) *http.Request {
	r := httptest.NewRequest(method, target, nil)
	r.Host = hostHeader
	r.RemoteAddr = remote
	return r
}

func runBoundary(t *testing.T, cfg Config, next http.Handler, r *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	h, err := New(cfg, next)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func TestHostAndPathBoundary(t *testing.T) {
	handler := recordingHandler()
	cases := []struct {
		name       string
		host       string
		path       string
		wantStatus int
		wantBody   string
	}{
		{name: "user root", host: "example.com", path: "/", wantStatus: http.StatusOK, wantBody: "station=user"},
		{name: "user case port and dot", host: "EXAMPLE.COM.:8080", path: "/deep/route", wantStatus: http.StatusOK, wantBody: "station=user"},
		{name: "admin root", host: "admin.example.com", path: "/", wantStatus: http.StatusOK, wantBody: "station=admin"},
		{name: "admin user api blocked", host: "admin.example.com", path: "/api/me", wantStatus: http.StatusNotFound, wantBody: `"code":"not_found"`},
		{name: "admin v1 blocked", host: "admin.example.com", path: "/v1/models", wantStatus: http.StatusNotFound, wantBody: `"code":"not_found"`},
		{name: "user admin api blocked", host: "example.com", path: "/admin/api/users", wantStatus: http.StatusNotFound, wantBody: `"code":"not_found"`},
		{name: "user api mount allowed", host: "example.com", path: "/api/unknown", wantStatus: http.StatusOK, wantBody: "station=user"},
		{name: "user v1 mount allowed", host: "example.com", path: "/v1/models", wantStatus: http.StatusOK, wantBody: "station=user"},
		{name: "unknown host", host: "unknown.example.com", path: "/healthz", wantStatus: http.StatusBadRequest, wantBody: `"code":"invalid_request"`},
		{name: "empty host", host: "", path: "/", wantStatus: http.StatusBadRequest, wantBody: `"code":"invalid_request"`},
		{name: "userinfo host", host: "user@example.com", path: "/", wantStatus: http.StatusBadRequest, wantBody: `"code":"invalid_request"`},
		{name: "invalid port", host: "example.com:not-a-port", path: "/", wantStatus: http.StatusBadRequest, wantBody: `"code":"invalid_request"`},
		{name: "host injection", host: "example.com\r\nX-Forwarded-Host: admin.example.com", path: "/", wantStatus: http.StatusBadRequest, wantBody: `"code":"invalid_request"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := makeRequest(http.MethodGet, "http://wire.invalid"+tc.path, tc.host, "198.51.100.20:4242")
			rec := runBoundary(t, testConfig(false), handler, req)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status=%d want=%d body=%s", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tc.wantBody) {
				t.Fatalf("body=%q does not contain %q", rec.Body.String(), tc.wantBody)
			}
			if tc.wantStatus != http.StatusOK && rec.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("error cache-control=%q", rec.Header().Get("Cache-Control"))
			}
		})
	}

	// Forwarded host is never used for station selection.
	for _, tc := range []struct {
		name string
		host string
		xfh  string
		want string
	}{
		{name: "user host with admin forwarded host", host: "example.com", xfh: "admin.example.com", want: "station=user"},
		{name: "admin host with user forwarded host", host: "admin.example.com", xfh: "example.com", want: "station=admin"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := makeRequest(http.MethodGet, "http://wire.invalid/", tc.host, "198.51.100.20:4242")
			req.Header.Set("X-Forwarded-Host", tc.xfh)
			rec := runBoundary(t, testConfig(false), handler, req)
			if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), tc.want) {
				t.Fatalf("status=%d body=%q want %q", rec.Code, rec.Body.String(), tc.want)
			}
			if strings.Contains(rec.Body.String(), tc.xfh) {
				t.Fatalf("forwarded host reached downstream: %q", rec.Body.String())
			}
		})
	}
}

func TestBrowserSessionAPISameOriginBoundary(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	tests := []struct {
		name       string
		method     string
		host       string
		path       string
		origin     []string
		fetchSite  string
		cookie     string
		secure     bool
		wantStatus int
	}{
		{name: "user sibling origin refused", method: http.MethodPost, host: "example.com", path: "/api/caller-key/regenerate", origin: []string{"https://admin.example.com"}, cookie: "nb_session=x", secure: true, wantStatus: http.StatusForbidden},
		{name: "admin sibling origin refused", method: http.MethodPost, host: "admin.example.com", path: "/admin/api/users/1/unban", origin: []string{"https://example.com"}, cookie: "nb_admin_session=x", secure: true, wantStatus: http.StatusForbidden},
		{name: "same-site fetch metadata is not same-origin", method: http.MethodPost, host: "admin.example.com", path: "/admin/api/logout", fetchSite: "same-site", cookie: "nb_admin_session=x", secure: true, wantStatus: http.StatusForbidden},
		{name: "cookie without browser evidence refused", method: http.MethodDelete, host: "example.com", path: "/api/account", cookie: "nb_session=x", secure: true, wantStatus: http.StatusForbidden},
		{name: "same origin accepted", method: http.MethodPost, host: "example.com", path: "/api/caller-key/regenerate", origin: []string{"https://example.com"}, fetchSite: "same-origin", cookie: "nb_session=x", secure: true, wantStatus: http.StatusNoContent},
		{name: "default port normalizes", method: http.MethodPatch, host: "admin.example.com:443", path: "/admin/api/site-config/site_name", origin: []string{"https://admin.example.com"}, cookie: "nb_admin_session=x", secure: true, wantStatus: http.StatusNoContent},
		{name: "nondefault port mismatch refused", method: http.MethodPatch, host: "admin.example.com:8443", path: "/admin/api/site-config/site_name", origin: []string{"https://admin.example.com"}, cookie: "nb_admin_session=x", secure: true, wantStatus: http.StatusForbidden},
		{name: "scheme mismatch refused", method: http.MethodPost, host: "example.com", path: "/api/logout", origin: []string{"http://example.com"}, cookie: "nb_session=x", secure: true, wantStatus: http.StatusForbidden},
		{name: "duplicate origin refused", method: http.MethodPost, host: "example.com", path: "/api/logout", origin: []string{"https://example.com", "https://example.com"}, cookie: "nb_session=x", secure: true, wantStatus: http.StatusForbidden},
		{name: "null origin refused", method: http.MethodPost, host: "example.com", path: "/api/logout", origin: []string{"null"}, cookie: "nb_session=x", secure: true, wantStatus: http.StatusForbidden},
		{name: "cookie-less CLI remains compatible", method: http.MethodPost, host: "admin.example.com", path: "/admin/api/login", wantStatus: http.StatusNoContent},
		{name: "bearer API excluded", method: http.MethodPost, host: "example.com", path: "/v1/chat/completions", origin: []string{"https://admin.example.com"}, fetchSite: "same-site", cookie: "unrelated=x", secure: true, wantStatus: http.StatusNoContent},
		{name: "safe method excluded", method: http.MethodGet, host: "example.com", path: "/api/me", origin: []string{"https://admin.example.com"}, fetchSite: "same-site", cookie: "nb_session=x", secure: true, wantStatus: http.StatusNoContent},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			scheme := "http"
			if test.secure {
				scheme = "https"
			}
			req := makeRequest(test.method, scheme+"://wire.invalid"+test.path, test.host, "198.51.100.20:4242")
			for _, value := range test.origin {
				req.Header.Add("Origin", value)
			}
			if test.fetchSite != "" {
				req.Header.Set("Sec-Fetch-Site", test.fetchSite)
			}
			if test.cookie != "" {
				req.Header.Set("Cookie", test.cookie)
			}
			rec := runBoundary(t, testConfig(false), next, req)
			if rec.Code != test.wantStatus {
				t.Fatalf("status=%d want=%d body=%q", rec.Code, test.wantStatus, rec.Body.String())
			}
			if test.wantStatus == http.StatusForbidden && !strings.Contains(rec.Body.String(), `"code":"forbidden"`) {
				t.Fatalf("forbidden response=%q", rec.Body.String())
			}
		})
	}
}

func TestTrustedProxyHeaderBoundary(t *testing.T) {
	cases := []struct {
		name       string
		remote     string
		xff        string
		xri        string
		xfp        string
		duplicates bool
		wantClient string
		wantHTTPS  bool
		wantTrust  bool
	}{
		{name: "untrusted spoof", remote: "198.51.100.9:1234", xff: "1.1.1.1", xri: "2.2.2.2", xfp: "https", wantClient: "198.51.100.9", wantHTTPS: false, wantTrust: false},
		{name: "trusted real ip and proto", remote: "127.0.0.1:1234", xri: "203.0.113.7:443", xfp: "HTTPS", wantClient: "203.0.113.7", wantHTTPS: true, wantTrust: true},
		{name: "trusted multi hop ipv4 and ipv6", remote: "[::1]:1234", xff: "1.1.1.1, 10.2.2.2, [::1]:443", xfp: "http", wantClient: "1.1.1.1", wantHTTPS: false, wantTrust: true},
		{name: "malformed xff fails closed", remote: "127.0.0.1:1234", xff: "1.1.1.1, not-an-ip", xri: "203.0.113.8", wantClient: "127.0.0.1", wantHTTPS: false, wantTrust: true},
		{name: "too many hops fails closed", remote: "127.0.0.1:1234", xff: strings.Repeat("1.1.1.1, ", maxForwardedHops) + "1.1.1.1", wantClient: "127.0.0.1", wantHTTPS: false, wantTrust: true},
		{name: "oversized xff fails closed", remote: "127.0.0.1:1234", xff: strings.Repeat("1", maxForwardedForBytes+1), wantClient: "127.0.0.1", wantHTTPS: false, wantTrust: true},
		{name: "oversized real ip fails closed", remote: "127.0.0.1:1234", xri: strings.Repeat("1", maxRealIPLen+1), wantClient: "127.0.0.1", wantHTTPS: false, wantTrust: true},
		{name: "invalid real ip fails closed", remote: "127.0.0.1:1234", xri: "not-an-ip", wantClient: "127.0.0.1", wantHTTPS: false, wantTrust: true},
		{name: "invalid proto fails closed", remote: "127.0.0.1:1234", xfp: "https, http", wantClient: "127.0.0.1", wantHTTPS: false, wantTrust: true},
		{name: "oversized proto fails closed", remote: "127.0.0.1:1234", xfp: strings.Repeat("h", maxForwardedProtoLen+1), wantClient: "127.0.0.1", wantHTTPS: false, wantTrust: true},
		{name: "duplicate proto fails closed", remote: "127.0.0.1:1234", xfp: "https", duplicates: true, wantClient: "127.0.0.1", wantHTTPS: false, wantTrust: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := makeRequest(http.MethodGet, "http://wire.invalid/", "example.com", tc.remote)
			if tc.xff != "" {
				req.Header.Set("X-Forwarded-For", tc.xff)
			}
			if tc.xri != "" {
				req.Header.Set("X-Real-IP", tc.xri)
			}
			if tc.xfp != "" {
				req.Header.Set("X-Forwarded-Proto", tc.xfp)
			}
			if tc.duplicates {
				req.Header.Add("X-Forwarded-Proto", "http")
			}
			rec := runBoundary(t, testConfig(false), recordingHandler(), req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			body := rec.Body.String()
			if !strings.Contains(body, "client="+tc.wantClient) ||
				!strings.Contains(body, fmt.Sprintf("https=%t", tc.wantHTTPS)) ||
				!strings.Contains(body, fmt.Sprintf("trusted=%t", tc.wantTrust)) {
				t.Fatalf("body=%q want client=%q https=%t trusted=%t", body, tc.wantClient, tc.wantHTTPS, tc.wantTrust)
			}
			if strings.Contains(body, "xff=\"") && !strings.Contains(body, `xff=""`) {
				t.Fatalf("forwarding header survived sanitization: %q", body)
			}
		})
	}
}

func TestRedirectIsFixedAndHostValidationPrecedesIt(t *testing.T) {
	cfg := testConfig(true)
	cfg.SiteBaseURL = "https://example.com:8443"
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })

	req := makeRequest(http.MethodGet, "http://wire.invalid/deep/a%2Fb?next=1", "example.com", "198.51.100.9:1234")
	rec := runBoundary(t, cfg, next, req)
	if rec.Code != http.StatusMovedPermanently {
		t.Fatalf("user redirect status=%d body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Location"); got != "https://example.com:8443/deep/a%2Fb?next=1" {
		t.Fatalf("user Location=%q", got)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("redirect cache-control=%q", rec.Header().Get("Cache-Control"))
	}

	req = makeRequest(http.MethodGet, "http://wire.invalid/admin/deep?x=1", "ADMIN.EXAMPLE.COM", "198.51.100.9:1234")
	req.Header.Set("X-Forwarded-Host", "evil.example")
	rec = runBoundary(t, cfg, next, req)
	if got := rec.Header().Get("Location"); got != "https://admin.example.com/admin/deep?x=1" {
		t.Fatalf("admin Location=%q", got)
	}

	req = makeRequest(http.MethodGet, "http://wire.invalid/healthz?next=1", "evil.example", "198.51.100.9:1234")
	rec = runBoundary(t, cfg, next, req)
	if rec.Code == http.StatusMovedPermanently || rec.Header().Get("Location") != "" {
		t.Fatalf("unknown host redirected: status=%d location=%q", rec.Code, rec.Header().Get("Location"))
	}

	req = makeRequest(http.MethodGet, "http://wire.invalid/", "example.com", "198.51.100.9:1234")
	req.Header.Set("X-Forwarded-Proto", "https")
	rec = runBoundary(t, cfg, next, req)
	if rec.Code != http.StatusMovedPermanently {
		t.Fatalf("untrusted forwarded https status=%d", rec.Code)
	}

	req = makeRequest(http.MethodGet, "http://wire.invalid/", "example.com", "127.0.0.1:1234")
	req.Header.Set("X-Forwarded-Proto", "https")
	rec = runBoundary(t, cfg, next, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("trusted forwarded https status=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Strict-Transport-Security") == "" {
		t.Fatal("trusted forwarded https did not receive HSTS")
	}

	req = makeRequest(http.MethodGet, "https://wire.invalid/", "example.com", "198.51.100.9:1234")
	rec = runBoundary(t, cfg, next, req)
	if rec.Code != http.StatusNoContent || rec.Header().Get("Strict-Transport-Security") == "" {
		t.Fatalf("direct TLS status=%d hsts=%q", rec.Code, rec.Header().Get("Strict-Transport-Security"))
	}
}

func TestSecurityHeadersPreserveSSE(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: keep\n\n"))
	})
	rec := runBoundary(t, testConfig(false), next, makeRequest(http.MethodGet, "http://wire.invalid/", "example.com", "198.51.100.9:1234"))
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "text/event-stream" || rec.Body.String() != "data: keep\n\n" {
		t.Fatalf("SSE status=%d content-type=%q body=%q", rec.Code, rec.Header().Get("Content-Type"), rec.Body.String())
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" || rec.Header().Get("X-Frame-Options") != "DENY" {
		t.Fatalf("SSE security headers missing: %v", rec.Header())
	}
}

func TestSecurityHeadersAndAPIEnvelope(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("GET /stream", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: ok\n\n"))
	})
	api := API(mux)

	missing := httptest.NewRecorder()
	api.ServeHTTP(missing, makeRequest(http.MethodGet, "http://wire.invalid/missing", "example.com", "198.51.100.9:1234"))
	if missing.Code != http.StatusNotFound || missing.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("missing status=%d cache=%q body=%s", missing.Code, missing.Header().Get("Cache-Control"), missing.Body.String())
	}
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(missing.Body.Bytes(), &env); err != nil || env.Error.Code != "not_found" {
		t.Fatalf("missing envelope=%s err=%v", missing.Body.String(), err)
	}

	method := httptest.NewRecorder()
	api.ServeHTTP(method, makeRequest(http.MethodPost, "http://wire.invalid/json", "example.com", "198.51.100.9:1234"))
	if method.Code != http.StatusMethodNotAllowed || !strings.Contains(method.Body.String(), `"code":"method_not_allowed"`) {
		t.Fatalf("method status=%d body=%s", method.Code, method.Body.String())
	}

	stream := httptest.NewRecorder()
	api.ServeHTTP(stream, makeRequest(http.MethodGet, "http://wire.invalid/stream", "example.com", "198.51.100.9:1234"))
	if stream.Code != http.StatusOK || stream.Header().Get("Content-Type") != "text/event-stream" || stream.Body.String() != "data: ok\n\n" {
		t.Fatalf("stream status=%d content-type=%q body=%q", stream.Code, stream.Header().Get("Content-Type"), stream.Body.String())
	}
}

func TestAPIAlwaysNoStoreEvenForHeaderOnlyHandlers(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /header-only", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=3600")
	})
	rec := httptest.NewRecorder()
	API(mux).ServeHTTP(rec, makeRequest(http.MethodGet, "http://wire.invalid/header-only", "example.com", "198.51.100.9:1234"))
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("header-only API response cache-control = %q, want no-store", got)
	}
}

func TestBoundaryConcurrentRequests(t *testing.T) {
	handlers := make([]http.Handler, 4)
	for i := range handlers {
		var err error
		handlers[i], err = New(testConfig(false), recordingHandler())
		if err != nil {
			t.Fatal(err)
		}
	}
	const workers = 16
	const perWorker = 50
	errs := make(chan string, workers*perWorker)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < perWorker; j++ {
				hostHeader := "example.com"
				if (i+j)%2 == 0 {
					hostHeader = "admin.example.com"
				}
				req := makeRequest(http.MethodGet, "http://wire.invalid/", hostHeader, "127.0.0.1:1234")
				req.Header.Set("X-Forwarded-For", "203.0.113.10, 10.0.0.1")
				req.Header.Set("X-Forwarded-Proto", "https")
				rec := httptest.NewRecorder()
				handlers[(i+j)%len(handlers)].ServeHTTP(rec, req)
				if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "client=203.0.113.10") {
					errs <- fmt.Sprintf("status=%d body=%q", rec.Code, rec.Body.String())
				}
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func TestStationContextCarriesValidatedSelection(t *testing.T) {
	var got host.Station
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = StationOf(r)
		w.WriteHeader(http.StatusNoContent)
	})
	req := makeRequest(http.MethodGet, "http://wire.invalid/", "admin.example.com", "198.51.100.9:1234")
	rec := runBoundary(t, testConfig(false), next, req)
	if rec.Code != http.StatusNoContent || got != host.StationAdmin {
		t.Fatalf("status=%d station=%v", rec.Code, got)
	}
}
