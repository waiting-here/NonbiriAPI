package openai

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

// RequestEnvelope retains bounded ingress JSON before optional parameter
// validation. It exposes no input value to policy lookup or ordinary logging.
type RequestEnvelope struct {
	fields []jsonField
	Model  string
	Stream bool
}

func (*RequestEnvelope) String() string   { return "[redacted request envelope]" }
func (*RequestEnvelope) GoString() string { return "[redacted request envelope]" }
func (*RequestEnvelope) LogValue() slog.Value {
	return slog.StringValue("[redacted request envelope]")
}

// NormalizeExcludedRequestFields validates a finite, case-sensitive set of
// top-level names. Core routing, input and response protocol fields cannot be
// excluded. A returned slice is owned by the caller.
func NormalizeExcludedRequestFields(fields []string) ([]string, error) {
	if len(fields) > 32 {
		return nil, ErrInvalidRequest
	}
	result := make([]string, 0, len(fields))
	seen := make(map[string]bool, len(fields))
	for _, name := range fields {
		if len(name) < 1 || len(name) > 64 {
			return nil, ErrInvalidRequest
		}
		for index := range len(name) {
			ch := name[index]
			if ch != '_' && !(ch >= 'A' && ch <= 'Z') && !(ch >= 'a' && ch <= 'z') && !(index > 0 && ch >= '0' && ch <= '9') {
				return nil, ErrInvalidRequest
			}
		}
		switch name {
		case "model", "messages", "input", "stream", "stream_options", "tools", "tool_choice", "functions", "function_call", "response_format", "encoding_format":
			return nil, ErrInvalidRequest
		}
		if !seen[name] {
			seen[name] = true
			result = append(result, name)
		}
	}
	return result, nil
}

// DecodeRequestEnvelope checks the complete JSON boundary and protected
// routing/input fields without interpreting optional business parameters.
// The ordinary operation decoder validates the independently filtered copy.
func DecodeRequestEnvelope(body io.Reader, limit int64, operation contract.Operation) (*RequestEnvelope, error) {
	if body == nil || operation != contract.OperationChatCompletions && operation != contract.OperationEmbeddings {
		return nil, ErrInvalidRequest
	}
	if limit <= 0 || limit > MaxRequestBodyBytes {
		limit = MaxRequestBodyBytes
	}
	data, err := readBounded(body, limit)
	if err != nil {
		return nil, err
	}
	defer clear(data)
	if operation == contract.OperationEmbeddings && !validateEmbeddingJSON(data) {
		return nil, ErrInvalidRequest
	}
	fields, err := decodeJSONObject(data, maxTopLevelFields)
	if err != nil {
		return nil, ErrInvalidRequest
	}
	result := &RequestEnvelope{fields: fields}
	valid := false
	defer func() {
		if !valid {
			result.Clear()
		}
	}()
	inputSeen := false
	for _, field := range fields {
		raw := bytes.TrimSpace(field.value)
		switch field.name {
		case "model":
			if json.Unmarshal(raw, &result.Model) != nil || !validOpaqueText(result.Model, MaxPlatformModelRunes, true) {
				return nil, ErrInvalidRequest
			}
		case "stream":
			if operation == contract.OperationEmbeddings {
				if !bytes.Equal(raw, []byte("false")) {
					return nil, ErrInvalidRequest
				}
			} else if !bytes.Equal(raw, []byte("null")) {
				if !bytes.Equal(raw, []byte("true")) && !bytes.Equal(raw, []byte("false")) || json.Unmarshal(raw, &result.Stream) != nil {
					return nil, ErrInvalidRequest
				}
			}
		case "input":
			if operation == contract.OperationEmbeddings {
				if embeddingInputCount(raw) == 0 {
					return nil, ErrInvalidRequest
				}
				inputSeen = true
			}
		case "encoding_format":
			if operation == contract.OperationEmbeddings {
				var value string
				if json.Unmarshal(raw, &value) != nil || value != "float" && value != "base64" {
					return nil, ErrInvalidRequest
				}
			}
		}
	}
	if result.Model == "" || operation == contract.OperationEmbeddings && !inputSeen {
		return nil, ErrInvalidRequest
	}
	valid = true
	return result, nil
}

func (r *RequestEnvelope) Clear() {
	if r != nil {
		clearFields(r.fields)
		r.fields, r.Model, r.Stream = nil, "", false
	}
}

// WithoutFields writes a separate JSON object, never the original input.
// The exclusion names must also be attached to the decoded request through
// ExcludeFields so connector-added defaults cannot recreate them.
func (r *RequestEnvelope) WithoutFields(names []string) ([]byte, error) {
	if r == nil || r.Model == "" || len(r.fields) == 0 {
		return nil, ErrInvalidRequest
	}
	fields, err := NormalizeExcludedRequestFields(names)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	out.WriteByte('{')
	wrote := false
	for _, field := range r.fields {
		if excludedField(fields, field.name) {
			continue
		}
		if wrote {
			out.WriteByte(',')
		}
		out.WriteString(strconv.Quote(field.name))
		out.WriteByte(':')
		out.Write(field.value)
		wrote = true
	}
	out.WriteByte('}')
	if int64(out.Len()) > MaxRequestBodyBytes {
		clear(out.Bytes())
		return nil, ErrPayloadTooLarge
	}
	return out.Bytes(), nil
}

func excludedField(fields []string, name string) bool {
	for _, field := range fields {
		if field == name {
			return true
		}
	}
	return false
}

func excludeRequestFields(fields []jsonField, names []string) []jsonField {
	result := fields[:0]
	for _, field := range fields {
		if excludedField(names, field.name) {
			clear(field.value)
		} else {
			result = append(result, field)
		}
	}
	clear(fields[len(result):])
	return result
}

func (r *ChatRequest) ExcludeFields(names []string) error {
	if r == nil {
		return ErrInvalidRequest
	}
	fields, err := NormalizeExcludedRequestFields(names)
	if err != nil {
		return err
	}
	r.excluded = fields
	r.fields = excludeRequestFields(r.fields, fields)
	r.requirements = projectCapabilities(r.fields, r.Stream)
	return nil
}

func (r *ChatRequest) FieldExcluded(name string) bool {
	return r != nil && excludedField(r.excluded, name)
}

func (r *EmbeddingRequest) ExcludeFields(names []string) error {
	if r == nil {
		return ErrInvalidRequest
	}
	fields, err := NormalizeExcludedRequestFields(names)
	if err != nil {
		return err
	}
	r.excluded = fields
	r.fields = excludeRequestFields(r.fields, fields)
	if r.FieldExcluded("dimensions") {
		r.Dimensions = 0
	}
	return nil
}

func (r *EmbeddingRequest) FieldExcluded(name string) bool {
	return r != nil && excludedField(r.excluded, name)
}
