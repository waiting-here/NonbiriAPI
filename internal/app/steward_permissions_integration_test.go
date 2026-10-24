package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
)

func TestPerUserStewardPermissionsRealSessionFullAndNone(t *testing.T) {
	f := newGameWireFixture(t)
	if _, err := f.store.DB().Exec(`UPDATE users SET level=5 WHERE id=?`, f.userID); err != nil {
		t.Fatal(err)
	}
	p := authz.DefaultStewardPermissions(6)
	put := func(revision, key string) {
		t.Helper()
		raw, _ := json.Marshal(map[string]any{"expected_revision": revision, "permissions": p.Permissions, "limits": p.Limits})
		got := testApplicationRequest(t, f.app.handler, "PUT", auditAdminHost, fmt.Sprintf("/admin/api/users/%d/steward-permissions", f.userID), string(raw), f.adminCookies, map[string]string{"Content-Type": "application/json", "Origin": "http://" + auditAdminHost, "Idempotency-Key": "steward-policy-" + strings.Repeat(key, 22)})
		if got.Code != 200 {
			t.Fatalf("put permissions: %d %s", got.Code, got.Body)
		}
	}
	paths := []string{"/api/steward/time-context", "/api/steward/users?page=1&page_size=10", "/api/steward/blacklist?page=1&page_size=10", "/api/steward/charity-models?page=1&page_size=10", "/api/steward/charity-binding-presets?page=1&page_size=10", "/api/steward/charity-price-templates?page=1&page_size=10", "/api/steward/logs?page=1&page_size=10", "/api/steward/logs/pressure", "/api/steward/abuse-audit/client-rules", "/api/steward/egress/cloudflare-routes", "/api/steward/maintenance"}
	put("0", "P")
	for _, path := range paths {
		got := testApplicationRequest(t, f.app.handler, http.MethodGet, auditUserHost, path, "", f.cookies, nil)
		if got.Code != 200 {
			t.Fatalf("full5 %s: %d %s", path, got.Code, got.Body)
		}
	}
	for key := range p.Permissions {
		p.Permissions[key] = false
	}
	put("1", "Q")
	for _, path := range paths {
		got := testApplicationRequest(t, f.app.handler, http.MethodGet, auditUserHost, path, "", f.cookies, nil)
		if got.Code != 403 || !strings.Contains(got.Body.String(), "steward_permission_denied") {
			t.Fatalf("none5 %s: %d %s", path, got.Code, got.Body)
		}
	}
	got := testApplicationRequest(t, f.app.handler, http.MethodGet, auditUserHost, "/api/session", "", f.cookies, nil)
	if got.Code != 200 || !strings.Contains(got.Body.String(), `"effective_level":5`) || !strings.Contains(got.Body.String(), `"steward_permissions"`) {
		t.Fatalf("personal session changed: %d %s", got.Code, got.Body)
	}
	if _, err := f.store.DB().Exec(`UPDATE users SET level=6 WHERE id=?`, f.userID); err != nil {
		t.Fatal(err)
	}
	got = testApplicationRequest(t, f.app.handler, http.MethodGet, auditUserHost, "/api/steward/users?page=1&page_size=10", "", f.cookies, nil)
	if got.Code != 200 {
		t.Fatalf("level6 lost authority: %d %s", got.Code, got.Body)
	}
}
