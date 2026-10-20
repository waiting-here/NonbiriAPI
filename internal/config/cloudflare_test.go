package config

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestCloudflareRouteConfiguration(t *testing.T) {
	valid := `[{"source_base_url":"https://provider.example/v1","gateway_base_url":"https://gateway.example/provider/v1","token_env":"GATEWAY_TOKEN","credential_header":"x-api-key","credential_query_parameter":"key"}]`
	lookup := func(string) (string, bool) { return "private-gateway-token", true }
	for _, raw := range []string{"", "  ", "[]"} {
		routes, err := ParseCloudflareRoutes(raw, lookup)
		if err != nil || len(routes) != 0 {
			t.Fatalf("disabled routes: %v", err)
		}
	}
	routes, err := ParseCloudflareRoutes(valid, lookup)
	if err != nil || len(routes) != 1 || routes[0].CredentialHeader != "X-Api-Key" || routes[0].GatewayToken() != "private-gateway-token" || routes[0].RetainCredentialHeader {
		t.Fatal(routes, err)
	}
	data, _ := json.Marshal(routes)
	for _, formatted := range []string{fmt.Sprint(routes), fmt.Sprintf("%#v", routes), string(data)} {
		if strings.Contains(formatted, "private-gateway-token") || strings.Contains(formatted, "gateway.example") {
			t.Fatal("private route was formatted", formatted)
		}
	}
	cases := []string{
		"null", `{}`, `[null]`, valid + "[]", strings.Replace(valid, `"token_env":"GATEWAY_TOKEN"`, `"token_env":"GATEWAY_TOKEN","token_env":"OTHER"`, 1),
		strings.Replace(valid, `"token_env"`, `"token"`, 1), strings.Replace(valid, `"GATEWAY_TOKEN"`, `""`, 1),
		strings.Replace(valid, "https://gateway.example", "http://gateway.example", 1),
		strings.Replace(valid, "https://provider.example/v1", "https://user:private@provider.example/v1", 1),
		strings.Replace(valid, "provider.example/v1", "provider.example/v1?key=private", 1),
		strings.Replace(valid, "provider.example/v1", "provider.example/v1#private", 1),
		strings.Replace(valid, `"x-api-key"`, `"cf-aig-authorization"`, 1),
		strings.Replace(valid, `"x-api-key"`, `"Host"`, 1),
		strings.Replace(valid, `"key"`, `"x=y"`, 1),
	}
	for i, raw := range cases {
		if _, err := ParseCloudflareRoutes(raw, lookup); err == nil {
			t.Fatalf("case %d accepted", i)
		} else if strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), "provider.example") {
			t.Fatal("configuration error exposed input", err)
		}
	}
	for _, token := range []string{"", "bad\nvalue", "bad value", strings.Repeat("x", 4097)} {
		if _, err := ParseCloudflareRoutes(valid, func(string) (string, bool) { return token, true }); err == nil {
			t.Fatal("invalid token accepted")
		}
	}
}

func TestCloudflareRetainCredentialHeaderConfiguration(t *testing.T) {
	const base = `[{"source_base_url":"https://provider.example/v1","gateway_base_url":"https://gateway.example/provider/v1","token_env":"GATEWAY_TOKEN","credential_header":"x-api-key"%s}]`
	const query = `,"credential_query_parameter":"key"`
	for _, tc := range []struct {
		name, fields   string
		retain, reject bool
	}{
		{"default without query", "", false, false},
		{"explicit false", query + `,"retain_credential_header":false`, false, false},
		{"retain", query + `,"retain_credential_header":true`, true, false},
		{"false without query", `,"retain_credential_header":false`, false, false},
		{"true without query", `,"retain_credential_header":true`, false, true},
		{"true with empty query", `,"credential_query_parameter":"","retain_credential_header":true`, false, true},
		{"string", query + `,"retain_credential_header":"true"`, false, true},
		{"number", query + `,"retain_credential_header":1`, false, true},
		{"null", query + `,"retain_credential_header":null`, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			routes, err := ParseCloudflareRoutes(fmt.Sprintf(base, tc.fields), func(string) (string, bool) { return "private-gateway-token", true })
			if tc.reject {
				if err == nil {
					t.Fatal("invalid retention setting accepted")
				}
				return
			}
			if err != nil || len(routes) != 1 || routes[0].RetainCredentialHeader != tc.retain {
				t.Fatal("retention setting not loaded", err)
			}
		})
	}
}

func TestLoadCloudflareRoutes(t *testing.T) {
	allEnvs(t)
	t.Setenv("NONBIRI_CLOUDFLARE_ROUTES", `[{"source_base_url":"https://provider.example/v1","gateway_base_url":"https://gateway.example/v1","token_env":"TEST_ROUTE_TOKEN","credential_header":"Authorization"}]`)
	t.Setenv("TEST_ROUTE_TOKEN", "gateway-token-value")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	closeLoadedVault(t, cfg)
	if len(cfg.CloudflareRoutes) != 1 || cfg.CloudflareRoutes[0].GatewayToken() != "gateway-token-value" {
		t.Fatal("routes not loaded")
	}
	t.Setenv("TEST_ROUTE_TOKEN", "")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "NONBIRI_CLOUDFLARE_ROUTES") {
		t.Fatal("missing token was not rejected", err)
	}
}
