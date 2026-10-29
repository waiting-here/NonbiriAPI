package openai

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/connector/continuation"
)

type responseChatCall struct {
	id, name, arguments string
	argumentsBuffer     *strings.Builder
}
type responseChatState struct {
	textBuffer, refusalBuffer, reasoningBuffer  strings.Builder
	id                                          string
	created                                     int64
	model                                       string
	text, refusal                               string
	reasoning                                   string
	gatewayReasoning                            bool
	extra                                       json.RawMessage
	calls                                       []responseChatCall
	finish                                      string
	usage                                       Usage
	usageRaw                                    json.RawMessage
	sequence                                    int
	messageStarted, textStarted, refusalStarted bool
	contentOrder                                []string
	outputOrder                                 []string
	reasoningStarted                            bool
}

func (s *responseChatState) clear() { clear(s.usageRaw); clear(s.extra); *s = responseChatState{} }

func (s *responseChatState) consume(raw []byte, stream bool) error {
	if !validateEmbeddingJSON(raw) {
		return errInvalidUpstreamResponse
	}
	root, ok := systemOneMap(raw)
	if !ok {
		return errInvalidUpstreamResponse
	}
	if usage := root["usage"]; usage != nil && !isJSONNull(usage) {
		s.usage = ParseChatUsage(usage)
		clear(s.usageRaw)
		s.usageRaw = bytes.Clone(usage)
	}
	if hasUpstreamError(root) {
		return errInvalidUpstreamResponse
	}
	object := "chat.completion"
	if stream {
		object += ".chunk"
	}
	if !bytes.Equal(root["object"], []byte(strconv.Quote(object))) {
		return errInvalidUpstreamResponse
	}
	id, ok := decisionString(root["id"], 1, 512)
	var created int64
	if !ok || !validOpaqueText(id, 512, true) || !decodeNonNegativeInt(root["created"], &created) || isJSONNull(root["created"]) || s.id != "" && s.id != id || s.id != "" && s.created != created {
		return errInvalidUpstreamResponse
	}
	s.id = id
	s.created = created
	var choices []json.RawMessage
	if json.Unmarshal(root["choices"], &choices) != nil || choices == nil || len(choices) > 1 || !stream && len(choices) != 1 {
		return errInvalidUpstreamResponse
	}
	if len(choices) == 0 {
		if !stream || s.finish == "" || root["usage"] == nil {
			return errInvalidUpstreamResponse
		}
		return nil
	}
	choice, ok := systemOneMap(choices[0])
	if !ok || !bytes.Equal(choice["index"], []byte("0")) || s.finish != "" {
		return errInvalidUpstreamResponse
	}
	key := "message"
	if stream {
		key = "delta"
	}
	allowed := []string{"role", "content", "refusal", "tool_calls", "annotations"}
	if s.gatewayReasoning {
		allowed = append(allowed, "reasoning_content", "extra_content")
	}
	message, ok := decisionObject(choice[key], allowed...)
	if !ok {
		return errInvalidUpstreamResponse
	}
	if value := message["extra_content"]; value != nil {
		outer, valid := systemOneMap(value)
		if !valid || !continuation.ValidExtra(outer, "gateway") || outer["gateway"] == nil {
			return errInvalidUpstreamResponse
		}
		s.extra = bytes.Clone(value)
	}
	if role := message["role"]; role != nil && !bytes.Equal(role, []byte(`"assistant"`)) || !stream && message["role"] == nil {
		return errInvalidUpstreamResponse
	}
	if annotations := message["annotations"]; annotations != nil && !isJSONNull(annotations) && !bytes.Equal(annotations, []byte("[]")) {
		return errInvalidUpstreamResponse
	}
	channels := map[string]*strings.Builder{"content": &s.textBuffer, "refusal": &s.refusalBuffer}
	if s.gatewayReasoning {
		channels["reasoning_content"] = &s.reasoningBuffer
	}
	for name, dst := range channels {
		if value := message[name]; value != nil && !isJSONNull(value) {
			text, ok := decisionString(value, 0, int(DefaultMaxStreamBytes))
			if !ok {
				return errInvalidUpstreamResponse
			}
			dst.WriteString(text)
		}
	}
	s.text, s.refusal, s.reasoning = s.textBuffer.String(), s.refusalBuffer.String(), s.reasoningBuffer.String()
	if raw := message["tool_calls"]; raw != nil && !isJSONNull(raw) {
		var calls []json.RawMessage
		if json.Unmarshal(raw, &calls) != nil || calls == nil || len(calls) > 128 {
			return errInvalidUpstreamResponse
		}
		seen := map[int64]bool{}
		for i, raw := range calls {
			call, ok := decisionObject(raw, "index", "id", "type", "function")
			if !ok {
				return errInvalidUpstreamResponse
			}
			index := int64(i)
			if stream {
				if !decodeNonNegativeInt(call["index"], &index) || isJSONNull(call["index"]) || index > 127 {
					return errInvalidUpstreamResponse
				}
			}
			if seen[index] || index > int64(len(s.calls)) {
				return errInvalidUpstreamResponse
			}
			seen[index] = true
			if index == int64(len(s.calls)) {
				s.calls = append(s.calls, responseChatCall{})
			}
			current := &s.calls[index]
			if kind := call["type"]; kind != nil && !bytes.Equal(kind, []byte(`"function"`)) {
				return errInvalidUpstreamResponse
			}
			if value := call["id"]; value != nil {
				id, ok := decisionString(value, 1, 512)
				if !ok || !validOpaqueText(id, 512, true) || current.id != "" && current.id != id {
					return errInvalidUpstreamResponse
				}
				current.id = id
			}
			function, ok := decisionObject(call["function"], "name", "arguments")
			if !ok {
				return errInvalidUpstreamResponse
			}
			if value := function["name"]; value != nil {
				name, ok := decisionString(value, 0, 64)
				if !ok {
					return errInvalidUpstreamResponse
				}
				current.name += name
				if len(current.name) > 64 {
					return errInvalidUpstreamResponse
				}
			}
			if value := function["arguments"]; value != nil {
				text, ok := decisionString(value, 0, int(DefaultMaxStreamBytes))
				if !ok {
					return errInvalidUpstreamResponse
				}
				if current.argumentsBuffer == nil {
					current.argumentsBuffer = &strings.Builder{}
				}
				current.argumentsBuffer.WriteString(text)
				current.arguments = current.argumentsBuffer.String()
			}
		}
	}
	if raw := choice["finish_reason"]; raw != nil && !isJSONNull(raw) {
		reason, ok := decisionString(raw, 1, 32)
		if !ok {
			return errInvalidUpstreamResponse
		}
		switch reason {
		case "stop", "tool_calls", "length", "content_filter":
		default:
			return errInvalidUpstreamResponse
		}
		if reason == "tool_calls" && len(s.calls) == 0 || reason == "stop" && len(s.calls) > 0 {
			return errInvalidUpstreamResponse
		}
		seen := map[string]bool{}
		for _, call := range s.calls {
			if call.id == "" || seen[call.id] || !responseFunctionName.MatchString(call.name) {
				return errInvalidUpstreamResponse
			}
			seen[call.id] = true
		}
		s.finish = reason
	} else if !stream {
		return errInvalidUpstreamResponse
	}
	return nil
}

