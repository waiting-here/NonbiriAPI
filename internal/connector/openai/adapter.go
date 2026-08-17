package openai

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strings"
	"time"

	"nonbiriapi/internal/egress"
	"nonbiriapi/internal/endpoint"
)

const (
	DefaultMaxJSONResponseBytes int64 = 8 << 20
	DefaultMaxStreamBytes       int64 = 32 << 20
	DefaultMaxSSELineBytes            = 256 << 10
	DefaultMaxSSEEventBytes           = 1 << 20
	DefaultStreamWriteTimeout         = 15 * time.Second
)

// FailureKind is a controlled classification. It never contains an upstream
// body, endpoint URL, request value, credential, or transport error string.
type FailureKind uint8

const (
	FailureNone FailureKind = iota
	FailureUpstream
	FailureInternal
	FailureCanceled
	FailureSink
)

// Credential carries short-lived copies consumed by Attempt. Bearer is the
// decrypted upstream key. Ciphertext is present only so the final response
// guard can fail closed if a hostile test/upstream reflects it. Both slices are
// cleared before the network call begins; Attempt also clears them on every
// earlier error path.
type Credential struct {
	bearer     []byte
	ciphertext []byte
}

// NewCredential transfers short-lived credential byte slices to the adapter.
// Attempt clears both slices on every path; callers keep only cleanup aliases.
func NewCredential(bearer, ciphertext []byte) Credential {
	return Credential{bearer: bearer, ciphertext: ciphertext}
}

func (c *Credential) clear() {
	if c == nil {
		return
	}
	clear(c.bearer)
	clear(c.ciphertext)
	c.bearer = nil
	c.ciphertext = nil
}

func (Credential) String() string   { return "[redacted upstream credential]" }
func (Credential) GoString() string { return "[redacted upstream credential]" }
func (Credential) LogValue() slog.Value {
	return slog.StringValue("[redacted upstream credential]")
}

// Target is a revalidated, caller-owned dispatch target. Connector selection
// happens outside the adapter; this type contains only OpenAI protocol inputs.
type Target struct {
	baseURL       string
	upstreamModel string
	credential    Credential
}

// NewTarget transfers one revalidated endpoint/model/credential tuple to a
// single attempt. Fields stay private so generic JSON encoding cannot expose
// endpoint or credential material.
func NewTarget(baseURL, upstreamModel string, credential Credential) Target {
	return Target{baseURL: baseURL, upstreamModel: upstreamModel, credential: credential}
}

func (Target) String() string   { return "[redacted upstream target]" }
func (Target) GoString() string { return "[redacted upstream target]" }
func (Target) LogValue() slog.Value {
	return slog.StringValue("[redacted upstream target]")
}

// AttemptResult contains only bounded metadata safe for forwarding hooks.
// Diagnostic is locally generated and never includes raw upstream text.
type AttemptResult struct {
	Success        bool
	Committed      bool
	SinkFailed     bool
	Failure        FailureKind
	Diagnostic     string
	UpstreamStatus int
	ClientStatus   int
	Usage          Usage
}

// AdapterConfig sets finite protocol bounds. A zero value selects the default;
// configured values may tighten but never widen the shared egress body limit.
type AdapterConfig struct {
	Stack *egress.Stack

	MaxJSONResponseBytes int64
	MaxStreamBytes       int64
	MaxSSELineBytes      int
	MaxSSEEventBytes     int
	StreamWriteTimeout   time.Duration
}

// Adapter translates one OpenAI-compatible chat request/response attempt. It
// never owns routing, retries, persistence, or caller authentication.
type Adapter struct {
	stack                *egress.Stack
	maxJSONResponseBytes int64
	maxStreamBytes       int64
	maxSSELineBytes      int
	maxSSEEventBytes     int
	streamWriteTimeout   time.Duration
}

