package aiagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/agentrequest"
)

func TestSendReplayBindsOriginalContent(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	in := SendInput{Content: "查询待审捐赠", RequestKey: "same-request-0001"}
	first, err := f.s.Send(ctx, f.actor, in)
	if err != nil {
		t.Fatal(err)
	}
	second, err := f.s.Send(ctx, f.actor, in)
	if err != nil || first.ID != second.ID {
		t.Fatalf("replay=%+v %v", second, err)
	}
	in.Content = "删除一个用户"
	if _, err = f.s.Send(ctx, f.actor, in); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed input: %v", err)
	}
	if n := count(t, f.db, "ai_agent_messages"); n != 1 {
		t.Fatalf("messages=%d", n)
	}
}

func TestEditReplayRecoversLostResponseAndBindsMessage(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	first := f.send(t, "原始问题", "x")
	c, err := f.s.Conversation(ctx, f.actor)
	if err != nil {
		t.Fatal(err)
	}
	input := SendInput{Content: "修改后的问题", ExpectedGeneration: c.Generation, RequestKey: "edit-response-lost-01"}
	edited, err := f.s.Edit(ctx, f.actor, c.Messages[0].ID, input)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := f.s.Edit(ctx, f.actor, c.Messages[0].ID, input)
	if err != nil || replayed.ID != edited.ID || replayed.Generation != first.Generation+1 {
		t.Fatalf("edit replay=%+v err=%v", replayed, err)
	}
	if count(t, f.db, "ai_agent_jobs") != 2 || count(t, f.db, "ai_agent_messages") != 2 {
		t.Fatal("edit was applied twice")
	}
	if _, err = f.s.Edit(ctx, f.actor, c.Messages[0].ID+1, input); !errors.Is(err, ErrConflict) {
		t.Fatal("changed message replay", err)
	}
	input.Content = "不同的修改"
	if _, err = f.s.Edit(ctx, f.actor, c.Messages[0].ID, input); !errors.Is(err, ErrConflict) {
		t.Fatal("changed content replay", err)
	}
	input.Content = "修改后的问题"
	if _, err = f.s.Send(ctx, f.actor, input); !errors.Is(err, ErrConflict) {
		t.Fatal("edit key reused for send", err)
	}
}

func TestCancelledToolTurnCanContinue(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	job := f.send(t, "读取捐赠", "c")
	if _, err := f.db.Exec(`UPDATE ai_agent_jobs SET state='running' WHERE id=?`, job.ID); err != nil {
		t.Fatal(err)
	}
	raw := json.RawMessage(`{"role":"assistant","content":null,"tool_calls":[{"id":"read_1","type":"function","function":{"name":"donations","arguments":"{}"}}]}`)
	var assistant assistantMessage
	if err := json.Unmarshal(raw, &assistant); err != nil {
		t.Fatal(err)
	}
	if err := f.s.persistAssistant(ctx, job, f.actor, raw, assistant); err != nil {
		t.Fatal(err)
	}
	if err := f.s.Cancel(ctx, f.actor, job.ID); err != nil {
		t.Fatal(err)
	}
	f.send(t, "继续查询", "d")
	c, err := f.s.Conversation(ctx, f.actor)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Messages) != 4 || c.Messages[2].Role != "tool" || c.Messages[2].ToolCallID != "read_1" || c.Messages[3].Role != "user" {
		t.Fatalf("invalid resumed messages: %+v", c.Messages)
	}
}

func TestRecoverAcceptedCallWithoutJobReference(t *testing.T) {
	f := newFixture(t)
	job := f.send(t, "查询", "r")
	if _, err := f.db.Exec(`UPDATE ai_agent_jobs SET state='interrupted',request_sequence=1 WHERE id=?`, job.ID); err != nil {
		t.Fatal(err)
	}
	f.driver.unconsumed = []agentrequest.Request{{ID: 7, Attribution: agentrequest.Attribution{ClientRequestID: fmt.Sprintf("job:%d:1", job.ID), ActorID: f.actor.ID, Kind: "model"}, State: "succeeded"}}
	f.s.recoverResults(context.Background())
	if n := count(t, f.db, "ai_agent_model_results"); n != 1 {
		t.Fatalf("results=%d", n)
	}
	if len(f.driver.acked) != 1 || f.driver.acked[0] != 7 {
		t.Fatalf("acks=%v", f.driver.acked)
	}
}

func TestRecoveryDoesNotAcknowledgeLiveWorkerResult(t *testing.T) {
	f := newFixture(t)
	job := f.send(t, "查询", "r")
	if _, err := f.db.Exec(`UPDATE ai_agent_jobs SET state='running',request_sequence=1 WHERE id=?`, job.ID); err != nil {
		t.Fatal(err)
	}
	f.driver.unconsumed = []agentrequest.Request{{ID: 7, Attribution: agentrequest.Attribution{ClientRequestID: fmt.Sprintf("job:%d:1", job.ID), ActorID: f.actor.ID, Kind: "model"}, State: "succeeded"}}
	f.s.recoverResults(context.Background())
	if len(f.driver.acked) != 0 {
		t.Fatalf("active request acknowledged before Wait consumed it: %v", f.driver.acked)
	}
}

func TestChangedMemoryRollsBackBusinessOperations(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	_, plan := f.plan(t, []Operation{{Kind: "change", TargetID: "virtual-user", Input: json.RawMessage(`{}`)}, {Kind: "memory.edit", TargetID: "chat", Input: json.RawMessage(`{"kind":"chat","content":"模型提议","expected_revision":"0"}`)}})
	if _, err := f.s.PutMemory(ctx, f.actor, "chat", "人类后来修改", 0); err != nil {
		t.Fatal(err)
	}
	result, err := f.s.Decide(ctx, f.actor, plan, true, "")
	if err != nil || result.State != "failed" {
		t.Fatalf("decision=%+v %v", result, err)
	}
	if n := count(t, f.db, "test_mutations"); n != 0 {
		t.Fatalf("partial changes=%d", n)
	}
	memory, err := f.s.Memory(ctx, f.actor, "chat")
	if err != nil || memory.Content != "人类后来修改" {
		t.Fatalf("memory=%+v %v", memory, err)
	}
}

func TestAssistantPrefillKeepsProviderFields(t *testing.T) {
	input := []json.RawMessage{json.RawMessage(`{"role":"assistant","content":"{\"decision\":"}`)}
	out := completePrefill(input, json.RawMessage(`{"role":"assistant","content":"\"manual\"}","reasoning_content":"context"}`))
	var wire map[string]json.RawMessage
	if err := json.Unmarshal(out, &wire); err != nil {
		t.Fatal(err)
	}
	if messageText(wire["content"]) != `{"decision":"manual"}` || messageText(wire["reasoning_content"]) != "context" {
		t.Fatalf("response=%s", out)
	}
}
