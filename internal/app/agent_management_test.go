package app

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/aiagent"
	"github.com/waiting-here/NonbiriAPI/internal/auth"
	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/managementlog"
)

func managementTestActor(t *testing.T, f *imageBrowserFixture, userID string) aiagent.Actor {
	t.Helper()
	actor := aiagent.Actor{Role: "admin"}
	query := "SELECT id,username FROM users WHERE is_admin=1"
	args := []any{}
	if userID != "" {
		query = "SELECT id,username FROM users WHERE id=?"
		args = append(args, userID)
		actor.Role = "level5"
	}
	if err := f.store.ReadDB().QueryRow(query, args...).Scan(&actor.ID, &actor.Label); err != nil {
		t.Fatal(err)
	}
	if err := f.store.ReadDB().QueryRow("SELECT token_hash,cred_gen FROM sessions WHERE user_id=?", actor.ID).Scan(&actor.SessionHash, &actor.SessionGeneration); err != nil {
		t.Fatal(err)
	}
	return actor
}

func TestAgentManagementCatalogAndDirectTools(t *testing.T) {
	f, _ := newAgentWireFixture(t)
	configureAgentDriver(t, f)
	configureAgentWorkflow(t, f, "preview", true)
	o := &agentOperations{runtime: f.app.Load().authRuntime}
	ctx := context.Background()
	admin := managementTestActor(t, f, "")
	auto := admin
	auto.Automatic = true
	auto.SessionHash = ""
	auto.SessionGeneration = ""
	for _, actor := range []aiagent.Actor{admin, auto} {
		routes, err := o.runtime.ManagementCatalog(ctx, sessionAgentActor(actor), actor.Automatic)
		if err != nil || len(routes) < 100 {
			t.Fatalf("catalog %d: %v", len(routes), err)
		}
		foundConfig, foundRefresh := false, false
		for _, route := range routes {
			method, path, _ := strings.Cut(route.Route, " ")
			_, mutation := managementlog.DescribeRoute(method, path)
			if method == "POST" && !mutation && route.Effect == auth.ManagementWrite && path != "/admin/api/ai/chatgpt/target" {
				t.Fatal("read-only POST omitted", route)
			}
			if strings.Contains(route.Route, "/chat/") && route.Effect != auth.ManagementRead {
				t.Fatal("recursive write exposed", route)
			}
			if actor.Automatic && route.Effect == auth.ManagementWrite {
				t.Fatal("automatic write exposed", route)
			}
			foundConfig = foundConfig || route.Route == "GET /admin/api/site-config"
			foundRefresh = foundRefresh || route.Route == "POST /admin/api/ai/driver/models"
		}
		if !foundConfig || !foundRefresh {
			t.Fatal("incomplete catalog")
		}
		for _, call := range []struct{ name, request string }{
			{"management_read", `{"route":"GET /admin/api/site-config"}`},
			{"refresh_models", `{"route":"POST /admin/api/ai/driver/models","body":{}}`},
		} {
			raw, err := o.readManagement(ctx, actor, call.name, json.RawMessage(call.request))
			if err != nil || !strings.Contains(string(raw), `"status_code":200`) {
				t.Fatalf("%s auto=%t: %s %v", call.name, actor.Automatic, raw, err)
			}
		}
		for _, name := range []string{"management_read", "refresh_models"} {
			if _, err = o.readManagement(ctx, actor, name, json.RawMessage(`{"route":"PATCH /admin/api/site-config/{key}","path_params":{"key":"site_name"},"body":{"value":"forbidden"}}`)); err == nil {
				t.Fatal("direct write allowed", name)
			}
		}
	}
	steward := managementTestActor(t, f, f.users[2].ID)
	shared, sharedErr := o.readManagement(ctx, steward, "management_read", json.RawMessage(`{"route":"GET /api/steward/ai/memories/{kind}","path_params":{"kind":"chat"}}`))
	if sharedErr != nil || !strings.Contains(string(shared), `"status_code":200`) {
		t.Fatal("shared read template lost its grant", string(shared), sharedErr)
	}
	routes, err := o.runtime.ManagementCatalog(ctx, sessionAgentActor(steward), false)
	if err != nil {
		t.Fatal(err)
	}
	for _, route := range routes {
		if strings.Contains(route.Route, "/admin/") || !authz.DefaultStewardPermissions(5).AllowsOperation(route.Permission) {
			t.Fatal("steward escaped grants", route)
		}
	}
	if _, err = o.readManagement(ctx, steward, "management_read", json.RawMessage(`{"route":"GET /admin/api/site-config"}`)); err == nil {
		t.Fatal("steward accessed admin API")
	}
	policy := authz.DefaultStewardPermissions(5)
	policy.Permissions["charity.read"] = false
	f.call("PUT", "/admin/api/users/"+f.users[2].ID+"/steward-permissions", map[string]any{"expected_revision": "0", "permissions": policy.Permissions, "limits": policy.Limits, "donation_scope": policy.DonationScope}, f.adminCookie, true)
	if _, err = o.readManagement(ctx, steward, "management_read", json.RawMessage(`{"route":"GET /api/steward/charity-models"}`)); err == nil {
		t.Fatal("revoked permission reused")
	}
	configureAgentWorkflow(t, f, "off", true)
	if _, err = o.readManagement(ctx, auto, "management_read", json.RawMessage(`{"route":"GET /admin/api/site-config"}`)); err == nil {
		t.Fatal("stopped automatic worker retained authority")
	}
}

