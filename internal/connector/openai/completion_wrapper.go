package openai

import (
	"bytes"
	"encoding/json"
)

// unwrapCompletion is called after ordinary completion validation fails.
// It accepts one narrow success wrapper; the caller validates its contents.
func unwrapCompletion(body []byte) ([]byte, error) {
	fields, err := decodeJSONObject(body, maxProtocolFields)
	if err != nil {
		return nil, errInvalidUpstreamResponse
	}
	defer clearFields(fields)
	if len(fields) != 2 {
		return nil, errInvalidUpstreamResponse
	}
	var data json.RawMessage
	success := false
	for _, field := range fields {
		switch field.name {
		case "success":
			success = bytes.Equal(bytes.TrimSpace(field.value), []byte("true"))
		case "data":
			data = field.value
		default:
			return nil, errInvalidUpstreamResponse
		}
	}
	if !success || len(data) == 0 {
		return nil, errInvalidUpstreamResponse
	}
	return append([]byte(nil), data...), nil
}
