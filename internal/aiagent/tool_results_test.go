package aiagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/agentplan"
	"github.com/waiting-here/NonbiriAPI/internal/agentrequest"
)

type resultBudgetOperations struct {
	Operations
	read         func(json.RawMessage) (json.RawMessage, error)
	intelligence func(int64) (json.RawMessage, string, error)
	preview      func([]Operation) ([]OperationPreview, error)
}

func (o resultBudgetOperations) Preview(ctx context.Context, actor Actor, operations []Operation) ([]OperationPreview, error) {
	if o.preview != nil {
		return o.preview(operations)
	}
	return o.Operations.Preview(ctx, actor, operations)
}

func (o resultBudgetOperations) Read(_ context.Context, _ Actor, _ string, args json.RawMessage) (json.RawMessage, error) {
	return o.read(args)
}

func (o resultBudgetOperations) Intelligence(ctx context.Context, id int64) (json.RawMessage, string, error) {
	if o.intelligence != nil {
		return o.intelligence(id)
	}
	return o.Operations.Intelligence(ctx, id)
}

func resultBudgetCall(id, args string) toolCall {
	call := toolCall{ID: id, Type: "function"}
	call.Function.Name, call.Function.Arguments = "donations", args
	return call
}

func resultBudgetAssistant(calls ...toolCall) string {
	raw, _ := json.Marshal(map[string]any{"role": "assistant", "content": "", "tool_calls": calls})
	return string(raw)
}

