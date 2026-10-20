// Package chatwire shares bounded HTTP execution between translating Chat
// connectors. Each protocol owns its request mapping and response state machine.
package chatwire

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/backend"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
	"github.com/waiting-here/NonbiriAPI/internal/connector/tooltext"
	"github.com/waiting-here/NonbiriAPI/internal/egress"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/requestadaptation"
	"github.com/waiting-here/NonbiriAPI/internal/sessionheader"
	"github.com/waiting-here/NonbiriAPI/internal/smartbackoff"
	"github.com/waiting-here/NonbiriAPI/internal/upstreamerror"
)

const MaxJSONBytes int64 = 8 << 20
const MaxStreamBytes int64 = 32 << 20
const MaxEventBytes = 1 << 20
const MaxLineBytes = 256 << 10
const WriteTimeout = 15 * time.Second

// Decoder emits complete caller JSON values; Finish checks the vendor's
// semantic terminal and all open item state before emitting final chunks.
type Decoder interface {
	JSON([]byte) ([]byte, contract.Usage, error)
	Event(string, []byte) ([][]byte, error)
	Finish() ([][]byte, error)
	Usage() contract.Usage
	Clear()
}
type Protocol struct {
	Type              contract.Type
	Header            string
	Compile           func(*openai.ChatRequest, string, contract.AttemptPolicy) ([]byte, error)
	URL               func(string, string, bool) (string, error)
	Decoder           func(string, time.Time, bool) Decoder
	ErrorEvent        func([]byte) ([]byte, bool)
	LowercaseIdentity bool
}
type Adapter struct {
	backend  backend.Backend
	protocol Protocol
}

