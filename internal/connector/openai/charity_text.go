package openai

import (
	"bytes"
	"encoding/json"
	"io"
	"unicode/utf8"
)

// These projections only count recognized prompt fields. Optional malformed
// tool fields remain the protocol validator's concern; never substitute their
// raw JSON byte length for text or mutate them while measuring the request.
func charityJSONArray(raw json.RawMessage) []json.RawMessage {
	var items []json.RawMessage
	_ = json.Unmarshal(raw, &items)
	return items
}

func charityJSONStringRunes(raw json.RawMessage) int {
	var text string
	if json.Unmarshal(raw, &text) != nil {
		return 0
	}
	return utf8.RuneCountInString(text)
}

func charityFunctionArgumentsRunes(raw json.RawMessage) int {
	var call map[string]json.RawMessage
	if json.Unmarshal(raw, &call) != nil {
		return 0
	}
	return charityJSONStringRunes(call["arguments"])
}

func charityToolDefinitionsRunes(raw json.RawMessage) int {
	count := 0
	for _, item := range charityJSONArray(raw) {
		var tool struct {
			Type     string          `json:"type"`
			Function json.RawMessage `json:"function"`
		}
		if json.Unmarshal(item, &tool) == nil && tool.Type == "function" {
			count += charityFunctionDefinitionRunes(tool.Function)
		}
	}
	return count
}

func charityFunctionDefinitionRunes(raw json.RawMessage) int {
	var definition map[string]json.RawMessage
	if json.Unmarshal(raw, &definition) != nil {
		return 0
	}
	return charityJSONStringRunes(definition["description"]) + charitySchemaTextRunes(definition["parameters"])
}

// A parameter schema is also model input: property names, descriptions,
// enums and nested definitions can carry substantial prompt text. Count its
// decoded strings (including keys), without JSON whitespace, punctuation or
// escape expansion. Token iteration avoids building another schema tree.
func charitySchemaTextRunes(raw json.RawMessage) int {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	count := 0
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			return count
		}
		if err != nil {
			return 0
		}
		if text, ok := token.(string); ok {
			count += utf8.RuneCountInString(text)
		}
	}
}
