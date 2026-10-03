package forward

import (
	"context"
	"fmt"
	"github.com/waiting-here/NonbiriAPI/internal/claim"
	"github.com/waiting-here/NonbiriAPI/internal/connector"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/transportpolicy"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func forceTransport(f *serviceFixture, rule transportpolicy.Rule) {
	f.charity.preflight.TransportRule = rule
	f.charity.snapshot.TransportRule = rule
}

type canceledTransportBody struct {
	ctx    context.Context
	first  *strings.Reader
	closed chan struct{}
	once   sync.Once
}

func (b *canceledTransportBody) Read(p []byte) (int, error) {
	if b.first.Len() > 0 {
		return b.first.Read(p)
	}
	<-b.ctx.Done()
	return 0, b.ctx.Err()
}
func (b *canceledTransportBody) Close() error { b.once.Do(func() { close(b.closed) }); return nil }

func TestBufferedTransportCancellationKeepsUpstreamUsage(t *testing.T) {
	for _, known := range []bool{false, true} {
		t.Run(fmt.Sprintf("known_usage=%t", known), func(t *testing.T) {
			f := newServiceFixture(t, nil)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			marks := 0
			f.service.claims = embeddingMarkRail{fakeClaimRail: f.claims, mark: func() error { marks++; cancel(); return nil }}
			usage := ""
			if known {
				usage = `,"usage":{"prompt_tokens":20,"completion_tokens":3,"total_tokens":23}`
			}
			chunk := `data: {"id":"c1","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"content":"generated"},"finish_reason":null}]` + usage + "}\n\n"
			calls := 0
			closed := make(chan struct{})
			b := &delayedStreamBackend{do: func(r *http.Request) (*http.Response, error) {
				calls++
				response := streamResponse(200, "")
				response.Body = &canceledTransportBody{ctx: r.Context(), first: strings.NewReader(chunk), closed: closed}
				return response, nil
			}}
			useRealStreamConnector(t, f, contract.TypeOpenAICompatible, b, 1, false)
			forceTransport(f, transportpolicy.ForceStream)
			w := httptest.NewRecorder()
			request := decodeChatForTest(t, `{"model":"[公益]care/model","messages":[{"role":"user","content":"hello"}]}`)
			f.service.Chat(ctx, w, 1, request, nil, "application/json", "en")
			select {
			case <-closed:
			default:
				t.Fatal("upstream body not closed")
			}
			if calls != 1 || marks != 1 || w.Body.Len() != 0 || len(f.claims.outcomes) != 1 {
				t.Fatalf("calls=%d marks=%d body=%q", calls, marks, w.Body.String())
			}
			out := f.claims.outcomes[0]
			if !out.ResponseStarted || out.ProtocolSuccess || out.Usage.Present != known {
				t.Fatalf("outcome=%+v", out)
			}
			if known && (out.Usage.UncachedInputTokens != 20 || out.Usage.OutputTokens != 3) {
				t.Fatalf("usage=%+v", out.Usage)
			}
			terminal := f.claims.requestResults[0]
			if terminal.Caller.Class != claim.ResultCancelled || terminal.Disposition != claim.AccountingCommit || terminal.ActualChargeMilli != 10 || f.charges.calls != 1 {
				t.Fatalf("terminal=%+v charge calls=%d", terminal, f.charges.calls)
			}
		})
	}
}
func TestBufferedTransportLateDisconnectKeepsFinalUsage(t *testing.T) {
	for _, cancelBeforeDelivery := range []bool{false, true} {
		t.Run(fmt.Sprintf("cancel_before_delivery=%t", cancelBeforeDelivery), func(t *testing.T) {
			f := newServiceFixture(t, nil)
			forceTransport(f, transportpolicy.ForceStream)
			f.addDispatch(f.charity.snapshot.Candidates[0])
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			f.openAI.attempt = func(_ context.Context, in connector.AttemptInput) contract.AttemptResult {
				if !in.Ingress.Stream {
					t.Fatal("upstream not streaming")
				}
				if _, err := in.Sink.Write([]byte(successfulStream(contract.TypeOpenAICompatible))); err != nil {
					t.Fatal(err)
				}
				if cancelBeforeDelivery {
					cancel()
				}
				return contract.ProtocolSucceeded(contract.AttemptResult{Success: true, Committed: true, UpstreamStatus: 200, ClientStatus: 200, Usage: contract.Usage{Present: true, UncachedInputTokens: 20, OutputTokens: 3}})
			}
			w := &failedBillingSink{header: make(http.Header)}
			f.service.Chat(ctx, w, 1, decodeChatForTest(t, `{"model":"[公益]care/model","messages":[]}`), nil, "application/json", "en")
			out := f.claims.outcomes[0]
			if !out.ResponseStarted || !out.ProtocolSuccess || out.Usage.OutputTokens != 3 || f.claims.requestResults[0].ActualChargeMilli != 10 {
				t.Fatalf("outcome=%+v terminal=%+v", out, f.claims.requestResults[0])
			}
		})
	}
}