func NewAdapter(config AdapterConfig) (*Adapter, error) {
	if config.Stack == nil {
		return nil, errors.New("openai connector: egress stack is required")
	}
	if config.MaxJSONResponseBytes < 0 || config.MaxStreamBytes < 0 || config.MaxSSELineBytes < 0 || config.MaxSSEEventBytes < 0 || config.StreamWriteTimeout < 0 {
		return nil, errors.New("openai connector: limits must not be negative")
	}
	if config.MaxJSONResponseBytes == 0 {
		config.MaxJSONResponseBytes = DefaultMaxJSONResponseBytes
	}
	if config.MaxStreamBytes == 0 {
		config.MaxStreamBytes = DefaultMaxStreamBytes
	}
	if config.MaxSSELineBytes == 0 {
		config.MaxSSELineBytes = DefaultMaxSSELineBytes
	}
	if config.MaxSSEEventBytes == 0 {
		config.MaxSSEEventBytes = DefaultMaxSSEEventBytes
	}
	if config.StreamWriteTimeout == 0 {
		config.StreamWriteTimeout = DefaultStreamWriteTimeout
	}
	sharedMax := config.Stack.MaxResponseBytes()
	if config.MaxJSONResponseBytes > sharedMax {
		config.MaxJSONResponseBytes = sharedMax
	}
	if config.MaxStreamBytes > sharedMax {
		config.MaxStreamBytes = sharedMax
	}
	if config.MaxJSONResponseBytes < 1 || config.MaxStreamBytes < 1 || config.MaxSSELineBytes < 1 || config.MaxSSEEventBytes < 1 {
		return nil, errors.New("openai connector: response limits must be positive")
	}
	if int64(config.MaxSSELineBytes) > config.MaxStreamBytes {
		config.MaxSSELineBytes = int(config.MaxStreamBytes)
	}
	if int64(config.MaxSSEEventBytes) > config.MaxStreamBytes {
		config.MaxSSEEventBytes = int(config.MaxStreamBytes)
	}
	return &Adapter{
		stack:                config.Stack,
		maxJSONResponseBytes: config.MaxJSONResponseBytes,
		maxStreamBytes:       config.MaxStreamBytes,
		maxSSELineBytes:      config.MaxSSELineBytes,
		maxSSEEventBytes:     config.MaxSSEEventBytes,
		streamWriteTimeout:   config.StreamWriteTimeout,
	}, nil
}

// ConnectorType ties the adapter to the single authoritative endpoint
// registry. Unknown persisted types never fall back to this adapter.
func (*Adapter) ConnectorType() endpoint.ConnectorType {
	return endpoint.ConnectorOpenAICompatible
}

// Attempt performs exactly one upstream attempt. All pre-commit failures are
// returned without writing a body, leaving the retry boundary available to a
// later routing layer. Once an SSE frame writes any byte, failures are emitted
// only as a bounded local SSE error frame and never as a second HTTP envelope.
func (a *Adapter) Attempt(ctx context.Context, writer http.ResponseWriter, target Target, request *ChatRequest, safetyIdentifier string) AttemptResult {
	result := AttemptResult{Failure: FailureInternal, Diagnostic: "forwarding attempt unavailable"}
	defer target.credential.clear()
	if a == nil || a.stack == nil || ctx == nil || writer == nil || request == nil {
		return result
	}
	if request.Stream {
		if _, ok := writer.(http.Flusher); !ok {
			result.Diagnostic = "streaming response is not supported"
			return result
		}
	}

	client, err := a.stack.NewClient(target.baseURL)
	if err != nil {
		return upstreamFailure("upstream endpoint was refused", 0)
	}
	body, err := request.marshalUpstream(target.upstreamModel, safetyIdentifier)
	if err != nil {
		return result
	}
	defer clear(body)

	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, chatCompletionsURL(client.BaseURL()), bytes.NewReader(body))
	if err != nil {
		return result
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	if request.Stream {
		httpRequest.Header.Set("Accept", "text/event-stream")
	} else {
		httpRequest.Header.Set("Accept", "application/json")
	}
	if len(target.credential.bearer) == 0 {
		return result
	}

	guard := newSensitiveGuard(target.credential.bearer, target.credential.ciphertext)
	defer guard.Clear()
	httpRequest.Header.Set("Authorization", "Bearer "+string(target.credential.bearer))
	// The guard has irreversibly fingerprinted both values and the request
	// header now owns the transient wire copy. Clear caller-owned byte slices
	// before DNS/dial/response latency can extend their lifetime.
	target.credential.clear()

	response, err := client.Do(httpRequest)
	httpRequest.Header.Del("Authorization")
	clear(body)
	if response != nil && response.Request != nil {
		response.Request.Header.Del("Authorization")
	}
	if err != nil {
		if ctx.Err() != nil {
			return canceledFailure()
		}
		return upstreamFailure(classifyTransportFailure(err), 0)
	}
	defer func() { _ = response.Body.Close() }()
	if ctx.Err() != nil {
		return canceledFailure()
	}

	if request.Stream {
		if response.StatusCode != http.StatusOK {
			return upstreamFailure(statusDiagnostic(response.StatusCode), response.StatusCode)
		}
		if !validResponseMediaType(response, "text/event-stream") {
			return upstreamFailure("upstream stream content type was invalid", response.StatusCode)
		}
		return a.stream(ctx, writer, response, guard)
	}

	if response.StatusCode < http.StatusOK || response.StatusCode > 299 {
		return upstreamFailure(statusDiagnostic(response.StatusCode), response.StatusCode)
	}
	if !validResponseMediaType(response, "application/json") {
		return upstreamFailure("upstream response content type was invalid", response.StatusCode)
	}
	return a.nonStream(ctx, writer, response, guard)
}

