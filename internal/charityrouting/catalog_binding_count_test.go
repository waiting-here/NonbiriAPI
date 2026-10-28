package charityrouting

import (
	"fmt"
	"strconv"
	"testing"
	"time"
)

func TestCatalogAvailabilityCountsBindingsAcrossManyTargets(t *testing.T) {
	env := newRoutingTestEnv(t)
	env.seedUser(t, true, nil)
	model := env.createModel(t, 'v')
	modelID, err := strconv.ParseInt(model.ID, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	owner := env.seedUser(t, false, nil)
	_, key, _ := env.seedCandidate(t, owner, 'v', "first")
	env.seedBinding(t, modelID, key, "first", 0)
	var bindingID int64
	if err := env.store.DB().QueryRow(`SELECT id FROM charity_model_bindings WHERE charity_model_id=?`, modelID).Scan(&bindingID); err != nil {
		t.Fatal(err)
	}
	for ordinal := 1; ordinal < 128; ordinal++ {
		if _, err := env.store.DB().Exec(`INSERT INTO charity_binding_targets(binding_id,donation_key_id,upstream_model_id,native_service_mask,selection_origin,ord,created_at,updated_at)
SELECT binding_id,donation_key_id,?,native_service_mask,selection_origin,?,created_at,updated_at FROM charity_binding_targets WHERE binding_id=? AND ord=0`, fmt.Sprintf("model-%03d", ordinal), ordinal, bindingID); err != nil {
			t.Fatal(err)
		}
	}
	query := func(scope string, caller bool, member ...string) int {
		t.Helper()
		var count int
		err := env.store.ReadDB().QueryRow(`SELECT `+availableCandidateCountSQL(scope, caller, member...)+`
FROM charity_models cm CROSS JOIN (SELECT ? AS decision_now,1 AS charity_enabled,? AS caller_id,1 AS caller_level,1 AS token_reserve) cx WHERE cm.id=?`, routingTestNow, owner, modelID).Scan(&count)
		if err != nil {
			t.Fatal(err)
		}
		return count
	}
	started := time.Now()
	for range 20 {
		if count := query("", false); count != 1 {
			t.Fatalf("one binding with 128 eligible targets counted as %d", count)
		}
	}
	t.Logf("20 availability reads with 128 targets: %s", time.Since(started))
	if count := query("", true); count != 0 {
		t.Fatalf("owner exclusion lost: %d", count)
	}
	if count := query(strconv.FormatInt(bindingID, 10), false, strconv.FormatInt(key, 10)); count != 1 {
		t.Fatalf("binding/key scope lost: %d", count)
	}
	if count := query("", false, strconv.FormatInt(key+1, 10)); count != 0 {
		t.Fatalf("another key matched: %d", count)
	}
	if _, err := env.store.DB().Exec(`UPDATE donation_keys SET enabled=0 WHERE id=?`, key); err != nil {
		t.Fatal(err)
	}
	if count := query("", false); count != 0 {
		t.Fatalf("disabled targets remained available: %d", count)
	}
}
