package egress

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestDiscoveryAddressLengthAndSharedSecurity(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if !strings.HasSuffix(r.URL.Path, "/models") {
			t.Error("catalog path changed")
		}
		http.Redirect(w, r, "/redirected", http.StatusFound)
	}))
	defer server.Close()
	stack := newLoopbackStack(t, []string{server.URL}, nil)
	address := server.URL + "/" + strings.Repeat("x", 4089-len(server.URL)-1) + "/models"
	if saved, err := stack.ValidateBaseURL(address); err != nil || saved != address {
		t.Fatal("maximum catalog address was not preserved", err)
	}
	if _, err := stack.NewDiscoveryClient(address + "x"); err == nil {
		t.Fatal("discovery limit was expanded")
	}
	if _, err := stack.NewClient(address + "x"); err == nil {
		t.Fatal("inference limit was expanded")
	}
	if _, err := stack.NewDiscoveryClient("http://127.0.0.2/private/models"); err == nil {
		t.Fatal("discovery bypassed private address policy")
	}
	client, err := stack.NewDiscoveryClient(address)
	if err != nil {
		t.Fatal(err)
	}
	wrong, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://other.example/models", nil)
	if _, err := client.Do(wrong); err == nil || calls != 0 {
		t.Fatal("discovery bypassed origin binding", err, calls)
	}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, address, nil)
	response, err := client.Do(req)
	if response != nil && response.Body != nil {
		response.Body.Close()
	}
	if !errors.Is(err, ErrRedirectBlocked) || calls != 1 {
		t.Fatal("discovery redirect was followed", err, calls)
	}
}

func TestStandardDiscoverySharesInferenceConcurrency(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	stack := newLoopbackStack(t, []string{server.URL}, func(o *StackOptions) { o.Concurrency = ConcurrencyLimits{Global: 4, PerEndpoint: 1} })
	for _, heldSuffix := range []string{"", "/"} {
		t.Run("inference"+heldSuffix, func(t *testing.T) {
			permit, err := stack.gate.Acquire(t.Context(), server.URL+"/v1"+heldSuffix)
			if err != nil {
				t.Fatal(err)
			}
			defer permit.Release()
			// Inference itself keeps the two spellings independent.
			otherSuffix := "/"
			if heldSuffix == "/" {
				otherSuffix = ""
			}
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			other, err := stack.gate.Acquire(ctx, server.URL+"/v1"+otherSuffix)
			cancel()
			if err != nil {
				t.Fatal("inference keys were merged", err)
			}
			other.Release()
			for _, suffix := range []string{"/models", "/models/", "/config"} {
				client, err := stack.NewDiscoveryClient(server.URL + "/v1" + suffix)
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
				req, _ := http.NewRequestWithContext(ctx, http.MethodGet, client.BaseURL(), nil)
				_, err = client.Do(req)
				cancel()
				if !errors.Is(err, context.DeadlineExceeded) || calls.Load() != 0 {
					t.Fatal("catalog bypassed an inference bucket", heldSuffix, suffix, err, calls.Load())
				}
				stack.gate.mu.Lock()
				partial := stack.gate.globalActive != 1 || len(stack.gate.perActive) != 1
				stack.gate.mu.Unlock()
				if partial {
					t.Fatal("canceled catalog retained a partial reservation")
				}
			}
		})
	}
	stack.gate.mu.Lock()
	leaked := stack.gate.globalActive != 0 || len(stack.gate.perActive) != 0
	stack.gate.mu.Unlock()
	if leaked {
		t.Fatal("inference permits leaked")
	}
	// A custom catalog is still keyed by its complete configured address.
	for _, suffix := range []string{"", "/"} {
		permit, err := stack.gate.Acquire(t.Context(), server.URL+"/v1"+suffix)
		if err != nil {
			t.Fatal(err)
		}
		defer permit.Release()
	}
	custom, err := stack.NewDiscoveryClient(server.URL + "/v1/catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, custom.BaseURL(), nil)
	response, err := custom.Do(req)
	if err != nil {
		t.Fatal("custom catalog shared a standard base bucket", err)
	}
	response.Body.Close()
	if calls.Load() != 1 {
		t.Fatal("custom catalog did not dispatch once", calls.Load())
	}
}

func TestDiscoveryCancellationReleasesBothInferenceAliases(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "ready")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	stack := newLoopbackStack(t, []string{server.URL}, func(o *StackOptions) { o.Concurrency = ConcurrencyLimits{Global: 4, PerEndpoint: 1} })
	client, err := stack.NewDiscoveryClient(server.URL + "/v1/models")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, client.BaseURL(), nil)
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	stack.gate.mu.Lock()
	wrong := stack.gate.globalActive != 1 || len(stack.gate.perActive) != 2
	stack.gate.mu.Unlock()
	if wrong {
		t.Fatal("discovery must reserve two endpoint keys and one global slot")
	}
	for _, suffix := range []string{"", "/"} {
		blocked, cancelWait := context.WithTimeout(t.Context(), 20*time.Millisecond)
		permit, err := stack.gate.Acquire(blocked, server.URL+"/v1"+suffix)
		cancelWait()
		if permit != nil {
			permit.Release()
		}
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal("inference bypassed discovery", suffix, err)
		}
	}
	cancel()
	response.Body.Close()
	response.Body.Close()
	stack.gate.mu.Lock()
	leaked := stack.gate.globalActive != 0 || len(stack.gate.perActive) != 0
	stack.gate.mu.Unlock()
	if leaked {
		t.Fatal("cancellation/body close did not release both aliases")
	}
	for _, suffix := range []string{"", "/"} {
		available, cancelWait := context.WithTimeout(t.Context(), time.Second)
		permit, err := stack.gate.Acquire(available, server.URL+"/v1"+suffix)
		cancelWait()
		if err != nil {
			t.Fatal("released inference key remained blocked", suffix, err)
		}
		permit.Release()
	}
}

