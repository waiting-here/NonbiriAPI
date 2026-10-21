package app

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/config"
)

func endpointPolicyBrowserBase(index int) string {
	return fmt.Sprintf("https://policy-%02d.example/api/gateway/v3/ai/complete-endpoint-mount", index)
}

func configureEndpointPoliciesBrowser(t *testing.T, cfg *config.Config) {
	t.Helper()
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
	cfg.CloudflareWorker, err = config.ParseCloudflareWorker("https://worker.example.net/private-entry", "synthetic-private-worker-token-12345")
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
