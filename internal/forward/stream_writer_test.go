package forward

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/backend"
	"github.com/waiting-here/NonbiriAPI/internal/claim"
	"github.com/waiting-here/NonbiriAPI/internal/connector"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/debug"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
)

func successfulStream(kind contract.Type) string {
	switch kind {
	case contract.TypeOpenAICompatible:
		return "data: " + `{"id":"c1","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"content":"Hello"},"finish_reason":null}]}` + "\n\n" +
			"data: " + `{"id":"c1","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}` + "\n\ndata: [DONE]\n\n"
	case contract.TypeAnthropicCompatible:
		return "event: message_start\ndata: " + `{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"m","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":1,"output_tokens":0}}}` + "\n\n" +
			"event: content_block_start\ndata: " + `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}` + "\n\n" +
			"event: content_block_delta\ndata: " + `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}` + "\n\n" +
			"event: content_block_stop\ndata: " + `{"type":"content_block_stop","index":0}` + "\n\n" +
			"event: message_delta\ndata: " + `{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":1}}` + "\n\n" +
			"event: message_stop\ndata: " + `{"type":"message_stop"}` + "\n\n"
	default:
		return "data: " + `{"type":"stream-start","warnings":[]}` + "\n\n" +
			"data: " + `{"type":"text-start","id":"text1"}` + "\n\n" +
			"data: " + `{"type":"text-delta","id":"text1","delta":"Hello"}` + "\n\n" +
			"data: " + `{"type":"text-end","id":"text1"}` + "\n\n" +
			"data: " + `{"type":"finish","finishReason":{"unified":"stop","raw":"stop"},"usage":{"inputTokens":{"total":1,"noCache":1,"cacheRead":0,"cacheWrite":0},"outputTokens":{"total":1,"text":1,"reasoning":0}}}` + "\n\n"
	}
}

type delayedStreamBody struct {
	ctx    context.Context
	w      *heartbeatRecorder
	reader io.Reader
	waited bool
}

func (r *delayedStreamBody) Read(body []byte) (int, error) {
	if !r.waited {
		r.waited = true
		if err := waitHeartbeat(r.ctx, r.w); err != nil {
			return 0, err
		}
	}
	return r.reader.Read(body)
}
func (*delayedStreamBody) Close() error { return nil }

func TestHeartbeatCoversContentWaitAndSuccessfulRetry(t *testing.T) {
	for _, kind := range []contract.Type{contract.TypeOpenAICompatible, contract.TypeAnthropicCompatible, contract.TypeAISDKGatewayV3} {
		for _, flatten := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/flatten=%t", kind, flatten), func(t *testing.T) {
				f := newServiceFixture(t, nil)
				w := &heartbeatRecorder{ResponseRecorder: httptest.NewRecorder(), heartbeat: make(chan struct{}, 1)}
				calls, marks := 0, 0
				f.service.claims = embeddingMarkRail{fakeClaimRail: f.claims, mark: func() error { marks++; return nil }}
				b := &delayedStreamBackend{do: func(r *http.Request) (*http.Response, error) {
					calls++
					if calls == 1 {
						if err := waitHeartbeat(r.Context(), w); err != nil {
							return nil, err
						}
						return streamResponse(503, `{"error":{"message":"try later"}}`), nil
					}
					response := streamResponse(200, "")
					response.Body = &delayedStreamBody{ctx: r.Context(), w: w, reader: strings.NewReader(successfulStream(kind))}
					return response, nil
				}}
				useRealStreamConnector(t, f, kind, b, 1, flatten)
				f.charges.charge = 7
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				r := streamTestRequest(t)
				f.service.Chat(ctx, w, 1, r.chat, nil, "application/json", "en")
				if calls != 2 || marks != 1 || strings.Count(w.Body.String(), ": heartbeat") < 2 || !strings.Contains(w.Body.String(), "Hello") || strings.Count(w.Body.String(), "[DONE]") != 1 || strings.Contains(w.Body.String(), `"error":`) {
					t.Fatalf("calls=%d marks=%d body=%q outcomes=%+v", calls, marks, w.Body.String(), f.claims.outcomes)
				}
				if f.claims.outcomes[0].ResponseStarted || !f.claims.outcomes[1].ResponseStarted || !f.claims.outcomes[1].ProtocolSuccess || f.claims.requestResults[0].ActualChargeMilli != 7 {
					t.Fatalf("outcomes=%+v", f.claims.outcomes)
				}
			})
		}
	}
}

