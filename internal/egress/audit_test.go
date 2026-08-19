// Independent audit: outbound boundary attack matrix.
//
// This file exercises the shared egress policy strictly through its exported
// surface (NewEgressPolicy / NewStack / NewClient / Client.Do / Gate) with a
// recording fake dialer and fake resolver, so DNS pinning, blocked ranges,
// self-origin refusal, redirect policy, response bounds, and concurrency
// accounting are verified without any network dependency.
package egress_test

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/config"
	"github.com/waiting-here/NonbiriAPI/internal/egress"
)

// fakeResolver answers every hostname with a fixed address list so a test can
// simulate a mixed public/private answer set (DNS rebinding shape).
type fakeResolver struct {
	addresses []netip.Addr
}

func (f fakeResolver) LookupNetIP(ctx context.Context, _ string, _ string) ([]netip.Addr, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if f.addresses == nil {
		return nil, errors.New("no such host")
	}
	return f.addresses, nil
}

// recordingDialer fails all dials but records the exact address it was asked
// to connect to, so a test can assert that the policy handed it a vetted IP
// literal (never the original hostname) and that blocked targets never
// reached the dialer at all.
type recordingDialer struct {
	mu    sync.Mutex
	calls []string
}

func (d *recordingDialer) DialContext(_ context.Context, network, address string) (net.Conn, error) {
	d.mu.Lock()
	d.calls = append(d.calls, network+" "+address)
	d.mu.Unlock()
	return nil, errors.New("dial refused by test dialer")
}

func (d *recordingDialer) callsList() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.calls...)
}

const publicIPv4 = "93.184.216.34"

