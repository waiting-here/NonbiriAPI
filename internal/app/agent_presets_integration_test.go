package app

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func agentPresetPlanFixture(t *testing.T) (*imageBrowserFixture, chan map[string]any, map[string]any, map[string]any) {
	t.Helper()
	return agentPresetSourcePlanFixture(t, false)
}

func agentPresetSourcePlanFixture(t *testing.T, custom bool) (*imageBrowserFixture, chan map[string]any, map[string]any, map[string]any) {
	t.Helper()
	f, replies := newAgentWireFixture(t)
	configureAgentDriver(t, f)
	configureAgentWorkflow(t, f, "off", true)
	if _, err := f.store.DB().Exec(`UPDATE site_config SET value='1' WHERE key IN ('donation_accept_enabled','charity_enabled')`); err != nil {
		t.Fatal(err)
	}
	var fixture map[string]any
	if custom {
		fixture = seedCustomBindingManagementBrowser(t, f, f.now().Unix())
	} else {
		fixture = seedBindingManagementBrowser(t, f, f.now().Unix())
	}
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

func TestCustomPresetRealCandidateHTTPAndApplication(t *testing.T) {
	f, _, fixture, op := agentPresetSourcePlanFixture(t, true)
	did, channel := fixture["donation_id"].(string), fixture["channel_id"].(string)
	for _, route := range []struct {
		path   string
		cookie *http.Cookie
		admin  bool
	}{
		{"/admin/api/charity-binding-presets", f.adminCookie, true},
		{"/api/steward/charity-binding-presets", f.users[3].Cookie, false},
	} {
		page := f.call("GET", route.path+"?donation_id="+did+"&q=Custom&page=1&page_size=10&state=all", nil, route.cookie, route.admin)
		data := page["data"].([]any)
		if len(data) != 1 || data[0].(map[string]any)["channel_id"] != channel || data[0].(map[string]any)["recommended"] != true || page["pagination"].(map[string]any)["total_items"] != "1" {
			t.Fatal("custom preset candidates", page)
		}
		global := f.call("GET", route.path+"?q=Custom", nil, route.cookie, route.admin)
		if _, present := global["data"].([]any)[0].(map[string]any)["recommended"]; present {
			t.Fatal("global configuration projection changed", global)
		}
	}
	for _, query := range []string{"donation_id=", "donation_id=0", "donation_id=01", "donation_id=" + did + "&donation_id=" + did} {
		response := testApplicationRequest(t, f.app.Load().handler, http.MethodGet, f.cfg.AdminHost, "/admin/api/charity-binding-presets?"+query, "", []*http.Cookie{f.adminCookie}, nil)
		if response.Code != http.StatusBadRequest {
			t.Fatal("invalid donation selector", query, response.Code, response.Body.String())
		}
	}
	// A nonmatching template remains usable for the custom endpoint.
	if _, err := f.store.DB().Exec(`UPDATE mainstream_channel_entries SET canonical_base_url=? WHERE channel_id=?`, f.upstream.URL+"/other-template", channel); err != nil {
		t.Fatal(err)
	}
	page := f.call("GET", "/admin/api/charity-binding-presets?donation_id="+did, nil, f.adminCookie, true)
	if page["data"].([]any)[0].(map[string]any)["recommended"] != false {
		t.Fatal("nonmatching template recommended", page)
	}
	result := f.call("POST", "/admin/api/donations/"+did+"/bindings/apply-preset", op["input"], f.adminCookie, true)
	if result["added_count"] != "16" || result["items"].([]any)[0].(map[string]any)["results"].([]any)[0] != "already_bound" {
		t.Fatal("custom application", result)
	}
	var sourceCount, endpointCount int
	if err := f.store.DB().QueryRow(`SELECT COUNT(*) FROM donation_keys WHERE donation_id=? AND mainstream_channel_id IS NOT NULL`, did).Scan(&sourceCount); err != nil || sourceCount != 0 {
		t.Fatal("custom donation source changed", sourceCount, err)
	}
	if err := f.store.DB().QueryRow(`SELECT COUNT(*) FROM endpoints e JOIN endpoint_keys k ON k.endpoint_id=e.id JOIN donation_keys dk ON dk.endpoint_key_id=k.id WHERE dk.donation_id=? AND e.mainstream_channel_id IS NOT NULL`, did).Scan(&endpointCount); err != nil || endpointCount != 0 {
		t.Fatal("custom endpoint source changed", endpointCount, err)
	}
}

func TestAgentRealCustomPresetReadAndHumanConfirmation(t *testing.T) {
	f, replies, fixture, op := agentPresetSourcePlanFixture(t, true)
	did := fixture["donation_id"].(string)
	if _, err := f.store.DB().Exec(`UPDATE mainstream_channel_entries SET canonical_base_url=? WHERE channel_id=?`, f.upstream.URL+"/other-template", fixture["channel_id"]); err != nil {
		t.Fatal(err)
	}
	replies <- map[string]any{"role": "assistant", "content": "", "tool_calls": []any{agentToolCall(t, "custom-preset-read", "charity_binding_presets", map[string]any{"donation_id": did, "query": "Custom"})}}
	replies <- map[string]any{"role": "assistant", "content": "请确认复用预设。", "tool_calls": []any{agentToolCall(t, "custom-preset-plan", "propose_operations", map[string]any{"operations": []any{op}})}}
	f.call("POST", "/admin/api/ai/chat/messages", map[string]any{"content": "为自建来源捐赠复用预设", "expected_generation": "0", "request_key": "real-custom-preset-001"}, f.adminCookie, true)
	chat := waitAgentChat(t, f, "waiting_confirmation")
	found := false
	for _, value := range chat["messages"].([]any) {
		message := value.(map[string]any)
		if message["role"] == "tool" && message["tool_call_id"] == "custom-preset-read" {
			content, _ := message["content"].(string)
			found = strings.Contains(content, `"total":"1"`) && strings.Contains(content, `"recommended":false`) && strings.Contains(content, "Custom preset browser service")
		}
	}
	if !found {
		t.Fatal("custom read facts missing", chat)
	}
	var count int
	if err := f.store.DB().QueryRow(`SELECT COUNT(*) FROM charity_model_bindings`).Scan(&count); err != nil || count != 1 {
		t.Fatal("unconfirmed plan wrote bindings", count, err)
	}
	plan := chat["plans"].([]any)[0].(map[string]any)
	result := f.call("POST", "/admin/api/ai/chat/plans/"+plan["id"].(string)+"/decision", map[string]any{"approve": true, "feedback": "应用映射模板"}, f.adminCookie, true)
	if result["state"] != "approved" || !strings.Contains(wireBody(t, result), `"added_count":"16"`) || !strings.Contains(wireBody(t, result), `"skipped_count":"2"`) {
		t.Fatal("custom confirmed plan", result)
	}
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
