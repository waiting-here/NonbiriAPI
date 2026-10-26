package aiagent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/agentrequest"
)

func TestCompactCommandDispatchAndContinuation(t *testing.T) {
	for _, edit := range []bool{false, true} {
		t.Run(map[bool]string{false: "send", true: "edit"}[edit], func(t *testing.T) {
			f := newFixture(t)
			ctx := context.Background()
			job := f.send(t, "Review donation 42 without executing any plan.", "before")
			if err := f.s.run(ctx, job.ID); err != nil {
				t.Fatal(err)
			}
			command := "  /compact\n保留待确认操作  \n"
			if edit {
				job = f.send(t, "Replace this draft", "draft")
				c, err := f.s.Conversation(ctx, f.actor)
				if err != nil {
					t.Fatal(err)
				}
				job, err = f.s.Edit(ctx, f.actor, c.Messages[len(c.Messages)-1].ID, SendInput{Content: command, ExpectedGeneration: c.Generation, RequestKey: "edit-compact-request-key"})
				if err != nil {
					t.Fatal(err)
				}
			} else {
				job = f.send(t, command, "c")
			}
			if job.Kind != "compact" {
				t.Fatalf("command dispatched as %q", job.Kind)
			}
			before := count(t, f.db, "ai_agent_messages")
			if err := f.s.run(ctx, job.ID); err != nil {
				t.Fatal(err)
			}
			if count(t, f.db, "ai_agent_messages") != before {
				t.Fatal("fabricated assistant receipt")
			}
			request := f.driver.inputs[len(f.driver.inputs)-1]
			if request.Kind != "compaction" || len(request.Tools) != 0 || len(request.ResponseFormat) != 0 {
				t.Fatalf("not a dedicated summary request: %+v", request)
			}
			if !strings.Contains(string(request.Messages), "保留待确认操作") || !strings.Contains(string(request.Messages), "donation 42") || strings.Contains(string(request.Messages), "/compact") {
				t.Fatalf("incorrect summary input: %s", request.Messages)
			}
			c, err := f.s.Conversation(ctx, f.actor)
			if err != nil || c.Messages[len(c.Messages)-1].Content != command || c.Summary == "" {
				t.Fatalf("command history/summary missing: %+v, %v", c, err)
			}
			job = f.send(t, "Continue with that review", "after")
			if err := f.s.run(ctx, job.ID); err != nil {
				t.Fatal(err)
			}
			request = f.driver.inputs[len(f.driver.inputs)-1]
			if request.Kind != "model" || !strings.Contains(string(request.Messages), "此前对话摘要") {
				t.Fatalf("command leaked into continuation: %s", request.Messages)
			}
			var sent []assistantMessage
			if err := json.Unmarshal(request.Messages, &sent); err != nil {
				t.Fatal(err)
			}
			for _, item := range sent {
				if _, command := compactInstructions(messageText(item.Content)); item.Role == "user" && command {
					t.Fatal("command leaked into user context")
				}
			}
		})
	}
}

func TestCompactEmptyAndCommandGrammar(t *testing.T) {
	for _, content := range []string{"/compact", " /compact\tkeep IDs ", "/compact\nkeep IDs"} {
		if _, ok := compactInstructions(content); !ok {
			t.Fatal(content)
		}
	}
	for _, content := range []string{"/compactly", "explain /compact", "`/compact`"} {
		if _, ok := compactInstructions(content); ok {
			t.Fatal(content)
		}
	}
	f := newFixture(t)
	job := f.send(t, "/compact", "empty")
	if err := f.s.run(context.Background(), job.ID); err != nil {
		t.Fatal(err)
	}
	if len(f.driver.inputs) != 0 || count(t, f.db, "ai_agent_messages") != 1 {
		t.Fatal("empty compaction sent a model request or generated a reply")
	}
}

