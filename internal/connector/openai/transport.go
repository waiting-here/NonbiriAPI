package openai

import (
	"bytes"
	"encoding/json"
	"strconv"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

// WithStream returns a physical request without changing the caller snapshot.
// Transport conversion accepts only stream options with a known equivalent.
func (r *ChatRequest) WithStream(stream bool) (*ChatRequest, error) {
	if r == nil {
		return nil, ErrInvalidRequest
	}
	copy := r.CloneForAttempt()
	if copy.Stream == stream {
		return copy, nil
	}
	fields := copy.fields[:0]
	for _, field := range copy.fields {
		switch field.name {
		case "stream":
			clear(field.value)
			field.value = json.RawMessage(strconv.FormatBool(stream))
		case "stream_options":
			if !bytes.Equal(bytes.TrimSpace(field.value), []byte("null")) {
				options, err := decodeJSONObject(field.value, maxTopLevelFields)
				valid := err == nil
				for _, option := range options {
					value := bytes.TrimSpace(option.value)
					valid = valid && option.name == "include_usage" && (bytes.Equal(value, []byte("true")) || bytes.Equal(value, []byte("false")))
				}
				clearFields(options)
				if !valid {
					copy.Clear()
					return nil, &contract.RequestRejection{Stage: "transport preflight", Field: "stream_options", Reason: "stream options cannot be converted"}
				}
			}
			clear(field.value)
			continue
		}
		fields = append(fields, field)
	}
	clear(copy.fields[len(fields):])
	copy.fields = fields
	if !hasField(fields, "stream") {
		copy.fields = append(copy.fields, jsonField{name: "stream", value: json.RawMessage(strconv.FormatBool(stream))})
	}
	if stream {
		options, _ := json.Marshal(map[string]bool{"include_usage": true})
		copy.fields = append(copy.fields, jsonField{name: "stream_options", value: options})
	}
	copy.Stream = stream
	copy.requirements = projectCapabilities(copy.fields, stream)
	return copy, nil
}
