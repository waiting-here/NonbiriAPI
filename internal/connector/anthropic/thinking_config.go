package anthropic

import (
	"encoding/json"
	"net/http"
	"strings"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
	"github.com/waiting-here/NonbiriAPI/internal/requestadaptation"
)

func ValidateTarget(target contract.Target, request *openai.ChatRequest, policy contract.AttemptPolicy) error {
	body, err := compileRequest(request, target.UpstreamModel(), "nbu_preflight", BuiltInDefaultMaxTokens)
	if err != nil {
		return err
	}
	defer func() { clear(body) }()
	if len(policy.NativeExtensions) > 0 {
		merged, err := requestadaptation.MergeNative(body, policy.NativeExtensions, MaxTranslatedRequestBytes)
		if err != nil {
			return err
		}
		clear(body)
		body = merged
	}
	return validateThinkingConfiguration(body, policy.AdditionalHeaders)
}

func validateThinkingConfiguration(body []byte, headers http.Header) error {
	root, err := strictObject(body)
	if err != nil {
		return ErrInvalidRequest
	}
	if len(root["thinking"]) == 0 || isNull(root["thinking"]) {
		return nil
	}
	thinking, err := strictObject(root["thinking"])
	var mode, model string
	if err != nil || !onlyKeys(thinking, "type", "budget_tokens", "display") || json.Unmarshal(thinking["type"], &mode) != nil {
		return fieldRejection("thinking", "expected a supported thinking configuration")
	}
	_ = json.Unmarshal(root["model"], &model)
	model = strings.ReplaceAll(strings.ToLower(model), ".", "-")
	known := func(names ...string) bool {
		for _, name := range names {
			if model == name || strings.HasPrefix(model, name+"-") {
				return true
			}
		}
		return false
	}
	if value, present := thinking["display"]; present {
		var display string
		if json.Unmarshal(value, &display) != nil || !oneOf(display, "summarized", "omitted", "updates") {
			return fieldRejection("thinking.display", "expected summarized, omitted, or updates")
		}
	}
	var choice struct {
		Type string `json:"type"`
	}
	_ = json.Unmarshal(root["tool_choice"], &choice)
	forced := choice.Type == "any" || choice.Type == "tool"
	switch mode {
	case "enabled":
		if known("claude-opus-4-7", "claude-opus-4-8", "claude-opus-5", "claude-sonnet-5", "claude-haiku-5", "claude-fable-5", "claude-mythos") {
			return fieldRejection("thinking.type", "this model uses adaptive thinking and does not support a manual budget")
		}
		if forced {
			return fieldRejection("tool_choice", "manual thinking requires auto or none tool choice")
		}
		var budget, maximum int64
		if json.Unmarshal(thinking["budget_tokens"], &budget) != nil || budget < 1024 || budget > MaxMaxTokens {
			return fieldRejection("thinking.budget_tokens", "manual thinking requires an integer budget of at least 1024")
		}
		_ = json.Unmarshal(root["max_tokens"], &maximum)
		interleaved := strings.Contains(headers.Get("Anthropic-Beta"), "interleaved-thinking-2025-05-14") && !known("claude-opus-4-6", "claude-haiku-4-5")
		if budget >= maximum && !interleaved {
			return fieldRejection("thinking.budget_tokens", "the thinking budget must be below max_tokens without supported interleaved thinking")
		}
	case "adaptive", "between_tools", "disabled":
		if _, present := thinking["budget_tokens"]; present {
			return fieldRejection("thinking.budget_tokens", "this thinking mode does not accept a manual budget")
		}
		if mode == "adaptive" && known("claude-3-7", "claude-sonnet-4-5", "claude-opus-4-5", "claude-haiku-4-5", "claude-opus-4-1") {
			return fieldRejection("thinking.type", "this model requires manual thinking with a budget")
		}
		if mode != "disabled" && forced && known("claude-opus-5-5", "claude-sonnet-5-5", "claude-fable-5-1", "claude-mythos-5-1") {
			return fieldRejection("tool_choice", "this model does not support forced tool use")
		}
	default:
		return fieldRejection("thinking.type", "unsupported thinking mode")
	}
	return nil
}
