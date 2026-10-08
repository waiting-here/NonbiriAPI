package openai

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/requestbody"
)

// ImageRequest owns the validated image generation parameters for one request.
type ImageRequest struct {
	bodyLimit int64
	fields    []jsonField
	excluded  []string
	Model     string
	Stream    bool
	Count     int
}

func (*ImageRequest) String() string       { return "[redacted image request]" }
func (*ImageRequest) GoString() string     { return "[redacted image request]" }
func (*ImageRequest) LogValue() slog.Value { return slog.StringValue("[redacted image request]") }

func DecodeImageRequest(body io.Reader, limit int64) (*ImageRequest, error) {
	if body == nil {
		return nil, invalidField("body", "expected one valid UTF-8 JSON object")
	}
	limit = requestbody.DecoderLimit(limit)
	data, err := readBounded(body, limit)
	if err != nil {
		return nil, err
	}
	defer clear(data)
	if !validateEmbeddingJSON(data) {
		return nil, invalidField("body", "expected one valid UTF-8 JSON object")
	}
	fields, ok := borrowedObjectFields(data)
	if !ok {
		return nil, invalidField("body", "expected one valid UTF-8 JSON object")
	}
	r := &ImageRequest{bodyLimit: limit, Count: 1}
	promptSeen := false
	for _, field := range fields {
		raw := field.value
		switch field.name {
		case "model":
			if json.Unmarshal(raw, &r.Model) != nil || !validOpaqueText(r.Model, MaxPlatformModelRunes, true) {
				return nil, invalidField("model", "expected a nonempty model name of at most 133 characters without control characters")
			}
		case "prompt":
			var prompt string
			if json.Unmarshal(raw, &prompt) != nil || isJSONNull(raw) || len(prompt) == 0 || !utf8.ValidString(prompt) {
				return nil, invalidField("prompt", "expected a nonempty string")
			}
			promptSeen = true
		case "stream":
			if !isJSONNull(raw) && (json.Unmarshal(raw, &r.Stream) != nil || !bytes.Equal(raw, []byte("true")) && !bytes.Equal(raw, []byte("false"))) {
				return nil, invalidField("stream", "expected a boolean or null")
			}
		case "n", "partial_images", "output_compression":
			if isJSONNull(raw) {
				continue
			}
			var value int
			minimum, maximum := 0, 100
			if field.name == "n" {
				minimum, maximum = 1, 10
			}
			if field.name == "partial_images" {
				maximum = 3
			}
			if json.Unmarshal(raw, &value) != nil || value < minimum || value > maximum {
				return nil, invalidField(field.name, "expected an integer in the supported range")
			}
			if field.name == "n" {
				r.Count = value
			}
		case "background", "moderation", "output_format", "quality", "response_format", "size", "style", "user":
			if isJSONNull(raw) {
				continue
			}
			var value string
			if json.Unmarshal(raw, &value) != nil || !validOpaqueText(value, 512, false) {
				return nil, invalidField(field.name, "expected a string without control characters")
			}
		}
	}
	if r.Model == "" {
		return nil, invalidField("model", "required field is missing")
	}
	if !promptSeen {
		return nil, invalidField("prompt", "required field is missing")
	}
	r.fields = make([]jsonField, len(fields))
	for i, field := range fields {
		r.fields[i] = jsonField{name: field.name, value: append(json.RawMessage(nil), field.value...)}
	}
	return r, nil
}

func (r *ImageRequest) Clear() {
	if r == nil {
		return
	}
	clearFields(r.fields)
	clear(r.excluded)
	*r = ImageRequest{}
}
func (r *ImageRequest) RequestBodyLimit() int64 {
	if r == nil {
		return requestbody.DefaultBytes
	}
	return requestbody.DecoderLimit(r.bodyLimit)
}
func (r *ImageRequest) ExcludeFields(names []string) error {
	if r == nil {
		return ErrInvalidRequest
	}
	fields, err := NormalizeExcludedRequestFields(names)
	if err != nil {
		return err
	}
	r.excluded = fields
	r.fields = excludeRequestFields(r.fields, fields)
	return nil
}
func (r *ImageRequest) FieldExcluded(name string) bool {
	return r != nil && excludedField(r.excluded, name)
}
func (r *ImageRequest) LogicalBody() ([]byte, error) {
	if r == nil {
		return nil, ErrInvalidRequest
	}
	fields := make(map[string]json.RawMessage, len(r.fields))
	for _, field := range r.fields {
		fields[field.name] = field.value
	}
	body, err := json.Marshal(fields)
	if err != nil || int64(len(body)) > r.bodyLimit {
		clear(body)
		return nil, ErrPayloadTooLarge
	}
	return body, nil
}

// TopLevelFields returns an immutable field-name projection for fidelity checks.
func (r *ImageRequest) TopLevelFields() []string {
	if r == nil {
		return nil
	}
	names := make([]string, len(r.fields))
	for i, field := range r.fields {
		names[i] = field.name
	}
	return names
}

func (r *ImageRequest) CloneForAttempt() *ImageRequest {
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

func (r *ImageRequest) RawField(name string) ([]byte, bool) {
	if r != nil {
		for _, field := range r.fields {
			if field.name == name {
				return append([]byte(nil), field.value...), true
			}
		}
	}
	return nil, false
}

func (r *ImageRequest) marshalUpstream(upstreamModel, identifier string) ([]byte, error) {
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
	if int64(out.Len()) > r.RequestBodyLimit()+(16<<10) {
		clear(out.Bytes())
		return nil, ErrPayloadTooLarge
	}
	return out.Bytes(), nil
}
