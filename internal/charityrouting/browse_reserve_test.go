package charityrouting

import (
	"errors"
	"net/http"
	"testing"

	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/modeltype"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

func TestCallerListsKeepUnconfiguredUsageModelsUnavailable(t *testing.T) {
	for _, mode := range []string{"per_token", "per_search_unit"} {
		t.Run(mode, func(t *testing.T) {
			env := newRoutingTestEnv(t)
			env.setCapabilityGates(t, "1", "1")
			env.seedUser(t, true, nil)
			owner := env.seedUser(t, false, nil)
			_, key, _ := env.seedCandidate(t, owner, 'k', "upstream")
			healthy := env.createModel(t, 'a')
			catalogBind(t, env, healthy, key, 'b')

			input := testModelCreate()
			input.Model, input.FlattenToolCalls = mode, false
			if mode == "per_token" {
				zero := &TokenPricesInput{UncachedInput: "0", CacheWriteInput: "0", CacheReadInput: "0", Output: "0"}
				input.Pricing = PricingInput{Mode: mode, UserPrices: zero, DonorRewards: zero}
			} else {
				input.ModelTypes = modeltype.Set{connectorcontract.OperationRerank}
				input.Pricing.Mode = mode
			}
			missing, err := env.service.CreateAdmin(t.Context(),
				routingMutation(t, 'c', http.MethodPost, routeAdminModels, nil, map[string]any{"mode": mode}), input)
			if err != nil {
				t.Fatal(err)
			}

			models, err := env.service.ListAvailableModels(t.Context(), env.caller, routingTestNow, MaxAvailableModels)
			if err != nil || len(models) != 1 {
				t.Fatalf("models=%+v err=%v", models, err)
			}
			capability, err := env.service.Capability(t.Context(), env.caller, routingTestNow)
			if err != nil || capability.State != "available" || capability.DonationIntake != "open" ||
				len(capability.Models) != 1 || capability.Models[0].ID != healthy.ID {
				t.Fatalf("capability=%+v err=%v", capability, err)
			}
			catalog, err := env.service.Catalog(t.Context(), env.caller, CatalogFilter{}, pagination.Default())
			if err != nil || len(catalog.Models) != 2 {
				t.Fatalf("catalog=%+v err=%v", catalog, err)
			}
			for _, model := range catalog.Models {
				if model.CurrentlyAvailable != (model.ID == healthy.ID) {
					t.Fatalf("availability=%+v missing=%s", model, missing.Value.ID)
				}
			}

			// Missing is supported; a stored invalid amount remains an invariant error.
			if _, err := env.store.DB().Exec(`INSERT INTO site_config(key,value,updated_at) VALUES('charity_token_reserve_milli','0',?)`, routingTestNow); err != nil {
				t.Fatal(err)
			}
			for name, call := range browseCalls(env.service, env.caller) {
				if err := call(t.Context()); !errors.Is(err, ErrInvariant) {
					t.Fatalf("%s invalid reserve: %v", name, err)
				}
			}
		})
	}
}
