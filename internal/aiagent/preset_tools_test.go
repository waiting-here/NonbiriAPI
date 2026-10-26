package aiagent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type presetToolsOperations struct{ *fakeOperations }

func TestPresetToolDefaultDescriptionExplainsCustomCandidatesAndFilters(t *testing.T) {
	description := defaultSettings().ToolDescriptions["charity_binding_presets"]
	for _, fact := range []string{"query", "channel_id", "recommended=true", "模型映射", "授权服务", "5000", "1 MiB", "无分页"} {
		if !strings.Contains(description, fact) {
			t.Fatal("preset tool description missing", fact)
		}
	}
}

func (o *presetToolsOperations) Tools(context.Context, Actor, bool) ([]Tool, error) {
	return []Tool{{Name: "charity_binding_presets", Parameters: json.RawMessage(`{"type":"object"}`), ChatOnly: true}}, nil
}

func TestPresetToolSettingsAndChatOnly(t *testing.T) {
	f := newFixture(t)
	f.s.config.Operations = &presetToolsOperations{f.ops}
	settings := defaultSettings()
	settings.ToolDescriptions["charity_binding_presets"] = "Current configured preset description"
	for _, automatic := range []bool{false, true} {
		items, allowed, err := f.s.tools(t.Context(), f.actor, settings, automatic)
		if err != nil || allowed["charity_binding_presets"] == automatic {
			t.Fatal("chat-only tool availability", allowed, err)
		}
		if !automatic {
			raw, _ := json.Marshal(items)
			var wire []struct {
				Function presetWireTool `json:"function"`
			}
			if err := json.Unmarshal(raw, &wire); err != nil {
				t.Fatal(err)
			}
			found := false
			for _, item := range wire {
				if item.Function.Name == "charity_binding_presets" {
					found = item.Function.Description == settings.ToolDescriptions["charity_binding_presets"]
				}
			}
			if !found {
				t.Fatal("configured description missing")
			}
		}
	}
	settings.Tools["charity_binding_presets"] = false
	if _, allowed, err := f.s.tools(t.Context(), f.actor, settings, false); err != nil || allowed["charity_binding_presets"] {
		t.Fatal("disabled preset tool advertised", allowed, err)
	}
	settings, err := f.s.Settings(t.Context(), f.actor)
	if err != nil {
		t.Fatal(err)
	}
	settings.Tools["charity_binding_presets"] = false
	settings.ToolDescriptions["charity_binding_presets"] = "Saved custom description"
	if _, err := f.s.PutSettings(t.Context(), f.actor, SettingsUpdate{Settings: settings, ExpectedRevision: settings.Revision}); err != nil {
		t.Fatal(err)
	}
	saved, err := f.s.Settings(t.Context(), f.actor)
	if err != nil || saved.Tools["charity_binding_presets"] || saved.ToolDescriptions["charity_binding_presets"] != "Saved custom description" {
		t.Fatal("saved preset tool configuration changed", saved, err)
	}
}
