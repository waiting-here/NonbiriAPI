package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"time"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
	"github.com/waiting-here/NonbiriAPI/internal/egress"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/upstreamerror"
)

type streamTool struct {
	name            string
	index           int
	args            bytes.Buffer
	ended, complete bool
}

func (a *Adapter) stream(ctx context.Context, w http.ResponseWriter, response *http.Response, request *openai.ChatRequest, guard responseGuard, errorContext upstreamerror.Context) contract.AttemptResult {
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	events, errs := egress.StreamSSE(streamCtx, response.Body, egress.SSEOptions{MaxBytes: min(maxStreamBytes, a.backend.MaxResponseBytes()), MaxLineBytes: maxLineBytes, MaxEventBytes: maxEventBytes, ReadBuffer: 64 << 10, EventBuffer: 4})
	controller := http.NewResponseController(w)
	defer func() { _ = controller.SetWriteDeadline(time.Time{}) }()
	id, created := responseID(), a.now().Unix()
	committed, terminal, started, roleSent, metadataSeen := false, false, false, false, false
	generated := int64(0)
	usage := contract.Usage{}
	finish := ""
	texts := map[string]bool{}
	tools := map[string]*streamTool{}
	defer func() {
		for _, tool := range tools {
			clear(tool.args.Bytes())
		}
	}()
	write := func(frame []byte, errorFrame bool) error {
		defer clear(frame)
		reserve := int64(httperr.MaxSSEErrorFrameBytes)
		if errorFrame {
			reserve = 0
		}
		if len(frame) > maxEventBytes || int64(len(frame)) > maxStreamBytes-generated-reserve {
			return errResponse
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !committed {
			if err := contract.MarkResponseStarted(w); err != nil {
				return err
			}
		}
		if err := controller.SetWriteDeadline(time.Now().Add(15 * time.Second)); err != nil && !errors.Is(err, http.ErrNotSupported) {
			return err
		}
		setResponseHeaders(w.Header(), true)
		n, err := w.Write(frame)
		committed = committed || n > 0
		generated += int64(n)
		if err != nil {
			return err
		}
		if n != len(frame) {
			return io.ErrShortWrite
		}
		if err := controller.Flush(); err != nil && !errors.Is(err, http.ErrNotSupported) {
			return err
		}
		return nil
	}
	failure := func(message string, detail upstreamerror.Detail) contract.AttemptResult {
		result := upstreamFailure(message, response.StatusCode)
		result.Committed, result.Usage, result.ErrorDetail = committed, usage, detail
		if committed {
			result.ClientStatus = 200
			if err := write(httperr.SSEErrorFrame(httperr.New(httperr.CodeUpstream, "upstream stream failed")), true); err != nil {
				return sinkFailure(true, usage)
			}
		}
		return result
	}
	emit := func(delta map[string]any, reason any, withUsage bool) error {
		if !roleSent {
			delta["role"] = "assistant"
			roleSent = true
		}
		out := map[string]any{"id": id, "object": "chat.completion.chunk", "created": created, "model": request.Model,
			"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": reason}}}
		if withUsage {
			out["choices"] = []any{}
			out["usage"] = callerUsage(usage)
		}
		body, err := json.Marshal(out)
		if err != nil {
			return errResponse
		}
		defer clear(body)
		frame := append(append([]byte("data: "), body...), '\n', '\n')
		if guard.ContainsJSON(frame, body) {
			clear(frame)
			return errResponse
		}
		return write(frame, false)
	}
	toolDelta := func(tool *streamTool, id, name, args string) map[string]any {
		fn := map[string]any{"arguments": args}
		call := map[string]any{"index": tool.index, "function": fn}
		if name != "" {
			fn["name"] = name
			call["id"] = id
			call["type"] = "function"
		}
		return map[string]any{"tool_calls": []any{call}}
	}
	for events != nil || errs != nil {
		var event egress.SSEEvent
		select {
		case <-ctx.Done():
			result := canceled()
			result.Committed, result.Usage = committed, usage
			if committed {
				result.ClientStatus = 200
			}
			return result
		case err, ok := <-errs:
			if !ok {
				errs = nil
				continue
			}
			if err != nil {
				return failure("upstream stream was interrupted", upstreamerror.Detail{})
			}
			continue
		case value, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			event = value
		}
		if terminal || event.Event != "" && event.Event != "message" || len(event.Data) == 0 {
			return failure("upstream stream event order was invalid", upstreamerror.Detail{})
		}
		part, err := parseObject([]byte(event.Data))
		kind, ok := text(part["type"])
		if err != nil || !ok {
			return failure("upstream stream event was invalid", upstreamerror.Detail{})
		}
		var delta map[string]any
		switch kind {
		case "stream-start":
			if started || len(texts) > 0 || len(tools) > 0 || !warningsOK(part["warnings"]) {
				return failure("upstream stream options were not honored", upstreamerror.Detail{})
			}
			started = true
		case "response-metadata":
			if metadataSeen {
				return failure("duplicate upstream stream metadata", upstreamerror.Detail{})
			}
			metadataSeen = true
		case "text-start", "reasoning-start":
			id, ok := text(part["id"])
			if !ok || !opaque(id, 512) || len(texts) >= 512 {
				return failure("invalid upstream text block", upstreamerror.Detail{})
			}
			id = strings.TrimSuffix(kind, "-start") + ":" + id
			if _, seen := texts[id]; seen {
				return failure("duplicate upstream text block", upstreamerror.Detail{})
			}
			texts[id] = false
		case "text-delta", "reasoning-delta":
			id, ok := text(part["id"])
			value, valid := text(part["delta"])
			id = strings.TrimSuffix(kind, "-delta") + ":" + id
			closed, exists := texts[id]
			if !ok || !valid || !exists || closed {
				return failure("invalid upstream text delta", upstreamerror.Detail{})
			}
			field := "content"
			if kind == "reasoning-delta" {
				field = "reasoning_content"
			}
			delta = map[string]any{field: value}
		case "text-end", "reasoning-end":
			id, ok := text(part["id"])
			id = strings.TrimSuffix(kind, "-end") + ":" + id
			closed, exists := texts[id]
			if !ok || !exists || closed {
				return failure("invalid upstream text end", upstreamerror.Detail{})
			}
			texts[id] = true
		case "tool-input-start":
			id, ok := text(part["id"])
			name, nameOK := text(part["toolName"])
			if !ok || !nameOK || !opaque(id, 512) || !opaque(name, 64) || tools[id] != nil || len(tools) >= 128 {
				return failure("invalid upstream tool start", upstreamerror.Detail{})
			}
			if raw, ok := part["providerExecuted"]; ok && string(raw) != "false" {
				return failure("upstream executed tools cannot be replayed", upstreamerror.Detail{})
			}
			tool := &streamTool{name: name, index: len(tools)}
			tools[id] = tool
			delta = toolDelta(tool, id, name, "")
		case "tool-input-delta":
			id, ok := text(part["id"])
			args, valid := text(part["delta"])
			tool := tools[id]
			if !ok || !valid || tool == nil || tool.ended || tool.complete || tool.args.Len()+len(args) > maxEventBytes {
				return failure("invalid upstream tool delta", upstreamerror.Detail{})
			}
			tool.args.WriteString(args)
			delta = toolDelta(tool, "", "", args)
		case "tool-input-end":
			id, ok := text(part["id"])
			tool := tools[id]
			if !ok || tool == nil || tool.ended || tool.complete {
				return failure("invalid upstream tool end", upstreamerror.Detail{})
			}
			tool.ended = true
		case "tool-call":
			call, err := parseToolCall(part)
			if err != nil {
				return failure("invalid upstream tool call", upstreamerror.Detail{})
			}
			tool := tools[call.ID]
			if tool == nil {
				if len(tools) >= 128 {
					return failure("too many upstream tools", upstreamerror.Detail{})
				}
				tool = &streamTool{name: call.Function.Name, index: len(tools), ended: true, complete: true}
				tools[call.ID] = tool
				delta = toolDelta(tool, call.ID, call.Function.Name, call.Function.Arguments)
			} else {
				if !tool.ended || tool.complete || tool.name != call.Function.Name {
					return failure("invalid upstream tool sequence", upstreamerror.Detail{})
				}
				if tool.args.Len() == 0 {
					delta = toolDelta(tool, "", "", call.Function.Arguments)
				} else if !sameJSON(tool.args.Bytes(), []byte(call.Function.Arguments)) {
					return failure("upstream tool arguments did not match", upstreamerror.Detail{})
				}
				tool.complete = true
			}
		case "finish":
			for _, closed := range texts {
				if !closed {
					return failure("upstream text block was incomplete", upstreamerror.Detail{})
				}
			}
			for _, tool := range tools {
				if !tool.complete {
					return failure("upstream tool call was incomplete", upstreamerror.Detail{})
				}
			}
			finish, err = finishReason(part["finishReason"])
			if err != nil || (finish == "tool_calls") != (len(tools) > 0) || len(texts)+len(tools) == 0 {
				return failure("upstream finish was invalid", upstreamerror.Detail{})
			}
			usage, err = parseUsage(part["usage"])
			if err != nil {
				return failure("upstream usage was invalid", upstreamerror.Detail{})
			}
			terminal = true
		case "error":
			upstreamerror.CaptureEvent(ctx, response.StatusCode, response.Header.Get("Content-Type"), []byte(event.Data))
			return failure("upstream stream reported an error", errorContext.Parse([]byte(event.Data)))
		default:
			return failure("upstream stream contained an unsupported event", upstreamerror.Detail{})
		}
		if delta != nil {
			if err := emit(delta, nil, false); err != nil {
				if errors.Is(err, errResponse) {
					return failure("upstream stream was rejected", upstreamerror.Detail{})
				}
				return sinkFailure(committed, usage)
			}
		}
	}
	if ctx.Err() != nil {
		result := canceled()
		result.Committed, result.Usage = committed, usage
		if committed {
			result.ClientStatus = 200
		}
		return result
	}
	if !terminal {
		return failure("upstream stream ended without a finish", upstreamerror.Detail{})
	}
	if err := emit(map[string]any{}, finish, false); err != nil {
		return sinkFailure(committed, usage)
	}
	options, _ := request.RawField("stream_options")
	defer clear(options)
	var streamOptions struct {
		IncludeUsage bool `json:"include_usage"`
	}
	_ = json.Unmarshal(options, &streamOptions)
	if streamOptions.IncludeUsage && usage.Present {
		if err := emit(map[string]any{}, nil, true); err != nil {
			return sinkFailure(committed, usage)
		}
	}
	if err := write([]byte("data: [DONE]\n\n"), false); err != nil {
		return sinkFailure(committed, usage)
	}
	return contract.AttemptResult{Success: true, Committed: committed, Failure: contract.FailureNone, UpstreamStatus: response.StatusCode, ClientStatus: 200, Usage: usage}
}

func sameJSON(a, b []byte) bool {
	if _, err := parseObject(a); err != nil {
		return false
	}
	if _, err := parseObject(b); err != nil {
		return false
	}
	var left, right any
	d := json.NewDecoder(bytes.NewReader(a))
	d.UseNumber()
	if d.Decode(&left) != nil {
		return false
	}
	d = json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	if d.Decode(&right) != nil {
		return false
	}
	return reflect.DeepEqual(left, right)
}
