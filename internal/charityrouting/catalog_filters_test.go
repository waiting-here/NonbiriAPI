package charityrouting

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/donationquota"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

func catalogBind(t *testing.T, env *routingTestEnv, model AdminCharityModel, key int64, seed byte) int64 {
	t.Helper()
	id, err := parsePositiveID(model.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = env.service.AddBindingsAdmin(context.Background(), id,
		routingMutation(t, seed, http.MethodPost, routeAdminBindingBatch, []int64{id}, map[string]any{"bind": model.ID}),
		BindingBatch{ExpectedBindingRevision: "0", Selections: []BindingSelection{{DonationKeyID: fmt.Sprint(key), UpstreamModelID: "upstream"}}})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestCatalogIndependentLevelPersonalAndResourceFilters(t *testing.T) {
	env := newRoutingTestEnv(t)
	env.seedUser(t, true, nil)
	owner := env.seedUser(t, false, nil)
	_, key, _ := env.seedCandidate(t, owner, 'k', "upstream")
	a := catalogCreate(t, env, 'a', "a", []int{1, 3}, true)
	b := catalogCreate(t, env, 'b', "b", []int{2, 4}, true)
	catalogCreate(t, env, 'c', "c", []int{}, false)
	catalogCreate(t, env, 'd', "d", []int{1, 2}, true)
	catalogBind(t, env, a, key, 'e')
	catalogBind(t, env, b, key, 'f')
	if _, err := env.store.DB().Exec(`UPDATE users SET level=1 WHERE id=?`, env.caller); err != nil {
		t.Fatal(err)
	}
	yes, no, one, two, three, five := true, false, 1, 2, 3, 5
	for _, test := range []struct {
		name   string
		filter CatalogFilter
		want   []string
	}{
		{"all", CatalogFilter{}, []string{"a", "b", "c", "d"}},
		{"level one", CatalogFilter{AllowedLevel: &one}, []string{"a", "d"}},
		{"level two", CatalogFilter{AllowedLevel: &two}, []string{"b", "d"}},
		{"level three is not cumulative", CatalogFilter{AllowedLevel: &three}, []string{"a"}},
		{"level five is not privileged", CatalogFilter{AllowedLevel: &five}, []string{}},
		{"my access", CatalogFilter{AllowedForMe: &yes}, []string{"a", "d"}},
		{"denied to me", CatalogFilter{AllowedForMe: &no}, []string{"b", "c"}},
		{"resources available", CatalogFilter{CurrentlyAvailable: &yes}, []string{"a", "b"}},
		{"resources unavailable", CatalogFilter{CurrentlyAvailable: &no}, []string{"c", "d"}},
		{"page defaults", CatalogFilter{AllowedForMe: &yes, CurrentlyAvailable: &yes}, []string{"a"}},
		{"denied but resources available", CatalogFilter{AllowedForMe: &no, CurrentlyAvailable: &yes}, []string{"b"}},
		{"allowed but resources unavailable", CatalogFilter{AllowedForMe: &yes, CurrentlyAvailable: &no}, []string{"d"}},
		{"all three intersect", CatalogFilter{AllowedLevel: &two, AllowedForMe: &no, CurrentlyAvailable: &yes}, []string{"b"}},
		{"conflicting intersection", CatalogFilter{AllowedLevel: &three, AllowedForMe: &no, CurrentlyAvailable: &yes}, []string{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			page, err := env.service.Catalog(context.Background(), env.caller, test.filter, pagination.Request{Page: 99, Size: 10})
			if err != nil {
				t.Fatal(err)
			}
			got := make([]string, 0, len(page.Models))
			for _, model := range page.Models {
				got = append(got, model.Model)
				if model.CurrentlyAvailable != (model.Model == "a" || model.Model == "b") {
					t.Fatalf("resource state: %+v", model)
				}
				if model.Model == "b" && (model.LevelAllowed || model.Availability != "level_denied" || !model.CurrentlyAvailable) {
					t.Fatalf("independent access state: %+v", model)
				}
			}
			if !reflect.DeepEqual(got, test.want) || page.Pagination.TotalItems != fmt.Sprint(len(test.want)) || page.Pagination.Page != "1" {
				t.Fatalf("got %v metadata %+v, want %v", got, page.Pagination, test.want)
			}
		})
	}
	if _, err := env.store.DB().Exec(`UPDATE users SET level=2 WHERE id=?`, env.caller); err != nil {
		t.Fatal(err)
	}
	page, err := env.service.Catalog(context.Background(), env.caller, CatalogFilter{AllowedForMe: &yes, CurrentlyAvailable: &yes}, pagination.Default())
	if err != nil || len(page.Models) != 1 || page.Models[0].Model != "b" {
		t.Fatalf("changed current level: %+v, %v", page, err)
	}
}

func TestCatalogFilteredCountAndProjectionFollowResourceChangesWithoutWrites(t *testing.T) {
	env := newRoutingTestEnv(t)
	env.seedUser(t, true, nil)
	owner := env.seedUser(t, false, nil)
	donation, key, physical := env.seedCandidate(t, owner, 'k', "upstream")
	model := catalogCreate(t, env, 'a', "resource", nil, true)
	modelID := catalogBind(t, env, model, key, 'b')
	ctx := context.Background()
	check := func(want bool) {
		t.Helper()
		var before, after int64
		if err := env.store.DB().QueryRow(`SELECT total_changes()`).Scan(&before); err != nil {
			t.Fatal(err)
		}
		for _, available := range []bool{false, true} {
			page, err := env.service.Catalog(ctx, env.caller, CatalogFilter{CurrentlyAvailable: &available}, pagination.Default())
			if err != nil {
				t.Fatal(err)
			}
			expected := 0
			if want == available {
				expected = 1
			}
			if len(page.Models) != expected || page.Pagination.TotalItems != fmt.Sprint(expected) {
				t.Fatalf("availability %v: %+v", available, page)
			}
			for _, model := range page.Models {
				if model.CurrentlyAvailable != want {
					t.Fatalf("row differs from count: %+v", model)
				}
			}
		}
		if err := env.store.DB().QueryRow(`SELECT total_changes()`).Scan(&after); err != nil || after != before {
			t.Fatalf("catalog wrote state: %d/%d %v", before, after, err)
		}
		_, err := env.service.snapshot(ctx, modelID, env.clock.Load(), false, nil)
		if (err == nil) != want {
			t.Fatalf("runtime disagrees: want=%v err=%v", want, err)
		}
	}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := env.store.DB().Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	mag := func(value uint64) []byte {
		t.Helper()
		n, err := db.ParseU128Decimal(fmt.Sprint(value))
		if err != nil {
			t.Fatal(err)
		}
		return db.EncodeU128(n)
	}
	check(true)
	exec(`UPDATE endpoint_keys SET enabled=0 WHERE id=?`, physical)
	check(false)
	exec(`UPDATE endpoint_keys SET enabled=1 WHERE id=?`, physical)
	check(true)
	exec(`UPDATE endpoints SET enabled=0 WHERE id=(SELECT endpoint_id FROM endpoint_keys WHERE id=?)`, physical)
	check(false)
	exec(`UPDATE endpoints SET enabled=1 WHERE id=(SELECT endpoint_id FROM endpoint_keys WHERE id=?)`, physical)
	exec(`UPDATE model_pair_catalog SET automatic_supports=0,manual_supports=0 WHERE endpoint_key_id=?`, physical)
	check(false)
	exec(`UPDATE model_pair_catalog SET automatic_supports=1 WHERE endpoint_key_id=?`, physical)
	check(true)
	exec(`UPDATE donation_keys SET expires_at=? WHERE id=?`, env.clock.Load(), key)
	check(false)
	exec(`UPDATE donation_keys SET expires_at=NULL WHERE id=?`, key)
	exec(`UPDATE donation_keys SET call_limit_mag=? WHERE id=?`, mag(0), key)
	check(false)
	exec(`UPDATE donation_keys SET call_limit_mag=NULL,token_limit_mag=? WHERE id=?`, mag(9), key)
	check(false)
	exec(`UPDATE donation_keys SET token_limit_mag=?,tokens_reserved=? WHERE id=?`, mag(10), mag(1), key)
	check(false)
	exec(`UPDATE donation_keys SET tokens_reserved=?,token_limit_mag=NULL WHERE id=?`, mag(0), key)
	check(true)
	var price uint64
	if err := env.store.DB().QueryRow(`SELECT request_user_price FROM charity_models WHERE id=?`, modelID).Scan(&price); err != nil {
		t.Fatal(err)
	}
	exec(`UPDATE charity_models SET discount_percent=100,discount_enabled=1 WHERE id=?`, modelID)
	exec(`UPDATE donation_keys SET price_limit_mag=? WHERE id=?`, mag(price-1), key)
	check(false)
	exec(`UPDATE donation_keys SET price_limit_mag=? WHERE id=?`, mag(price), key)
	check(true)
	exec(`UPDATE donation_keys SET price_reserved_mag=? WHERE id=?`, mag(1), key)
	check(false)
	exec(`UPDATE donation_keys SET price_reserved_mag=?,price_limit_mag=NULL WHERE id=?`, mag(0), key)
	exec(`INSERT INTO site_config(key,value,updated_at) VALUES('charity_token_reserve_milli','5',?)`, env.clock.Load())
	exec(`UPDATE charity_models SET pricing_mode='per_token' WHERE id=?`, modelID)
	exec(`UPDATE donation_keys SET price_limit_mag=? WHERE id=?`, mag(4), key)
	check(false)
	exec(`UPDATE donation_keys SET price_limit_mag=? WHERE id=?`, mag(5), key)
	check(true)
	exec(`UPDATE site_config SET value='6' WHERE key='charity_token_reserve_milli'`)
	check(false)
	exec(`UPDATE donation_keys SET price_limit_mag=NULL WHERE id=?`, key)
	for _, limit := range []string{"0", "1"} {
		tx, err := env.store.DB().BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		alignment := "calendar"
		if err := donationquota.Replace(ctx, tx, key, env.clock.Load(), []donationquota.RuleInput{{Mode: "reset", Interval: "day", Alignment: &alignment, TimeZone: "America/New_York", Metric: "calls", Limit: limit}}); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		check(limit == "1")
	}
	exec(`UPDATE charity_models SET enabled=0 WHERE id=?`, modelID)
	check(false)
	exec(`UPDATE charity_models SET enabled=1 WHERE id=?`, modelID)
	env.setCapabilityGates(t, "0", "0")
	check(false)
	env.setCapabilityGates(t, "1", "1")
	check(true)
	exec(`DELETE FROM donation_key_memberships WHERE donation_id=?`, donation)
	check(false)
}

func TestCatalogFilteredPagesUseCompleteCollectionAndStrictQueries(t *testing.T) {
	env := newRoutingTestEnv(t)
	env.seedUser(t, true, nil)
	owner := env.seedUser(t, false, nil)
	_, key, _ := env.seedCandidate(t, owner, 'k', "upstream")
	for i := 0; i < 23; i++ {
		model := catalogCreate(t, env, byte('A'+i), fmt.Sprintf("page-%02d", i), []int{1, 3}, true)
		catalogBind(t, env, model, key, byte('a'+i))
	}
	yes, level := true, 3
	filter := CatalogFilter{AllowedForMe: &yes, AllowedLevel: &level, CurrentlyAvailable: &yes}
	for _, size := range []int{10, 20, 50, 100} {
		value, err := env.service.Catalog(context.Background(), env.caller, filter, pagination.Request{Page: 999, Size: size})
		if err != nil {
			t.Fatal(err)
		}
		last := (23 + size - 1) / size
		if value.Pagination.TotalItems != "23" || value.Pagination.Page != fmt.Sprint(last) || len(value.Models) != 23-(last-1)*size {
			t.Fatalf("filtered last page: %+v", value)
		}
	}
	for _, query := range []string{"allowed_level=0", "allowed_level=7", "allowed_level=01", "allowed_level=1&allowed_level=2", "allowed_level=", "currently_available=1", "currently_available=", "currently_available=true&currently_available=false"} {
		response := httptest.NewRecorder()
		(&httpAPI{service: env.service}).capability(response, httptest.NewRequest(http.MethodGet, routeCapability+"?view=catalog&"+query, nil), UserPrincipal{UserID: env.caller})
		if response.Code != 400 {
			t.Fatalf("%s: %d %s", query, response.Code, response.Body)
		}
	}
	response := httptest.NewRecorder()
	(&httpAPI{service: env.service}).capability(response, httptest.NewRequest(http.MethodGet, routeCapability+"?view=catalog&allowed_level=3&allowed_for_me=true&currently_available=true&page=3&page_size=10", nil), UserPrincipal{UserID: env.caller})
	if response.Code != 200 {
		t.Fatalf("combined HTTP query: %d %s", response.Code, response.Body)
	}
	level = 7
	if _, err := env.service.Catalog(context.Background(), env.caller, filter, pagination.Default()); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("invalid repository level: %v", err)
	}
}

func TestCatalogResourceAvailabilityHonorsCandidateCeiling(t *testing.T) {
	env := newRoutingTestEnv(t)
	env.seedUser(t, true, nil)
	owner := env.seedUser(t, false, nil)
	model := catalogCreate(t, env, 'A', "many-candidates", nil, true)
	modelID, _ := parsePositiveID(model.ID)
	selections := make([]BindingSelection, 0, MaxRuntimeCandidates+1)
	var firstKey int64
	for i := 0; i <= MaxRuntimeCandidates; i++ {
		_, key, _ := env.seedCandidateWithConnector(t, owner, fmt.Sprintf("ceiling-%d", i), "upstream", "openai-compatible")
		if i == 0 {
			firstKey = key
		}
		selections = append(selections, BindingSelection{DonationKeyID: fmt.Sprint(key), UpstreamModelID: "upstream"})
	}
	if _, err := env.service.AddBindingsAdmin(context.Background(), modelID, routingMutation(t, 'B', http.MethodPost, routeAdminBindingBatch, []int64{modelID}, map[string]any{"ceiling": true}), BindingBatch{ExpectedBindingRevision: "0", Selections: selections}); err != nil {
		t.Fatal(err)
	}
	yes := true
	page, err := env.service.Catalog(context.Background(), env.caller, CatalogFilter{CurrentlyAvailable: &yes}, pagination.Default())
	if err != nil || page.Pagination.TotalItems != "0" {
		t.Fatalf("oversized candidate set counted as available: %+v %v", page, err)
	}
	if _, err := env.service.snapshot(context.Background(), modelID, env.clock.Load(), false, nil); !errors.Is(err, ErrResourceLimit) {
		t.Fatalf("runtime ceiling: %v", err)
	}
	if _, err := env.store.DB().Exec(`UPDATE donation_keys SET enabled=0 WHERE id=?`, firstKey); err != nil {
		t.Fatal(err)
	}
	page, err = env.service.Catalog(context.Background(), env.caller, CatalogFilter{CurrentlyAvailable: &yes}, pagination.Default())
	if err != nil || page.Pagination.TotalItems != "1" || !page.Models[0].CurrentlyAvailable {
		t.Fatalf("legal candidate count: %+v %v", page, err)
	}
	if _, err := env.service.snapshot(context.Background(), modelID, env.clock.Load(), false, nil); err != nil {
		t.Fatal(err)
	}
}