func TestAgentManagementRealApprovalAndReplay(t *testing.T) {
	for _, approve := range []bool{false, true} {
		t.Run(strconv.FormatBool(approve), func(t *testing.T) {
			f, replies := newAgentWireFixture(t)
			configureAgentDriver(t, f)
			configureAgentWorkflow(t, f, "off", true)
			request := map[string]any{"route": "PATCH /admin/api/site-config/{key}", "path_params": map[string]string{"key": "site_name"}, "body": map[string]any{"value": "Assistant approved site"}}
			replies <- map[string]any{"role": "assistant", "content": "请批准站点名称修改。", "tool_calls": []any{agentToolCall(t, "management-1", "propose_management_operation", request)}}
			f.call("POST", "/admin/api/ai/chat/messages", map[string]any{"content": "修改站点名称", "expected_generation": "0", "request_key": "management-real-approval-001"}, f.adminCookie, true)
			chat := waitAgentChat(t, f, "waiting_confirmation")
			plan := chat["plans"].([]any)[0].(map[string]any)
			var before string
			if err := f.store.ReadDB().QueryRow("SELECT value FROM site_config WHERE key='site_name'").Scan(&before); err != nil {
				t.Fatal(err)
			}
			if before == "Assistant approved site" {
				t.Fatal("executed before approval")
			}
			path := "/admin/api/ai/chat/plans/" + plan["id"].(string) + "/decision"
			if out := f.request("POST", strings.Replace(path, "/admin/api/", "/api/steward/", 1), map[string]any{"approve": true}, f.users[3].Cookie, false); out.Code != 403 {
				t.Fatal("different interlocutor decision", out.Code, out.Body.String())
			}
			result := f.call("POST", path, map[string]any{"approve": approve}, f.adminCookie, true)
			want := "rejected"
			if approve {
				want = "approved"
			}
			if result["state"] != want {
				t.Fatal("decision", result)
			}
			waitAgentChat(t, f, "completed")
			var after string
			if err := f.store.ReadDB().QueryRow("SELECT value FROM site_config WHERE key='site_name'").Scan(&after); err != nil {
				t.Fatal(err)
			}
			if approve && after != "Assistant approved site" || !approve && after != before {
				t.Fatal("write outcome", before, after, result)
			}
			replayed := f.call("POST", path, map[string]any{"approve": true}, f.adminCookie, true)
			if replayed["state"] != want {
				t.Fatal("replayed approval", replayed)
			}
			if approve {
				var count int
				if err := f.store.ReadDB().QueryRow("SELECT COUNT(*) FROM management_operations WHERE correlation_kind='ai_plan' AND correlation_id=? AND source='ai_manual'", plan["id"]).Scan(&count); err != nil || count != 1 {
					t.Fatal("audit provenance", count, err)
				}
			}
		})
	}
}
