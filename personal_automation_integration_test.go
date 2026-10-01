package main

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestPersonalAutomationProductionIngressAndRecovery(t *testing.T) {
	f := newAutomationFixture(t)
	f.exec(t, `UPDATE users SET level=5 WHERE id=?`, f.userID)
	now := time.Now().Unix()
	endpoint := f.exec(t, `INSERT INTO endpoints(user_id,connector_type,base_url,note,enabled,revision,created_at,updated_at) VALUES(?,'openai-compatible','https://synthetic.invalid/v1','owner endpoint',1,1,?,?)`, f.userID, now, now)
	path := fmt.Sprintf("/api/automation/endpoints/%d/keys/batch-import", endpoint)
	body := `{"ownership_confirmed":true,"keys":[{"secret":"root-personal-credential"}]}`
	for _, test := range []struct {
		method, host, path, caller, body string
		want                             int
	}{
		{"GET", auditUserHost, "/api/automation/endpoints", f.caller, "", 200},
		{"GET", auditUserHost, "/api/steward/automation/donations", f.caller, "", 403},
		{"GET", auditUserHost, "/api/endpoints", f.caller, "", 401},
		{"GET", auditAdminHost, "/api/automation/endpoints", f.caller, "", 404},
		{"POST", auditUserHost, "/api/automation/endpoints", f.caller, "{}", 405},
		{"GET", auditUserHost, "/api/automation/%65ndpoints", f.caller, "", 404},
		{"GET", auditUserHost, "/api/automation/endpoints", "", "", 401},
		{"POST", auditUserHost, path, f.caller, body, 200},
	} {
		headers := map[string]string{"Authorization": "Bearer " + test.caller, "Content-Type": "application/json"}
		if test.method == http.MethodPost {
			headers["Idempotency-Key"] = strings.Repeat("R", 22)
		}
		out := testApplicationRequest(t, f.app.handler, test.method, test.host, test.path, test.body, nil, headers)
		if out.Code != test.want {
			t.Fatalf("%s %s at %s: %d %s", test.method, test.path, test.host, out.Code, out.Body.String())
		}
		if strings.Contains(out.Body.String(), "root-personal-credential") {
			t.Fatal("CallerKey resource projection exposed the upstream credential")
		}
	}
	f.exec(t, `UPDATE users SET level=6 WHERE id=?`, f.userID)
	get := func(path string, want int) {
		t.Helper()
		out := testApplicationRequest(t, f.app.handler, http.MethodGet, auditUserHost, path, "", nil, map[string]string{"Authorization": "Bearer " + f.caller})
		if out.Code != want {
			t.Fatalf("GET %s: %d %s", path, out.Code, out.Body.String())
		}
	}
	get("/api/steward/automation/donations", 200)
	f.exec(t, `UPDATE users SET is_banned=1,banned_until=NULL WHERE id=?`, f.userID)
	get("/api/automation/endpoints", 401)
	f.exec(t, `UPDATE users SET is_banned=0 WHERE id=?`, f.userID)
	f.exec(t, `UPDATE caller_keys SET generation=generation+1,key_hash=zeroblob(32),display_head='newh',display_tail='newt' WHERE user_id=?`, f.userID)
	get("/api/automation/endpoints", 401)
	if err := f.app.lifecycle.RecoverBeforeListenerAt(context.Background(), now+86402); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"personal_automation_batches", "personal_automation_steps"} {
		if f.count(t, table) != 0 {
			t.Fatalf("startup recovery retained expired %s", table)
		}
	}
	if f.count(t, "endpoint_keys") != 1 {
		t.Fatal("receipt expiration removed the imported credential")
	}
}
