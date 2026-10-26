package aiagent

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestPlanHistorySurvivesNextTurnAndKeepsEditedPrefix(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	j, id := f.plan(t, []Operation{{Kind: "write", TargetID: "one", Input: json.RawMessage(`{}`)}})
	if _, err := f.s.Decide(ctx, f.actor, id, true, "已核对"); err != nil {
		t.Fatal(err)
	}
	if err := f.s.run(ctx, j.ID); err != nil {
		t.Fatal(err)
	}
	second := f.send(t, "下一条用户消息", "n")
	if err := f.s.run(ctx, second.ID); err != nil {
		t.Fatal(err)
	}
	c, err := f.s.Conversation(ctx, f.actor)
	if err != nil || len(c.Plans) != 1 || c.Plans[0].ID != id || c.Plans[0].State != "approved" || len(c.Plans[0].ToolCallIDs) != 1 {
		t.Fatalf("earlier plan missing: %+v %v", c.Plans, err)
	}
	var messageID int64
	for _, m := range c.Messages {
		if m.Content == "下一条用户消息" {
			messageID = m.ID
		}
	}
	if _, err = f.s.Edit(ctx, f.actor, messageID, SendInput{Content: "修改下一条", ExpectedGeneration: c.Generation, RequestKey: "edit-history-message-01"}); err != nil {
		t.Fatal(err)
	}
	p, err := f.s.Plans(ctx, f.actor, f.actor.ID, 0, false)
	if err != nil || len(p.Items) != 1 || p.Items[0].ID != id {
		t.Fatalf("edit removed retained prefix plan: %+v %v", p, err)
	}
	c, err = f.s.Conversation(ctx, f.actor)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.s.Clear(ctx, f.actor, c.Generation); err != nil {
		t.Fatal(err)
	}
	p, err = f.s.Plans(ctx, f.actor, f.actor.ID, 0, false)
	if err != nil || len(p.Items) != 0 {
		t.Fatalf("cleared plans reappeared: %+v %v", p, err)
	}
	p, err = f.s.Plans(ctx, f.actor, f.actor.ID, 0, true)
	if err != nil || len(p.Items) != 1 || p.Items[0].ID != id {
		t.Fatalf("archive plan lost: %+v %v", p, err)
	}
	if count(t, f.db, "test_mutations") != 1 {
		t.Fatal("editing reversed or repeated an executed action")
	}
}

func TestStewardPlanReceiptCannotReadArchivedConversation(t *testing.T) {
	f := newFixture(t)
	f.actor.Role = "level5"
	ctx := context.Background()
	_, id := f.plan(t, []Operation{{Kind: "write", TargetID: "one", Input: json.RawMessage(`{}`)}})
	if _, err := f.s.Decide(ctx, f.actor, id, true, ""); err != nil {
		t.Fatal(err)
	}
	if result, err := f.s.Decide(ctx, f.actor, id, true, ""); err != nil || result.State != "approved" {
		t.Fatal("active receipt recovery failed", result, err)
	}
	c, err := f.s.Conversation(ctx, f.actor)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.s.Clear(ctx, f.actor, c.Generation); err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.Decide(ctx, f.actor, id, true, ""); !errors.Is(err, ErrForbidden) {
		t.Fatal("archived receipt exposed to steward", err)
	}
	if count(t, f.db, "test_mutations") != 1 {
		t.Fatal("receipt recovery repeated a write")
	}
}