func (a *Adapter) nonStream(ctx context.Context, writer http.ResponseWriter, response *http.Response, guard *sensitiveGuard) AttemptResult {
	if response.ContentLength > a.maxJSONResponseBytes {
		return upstreamFailure("upstream response exceeded its limit", response.StatusCode)
	}
	body, err := readResponseBody(response.Body, a.maxJSONResponseBytes)
	if err != nil {
		if ctx.Err() != nil {
			return canceledFailure()
		}
		return upstreamFailure(classifyReadFailure(err), response.StatusCode)
	}
	defer clear(body)
	if !validProtocolBytes(body) || guard.Contains(body) {
		return upstreamFailure("upstream response was rejected", response.StatusCode)
	}
	usage, err := validateCompletion(body)
	if err != nil {
		return upstreamFailure("upstream response was invalid", response.StatusCode)
	}
	if ctx.Err() != nil {
		return canceledFailure()
	}

	setJSONResponseHeaders(writer.Header())
	n, writeErr := writer.Write(body)
	committed := n > 0
	if writeErr != nil || n != len(body) {
		return sinkFailure(committed, usage)
	}
	return AttemptResult{
		Success:        true,
		Committed:      committed,
		Failure:        FailureNone,
		UpstreamStatus: response.StatusCode,
		ClientStatus:   http.StatusOK,
		Usage:          usage,
	}
}

func (a *Adapter) stream(ctx context.Context, writer http.ResponseWriter, response *http.Response, guard *sensitiveGuard) AttemptResult {
	// Do not drive parser delivery from response.Request.Context: the egress
	// managed body cancels that internal context on ordinary EOF to release its
	// permit. A caller-derived parser context lets already-parsed events drain
	// after clean EOF while the managed body still enforces timeout/close.
	streamCtx, cancelStream := context.WithCancel(ctx)
	defer cancelStream()
	events, errs := egress.StreamSSE(streamCtx, response.Body, egress.SSEOptions{
		MaxBytes:      a.maxStreamBytes,
		MaxLineBytes:  a.maxSSELineBytes,
		MaxEventBytes: a.maxSSEEventBytes,
		ReadBuffer:    min(a.maxSSELineBytes, 64<<10),
		EventBuffer:   1,
	})

	controller := http.NewResponseController(writer)
	defer func() { _ = controller.SetWriteDeadline(time.Time{}) }()
	committed := false
	seenChunk := false
	usage := Usage{}

	for {
		event, ok, nextErr := nextSSEEvent(streamCtx, events, errs)
		if nextErr != nil || !ok {
			if ctx.Err() != nil {
				result := canceledFailure()
				result.Committed = committed
				result.ClientStatus = committedStatus(committed)
				result.Usage = usage
				return result
			}
			return a.streamProtocolFailure(writer, controller, committed, usage, "upstream stream ended before completion")
		}
		if event.Event != "message" {
			return a.streamProtocolFailure(writer, controller, committed, usage, "upstream stream event type was invalid")
		}
		if event.Data == "[DONE]" {
			if !seenChunk {
				return a.streamProtocolFailure(writer, controller, committed, usage, "upstream stream ended without a chunk")
			}
			frame := []byte("data: [DONE]\n\n")
			if guard.Contains(frame) {
				return a.streamProtocolFailure(writer, controller, committed, usage, "upstream stream was rejected")
			}
			wrote, writeErr := a.writeStreamFrame(writer, controller, frame)
			committed = committed || wrote
			if writeErr != nil {
				return sinkFailureWithCommit(committed, usage)
			}
			return AttemptResult{
				Success:        true,
				Committed:      committed,
				Failure:        FailureNone,
				UpstreamStatus: response.StatusCode,
				ClientStatus:   http.StatusOK,
				Usage:          usage,
			}
		}
		if len(event.Data) == 0 || len(event.Data) > a.maxSSEEventBytes || !validProtocolBytes([]byte(event.Data)) {
			return a.streamProtocolFailure(writer, controller, committed, usage, "upstream stream chunk exceeded protocol bounds")
		}
		compact, chunkUsage, err := validateChunk([]byte(event.Data))
		if err != nil {
			return a.streamProtocolFailure(writer, controller, committed, usage, "upstream stream chunk was invalid")
		}
		frame := make([]byte, 0, len(compact)+8)
		frame = append(frame, "data: "...)
		frame = append(frame, compact...)
		frame = append(frame, '\n', '\n')
		clear(compact)
		if guard.Contains(frame) {
			clear(frame)
			return a.streamProtocolFailure(writer, controller, committed, usage, "upstream stream was rejected")
		}
		wrote, writeErr := a.writeStreamFrame(writer, controller, frame)
		clear(frame)
		committed = committed || wrote
		if writeErr != nil {
			return sinkFailureWithCommit(committed, usage)
		}
		seenChunk = true
		if chunkUsage.Present {
			usage = chunkUsage
		}
	}
}

