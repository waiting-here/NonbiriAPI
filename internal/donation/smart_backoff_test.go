package donation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/waiting-here/NonbiriAPI/internal/charityscope"
	"github.com/waiting-here/NonbiriAPI/internal/smartbackoff"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func smartPolicyHTTP(t *testing.T, api *httpAPI, owner int64, d Donation, body, seed string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPatch, "/policy", strings.NewReader(body))
	r.SetPathValue("id", d.ID)
	r.SetPathValue("keyId", d.Keys[0].ID)
	r.Header.Set("Idempotency-Key", strings.Repeat(seed, 22))
	w := httptest.NewRecorder()
	api.failurePolicyEdit(w, r, UserPrincipal{UserID: owner}, "")
	return w
}
func TestSmartBackoffThresholdEditsAndResetPreserveCooldowns(t *testing.T) {
	e := newDonationTestEnv(t)
	owner := e.seedUser(t, "smart-owner", nil, false)
	e.seedUser(t, "", nil, true)
	_, physical := e.seedEndpointKey(t, owner, 'z')
	d := approvedResetDonation(t, e, owner, physical)
	key := parseTestID(t, d.Keys[0].ID)
	api := &httpAPI{service: e.service}
	until := e.clock.Load() + 900
	if _, err := e.store.DB().Exec(`INSERT INTO donation_key_backoffs(donation_key_id,generation,episode,period,source,stage,retry_at,updated_at) VALUES(?,7,3,'day','custom_rule',1,?,?)`, key, until, e.clock.Load()); err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.DB().Exec("INSERT INTO donation_key_cooldowns VALUES(?,?,?)", key, until+60, e.clock.Load()); err != nil {
		t.Fatal(err)
	}
	body := `{"expected_revision":"2","failure_disable_threshold":"10"}`
	w := smartPolicyHTTP(t, api, owner, d, body, "a")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	replay := smartPolicyHTTP(t, api, owner, d, body, "a")
	if replay.Code != 200 || replay.Body.String() != w.Body.String() {
		t.Fatal("replay", replay.Code, replay.Body)
	}
	var policy FailurePolicy
	if err := json.Unmarshal(w.Body.Bytes(), &policy); err != nil || policy.Backoff == nil || policy.Backoff.RetryAt != until || policy.Backoff.Source != "custom_rule" {
		t.Fatal(policy, err)
	}
	if strings.Contains(w.Body.String(), "smart_backoff_profile") || strings.Contains(w.Body.String(), "smart_backoff_rules") {
		t.Fatal("retired fields returned")
	}
	reset := resetHTTP(t, api, recurringOwner, owner, "/reset", `{"expected_revision":"3"}`, "b", d.ID, d.Keys[0].ID)
	if reset.Code != 200 {
		t.Fatal(reset.Code, reset.Body)
	}
	current, err := smartbackoff.Read(t.Context(), e.store.DB(), key, e.clock.Load())
	if err != nil || current.Backoff == nil || *current.Backoff != *policy.Backoff {
		t.Fatal("failure reset changed backoff", current, err)
	}
	var headerUntil, generation, episode int64
	if err := e.store.DB().QueryRow("SELECT rate_limited_until FROM donation_key_cooldowns WHERE donation_key_id=?", key).Scan(&headerUntil); err != nil || headerUntil != until+60 {
		t.Fatal("header cleared", headerUntil, err)
	}
	if err := e.store.DB().QueryRow("SELECT generation,episode FROM donation_key_backoffs WHERE donation_key_id=?", key).Scan(&generation, &episode); err != nil || generation != 7 || episode != 3 {
		t.Fatal("episode changed", generation, episode, err)
	}
	for _, extra := range []string{`,"smart_backoff_profile":"none"`, `,"smart_backoff_profile":"day"`, `,"smart_backoff_rules":[]`, `,"smart_backoff_rules":null`} {
		rejected := smartPolicyHTTP(t, api, owner, d, `{"expected_revision":"4","failure_disable_threshold":"10"`+extra+`}`, "c")
		if rejected.Code != 400 {
			t.Fatal("retired field accepted", rejected.Code, rejected.Body)
		}
	}
	collision := smartPolicyHTTP(t, api, owner, d, `{"expected_revision":"2","failure_disable_threshold":"11"}`, "a")
	if collision.Code != 409 {
		t.Fatal(collision.Code, collision.Body)
	}
}
func TestSmartBackoffCreationExportAndAccountDeletion(t *testing.T) {
	e := newDonationTestEnv(t)
	owner := e.seedUser(t, "smart-export", nil, false)
	_, physical := e.seedEndpointKey(t, owner, 'y')
	created, err := e.service.Create(t.Context(), owner, donationCreateMutation(t, 'W', map[string]any{"smart": true}), CreateInput{Description: "smart", OwnershipAuthorized: true, Keys: []CreateKeyInput{{EndpointKeyID: physical}}})
	if err != nil {
		t.Fatal(err)
	}
	key := parseTestID(t, created.Value.Keys[0].ID)
	if created.Value.Keys[0].Backoff != nil {
		t.Fatal("new key has episode")
	}
	until := e.clock.Load() + 900
	if _, err := e.store.DB().Exec(`INSERT INTO donation_key_backoffs(donation_key_id,generation,episode,period,source,stage,retry_at,updated_at) VALUES(?,1,1,'week','builtin',0,?,?)`, key, until, e.clock.Load()); err != nil {
		t.Fatal(err)
	}
	tx, err := e.store.DB().Begin()
	if err != nil {
		t.Fatal(err)
	}
	exported, err := e.service.ExportUserTx(t.Context(), tx, owner, e.clock.Load(), 100)
	tx.Rollback()
	if err != nil || len(exported) != 1 || exported[0].Keys[0].Backoff == nil || exported[0].Keys[0].Backoff.RetryAt != until {
		t.Fatal(exported, err)
	}
	body, _ := json.Marshal(exported)
	for _, name := range []string{"smart_backoff_profile", "smart_backoff_rules", "smart_backoff_generation", "trial_claim_id"} {
		if strings.Contains(string(body), name) {
			t.Fatal("internal or retired field exported", name)
		}
	}
	tx, err = e.store.DB().Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := e.service.PrepareAccountDeletion(t.Context(), tx, owner, e.clock.Load()); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := e.store.DB().QueryRow("SELECT count(*) FROM donation_key_backoffs WHERE donation_key_id=?", key).Scan(&count); err != nil || count != 0 {
		t.Fatal("state survived account deletion", count, err)
	}
}
func TestSmartBackoffTraineeScopeRecheckedBeforeReplay(t *testing.T) {
	e := newDonationTestEnv(t)
	e.seedUser(t, "", nil, true)
	five := int64(5)
	trainee := e.seedUser(t, "smart-trainee", &five, false)
	owner := e.seedUser(t, "smart-donor", nil, false)
	_, physical := e.seedEndpointKey(t, owner, 't')
	d := approvedResetDonation(t, e, owner, physical)
	id, key := parseTestID(t, d.ID), parseTestID(t, d.Keys[0].ID)
	model := controlModel(t, e, "smart-maintenance", true)
	ctx := charityscope.WithModel(context.Background(), model)
	mutation := donationMutation(t, 'u', http.MethodPatch, routeStewardFailurePolicy, []int64{id, key}, map[string]any{"expected_revision": "2", "failure_disable_threshold": "10"})
	mutation.Query = charityscope.Query(ctx)
	save := func() error {
		_, err := e.service.failurePolicy(ctx, trainee, reviewerSteward, id, key, 2, "10", mutation)
		return err
	}
	if err := save(); !errors.Is(err, ErrNotFound) {
		t.Fatal("unbound key accepted", err)
	}
	if _, err := e.store.DB().Exec("INSERT INTO model_pair_catalog(endpoint_key_id,normalized_model_id,manual_supports,updated_at) VALUES(?,?,1,?)", physical, "upstream", donationTestNow); err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.DB().Exec("INSERT INTO charity_model_bindings(charity_model_id,donation_key_id,endpoint_key_id,upstream_model_id,ord,created_at,updated_at) VALUES(?,?,?, ?,0,?,?)", model, key, physical, "upstream", donationTestNow, donationTestNow); err != nil {
		t.Fatal(err)
	}
	if err := save(); err != nil {
		t.Fatal(err)
	}
	if err := save(); err != nil {
		t.Fatal("authorized replay", err)
	}
	if _, err := e.store.DB().Exec("DELETE FROM charity_model_bindings WHERE charity_model_id=?", model); err != nil {
		t.Fatal(err)
	}
	if err := save(); !errors.Is(err, ErrNotFound) {
		t.Fatal("revoked binding replay", err)
	}
}

func TestAutomaticBackoffCreateRejectsRetiredSettings(t *testing.T) {
	e := newDonationTestEnv(t)
	owner := e.seedUser(t, "automatic-owner", nil, false)
	_, key := e.seedEndpointKey(t, owner, 'a')
	api := &httpAPI{service: e.service}
	for _, field := range []string{"smart_backoff_profile", "smart_backoff_rules"} {
		raw, err := json.Marshal(map[string]any{"description": "automatic", "discord_public_thanks": false, "ownership_authorized": true, "keys": []map[string]any{{"endpoint_key_id": fmt.Sprint(key), "expires_at": nil, field: nil}}})
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(http.MethodPost, "/api/donations", strings.NewReader(string(raw)))
		request.Header.Set("Idempotency-Key", strings.Repeat("S", 22))
		response := httptest.NewRecorder()
		api.createOwner(response, request, UserPrincipal{UserID: owner})
		if response.Code != 400 {
			t.Fatal("retired create field accepted", field, response.Code, response.Body)
		}
	}
}
