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
	s := CaptureSource(r)
	if s.EffectiveIP != "192.0.2.5" || s.UserAgent != "firstagent" || !s.Quality["user_agent"].Multiple || s.Origin != "https://example.invalid" || s.Referer != "https://example.invalid:8443" || s.HTTPReferer != "" || !s.Quality["http_referer"].Invalid || s.OpenRouterTitle != "modern" || s.LegacyTitle != "legacy" {
		t.Fatalf("wrong source: %#v", s)
	}
	encoded, _ := json.Marshal(s)
	if _, err := ParseSource(encoded); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"password", "query", "hidden", "auth-secret", "cookie-secret", "body secret", "second-agent"} {
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
	for _, tc := range []struct{ peer, forward, wantIP, quality string }{
		{"198.51.100.4:42", "203.0.113.9", "198.51.100.4", "direct_peer"},
		{"127.0.0.1:42", "203.0.113.9", "203.0.113.9", "trusted_forwarded"},
		{"127.0.0.1:42", "invalid", "127.0.0.1", "peer_fallback"},
		{"127.0.0.1:42", "", "127.0.0.1", "peer_fallback"},
		{"[::1]:42", "::ffff:192.0.2.7", "192.0.2.7", "trusted_forwarded"},
	} {
		var source Source
		handler, err := httpmw.New(httpmw.Config{UserHost: "example.invalid", AdminHost: "admin.example.invalid", SiteBaseURL: "https://example.invalid", TrustedProxyCIDRs: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8"), netip.MustParsePrefix("::1/128")}}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { source = CaptureSource(r) }))
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest("GET", "https://example.invalid/v1/models", nil)
		r.RemoteAddr = tc.peer
		if tc.forward != "" {
			r.Header.Set("X-Forwarded-For", tc.forward)
		}
		handler.ServeHTTP(httptest.NewRecorder(), r)
		if source.EffectiveIP != tc.wantIP || source.IPQuality != tc.quality {
			t.Fatalf("peer=%s source=%s/%s", tc.peer, source.EffectiveIP, source.IPQuality)
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
