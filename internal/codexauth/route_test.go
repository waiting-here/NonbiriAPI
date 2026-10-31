package codexauth

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/config"
	"github.com/waiting-here/NonbiriAPI/internal/egress"
)

type localOnlyDialer struct{}

func (localOnlyDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, _, err := net.SplitHostPort(address)
	if err != nil || host != "127.0.0.1" {
		return nil, errors.New("forwarded connection is unavailable")
	}
	return (&net.Dialer{}).DialContext(ctx, network, address)
}

func TestFixedEndpointsUseDirectRoute(t *testing.T) {
	for _, mode := range []string{"worker", "legacy"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t, func(source string, options *egress.StackOptions) {
				options.Dialer = localOnlyDialer{}
				var err error
				if mode == "worker" {
					options.CloudflareWorker, err = config.ParseCloudflareWorker("https://worker.example.invalid/proxy", strings.Repeat("w", 32))
				} else {
					options.CloudflareRoutes, err = config.ParseCloudflareRoutes(`[{"source_base_url":"`+source+`","gateway_base_url":"https://gateway.example.invalid/private","token_env":"GATEWAY_TOKEN","credential_header":"Authorization"},{"source_base_url":"`+source+`/backend-api/wham/usage","gateway_base_url":"https://gateway.example.invalid/usage","token_env":"GATEWAY_TOKEN","credential_header":"Authorization"}]`, func(string) (string, bool) { return "gateway-token", true })
				}
				if err != nil {
					t.Fatal(err)
				}
			})
			f.usageFn = func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") == "" || r.Header.Get("ChatGPT-Account-ID") != "workspace" || r.Header.Get("X-Nonbiri-Proxy-Token") != "" {
					t.Error("incorrect direct request identity")
				}
				_, _ = w.Write([]byte(`{"plan_type":"business"}`))
			}
			// Ordinary callers still use the configured forwarding route.
			client, err := f.m.config.Stack.NewClient(f.m.client.issuer)
			if err != nil {
				t.Fatal(err)
			}
			request, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, f.m.client.issuer+"/backend-api/wham/usage", nil)
			if response, err := client.Do(request); err == nil {
				response.Body.Close()
				t.Fatal("fixture did not activate forwarding")
			}
			// Device code, polling and exchange must all reach the fixed issuer.
			status := quotaConnected(t, f)
			f.now.Add(3541)
			if token, _, revision, err := f.m.AccessTokenSnapshot(t.Context()); err != nil || len(token) == 0 || revision != status.Revision || f.refreshes.Load() != 1 {
				t.Fatal("direct refresh failed", revision, err)
			}
			quota, err := f.m.Quota(t.Context(), true)
			if err != nil || quota.Error != nil || quota.Data == nil {
				t.Fatal("direct quota failed", quota, err)
			}
		})
	}
}
