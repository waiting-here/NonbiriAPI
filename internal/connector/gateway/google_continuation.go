package gateway

import (
	"encoding/json"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/connector/continuation"
)

func googleContinuationError(reason string) error {
	return reject("protocol preflight", "extra_content.google", reason)
}

func googleThoughtSignature(raw []byte) (string, error) {
	value, ok := text(raw)
	if !ok || value == "" || len(value) > 1<<20 || strings.ContainsAny(value, "\x00\r\n") {
		return "", googleContinuationError("expected a nonempty opaque thought signature")
	}
	return value, nil
}

// AI SDK v3/v4 consume per-part providerOptions, while generated parts carry
// providerMetadata. Never replay arbitrary provider settings from Chat extras.
func setGoogleSignature(part map[string]any, signature string) error {
	options, _ := part["providerOptions"].(map[string]any)
	if options == nil {
		options = map[string]any{}
	}
	for _, namespace := range []string{"google", "googleVertex", "vertex"} {
		fields, _ := options[namespace].(map[string]any)
		if existing, present := fields["thoughtSignature"]; present && existing != signature {
			return googleContinuationError("thought signatures for the same part conflict")
		}
	}
	fields, _ := options["google"].(map[string]any)
	if fields == nil {
		fields = map[string]any{}
	}
	fields["thoughtSignature"] = signature
	options["google"] = fields
	part["providerOptions"] = options
	return nil
}

func applyToolSignature(extra []byte, part map[string]any) error {
	if len(extra) == 0 || isNull(extra) {
		return nil
	}
	outer, err := parseObject(extra)
	if err != nil || !continuation.ValidExtra(outer, "google") {
		return googleContinuationError("tool continuation requires a Google thought signature")
	}
	google, err := parseObject(outer["google"])
	if err != nil || !only(google, "thought_signature") {
		return googleContinuationError("tool continuation requires a Google thought signature")
	}
	signature, err := googleThoughtSignature(google["thought_signature"])
	if err != nil {
		return err
	}
	return setGoogleSignature(part, signature)
}

func replayToolSignatures(parts, expected []any) error {
	signatures := map[string]string{}
	for _, raw := range expected {
		part := raw.(map[string]any)
		if part["type"] != "tool-call" {
			continue
		}
		options, _ := part["providerOptions"].(map[string]any)
		google, _ := options["google"].(map[string]any)
		if signature, ok := google["thoughtSignature"].(string); ok {
			signatures[part["toolCallId"].(string)] = signature
		}
	}
	for _, raw := range parts {
		part := raw.(map[string]any)
		if part["type"] == "tool-call" {
			if signature := signatures[part["toolCallId"].(string)]; signature != "" {
				if err := setGoogleSignature(part, signature); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// Native Google text/thought/functionCall parts have direct SDK equivalents.
// Keep their order and exact signatures; reject fields without a representation.
func googleContinuationParts(raw []byte, expected []any) ([]any, error) {
	var native []json.RawMessage
	if json.Unmarshal(raw, &native) != nil || len(native) == 0 || len(native) > 4096 {
		return nil, googleContinuationError("expected supported native continuation parts")
	}
	calls := []map[string]any{}
	for _, raw := range expected {
		part := raw.(map[string]any)
		if part["type"] == "tool-call" {
			calls = append(calls, part)
		}
	}
	parts := make([]any, 0, len(native))
	callIndex := 0
	for _, raw := range native {
		part, err := parseObject(raw)
		if err != nil || !only(part, "text", "thought", "thoughtSignature", "functionCall") {
			return nil, googleContinuationError("native part has fields without a Gateway representation")
		}
		thought := false
		if raw, present := part["thought"]; present && string(raw) != "true" && string(raw) != "false" {
			return nil, googleContinuationError("expected a boolean thought marker")
		} else if present {
			thought = string(raw) == "true"
		}
		_, hasText := part["text"]
		_, hasCall := part["functionCall"]
		if hasText == hasCall || hasCall && thought {
			return nil, googleContinuationError("native part requires either text or a client function call")
		}
		value := map[string]any{}
		if hasText {
			content, ok := text(part["text"])
			if !ok {
				return nil, googleContinuationError("expected native text")
			}
			value["type"], value["text"] = "text", content
			if thought {
				value["type"] = "reasoning"
			}
		} else {
			call, err := parseObject(part["functionCall"])
			name, nameOK := text(call["name"])
			if err != nil || !only(call, "id", "name", "args") || !nameOK || !opaque(name, 64) || callIndex >= len(calls) {
				return nil, googleContinuationError("native function call must match the assistant tool call")
			}
			// Google IDs are optional. The ordered outer Chat call supplies the
			// missing ID; the subsequent complete projection check binds name/args.
			id := calls[callIndex]["toolCallId"].(string)
			if raw, present := call["id"]; present {
				var valid bool
				id, valid = text(raw)
				if !valid || !opaque(id, 512) {
					return nil, googleContinuationError("expected a native function call ID")
				}
			}
			args, present := call["args"]
			if !present {
				args = json.RawMessage(`{}`)
			}
			if _, err := parseObject(args); err != nil {
				return nil, googleContinuationError("expected native function arguments")
			}
			value["type"], value["toolCallId"], value["toolName"], value["input"] = "tool-call", id, name, args
			callIndex++
		}
		if raw, present := part["thoughtSignature"]; present {
			signature, err := googleThoughtSignature(raw)
			if err != nil {
				return nil, err
			}
			if err := setGoogleSignature(value, signature); err != nil {
				return nil, err
			}
		}
		parts = append(parts, value)
	}
	return parts, nil
}
