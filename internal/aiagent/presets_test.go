package aiagent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/agentrequest"
)

func TestPresetsDefaultSettingsAndDriverRequests(t *testing.T) {
	defaults := defaultSettings()
	if defaults.Mode != "off" || defaults.ChatEnabled || defaults.SearchHasKey || defaults.SearchURL != "" || defaults.AutoPrefill != "" || defaults.ChatPrefill != "" ||
		!validTemplate(defaults.AutoTemplate, false) || !validTemplate(defaults.ChatTemplate, true) || len(defaults.ToolDescriptions) != len(toolNames) {
		t.Fatalf("invalid defaults: %+v", defaults)
	}
	defaults.AutoTemplate[0].Content = "changed"
	defaults.ToolDescriptions["donations"] = "changed"
	if next := defaultSettings(); next.AutoTemplate[0].Content == "changed" || next.ToolDescriptions["donations"] == "changed" {
		t.Fatal("default settings share mutable presets")
	}
	for _, kind := range []string{"chat", "auto_review"} {
		t.Run(kind, func(t *testing.T) {
			f := newFixture(t)
			ctx := context.Background()
			var id int64
			if kind == "chat" {
				id = f.send(t, "Read the current state", "preset").ID
			} else {
				settings := defaultSettings()
				settings.Mode = "preview"
				if _, err := f.s.PutSettings(ctx, f.actor, SettingsUpdate{Settings: settings}); err != nil {
					t.Fatal(err)
				}
				tx, err := f.db.BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				if err = f.s.PendingSubmission(ctx, tx, 10); err != nil {
					tx.Rollback()
					t.Fatal(err)
				}
				if err = tx.Commit(); err != nil {
					t.Fatal(err)
				}
				if err = f.s.collectReviewEvents(ctx); err != nil {
					t.Fatal(err)
				}
				if err = f.db.QueryRow("SELECT id FROM ai_agent_jobs WHERE kind='auto_review'").Scan(&id); err != nil {
					t.Fatal(err)
				}
				outcome, _ := json.Marshal(ReviewResult{Decision: "manual", InternalReason: "More evidence is needed", UserNote: "Please provide model details", Bindings: []ReviewBinding{}})
				message, _ := json.Marshal(map[string]string{"role": "assistant", "content": string(outcome)})
				f.driver.replies = []string{string(message)}
			}
			if err := f.s.run(ctx, id); err != nil {
				t.Fatal(err)
			}
			f.driver.mu.Lock()
			inputs := append([]agentrequest.Input(nil), f.driver.inputs...)
			f.driver.mu.Unlock()
			if len(inputs) != 1 {
				t.Fatalf("model calls=%d", len(inputs))
			}
			var messages []TemplateMessage
			if err := json.Unmarshal(inputs[0].Messages, &messages); err != nil || len(messages) < 2 ||
				!strings.Contains(messages[0].Content, initialMemory(kind)) || strings.Contains(messages[0].Content, "{{memory}}") {
				t.Fatalf("rendered messages=%s err=%v", inputs[0].Messages, err)
			}
			template := defaultSettings().ChatTemplate
			if kind == "auto_review" {
				template = defaultSettings().AutoTemplate
				if !strings.Contains(messages[1].Content, `"donation":"virtual"`) || strings.Contains(messages[1].Content, "{{intelligence}}") {
					t.Fatalf("missing actual intelligence: %s", inputs[0].Messages)
				}
			}
			prefix := strings.Split(template[0].Content, "{{memory}}")[0]
			if !strings.HasPrefix(messages[0].Content, prefix) {
				t.Fatal("request did not use the embedded template")
			}
			tools := presetRequestTools(t, inputs[0])
			if tools["donations"].Description != defaultSettings().ToolDescriptions["donations"] {
				t.Fatal("default tool description did not reach the driver")
			}
		})
	}
}

