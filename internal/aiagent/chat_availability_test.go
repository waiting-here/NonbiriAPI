package aiagent

import (
	"context"
	"errors"
	"testing"
)

func TestChatRequiresSwitchAndConfiguredDriverWithoutHidingHistory(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	job := f.send(t, "保留的消息", "a")
	if _, err := f.db.Exec(`UPDATE ai_agent_jobs SET state='completed' WHERE id=?`, job.ID); err != nil {
		t.Fatal(err)
	}
	settings, err := f.s.Settings(ctx, f.actor)
	if err != nil {
		t.Fatal(err)
	}
	settings.ChatEnabled = false
	settings, err = f.s.PutSettings(ctx, f.actor, SettingsUpdate{Settings: settings, ExpectedRevision: settings.Revision})
	if err != nil {
		t.Fatal(err)
	}
	c, err := f.s.Conversation(ctx, f.actor)
	if err != nil || c.Enabled || len(c.Messages) != 1 {
		t.Fatalf("disabled history=%+v %v", c, err)
	}
	input := SendInput{Content: "/compact", ExpectedGeneration: c.Generation, RequestKey: "disabled-new-request"}
	if _, err = f.s.Send(ctx, f.actor, input); !errors.Is(err, ErrChatDisabled) {
		t.Fatal("closed send", err)
	}
	if _, err = f.s.Edit(ctx, f.actor, c.Messages[0].ID, input); !errors.Is(err, ErrChatDisabled) {
		t.Fatal("closed edit", err)
	}
	settings.ChatEnabled = true
	if _, err = f.s.PutSettings(ctx, f.actor, SettingsUpdate{Settings: settings, ExpectedRevision: settings.Revision}); err != nil {
		t.Fatal(err)
	}
	f.driver.unconfigured = true
	if _, err = f.s.Send(ctx, f.actor, input); !errors.Is(err, ErrChatDisabled) {
		t.Fatal("unconfigured send", err)
	}
	c, err = f.s.Conversation(ctx, f.actor)
	if err != nil || c.Enabled {
		t.Fatal("unconfigured availability", c, err)
	}
	f.driver.unconfigured = false
	if _, err = f.s.Send(ctx, f.actor, input); err != nil {
		t.Fatal("configured open send", err)
	}
}

func TestChatIsClosedByDefaultAndClosingInvalidatesPlans(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	_, id := f.plan(t, []Operation{{Kind: "write", TargetID: "unused", Input: []byte(`{}`)}})
	settings, err := f.s.Settings(ctx, f.actor)
	if err != nil {
		t.Fatal(err)
	}
	settings.ChatEnabled = false
	if _, err = f.s.PutSettings(ctx, f.actor, SettingsUpdate{Settings: settings, ExpectedRevision: settings.Revision}); err != nil {
		t.Fatal(err)
	}
	p, err := f.s.Decide(ctx, f.actor, id, true, "")
	if err != nil || p.State != "superseded" || count(t, f.db, "test_mutations") != 0 {
		t.Fatal("closed plan executed", p, err)
	}
	if _, err = f.db.Exec(`DELETE FROM ai_agent_settings`); err != nil {
		t.Fatal(err)
	}
	settings, err = f.s.Settings(ctx, f.actor)
	if err != nil || settings.ChatEnabled {
		t.Fatal("default is open", settings, err)
	}
	c, err := f.s.Conversation(ctx, f.actor)
	if err != nil || c.Enabled {
		t.Fatal("default availability", c, err)
	}
}
