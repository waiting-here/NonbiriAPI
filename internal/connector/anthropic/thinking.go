package anthropic

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/connector/continuation"
)

func emptyDiagnostics(raw []byte) bool { return len(raw) == 0 || isNull(raw) }
func emptyCitations(raw []byte) bool {
	if len(raw) == 0 || isNull(raw) {
		return true
	}
	var values []json.RawMessage
	return json.Unmarshal(raw, &values) == nil && values != nil && len(values) == 0
}

func thinkingBlock(block map[string]json.RawMessage, complete bool) (string, error) {
	var kind string
	_ = json.Unmarshal(block["type"], &kind)
	if kind == "redacted_thinking" {
		var data string
		if !onlyKeys(block, "type", "data") || json.Unmarshal(block["data"], &data) != nil || len(data) > 1<<20 || !validOpaqueRunes(data, 1<<20, false) {
			return "", ErrInvalidResponse
		}
		return "", nil
	}
	if kind != "thinking" || !onlyKeys(block, "type", "thinking", "signature") {
		return "", ErrInvalidResponse
	}
	var text, signature string
	if value, present := block["thinking"]; present && (isNull(value) || json.Unmarshal(value, &text) != nil) {
		return "", ErrInvalidResponse
	}
	if value, present := block["signature"]; present {
		if isNull(value) || json.Unmarshal(value, &signature) != nil || len(signature) > 64<<10 {
			return "", ErrInvalidResponse
		}
	}
	if complete && !validOpaqueRunes(signature, 64<<10, false) {
		return "", ErrInvalidResponse
	}
	return text, nil
}

func thinkingExtra(blocks []json.RawMessage) map[string]any {
	return map[string]any{"anthropic": map[string]any{"content": blocks}}
}

func replayThinking(message map[string]json.RawMessage, expected []outboundBlock) ([]outboundBlock, error) {
	var readable string
	if value := message["reasoning_content"]; len(value) > 0 && !isNull(value) && json.Unmarshal(value, &readable) != nil {
		return nil, ErrInvalidRequest
	}
	if len(message["extra_content"]) == 0 || isNull(message["extra_content"]) {
		// Ordinary history can omit thinking. A tool turn with displayed
		// thinking cannot reconstruct the required signed blocks from text.
		if readable != "" && len(message["tool_calls"]) > 0 && !isNull(message["tool_calls"]) {
			return nil, fieldRejection("extra_content.anthropic", "tool thinking requires the original signed content blocks")
		}
		return expected, nil
	}
	outer, err := strictObject(message["extra_content"])
	if err != nil || !continuation.ValidExtra(outer, "anthropic") {
		return nil, ErrInvalidRequest
	}
	native, err := strictObject(outer["anthropic"])
	if err != nil || !onlyKeys(native, "content") {
		return nil, ErrInvalidRequest
	}
	var blocks []outboundBlock
	if json.Unmarshal(native["content"], &blocks) != nil || len(blocks) == 0 || len(blocks) > maxMessages {
		return nil, ErrInvalidRequest
	}
	stop := "end_turn"
	var text strings.Builder
	wantTools := []callerToolCall{}
	for _, block := range expected {
		if block.Type == "text" && block.Text != nil {
			text.WriteString(*block.Text)
		}
		if block.Type == "tool_use" {
			stop = "tool_use"
			wantTools = append(wantTools, callerToolCall{ID: block.ID, Type: "function", Function: callerToolFunction{Name: block.Name, Arguments: string(block.Input)}})
		}
	}
	raw, _ := json.Marshal(map[string]any{"id": "continuation", "type": "message", "role": "assistant", "model": "continuation", "content": json.RawMessage(native["content"]), "stop_reason": stop})
	defer clear(raw)
	projected, _, _, err := translateNonStream(raw, "continuation", time.Unix(0, 0))
	defer clear(projected)
	if err != nil {
		return nil, ErrInvalidRequest
	}
	var result completionEnvelope
	if json.Unmarshal(projected, &result) != nil || len(result.Choices) != 1 {
		return nil, ErrInvalidRequest
	}
	actual := result.Choices[0].Message
	actualText, _ := actual.Content.(string)
	if actualText != text.String() || actual.Reasoning != readable || len(actual.ToolCalls) != len(wantTools) {
		return nil, ErrInvalidRequest
	}
	for i, call := range actual.ToolCalls {
		want := wantTools[i]
		if call.ID != want.ID || call.Function.Name != want.Function.Name || !sameThinkingJSON([]byte(call.Function.Arguments), []byte(want.Function.Arguments)) {
			return nil, ErrInvalidRequest
		}
	}
	return blocks, nil
}

func sameThinkingJSON(a, b []byte) bool {
	var left, right any
	d := json.NewDecoder(bytes.NewReader(a))
	d.UseNumber()
	if d.Decode(&left) != nil {
		return false
	}
	d = json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	return d.Decode(&right) == nil && reflect.DeepEqual(left, right)
}
