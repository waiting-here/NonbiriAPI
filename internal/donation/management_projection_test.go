package donation

import (
	"context"
	"database/sql"
	"net/http"
	"testing"
)

func TestIdleCountsBindingsToDisabledModelsAndChangesImmediately(t *testing.T) {
	env := newDonationTestEnv(t)
	env.seedUser(t, "", nil, true)
	owner := env.seedUser(t, "binding-owner", nil, false)
	_, key := env.seedEndpointKey(t, owner, 'b')
	d := env.createDonation(t, owner, key)
	id, keyID := parseTestID(t, d.ID), parseTestID(t, d.Keys[0].ID)
	assertCount := func(count string, idle bool) {
		t.Helper()
		value, err := env.service.GetAdmin(context.Background(), id)
		if err != nil || len(value.Keys) != 1 || value.Keys[0].BindingCount != count || value.Keys[0].Idle != idle {
			t.Fatalf("projection %+v %v", value, err)
		}
	}
	assertCount("0", true)
	model, err := env.store.DB().Exec("INSERT INTO charity_models(provider,model,full_name,enabled,pricing_mode,created_at,updated_at) VALUES('provider','disabled','[公益]provider/disabled',0,'per_request',?,?)", donationTestNow, donationTestNow)
	if err != nil {
		t.Fatal(err)
	}
	modelID, err := model.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.store.DB().Exec("INSERT INTO charity_model_access(model_id,allowed_level_mask,public_description) VALUES(?,31,'')", modelID); err != nil {
		t.Fatal(err)
	}
	if _, err := env.store.DB().Exec("INSERT INTO model_pair_catalog(endpoint_key_id,normalized_model_id,manual_supports,updated_at) VALUES(?,'upstream',1,?)", key, donationTestNow); err != nil {
		t.Fatal(err)
	}
	if _, err := env.store.DB().Exec("INSERT INTO charity_model_bindings(charity_model_id,donation_key_id,endpoint_key_id,upstream_model_id,ord,created_at,updated_at) VALUES(?,?,?,'upstream',0,?,?)", modelID, keyID, key, donationTestNow, donationTestNow); err != nil {
		t.Fatal(err)
	}
	assertCount("1", false)
	if _, err := env.store.DB().Exec("DELETE FROM charity_model_bindings WHERE charity_model_id=?", modelID); err != nil {
		t.Fatal(err)
	}
	assertCount("0", true)
}

func TestProcessedHandlingSurvivesActorDeletionWithoutAnIdentitySnapshot(t *testing.T) {
	env := newDonationTestEnv(t)
	owner := env.seedUser(t, "handling-owner", nil, false)
	level := int64(5)
	actor := env.seedUser(t, "handling-actor", &level, false)
	env.seedUser(t, "", nil, true)
	_, key := env.seedEndpointKey(t, owner, 'd')
	d := env.createDonation(t, owner, key)
	id := parseTestID(t, d.ID)
	mutation := donationMutation(t, 'D', http.MethodPost, routeStewardProcessed, []int64{id}, map[string]any{"expected_handling_revision": "1"})
	if _, err := env.service.ProcessSteward(context.Background(), actor, id, mutation, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := env.store.DB().Exec("DELETE FROM users WHERE id=?", actor); err != nil {
		t.Fatal(err)
	}
	var actorID sql.NullInt64
	if err := env.store.DB().QueryRow("SELECT processed_by_user_id FROM donation_handling WHERE donation_id=?", id).Scan(&actorID); err != nil {
		t.Fatal(err)
	}
	if actorID.Valid {
		t.Fatal("deleted actor linkage remained")
	}
	view, err := env.service.GetAdmin(context.Background(), id)
	if err != nil || view.Handling.State != "processed" || view.Handling.Revision != "2" || view.Handling.ProcessedByRole == nil || *view.Handling.ProcessedByRole != "steward" {
		t.Fatalf("processed facts %+v %v", view, err)
	}
}
