package forward

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
)

var errTransportResponse = errors.New("upstream response cannot be converted")

// Conversion buffers validated connector output independently of caller delivery.
type transportResponse struct {
	header         http.Header
	body           bytes.Buffer
	upstreamStream bool
	err            error
}

func newTransportResponse(stream bool) *transportResponse {
	return &transportResponse{header: make(http.Header), upstreamStream: stream}
}
func (w *transportResponse) Header() http.Header            { return w.header }
func (*transportResponse) WriteHeader(int)                  {}
func (*transportResponse) Flush()                           {}
func (*transportResponse) SetWriteDeadline(time.Time) error { return nil }
func (w *transportResponse) Write(p []byte) (int, error) {
	limit := openai.DefaultMaxJSONResponseBytes
	if w.upstreamStream {
		limit = openai.DefaultMaxStreamBytes
	}
	if int64(len(p)) > limit-int64(w.body.Len()) {
		w.err = errTransportResponse
	}
	if w.err != nil {
		return 0, w.err
	}
	return w.body.Write(p)
}
func (w *transportResponse) clear() { clear(w.body.Bytes()); w.body.Reset() }

func (w *transportResponse) complete(ctx context.Context, sink http.ResponseWriter, includeUsage bool, result contract.AttemptResult) contract.AttemptResult {
	result.Committed = false
	if w.err != nil {
		return conversionFailure(result)
	}
	if !result.Success {
		return result
	}
	if ctx.Err() != nil {
		return transportContextFailure(ctx, result)
	}
	var frames [][]byte
	var err error
	if w.upstreamStream {
		var body []byte
		body, err = streamToCompletion(w.body.Bytes())
		frames = [][]byte{body}
	} else {
		frames, err = completionToStream(w.body.Bytes(), includeUsage)
	}
	if err != nil {
		return conversionFailure(result)
	}
	defer func() {
		for _, frame := range frames {
			clear(frame)
		}
	}()
	if ctx.Err() != nil {
		return transportContextFailure(ctx, result)
	}
	sink.Header().Set("Content-Type", "application/json")
	if !w.upstreamStream {
		sink.Header().Set("Content-Type", "text/event-stream")
		sink.Header().Set("Cache-Control", "no-cache")
		sink.Header().Set("X-Accel-Buffering", "no")
	}
	controller := http.NewResponseController(sink)
	deadline := time.Now().Add(streamWriteTimeout)
	if limit, ok := ctx.Deadline(); ok && limit.Before(deadline) {
		deadline = limit
	}
	if err := controller.SetWriteDeadline(deadline); err != nil && !errors.Is(err, http.ErrNotSupported) {
		if ctx.Err() != nil {
			return transportContextFailure(ctx, result)
		}
		return transportSinkFailure(result)
	}
	defer func() { _ = controller.SetWriteDeadline(time.Time{}) }()
	for _, frame := range frames {
		if ctx.Err() != nil {
			return transportContextFailure(ctx, result)
		}
		n, err := sink.Write(frame)
		result.Committed = result.Committed || n > 0
		if err != nil || n != len(frame) {
			if ctx.Err() != nil {
				return transportContextFailure(ctx, result)
			}
			return transportSinkFailure(result)
		}
	}
	if !w.upstreamStream {
		if err := controller.Flush(); err != nil && !errors.Is(err, http.ErrNotSupported) {
			if ctx.Err() != nil {
				return transportContextFailure(ctx, result)
			}
			return transportSinkFailure(result)
		}
	}
	result.ClientStatus = http.StatusOK
	return result
}

func conversionFailure(result contract.AttemptResult) contract.AttemptResult {
	result.Success, result.Committed, result.SinkFailed = false, false, false
	result.Failure, result.ClientStatus = contract.FailureUpstream, http.StatusBadGateway
	result.Diagnostic = "upstream response cannot be converted within response limits"
	if result.StreakDisposition == contract.StreakSuccess {
		return result
	}
	return contract.UpstreamFailed(result, contract.OriginUpstreamProtocol)
}

