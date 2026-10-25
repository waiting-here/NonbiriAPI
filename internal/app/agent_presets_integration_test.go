package app

import (
	"fmt"
	"strings"
	"testing"
)

func agentPresetPlanFixture(t *testing.T) (*imageBrowserFixture, chan map[string]any, map[string]any, map[string]any) {
	t.Helper()
	f, replies := newAgentWireFixture(t)
	configureAgentDriver(t, f)
	configureAgentWorkflow(t, f, "off", true)
	if _, err := f.store.DB().Exec(`UPDATE site_config SET value='1' WHERE key IN ('donation_accept_enabled','charity_enabled')`); err != nil {
		t.Fatal(err)
	}
	fixture := seedBindingManagementBrowser(t, f, f.now().Unix())
	did := fixture["donation_id"].(string)
	channel := fixture["channel_id"].(string)
	models := fixture["model_ids"].([]string)
	mappings := []map[string]any{}
	revisions := []map[string]any{}
	for i, id := range models {
		mappings = append(mappings, map[string]any{"charity_model_id": id, "upstream_model_id": fmt.Sprint("binding-up-", i)})
		revision := "0"
		if i == 0 {
			revision = "1"
		}
		revisions = append(revisions, map[string]any{"charity_model_id": id, "expected_binding_revision": revision})
	}
	preset := f.call("PUT", "/admin/api/charity-binding-presets/"+channel, map[string]any{"expected_channel_revision": "1", "expected_preset_revision": "0", "mappings": mappings}, f.adminCookie, true)
	donation := f.call("GET", "/admin/api/donations/"+did, nil, f.adminCookie, true)
	op := map[string]any{"kind": "charity.binding.apply_preset", "target_id": did, "input": map[string]any{"channel_id": channel, "expected_channel_revision": preset["channel_revision"], "expected_preset_revision": preset["preset_revision"], "expected_donation_revision": donation["revision"], "models": revisions}}
	return f, replies, fixture, op
}

func TestAgentRealPresetReadHumanConfirmationAndReplay(t *testing.T) {
	f, replies, fixture, op := agentPresetPlanFixture(t)
	did := fixture["donation_id"].(string)
	replies <- map[string]any{"role": "assistant", "content": "", "tool_calls": []any{agentToolCall(t, "preset-read", "charity_binding_presets", map[string]any{"donation_id": did})}}
	replies <- map[string]any{"role": "assistant", "content": "请确认应用预设；缺失模型和已有绑定会跳过。", "tool_calls": []any{agentToolCall(t, "preset-plan", "propose_operations", map[string]any{"operations": []any{op}})}}
	f.call("POST", "/admin/api/ai/chat/messages", map[string]any{"content": "为这份主流渠道捐赠应用保存的预设", "expected_generation": "0", "request_key": "real-preset-plan-001"}, f.adminCookie, true)
	chat := waitAgentChat(t, f, "waiting_confirmation")
	readSucceeded := false
	for _, value := range chat["messages"].([]any) {
		message := value.(map[string]any)
		if message["role"] == "tool" && message["tool_call_id"] == "preset-read" {
			content, _ := message["content"].(string)
			readSucceeded = strings.Contains(content, `"total":"1"`) && strings.Contains(content, "Binding browser service") && strings.Contains(content, "binding-up-3") && !strings.Contains(content, "synthetic-binding-donation-")
		}
	}
	if !readSucceeded {
		t.Fatal("real preset read did not return safe discovered channel facts", chat)
	}
	plan := chat["plans"].([]any)[0].(map[string]any)
	if !strings.Contains(wireBody(t, plan), "Binding browser service") || !strings.Contains(wireBody(t, plan), "binding-up-3") {
		t.Fatal("preset human preview missing", plan)
	}
	var before int
	if err := f.store.DB().QueryRow(`SELECT COUNT(*) FROM charity_model_bindings`).Scan(&before); err != nil || before != 1 {
		t.Fatal("proposed plan wrote bindings", before, err)
	}
	path := "/admin/api/ai/chat/plans/" + plan["id"].(string) + "/decision"
	decision := map[string]any{"approve": true, "feedback": "应用预设并接受正常跳过"}
	result := f.call("POST", path, decision, f.adminCookie, true)
	if result["state"] != "approved" || result["result"].(map[string]any)["status"] != "completed" {
		t.Fatal("preset plan failed", result)
	}
	resultBody := wireBody(t, result)
	for _, field := range []string{`"added_count":"16"`, `"missing_model_count":"1"`, `"already_bound_count":"1"`, `"skipped_count":"2"`} {
		if !strings.Contains(resultBody, field) {
			t.Fatal("skip receipt missing", field, resultBody)
		}
	}
	replayed := f.call("POST", path, decision, f.adminCookie, true)
	if replayed["state"] != "approved" || replayed["result"].(map[string]any)["status"] != "completed" {
		t.Fatal("decision replay failed", replayed)
	}
	var after int
	if err := f.store.DB().QueryRow(`SELECT COUNT(*) FROM charity_model_bindings`).Scan(&after); err != nil || after != 17 {
		t.Fatal("replay duplicated bindings", after, err)
	}
}

