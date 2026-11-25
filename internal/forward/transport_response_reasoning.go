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

func reasoningPartTextKey(field, kind string) string {
	switch field {
	case "reasoning_details":
		switch kind {
		case "reasoning.text":
			return "text"
		case "reasoning.summary":
			return "summary"
		}
	case "thinking_blocks":
		if kind == "thinking" {
			return "thinking"
		}
	}
	return ""
}

func reasoningPartTextField(field string, part map[string]any) string {
	kind, _ := part["type"].(string)
	textKey := reasoningPartTextKey(field, kind)
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
		clearReasoningPartText(field, fragment, key)
		fragment["signature"] = signature
		return emit([]any{fragment})
	}
	return nil
}

func clearReasoningPartText(field string, part map[string]any, key string) {
	if field == "thinking_blocks" {
		part[key] = ""
	} else {
		delete(part, key)
	}
}

type reasoningMirror struct {
	field    string
	parts    []any
	readable []int
}

type reasoningMirrorPart struct {
	field, textKey string
	part           map[string]any
}

func matchingReasoningMirror(field string, value any, text string) (reasoningMirror, bool) {
	parts, _ := value.([]any)
	mirror := reasoningMirror{field: field, parts: parts}
	for i, value := range parts {
		part, ok := value.(map[string]any)
		if !ok {
			return reasoningMirror{}, false
		}
		visible, known := reasoningPartVisibleText(field, part)
		var matches bool
		text, matches = strings.CutPrefix(text, visible)
		if !known || !matches {
			return reasoningMirror{}, false
		}
		if visible != "" {
			mirror.readable = append(mirror.readable, i)
		}
	}
	return mirror, text == "" && len(mirror.readable) != 0
}

func reasoningPartVisibleText(field string, part map[string]any) (string, bool) {
	kind, _ := part["type"].(string)
	if field == "reasoning_details" && kind == "reasoning.encrypted" || field == "thinking_blocks" && kind == "redacted_thinking" {
		return "", true
	}
	key := reasoningPartTextKey(field, kind)
	if key == "" {
		return "", false
	}
	if part[key] == nil {
		return "", true
	}
	text, ok := part[key].(string)
	return text, ok
}

func reasoningMirrorDelta(text string, hasAlias bool) map[string]any {
	group := make(map[string]any)
	if text != "" {
		group["reasoning_content"] = text
		if hasAlias {
			group["reasoning"] = text
		}
	}
	return group
}

func fragmentChatCompletionFields(fields map[string]any, emit func(map[string]any) error) error {
	canonical, _ := fields["reasoning_content"].(string)
	if canonical == "" {
		return fragmentCompletionFields(fields, emit)
	}
	alias, _ := fields["reasoning"].(string)
	hasAlias := alias == canonical
	group := reasoningMirrorDelta(canonical, hasAlias)
	var mirrors []reasoningMirror
	for _, field := range [...]string{"reasoning_details", "thinking_blocks"} {
		if mirror, ok := matchingReasoningMirror(field, fields[field], canonical); ok {
			mirrors = append(mirrors, mirror)
			group[field] = mirror.parts
		}
	}
	if len(group) == 1 {
		return fragmentCompletionFields(fields, emit)
	}
	if role, ok := fields["role"]; ok {
		if err := emit(map[string]any{"role": role}); err != nil {
			return err
		}
		delete(fields, "role")
	}
	// Keep equivalent channels in the same event even if the full message or
	// the reasoning group exceeds one frame. Fallback readers then see one copy.
	if err := emit(group); err != nil {
		switch len(mirrors) {
		case 0:
			err = fragmentReasoningMirrors(canonical, group, nil, emit)
		case 1:
			err = fragmentReasoningMirrorArray(mirrors[0], hasAlias, emit)
		default:
			err = fragmentParallelReasoningMirrors(mirrors, hasAlias, emit)
		}
		if err != nil {
			return err
		}
	}
	for field := range group {
		delete(fields, field)
	}
	return fragmentCompletionFields(fields, emit)
}

