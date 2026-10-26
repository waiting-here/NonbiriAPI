package adminusers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

func TestEndpointTagRulesApplyToFutureEndpointsAndLockManualReplay(t *testing.T) {
	f := newAdminUsersFixture(t)
	owner := f.seedUser("rules-owner", false)
	root, child, unrelated := "https://example.test/v1", "http://a.foo.example.test:8080/other", "https://notexample.test/v1"
	f.seedEndpoint(owner, root, true, 0)
	f.seedEndpoint(owner, unrelated, true, 0)
	request := func(method, route string, body any, key string, want int) {
		t.Helper()
		encoded, _ := json.Marshal(body)
		got := f.request(method, route, "https://admin.example"+route, string(encoded), 0, key)
		if got.Code != want {
			t.Fatalf("%s %s: %d %s", method, route, got.Code, got.Body.String())
		}
	}
	manual := endpointTagsMutation{[]string{root}, "mysterious", true}
	request(http.MethodPatch, routeEndpointTags, manual, "rules-manual-original-0001", 204)
	request(http.MethodPut, routeEndpointTagRules, EndpointTagRule{"*.EXAMPLE.test.", "add", []string{"mainstream"}}, "rules-broad-original-0001", 204)
	f.seedEndpoint(owner, child, true, 0)
	request(http.MethodPatch, routeEndpointTags, manual, "rules-manual-original-0001", 409)
	request(http.MethodPatch, routeEndpointTags, endpointTagsMutation{[]string{unrelated, root}, "other_third_party", true}, "rules-manual-atomic-0001", 409)
	var count int
	if err := f.store.DB().QueryRow(`SELECT COUNT(*) FROM admin_endpoint_tags WHERE base_url=?`, unrelated).Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
	request(http.MethodPut, routeEndpointTagRules, EndpointTagRule{"*.foo.example.test", "remove", []string{}}, "rules-narrow-remove-0001", 204)
	page, err := f.service.EndpointOverview(t.Context(), f.adminID, EndpointOverviewQuery{Page: ptrPage(pagination.Default())})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range page.Data {
		switch item.BaseURL {
		case root:
			if !slices.Equal(item.Tags, []string{"mainstream", "mysterious"}) || !slices.Equal(item.ManualTags, []string{"mysterious"}) || len(item.TagRules) != 1 {
				t.Fatal(item)
			}
		case child:
			if len(item.Tags) != 0 || len(item.TagRules) != 2 || item.TagRules[0].Pattern != "*.example.test" || item.TagRules[1].Action != "remove" {
				t.Fatal(item)
			}
		case unrelated:
			if len(item.Tags) != 0 || len(item.TagRules) != 0 {
				t.Fatal(item)
			}
		}
	}
	request(http.MethodDelete, routeEndpointTagRules, map[string]string{"pattern": "*.example.test"}, "rules-delete-broad-0001", 204)
	page, err = f.service.EndpointOverview(t.Context(), f.adminID, EndpointOverviewQuery{Tag: "mysterious", Limit: 20})
	if err != nil || len(page.Data) != 1 || !slices.Equal(page.Data[0].Tags, []string{"mysterious"}) || len(page.Data[0].TagRules) != 0 {
		t.Fatal(page, err)
	}
	request(http.MethodPatch, routeEndpointTags, manual, "rules-manual-original-0001", 204)
	for i, invalid := range []EndpointTagRule{{"example.test", "add", []string{"mainstream"}}, {"*.example.test", "remove", []string{"mainstream"}}, {"*.example.test", "add", []string{}}, {"*.example.test", "add", nil}, {"*.example.test", "add", []string{"unknown"}}} {
		request(http.MethodPut, routeEndpointTagRules, invalid, fmt.Sprintf("rules-invalid-%04d-0001", i), 400)
	}
}

func ptrPage(value pagination.Request) *pagination.Request { return &value }

func TestEndpointTagRuleStewardGrantsRevokeReceiptsAndPage(t *testing.T) {
	f, six := newStewardUsersFixture(t)
	actor := f.seedUser("rules-five", false)
	if _, err := f.store.DB().Exec(`UPDATE users SET level=5 WHERE id=?`, actor); err != nil {
		t.Fatal(err)
	}
	body := `{"pattern":"*.future.test","action":"add","tags":["mainstream"]}`
	request := func(who int64, method, body, key string, want int) {
		t.Helper()
		got := stewardUserRequest(t, f, who, 0, method, routeEndpointTagRules, "", body, key)
		if got.Code != want {
			t.Fatalf("%s: %d %s", method, got.Code, got.Body.String())
		}
	}
	request(actor, http.MethodPut, body, "rules-grant-replay-0001", 403)
	request(six, http.MethodPut, body, "rules-six-write-000001", 204)
	policy := authz.DefaultStewardPermissions(5)
	policy.Permissions["endpoints.read"], policy.Permissions["endpoints.tags"] = true, true
	grants, _ := json.Marshal(policy.Permissions)
	limits, _ := json.Marshal(policy.Limits)
	if _, err := f.store.DB().Exec(`INSERT INTO steward_permissions(user_id,revision,permissions,limits,donation_scope,created_at,updated_at) VALUES(?,1,?,?,'standard',1,1)`, actor, string(grants), string(limits)); err != nil {
		t.Fatal(err)
	}
	request(actor, http.MethodPut, body, "rules-grant-replay-0001", 204)
	request(actor, http.MethodPut, body, "rules-grant-replay-0001", 204)
	request(actor, http.MethodGet, "", "", 200)
	for i := 0; i < 23; i++ {
		if _, err := f.store.DB().Exec(`INSERT INTO endpoint_tag_rules VALUES(?,'remove')`, fmt.Sprintf("child-%02d.future.test", i)); err != nil {
			t.Fatal(err)
		}
	}
	page, err := f.service.endpointTagRules(t.Context(), six, roleSteward, "future", pagination.Request{Page: 3, Size: 10})
	if err != nil || len(page.Data) != 4 || page.Pagination.TotalItems != "24" {
		t.Fatal(page, err)
	}
	if _, err := f.store.DB().Exec(`UPDATE steward_permissions SET permissions=json_set(permissions,'$."endpoints.tags"',json('false')),revision=revision+1 WHERE user_id=?`, actor); err != nil {
		t.Fatal(err)
	}
	request(actor, http.MethodPut, body, "rules-grant-replay-0001", 403)
	request(actor, http.MethodDelete, `{"pattern":"*.future.test"}`, "rules-delete-grant-0001", 403)
	request(actor, http.MethodGet, "", "", 200)
	if strings.TrimSpace(page.Data[0].Pattern) == "" {
		t.Fatal("empty pattern")
	}
}
