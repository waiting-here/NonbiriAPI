package charityrouting

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestBindingReorderAndDeletePreserveAdaptationsAndReplay(t *testing.T) {
	env := newRoutingTestEnv(t)
	env.seedUser(t, true, nil)
	owner := env.seedUser(t, false, nil)
	model := env.createModel(t, 'a')
	modelID, _ := parsePositiveID(model.ID)
	selections := []BindingSelection{}
	for _, suffix := range []byte{'a', 'b', 'c'} {
		_, key, _ := env.seedCandidate(t, owner, suffix, "upstream")
		selections = append(selections, BindingSelection{DonationKeyID: fmt.Sprint(key), UpstreamModelID: "upstream"})
	}
	added, err := env.service.AddBindingsAdmin(context.Background(), modelID, routingMutation(t, 'b', http.MethodPost, routeAdminBindingBatch, []int64{modelID}, selections), BindingBatch{ExpectedBindingRevision: "0", Selections: selections})
	if err != nil {
		t.Fatal(err)
	}
	order := []string{}
	for i, binding := range added.Value.Bindings {
		order = append([]string{binding.ID}, order...)
		if _, err := env.store.DB().Exec(`INSERT INTO request_adaptations(id,scope,binding_id,revision,secret_context,secret_ciphertext,structure_json,updated_at) VALUES(?,'binding',?,7,zeroblob(16),?,'{}',6)`, i+1, binding.ID, "cipher-"+binding.ID); err != nil {
			t.Fatal(err)
		}
	}
	input := BindingOrder{ExpectedBindingRevision: "1", Order: order}
	mutation := routingMutation(t, 'c', http.MethodPut, routeAdminBindingOrder, []int64{modelID}, input)
	for attempt := 0; attempt < 2; attempt++ {
		result, err := env.service.OrderBindingsAdmin(context.Background(), modelID, mutation, input)
		if err != nil || result.Value.BindingRevision != "2" || result.Replayed != (attempt == 1) {
			t.Fatal("reorder/replay", result, err)
		}
		for i, binding := range result.Value.Bindings {
			if binding.ID != order[i] || binding.Ord != i {
				t.Fatal("order mismatch", binding)
			}
		}
	}
	removed, _ := parsePositiveID(order[1])
	del := BindingDelete{ExpectedBindingRevision: "2"}
	mutation = routingMutation(t, 'd', http.MethodDelete, routeAdminBinding, []int64{modelID, removed}, del)
	for attempt := 0; attempt < 2; attempt++ {
		result, err := env.service.DeleteBindingAdmin(context.Background(), modelID, removed, mutation, del)
		if err != nil || result.Value.BindingRevision != "3" || len(result.Value.Bindings) != 2 || result.Replayed != (attempt == 1) {
			t.Fatal("delete/replay", result, err)
		}
		for i, binding := range result.Value.Bindings {
			if binding.Ord != i {
				t.Fatal("delete left gap", binding)
			}
		}
	}
	var intact, removedRows int
	if err := env.store.DB().QueryRow(`SELECT count(*) FROM request_adaptations WHERE revision=7 AND secret_context=zeroblob(16) AND secret_ciphertext='cipher-'||binding_id AND updated_at=6`).Scan(&intact); err != nil || intact != 2 {
		t.Fatal("surviving child identity/revision/cipher changed", intact, err)
	}
	if err := env.store.DB().QueryRow(`SELECT count(*) FROM request_adaptations WHERE binding_id=?`, removed).Scan(&removedRows); err != nil || removedRows != 0 {
		t.Fatal("deleted binding child retained", removedRows, err)
	}
}

func TestMissingBindingSourceRemainsAnArrayAndRecovers(t *testing.T) {
	env := newRoutingTestEnv(t)
	env.seedUser(t, true, nil)
	owner := env.seedUser(t, false, nil)
	_, key, endpointKey := env.seedCandidate(t, owner, 'k', "upstream")
	model := env.createModel(t, 'a')
	modelID := catalogBind(t, env, model, key, 'b')
	for _, supports := range []int{0, 1} {
		if _, err := env.store.DB().Exec(`UPDATE model_pair_catalog SET automatic_supports=0,manual_supports=? WHERE endpoint_key_id=? AND normalized_model_id='upstream'`, supports, endpointKey); err != nil {
			t.Fatal(err)
		}
		value, err := readAdminBindingsDB(context.Background(), env.store.DB(), modelID)
		if err != nil || len(value.Bindings) != 1 {
			t.Fatal(value, err)
		}
		for _, projection := range []any{value, stewardBindings(value)} {
			encoded, err := json.Marshal(projection)
			if err != nil {
				t.Fatal(err)
			}
			want := `"source_types":[]`
			if supports == 1 {
				want = `"source_types":["manual"]`
			}
			if !strings.Contains(string(encoded), want) {
				t.Fatalf("source wire %s lacks %s", encoded, want)
			}
		}
	}
}