func TestDiscoveryGateAliasesDeduplicateCanonicalKeys(t *testing.T) {
	gate, err := NewGate(ConcurrencyLimits{Global: 2, PerEndpoint: 1})
	if err != nil {
		t.Fatal(err)
	}
	permit, err := gate.acquireAliases(t.Context(), "https://EXAMPLE.com./v1", "https://example.com:443/v1")
	if err != nil {
		t.Fatal(err)
	}
	if gate.globalActive != 1 || len(gate.perActive) != 1 {
		t.Fatal("equivalent aliases counted twice")
	}
	permit.Release()
	permit.Release()
	if gate.globalActive != 0 || len(gate.perActive) != 0 {
		t.Fatal("deduplicated alias leaked")
	}
	if _, err := gate.acquireAliases(t.Context(), "https://example.com/v1", "invalid"); err == nil {
		t.Fatal("invalid alias accepted")
	}
	if gate.globalActive != 0 || len(gate.perActive) != 0 {
		t.Fatal("invalid alias partially acquired")
	}
}

func TestDiscoveryRetainsCloudflareRoutesWithoutPrefixExpansion(t *testing.T) {
	for _, suffix := range []string{"/models", "/config", "/models/"} {
		for _, sourceSlash := range []string{"", "/"} {
			t.Run(suffix+sourceSlash, func(t *testing.T) {
				_, stack, server := routedClient(t, func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path != "/gateway/v1"+suffix || r.Header.Get("Cf-Aig-Authorization") != "Bearer "+routeToken || r.URL.Query().Get("key") != routeKey {
						t.Error("legacy route path or credential changed")
					}
					io.WriteString(w, `{"ok":true}`)
				}, func(o *StackOptions) { o.CloudflareRoutes[0].SourceBaseURL += sourceSlash })
				client, err := stack.NewDiscoveryClient(routeSource + suffix)
				if err != nil || client.route == nil {
					t.Fatal("legacy route was bypassed", err)
				}
				client.httpClient.Transport.(*http.Transport).TLSClientConfig = server.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
				response, err := client.Do(routedRequest(context.Background(), suffix))
				if err != nil {
					t.Fatal(err)
				}
				response.Body.Close()
				custom, err := stack.NewDiscoveryClient(routeSource + "/catalog.json")
				if err != nil || custom.route != nil {
					t.Fatal("arbitrary prefix routing added", err)
				}
			})
		}
	}
	t.Run("custom exact route", func(t *testing.T) {
		_, stack, server := routedClient(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/gateway/v1" || r.Header.Get("Cf-Aig-Authorization") != "Bearer "+routeToken || r.URL.Query().Get("key") != routeKey {
				t.Error("custom exact route changed")
			}
			io.WriteString(w, `{"ok":true}`)
		}, func(o *StackOptions) { o.CloudflareRoutes[0].SourceBaseURL += "/catalog.json" })
		client, err := stack.NewDiscoveryClient(routeSource + "/catalog.json")
		if err != nil || client.route == nil {
			t.Fatal("custom exact route bypassed", err)
		}
		client.httpClient.Transport.(*http.Transport).TLSClientConfig = server.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
		response, err := client.Do(routedRequest(context.Background(), "/catalog.json"))
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
	})
	stack := newLoopbackStack(t, nil, func(o *StackOptions) {
		o.CloudflareRoutes = append(configuredRoutes(t, routeSource, "https://gateway.example/base", "X-Api-Key", ""),
			configuredRoutes(t, routeSource+"/", "https://gateway.example/other", "X-Api-Key", "")...)
		o.CloudflareRoutes = append(o.CloudflareRoutes, configuredRoutes(t, routeSource+"/catalog.json", "https://gateway.example/exact", "X-Api-Key", "")...)
		o.CloudflareRoutes = append(o.CloudflareRoutes, configuredRoutes(t, routeSource+"/models", "https://gateway.example/models-exact", "X-Api-Key", "")...)
	})
	if _, err := stack.NewDiscoveryClient(routeSource + "/config"); err == nil {
		t.Fatal("conflicting legacy routes were silently selected")
	}
	for _, path := range []string{"/catalog.json", "/models"} {
		client, err := stack.NewDiscoveryClient(routeSource + path)
		if err != nil || client.route == nil || client.route.source.Path != "/v1"+path {
			t.Fatal("exact route did not take precedence", err)
		}
	}
}
