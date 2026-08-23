package egress

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/config"
)

type staticResolver struct {
	mu        sync.Mutex
	byHost    map[string][]netip.Addr
	addresses []netip.Addr
	err       error
	calls     []string
}

func (r *staticResolver) LookupNetIP(ctx context.Context, _ string, host string) ([]netip.Addr, error) {
	r.mu.Lock()
	r.calls = append(r.calls, host)
	addresses := r.addresses
	if r.byHost != nil {
		addresses = r.byHost[host]
	}
	err := r.err
	r.mu.Unlock()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	return append([]netip.Addr(nil), addresses...), err
}

func publicResolver() *staticResolver {
	return &staticResolver{addresses: []netip.Addr{netip.MustParseAddr("93.184.216.34")}}
}

func selfConfig(listenAddr string) *config.Config {
	return &config.Config{
		SiteBaseURL: "https://gateway.example",
		UserHost:    "gateway.example",
		AdminHost:   "admin.gateway.example",
		ListenAddr:  listenAddr,
	}
}

func newLoopbackStack(t *testing.T, allowed []string, mutate func(*StackOptions)) *Stack {
	t.Helper()
	options := StackOptions{
		AllowedOrigins: allowed,
		Resolver:       publicResolver(),
		RequestTimeout: 5 * time.Second,
	}
	if mutate != nil {
		mutate(&options)
	}
	stack, err := NewStack(options)
	if err != nil {
		t.Fatalf("NewStack: %v", err)
	}
	if err := stack.AddSelfOrigins(context.Background(), selfConfig("127.0.0.1:1")); err != nil {
		t.Fatalf("AddSelfOrigins: %v", err)
	}
	t.Cleanup(stack.CloseIdleConnections)
	return stack
}

func newLoopbackClient(t *testing.T, baseURL string, mutate func(*StackOptions)) *Client {
	t.Helper()
	stack := newLoopbackStack(t, []string{baseURL}, mutate)
	client, err := stack.NewClient(baseURL)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return client
}

func requireFakeRefusal(t *testing.T, err error) {
	t.Helper()
	if err == nil || !errors.Is(err, syscall.ECONNREFUSED) {
		t.Fatalf("self-origin error = %T %v, want connection refused", err, err)
	}
	var opErr *net.OpError
	if !errors.As(err, &opErr) || opErr.Op != "dial" {
		t.Fatalf("self-origin error = %T %v, want dial *net.OpError", err, err)
	}
}

