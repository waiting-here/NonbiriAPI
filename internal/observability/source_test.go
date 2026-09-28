package observability

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/httpmw"
)

func TestSourceFixedAllowlistAndOrigins(t *testing.T) {
	r := httptest.NewRequest("GET", "https://example.invalid/v1/models?secret=query", strings.NewReader("body secret"))
	r.RemoteAddr = "[::ffff:192.0.2.5]:1234"
	r.Header.Set("Authorization", "Bearer auth-secret")
	r.Header.Set("Cookie", "cookie-secret")
	r.Header["User-Agent"] = []string{"first\x00agent\u202e", "second-agent"}
	r.Header.Set("Origin", "https://User:password@EXAMPLE.invalid:443/path?secret=hidden#part")
	r.Header.Set("Referer", "https://example.invalid:8443/deep?hidden=query")
	r.Header.Set("HTTP-Referer", "file:///private/path")
	r.Header.Set("X-OpenRouter-Title", "modern")
	r.Header.Set("X-Title", "legacy")
	r.Header.Set("X-Stainless-Lang", "go")
	r.Header.Set("X-Stainless-Package-Version", "1.2.3")
	r.Header.Set("CF-Connecting-IP", "cf-source-secret")
	r.Header.Set("CF-Connecting-IPv6", "cf-ipv6-secret")
	r.Header.Set("CF-Ray", "cf-ray-secret")
	r.Header.Set("True-Client-IP", "true-client-secret")
	r.Header.Set("X-Forwarded-For", "xff-source-secret")
	r.Header.Set("X-Real-IP", "real-ip-secret")
	r.Header.Set("Forwarded", "forwarded-source-secret")
	s := CaptureSource(r)
	if s.EffectiveIP != "192.0.2.5" || s.UserAgent != "firstagent" || !s.Quality["user_agent"].Multiple || s.Origin != "https://example.invalid" || s.Referer != "https://example.invalid:8443" || s.HTTPReferer != "" || !s.Quality["http_referer"].Invalid || s.OpenRouterTitle != "modern" || s.LegacyTitle != "legacy" {
		t.Fatalf("wrong source: %#v", s)
	}
	encoded, _ := json.Marshal(s)
	if _, err := ParseSource(encoded); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"password", "query", "hidden", "auth-secret", "cookie-secret", "body secret", "second-agent", "cf-source-secret", "cf-ipv6-secret", "cf-ray-secret", "true-client-secret", "xff-source-secret", "real-ip-secret", "forwarded-source-secret"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("source contains %q", forbidden)
		}
	}
	if strings.Contains(fmt.Sprintf("%+v %#v", s, s), "firstagent") {
		t.Fatal("formatting exposed source")
	}
}

func TestSourceUTF8AndAggregateBounds(t *testing.T) {
	r := httptest.NewRequest("GET", "https://example.invalid", nil)
	r.Header.Set("User-Agent", strings.Repeat("長<", 3000)+string([]byte{0xff}))
	for _, name := range []string{"Origin", "Referer", "HTTP-Referer"} {
		r.Header.Set(name, "https://"+strings.Repeat("<", 510)+".invalid/path")
	}
	for _, name := range []string{"X-Title", "X-OpenRouter-Title", "X-Stainless-Lang", "X-Stainless-Package-Version", "X-Stainless-Runtime", "X-Stainless-Runtime-Version"} {
		r.Header.Set(name, strings.Repeat("<", 500))
	}
	s := CaptureSource(r)
	encoded, _ := json.Marshal(s)
	if len(encoded) > MaxSourceBytes || len(s.UserAgent) > 2048 || !s.Quality["user_agent"].Truncated || !s.Quality["user_agent"].Invalid {
		t.Fatal("source limits failed")
	}
	if _, err := ParseSource(encoded); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{`{"ip_quality":"direct_peer","authorization":"secret"}`, `{"ip_quality":"direct_peer","origin":"https://x.invalid/path"}`, `{"ip_quality":"direct_peer","ip_quality":"peer_fallback"}`, `{"ip_quality":"direct_peer"} {}`} {
		if _, err := ParseSource([]byte(bad)); err == nil {
			t.Fatal("accepted arbitrary source")
		}
	}
}

