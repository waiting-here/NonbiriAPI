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
	if err != nil || len(routes) != 1 || routes[0].CredentialHeader != "X-Api-Key" || routes[0].GatewayToken() != "private-gateway-token" {
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