func (a *Adapter) streamProtocolFailure(writer http.ResponseWriter, controller *http.ResponseController, committed bool, usage Usage, diagnostic string) AttemptResult {
	if !committed {
		result := upstreamFailure(diagnostic, http.StatusOK)
		result.Usage = usage
		return result
	}
	frame := []byte(`data: {"error":{"code":"upstream","message":"upstream stream failed","type":"upstream_error"}}` + "\n\n")
	_, err := a.writeStreamFrame(writer, controller, frame)
	if err != nil {
		return sinkFailureWithCommit(true, usage)
	}
	return AttemptResult{
		Committed:      true,
		Failure:        FailureUpstream,
		Diagnostic:     diagnostic,
		UpstreamStatus: http.StatusOK,
		ClientStatus:   http.StatusOK,
		Usage:          usage,
	}
}

func (a *Adapter) writeStreamFrame(writer http.ResponseWriter, controller *http.ResponseController, frame []byte) (bool, error) {
	setSSEHeaders(writer.Header())
	if a.streamWriteTimeout > 0 {
		if err := controller.SetWriteDeadline(time.Now().Add(a.streamWriteTimeout)); err != nil && !errors.Is(err, http.ErrNotSupported) {
			return false, err
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

func readResponseBody(reader io.Reader, limit int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		clear(body)
		return nil, err
	}
	if int64(len(body)) > limit {
		clear(body)
		return nil, &egress.ResponseTooLargeError{Limit: limit}
	}
	return body, nil
}

func validResponseMediaType(response *http.Response, expected string) bool {
	if response == nil || response.Header.Get("Content-Encoding") != "" {
		return false
	}
	values := response.Header.Values("Content-Type")
	if len(values) != 1 {
		return false
	}
	mediaType, params, err := mime.ParseMediaType(values[0])
	if err != nil || !strings.EqualFold(mediaType, expected) {
		return false
	}
	for key, value := range params {
		if !strings.EqualFold(key, "charset") || !strings.EqualFold(value, "utf-8") {
			return false
		}
	}
	return true
}

func chatCompletionsURL(baseURL string) string {
	return strings.TrimSuffix(baseURL, "/") + "/v1/chat/completions"
}

func setJSONResponseHeaders(header http.Header) {
	clearUnsafeResponseHeaders(header)
	header.Set("Content-Type", "application/json; charset=utf-8")
	header.Set("Cache-Control", "no-store")
}

func setSSEHeaders(header http.Header) {
	clearUnsafeResponseHeaders(header)
	header.Set("Content-Type", "text/event-stream")
	header.Set("Cache-Control", "no-store")
	header.Set("X-Accel-Buffering", "no")
}

func clearUnsafeResponseHeaders(header http.Header) {
	for _, name := range []string{
		"Connection", "Content-Encoding", "Content-Length", "Keep-Alive",
		"Location", "Proxy-Authenticate", "Proxy-Authorization", "Set-Cookie",
		"TE", "Trailer", "Transfer-Encoding", "Upgrade",
	} {
		header.Del(name)
	}
}

func upstreamFailure(diagnostic string, status int) AttemptResult {
	return AttemptResult{
		Failure:        FailureUpstream,
		Diagnostic:     diagnostic,
		UpstreamStatus: status,
	}
}

func canceledFailure() AttemptResult {
	return AttemptResult{Failure: FailureCanceled, Diagnostic: "request canceled"}
}

func sinkFailure(committed bool, usage Usage) AttemptResult {
	return sinkFailureWithCommit(committed, usage)
}

func sinkFailureWithCommit(committed bool, usage Usage) AttemptResult {
	return AttemptResult{
		Committed:    committed,
		SinkFailed:   true,
		Failure:      FailureSink,
		Diagnostic:   "client response write failed",
		ClientStatus: committedStatus(committed),
		Usage:        usage,
	}
}

func committedStatus(committed bool) int {
	if committed {
		return http.StatusOK
	}
	return 0
}

func statusDiagnostic(status int) string {
	if status < 100 || status > 999 {
		return "upstream returned an invalid status"
	}
	return fmt.Sprintf("upstream returned HTTP %d", status)
}

func classifyTransportFailure(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "upstream request timed out"
	case errors.Is(err, egress.ErrRedirectBlocked):
		return "upstream redirect was refused"
	default:
		return "upstream request failed"
	}
}

func classifyReadFailure(err error) string {
	var tooLarge *egress.ResponseTooLargeError
	if errors.As(err, &tooLarge) {
		return "upstream response exceeded its limit"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "upstream response timed out"
	}
	return "upstream response was truncated"
}