func TestCompactionChunksAndRejectsIncompleteSummary(t *testing.T) {
	for _, finish := range []string{"stop", "length", "content_filter", ""} {
		t.Run("finish_"+finish, func(t *testing.T) {
			f := newFixture(t)
			ctx := context.Background()
			job := f.send(t, strings.Repeat("资源42: pending; ", 4500), "long")
			if _, err := f.db.Exec(`UPDATE ai_agent_jobs SET state='completed' WHERE id=?`, job.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := f.db.Exec(`UPDATE ai_agent_conversations SET summary='existing summary' WHERE id=?`, job.ConversationID); err != nil {
				t.Fatal(err)
			}
			job = f.send(t, "/compact keep permissions", "chunk")
			_, original, _, _, readErr := f.s.contextMessages(ctx, job, defaultSettings())
			if readErr != nil {
				t.Fatal(readErr)
			}
			originalWire := []json.RawMessage{}
			for _, message := range original {
				originalWire = append(originalWire, wireMessage(message))
			}
			originalJSON, _ := json.Marshal(originalWire)
			f.driver.finishReasons = []string{"stop", finish}
			before := count(t, f.db, "ai_agent_messages")
			err := f.s.run(ctx, job.ID)
			if (finish == "stop") != (err == nil) {
				t.Fatalf("unexpected completion: %v", err)
			}
			c, readErr := f.s.Conversation(ctx, f.actor)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if finish != "stop" && c.Summary != "existing summary" {
				t.Fatal("partial summary replaced current summary")
			}
			if count(t, f.db, "ai_agent_messages") != before {
				t.Fatal("original messages changed")
			}
			if len(f.driver.inputs) < 2 {
				t.Fatal("large summary was not chunked")
			}
			var fragments strings.Builder
			for _, in := range f.driver.inputs {
				if in.Kind != "compaction" || estimatedTokens(in.Messages) > 6452 || !utf8.Valid(in.Messages) {
					t.Fatalf("unbounded/invalid summary request: %d", estimatedTokens(in.Messages))
				}
				var prompt []map[string]string
				if err := json.Unmarshal(in.Messages, &prompt); err != nil {
					t.Fatal(err)
				}
				_, source, found := strings.Cut(prompt[1]["content"], "待总结原文（连续片段）：\n")
				if !found || !utf8.ValidString(source) {
					t.Fatal("invalid source fragment")
				}
				fragments.WriteString(source)
			}
			if finish == "stop" && fragments.String() != string(originalJSON) {
				t.Fatal("fragmented summary omitted or replaced original bytes")
			}
			var through int64
			if err := f.db.QueryRow(`SELECT summary_through_id FROM ai_agent_conversations WHERE id=?`, job.ConversationID).Scan(&through); err != nil {
				t.Fatal(err)
			}
			if finish != "stop" && through != 0 {
				t.Fatal("partial summary discarded active original context")
			}
		})
	}
}

func TestCompactionLateResultAfterCancelOrEdit(t *testing.T) {
	for _, edit := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancel", true: "edit"}[edit], func(t *testing.T) {
			f := newFixture(t)
			ctx := context.Background()
			job := f.send(t, strings.Repeat("retained original ", 4000), "old")
			if _, err := f.db.Exec(`UPDATE ai_agent_jobs SET state='completed' WHERE id=?`, job.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := f.db.Exec(`UPDATE ai_agent_conversations SET summary='existing' WHERE id=?`, job.ConversationID); err != nil {
				t.Fatal(err)
			}
			job = f.send(t, "/compact", "c")
			c, err := f.s.Conversation(ctx, f.actor)
			if err != nil {
				t.Fatal(err)
			}
			f.driver.afterWait = func(id int64) {
				if id != 2 {
					return
				}
				if edit {
					_, err = f.s.Edit(ctx, f.actor, c.Messages[len(c.Messages)-1].ID, SendInput{Content: "Revised request", ExpectedGeneration: c.Generation, RequestKey: "edit-during-compaction"})
				} else {
					err = f.s.Cancel(ctx, f.actor, job.ID)
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := f.s.run(ctx, job.ID); !errors.Is(err, ErrConflict) {
				t.Fatalf("late result accepted: %v", err)
			}
			var summary string
			var through int64
			if err := f.db.QueryRow(`SELECT summary,summary_through_id FROM ai_agent_conversations WHERE id=?`, job.ConversationID).Scan(&summary, &through); err != nil {
				t.Fatal(err)
			}
			if summary != "existing" || through != 0 {
				t.Fatal("late summary replaced active context")
			}
		})
	}
}

func TestAutomaticCompactionIncludesToolsAndUsesSummaryPipeline(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	job := f.send(t, strings.Repeat("o", 8000), "old")
	f.driver.replies = []string{`{"role":"assistant","content":"` + strings.Repeat("r", 10000) + `"}`}
	if err := f.s.run(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	job = f.send(t, "continue", "c")
	if _, err := f.db.Exec(`UPDATE ai_agent_jobs SET state='running' WHERE id=?`, job.ID); err != nil {
		t.Fatal(err)
	}
	settings := defaultSettings()
	settings.ChatTemplate = []TemplateMessage{{Role: "user", Content: "{{conversation}}"}}
	settings.ChatPrefill = ""
	tools, _ := json.Marshal([]map[string]string{{"description": strings.Repeat("tool", 5000)}})
	messages, err := f.s.compactIfNeeded(ctx, job, f.actor, settings, tools, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.driver.inputs) != 3 || f.driver.inputs[1].Kind != "compaction" || f.driver.inputs[2].Kind != "compaction" || len(f.driver.inputs[1].Tools) != 0 {
		t.Fatalf("tools omitted from compaction threshold: %+v", f.driver.inputs)
	}
	raw, _ := json.Marshal(messages)
	if !strings.Contains(string(raw), "此前对话摘要") || !strings.Contains(string(raw), "continue") {
		t.Fatalf("continuation lost: %s", raw)
	}
	if estimatedTokens(raw)+estimatedTokens(tools) >= 8192-1024 {
		t.Fatal("compacted context still over window")
	}
}

func TestCompactionNoInputBudgetPreservesOriginal(t *testing.T) {
	f := newFixture(t)
	job := f.send(t, "remember this", "o")
	if err := f.s.run(context.Background(), job.ID); err != nil {
		t.Fatal(err)
	}
	f.driver.settings = &agentrequest.Settings{ContextWindow: 1024, MaxOutputTokens: 1023}
	job = f.send(t, "/compact", "n")
	if err := f.s.run(context.Background(), job.ID); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if len(f.driver.inputs) != 1 {
		t.Fatal("sent an impossible summary request")
	}
}
