package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/connectionstore/testseed"
	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
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
	appFixtureMainstreamChannel(t, f.store, channel, "Synthetic channel", "https://scope.example.test/v1", connectorcontract.TypeOpenAICompatible, f.now)

	type donated struct {
		id, revision, key string
		physical          int64
	}
	seedDonation := func(mainstream bool) donated {
		t.Helper()
		input := testseed.EndpointInput{OwnerUserID: f.userID, Type: connectorcontract.TypeOpenAICompatible, BaseURL: "https://custom.example.test/v1", Note: "Private physical endpoint note", At: f.now}
		if mainstream {
			revision, name, category := int64(1), "Synthetic channel", "subscription"
			input.BaseURL = "https://scope.example.test/v1"
			input.MainstreamChannelID, input.MainstreamChannelRevision = &channel, &revision
			input.MainstreamChannelName, input.MainstreamChannelCategory = &name, &category
		}
		endpoint := appFixtureEndpoint(t, f.store, input)
		base := "https://scope.example.test/v1"
		if !mainstream {
			base = "https://custom.example.test/v1"
		}
		contextID, envelope := sealRootTestCredential(t)
		secretID := exec(`INSERT INTO endpoint_key_secrets(context_id,canonical_base_url,connector_type,encrypted_secret,created_at)
VALUES(?,?,'openai-compatible',?,?)`, contextID, base, envelope, f.now)
		physical := appFixtureKey(t, f.store, testseed.KeyInput{EndpointID: endpoint, SecretRefID: secretID, Note: "Private physical key note", At: f.now}).KeyID
		appFixtureManualModel(t, f.store, physical, "synthetic/maintained", f.now)
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
		catalogPath := "/admin/api/donations/" + value.ID + "/keys/" + value.Keys[0].ID + "/models"
		catalog := call(http.MethodGet, catalogPath, "", "", true)
		requireStatus(catalog, http.StatusOK)
		var material struct {
			ManualRevision string `json:"manual_catalog_revision"`
		}
		if err := json.Unmarshal(catalog.Body.Bytes(), &material); err != nil {
			t.Fatal(err)
		}
		manual := call(http.MethodPost, catalogPath+"/manual", fmt.Sprintf(`{"expected_manual_catalog_revision":%q,"entries":["synthetic/maintained"]}`, material.ManualRevision), nextKey(), true)
		requireStatus(manual, http.StatusOK)
		return donated{id: value.ID, revision: value.Revision, key: value.Keys[0].ID, physical: physical}
	}
	mainKey, customKey := seedDonation(true), seedDonation(false)
	model := exec(`INSERT INTO charity_models(provider,model,full_name,enabled,pricing_mode,is_mainstream,created_at,updated_at)
VALUES('synthetic','managed','[公益]synthetic/managed',1,'per_request',1,?,?)`, f.now, f.now)
	other := exec(`INSERT INTO charity_models(provider,model,full_name,enabled,pricing_mode,is_mainstream,created_at,updated_at)
VALUES('synthetic','outside-scope','[公益]synthetic/outside-scope',1,'per_request',0,?,?)`, f.now, f.now)
	for _, id := range []int64{model, other} {
		exec(`INSERT INTO charity_model_access(model_id,allowed_level_mask) VALUES(?,63)`, id)
		exec(`INSERT INTO charity_routing_settings(model_id,revision,affinity_ttl_seconds) VALUES(?,1,300)`, id)
	}
	appFixtureBinding(t, f.store, testseed.BindingInput{ModelID: other, KeyID: mustAppFixtureID(t, customKey.key), UpstreamModelID: "synthetic/maintained", At: f.now}, true)
	exec("UPDATE users SET level=5 WHERE id=?", f.userID)
	queue := call(http.MethodGet, "/api/steward/donation-handling?page=1&page_size=10", "", "", false)
	requireStatus(queue, http.StatusOK)
	var handlingQueue donation.Page[donation.HandlingSummary]
	if err := json.Unmarshal(queue.Body.Bytes(), &handlingQueue); err != nil || handlingQueue.Pagination.TotalItems != "2" || len(handlingQueue.Data) != 2 {
		t.Fatalf("approved handling queue: %s %v", queue.Body, err)
	}
	for _, private := range []string{"Scoped donor note", "Scoped approval note", "Private physical", "https://", "endpoint_key_id", "reviewer", "owner", "display_head"} {
		if strings.Contains(queue.Body.String(), private) {
			t.Fatal("minimal handling queue disclosed", private)
		}
	}
	processing := map[string]string{}
	for _, item := range []donated{mainKey, customKey} {
		path := "/api/steward/donations/" + item.id + "/handling/processed"
		key := nextKey()
		processed := call(http.MethodPost, path, `{"expected_handling_revision":"1"}`, key, false)
		requireStatus(processed, http.StatusOK)
		var receipt donation.HandlingReceipt
		if err := json.Unmarshal(processed.Body.Bytes(), &receipt); err != nil || receipt.Handling.State != "processed" || receipt.Handling.ProcessedByRole == nil || *receipt.Handling.ProcessedByRole != "steward" {
			t.Fatalf("whole approved processing: %s %v", processed.Body, err)
		}
		processing[path] = key
	}
	checkProcessingReplay := func(status int) {
		t.Helper()
		for path, key := range processing {
			requireStatus(call(http.MethodPost, path, `{"expected_handling_revision":"1"}`, key, false), status)
		}
	}
	checkProcessingReplay(http.StatusOK)
	processedQueue := call(http.MethodGet, "/api/steward/donation-handling?handling=processed&donation_id="+customKey.id, "", "", false)
	requireStatus(processedQueue, http.StatusOK)
	if err := json.Unmarshal(processedQueue.Body.Bytes(), &handlingQueue); err != nil || handlingQueue.Pagination.TotalItems != "1" || handlingQueue.Data[0].DonationID != customKey.id {
		t.Fatalf("exact processed queue: %s %v", processedQueue.Body, err)
	}
	requireStatus(call(http.MethodPost, "/api/steward/donations/"+customKey.id+"/review", fmt.Sprintf(`{"decision":"force_reject","expected_revision":%q,"reason":"Must remain forbidden"}`, customKey.revision), nextKey(), false), http.StatusForbidden)

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

	if keyView.KeyLimitsRevision == nil || *keyView.KeyLimitsRevision != "1" {
		t.Fatalf("physical revision missing from scoped view: %s", view.Body)
	}
	patchBody := fmt.Sprintf(`{"expected_revision":%q,"expected_key_limits_revision":%q,"max_concurrency":3,"max_rpm":120,"safe_note":"Scoped maintenance applied","calls_limit":"7"}`, keyView.DonationRevision, *keyView.KeyLimitsRevision)
	for _, wrong := range []string{"", "?charity_model_id=" + strconv.FormatInt(other, 10), "?charity_model_id=9223372036854775807"} {
		status := http.StatusNotFound
		if wrong == "" {
			status = http.StatusForbidden
		}
		requireStatus(call(http.MethodGet, keyPath+wrong, "", "", false), status)
		requireStatus(call(http.MethodPatch, keyPath+wrong, patchBody, nextKey(), false), status)
	}
	foreignPath := "/api/steward/donations/" + customKey.id + "/keys/" + customKey.key + scope
	requireStatus(call(http.MethodGet, foreignPath, "", "", false), http.StatusNotFound)
	requireStatus(call(http.MethodPatch, foreignPath, fmt.Sprintf(`{"expected_revision":%q,"safe_note":"Must not apply"}`, customKey.revision), nextKey(), false), http.StatusNotFound)
	requireStatus(call(http.MethodGet, "/api/steward/donations/"+mainKey.id, "", "", false), http.StatusForbidden)

	patchKey := nextKey()
	patched := call(http.MethodPatch, keyPath+scope, patchBody, patchKey, false)
	requireStatus(patched, http.StatusOK)
	var receipt donation.ManagedKeyReceipt
	if err := json.Unmarshal(patched.Body.Bytes(), &receipt); err != nil || receipt.Key.SafeNote != "Scoped maintenance applied" || receipt.Key.Limits.Calls == nil || *receipt.Key.Limits.Calls != "7" || receipt.Key.KeyLimitsRevision == nil || *receipt.Key.KeyLimitsRevision != "2" || *receipt.Key.MaxConcurrency != 3 || *receipt.Key.MaxRPM != 120 || receipt.Key.EndpointKeyID != nil {
		t.Fatalf("scoped configuration receipt: %s; %v", patched.Body, err)
	}
	replayed := call(http.MethodPatch, keyPath+scope, patchBody, patchKey, false)
	requireStatus(replayed, http.StatusOK)
	if replayed.Body.String() != patched.Body.String() {
		t.Fatal("authorized configuration replay changed its receipt")
	}

	bindingPath := modelPath + "/bindings/batch-receipt"
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
		var revision, reviews, bindings, physicalRevision, concurrency, rpm int64
		var note string
		var limit []byte
		if err := f.store.DB().QueryRow(`SELECT d.revision,k.safe_note,k.call_limit_mag,
(SELECT count(*) FROM donation_reviews WHERE donation_id=d.id),
(SELECT count(*) FROM charity_model_bindings WHERE charity_model_id=?),physical.revision,l.max_concurrency,l.max_rpm
FROM donation_keys k JOIN donations d ON d.id=k.donation_id
JOIN endpoint_keys physical ON physical.id=k.endpoint_key_id JOIN endpoint_key_limits l ON l.endpoint_key_id=physical.id
WHERE k.id=?`, model, mainKey.key).Scan(&revision, &note, &limit, &reviews, &bindings, &physicalRevision, &concurrency, &rpm); err != nil {
			t.Fatal(err)
		}
		if bindings != 1 {
			t.Fatalf("binding count = %d, want 1", bindings)
		}
		return fmt.Sprintf("%d:%s:%x:%d:%d:%d:%d:%d", revision, note, limit, reviews, bindings, physicalRevision, concurrency, rpm)
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
	checkProcessingReplay(http.StatusOK)
	requireStatus(call(http.MethodGet, modelPath+"/binding-candidates", "", "", false), http.StatusNotFound)

	exec("UPDATE charity_models SET is_mainstream=1,revision=revision+1 WHERE id=?", model)
	exec("UPDATE users SET level=4 WHERE id=?", f.userID)
	denyReplays(http.StatusForbidden)
	checkProcessingReplay(http.StatusForbidden)
	requireStatus(call(http.MethodGet, "/api/steward/donation-handling", "", "", false), http.StatusForbidden)
	exec("UPDATE users SET level=5 WHERE id=?", f.userID)
	requireStatus(call(http.MethodGet, keyPath+scope, "", "", false), http.StatusOK)

	exec("DELETE FROM sessions WHERE user_id=?", f.userID)
	denyReplays(http.StatusUnauthorized)
	checkProcessingReplay(http.StatusUnauthorized)
	requireStatus(call(http.MethodGet, "/api/steward/donation-handling", "", "", false), http.StatusUnauthorized)
}
