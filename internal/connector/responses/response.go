package responses

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/connector/chatwire"
	"github.com/waiting-here/NonbiriAPI/internal/connector/continuation"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
)

type partState struct {
	kind           string
	text           strings.Builder
	textDone, done bool
}
type itemState struct {
	id, kind, callID, name string
	args                   strings.Builder
	toolIndex              int
	argsDone, done         bool
	parts                  []*partState
	final                  json.RawMessage
}
type decoder struct {
	includeUsage                                                  bool
	id, model, responseID, finish                                 string
	created, sequence                                             int64
	started, terminal, finished, roleSent, nonReasoning, toolSeen bool
	items                                                         []*itemState
	toolCount                                                     int
	usage                                                         contract.Usage
	continuationID                                                string
}

func NewDecoder(model string, now time.Time, includeUsage bool) chatwire.Decoder {
	return &decoder{id: fmt.Sprintf("chatcmpl-%x", now.UnixNano()), model: model, created: now.Unix(), sequence: -1, includeUsage: includeUsage}
}
func (d *decoder) Clear() {
	for _, item := range d.items {
		clear(item.final)
	}
	*d = decoder{}
}
func (d *decoder) Usage() contract.Usage       { return d.usage }
func (d *decoder) SetContinuationID(id string) { d.continuationID = id }
func (d *decoder) ObserveFailure(raw []byte) {
	root, err := chatwire.Object(raw)
	if err != nil {
		return
	}
	if nested, ok := root["response"]; ok {
		root, err = chatwire.Object(nested)
		if err != nil {
			return
		}
	}
	if value, ok := root["usage"]; ok && !chatwire.Null(value) {
		d.usage = openai.ParseResponseUsage(value)
	}
}
func (d *decoder) JSON(raw []byte) ([]byte, contract.Usage, error) {
	message, finish, usage, err := projectResponse(raw)
	if err != nil {
		return nil, usage, err
	}
	if extra, ok := message["extra_content"].(map[string]any); ok {
		message["extra_content"] = continuation.Bind(extra, d.continuationID)
	}
	out, err := chatwire.Completion(d.id, d.model, d.created, message, finish, usage)
	d.usage = usage
	return out, usage, err
}
func finishReason(root map[string]json.RawMessage, tools bool) (string, error) {
	status, _ := chatwire.String(root["status"])
	switch status {
	case "completed":
		if tools {
			return "tool_calls", nil
		}
		return "stop", nil
	case "incomplete":
		details, err := chatwire.Object(root["incomplete_details"])
		reason, _ := chatwire.String(details["reason"])
		if err != nil {
			return "", chatwire.ErrProtocol
		}
		switch reason {
		case "max_output_tokens":
			return "length", nil
		case "content_filter":
			return "content_filter", nil
		}
	}
	return "", chatwire.ErrProtocol
}
func projectResponse(raw []byte) (map[string]any, string, contract.Usage, error) {
	root, err := chatwire.Object(raw)
	if err != nil {
		return nil, "", contract.Usage{}, err
	}
	usage := openai.ParseResponseUsage(root["usage"])
	if !chatwire.Null(root["error"]) {
		return nil, "", usage, chatwire.ErrProtocol
	}
	var items []json.RawMessage
	if json.Unmarshal(root["output"], &items) != nil || chatwire.Null(root["output"]) || len(items) > 256 {
		return nil, "", usage, chatwire.ErrProtocol
	}
	message, err := projectItems(items)
	if err != nil {
		return nil, "", usage, err
	}
	_, tools := message["tool_calls"]
	finish, err := finishReason(root, tools)
	if err != nil {
		return nil, "", usage, err
	}
	return message, finish, usage, nil
}
func projectItems(items []json.RawMessage) (map[string]any, error) {
	var text, refusal, summary strings.Builder
	calls := []any{}
	reasoning := []any{}
	seen := map[string]bool{}
	nonReasoning := false
	toolSeen := false
	for _, raw := range items {
		item, err := chatwire.Object(raw)
		if err != nil {
			return nil, err
		}
		id, ok := chatwire.String(item["id"])
		if !ok || id == "" || len(id) > 512 || seen[id] {
			return nil, chatwire.ErrProtocol
		}
		seen[id] = true
		kind, _ := chatwire.String(item["type"])
		switch kind {
		case "reasoning":
			if nonReasoning {
				return nil, chatwire.ErrProtocol
			}
			value, body, err := reasoningItem(raw)
			if err != nil {
				return nil, err
			}
			reasoning = append(reasoning, value)
			summary.WriteString(body)
		case "message":
			if toolSeen || !chatwire.Keys(item, "type", "id", "role", "content", "status", "phase") || string(item["role"]) != `"assistant"` || !validItemStatus(item) {
				return nil, chatwire.ErrProtocol
			}
			nonReasoning = true
			if raw, ok := item["phase"]; ok && !chatwire.Null(raw) && string(raw) != `"final_answer"` {
				return nil, chatwire.ErrProtocol
			}
			var parts []json.RawMessage
			if json.Unmarshal(item["content"], &parts) != nil || chatwire.Null(item["content"]) {
				return nil, chatwire.ErrProtocol
			}
			for _, raw := range parts {
				kind, value, err := parsePart(raw, false)
				if err != nil {
					return nil, err
				}
				if kind == "output_text" {
					text.WriteString(value)
				} else {
					refusal.WriteString(value)
				}
			}
		case "function_call":
			if !chatwire.Keys(item, "type", "id", "call_id", "name", "arguments", "status") || !validItemStatus(item) || len(calls) >= 128 {
				return nil, chatwire.ErrProtocol
			}
			nonReasoning = true
			toolSeen = true
			callID, ok := chatwire.String(item["call_id"])
			name, _ := chatwire.String(item["name"])
			args, argsOK := chatwire.String(item["arguments"])
			if !ok || callID == "" || len(callID) > 128 || seen["call:"+callID] || !namePattern.MatchString(name) || !argsOK {
				return nil, chatwire.ErrProtocol
			}
			seen["call:"+callID] = true
			if _, err := chatwire.Object([]byte(args)); err != nil {
				return nil, err
			}
			calls = append(calls, map[string]any{"id": callID, "type": "function", "function": map[string]string{"name": name, "arguments": args}})
		default:
			return nil, chatwire.ErrProtocol
		}
	}
	message := map[string]any{"role": "assistant", "content": text.String()}
	if refusal.Len() != 0 {
		message["refusal"] = refusal.String()
	}
	if len(calls) > 0 {
		message["tool_calls"] = calls
		if text.Len() == 0 {
			message["content"] = nil
		}
	}
	if summary.Len() != 0 {
		message["reasoning_content"] = summary.String()
	}
	if len(reasoning) > 0 {
		extra := map[string]any{"openai": map[string]any{"reasoning": reasoning}}
		encoded, _ := json.Marshal(extra)
		if len(encoded) > 1<<20 {
			return nil, chatwire.ErrProtocol
		}
		message["extra_content"] = extra
	}
	return message, nil
}
func validItemStatus(item map[string]json.RawMessage) bool {
	raw, ok := item["status"]
	return !ok || string(raw) == `"completed"` || string(raw) == `"incomplete"`
}
func parsePart(raw []byte, summary bool) (string, string, error) {
	part, err := chatwire.Object(raw)
	if err != nil {
		return "", "", err
	}
	kind, _ := chatwire.String(part["type"])
	field := "text"
	if summary {
		if kind != "summary_text" || !chatwire.Keys(part, "type", "text") {
			return "", "", chatwire.ErrProtocol
		}
	} else {
		switch kind {
		case "output_text":
			if !chatwire.Keys(part, "type", "text", "annotations", "logprobs") {
				return "", "", chatwire.ErrProtocol
			}
			for _, key := range []string{"annotations", "logprobs"} {
				if raw, ok := part[key]; ok && !chatwire.Null(raw) {
					var values []json.RawMessage
					if json.Unmarshal(raw, &values) != nil || len(values) > 0 {
						return "", "", chatwire.ErrProtocol
					}
				}
			}
		case "refusal":
			field = "refusal"
			if !chatwire.Keys(part, "type", "refusal") {
				return "", "", chatwire.ErrProtocol
			}
		default:
			return "", "", chatwire.ErrProtocol
		}
	}
	text, ok := chatwire.String(part[field])
	if !ok {
		return "", "", chatwire.ErrProtocol
	}
	return kind, text, nil
}
func sameJSON(a, b []byte) bool {
	var x, y any
	return json.Unmarshal(a, &x) == nil && json.Unmarshal(b, &y) == nil && reflect.DeepEqual(x, y)
}
func (d *decoder) Event(name string, raw []byte) ([][]byte, error) {
	root, err := chatwire.Object(raw)
	if err != nil || d.terminal || d.finished {
		return nil, chatwire.ErrProtocol
	}
	kind, ok := chatwire.String(root["type"])
	sequence, seqOK := chatwire.Integer(root["sequence_number"])
	if !ok || name != "" && name != "message" && name != kind || !seqOK || sequence <= d.sequence {
		return nil, chatwire.ErrProtocol
	}
	d.sequence = sequence
	if value, present := root["response_id"]; present {
		id, ok := chatwire.String(value)
		if !ok || !d.started || id != d.responseID {
			return nil, chatwire.ErrProtocol
		}
	}
	var delta map[string]any
	switch kind {
	case "response.created", "response.in_progress":
		response, err := chatwire.Object(root["response"])
		id, ok := chatwire.String(response["id"])
		if err != nil || !ok || id == "" || len(id) > 512 || string(response["status"]) != `"in_progress"` {
			return nil, chatwire.ErrProtocol
		}
		if kind == "response.created" {
			if d.started {
				return nil, chatwire.ErrProtocol
			}
			d.started = true
			d.responseID = id
		} else if !d.started || id != d.responseID {
			return nil, chatwire.ErrProtocol
		}
		return nil, nil
	case "response.completed", "response.incomplete":
		if !d.started {
			return nil, chatwire.ErrProtocol
		}
		response, err := chatwire.Object(root["response"])
		id, _ := chatwire.String(response["id"])
		if err != nil || id != d.responseID {
			return nil, chatwire.ErrProtocol
		}
		output := make([]json.RawMessage, len(d.items))
		for i, item := range d.items {
			if !item.done {
				return nil, chatwire.ErrProtocol
			}
			output[i] = item.final
		}
		completed, err := openai.CompleteResponseTerminal(root["response"], kind, d.responseID, output)
		if err != nil {
			return nil, chatwire.ErrProtocol
		}
		defer clear(completed)
		_, finish, usage, err := projectResponse(completed)
		if err != nil {
			return nil, err
		}
		d.finish = finish
		if finish == "stop" && d.toolCount > 0 {
			d.finish = "tool_calls"
		}
		d.usage = usage
		d.terminal = true
		return nil, nil
	case "response.output_item.added":
		if !d.started || len(d.items) >= 256 {
			return nil, chatwire.ErrProtocol
		}
		index, ok := chatwire.Integer(root["output_index"])
		item, err := chatwire.Object(root["item"])
		if !ok || index != int64(len(d.items)) || err != nil {
			return nil, chatwire.ErrProtocol
		}
		id, ok := chatwire.String(item["id"])
		itemType, _ := chatwire.String(item["type"])
		if !ok || id == "" || len(id) > 512 {
			return nil, chatwire.ErrProtocol
		}
		for _, existing := range d.items {
			if existing.id == id {
				return nil, chatwire.ErrProtocol
			}
		}
		state := &itemState{id: id, kind: itemType, toolIndex: -1}
		switch itemType {
		case "reasoning":
			if d.nonReasoning {
				return nil, chatwire.ErrProtocol
			}
			var summary []json.RawMessage
			if json.Unmarshal(item["summary"], &summary) != nil || len(summary) > 0 {
				return nil, chatwire.ErrProtocol
			}
		case "message":
			if d.toolSeen || string(item["role"]) != `"assistant"` {
				return nil, chatwire.ErrProtocol
			}
			var content []json.RawMessage
			if json.Unmarshal(item["content"], &content) != nil || len(content) > 0 {
				return nil, chatwire.ErrProtocol
			}
			d.nonReasoning = true
		case "function_call":
			if d.toolCount >= 128 {
				return nil, chatwire.ErrProtocol
			}
			d.nonReasoning = true
			d.toolSeen = true
			state.callID, ok = chatwire.String(item["call_id"])
			state.name, _ = chatwire.String(item["name"])
			args, argsOK := chatwire.String(item["arguments"])
			if !ok || state.callID == "" || len(state.callID) > 128 || !namePattern.MatchString(state.name) || !argsOK || args != "" {
				return nil, chatwire.ErrProtocol
			}
			for _, existing := range d.items {
				if existing.kind == "function_call" && existing.callID == state.callID {
					return nil, chatwire.ErrProtocol
				}
			}
			state.toolIndex = d.toolCount
			d.toolCount++
			delta = map[string]any{"tool_calls": []any{map[string]any{"index": state.toolIndex, "id": state.callID, "type": "function", "function": map[string]string{"name": state.name, "arguments": ""}}}}
		default:
			return nil, chatwire.ErrProtocol
		}
		d.items = append(d.items, state)
	default:
		if !d.started {
			return nil, chatwire.ErrProtocol
		}
		index, ok := chatwire.Integer(root["output_index"])
		if root["output_index"] == nil && kind == "response.output_item.done" {
			final, err := chatwire.Object(root["item"])
			id, valid := chatwire.String(final["id"])
			if err != nil || !valid {
				return nil, chatwire.ErrProtocol
			}
			for i, candidate := range d.items {
				if candidate.id == id {
					index, ok = int64(i), true
					break
				}
			}
		}
		if !ok || index >= int64(len(d.items)) {
			return nil, chatwire.ErrProtocol
		}
		item := d.items[index]
		if item.done {
			return nil, chatwire.ErrProtocol
		}
		if kind != "response.output_item.done" {
			id, ok := chatwire.String(root["item_id"])
			if !ok || id != item.id {
				return nil, chatwire.ErrProtocol
			}
		}
		switch kind {
		case "response.output_item.done":
			final, err := chatwire.Object(root["item"])
			id, _ := chatwire.String(final["id"])
			if err != nil || id != item.id || string(final["type"]) != fmt.Sprintf("%q", item.kind) {
				return nil, chatwire.ErrProtocol
			}
			if item.kind == "function_call" {
				args, _ := chatwire.String(final["arguments"])
				name, _ := chatwire.String(final["name"])
				callID, _ := chatwire.String(final["call_id"])
				if !item.argsDone || args != item.args.String() || name != item.name || callID != item.callID {
					return nil, chatwire.ErrProtocol
				}
			} else {
				key := "content"
				if item.kind == "reasoning" {
					key = "summary"
				}
				var parts []json.RawMessage
				if json.Unmarshal(final[key], &parts) != nil || len(parts) != len(item.parts) {
					return nil, chatwire.ErrProtocol
				}
				for i, part := range item.parts {
					kind, text, err := parsePart(parts[i], item.kind == "reasoning")
					if err != nil || !part.done || kind != part.kind || text != part.text.String() {
						return nil, chatwire.ErrProtocol
					}
				}
			}
			if _, err := projectItems([]json.RawMessage{root["item"]}); err != nil {
				return nil, err
			}
			item.done = true
			item.final = append([]byte(nil), root["item"]...)
			if item.kind == "reasoning" {
				delta = map[string]any{"extra_content": continuation.Bind(map[string]any{"openai": map[string]any{"reasoning": []any{final}}}, d.continuationID)}
			}
		case "response.function_call_arguments.delta", "response.function_call_arguments.done":
			if item.kind != "function_call" || item.argsDone {
				return nil, chatwire.ErrProtocol
			}
			if kind == "response.function_call_arguments.delta" {
				value, ok := chatwire.String(root["delta"])
				if !ok {
					return nil, chatwire.ErrProtocol
				}
				item.args.WriteString(value)
				delta = map[string]any{"tool_calls": []any{map[string]any{"index": item.toolIndex, "function": map[string]string{"arguments": value}}}}
			} else {
				value, ok := chatwire.String(root["arguments"])
				if !ok || value != item.args.String() {
					return nil, chatwire.ErrProtocol
				}
				if _, err := chatwire.Object([]byte(value)); err != nil {
					return nil, err
				}
				item.argsDone = true
			}
		case "response.content_part.added", "response.reasoning_summary_part.added":
			summary := kind == "response.reasoning_summary_part.added"
			if summary && item.kind != "reasoning" || !summary && item.kind != "message" {
				return nil, chatwire.ErrProtocol
			}
			key := "content_index"
			if summary {
				key = "summary_index"
			}
			partIndex, ok := chatwire.Integer(root[key])
			kind, text, err := parsePart(root["part"], summary)
			if !ok || partIndex != int64(len(item.parts)) || err != nil || text != "" {
				return nil, chatwire.ErrProtocol
			}
			item.parts = append(item.parts, &partState{kind: kind})
		case "response.output_text.delta", "response.output_text.done", "response.refusal.delta", "response.refusal.done", "response.reasoning_summary_text.delta", "response.reasoning_summary_text.done", "response.content_part.done", "response.reasoning_summary_part.done":
			summary := kind == "response.reasoning_summary_text.delta" || kind == "response.reasoning_summary_text.done" || kind == "response.reasoning_summary_part.done"
			if summary && item.kind != "reasoning" || !summary && item.kind != "message" {
				return nil, chatwire.ErrProtocol
			}
			key := "content_index"
			if summary {
				key = "summary_index"
			}
			partIndex, ok := chatwire.Integer(root[key])
			if !ok || partIndex >= int64(len(item.parts)) {
				return nil, chatwire.ErrProtocol
			}
			part := item.parts[partIndex]
			if part.done {
				return nil, chatwire.ErrProtocol
			}
			switch kind {
			case "response.content_part.done", "response.reasoning_summary_part.done":
				partKind, text, err := parsePart(root["part"], summary)
				if err != nil || partKind != part.kind || text != part.text.String() || !part.textDone {
					return nil, chatwire.ErrProtocol
				}
				part.done = true
			default:
				want, field := "output_text", "content"
				if summary {
					want = "summary_text"
					field = "reasoning_content"
				} else if kind == "response.refusal.delta" || kind == "response.refusal.done" {
					want = "refusal"
					field = "refusal"
				}
				if part.kind != want || part.textDone {
					return nil, chatwire.ErrProtocol
				}
				isDelta := kind == "response.output_text.delta" || kind == "response.refusal.delta" || kind == "response.reasoning_summary_text.delta"
				key := "text"
				if want == "refusal" {
					key = "refusal"
				}
				if isDelta {
					key = "delta"
				}
				value, ok := chatwire.String(root[key])
				if !ok {
					return nil, chatwire.ErrProtocol
				}
				if isDelta {
					part.text.WriteString(value)
					delta = map[string]any{field: value}
				} else {
					if value != part.text.String() {
						return nil, chatwire.ErrProtocol
					}
					part.textDone = true
				}
			}
		default:
			return nil, chatwire.ErrProtocol
		}
	}
	if delta == nil {
		return nil, nil
	}
	if !d.roleSent {
		delta["role"] = "assistant"
		d.roleSent = true
	}
	frame, err := chatwire.Chunk(d.id, d.model, d.created, delta, "", contract.Usage{})
	if err != nil {
		return nil, err
	}
	return [][]byte{frame}, nil
}
func (d *decoder) Finish() ([][]byte, error) {
	if !d.terminal || d.finished {
		return nil, chatwire.ErrProtocol
	}
	d.finished = true
	delta := map[string]any{}
	if !d.roleSent {
		delta["role"] = "assistant"
	}
	frame, err := chatwire.Chunk(d.id, d.model, d.created, delta, d.finish, contract.Usage{})
	if err != nil {
		return nil, err
	}
	frames := [][]byte{frame}
	if d.usage.Present && d.includeUsage {
		frame, err = chatwire.Chunk(d.id, d.model, d.created, nil, "", d.usage)
		if err != nil {
			return nil, err
		}
		frames = append(frames, frame)
	}
	return frames, nil
}
