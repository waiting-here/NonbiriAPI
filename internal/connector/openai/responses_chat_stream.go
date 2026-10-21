package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"time"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/egress"
	"github.com/waiting-here/NonbiriAPI/internal/upstreamerror"
)

func (s *responseChatState) event(kind string, fields map[string]any) map[string]any {
	fields["type"] = kind
	fields["sequence_number"] = s.sequence
	s.sequence++
	return fields
}
func (s *responseChatState) textEvents(oldText, oldRefusal string) []map[string]any {
	events := []map[string]any{}
	if s.text == oldText && s.refusal == oldRefusal {
		return events
	}
	if !s.messageStarted {
		s.messageStarted = true
		events = append(events, s.event("response.output_item.added", map[string]any{"output_index": 0, "item": map[string]any{"id": s.messageID(), "type": "message", "role": "assistant", "status": "in_progress", "content": []any{}}}))
	}
	for _, kind := range []string{"text", "refusal"} {
		value, old, started := s.text, oldText, &s.textStarted
		if kind == "refusal" {
			value, old, started = s.refusal, oldRefusal, &s.refusalStarted
		}
		if value == old {
			continue
		}
		if !*started {
			*started = true
			s.contentOrder = append(s.contentOrder, kind)
			part := map[string]any{"type": "refusal", "refusal": ""}
			if kind == "text" {
				part = map[string]any{"type": "output_text", "text": "", "annotations": []any{}, "logprobs": []any{}}
			}
			events = append(events, s.event("response.content_part.added", map[string]any{"item_id": s.messageID(), "output_index": 0, "content_index": len(s.contentOrder) - 1, "part": part}))
		}
		index := 0
		for i, current := range s.contentOrder {
			if current == kind {
				index = i
			}
		}
		eventKind := "response.output_text.delta"
		if kind == "refusal" {
			eventKind = "response.refusal.delta"
		}
		events = append(events, s.event(eventKind, map[string]any{"item_id": s.messageID(), "output_index": 0, "content_index": index, "delta": value[len(old):]}))
	}
	return events
}
func (s *responseChatState) terminalEvents() []map[string]any {
	events := []map[string]any{}
	offset := 0
	if s.messageStarted {
		parts := s.content()
		for i, kind := range s.contentOrder {
			eventKind, key, value := "response.output_text.done", "text", s.text
			if kind == "refusal" {
				eventKind, key, value = "response.refusal.done", "refusal", s.refusal
			}
			events = append(events, s.event(eventKind, map[string]any{"item_id": s.messageID(), "output_index": 0, "content_index": i, key: value}))
			events = append(events, s.event("response.content_part.done", map[string]any{"item_id": s.messageID(), "output_index": 0, "content_index": i, "part": parts[i]}))
		}
		events = append(events, s.event("response.output_item.done", map[string]any{"output_index": 0, "item": s.message()}))
		offset = 1
	}
	// Chat may fragment function names and IDs. Publish each correlated call
	// only once the complete identity and terminal marker have been received.
	for i, call := range s.calls {
		complete := s.function(i)
		start := s.function(i)
		start["arguments"] = ""
		start["status"] = "in_progress"
		events = append(events, s.event("response.output_item.added", map[string]any{"output_index": offset + i, "item": start}))
		events = append(events, s.event("response.function_call_arguments.delta", map[string]any{"item_id": complete["id"], "output_index": offset + i, "delta": call.arguments}))
		events = append(events, s.event("response.function_call_arguments.done", map[string]any{"item_id": complete["id"], "output_index": offset + i, "arguments": call.arguments}))
		events = append(events, s.event("response.output_item.done", map[string]any{"output_index": offset + i, "item": complete}))
	}
	events = append(events, s.event("response."+s.status(), map[string]any{"response": s.response(true)}))
	return events
}

func (a *Adapter) responsesChatStream(ctx context.Context, writer http.ResponseWriter, response *http.Response, guard *responseGuard, errorContext upstreamerror.Context, request *InferenceRequest, limit int64, lineLimit, eventLimit int) AttemptResult {
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	events, errs := egress.StreamSSE(streamCtx, response.Body, egress.SSEOptions{MaxBytes: limit, MaxLineBytes: lineLimit, MaxEventBytes: eventLimit, ReadBuffer: 64 << 10, EventBuffer: 1})
	controller := http.NewResponseController(writer)
	defer func() { _ = controller.SetWriteDeadline(time.Time{}) }()
	state := responseChatState{model: request.Model}
	defer state.clear()
	committed := false
	written := int64(0)
	failure := func(message string) AttemptResult {
		return a.streamProtocolFailure(writer, controller, committed, state.usage, message)
	}
	for {
		event, ok, err := nextSSEEvent(streamCtx, events, errs)
		if err != nil || !ok {
			if ctx.Err() != nil {
				result := contract.ReadFailed(canceledFailure(), ctx.Err(), ctx)
				result.Committed = committed
				result.ClientStatus = committedStatus(committed)
				result.Usage = state.usage
				return result
			}
			return contract.ReadFailed(failure("upstream stream ended before completion"), err, ctx)
		}
		raw := []byte(event.Data)
		if event.Event == "error" || upstreamerror.IsEvent(raw) {
			if root, ok := systemOneMap(raw); ok && root["usage"] != nil {
				state.usage = ParseChatUsage(root["usage"])
			}
			upstreamerror.CaptureEvent(ctx, response.StatusCode, response.Header.Get("Content-Type"), raw)
			result := a.streamReportedFailure(writer, controller, committed, state.usage, guard, errorContext.Parse(raw))
			clear(raw)
			return result
		}
		terminal := bytes.Equal(bytes.TrimSpace(raw), []byte("[DONE]"))
		var projected []map[string]any
		if terminal {
			clear(raw)
			if state.finish == "" {
				return failure("upstream completion was incomplete")
			}
			projected = state.terminalEvents()
		} else {
			first := state.id == ""
			text, refusal := state.text, state.refusal
			if event.Event != "" && event.Event != "message" {
				clear(raw)
				return failure("upstream stream event was invalid")
			}
			err = state.consume(raw, true)
			clear(raw)
			if err != nil {
				return failure("upstream stream event was invalid")
			}
			if first {
				projected = append(projected, state.event("response.created", map[string]any{"response": state.response(false)}), state.event("response.in_progress", map[string]any{"response": state.response(false)}))
			}
			projected = append(projected, state.textEvents(text, refusal)...)
		}
		for _, value := range projected {
			body, err := json.Marshal(value)
			if err != nil || guard.ContainsJSON(body, body) {
				clear(body)
				return failure("upstream stream event was rejected")
			}
			frame := []byte("event: " + value["type"].(string) + "\ndata: ")
			frame = append(frame, body...)
			frame = append(frame, '\n', '\n')
			clear(body)
			if int64(len(frame)) > limit-written {
				clear(frame)
				return failure("upstream stream exceeded its limit")
			}
			written += int64(len(frame))
			wrote, err := a.writeStreamFrame(writer, controller, frame)
			clear(frame)
			committed = committed || wrote
			if err != nil {
				result := sinkFailureWithCommit(committed, state.usage)
				if terminal {
					result = contract.ConfirmedSuccess(result, response.StatusCode)
				}
				return result
			}
		}
		if terminal {
			return AttemptResult{Success: true, Committed: committed, Failure: FailureNone, ClientStatus: http.StatusOK, UpstreamStatus: response.StatusCode, Usage: state.usage}
		}
	}
}