func TestForcedNonstreamHeartbeatRetainsRetryAndFrozenPolicy(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprintf("final_failure=%t", fail), func(t *testing.T) {
			f := newServiceFixture(t, nil)
			w := &heartbeatRecorder{ResponseRecorder: httptest.NewRecorder(), heartbeat: make(chan struct{}, 1)}
			calls, marks := 0, 0
			f.service.claims = embeddingMarkRail{fakeClaimRail: f.claims, mark: func() error { marks++; return nil }}
			b := &delayedStreamBackend{do: func(r *http.Request) (*http.Response, error) {
				calls++
				body, _ := io.ReadAll(r.Body)
				if !strings.Contains(string(body), `"stream":false`) {
					t.Fatalf("physical body=%s", body)
				}
				f.charity.preflight.TransportRule = transportpolicy.ForceStream
				f.charity.snapshot.TransportRule = transportpolicy.ForceStream
				if err := waitHeartbeat(r.Context(), w); err != nil {
					return nil, err
				}
				if calls == 1 || fail {
					return streamResponse(503, `{"error":{"message":"try later"}}`), nil
				}
				response := streamResponse(200, transportJSON(contract.TypeOpenAICompatible))
				response.Header.Set("Content-Type", "application/json")
				return response, nil
			}}
			useRealStreamConnector(t, f, contract.TypeOpenAICompatible, b, 1, false)
			forceTransport(f, transportpolicy.ForceNonStream)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			f.service.Chat(ctx, w, 1, streamTestRequest(t).chat, nil, "application/json", "en")
			body := w.Body.String()
			if calls != 2 || !strings.Contains(body, ": heartbeat") || f.charity.policyCalls != 1 {
				t.Fatalf("calls=%d policies=%d body=%s", calls, f.charity.policyCalls, body)
			}
			if fail {
				if marks != 0 || strings.Count(body, `"error":`) != 1 || strings.Contains(body, "[DONE]") {
					t.Fatal(body)
				}
			} else if marks != 1 || strings.Count(body, "[DONE]") != 1 || !strings.Contains(body, "Hello") {
				t.Fatal(body)
			}
		})
	}
}
func TestForcedStreamFailureKeepsJSONStatusAndNoPartialResult(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(fmt.Sprintf("partial=%t", partial), func(t *testing.T) {
			f := newServiceFixture(t, nil)
			b := &delayedStreamBackend{do: func(*http.Request) (*http.Response, error) {
				if !partial {
					return streamResponse(503, `{"error":{"message":"unavailable"}}`), nil
				}
				stream := successfulStream(contract.TypeOpenAICompatible)
				return streamResponse(200, strings.Split(stream, "data: [DONE]")[0]), nil
			}}
			useRealStreamConnector(t, f, contract.TypeOpenAICompatible, b, 0, false)
			forceTransport(f, transportpolicy.ForceStream)
			w := httptest.NewRecorder()
			f.service.Chat(context.Background(), w, 1, decodeChatForTest(t, `{"model":"[公益]care/model","messages":[]}`), nil, "application/json", "en")
			if w.Code < 400 || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") || strings.Contains(w.Body.String(), "Hello") || strings.Contains(w.Body.String(), "data:") {
				t.Fatalf("status=%d body=%s", w.Code, w.Body)
			}
			if f.claims.outcomes[0].ResponseStarted != partial {
				t.Fatalf("outcome=%+v", f.claims.outcomes[0])
			}
		})
	}
}
func TestCharityTransportIngressSnapshotSurvivesEdit(t *testing.T) {
	f := newServiceFixture(t, nil)
	forceTransport(f, transportpolicy.ForceStream)
	raw := []byte(`{"model":"[公益]care/model","messages":[]}`)
	request, filtered, _, err := f.service.decodeIngress(context.Background(), 1, raw, contract.OperationChatCompletions)
	if err != nil {
		t.Fatal(err)
	}
	defer request.Clear()
	defer clear(filtered)
	forceTransport(f, transportpolicy.ForceNonStream)
	f.charity.preflight.Revision++
	f.charity.snapshot.Revision++
	f.addDispatch(f.charity.snapshot.Candidates[0])
	f.openAI.attempt = func(_ context.Context, in connector.AttemptInput) contract.AttemptResult {
		if !in.Ingress.Stream {
			t.Fatal("early policy changed")
		}
		_, _ = in.Sink.Write([]byte(successfulStream(contract.TypeOpenAICompatible)))
		return contract.ProtocolSucceeded(contract.AttemptResult{Success: true, Committed: true, UpstreamStatus: 200, ClientStatus: 200})
	}
	w := httptest.NewRecorder()
	f.service.execute(context.Background(), w, 1, request, filtered, "application/json", "en", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"object":"chat.completion"`) {
		t.Fatal(w.Body.String())
	}
}