type heartbeatRecorder struct {
	*httptest.ResponseRecorder
	heartbeat   chan struct{}
	fail        bool
	onHeartbeat func()
}

func (w *heartbeatRecorder) Write(body []byte) (int, error) {
	if strings.HasPrefix(string(body), ": heartbeat") {
		select {
		case w.heartbeat <- struct{}{}:
		default:
		}
		if w.onHeartbeat != nil {
			w.onHeartbeat()
		}
		if w.fail {
			return 0, io.ErrClosedPipe
		}
	}
	return w.ResponseRecorder.Write(body)
}

type delayedStreamBackend struct {
	do func(*http.Request) (*http.Response, error)
}

func (b *delayedStreamBackend) Open(string) (backend.EndpointClient, error) { return b, nil }
func (*delayedStreamBackend) BaseURL() string                               { return "https://upstream.example/v1" }
func (*delayedStreamBackend) MaxResponseBytes() int64                       { return 32 << 20 }
func (b *delayedStreamBackend) Do(r *http.Request) (*http.Response, error)  { return b.do(r) }

func waitHeartbeat(ctx context.Context, w *heartbeatRecorder) error {
	select {
	case <-w.heartbeat:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func streamResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body)), ContentLength: -1}
}

func useRealStreamConnector(t *testing.T, f *serviceFixture, kind contract.Type, b backend.Backend, retries int, flatten bool) {
	t.Helper()
	actual, err := f.service.registry.NewConnector(kind, connector.Dependencies{Backend: b})
	if err != nil {
		t.Fatal(err)
	}
	f.service.connectors[kind] = actual
	f.service.heartbeatInterval = 5 * time.Millisecond
	f.charity.preflight.FlattenToolCalls = flatten
	f.charity.snapshot.CharityPreflight.FlattenToolCalls = flatten
	candidate := f.charity.snapshot.Candidates[0]
	candidate.ConnectorType = kind
	f.charity.snapshot.Candidates = nil
	for range retries + 1 {
		f.charity.snapshot.Candidates = append(f.charity.snapshot.Candidates, candidate)
		f.addDispatch(candidate)
		candidate.EndpointKeyID++
		candidate.DonationKeyID++
	}
}

func streamTestRequest(t *testing.T) *validatedRequest {
	t.Helper()
	return chatRequest(decodeChatForTest(t, `{"model":"[公益]care/model","stream":true,"max_tokens":8,"messages":[{"role":"user","content":"hello"}]}`))
}