// Caller delivery can time out after the upstream has already succeeded. Keep
// that confirmed protocol result and usage independently of the caller error.
func transportContextFailure(ctx context.Context, result contract.AttemptResult) contract.AttemptResult {
	result.Success, result.SinkFailed = false, false
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		result.Failure, result.ClientStatus = contract.FailureUpstream, http.StatusGatewayTimeout
		result.Diagnostic = "forward request timed out"
	} else {
		result.Failure, result.ClientStatus = contract.FailureCanceled, 0
		result.Diagnostic = "request canceled"
	}
	return result
}
func transportSinkFailure(result contract.AttemptResult) contract.AttemptResult {
	result.Success, result.SinkFailed, result.Failure = false, true, contract.FailureSink
	return result
}

func callerIncludesUsage(request *openai.ChatRequest) bool {
	raw, _ := request.RawField("stream_options")
	defer clear(raw)
	var options map[string]bool
	return json.Unmarshal(raw, &options) == nil && options["include_usage"]
}

func readWireObject(raw []byte) (map[string]any, error) {
	var value map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&value) != nil || value == nil {
		return nil, errTransportResponse
	}
	return value, nil
}

func completionToStream(raw []byte, includeUsage bool) ([][]byte, error) {
	if int64(len(raw)) > openai.DefaultMaxJSONResponseBytes {
		return nil, errTransportResponse
	}
	root, err := readWireObject(raw)
	if err != nil {
		return nil, err
	}
	choices, ok := root["choices"].([]any)
	if !ok || len(choices) == 0 {
		return nil, errTransportResponse
	}
	usage := root["usage"]
	delete(root, "usage")
	root["object"] = "chat.completion.chunk"
	var frames [][]byte
	var total int64
	appendFrame := func(rows []any) error {
		root["choices"] = rows
		body, err := json.Marshal(root)
		defer clear(body)
		if err != nil || len(body)+6 > openai.DefaultMaxSSELineBytes || len(body)+8 > openai.DefaultMaxSSEEventBytes || int64(len(body)+8) > openai.DefaultMaxStreamBytes-total {
			return errTransportResponse
		}
		frames = append(frames, append(append([]byte("data: "), body...), '\n', '\n'))
		total += int64(len(body) + 8)
		return nil
	}
	complete := false
	defer func() {
		if !complete {
			for _, frame := range frames {
				clear(frame)
			}
		}
	}()
	ends := make([]map[string]any, 0, len(choices))
	for _, value := range choices {
		choice, ok := value.(map[string]any)
		if !ok {
			return nil, errTransportResponse
		}
		message, ok := choice["message"].(map[string]any)
		if !ok {
			return nil, errTransportResponse
		}
		if calls, ok := message["tool_calls"].([]any); ok {
			for i, value := range calls {
				call, ok := value.(map[string]any)
				if !ok {
					return nil, errTransportResponse
				}
				call["index"] = i
			}
		}
		index := choice["index"]
		extra := make(map[string]any)
		for key, value := range choice {
			if key != "index" && key != "message" && key != "finish_reason" {
				extra[key] = value
			}
		}
		emit := func(delta, extra map[string]any) error {
			row := map[string]any{"index": index, "delta": delta, "finish_reason": nil}
			for key, value := range extra {
				row[key] = value
			}
			return appendFrame([]any{row})
		}
		if err := emit(message, extra); err != nil {
			if err := fragmentCompletionFields(message, func(fragment map[string]any) error { return emit(fragment, nil) }); err != nil {
				return nil, err
			}
			if err := fragmentCompletionFields(extra, func(fragment map[string]any) error { return emit(map[string]any{}, fragment) }); err != nil {
				return nil, err
			}
		}
		ends = append(ends, map[string]any{"index": index, "delta": map[string]any{}, "finish_reason": choice["finish_reason"]})
	}
	for _, end := range ends {
		if err := appendFrame([]any{end}); err != nil {
			return nil, err
		}
	}
	if includeUsage && usage != nil {
		root["usage"] = usage
		if err := appendFrame([]any{}); err != nil {
			return nil, err
		}
	}
	if int64(len("data: [DONE]\n\n")) > openai.DefaultMaxStreamBytes-total {
		return nil, errTransportResponse
	}
	complete = true
	return append(frames, []byte("data: [DONE]\n\n")), nil
}

