package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"
	"unicode/utf8"

	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/egress"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
)

type streamEnvelope struct {
	ID      string         `json:"id"`
	Object  string         `json:"object"`
	Created int64          `json:"created"`
	Model   string         `json:"model"`
	Choices []streamChoice `json:"choices"`
	Usage   *callerUsage   `json:"usage,omitempty"`
}

type streamChoice struct {
	Index        int         `json:"index"`
	Delta        streamDelta `json:"delta"`
	FinishReason *string     `json:"finish_reason"`
}

type streamDelta struct {
	Role      string                `json:"role,omitempty"`
	Content   *string               `json:"content,omitempty"`
	ToolCalls []streamToolCallDelta `json:"tool_calls,omitempty"`
}

type streamToolCallDelta struct {
	Index    int                     `json:"index"`
	ID       string                  `json:"id,omitempty"`
	Type     string                  `json:"type,omitempty"`
	Function streamToolFunctionDelta `json:"function"`
}

type streamToolFunctionDelta struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments"`
}

type openContentBlock struct {
	index     int64
	kind      string
	toolIndex int
	arguments bytes.Buffer
}

type cumulativeUsage struct {
	usage     connectorcontract.Usage
	seen      [4]bool
	deltaSeen bool
	poisoned  bool
}

var errCallerStreamLimit = errors.New("anthropic connector: caller stream exceeded its limit")

type callerStreamBudget struct {
	generated      int64
	errorFrameSize int64
}

type callerStreamWriter struct {
	http.ResponseWriter
	budget callerStreamBudget
}

