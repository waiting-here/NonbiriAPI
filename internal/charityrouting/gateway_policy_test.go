package charityrouting

import (
	"context"
	"errors"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

func TestGatewayBindingPolicyUsesExactTargetAndStewardScope(t *testing.T) {
	for _, kind := range []contract.Type{contract.TypeAISDKGatewayV3, contract.TypeAISDKGatewayV4} {
		t.Run(string(kind), func(t *testing.T) {
			e := newRoutingTestEnv(t)
			e.seedUser(t, true, nil)
			level := int64(6)
			steward := e.seedUser(t, false, &level)
			owner := e.seedUser(t, false, nil)
			_, key, _ := e.seedCandidateWithConnector(t, owner, "g", "upstream", kind)
			model := e.createModel(t, 'a')
			mid := catalogBind(t, e, model, key, 'b')
			raw := `{"adapter":"anthropic_effort","efforts":["high"],"max_output_tokens":128000,"storage":"reject","cache":"anthropic"}`
			for _, target := range [][2]string{{"https://other.test/v1", "upstream"}, {"https://g.routing.test/v1", "other"}} {
				if _, err := e.store.DB().Exec(`INSERT INTO gateway_model_capabilities(base_url,model,policy_json,revision,updated_at) VALUES(?,?,?,1,1)`, target[0], target[1], raw); err != nil {
					t.Fatal(err)
				}
			}
			ctx := context.Background()
			before, err := e.service.GetBindingsAdmin(ctx, mid)
			if err != nil || len(before.Bindings) != 1 || before.Bindings[0].GatewayCapabilities != nil {
				t.Fatalf("wrong target leaked: %+v %v", before, err)
			}
			if _, err := e.store.DB().Exec(`INSERT INTO gateway_model_capabilities(base_url,model,policy_json,revision,updated_at) VALUES('https://g.routing.test/v1','upstream',?,1,1)`, raw); err != nil {
				t.Fatal(err)
			}
			// An alternative target on the same key has its own declaration.
			if _, err := e.store.DB().Exec(`INSERT INTO charity_binding_targets(binding_id,donation_key_id,upstream_model_id,native_service_mask,selection_origin,ord,revision,created_at,updated_at)
SELECT binding_id,donation_key_id,'unconfigured',native_service_mask,selection_origin,1,1,created_at,updated_at FROM charity_binding_targets WHERE binding_id=?`, before.Bindings[0].ID); err != nil {
				t.Fatal(err)
			}
			got, err := e.service.GetBindingsSteward(ctx, steward, mid)
			if err != nil || len(got.Bindings) != 1 || got.Bindings[0].GatewayCapabilities == nil || got.Bindings[0].GatewayCapabilities.Cache != "anthropic" {
				t.Fatalf("scoped policy: %+v %v", got, err)
			}
			targets := got.Bindings[0].Targets
			if len(targets) != 2 || targets[0].GatewayCapabilities == nil || targets[0].GatewayCapabilities.Cache != "anthropic" || targets[1].GatewayCapabilities != nil {
				t.Fatalf("target policy inherited from sibling: %+v", targets)
			}
			if _, err := e.service.GetBindingsSteward(ctx, owner, mid); !errors.Is(err, ErrForbidden) {
				t.Fatalf("ordinary user read management policy: %v", err)
			}
		})
	}
}
