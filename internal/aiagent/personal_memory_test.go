package aiagent

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func seedMemoryOwners(t *testing.T, f fixture) {
	t.Helper()
	if _, err := f.db.Exec(`CREATE TABLE users(id INTEGER PRIMARY KEY,username TEXT NOT NULL,is_admin INTEGER NOT NULL DEFAULT 0,level INTEGER,auto_level INTEGER NOT NULL DEFAULT 1);
 INSERT INTO users(id,username,is_admin,level) VALUES(1,'administrator',1,NULL),(2,'first steward',0,5),(3,'second steward',0,6),(4,'ordinary member',0,1)`); err != nil {
		t.Fatal(err)
	}
}

func TestPersonalMemoryIsolationAndSharedMemoryAuthority(t *testing.T) {
	f := newFixture(t)
	seedMemoryOwners(t, f)
	ctx := context.Background()
	first := Actor{ID: 2, Role: "level5", Label: "first steward"}
	second := Actor{ID: 3, Role: "level6", Label: "second steward"}
	for _, actor := range []Actor{first, second} {
		value, err := f.s.PutPersonalMemory(ctx, actor, 0, actor.Label+" preferences", 0)
		if err != nil || value.OwnerID != actor.ID || !value.Writable {
			t.Fatal(value, err)
		}
		for _, kind := range []string{"chat", "auto_review"} {
			shared, err := f.s.Memory(ctx, actor, kind)
			if err != nil || shared.Writable {
				t.Fatal("shared read", shared, err)
			}
			if _, err = f.s.PutMemory(ctx, actor, kind, "forged global write", 0); !errors.Is(err, ErrForbidden) {
				t.Fatal("global write accepted", err)
			}
			raw, _ := json.Marshal(memoryEdit{Kind: kind, Content: "forged global write"})
			if _, err = f.s.previewOperations(ctx, actor, []Operation{{Kind: "memory.edit", Input: raw}}); !errors.Is(err, ErrForbidden) {
				t.Fatal("global plan accepted", err)
			}
		}
	}
	if _, err := f.s.PersonalMemory(ctx, first, second.ID); !errors.Is(err, ErrForbidden) {
		t.Fatal("read another interlocutor", err)
	}
	if _, err := f.s.PutPersonalMemory(ctx, first, second.ID, "forged cross-owner write", 1); !errors.Is(err, ErrForbidden) {
		t.Fatal("write another interlocutor", err)
	}
	for _, owner := range []int64{4, 999} {
		if _, err := f.s.PutPersonalMemory(ctx, f.actor, owner, "invented owner", 0); !errors.Is(err, ErrNotFound) {
			t.Fatal("created memory for ineligible owner", owner, err)
		}
	}
	value, err := f.s.PersonalMemory(ctx, f.actor, second.ID)
	if err != nil || value.Content != "second steward preferences" {
		t.Fatal(value, err)
	}
	if _, err = f.s.PutPersonalMemory(ctx, f.actor, second.ID, "administrator correction\r\nkept", value.Revision); err != nil {
		t.Fatal(err)
	}
	value, err = f.s.PersonalMemory(ctx, second, 0)
	if err != nil || value.Content != "administrator correction\nkept" {
		t.Fatal(value, err)
	}
}

func TestPersonalMemorySurvivesAccountDeletionAndLossOfRole(t *testing.T) {
	f := newFixture(t)
	seedMemoryOwners(t, f)
	ctx := context.Background()
	actor := Actor{ID: 2, Role: "level5", Label: "former steward"}
	if _, err := f.s.PutPersonalMemory(ctx, actor, 0, "retained preference", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`UPDATE users SET level=4 WHERE id=2`); err != nil {
		t.Fatal(err)
	}
	demoted := actor
	demoted.Role = "level4"
	if _, err := f.s.PersonalMemory(ctx, demoted, 0); !errors.Is(err, ErrForbidden) {
		t.Fatal("demoted access", err)
	}
	if _, err := f.db.Exec(`DELETE FROM users WHERE id=2`); err != nil {
		t.Fatal(err)
	}
	value, err := f.s.PutPersonalMemory(ctx, f.actor, 2, "retained and maintained", 1)
	if err != nil || value.Revision != 2 {
		t.Fatal(value, err)
	}
	page, err := f.s.MemoryOwners(ctx, f.actor, "former steward", 1, 1)
	if err != nil || page.Total != 1 || len(page.Items) != 1 || page.Items[0].ID != 2 {
		t.Fatal(page, err)
	}
	if _, err = f.s.MemoryOwners(ctx, actor, "", 1, 20); !errors.Is(err, ErrForbidden) {
		t.Fatal("owner list exposed to steward", err)
	}
	var history int
	if err = f.db.QueryRow(`SELECT COUNT(*) FROM ai_agent_personal_memory_history WHERE owner_id=2`).Scan(&history); err != nil || history != 2 {
		t.Fatal("history retained", history, err)
	}
}

