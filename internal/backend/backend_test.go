package backend

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/config"
	"github.com/waiting-here/NonbiriAPI/internal/egress"
)

type fixedResolver struct{ addrs []netip.Addr }

func (r fixedResolver) LookupNetIP(ctx context.Context, _ string, _ string) ([]netip.Addr, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return append([]netip.Addr(nil), r.addrs...), nil
}

func newTestStack(t *testing.T, allowed []string, mutate func(*egress.StackOptions)) *egress.Stack {
	t.Helper()
	options := egress.StackOptions{
		AllowedOrigins: allowed,
		Resolver:       fixedResolver{addrs: []netip.Addr{netip.MustParseAddr("93.184.216.34")}},
		RequestTimeout: 3 * time.Second,
		Concurrency:    egress.ConcurrencyLimits{Global: 8, PerEndpoint: 4},
	}
	if mutate != nil {
		mutate(&options)
	}
	stack, err := egress.NewStack(options)
	if err != nil {
		t.Fatalf("NewStack: %v", err)
	}
	t.Cleanup(stack.CloseIdleConnections)
	return stack
}

func addSelfOrigins(t *testing.T, stack *egress.Stack) {
	t.Helper()
	if err := stack.AddSelfOrigins(context.Background(), &config.Config{
		SiteBaseURL: "https://gateway.example",
		UserHost:    "gateway.example",
		AdminHost:   "admin.gateway.example",
		ListenAddr:  "127.0.0.1:1",
	}); err != nil {
		t.Fatalf("AddSelfOrigins: %v", err)
	}
}

func TestNewLocalRejectsNilStack(t *testing.T) {
	if _, err := NewLocal(nil); err == nil {
		t.Fatal("NewLocal(nil) must fail")
	}
	var typedNil *LocalBackend
	if !IsNil(typedNil) || !IsNil(nil) || IsNil(&LocalBackend{}) {
		t.Fatal("IsNil must reject nil and typed-nil backends only")
	}
}

// TestLocalBackendDelegatesToSharedStack verifies that Open and
// MaxResponseBytes are pure delegation to the wrapped Stack: validation,
// canonicalization, and the response ceiling all come from the stack.
func TestLocalBackendDelegatesToSharedStack(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	stack := newTestStack(t, []string{upstream.URL}, nil)
	addSelfOrigins(t, stack)
	backend, err := NewLocal(stack)
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}

	if got := backend.MaxResponseBytes(); got != stack.MaxResponseBytes() {
		t.Fatalf("MaxResponseBytes = %d, want %d", got, stack.MaxResponseBytes())
	}

	client, err := backend.Open(upstream.URL)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if client.BaseURL() != upstream.URL {
		t.Fatalf("client BaseURL = %q, want canonical %q", client.BaseURL(), upstream.URL)
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, client.BaseURL(), nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	// An origin outside the shared allowlist may open a client (validation is
	// shape/canonicalization only), but its requests are refused by the stack's
	// dial-time policy — there is no side door around the allowlist.
	outside, err := backend.Open("https://api.example.com/v1")
	if err != nil {
		t.Fatalf("Open outside allowlist: %v", err)
	}
	ctxOutside, cancelOutside := context.WithTimeout(context.Background(), time.Second)
	defer cancelOutside()
	reqOutside, err := http.NewRequestWithContext(ctxOutside, http.MethodGet, outside.BaseURL()+"/models", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := outside.Do(reqOutside); err == nil {
		t.Fatal("request to a non-allowlisted origin must be refused by the shared policy")
	}
}

// TestLocalBackendRequiresRegisteredSelfOrigins keeps the fail-closed startup
// ordering: no dialing client may exist before self origins are registered.
func TestLocalBackendRequiresRegisteredSelfOrigins(t *testing.T) {
	stack := newTestStack(t, []string{"https://api.example.com"}, nil)
	backend, err := NewLocal(stack)
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}
	if _, err := backend.Open("https://api.example.com/v1"); !errors.Is(err, egress.ErrSelfOriginsNotConfigured) {
		t.Fatalf("Open before AddSelfOrigins = %v, want ErrSelfOriginsNotConfigured", err)
	}
}

// TestLocalBackendClientsShareOneGate proves that every client handed out by
// the LocalBackend draws from the same stack-owned concurrency gate. With a
// per-endpoint limit of one, a second client for the same origin must block
// while the first request is in flight; an independent gate would let it
// through immediately.
func TestLocalBackendClientsShareOneGate(t *testing.T) {
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer upstream.Close()

	stack := newTestStack(t, []string{upstream.URL}, func(options *egress.StackOptions) {
		options.Concurrency = egress.ConcurrencyLimits{Global: 8, PerEndpoint: 1}
	})
	addSelfOrigins(t, stack)
	backend, err := NewLocal(stack)
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}

	first, err := backend.Open(upstream.URL)
	if err != nil {
		t.Fatalf("Open first: %v", err)
	}
	second, err := backend.Open(upstream.URL)
	if err != nil {
		t.Fatalf("Open second: %v", err)
	}

	req1, err := http.NewRequestWithContext(context.Background(), http.MethodGet, first.BaseURL(), nil)
	if err != nil {
		t.Fatal(err)
	}
	type firstResult struct {
		resp *http.Response
		err  error
	}
	firstDone := make(chan firstResult, 1)
	go func() {
		resp, doErr := first.Do(req1)
		firstDone <- firstResult{resp: resp, err: doErr}
	}()

	// The first request now holds the single per-endpoint permit.
	ctx2, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	req2, err := http.NewRequestWithContext(ctx2, http.MethodGet, second.BaseURL(), nil)
	if err != nil {
		close(release)
		t.Fatal(err)
	}
	if _, err := second.Do(req2); err == nil {
		close(release)
		t.Fatal("second concurrent request must block on the shared per-endpoint gate")
	} else if !errors.Is(err, context.DeadlineExceeded) && !strings.Contains(err.Error(), "deadline") {
		close(release)
		t.Fatalf("second Do error = %v, want deadline exceeded while gate is held", err)
	}
	close(release)

	select {
	case result := <-firstDone:
		if result.err != nil {
			t.Fatalf("first Do: %v", result.err)
		}
		defer func() { _ = result.resp.Body.Close() }()
		if result.resp.StatusCode != http.StatusOK {
			t.Fatalf("first status = %d, want 200", result.resp.StatusCode)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("first request never completed after the gate was released")
	}
}
