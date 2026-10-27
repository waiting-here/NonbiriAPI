package app

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

func TestAgentDriverRoutesSelectionAndNativeContinuation(t *testing.T) {
	requests := make(chan string, 8)
	var calls atomic.Int64
	f := newActivityWireFixtureWithUpstream(t, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/agent/v1/responses" {
				next.ServeHTTP(w, r)
				return
			}
			if r.Header.Get("Authorization") != "Bearer "+agentFixtureSecret {
				http.Error(w, "unexpected credential", 401)
				return
			}
			raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
			if err != nil {
				t.Error(err)
				return
			}
			requests <- string(raw)
			output := []any{map[string]any{"type": "message", "id": "message-finished", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "Records checked."}}}}
			if calls.Add(1) == 1 {
				output = []any{
					map[string]any{"type": "reasoning", "id": "reason-fixture", "summary": []any{}, "encrypted_content": "synthetic-opaque-reasoning"},
					map[string]any{"type": "function_call", "id": "function-fixture", "call_id": "call-fixture", "name": "management_history", "arguments": `{"action":"list","filter":{"domain":"ai"}}`},
				}
			}
			event, _ := json.Marshal(map[string]any{"type": "response.completed", "response": map[string]any{"status": "completed", "output": output, "usage": map[string]int{"input_tokens": 20, "output_tokens": 5}}})
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprintf(w, "event: response.completed\ndata: %s\n\n", event)
		})
	})
	status := f.call("GET", "/admin/api/ai/chatgpt", nil, f.adminCookie, true)
	if status["connected"] != false || status["updated_at"] != float64(0) || status["official_usage"].(map[string]any)["availability"] != "unavailable" || status["local_usage"].(map[string]any)["requests"] != "0" || status["last_limit_error"] != nil {
		t.Fatal(status)
	}
	f.call("POST", "/admin/api/ai/chatgpt/target", map[string]any{}, f.adminCookie, true)
	status = f.call("GET", "/admin/api/ai/chatgpt", nil, f.adminCookie, true)
	if status["updated_at"].(float64) <= 0 || calls.Load() != 0 {
		t.Fatal("status did not remain local", status)
	}
	for _, seat := range []int{0, 2, 3} {
		out := f.request("GET", "/admin/api/ai/chatgpt", nil, f.users[seat].Cookie, true)
		if out.Code != http.StatusUnauthorized && out.Code != http.StatusForbidden {
			t.Fatal("non-administrator connection status", out.Code)
		}
	}
	configuration := f.call("GET", "/admin/api/ai/drivers", nil, f.adminCookie, true)
	if len(configuration["profiles"].([]any)) != 1 {
		t.Fatal(configuration)
	}
	profile := f.call("POST", "/admin/api/ai/drivers", map[string]any{
		"name": "Responses fixture", "api_key": agentFixtureSecret,
		"settings": map[string]any{"provider": "responses", "base_url": f.upstream.URL + "/agent/v1", "model": "fixture-responses", "context_window": 32768, "output_reserve_tokens": 1024, "max_output_tokens": 0, "extra_parameters": map[string]any{}, "concurrency": 1, "timeout_seconds": 30},
	}, f.adminCookie, true)
	bind := func(scope, actor string, driver any, revision string) map[string]any {
		return f.call("PUT", "/admin/api/ai/driver-bindings", map[string]any{"scope": scope, "actor_id": actor, "driver_id": driver, "expected_revision": revision}, f.adminCookie, true)
	}
	bind("admin_chat", "0", profile["id"], "0")
	configureAgentWorkflow(t, f, "off", true)
	chat := f.call("GET", "/admin/api/ai/chat", nil, f.adminCookie, true)
	if chat["enabled"] != true {
		t.Fatal("administrator purpose ignored", chat)
	}
	steward := f.call("GET", "/api/steward/ai/chat", nil, f.users[3].Cookie, false)
	if steward["enabled"] != false {
		t.Fatal("administrator override escaped to steward")
	}
	bind("steward_chat", "0", profile["id"], "0")
	steward = f.call("GET", "/api/steward/ai/chat", nil, f.users[3].Cookie, false)
	if steward["enabled"] != true {
		t.Fatal("shared steward driver ignored")
	}
	personal := bind("steward_chat", f.users[3].ID, "0", "0")
	steward = f.call("GET", "/api/steward/ai/chat", nil, f.users[3].Cookie, false)
	if steward["enabled"] != false {
		t.Fatal("personal unconfigured driver ignored")
	}
	bind("steward_chat", f.users[3].ID, nil, personal["revision"].(string))
	steward = f.call("GET", "/api/steward/ai/chat", nil, f.users[3].Cookie, false)
	if steward["enabled"] != true {
		t.Fatal("removing personal override did not inherit")
	}
	f.call("POST", "/admin/api/ai/chat/messages", map[string]any{"content": "Inspect donations", "expected_generation": "0", "request_key": "responses-round-one"}, f.adminCookie, true)
	chat = waitAgentChat(t, f, "completed")
	if calls.Load() != 2 {
		t.Fatal("tool round did not complete", calls.Load())
	}
	first, second := <-requests, <-requests
	if strings.Contains(first, "max_output_tokens") || !strings.Contains(second, "synthetic-opaque-reasoning") || !strings.Contains(second, "function_call_output") {
		t.Fatal("native output or default parameters were lost")
	}
	var wire struct {
		Input []struct {
			Type   string `json:"type"`
			Output string `json:"output"`
		} `json:"input"`
	}
	if err := json.Unmarshal([]byte(second), &wire); err != nil {
		t.Fatal(err)
	}
	historyReturned := false
	for _, item := range wire.Input {
		if item.Type != "function_call_output" {
			continue
		}
		var page map[string]any
		if json.Unmarshal([]byte(item.Output), &page) == nil && page["view"] == "groups" && page["total"] != nil {
			historyReturned = true
		}
	}
	if !historyReturned {
		t.Fatal("administrator history tool did not return a real page")
	}
	f.call("POST", "/admin/api/ai/chat/messages", map[string]any{"content": "Continue", "expected_generation": chat["generation"], "request_key": "responses-round-two"}, f.adminCookie, true)
	waitAgentChat(t, f, "completed")
	if third := <-requests; !strings.Contains(third, "synthetic-opaque-reasoning") {
		t.Fatal("persisted native history was lost")
	}
	status = f.call("GET", "/admin/api/ai/chatgpt", nil, f.adminCookie, true)
	if status["local_usage"].(map[string]any)["requests"] != "0" {
		t.Fatal("external Responses counted as ChatGPT")
	}
}