func TestHeartbeatOnlyFailurePreservesRetryAndAccounting(t *testing.T) {
	for _, kind := range []contract.Type{contract.TypeOpenAICompatible, contract.TypeAnthropicCompatible, contract.TypeAISDKGatewayV3} {
		for _, flatten := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/flatten=%t", kind, flatten), func(t *testing.T) {
				f := newServiceFixture(t, nil)
				w := &heartbeatRecorder{ResponseRecorder: httptest.NewRecorder(), heartbeat: make(chan struct{}, 1)}
				calls := 0
				b := &delayedStreamBackend{do: func(r *http.Request) (*http.Response, error) {
					calls++
					if err := waitHeartbeat(r.Context(), w); err != nil {
						return nil, err
					}
					return streamResponse(503, `{"error":{"message":"try later"}}`), nil
				}}
				useRealStreamConnector(t, f, kind, b, 1, flatten)
				f.charges.charge = 0
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				r := streamTestRequest(t)
				f.service.Chat(ctx, w, 1, r.chat, nil, "application/json", "en")
				if ctx.Err() != nil {
					t.Fatal("heartbeat or retry did not complete")
				}
				body := w.Body.String()
				if calls != 2 || w.Code != 200 || !strings.Contains(body, ": heartbeat") || strings.Count(body, `"error":`) != 1 || strings.Contains(body, "[DONE]") || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/event-stream") {
					t.Fatalf("calls=%d status=%d body=%q", calls, w.Code, body)
				}
				for _, out := range f.claims.outcomes {
					if out.ResponseStarted || out.ProtocolSuccess || out.StreakDisposition != contract.StreakUpstreamFailure {
						t.Fatalf("heartbeat established success: %+v", out)
					}
				}
				terminal := f.claims.requestResults[0]
				if terminal.ActualChargeMilli != 0 || terminal.Disposition != claim.AccountingCommit {
					t.Fatalf("terminal=%+v", terminal)
				}
			})
		}
	}
}

func TestHeartbeatCancellationAndWriteFailureStopHeaderWait(t *testing.T) {
	for _, kind := range []contract.Type{contract.TypeOpenAICompatible, contract.TypeAnthropicCompatible, contract.TypeAISDKGatewayV3} {
		for _, writeFailure := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/writeFailure=%t", kind, writeFailure), func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				f := newServiceFixture(t, nil)
				w := &heartbeatRecorder{ResponseRecorder: httptest.NewRecorder(), heartbeat: make(chan struct{}, 1), fail: writeFailure}
				if !writeFailure {
					w.onHeartbeat = cancel
				}
				stopped := false
				b := &delayedStreamBackend{do: func(r *http.Request) (*http.Response, error) {
					<-r.Context().Done()
					stopped = true
					return nil, r.Context().Err()
				}}
				useRealStreamConnector(t, f, kind, b, 0, false)
				f.charges.charge = 0
				r := streamTestRequest(t)
				f.service.Chat(ctx, w, 1, r.chat, nil, "application/json", "en")
				if !stopped || len(w.heartbeat) == 0 || strings.Contains(w.Body.String(), "[DONE]") || strings.Contains(w.Body.String(), `"error"`) {
					t.Fatalf("upstream stopped=%t body=%q", stopped, w.Body.String())
				}
				out := f.claims.outcomes[0]
				if out.ResponseStarted || out.ProtocolSuccess || out.StreakDisposition != contract.StreakNeutral || f.claims.requestResults[0].ActualChargeMilli != 0 {
					t.Fatalf("outcome=%+v", out)
				}
			})
		}
	}
}

func TestStreamWriterSerializesFramesAndPreservesCheckpoint(t *testing.T) {
	w := &heartbeatRecorder{ResponseRecorder: httptest.NewRecorder(), heartbeat: make(chan struct{}, 1)}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	stream, _ := newStreamWriter(ctx, w, 5*time.Millisecond)
	defer stream.cancel()
	marks := 0
	sink, checkpoint := checkpointResponseWriter(stream, func() error { marks++; return nil })
	// Mutating this header map during heartbeat output exercises the boundary
	// under race without sharing the connector map with the heartbeat goroutine.
	mutating := true
	for mutating {
		sink.Header().Set("Content-Type", "text/event-stream")
		select {
		case <-w.heartbeat:
			mutating = false
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		default:
		}
	}
	if checkpoint.started || marks != 0 {
		t.Fatal("heartbeat passed through billing checkpoint")
	}
	if _, err := sink.Write([]byte("data: {\"choices\":[]}\n\n")); err != nil {
		t.Fatal(err)
	}
	frame := httperr.SSEErrorFrame(httperr.New(httperr.CodeUpstream, "upstream stream failed"))
	if _, err := sink.Write(frame); err != nil {
		t.Fatal(err)
	}
	stream.stopHeartbeat()
	if !stream.finishFailure(platformFailure(httperr.CodeInternal, "internal error")) {
		t.Fatal("transport was not started")
	}
	if !checkpoint.started || marks != 1 || strings.Count(w.Body.String(), `"error":`) != 1 || strings.Contains(w.Body.String(), "[DONE]") {
		t.Fatalf("marks=%d body=%q", marks, w.Body.String())
	}
}

