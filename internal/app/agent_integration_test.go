package app

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
)

const agentFixtureSecret = "synthetic-assistant-credential"

func newAgentWireFixture(t *testing.T) (*imageBrowserFixture, chan map[string]any) {
	t.Helper()
	replies := make(chan map[string]any, 8)
	f := newActivityWireFixtureWithUpstream(t, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !strings.HasPrefix(r.URL.Path, "/agent/v1/") {
				next.ServeHTTP(w, r)
				return
			}
			if r.Header.Get("Authorization") != "Bearer "+agentFixtureSecret {
				http.Error(w, "invalid fixture key", 401)
				return
			}
			if r.URL.Path == "/agent/v1/models" && r.Method == http.MethodGet {
				writeImageFixtureJSON(w, map[string]any{"data": []any{map[string]any{"id": "fixture-assistant"}}})
				return
			}
			if r.URL.Path != "/agent/v1/chat/completions" || r.Method != http.MethodPost {
				http.NotFound(w, r)
				return
			}
			var input map[string]any
			if json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&input) != nil || input["model"] != "fixture-assistant" || input["stream"] != false {
				http.Error(w, "invalid fixture request", 400)
				return
			}
			message := map[string]any{"role": "assistant", "content": "已根据执行结果完成说明。"}
			select {
			case message = <-replies:
			default:
			}
			finish := "stop"
			if message["tool_calls"] != nil {
				finish = "tool_calls"
			}
			writeImageFixtureJSON(w, map[string]any{"id": "fixture-response", "object": "chat.completion", "model": "fixture-assistant", "choices": []any{map[string]any{"index": 0, "finish_reason": finish, "message": message}}, "usage": map[string]any{"prompt_tokens": 20, "completion_tokens": 10, "total_tokens": 30}})
		})
	})
	return f, replies
}

func configureAgentDriver(t *testing.T, f *imageBrowserFixture) {
	t.Helper()
	old := f.call("GET", "/admin/api/ai/driver", nil, f.adminCookie, true)
	f.call("PUT", "/admin/api/ai/driver", map[string]any{"expected_revision": old["revision"], "base_url": f.upstream.URL + "/agent/v1", "api_key": agentFixtureSecret, "model": "fixture-assistant", "context_window": 8192, "max_output_tokens": 1024, "extra_parameters": map[string]any{}, "concurrency": 1, "timeout_seconds": 30}, f.adminCookie, true)
}

func configureAgentWorkflow(t *testing.T, f *imageBrowserFixture, mode string, chat bool) {
	t.Helper()
	settings := f.call("GET", "/admin/api/ai/config", nil, f.adminCookie, true)
	settings["expected_revision"], settings["mode"], settings["chat_enabled"] = settings["revision"], mode, chat
	f.call("PUT", "/admin/api/ai/config", settings, f.adminCookie, true)
}

