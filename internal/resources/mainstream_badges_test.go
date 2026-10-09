package resources

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

func TestEndpointBrowseFollowsCurrentMainstreamChannels(t *testing.T) {
	env := newResourceTestEnvironment(t)
	owner := env.seedUser(t, "badge-owner")
	other := env.seedUser(t, "badge-other")
	custom := env.createEndpoint(t, owner, resourceTestKey('a'))
	env.createEndpoint(t, other, resourceTestKey('b'))
	admin := resourceAdminUser(t, env, "badge-admin")
	admins := &resourceAdminTestRegistrar{}
	users := &resourceTestRegistrar{}
	if err := RegisterAdminRoutes(admins, env.repository); err != nil {
		t.Fatal(err)
	}
	if err := RegisterRoutes(users, env.repository); err != nil {
		t.Fatal(err)
	}
	wantCount := 1
	assertCategories := func(t *testing.T, want ...string) {
		t.Helper()
		page, err := env.repository.ListEndpointsPage(context.Background(), owner, pagination.Default())
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range page.Data {
			if item.Browse == nil || !reflect.DeepEqual(item.Browse.MainstreamCategories, append([]string{}, want...)) {
				t.Fatalf("endpoint %s categories=%+v want=%v", item.ID, item.Browse, want)
			}
			if item.ID == custom.ID && item.Origin.Kind != "custom" {
				t.Fatal("current classification changed the creation origin")
			}
		}
		if len(page.Data) != wantCount {
			t.Fatalf("owner endpoint count=%d", len(page.Data))
		}
	}
	assertCategories(t)
	create := admins.handlers[http.MethodPost+" "+routeMainstreamChannels]
	response := resourceAdminHTTPCall(t, create, admin, http.MethodPost, routeMainstreamChannels,
		`{"name":"Channel","category":"subscription","connector_type":"openai-compatible","base_url":"https://example.com/v1","enabled":true}`, resourceTestKey('c'), "")
	if response.Code != http.StatusCreated {
		t.Fatalf("create channel: %d %s", response.Code, response.Body.String())
	}
	var channel MainstreamChannel
	if err := json.Unmarshal(response.Body.Bytes(), &channel); err != nil {
		t.Fatal(err)
	}
	assertCategories(t, "subscription")
	response = resourceHTTPCall(t, users.handlers[http.MethodPost+" "+routeEndpoints], UserPrincipal{UserID: owner}, http.MethodPost, routeEndpoints,
		`{"source":"mainstream","channel_id":"`+channel.ID+`","note":"from channel","enabled":true}`, resourceTestKey('d'), nil)
	if response.Code != http.StatusCreated {
		t.Fatalf("create mainstream endpoint: %d %s", response.Code, response.Body.String())
	}
	wantCount = 2
	assertCategories(t, "subscription")
	for _, step := range []struct {
		name, update string
		want         []string
	}{
		{"category change", `category='api_platform'`, []string{"api_platform"}},
		{"connector mismatch", `connector_type='anthropic-compatible'`, nil},
		{"connector restored", `connector_type='openai-compatible'`, []string{"api_platform"}},
		{"address changed", `canonical_base_url='https://other.example/v1'`, nil},
		{"address restored", `canonical_base_url='https://example.com/v1'`, []string{"api_platform"}},
		{"disabled", `enabled=0`, nil},
		{"enabled again", `enabled=1`, []string{"api_platform"}},
		{"retired", `state='retired',enabled=0,retired_at=1700000000`, nil},
	} {
		t.Run(step.name, func(t *testing.T) {
			if _, err := env.store.DB().Exec(`UPDATE mainstream_channels SET `+step.update+` WHERE id=?`, channel.ID); err != nil {
				t.Fatal(err)
			}
			assertCategories(t, step.want...)
		})
	}
	for i, category := range []string{"subscription", "subscription", "api_platform"} {
		response := resourceAdminHTTPCall(t, create, admin, http.MethodPost, routeMainstreamChannels,
			`{"name":"Current channel","category":"`+category+`","connector_type":"openai-compatible","base_url":"https://example.com/v1","enabled":true}`, resourceTestKey(byte('e'+i)), "")
		if response.Code != http.StatusCreated {
			t.Fatalf("create replacement channel: %d %s", response.Code, response.Body.String())
		}
	}
	assertCategories(t, "subscription", "api_platform")
}
