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
func TestSmartBackoffPolicyPreservesOmittedConfigAndResetCooldowns(t *testing.T) {
	e := newDonationTestEnv(t)
	owner := e.seedUser(t, "smart-owner", nil, false)
	e.seedUser(t, "", nil, true)
	_, physical := e.seedEndpointKey(t, owner, 'z')
	d := approvedResetDonation(t, e, owner, physical)
	key := parseTestID(t, d.Keys[0].ID)
	api := &httpAPI{service: e.service}
	save := func(rev int, extra, seed string) FailurePolicy {
		t.Helper()
		body := fmt.Sprintf(`{"expected_revision":"%d","failure_disable_threshold":"10"%s}`, rev, extra)
		w := smartPolicyHTTP(t, api, owner, d, body, seed)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body)
		}
		replay := smartPolicyHTTP(t, api, owner, d, body, seed)
		if replay.Code != 200 || replay.Body.String() != w.Body.String() {
			t.Fatal("replay", replay.Code, replay.Body)
		}
		var p FailurePolicy
		if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
			t.Fatal(err)
		}
		return p
	}
	config := `,"smart_backoff_profile":"day_month","smart_backoff_rules":[{"kind":"message","value":"  ` + strings.Repeat("额", 160) + `  "}]`
	p := save(2, config, "a")
	if p.Profile != "day_month" || len(p.Rules) != 1 || p.Rules[0].Period != "auto" || len([]rune(p.Rules[0].Value)) != 160 {
		t.Fatal(p)
	}
	until := e.clock.Load() + 900
	if _, err := e.store.DB().Exec("UPDATE donation_key_backoffs SET episode=1,period=?,source=?,retry_at=? WHERE donation_key_id=?", "day", "builtin", until, key); err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.DB().Exec("INSERT INTO donation_key_cooldowns VALUES(?,?,?)", key, until+60, e.clock.Load()); err != nil {
		t.Fatal(err)
	}
	p = save(3, "", "b")
	if p.Backoff == nil || p.Backoff.RetryAt != until || len(p.Rules) != 1 {
		t.Fatal("omission reset", p)
	}
	p = save(4, config, "c")
	if p.Backoff == nil || p.Backoff.RetryAt != until {
		t.Fatal("same config reset", p)
	}
	reset := resetHTTP(t, api, recurringOwner, owner, "/reset", `{"expected_revision":"5"}`, "d", d.ID, d.Keys[0].ID)
	if reset.Code != 200 {
		t.Fatal(reset.Code, reset.Body)
	}
	current, err := smartbackoff.Read(t.Context(), e.store.DB(), key, e.clock.Load())
	if err != nil || current.Backoff == nil || current.Backoff.RetryAt != until {
		t.Fatal("failure reset cleared backoff", current, err)
	}
	p = save(6, `,"smart_backoff_rules":[]`, "e")
	if p.Backoff != nil || len(p.Rules) != 0 {
		t.Fatal("rule edit did not clear episode", p)
	}
	var headerUntil int64
	if err := e.store.DB().QueryRow("SELECT rate_limited_until FROM donation_key_cooldowns WHERE donation_key_id=?", key).Scan(&headerUntil); err != nil || headerUntil != until+60 {
		t.Fatal("header was cleared", headerUntil, err)
	}
	for _, extra := range []string{
		`,"smart_backoff_rules":[{"kind":"code","value":"quota","period":null}]`,
		`,"smart_backoff_rules":[{"kind":"code","value":"quota","period":""}]`, `,"smart_backoff_profile":null`, `,"smart_backoff_rules":null`, `,"smart_backoff_rules":[{"kind":"code","value":"x","period":"week"}]`, `,"smart_backoff_rules":[{"kind":"message","value":"a","unexpected":true}]`} {
		w := smartPolicyHTTP(t, api, owner, d, `{"expected_revision":"7","failure_disable_threshold":"10"`+extra+`}`, "f")
		if w.Code != 400 {
			t.Fatal("bad config", w.Code, w.Body)
		}
	}
	// Omission and explicit empty are different idempotency requests.
	collision := smartPolicyHTTP(t, api, owner, d, `{"expected_revision":"6","failure_disable_threshold":"10"}`, "e")
	if collision.Code != 409 {
		t.Fatal(collision.Code, collision.Body)
	}
}
func TestSmartBackoffCreationExportAndAccountDeletion(t *testing.T) {
	e := newDonationTestEnv(t)
	owner := e.seedUser(t, "smart-export", nil, false)
	_, physical := e.seedEndpointKey(t, owner, 'y')
	profile := "day"
	rules := []smartbackoff.Rule{{Kind: "message", Value: "personal fixed phrase"}}
	created, err := e.service.Create(t.Context(), owner, donationCreateMutation(t, 'W', map[string]any{"smart": true}), CreateInput{Description: "smart", OwnershipAuthorized: true, Keys: []CreateKeyInput{{EndpointKeyID: physical, SmartBackoffProfile: &profile, SmartBackoffRules: &rules}}})
	if err != nil {
		t.Fatal(err)
	}
	key := parseTestID(t, created.Value.Keys[0].ID)
	if created.Value.Keys[0].Profile != "day" {
		t.Fatal("creation lost config")
	}
	tx, err := e.store.DB().Begin()
	if err != nil {
		t.Fatal(err)
	}
	exported, err := e.service.ExportUserTx(t.Context(), tx, owner, e.clock.Load(), 100)
	tx.Rollback()
	if err != nil || len(exported) != 1 || exported[0].Keys[0].Rules[0].Value != rules[0].Value {
		t.Fatal(exported, err)
	}
	body, _ := json.Marshal(exported)
	if strings.Contains(string(body), "smart_backoff_generation") || strings.Contains(string(body), "trial_claim_id") {
		t.Fatal("internal facts exported")
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
		t.Fatal("personal rules survived account deletion", count, err)
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
	profile := "day"
	mutation := donationMutation(t, 'u', http.MethodPatch, routeStewardFailurePolicy, []int64{id, key}, map[string]any{"expected_revision": "2", "failure_disable_threshold": "10", "smart_backoff_profile": profile})
	mutation.Query = charityscope.Query(ctx)
	save := func() error {
		_, err := e.service.failurePolicy(ctx, trainee, reviewerSteward, id, key, 2, "10", mutation, smartbackoff.Patch{Profile: &profile})
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