func waitAgentChat(t *testing.T, f *imageBrowserFixture, state string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(12 * time.Second)
	for {
		chat := f.call("GET", "/admin/api/ai/chat", nil, f.adminCookie, true)
		if job, ok := chat["job"].(map[string]any); ok {
			if job["state"] == state {
				return chat
			}
			if job["state"] == "failed" || job["state"] == "interrupted" || job["state"] == "cancelled" {
				t.Fatalf("agent job failed: %v", job)
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("agent did not reach %s: %v", state, chat)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func agentToolCall(t *testing.T, id, name string, args any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	return map[string]any{"id": id, "type": "function", "function": map[string]any{"name": name, "arguments": string(raw)}}
}

func TestAgentRealSessionsSwitchAndDriverReadiness(t *testing.T) {
	f, _ := newAgentWireFixture(t)
	admin := f.call("GET", "/admin/api/ai/chat", nil, f.adminCookie, true)
	if admin["enabled"] != false {
		t.Fatal("chat default open", admin)
	}
	input := map[string]any{"content": "请查询站点情况", "expected_generation": "0", "request_key": "availability-test-001"}
	if out := f.request("POST", "/admin/api/ai/chat/messages", input, f.adminCookie, true); out.Code != 503 {
		t.Fatal("closed chat", out.Code, out.Body.String())
	}
	configureAgentWorkflow(t, f, "off", true)
	if out := f.request("POST", "/admin/api/ai/chat/messages", input, f.adminCookie, true); out.Code != 503 {
		t.Fatal("unconfigured chat", out.Code, out.Body.String())
	}
	configureAgentDriver(t, f)
	models := f.call("POST", "/admin/api/ai/driver/models", map[string]any{}, f.adminCookie, true)
	if !strings.Contains(wireBody(t, models), "fixture-assistant") {
		t.Fatal("model discovery", models)
	}
	for _, seat := range []int{2, 3} {
		value := f.call("GET", "/api/steward/ai/chat", nil, f.users[seat].Cookie, false)
		if value["enabled"] != true {
			t.Fatal("enabled steward", seat, value)
		}
	}
	policy := authz.DefaultStewardPermissions(5)
	policy.Permissions["ai.chat"] = false
	setPolicy := func(revision string) {
		t.Helper()
		f.call("PUT", "/admin/api/users/"+f.users[2].ID+"/steward-permissions", map[string]any{"expected_revision": revision, "permissions": policy.Permissions, "limits": policy.Limits, "donation_scope": policy.DonationScope}, f.adminCookie, true)
	}
	setPolicy("0")
	if out := f.request("POST", "/api/steward/ai/chat/messages", input, f.users[2].Cookie, false); out.Code != 403 {
		t.Fatal("level5 missing grant", out.Code, out.Body.String())
	}
	if out := f.request("POST", "/api/steward/ai/chat/messages", input, f.users[0].Cookie, false); out.Code != 403 {
		t.Fatal("ordinary user admitted", out.Code, out.Body.String())
	}
	policy.Permissions["ai.chat"] = true
	setPolicy("1")
	f.call("POST", "/admin/api/ai/chat/messages", input, f.adminCookie, true)
	waitAgentChat(t, f, "completed")
	configureAgentWorkflow(t, f, "off", false)
	chat := f.call("GET", "/admin/api/ai/chat", nil, f.adminCookie, true)
	if chat["enabled"] != false || len(chat["messages"].([]any)) != 2 {
		t.Fatal("closing lost history", chat)
	}
	if out := f.request("POST", "/api/steward/ai/chat/messages", input, f.users[3].Cookie, false); out.Code != 503 {
		t.Fatal("level6 bypass switch", out.Code, out.Body.String())
	}
}

func TestAgentRealPlanRollsBackAccountAndMemoryAndRetainsLogs(t *testing.T) {
	f, replies := newAgentWireFixture(t)
	configureAgentDriver(t, f)
	configureAgentWorkflow(t, f, "off", true)
	beforeMemory := f.call("GET", "/admin/api/ai/memories/chat", nil, f.adminCookie, true)
	target := f.call("GET", "/admin/api/users/"+f.users[0].ID, nil, f.adminCookie, true)
	revision := target["revision"]
	operations := []map[string]any{
		{"kind": "users.profile", "target_id": f.users[0].ID, "input": map[string]any{"expected_revision": revision, "endpoint_limit": "11"}},
		{"kind": "users.profile", "target_id": f.users[0].ID, "input": map[string]any{"expected_revision": revision, "endpoint_limit": "12"}},
	}
	replies <- map[string]any{"role": "assistant", "content": "请确认整套修改。", "tool_calls": []any{
		agentToolCall(t, "memory-1", "edit_memory", map[string]any{"action": "write", "kind": "chat", "content": "只在全部操作成功后保存", "expected_revision": beforeMemory["revision"]}),
		agentToolCall(t, "profile-1", "propose_operations", map[string]any{"operations": operations}),
	}}
	f.call("POST", "/admin/api/ai/chat/messages", map[string]any{"content": "测试整套修改的原子性", "expected_generation": "0", "request_key": "real-atomic-plan-001"}, f.adminCookie, true)
	chat := waitAgentChat(t, f, "waiting_confirmation")
	plans := chat["plans"].([]any)
	if len(plans) != 1 {
		t.Fatal("missing plan", chat)
	}
	plan := plans[0].(map[string]any)
	if len(plan["operations"].([]any)) != 3 {
		t.Fatal("split plan", plan)
	}
	result := f.call("POST", "/admin/api/ai/chat/plans/"+plan["id"].(string)+"/decision", map[string]any{"approve": true, "feedback": "同意整套操作"}, f.adminCookie, true)
	if result["state"] != "failed" {
		t.Fatal("stale second action should roll back", result)
	}
	after := f.call("GET", "/admin/api/users/"+f.users[0].ID, nil, f.adminCookie, true)
	if after["revision"] != revision || after["endpoint_limit"] != target["endpoint_limit"] {
		t.Fatal("partial account mutation", target, after)
	}
	memory := f.call("GET", "/admin/api/ai/memories/chat", nil, f.adminCookie, true)
	if memory["revision"] != beforeMemory["revision"] || memory["content"] != beforeMemory["content"] {
		t.Fatal("partial memory mutation", beforeMemory, memory)
	}
	waitAgentChat(t, f, "completed")
	logs := f.call("GET", "/admin/api/ai/requests?page=1&page_size=20", nil, f.adminCookie, true)
	items := logs["items"].([]any)
	if len(items) != 2 {
		t.Fatal("model loop logs", logs)
	}
	detail := f.call("GET", "/admin/api/ai/requests/"+items[1].(map[string]any)["id"].(string), nil, f.adminCookie, true)
	encoded := wireBody(t, detail)
	if !strings.Contains(encoded, "测试整套修改的原子性") || !strings.Contains(encoded, "memory-1") || strings.Contains(encoded, agentFixtureSecret) {
		t.Fatal("request log completeness or secret leak", encoded)
	}
	usage := f.call("GET", "/admin/api/ai/usage", nil, f.adminCookie, true)
	if len(usage["total"].([]any)) != 1 {
		t.Fatal("usage attribution", usage)
	}
	chat = f.call("GET", "/admin/api/ai/chat", nil, f.adminCookie, true)
	original := chat["messages"].([]any)[0].(map[string]any)
	edit := map[string]any{"content": "只解释结果，无需修改", "expected_generation": chat["generation"], "request_key": "real-edit-lost-reply-01"}
	first := f.call("PUT", "/admin/api/ai/chat/messages/"+original["id"].(string), edit, f.adminCookie, true)
	replay := f.call("PUT", "/admin/api/ai/chat/messages/"+original["id"].(string), edit, f.adminCookie, true)
	if first["id"] != replay["id"] || first["generation"] != replay["generation"] {
		t.Fatal("edit replay duplicated", first, replay)
	}
	waitAgentChat(t, f, "completed")
}

func TestAgentAutomaticReviewNewSubmissionOnlyAndIndependentOfChat(t *testing.T) {
	f, replies := newAgentWireFixture(t)
	configureAgentDriver(t, f)
	if _, err := f.store.DB().Exec(`UPDATE site_config SET value='1' WHERE key IN ('donation_accept_enabled','charity_enabled')`); err != nil {
		t.Fatal(err)
	}
	owner := f.users[1].Cookie
	endpoint := f.call("POST", "/api/endpoints", map[string]any{"source": "custom", "connector_type": "openai-compatible", "base_url": f.upstream.URL + "/v1", "note": "Review fixture", "enabled": true}, owner, false)
	key := f.call("POST", "/api/endpoints/"+endpoint["id"].(string)+"/keys", map[string]any{"secret": imageFixtureSecret, "note": "Review fixture key", "enabled": true, "force_store_false": false, "ownership_confirmed": true}, owner, false)
	donation := f.call("POST", "/api/donations", map[string]any{"description": "Submitted while off", "discord_public_thanks": false, "ownership_authorized": true, "keys": []any{map[string]any{"endpoint_key_id": key["id"], "expires_at": nil}}}, owner, false)
	if donation["status"] != "pending" {
		t.Fatal("fixture is not pending", donation)
	}
	configureAgentWorkflow(t, f, "preview", false)
	var events int
	if err := f.store.DB().QueryRow(`SELECT count(*) FROM ai_agent_review_events`).Scan(&events); err != nil || events != 0 {
		t.Fatal("backlog was enqueued", events, err)
	}
	raw, _ := json.Marshal(map[string]any{"decision": "manual", "internal_reason": "需要人工确认资源用途", "user_note": "请补充说明", "bindings": []any{}})
	replies <- map[string]any{"role": "assistant", "content": string(raw)}
	f.call("PATCH", "/api/donations/"+donation["id"].(string), map[string]any{"expected_revision": donation["revision"], "description": "New pending revision while enabled"}, owner, false)
	deadline := time.Now().Add(12 * time.Second)
	for {
		page := f.call("GET", "/admin/api/ai/reviews?page=1&page_size=20", nil, f.adminCookie, true)
		items := page["items"].([]any)
		if len(items) > 0 {
			review := items[0].(map[string]any)
			job := review["job"].(map[string]any)
			if job["state"] == "completed" {
				if review["result"].(map[string]any)["decision"] != "manual" {
					t.Fatal("review result", review)
				}
				if strings.Contains(wireBody(t, review), imageFixtureSecret) {
					t.Fatal("donation secret in review")
				}
				break
			}
			if job["state"] == "failed" {
				t.Fatal("review failed", review)
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("review did not finish", page)
		}
		time.Sleep(10 * time.Millisecond)
	}
	current := f.call("GET", fmt.Sprintf("/api/donations/%s", donation["id"]), nil, owner, false)
	if current["status"] != "pending" {
		t.Fatal("preview mutated donation", current)
	}
	chat := f.call("GET", "/admin/api/ai/chat", nil, f.adminCookie, true)
	if chat["enabled"] != false {
		t.Fatal("automatic review opened chat", chat)
	}
	model := f.call("POST", "/admin/api/charity-models", map[string]any{
		"provider": "assistant", "model": "reviewed", "enabled": true, "is_mainstream": false, "flatten_tool_calls": false,
		"pricing":  map[string]any{"mode": "per_request", "user_price": "0", "donor_reward": "0"},
		"discount": map[string]any{"enabled": false, "percent": 100, "start_at": nil, "end_at": nil},
	}, f.adminCookie, true)
	detail := f.call("GET", "/admin/api/donations/"+donation["id"].(string), nil, f.adminCookie, true)
	member := detail["keys"].([]any)[0].(map[string]any)
	configureAgentWorkflow(t, f, "auto", false)
	raw, _ = json.Marshal(map[string]any{"decision": "approve", "internal_reason": "虚拟资源可绑定已有模型", "user_note": "批准并配置", "bindings": []any{map[string]any{
		"charity_model_id": model["id"], "expected_binding_revision": "0", "donation_key_id": member["id"], "upstream_model_id": "fixture/chat", "native_services": []string{"chat_completions"},
	}}})
	replies <- map[string]any{"role": "assistant", "content": string(raw)}
	f.call("PATCH", "/api/donations/"+donation["id"].(string), map[string]any{"expected_revision": current["revision"], "description": "Resubmitted for actual approval"}, owner, false)
	deadline = time.Now().Add(12 * time.Second)
	for {
		page := f.call("GET", "/admin/api/ai/reviews?page=1&page_size=20", nil, f.adminCookie, true)
		items := page["items"].([]any)
		if len(items) == 2 {
			review := items[0].(map[string]any)
			job := review["job"].(map[string]any)
			if job["state"] == "completed" {
				if strings.Contains(wireBody(t, review["execution"]), `"applied":false`) {
					t.Fatal("automatic approval did not execute", review)
				}
				break
			}
			if job["state"] == "failed" {
				t.Fatal("automatic approval failed", review)
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("automatic approval did not finish", page)
		}
		time.Sleep(10 * time.Millisecond)
	}
	approved := f.call("GET", "/api/donations/"+donation["id"].(string), nil, owner, false)
	if approved["status"] != "approved" {
		t.Fatal("approval did not persist", approved)
	}
	var bindings int
	if err := f.store.DB().QueryRow(`SELECT count(*) FROM charity_binding_targets t JOIN charity_model_bindings b ON b.id=t.binding_id WHERE b.charity_model_id=? AND t.donation_key_id=?`, model["id"], member["id"]).Scan(&bindings); err != nil || bindings != 1 {
		t.Fatal("approved without binding", bindings, err)
	}
}
