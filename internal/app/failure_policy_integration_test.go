package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/donation"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/stewardautomation"
)

func TestStewardAutomationFailurePolicyAuthorityAndReplay(t *testing.T) {
	f := newAutomationFixture(t)
	for _, field := range []string{"smart_backoff_profile", "smart_backoff_rules"} {
		old := f.input(1)
		old["keys"].([]map[string]any)[0][field] = nil
		rejected := f.call(t, stewardautomation.DonationsPath, strings.Repeat("R", 22), old)
		if rejected.Code != 400 {
			t.Fatal("retired create field accepted", field, rejected.Code, rejected.Body)
		}
	}
	input := f.input(1)
	input["keys"].([]map[string]any)[0]["failure_disable_threshold"] = "0"
	created := f.create(t, strings.Repeat("P", 22), input)
	key := created.Keys[0].DonationKeyID
	f.exec(t, "UPDATE donation_keys SET failure_streak=?,enabled=0 WHERE id=?", db.EncodeU128(db.U128{15: 12}), key)
	readPath := stewardautomation.FailurePolicyPath + "?donation_id=" + created.DonationID + "&donation_key_id=" + key
	headers := map[string]string{"Authorization": "Bearer " + f.caller, "Content-Type": "application/json"}
	read := testApplicationRequest(t, f.handler, "GET", auditUserHost, readPath, "", nil, headers)
	var policy donation.FailurePolicy
	if read.Code != 200 || json.Unmarshal(read.Body.Bytes(), &policy) != nil || policy.FailureDisableThreshold != "0" || policy.FailureStreak != "12" {
		t.Fatal(read.Code, read.Body)
	}
	body := `{"donation_id":"` + created.DonationID + `","donation_key_id":"` + key + `","expected_revision":"` + policy.Revision + `","failure_disable_threshold":"10"}`
	headers["Idempotency-Key"] = strings.Repeat("Q", 22)
	for _, extra := range []string{`,"smart_backoff_profile":"none"}`, `,"smart_backoff_rules":[]}`} {
		rejected := testApplicationRequest(t, f.handler, "PATCH", auditUserHost, stewardautomation.FailurePolicyPath, strings.TrimSuffix(body, "}")+extra, nil, headers)
		if rejected.Code != 400 {
			t.Fatal("retired policy field accepted", rejected.Code, rejected.Body)
		}
	}
	patch := testApplicationRequest(t, f.handler, "PATCH", auditUserHost, stewardautomation.FailurePolicyPath, body, nil, headers)
	if patch.Code != 200 || json.Unmarshal(patch.Body.Bytes(), &policy) != nil || !policy.FailureDisabled {
		t.Fatal(patch.Code, patch.Body)
	}
	var legacy map[string]any
	if err := json.Unmarshal(patch.Body.Bytes(), &legacy); err != nil {
		t.Fatal(err)
	}
	legacy["smart_backoff_profile"] = "day"
	legacy["smart_backoff_rules"] = []map[string]any{{"kind": "code", "value": "old.private-rule", "period": "auto"}}
	historical, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	receiptHash, err := idempotency.KeyHash(headers["Idempotency-Key"])
	if err != nil {
		t.Fatal(err)
	}
	// Recreate only this synthetic historical receipt; terminal rows stay immutable.
	f.exec(t, "INSERT OR REPLACE INTO idempotency_records(scope,actor_scope_hash,key_hash,request_hash,state,http_status,response_body,created_at,expires_at) SELECT scope,actor_scope_hash,key_hash,request_hash,state,http_status,?,created_at,expires_at FROM idempotency_records WHERE scope=? AND key_hash=?", historical, string(idempotency.ScopeControlMutation), receiptHash[:])
	var beforeAudits int
	if err := f.store.DB().QueryRow("SELECT count(*) FROM donation_reviews").Scan(&beforeAudits); err != nil {
		t.Fatal(err)
	}
	replay := testApplicationRequest(t, f.handler, "PATCH", auditUserHost, stewardautomation.FailurePolicyPath, body, nil, headers)
	if replay.Code != 200 || strings.Contains(replay.Body.String(), "smart_backoff_profile") || strings.Contains(replay.Body.String(), "smart_backoff_rules") {
		t.Fatal(replay.Code, replay.Body)
	}
	for _, field := range []string{"smart_backoff_profile", "smart_backoff_rules"} {
		if strings.Contains(read.Body.String(), field) || strings.Contains(patch.Body.String(), field) {
			t.Fatal("retired field returned", field)
		}
	}
	var replayPolicy donation.FailurePolicy
	strict := json.NewDecoder(bytes.NewReader(replay.Body.Bytes()))
	strict.DisallowUnknownFields()
	if err := strict.Decode(&replayPolicy); err != nil || replayPolicy.Revision != policy.Revision || replayPolicy.FailureDisableThreshold != policy.FailureDisableThreshold {
		t.Fatal(replayPolicy, err)
	}
	var afterAudits int
	var stored []byte
	var revision string
	if err := f.store.DB().QueryRow("SELECT response_body FROM idempotency_records WHERE scope=? AND key_hash=?", string(idempotency.ScopeControlMutation), receiptHash[:]).Scan(&stored); err != nil || !bytes.Equal(stored, historical) {
		t.Fatal("receipt rewritten", err)
	}
	if err := f.store.DB().QueryRow("SELECT CAST(revision AS TEXT),(SELECT count(*) FROM donation_reviews) FROM donations WHERE id=?", created.DonationID).Scan(&revision, &afterAudits); err != nil || revision != policy.Revision || beforeAudits != afterAudits {
		t.Fatal("replay applied a second mutation", revision, afterAudits, err)
	}
	if strings.Contains(patch.Body.String(), "base_url") || strings.Contains(patch.Body.String(), "secret") || strings.Contains(patch.Body.String(), "user_id") {
		t.Fatal("unsafe policy projection")
	}
	for _, method := range []string{"POST", "DELETE", "PUT"} {
		r := testApplicationRequest(t, f.handler, method, auditUserHost, stewardautomation.FailurePolicyPath, body, nil, headers)
		if r.Code != 405 {
			t.Fatal(method, r.Code)
		}
	}
	for _, path := range []string{stewardautomation.FailurePolicyPath + "/", strings.Replace(stewardautomation.FailurePolicyPath, "failure", "%66ailure", 1)} {
		r := testApplicationRequest(t, f.handler, "PATCH", auditUserHost, path, body, nil, headers)
		if r.Code != 404 {
			t.Fatal(path, r.Code)
		}
	}
	for _, blocked := range []struct {
		query string
		args  []any
	}{
		{"UPDATE users SET level=5 WHERE id=?", []any{f.userID}},
		{"UPDATE users SET level=4 WHERE id=?", []any{f.userID}},
		{"UPDATE users SET level=6,is_banned=1 WHERE id=?", []any{f.userID}},
		{"UPDATE users SET is_banned=0 WHERE id=?", []any{f.userID}},
	} {
		f.exec(t, blocked.query, blocked.args...)
		if strings.Contains(blocked.query, "is_banned=0") {
			continue
		}
		r := testApplicationRequest(t, f.handler, "PATCH", auditUserHost, stewardautomation.FailurePolicyPath, body, nil, headers)
		if r.Code != http.StatusForbidden && r.Code != http.StatusUnauthorized {
			t.Fatal("cached replay authority", r.Code, r.Body)
		}
	}
	f.exec(t, "UPDATE caller_keys SET generation=generation+1,key_hash=NULL,display_head='',display_tail='',key_created_at=NULL WHERE user_id=?", f.userID)
	revoked := testApplicationRequest(t, f.handler, "GET", auditUserHost, readPath, "", nil, headers)
	if revoked.Code != 401 {
		t.Fatal("revoked", revoked.Code)
	}
}
