package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/config"
)

type endpointPolicyBrowserResolver struct{}

func (endpointPolicyBrowserResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	switch host {
	case "upstream.example":
		return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
	case "localhost":
		return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
	default:
		return nil, errors.New("unexpected browser fixture hostname")
	}
}

func endpointPolicyBrowserBase(index int) string {
	return fmt.Sprintf("https://policy-%02d.example/api/gateway/v3/ai/complete-endpoint-mount", index)
}

func configureEndpointPoliciesBrowser(t *testing.T, f *imageBrowserFixture) {
	t.Helper()
	cfg := f.cfg
	entries := make([]map[string]string, 0, 21)
	for index := 1; index <= 21; index++ {
		entries = append(entries, map[string]string{
			"source_base_url":   endpointPolicyBrowserBase(index),
			"gateway_base_url":  "https://gateway.example/private-account/private-gateway",
			"token_env":         "SYNTHETIC_GATEWAY_TOKEN",
			"credential_header": "Authorization",
		})
	}
	raw, err := json.Marshal(entries)
	if err != nil {
		t.Fatal(err)
	}
	cfg.CloudflareRoutes, err = config.ParseCloudflareRoutes(string(raw), func(string) (string, bool) {
		return "synthetic-private-gateway-token", true
	})
	if err != nil {
		t.Fatal(err)
	}
	// The real Worker requires a DNS target; the fixture pins this name to its local server.
	f.upstream.URL = "http://upstream.example:80"
	const workerToken = "synthetic-private-worker-token-12345"
	f.worker = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		target, parseErr := url.Parse(r.Header.Get("X-Nonbiri-Proxy-Target"))
		if parseErr != nil || target.Scheme != "http" || "http://"+target.Host != f.upstream.URL || r.Header.Get("X-Nonbiri-Proxy-Token") != workerToken {
			t.Error("browser relay received an unexpected target or token")
			http.Error(w, "invalid synthetic relay request", http.StatusBadGateway)
			return
		}
		forwarded := r.Clone(r.Context())
		forwarded.URL, forwarded.Host, forwarded.RequestURI = target, target.Host, ""
		for name := range forwarded.Header {
			if strings.HasPrefix(strings.ToLower(name), "x-nonbiri-proxy-") {
				forwarded.Header.Del(name)
			}
		}
		w.Header().Set("X-Nonbiri-Proxy-Response", "upstream")
		f.upstreamState.ServeHTTP(w, forwarded)
	}))
	t.Cleanup(f.worker.Close)
	previous := http.DefaultTransport
	transport := previous.(*http.Transport).Clone()
	transport.TLSClientConfig = f.worker.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	http.DefaultTransport = transport
	t.Cleanup(func() {
		http.DefaultTransport = previous
		transport.CloseIdleConnections()
	})

	cfg.CloudflareWorker, err = config.ParseCloudflareWorker(f.worker.URL+"/private-entry", workerToken)
	if err != nil {
		t.Fatal(err)
	}
}

func seedEndpointPoliciesBrowser(t *testing.T, f *imageBrowserFixture) map[string]string {
	t.Helper()
	for index := 1; index <= 21; index++ {
		f.call("PUT", "/admin/api/endpoint-backoff-policy", map[string]any{
			"connector_type":    "openai-compatible",
			"base_url":          endpointPolicyBrowserBase(index),
			"expected_revision": "0",
			"rules":             []any{},
		}, f.adminCookie, true)
	}
	return map[string]string{"connector_type": "openai-compatible", "base_url": endpointPolicyBrowserBase(1)}
}
