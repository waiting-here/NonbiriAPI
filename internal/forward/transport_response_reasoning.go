package forward

import (
	"maps"
	"reflect"
	"strings"
)

func mergeChatReasoning(message map[string]any, field string, value any) error {
	previous, exists := message[field]
	if !exists || previous == nil {
		message[field] = value
		return nil
	}
	if value == nil {
		return nil
	}
	parts, priorArray := previous.([]any)
	deltas, nextArray := value.([]any)
	if field == "encrypted_content" || !priorArray || !nextArray {
		// Ciphertext and unrecognized values are complete snapshots. A single
		// completion field cannot represent two conflicting snapshots.
		if !reflect.DeepEqual(previous, value) {
			return errTransportResponse
		}
		return nil
	}
	if len(parts) != 0 && len(deltas) != 0 {
		// An incoming array can contain several distinct blocks. Only its first
		// block can continue the last block from an earlier event.
		if key := reasoningContinuationField(field, parts[len(parts)-1], deltas[0]); key != "" {
			mergeReasoningPart(parts[len(parts)-1].(map[string]any), deltas[0].(map[string]any), key)
			deltas = deltas[1:]
		}
	}
	message[field] = append(parts, deltas...)
	return nil
}

func reasoningContinuationField(field string, previous, next any) string {
	left, leftOK := previous.(map[string]any)
	right, rightOK := next.(map[string]any)
	if !leftOK || !rightOK {
		return ""
	}
	key := reasoningPartTextField(field, left)
	if key == "" || key != reasoningPartTextField(field, right) {
		return ""
	}
	if signature, _ := left["signature"].(string); signature != "" {
		return ""
	}
	for _, name := range [...]string{"index", "id", "format"} {
		if a, b := left[name], right[name]; a != nil && b != nil && a != b {
			return ""
		}
	}
	return key
}

func reasoningPartTextField(field string, part map[string]any) string {
	var textKey string
	switch field {
	case "reasoning_details":
		switch part["type"] {
		case "reasoning.text":
			textKey = "text"
		case "reasoning.summary":
			textKey = "summary"
		}
	case "thinking_blocks":
		if part["type"] == "thinking" {
			textKey = "thinking"
		}
	}
	if textKey == "" {
		return ""
	}
	for key, value := range part {
		switch key {
		case "type":
		case "id", "format", "signature":
			if key == "signature" && textKey == "summary" {
				return ""
			}
			if _, ok := value.(string); !ok && value != nil {
				return ""
			}
		case "index":
			if _, err := wireIndex(value); err != nil && value != nil {
				return ""
			}
		case textKey:
			switch value.(type) {
			case string, *strings.Builder, nil:
			default:
				return ""
			}
		default:
			// Other fields may carry independent replay semantics.
			return ""
		}
	}
	return textKey
}

func mergeReasoningPart(dst, delta map[string]any, textKey string) {
	for key, value := range delta {
		if key == textKey {
			if text, ok := value.(string); ok {
				builder, ok := dst[key].(*strings.Builder)
				if !ok {
					builder = new(strings.Builder)
					previous, _ := dst[key].(string)
					builder.WriteString(previous)
					dst[key] = builder
				}
				builder.WriteString(text)
				continue
			}
		}
		if prior, exists := dst[key]; !exists || prior == nil || key == "signature" && prior == "" && value != nil {
			dst[key] = value
		}
	}
}

func fragmentReasoningParts(field string, parts []any, emit func(any) error) error {
	for start := 0; start < len(parts); {
		end := start + 1
		for end < len(parts) && reasoningContinuationField(field, parts[end-1], parts[end]) != "" {
			end++
		}
		if err := emit(parts[start:end]); err != nil {
			// A frame boundary between compatible complete blocks could turn them
			// into one replay item. Keep such groups intact or fail conversion.
			if end-start != 1 {
				return err
			}
			part, ok := parts[start].(map[string]any)
			if !ok {
				return err
			}
			if err := fragmentReasoningPart(field, part, emit); err != nil {
				return err
			}
		}
		start = end
	}
	return nil
}

func fragmentReasoningPart(field string, part map[string]any, emit func(any) error) error {
	key := reasoningPartTextField(field, part)
	text, _ := part[key].(string)
	if text == "" {
		return errTransportResponse
	}
	fragment := maps.Clone(part)
	signature, hasSignature := fragment["signature"]
	delete(fragment, "signature")
	if err := fragmentWireString(text, func(piece string) error {
		fragment[key] = piece
		return emit([]any{fragment})
	}); err != nil {
		return err
	}
	if hasSignature {
		if field == "thinking_blocks" {
			fragment[key] = ""
		} else {
			delete(fragment, key)
		}
		fragment["signature"] = signature
		return emit([]any{fragment})
	}
	return nil
}
