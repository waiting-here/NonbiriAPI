package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/donation"
)

func TestTraineeCharityMaintenanceRealSessionScopeAndRevokedReplay(t *testing.T) {
	f := newGameWireFixture(t)
	exec := func(query string, args ...any) int64 {
		t.Helper()
		result, err := f.store.DB().Exec(query, args...)
		if err != nil {
			t.Fatal(err)
		}
		id, err := result.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	sequence := 0
	nextKey := func() string {
		sequence++
		return fmt.Sprintf("trainee-session-%016d", sequence)
	}
	call := func(method, path, body, key string, admin bool) *httptest.ResponseRecorder {
		t.Helper()
		station, cookies := auditUserHost, f.cookies
		if admin {
			station, cookies = auditAdminHost, f.adminCookies
		}
		headers := map[string]string{"Content-Type": "application/json", "Origin": "http://" + station}
		if key != "" {
			headers["Idempotency-Key"] = key
		}
		return testApplicationRequest(t, f.app.handler, method, station, path, body, cookies, headers)
	}
	requireStatus := func(response *httptest.ResponseRecorder, status int) {
		t.Helper()
		if response.Code != status {
			t.Fatalf("HTTP status = %d, want %d: %s", response.Code, status, response.Body)
		}
	}
	exec(`UPDATE site_config SET value='1' WHERE key IN ('donation_accept_enabled','charity_enabled')`)
	channel, err := db.GenerateOpaqueID("mch_")
	if err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO mainstream_channels(id,name,category,connector_type,canonical_base_url,enabled,state,revision,created_at,updated_at)
VALUES(?,'Synthetic channel','subscription','openai-compatible','https://scope.example.test/v1',1,'active',1,?,?)`, channel, f.now, f.now)

	type donated struct {
		id, revision, key string
		physical          int64
	}
	seedDonation := func(mainstream bool) donated {
		t.Helper()
		var endpoint int64
		if mainstream {
			endpoint = exec(`INSERT INTO endpoints(user_id,connector_type,base_url,note,enabled,revision,mainstream_channel_id,mainstream_channel_revision,mainstream_channel_name,mainstream_channel_category,created_at,updated_at)
VALUES(?,'openai-compatible','https://scope.example.test/v1','Private physical endpoint note',1,1,?,1,'Synthetic channel','subscription',?,?)`, f.userID, channel, f.now, f.now)
		} else {
			endpoint = exec(`INSERT INTO endpoints(user_id,connector_type,base_url,note,enabled,revision,created_at,updated_at)
VALUES(?,'openai-compatible','https://custom.example.test/v1','Private physical endpoint note',1,1,?,?)`, f.userID, f.now, f.now)
		}
		base := "https://scope.example.test/v1"
		if !mainstream {
			base = "https://custom.example.test/v1"
		}
		secretID := exec(`INSERT INTO endpoint_key_secrets(context_id,canonical_base_url,connector_type,encrypted_secret,created_at)
VALUES(randomblob(16),?,'openai-compatible','synthetic-envelope',?)`, base, f.now)
		physical := exec(`INSERT INTO endpoint_keys(endpoint_id,secret_ref_id,secret_fingerprint,display_head,display_tail,note,enabled,force_store_false,revision,created_at,updated_at)
VALUES(?,?,randomblob(32),'head','tail','Private physical key note',1,0,1,?,?)`, endpoint, secretID, f.now, f.now)
		exec(`INSERT INTO model_pair_catalog(endpoint_key_id,normalized_model_id,manual_supports,updated_at)
VALUES(?,'synthetic/maintained',1,?)`, physical, f.now)
		created := call(http.MethodPost, "/api/donations", fmt.Sprintf(`{"description":"Scoped donor note","keys":[{"endpoint_key_id":"%d","expires_at":null}],"ownership_authorized":true,"discord_public_thanks":false}`, physical), nextKey(), false)
		requireStatus(created, http.StatusCreated)
		var value struct {
			ID, Revision, Status string
			Keys                 []struct{ ID string }
		}
		if err := json.Unmarshal(created.Body.Bytes(), &value); err != nil || len(value.Keys) != 1 {
			t.Fatalf("donation receipt: %s; %v", created.Body, err)
		}
		if value.Status != "approved" {
			review := call(http.MethodPost, "/admin/api/donations/"+value.ID+"/review",
				fmt.Sprintf(`{"decision":"approve","expected_revision":%q,"reason":"Scoped approval note","key_settings":[{"donation_key_id":%q,"enabled":true,"price_limit":null,"calls_limit":null,"tokens_limit":null,"token_reserve":0,"safe_note":"","expires_at":null}]}`, value.Revision, value.Keys[0].ID), nextKey(), true)
			requireStatus(review, http.StatusOK)
			if err := json.Unmarshal(review.Body.Bytes(), &value); err != nil {
				t.Fatal(err)
			}
		}
		return donated{id: value.ID, revision: value.Revision, key: value.Keys[0].ID, physical: physical}
	}
	mainKey, customKey := seedDonation(true), seedDonation(false)
	model := exec(`INSERT INTO charity_models(provider,model,full_name,enabled,pricing_mode,is_mainstream,created_at,updated_at)
VALUES('synthetic','managed','[公益]synthetic/managed',1,'per_request',1,?,?)`, f.now, f.now)
	other := exec(`INSERT INTO charity_models(provider,model,full_name,enabled,pricing_mode,is_mainstream,created_at,updated_at)
VALUES('synthetic','outside-scope','[公益]synthetic/outside-scope',1,'per_request',0,?,?)`, f.now, f.now)
	for _, id := range []int64{model, other} {
		exec(`INSERT INTO charity_model_access(model_id,allowed_level_mask) VALUES(?,63)`, id)
	}
	exec(`INSERT INTO charity_model_bindings(charity_model_id,donation_key_id,endpoint_key_id,upstream_model_id,ord,created_at,updated_at)
VALUES(?,?,?,'synthetic/maintained',0,?,?)`, other, customKey.key, customKey.physical, f.now, f.now)
	exec("UPDATE users SET level=5 WHERE id=?", f.userID)

	scope := "?charity_model_id=" + strconv.FormatInt(model, 10)
	keyPath := "/api/steward/donations/" + mainKey.id + "/keys/" + mainKey.key
	modelPath := "/api/steward/charity-models/" + strconv.FormatInt(model, 10)
	candidates := call(http.MethodGet, modelPath+"/binding-candidates", "", "", false)
	requireStatus(candidates, http.StatusOK)
	var page struct {
		Data []struct {
			DonationKeyID string `json:"donation_key_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(candidates.Body.Bytes(), &page); err != nil || len(page.Data) != 1 || page.Data[0].DonationKeyID != mainKey.key {
		t.Fatalf("mainstream candidate scope: %s; %v", candidates.Body, err)
	}
	view := call(http.MethodGet, keyPath+scope, "", "", false)
	requireStatus(view, http.StatusOK)
	var keyView donation.ManagedKeySummary
	if err := json.Unmarshal(view.Body.Bytes(), &keyView); err != nil || keyView.BindingCount != "0" || keyView.DonationNote != "Scoped donor note" || keyView.EndpointKeyID != nil {
		t.Fatalf("unbound scoped key: %s; %v", view.Body, err)
	}
	for _, private := range []string{"Private physical endpoint note", "Private physical key note", "synthetic-envelope", "outside-scope"} {
		if strings.Contains(view.Body.String(), private) {
			t.Fatal("scoped key exposed", private)
		}
	}

	patchBody := fmt.Sprintf(`{"expected_revision":%q,"safe_note":"Scoped maintenance applied","calls_limit":"7"}`, keyView.DonationRevision)
	for _, wrong := range []string{"", "?charity_model_id=" + strconv.FormatInt(other, 10), "?charity_model_id=9223372036854775807"} {
		requireStatus(call(http.MethodGet, keyPath+wrong, "", "", false), http.StatusNotFound)
		requireStatus(call(http.MethodPatch, keyPath+wrong, patchBody, nextKey(), false), http.StatusNotFound)
	}
	foreignPath := "/api/steward/donations/" + customKey.id + "/keys/" + customKey.key + scope
	requireStatus(call(http.MethodGet, foreignPath, "", "", false), http.StatusNotFound)
	requireStatus(call(http.MethodPatch, foreignPath, fmt.Sprintf(`{"expected_revision":%q,"safe_note":"Must not apply"}`, customKey.revision), nextKey(), false), http.StatusNotFound)
	requireStatus(call(http.MethodGet, "/api/steward/donations/"+mainKey.id, "", "", false), http.StatusForbidden)

	patchKey := nextKey()
	patched := call(http.MethodPatch, keyPath+scope, patchBody, patchKey, false)
	requireStatus(patched, http.StatusOK)
	var receipt donation.ManagedKeyReceipt
	if err := json.Unmarshal(patched.Body.Bytes(), &receipt); err != nil || receipt.Key.SafeNote != "Scoped maintenance applied" || receipt.Key.Limits.Calls == nil || *receipt.Key.Limits.Calls != "7" {
		t.Fatalf("scoped configuration receipt: %s; %v", patched.Body, err)
	}
	replayed := call(http.MethodPatch, keyPath+scope, patchBody, patchKey, false)
	requireStatus(replayed, http.StatusOK)
	if replayed.Body.String() != patched.Body.String() {
		t.Fatal("authorized configuration replay changed its receipt")
	}

	bindingPath := modelPath + "/bindings/batch"
	bindingBody := fmt.Sprintf(`{"expected_binding_revision":"0","selections":[{"donation_key_id":%q,"upstream_model_id":"synthetic/maintained"}]}`, mainKey.key)
	customBody := strings.Replace(bindingBody, mainKey.key, customKey.key, 1)
	requireStatus(call(http.MethodPost, bindingPath, customBody, nextKey(), false), http.StatusNotFound)
	bindingKey := nextKey()
	bound := call(http.MethodPost, bindingPath, bindingBody, bindingKey, false)
	requireStatus(bound, http.StatusCreated)
	replayed = call(http.MethodPost, bindingPath, bindingBody, bindingKey, false)
	requireStatus(replayed, http.StatusCreated)
	if replayed.Body.String() != bound.Body.String() {
		t.Fatal("authorized binding replay changed its receipt")
	}

	snapshot := func() string {
		t.Helper()
		var revision, reviews, bindings int64
		var note string
		var limit []byte
		if err := f.store.DB().QueryRow(`SELECT d.revision,k.safe_note,k.call_limit_mag,
(SELECT count(*) FROM donation_reviews WHERE donation_id=d.id),
(SELECT count(*) FROM charity_model_bindings WHERE charity_model_id=?)
FROM donation_keys k JOIN donations d ON d.id=k.donation_id WHERE k.id=?`, model, mainKey.key).Scan(&revision, &note, &limit, &reviews, &bindings); err != nil {
			t.Fatal(err)
		}
		if bindings != 1 {
			t.Fatalf("binding count = %d, want 1", bindings)
		}
		return fmt.Sprintf("%d:%s:%x:%d:%d", revision, note, limit, reviews, bindings)
	}
	before := snapshot()
	denyReplays := func(status int) {
		t.Helper()
		requireStatus(call(http.MethodPatch, keyPath+scope, patchBody, patchKey, false), status)
		requireStatus(call(http.MethodPost, bindingPath, bindingBody, bindingKey, false), status)
		if got := snapshot(); got != before {
			t.Fatalf("denied replay changed facts: %q != %q", got, before)
		}
	}
	exec("UPDATE charity_models SET is_mainstream=0,revision=revision+1 WHERE id=?", model)
	denyReplays(http.StatusNotFound)
	requireStatus(call(http.MethodGet, modelPath+"/binding-candidates", "", "", false), http.StatusNotFound)

	exec("UPDATE charity_models SET is_mainstream=1,revision=revision+1 WHERE id=?", model)
	exec("UPDATE users SET level=4 WHERE id=?", f.userID)
	denyReplays(http.StatusForbidden)
	exec("UPDATE users SET level=5 WHERE id=?", f.userID)
	requireStatus(call(http.MethodGet, keyPath+scope, "", "", false), http.StatusOK)

	exec("DELETE FROM sessions WHERE user_id=?", f.userID)
	denyReplays(http.StatusUnauthorized)
}
