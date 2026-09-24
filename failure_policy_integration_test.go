package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/donation"
	"github.com/waiting-here/NonbiriAPI/internal/stewardautomation"
)

func TestStewardAutomationFailurePolicyAuthorityAndReplay(t *testing.T) {
	f := newAutomationFixture(t)
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
	patch := testApplicationRequest(t, f.handler, "PATCH", auditUserHost, stewardautomation.FailurePolicyPath, body, nil, headers)
	if patch.Code != 200 || json.Unmarshal(patch.Body.Bytes(), &policy) != nil || !policy.FailureDisabled {
		t.Fatal(patch.Code, patch.Body)
	}
	replay := testApplicationRequest(t, f.handler, "PATCH", auditUserHost, stewardautomation.FailurePolicyPath, body, nil, headers)
	if replay.Code != 200 || replay.Body.String() != patch.Body.String() {
		t.Fatal(replay.Code, replay.Body)
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
