package charityrouting

import (
	"context"
	"errors"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

func TestGatewayBindingPolicyUsesExactTargetAndStewardScope(t *testing.T) {
	e := newRoutingTestEnv(t)
	e.seedUser(t, true, nil)
	level := int64(6)
	steward := e.seedUser(t, false, &level)
	owner := e.seedUser(t, false, nil)
	_, key, _ := e.seedCandidateWithConnector(t, owner, "g", "upstream", contract.TypeAISDKGatewayV3)
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
	got, err := e.service.GetBindingsSteward(ctx, steward, mid)
	if err != nil || len(got.Bindings) != 1 || got.Bindings[0].GatewayCapabilities == nil || got.Bindings[0].GatewayCapabilities.Cache != "anthropic" {
		t.Fatalf("scoped policy: %+v %v", got, err)
	}
	if _, err := e.service.GetBindingsSteward(ctx, owner, mid); !errors.Is(err, ErrForbidden) {
		t.Fatalf("ordinary user read management policy: %v", err)
	}
}
