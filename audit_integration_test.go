package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/auth"
	"github.com/waiting-here/NonbiriAPI/internal/observability"
)

func TestAuditHTTPDispatchSourceAndPrivilegedReaders(t *testing.T) {
	f := newEmbeddingHTTPFixture(t)
	if _, err := f.store.DB().Exec(`INSERT INTO site_config(key,value,updated_at) VALUES('site_timezone_offset_minutes','0',0) ON CONFLICT(key) DO UPDATE SET value='0'`); err != nil { t.Fatal(err) }
	status, body := f.call(t, "provider/self", `"private input must not be logged"`, "float", "error")
	if status != http.StatusTooManyRequests {
		t.Fatalf("upstream failure: %d %s", status, body)
	}
	var requestID string
	if err := f.store.DB().QueryRow(`SELECT logical_request_id FROM request_logs ORDER BY id DESC LIMIT 1`).Scan(&requestID); err != nil {
		t.Fatal(err)
	}
	var kind, source string
	if err := f.store.DB().QueryRow(`SELECT kind,source_json FROM request_source_facts s JOIN request_logs l ON l.id=s.request_log_id WHERE l.logical_request_id=?`, requestID).Scan(&kind, &source); err != nil {
		t.Fatal(err)
	}
	if kind != "self" || !strings.Contains(source, "Go-http-client") || strings.Contains(source, "private input") {
		t.Fatalf("source projection: %s %s", kind, source)
	}
	login := testApplicationRequest(t, f.app.handler, "POST", auditAdminHost, "/admin/api/login", `{"username":"operator","password":"correct horse battery staple"}`, nil, map[string]string{"Content-Type": "application/json"})
	if login.Code != http.StatusOK {
		t.Fatalf("admin login: %d %s", login.Code, login.Body)
	}
	cookies := []*http.Cookie{responseCookieNamed(t, login, auth.AdminSessionCookieName)}
	path := "/admin/api/logs/" + requestID + "/attempts/1/errors/1"
	raw := testApplicationRequest(t, f.app.handler, "GET", auditAdminHost, path, "", cookies, nil)
	var original observability.ErrorBody
	if raw.Code != 200 || json.Unmarshal(raw.Body.Bytes(), &original) != nil || original.Body != `{"error":{"message":"Capacity exhausted","code":"busy"}}` {
		t.Fatalf("private raw capture: %d %s", raw.Code, raw.Body)
	}
	for _, path := range []string{
		"/admin/api/logs/" + requestID + "/source", "/admin/api/logs/diagnostic-capacity",
		"/admin/api/abuse-audit/users", "/admin/api/abuse-audit/access-summary", "/admin/api/economy-audit/summary",
	} {
		read := testApplicationRequest(t, f.app.handler, "GET", auditAdminHost, path, "", cookies, nil)
		if read.Code != 200 {
			t.Fatalf("management reader %s: %d %s", path, read.Code, read.Body)
		}
		denied := testApplicationRequest(t, f.app.handler, "GET", auditAdminHost, path, "", nil, map[string]string{"Authorization": "Bearer " + f.caller})
		if denied.Code != 401 {
			t.Fatalf("CallerKey opened administrator audit %s: %d", path, denied.Code)
		}
	}
	status, body = f.call(t, "[公益]provider/per_request", `"private input"`, "float", "")
	if status != 200 {
		t.Fatalf("charity dispatch: %d %s", status, body)
	}
	var count int
	if err := f.store.DB().QueryRow(`SELECT COUNT(*) FROM charity_request_outcomes WHERE result='success' AND dispatched=1`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("terminal outcome: %d %v", count, err)
	}
	// Only the upstream error response enters diagnostic storage. Accepted
	// request bodies remain ephemeral even when a failure is captured.
	if err := f.store.DB().QueryRow(`SELECT COUNT(*) FROM request_error_bodies WHERE instr(CAST(body AS TEXT),'private input')>0`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("request body was recorded: %d %v", count, err)
	}
}