func testStack(t *testing.T, allowed []string, resolver egress.IPResolver, dialer egress.ContextDialer, limits egress.ConcurrencyLimits) *egress.Stack {
	t.Helper()
	if limits == (egress.ConcurrencyLimits{}) {
		limits = egress.ConcurrencyLimits{Global: 16, PerEndpoint: 8}
	}
	stack, err := egress.NewStack(egress.StackOptions{
		AllowedOrigins:   allowed,
		Resolver:         resolver,
		Dialer:           dialer,
		Concurrency:      limits,
		RequestTimeout:   3 * time.Second,
		MaxResponseBytes: 128,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Self-origin registration must precede dialing; use a port that will
	// never be dialed so the refusal path is not accidentally exercised.
	if err := stack.AddSelfOrigins(context.Background(), &config.Config{
		SiteBaseURL: "https://127.0.0.1",
		UserHost:    "127.0.0.1",
		AdminHost:   "127.0.0.2",
		ListenAddr:  "127.0.0.1:1",
	}); err != nil {
		t.Fatal(err)
	}
	return stack
}

func TestAuditEgressCanonicalizationMatrix(t *testing.T) {
	policy, err := egress.NewEgressPolicy(nil)
	if err != nil {
		t.Fatal(err)
	}
	valid := []struct {
		name string
		in   string
		want string
	}{
		{"scheme case", "HTTPS://Example.COM:443/v1", "https://example.com:443/v1"},
		{"https default port not appended", "https://example.com/v1", "https://example.com/v1"},
		{"http default port not appended", "http://example.com/v1", "http://example.com/v1"},
		{"explicit default http port kept", "http://example.com:80/v1", "http://example.com:80/v1"},
		{"explicit default https port kept", "https://example.com:443/v1", "https://example.com:443/v1"},
		{"explicit non-default port kept", "https://example.com:8443/v1", "https://example.com:8443/v1"},
		{"trailing dot", "http://example.com./v1", "http://example.com/v1"},
		{"double slashes collapsed", "http://example.com//v1//chat", "http://example.com/v1/chat"},
		{"query stripped", "http://example.com/v1?token=abc", "http://example.com/v1"},
		{"fragment stripped", "http://example.com/v1#frag", "http://example.com/v1"},
		{"bracketed ipv6", "http://[2001:4860:4860::8888]:8080/v1", "http://[2001:4860:4860::8888]:8080/v1"},
		{"path preserved", "http://example.com/a/b/", "http://example.com/a/b/"},
		{"underscore label", "http://api_v1.example.com/v1", "http://api_v1.example.com/v1"},
	}
	for _, tc := range valid {
		t.Run("valid/"+tc.name, func(t *testing.T) {
			got, err := policy.ValidateBaseURL(tc.in)
			if err != nil {
				t.Fatalf("ValidateBaseURL(%q): %v", tc.in, err)
			}
			if got != tc.want {
				t.Fatalf("ValidateBaseURL(%q)=%q want %q", tc.in, got, tc.want)
			}
		})
	}
	invalid := []struct {
		name string
		in   string
	}{
		{"empty", ""},
		{"backslash host", "http://example.com\\@evil.com/v1"},
		{"control newline", "http://example.com/v1\nx"},
		{"tab in host", "http://exa\tmple.com/v1"},
		{"unicode host", "http://例え.jp/v1"},
		{"non-http scheme", "ftp://example.com/v1"},
		{"opaque", "mailto:x@y"},
		{"empty host", "http:///v1"},
		{"port zero", "http://example.com:0/v1"},
		{"port too big", "http://example.com:65536/v1"},
		{"trailing colon", "http://example.com:/v1"},
		{"bare ipv6", "http://2001:4860:4860::8888/v1"},
		{"host with percent", "http://ex%61mple.com/v1"},
		{"label starts with dash", "http://-bad.example.com/v1"},
		{"label ends with dash", "http://bad-.example.com/v1"},
		{"empty label", "http://a..b/v1"},
		{"long host", "http://" + strings.Repeat("a", 64) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 63) + ".com/v1"},
	}
	// Per the frozen requirement, userinfo is deliberately stripped during
	// normalization ("去 userinfo/query/fragment") so an accidental
	// credential in a base URL never survives into the stored endpoint.
	for _, tc := range []struct{ in, want string }{
		{"http://user:pass@example.com/v1", "http://example.com/v1"},
		{"http://exa@mple.com/v1", "http://mple.com/v1"},
	} {
		got, err := policy.ValidateBaseURL(tc.in)
		if err != nil {
			t.Fatalf("ValidateBaseURL(%q): %v", tc.in, err)
		}
		if got != tc.want {
			t.Fatalf("ValidateBaseURL(%q)=%q want %q", tc.in, got, tc.want)
		}
	}
	for _, tc := range invalid {
		t.Run("invalid/"+tc.name, func(t *testing.T) {
			got, err := policy.ValidateBaseURL(tc.in)
			if err == nil {
				t.Fatalf("ValidateBaseURL(%q) accepted as %q", tc.in, got)
			}
			if strings.Contains(err.Error(), tc.in) && len(tc.in) > 8 {
				// The error may echo a bounded reason but must not echo a URL
				// that could contain credentials.
				t.Fatalf("error echoes input: %q", err.Error())
			}
		})
	}
}

func TestAuditEgressBlockedAddressRanges(t *testing.T) {
	policy, err := egress.NewEgressPolicy(nil)
	if err != nil {
		t.Fatal(err)
	}
	blocked := []string{
		"http://127.0.0.1:80/x",
		"http://127.0.0.2:8080/x",
		"http://[::1]:80/x",
		"http://10.0.0.1:80/x",
		"http://172.16.0.1:80/x",
		"http://192.168.1.1:80/x",
		"http://169.254.169.254:80/x", // cloud metadata
		"http://0.0.0.0:80/x",
		"http://100.64.0.1:80/x", // CGNAT
		"http://224.0.0.1:80/x",  // multicast
		"http://240.0.0.1:80/x",  // reserved
		"http://255.255.255.255:80/x",
		"http://192.0.2.1:80/x",    // documentation
		"http://198.51.100.1:80/x", // documentation
		"http://203.0.113.1:80/x",  // documentation
		"http://192.0.0.1:80/x",
		"http://198.18.0.1:80/x", // benchmarking
		"http://192.88.99.1:80/x",
		"http://[fe80::1]:80/x",          // link-local
		"http://[2001:db8::1]:80/x",      // documentation v6
		"http://[64:ff9b::1]:80/x",       // NAT64
		"http://[::ffff:127.0.0.1]:80/x", // v4-mapped loopback
		"http://[::ffff:10.1.1.1]:80/x",  // v4-mapped private
		"http://localhost:80/x",
		"http://db.local:80/x",
		"http://host.localhost:80/x",
	}
	for _, raw := range blocked {
		t.Run(raw, func(t *testing.T) {
			if _, err := policy.ValidateBaseURL(raw); err == nil {
				t.Fatalf("ValidateBaseURL(%q) accepted a blocked address", raw)
			}
		})
	}
	// A globally routable address must be accepted at save time.
	if _, err := policy.ValidateBaseURL("http://93.184.216.34:80/x"); err != nil {
		t.Fatalf("public address rejected: %v", err)
	}
}

func TestAuditEgressDNSMixedAnswerRejected(t *testing.T) {
	// DNS rebinding shape: one hostname answers with a public and a private
	// address. The dial must be refused for the whole set and the dialer
	// never called.
	dialer := &recordingDialer{}
	stack := testStack(t, nil,
		fakeResolver{addresses: []netip.Addr{netip.MustParseAddr(publicIPv4), netip.MustParseAddr("127.0.0.1")}},
		dialer, egress.ConcurrencyLimits{})
	client, err := stack.NewClient("http://rebind.example/v1")
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodGet, "http://rebind.example/v1/models", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err == nil {
		resp.Body.Close()
		t.Fatal("mixed public/private answer was dialed")
	}
	if calls := dialer.callsList(); len(calls) != 0 {
		t.Fatalf("dialer was reached for a blocked answer: %v", calls)
	}
}

func TestAuditEgressPublicOnlyAnswerPinsIPLiteral(t *testing.T) {
	dialer := &recordingDialer{}
	stack := testStack(t, nil,
		fakeResolver{addresses: []netip.Addr{netip.MustParseAddr(publicIPv4)}},
		dialer, egress.ConcurrencyLimits{})
	client, err := stack.NewClient("http://upstream.example/v1")
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodGet, "http://upstream.example/v1/models", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Do(req)
	if err == nil {
		t.Fatal("request unexpectedly succeeded")
	}
	calls := dialer.callsList()
	if len(calls) != 1 {
		t.Fatalf("expected exactly one dial, got %v", calls)
	}
	if !strings.HasPrefix(calls[0], "tcp ") || !strings.HasPrefix(calls[0], "tcp "+publicIPv4+":") {
		t.Fatalf("dialer received unvetted target %q (hostname must be pinned to the vetted IP)", calls[0])
	}
}

func TestAuditEgressAllowlistExactOriginOnly(t *testing.T) {
	// Private origins are dialable only with an exact-origin allowlist entry.
	dialer := &recordingDialer{}
	stack := testStack(t, []string{"http://127.0.0.1:18081"},
		fakeResolver{addresses: []netip.Addr{netip.MustParseAddr("127.0.0.1")}},
		dialer, egress.ConcurrencyLimits{})
	client, err := stack.NewClient("http://127.0.0.1:18081/v1")
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1:18081/v1/models", nil)
	if _, err := client.Do(req); err == nil {
		t.Fatal("unexpected dial success")
	}
	if len(dialer.callsList()) != 1 {
		t.Fatalf("allowlisted origin was not dialed: %v", dialer.callsList())
	}

	// A different port must not be covered by the entry: validation itself
	// refuses the non-allowlisted private origin.
	if _, err := stack.NewClient("http://127.0.0.1:18082/v1"); err == nil {
		t.Fatal("non-allowlisted port accepted by NewClient")
	}
	if got := len(dialer.callsList()); got != 1 {
		t.Fatalf("non-allowlisted port reached the dialer: %v", dialer.callsList())
	}
}

func TestAuditEgressAllowlistEntryShapeRejected(t *testing.T) {
	for _, entry := range []string{
		"http://127.0.0.1:18081/path", // path not allowed
		"127.0.0.1:18081",             // no scheme
		"http://user@127.0.0.1:18081", // userinfo
		"http://127.0.0.1:18081?x=1",  // query
		"http://127.0.0.1:18081#f",    // fragment
		"10.0.0.0/8",                  // CIDR is not an origin
	} {
		t.Run(entry, func(t *testing.T) {
			if _, err := egress.NewEgressPolicy([]string{entry}); err == nil {
				t.Fatalf("allowlist entry %q accepted", entry)
			}
		})
	}
}

func TestAuditEgressSelfOriginRefusedWithDelay(t *testing.T) {
	// The stack registers its own listen origin; dialing it must be refused
	// (never forwarded) and must take the minimum refusal delay.
	dialer := &recordingDialer{}
	stack, err := egress.NewStack(egress.StackOptions{
		Resolver: fakeResolver{addresses: []netip.Addr{netip.MustParseAddr(publicIPv4)}},
		Dialer:   dialer,
		// The listen origin must be allowlisted for validation to admit it;
		// the dial-time self-origin check still refuses it.
		AllowedOrigins: []string{"http://127.0.0.1:18099"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := stack.AddSelfOrigins(context.Background(), &config.Config{
		SiteBaseURL: "https://127.0.0.1",
		UserHost:    "127.0.0.1",
		AdminHost:   "127.0.0.2",
		ListenAddr:  "127.0.0.1:18099",
	}); err != nil {
		t.Fatal(err)
	}
	client, err := stack.NewClient("http://127.0.0.1:18099/v1")
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1:18099/v1/models", nil)
	started := time.Now()
	_, err = client.Do(req)
	elapsed := time.Since(started)
	if err == nil {
		t.Fatal("self-origin dial unexpectedly succeeded")
	}
	if elapsed < 140*time.Millisecond {
		t.Fatalf("self-origin refusal was too fast (%v); anti-probe delay missing", elapsed)
	}
	if calls := dialer.callsList(); len(calls) != 0 {
		t.Fatalf("self-origin dial reached the real dialer: %v", calls)
	}
}

func TestAuditEgressRedirectBlocked(t *testing.T) {
	redirected := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/latest/meta-data", http.StatusFound)
	}))
	defer redirected.Close()
	stack := testStack(t, []string{redirected.URL}, nil, nil, egress.ConcurrencyLimits{})
	client, err := stack.NewClient(redirected.URL)
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodGet, redirected.URL+"/v1", nil)
	_, err = client.Do(req)
	if !errors.Is(err, egress.ErrRedirectBlocked) {
		t.Fatalf("redirect not blocked: %v", err)
	}
}