func (s *responseChatState) status() string {
	if s.finish == "length" || s.finish == "content_filter" {
		return "incomplete"
	}
	if s.finish != "" {
		return "completed"
	}
	return "in_progress"
}
func (s *responseChatState) messageID() string   { return "msg_" + s.id }
func (s *responseChatState) reasoningID() string { return "rs_" + s.id }
func (s *responseChatState) reasoningItem() map[string]any {
	item := map[string]any{"id": s.reasoningID(), "type": "reasoning", "summary": []any{}, "content": []any{map[string]string{"type": "reasoning_text", "text": s.reasoning}}, "status": s.status()}
	if s.extra != nil && len(s.outputKinds()) > 0 && s.outputKinds()[0] == "reasoning" {
		item["extra_content"] = s.extra
	}
	return item
}

// Streaming output indices follow first publication. JSON has no delta order,
// so its reasoning precedes the assistant message and correlated tool calls.
func (s *responseChatState) outputKinds() []string {
	if len(s.outputOrder) != 0 {
		return s.outputOrder
	}
	order := []string{}
	if s.reasoning != "" {
		order = append(order, "reasoning")
	}
	if s.text != "" || s.refusal != "" || s.messageStarted {
		order = append(order, "message")
	}
	if len(order) == 0 && s.extra != nil && len(s.calls) == 0 {
		order = append(order, "message")
	}
	return order
}

