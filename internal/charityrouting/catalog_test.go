package charityrouting

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/donationquota"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

func TestRecurringLimitsAgreeAcrossCatalogCapabilityAndRuntimeWithoutWrites(t *testing.T) {
	env := newRoutingTestEnv(t)
	env.seedUser(t, true, nil)
	owner := env.seedUser(t, false, nil)
	model := catalogCreate(t, env, 'a', "limited", nil, true)
	_, key, _ := env.seedCandidate(t, owner, 'k', "upstream")
	modelID, _ := parsePositiveID(model.ID)
	if _, err := env.service.AddBindingsAdmin(context.Background(), modelID, routingMutation(t, 'b', http.MethodPost, routeAdminBindingBatch, []int64{modelID}, map[string]any{"bind": true}), BindingBatch{ExpectedBindingRevision: "0", Selections: []BindingSelection{{DonationKeyID: fmt.Sprint(key), UpstreamModelID: "upstream"}}}); err != nil {
		t.Fatal(err)
	}
	for _, limit := range []string{"0", "1"} {
		tx, err := env.store.DB().BeginTx(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		alignment := "first_success"
		err = donationquota.Replace(context.Background(), tx, key, env.clock.Load(), []donationquota.RuleInput{{Mode: "reset", Interval: "5h", Alignment: &alignment, TimeZone: "UTC", Metric: "calls", Limit: limit}})
		if err != nil {
			_ = tx.Rollback()
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		var before int64
		if err := env.store.DB().QueryRow(`SELECT total_changes()`).Scan(&before); err != nil {
			t.Fatal(err)
		}
		catalog, err := env.service.Catalog(context.Background(), env.caller, CatalogFilter{}, pagination.Default())
		if err != nil {
			t.Fatal(err)
		}
		want := "available"
		if limit == "0" {
			want = "no_usable_key"
		}
		if len(catalog.Models) != 1 || catalog.Models[0].Availability != want {
			t.Fatal(catalog)
		}
		available, err := env.service.ListAvailableModels(context.Background(), env.caller, env.clock.Load(), 100)
		if err != nil {
			t.Fatal(err)
		}
		capability, err := env.service.Capability(context.Background(), env.caller, env.clock.Load())
		if err != nil {
			t.Fatal(err)
		}
		if (len(available) == 0) != (limit == "0") || (len(capability.Models) == 0) != (limit == "0") {
			t.Fatal(available, capability)
		}
		var after int64
		if err := env.store.DB().QueryRow(`SELECT total_changes()`).Scan(&after); err != nil || after != before {
			t.Fatal("readonly availability wrote state", before, after, err)
		}
		_, err = env.service.Snapshot(context.Background(), modelID, env.clock.Load(), []connectorcontract.Type{connectorcontract.TypeOpenAICompatible})
		if limit == "0" && !errors.Is(err, donationquota.ErrLimited) {
			t.Fatal(err)
		}
		if limit != "0" && err != nil {
			t.Fatal(err)
		}
	}
}

func catalogCreate(t *testing.T, env *routingTestEnv, seed byte, name string, levels []int, enabled bool) AdminCharityModel {
	t.Helper()
	input := testModelCreate()
	input.Model, input.AllowedLevels, input.Enabled = name, levels, enabled
	input.PublicDescription = "<b>plain</b>\r\n" + name
	result, err := env.service.CreateAdmin(context.Background(), routingMutation(t, seed, http.MethodPost, routeAdminModels, nil,
		map[string]any{"name": name}), input)
	if err != nil {
		t.Fatal(err)
	}
	return result.Value
}

func TestCatalogExplainsAllStatesWithoutPrivateFieldsOrWrites(t *testing.T) {
	env := newRoutingTestEnv(t)
	env.seedUser(t, true, nil)
	owner := env.seedUser(t, false, nil)
	available := catalogCreate(t, env, 'a', "available", nil, true)
	catalogCreate(t, env, 'b', "disabled", []int{}, false)
	catalogCreate(t, env, 'c', "denied", []int{}, true)
	catalogCreate(t, env, 'd', "unbound", nil, true)
	_, key, _ := env.seedCandidate(t, owner, 'k', "upstream")
	modelID, _ := parsePositiveID(available.ID)
	if _, err := env.service.AddBindingsAdmin(context.Background(), modelID,
		routingMutation(t, 'e', http.MethodPost, routeAdminBindingBatch, []int64{modelID}, map[string]any{"bind": true}),
		BindingBatch{ExpectedBindingRevision: "0", Selections: []BindingSelection{{DonationKeyID: fmt.Sprint(key), UpstreamModelID: "upstream"}}}); err != nil {
		t.Fatal(err)
	}
	before := env.state.dueCalls.Load()
	catalog, err := env.service.Catalog(context.Background(), env.caller, CatalogFilter{}, pagination.Default())
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"available": "available", "disabled": "model_disabled", "denied": "level_denied", "unbound": "no_usable_key"}
	if len(catalog.Models) != 4 || catalog.Pagination.TotalItems != "4" {
		t.Fatalf("catalog: %+v", catalog)
	}
	for _, model := range catalog.Models {
		if model.Availability != want[model.Model] || model.PublicDescription != "<b>plain</b>\n"+model.Model {
			t.Fatalf("model: %+v", model)
		}
		encoded, _ := json.Marshal(model)
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(encoded, &fields); err != nil {
			t.Fatal(err)
		}
		if len(fields) != 13 {
			t.Fatalf("catalog model fields: %s", encoded)
		}
		for _, forbidden := range []string{"source", "bindings", "donor_reward", "limits", "donation_key_id", "owner", "route_strategy"} {
			if strings.Contains(string(encoded), `"`+forbidden+`"`) {
				t.Fatalf("private field %s: %s", forbidden, encoded)
			}
		}
	}
	if env.state.dueCalls.Load() != before {
		t.Fatal("catalog materialized expiry")
	}
	env.setCapabilityGates(t, "0", "0")
	catalog, err = env.service.Catalog(context.Background(), env.caller, CatalogFilter{}, pagination.Default())
	if err != nil || len(catalog.Models) != 4 || catalog.DonationIntake != "closed" {
		t.Fatalf("closed catalog: %+v %v", catalog, err)
	}
	for _, model := range catalog.Models {
		if model.Availability != "feature_disabled" {
			t.Fatalf("gate priority: %+v", model)
		}
	}
}

func TestCatalogPagesSearchAndCurrentCallerLevel(t *testing.T) {
	env := newRoutingTestEnv(t)
	admin := env.seedUser(t, true, nil)
	for n := 0; n < 23; n++ {
		catalogCreate(t, env, byte(n+'A'), fmt.Sprintf("model-%02d", n), []int{1, 3, 5}, true)
	}
	page := pagination.Request{Page: 99, Size: 10}
	value, err := env.service.Catalog(context.Background(), env.caller, CatalogFilter{}, page)
	if err != nil || value.Pagination.Page != "3" || value.Pagination.TotalPages != "3" || len(value.Models) != 3 || value.Models[0].Model != "model-20" {
		t.Fatalf("clamped catalog %+v %v", value, err)
	}
	for level := 1; level <= 5; level++ {
		if _, err := env.store.DB().Exec(`UPDATE users SET level=? WHERE id=?`, level, env.caller); err != nil {
			t.Fatal(err)
		}
		for _, allowed := range []bool{true, false} {
			value, err := env.service.Catalog(context.Background(), env.caller, CatalogFilter{AllowedForMe: &allowed}, pagination.Default())
			if err != nil {
				t.Fatal(err)
			}
			want := "0"
			if allowed == (level == 1 || level == 3 || level == 5) {
				want = "23"
			}
			if value.Pagination.TotalItems != want {
				t.Fatalf("level %d filter %v got %s", level, allowed, value.Pagination.TotalItems)
			}
		}
	}
	for _, test := range []struct {
		query string
		count string
	}{{"<b>plain</b>", "23"}, {"MODEL-01", "1"}, {"%", "0"}, {"donor-secret", "0"}} {
		value, err := env.service.Catalog(context.Background(), env.caller, CatalogFilter{Query: test.query}, pagination.Default())
		if err != nil || value.Pagination.TotalItems != test.count {
			t.Fatalf("query %q: %+v %v", test.query, value, err)
		}
	}
	if _, err := env.service.Catalog(context.Background(), admin, CatalogFilter{}, pagination.Default()); err != ErrUnauthorized {
		t.Fatalf("admin catalog err %v", err)
	}
	if _, err := env.service.Catalog(context.Background(), 99999, CatalogFilter{}, pagination.Default()); err != ErrUnauthorized {
		t.Fatalf("missing caller err %v", err)
	}
}

func TestCatalogHTTPRejectsAmbiguousQueriesAndPreservesCapability(t *testing.T) {
	env := newRoutingTestEnv(t)
	for _, query := range []string{"view=unknown", "page=1", "view=catalog&cursor=", "view=catalog&limit=20", "view=catalog&page=01", "view=catalog&allowed_for_me=1", "view=catalog&q=a&q=b", "view=catalog&view=catalog", "view=catalog&unknown=x", "view=catalog&q=%00", "view=catalog&q=%ff"} {
		request := httptest.NewRequest(http.MethodGet, routeCapability+"?"+query, nil)
		response := httptest.NewRecorder()
		(&httpAPI{service: env.service}).capability(response, request, UserPrincipal{UserID: env.caller})
		if response.Code != 400 {
			t.Fatalf("query %q status=%d body=%s", query, response.Code, response.Body.String())
		}
	}
	for _, query := range []string{"?view=catalog", "?view=catalog&page_size=100&allowed_for_me=false", ""} {
		response := httptest.NewRecorder()
		(&httpAPI{service: env.service}).capability(response, httptest.NewRequest(http.MethodGet, routeCapability+query, nil), UserPrincipal{UserID: env.caller})
		if response.Code != 200 || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("query %q status=%d body=%s", query, response.Code, response.Body.String())
		}
	}
}
