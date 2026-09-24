package charityrouting

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/db"
)

func TestTraineeBindingOriginAndModelRevocation(t *testing.T) {
	e := newRoutingTestEnv(t)
	e.seedUser(t, true, nil)
	five := int64(5)
	trainee := e.seedUser(t, false, &five)
	owner := e.seedUser(t, false, nil)
	model := e.createModel(t, 'a')
	mid, _ := parsePositiveID(model.ID)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := e.store.DB().Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`UPDATE charity_models SET is_mainstream=1 WHERE id=?`, mid)
	_, customKey, _ := e.seedCandidate(t, owner, 'n', "upstream")
	channel, err := db.GenerateOpaqueID("mch_")
	if err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO mainstream_channels(id,name,category,connector_type,canonical_base_url,enabled,state,revision,created_at,updated_at) VALUES(?,'Example','subscription','openai-compatible','https://example.test/v1',1,'active',1,?,?)`, channel, routingTestNow, routingTestNow)
	_, mainKey, _ := e.seedCandidateWithConnector(t, owner, "m", "upstream", connectorcontract.TypeOpenAICompatible, channel)
	ctx := context.Background()
	candidates, _, _, err := e.service.BindingCandidatesSteward(ctx, trainee, mid, CandidateQuery{Limit: 50})
	if err != nil || len(candidates) != 1 || candidates[0].DonationKeyID != fmt.Sprint(mainKey) {
		t.Fatalf("candidate scope: %+v %v", candidates, err)
	}
	batch := BindingBatch{ExpectedBindingRevision: "0", Selections: []BindingSelection{{DonationKeyID: fmt.Sprint(customKey), UpstreamModelID: "upstream"}}}
	if _, err := e.service.AddBindingsSteward(ctx, trainee, mid, routingMutation(t, 'b', http.MethodPost, routeStewardBindingBatch, []int64{mid}, batch), batch); !errors.Is(err, ErrNotFound) {
		t.Fatal("custom candidate accepted", err)
	}
	added, err := e.service.AddBindingsAdmin(ctx, mid, routingMutation(t, 'c', http.MethodPost, routeAdminBindingBatch, []int64{mid}, batch), batch)
	if err != nil {
		t.Fatal(err)
	}
	bid, _ := parsePositiveID(added.Value.Bindings[0].ID)
	remove := BindingDelete{ExpectedBindingRevision: added.Value.BindingRevision}
	mutation := routingMutation(t, 'd', http.MethodDelete, routeStewardBinding, []int64{mid, bid}, remove)
	removed, err := e.service.DeleteBindingSteward(ctx, trainee, mid, bid, mutation, remove)
	if err != nil || len(removed.Value.Bindings) != 0 {
		t.Fatalf("bound custom removal: %+v %v", removed, err)
	}
	if replayed, err := e.service.DeleteBindingSteward(ctx, trainee, mid, bid, mutation, remove); err != nil || !replayed.Replayed {
		t.Fatal("removal replay", err)
	}
	batch.ExpectedBindingRevision = removed.Value.BindingRevision
	if _, err := e.service.AddBindingsSteward(ctx, trainee, mid, routingMutation(t, 'e', http.MethodPost, routeStewardBindingBatch, []int64{mid}, batch), batch); !errors.Is(err, ErrNotFound) {
		t.Fatal("removed custom rebound", err)
	}
	exec(`UPDATE charity_models SET is_mainstream=0,revision=revision+1 WHERE id=?`, mid)
	if _, err := e.service.DeleteBindingSteward(ctx, trainee, mid, bid, mutation, remove); !errors.Is(err, ErrNotFound) {
		t.Fatal("stale model replay", err)
	}
}

func TestRequestPolicyAndCallerSnapshotUseCurrentAuthority(t *testing.T) {
	e := newRoutingTestEnv(t)
	e.seedUser(t, true, nil)
	owner := e.seedUser(t, false, nil)
	model := e.createModel(t, 'f')
	mid, _ := parsePositiveID(model.ID)
	_, key, _ := e.seedCandidate(t, owner, 'p', "upstream")
	batch := BindingBatch{ExpectedBindingRevision: "0", Selections: []BindingSelection{{DonationKeyID: fmt.Sprint(key), UpstreamModelID: "upstream"}}}
	if _, err := e.service.AddBindingsAdmin(context.Background(), mid, routingMutation(t, 'g', http.MethodPost, routeAdminBindingBatch, []int64{mid}, batch), batch); err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.DB().Exec(`UPDATE charity_models SET excluded_request_fields='["temperature","top_p"]' WHERE id=?`, mid); err != nil {
		t.Fatal(err)
	}
	policy, err := e.service.RequestPolicy(context.Background(), owner, model.FullName, routingTestNow)
	if err != nil || policy.ModelID != mid || len(policy.ExcludedRequestFields) != 2 {
		t.Fatalf("policy: %+v %v", policy, err)
	}
	connectors := []connectorcontract.Type{connectorcontract.TypeOpenAICompatible}
	if _, err := e.service.SnapshotForCaller(context.Background(), owner, mid, routingTestNow, connectors); !errors.Is(err, ErrUnavailable) {
		t.Fatal("self donation candidate admitted", err)
	}
	if _, err := e.store.DB().Exec(`UPDATE users SET level=6 WHERE id=?`, owner); err != nil {
		t.Fatal(err)
	}
	if value, err := e.service.SnapshotForCaller(context.Background(), owner, mid, routingTestNow, connectors); err != nil || len(value.Candidates()) != 1 {
		t.Fatalf("six exemption: %+v %v", value, err)
	}
	if _, err := e.store.DB().Exec(`UPDATE charity_model_access SET allowed_level_mask=1 WHERE model_id=?`, mid); err != nil {
		t.Fatal(err)
	}
	if _, err := e.service.RequestPolicy(context.Background(), owner, model.FullName, routingTestNow); !errors.Is(err, ErrForbidden) {
		t.Fatal("request policy ignored access", err)
	}
}
