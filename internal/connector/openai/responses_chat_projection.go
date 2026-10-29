package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
)

// AsChatRequest applies the same finite conversion as a Chat upstream, without
// sending anything. The caller owns the returned snapshot and must clear it.
func (r *InferenceRequest) AsChatRequest() (*ChatRequest, error) {
	return r.asChatRequest(false)
}

// AsGatewayChatRequest also accepts the bounded reasoning and continuation
// items emitted by the Gateway Responses projection.
func (r *InferenceRequest) AsGatewayChatRequest() (*ChatRequest, error) {
	return r.asChatRequest(true)
}

func (r *InferenceRequest) asChatRequest(gateway bool) (*ChatRequest, error) {
	if r == nil || r.operation != contract.OperationResponses {
		return nil, ErrInvalidRequest
	}
	body, err := r.compileChatResponseMode(r.Model, contract.AttemptPolicy{}, gateway)
	defer clear(body)
	if err != nil {
		return nil, err
	}
	return DecodeChatRequest(bytes.NewReader(body), r.RequestBodyLimit()+(16<<10))
}

// ResponsesChatProjection converts a connector's validated Chat output. Its
// streaming input consists of complete SSE frames, not arbitrary network reads.
// It adds no goroutine or transport, and bounds the generated response too.
type ResponsesChatProjection struct {
	ctx       context.Context
	writer    http.ResponseWriter
	request   *InferenceRequest
	state     responseChatState
	limit     int64
	written   int64
	read      int64
	committed bool
	complete  bool
	rejected  bool
	failed    bool
}

// NewGatewayResponsesChatProjection explicitly enables the reasoning text
// already validated by the Gateway codec. Ordinary Chat conversion stays strict.
func NewGatewayResponsesChatProjection(ctx context.Context, writer http.ResponseWriter, request *InferenceRequest, limit int64) (*ResponsesChatProjection, error) {
	if ctx == nil || writer == nil || request == nil || request.Operation() != contract.OperationResponses || limit <= httperr.MaxSSEErrorFrameBytes {
		return nil, ErrInvalidRequest
	}
	maximum := DefaultMaxJSONResponseBytes
	if request.Stream {
		maximum = DefaultMaxStreamBytes
	}
	return &ResponsesChatProjection{ctx: ctx, writer: writer, request: request, state: responseChatState{model: request.Model, gatewayReasoning: true}, limit: min(limit, maximum)}, nil
}

func (p *ResponsesChatProjection) Clear()                      { p.state.clear() }
func (p *ResponsesChatProjection) Header() http.Header         { return p.writer.Header() }
func (p *ResponsesChatProjection) Unwrap() http.ResponseWriter { return p.writer }
func (p *ResponsesChatProjection) WriteHeader(status int)      { p.writer.WriteHeader(status) }
func (p *ResponsesChatProjection) Rejected() bool              { return p.rejected }
func (p *ResponsesChatProjection) Complete() bool              { return p.complete }
func (p *ResponsesChatProjection) Committed() bool             { return p.committed }
func (p *ResponsesChatProjection) Flush()                      { _ = p.FlushError() }
func (p *ResponsesChatProjection) FlushError() error {
	if err := p.ctx.Err(); err != nil {
		return err
	}
	if !p.committed {
		return nil
	}
	err := http.NewResponseController(p.writer).Flush()
	if err != nil && !errors.Is(err, http.ErrNotSupported) {
		p.failed = true
	}
	return err
}

// Only projected output establishes the downstream checkpoint. Upstream header
// observation and response deadlines still traverse Unwrap normally.
func (p *ResponsesChatProjection) MarkResponseStarted() error { return nil }

func (p *ResponsesChatProjection) Write(raw []byte) (int, error) {
	if p.complete || p.rejected || p.failed {
		return 0, errInvalidUpstreamResponse
	}
	if err := p.ctx.Err(); err != nil {
		return 0, err
	}
	if int64(len(raw)) > p.limit-p.read {
		return p.reject()
	}
	p.read += int64(len(raw))
	if !p.request.Stream {
		if err := p.state.consume(raw, false); err != nil {
			return p.reject()
		}
		body, err := json.Marshal(p.state.response(true))
		defer clear(body)
		if err != nil || int64(len(body)) > p.limit {
			return p.reject()
		}
		if err := p.write(body, false); err != nil {
			return 0, err
		}
		p.complete = true
		return len(raw), nil
	}
	if !bytes.HasPrefix(raw, []byte("data: ")) || !bytes.HasSuffix(raw, []byte("\n\n")) {
		return p.reject()
	}
	data := bytes.TrimSpace(raw[len("data: ") : len(raw)-2])
	terminal := bytes.Equal(data, []byte("[DONE]"))
	var events []map[string]any
	if terminal {
		if p.state.finish == "" {
			return p.reject()
		}
		events = p.state.terminalEvents()
	} else {
		root, valid := systemOneMap(data)
		if valid && hasUpstreamError(root) {
			// This is the connector's sanitized terminal error envelope. Never
			// publish a completion event after a failure or incomplete stream.
			frame := httperr.SSEErrorFrame(httperr.New(httperr.CodeUpstream, "upstream stream failed"))
			defer clear(frame)
			if err := p.write(frame, true); err != nil {
				return 0, err
			}
			p.failed = true
			return len(raw), nil
		}
		first := p.state.id == ""
		text, refusal, reasoning := p.state.text, p.state.refusal, p.state.reasoning
		if err := p.state.consume(data, true); err != nil {
			return p.reject()
		}
		if first {
			events = append(events, p.state.event("response.created", map[string]any{"response": p.state.response(false)}), p.state.event("response.in_progress", map[string]any{"response": p.state.response(false)}))
		}
		events = append(events, p.state.reasoningEvents(reasoning)...)
		events = append(events, p.state.textEvents(text, refusal)...)
	}
	for _, event := range events {
		body, err := json.Marshal(event)
		if err != nil {
			clear(body)
			return p.reject()
		}
		frame := append([]byte("event: "+event["type"].(string)+"\ndata: "), body...)
		frame = append(frame, '\n', '\n')
		clear(body)
		err = p.write(frame, false)
		clear(frame)
		if errors.Is(err, ErrPayloadTooLarge) {
			return p.reject()
		}
		if err != nil {
			return 0, err
		}
	}
	if terminal {
		p.complete = true
	}
	return len(raw), nil
}

func (p *ResponsesChatProjection) write(body []byte, terminalError bool) error {
	if err := p.ctx.Err(); err != nil {
		return err
	}
	reserve := int64(0)
	if p.request.Stream && !terminalError {
		reserve = httperr.MaxSSEErrorFrameBytes
	}
	if int64(len(body)) > p.limit-p.written-reserve {
		return ErrPayloadTooLarge
	}
	if !p.committed {
		if err := contract.MarkResponseStarted(p.writer); err != nil {
			p.rejected = true
			return err
		}
	}
	n, err := p.writer.Write(body)
	p.written += int64(n)
	p.committed = p.committed || n > 0
	if err == nil && n != len(body) {
		err = io.ErrShortWrite
	}
	if err != nil {
		p.failed = true
	}
	return err
}

func (p *ResponsesChatProjection) reject() (int, error) {
	p.rejected = true
	if p.committed && p.request.Stream {
		frame := httperr.SSEErrorFrame(httperr.New(httperr.CodeInternal, "response conversion failed"))
		defer clear(frame)
		if err := p.write(frame, true); err != nil {
			return 0, err
		}
	}
	return 0, errInvalidUpstreamResponse
}
