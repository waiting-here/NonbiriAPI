// Independent audit: inbound edge proxy/forwarded-header spoofing and
// station-path isolation through the exported httpmw boundary.
package httpmw_test

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"

	"nonbiriapi/internal/host"
	"nonbiriapi/internal/httpmw"
)

func spoofConfig() httpmw.Config {
	return httpmw.Config{
		UserHost: "user.example", AdminHost: "admin.example", SiteBaseURL: "https://user.example",
		TrustedProxyCIDRs: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8"), netip.MustParsePrefix("10.0.0.0/8")},
	}
}

// probeHandler echoes the edge-selected client IP and station so tests can
// observe the decision without touching application routes.
func probeHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("station=" + httpmw.StationOf(r).String() + " client=" + httpmw.ClientIP(r) + " peer=" + httpmw.PeerIP(r) + " https=" + boolStr(httpmw.RequestIsHTTPS(r)) + " trusted=" + boolStr(httpmw.TrustedProxy(r))))
	})
}

func boolStr(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

func spoofRequest(remoteAddr, host string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "http://wire.invalid/healthz", nil)
	req.Host = host
	req.RemoteAddr = remoteAddr
	return req
}

func TestAuditEdgeUntrustedPeerHeadersIgnored(t *testing.T) {
	handler, err := httpmw.New(spoofConfig(), probeHandler())
	if err != nil {
		t.Fatal(err)
	}
	// An untrusted peer supplies forged forwarding headers; the edge must
	// ignore them and keep the direct peer as the client identity.
	for _, headers := range []map[string]string{
		{"X-Forwarded-For": "6.6.6.6"},
		{"X-Forwarded-For": "6.6.6.6, 7.7.7.7"},
		{"X-Real-IP": "6.6.6.6"},
		{"Forwarded": "for=6.6.6.6;proto=https"},
		{"X-Forwarded-Proto": "https"},
		{"X-Forwarded-Host": "user.example"},
	} {
		req := spoofRequest("198.51.100.7:4000", "user.example")
		for name, value := range headers {
			req.Header.Set(name, value)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		body := rec.Body.String()
		if body != "station=user client=198.51.100.7 peer=198.51.100.7 https=0 trusted=0" {
			t.Fatalf("untrusted spoof %v => %q", headers, body)
		}
	}
}

func TestAuditEdgeTrustedProxyChainLeftmostUntrusted(t *testing.T) {
	handler, err := httpmw.New(spoofConfig(), probeHandler())
	if err != nil {
		t.Fatal(err)
	}
	req := spoofRequest("127.0.0.1:4000", "user.example")
	// Chain: 203.0.113.9 -> 10.1.1.1 (trusted) -> 127.0.0.1 (trusted peer).
	// The leftmost untrusted address is 203.0.113.9 and proto is honored
	// from the trusted proxy.
	req.Header.Set("X-Forwarded-For", "203.0.113.9, 10.1.1.1")
	req.Header.Set("X-Forwarded-Proto", "https")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	body := rec.Body.String()
	if body != "station=user client=203.0.113.9 peer=127.0.0.1 https=1 trusted=1" {
		t.Fatalf("trusted chain => %q", body)
	}

	// A chain where every hop is trusted falls back to the leftmost entry.
	req = spoofRequest("127.0.0.1:4000", "user.example")
	req.Header.Set("X-Forwarded-For", "10.1.1.2, 10.1.1.1")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	body = rec.Body.String()
	if body != "station=user client=10.1.1.2 peer=127.0.0.1 https=0 trusted=1" {
		t.Fatalf("all-trusted chain => %q", body)
	}
}

func TestAuditEdgeForgedChainFromUntrustedPeerIgnored(t *testing.T) {
	handler, err := httpmw.New(spoofConfig(), probeHandler())
	if err != nil {
		t.Fatal(err)
	}
	// Even a chain that includes trusted hops is ignored when the immediate
	// peer is untrusted.
	req := spoofRequest("198.51.100.7:4000", "user.example")
	req.Header.Set("X-Forwarded-For", "203.0.113.9, 10.1.1.1")
	req.Header.Set("X-Forwarded-Proto", "https")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	body := rec.Body.String()
	if body != "station=user client=198.51.100.7 peer=198.51.100.7 https=0 trusted=0" {
		t.Fatalf("forged chain => %q", body)
	}
}

func TestAuditEdgeMalformedForwardedHeadersRejected(t *testing.T) {
	handler, err := httpmw.New(spoofConfig(), probeHandler())
	if err != nil {
		t.Fatal(err)
	}
	// From a trusted peer, malformed or duplicate forwarding headers must be
	// rejected wholesale (fall back to the peer, not partially trusted).
	cases := []map[string][]string{
		{"X-Forwarded-For": {"203.0.113.9", "203.0.113.10"}}, // duplicate header
		{"X-Forwarded-For": {"203.0.113.9, garbage"}},        // unparsable element
		{"X-Forwarded-For": {"203.0.113.9, " + "10.1.1.1, 10.1.1.2, 10.1.1.3, 10.1.1.4, 10.1.1.5, 10.1.1.6, 10.1.1.7, 10.1.1.8, 10.1.1.9, 10.1.1.10, 10.1.1.11, 10.1.1.12, 10.1.1.13, 10.1.1.14, 10.1.1.15, 10.1.1.16, 10.1.1.17, 10.1.1.18, 10.1.1.19, 10.1.1.20, 10.1.1.21, 10.1.1.22, 10.1.1.23, 10.1.1.24, 10.1.1.25, 10.1.1.26, 10.1.1.27, 10.1.1.28, 10.1.1.29, 10.1.1.30, 10.1.1.31, 10.1.1.32, 10.1.1.33"}}, // too many hops
		{"X-Real-IP": {"6.6.6.6, 7.7.7.7"}},    // comma in real-ip
		{"X-Forwarded-Proto": {"https, http"}}, // duplicate proto values
	}
	for i, headers := range cases {
		req := spoofRequest("127.0.0.1:4000", "user.example")
		for name, values := range headers {
			for _, value := range values {
				req.Header.Add(name, value)
			}
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		body := rec.Body.String()
		if body != "station=user client=127.0.0.1 peer=127.0.0.1 https=0 trusted=1" {
			t.Fatalf("case %d malformed headers => %q", i, body)
		}
	}
}

func TestAuditEdgeStationIsolationAndUnknownHost(t *testing.T) {
	handler, err := httpmw.New(spoofConfig(), probeHandler())
	if err != nil {
		t.Fatal(err)
	}
	// Cross-station paths are refused at the boundary (before any handler).
	for _, tc := range []struct {
		host, path string
		want       int
	}{
		{"user.example", "/admin/api/login", 404},
		{"admin.example", "/api/endpoints", 404},
		{"admin.example", "/v1/models", 404},
		{"user.example", "/api/endpoints", 200},
		{"admin.example", "/admin/api/login", 200},
		{"evil.example", "/", 400},
		{"user.example:8080", "/api/endpoints", 200}, // port-insensitive match
		{"admin.example.", "/admin/api/login", 200},  // DNS FQDN trailing dot normalizes to the same station (documented)
	} {
		req := spoofRequest("198.51.100.7:4000", tc.host)
		req.URL.Path = tc.path
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != tc.want {
			t.Fatalf("%s%s => %d want %d body=%q", tc.host, tc.path, rec.Code, tc.want, rec.Body.String())
		}
	}
}

func TestAuditEdgeHostValidationPrecedesRedirect(t *testing.T) {
	handler, err := httpmw.New(httpmw.Config{
		UserHost: "user.example", AdminHost: "admin.example", SiteBaseURL: "https://user.example",
		TrustedProxyCIDRs: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}, ForceHTTPS: true,
	}, probeHandler())
	if err != nil {
		t.Fatal(err)
	}
	// An unknown host must be refused outright, never redirected to a
	// location derived from attacker-controlled Host.
	req := spoofRequest("198.51.100.7:4000", "evil.example")
	req.URL.Path = "/healthz"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown host over http => %d (must not redirect)", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "" {
		t.Fatalf("unknown host got redirect to %q", loc)
	}
	// A known station is redirected to the fixed configured origin.
	req = spoofRequest("198.51.100.7:4000", "user.example")
	req.URL.Path = "/healthz"
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusMovedPermanently {
		t.Fatalf("http user station => %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "https://user.example/healthz" {
		t.Fatalf("redirect target %q", loc)
	}
	req = spoofRequest("198.51.100.7:4000", "admin.example")
	req.URL.Path = "/admin/api/login"
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if loc := rec.Header().Get("Location"); loc != "https://admin.example/admin/api/login" {
		t.Fatalf("admin redirect target %q", loc)
	}
}

func TestAuditEdgeHeadersRemovedFromDownstream(t *testing.T) {
	// The boundary must delete forwarding headers before delegation so a
	// downstream handler cannot be tricked into trusting them.
	seen := make(chan map[string][]string, 1)
	handler, err := httpmw.New(spoofConfig(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		values := map[string][]string{}
		for _, name := range []string{"Forwarded", "X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto", "X-Real-IP"} {
			values[name] = r.Header.Values(name)
		}
		seen <- values
		w.WriteHeader(http.StatusNoContent)
	}))
	if err != nil {
		t.Fatal(err)
	}
	req := spoofRequest("127.0.0.1:4000", "user.example")
	req.Header.Set("X-Forwarded-For", "203.0.113.9")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	values := <-seen
	for name, list := range values {
		if len(list) != 0 {
			t.Fatalf("downstream still sees %s=%v", name, list)
		}
	}
	if got := httpmw.StationOf(req); got != host.StationUnknown {
		// The original request object must not carry the station context;
		// only the cloned request inside the boundary does.
		t.Fatalf("original request station=%v", got)
	}
}