func TestAuditEgressHostHeaderInjectionRejected(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()
	stack := testStack(t, []string{upstream.URL}, nil, nil, egress.ConcurrencyLimits{})
	client, err := stack.NewClient(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodGet, upstream.URL+"/v1", nil)
	req.Host = "evil.example" // custom Host header must be rejected
	if _, err := client.Do(req); err == nil {
		t.Fatal("custom Host header accepted")
	}
	req.Host = "" // default: same origin
	if resp, err := client.Do(req); err != nil {
		t.Fatalf("same-origin request failed: %v", err)
	} else {
		resp.Body.Close()
	}
}

func TestAuditEgressResponseBodyBound(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = io.WriteString(w, strings.Repeat("A", 4096))
	}))
	defer upstream.Close()
	stack := testStack(t, []string{upstream.URL}, nil, nil, egress.ConcurrencyLimits{})
	client, err := stack.NewClient(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodGet, upstream.URL+"/big", nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	read, err := io.Copy(io.Discard, resp.Body)
	if err == nil {
		t.Fatal("oversized response was fully read without error")
	}
	if read > 200 {
		t.Fatalf("oversized response streamed %d bytes before the bound", read)
	}
}

func TestAuditEgressGateIsolationAndRelease(t *testing.T) {
	// Limits of 1/1: two concurrent calls to the same origin serialize; a
	// call to a different canonical base URL uses an independent counter.
	var mu sync.Mutex
	active := 0
	maxActive := 0
	arrived := make(chan struct{}, 4)
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		active++
		if active > maxActive {
			maxActive = active
		}
		arrived <- struct{}{}
		mu.Unlock()
		<-release
		mu.Lock()
		active--
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()
	// Release the two admitted handlers once both are inside; the third
	// (waiting on the per-endpoint slot) then proceeds and exits immediately.
	go func() {
		<-arrived
		<-arrived
		close(release)
	}()

	stack := testStack(t, []string{upstream.URL}, nil, nil,
		egress.ConcurrencyLimits{Global: 4, PerEndpoint: 1})
	client, err := stack.NewClient(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	clientB, err := stack.NewClient(upstream.URL + "/different-path")
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	results := make(chan error, 3)
	for _, c := range []*egress.Client{client, clientB, client} {
		wg.Add(1)
		go func(c *egress.Client) {
			defer wg.Done()
			req, _ := http.NewRequest(http.MethodGet, upstream.URL+"/x", nil)
			resp, err := c.Do(req)
			if err == nil {
				resp.Body.Close()
			}
			results <- err
		}(c)
	}
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatalf("gate request failed: %v", err)
		}
	}
	mu.Lock()
	peak := maxActive
	mu.Unlock()
	// The path-differentiated client shares the origin transport but has an
	// independent per-endpoint counter: peak 2 (one per client) is the
	// expected result with PerEndpoint=1 keyed by canonical base URL.
	if peak != 2 {
		t.Fatalf("expected independent per-endpoint counters to admit 2 concurrent, got peak=%d", peak)
	}
}

