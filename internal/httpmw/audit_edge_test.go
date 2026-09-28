// Independent audit: inbound edge proxy/forwarded-header spoofing and
// station-path isolation through the exported httpmw boundary.
package httpmw_test

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/host"
	"github.com/waiting-here/NonbiriAPI/internal/httpmw"
)

func spoofConfig() httpmw.Config {
	return httpmw.Config{
		UserHost: "user.example", AdminHost: "admin.example", SiteBaseURL: "https://user.example",
		TrustedProxyCIDRs: []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32"), netip.MustParsePrefix("::1/128"), netip.MustParsePrefix("10.0.0.0/8")},
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
	for _, station := range []struct{ host, name string }{{"user.example", "user"}, {"admin.example", "admin"}} {
		for _, peer := range []struct{ name, remote, ip string }{
			{"ipv4", "198.51.100.7:4000", "198.51.100.7"},
			{"ipv6", "[2001:db8::7]:4000", "2001:db8::7"},
			{"cloudflare", "173.245.48.10:4000", "173.245.48.10"},
			{"other_loopback", "127.0.0.2:4000", "127.0.0.2"},
		} {
			for _, tc := range []struct {
				name    string
				headers map[string]string
			}{
				{"xff", map[string]string{"X-Forwarded-For": "6.6.6.6"}},
				{"xff_chain", map[string]string{"X-Forwarded-For": "6.6.6.6, 7.7.7.7"}},
				{"real_ip", map[string]string{"X-Real-IP": "6.6.6.6"}},
				{"forwarded", map[string]string{"Forwarded": "for=6.6.6.6;proto=https"}},
				{"proto", map[string]string{"X-Forwarded-Proto": "https"}},
				{"host", map[string]string{"X-Forwarded-Host": "user.example"}},
				{"cf_ip", map[string]string{"CF-Connecting-IP": "203.0.113.66"}},
				{"cf_ipv6", map[string]string{"CF-Connecting-IPv6": "2001:db8::66"}},
				{"true_client_ip", map[string]string{"True-Client-IP": "203.0.113.66"}},
				{"combined", map[string]string{"CF-Connecting-IP": "203.0.113.66", "X-Forwarded-For": "203.0.113.9, 10.1.1.1", "X-Real-IP": "192.0.2.66", "X-Forwarded-Proto": "https"}},
			} {
				t.Run(station.name+"/"+peer.name+"/"+tc.name, func(t *testing.T) {
					req := spoofRequest(peer.remote, station.host)
					for name, value := range tc.headers {
						req.Header.Set(name, value)
					}
					rec := httptest.NewRecorder()
					handler.ServeHTTP(rec, req)
					want := "station=" + station.name + " client=" + peer.ip + " peer=" + peer.ip + " https=0 trusted=0"
					if rec.Code != http.StatusOK || rec.Body.String() != want {
						t.Fatalf("untrusted spoof => %d %q, want %q", rec.Code, rec.Body.String(), want)
					}
				})
			}
		}
	}
}

func TestAuditEdgeTrustedProxyChainLeftmostUntrusted(t *testing.T) {
	handler, err := httpmw.New(spoofConfig(), probeHandler())
	if err != nil {
		t.Fatal(err)
	}
	for _, station := range []struct{ host, name string }{{"user.example", "user"}, {"admin.example", "admin"}} {
		for _, tc := range []struct {
			name, remote, peer, forward, client string
			https                               bool
		}{
			{"ipv4", "127.0.0.1:4000", "127.0.0.1", "203.0.113.9", "203.0.113.9", true},
			{"ipv6", "[::1]:4000", "::1", "2001:db8::9", "2001:db8::9", true},
			{"mapped_ipv4", "[::1]:4000", "::1", "::ffff:192.0.2.7", "192.0.2.7", true},
			{"trusted_hop", "127.0.0.1:4000", "127.0.0.1", "203.0.113.9, 10.1.1.1", "203.0.113.9", true},
			{"forged_leftmost", "127.0.0.1:4000", "127.0.0.1", "192.0.2.66, 198.51.100.7, 10.1.1.1", "198.51.100.7", true},
			{"mixed_chain", "[::1]:4000", "::1", "203.0.113.9, 2001:db8::7, 10.1.1.1", "2001:db8::7", true},
			{"all_trusted", "127.0.0.1:4000", "127.0.0.1", "10.1.1.2, 10.1.1.1", "10.1.1.2", false},
			{"maximum_hops", "127.0.0.1:4000", "127.0.0.1", "203.0.113.9, " + strings.Repeat("10.1.1.1, ", 30) + "10.1.1.1", "203.0.113.9", true},
		} {
			t.Run(station.name+"/"+tc.name, func(t *testing.T) {
				req := spoofRequest(tc.remote, station.host)
				req.Header.Set("X-Forwarded-For", tc.forward)
				req.Header.Set("X-Real-IP", "192.0.2.66")
				req.Header.Set("CF-Connecting-IP", "203.0.113.66")
				if tc.https {
					req.Header.Set("X-Forwarded-Proto", "https")
				}
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, req)
				want := "station=" + station.name + " client=" + tc.client + " peer=" + tc.peer + " https=" + boolStr(tc.https) + " trusted=1"
				if rec.Code != http.StatusOK || rec.Body.String() != want {
					t.Fatalf("trusted chain => %d %q, want %q", rec.Code, rec.Body.String(), want)
				}
			})
		}
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
	cases := []struct {
		name    string
		headers map[string][]string
	}{
		{"duplicate_xff", map[string][]string{"X-Forwarded-For": {"203.0.113.9", "203.0.113.10"}}},
		{"identical_duplicate_xff", map[string][]string{"X-Forwarded-For": {"203.0.113.9", "203.0.113.9"}}},
		{"invalid_hop", map[string][]string{"X-Forwarded-For": {"203.0.113.9, garbage"}}},
		{"invalid_left_of_untrusted", map[string][]string{"X-Forwarded-For": {"garbage, 203.0.113.9, 10.1.1.1"}}},
		{"too_many_hops", map[string][]string{"X-Forwarded-For": {"203.0.113.9, " + strings.Repeat("10.1.1.1, ", 31) + "10.1.1.1"}}},
		{"too_many_bytes", map[string][]string{"X-Forwarded-For": {strings.Repeat(" ", 4096) + "203.0.113.9"}}},
		{"empty_xff", map[string][]string{"X-Forwarded-For": {""}}},
		{"empty_hop", map[string][]string{"X-Forwarded-For": {"203.0.113.9, "}}},
		{"quoted_ip", map[string][]string{"X-Forwarded-For": {`"203.0.113.9"`}}},
		{"control_xff", map[string][]string{"X-Forwarded-For": {"203.0.113.9\r\n"}}},
		{"zoned_ipv6", map[string][]string{"X-Forwarded-For": {"fe80::1%eth0"}}},
		{"invalid_ipv6", map[string][]string{"X-Forwarded-For": {"2001:db8::invalid"}}},
		{"comma_real_ip", map[string][]string{"X-Real-IP": {"6.6.6.6, 7.7.7.7"}}},
		{"duplicate_real_ip", map[string][]string{"X-Real-IP": {"203.0.113.9", "203.0.113.10"}}},
		{"duplicate_proto_values", map[string][]string{"X-Forwarded-Proto": {"https, http"}}},
		{"cf_header_only", map[string][]string{}},
	}
	for _, station := range []struct{ host, name string }{{"user.example", "user"}, {"admin.example", "admin"}} {
		for _, peer := range []struct{ name, remote, ip string }{{"ipv4", "127.0.0.1:4000", "127.0.0.1"}, {"ipv6", "[::1]:4000", "::1"}} {
			for _, tc := range cases {
				t.Run(station.name+"/"+peer.name+"/"+tc.name, func(t *testing.T) {
					req := spoofRequest(peer.remote, station.host)
					req.Header.Set("CF-Connecting-IP", "203.0.113.66")
					req.Header.Set("CF-Connecting-IPv6", "2001:db8::66")
					req.Header.Set("True-Client-IP", "203.0.113.66")
					if _, present := tc.headers["X-Forwarded-For"]; present {
						// An invalid XFF must not fall through to another IP header.
						req.Header.Set("X-Real-IP", "192.0.2.66")
					}
					for name, values := range tc.headers {
						for _, value := range values {
							req.Header.Add(name, value)
						}
					}
					rec := httptest.NewRecorder()
					handler.ServeHTTP(rec, req)
					want := "station=" + station.name + " client=" + peer.ip + " peer=" + peer.ip + " https=0 trusted=1"
					if rec.Code != http.StatusOK || rec.Body.String() != want {
						t.Fatalf("malformed headers => %d %q, want %q", rec.Code, rec.Body.String(), want)
					}
				})
			}
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
