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

const MaxEmbeddingBatch = 2048

// EmbeddingRequest owns one validated protocol snapshot. It never represents
// a chat message or guesses model capabilities from a model name.
type EmbeddingRequest struct {
	fields         []jsonField
	excluded       []string
	Model          string
	InputCount     int
	EncodingFormat string
	Dimensions     int
}

// TopLevelFields returns an immutable field-name projection for fidelity checks.
func (r *EmbeddingRequest) TopLevelFields() []string {
	if r == nil {
		return nil
	}
	names := make([]string, len(r.fields))
	for i, field := range r.fields {
		names[i] = field.name
	}
	return names
}

func (*EmbeddingRequest) String() string   { return "[redacted embedding request]" }
func (*EmbeddingRequest) GoString() string { return "[redacted embedding request]" }
func (*EmbeddingRequest) LogValue() slog.Value {
	return slog.StringValue("[redacted embedding request]")
}

func DecodeEmbeddingRequest(body io.Reader, limit int64) (*EmbeddingRequest, error) {
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
	if !validateEmbeddingJSON(data) {
		return nil, ErrInvalidRequest
	}
	fields, ok := borrowedObjectFields(data)
	if !ok {
		return nil, ErrInvalidRequest
	}
	r := &EmbeddingRequest{EncodingFormat: "float"}
	for _, field := range fields {
		switch field.name {
		case "model":
			if json.Unmarshal(field.value, &r.Model) != nil || !validOpaqueText(r.Model, MaxPlatformModelRunes, true) {
				return nil, ErrInvalidRequest
			}
		case "input":
			r.InputCount = embeddingInputCount(field.value)
			if r.InputCount == 0 {
				return nil, ErrInvalidRequest
			}
		case "encoding_format":
			if json.Unmarshal(field.value, &r.EncodingFormat) != nil || (r.EncodingFormat != "float" && r.EncodingFormat != "base64") || isJSONNull(field.value) {
				return nil, ErrInvalidRequest
			}
		case "dimensions":
			value, ok := embeddingInteger(field.value)
			if !ok || value == 0 {
				return nil, ErrInvalidRequest
			}
			r.Dimensions = int(value)
		case "user":
			var value string
			if isJSONNull(field.value) || json.Unmarshal(field.value, &value) != nil || utf8.RuneCountInString(value) > 512 {
				return nil, ErrInvalidRequest
			}
			for _, r := range value {
				if unicode.IsControl(r) {
					return nil, ErrInvalidRequest
				}
			}
		case "stream":
			if !bytes.Equal(field.value, []byte("false")) {
				return nil, ErrInvalidRequest
			}
		}
	}
	if r.Model == "" || r.InputCount == 0 {
		return nil, ErrInvalidRequest
	}
	r.fields = make([]jsonField, len(fields))
	for i, field := range fields {
		r.fields[i] = jsonField{name: field.name, value: append(json.RawMessage(nil), field.value...)}
	}
	return r, nil
}

func embeddingInteger(raw []byte) (int64, bool) {
	if len(raw) == 0 || len(raw) > 10 {
		return 0, false
	}
	for _, ch := range raw {
		if ch < '0' || ch > '9' {
			return 0, false
		}
	}
	value, err := strconv.ParseInt(string(raw), 10, 32)
	return value, err == nil
}

func embeddingInputCount(raw []byte) int {
	if len(raw) == 0 {
		return 0
	}
	validText := func(value []byte) bool {
		var text string
		return len(value) > 0 && value[0] == '"' && json.Unmarshal(value, &text) == nil && text != ""
	}
	if raw[0] == '"' {
		if validText(raw) {
			return 1
		}
		return 0
	}
	count := 0
	var kind byte
	if !visitJSONArray(raw, func(value []byte) bool {
		if count == 0 {
			kind = value[0]
		}
		count++
		switch kind {
		case '"':
			return count <= MaxEmbeddingBatch && validText(value)
		case '[':
			if count > MaxEmbeddingBatch {
				return false
			}
			tokens := 0
			ok := visitJSONArray(value, func(token []byte) bool {
				tokens++
				_, valid := embeddingInteger(token)
				return valid
			})
			return ok && tokens > 0
		default:
			_, ok := embeddingInteger(value)
			return ok
		}
	}) || count == 0 {
		return 0
	}
	if kind == '"' || kind == '[' {
		return count
	}
	return 1
}

func (r *EmbeddingRequest) Clear() {
	if r == nil {
		return
	}
	clearFields(r.fields)
	r.fields = nil
	clear(r.excluded)
	r.excluded = nil
	r.Model, r.EncodingFormat = "", ""
	r.InputCount, r.Dimensions = 0, 0
}

func (r *EmbeddingRequest) CloneForAttempt() *EmbeddingRequest {
	if r == nil {
		return nil
	}
	clone := *r
	clone.excluded = append([]string(nil), r.excluded...)
	clone.fields = make([]jsonField, len(r.fields))
	for i, field := range r.fields {
		clone.fields[i] = jsonField{name: field.name, value: append(json.RawMessage(nil), field.value...)}
	}
	return &clone
}

func (r *EmbeddingRequest) RawField(name string) ([]byte, bool) {
	if r != nil {
		for _, field := range r.fields {
			if field.name == name {
				return append([]byte(nil), field.value...), true
			}
		}
	}
	return nil, false
}

func (r *EmbeddingRequest) marshalUpstream(upstreamModel, identifier string) ([]byte, error) {
	if r == nil || len(r.fields) == 0 || !validOpaqueText(upstreamModel, MaxUpstreamModelRunes, true) || !validSafetyIdentifier(identifier) {
		return nil, ErrInvalidRequest
	}
	modelJSON, _ := json.Marshal(upstreamModel)
	defer clear(modelJSON)
	identifierJSON, _ := json.Marshal(identifier)
	defer clear(identifierJSON)
	var out bytes.Buffer
	out.WriteByte('{')
	userSeen := false
	for i, field := range r.fields {
		if i > 0 {
			out.WriteByte(',')
		}
		name, _ := json.Marshal(field.name)
		out.Write(name)
		out.WriteByte(':')
		switch field.name {
		case "model":
			out.Write(modelJSON)
		case "user":
			out.Write(identifierJSON)
			userSeen = true
		case "safety_identifier":
			out.Write(identifierJSON)
		default:
			out.Write(field.value)
		}
	}
	if !userSeen && !r.FieldExcluded("user") {
		out.WriteString(`,"user":`)
		out.Write(identifierJSON)
	}
	out.WriteByte('}')
	if int64(out.Len()) > maxForwardBodyBytes {
		clear(out.Bytes())
		return nil, ErrPayloadTooLarge
	}
	return out.Bytes(), nil
}