func TestSourceIPProvenanceIsAuthoritative(t *testing.T) {
	for _, station := range []struct{ host, name string }{{"example.invalid", "user"}, {"admin.example.invalid", "admin"}} {
		for _, tc := range []struct {
			name, peer, wantIP, quality string
			forward, realIP             []string
		}{
			{"untrusted_ipv4", "198.51.100.4:42", "198.51.100.4", "direct_peer", []string{"203.0.113.9"}, []string{"192.0.2.66"}},
			{"untrusted_ipv6", "[2001:db8::4]:42", "2001:db8::4", "direct_peer", []string{"203.0.113.9, 127.0.0.1"}, nil},
			{"untrusted_cloudflare", "173.245.48.10:42", "173.245.48.10", "direct_peer", []string{"203.0.113.9"}, nil},
			{"other_loopback", "127.0.0.2:42", "127.0.0.2", "direct_peer", []string{"203.0.113.9"}, nil},
			{"trusted_ipv4", "127.0.0.1:42", "203.0.113.9", "trusted_forwarded", []string{"203.0.113.9"}, []string{"192.0.2.66"}},
			{"trusted_ipv6", "[::1]:42", "2001:db8::9", "trusted_forwarded", []string{"2001:db8::9"}, nil},
			{"mapped_ipv4", "[::1]:42", "192.0.2.7", "trusted_forwarded", []string{"::ffff:192.0.2.7"}, nil},
			{"trusted_hops", "127.0.0.1:42", "203.0.113.9", "trusted_forwarded", []string{"203.0.113.9, ::ffff:127.0.0.1, ::1"}, nil},
			{"forged_leftmost", "[::1]:42", "198.51.100.9", "trusted_forwarded", []string{"203.0.113.99, 198.51.100.9, 127.0.0.1, ::1"}, nil},
			{"invalid_xff", "127.0.0.1:42", "127.0.0.1", "peer_fallback", []string{"invalid"}, []string{"192.0.2.66"}},
			{"invalid_left_of_untrusted", "[::1]:42", "::1", "peer_fallback", []string{"invalid, 203.0.113.9, 127.0.0.1"}, []string{"192.0.2.66"}},
			{"duplicate_xff", "127.0.0.1:42", "127.0.0.1", "peer_fallback", []string{"203.0.113.9", "203.0.113.10"}, []string{"192.0.2.66"}},
			{"empty_xff", "[::1]:42", "::1", "peer_fallback", []string{""}, []string{"192.0.2.66"}},
			{"missing_xff", "127.0.0.1:42", "127.0.0.1", "peer_fallback", nil, nil},
			{"real_ip_without_xff", "[::1]:42", "2001:db8::9", "trusted_forwarded", nil, []string{"2001:db8::9"}},
			{"duplicate_real_ip", "127.0.0.1:42", "127.0.0.1", "peer_fallback", nil, []string{"203.0.113.9", "203.0.113.10"}},
			{"invalid_peer", "invalid:42", "", "peer_fallback", []string{"203.0.113.9"}, nil},
		} {
			t.Run(station.name+"/"+tc.name, func(t *testing.T) {
				var source Source
				called := false
				handler, err := httpmw.New(httpmw.Config{UserHost: "example.invalid", AdminHost: "admin.example.invalid", SiteBaseURL: "https://example.invalid", TrustedProxyCIDRs: []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32"), netip.MustParsePrefix("::1/128")}}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					called = true
					if got := httpmw.StationOf(r).String(); got != station.name {
						t.Fatalf("station=%q, want %q", got, station.name)
					}
					source = CaptureSource(r)
					w.WriteHeader(http.StatusNoContent)
				}))
				if err != nil {
					t.Fatal(err)
				}
				r := httptest.NewRequest(http.MethodGet, "https://"+station.host+"/healthz", nil)
				r.RemoteAddr = tc.peer
				for _, value := range tc.forward {
					r.Header.Add("X-Forwarded-For", value)
				}
				for _, value := range tc.realIP {
					r.Header.Add("X-Real-IP", value)
				}
				r.Header.Set("CF-Connecting-IP", "203.0.113.250")
				r.Header.Set("CF-Connecting-IPv6", "2001:db8::250")
				r.Header.Set("True-Client-IP", "192.0.2.250")
				r.Header.Set("CF-Ray", "raw-cf-ray-only")
				r.Header.Set("Forwarded", "for=192.0.2.251;proto=https")
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, r)
				if !called || rec.Code != http.StatusNoContent {
					t.Fatalf("source handler called=%v status=%d", called, rec.Code)
				}
				if source.EffectiveIP != tc.wantIP || source.IPQuality != tc.quality {
					t.Fatalf("peer=%s source=%s/%s, want %s/%s", tc.peer, source.EffectiveIP, source.IPQuality, tc.wantIP, tc.quality)
				}
				encoded, err := json.Marshal(source)
				if err != nil {
					t.Fatal(err)
				}
				// Only the edge-selected address and its provenance may survive.
				// This also detects raw headers hidden in additional JSON fields.
				want := fmt.Sprintf(`{"effective_ip":%q,"ip_quality":%q}`, tc.wantIP, tc.quality)
				if string(encoded) != want {
					t.Fatalf("source JSON=%s, want %s", encoded, want)
				}
				parsed, err := ParseSource(encoded)
				if err != nil || parsed.EffectiveIP != tc.wantIP || parsed.IPQuality != tc.quality {
					t.Fatalf("persisted source changed: %s err=%v", encoded, err)
				}
			})
		}
	}
}

func TestCapturedSourceOwnsHeaderQuality(t *testing.T) {
	s := Source{IPQuality: "direct_peer", Quality: map[string]FieldQuality{"user_agent": {Multiple: true}}}
	ctx := WithSource(context.Background(), s)
	s.Quality["user_agent"] = FieldQuality{}
	got, ok := SourceFromContext(ctx)
	if !ok || !got.Quality["user_agent"].Multiple {
		t.Fatal("source context shares mutable input")
	}
}
