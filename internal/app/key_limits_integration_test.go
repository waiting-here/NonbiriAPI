package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/claim"
	"github.com/waiting-here/NonbiriAPI/internal/connectionstore/testseed"
	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/donation"
	"github.com/waiting-here/NonbiriAPI/internal/forward"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
	"github.com/waiting-here/NonbiriAPI/internal/routing"
)

func TestManagedSharedKeyLimitsOwnerCASAndAdmission(t *testing.T) {
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
	call := func(method, path, body, key string, admin bool) *httptest.ResponseRecorder {
		t.Helper()
		host, cookies := auditUserHost, f.cookies
		if admin {
			host, cookies = auditAdminHost, f.adminCookies
		}
		headers := map[string]string{"Content-Type": "application/json", "Origin": "http://" + host}
		if key != "" {
			headers["Idempotency-Key"] = key
		}
		return testApplicationRequest(t, f.app.handler, method, host, path, body, cookies, headers)
	}
	requireStatus := func(response *httptest.ResponseRecorder, status int) {
		t.Helper()
		if response.Code != status {
			t.Fatalf("HTTP %d, want %d: %s", response.Code, status, response.Body)
		}
	}
	exec(`UPDATE site_config SET value='1' WHERE key IN ('donation_accept_enabled','charity_enabled')`)
	endpoint := appFixtureEndpoint(t, f.store, testseed.EndpointInput{OwnerUserID: f.userID, Type: connectorcontract.TypeOpenAICompatible, BaseURL: "https://shared.example.test/v1", Note: "Private endpoint note", At: f.now})
	contextID, envelope := sealRootTestCredential(t)
	secretID := exec(`INSERT INTO endpoint_key_secrets(context_id,canonical_base_url,connector_type,encrypted_secret,created_at)
VALUES(?,'https://shared.example.test/v1','openai-compatible',?,?)`, contextID, envelope, f.now)
	identity := appFixtureKey(t, f.store, testseed.KeyInput{EndpointID: endpoint, SecretRefID: secretID, Note: "Private key note", At: f.now})
	physical := identity.KeyID
	appFixtureManualModel(t, f.store, physical, "upstream", f.now)
	created := call(http.MethodPost, "/api/donations", fmt.Sprintf(`{"description":"Shared limit test","keys":[{"endpoint_key_id":"%d","expires_at":null}],"ownership_authorized":true,"discord_public_thanks":false}`, physical), "shared-limits-create-0001", false)
	requireStatus(created, http.StatusCreated)
	var d donation.Donation
	if err := json.Unmarshal(created.Body.Bytes(), &d); err != nil || len(d.Keys) != 1 {
		t.Fatalf("donation: %s %v", created.Body, err)
	}
	review := call(http.MethodPost, "/admin/api/donations/"+d.ID+"/review", fmt.Sprintf(`{"decision":"approve","expected_revision":%q,"reason":"Accepted","key_settings":[{"donation_key_id":%q,"enabled":true,"price_limit":null,"calls_limit":null,"tokens_limit":null,"token_reserve":0,"safe_note":"","expires_at":null}]}`, d.Revision, d.Keys[0].ID), "shared-limits-review-0001", true)
	requireStatus(review, http.StatusOK)
	// Level-six callers may use their own donated key.
	exec(`UPDATE users SET level=6 WHERE id=?`, f.userID)
	model := exec(`INSERT INTO charity_models(provider,model,full_name,enabled,pricing_mode,created_at,updated_at)
VALUES('synthetic','shared','[公益]synthetic/shared',1,'per_request',?,?)`, f.now, f.now)
	exec(`INSERT INTO charity_model_access(model_id,allowed_level_mask) VALUES(?,63)`, model)
	exec(`INSERT INTO charity_routing_settings(model_id,revision,affinity_ttl_seconds) VALUES(?,1,300)`, model)
	appFixtureDonationManualModel(t, f.store, mustAppFixtureID(t, d.Keys[0].ID), "upstream", f.now)
	charityTarget := appFixtureBinding(t, f.store, testseed.BindingInput{ModelID: model, KeyID: mustAppFixtureID(t, d.Keys[0].ID), UpstreamModelID: "upstream", At: f.now}, true)
	ownerPath := fmt.Sprintf("/api/endpoints/%d/keys/%d", endpoint, physical)
	managedPath := "/admin/api/donations/" + d.ID + "/keys/" + d.Keys[0].ID
	requireStatus(call(http.MethodPatch, ownerPath, `{"expected_revision":"1","max_rpm":10}`, "shared-limits-owner-0001", false), http.StatusOK)
	stale := `{"expected_revision":"2","expected_key_limits_revision":"1","max_concurrency":1}`
	requireStatus(call(http.MethodPatch, managedPath, stale, "shared-limits-stale-0001", true), http.StatusConflict)

	personalModel := exec(`INSERT INTO models(user_id,provider,model,full_name,route_strategy,model_types,created_at,updated_at) VALUES(?,'synthetic','shared','synthetic/shared','ordered',1,?,?)`, f.userID, f.now, f.now)
	personalTarget := appFixtureBinding(t, f.store, testseed.BindingInput{ModelID: personalModel, KeyID: physical, UpstreamModelID: "upstream", At: f.now}, false)
	authorization := routing.TargetAuthorization{BindingID: personalTarget.BindingID, TargetID: personalTarget.TargetID, TargetRevision: 1, CredentialID: identity.CredentialID, CredentialVersionID: identity.CredentialVersionID, CredentialVersion: 1, GrantRevisionID: identity.GrantRevisionID, GrantRevision: 1, ConfiguredType: connectorcontract.TypeOpenAICompatible, NativeService: connectorcontract.NativeServiceChatCompletions}
	charityAuthorization := authorization
	charityAuthorization.BindingID, charityAuthorization.TargetID = charityTarget.BindingID, charityTarget.TargetID
	if err := f.store.DB().QueryRow(`SELECT u.id,r.id,r.use_revision FROM donation_uses u JOIN donation_use_revisions r ON r.id=u.current_approved_revision_id WHERE u.donation_key_id=? AND u.native_service_bit=1`, d.Keys[0].ID).Scan(&charityAuthorization.UseID, &charityAuthorization.UseRevisionID, &charityAuthorization.UseRevision); err != nil {
		t.Fatal(err)
	}
	candidate := claim.Candidate{Authorization: authorization, EndpointID: endpoint, EndpointKeyID: physical, ConnectorType: "openai-compatible", CanonicalBaseURL: "https://shared.example.test/v1", UpstreamModelID: "upstream"}
	callerMiddleware, err := forward.NewCallerKeyMiddleware(f.app.resourceRepo, f.app.forward.lifecycle)
	if err != nil {
		t.Fatal(err)
	}
	accept := func(charity bool) claim.ClaimInput {
		t.Helper()
		input := claim.AcceptInput{UserID: f.userID, Route: claim.RouteOpenAIChat, ModelSnapshot: "synthetic/shared", AttemptLimit: 1}
		purpose := claim.PurposeSelf
		if charity {
			at := time.Now().Unix()
			input.Route, input.ModelSnapshot, input.CharityModelID, input.CharityDecisionNow = claim.RouteCharityChat, "[公益]synthetic/shared", model, &at
			purpose = claim.PurposeCharity
		}
		var request claim.Request
		var acceptErr error
		handler := callerMiddleware.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			request, acceptErr = f.app.claims.Accept(r.Context(), input)
			w.WriteHeader(http.StatusNoContent)
		}))
		wire := httptest.NewRequest(http.MethodPost, "http://"+auditUserHost+"/v1/chat/completions", nil)
		wire.Header.Set("Authorization", "Bearer nbk_"+base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x4a}, 32)))
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, wire)
		requireStatus(response, http.StatusNoContent)
		if acceptErr != nil {
			t.Fatal(acceptErr)
		}
		selected, personal := candidate, personalModel
		var logicalKey int64
		if charity {
			selected.Authorization, personal = charityAuthorization, 0
			if err := f.store.DB().QueryRow(`SELECT id FROM donation_keys WHERE donation_id=?`, d.ID).Scan(&logicalKey); err != nil {
				t.Fatal(err)
			}
		}
		return claim.ClaimInput{RequestID: request.ID, ActorUserID: f.userID, AttemptSeq: 1, Purpose: purpose, Candidate: selected, DonationKeyID: logicalKey, PersonalModelID: personal}
	}
	dispatch := func(handle claim.Handle) {
		t.Helper()
		grant, err := f.app.claims.TakeForDispatch(context.Background(), handle)
		if err != nil {
			t.Fatal(err)
		}
		grant.Clear()
	}
	complete := func(handle claim.Handle) {
		t.Helper()
		if _, err := f.app.claims.CompleteAttempt(context.Background(), handle, claim.AttemptOutcome{Kind: claim.ResultSynthetic, UpstreamStatus: 502, Diagnostic: "synthetic completion"}); err != nil {
			t.Fatal(err)
		}
	}
	old, err := f.app.claims.Claim(context.Background(), accept(false))
	if err != nil {
		t.Fatal(err)
	}
	dispatch(old)
	body := `{"expected_revision":"2","expected_key_limits_revision":"2","max_concurrency":1,"max_rpm":3}`
	updated := call(http.MethodPatch, managedPath, body, "shared-limits-manage-0001", true)
	requireStatus(updated, http.StatusOK)
	var receipt donation.AdminDonation
	if err := json.Unmarshal(updated.Body.Bytes(), &receipt); err != nil || receipt.Revision != "3" || *receipt.Keys[0].KeyLimitsRevision != "3" {
		t.Fatalf("managed receipt: %s %v", updated.Body, err)
	}
	selfInput, charityInput := accept(false), accept(true)
	for _, input := range []claim.ClaimInput{selfInput, charityInput} {
		if _, err := f.app.claims.Claim(context.Background(), input); !errors.Is(err, claim.ErrKeyRateLimited) {
			t.Fatal("managed limit did not constrain shared key", input.Purpose, err)
		}
	}
	complete(old)
	for _, input := range []claim.ClaimInput{selfInput, charityInput} {
		handle, err := f.app.claims.Claim(context.Background(), input)
		if err != nil {
			t.Fatal("old admitted claim did not release capacity", input.Purpose, err)
		}
		dispatch(handle)
		complete(handle)
	}
	for _, input := range []claim.ClaimInput{accept(false), accept(true)} {
		if _, err := f.app.claims.Claim(context.Background(), input); !errors.Is(err, claim.ErrKeyRateLimited) {
			t.Fatal("managed RPM did not retain completed dispatches", input.Purpose, err)
		}
	}
	requireStatus(call(http.MethodPatch, ownerPath, `{"expected_revision":"2","max_rpm":5}`, "shared-limits-owner-0002", false), http.StatusConflict)
	requireStatus(call(http.MethodPatch, ownerPath, `{"expected_revision":"3","max_rpm":5}`, "shared-limits-owner-0003", false), http.StatusOK)
	replayed := call(http.MethodPatch, managedPath, body, "shared-limits-manage-0001", true)
	requireStatus(replayed, http.StatusOK)
	if replayed.Body.String() != updated.Body.String() {
		t.Fatal("authorized replay changed its receipt")
	}
	view := call(http.MethodGet, fmt.Sprintf("/api/endpoints/%d/keys?page=1&page_size=20", endpoint), "", "", false)
	requireStatus(view, http.StatusOK)
	var ownerPage struct {
		Data []resources.EndpointKey `json:"data"`
	}
	if err := json.Unmarshal(view.Body.Bytes(), &ownerPage); err != nil || len(ownerPage.Data) != 1 {
		t.Fatalf("owner key page: %s %v", view.Body, err)
	}
	owner := ownerPage.Data[0]
	if owner.Revision != "4" || owner.MaxConcurrency != 1 || owner.MaxRPM != 5 || owner.Note != "Private key note" || !owner.Enabled || owner.ForceStoreFalse {
		t.Fatalf("managed write or replay overwrote owner fields: %s", view.Body)
	}
	stewardPath := "/api/steward/donations/" + d.ID + "/keys/" + d.Keys[0].ID
	steward := call(http.MethodPatch, stewardPath, `{"expected_revision":"3","expected_key_limits_revision":"4","max_concurrency":0}`, "shared-limits-steward-001", false)
	requireStatus(steward, http.StatusOK)
	if err := json.Unmarshal(steward.Body.Bytes(), &receipt); err != nil || receipt.Revision != "4" || *receipt.Keys[0].KeyLimitsRevision != "5" || *receipt.Keys[0].MaxConcurrency != 0 || *receipt.Keys[0].MaxRPM != 5 {
		t.Fatalf("level-six shared limit receipt: %s %v", steward.Body, err)
	}
	requireStatus(call(http.MethodDelete, ownerPath, `{"expected_revision":"5"}`, "shared-limits-delete-0001", false), http.StatusNoContent)
	deleted := call(http.MethodGet, "/admin/api/donations/"+d.ID, "", "", true)
	requireStatus(deleted, http.StatusOK)
	if err := json.Unmarshal(deleted.Body.Bytes(), &receipt); err != nil || receipt.Keys[0].KeyLimitsRevision != nil || receipt.Keys[0].MaxConcurrency != nil || receipt.Keys[0].MaxRPM != nil {
		t.Fatalf("deleted physical key retains editable limits: %s %v", deleted.Body, err)
	}
	requireStatus(call(http.MethodPatch, managedPath, fmt.Sprintf(`{"expected_revision":%q,"expected_key_limits_revision":"5","max_rpm":1}`, receipt.Revision), "shared-limits-ended-0001", true), http.StatusConflict)
}