func TestStreamingDeadlineAfterHeartbeatFinishesWithSSEError(t *testing.T) {
	f := newServiceFixture(t, nil)
	f.service.timeout = 30 * time.Millisecond
	w := &heartbeatRecorder{ResponseRecorder: httptest.NewRecorder(), heartbeat: make(chan struct{}, 1)}
	b := &delayedStreamBackend{do: func(r *http.Request) (*http.Response, error) { <-r.Context().Done(); return nil, r.Context().Err() }}
	useRealStreamConnector(t, f, contract.TypeAISDKGatewayV3, b, 0, false)
	f.charges.charge = 0
	r := streamTestRequest(t)
	f.service.Chat(context.Background(), w, 1, r.chat, nil, "application/json", "en")
	if w.Code != 200 || strings.Count(w.Body.String(), `"error":`) != 1 || strings.Contains(w.Body.String(), "[DONE]") || !strings.Contains(w.Body.String(), ": heartbeat") {
		t.Fatalf("deadline response=%d %q", w.Code, w.Body.String())
	}
	if f.claims.outcomes[0].ResponseStarted {
		t.Fatal("deadline charged heartbeat")
	}
}

func TestHeartbeatDoesNotChangeNonstreamOrDebugLiveWire(t *testing.T) {
	for _, live := range []bool{false, true} {
		t.Run(fmt.Sprintf("debugLive=%t", live), func(t *testing.T) {
			var capture DebugCapture
			if live {
				hub, err := debug.NewHub(activeIdentityVerifier{})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = hub.Close() })
				metadata, _, err := hub.Start(1, "browser-binding")
				if err != nil {
					t.Fatal(err)
				}
				if _, err := hub.ChangeMode(1, metadata.Revision, debug.ModeLive, true); err != nil {
					t.Fatal(err)
				}
				capture = hub
			}
			f := newServiceFixture(t, capture)
			f.service.heartbeatInterval = time.Millisecond
			f.addDispatch(f.personal.snapshot.Candidates[0])
			f.openAI.attempt = func(ctx context.Context, input connector.AttemptInput) contract.AttemptResult {
				timer := time.NewTimer(10 * time.Millisecond)
				defer timer.Stop()
				select {
				case <-timer.C:
				case <-ctx.Done():
					return contract.AttemptResult{Failure: contract.FailureCanceled}
				}
				_, _ = input.Sink.Write([]byte(`{"id":"successful-response"}`))
				return contract.AttemptResult{Success: true, Committed: true, UpstreamStatus: 200, ClientStatus: 200}
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			request := decodeChatForTest(t, fmt.Sprintf(`{"model":"provider/model","stream":%t,"messages":[{"role":"user","content":"hi"}]}`, live))
			w := httptest.NewRecorder()
			f.service.Chat(ctx, w, 1, request, nil, "application/json", "en")
			if strings.Contains(w.Body.String(), ": heartbeat") || strings.HasPrefix(w.Header().Get("Content-Type"), "text/event-stream") {
				t.Fatalf("unexpected heartbeat: %q", w.Body.String())
			}
			if live {
				if w.Code != 422 || !strings.Contains(w.Body.String(), httperr.CodeDebugLiveResultCaptured) || strings.Contains(w.Body.String(), "successful-response") {
					t.Fatalf("debug body=%q", w.Body.String())
				}
			} else if w.Code != 200 || w.Body.String() != `{"id":"successful-response"}` {
				t.Fatalf("nonstream body=%q", w.Body.String())
			}
		})
	}
}