func (s *responseChatState) outputIndex(kind string) int {
	for i, current := range s.outputOrder {
		if current == kind {
			return i
		}
	}
	s.outputOrder = append(s.outputOrder, kind)
	return len(s.outputOrder) - 1
}
func (s *responseChatState) content() []any {
	out := []any{}
	order := s.contentOrder
	if len(order) == 0 {
		if s.text != "" {
			order = append(order, "text")
		}
		if s.refusal != "" {
			order = append(order, "refusal")
		}
	}
	for _, kind := range order {
		if kind == "text" {
			out = append(out, map[string]any{"type": "output_text", "text": s.text, "annotations": []any{}, "logprobs": []any{}})
		} else {
			out = append(out, map[string]any{"type": "refusal", "refusal": s.refusal})
		}
	}
	return out
}
func (s *responseChatState) message() map[string]any {
	item := map[string]any{"id": s.messageID(), "type": "message", "role": "assistant", "status": s.status(), "content": s.content()}
	if s.extra != nil && len(s.outputKinds()) > 0 && s.outputKinds()[0] == "message" {
		item["extra_content"] = s.extra
	}
	return item
}
func (s *responseChatState) function(i int) map[string]any {
	call := s.calls[i]
	item := map[string]any{"id": "fc_" + s.id + "_" + strconv.Itoa(i), "type": "function_call", "status": s.status(), "call_id": call.id, "name": call.name, "arguments": call.arguments}
	if i == 0 && s.extra != nil && len(s.outputKinds()) == 0 {
		item["extra_content"] = s.extra
	}
	return item
}
func (s *responseChatState) response(completed bool) map[string]any {
	output := []any{}
	status := "in_progress"
	var usage any
	var incomplete any
	if completed {
		status = s.status()
		for _, kind := range s.outputKinds() {
			if kind == "reasoning" {
				output = append(output, s.reasoningItem())
			} else {
				output = append(output, s.message())
			}
		}
		for i := range s.calls {
			output = append(output, s.function(i))
		}
		if s.usage.Present {
			input := s.usage.UncachedInputTokens + s.usage.CacheReadInputTokens + s.usage.CacheWriteInputTokens
			reasoning := int64(0)
			root, _ := systemOneMap(s.usageRaw)
			details, _ := systemOneMap(root["completion_tokens_details"])
			_ = decodeNonNegativeInt(details["reasoning_tokens"], &reasoning)
			usage = map[string]any{"input_tokens": input, "output_tokens": s.usage.OutputTokens, "total_tokens": input + s.usage.OutputTokens, "input_tokens_details": map[string]int64{"cached_tokens": s.usage.CacheReadInputTokens, "cache_write_tokens": s.usage.CacheWriteInputTokens}, "output_tokens_details": map[string]int64{"reasoning_tokens": reasoning}}
		}
		if status == "incomplete" {
			reason := s.finish
			if reason == "length" {
				reason = "max_output_tokens"
			}
			incomplete = map[string]string{"reason": reason}
		}
	}
	return map[string]any{"id": "resp_" + s.id, "object": "response", "created_at": s.created, "model": s.model, "status": status, "output": output, "usage": usage, "store": false, "background": false, "error": nil, "incomplete_details": incomplete}
}
func projectChatAsResponse(raw []byte, request *InferenceRequest) ([]byte, Usage, error) {
	state := responseChatState{model: request.Model}
	defer state.clear()
	if err := state.consume(raw, false); err != nil {
		return nil, state.usage, err
	}
	body, err := json.Marshal(state.response(true))
	return body, state.usage, err
}
