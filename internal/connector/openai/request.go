// Package openai implements the OpenAI-compatible upstream connector. It owns
// protocol parsing and framing only; every network call is made through the
// shared egress Stack supplied to Adapter.
package openai

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strconv"
	"unicode"
	"unicode/utf8"
)

const (
	// MaxRequestBodyBytes is the caller request ceiling for chat completions.
	MaxRequestBodyBytes int64 = 1 << 20
	// MaxPlatformModelRunes follows the persisted provider/model bounds:
	// 64 runes + one separator + 64 runes. The value remains opaque here.
	MaxPlatformModelRunes = 129
	maxTopLevelFields     = 1024
	maxFieldNameRunes     = 256
	maxForwardBodyBytes   = MaxRequestBodyBytes + (16 << 10)
)

var (
	// ErrInvalidRequest deliberately carries no request-derived detail.
	ErrInvalidRequest = errors.New("openai connector: invalid request")
	// ErrPayloadTooLarge identifies the finite caller-body boundary.
	ErrPayloadTooLarge = errors.New("openai connector: request body too large")
)

type jsonField struct {
	name  string
	value json.RawMessage
}

// ChatRequest is a validated caller request. Unknown top-level fields are
// retained as raw JSON and passed through semantically unchanged. The model
// and safety_identifier values are replaced at dispatch time. Values are
// private so they cannot accidentally enter a metadata hook or formatter.
type ChatRequest struct {
	fields []jsonField
	Model  string
	Stream bool
}

func (*ChatRequest) String() string   { return "[redacted chat request]" }
func (*ChatRequest) GoString() string { return "[redacted chat request]" }
func (*ChatRequest) LogValue() slog.Value {
	return slog.StringValue("[redacted chat request]")
}

// DecodeChatRequest reads and validates one bounded JSON object. Unknown
// fields are accepted, duplicate fields and trailing JSON values are rejected,
// model is a bounded opaque string, and stream (when present) must be a bool.
func DecodeChatRequest(body io.Reader, limit int64) (*ChatRequest, error) {
	if body == nil {
		return nil, ErrInvalidRequest
	}
	if limit <= 0 || limit > MaxRequestBodyBytes {
		limit = MaxRequestBodyBytes
	}
	data, err := readBounded(body, limit)
	if err != nil {
		if errors.Is(err, ErrPayloadTooLarge) {
			return nil, err
		}
		return nil, ErrInvalidRequest
	}
	defer clear(data)

	fields, err := decodeJSONObject(data, maxTopLevelFields)
	if err != nil {
		clearFields(fields)
		return nil, ErrInvalidRequest
	}
	request := &ChatRequest{fields: fields}
	modelSeen := false
	streamSeen := false
	for _, field := range fields {
		switch field.name {
		case "model":
			modelSeen = true
			if err := json.Unmarshal(field.value, &request.Model); err != nil || !validOpaqueText(request.Model, MaxPlatformModelRunes, true) {
				request.Clear()
				return nil, ErrInvalidRequest
			}
		case "stream":
			streamSeen = true
			trimmed := bytes.TrimSpace(field.value)
			if !bytes.Equal(trimmed, []byte("true")) && !bytes.Equal(trimmed, []byte("false")) {
				request.Clear()
				return nil, ErrInvalidRequest
			}
			if err := json.Unmarshal(trimmed, &request.Stream); err != nil {
				request.Clear()
				return nil, ErrInvalidRequest
			}
		}
	}
	if !modelSeen {
		request.Clear()
		return nil, ErrInvalidRequest
	}
	if !streamSeen {
		request.Stream = false
	}
	return request, nil
}

// Clear releases and overwrites the retained request JSON on a best-effort
// basis. The handler calls it after the single-attempt service returns; future
// retry orchestration may retain it only for the duration of those attempts.
func (r *ChatRequest) Clear() {
	if r == nil {
		return
	}
	clearFields(r.fields)
	r.fields = nil
}

// marshalUpstream replaces the caller's platform model and any forged
// safety_identifier while retaining every other field. It writes a fresh JSON
// object instead of mutating caller bytes in place, so duplicate-key ambiguity
// cannot be reintroduced.
func (r *ChatRequest) marshalUpstream(upstreamModel, safetyIdentifier string) ([]byte, error) {
	if r == nil || !validOpaqueText(upstreamModel, MaxUpstreamModelRunes, true) || !validSafetyIdentifier(safetyIdentifier) {
		return nil, ErrInvalidRequest
	}
	modelJSON, _ := json.Marshal(upstreamModel)
	safetyJSON, _ := json.Marshal(safetyIdentifier)

	var out bytes.Buffer
	out.Grow(min(int(maxForwardBodyBytes), 4096))
	out.WriteByte('{')
	wrote := 0
	safetySeen := false
	for _, field := range r.fields {
		if wrote != 0 {
			out.WriteByte(',')
		}
		out.WriteString(strconv.Quote(field.name))
		out.WriteByte(':')
		switch field.name {
		case "model":
			out.Write(modelJSON)
		case "safety_identifier":
			out.Write(safetyJSON)
			safetySeen = true
		case "stream_options":
			if r.Stream {
				if merged, ok := withUsageEnabled(field.value); ok {
					out.Write(merged)
					clear(merged)
				} else {
					out.Write(field.value)
				}
			} else {
				out.Write(field.value)
			}
		default:
			out.Write(field.value)
		}
		wrote++
		if int64(out.Len()) > maxForwardBodyBytes {
			clear(out.Bytes())
			return nil, ErrPayloadTooLarge
		}
	}
	if !safetySeen {
		if wrote != 0 {
			out.WriteByte(',')
		}
		out.WriteString(`"safety_identifier":`)
		out.Write(safetyJSON)
		wrote++
	}
	if r.Stream && !hasField(r.fields, "stream_options") {
		if wrote != 0 {
			out.WriteByte(',')
		}
		out.WriteString(`"stream_options":{"include_usage":true}`)
	}
	out.WriteByte('}')
	if int64(out.Len()) > maxForwardBodyBytes {
		clear(out.Bytes())
		return nil, ErrPayloadTooLarge
	}
	return out.Bytes(), nil
}

