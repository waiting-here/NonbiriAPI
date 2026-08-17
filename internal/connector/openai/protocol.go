package openai

import (
	"bytes"
	"encoding/json"
	"errors"
	"unicode/utf8"
)

const (
	MaxUpstreamModelRunes = 512
	maxResponseIDRunes    = 512
	maxChoices            = 128
	maxProtocolFields     = 1024
)

var errInvalidUpstreamResponse = errors.New("openai connector: invalid upstream response")

// Usage is validated token metadata only. Present distinguishes an omitted or
// null usage object from a real all-zero usage object.
type Usage struct {
	PromptTokens     int64
	CompletionTokens int64
	TotalTokens      int64
	Present          bool
}

func validateCompletion(body []byte) (Usage, error) {
	fields, err := decodeJSONObject(body, maxProtocolFields)
	if err != nil {
		return Usage{}, errInvalidUpstreamResponse
	}
	defer clearFields(fields)
	root := fieldsByName(fields)
	if hasUpstreamError(root) {
		return Usage{}, errInvalidUpstreamResponse
	}
	if !requiredString(root, "id", maxResponseIDRunes, true) ||
		!exactString(root, "object", "chat.completion") ||
		!requiredNonNegativeInt(root, "created") ||
		!requiredString(root, "model", MaxUpstreamModelRunes, true) {
		return Usage{}, errInvalidUpstreamResponse
	}
	if err := validateChoices(root["choices"], false); err != nil {
		return Usage{}, errInvalidUpstreamResponse
	}
	usage, err := parseUsage(root["usage"])
	if err != nil {
		return Usage{}, errInvalidUpstreamResponse
	}
	return usage, nil
}

func validateChunk(data []byte) ([]byte, Usage, error) {
	fields, err := decodeJSONObject(data, maxProtocolFields)
	if err != nil {
		return nil, Usage{}, errInvalidUpstreamResponse
	}
	defer clearFields(fields)
	root := fieldsByName(fields)
	if hasUpstreamError(root) {
		return nil, Usage{}, errInvalidUpstreamResponse
	}
	if !requiredString(root, "id", maxResponseIDRunes, true) ||
		!exactString(root, "object", "chat.completion.chunk") ||
		!requiredNonNegativeInt(root, "created") ||
		!requiredString(root, "model", MaxUpstreamModelRunes, true) {
		return nil, Usage{}, errInvalidUpstreamResponse
	}
	if err := validateChoices(root["choices"], true); err != nil {
		return nil, Usage{}, errInvalidUpstreamResponse
	}
	usage, err := parseUsage(root["usage"])
	if err != nil {
		return nil, Usage{}, errInvalidUpstreamResponse
	}

	var compact bytes.Buffer
	compact.Grow(len(data))
	if err := json.Compact(&compact, data); err != nil {
		return nil, Usage{}, errInvalidUpstreamResponse
	}
	return compact.Bytes(), usage, nil
}

func hasUpstreamError(root map[string]json.RawMessage) bool {
	raw, ok := root["error"]
	return ok && !isJSONNull(raw)
}

func requiredString(root map[string]json.RawMessage, name string, maxRunes int, rejectEdgeSpace bool) bool {
	raw, ok := root[name]
	if !ok {
		return false
	}
	var value string
	return json.Unmarshal(raw, &value) == nil && validOpaqueText(value, maxRunes, rejectEdgeSpace)
}

func exactString(root map[string]json.RawMessage, name, expected string) bool {
	raw, ok := root[name]
	if !ok {
		return false
	}
	var value string
	return json.Unmarshal(raw, &value) == nil && value == expected
}

func requiredNonNegativeInt(root map[string]json.RawMessage, name string) bool {
	raw, ok := root[name]
	if !ok {
		return false
	}
	var value int64
	return json.Unmarshal(raw, &value) == nil && value >= 0
}

func validateChoices(raw json.RawMessage, stream bool) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '[' {
		return errInvalidUpstreamResponse
	}
	var choices []json.RawMessage
	if err := json.Unmarshal(trimmed, &choices); err != nil {
		clearRawMessages(choices)
		return errInvalidUpstreamResponse
	}
	if len(choices) > maxChoices || (!stream && len(choices) == 0) {
		clearRawMessages(choices)
		return errInvalidUpstreamResponse
	}
	for _, rawChoice := range choices {
		fields, err := decodeJSONObject(rawChoice, maxProtocolFields)
		if err != nil {
			clearRawMessages(choices)
			return errInvalidUpstreamResponse
		}
		choice := fieldsByName(fields)
		valid := requiredNonNegativeInt(choice, "index")
		if valid {
			if stream {
				valid = requiredJSONObject(choice["delta"])
			} else {
				valid = requiredJSONObject(choice["message"])
			}
		}
		clearFields(fields)
		if !valid {
			clearRawMessages(choices)
			return errInvalidUpstreamResponse
		}
	}
	clearRawMessages(choices)
	return nil
}

func requiredJSONObject(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return false
	}
	fields, err := decodeJSONObject(trimmed, maxProtocolFields)
	if err != nil {
		return false
	}
	clearFields(fields)
	return true
}

func parseUsage(raw json.RawMessage) (Usage, error) {
	if len(bytes.TrimSpace(raw)) == 0 || isJSONNull(raw) {
		return Usage{}, nil
	}
	fields, err := decodeJSONObject(raw, 64)
	if err != nil {
		return Usage{}, errInvalidUpstreamResponse
	}
	defer clearFields(fields)
	values := fieldsByName(fields)
	var usage Usage
	if !decodeNonNegativeInt(values["prompt_tokens"], &usage.PromptTokens) ||
		!decodeNonNegativeInt(values["completion_tokens"], &usage.CompletionTokens) ||
		!decodeNonNegativeInt(values["total_tokens"], &usage.TotalTokens) {
		return Usage{}, errInvalidUpstreamResponse
	}
	usage.Present = true
	return usage, nil
}

func decodeNonNegativeInt(raw json.RawMessage, dst *int64) bool {
	if len(raw) == 0 || dst == nil {
		return false
	}
	return json.Unmarshal(raw, dst) == nil && *dst >= 0
}

func isJSONNull(raw json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

func clearRawMessages(messages []json.RawMessage) {
	for i := range messages {
		clear(messages[i])
		messages[i] = nil
	}
}

// validProtocolBytes catches raw invalid UTF-8 before encoding/json can replace
// it with U+FFFD. Unknown generated content and identifiers may contain a
// literal U+FFFD; identifier length, controls, and edge whitespace are checked
// separately by validOpaqueText.
func validProtocolBytes(data []byte) bool { return utf8.Valid(data) }
