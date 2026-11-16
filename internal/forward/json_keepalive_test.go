package forward

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/claim"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/transportpolicy"
)

type jsonHeartbeatRecorder struct {
	*httptest.ResponseRecorder
	heartbeat chan struct{}
	fail      bool
}

func (w *jsonHeartbeatRecorder) Write(body []byte) (int, error) {
	if string(body) == "\n" {
		select {
		case w.heartbeat <- struct{}{}:
		default:
		}
		if w.fail {
			return 0, io.ErrClosedPipe
		}
	}
	return w.ResponseRecorder.Write(body)
}

func awaitJSONHeartbeat(ctx context.Context, w *jsonHeartbeatRecorder) error {
	select {
	case <-w.heartbeat:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestJSONKeepaliveFlushesBeforeFinalJSONOverHTTP(t *testing.T) {
	finish := make(chan struct{})
	done := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(done)
		writer, ctx := newJSONKeepaliveWriter(r.Context(), w, 5*time.Millisecond)
		defer writer.cancel()
		writer.armHeartbeat()
		select {
		case <-finish:
		case <-ctx.Done():
		}
		_, _ = writer.Write([]byte(`{"answer":"done"}`))
		writer.stopHeartbeat()
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
	response, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 || response.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("status=%d headers=%v", response.StatusCode, response.Header)
	}
	var prefix [2]byte
	if _, err := io.ReadFull(response.Body, prefix[:]); err != nil || string(prefix[:]) != "\n\n" {
		t.Fatalf("periodic whitespace=%q err=%v", prefix, err)
	}
	close(finish)
	var value map[string]string
	if err := json.NewDecoder(response.Body).Decode(&value); err != nil || value["answer"] != "done" {
		t.Fatalf("final JSON=%v err=%v", value, err)
	}
	<-done
}

func TestJSONKeepaliveEarlyAndLateFailures(t *testing.T) {
	for _, tc := range []struct {
		language, message string
	}{
		{"en", "at capacity"},
		{"zh", "请求过于频繁，请稍后重试。"},
	} {
		for _, late := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/late=%v", tc.language, late), func(t *testing.T) {
				w := &jsonHeartbeatRecorder{ResponseRecorder: httptest.NewRecorder(), heartbeat: make(chan struct{}, 1)}
				ctx, cancel := context.WithTimeout(t.Context(), time.Second)
				defer cancel()
				interval := time.Second
				if late {
					interval = 5 * time.Millisecond
				}
				writer, _ := newJSONKeepaliveWriter(ctx, httperr.WithLanguage(w, tc.language), interval)
				defer writer.cancel()
				writer.armHeartbeat()
				want := http.StatusTooManyRequests
				if late {
					if err := awaitJSONHeartbeat(ctx, w); err != nil {
						t.Fatal(err)
					}
					want = http.StatusOK
				}
				writer.stopHeartbeat()
				writer.finishFailure(platformFailure(httperr.CodeRateLimited, "at capacity"))
				if w.Code != want || !json.Valid(w.Body.Bytes()) || !strings.Contains(w.Body.String(), `"error"`) {
					t.Fatalf("status=%d body=%q", w.Code, w.Body.String())
				}
				if !strings.Contains(w.Body.String(), tc.message) {
					t.Fatalf("language=%s body=%q", tc.language, w.Body.String())
				}
			})
		}
	}
}

