package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/backend"
	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/egress"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
)

const (
	DefaultMaxJSONResponseBytes int64 = 8 << 20
	DefaultMaxStreamBytes       int64 = 32 << 20
	DefaultMaxSSELineBytes            = 256 << 10
	DefaultMaxSSEEventBytes           = 1 << 20
	DefaultStreamWriteTimeout         = 15 * time.Second
)

// Compatibility aliases keep the protocol package's existing tests and
// callers source-compatible while forwarding and accounting consume the
// connector-neutral contract directly.
type FailureKind = connectorcontract.FailureKind

const (
	FailureNone     = connectorcontract.FailureNone
	FailureUpstream = connectorcontract.FailureUpstream
	FailureInternal = connectorcontract.FailureInternal
	FailureCanceled = connectorcontract.FailureCanceled
	FailureSink     = connectorcontract.FailureSink
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

type AttemptResult = connectorcontract.AttemptResult

// AdapterConfig sets finite protocol bounds. A zero value selects the default;
// configured values may tighten but never widen the shared egress body limit.
type AdapterConfig struct {
	// Backend is the shared outbound execution boundary. Production wiring
	// passes the single LocalBackend over the process-wide egress Stack.
	Backend backend.Backend

	MaxJSONResponseBytes int64
	MaxStreamBytes       int64
	MaxSSELineBytes      int
	MaxSSEEventBytes     int
	StreamWriteTimeout   time.Duration
}

// Adapter translates one OpenAI-compatible chat request/response attempt. It
// never owns routing, retries, persistence, or caller authentication.
type Adapter struct {
	backend              backend.Backend
	maxJSONResponseBytes int64
	maxStreamBytes       int64
	maxSSELineBytes      int
	maxSSEEventBytes     int
	streamWriteTimeout   time.Duration
}

func NewAdapter(config AdapterConfig) (*Adapter, error) {
	if backend.IsNil(config.Backend) {
		return nil, errors.New("openai connector: egress backend is required")
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
	if config.MaxJSONResponseBytes > DefaultMaxJSONResponseBytes {
		config.MaxJSONResponseBytes = DefaultMaxJSONResponseBytes
	}
	if config.MaxStreamBytes > DefaultMaxStreamBytes {
		config.MaxStreamBytes = DefaultMaxStreamBytes
	}
	if config.MaxSSELineBytes > DefaultMaxSSELineBytes {
		config.MaxSSELineBytes = DefaultMaxSSELineBytes
	}
	if config.MaxSSEEventBytes > DefaultMaxSSEEventBytes {
		config.MaxSSEEventBytes = DefaultMaxSSEEventBytes
	}
	if config.StreamWriteTimeout > DefaultStreamWriteTimeout {
		config.StreamWriteTimeout = DefaultStreamWriteTimeout
	}
	sharedMax := config.Backend.MaxResponseBytes()
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
		backend:              config.Backend,
		maxJSONResponseBytes: config.MaxJSONResponseBytes,
		maxStreamBytes:       config.MaxStreamBytes,
		maxSSELineBytes:      config.MaxSSELineBytes,
		maxSSEEventBytes:     config.MaxSSEEventBytes,
		streamWriteTimeout:   config.StreamWriteTimeout,
	}, nil
}

// ConnectorType ties the adapter to the single authoritative endpoint
// registry. Unknown persisted types never fall back to this adapter.
func (*Adapter) ConnectorType() connectorcontract.Type {
	return connectorcontract.TypeOpenAICompatible
}

// Attempt performs exactly one upstream attempt. All pre-commit failures are
// returned without writing a body, leaving the retry boundary available to a
// later routing layer. Once an SSE frame writes any byte, failures are emitted
// only as a bounded local SSE error frame and never as a second HTTP envelope.
func (a *Adapter) Attempt(ctx context.Context, writer http.ResponseWriter, target Target, request *ChatRequest, safetyIdentifier string) AttemptResult {
	return a.AttemptWithPolicy(ctx, writer, target, request, connectorcontract.AttemptPolicy{SafetyIdentifier: safetyIdentifier})
}

// AttemptWithPolicy applies the immutable per-attempt OpenAI strategy
// projection. Store policy affects only the final serialized request; tool
// flattening affects only validated OpenAI responses after the shared guards.
func (a *Adapter) AttemptWithPolicy(ctx context.Context, writer http.ResponseWriter, target Target, request *ChatRequest, policy connectorcontract.AttemptPolicy) AttemptResult {
	result := AttemptResult{Failure: FailureInternal, Diagnostic: "forwarding attempt unavailable"}
	defer target.credential.clear()
	if a == nil || a.backend == nil || ctx == nil || writer == nil || request == nil {
		return result
	}
	if request.Stream {
		if _, ok := writer.(http.Flusher); !ok {
			result.Diagnostic = "streaming response is not supported"
			return result
		}
	}

	client, err := a.backend.Open(target.baseURL)
	if err != nil {
		return upstreamFailure("upstream endpoint was refused", 0)
	}
	body, err := request.marshalUpstreamWithPolicy(target.upstreamModel, policy.SafetyIdentifier, policy)
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

	guard := newResponseGuard(target.credential.bearer, target.credential.ciphertext)
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
		if policy.FlattenToolCalls {
			return a.flattenStream(ctx, writer, response, guard)
		}
		return a.stream(ctx, writer, response, guard)
	}

	if response.StatusCode < http.StatusOK || response.StatusCode > 299 {
		return upstreamFailure(statusDiagnostic(response.StatusCode), response.StatusCode)
	}
	if !validResponseMediaType(response, "application/json") {
		return upstreamFailure("upstream response content type was invalid", response.StatusCode)
	}
	return a.nonStreamWithPolicy(ctx, writer, response, guard, policy)
}