// Split only fields whose delta representation can be faithfully accumulated.
// Each encoded fragment is checked by the frame emitter before being retained.
// No sleeps or token timing are introduced; completed output is delivered at once.
func fragmentCompletionFields(fields map[string]any, emit func(map[string]any) error) error {
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	priority := map[string]int{"role": 0, "id": 1, "type": 2, "name": 3}
	sort.Slice(keys, func(i, j int) bool {
		left, leftOK := priority[keys[i]]
		right, rightOK := priority[keys[j]]
		if leftOK != rightOK {
			return leftOK
		}
		if leftOK && left != right {
			return left < right
		}
		return keys[i] < keys[j]
	})
	for _, key := range keys {
		value := fields[key]
		wrap := func(value any) error { return emit(map[string]any{key: value}) }
		if err := wrap(value); err == nil {
			continue
		}
		switch typed := value.(type) {
		case string:
			switch key {
			case "content", "refusal", "reasoning", "reasoning_content", "name", "arguments":
			default:
				return errTransportResponse
			}
			for len(typed) > 0 {
				// JSON escaping can expand one source byte to six wire bytes.
				end := min(len(typed), openai.DefaultMaxSSELineBytes/6)
				for end < len(typed) && !utf8.RuneStart(typed[end]) {
					end--
				}
				for end > 0 {
					if err := wrap(typed[:end]); err == nil {
						break
					}
					end /= 2
					for end > 0 && !utf8.RuneStart(typed[end]) {
						end--
					}
				}
				if end == 0 {
					return errTransportResponse
				}
				typed = typed[end:]
			}
		case map[string]any:
			if err := fragmentCompletionFields(typed, func(part map[string]any) error { return wrap(part) }); err != nil {
				return err
			}
		case []any:
			for _, item := range typed {
				if key != "tool_calls" {
					if err := wrap([]any{item}); err != nil {
						return err
					}
					continue
				}
				tool, ok := item.(map[string]any)
				if !ok {
					return errTransportResponse
				}
				index := tool["index"]
				parts := make(map[string]any, len(tool))
				for field, value := range tool {
					if field != "index" {
						parts[field] = value
					}
				}
				if err := fragmentCompletionFields(parts, func(part map[string]any) error {
					part["index"] = index
					return wrap([]any{part})
				}); err != nil {
					return err
				}
			}
		default:
			return errTransportResponse
		}
	}
	return nil
}

type indexedDeltas map[int64]map[string]any

// mergeDelta follows Chat Completions delta semantics. Text and function
// fragments append; indexed tools merge without using remote indices as sizes.
func mergeDelta(dst, src map[string]any) error {
	for key, value := range src {
		if value == nil {
			continue
		}
		switch typed := value.(type) {
		case string:
			switch key {
			case "content", "refusal", "reasoning", "reasoning_content", "name", "arguments":
				builder, ok := dst[key].(*strings.Builder)
				if !ok {
					builder = &strings.Builder{}
					dst[key] = builder
				}
				builder.WriteString(typed)
			default:
				dst[key] = typed
			}
		case map[string]any:
			object, ok := dst[key].(map[string]any)
			if !ok {
				object = make(map[string]any)
				dst[key] = object
			}
			if err := mergeDelta(object, typed); err != nil {
				return err
			}
		case []any:
			if key == "tool_calls" {
				tools, ok := dst[key].(indexedDeltas)
				if !ok {
					tools = make(indexedDeltas)
					dst[key] = tools
				}
				for _, raw := range typed {
					tool, ok := raw.(map[string]any)
					if !ok {
						return errTransportResponse
					}
					index, err := wireIndex(tool["index"])
					if err != nil {
						return err
					}
					if tools[index] == nil {
						tools[index] = make(map[string]any)
					}
					delete(tool, "index")
					if err := mergeDelta(tools[index], tool); err != nil {
						return err
					}
				}
			} else {
				prior, _ := dst[key].([]any)
				dst[key] = append(prior, typed...)
			}
		default:
			dst[key] = value
		}
	}
	return nil
}