func TestJSONKeepaliveRetryUsesUpstreamFacts(t *testing.T) {
	for _, finalSuccess := range []bool{false, true} {
		t.Run(fmt.Sprint(finalSuccess), func(t *testing.T) {
			f := newServiceFixture(t, nil)
			w := &jsonHeartbeatRecorder{ResponseRecorder: httptest.NewRecorder(), heartbeat: make(chan struct{}, 1)}
			calls, snapshots := 0, 0
			var marks atomic.Int32
			f.service.jsonKeepalive = func() time.Duration { snapshots++; return 5 * time.Millisecond }
			f.service.claims = embeddingMarkRail{fakeClaimRail: f.claims, mark: func() error { marks.Add(1); return nil }}
			b := &delayedStreamBackend{do: func(r *http.Request) (*http.Response, error) {
				calls++
				if err := awaitJSONHeartbeat(r.Context(), w); err != nil {
					return nil, err
				}
				if marks.Load() != 0 {
					t.Error("whitespace marked a billable response before upstream output")
				}
				status, body := 503, `{"error":{"message":"try later"}}`
				if calls == 2 && finalSuccess {
					status, body = 200, transportJSON(contract.TypeOpenAICompatible)
				}
				rsp := streamResponse(status, body)
				rsp.Header.Set("Content-Type", "application/json")
				return rsp, nil
			}}
			useRealStreamConnector(t, f, contract.TypeOpenAICompatible, b, 1, false)
			f.charges.charge = 0
			if finalSuccess {
				f.charges.charge = 7
			}
			request := decodeChatForTest(t, `{"model":"[公益]care/model","messages":[{"role":"user","content":"hello"}]}`)
			defer request.Clear()
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			f.service.Chat(ctx, w, 1, request, nil, "application/json", "en")
			if calls != 2 || snapshots != 1 || w.Code != 200 || !json.Valid(w.Body.Bytes()) || len(f.claims.outcomes) != 2 {
				t.Fatalf("calls=%d snapshots=%d status=%d body=%q outcomes=%+v", calls, snapshots, w.Code, w.Body.String(), f.claims.outcomes)
			}
			first, last := f.claims.outcomes[0], f.claims.outcomes[1]
			if first.UpstreamStatus != 503 || first.ResponseStarted || first.ProtocolSuccess || first.StreakDisposition != contract.StreakUpstreamFailure {
				t.Fatalf("heartbeat changed failed upstream outcome: %+v", first)
			}
			if finalSuccess {
				if marks.Load() != 1 || !last.ResponseStarted || !last.ProtocolSuccess || f.claims.requestResults[0].ActualChargeMilli != 7 {
					t.Fatalf("success marks=%d outcome=%+v settlement=%+v", marks.Load(), last, f.claims.requestResults)
				}
			} else if marks.Load() != 0 || last.ResponseStarted || last.StreakDisposition != contract.StreakUpstreamFailure || f.claims.requestResults[0].ActualChargeMilli != 0 || !strings.Contains(w.Body.String(), `"error"`) {
				t.Fatalf("failure marks=%d outcome=%+v body=%q", marks.Load(), last, w.Body.String())
			}
		})
	}
}

type waitingJSONClaimRail struct{ *fakeClaimRail }

func (r waitingJSONClaimRail) Claim(ctx context.Context, _ claim.ClaimInput) (claim.Handle, error) {
	<-ctx.Done()
	return claim.Handle{}, ctx.Err()
}

func TestJSONKeepaliveAcceptedWaitReleasesWithoutUpstreamOutcome(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		t.Run(fmt.Sprint(canceled), func(t *testing.T) {
			f := newServiceFixture(t, nil)
			f.service.claims = waitingJSONClaimRail{f.claims}
			f.service.jsonKeepalive = func() time.Duration { return 2 * time.Millisecond }
			f.service.timeout = 30 * time.Millisecond
			w := &jsonHeartbeatRecorder{ResponseRecorder: httptest.NewRecorder(), heartbeat: make(chan struct{}, 1)}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan struct{})
			request := decodeChatForTest(t, `{"model":"[公益]care/model","messages":[]}`)
			defer request.Clear()
			go func() {
				defer close(done)
				f.service.Chat(ctx, w, 1, request, nil, "application/json", "en")
			}()
			wait, stop := context.WithTimeout(t.Context(), time.Second)
			defer stop()
			if err := awaitJSONHeartbeat(wait, w); err != nil {
				t.Fatal(err)
			}
			if canceled {
				cancel()
			}
			select {
			case <-done:
			case <-wait.Done():
				t.Fatal("accepted wait did not stop")
			}
			if f.claims.takeCall != 0 || len(f.claims.outcomes) != 0 || f.charges.calls != 0 || len(f.claims.requestResults) != 1 || f.claims.requestResults[0].Disposition != claim.AccountingRelease {
				t.Fatalf("undispatched request affected upstream/charge: claims=%+v charges=%d", f.claims, f.charges.calls)
			}
			if !canceled && (!json.Valid(w.Body.Bytes()) || !strings.Contains(w.Body.String(), `"error"`)) {
				t.Fatalf("queue timeout body=%q", w.Body.String())
			}
		})
	}
}

