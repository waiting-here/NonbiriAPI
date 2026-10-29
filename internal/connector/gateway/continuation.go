package gateway

import (
	"encoding/json"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/connector/continuation"
)

// These per-part keys are consumed by the pinned provider adapters. Other
// provider options, especially routing and execution controls, are not replayed.
func continuationMetadata(raw []byte, strict bool) (map[string]any, error) {
	if len(raw) == 0 || isNull(raw) {
		return nil, nil
	}
	root, err := parseObject(raw)
	if err != nil {
		return nil, errResponse
	}
	allowed := map[string][]string{
		"google": {"thoughtSignature"}, "googleVertex": {"thoughtSignature"}, "vertex": {"thoughtSignature"},
		"anthropic": {"signature", "redactedData"},
		"openai":    {"itemId", "reasoningEncryptedContent"}, "azure": {"itemId", "reasoningEncryptedContent"},
	}
	result := map[string]any{}
	for provider, value := range root {
		keys, known := allowed[provider]
		if !known {
			if strict {
				return nil, errRequest
			}
			continue
		}
		fields, err := parseObject(value)
		if err != nil {
			return nil, errResponse
		}
		out := map[string]any{}
		for key, value := range fields {
			accepted := false
			for _, allowed := range keys {
				accepted = accepted || key == allowed
			}
			if !accepted {
				if strict {
					return nil, errRequest
				}
				continue
			}
			if isNull(value) {
				continue
			}
			s, ok := text(value)
			if !ok || len(s) == 0 || len(s) > 1<<20 || strings.ContainsAny(s, "\x00\r\n") {
				return nil, errResponse
			}
			out[key] = s
		}
		if len(out) > 0 {
			result[provider] = out
		}
	}
	return result, nil
}

func continuationPart(part object, strict bool) (map[string]any, error) {
	kind, ok := text(part["type"])
	if !ok {
		return nil, errResponse
	}
	out := map[string]any{"type": kind}
	switch kind {
	case "text", "reasoning":
		value, valid := text(part["text"])
		if !valid || !noCitations(part["citations"]) || strict && !only(part, "type", "text", "providerMetadata") {
			return nil, errResponse
		}
		out["text"] = value
	case "tool-call":
		call, err := parseToolCall(part)
		if err != nil || strict && !only(part, "type", "toolCallId", "toolName", "input", "providerMetadata") {
			return nil, errResponse
		}
		out["toolCallId"], out["toolName"], out["input"] = call.ID, call.Function.Name, call.Function.Arguments
	default:
		return nil, errResponse
	}
	metadata, err := continuationMetadata(part["providerMetadata"], strict)
	if err != nil {
		return nil, err
	}
	if len(metadata) > 0 {
		out["providerMetadata"] = metadata
	}
	return out, nil
}

func gatewayExtra(parts []map[string]any, id string) map[string]any {
	needed := false
	opaque := false
	for _, part := range parts {
		needed = needed || part["type"] == "reasoning" || part["providerMetadata"] != nil
		opaque = opaque || part["providerMetadata"] != nil
	}
	if !needed {
		return nil
	}
	if !opaque {
		id = ""
	}
	return continuation.Bind(map[string]any{"gateway": map[string]any{"content": parts}}, id)
}

func replayContinuation(extra []byte, expected []any) ([]any, error) {
	if len(extra) == 0 || isNull(extra) {
		return expected, nil
	}
	outer, err := parseObject(extra)
	if err != nil || !continuation.ValidExtra(outer, "gateway") {
		return nil, errRequest
	}
	native, err := parseObject(outer["gateway"])
	if err != nil || !only(native, "content") {
		return nil, errRequest
	}
	var rawParts []json.RawMessage
	if json.Unmarshal(native["content"], &rawParts) != nil || len(rawParts) == 0 || len(rawParts) > 4096 {
		return nil, errRequest
	}
	parts := make([]any, 0, len(rawParts))
	for _, raw := range rawParts {
		part, err := parseObject(raw)
		if err != nil {
			return nil, errRequest
		}
		value, err := continuationPart(part, true)
		if err != nil {
			return nil, reject("protocol preflight", "extra_content.gateway", "expected supported continuation parts and metadata")
		}
		if metadata := value["providerMetadata"]; metadata != nil {
			value["providerOptions"] = metadata
			delete(value, "providerMetadata")
		}
		if value["type"] == "tool-call" {
			value["input"] = json.RawMessage(value["input"].(string))
		}
		parts = append(parts, value)
	}
	project := func(parts []any) ([]byte, error) {
		var content, reasoning strings.Builder
		calls := []any{}
		for _, raw := range parts {
			part := raw.(map[string]any)
			switch part["type"] {
			case "text":
				content.WriteString(part["text"].(string))
			case "reasoning":
				reasoning.WriteString(part["text"].(string))
			case "tool-call":
				calls = append(calls, map[string]any{"id": part["toolCallId"], "name": part["toolName"], "input": part["input"]})
			default:
				return nil, errRequest
			}
		}
		return json.Marshal(map[string]any{"text": content.String(), "reasoning": reasoning.String(), "calls": calls})
	}
	actual, err := project(parts)
	wanted, expectedErr := project(expected)
	defer clear(actual)
	defer clear(wanted)
	if err != nil || expectedErr != nil || !sameJSON(actual, wanted) {
		return nil, reject("protocol preflight", "extra_content.gateway", "continuation parts must match the assistant message")
	}
	return parts, nil
}

func mergeContinuationMetadata(part map[string]any, raw []byte) error {
	metadata, err := continuationMetadata(raw, false)
	if err != nil {
		return err
	}
	if len(metadata) == 0 {
		return nil
	}
	current, _ := part["providerMetadata"].(map[string]any)
	if current == nil {
		current = map[string]any{}
	}
	for provider, rawFields := range metadata {
		fields, _ := current[provider].(map[string]any)
		if fields == nil {
			fields = map[string]any{}
		}
		for key, value := range rawFields.(map[string]any) {
			fields[key] = value
		}
		current[provider] = fields
	}
	part["providerMetadata"] = current
	return nil
}