func resultBudgetReview(t *testing.T, f fixture, intelligence string) Job {
	t.Helper()
	settings := defaultSettings()
	settings.Mode = "preview"
	if _, err := f.s.PutSettings(t.Context(), f.actor, SettingsUpdate{Settings: settings}); err != nil {
		t.Fatal(err)
	}
	result, err := f.db.Exec(`INSERT INTO ai_agent_jobs(kind,actor_id,actor_role,actor_label,donation_id,revision_signature,mode,state,intelligence,created_at,updated_at) VALUES('auto_review',1,'admin','review',10,'v1','preview','pending',?,0,0)`, intelligence)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := result.LastInsertId()
	job, _, err := f.s.loadJob(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	return job
}

func assertResultBudgetProtocol(t *testing.T, raw []byte) {
	t.Helper()
	var messages []struct {
		Role       string     `json:"role"`
		ToolCalls  []toolCall `json:"tool_calls"`
		ToolCallID string     `json:"tool_call_id"`
	}
	if err := json.Unmarshal(raw, &messages); err != nil {
		t.Fatal(err)
	}
	pending := map[string]bool{}
	for _, message := range messages {
		if len(pending) != 0 && message.Role != "tool" {
			t.Fatal("message inserted before all tool results", message.Role)
		}
		for _, call := range message.ToolCalls {
			pending[call.ID] = true
		}
		if message.Role == "tool" {
			if !pending[message.ToolCallID] {
				t.Fatal("orphan tool result", message.ToolCallID)
			}
			delete(pending, message.ToolCallID)
		}
	}
	if len(pending) != 0 {
		t.Fatal("missing tool results", pending)
	}
}

func TestWritePlansDoNotConsumeReadFailureBudget(t *testing.T) {
	f := newFixture(t)
	calls := []toolCall{}
	for i := range 30 {
		call := resultBudgetCall(fmt.Sprintf("write_%d", i), `{"operations":[{"kind":"write","target_id":"42","input":{}}]}`)
		call.Function.Name = "propose_operations"
		calls = append(calls, call)
	}
	f.driver.replies = []string{resultBudgetAssistant(calls...)}
	job := f.send(t, "Propose all thirty changes", "w")
	if err := f.s.run(t.Context(), job.ID); err != nil {
		t.Fatal(err)
	}
	loaded, _, err := f.s.loadJob(t.Context(), job.ID)
	if err != nil || loaded.State != "waiting_confirmation" || len(f.driver.inputs) != 1 || count(t, f.db, "ai_agent_plans") != 1 {
		t.Fatal("valid write-only plan was blocked by read budget", loaded.State, err)
	}
}

func TestWritePreviewFailureKeepsMixedRoundRecoverable(t *testing.T) {
	for _, mixed := range []bool{false, true} {
		t.Run(fmt.Sprint(mixed), func(t *testing.T) {
			f := newFixture(t)
			f.s.config.Operations = resultBudgetOperations{Operations: f.ops, read: func(json.RawMessage) (json.RawMessage, error) {
				raw, _ := json.Marshal(map[string]string{"data": strings.Repeat("x", 20000)})
				return raw, nil
			}, preview: func([]Operation) ([]OperationPreview, error) { return nil, ErrConflict }}
			calls := []toolCall{}
			if mixed {
				calls = append(calls, resultBudgetCall("read", `{}`))
			}
			for i := range 30 {
				call := resultBudgetCall(fmt.Sprintf("write_%d", i), `{"operations":[{"kind":"write","target_id":"42","input":{}}]}`)
				call.Function.Name = "propose_operations"
				calls = append(calls, call)
			}
			f.driver.replies = []string{resultBudgetAssistant(calls...), `{"role":"assistant","content":"Read current versions before retrying."}`}
			job := f.send(t, "Propose the changes", "f")
			if err := f.s.run(t.Context(), job.ID); err != nil {
				settings, _ := f.s.Settings(t.Context(), f.actor)
				messages, history, _, _, _ := f.s.contextMessages(t.Context(), job, settings)
				tools, allowed, _ := f.s.tools(t.Context(), f.actor, settings, false)
				toolJSON, _ := json.Marshal(tools)
				driver, _ := f.driver.Settings(t.Context())
				_, writes, reads, _ := collectToolWrites(calls, allowed)
				budget := newToolRoundBudget(messages, history, toolJSON, nil, driver, reads)
				budget.reservePlanFailures(writes, nil)
				t.Fatalf("%v; base_tokens=%d reserved_tokens=%d input_budget=%d", err, budget.tokens, budget.reservedTokens, budget.limit)
			}
			last := f.driver.inputs[len(f.driver.inputs)-1]
			assertResultBudgetProtocol(t, last.Messages)
			var dispatched []json.RawMessage
			driver, _ := f.driver.Settings(t.Context())
			if json.Unmarshal(last.Messages, &dispatched) != nil || !modelInputFits(dispatched, last.Tools, last.ResponseFormat, driver) {
				t.Fatal("actual failure-recovery request exceeded the configured budget")
			}
			if last.Kind != "model" || !strings.Contains(string(last.Messages), "details_tool_call_id") || !strings.Contains(string(last.Messages), "write_0") || count(t, f.db, "ai_agent_plans") != 0 {
				t.Fatal("plan failure was not delivered as complete recoverable receipts")
			}
		})
	}
}

func TestPlanFailureReservationIncludesWorstJSONEscaping(t *testing.T) {
	calls := []toolCall{resultBudgetCall("first", `{}`), resultBudgetCall("later", `{}`)}
	for _, message := range []string{strings.Repeat("<", 256), strings.Repeat("\"", 512), strings.Repeat("é", 512), strings.Repeat("界", 512), strings.Repeat("\n", 256)} {
		b := newToolRoundBudget(nil, nil, nil, nil, agentrequest.Settings{ContextWindow: 10000, MaxOutputTokens: 1000}, nil)
		b.reservePlanFailures(calls, nil)
		for _, call := range calls {
			value := planFailureReceipts(calls, errors.New(message))[call.ID]
			wire := toolMessage(call, value)
			reserved := b.reserved[call.ID]
			if toolTokenCost(wire) > reserved.tokens || len(wire)+1 > reserved.bytes || toolHistoryBytes(call, value) > reserved.history {
				t.Fatal("plan error exceeded its reservation")
			}
		}
	}
}

func TestToolResultDispatchKeepsPrefillAfterCompleteRound(t *testing.T) {
	f := newFixture(t)
	settings := defaultSettings()
	settings.ChatEnabled = true
	settings.ChatPrefill = "Start "
	settings.ChatTemplate = append(settings.ChatTemplate, TemplateMessage{Role: "user", Content: "Template tail"})
	if _, err := f.s.PutSettings(t.Context(), f.actor, SettingsUpdate{Settings: settings}); err != nil {
		t.Fatal(err)
	}
	f.driver.replies = []string{resultBudgetAssistant(resultBudgetCall("read", `{}`)), `{"role":"assistant","content":"Done"}`}
	job := f.send(t, "Read current donations", "p")
	if err := f.s.run(t.Context(), job.ID); err != nil {
		t.Fatal(err)
	}
	if len(f.driver.inputs) != 2 {
		t.Fatal("unexpected model round count", len(f.driver.inputs))
	}
	assertResultBudgetProtocol(t, f.driver.inputs[1].Messages)
}

func TestReadResultBudgetLetsChatAndReviewNarrowAndContinue(t *testing.T) {
	for _, automatic := range []bool{false, true} {
		for _, hardLimit := range []bool{false, true} {
			t.Run(fmt.Sprintf("automatic_%t_hard_%t", automatic, hardLimit), func(t *testing.T) {
				f := newFixture(t)
				f.s.config.Operations = resultBudgetOperations{Operations: f.ops, read: func(args json.RawMessage) (json.RawMessage, error) {
					if strings.Contains(string(args), "donation_id") {
						return json.RawMessage(`{"data":[{"id":"10","revision":"1"}],"total":"1"}`), nil
					}
					if hardLimit {
						return nil, &agentplan.ReadLimitError{}
					}
					raw, _ := json.Marshal(map[string]any{"data": []string{strings.Repeat("payload_marker", 4000)}, "total": "1"})
					return raw, nil
				}}
				f.driver.replies = []string{resultBudgetAssistant(resultBudgetCall("wide", `{}`)), resultBudgetAssistant(resultBudgetCall("narrow", `{"donation_id":"10"}`)), `{"role":"assistant","content":"done"}`}
				var job Job
				if automatic {
					job = resultBudgetReview(t, f, `{}`)
					outcome, _ := json.Marshal(manualSizeReview("checked current evidence"))
					last, _ := json.Marshal(map[string]string{"role": "assistant", "content": string(outcome)})
					f.driver.replies[2] = string(last)
				} else {
					job = f.send(t, "Read the matching donation", "budget")
				}
				if err := f.s.run(t.Context(), job.ID); err != nil {
					t.Fatal(err)
				}
				if len(f.driver.inputs) != 3 || !strings.Contains(string(f.driver.inputs[1].Messages), "tool_result_too_large") || strings.Contains(string(f.driver.inputs[1].Messages), "payload_marker") {
					t.Fatal("model did not receive a recoverable whole-read failure", len(f.driver.inputs))
				}
				for _, input := range f.driver.inputs[1:] {
					assertResultBudgetProtocol(t, input.Messages)
				}
				if !strings.Contains(string(f.driver.inputs[2].Messages), `\"revision\":\"1\"`) {
					t.Fatal("narrow result missing")
				}
				loaded, _, err := f.s.loadJob(t.Context(), job.ID)
				if err != nil || loaded.State != "completed" {
					t.Fatal("recoverable failure ended the job", loaded.State, err)
				}
				if !automatic {
					var saved string
					if err = f.db.QueryRow(`SELECT content FROM ai_agent_messages WHERE tool_call_id='wide'`).Scan(&saved); err != nil || !strings.Contains(saved, "tool_result_too_large") || strings.Contains(saved, "payload_marker") {
						t.Fatal("large result persisted before rejection", err)
					}
				}
			})
		}
	}
}

func TestReadResultBudgetCountsTheWholeToolRound(t *testing.T) {
	settings := agentrequest.Settings{ContextWindow: 4096, MaxOutputTokens: 512}
	calls := []toolCall{resultBudgetCall("one", `{}`), resultBudgetCall("two", `{}`)}
	value, _ := json.Marshal(map[string]string{"data": strings.Repeat("x", 7000)})
	for _, call := range calls {
		b := newToolRoundBudget(nil, nil, nil, nil, settings, []toolCall{call})
		if got := b.accept(call, value); string(got) != string(value) {
			t.Fatal("individual result should fit")
		}
	}
	b := newToolRoundBudget(nil, nil, nil, nil, settings, calls)
	first, second := b.accept(calls[0], value), b.accept(calls[1], value)
	if string(first) != string(value) || !strings.Contains(string(second), "tool_result_too_large") || strings.Contains(string(second), `"data"`) || !b.fits(0, 0, 0) {
		t.Fatal("combined results exceeded the reserved round budget")
	}
}

func TestReadResultFailureListsOnlySupportedFilters(t *testing.T) {
	for name, want := range map[string]string{"charity_binding_presets": "donation_id,channel_id,query", "review_history": "donation_id,decision", "users": "user_id,query", "donation_models": "donation_id,donation_key_id,source,query", "site_status": ""} {
		var receipt struct {
			Error struct {
				Code    string   `json:"code"`
				Filters []string `json:"filters"`
			} `json:"error"`
		}
		raw := toolResultTooLarge(name, "read_limit")
		if err := json.Unmarshal(raw, &receipt); err != nil || receipt.Error.Code != "tool_result_too_large" || strings.Join(receipt.Error.Filters, ",") != want || strings.Contains(string(raw), `"total"`) {
			t.Fatal(name, string(raw), err)
		}
	}
}

func TestToolRoundCompactsOldContextWithoutOrphanResults(t *testing.T) {
	f := newFixture(t)
	old := f.send(t, strings.Repeat("old ", 10000), "o")
	if _, err := f.db.Exec(`UPDATE ai_agent_jobs SET state='completed' WHERE id=?`, old.ID); err != nil {
		t.Fatal(err)
	}
	job := f.send(t, "Read current donations", "n")
	if _, err := f.db.Exec(`UPDATE ai_agent_jobs SET state='running' WHERE id=?`, job.ID); err != nil {
		t.Fatal(err)
	}
	calls := []toolCall{resultBudgetCall("pending_tool_one", `{}`), resultBudgetCall("pending_tool_two", `{}`)}
	raw := json.RawMessage(resultBudgetAssistant(calls...))
	var assistant assistantMessage
	if err := json.Unmarshal(raw, &assistant); err != nil {
		t.Fatal(err)
	}
	if err := f.s.persistAssistant(t.Context(), job, f.actor, raw, assistant); err != nil {
		t.Fatal(err)
	}
	settings, err := f.s.Settings(t.Context(), f.actor)
	if err != nil {
		t.Fatal(err)
	}
	_, budget, err := f.s.prepareToolRound(t.Context(), job, f.actor, settings, nil, nil, nil, calls, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.driver.inputs) == 0 {
		t.Fatal("old context was not compacted")
	}
	for _, input := range f.driver.inputs {
		if input.Kind != "compaction" || strings.Contains(string(input.Messages), "pending_tool_") {
			t.Fatal("pending tool call was summarized before its results")
		}
	}
	for _, call := range calls {
		value := budget.accept(call, json.RawMessage(`{"data":[],"total":"0"}`))
		if err := f.s.persistTool(t.Context(), job, f.actor, call.ID, value); err != nil {
			t.Fatal(err)
		}
	}
	messages, _, _, _, err := f.s.contextMessages(t.Context(), job, settings)
	if err != nil {
		t.Fatal(err)
	}
	wire, _ := json.Marshal(messages)
	assertResultBudgetProtocol(t, wire)
}

func TestOversizedInitialReviewTransfersToManualWithoutDroppingCatalog(t *testing.T) {
	f := newFixture(t)
	intelligence := `{"models":["` + strings.Repeat("complete-catalog", 4000) + `"]}`
	job := resultBudgetReview(t, f, intelligence)
	if err := f.s.run(t.Context(), job.ID); err != nil {
		t.Fatal(err)
	}
	var state, saved, result, execution string
	if err := f.db.QueryRow(`SELECT state,intelligence,review_result,execution FROM ai_agent_jobs WHERE id=?`, job.ID).Scan(&state, &saved, &result, &execution); err != nil {
		t.Fatal(err)
	}
	if state != "completed" || saved != intelligence || !strings.Contains(result, `"decision":"manual"`) || !strings.Contains(execution, `"status":"manual"`) || len(f.driver.inputs) != 0 || count(t, f.db, "test_mutations") != 0 {
		t.Fatal("large initial review lost evidence or reached a model")
	}
}

func TestHardLimitedReviewEventDoesNotBlockFollowingSubmission(t *testing.T) {
	f := newFixture(t)
	settings := defaultSettings()
	settings.Mode = "preview"
	if _, err := f.s.PutSettings(t.Context(), f.actor, SettingsUpdate{Settings: settings}); err != nil {
		t.Fatal(err)
	}
	f.s.config.Operations = resultBudgetOperations{Operations: f.ops, intelligence: func(id int64) (json.RawMessage, string, error) {
		if id == 10 {
			return nil, "v1", &agentplan.ReadLimitError{}
		}
		return json.RawMessage(`{}`), "v1", nil
	}}
	if _, err := f.db.Exec(`INSERT INTO donations(id) VALUES(11); INSERT INTO ai_agent_review_events(donation_id,sequence,mode,created_at,updated_at) VALUES(10,1,'preview',0,0),(11,1,'preview',0,0)`); err != nil {
		t.Fatal(err)
	}
	if err := f.s.collectReviewEvents(t.Context()); err != nil {
		t.Fatal(err)
	}
	var state, result, intel string
	if err := f.db.QueryRow(`SELECT state,review_result,intelligence FROM ai_agent_jobs WHERE donation_id=10`).Scan(&state, &result, &intel); err != nil || state != "completed" || !strings.Contains(result, `"decision":"manual"`) || !strings.Contains(intel, "tool_result_too_large") {
		t.Fatal("limited event missing manual receipt", state, err)
	}
	if err := f.db.QueryRow(`SELECT state FROM ai_agent_jobs WHERE donation_id=11`).Scan(&state); err != nil || state != "pending" || count(t, f.db, "ai_agent_review_events") != 0 || len(f.driver.inputs) != 0 {
		t.Fatal("following submission was blocked", state, err)
	}
}

func TestReviewWithoutRoomForToolReceiptsTransfersToManual(t *testing.T) {
	f := newFixture(t)
	reads := 0
	f.s.config.Operations = resultBudgetOperations{Operations: f.ops, read: func(json.RawMessage) (json.RawMessage, error) {
		reads++
		return json.RawMessage(`{}`), nil
	}}
	calls := []toolCall{}
	for i := range 30 {
		calls = append(calls, resultBudgetCall(fmt.Sprintf("call_%d", i), `{}`))
	}
	f.driver.replies = []string{resultBudgetAssistant(calls...)}
	job := resultBudgetReview(t, f, `{}`)
	if err := f.s.run(t.Context(), job.ID); err != nil {
		t.Fatal(err)
	}
	var state, result string
	if err := f.db.QueryRow(`SELECT state,review_result FROM ai_agent_jobs WHERE id=?`, job.ID).Scan(&state, &result); err != nil || state != "completed" || !strings.Contains(result, "manual") || !strings.Contains(result, "失败回执") || len(f.driver.inputs) != 1 || reads != 0 {
		t.Fatal("review continued without room for tool receipts", state, reads, err)
	}
}
