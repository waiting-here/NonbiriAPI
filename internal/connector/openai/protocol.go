package openai

import (
	"bytes"
	"encoding/json"
	"errors"
	"unicode/utf8"

	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

const (
	MaxUpstreamModelRunes = 512
	maxResponseIDRunes    = 512
	maxChoices            = 128
	maxProtocolFields     = 1024
)

var (
	errInvalidUpstreamResponse = errors.New("openai connector: invalid upstream response")
	// errUsageMalformed marks a usage object that exists but violates the
	// token invariants (wrong shape, non-integer or negative token values,
	// cache sub-items exceeding the prompt total, or checked-sum overflow).
	// It degrades the whole usage to unknown instead of failing the response:
	// no token value is ever fabricated from contradictory upstream data.
	errUsageMalformed = errors.New("openai connector: malformed usage")
)

// Usage is the normalized, connector-neutral four-bucket token metadata.
// The buckets are mutually exclusive and non-negative:
//
//	uncached_input = prompt_tokens - cached_tokens - cache_write_tokens
//	cache_write    = prompt_tokens_details.cache_write_tokens (0 when absent)
//	cache_read     = prompt_tokens_details.cached_tokens
//	output         = completion_tokens
//
// OpenAI's prompt_tokens already includes cache reads and writes, so the
// uncached bucket is derived by subtraction after validating that both cache
// sub-items are non-negative and their checked sum never exceeds the prompt
// total. Present distinguishes an omitted or null usage object (and a
// malformed one, which degrades to unknown) from a real all-zero usage object.
type Usage = connectorcontract.Usage

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
	// A malformed usage object degrades to unknown; it never invalidates an
	// otherwise protocol-valid completion.
	usage, _ := parseUsage(root["usage"])
	return usage, nil
}

func validateChunk(data []byte) ([]byte, Usage, bool, error) {
	fields, err := decodeJSONObject(data, maxProtocolFields)
	if err != nil {
		return nil, Usage{}, false, errInvalidUpstreamResponse
	}
	defer clearFields(fields)
	root := fieldsByName(fields)
	if hasUpstreamError(root) {
		return nil, Usage{}, false, errInvalidUpstreamResponse
	}
	if !requiredString(root, "id", maxResponseIDRunes, true) ||
		!exactString(root, "object", "chat.completion.chunk") ||
		!requiredNonNegativeInt(root, "created") ||
		!requiredString(root, "model", MaxUpstreamModelRunes, true) {
		return nil, Usage{}, false, errInvalidUpstreamResponse
	}
	if err := validateChoices(root["choices"], true); err != nil {
		return nil, Usage{}, false, errInvalidUpstreamResponse
	}
	usage, err := parseUsage(root["usage"])
	// The malformed flag lets the stream loop poison the whole request's
	// usage (contradictory upstream data must never yield token values)
	// while the otherwise valid chunk still forwards to the client.
	malformed := errors.Is(err, errUsageMalformed)
	if err != nil && !malformed {
		return nil, Usage{}, false, errInvalidUpstreamResponse
	}

	var compact bytes.Buffer
	compact.Grow(len(data))
	if err := json.Compact(&compact, data); err != nil {
		return nil, Usage{}, false, errInvalidUpstreamResponse
	}
	return compact.Bytes(), usage, malformed, nil
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

// parseUsage normalizes one usage object into the neutral four buckets.
// An absent or null usage yields Present=false with no error. A present but
// malformed usage (wrong shape, float/string/negative token values, cache
// sub-items exceeding the prompt total, or checked-sum overflow) yields
// errUsageMalformed and no values: the caller must treat the whole usage as
// unknown rather than fabricate numbers. prompt_tokens, completion_tokens,
// and total_tokens stay required non-negative integers; the cache detail
// sub-object and its members are optional and default to zero.
func parseUsage(raw json.RawMessage) (Usage, error) {
	if len(bytes.TrimSpace(raw)) == 0 || isJSONNull(raw) {
		return Usage{}, nil
	}
	fields, err := decodeJSONObject(raw, 64)
	if err != nil {
		return Usage{}, errUsageMalformed
	}
	defer clearFields(fields)
	values := fieldsByName(fields)
	var prompt, completion, total int64
	if !decodeNonNegativeInt(values["prompt_tokens"], &prompt) ||
		!decodeNonNegativeInt(values["completion_tokens"], &completion) ||
		!decodeNonNegativeInt(values["total_tokens"], &total) {
		return Usage{}, errUsageMalformed
	}
	var cacheRead, cacheWrite int64
	if detailsRaw := values["prompt_tokens_details"]; len(bytes.TrimSpace(detailsRaw)) != 0 && !isJSONNull(detailsRaw) {
		details, err := decodeJSONObject(detailsRaw, 64)
		if err != nil {
			return Usage{}, errUsageMalformed
		}
		detailValues := fieldsByName(details)
		readOK, writeOK := true, true
		if raw := detailValues["cached_tokens"]; len(raw) != 0 {
			readOK = decodeNonNegativeInt(raw, &cacheRead)
		}
		if raw := detailValues["cache_write_tokens"]; len(raw) != 0 {
			writeOK = decodeNonNegativeInt(raw, &cacheWrite)
		}
		clearFields(details)
		if !readOK || !writeOK {
			return Usage{}, errUsageMalformed
		}
	}
	cacheSummed, ok := addChecked(cacheRead, cacheWrite)
	if !ok || cacheSummed > prompt {
		return Usage{}, errUsageMalformed
	}
	return Usage{
		UncachedInputTokens:   prompt - cacheSummed,
		CacheWriteInputTokens: cacheWrite,
		CacheReadInputTokens:  cacheRead,
		OutputTokens:          completion,
		Present:               true,
	}, nil
}

// addChecked adds two non-negative-oriented int64 values and reports whether
// the result fits in int64.
func addChecked(a, b int64) (int64, bool) {
	sum := a + b
	if (b > 0 && sum < a) || (b < 0 && sum > a) {
		return 0, false
	}
	return sum, true
}

// decodeNonNegativeInt decodes a JSON integer into dst. Fractional or
// exponent-encoded numbers (for example 1.5 or 1e3) fail because encoding/json
// refuses them for an int64 target, so string and float token counts can never
// enter the buckets.
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
