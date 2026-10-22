package charityrouting

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

func TestDonorRestrictionsAgreeAcrossCatalogRuntimeAndKeyModels(t *testing.T) {
	env := newRoutingTestEnv(t)
	env.seedUser(t, true, nil)
	level := int64(6)
	steward := env.seedUser(t, false, &level)
	owners := []int64{env.seedUser(t, false, nil), env.seedUser(t, false, nil)}
	donations, keys := make([]int64, 2), make([]int64, 2)
	selections := make([]BindingSelection, 2)
	for i, owner := range owners {
		donations[i], keys[i], _ = env.seedCandidate(t, owner, byte('k'+i), "upstream")
		selections[i] = BindingSelection{DonationKeyID: fmt.Sprint(keys[i]), UpstreamModelID: "upstream"}
	}
	model := catalogCreate(t, env, 'a', "shared", nil, true)
	modelID, _ := parsePositiveID(model.ID)
	ctx := context.Background()
	if _, err := env.service.AddBindingsAdmin(ctx, modelID,
		routingMutation(t, 'b', http.MethodPost, routeAdminBindingBatch, []int64{modelID}, map[string]any{"bind": true}),
		BindingBatch{ExpectedBindingRevision: "0", Selections: selections}); err != nil {
		t.Fatal(err)
	}
	now := env.clock.Load()
	for _, step := range []struct {
		name   string
		donor  int
		banned bool
		kind   string
		until  any
		want   [2]bool
	}{
		{"both eligible", 0, false, "", nil, [2]bool{true, true}},
		{"temporary ban leaves another donor", 0, true, "", now + 1, [2]bool{false, true}},
		{"all donors banned", 1, true, "", nil, [2]bool{false, false}},
		{"ban expires at decision time", 0, true, "", now, [2]bool{true, false}},
		{"ordinary permanent ban", 0, true, "", nil, [2]bool{false, false}},
		{"permanent inactivity protection", 0, true, "protective_inactivity", nil, [2]bool{true, false}},
		{"ordinary ban replaces protection", 0, true, "", now + 1, [2]bool{false, false}},
		{"manual unban restores supply", 0, false, "", nil, [2]bool{true, false}},
		{"other ban expires", 1, true, "", now - 1, [2]bool{true, true}},
	} {
		t.Run(step.name, func(t *testing.T) {
			if _, err := env.store.DB().Exec("UPDATE users SET is_banned=?,ban_kind=?,banned_until=? WHERE id=?",
				step.banned, step.kind, step.until, owners[step.donor]); err != nil {
				t.Fatal(err)
			}
			wantCount := 0
			for _, available := range step.want {
				if available {
					wantCount++
				}
			}
			wantAvailable := wantCount > 0
			var before, after int64
			if err := env.store.DB().QueryRow("SELECT total_changes()").Scan(&before); err != nil {
				t.Fatal(err)
			}
			page, err := env.service.Catalog(ctx, env.caller, CatalogFilter{}, pagination.Default())
			if err != nil || len(page.Models) != 1 {
				t.Fatalf("catalog: %+v %v", page, err)
			}
			wantState := "no_usable_key"
			if wantAvailable {
				wantState = "available"
			}
			if page.Models[0].CurrentlyAvailable != wantAvailable || page.Models[0].Availability != wantState {
				t.Errorf("catalog availability: %+v, want %s", page.Models[0], wantState)
			}
			for _, available := range []bool{false, true} {
				filtered, err := env.service.Catalog(ctx, env.caller, CatalogFilter{CurrentlyAvailable: &available}, pagination.Default())
				wantRows := 0
				if available == wantAvailable {
					wantRows = 1
				}
				if err != nil || len(filtered.Models) != wantRows || filtered.Pagination.TotalItems != fmt.Sprint(wantRows) {
					t.Errorf("filtered availability %v: %+v %v", available, filtered, err)
				}
			}
			available, err := env.service.ListAvailableModels(ctx, env.caller, now, 100)
			if err != nil || (len(available) == 1) != wantAvailable {
				t.Errorf("model list: %+v %v", available, err)
			}
			capability, err := env.service.Capability(ctx, env.caller, now)
			if err != nil || (len(capability.Models) == 1) != wantAvailable {
				t.Errorf("capability: %+v %v", capability, err)
			}
			for i := range owners {
				for _, role := range []roleKind{roleAdmin, roleSteward} {
					models, _, err := env.service.keyModelPages(ctx, role, testRoleActor(role, steward), donations[i], keys[i], 0, pagination.Default())
					count, state := "0", "unavailable"
					if step.want[i] {
						count, state = "1", "available"
					}
					if err != nil || len(models.Data) != 1 {
						t.Fatalf("key model associations: %+v %v", models, err)
					}
					if models.Data[0].BindingCount != "1" || models.Data[0].AvailableBindingCount != count {
						t.Errorf("key model availability: %+v, want %s", models, count)
					}
					_, bindings, err := env.service.keyModelPages(ctx, role, testRoleActor(role, steward), donations[i], keys[i], modelID, pagination.Default())
					if err != nil || len(bindings.Data) != 1 || bindings.Data[0].State != state {
						t.Errorf("binding availability: %+v %v, want %s", bindings, err, state)
					}
				}
			}
			if err := env.store.DB().QueryRow("SELECT total_changes()").Scan(&after); err != nil || before != after {
				t.Fatalf("availability reads wrote state: %d/%d %v", before, after, err)
			}
			snapshot, err := env.service.snapshot(ctx, modelID, now, false, nil, connectorcontract.OperationChatCompletions)
			if wantAvailable {
				if err != nil || len(snapshot.Candidates()) != wantCount {
					t.Fatalf("runtime candidates: %+v %v", snapshot, err)
				}
			} else if !errors.Is(err, ErrUnavailable) {
				t.Fatalf("runtime error: %v", err)
			}
		})
	}
}