func (a *Adapter) nonStream(ctx context.Context, writer http.ResponseWriter, response *http.Response, guard *responseGuard) AttemptResult {
	return a.nonStreamWithPolicy(ctx, writer, response, guard, connectorcontract.AttemptPolicy{})
}

func (a *Adapter) nonStreamWithPolicy(ctx context.Context, writer http.ResponseWriter, response *http.Response, guard *responseGuard, policy connectorcontract.AttemptPolicy) AttemptResult {
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
	if !validProtocolBytes(body) {
		return upstreamFailure("upstream response was invalid", response.StatusCode)
	}
	usage, err := validateCompletion(body)
	if err != nil {
		return upstreamFailure("upstream response was invalid", response.StatusCode)
	}
	if guard.ContainsJSON(body, body) {
		return upstreamFailure("upstream response was rejected", response.StatusCode)
	}
	if policy.FlattenToolCalls {
		flattened, ferr := flattenCompletion(body)
		if ferr != nil {
			return upstreamFailure("upstream response was invalid", response.StatusCode)
		}
		clear(body)
		body = flattened
		defer clear(body)
		if int64(len(body)) > a.maxJSONResponseBytes {
			return upstreamFailure("upstream response exceeded its limit", response.StatusCode)
		}
		if guard.ContainsJSON(body, body) {
			return upstreamFailure("upstream response was rejected", response.StatusCode)
		}
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

func (a *Adapter) stream(ctx context.Context, writer http.ResponseWriter, response *http.Response, guard *responseGuard) AttemptResult {
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
	// usageCaptured records that a valid usage chunk was seen; usagePoisoned
	// records that the upstream contradicted itself (a malformed usage object
	// or two different usage values). Once poisoned, the whole request's
	// usage stays unknown: no token value is ever fabricated from
	// contradictory data, and a later valid-looking chunk cannot resurrect it.
	usageCaptured := false
	usagePoisoned := false

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
			if guard.ContainsBytes(frame) {
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
		compact, chunkUsage, chunkUsageMalformed, err := validateChunk([]byte(event.Data))
		if err != nil {
			return a.streamProtocolFailure(writer, controller, committed, usage, "upstream stream chunk was invalid")
		}
		frame := make([]byte, 0, len(compact)+8)
		frame = append(frame, "data: "...)
		frame = append(frame, compact...)
		frame = append(frame, '\n', '\n')
		rejected := guard.ContainsJSON(frame, compact)
		clear(compact)
		if rejected {
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
		switch {
		case chunkUsageMalformed:
			usagePoisoned = true
			usage = Usage{}
		case chunkUsage.Present && !usagePoisoned:
			if usageCaptured {
				if chunkUsage != usage {
					// Contradictory repeated usage: degrade to unknown.
					usagePoisoned = true
					usage = Usage{}
				}
			} else {
				usage = chunkUsage
				usageCaptured = true
			}
		}
	}
}

// flattenStream forwards ordinary content as soon as it has passed the
// protocol and leak guards, while retaining only tool deltas until a valid
// terminal marker proves that the whole call set is complete.
func (a *Adapter) flattenStream(ctx context.Context, writer http.ResponseWriter, response *http.Response, guard *responseGuard) AttemptResult {
	streamCtx, cancelStream := context.WithCancel(ctx)
	defer cancelStream()
	events, errs := egress.StreamSSE(streamCtx, response.Body, egress.SSEOptions{
		MaxBytes: a.maxStreamBytes, MaxLineBytes: a.maxSSELineBytes, MaxEventBytes: a.maxSSEEventBytes,
		ReadBuffer: min(a.maxSSELineBytes, 64<<10), EventBuffer: 1,
	})
	usageFrames := make([][]byte, 0, 2)
	defer func() {
		for _, frame := range usageFrames {
			clear(frame)
		}
	}()
	states := make(map[int]*streamChoiceState)
	defer clearStreamStates(states)
	var firstRoot map[string]json.RawMessage
	defer func() {
		for _, value := range firstRoot {
			clear(value)
		}
	}()
	var usage Usage
	usageCaptured, usagePoisoned := false, false
	seenChunk := false
	hasToolsSeen := false
	controller := http.NewResponseController(writer)
	defer func() { _ = controller.SetWriteDeadline(time.Time{}) }()
	committed := false
	failure := func(diagnostic string) AttemptResult {
		return a.streamProtocolFailure(writer, controller, committed, usage, diagnostic)
	}
	writeFrame := func(frame, jsonData []byte) (bool, error) {
		if (jsonData != nil && guard.ContainsJSON(frame, jsonData)) || (jsonData == nil && guard.ContainsBytes(frame)) {
			return false, errFlattenStreamRejected
		}
		wrote, writeErr := a.writeStreamFrame(writer, controller, frame)
		committed = committed || wrote
		return wrote, writeErr
	}
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
			return failure("upstream stream ended before completion")
		}
		if event.Event != "message" {
			return failure("upstream stream event type was invalid")
		}
		if event.Data == "[DONE]" {
			if !seenChunk {
				return failure("upstream stream ended without a chunk")
			}
			if hasToolsSeen {
				if firstRoot == nil {
					return failure("upstream stream ended before completion")
				}
				body, err := streamCompletionBodyAfter(firstRoot, states)
				if err != nil {
					return failure("upstream stream ended before completion")
				}
				transformed, flattenErr := flattenCompletion(body)
				clear(body)
				if flattenErr != nil {
					clear(transformed)
					return failure("upstream stream was invalid")
				}
				content, finish, frameErr := completionToStreamFrames(transformed)
				clear(transformed)
				if frameErr != nil || len(content) > a.maxSSEEventBytes || len(finish) > a.maxSSEEventBytes ||
					int64(len(content)) > a.maxStreamBytes || int64(len(finish)) > a.maxStreamBytes {
					clear(content)
					clear(finish)
					return failure("upstream stream chunk exceeded protocol bounds")
				}
				contentFrame := append([]byte("data: "), content...)
				contentFrame = append(contentFrame, '\n', '\n')
				if _, writeErr := writeFrame(contentFrame, content); writeErr != nil {
					clear(contentFrame)
					clear(content)
					clear(finish)
					if errors.Is(writeErr, errFlattenStreamRejected) {
						return failure("upstream stream was rejected")
					}
					return sinkFailureWithCommit(committed, usage)
				}
				clear(contentFrame)
				clear(content)
				finishFrame := append([]byte("data: "), finish...)
				finishFrame = append(finishFrame, '\n', '\n')
				if _, writeErr := writeFrame(finishFrame, finish); writeErr != nil {
					clear(finishFrame)
					clear(finish)
					if errors.Is(writeErr, errFlattenStreamRejected) {
						return failure("upstream stream was rejected")
					}
					return sinkFailureWithCommit(committed, usage)
				}
				clear(finishFrame)
				clear(finish)
			}
			for _, frame := range usageFrames {
				if _, writeErr := writeFrame(frame, streamFramePayload(frame)); writeErr != nil {
					if errors.Is(writeErr, errFlattenStreamRejected) {
						return failure("upstream stream was rejected")
					}
					return sinkFailureWithCommit(committed, usage)
				}
			}
			final := []byte("data: [DONE]\n\n")
			_, writeErr := writeFrame(final, nil)
			if writeErr != nil {
				if errors.Is(writeErr, errFlattenStreamRejected) {
					return failure("upstream stream was rejected")
				}
				return sinkFailureWithCommit(committed, usage)
			}
			return AttemptResult{Success: true, Committed: committed, Failure: FailureNone, UpstreamStatus: response.StatusCode, ClientStatus: http.StatusOK, Usage: usage}
		}
		if len(event.Data) == 0 || len(event.Data) > a.maxSSEEventBytes || !validProtocolBytes([]byte(event.Data)) {
			return failure("upstream stream chunk exceeded protocol bounds")
		}
		compact, chunkUsage, chunkMalformed, err := validateChunk([]byte(event.Data))
		if err != nil {
			return failure("upstream stream chunk was invalid")
		}
		var root map[string]json.RawMessage
		if json.Unmarshal(compact, &root) != nil {
			clear(compact)
			return failure("upstream stream chunk was invalid")
		}
		var choices []json.RawMessage
		if json.Unmarshal(root["choices"], &choices) != nil {
			clear(compact)
			return failure("upstream stream chunk was invalid")
		}
		// Compatible providers normally emit usage in a terminal choices:[]
		// chunk. If one attaches a non-null usage object to a content/finish
		// chunk, retain the original usage value but move it to a bounded
		// usage-only chunk. A tool-bearing source frame cannot be forwarded as
		// is because it would expose the structured tool delta that flattening
		// promises to remove.
		if usageRaw, present := root["usage"]; present && !isJSONNull(usageRaw) && len(choices) != 0 {
			usagePayload, usageErr := isolatedUsageChunk(root, usageRaw)
			if usageErr != nil || len(usagePayload) > a.maxSSEEventBytes || int64(len(usagePayload)) > a.maxStreamBytes {
				clear(compact)
				clear(usagePayload)
				return failure("upstream stream chunk exceeded protocol bounds")
			}
			usageFrame := append([]byte("data: "), usagePayload...)
			usageFrame = append(usageFrame, '\n', '\n')
			clear(usagePayload)
			usageFrames = append(usageFrames, usageFrame)

			delete(root, "usage")
			withoutUsage, marshalErr := json.Marshal(root)
			clear(compact)
			if marshalErr != nil || len(withoutUsage) > a.maxSSEEventBytes || int64(len(withoutUsage)) > a.maxStreamBytes {
				clear(withoutUsage)
				return failure("upstream stream chunk exceeded protocol bounds")
			}
			compact = withoutUsage
		}
		frame := append([]byte("data: "), compact...)
		frame = append(frame, '\n', '\n')
		clear(compact)
		if firstRoot == nil && len(choices) > 0 {
			firstRoot = root
		}
		_, toolsSeen, err := accumulateStreamChunk([]byte(event.Data), states)
		if err != nil {
			clear(frame)
			return failure("upstream stream chunk was invalid")
		}
		if chunkMalformed {
			usagePoisoned = true
			usage = Usage{}
		} else if chunkUsage.Present && !usagePoisoned {
			if usageCaptured && chunkUsage != usage {
				usagePoisoned = true
				usage = Usage{}
			} else if !usageCaptured {
				usage = chunkUsage
				usageCaptured = true
			}
		}
		seenChunk = true
		if len(choices) == 0 {
			// Usage/empty-choice chunks are held until the terminal rewrite so
			// tool streams preserve the required finish -> usage -> DONE order.
			usageFrames = append(usageFrames, frame)
			continue
		}
		if !toolsSeen && !hasToolsSeen {
			if _, writeErr := writeFrame(frame, []byte(event.Data)); writeErr != nil {
				clear(frame)
				if errors.Is(writeErr, errFlattenStreamRejected) {
					return failure("upstream stream was rejected")
				}
				return sinkFailureWithCommit(committed, usage)
			}
			clear(frame)
			markStreamContentEmitted(states)
		} else {
			// Once a tool delta is observed, ordinary content is retained in
			// state and emitted with the generated blocks at the terminal. This
			// preserves the non-stream ordering of all content before tool tags.
			clear(frame)
		}
		hasToolsSeen = hasToolsSeen || toolsSeen
	}
}

func isolatedUsageChunk(root map[string]json.RawMessage, usage json.RawMessage) ([]byte, error) {
	if len(root) == 0 || len(usage) == 0 || isJSONNull(usage) {
		return nil, errToolFlatten
	}
	chunk := make(map[string]json.RawMessage, len(root))
	for name, value := range root {
		chunk[name] = value
	}
	chunk["choices"] = json.RawMessage(`[]`)
	chunk["usage"] = usage
	return json.Marshal(chunk)
}

func streamFramePayload(frame []byte) []byte {
	trimmed := bytes.TrimSpace(frame)
	trimmed = bytes.TrimSpace(bytes.TrimSuffix(trimmed, []byte("\n\n")))
	if bytes.HasPrefix(trimmed, []byte("data:")) {
		return bytes.TrimSpace(bytes.TrimPrefix(trimmed, []byte("data:")))
	}
	return trimmed
}

// accumulateStreamState reconstructs the buffered deltas once more to retain
// a single source of truth for the bounded stream parser. It returns whether
// any structured tool delta was observed.
func accumulateStreamState(frames [][]byte, states map[int]*streamChoiceState) (map[string]json.RawMessage, bool, error) {
	var first map[string]json.RawMessage
	hasTools := false
	for _, frame := range frames {
		data := bytes.TrimSpace(frame)
		if !bytes.HasPrefix(data, []byte("data:")) {
			return nil, false, errToolFlatten
		}
		data = bytes.TrimSpace(bytes.TrimPrefix(data, []byte("data:")))
		root, tools, err := accumulateStreamChunk(data, states)
		if err != nil {
			return nil, false, err
		}
		if first == nil {
			first = root
		}
		hasTools = hasTools || tools
	}
	return first, hasTools, nil
}

func (a *Adapter) streamProtocolFailure(writer http.ResponseWriter, controller *http.ResponseController, committed bool, usage Usage, diagnostic string) AttemptResult {
	if !committed {
		result := upstreamFailure(diagnostic, http.StatusOK)
		result.Usage = usage
		return result
	}
	// Once any byte is committed, the response status and headers are already
	// written, so the failure can only be emitted as an in-stream SSE error
	// frame — never a second HTTP envelope. The frame uses the same stable
	// {error:{code,source,message}} shape as the JSON envelope, with source
	// derived at the shared wire sink (upstream for an upstream stream
	// failure), so a client never has to infer attribution from a prefix.
	frame := httperr.SSEErrorFrame(httperr.New(httperr.CodeUpstream, "upstream stream failed"))
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

// chatCompletionsURL appends the chat-completions resource path to a canonical
// endpoint base URL. The base URL carries the provider's full API mount up to
// and including the version segment (for example https://host/v1 or
// https://host/api/v1); only the resource path is appended, so a base already
// ending in /v1 never becomes /v1/v1/chat/completions. Callers must supply the
// version segment as part of the endpoint base URL.
func chatCompletionsURL(baseURL string) string {
	return strings.TrimSuffix(baseURL, "/") + "/chat/completions"
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