func TestForgedMemoryOperationIsRejectedInTheFinalTransaction(t *testing.T) {
	f := newFixture(t)
	seedMemoryOwners(t, f)
	actor := Actor{ID: 2, Role: "level5", Label: "first steward"}
	for _, edit := range []memoryEdit{{Kind: "chat", Content: "forged"}, {Kind: "personal", OwnerID: 3, Content: "forged"}} {
		raw, _ := json.Marshal(edit)
		pending, err := f.s.prepare(t.Context(), actor, []Operation{{Kind: "memory.edit", Input: raw}})
		if err != nil {
			t.Fatal(err)
		}
		tx, err := f.db.BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		_, err = pending.Apply(t.Context(), tx)
		_ = tx.Rollback()
		pending.Finalize(false)
		if !errors.Is(err, ErrForbidden) {
			t.Fatal("forged final write accepted", edit.Kind, err)
		}
	}
}

func TestPersonalMemoryConcurrentRevisionAndSinglePlanConfirmation(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, value := range []string{"one", "two"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := f.s.PutPersonalMemory(ctx, f.actor, 0, value, 0)
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	succeeded, conflicts := 0, 0
	for err := range results {
		if err == nil {
			succeeded++
		} else if errors.Is(err, ErrConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if succeeded != 1 || conflicts != 1 {
		t.Fatal(succeeded, conflicts)
	}
	current, err := f.s.PersonalMemory(ctx, f.actor, 0)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(memoryEdit{Kind: "personal", Content: "confirmed preference", ExpectedRevision: current.Revision})
	_, id := f.plan(t, []Operation{{Kind: "memory.edit", TargetID: "personal", Input: raw}})
	for range 2 {
		if _, err := f.s.Decide(ctx, f.actor, id, true, "keep this preference"); err != nil {
			t.Fatal(err)
		}
	}
	current, err = f.s.PersonalMemory(ctx, f.actor, 0)
	if err != nil || current.Revision != 2 || current.Content != "confirmed preference" {
		t.Fatal(current, err)
	}
}

func TestInterlocutorMacrosUseTheCurrentConversationAndDoNotExpandData(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.actor.Label = "name {{memory}}"
	if _, err := f.s.PutPersonalMemory(ctx, f.actor, 0, "Use brief replies; literal {{interlocutor}}", 0); err != nil {
		t.Fatal(err)
	}
	job := f.send(t, "Current request", "i")
	settings := defaultSettings()
	messages, _, _, _, err := f.s.contextMessages(ctx, job, settings)
	if err != nil {
		t.Fatal(err)
	}
	var identity TemplateMessage
	if err = json.Unmarshal(messages[1], &identity); err != nil || !strings.Contains(identity.Content, `"id":"1"`) || !strings.Contains(identity.Content, "name {{memory}}") || !strings.Contains(identity.Content, "literal {{interlocutor}}") {
		t.Fatal(string(messages[1]), err)
	}
	if strings.Contains(identity.Content, "SessionHash") || strings.Contains(identity.Content, "权限") {
		t.Fatal("unexpected actor material", identity.Content)
	}
}

func TestSavedCustomTemplatesAndMemoriesArePreserved(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	settings := defaultSettings()
	settings.ChatTemplate = append([]TemplateMessage(nil), builtInPresets.previousChat...)
	settings.ToolDescriptions["edit_memory"] = builtInPresets.previousMemoryTool
	raw, _ := json.Marshal(settings)
	if _, err := f.db.Exec(`UPDATE ai_agent_settings SET config_json=?`, string(raw)); err != nil {
		t.Fatal(err)
	}
	current, err := f.s.Settings(ctx, f.actor)
	if err != nil || !reflect.DeepEqual(current.ChatTemplate, builtInPresets.chat) || current.ToolDescriptions["edit_memory"] != builtInPresets.descriptions["edit_memory"] {
		t.Fatal("exact previous defaults not refreshed", current, err)
	}
	settings.ChatTemplate[0].Content += "\nCustom instruction."
	settings.ToolDescriptions["edit_memory"] = "Custom memory instructions"
	raw, _ = json.Marshal(settings)
	if _, err = f.db.Exec(`UPDATE ai_agent_settings SET config_json=?`, string(raw)); err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.PutMemory(ctx, f.actor, "chat", "Saved custom experience", 0); err != nil {
		t.Fatal(err)
	}
	current, err = f.s.Settings(ctx, f.actor)
	if err != nil || !reflect.DeepEqual(current.ChatTemplate, settings.ChatTemplate) || current.ToolDescriptions["edit_memory"] != "Custom memory instructions" {
		t.Fatal("custom templates changed", current, err)
	}
	memory, err := f.s.Memory(ctx, f.actor, "chat")
	if err != nil || memory.Content != "Saved custom experience" {
		t.Fatal(memory, err)
	}
}
