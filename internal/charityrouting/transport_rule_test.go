package charityrouting

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/transportpolicy"
)

func TestTransportRuleManagementAndIngressSnapshot(t *testing.T) {
	e := newRoutingTestEnv(t)
	e.seedUser(t, true, nil)
	six := int64(6)
	steward := e.seedUser(t, false, &six)
	m := e.createModel(t, 'a')
	id, _ := parsePositiveID(m.ID)
	if m.TransportRule != transportpolicy.Passthrough {
		t.Fatal(m.TransportRule)
	}
	for index, rule := range []transportpolicy.Rule{transportpolicy.ForceNonStream, transportpolicy.ForceStream} {
		p := ModelPatch{TransportRule: &rule, ExpectedRevision: string(rune('1' + index))}
		mutation := routingMutation(t, byte('b'+index), http.MethodPatch, routeStewardModel, []int64{id}, p)
		changed, err := e.service.PatchSteward(context.Background(), steward, id, mutation, p)
		if err != nil || changed.Value.TransportRule != rule {
			t.Fatalf("%+v %v", changed, err)
		}
		replay, err := e.service.PatchSteward(context.Background(), steward, id, mutation, p)
		if err != nil || !replay.Replayed || replay.Value.TransportRule != rule {
			t.Fatalf("%+v %v", replay, err)
		}
	}
	if _, err := e.store.DB().Exec("UPDATE site_config SET value='1' WHERE key='charity_enabled'"); err != nil {
		t.Fatal(err)
	}
	frozen, err := e.service.RequestPolicy(context.Background(), steward, m.FullName, routingTestNow)
	if err != nil || frozen.TransportRule != transportpolicy.ForceStream {
		t.Fatalf("%+v %v", frozen, err)
	}
	rename := "new"
	p := ModelPatch{Model: &rename, ExpectedRevision: "3"}
	changed, err := e.service.PatchSteward(context.Background(), steward, id, routingMutation(t, 'd', http.MethodPatch, routeStewardModel, []int64{id}, p), p)
	if err != nil || changed.Value.TransportRule != transportpolicy.ForceStream || frozen.TransportRule != transportpolicy.ForceStream {
		t.Fatalf("%+v %v", changed, err)
	}
	bad := transportpolicy.Rule("unknown")
	p = ModelPatch{TransportRule: &bad, ExpectedRevision: "4"}
	if _, err := e.service.PatchSteward(context.Background(), steward, id, routingMutation(t, 'e', http.MethodPatch, routeStewardModel, []int64{id}, p), p); !errors.Is(err, ErrInvalidRequest) {
		t.Fatal(err)
	}
}