func TestJSONKeepaliveWriteFailureCancels(t *testing.T) {
	w := &jsonHeartbeatRecorder{ResponseRecorder: httptest.NewRecorder(), heartbeat: make(chan struct{}, 1), fail: true}
	writer, ctx := newJSONKeepaliveWriter(t.Context(), w, time.Millisecond)
	defer writer.cancel()
	writer.armHeartbeat()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("failed heartbeat did not cancel upstream context")
	}
	writer.stopHeartbeat()
	writer.finishFailure(platformFailure(httperr.CodeInternal, "failed"))
	if w.Body.Len() != 0 {
		t.Fatalf("failed downstream received terminal data: %q", w.Body.String())
	}
}

func TestJSONKeepaliveDisabledRetainsUpstreamErrorStatus(t *testing.T) {
	f := newServiceFixture(t, nil)
	f.service.jsonKeepalive = func() time.Duration { return 0 }
	b := &delayedStreamBackend{do: func(*http.Request) (*http.Response, error) {
		return streamResponse(503, `{"error":{"message":"busy"}}`), nil
	}}
	useRealStreamConnector(t, f, contract.TypeOpenAICompatible, b, 0, false)
	w := httptest.NewRecorder()
	r := decodeChatForTest(t, `{"model":"[公益]care/model","messages":[]}`)
	defer r.Clear()
	f.service.Chat(t.Context(), w, 1, r, nil, "application/json", "en")
	if w.Code == 200 || !json.Valid(w.Body.Bytes()) || strings.HasPrefix(w.Body.String(), "\n") {
		t.Fatalf("disabled status=%d body=%q", w.Code, w.Body.String())
	}
}

func TestJSONKeepalivePreservesRealUpstreamAcceptanceAndConversion(t *testing.T) {
	for _, converted := range []bool{false, true} {
		t.Run(fmt.Sprint(converted), func(t *testing.T) {
			f := newServiceFixture(t, nil)
			f.service.jsonKeepalive = func() time.Duration { return 2 * time.Millisecond }
			w := &jsonHeartbeatRecorder{ResponseRecorder: httptest.NewRecorder(), heartbeat: make(chan struct{}, 1)}
			calls := 0
			b := &delayedStreamBackend{do: func(r *http.Request) (*http.Response, error) {
				calls++
				if err := awaitJSONHeartbeat(r.Context(), w); err != nil {
					return nil, err
				}
				if converted {
					return streamResponse(200, "data: {\"id\":\"chat-result\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"private-model\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"done\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"), nil
				}
				rsp := streamResponse(200, `{"choices":`)
				rsp.Header.Set("Content-Type", "application/json")
				return rsp, nil
			}}
			useRealStreamConnector(t, f, contract.TypeOpenAICompatible, b, 1, false)
			if converted {
				forceTransport(f, transportpolicy.ForceStream)
			}
			r := decodeChatForTest(t, `{"model":"[公益]care/model","messages":[]}`)
			defer r.Clear()
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			f.service.Chat(ctx, w, 1, r, nil, "application/json", "en")
			if calls != 1 || len(f.claims.outcomes) != 1 || w.Code != 200 || !json.Valid(w.Body.Bytes()) {
				t.Fatalf("calls=%d outcomes=%+v status=%d body=%q", calls, f.claims.outcomes, w.Code, w.Body.String())
			}
			outcome := f.claims.outcomes[0]
			if outcome.UpstreamStatus != 200 || outcome.StreakDisposition != contract.StreakSuccess || outcome.ProtocolSuccess != converted || outcome.ResponseStarted != converted {
				t.Fatalf("upstream acceptance/conversion changed: %+v", outcome)
			}
		})
	}
}

