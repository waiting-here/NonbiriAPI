package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/auth"
	"github.com/waiting-here/NonbiriAPI/internal/config"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbfixture"
	"github.com/waiting-here/NonbiriAPI/internal/egress"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

func TestCloudflareStatusRoutesRequireCurrentWebSessionRole(t *testing.T) {
	f := newGameWireFixture(t)
	setLevel := func(level int) {
		t.Helper()
		if _, err := f.store.DB().Exec(`UPDATE users SET level=? WHERE id=?`, level, f.userID); err != nil {
			t.Fatal(err)
		}
	}
	call := func(method, host, path, body string, cookies []*http.Cookie, headers map[string]string, want int) {
		t.Helper()
		response := testApplicationRequest(t, f.app.handler, method, host, path, body, cookies, headers)
		if response.Code != want {
			t.Fatalf("%s %s %s = %d; want %d: %s", method, host, path, response.Code, want, response.Body.String())
		}
		if want == http.StatusOK && (response.Header().Get("Cache-Control") != "no-store" || strings.TrimSpace(response.Body.String()) != `{"enabled":false,"routes":[]}`) {
			t.Fatalf("unexpected status projection: %s", response.Body.String())
		}
	}
	setLevel(6)
	call("GET", auditAdminHost, adminCloudflareRoutes, "", f.adminCookies, nil, http.StatusOK)
	call("GET", auditUserHost, stewardCloudflareRoutes, "", f.cookies, nil, http.StatusOK)
	call("GET", auditUserHost, stewardCloudflareRoutes, "", nil, nil, http.StatusUnauthorized)
	call("GET", auditUserHost, stewardCloudflareRoutes, "", nil, map[string]string{"Authorization": "Bearer caller-key-does-not-authorize-web-routes"}, http.StatusUnauthorized)
	call("GET", auditAdminHost, adminCloudflareRoutes, "", f.cookies, nil, http.StatusUnauthorized)
	call("GET", auditUserHost, stewardCloudflareRoutes, "", f.adminCookies, nil, http.StatusUnauthorized)
	call("GET", auditUserHost, adminCloudflareRoutes, "", f.adminCookies, nil, http.StatusNotFound)
	call("GET", auditAdminHost, stewardCloudflareRoutes, "", f.cookies, nil, http.StatusNotFound)
	for _, target := range []struct {
		host, path string
		cookies    []*http.Cookie
	}{{auditAdminHost, adminCloudflareRoutes, f.adminCookies}, {auditUserHost, stewardCloudflareRoutes, f.cookies}} {
		call("GET", target.host, target.path+"?unused=1", "", target.cookies, nil, http.StatusBadRequest)
		call("GET", target.host, target.path+"?", "", target.cookies, nil, http.StatusBadRequest)
		call("GET", target.host, target.path, "{}", target.cookies, nil, http.StatusBadRequest)
		call("POST", target.host, target.path, "", target.cookies, map[string]string{"Origin": "http://" + target.host}, http.StatusMethodNotAllowed)
	}
	for _, level := range []int{5, 4} {
		setLevel(level)
		call("GET", auditUserHost, stewardCloudflareRoutes, "", f.cookies, nil, http.StatusForbidden)
	}
	setLevel(6)
	if _, err := f.store.DB().Exec(`DELETE FROM sessions WHERE user_id=?`, f.userID); err != nil {
		t.Fatal(err)
	}
	call("GET", auditUserHost, stewardCloudflareRoutes, "", f.cookies, nil, http.StatusUnauthorized)
}

func TestCloudflareStatusUsesInjectedStackInsteadOfConfig(t *testing.T) {
	vault, err := secret.New(bytes.Repeat([]byte{0x53}, secret.MasterKeyBytes))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = vault.Close() })
	path := filepath.Join(t.TempDir(), "routes.sqlite")
	dbfixture.Materialize(t, path, vault)
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	route := func(source string) []config.CloudflareRoute {
		t.Helper()
		raw, err := json.Marshal([]map[string]string{{"source_base_url": source, "gateway_base_url": "https://gateway.example/private-account/private-gateway", "token_env": "PRIVATE_GATEWAY_TOKEN", "credential_header": "Authorization"}})
		if err != nil {
			t.Fatal(err)
		}
		entries, err := config.ParseCloudflareRoutes(string(raw), func(string) (string, bool) { return "private-runtime-token", true })
		if err != nil {
			t.Fatal(err)
		}
		return entries
	}
	cfg := auditConfig()
	cfg.CloudflareRoutes = route("https://unused-config.example/v1")
	stack, err := egress.NewStack(egress.StackOptions{CloudflareRoutes: route("https://loaded.example/v1")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stack.CloseIdleConnections)
	application, err := buildApplicationWithRuntimeOptions(context.Background(), cfg, store, vault, applicationRuntimeOptions{Egress: stack})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = application.Close() })
	login := testApplicationRequest(t, application.handler, "POST", auditAdminHost, "/admin/api/login", `{"username":"operator","password":"correct horse battery staple"}`, nil, map[string]string{"Content-Type": "application/json"})
	cookie := responseCookieNamed(t, login, auth.AdminSessionCookieName)
	response := testApplicationRequest(t, application.handler, "GET", auditAdminHost, adminCloudflareRoutes, "", []*http.Cookie{cookie}, nil)
	want := `{"enabled":true,"routes":[{"source_base_url":"https://loaded.example/v1","gateway_origin":"https://gateway.example","credential_header":"Authorization","credential_query_parameter":"","retain_credential_header":false}]}`
	if response.Code != http.StatusOK || strings.TrimSpace(response.Body.String()) != want {
		t.Fatalf("status used an unloaded route or exposed private config: %d %s", response.Code, response.Body.String())
	}
}
