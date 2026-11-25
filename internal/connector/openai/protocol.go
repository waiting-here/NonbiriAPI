package openai

import (
	"bytes"
	"crypto/rand"
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
	// no token value is ever fabricated from invalid individual buckets.
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

// A nil projection retains the original completion byte for byte.
func validateCompletion(body []byte) (Usage, []byte, error) {
	fields, err := decodeJSONObject(body, maxProtocolFields)
	if err != nil {
		return Usage{}, nil, errInvalidUpstreamResponse
	}
	defer clearFields(fields)
	root := fieldsByName(fields)
	if hasUpstreamError(root) {
		return Usage{}, nil, errInvalidUpstreamResponse
	}
	if !requiredString(root, "id", maxResponseIDRunes, true) ||
		!exactString(root, "object", "chat.completion") ||
		!requiredNonNegativeInt(root, "created") ||
		!requiredString(root, "model", MaxUpstreamModelRunes, true) {
		return Usage{}, nil, errInvalidUpstreamResponse
	}
	choices, err := validateChoices(root["choices"], false)
	if err != nil {
		return Usage{}, nil, errInvalidUpstreamResponse
	}
	// A malformed usage object degrades to unknown; it never invalidates an
	// otherwise protocol-valid completion.
	usage, _ := parseUsage(root["usage"])
	if choices == nil {
		return usage, nil, nil
	}
	root["choices"] = choices
	projected, err := marshalChatProjection(root)
	return usage, projected, err
}

// streamResponseIdentity is local to one upstream attempt. A valid first ID
// leaves the stream unchanged; only an invalid initial string enables repair.
type streamResponseIdentity struct {
	seen      bool
	id        string
	bytes     int64
	projected bool
}

func (s *streamResponseIdentity) withinBounds(data []byte, maxEvent, maxLine int, maxStream int64) bool {
	if len(data) > maxEvent || int64(len(data)) > maxStream {
		return false
	}
	// Track earlier frames too: content projection can start midway through
	// a stream. Ordinary passthrough retains its original ingress limits.
	size := int64(len(data)) + 8
	if (s.id != "" || s.projected) && (len(data)+6 > maxLine || size > maxStream-s.bytes-14) {
		return false
	}
	s.bytes += size
	return true
}

var errStreamSourceRejected = errors.New("openai connector: stream source rejected")

func validateChunk(data []byte) ([]byte, Usage, bool, error) {
	return validateStreamChunk(data, nil, nil)
}

func validateStreamChunk(data []byte, identity *streamResponseIdentity, sourceGuard *responseGuard) ([]byte, Usage, bool, error) {
	fields, err := decodeJSONObject(data, maxProtocolFields)
	if err != nil {
		return nil, Usage{}, false, errInvalidUpstreamResponse
	}
	defer clearFields(fields)
	root := fieldsByName(fields)
	if hasUpstreamError(root) {
		return nil, Usage{}, false, errInvalidUpstreamResponse
	}
	var sourceID string
	idRaw := bytes.TrimSpace(root["id"])
	// null, absent IDs and non-string values must never enter repair mode.
	idString := len(idRaw) > 0 && idRaw[0] == '"' && json.Unmarshal(idRaw, &sourceID) == nil
	idValid := idString && validOpaqueText(sourceID, maxResponseIDRunes, true)
	repairInitial := identity != nil && !identity.seen && idString && !idValid
	repairString := identity != nil && identity.id != "" && idString
	if (!idValid && !repairInitial && !repairString) ||
		!exactString(root, "object", "chat.completion.chunk") ||
		!requiredNonNegativeInt(root, "created") ||
		!requiredString(root, "model", MaxUpstreamModelRunes, true) {
		return nil, Usage{}, false, errInvalidUpstreamResponse
	}
	choices, err := validateChoices(root["choices"], true)
	if err != nil {
		return nil, Usage{}, false, errInvalidUpstreamResponse
	}
	usage, err := parseUsage(root["usage"])
	// The malformed flag lets the stream loop poison the whole request's
	// usage (invalid individual buckets must never yield billable values)
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
	// Scan original source bytes and decoded strings before replacing metadata.
	// The independent wire guard still checks the projected output afterwards.
	if sourceGuard.ContainsJSON(compact.Bytes(), compact.Bytes()) {
		clear(compact.Bytes())
		return nil, Usage{}, false, errStreamSourceRejected
	}
	if identity != nil {
		if repairInitial {
			identity.id = "chatcmpl-" + rand.Text()
		}
		identity.seen = true
		identity.projected = identity.projected || choices != nil
	}
	if choices != nil || identity != nil && identity.id != "" {
		clear(compact.Bytes())
		compact.Reset()
		var rewritten bytes.Buffer
		rewritten.Grow(len(data))
		rewritten.WriteByte('{')
		for i, field := range fields {
			if i != 0 {
				rewritten.WriteByte(',')
			}
			name, _ := json.Marshal(field.name)
			rewritten.Write(name)
			rewritten.WriteByte(':')
			switch {
			case field.name == "choices" && choices != nil:
				rewritten.Write(choices)
			case field.name == "id" && identity != nil && identity.id != "":
				value, _ := json.Marshal(identity.id)
				rewritten.Write(value)
			default:
				rewritten.Write(field.value)
			}
		}
		rewritten.WriteByte('}')
		err := json.Compact(&compact, rewritten.Bytes())
		clear(rewritten.Bytes())
		if err != nil {
			clear(compact.Bytes())
			return nil, Usage{}, false, errInvalidUpstreamResponse
		}
	}
	return compact.Bytes(), usage, malformed, nil
}

// A usage-only chunk cannot establish a successful response on its own.
func chunkHasChoices(data []byte) bool {
	var root struct {
		Choices []json.RawMessage `json:"choices"`
	}
	return json.Unmarshal(data, &root) == nil && len(root.Choices) != 0
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

func validateChoices(raw json.RawMessage, stream bool) ([]byte, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '[' {
		return nil, errInvalidUpstreamResponse
	}
	var choices []json.RawMessage
	if err := json.Unmarshal(trimmed, &choices); err != nil {
		clearRawMessages(choices)
		return nil, errInvalidUpstreamResponse
	}
	defer clearRawMessages(choices)
	if len(choices) > maxChoices || (!stream && len(choices) == 0) {
		return nil, errInvalidUpstreamResponse
	}
	field := "message"
	if stream {
		field = "delta"
	}
	changed := false
	for i, rawChoice := range choices {
		projected, err := validateChoice(rawChoice, field)
		if err != nil {
			return nil, err
		}
		if projected != nil {
			clear(rawChoice)
			choices[i] = projected
			changed = true
		}
	}
	if !changed {
		return nil, nil
	}
	return marshalChatProjection(choices)
}

func validateChoice(raw json.RawMessage, field string) ([]byte, error) {
	fields, err := decodeJSONObject(raw, maxProtocolFields)
	if err != nil {
		return nil, errInvalidUpstreamResponse
	}
	defer clearFields(fields)
	choice := fieldsByName(fields)
	if !requiredNonNegativeInt(choice, "index") {
		return nil, errInvalidUpstreamResponse
	}
	fields, err = decodeJSONObject(choice[field], maxProtocolFields)
	if err != nil {
		return nil, errInvalidUpstreamResponse
	}
	defer clearFields(fields)
	message := fieldsByName(fields)
	if !normalizeChatContentParts(message) {
		return nil, nil
	}
	choice[field], err = marshalChatProjection(message)
	if err != nil {
		return nil, errInvalidUpstreamResponse
	}
	return marshalChatProjection(choice)
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
	summed, sumOK := addChecked(prompt, completion)
	mismatch := !sumOK || summed != total
	if !sumOK {
		return Usage{TotalMismatch: true}, errUsageMalformed
	}
	var cacheRead, cacheWrite int64
	if detailsRaw := values["prompt_tokens_details"]; len(bytes.TrimSpace(detailsRaw)) != 0 && !isJSONNull(detailsRaw) {
		details, err := decodeJSONObject(detailsRaw, 64)
		if err != nil {
			return Usage{TotalMismatch: mismatch}, errUsageMalformed
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
			return Usage{TotalMismatch: mismatch}, errUsageMalformed
		}
	}
	cacheSummed, ok := addChecked(cacheRead, cacheWrite)
	if !ok || cacheSummed > prompt {
		return Usage{TotalMismatch: mismatch}, errUsageMalformed
	}
	usage := Usage{
		UncachedInputTokens:   prompt - cacheSummed,
		CacheWriteInputTokens: cacheWrite,
		CacheReadInputTokens:  cacheRead,
		OutputTokens:          completion,
		Present:               true,
		TotalMismatch:         mismatch,
	}
	normalizeChatModalities(&usage, values)
	return usage, nil
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
	if len(raw) == 0 || dst == nil || isJSONNull(raw) {
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