func TestJSONKeepaliveWriteFailureStopsUpstreamWithoutStreakFailure(t *testing.T) {
	f := newServiceFixture(t, nil)
	f.charges.charge = 0
	f.service.jsonKeepalive = func() time.Duration { return 2 * time.Millisecond }
	w := &jsonHeartbeatRecorder{ResponseRecorder: httptest.NewRecorder(), heartbeat: make(chan struct{}, 1), fail: true}
	stopped := false
	b := &delayedStreamBackend{do: func(r *http.Request) (*http.Response, error) {
		<-r.Context().Done()
		stopped = true
		return nil, r.Context().Err()
	}}
	useRealStreamConnector(t, f, contract.TypeOpenAICompatible, b, 0, false)
	r := decodeChatForTest(t, `{"model":"[公益]care/model","messages":[]}`)
	defer r.Clear()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	f.service.Chat(ctx, w, 1, r, nil, "application/json", "en")
	if !stopped || len(f.claims.outcomes) != 1 || f.claims.outcomes[0].StreakDisposition != contract.StreakNeutral || f.claims.outcomes[0].ResponseStarted || len(f.claims.requestResults) != 1 || f.claims.requestResults[0].ActualChargeMilli != 0 {
		t.Fatalf("write failure outcome=%+v settlement=%+v stopped=%t", f.claims.outcomes, f.claims.requestResults, stopped)
	}
}

func TestJSONKeepaliveExcludesNonJSONAndStreamingOperations(t *testing.T) {
	for _, operation := range []contract.Operation{contract.OperationAudioSpeech, contract.OperationAudioTranscriptions, contract.OperationAudioTranslations, "unknown"} {
		if supportsJSONKeepalive(&validatedRequest{operation: operation}) {
			t.Fatalf("non-JSON operation %s received whitespace", operation)
		}
	}
	r := streamTestRequest(t)
	defer r.Clear()
	if supportsJSONKeepalive(r) {
		t.Fatal("SSE request received JSON whitespace")
	}
}

func TestJSONKeepaliveTotalDeadlineReturnsTerminalJSON(t *testing.T) {
	f := newServiceFixture(t, nil)
	f.service.jsonKeepalive = func() time.Duration { return 2 * time.Millisecond }
	f.service.timeout = 30 * time.Millisecond
	w := &jsonHeartbeatRecorder{ResponseRecorder: httptest.NewRecorder(), heartbeat: make(chan struct{}, 1)}
	b := &delayedStreamBackend{do: func(r *http.Request) (*http.Response, error) {
		<-r.Context().Done()
		return nil, r.Context().Err()
	}}
	useRealStreamConnector(t, f, contract.TypeOpenAICompatible, b, 0, false)
	r := decodeChatForTest(t, `{"model":"[公益]care/model","messages":[]}`)
	defer r.Clear()
	f.service.Chat(t.Context(), w, 1, r, nil, "application/json", "en")
	if w.Code != 200 || !json.Valid(w.Body.Bytes()) || !strings.Contains(w.Body.String(), `"error"`) || f.claims.requestResults[0].Caller.Class == claim.ResultCancelled {
		t.Fatalf("timeout status=%d body=%q settlement=%+v", w.Code, w.Body.String(), f.claims.requestResults)
	}
	if len(f.claims.outcomes) != 1 || f.claims.outcomes[0].FailureOrigin != contract.OriginTimeout || f.claims.outcomes[0].ResponseStarted {
		t.Fatalf("timeout outcome=%+v", f.claims.outcomes)
	}
}