func readBounded(reader io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		clear(data)
		return nil, err
	}
	if int64(len(data)) > limit {
		clear(data)
		return nil, ErrPayloadTooLarge
	}
	return data, nil
}

// decodeJSONObject rejects duplicate decoded keys, non-object roots, invalid
// UTF-8, excessive field counts, and trailing values. Raw field values are
// copied by encoding/json and remain valid after the input buffer is cleared.
func decodeJSONObject(data []byte, maxFields int) ([]jsonField, error) {
	if len(data) == 0 || !utf8.Valid(data) || maxFields < 1 {
		return nil, ErrInvalidRequest
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	first, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	delim, ok := first.(json.Delim)
	if !ok || delim != '{' {
		return nil, ErrInvalidRequest
	}

	fields := make([]jsonField, 0, min(maxFields, 16))
	seen := make(map[string]struct{}, min(maxFields, 16))
	for decoder.More() {
		if len(fields) == maxFields {
			clearFields(fields)
			return nil, ErrInvalidRequest
		}
		token, err := decoder.Token()
		if err != nil {
			clearFields(fields)
			return nil, err
		}
		name, ok := token.(string)
		if !ok || !validFieldName(name) {
			clearFields(fields)
			return nil, ErrInvalidRequest
		}
		if _, duplicate := seen[name]; duplicate {
			clearFields(fields)
			return nil, ErrInvalidRequest
		}
		seen[name] = struct{}{}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			clear(value)
			clearFields(fields)
			return nil, err
		}
		fields = append(fields, jsonField{name: name, value: value})
	}
	last, err := decoder.Token()
	if err != nil {
		clearFields(fields)
		return nil, err
	}
	if delim, ok := last.(json.Delim); !ok || delim != '}' {
		clearFields(fields)
		return nil, ErrInvalidRequest
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		clearFields(fields)
		return nil, ErrInvalidRequest
	}
	return fields, nil
}

func withUsageEnabled(raw json.RawMessage) ([]byte, bool) {
	fields, err := decodeJSONObject(raw, 64)
	if err != nil {
		return nil, false
	}
	defer clearFields(fields)
	var output bytes.Buffer
	output.WriteByte('{')
	wrote := 0
	found := false
	for _, field := range fields {
		if wrote != 0 {
			output.WriteByte(',')
		}
		output.WriteString(strconv.Quote(field.name))
		output.WriteByte(':')
		if field.name == "include_usage" {
			output.WriteString("true")
			found = true
		} else {
			output.Write(field.value)
		}
		wrote++
	}
	if !found {
		if wrote != 0 {
			output.WriteByte(',')
		}
		output.WriteString(`"include_usage":true`)
	}
	output.WriteByte('}')
	return output.Bytes(), true
}

func hasField(fields []jsonField, name string) bool {
	for _, field := range fields {
		if field.name == name {
			return true
		}
	}
	return false
}

func fieldsByName(fields []jsonField) map[string]json.RawMessage {
	result := make(map[string]json.RawMessage, len(fields))
	for _, field := range fields {
		result[field.name] = field.value
	}
	return result
}

func clearFields(fields []jsonField) {
	for i := range fields {
		clear(fields[i].value)
		fields[i].value = nil
	}
}

func validFieldName(value string) bool {
	if value == "" || !utf8.ValidString(value) || utf8.RuneCountInString(value) > maxFieldNameRunes {
		return false
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func validOpaqueText(value string, maxRunes int, rejectEdgeSpace bool) bool {
	if value == "" || !utf8.ValidString(value) || utf8.RuneCountInString(value) > maxRunes {
		return false
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	if rejectEdgeSpace {
		first, _ := utf8.DecodeRuneInString(value)
		last, _ := utf8.DecodeLastRuneInString(value)
		if unicode.IsSpace(first) || unicode.IsSpace(last) {
			return false
		}
	}
	return true
}

func validSafetyIdentifier(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-' {
			continue
		}
		return false
	}
	return true
}
