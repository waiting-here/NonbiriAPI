package charityrouting

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

func TestRoutingStrategiesPersistAndControlSnapshots(t *testing.T) {
	environment := newRoutingTestEnv(t)
	environment.seedUser(t, true, nil)
	owner := environment.seedUser(t, false, nil)
	model := environment.createModel(t, 'a')
	modelID, _ := parsePositiveID(model.ID)
	if model.RouteStrategy != RouteExpiryWeighted {
		t.Fatal("existing default changed")
	}
	var selections []BindingSelection
	var ids []int64
	for _, suffix := range []byte{'a', 'b'} {
		_, id, _ := environment.seedCandidate(t, owner, suffix, fmt.Sprintf("model-%c", suffix))
		ids = append(ids, id)
		selections = append(selections, BindingSelection{DonationKeyID: fmt.Sprint(id), UpstreamModelID: fmt.Sprintf("model-%c", suffix)})
	}
	if _, err := environment.store.DB().Exec(`UPDATE donation_keys SET expires_at=? WHERE id=?`, routingTestNow+1, ids[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := environment.service.AddBindingsAdmin(context.Background(), modelID, routingMutation(t, 'b', http.MethodPost, routeAdminBindingBatch, []int64{modelID}, selections), BindingBatch{ExpectedBindingRevision: "0", Selections: selections}); err != nil {
		t.Fatal(err)
	}
	revision := "1"
	for index, strategy := range []string{RouteOrdered, RouteRandom, RouteExpiryWeighted, RouteCacheBalanced} {
		input := ModelPatch{ExpectedRevision: revision, RouteStrategy: &strategy}
		mutation := routingMutation(t, byte('c'+index), http.MethodPatch, routeAdminModel, []int64{modelID}, input)
		result, err := environment.service.PatchAdmin(context.Background(), modelID, mutation, input)
		if err != nil {
			t.Fatal(err)
		}
		if result.Value.RouteStrategy != strategy {
			t.Fatal("strategy not projected")
		}
		replay, err := environment.service.PatchAdmin(context.Background(), modelID, mutation, input)
		if err != nil || !replay.Replayed || replay.Value.Revision != result.Value.Revision {
			t.Fatalf("replay changed revision: %v", err)
		}
		revision = result.Value.Revision
		environment.useEntropy(t, bytes.NewReader(entropyWords(0)))
		if strategy == RouteOrdered {
			environment.useEntropy(t, failedEntropy{})
		} else if strategy == RouteRandom {
			environment.useEntropy(t, bytes.NewReader(entropyWords(1)))
		}
		snapshot, err := environment.service.Snapshot(context.Background(), modelID, routingTestNow, []connectorcontract.Type{connectorcontract.TypeOpenAICompatible})
		if err != nil {
			t.Fatal(err)
		}
		want := []int64{ids[0], ids[1]}
		if strategy == RouteRandom {
			want = []int64{ids[1], ids[0]}
		}
		if !equalInt64s(runtimeCandidateIDs(snapshot.Candidates()), want) {
			t.Fatalf("%s did not control ordering", strategy)
		}
	}
	invalid := "nearest"
	input := ModelPatch{ExpectedRevision: revision, RouteStrategy: &invalid}
	if _, err := environment.service.PatchAdmin(context.Background(), modelID, routingMutation(t, 'x', http.MethodPatch, routeAdminModel, []int64{modelID}, input), input); err != ErrInvalidRequest {
		t.Fatalf("unknown strategy accepted: %v", err)
	}
	if _, err := environment.store.DB().Exec(`DELETE FROM charity_models WHERE id=?`, modelID); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := environment.store.DB().QueryRow(`SELECT COUNT(*) FROM charity_model_routing WHERE model_id=?`, modelID).Scan(&count); err != nil || count != 0 {
		t.Fatal("routing settings did not follow model deletion")
	}
}

func TestRoutingStrategyRejectsNullAndInvalidWireValues(t *testing.T) {
	for _, body := range []string{`{"expected_revision":"1","route_strategy":null}`, `{"expected_revision":"1","route_strategy":""}`, `{"expected_revision":"1","route_strategy":"unknown"}`} {
		var wire modelPatchWire
		if err := json.Unmarshal([]byte(body), &wire); err != nil {
			continue
		}
		input, _, err := parseModelPatch(wire)
		if err == nil && validateModelPatch(input) {
			t.Fatalf("accepted invalid strategy %s", body)
		}
	}
}

func TestRoutingAffinitySettingsRevisionOnlyChangesWithStrategyOrTTL(t *testing.T) {
	environment := newRoutingTestEnv(t)
	environment.seedUser(t, true, nil)
	input := testModelCreate()
	input.RouteStrategy = RouteCacheBalanced
	ttl := 86_400
	input.AffinityTTLSeconds = &ttl
	created, err := environment.service.CreateAdmin(context.Background(),
		routingMutation(t, 'p', http.MethodPost, routeAdminModels, nil, input), input)
	if err != nil {
		t.Fatal(err)
	}
	modelID, _ := parsePositiveID(created.Value.ID)
	if created.Value.RouteStrategy != RouteCacheBalanced || created.Value.AffinityTTLSeconds != ttl {
		t.Fatalf("created routing settings = %+v", created.Value)
	}
	assertSettings := func(wantRevision, wantTTL int64) {
		t.Helper()
		var revision, actualTTL int64
		if err := environment.store.DB().QueryRow(`SELECT revision,affinity_ttl_seconds
FROM charity_routing_settings WHERE model_id=?`, modelID).Scan(&revision, &actualTTL); err != nil {
			t.Fatal(err)
		}
		if revision != wantRevision || actualTTL != wantTTL {
			t.Fatalf("routing settings = revision %d ttl %d, want %d/%d", revision, actualTTL, wantRevision, wantTTL)
		}
	}
	assertSettings(1, 86_400)
	patch := ModelPatch{ExpectedRevision: created.Value.Revision, RouteStrategy: &input.RouteStrategy, AffinityTTLSeconds: &ttl}
	unchanged, err := environment.service.PatchAdmin(context.Background(), modelID,
		routingMutation(t, 'q', http.MethodPatch, routeAdminModel, []int64{modelID}, patch), patch)
	if err != nil {
		t.Fatal(err)
	}
	assertSettings(1, 86_400)
	ttl = 60
	patch = ModelPatch{ExpectedRevision: unchanged.Value.Revision, AffinityTTLSeconds: &ttl}
	changed, err := environment.service.PatchAdmin(context.Background(), modelID,
		routingMutation(t, 'r', http.MethodPatch, routeAdminModel, []int64{modelID}, patch), patch)
	if err != nil {
		t.Fatal(err)
	}
	assertSettings(2, 60)
	strategy := RouteOrdered
	patch = ModelPatch{ExpectedRevision: changed.Value.Revision, RouteStrategy: &strategy}
	if _, err := environment.service.PatchAdmin(context.Background(), modelID,
		routingMutation(t, 's', http.MethodPatch, routeAdminModel, []int64{modelID}, patch), patch); err != nil {
		t.Fatal(err)
	}
	assertSettings(3, 60)

	stewardMutation := routingMutation(t, 't', http.MethodPost, routeStewardModels, nil, input)
	if _, err := environment.service.CreateSteward(context.Background(), 1, stewardMutation, input); err != ErrForbidden {
		t.Fatalf("steward create accepted administrator TTL: %v", err)
	}
	patch = ModelPatch{ExpectedRevision: "1", AffinityTTLSeconds: &ttl}
	if _, err := environment.service.PatchSteward(context.Background(), 1, modelID,
		routingMutation(t, 'u', http.MethodPatch, routeStewardModel, []int64{modelID}, patch), patch); err != ErrForbidden {
		t.Fatalf("steward patch accepted administrator TTL: %v", err)
	}
}

func TestRoutingAffinityTTLWireBounds(t *testing.T) {
	for _, body := range []string{
		`{"expected_revision":"1","affinity_ttl_seconds":null}`,
		`{"expected_revision":"1","affinity_ttl_seconds":0}`,
		`{"expected_revision":"1","affinity_ttl_seconds":86401}`,
		`{"expected_revision":"1","affinity_ttl_seconds":1.5}`,
	} {
		var wire modelPatchWire
		if err := json.Unmarshal([]byte(body), &wire); err != nil {
			continue
		}
		input, _, err := parseModelPatch(wire)
		if err == nil && validateModelPatch(input) {
			t.Fatalf("accepted invalid TTL %s", body)
		}
	}
}
