package adminusers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
)

func TestEndpointTagsBulkAtomicityFiltersAndAuthority(t *testing.T) {
	for _, tag := range []string{"community_charity", "noncommercial_gateway", "user_gateway", "steward_gateway", "invalid_entry", "mainstream", "mysterious", "other_third_party"} {
		t.Run(tag, func(t *testing.T) { checkEndpointTag(t, tag) })
	}
}

func checkEndpointTag(t *testing.T, tag string) {
	f := newAdminUsersFixture(t)
	u := f.seedUser("tagged-endpoints", false)
	urls := []string{"https://first.example/v1", "https://second.example/v1"}
	for _, url := range urls {
		f.seedEndpoint(u, url, true, 0)
	}
	f.seedEndpoint(f.seedUser("shared-endpoint", false), urls[0], true, 0)
	write := func(urls []string, tag string, add bool, key string) int {
		body, _ := json.Marshal(endpointTagsMutation{urls, tag, add})
		return f.request(http.MethodPatch, routeEndpointTags, "https://admin.example"+routeEndpointTags, string(body), 0, key).Code
	}
	key := "endpoint-tags-bulk-000001"
	for range 2 {
		if code := write(urls, tag, true, key); code != 204 {
			t.Fatal("add/replay", code)
		}
	}
	if code := write(urls, tag, false, key); code != 409 {
		t.Fatal("changed replay", code)
	}
	if code := write(urls[:1], "abusive_third_party", true, "endpoint-tags-second-0001"); code != 204 {
		t.Fatal(code)
	}
	page, err := f.service.EndpointOverview(context.Background(), f.adminID, EndpointOverviewQuery{Tag: "abusive_third_party", Limit: 10})
	if err != nil || len(page.Data) != 1 || page.Data[0].UserCount != "2" || !slices.Equal(page.Data[0].Tags, []string{"abusive_third_party", tag}) {
		t.Fatalf("filter: %+v %v", page, err)
	}
	if code := write([]string{urls[0], "https://missing.example"}, tag, false, "endpoint-tags-atomic-0001"); code != 404 {
		t.Fatal("missing target", code)
	}
	page, err = f.service.EndpointOverview(context.Background(), f.adminID, EndpointOverviewQuery{Tag: tag, Limit: 1})
	if err != nil || len(page.Data) != 1 || page.NextCursor == nil {
		t.Fatalf("rollback/filter %+v %v", page, err)
	}
	if _, err = f.service.EndpointOverview(context.Background(), f.adminID, EndpointOverviewQuery{Tag: "abusive_third_party", Limit: 1, Cursor: *page.NextCursor}); err == nil {
		t.Fatal("cursor escaped tag filter")
	}
	if code := write(urls, tag, false, "endpoint-tags-remove-0001"); code != 204 {
		t.Fatal(code)
	}
	page, err = f.service.EndpointOverview(context.Background(), f.adminID, EndpointOverviewQuery{Tag: "untagged", Limit: 10})
	if err != nil || len(page.Data) != 1 || page.Data[0].BaseURL != urls[1] {
		t.Fatalf("untagged %+v %v", page, err)
	}
	for i, tag := range []string{"unknown", ""} {
		if code := write(urls, tag, true, fmt.Sprintf("endpoint-tags-invalid-%04d", i)); code != 400 {
			t.Fatal("unknown tag", code)
		}
	}
	f.auth.err = authz.ErrForbidden
	if code := write(urls, tag, true, "endpoint-tags-denied-0001"); code != 403 {
		t.Fatal("final authority", code)
	}
}

func TestStewardEndpointSectionsCheckLiveGrantsBeforeReplay(t *testing.T) {
	f, six := newStewardUsersFixture(t)
	actor := f.seedUser("endpoint-reviewer", false)
	owner := f.seedUser("endpoint-owner", false)
	if _, err := f.store.DB().Exec(`UPDATE users SET level=5 WHERE id=?`, actor); err != nil {
		t.Fatal(err)
	}
	base := "https://endpoint-grants.example/v1"
	f.seedEndpoint(owner, base, true, 0)
	body, _ := json.Marshal(endpointTagsMutation{[]string{base}, "steward_gateway", true})
	request := func(who int64, method, route, body, key string, want int) {
		t.Helper()
		got := stewardUserRequest(t, f, who, 0, method, route, "", body, key)
		if got.Code != want {
			t.Fatalf("%s %s=%d want%d: %s", method, route, got.Code, want, got.Body.String())
		}
	}
	request(actor, http.MethodGet, routeEndpointOverview, "", "", 403)
	request(actor, http.MethodPatch, routeEndpointTags, string(body), "endpoint-live-replay-key1", 403)
	request(six, http.MethodGet, routeEndpointOverview, "", "", 200)
	request(six, http.MethodPatch, routeEndpointTags, string(body), "endpoint-six-write-key01", 204)
	policy := authz.DefaultStewardPermissions(5)
	policy.Permissions["endpoints.read"] = true
	policy.Permissions["endpoints.tags"] = true
	grants, _ := json.Marshal(policy.Permissions)
	limits, _ := json.Marshal(policy.Limits)
	if _, err := f.store.DB().Exec(`INSERT INTO steward_permissions(user_id,revision,permissions,limits,donation_scope,created_at,updated_at) VALUES(?,1,?,?,'standard',1,1)`, actor, string(grants), string(limits)); err != nil {
		t.Fatal(err)
	}
	request(actor, http.MethodGet, routeEndpointOverview, "", "", 200)
	request(actor, http.MethodPatch, routeEndpointTags, string(body), "endpoint-live-replay-key1", 204)
	request(actor, http.MethodPatch, routeEndpointTags, string(body), "endpoint-live-replay-key1", 204)
	if _, err := f.store.DB().Exec(`UPDATE steward_permissions SET permissions=json_set(permissions,'$."endpoints.tags"',json('false')),revision=revision+1 WHERE user_id=?`, actor); err != nil {
		t.Fatal(err)
	}
	request(actor, http.MethodPatch, routeEndpointTags, string(body), "endpoint-live-replay-key1", 403)
	request(actor, http.MethodGet, routeEndpointOverview, "", "", 200)
}
