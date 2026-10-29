package app

import (
	"context"
	"fmt"
	"strconv"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/aiagent"
)

func TestAgentPlanAuditUsesReadOnlyPreflightAndWriterCapture(t *testing.T) {
	for _, approve := range []bool{true, false} {
		t.Run(fmt.Sprintf("approve=%t", approve), func(t *testing.T) {
			f, replies := newAgentWireFixture(t)
			configureAgentDriver(t, f)
			configureAgentWorkflow(t, f, "off", true)
			before := f.call("GET", "/admin/api/ai/memories/chat", nil, f.adminCookie, true)
			replies <- map[string]any{"role": "assistant", "content": "请确认记忆修改。", "tool_calls": []any{
				agentToolCall(t, "audit-memory", "edit_memory", map[string]any{"action": "write", "kind": "chat", "content": "synthetic confirmed memory", "expected_revision": before["revision"]}),
			}}
			f.call("POST", "/admin/api/ai/chat/messages", map[string]any{"content": "更新记忆", "expected_generation": "0", "request_key": "real-plan-audit-preflight-01"}, f.adminCookie, true)
			chat := waitAgentChat(t, f, "waiting_confirmation")
			plan := chat["plans"].([]any)[0].(map[string]any)
			planID, err := strconv.ParseInt(plan["id"].(string), 10, 64)
			if err != nil {
				t.Fatal(err)
			}
			var actor aiagent.Actor
			if err := f.store.ReadDB().QueryRow("SELECT id,username FROM users WHERE is_admin=1").Scan(&actor.ID, &actor.Label); err != nil {
				t.Fatal(err)
			}
			actor.Role = "admin"
			if err := f.store.ReadDB().QueryRow("SELECT token_hash,cred_gen FROM sessions WHERE user_id=?", actor.ID).Scan(&actor.SessionHash, &actor.SessionGeneration); err != nil {
				t.Fatal(err)
			}
			// Call the real service to expose database failures hidden by the HTTP envelope.
			decided, err := f.app.Load().agent.Decide(context.Background(), actor, planID, approve, "")
			if err != nil {
				t.Fatalf("confirm plan through read-only preflight: %v", err)
			}
			wantState, wantOutcome, wantSource, wantExecutor := "rejected", "rejected", "web", "human"
			if approve {
				wantState, wantOutcome, wantSource, wantExecutor = "approved", "succeeded", "ai_manual", "ai"
			}
			if decided.State != wantState {
				t.Fatalf("plan state=%s want=%s", decided.State, wantState)
			}
			after := f.call("GET", "/admin/api/ai/memories/chat", nil, f.adminCookie, true)
			if approve && after["content"] != "synthetic confirmed memory" || !approve && (after["content"] != before["content"] || after["revision"] != before["revision"]) {
				t.Fatalf("memory after decision: before=%v after=%v", before, after)
			}
			var source, executor, actorID, authorizerID, outcome string
			var operationID int64
			if err := f.store.ReadDB().QueryRow("SELECT id,source,executor_kind,actor_id,authorizer_id,outcome FROM management_operations WHERE correlation_kind='ai_plan' AND correlation_id=?", plan["id"]).Scan(&operationID, &source, &executor, &actorID, &authorizerID, &outcome); err != nil {
				t.Fatal(err)
			}
			if source != wantSource || executor != wantExecutor || outcome != wantOutcome {
				t.Fatalf("audit source=%s executor=%s outcome=%s", source, executor, outcome)
			}
			humanID := strconv.FormatInt(actor.ID, 10)
			if approve && (actorID != "ai" || authorizerID != humanID) || !approve && (actorID != humanID || authorizerID != "") {
				t.Fatalf("audit actor=%s authorizer=%s", actorID, authorizerID)
			}
			if approve {
				var captured int
				if err := f.store.ReadDB().QueryRow("SELECT COUNT(*) FROM management_operation_items WHERE operation_id=? AND target_kind='ai_memory' AND target_id='chat' AND json_extract(details,'$.state') IN ('created','updated')", operationID).Scan(&captured); err != nil || captured != 1 {
					t.Fatalf("committed memory capture=%d err=%v", captured, err)
				}
			}
			if _, err := f.app.Load().agent.Decide(context.Background(), actor, planID, approve, "replay"); err != nil {
				t.Fatal(err)
			}
			var count int
			if err := f.store.ReadDB().QueryRow("SELECT COUNT(*) FROM management_operations WHERE correlation_kind='ai_plan' AND correlation_id=?", plan["id"]).Scan(&count); err != nil || count != 1 {
				t.Fatalf("replayed audit count=%d err=%v", count, err)
			}
			for _, table := range []string{"management_capture_context", "management_capture_rows", "management_capture_totals"} {
				if err := f.store.ReadDB().QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil || count != 0 {
					t.Fatalf("capture left in %s: count=%d err=%v", table, count, err)
				}
			}
		})
	}
}