func fragmentReasoningMirrorArray(mirror reasoningMirror, hasAlias bool, emit func(map[string]any) error) error {
	return fragmentReasoningParts(mirror.field, mirror.parts, func(value any) error {
		var visible strings.Builder
		for _, item := range value.([]any) {
			text, _ := reasoningPartVisibleText(mirror.field, item.(map[string]any))
			visible.WriteString(text)
		}
		delta := reasoningMirrorDelta(visible.String(), hasAlias)
		delta[mirror.field] = value
		return emit(delta)
	})
}

func fragmentParallelReasoningMirrors(mirrors []reasoningMirror, hasAlias bool, emit func(map[string]any) error) error {
	first := mirrors[0]
	for _, mirror := range mirrors[1:] {
		if len(mirror.readable) != len(first.readable) {
			return errTransportResponse
		}
	}
	for i, index := range first.readable {
		text, _ := reasoningPartVisibleText(first.field, first.parts[index].(map[string]any))
		group := reasoningMirrorDelta(text, hasAlias)
		parts := make([]reasoningMirrorPart, 0, len(mirrors))
		for _, mirror := range mirrors {
			pos := mirror.readable[i]
			part := mirror.parts[pos].(map[string]any)
			visible, _ := reasoningPartVisibleText(mirror.field, part)
			if visible != text || reasoningMirrorBoundaryAmbiguous(mirror, pos) {
				return errTransportResponse
			}
			start := 0
			if i > 0 {
				start = mirror.readable[i-1] + 1
			}
			if err := emitReasoningRemainder(mirror.field, mirror.parts[start:pos], emit); err != nil {
				return err
			}
			kind, _ := part["type"].(string)
			parts = append(parts, reasoningMirrorPart{field: mirror.field, textKey: reasoningPartTextKey(mirror.field, kind), part: part})
			group[mirror.field] = []any{part}
		}
		if err := emit(group); err != nil {
			if err := fragmentReasoningMirrors(text, group, parts, emit); err != nil {
				return err
			}
		}
	}
	for _, mirror := range mirrors {
		if err := emitReasoningRemainder(mirror.field, mirror.parts[mirror.readable[len(mirror.readable)-1]+1:], emit); err != nil {
			return err
		}
	}
	return nil
}

func reasoningMirrorBoundaryAmbiguous(mirror reasoningMirror, index int) bool {
	return index > 0 && reasoningContinuationField(mirror.field, mirror.parts[index-1], mirror.parts[index]) != "" ||
		index+1 < len(mirror.parts) && reasoningContinuationField(mirror.field, mirror.parts[index], mirror.parts[index+1]) != ""
}

func emitReasoningRemainder(field string, parts []any, emit func(map[string]any) error) error {
	if len(parts) == 0 {
		return nil
	}
	wrap := func(value any) error { return emit(map[string]any{field: value}) }
	if err := wrap(parts); err == nil {
		return nil
	}
	return fragmentReasoningParts(field, parts, wrap)
}

func fragmentReasoningMirrors(text string, group map[string]any, mirrors []reasoningMirrorPart, emit func(map[string]any) error) error {
	var signatures []map[string]any
	for i := range mirrors {
		mirror := &mirrors[i]
		if reasoningPartTextField(mirror.field, mirror.part) == "" {
			return errTransportResponse
		}
		mirror.part = maps.Clone(mirror.part)
		if _, ok := mirror.part["signature"]; ok {
			signed := maps.Clone(mirror.part)
			clearReasoningPartText(mirror.field, signed, mirror.textKey)
			signatures = append(signatures, map[string]any{mirror.field: []any{signed}})
			delete(mirror.part, "signature")
		}
		group[mirror.field] = []any{mirror.part}
	}
	_, hasAlias := group["reasoning"]
	if err := fragmentWireString(text, func(piece string) error {
		group["reasoning_content"] = piece
		if hasAlias {
			group["reasoning"] = piece
		}
		for _, mirror := range mirrors {
			mirror.part[mirror.textKey] = piece
		}
		return emit(group)
	}); err != nil {
		return err
	}
	for _, signature := range signatures {
		if err := emit(signature); err != nil {
			return err
		}
	}
	return nil
}