func (w *callerStreamWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func newCallerStreamBudget() callerStreamBudget {
	frame := httperr.SSEErrorFrame(httperr.New(httperr.CodeUpstream, "upstream stream failed"))
	return callerStreamBudget{errorFrameSize: int64(len(frame))}
}

func (b *callerStreamBudget) consume(size int, errorFrame bool) error {
	if b == nil || size < 0 || size > MaxCallerSSEEventBytes {
		return errCallerStreamLimit
	}
	next, ok := checkedAdd(b.generated, int64(size))
	if !ok {
		return errCallerStreamLimit
	}
	if !errorFrame {
		withReserve, ok := checkedAdd(next, b.errorFrameSize)
		if !ok || withReserve > MaxCallerStreamBytes {
			return errCallerStreamLimit
		}
	} else if next > MaxCallerStreamBytes {
		return errCallerStreamLimit
	}
	b.generated = next
	return nil
}

func (a *Adapter) stream(ctx context.Context, writer http.ResponseWriter, response *http.Response, publicModel string, started time.Time, wireGuard, semanticGuard *sensitiveGuard) connectorcontract.AttemptResult {
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	events, errs := egress.StreamSSE(streamCtx, response.Body, egress.SSEOptions{
		MaxBytes: a.maxStreamBytes, MaxLineBytes: a.maxSSELineBytes, MaxEventBytes: a.maxSSEEventBytes,
		ReadBuffer: min(a.maxSSELineBytes, 64<<10), EventBuffer: 4,
	})
	writer = &callerStreamWriter{ResponseWriter: writer, budget: newCallerStreamBudget()}
	controller := http.NewResponseController(writer)
	defer func() { _ = controller.SetWriteDeadline(time.Time{}) }()

	committed := false
	seenStart := false
	messageID := ""
	nextBlockIndex := int64(0)
	toolCount := 0
	toolIDs := make(map[string]struct{})
	var openBlock *openContentBlock
	defer func() {
		if openBlock != nil {
			clear(openBlock.arguments.Bytes())
		}
	}()
	stopReason := ""
	usageState := cumulativeUsage{}
	terminalSeen := false

	for {
		event, ok, nextErr := nextSSEEvent(streamCtx, events, errs)
		if nextErr != nil || !ok {
			if ctx.Err() != nil {
				result := canceledFailure()
				result.Committed = committed
				result.ClientStatus = committedStatus(committed)
				result.Usage = usageState.final()
				return result
			}
			if terminalSeen && (nextErr == nil || errors.Is(nextErr, io.EOF)) {
				return a.finalizeStream(writer, controller, response.StatusCode, committed, wireGuard, messageID, publicModel, started, stopReason, usageState.final())
			}
			return a.streamProtocolFailure(writer, controller, committed, usageState.final(), "upstream stream ended before completion")
		}
		if len(event.Data) == 0 || len(event.Data) > a.maxSSEEventBytes {
			return a.streamProtocolFailure(writer, controller, committed, usageState.final(), "upstream stream event exceeded protocol bounds")
		}
		root, err := strictObject([]byte(event.Data))
		if err != nil {
			return a.streamProtocolFailure(writer, controller, committed, usageState.final(), "upstream stream event was invalid")
		}
		var eventType string
		if json.Unmarshal(root["type"], &eventType) != nil || event.Event != eventType {
			return a.streamProtocolFailure(writer, controller, committed, usageState.final(), "upstream stream event type was invalid")
		}
		if terminalSeen && eventType != "ping" {
			switch eventType {
			case "error", "message_start", "content_block_start", "content_block_delta", "content_block_stop", "message_delta", "message_stop":
				return a.streamProtocolFailure(writer, controller, committed, usageState.final(), "upstream stream continued after completion")
			default:
				continue
			}
		}
		switch eventType {
		case "ping":
			if !onlyKeys(root, "type") {
				return a.streamProtocolFailure(writer, controller, committed, usageState.final(), "upstream stream ping was invalid")
			}
			continue
		case "error":
			return a.streamProtocolFailure(writer, controller, committed, usageState.final(), "upstream stream reported an error")
		case "message_start":
			if seenStart || !onlyKeys(root, "type", "message") {
				return a.streamProtocolFailure(writer, controller, committed, usageState.final(), "upstream stream start was invalid")
			}
			message, err := strictObject(root["message"])
			if err != nil || !onlyKeys(message, "id", "type", "role", "model", "content", "stop_reason", "stop_sequence", "stop_details", "container", "usage") {
				return a.streamProtocolFailure(writer, controller, committed, usageState.final(), "upstream stream start was invalid")
			}
			var kind, role, upstreamModel string
			var content []json.RawMessage
			if json.Unmarshal(message["id"], &messageID) != nil || !validOpaqueRunes(messageID, 512, true) ||
				json.Unmarshal(message["type"], &kind) != nil || kind != "message" ||
				json.Unmarshal(message["role"], &role) != nil || role != "assistant" ||
				json.Unmarshal(message["model"], &upstreamModel) != nil || !validOpaqueRunes(upstreamModel, 512, true) ||
				json.Unmarshal(message["content"], &content) != nil || len(content) != 0 ||
				!isNull(message["stop_reason"]) || !isNull(message["stop_sequence"]) {
				return a.streamProtocolFailure(writer, controller, committed, usageState.final(), "upstream stream start was invalid")
			}
			containerPresent, containerErr := validateContainer(message["container"])
			if containerErr != nil || containerPresent || validateStopDetails(message["stop_details"], "") != nil {
				return a.streamProtocolFailure(writer, controller, committed, usageState.final(), "upstream stream start was invalid")
			}
			if usageState.merge(message["usage"], true, true) != nil {
				return a.streamProtocolFailure(writer, controller, committed, usageState.final(), "upstream stream usage was invalid")
			}
			if semanticGuard.Contains([]byte(messageID)) {
				return a.streamProtocolFailure(writer, controller, committed, usageState.final(), "upstream stream was rejected")
			}
			frame, err := marshalStreamFrame(streamEnvelope{
				ID: messageID, Object: "chat.completion.chunk", Created: started.Unix(), Model: publicModel,
				Choices: []streamChoice{{Index: 0, Delta: streamDelta{Role: "assistant"}}},
			})
			if err != nil || wireGuard.Contains(frame) {
				clear(frame)
				return a.streamProtocolFailure(writer, controller, committed, usageState.final(), "upstream stream was rejected")
			}
			wrote, writeErr := a.writeStreamFrame(writer, controller, frame)
			clear(frame)
			committed = committed || wrote
			if writeErr != nil {
				if errors.Is(writeErr, errCallerStreamLimit) {
					return a.streamProtocolFailure(writer, controller, committed, usageState.final(), "caller stream exceeded its limit")
				}
				return sinkFailure(committed, usageState.final())
			}
			seenStart = true
		case "content_block_start":
			if !seenStart || openBlock != nil || stopReason != "" || nextBlockIndex >= maxMessages || !onlyKeys(root, "type", "index", "content_block") {
				return a.streamProtocolFailure(writer, controller, committed, usageState.final(), "upstream content block start was invalid")
			}
			index, ok := parseIndex(root["index"])
			if !ok || index != nextBlockIndex {
				return a.streamProtocolFailure(writer, controller, committed, usageState.final(), "upstream content block index was invalid")
			}
			block, err := strictObject(root["content_block"])
			if err != nil {
				return a.streamProtocolFailure(writer, controller, committed, usageState.final(), "upstream content block was invalid")
			}
			var blockType string
			if json.Unmarshal(block["type"], &blockType) != nil {
				return a.streamProtocolFailure(writer, controller, committed, usageState.final(), "upstream content block was invalid")
			}
			switch blockType {
			case "text":
				if !onlyKeys(block, "type", "text") {
					return a.streamProtocolFailure(writer, controller, committed, usageState.final(), "upstream text block was invalid")
				}
				var initial string
				if json.Unmarshal(block["text"], &initial) != nil {
					return a.streamProtocolFailure(writer, controller, committed, usageState.final(), "upstream text block was invalid")
				}
				openBlock = &openContentBlock{index: index, kind: "text"}
				if initial != "" {
					var result connectorcontract.AttemptResult
					committed, result = a.writeTextDelta(writer, controller, wireGuard, semanticGuard, messageID, publicModel, started, initial, committed, usageState.final())
					if result.Failure != connectorcontract.FailureNone {
						return result
					}
				}
			case "tool_use":
				if !onlyKeys(block, "type", "id", "name", "input") || toolCount >= maxTools {
					return a.streamProtocolFailure(writer, controller, committed, usageState.final(), "upstream tool block was invalid")
				}
				var id, name string
				input, err := strictObject(block["input"])
				if err != nil || len(input) != 0 || json.Unmarshal(block["id"], &id) != nil || !validToolID(id) || json.Unmarshal(block["name"], &name) != nil || !functionNameRegexp.MatchString(name) {
					return a.streamProtocolFailure(writer, controller, committed, usageState.final(), "upstream tool block was invalid")
				}
				if _, duplicate := toolIDs[id]; duplicate {
					return a.streamProtocolFailure(writer, controller, committed, usageState.final(), "upstream tool block was invalid")
				}
				toolIDs[id] = struct{}{}
				if semanticGuard.Contains([]byte(id)) || semanticGuard.Contains([]byte(name)) {
					return a.streamProtocolFailure(writer, controller, committed, usageState.final(), "upstream stream was rejected")
				}
				openBlock = &openContentBlock{index: index, kind: "tool_use", toolIndex: toolCount}
				frame, err := marshalStreamFrame(streamEnvelope{
					ID: messageID, Object: "chat.completion.chunk", Created: started.Unix(), Model: publicModel,
					Choices: []streamChoice{{Index: 0, Delta: streamDelta{ToolCalls: []streamToolCallDelta{{Index: toolCount, ID: id, Type: "function", Function: streamToolFunctionDelta{Name: name, Arguments: ""}}}}}},
				})
				if err != nil || wireGuard.Contains(frame) {
					clear(frame)
					return a.streamProtocolFailure(writer, controller, committed, usageState.final(), "upstream stream was rejected")
				}
				wrote, writeErr := a.writeStreamFrame(writer, controller, frame)
				clear(frame)
				committed = committed || wrote
				if writeErr != nil {
					if errors.Is(writeErr, errCallerStreamLimit) {
						return a.streamProtocolFailure(writer, controller, committed, usageState.final(), "caller stream exceeded its limit")
					}
					return sinkFailure(committed, usageState.final())
				}
				toolCount++
			default:
				return a.streamProtocolFailure(writer, controller, committed, usageState.final(), "upstream content block type was unsupported")
			}
			nextBlockIndex++
		case "content_block_delta":
			if !seenStart || openBlock == nil || stopReason != "" || !onlyKeys(root, "type", "index", "delta") {
				return a.streamProtocolFailure(writer, controller, committed, usageState.final(), "upstream content delta was invalid")
			}
			index, ok := parseIndex(root["index"])
			if !ok || index != openBlock.index {
				return a.streamProtocolFailure(writer, controller, committed, usageState.final(), "upstream content delta index was invalid")
			}
			delta, err := strictObject(root["delta"])
			if err != nil {
				return a.streamProtocolFailure(writer, controller, committed, usageState.final(), "upstream content delta was invalid")
			}
			var deltaType string
			if json.Unmarshal(delta["type"], &deltaType) != nil {
				return a.streamProtocolFailure(writer, controller, committed, usageState.final(), "upstream content delta was invalid")
			}
			switch {
			case openBlock.kind == "text" && deltaType == "text_delta" && onlyKeys(delta, "type", "text"):
				var text string
				if json.Unmarshal(delta["text"], &text) != nil {
					return a.streamProtocolFailure(writer, controller, committed, usageState.final(), "upstream text delta was invalid")
				}
				var result connectorcontract.AttemptResult
				committed, result = a.writeTextDelta(writer, controller, wireGuard, semanticGuard, messageID, publicModel, started, text, committed, usageState.final())
				if result.Failure != connectorcontract.FailureNone {
					return result
				}
			case openBlock.kind == "tool_use" && deltaType == "input_json_delta" && onlyKeys(delta, "type", "partial_json"):
				var partial string
				if json.Unmarshal(delta["partial_json"], &partial) != nil {
					return a.streamProtocolFailure(writer, controller, committed, usageState.final(), "upstream tool arguments were invalid")
				}
				aggregate, ok := checkedAdd(int64(openBlock.arguments.Len()), int64(len(partial)))
				if !ok || aggregate > a.maxStreamBytes {
					return a.streamProtocolFailure(writer, controller, committed, usageState.final(), "upstream tool arguments were invalid")
				}
				openBlock.arguments.WriteString(partial)
			default:
				return a.streamProtocolFailure(writer, controller, committed, usageState.final(), "upstream content delta type was invalid")
			}
		case "content_block_stop":
			if !seenStart || openBlock == nil || stopReason != "" || !onlyKeys(root, "type", "index") {
				return a.streamProtocolFailure(writer, controller, committed, usageState.final(), "upstream content block stop was invalid")
			}
			index, ok := parseIndex(root["index"])
			if !ok || index != openBlock.index {
				return a.streamProtocolFailure(writer, controller, committed, usageState.final(), "upstream content block stop index was invalid")
			}
			if openBlock.kind == "tool_use" {
				arguments := openBlock.arguments.Bytes()
				if len(arguments) == 0 {
					arguments = []byte("{}")
				}
				if _, err := strictObject(arguments); err != nil {
					return a.streamProtocolFailure(writer, controller, committed, usageState.final(), "upstream tool arguments were invalid")
				}
				reflected, err := semanticGuard.ContainsJSONStrings(arguments)
				if err != nil {
					return a.streamProtocolFailure(writer, controller, committed, usageState.final(), "upstream tool arguments were invalid")
				}
				if reflected {
					return a.streamProtocolFailure(writer, controller, committed, usageState.final(), "upstream stream was rejected")
				}
				var result connectorcontract.AttemptResult
				committed, result = a.writeToolArguments(writer, controller, wireGuard, messageID, publicModel, started, openBlock.toolIndex, arguments, committed, usageState.final())
				if result.Failure != connectorcontract.FailureNone {
					return result
				}
			}
			clear(openBlock.arguments.Bytes())
			openBlock = nil
		case "message_delta":
			if !seenStart || openBlock != nil || !onlyKeys(root, "type", "delta", "usage") {
				return a.streamProtocolFailure(writer, controller, committed, usageState.final(), "upstream message delta was invalid")
			}
			delta, err := strictObject(root["delta"])
			if err != nil || !onlyKeys(delta, "stop_reason", "stop_sequence", "stop_details", "container") {
				return a.streamProtocolFailure(writer, controller, committed, usageState.final(), "upstream message delta was invalid")
			}
			mapped, upstreamStopReason, err := parseStopReasonKind(delta["stop_reason"])
			if err != nil || (stopReason != "" && stopReason != mapped) {
				return a.streamProtocolFailure(writer, controller, committed, usageState.final(), "upstream stop reason was invalid")
			}
			containerPresent, containerErr := validateContainer(delta["container"])
			if containerErr != nil || containerPresent || validateStopDetails(delta["stop_details"], mapped) != nil {
				return a.streamProtocolFailure(writer, controller, committed, usageState.final(), "upstream message delta was invalid")
			}
			if validateStopSequence(delta["stop_sequence"], upstreamStopReason) != nil {
				return a.streamProtocolFailure(writer, controller, committed, usageState.final(), "upstream stop sequence was invalid")
			}
			stopReason = mapped
			if usageState.merge(root["usage"], false, false) != nil {
				return a.streamProtocolFailure(writer, controller, committed, usageState.final(), "upstream stream usage was invalid")
			}
		case "message_stop":
			if !seenStart || openBlock != nil || stopReason == "" || !onlyKeys(root, "type") || (stopReason == "tool_calls") != (toolCount > 0) {
				return a.streamProtocolFailure(writer, controller, committed, usageState.final(), "upstream message stop was invalid")
			}
			terminalSeen = true
			continue
		default:
			// Future outer event types are ignored only when the named SSE event
			// exactly matches their JSON type. Unknown content block types remain
			// protocol failures in the known branches above.
			continue
		}
	}
}

func (a *Adapter) writeTextDelta(writer http.ResponseWriter, controller *http.ResponseController, wireGuard, semanticGuard *sensitiveGuard, messageID, publicModel string, started time.Time, text string, committed bool, usage connectorcontract.Usage) (bool, connectorcontract.AttemptResult) {
	decoded := []byte(text)
	reflected := semanticGuard.Contains(decoded)
	clear(decoded)
	if reflected {
		return committed, a.streamProtocolFailure(writer, controller, committed, usage, "upstream stream was rejected")
	}
	remaining := text
	for {
		chunk, rest := takeSafeStringChunk(remaining)
		frame, err := marshalStreamFrame(streamEnvelope{
			ID: messageID, Object: "chat.completion.chunk", Created: started.Unix(), Model: publicModel,
			Choices: []streamChoice{{Index: 0, Delta: streamDelta{Content: &chunk}}},
		})
		if err != nil || wireGuard.Contains(frame) {
			clear(frame)
			return committed, a.streamProtocolFailure(writer, controller, committed, usage, "upstream stream was rejected")
		}
		wrote, writeErr := a.writeStreamFrame(writer, controller, frame)
		clear(frame)
		committed = committed || wrote
		if writeErr != nil {
			if errors.Is(writeErr, errCallerStreamLimit) {
				return committed, a.streamProtocolFailure(writer, controller, committed, usage, "caller stream exceeded its limit")
			}
			return committed, sinkFailure(committed, usage)
		}
		if rest == "" {
			break
		}
		remaining = rest
	}
	return committed, connectorcontract.AttemptResult{Failure: connectorcontract.FailureNone}
}

func (a *Adapter) writeToolArguments(writer http.ResponseWriter, controller *http.ResponseController, wireGuard *sensitiveGuard, messageID, publicModel string, started time.Time, toolIndex int, arguments []byte, committed bool, usage connectorcontract.Usage) (bool, connectorcontract.AttemptResult) {
	remaining := string(arguments)
	for {
		chunk, rest := takeSafeStringChunk(remaining)
		frame, err := marshalStreamFrame(streamEnvelope{
			ID: messageID, Object: "chat.completion.chunk", Created: started.Unix(), Model: publicModel,
			Choices: []streamChoice{{Index: 0, Delta: streamDelta{ToolCalls: []streamToolCallDelta{{Index: toolIndex, Function: streamToolFunctionDelta{Arguments: chunk}}}}}},
		})
		if err != nil || wireGuard.Contains(frame) {
			clear(frame)
			return committed, a.streamProtocolFailure(writer, controller, committed, usage, "upstream stream was rejected")
		}
		wrote, writeErr := a.writeStreamFrame(writer, controller, frame)
		clear(frame)
		committed = committed || wrote
		if writeErr != nil {
			if errors.Is(writeErr, errCallerStreamLimit) {
				return committed, a.streamProtocolFailure(writer, controller, committed, usage, "caller stream exceeded its limit")
			}
			return committed, sinkFailure(committed, usage)
		}
		if rest == "" {
			break
		}
		remaining = rest
	}
	return committed, connectorcontract.AttemptResult{Failure: connectorcontract.FailureNone}
}

func takeSafeStringChunk(value string) (string, string) {
	if len(value) <= maxSafeStringChunkBytes {
		return value, ""
	}
	end := maxSafeStringChunkBytes
	for end > 0 && !utf8.RuneStart(value[end]) {
		end--
	}
	if end == 0 {
		return value, ""
	}
	return value[:end], value[end:]
}

func (a *Adapter) finalizeStream(writer http.ResponseWriter, controller *http.ResponseController, upstreamStatus int, committed bool, wireGuard *sensitiveGuard, messageID, publicModel string, started time.Time, stopReason string, finalUsage connectorcontract.Usage) connectorcontract.AttemptResult {
	finish := stopReason
	frame, err := marshalStreamFrame(streamEnvelope{
		ID: messageID, Object: "chat.completion.chunk", Created: started.Unix(), Model: publicModel,
		Choices: []streamChoice{{Index: 0, Delta: streamDelta{}, FinishReason: &finish}},
	})
	if err != nil || wireGuard.Contains(frame) {
		clear(frame)
		return a.streamProtocolFailure(writer, controller, committed, finalUsage, "upstream stream was rejected")
	}
	wrote, writeErr := a.writeStreamFrame(writer, controller, frame)
	clear(frame)
	committed = committed || wrote
	if writeErr != nil {
		if errors.Is(writeErr, errCallerStreamLimit) {
			return a.streamProtocolFailure(writer, controller, committed, finalUsage, "caller stream exceeded its limit")
		}
		return sinkFailure(committed, finalUsage)
	}
	if finalUsage.Present {
		visible, ok := visibleUsage(finalUsage)
		if ok {
			frame, err = marshalStreamFrame(streamEnvelope{ID: messageID, Object: "chat.completion.chunk", Created: started.Unix(), Model: publicModel, Choices: []streamChoice{}, Usage: &visible})
			if err != nil || wireGuard.Contains(frame) {
				clear(frame)
				return a.streamProtocolFailure(writer, controller, committed, connectorcontract.Usage{}, "upstream stream was rejected")
			}
			wrote, writeErr = a.writeStreamFrame(writer, controller, frame)
			clear(frame)
			committed = committed || wrote
			if writeErr != nil {
				if errors.Is(writeErr, errCallerStreamLimit) {
					return a.streamProtocolFailure(writer, controller, committed, finalUsage, "caller stream exceeded its limit")
				}
				return sinkFailure(committed, finalUsage)
			}
		} else {
			finalUsage = connectorcontract.Usage{}
		}
	}
	done := []byte("data: [DONE]\n\n")
	if wireGuard.Contains(done) {
		return a.streamProtocolFailure(writer, controller, committed, finalUsage, "upstream stream was rejected")
	}
	wrote, writeErr = a.writeStreamFrame(writer, controller, done)
	committed = committed || wrote
	if writeErr != nil {
		if errors.Is(writeErr, errCallerStreamLimit) {
			return a.streamProtocolFailure(writer, controller, committed, finalUsage, "caller stream exceeded its limit")
		}
		return sinkFailure(committed, finalUsage)
	}
	return connectorcontract.AttemptResult{Success: true, Committed: committed, Failure: connectorcontract.FailureNone, UpstreamStatus: upstreamStatus, ClientStatus: http.StatusOK, Usage: finalUsage}
}

func marshalStreamFrame(envelope streamEnvelope) ([]byte, error) {
	data, err := marshalJSONNoEscapeLimited(envelope, MaxCallerSSEEventBytes-8)
	if err != nil {
		return nil, err
	}
	frame := make([]byte, 0, len(data)+8)
	frame = append(frame, "data: "...)
	frame = append(frame, data...)
	frame = append(frame, '\n', '\n')
	clear(data)
	return frame, nil
}

func (a *Adapter) streamProtocolFailure(writer http.ResponseWriter, controller *http.ResponseController, committed bool, usage connectorcontract.Usage, diagnostic string) connectorcontract.AttemptResult {
	if !committed {
		result := upstreamFailure(diagnostic, http.StatusOK)
		result.Usage = usage
		return result
	}
	frame := httperr.SSEErrorFrame(httperr.New(httperr.CodeUpstream, "upstream stream failed"))
	_, err := a.writeStreamErrorFrame(writer, controller, frame)
	if err != nil {
		return sinkFailure(true, usage)
	}
	return connectorcontract.AttemptResult{Committed: true, Failure: connectorcontract.FailureUpstream, Diagnostic: diagnostic, UpstreamStatus: http.StatusOK, ClientStatus: http.StatusOK, Usage: usage}
}

func (a *Adapter) writeStreamFrame(writer http.ResponseWriter, controller *http.ResponseController, frame []byte) (bool, error) {
	if budgeted, ok := writer.(*callerStreamWriter); ok {
		if err := budgeted.budget.consume(len(frame), false); err != nil {
			return false, err
		}
	}
	return a.writeStreamFrameUnchecked(writer, controller, frame)
}

func (a *Adapter) writeStreamErrorFrame(writer http.ResponseWriter, controller *http.ResponseController, frame []byte) (bool, error) {
	if budgeted, ok := writer.(*callerStreamWriter); ok {
		if err := budgeted.budget.consume(len(frame), true); err != nil {
			return false, err
		}
	}
	return a.writeStreamFrameUnchecked(writer, controller, frame)
}

func (a *Adapter) writeStreamFrameUnchecked(writer http.ResponseWriter, controller *http.ResponseController, frame []byte) (bool, error) {
	setSSEHeaders(writer.Header())
	if a.streamWriteTimeout > 0 {
		if err := controller.SetWriteDeadline(time.Now().Add(a.streamWriteTimeout)); err != nil && !errors.Is(err, http.ErrNotSupported) {
			return false, err
		} else if err == nil {
			defer func() { _ = controller.SetWriteDeadline(time.Time{}) }()
		}
	}
	n, err := writer.Write(frame)
	committed := n > 0
	if err != nil {
		return committed, err
	}
	if n != len(frame) {
		return committed, io.ErrShortWrite
	}
	if err := controller.Flush(); err != nil && !errors.Is(err, http.ErrNotSupported) {
		return committed, err
	}
	return committed, nil
}

func nextSSEEvent(ctx context.Context, events <-chan egress.SSEEvent, errs <-chan error) (egress.SSEEvent, bool, error) {
	for events != nil || errs != nil {
		select {
		case <-ctx.Done():
			return egress.SSEEvent{}, false, ctx.Err()
		case event, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			return event, true, nil
		case err, ok := <-errs:
			if !ok {
				errs = nil
				continue
			}
			if err != nil {
				return egress.SSEEvent{}, false, err
			}
		}
	}
	return egress.SSEEvent{}, false, io.EOF
}

func parseIndex(raw []byte) (int64, bool) {
	var index int64
	return index, json.Unmarshal(raw, &index) == nil && index >= 0
}

func isNull(raw []byte) bool { return bytes.Equal(bytes.TrimSpace(raw), []byte("null")) }

func (s *cumulativeUsage) merge(raw []byte, initial, full bool) error {
	if s == nil {
		return ErrInvalidResponse
	}
	object, err := strictObject(raw)
	allowed := []string{"input_tokens", "cache_creation_input_tokens", "cache_read_input_tokens", "output_tokens", "output_tokens_details", "server_tool_use"}
	if full {
		allowed = append(allowed, "cache_creation", "inference_geo", "service_tier")
	}
	if err != nil || !onlyKeys(object, allowed...) {
		return ErrInvalidResponse
	}
	values := []*int64{&s.usage.UncachedInputTokens, &s.usage.CacheWriteInputTokens, &s.usage.CacheReadInputTokens, &s.usage.OutputTokens}
	names := []string{"input_tokens", "cache_creation_input_tokens", "cache_read_input_tokens", "output_tokens"}
	outputKnown := s.seen[3]
	for index, name := range names {
		rawValue, present := object[name]
		if !present {
			if initial && (index == 1 || index == 2) {
				s.seen[index] = true
			}
			if (initial && (index == 0 || index == 3)) || (!initial && index == 3) {
				s.poisoned = true
				if index == 3 {
					outputKnown = false
				}
			}
			continue
		}
		var value int64
		if isNull(rawValue) || json.Unmarshal(rawValue, &value) != nil || value < 0 || (s.seen[index] && value < *values[index]) {
			s.poisoned = true
			if index == 3 {
				outputKnown = false
			}
			continue
		}
		*values[index] = value
		s.seen[index] = true
		if index == 3 {
			outputKnown = true
		}
	}
	if err := validateUsageMetadata(object, full, s.usage.OutputTokens, outputKnown); err != nil {
		return err
	}
	if !s.poisoned {
		if _, ok := visibleUsage(connectorcontract.Usage{UncachedInputTokens: *values[0], CacheWriteInputTokens: *values[1], CacheReadInputTokens: *values[2], OutputTokens: *values[3], Present: true}); !ok {
			s.poisoned = true
		}
	}
	if !initial {
		s.deltaSeen = true
	}
	return nil
}

func (s cumulativeUsage) final() connectorcontract.Usage {
	if s.poisoned || !s.deltaSeen || !s.seen[0] || !s.seen[1] || !s.seen[2] || !s.seen[3] {
		return connectorcontract.Usage{}
	}
	s.usage.Present = true
	if _, ok := visibleUsage(s.usage); !ok {
		return connectorcontract.Usage{}
	}
	return s.usage
}