func TestAuditEgressGateEquivalentOriginsShareCounter(t *testing.T) {
	// http://host:80/p and http://host/p canonicalize to the same origin and
	// must contend on the same per-endpoint slot.
	gate, err := egress.NewGate(egress.ConcurrencyLimits{Global: 2, PerEndpoint: 1})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first, err := gate.Acquire(ctx, "http://host.example:80/v1")
	if err != nil {
		t.Fatal(err)
	}
	// Second acquisition on the equivalent origin must block (context
	// cancelable) rather than admit.
	blocked := make(chan error, 1)
	go func() {
		_, err := gate.Acquire(ctx, "http://host.example/v1")
		blocked <- err
	}()
	select {
	case err := <-blocked:
		t.Fatalf("equivalent origin admitted concurrently: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	first.Release()
	select {
	case err := <-blocked:
		if err != nil {
			t.Fatalf("acquisition after release failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("acquisition never unblocked after release")
	}
}

func TestAuditEgressEnvProxyIgnored(t *testing.T) {
	// Even with proxy environment variables set, the client must dial
	// directly (transport.Proxy is nil in production wiring).
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:3128")
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:3128")
	t.Setenv("http_proxy", "http://127.0.0.1:3128")
	t.Setenv("https_proxy", "http://127.0.0.1:3128")
	t.Setenv("ALL_PROXY", "http://127.0.0.1:3128")
	dialer := &recordingDialer{}
	stack := testStack(t, nil,
		fakeResolver{addresses: []netip.Addr{netip.MustParseAddr(publicIPv4)}},
		dialer, egress.ConcurrencyLimits{})
	client, err := stack.NewClient("http://upstream.example/v1")
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodGet, "http://upstream.example/v1/models", nil)
	_, err = client.Do(req)
	if err == nil {
		t.Fatal("request unexpectedly succeeded")
	}
	calls := dialer.callsList()
	if len(calls) != 1 {
		t.Fatalf("expected direct dial, got %v", calls)
	}
	if strings.Contains(calls[0], "3128") || strings.Contains(calls[0], "127.0.0.1") {
		t.Fatalf("request went through the environment proxy: %v", calls)
	}
}

func TestAuditEgressConcurrentCancelReleasesPermit(t *testing.T) {
	// A canceled waiter must never consume a slot and must unblock others.
	gate, err := egress.NewGate(egress.ConcurrencyLimits{Global: 1, PerEndpoint: 1})
	if err != nil {
		t.Fatal(err)
	}
	ctx1, cancel1 := context.WithCancel(context.Background())
	first, err := gate.Acquire(ctx1, "http://a.example/v1")
	if err != nil {
		t.Fatal(err)
	}
	ctx2, cancel2 := context.WithCancel(context.Background())
	waiter := make(chan error, 1)
	go func() {
		_, err := gate.Acquire(ctx2, "http://a.example/v1")
		waiter <- err
	}()
	time.Sleep(100 * time.Millisecond)
	cancel2()
	select {
	case err := <-waiter:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled waiter returned %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("canceled waiter never returned")
	}
	first.Release()
	cancel1()
	ctx3, cancel3 := context.WithCancel(context.Background())
	defer cancel3()
	third, err := gate.Acquire(ctx3, "http://a.example/v1")
	if err != nil {
		t.Fatalf("slot never freed: %v", err)
	}
	third.Release()
}
