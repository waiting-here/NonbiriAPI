package openai

import (
	"bytes"
	"encoding/json"
	"strings"
)

// normalizeChatContentParts projects a validated message or delta containing
// only text and readable thinking. Unknown parts and replay metadata keep the
// complete original array. The caller owns the message map.
func normalizeChatContentParts(message map[string]json.RawMessage) bool {
	content := bytes.TrimSpace(message["content"])
	if len(content) == 0 || content[0] != '[' {
		return false
	}
	var answer, reasoning strings.Builder
	if !visitJSONArray(content, func(raw []byte) bool {
		part, ok := chatPartFields(raw)
		if !ok {
			return false
		}
		if exactString(part, "type", "text") {
			return appendChatText(&answer, part)
		}
		if !exactString(part, "type", "thinking") {
			return false
		}
		for name, value := range part {
			switch name {
			case "type", "thinking":
			case "closed":
				if !isJSONNull(value) && !bytes.Equal(value, []byte("true")) && !bytes.Equal(value, []byte("false")) {
					return false
				}
			case "signature":
				if !isJSONNull(value) {
					return false
				}
			default:
				return false
			}
		}
		return visitJSONArray(part["thinking"], func(raw []byte) bool {
			text, ok := chatPartFields(raw)
			return ok && appendChatText(&reasoning, text)
		})
	}) {
		return false
	}
	if reasoning.Len() > 0 {
		var current string
		raw := message["reasoning_content"]
		if len(raw) != 0 && !isJSONNull(raw) {
			if !jsonString(raw, &current) || current != "" && current != reasoning.String() {
				return false
			}
		}
		if current == "" {
			message["reasoning_content"], _ = marshalChatProjection(reasoning.String())
		}
	}
	message["content"], _ = marshalChatProjection(answer.String())
	return true
}

// The wire carries JSON, not HTML. Keep text such as XML or code containing
// angle brackets within the same response limits as ordinary string content.
func marshalChatProjection(value any) ([]byte, error) {
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(out.Bytes(), []byte{'\n'}), nil
}

func chatPartFields(raw []byte) (map[string]json.RawMessage, bool) {
	fields, ok := borrowedObjectFields(raw)
	if !ok {
		return nil, false
	}
	part := fieldsByName(fields)
	return part, len(part) == len(fields)
}

func appendChatText(dst *strings.Builder, part map[string]json.RawMessage) bool {
	var text string
	if len(part) != 2 || !exactString(part, "type", "text") || !jsonString(part["text"], &text) {
		return false
	}
	dst.WriteString(text)
	return true
}
