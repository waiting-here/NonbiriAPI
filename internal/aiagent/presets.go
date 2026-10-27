package aiagent

import (
	"embed"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
)

//go:embed presets/*
var presetFiles embed.FS

type presetBundle struct {
	auto, chat             []TemplateMessage
	previousChat           []TemplateMessage
	previousMemoryTool     string
	descriptions           map[string]string
	autoMemory, chatMemory string
}

var builtInPresets = loadPresets()

func loadPresets() presetBundle {
	var value presetBundle
	read := func(name string) []byte {
		raw, err := presetFiles.ReadFile("presets/" + name)
		if err != nil {
			panic(fmt.Sprintf("ai agent: missing preset %s: %v", name, err))
		}
		return raw
	}
	for name, target := range map[string]any{
		"auto-review.json":       &value.auto,
		"chat.json":              &value.chat,
		"previous-chat.json":     &value.previousChat,
		"tool-descriptions.json": &value.descriptions,
	} {
		if err := json.Unmarshal(read(name), target); err != nil {
			panic(fmt.Sprintf("ai agent: invalid preset %s: %v", name, err))
		}
	}
	if !validTemplate(value.auto, false) || !validTemplate(value.chat, true) || len(value.descriptions) != len(toolNames) {
		panic("ai agent: invalid built-in templates or tool descriptions")
	}
	descriptions, err := normalizeToolDescriptions(value.descriptions)
	if err != nil {
		panic("ai agent: invalid built-in tool descriptions")
	}
	value.descriptions = descriptions
	value.autoMemory = string(read("auto-review-memory.md"))
	value.chatMemory = string(read("chat-memory.md"))
	value.previousMemoryTool = string(read("previous-memory-description.txt"))
	return value
}

func refreshDefaultTemplate(settings *Settings) {
	if reflect.DeepEqual(settings.ChatTemplate, builtInPresets.previousChat) {
		settings.ChatTemplate = append([]TemplateMessage(nil), builtInPresets.chat...)
	}
	if settings.ToolDescriptions["edit_memory"] == builtInPresets.previousMemoryTool {
		settings.ToolDescriptions["edit_memory"] = builtInPresets.descriptions["edit_memory"]
	}
}

func mergeToolDescriptions(current, updates map[string]string) map[string]string {
	result := make(map[string]string, len(toolNames))
	for _, name := range toolNames {
		result[name] = current[name]
		if description, exists := updates[name]; exists {
			result[name] = description
		}
	}
	return result
}

func defaultToolDescriptions() map[string]string {
	return mergeToolDescriptions(builtInPresets.descriptions, nil)
}

func normalizeToolDescriptions(input map[string]string) (map[string]string, error) {
	if input == nil {
		return nil, nil
	}
	result := make(map[string]string, len(input))
	total := 0
	for name, description := range input {
		if !slices.Contains(toolNames, name) {
			return nil, ErrInvalid
		}
		description = strings.ReplaceAll(strings.ReplaceAll(description, "\r\n", "\n"), "\r", "\n")
		if len(description) > 16<<10 {
			return nil, ErrInvalid
		}
		total += len(description)
		if total > 256<<10 {
			return nil, ErrInvalid
		}
		result[name] = description
	}
	return result, nil
}

func initialMemory(kind string) string {
	if kind == "auto_review" {
		return builtInPresets.autoMemory
	}
	if kind == "chat" {
		return builtInPresets.chatMemory
	}
	return ""
}
