package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/connectionstore/testseed"
	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/donation"
)

func TestTraineePendingReviewWithRealSession(t *testing.T) {
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
	call := func(method, path, body, key string) *httptest.ResponseRecorder {
		t.Helper()
		headers := map[string]string{"Content-Type": "application/json", "Origin": "http://" + auditUserHost}
		if key != "" {
			headers["Idempotency-Key"] = key
		}
		return testApplicationRequest(t, f.app.handler, method, auditUserHost, path, body, f.cookies, headers)
	}
	require := func(r *httptest.ResponseRecorder, status int) {
		t.Helper()
		if r.Code != status {
			t.Fatalf("status %d, want %d: %s", r.Code, status, r.Body)
		}
	}
	exec(`UPDATE site_config SET value='1' WHERE key IN ('donation_accept_enabled','charity_enabled')`)
	for index, decision := range []string{"approve", "reject"} {
		exec(`UPDATE users SET level=1 WHERE id=?`, f.userID)
		base := fmt.Sprintf("https://pending-%d.example.test/v1", index)
		endpoint := appFixtureEndpoint(t, f.store, testseed.EndpointInput{OwnerUserID: f.userID, Type: connectorcontract.TypeOpenAICompatible, BaseURL: base, Note: "", At: f.now})
		contextID, envelope := sealRootTestCredential(t)
		secretID := exec(`INSERT INTO endpoint_key_secrets(context_id,canonical_base_url,connector_type,encrypted_secret,created_at) VALUES(?,?,'openai-compatible',?,?)`, contextID, base, envelope, f.now)
		physical := appFixtureKey(t, f.store, testseed.KeyInput{EndpointID: endpoint, SecretRefID: secretID, Note: "", At: f.now}).KeyID
		created := call(http.MethodPost, "/api/donations", fmt.Sprintf(`{"description":"Pending review","keys":[{"endpoint_key_id":"%d","expires_at":null}],"ownership_authorized":true,"discord_public_thanks":false}`, physical), fmt.Sprintf("pending-create-%016d", index))
		require(created, http.StatusCreated)
		var d donation.Donation
		if err := json.Unmarshal(created.Body.Bytes(), &d); err != nil || d.Status != "pending" {
			t.Fatalf("pending donation: %+v %v", d, err)
		}
		path := "/api/steward/donations/" + d.ID
		require(call(http.MethodGet, path, "", ""), http.StatusForbidden)
		exec(`UPDATE users SET level=5 WHERE id=?`, f.userID)
		queue := call(http.MethodGet, "/api/steward/donations?page=1&page_size=10", "", "")
		require(queue, http.StatusOK)
		var page donation.Page[donation.DonationSummary]
		if err := json.Unmarshal(queue.Body.Bytes(), &page); err != nil || len(page.Data) != 1 || page.Pagination.TotalItems != "1" {
			t.Fatalf("pending page: %s %v", queue.Body, err)
		}
		detail := call(http.MethodGet, path, "", "")
		require(detail, http.StatusOK)
		var value donation.StewardDonation
		if err := json.Unmarshal(detail.Body.Bytes(), &value); err != nil || value.Owner != nil || value.Reviewer != nil || value.Keys[0].EndpointKeyID != nil {
			t.Fatalf("redaction: %s %v", detail.Body, err)
		}
		body := fmt.Sprintf(`{"decision":%q,"expected_revision":%q,"reason":"Reviewed"}`, decision, d.Revision)
		if decision == "approve" {
			body = fmt.Sprintf(`{"decision":"approve","expected_revision":%q,"reason":"Reviewed","key_settings":[{"donation_key_id":%q,"enabled":true,"price_limit":null,"calls_limit":null,"tokens_limit":null,"token_reserve":0,"safe_note":"","expires_at":null}]}`, d.Revision, d.Keys[0].ID)
		}
		key := fmt.Sprintf("pending-review-%016d", index)
		saved := call(http.MethodPost, path+"/review", body, key)
		require(saved, http.StatusOK)
		replayed := call(http.MethodPost, path+"/review", body, key)
		require(replayed, http.StatusOK)
		if replayed.Body.String() != saved.Body.String() {
			t.Fatal("review replay changed")
		}
		require(call(http.MethodGet, path, "", ""), http.StatusForbidden)
		require(call(http.MethodGet, "/api/steward/donations?status=approved", "", ""), http.StatusForbidden)
		exec(`UPDATE users SET level=6 WHERE id=?`, f.userID)
		full := call(http.MethodGet, path, "", "")
		require(full, http.StatusOK)
		if err := json.Unmarshal(full.Body.Bytes(), &value); err != nil || value.Reviewer == nil || value.Reviewer.Role != "steward" {
			t.Fatalf("public reviewer role: %s %v", full.Body, err)
		}
		exec(`UPDATE users SET level=4 WHERE id=?`, f.userID)
		require(call(http.MethodPost, path+"/review", body, key), http.StatusForbidden)
	}
	exec(`UPDATE users SET level=5 WHERE id=?`, f.userID)
	exec(`DELETE FROM sessions WHERE user_id=?`, f.userID)
	require(call(http.MethodGet, "/api/steward/donations", "", ""), http.StatusUnauthorized)
}