func TestEgressPolicyBaseURLValidation(t *testing.T) {
	policy, err := NewEgressPolicy(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{
		"file:///etc/passwd",
		"https://",
		"https://exa mple.com",
		"http://127.0.0.1:8080",
		"http://169.254.169.254/latest/meta-data",
		"http://100.100.100.200/latest/meta-data",
		"http://[::1]:8080",
		"http://[fe80::1]/",
	} {
		if _, err := policy.ValidateBaseURL(raw); err == nil {
			t.Errorf("ValidateBaseURL(%q) should reject", raw)
		}
	}

	got, err := policy.ValidateBaseURL(" HTTPS://user:secret@EXAMPLE.COM.:443//api///v1/?token=x#fragment ")
	if err != nil {
		t.Fatalf("canonicalization failed: %v", err)
	}
	if want := "https://example.com:443/api/v1/"; got != want {
		t.Fatalf("canonical URL = %q, want %q", got, want)
	}
	withoutSlash, err := policy.ValidateBaseURL("https://example.com/v1")
	if err != nil {
		t.Fatal(err)
	}
	withSlash, err := policy.ValidateBaseURL("https://example.com/v1/")
	if err != nil {
		t.Fatal(err)
	}
	if withoutSlash == withSlash {
		t.Fatalf("trailing slash distinction was lost: %q", withoutSlash)
	}
	if withoutSlash != "https://example.com/v1" || withSlash != "https://example.com/v1/" {
		t.Fatalf("unexpected path normalization: %q / %q", withoutSlash, withSlash)
	}

	allowed, err := NewEgressPolicy([]string{"http://127.0.0.1:9000", "http://[::1]:8080"})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := allowed.ValidateBaseURL("http://127.0.0.1:9000/v1/"); err != nil || got != "http://127.0.0.1:9000/v1/" {
		t.Fatalf("exact-origin allowlist normalization = %q, %v", got, err)
	}
	if _, err := allowed.ValidateBaseURL("http://[::1]:8080"); err != nil {
		t.Fatalf("exact origin allowlist should allow loopback: %v", err)
	}
	if _, err := allowed.ValidateBaseURL("http://127.0.0.1:8081"); err == nil {
		t.Fatal("loopback on a non-allowlisted port should be rejected")
	}
}

func TestCanonicalEndpointTargetUsesEffectiveOriginAndPreservesPath(t *testing.T) {
	implicitTarget, implicitOrigin, err := CanonicalEndpointTarget("HTTPS://EXAMPLE.COM./api//v1")
	if err != nil {
		t.Fatal(err)
	}
	explicitTarget, explicitOrigin, err := CanonicalEndpointTarget("https://example.com:443/api/v1")
	if err != nil {
		t.Fatal(err)
	}
	if implicitTarget != "https://example.com:443/api/v1" || explicitTarget != implicitTarget {
		t.Fatalf("equivalent targets = %q / %q", implicitTarget, explicitTarget)
	}
	if implicitOrigin != "https://example.com:443" || explicitOrigin != implicitOrigin {
		t.Fatalf("equivalent origins = %q / %q", implicitOrigin, explicitOrigin)
	}

	pathTarget, pathOrigin, err := CanonicalEndpointTarget("https://example.com/api/v2")
	if err != nil {
		t.Fatal(err)
	}
	if pathTarget == implicitTarget || pathOrigin != implicitOrigin {
		t.Fatalf("path comparison target=%q origin=%q", pathTarget, pathOrigin)
	}

	_, httpOrigin, err := CanonicalEndpointTarget("http://example.com/api/v1")
	if err != nil {
		t.Fatal(err)
	}
	if httpOrigin == implicitOrigin {
		t.Fatalf("scheme change retained origin %q", httpOrigin)
	}
}

func TestEgressPolicyExactOriginOnly(t *testing.T) {
	for _, entry := range []string{
		"10.0.0.0/8",
		"2001:db8::/32",
		"0.0.0.0/0",
		"10.1.2.3",
		"::1",
		"127.0.0.1",
		"http://10.0.0.1/path",
		"http://10.0.0.1?query=x",
		"http://user:secret@10.0.0.1",
	} {
		if _, err := NewEgressPolicy([]string{entry}); err == nil {
			t.Errorf("NewEgressPolicy(%q) should reject non-origin entry", entry)
		}
	}
	for _, entry := range []string{
		"http://127.0.0.1:9000",
		"http://[::1]:8080/",
		"https://private.example:5001",
	} {
		if _, err := NewEgressPolicy([]string{entry}); err != nil {
			t.Errorf("NewEgressPolicy(%q) should accept: %v", entry, err)
		}
	}

	policy, err := NewEgressPolicy([]string{"https://PRIVATE.EXAMPLE.:443"})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := policy.ValidateBaseURL("https://private.example/api/"); err != nil || got != "https://private.example/api/" {
		t.Fatalf("allowlist default-port equivalence = %q, %v", got, err)
	}
}

func TestEgressPolicyRejectsMixedDNSAnswer(t *testing.T) {
	policy, _ := NewEgressPolicy(nil)
	policy.resolver = &staticResolver{addresses: []netip.Addr{
		netip.MustParseAddr("93.184.216.34"),
		netip.MustParseAddr("127.0.0.1"),
	}}
	dial := policy.dialContext("https", "upstream.example", "443")
	_, err := dial(context.Background(), "tcp", "upstream.example:443")
	if err == nil || !strings.Contains(err.Error(), "blocked address") {
		t.Fatalf("mixed DNS answer error = %v", err)
	}
}

func TestEgressPolicyAllowlistedOriginMayDialLoopback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"data":[]}`)
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	policy, err := NewEgressPolicy([]string{srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	dial := policy.dialContext("http", "127.0.0.1", u.Port())
	conn, err := dial(context.Background(), "tcp", u.Host)
	if err != nil {
		t.Fatalf("allowlisted loopback origin should dial: %v", err)
	}
	_ = conn.Close()
}

func TestEgressPolicySelfOriginLiteralIPBlocked(t *testing.T) {
	policy, err := NewEgressPolicy([]string{"http://127.0.0.1:9000"})
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		SiteBaseURL: "http://127.0.0.1:9000",
		UserHost:    "127.0.0.1",
		AdminHost:   "127.0.0.2",
		ListenAddr:  "127.0.0.1:9000",
	}
	if err := policy.AddSelfOrigins(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	dial := policy.dialContext("http", "127.0.0.1", "9000")
	start := time.Now()
	_, err = dial(context.Background(), "tcp", "127.0.0.1:9000")
	requireFakeRefusal(t, err)
	elapsed := time.Since(start)
	if elapsed < 140*time.Millisecond || elapsed > 750*time.Millisecond {
		t.Errorf("self-origin refusal delay = %v, want 150-350ms plus scheduler slack", elapsed)
	}
}

func TestEgressPolicySelfOriginNonGatewayPortDialed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	policy, err := NewEgressPolicy([]string{srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		SiteBaseURL: "http://127.0.0.1:1",
		UserHost:    "127.0.0.1",
		AdminHost:   "127.0.0.2",
		ListenAddr:  "127.0.0.1:1",
	}
	if err := policy.AddSelfOrigins(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	dial := policy.dialContext("http", "127.0.0.1", u.Port())
	conn, err := dial(context.Background(), "tcp", u.Host)
	if err != nil {
		t.Fatalf("same-host non-listener port must dial normally: %v", err)
	}
	_ = conn.Close()
}

func TestEgressPolicySelfOriginHostnameBlocked(t *testing.T) {
	policy, _ := NewEgressPolicy(nil)
	policy.resolver = &staticResolver{addresses: []netip.Addr{netip.MustParseAddr("93.184.216.34")}}
	cfg := &config.Config{
		SiteBaseURL: "https://gateway.example",
		UserHost:    "gateway.example",
		AdminHost:   "admin.example",
		ListenAddr:  ":10086",
	}
	if err := policy.AddSelfOrigins(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}

	dial := policy.dialContext("https", "gateway.example", "443")
	start := time.Now()
	_, err := dial(context.Background(), "tcp", "gateway.example:443")
	requireFakeRefusal(t, err)
	if elapsed := time.Since(start); elapsed < 140*time.Millisecond || elapsed > 750*time.Millisecond {
		t.Errorf("self hostname refusal delay = %v", elapsed)
	}

	dialListener := policy.dialContext("https", "gateway.example", "10086")
	_, err = dialListener(context.Background(), "tcp", "gateway.example:10086")
	requireFakeRefusal(t, err)
}

func TestEgressPolicySelfOriginUnrelatedIPNotBlocked(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	policy, err := NewEgressPolicy([]string{srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		SiteBaseURL: "http://127.0.0.2:1",
		UserHost:    "127.0.0.2",
		AdminHost:   "127.0.0.3",
		ListenAddr:  "127.0.0.2:1",
	}
	if err := policy.AddSelfOrigins(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	dial := policy.dialContext("http", "127.0.0.1", u.Port())
	conn, err := dial(context.Background(), "tcp", u.Host)
	if err != nil {
		t.Fatalf("unrelated address and non-listener port must dial: %v", err)
	}
	_ = conn.Close()
}

func TestEgressPolicySelfOriginAllowlistCannotOverride(t *testing.T) {
	policy, err := NewEgressPolicy([]string{"http://127.0.0.1:9000"})
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		SiteBaseURL: "http://127.0.0.1:9000",
		UserHost:    "127.0.0.1",
		AdminHost:   "127.0.0.2",
		ListenAddr:  "127.0.0.1:9000",
	}
	if err := policy.AddSelfOrigins(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	_, err = policy.dialContext("http", "127.0.0.1", "9000")(context.Background(), "tcp", "127.0.0.1:9000")
	requireFakeRefusal(t, err)
}

func TestEgressPolicySelfLoopbackAllowlistedWithNonLiteralListenAddr(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	acceptDone := make(chan struct{})
	go func() {
		defer close(acceptDone)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()

	allowlisted := fmt.Sprintf("http://127.0.0.1:%d", port)
	policy, err := NewEgressPolicy([]string{allowlisted})
	if err != nil {
		t.Fatal(err)
	}
	policy.resolver = publicResolver()
	if err := policy.AddSelfOrigins(context.Background(), selfConfig("localhost:"+strconv.Itoa(port))); err != nil {
		t.Fatal(err)
	}

	control, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
	if err != nil {
		t.Fatalf("control dial failed: %v", err)
	}
	_ = control.Close()

	_, err = policy.dialContext("http", "127.0.0.1", strconv.Itoa(port))(context.Background(), "tcp", fmt.Sprintf("127.0.0.1:%d", port))
	requireFakeRefusal(t, err)
	_ = listener.Close()
	<-acceptDone
}

func TestEgressPolicySaveStageDoesNotRevealSelf(t *testing.T) {
	policy, _ := NewEgressPolicy(nil)
	policy.resolver = publicResolver()
	if err := policy.AddSelfOrigins(context.Background(), selfConfig(":10086")); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"https://gateway.example", "https://93.184.216.34"} {
		if _, err := policy.ValidateBaseURL(raw); err != nil {
			t.Errorf("ValidateBaseURL(%q) must pass at save time: %v", raw, err)
		}
	}
}

func TestEgressPolicySelfIPsIncludeV4AndV6(t *testing.T) {
	policy, _ := NewEgressPolicy(nil)
	policy.resolver = &staticResolver{addresses: []netip.Addr{
		netip.MustParseAddr("93.184.216.34"),
		netip.MustParseAddr("2606:2800:220:1:248:1893:25c8:1946"),
	}}
	if err := policy.AddSelfOrigins(context.Background(), selfConfig(":10086")); err != nil {
		t.Fatal(err)
	}
	policy.mu.RLock()
	defer policy.mu.RUnlock()
	for _, want := range []string{"93.184.216.34", "2606:2800:220:1:248:1893:25c8:1946"} {
		if _, ok := policy.selfIPs[netip.MustParseAddr(want)]; !ok {
			t.Errorf("self IP set missing %s", want)
		}
	}
	if _, ok := policy.selfPorts[10086]; !ok {
		t.Errorf("self listener port missing: %v", policy.selfPorts)
	}
	if _, ok := policy.selfOrigins["https://gateway.example:443"]; !ok {
		t.Errorf("self origin missing: %v", policy.selfOrigins)
	}
}

func TestEgressPolicyPortSuffixedAdminHostRegistered(t *testing.T) {
	policy, _ := NewEgressPolicy(nil)
	policy.resolver = publicResolver()
	cfg := selfConfig(":10086")
	cfg.AdminHost = "admin.example:8443"
	if err := policy.AddSelfOrigins(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	policy.mu.RLock()
	_, hasHTTP := policy.selfOrigins["http://admin.example:8443"]
	_, hasHTTPS := policy.selfOrigins["https://admin.example:8443"]
	policy.mu.RUnlock()
	if !hasHTTP || !hasHTTPS {
		t.Fatalf("port-suffixed administrator origin missing: http=%v https=%v", hasHTTP, hasHTTPS)
	}
	_, err := policy.dialContext("https", "admin.example", "8443")(context.Background(), "tcp", "admin.example:8443")
	requireFakeRefusal(t, err)
}

func TestEgressPolicySelfOriginConcurrentDials(t *testing.T) {
	policy, err := NewEgressPolicy([]string{"http://127.0.0.1:9000"})
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		SiteBaseURL: "http://127.0.0.1:9000",
		UserHost:    "127.0.0.1",
		AdminHost:   "127.0.0.2",
		ListenAddr:  "127.0.0.1:9000",
	}
	if err := policy.AddSelfOrigins(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	dial := policy.dialContext("http", "127.0.0.1", "9000")
	var wait sync.WaitGroup
	for i := 0; i < 8; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, err := dial(context.Background(), "tcp", "127.0.0.1:9000")
			if err == nil || !errors.Is(err, syscall.ECONNREFUSED) {
				t.Errorf("concurrent self dial error = %v", err)
			}
		}()
	}
	wait.Wait()
}

func TestEgressPolicyDisablesRedirects(t *testing.T) {
	var targetHits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		targetHits.Add(1)
		_, _ = io.WriteString(w, `{"data":[]}`)
	}))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+r.URL.Path, http.StatusFound)
	}))
	defer redirect.Close()

	client := newLoopbackClient(t, redirect.URL, nil)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, client.BaseURL()+"/models", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Do(req)
	if !errors.Is(err, ErrRedirectBlocked) {
		t.Fatalf("redirect error = %T %v", err, err)
	}
	if targetHits.Load() != 0 {
		t.Fatalf("redirect target was contacted %d times", targetHits.Load())
	}
}

func TestClientResponseLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, strings.Repeat("x", 1024))
	}))
	defer srv.Close()
	client := newLoopbackClient(t, srv.URL, func(options *StackOptions) {
		options.MaxResponseBytes = 128
	})
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, client.BaseURL(), nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	var tooLarge *ResponseTooLargeError
	if !errors.As(err, &tooLarge) {
		t.Fatalf("response limit error = %T %v", err, err)
	}
	if len(body) > 128 || tooLarge.Limit != 128 {
		t.Fatalf("bounded body len=%d limit=%d", len(body), tooLarge.Limit)
	}
}

func TestClientBlockingCancellationPropagates(t *testing.T) {
	started := make(chan struct{})
	serverCanceled := make(chan struct{})
	releaseServer := make(chan struct{})
	var startOnce sync.Once
	var cancelOnce sync.Once
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/fast" {
			_, _ = io.WriteString(w, "ok")
			return
		}
		startOnce.Do(func() { close(started) })
		select {
		case <-r.Context().Done():
			cancelOnce.Do(func() { close(serverCanceled) })
		case <-releaseServer:
		}
	}))
	defer func() {
		close(releaseServer)
		srv.Close()
	}()
	client := newLoopbackClient(t, srv.URL, func(options *StackOptions) {
		options.RequestTimeout = 30 * time.Second
		options.Concurrency = ConcurrencyLimits{Global: 1, PerEndpoint: 1}
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, client.BaseURL()+"/block", nil)
		_, err := client.Do(req)
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("blocking request did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel error = %T %v", err, err)
		}
	case <-time.After(time.Second):
		t.Fatal("blocking request did not cancel promptly")
	}
	select {
	case <-serverCanceled:
	case <-time.After(time.Second):
		t.Fatal("server did not observe request cancellation")
	}

	fastCtx, fastCancel := context.WithTimeout(context.Background(), time.Second)
	defer fastCancel()
	fastReq, _ := http.NewRequestWithContext(fastCtx, http.MethodGet, client.BaseURL()+"/fast", nil)
	resp, err := client.Do(fastReq)
	if err != nil {
		t.Fatalf("concurrency permit was not released: %v", err)
	}
	_, err = io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
}

func TestClientStreamCancellationClosesChannels(t *testing.T) {
	started := make(chan struct{})
	serverCanceled := make(chan struct{})
	releaseServer := make(chan struct{})
	var startOnce sync.Once
	var cancelOnce sync.Once
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		startOnce.Do(func() { close(started) })
		select {
		case <-r.Context().Done():
			cancelOnce.Do(func() { close(serverCanceled) })
		case <-releaseServer:
		}
	}))
	defer func() {
		close(releaseServer)
		srv.Close()
	}()
	client := newLoopbackClient(t, srv.URL, func(options *StackOptions) {
		options.RequestTimeout = 30 * time.Second
	})
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, client.BaseURL(), nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	events, errs := StreamSSE(resp.Request.Context(), resp.Body, SSEOptions{MaxBytes: 1024})
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("stream request did not start")
	}
	cancel()
	select {
	case _, ok := <-events:
		if ok {
			t.Fatal("unexpected event after cancellation")
		}
	case <-time.After(time.Second):
		t.Fatal("event channel did not close after cancellation")
	}
	select {
	case _, ok := <-errs:
		if ok {
			t.Fatal("cancellation should not emit a stream error")
		}
	case <-time.After(time.Second):
		t.Fatal("error channel did not close after cancellation")
	}
	select {
	case <-serverCanceled:
	case <-time.After(time.Second):
		t.Fatal("upstream stream did not observe cancellation")
	}
}

func TestParseSSECumulativeLimit(t *testing.T) {
	events := make(chan SSEEvent, 1)
	err := ParseSSE(context.Background(), strings.NewReader(":"+strings.Repeat("x", 256)+"\n"), events, SSEOptions{
		MaxBytes:     128,
		MaxLineBytes: 1024,
	})
	var tooLarge *ResponseTooLargeError
	if !errors.As(err, &tooLarge) {
		t.Fatalf("SSE cumulative limit error = %T %v", err, err)
	}
}

func TestParseSSEBackpressureCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	events := make(chan SSEEvent)
	done := make(chan error, 1)
	go func() {
		done <- ParseSSE(ctx, strings.NewReader("data: {\"event\":\"chunk\"}\n\n"), events, SSEOptions{MaxBytes: 1024})
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("backpressure cancellation error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("SSE parser remained blocked on event delivery")
	}
}

func TestGateGlobalAndCanonicalPerEndpointLimits(t *testing.T) {
	t.Run("global across endpoint keys", func(t *testing.T) {
		gate, err := NewGate(ConcurrencyLimits{Global: 1, PerEndpoint: 2})
		if err != nil {
			t.Fatal(err)
		}
		first, err := gate.Acquire(context.Background(), "https://one.example/v1")
		if err != nil {
			t.Fatal(err)
		}
		defer first.Release()
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		if _, err := gate.Acquire(ctx, "https://two.example/v1"); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("global gate wait error = %v", err)
		}
	})

	t.Run("equivalent base URLs share per-endpoint counter", func(t *testing.T) {
		gate, err := NewGate(ConcurrencyLimits{Global: 4, PerEndpoint: 1})
		if err != nil {
			t.Fatal(err)
		}
		first, err := gate.Acquire(context.Background(), "HTTPS://SUPPLIER.EXAMPLE.:443/api//v1")
		if err != nil {
			t.Fatal(err)
		}
		acquired := make(chan *Permit, 1)
		acquireErr := make(chan error, 1)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		go func() {
			permit, err := gate.Acquire(ctx, "https://supplier.example/api/v1")
			if err != nil {
				acquireErr <- err
				return
			}
			acquired <- permit
		}()
		select {
		case permit := <-acquired:
			permit.Release()
			t.Fatal("equivalent base URL bypassed per-endpoint limit")
		case err := <-acquireErr:
			t.Fatalf("equivalent base URL wait failed early: %v", err)
		case <-time.After(50 * time.Millisecond):
		}

		separateCtx, separateCancel := context.WithTimeout(context.Background(), time.Second)
		defer separateCancel()
		separate, err := gate.Acquire(separateCtx, "https://supplier.example/api/v1/")
		if err != nil {
			t.Fatalf("trailing-slash-distinct base URL should have its own limit: %v", err)
		}
		separate.Release()

		first.Release()
		select {
		case permit := <-acquired:
			permit.Release()
		case err := <-acquireErr:
			t.Fatalf("waiting equivalent base URL failed: %v", err)
		case <-time.After(time.Second):
			t.Fatal("waiting equivalent base URL did not acquire after release")
		}
	})
}

func TestGateCancellationAndRuntimeLimitUpdate(t *testing.T) {
	gate, err := NewGate(ConcurrencyLimits{Global: 2, PerEndpoint: 1})
	if err != nil {
		t.Fatal(err)
	}
	first, err := gate.Acquire(context.Background(), "https://supplier.example/v1")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := gate.Acquire(ctx, "https://supplier.example:443/v1")
		done <- err
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("gate cancellation error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("gate waiter did not cancel")
	}
	if err := gate.SetLimits(ConcurrencyLimits{Global: 1, PerEndpoint: 2}); err != nil {
		t.Fatal(err)
	}
	first.Release()
	first.Release()
	permit, err := gate.Acquire(context.Background(), "https://supplier.example/v1")
	if err != nil {
		t.Fatalf("gate accounting leaked after cancellation: %v", err)
	}
	permit.Release()
}

func TestStackRequiresSelfOriginRegistration(t *testing.T) {
	stack, err := NewStack(StackOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stack.NewClient("https://example.com"); !errors.Is(err, ErrSelfOriginsNotConfigured) {
		t.Fatalf("NewClient before self registration = %v", err)
	}
	stack.policy.resolver = &staticResolver{err: errors.New("DNS unavailable")}
	if err := stack.AddSelfOrigins(context.Background(), selfConfig("127.0.0.1:8080")); err == nil {
		t.Fatal("self-origin registration should fail closed when station DNS fails")
	}
	if _, err := stack.NewClient("https://example.com"); !errors.Is(err, ErrSelfOriginsNotConfigured) {
		t.Fatalf("NewClient after failed self registration = %v", err)
	}
}

func TestClientIgnoresEnvironmentProxy(t *testing.T) {
	var proxyHits atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		proxyHits.Add(1)
		http.Error(w, "proxy should not be used", http.StatusBadGateway)
	}))
	defer proxy.Close()
	t.Setenv("HTTP_PROXY", proxy.URL)
	t.Setenv("HTTPS_PROXY", proxy.URL)
	t.Setenv("NO_PROXY", "")

	var targetHits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		targetHits.Add(1)
		_, _ = io.WriteString(w, "direct")
	}))
	defer target.Close()
	client := newLoopbackClient(t, target.URL, nil)
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, client.BaseURL(), nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "direct" || targetHits.Load() != 1 || proxyHits.Load() != 0 {
		t.Fatalf("body=%q target_hits=%d proxy_hits=%d", body, targetHits.Load(), proxyHits.Load())
	}
}

func TestClientRequestTimeoutClosesUpstream(t *testing.T) {
	started := make(chan struct{})
	canceled := make(chan struct{})
	release := make(chan struct{})
	var startOnce sync.Once
	var cancelOnce sync.Once
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	srv.Config.Handler = http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		startOnce.Do(func() { close(started) })
		select {
		case <-r.Context().Done():
			cancelOnce.Do(func() { close(canceled) })
		case <-release:
		}
	})
	defer func() {
		close(release)
		srv.Close()
	}()
	client := newLoopbackClient(t, srv.URL, func(options *StackOptions) {
		options.RequestTimeout = 50 * time.Millisecond
	})
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, client.BaseURL(), nil)
	_, err := client.Do(req)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("request timeout error = %T %v", err, err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("timed request never reached upstream")
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("upstream did not observe timeout cancellation")
	}
}

func TestSSEParserEventFramingAndLineLimit(t *testing.T) {
	input := "\ufeffid: 7\r\nevent: chunk\r\ndata: hello\r\ndata: world\r\nretry: 1000\r\n\r\ndata: [DONE]\n\n"
	events := make(chan SSEEvent, 2)
	if err := ParseSSE(context.Background(), strings.NewReader(input), events, SSEOptions{MaxBytes: 1024}); err != nil {
		t.Fatal(err)
	}
	first := <-events
	second := <-events
	if first.Event != "chunk" || first.Data != "hello\nworld" || first.ID != "7" || first.Retry != 1000 {
		t.Fatalf("first event = %#v", first)
	}
	if second.Event != "message" || second.Data != "[DONE]" || second.ID != "7" {
		t.Fatalf("second event = %#v", second)
	}

	err := ParseSSE(context.Background(), strings.NewReader("data: "+strings.Repeat("x", 65)+"\n"), make(chan SSEEvent, 1), SSEOptions{
		MaxBytes:     1024,
		MaxLineBytes: 64,
	})
	var lineTooLarge *SSELineTooLargeError
	if !errors.As(err, &lineTooLarge) {
		t.Fatalf("line limit error = %T %v", err, err)
	}

	err = ParseSSE(context.Background(), strings.NewReader("data: "+strings.Repeat("x", 40)+"\ndata: "+strings.Repeat("y", 40)+"\n"), make(chan SSEEvent, 1), SSEOptions{
		MaxBytes:      1024,
		MaxLineBytes:  128,
		MaxEventBytes: 64,
	})
	var eventTooLarge *SSEEventTooLargeError
	if !errors.As(err, &eventTooLarge) {
		t.Fatalf("event limit error = %T %v", err, err)
	}
}
