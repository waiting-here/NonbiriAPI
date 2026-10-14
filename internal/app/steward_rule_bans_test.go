package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/riskaudit"
)

func TestStewardRuleBanHTTPAuthority(t *testing.T) {
	f := newGameWireFixture(t)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := f.store.DB().Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	const path = "/api/steward/abuse-audit/client-rules"
	const body = `{"name":"Client restriction","status":"suspected","enabled":true,"conditions":[{"field":"user_agent","operator":"prefix","value":"Restricted/"}],"auto_ban":{"enabled":true,"duration_seconds":3600}}`
	headers := map[string]string{"Content-Type": "application/json", "Origin": "http://" + auditUserHost}
	for _, level := range []int{1, 5, 6} {
		exec("UPDATE users SET level=? WHERE id=?", level, f.userID)
		response := testApplicationRequest(t, f.app.handler, http.MethodPost, auditUserHost, path, body, f.cookies, headers)
		want := http.StatusForbidden
		if level == 6 {
			want = http.StatusCreated
		}
		if response.Code != want {
			t.Fatalf("level %d: %d %s", level, response.Code, response.Body)
		}
	}
	for _, tc := range []struct {
		name    string
		cookies []*http.Cookie
		headers map[string]string
		status  int
	}{
		{"anonymous", nil, headers, http.StatusUnauthorized},
		{"caller key", nil, map[string]string{"Content-Type": "application/json", "Origin": "http://" + auditUserHost, "Authorization": "Bearer test-key"}, http.StatusUnauthorized},
		{"cross origin", f.cookies, map[string]string{"Content-Type": "application/json", "Origin": "https://foreign.example"}, http.StatusForbidden},
	} {
		response := testApplicationRequest(t, f.app.handler, http.MethodPost, auditUserHost, path, body, tc.cookies, tc.headers)
		if response.Code != tc.status {
			t.Fatalf("%s: %d %s", tc.name, response.Code, response.Body)
		}
	}
	response := testApplicationRequest(t, f.app.handler, http.MethodGet, auditUserHost, path, "", f.cookies, nil)
	var list struct {
		Items []riskaudit.Rule `json:"items"`
	}
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &list) != nil || len(list.Items) != 1 {
		t.Fatalf("rules: %d %s", response.Code, response.Body)
	}
	rule := list.Items[0]
	if rule.AutoBan == nil || !rule.AutoBan.Enabled || rule.UpdatedByRole != "level6" {
		t.Fatalf("stored binding: %+v", rule)
	}
	update := fmt.Sprintf(`{"name":"Updated","status":"confirmed","enabled":true,"revision":%d,"conditions":[{"field":"user_agent","operator":"prefix","value":"Restricted/"}],"auto_ban":null}`, rule.Revision)
	exec("UPDATE users SET level=5 WHERE id=?", f.userID)
	response = testApplicationRequest(t, f.app.handler, http.MethodPatch, auditUserHost, path+"/"+rule.ID, update, f.cookies, headers)
	if response.Code != 403 {
		t.Fatalf("demoted write: %d %s", response.Code, response.Body)
	}
	exec("UPDATE users SET level=6 WHERE id=?", f.userID)
	response = testApplicationRequest(t, f.app.handler, http.MethodPatch, auditUserHost, path+"/"+rule.ID, update, f.cookies, headers)
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &rule) != nil || rule.AutoBan != nil || rule.Revision != 2 {
		t.Fatalf("unbind: %d %s", response.Code, response.Body)
	}
	response = testApplicationRequest(t, f.app.handler, http.MethodDelete, auditUserHost, fmt.Sprintf("%s/%s?revision=%d", path, rule.ID, rule.Revision), "", f.cookies, headers)
	if response.Code != 200 {
		t.Fatalf("delete: %d %s", response.Code, response.Body)
	}
}