func TestPresetsInitialMemoryDiffHistoryAndSavedEmpty(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	for _, kind := range []string{"auto_review", "chat"} {
		before, err := f.s.Memory(ctx, f.actor, kind)
		if err != nil || before.Revision != 0 || before.Content == "" || before.Content != initialMemory(kind) {
			t.Fatal("initial memory", before, err)
		}
		input, _ := json.Marshal(memoryEdit{Kind: kind, Content: "A confirmed local rule", ExpectedRevision: 0})
		preview, err := f.s.previewOperations(ctx, f.actor, []Operation{{Kind: "memory.edit", TargetID: kind, Input: input}})
		if err != nil || len(preview) != 1 || preview[0].Before != before.Content || preview[0].After != "A confirmed local rule" {
			t.Fatal("initial diff", preview, err)
		}
		custom, err := f.s.PutMemory(ctx, f.actor, kind, "A confirmed local rule", 0)
		if err != nil || custom.Revision != 1 {
			t.Fatal(custom, err)
		}
		read, err := f.s.Memory(ctx, f.actor, kind)
		if err != nil || read.Content != custom.Content || read.Revision != 1 {
			t.Fatal("saved custom replaced", read, err)
		}
		var historical string
		if err = f.db.QueryRow("SELECT before_content FROM ai_agent_memory_history WHERE kind=? AND revision=1", kind).Scan(&historical); err != nil || historical != before.Content {
			t.Fatal("initial history", historical, err)
		}
		if _, err = f.s.PutMemory(ctx, f.actor, kind, "", 1); err != nil {
			t.Fatal(err)
		}
		read, err = f.s.Memory(ctx, f.actor, kind)
		if err != nil || read.Content != "" || read.Revision != 2 {
			t.Fatal("saved empty replaced by preset", read, err)
		}
	}
}

func TestPresetsLegacySettingsAndPartialDescriptions(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	legacy := defaultSettings()
	legacy.ChatEnabled = true
	legacy.Tools["donations"] = false
	delete(legacy.Tools, "charity_binding_presets")
	legacy.AutoTemplate = []TemplateMessage{{Role: "system", Content: "Existing auto {{memory}}"}, {Role: "user", Content: "{{intelligence}}"}}
	legacy.ChatTemplate = []TemplateMessage{{Role: "system", Content: "Existing chat {{memory}}"}, {Role: "user", Content: "{{conversation}}"}}
	raw, _ := json.Marshal(legacy)
	var stored map[string]json.RawMessage
	json.Unmarshal(raw, &stored)
	delete(stored, "tool_descriptions")
	raw, _ = json.Marshal(stored)
	if _, err := f.db.Exec("UPDATE ai_agent_settings SET config_json=?,mode='preview',revision=7", string(raw)); err != nil {
		t.Fatal(err)
	}
	current, err := f.s.Settings(ctx, f.actor)
	if err != nil || current.Revision != 7 || current.Mode != "preview" || !current.ChatEnabled || current.Tools["donations"] || !current.Tools["charity_binding_presets"] ||
		!reflect.DeepEqual(current.AutoTemplate, legacy.AutoTemplate) || !reflect.DeepEqual(current.ChatTemplate, legacy.ChatTemplate) ||
		!reflect.DeepEqual(current.ToolDescriptions, defaultToolDescriptions()) {
		t.Fatal("legacy settings changed", current, err)
	}
	current.ToolDescriptions = map[string]string{"donations": "First line\r\nSecond line\rThird"}
	changed, err := f.s.PutSettings(ctx, f.actor, SettingsUpdate{Settings: current, ExpectedRevision: 7})
	if err != nil || changed.ToolDescriptions["donations"] != "First line\nSecond line\nThird" || len(changed.ToolDescriptions) != len(toolNames) ||
		changed.ToolDescriptions["users"] != defaultToolDescriptions()["users"] {
		t.Fatal("partial descriptions", changed, err)
	}
	changed.ToolDescriptions = nil
	preserved, err := f.s.PutSettings(ctx, f.actor, SettingsUpdate{Settings: changed, ExpectedRevision: changed.Revision})
	if err != nil || preserved.ToolDescriptions["donations"] != "First line\nSecond line\nThird" {
		t.Fatal("omitted map reset descriptions", preserved, err)
	}
	preserved.ToolDescriptions = map[string]string{"donations": ""}
	cleared, err := f.s.PutSettings(ctx, f.actor, SettingsUpdate{Settings: preserved, ExpectedRevision: preserved.Revision})
	if err != nil || cleared.ToolDescriptions["donations"] != "" {
		t.Fatal("explicit empty description", cleared, err)
	}
	read, err := f.s.Settings(ctx, f.actor)
	if err != nil || read.ToolDescriptions["donations"] != "" || !reflect.DeepEqual(read.ChatTemplate, legacy.ChatTemplate) || read.Tools["donations"] {
		t.Fatal("stored settings not preserved", read, err)
	}
	for _, invalid := range []map[string]string{{"unknown_tool": "text"}, {"": "text"}, {"donations": strings.Repeat("x", (16<<10)+1)}, {"donations": strings.Repeat("界", 5500)}} {
		update := read
		update.ToolDescriptions = invalid
		if _, err := f.s.PutSettings(ctx, f.actor, SettingsUpdate{Settings: update, ExpectedRevision: read.Revision}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid description accepted: %v", err)
		}
	}
	limit := read
	limit.ToolDescriptions = map[string]string{"donations": strings.Repeat("x", (16<<10)-1) + "\r\n"}
	if accepted, err := f.s.PutSettings(ctx, f.actor, SettingsUpdate{Settings: limit, ExpectedRevision: read.Revision}); err != nil || len(accepted.ToolDescriptions["donations"]) != 16<<10 {
		t.Fatal("normalized byte boundary", err)
	}
}

