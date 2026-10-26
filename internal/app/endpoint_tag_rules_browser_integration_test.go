package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
)

// Opt-in companion to TestAuditBrowserFixture. The caller stores the result at
// management.endpoint_tag_rules when NONBIRI_ENDPOINT_TAG_RULES_BROWSER=1.
// All samples use its private database, real routes and synthetic sessions.
func seedEndpointTagRulesBrowser(t *testing.T, f *imageBrowserFixture, now int64) map[string]any {
	t.Helper()
	var admin int64
	if err := f.store.DB().QueryRow(`SELECT id FROM users WHERE is_admin=1`).Scan(&admin); err != nil {
		t.Fatal(err)
	}
	users := map[string]imageBrowserUser{
		"denied_five":  f.seedUser(101, 5, admin),
		"granted_five": f.seedUser(102, 5, admin),
		"six":          f.seedUser(103, 6, admin),
	}
	for name, user := range users {
		if _, err := f.store.DB().Exec(`UPDATE users SET username=? WHERE id=?`, "Endpoint rules "+name, user.ID); err != nil {
			t.Fatal(err)
		}
		if name == "six" {
			continue
		}
		policy := authz.DefaultStewardPermissions(5)
		policy.Permissions["endpoints.read"] = true
		policy.Permissions["endpoints.tags"] = name == "granted_five"
		permissions, _ := json.Marshal(policy.Permissions)
		limits, _ := json.Marshal(policy.Limits)
		if _, err := f.store.DB().Exec(`INSERT INTO steward_permissions(user_id,revision,permissions,limits,donation_scope,created_at,updated_at) VALUES(?,1,?,?,'standard',?,?)`, user.ID, string(permissions), string(limits), now, now); err != nil {
			t.Fatal(err)
		}
	}
	addresses := map[string]string{
		"root":      "https://public.example.test/v1",
		"child":     "https://edge.public.example.test:8443/api",
		"removed":   "https://private.public.example.test/v1",
		"unmatched": "https://independent.example.test/v1",
		"future":    "https://future.public.example.test/v1",
	}
	ids := map[string]string{}
	owner := f.users[0].Cookie
	for _, name := range []string{"root", "child", "removed", "unmatched"} {
		ids[name] = f.call(http.MethodPost, "/api/endpoints", map[string]any{"source": "custom", "connector_type": "openai-compatible", "base_url": addresses[name], "note": "Endpoint rule sample " + name, "enabled": true}, owner, false)["id"].(string)
	}
	// Create an independent pending submission before assigning its future
	// mainstream rule. Rule updates must leave this existing submission pending.
	keys := []map[string]any{}
	for i, name := range []string{"root", "child"} {
		key := f.call(http.MethodPost, "/api/endpoints/"+ids[name]+"/keys", map[string]any{"secret": fmt.Sprintf("synthetic-endpoint-rule-key-%d", i), "note": "Endpoint rule review sample", "enabled": true, "force_store_false": false, "ownership_confirmed": true}, owner, false)["id"].(string)
		keys = append(keys, appFixtureSharedCatalog(t, f.store, key, []string{"sample-model-a", "sample-model-b"}, now))
	}
	pending := f.call(http.MethodPost, "/api/donations", map[string]any{"description": "Endpoint rules approval layout sample", "discord_public_thanks": false, "ownership_authorized": true, "keys": keys}, owner, false)
	mutate := func(method, path string, body any) {
		t.Helper()
		response := f.request(method, path, body, f.adminCookie, true)
		if response.Code != http.StatusNoContent {
			t.Fatalf("endpoint rule fixture %s %s: %d %s", method, path, response.Code, response.Body.String())
		}
	}
	mutate(http.MethodPatch, "/admin/api/overview/endpoints/tags", map[string]any{"base_urls": []string{addresses["root"]}, "tag": "mysterious", "add": true})
	mutate(http.MethodPut, "/admin/api/overview/endpoints/tag-rules", map[string]any{"pattern": "*.public.example.test", "action": "add", "tags": []string{"mainstream"}})
	mutate(http.MethodPut, "/admin/api/overview/endpoints/tag-rules", map[string]any{"pattern": "*.private.public.example.test", "action": "remove", "tags": []string{}})
	id := pending["id"].(string)
	read := f.call(http.MethodGet, "/admin/api/donations/"+id, nil, f.adminCookie, true)
	if read["status"] != "pending" {
		t.Fatalf("rule approved an old pending submission: %v", read["status"])
	}
	keyIDs := []string{}
	for _, key := range pending["keys"].([]any) {
		keyIDs = append(keyIDs, key.(map[string]any)["id"].(string))
	}
	return map[string]any{"addresses": addresses, "endpoint_ids": ids, "pending_donation_id": id, "pending_key_ids": keyIDs, "users": users}
}