func wireIndex(value any) (int64, error) {
	n, ok := value.(json.Number)
	if !ok {
		return 0, errTransportResponse
	}
	index, err := n.Int64()
	if err != nil || index < 0 {
		return 0, errTransportResponse
	}
	return index, nil
}

func materializeWire(value any) any {
	switch typed := value.(type) {
	case *strings.Builder:
		return typed.String()
	case indexedDeltas:
		keys := make([]int64, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
		result := make([]any, 0, len(keys))
		for _, key := range keys {
			result = append(result, materializeWire(typed[key]))
		}
		return result
	case map[string]any:
		for key, value := range typed {
			typed[key] = materializeWire(value)
		}
	case []any:
		for i, value := range typed {
			typed[i] = materializeWire(value)
		}
	}
	return value
}

func streamToCompletion(raw []byte) ([]byte, error) {
	root := make(map[string]any)
	choices := make(indexedDeltas)
	done := false
	for len(raw) > 0 {
		end := bytes.Index(raw, []byte("\n\n"))
		if end < 0 {
			return nil, errTransportResponse
		}
		frame := bytes.TrimSpace(raw[:end])
		raw = raw[end+2:]
		if len(frame) == 0 || frame[0] == ':' {
			continue
		}
		if done || !bytes.HasPrefix(frame, []byte("data:")) {
			return nil, errTransportResponse
		}
		data := bytes.TrimSpace(frame[5:])
		if bytes.Equal(data, []byte("[DONE]")) {
			done = true
			continue
		}
		chunk, err := readWireObject(data)
		if err != nil {
			return nil, err
		}
		if chunk["error"] != nil {
			return nil, errTransportResponse
		}
		for key, value := range chunk {
			if key != "choices" && value != nil {
				root[key] = value
			}
		}
		rows, ok := chunk["choices"].([]any)
		if !ok {
			return nil, errTransportResponse
		}
		for _, value := range rows {
			row, ok := value.(map[string]any)
			if !ok {
				return nil, errTransportResponse
			}
			index, err := wireIndex(row["index"])
			if err != nil {
				return nil, err
			}
			choice := choices[index]
			if choice == nil {
				choice = map[string]any{"index": index, "message": map[string]any{}}
				choices[index] = choice
			}
			if delta, ok := row["delta"].(map[string]any); ok {
				if err := mergeDelta(choice["message"].(map[string]any), delta); err != nil {
					return nil, err
				}
			}
			delete(row, "delta")
			if err := mergeDelta(choice, row); err != nil {
				return nil, err
			}
		}
	}
	if !done || len(choices) == 0 {
		return nil, errTransportResponse
	}
	for _, choice := range choices {
		if choice["finish_reason"] == nil {
			return nil, errTransportResponse
		}
		message := choice["message"].(map[string]any)
		if message["role"] == nil {
			message["role"] = "assistant"
		}
		if message["content"] == nil {
			message["content"] = nil
		}
	}
	root["object"], root["choices"] = "chat.completion", choices
	body, err := json.Marshal(materializeWire(root))
	if err != nil || int64(len(body)) > openai.DefaultMaxJSONResponseBytes {
		clear(body)
		return nil, errTransportResponse
	}
	return body, nil
}