func New(outbound backend.Backend, protocol Protocol) (*Adapter, error) {
	if backend.IsNil(outbound) || outbound.MaxResponseBytes() < 1 || protocol.Compile == nil || protocol.URL == nil || protocol.Decoder == nil || (protocol.Header != "Authorization" && protocol.Header != "X-Goog-Api-Key") {
		return nil, errors.New("chat connector configuration unavailable")
	}
	return &Adapter{backend: outbound, protocol: protocol}, nil
}
func (a *Adapter) Attempt(ctx context.Context, w http.ResponseWriter, target contract.Target, credential *contract.ShortLivedSecret, request *openai.ChatRequest, policy contract.AttemptPolicy) (result contract.AttemptResult) {
	defer func() { result = contract.NormalizeOutcome(result) }()
	defer credential.Clear()
	result = contract.AttemptResult{Failure: contract.FailureInternal, Diagnostic: "forwarding attempt unavailable"}
	if a == nil || ctx == nil || w == nil || request == nil || target.Type() != a.protocol.Type {
		return result
	}
	if ctx.Err() != nil {
		return contract.TransportFailed(result, ctx.Err(), ctx)
	}
	if request.Stream {
		if _, ok := w.(http.Flusher); !ok {
			return result
		}
	}
	body, err := a.protocol.Compile(request, target.UpstreamModel(), policy)
	if err != nil {
		return result
	}
	defer clear(body)
	if int64(len(body)) > request.RequestBodyLimit()+(16<<10) {
		return result
	}
	client, err := a.backend.Open(target.BaseURL())
	if err != nil {
		return failure("upstream endpoint was refused")
	}
	url, err := a.protocol.URL(client.BaseURL(), target.UpstreamModel(), request.Stream)
	if err != nil {
		return result
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return result
	}
	if requestadaptation.ApplyAddedHeaders(req.Header, policy.AdditionalHeaders) != nil || sessionheader.Apply(req, policy.ManagedHeaders, policy.OnRequestSent) != nil {
		return result
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if request.Stream {
		req.Header.Set("Accept", "text/event-stream")
	}
	sent, err := requestadaptation.SentSecrets(policy.ResponseSecrets, req.Header, body)
	if err != nil {
		return result
	}
	key, ciphertext, ok := credential.Take()
	if !ok {
		return result
	}
	defer clear(key)
	defer clear(ciphertext)
	identifier := []byte(policy.SafetyIdentifier)
	defer clear(identifier)
	materials := append([][]byte{key, ciphertext, identifier}, sent...)
	if a.protocol.LowercaseIdentity {
		lower := []byte(strings.ToLower(policy.SafetyIdentifier))
		defer clear(lower)
		materials = append(materials, lower)
	}
	guard := openai.NewResponseGuard(materials...)
	defer guard.Clear()
	wireGuard := openai.NewResponseGuard(materials...)
	defer wireGuard.Clear()
	errorGuard := openai.NewResponseGuard(materials...)
	defer errorGuard.Clear()
	errorContext := upstreamerror.Context{CharityCall: smartbackoff.IsCharityCall(ctx), BaseURL: target.BaseURL(), PrivateModel: target.UpstreamModel(), ContainsSecret: errorGuard.ContainsBytes}
	if policy.HasAdaptation || len(policy.ManagedHeaders) > 0 {
		errorContext.ContainsSecret = func(value []byte) bool { return len(value) > 0 }
	}
	if policy.FlattenToolCalls && request.Stream {
		outputGuard := openai.NewResponseGuard(materials...)
		defer outputGuard.Clear()
		sink := openai.NewFlattenSink(w, min(a.backend.MaxResponseBytes(), MaxStreamBytes), MaxEventBytes, outputGuard.ContainsJSON)
		defer sink.Clear()
		w = sink
		defer func() {
			if result.Success {
				if err := sink.Complete(WriteTimeout); err != nil {
					result.Success = false
					if errors.Is(err, tooltext.ErrToolFlatten) || errors.Is(err, tooltext.ErrFlattenStreamRejected) {
						result.Failure = contract.FailureUpstream
						result.Diagnostic = "tool projection was invalid"
						result = contract.UpstreamFailed(result, contract.OriginUpstreamProtocol)
					} else {
						result.Failure = contract.FailureSink
						result.SinkFailed = true
						result.Diagnostic = "caller response failed"
					}
				}
			}
			result.Committed = sink.Committed()
		}()
	}
	auth := string(key)
	if a.protocol.Header == "Authorization" {
		auth = "Bearer " + auth
	}
	req.Header.Set(a.protocol.Header, auth)
	clear(key)
	clear(ciphertext)
	req = egress.WithResponseLimits(req, egress.ResponseLimits{JSONBytes: MaxJSONBytes, StreamBytes: MaxStreamBytes, LineBytes: MaxLineBytes, EventBytes: MaxEventBytes})
	response, err := client.Do(req)
	req.Header.Del(a.protocol.Header)
	clear(body)
	if response != nil {
		defer func() { result.UpstreamStatus = response.StatusCode }()
		if response.Request != nil {
			response.Request.Header.Del(a.protocol.Header)
		}
	}
	if response != nil && err != nil {
		if err := contract.ObserveUpstreamResponse(w, response.StatusCode, request.Stream, response.Header); err != nil {
			return result
		}
	}
	if err != nil {
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		return contract.TransportFailed(failure("upstream request failed"), err, ctx)
	}
	if response == nil || response.Body == nil {
		return failure("upstream response unavailable")
	}
	defer response.Body.Close()
	if err := contract.ObserveUpstreamResponse(w, response.StatusCode, request.Stream, response.Header); err != nil {
		return result
	}
	if response.StatusCode != 200 {
		result = contract.UpstreamFailed(failure("upstream request failed"), contract.OriginUpstreamResponse)
		result.ErrorDetail = errorContext.ReadResponse(ctx, response, min(a.backend.MaxResponseBytes(), MaxJSONBytes))
		return result
	}
	expected := "application/json"
	if request.Stream {
		expected = "text/event-stream"
	}
	if !validMedia(response, expected) {
		return failure("upstream response content type was invalid")
	}
	options, _ := request.RawField("stream_options")
	var streamOptions struct {
		IncludeUsage bool `json:"include_usage"`
	}
	_ = json.Unmarshal(options, &streamOptions)
	clear(options)
	decoder := a.protocol.Decoder(request.Model, time.Now().UTC(), streamOptions.IncludeUsage)
	defer decoder.Clear()
	if request.Stream {
		return a.stream(ctx, w, response, decoder, guard, wireGuard, errorContext)
	}
	limit := min(a.backend.MaxResponseBytes(), MaxJSONBytes)
	if response.ContentLength > limit {
		return failure("upstream response exceeded its limit")
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	defer clear(raw)
	if err != nil {
		return contract.ReadFailed(failure("upstream response read failed"), err, ctx)
	}
	if int64(len(raw)) > limit {
		return failure("upstream response exceeded its limit")
	}
	if wireGuard.ContainsJSON(raw, raw) {
		upstreamerror.CaptureEvent(ctx, response.StatusCode, response.Header.Get("Content-Type"), raw)
		return failure("upstream response was rejected")
	}
	if upstreamerror.IsEvent(raw) {
		upstreamerror.CaptureEvent(ctx, response.StatusCode, response.Header.Get("Content-Type"), raw)
		result = contract.UpstreamFailed(failure("upstream response reported an error"), contract.OriginUpstreamResponse)
		result.ErrorDetail = errorContext.Parse(raw)
		return result
	}
	out, usage, err := decoder.JSON(raw)
	if err != nil {
		upstreamerror.CaptureEvent(ctx, response.StatusCode, response.Header.Get("Content-Type"), raw)
		return failure("upstream response was invalid")
	}
	defer func() { clear(out) }()
	if policy.FlattenToolCalls {
		projected, err := tooltext.FlattenCompletion(out)
		clear(out)
		if err != nil {
			return failure("tool projection was invalid")
		}
		out = projected
	}
	if int64(len(out)) > limit || guard.ContainsJSON(out, out) {
		return failure("upstream response was rejected")
	}
	defer func() { result = contract.ProtocolSucceeded(result) }()
	if err := contract.MarkResponseStarted(w); err != nil {
		return sinkFailure(false, usage)
	}
	if ctx.Err() != nil {
		result = contract.ReadFailed(result, ctx.Err(), ctx)
		result.Usage = usage
		return result
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	n, err := w.Write(out)
	if err != nil || n != len(out) {
		return sinkFailure(n > 0, usage)
	}
	return contract.AttemptResult{Success: true, Committed: n > 0, Failure: contract.FailureNone, ClientStatus: 200, Usage: usage}
}

type responseGuard interface {
	ContainsJSON([]byte, []byte) bool
	ContainsBytes([]byte) bool
}

var errOutput = errors.New("translated stream output rejected")

func (a *Adapter) stream(ctx context.Context, w http.ResponseWriter, response *http.Response, decoder Decoder, guard, wireGuard responseGuard, errorContext upstreamerror.Context) (result contract.AttemptResult) {
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	events, errs := egress.StreamSSE(streamCtx, response.Body, egress.SSEOptions{MaxBytes: min(a.backend.MaxResponseBytes(), MaxStreamBytes), MaxLineBytes: MaxLineBytes, MaxEventBytes: MaxEventBytes, ReadBuffer: 64 << 10, EventBuffer: 1})
	controller := http.NewResponseController(w)
	defer func() { _ = controller.SetWriteDeadline(time.Time{}) }()
	committed := false
	var written int64
	write := func(payload []byte, done bool) error {
		if len(payload)+6 > MaxLineBytes || len(payload) > MaxEventBytes || written+int64(len(payload))+8+httperr.MaxSSEErrorFrameBytes > min(a.backend.MaxResponseBytes(), MaxStreamBytes) {
			return errOutput
		}
		frame := append([]byte("data: "), payload...)
		frame = append(frame, '\n', '\n')
		defer clear(frame)
		if done {
			if guard.ContainsBytes(frame) {
				return errOutput
			}
		} else if guard.ContainsJSON(frame, payload) {
			return errOutput
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if !committed {
			if err := contract.MarkResponseStarted(w); err != nil {
				return err
			}
		}
		if err := controller.SetWriteDeadline(time.Now().Add(WriteTimeout)); err != nil && !errors.Is(err, http.ErrNotSupported) {
			return err
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Accel-Buffering", "no")
		n, err := w.Write(frame)
		committed = committed || n > 0
		written += int64(n)
		if err != nil {
			return err
		}
		if n != len(frame) {
			return io.ErrShortWrite
		}
		return controller.Flush()
	}
	fail := func(err error) contract.AttemptResult {
		failed := failure("upstream stream was invalid")
		failed.Committed = committed
		failed.Usage = decoder.Usage()
		if ctx.Err() != nil {
			return contract.ReadFailed(failed, ctx.Err(), ctx)
		}
		if committed {
			frame := httperr.SSEErrorFrame(httperr.New(httperr.CodeUpstream, "upstream stream failed"))
			if written+int64(len(frame)) <= min(a.backend.MaxResponseBytes(), MaxStreamBytes) && !guard.ContainsBytes(frame) {
				_ = controller.SetWriteDeadline(time.Now().Add(WriteTimeout))
				_, _ = w.Write(frame)
				_ = controller.Flush()
			}
			clear(frame)
			failed.ClientStatus = 200
		}
		return contract.ReadFailed(failed, err, ctx)
	}
	emit := func(frames [][]byte) error {
		defer func() {
			for _, frame := range frames {
				clear(frame)
			}
		}()
		for _, frame := range frames {
			if err := write(frame, false); err != nil {
				return err
			}
		}
		return nil
	}
	writeFailure := func(err error) contract.AttemptResult {
		if errors.Is(err, errOutput) || ctx.Err() != nil {
			return fail(err)
		}
		return sinkFailure(committed, decoder.Usage())
	}
	for events != nil || errs != nil {
		select {
		case <-ctx.Done():
			return fail(ctx.Err())
		case err, ok := <-errs:
			if !ok {
				errs = nil
				continue
			}
			if err != nil && !errors.Is(err, io.EOF) {
				return fail(err)
			}
		case event, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			if event.Data == "" {
				continue
			}
			data := []byte(event.Data)
			if wireGuard.ContainsJSON(data, data) {
				upstreamerror.CaptureEvent(ctx, response.StatusCode, response.Header.Get("Content-Type"), data)
				clear(data)
				return fail(errOutput)
			}
			errorBody, errorEvent := data, upstreamerror.IsEvent(data)
			if !errorEvent && a.protocol.ErrorEvent != nil {
				errorBody, errorEvent = a.protocol.ErrorEvent(data)
			}
			if errorEvent {
				upstreamerror.CaptureEvent(ctx, response.StatusCode, response.Header.Get("Content-Type"), data)
				failed := fail(errors.New("upstream error event"))
				failed.ErrorDetail = errorContext.Parse(errorBody)
				clear(data)
				return contract.UpstreamFailed(failed, contract.OriginUpstreamResponse)
			}
			frames, err := decoder.Event(event.Event, data)
			if err != nil {
				upstreamerror.CaptureEvent(ctx, response.StatusCode, response.Header.Get("Content-Type"), data)
			}
			clear(data)
			if err != nil {
				for _, frame := range frames {
					clear(frame)
				}
				return fail(err)
			}
			if err := emit(frames); err != nil {
				return writeFailure(err)
			}
		}
	}
	frames, err := decoder.Finish()
	if err != nil {
		return fail(err)
	}
	if err := emit(frames); err != nil {
		if errors.Is(err, errOutput) {
			return writeFailure(err)
		}
		return contract.ProtocolSucceeded(writeFailure(err))
	}
	if err = write([]byte("[DONE]"), true); err != nil {
		if errors.Is(err, errOutput) {
			return writeFailure(err)
		}
		return contract.ProtocolSucceeded(writeFailure(err))
	}
	return contract.ProtocolSucceeded(contract.AttemptResult{Success: true, Committed: committed, Failure: contract.FailureNone, ClientStatus: 200, Usage: decoder.Usage()})
}

func failure(message string) contract.AttemptResult {
	return contract.AttemptResult{Failure: contract.FailureUpstream, FailureOrigin: contract.OriginUpstreamProtocol, StreakDisposition: contract.StreakUpstreamFailure, Diagnostic: message}
}
func sinkFailure(committed bool, usage contract.Usage) contract.AttemptResult {
	return contract.AttemptResult{Failure: contract.FailureSink, SinkFailed: true, Committed: committed, Diagnostic: "caller response failed", Usage: usage}
}
func validMedia(response *http.Response, expected string) bool {
	if response.Header.Get("Content-Encoding") != "" || len(response.Header.Values("Content-Type")) != 1 {
		return false
	}
	kind, params, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || !strings.EqualFold(kind, expected) {
		return false
	}
	for key, value := range params {
		if !strings.EqualFold(key, "charset") || !strings.EqualFold(value, "utf-8") {
			return false
		}
	}
	return true
}
