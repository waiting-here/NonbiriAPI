package aiagent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/agentrequest"
)

func TestDriverSelectionAndNativeHistoryThroughChatToolsAndCompaction(t *testing.T) {
	for _, role := range []string{"admin", "level5", "level6"} {
		t.Run(role, func(t *testing.T) {
			f := newFixture(t)
			f.actor = Actor{ID: 17, Role: role, Label: "interlocutor"}
			f.driver.settings = &agentrequest.Settings{Provider: agentrequest.ProviderChatGPT, ContextWindow: 8192, OutputReserveTokens: 1024}
			call := resultBudgetCall("native-call", `{}`)
			var native map[string]any
			if err := json.Unmarshal([]byte(resultBudgetAssistant(call)), &native); err != nil {
				t.Fatal(err)
			}
			native["responses_provider"] = "connection-identity"
			native["responses_output"] = []any{
				map[string]any{"type": "reasoning", "id": "reason-1", "encrypted_content": "opaque-reasoning"},
				map[string]any{"type": "function_call", "id": "function-1", "call_id": call.ID, "name": call.Function.Name, "arguments": call.Function.Arguments},
			}
			encoded, _ := json.Marshal(native)
			f.driver.replies = []string{string(encoded), `{"role":"assistant","content":"The requested records were checked."}`}
			job := f.send(t, "Inspect the records", "native")
			if err := f.s.run(t.Context(), job.ID); err != nil {
				t.Fatal(err)
			}
			if len(f.driver.inputs) != 2 || !strings.Contains(string(f.driver.inputs[1].Messages), "opaque-reasoning") || !strings.Contains(string(f.driver.inputs[1].Messages), "native-call") {
				t.Fatal("native tool round was not replayed")
			}
			var persisted string
			if err := f.db.QueryRow(`SELECT wire_json FROM ai_agent_messages WHERE conversation_id=? AND role='assistant' ORDER BY id LIMIT 1`, job.ConversationID).Scan(&persisted); err != nil || persisted != string(encoded) {
				t.Fatal("native message was changed in storage", err)
			}
			job = f.send(t, "Continue the conversation", "next")
			if err := f.s.run(t.Context(), job.ID); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(f.driver.inputs[2].Messages), "opaque-reasoning") {
				t.Fatal("native history was lost between jobs")
			}
			job = f.send(t, "/compact Preserve the inspected record", "short")
			if err := f.s.run(t.Context(), job.ID); err != nil {
				t.Fatal(err)
			}
			last := f.driver.inputs[len(f.driver.inputs)-1]
			if last.Kind != "compaction" || strings.Contains(string(last.Messages), "opaque-reasoning") || strings.Contains(string(last.Messages), "responses_provider") || !strings.Contains(string(last.Messages), "native-call") {
				t.Fatal("summary must quote compatible content and tool calls only")
			}
			source := "steward_chat"
			if role == "admin" {
				source = "admin_chat"
			}
			compactionResolved := false
			for _, attr := range append(append([]agentrequest.Attribution{}, f.driver.resolved...), f.driver.readiness...) {
				if attr.Source != source || attr.ActorID != f.actor.ID || attr.ActorRole != role || attr.ActorLabel != f.actor.Label {
					t.Fatalf("wrong resolved interlocutor: %+v", attr)
				}
				compactionResolved = compactionResolved || attr.Kind == "compaction"
			}
			if !compactionResolved || len(f.driver.readiness) == 0 {
				t.Fatal("readiness or summary settings bypassed selection")
			}
			for _, in := range f.driver.inputs {
				if in.Source != source || in.ActorID != f.actor.ID {
					t.Fatalf("dispatch selected another purpose: %+v", in.Attribution)
				}
			}
		})
	}
}

func TestAutomaticReviewResolvesItsPurposeForTools(t *testing.T) {
	f := newFixture(t)
	settings := defaultSettings()
	settings.Mode = "preview"
	if _, err := f.s.PutSettings(t.Context(), f.actor, SettingsUpdate{Settings: settings}); err != nil {
		t.Fatal(err)
	}
	result, err := f.db.Exec(`INSERT INTO ai_agent_jobs(kind,actor_id,actor_role,actor_label,donation_id,revision_signature,mode,state,intelligence,created_at,updated_at) VALUES('auto_review',1,'admin','review owner',10,'v1','preview','pending','{}',0,0)`)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := result.LastInsertId()
	manual, _ := json.Marshal(map[string]string{"role": "assistant", "content": `{"decision":"manual","internal_reason":"Needs review","user_note":"Please wait","bindings":[]}`})
	f.driver.replies = []string{resultBudgetAssistant(resultBudgetCall("review-read", `{}`)), string(manual)}
	if err := f.s.run(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	if len(f.driver.inputs) != 2 || len(f.driver.resolved) < 3 {
		t.Fatal("review did not budget both requests and the tool round")
	}
	for _, attr := range f.driver.resolved {
		if attr.Source != "auto_review" || attr.Kind != "model" || attr.ActorID != 1 {
			t.Fatal("review settings used chat defaults", attr)
		}
	}
	for _, in := range f.driver.inputs {
		if in.Source != "auto_review" || len(in.ResponseFormat) == 0 {
			t.Fatal("review lost its purpose or structured output")
		}
	}
}

func TestSubscriptionBudgetUsesLocalOutputReserve(t *testing.T) {
	f := newFixture(t)
	f.driver.settings = &agentrequest.Settings{Provider: agentrequest.ProviderChatGPT, ContextWindow: 2048, OutputReserveTokens: 1024}
	job := f.send(t, strings.Repeat("older transcript ", 1500), "long")
	if err := f.s.run(t.Context(), job.ID); err != nil {
		t.Fatal(err)
	}
	job = f.send(t, "/compact", "short")
	if err := f.s.run(t.Context(), job.ID); err != nil {
		t.Fatal(err)
	}
	parts := 0
	for _, in := range f.driver.inputs {
		if in.Kind == "compaction" {
			parts++
			if estimatedTokens(in.Messages) > 896 {
				t.Fatal("summary consumed the output reserve")
			}
		}
	}
	if parts < 2 {
		t.Fatal("oversized summary input was not chunked")
	}
	budget := newToolRoundBudget(nil, nil, nil, nil, *f.driver.settings, nil)
	if budget.fits(897, 1, 1) {
		t.Fatal("tool budget consumed the output reserve")
	}
}
