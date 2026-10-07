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
		if code := write(urls, "community_charity", true, key); code != 204 {
			t.Fatal("add/replay", code)
		}
	}
	if code := write(urls, "community_charity", false, key); code != 409 {
		t.Fatal("changed replay", code)
	}
	if code := write(urls[:1], "abusive_third_party", true, "endpoint-tags-second-0001"); code != 204 {
		t.Fatal(code)
	}
	page, err := f.service.EndpointOverview(context.Background(), f.adminID, EndpointOverviewQuery{Tag: "abusive_third_party", Limit: 10})
	if err != nil || len(page.Data) != 1 || page.Data[0].UserCount != "2" || !slices.Equal(page.Data[0].Tags, []string{"abusive_third_party", "community_charity"}) {
		t.Fatalf("filter: %+v %v", page, err)
	}
	if code := write([]string{urls[0], "https://missing.example"}, "community_charity", false, "endpoint-tags-atomic-0001"); code != 404 {
		t.Fatal("missing target", code)
	}
	page, err = f.service.EndpointOverview(context.Background(), f.adminID, EndpointOverviewQuery{Tag: "community_charity", Limit: 1})
	if err != nil || len(page.Data) != 1 || page.NextCursor == nil {
		t.Fatalf("rollback/filter %+v %v", page, err)
	}
	if _, err = f.service.EndpointOverview(context.Background(), f.adminID, EndpointOverviewQuery{Tag: "abusive_third_party", Limit: 1, Cursor: *page.NextCursor}); err == nil {
		t.Fatal("cursor escaped tag filter")
	}
	if code := write(urls, "community_charity", false, "endpoint-tags-remove-0001"); code != 204 {
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
	if code := write(urls, "community_charity", true, "endpoint-tags-denied-0001"); code != 403 {
		t.Fatal("final authority", code)
	}
}
