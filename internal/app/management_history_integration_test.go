package app

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestManagementHistoryRoutesAndCleanupWorker(t *testing.T) {
	f := newActivityWireFixture(t)
	const base = "/admin/api/management-operations"
	profile := f.call("POST", "/admin/api/ai/drivers", map[string]any{
		"name": "Audit fixture", "api_key": agentFixtureSecret,
		"settings": map[string]any{"provider": "responses", "base_url": f.upstream.URL + "/agent/v1", "model": "fixture", "context_window": 32768, "output_reserve_tokens": 1024, "max_output_tokens": 0, "extra_parameters": map[string]any{}, "concurrency": 1, "timeout_seconds": 30},
	}, f.adminCookie, true)
	query := base + "?view=operations&target_kind=agent_driver&target_id=" + profile["id"].(string)
	page := f.call("GET", query, nil, f.adminCookie, true)
	operations := page["operations"].([]any)
	if page["total"] != "1" || len(operations) != 1 {
		t.Fatal(page)
	}
	operation := operations[0].(map[string]any)
	actor := operation["actor"].(map[string]any)
	if operation["source"] != "web" || operation["executor_kind"] != "human" || operation["outcome"] != "succeeded" || actor["role"] != "admin" || actor["id"] == "" {
		t.Fatal(operation)
	}
	items := f.call("GET", base+"/"+operation["id"].(string)+"/items", nil, f.adminCookie, true)
	raw, _ := json.Marshal(items)
	if strings.Contains(string(raw), agentFixtureSecret) || strings.Contains(string(raw), f.upstream.URL) || items["total"] != "1" {
		t.Fatal("history exposed configuration values or missed target", string(raw))
	}
	before := f.call("GET", base+"?view=operations&domain=ai", nil, f.adminCookie, true)["total"]
	f.call("GET", "/admin/api/ai/drivers", nil, f.adminCookie, true)
	f.call("GET", "/admin/api/ai/chatgpt", nil, f.adminCookie, true)
	if after := f.call("GET", base+"?view=operations&domain=ai", nil, f.adminCookie, true)["total"]; after != before {
		t.Fatal("reads created records", before, after)
	}
	bad := f.request("POST", "/admin/api/ai/drivers", map[string]any{"name": "Invalid"}, f.adminCookie, true)
	if bad.Code != http.StatusBadRequest {
		t.Fatal(bad.Code, bad.Body.String())
	}
	page = f.call("GET", base+"?view=operations&domain=ai&outcome=rejected", nil, f.adminCookie, true)
	if page["total"] != "1" {
		t.Fatal("rejected write missing", page)
	}
	for _, seat := range []int{0, 2, 3} {
		response := f.request("GET", base, nil, f.users[seat].Cookie, true)
		if response.Code != http.StatusUnauthorized && response.Code != http.StatusForbidden {
			t.Fatal("non-admin read history", response.Code)
		}
	}
	unauthorized := f.request("POST", "/api/steward/automation/donations", map[string]any{}, nil, false)
	if unauthorized.Code < 400 || unauthorized.Code >= 500 {
		t.Fatal(unauthorized.Code, unauthorized.Body.String())
	}
	callerAttempts := f.call("GET", base+"?view=operations&source=caller_key", nil, f.adminCookie, true)
	if callerAttempts["total"] != "1" {
		t.Fatal("CallerKey rejection source", callerAttempts)
	}
	callerAttempt := callerAttempts["operations"].([]any)[0].(map[string]any)
	if callerAttempt["outcome"] != "rejected" || callerAttempt["executor_kind"] != "caller_key" || callerAttempt["actor"].(map[string]any)["id"] != "" {
		t.Fatal(callerAttempt)
	}
	prepared := f.call("POST", base+"/cleanup/prepare", map[string]any{"filter": map[string]any{"domain": "ai"}}, f.adminCookie, true)
	f.call("PUT", "/admin/api/ai/driver-bindings", map[string]any{"scope": "admin_chat", "actor_id": "0", "driver_id": profile["id"], "expected_revision": "0"}, f.adminCookie, true)
	response := f.request("POST", base+"/cleanup", map[string]any{"request_id": "history-cleanup-fixture", "prepared": prepared}, f.adminCookie, true)
	job := map[string]any{}
	if response.Code != http.StatusAccepted || json.Unmarshal(response.Body.Bytes(), &job) != nil {
		t.Fatal(response.Code, response.Body.String())
	}
	deadline := time.Now().Add(5 * time.Second)
	for job["state"] != "completed" {
		if job["state"] == "failed" || time.Now().After(deadline) {
			t.Fatal("cleanup worker did not finish", job)
		}
		time.Sleep(10 * time.Millisecond)
		job = f.call("GET", base+"/cleanup/"+job["id"].(string)+"/status", nil, f.adminCookie, true)
	}
	page = f.call("GET", base+"?view=operations&domain=ai", nil, f.adminCookie, true)
	if page["total"] != "1" || page["operations"].([]any)[0].(map[string]any)["action"] != "driver-bindings.update" {
		t.Fatal("cleanup crossed fixed cutoff", page)
	}
	receipts := f.call("GET", base+"?view=operations&domain=management-operations", nil, f.adminCookie, true)
	if receipts["total"] == "0" {
		t.Fatal("cleanup receipt missing")
	}
	app := f.app.Load()
	if err := f.closeApplication(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-app.managementDone:
	default:
		t.Fatal("cleanup worker survived application close")
	}
}
