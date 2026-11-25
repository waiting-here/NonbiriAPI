package responses

import (
	"encoding/json"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/connector/chatwire"
	"github.com/waiting-here/NonbiriAPI/internal/connector/continuation"
)

func (item *itemState) argumentsDelta(value string) map[string]any {
	return map[string]any{"tool_calls": []any{map[string]any{"index": item.toolIndex, "function": map[string]string{"arguments": value}}}}
}

// A completed item can be the first and only event describing its output.
func (d *decoder) completedItemSnapshot(raw json.RawMessage) (map[string]any, error) {
	if len(d.items) >= 256 {
		return nil, chatwire.ErrProtocol
	}
	message, err := projectItems([]json.RawMessage{raw})
	if err != nil {
		return nil, err
	}
	item, _ := chatwire.Object(raw)
	id, _ := chatwire.String(item["id"])
	kind, _ := chatwire.String(item["type"])
	callID, _ := chatwire.String(item["call_id"])
	for _, prior := range d.items {
		if prior.id == id || callID != "" && prior.callID == callID {
			return nil, chatwire.ErrProtocol
		}
	}
	toolIndex := d.toolCount
	switch kind {
	case "reasoning":
		if d.nonReasoning || len(d.items) >= 128 {
			return nil, chatwire.ErrProtocol
		}
	case "message":
		if d.toolSeen {
			return nil, chatwire.ErrProtocol
		}
		d.nonReasoning = true
	case "function_call":
		if d.toolCount >= 128 {
			return nil, chatwire.ErrProtocol
		}
		d.nonReasoning = true
		d.toolSeen = true
		d.toolCount++
	}
	d.items = append(d.items, &itemState{id: id, kind: kind, callID: callID, done: true, final: append(json.RawMessage(nil), raw...)})
	return d.snapshotDelta(message, toolIndex)
}

// Count the same compact, source-bound extension that a client will replay.
func bindReasoning(message map[string]any, id string) (int, error) {
	extra, ok := message["extra_content"].(map[string]any)
	if !ok {
		return 0, nil
	}
	encoded, err := json.Marshal(continuation.Bind(extra, id))
	defer clear(encoded)
	if err != nil || len(encoded) > maxReasoningBytes {
		return 0, chatwire.ErrProtocol
	}
	return len(encoded), nil
}

func (d *decoder) snapshotDelta(message map[string]any, toolIndex int) (map[string]any, error) {
	delete(message, "role") // emitDelta sends the role exactly once for the whole response.
	if message["content"] == "" || message["content"] == nil {
		delete(message, "content")
	}
	if calls, ok := message["tool_calls"].([]any); ok {
		for i, call := range calls {
			call.(map[string]any)["index"] = toolIndex + i
		}
	}
	size, err := bindReasoning(message, d.continuationID)
	if err != nil {
		return nil, err
	}
	if size > 0 {
		if d.replaySize == 0 {
			empty := map[string]any{"openai": map[string]any{"reasoning": []any{}}}
			envelope, _ := json.Marshal(continuation.Bind(empty, d.continuationID))
			d.replayEnvelope = len(envelope)
			d.replaySize = size
		} else {
			d.replaySize += 1 + size - d.replayEnvelope // comma between complete items
		}
		if d.replaySize > maxReasoningBytes {
			return nil, chatwire.ErrProtocol
		}
	}
	return message, nil
}

func finalPartsMatch(raw json.RawMessage, observed []*partState, reasoningKind string) bool {
	// The complete snapshot was independently validated by projectItems.
	if len(observed) == 0 {
		return true
	}
	var parts []json.RawMessage
	if json.Unmarshal(raw, &parts) != nil || len(parts) != len(observed) {
		return false
	}
	for i, part := range observed {
		kind, text, err := parsePart(parts[i], reasoningKind)
		if err != nil || !part.done || kind != part.kind || text != part.text.String() {
			return false
		}
	}
	return true
}

// Raw reasoning and summaries both occur with or without part wrappers.
// Keep their indexes independent. Only the summary can be projected before
// the final item establishes which text is displayed on replay.
func (d *decoder) observeReasoningPart(item *itemState, kind string, root map[string]json.RawMessage) (map[string]any, error) {
	parts, indexKey, partType := &item.reasoningParts, "content_index", "reasoning_text"
	summary := strings.HasPrefix(kind, "response.reasoning_summary_")
	if summary {
		parts, indexKey, partType = &item.parts, "summary_index", "summary_text"
	}
	index, ok := chatwire.Integer(root[indexKey])
	if !ok || index > int64(len(*parts)) {
		return nil, chatwire.ErrProtocol
	}
	added := strings.HasSuffix(kind, "_part.added")
	partDone := strings.HasSuffix(kind, "_part.done")
	if index == int64(len(*parts)) {
		if partDone {
			return nil, chatwire.ErrProtocol
		}
		*parts = append(*parts, &partState{kind: partType, wrapped: added})
	} else if added {
		return nil, chatwire.ErrProtocol
	}
	part := (*parts)[index]
	if part.done {
		return nil, chatwire.ErrProtocol
	}
	if added || partDone {
		_, text, err := parsePart(root["part"], partType)
		if err != nil || added && text != "" || partDone && (!part.wrapped || !part.textDone || part.text.String() != text) {
			return nil, chatwire.ErrProtocol
		}
		part.done = partDone
		return nil, nil
	}
	key := "delta"
	if strings.HasSuffix(kind, "_text.done") {
		key = "text"
	}
	text, ok := chatwire.String(root[key])
	if !ok || part.textDone {
		return nil, chatwire.ErrProtocol
	}
	if key == "text" {
		part.textDone = true
		part.done = !part.wrapped
		if part.text.Len() != 0 {
			if part.text.String() != text {
				return nil, chatwire.ErrProtocol
			}
			return nil, nil
		}
	}
	if d.reasoningBytes+len(text) > maxReasoningBytes {
		return nil, chatwire.ErrProtocol
	}
	d.reasoningBytes += len(text)
	part.text.WriteString(text)
	if summary && text != "" {
		return map[string]any{"reasoning_content": text}, nil
	}
	return nil, nil
}