type presetRefreshOperations struct {
	*fakeOperations
	refresh func() error
}

func (o *presetRefreshOperations) Read(ctx context.Context, actor Actor, name string, args json.RawMessage) (json.RawMessage, error) {
	if o.refresh != nil {
		refresh := o.refresh
		o.refresh = nil
		if err := refresh(); err != nil {
			return nil, err
		}
	}
	return o.fakeOperations.Read(ctx, actor, name, args)
}

type presetWireTool struct {
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
	Name        string          `json:"name"`
}

func presetRequestTools(t *testing.T, input agentrequest.Input) map[string]presetWireTool {
	t.Helper()
	var items []struct {
		Function presetWireTool `json:"function"`
	}
	if err := json.Unmarshal(input.Tools, &items); err != nil {
		t.Fatal(err)
	}
	result := map[string]presetWireTool{}
	for _, item := range items {
		result[item.Function.Name] = item.Function
	}
	return result
}

func TestPresetsLiveDescriptionsReachDriverWithoutChangingAvailability(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	authorize := f.s.config.Authorize
	f.s.config.Authorize = func(ctx context.Context, tx *sql.Tx, actor Actor, permission string) error {
		if permission == "ai.memory.edit" {
			return ErrForbidden
		}
		return authorize(ctx, tx, actor, permission)
	}
	settings, err := f.s.Settings(ctx, f.actor)
	if err != nil {
		t.Fatal(err)
	}
	settings.Tools["propose_operations"] = false
	settings.ToolDescriptions = map[string]string{"donations": "First description", "users": "No domain permission", "edit_memory": "No memory permission"}
	if _, err = f.s.PutSettings(ctx, f.actor, SettingsUpdate{Settings: settings, ExpectedRevision: settings.Revision}); err != nil {
		t.Fatal(err)
	}
	f.s.config.Operations = &presetRefreshOperations{fakeOperations: f.ops, refresh: func() error {
		current, err := f.s.Settings(ctx, f.actor)
		if err != nil {
			return err
		}
		current.ToolDescriptions = map[string]string{"donations": "Live description"}
		_, err = f.s.PutSettings(ctx, f.actor, SettingsUpdate{Settings: current, ExpectedRevision: current.Revision})
		return err
	}}
	f.driver.replies = []string{
		`{"role":"assistant","content":"","tool_calls":[{"id":"read-1","type":"function","function":{"name":"donations","arguments":"{}"}}]}`,
		`{"role":"assistant","content":"The query is complete."}`,
	}
	job := f.send(t, "Read one current item", "l")
	if err = f.s.run(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	f.driver.mu.Lock()
	inputs := append([]agentrequest.Input(nil), f.driver.inputs...)
	f.driver.mu.Unlock()
	if len(inputs) != 2 {
		t.Fatalf("model calls=%d", len(inputs))
	}
	for i, input := range inputs {
		tools := presetRequestTools(t, input)
		want := "First description"
		if i == 1 {
			want = "Live description"
		}
		if len(tools) != 2 || tools["edit_memory"].Name != "edit_memory" || tools["donations"].Description != want ||
			string(tools["donations"].Parameters) != `{"type":"object","properties":{}}` {
			t.Fatalf("description changed tool availability or schema: %+v", tools)
		}
	}
}

func TestSiteReadDescriptionsUseCompleteFilteredResults(t *testing.T) {
	for _, name := range []string{"donations", "donation_models", "review_history", "charity_models", "users"} {
		description := defaultToolDescriptions()[name]
		for _, obsolete := range []string{"page_size", "has_more", "第一页", "翻页"} {
			if strings.Contains(description, obsolete) {
				t.Fatal("obsolete model-visible paging guidance", name, obsolete)
			}
		}
		if !strings.Contains(description, "total") || !strings.Contains(description, "全部") {
			t.Fatal("complete result contract missing", name)
		}
	}
}