func TestAgentRealPresetPlanFailuresRollBack(t *testing.T) {
	for _, reason := range []string{"invalid_resource", "later_operation", "stale_revision"} {
		t.Run(reason, func(t *testing.T) {
			f, replies, fixture, op := agentPresetPlanFixture(t)
			models := fixture["model_ids"].([]string)
			ops := []any{op}
			if reason == "later_operation" {
				ops = append(ops, map[string]any{"kind": "charity.model.update", "target_id": models[5], "input": map[string]any{"expected_revision": "1", "public_description": "Approved final description"}})
			}
			replies <- map[string]any{"role": "assistant", "content": "请确认完整计划。", "tool_calls": []any{agentToolCall(t, "preset-plan", "propose_operations", map[string]any{"operations": ops})}}
			f.call("POST", "/admin/api/ai/chat/messages", map[string]any{"content": "应用预设并完成所有指定修改", "expected_generation": "0", "request_key": "preset-failure-plan-001"}, f.adminCookie, true)
			chat := waitAgentChat(t, f, "waiting_confirmation")
			plan := chat["plans"].([]any)[0].(map[string]any)
			switch reason {
			case "invalid_resource":
				keys := fixture["key_ids"].([]string)
				if _, err := f.store.DB().Exec(`DELETE FROM donation_key_memberships WHERE donation_key_id=?`, keys[2]); err != nil {
					t.Fatal(err)
				}
			case "later_operation":
				if _, err := f.store.DB().Exec(`UPDATE charity_models SET revision=revision+1 WHERE id=?`, models[5]); err != nil {
					t.Fatal(err)
				}
			case "stale_revision":
				if _, err := f.store.DB().Exec(`UPDATE mainstream_channels SET revision=revision+1 WHERE id=?`, fixture["channel_id"]); err != nil {
					t.Fatal(err)
				}
			}
			result := f.call("POST", "/admin/api/ai/chat/plans/"+plan["id"].(string)+"/decision", map[string]any{"approve": true, "feedback": "执行完整计划"}, f.adminCookie, true)
			if result["state"] != "failed" {
				t.Fatal("invalid whole plan accepted", result)
			}
			var count, revisions int
			if err := f.store.DB().QueryRow(`SELECT COUNT(*) FROM charity_model_bindings`).Scan(&count); err != nil || count != 1 {
				t.Fatal("partial preset writes survived", count, err)
			}
			if err := f.store.DB().QueryRow(`SELECT SUM(binding_revision) FROM charity_models`).Scan(&revisions); err != nil || revisions != 1 {
				t.Fatal("partial binding revisions survived", revisions, err)
			}
		})
	}
}
