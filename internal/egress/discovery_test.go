package egress

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer server.Close()
	stack := newLoopbackStack(t, []string{server.URL}, func(o *StackOptions) { o.Concurrency = ConcurrencyLimits{Global: 4, PerEndpoint: 1} })
	permit, err := stack.gate.Acquire(t.Context(), server.URL+"/v1")
	if err != nil {
		t.Fatal(err)
	}
	defer permit.Release()
	for _, suffix := range []string{"/models", "/models/", "/config"} {
		client, err := stack.NewDiscoveryClient(server.URL + "/v1" + suffix)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, client.BaseURL(), nil)
		_, err = client.Do(req)
		cancel()
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal("catalog did not share standard inference bucket", suffix, err)
		}
	}
	// Explicit inference trailing slashes retain their existing distinct key.
	for _, path := range []string{"/v1/", "/v1/catalog.json"} {
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		other, err := stack.gate.Acquire(ctx, server.URL+path)
		cancel()
		if err != nil {
			t.Fatal("unrelated gate semantics changed", err)
		}
		other.Release()
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
