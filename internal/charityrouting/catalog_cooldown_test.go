package charityrouting

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

func TestBrowseCooldownUsesSharedKeyAndExactDecisionTime(t *testing.T) {
	env := newRoutingTestEnv(t)
	env.seedUser(t, true, nil)
	owner := env.seedUser(t, false, nil)
	_, first, _ := env.seedCandidate(t, owner, 'k', "upstream")
	_, second, physical := env.seedCandidate(t, owner, 'l', "upstream")
	for i, name := range []string{"first-model", "second-model"} {
		model := catalogCreate(t, env, byte('a'+i), name, nil, true)
		id, _ := parsePositiveID(model.ID)
		_, err := env.service.AddBindingsAdmin(t.Context(), id, routingMutation(t, byte('c'+i), http.MethodPost, routeAdminBindingBatch, []int64{id}, map[string]any{"bind": name}), BindingBatch{ExpectedBindingRevision: "0", Selections: []BindingSelection{{DonationKeyID: fmt.Sprint(first), UpstreamModelID: "upstream"}, {DonationKeyID: fmt.Sprint(second), UpstreamModelID: "upstream"}}})
		if err != nil {
			t.Fatal(err)
		}
	}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := env.store.DB().Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	check := func(label string, want bool) {
		t.Helper()
		t.Run(label, func(t *testing.T) {
			ctx := context.Background()
			var before, after int64
			if err := env.store.DB().QueryRow("SELECT total_changes()").Scan(&before); err != nil {
				t.Fatal(err)
			}
			available, err := env.service.ListAvailableModels(ctx, env.caller, env.clock.Load(), MaxAvailableModels)
			if err != nil || (len(available) == 2) != want || (!want && len(available) != 0) {
				t.Fatalf("OpenAI model source: count=%d want_available=%v err=%v", len(available), want, err)
			}
			capability, err := env.service.Capability(ctx, env.caller, env.clock.Load())
			if err != nil || (len(capability.Models) == 2) != want || (!want && len(capability.Models) != 0) {
				t.Fatalf("capability: %+v err=%v", capability, err)
			}
			page, err := env.service.Catalog(ctx, env.caller, CatalogFilter{}, pagination.Default())
			if err != nil || len(page.Models) != 2 {
				t.Fatalf("catalog: %+v %v", page, err)
			}
			for _, model := range page.Models {
				if model.CurrentlyAvailable != want {
					t.Fatalf("catalog projection: %+v", model)
				}
			}
			for _, filter := range []bool{true, false} {
				page, err := env.service.Catalog(ctx, env.caller, CatalogFilter{CurrentlyAvailable: &filter}, pagination.Default())
				count := 0
				if filter == want {
					count = 2
				}
				if err != nil || len(page.Models) != count || page.Pagination.TotalItems != fmt.Sprint(count) {
					t.Fatalf("catalog filter=%v: %+v %v", filter, page, err)
				}
			}
			if err := env.store.DB().QueryRow("SELECT total_changes()").Scan(&after); err != nil || before != after {
				t.Fatalf("browse mutated state: %d -> %d: %v", before, after, err)
			}
		})
	}
	start := env.clock.Load()
	check("no cooldown", true)
	exec("INSERT INTO donation_key_cooldowns VALUES(?,?,?)", first, start+60, start)
	check("another usable key", true)
	exec("INSERT INTO donation_key_cooldowns VALUES(?,?,?)", second, start+120, start)
	check("all keys cooling", false)
	env.clock.Store(start + 59)
	check("before first boundary", false)
	env.clock.Store(start + 60)
	check("first expires at decision time", true)
	exec("UPDATE donation_keys SET enabled=0 WHERE id=?", first)
	check("expired key remains disabled", false)
	env.clock.Store(start + 120)
	check("second expires at decision time", true)
	exec("UPDATE endpoint_keys SET enabled=0 WHERE id=?", physical)
	check("expired physical key remains disabled", false)
}
