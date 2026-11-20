package gemini

import (
	"encoding/json"
	"maps"
	"math"
	"slices"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/connector/chatwire"
	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
)

// Only the resolved GenerateContent model ID chooses a thinking configuration.
// Custom and moving model aliases cannot identify a version's thinking controls.
type generationSupport struct {
	levels               []string
	minBudget, maxBudget int64
	disableThinking      bool
	minimalAsLow         bool
	fixedSampling        bool
}

func modelSettings(model string) generationSupport {
	switch strings.TrimPrefix(model, "models/") {
	case "gemini-2.5-pro":
		return generationSupport{minBudget: 128, maxBudget: 32768}
	case "gemini-2.5-flash", "gemini-2.5-flash-preview-09-2025":
		return generationSupport{minBudget: 1, maxBudget: 24576, disableThinking: true}
	case "gemini-2.5-flash-lite", "gemini-2.5-flash-lite-preview-09-2025":
		return generationSupport{minBudget: 512, maxBudget: 24576, disableThinking: true}
	case "gemini-4-neon", "gemini-4-hydrogen":
		return generationSupport{levels: []string{"LOW", "MEDIUM", "HIGH", "XHIGH"}, minimalAsLow: true, fixedSampling: true}
	case "gemini-4-carbon", "gemini-4-argon":
		return generationSupport{levels: []string{"LOW", "MEDIUM", "HIGH"}, minimalAsLow: true, fixedSampling: true}
	case "gemini-3.8-flash":
		return generationSupport{levels: []string{"LOW", "MEDIUM", "HIGH"}, fixedSampling: true}
	case "gemini-3.6-flash":
		return generationSupport{levels: []string{"MINIMAL", "LOW", "MEDIUM", "HIGH"}, fixedSampling: true}
	case "gemini-3.5-flash", "gemini-3.5-flash-lite", "gemini-3.1-flash-lite", "gemini-3-flash-preview", "gemini-robotics-er-2-preview":
		return generationSupport{levels: []string{"MINIMAL", "LOW", "MEDIUM", "HIGH"}}
	case "gemini-3.1-pro-preview", "gemini-3.1-pro-preview-customtools":
		return generationSupport{levels: []string{"LOW", "MEDIUM", "HIGH"}}
	case "gemini-3-pro-preview":
		return generationSupport{levels: []string{"LOW", "HIGH"}}
	case "gemini-3.1-flash-lite-image":
		return generationSupport{levels: []string{"MINIMAL", "HIGH"}}
	default:
		return generationSupport{}
	}
}

func (s generationSupport) acceptsLevel(level string) bool {
	if len(s.levels) == 0 && s.maxBudget == 0 {
		// Preserve explicit native overrides on custom endpoints. XHIGH is a
		// model-specific addition and is accepted only on its documented models.
		return level != "XHIGH"
	}
	return slices.Contains(s.levels, level)
}

func (s generationSupport) acceptsBudget(budget int64) bool {
	if s.maxBudget != 0 {
		return budget == -1 || budget == 0 && s.disableThinking || budget >= s.minBudget && budget <= s.maxBudget
	}
	// Existing native budget overrides remain usable on custom deployments.
	// Level-based thinking models cannot disable reasoning.
	return len(s.levels) == 0 || budget != 0
}

func (s generationSupport) thinking(effort string) (map[string]any, error) {
	if s.maxBudget != 0 {
		switch effort {
		case "none":
			if s.disableThinking {
				return map[string]any{"thinkingBudget": 0}, nil
			}
		case "minimal", "low":
			return map[string]any{"thinkingBudget": 1024}, nil
		case "medium":
			return map[string]any{"thinkingBudget": 8192}, nil
		case "high":
			return map[string]any{"thinkingBudget": 24576}, nil
		}
	}
	level := strings.ToUpper(effort)
	if effort == "minimal" && s.minimalAsLow {
		level = "LOW"
	}
	if slices.Contains(s.levels, level) {
		return map[string]any{"thinkingLevel": level}, nil
	}
	return nil, chatwire.RejectionReason("reasoning_effort", "effort is not enabled for this model")
}

func addGenerationSettings(root map[string]json.RawMessage, config map[string]any, support generationSupport, protocolOnly bool) error {
	for _, field := range [][2]string{{"presence_penalty", "presencePenalty"}, {"frequency_penalty", "frequencyPenalty"}, {"top_k", "topK"}} {
		name, raw := field[0], root[field[0]]
		if chatwire.Null(raw) {
			continue
		}
		if support.fixedSampling {
			return chatwire.RejectionReason(name, "sampling parameter is not supported by this model")
		}
		if name == "top_k" {
			k, ok := chatwire.Integer(raw)
			if !ok || k < 1 || k > math.MaxInt32 {
				return chatwire.RejectionReason(name, "expected an integer from 1 to 2147483647")
			}
			config[field[1]] = k
		} else {
			penalty, ok := chatwire.Number(raw)
			if !ok || penalty < -2 || penalty > 2 {
				return chatwire.RejectionReason(name, "expected a number from -2 to 2")
			}
			if penalty == 2 && (support.maxBudget != 0 || len(support.levels) != 0) {
				return chatwire.RejectionReason(name, "expected a number from -2 inclusive to 2 exclusive")
			}
			config[field[1]] = penalty
		}
	}
	if raw := root["seed"]; !chatwire.Null(raw) {
		var seed int32
		if json.Unmarshal(raw, &seed) != nil {
			return chatwire.RejectionReason("seed", "expected a signed 32-bit integer")
		}
		config["seed"] = seed
	}
	if raw := root["reasoning_effort"]; !chatwire.Null(raw) {
		effort, ok := chatwire.String(raw)
		if !ok || !openai.ValidReasoningEffort(effort) {
			return chatwire.RejectionReason("reasoning_effort", "unsupported effort value")
		}
		if !protocolOnly {
			thinking, err := support.thinking(effort)
			if err != nil {
				return err
			}
			config["thinkingConfig"] = thinking
		}
	}
	return nil
}

// A parent native object replaces its entire destination. Expand the independent
// includeThoughts flag into a leaf so it can coexist with a caller's effort.
func effortExtensions(native map[string]json.RawMessage) (map[string]json.RawMessage, error) {
	const base = "/generationConfig/thinkingConfig"
	for _, field := range []string{"thinkingBudget", "thinkingLevel"} {
		if _, ok := native[base+"/"+field]; ok {
			return nil, chatwire.RejectionReason("reasoning_effort", "reasoning effort conflicts with native thinking settings")
		}
	}
	raw, ok := native[base]
	if !ok {
		return native, nil
	}
	thinking, err := chatwire.Object(raw)
	if err != nil || !chatwire.Keys(thinking, "thinkingBudget", "thinkingLevel", "includeThoughts") {
		return nil, chatwire.Rejection("native_extensions")
	}
	if thinking["thinkingBudget"] != nil || thinking["thinkingLevel"] != nil {
		return nil, chatwire.RejectionReason("reasoning_effort", "reasoning effort conflicts with native thinking settings")
	}
	out := maps.Clone(native)
	delete(out, base)
	if flag := thinking["includeThoughts"]; flag != nil {
		if out[base+"/includeThoughts"] != nil {
			return nil, chatwire.Rejection("native_extensions")
		}
		out[base+"/includeThoughts"] = flag
	}
	return out, nil
}
