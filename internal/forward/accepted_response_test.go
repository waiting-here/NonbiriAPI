package forward

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/connector"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
	"github.com/waiting-here/NonbiriAPI/internal/transportpolicy"
)

type failedResponseBody struct{ err error }

func (b failedResponseBody) Read([]byte) (int, error) { return 0, b.err }
func (failedResponseBody) Close() error               { return nil }

func incompleteStream(kind contract.Type) string {
	stream := successfulStream(kind)
	switch kind {
	case contract.TypeOpenAICompatible:
		return strings.Split(stream, "data: [DONE]")[0]
	case contract.TypeAnthropicCompatible:
		return strings.Split(stream, "event: message_stop")[0]
	default:
		return strings.Split(stream, `data: {"type":"finish"`)[0]
	}
}

func TestPersonalProtocolFailureRetainsOptInRetry(t *testing.T) {
	f := newServiceFixture(t, nil)
	f.personal.preflight.SilentRetry = true
	f.personal.snapshot.SilentRetry = true
	candidate := f.personal.snapshot.Candidates[0]
	f.addDispatch(candidate)
	candidate.EndpointKeyID++
	f.personal.snapshot.Candidates = append(f.personal.snapshot.Candidates, candidate)
	f.addDispatch(candidate)
	f.openAI.attempt = func(context.Context, connector.AttemptInput) contract.AttemptResult {
		return contract.UpstreamFailed(contract.AttemptResult{Failure: contract.FailureUpstream, UpstreamStatus: 200}, contract.OriginUpstreamProtocol)
	}
	request := decodeChatForTest(t, `{"model":"provider/model","messages":[]}`)
	defer request.Clear()
	f.service.Chat(context.Background(), httptest.NewRecorder(), 1, request, nil, "application/json", "en")
	if f.openAI.calls != 2 || len(f.claims.outcomes) != 2 || f.claims.outcomes[0].StreakDisposition != contract.StreakUpstreamFailure {
		t.Fatalf("personal policy changed: calls=%d outcomes=%+v", f.openAI.calls, f.claims.outcomes)
	}
}

func TestCharityEmbeddingHTTPAcceptanceStopsRetry(t *testing.T) {
	for _, kind := range []contract.Type{contract.TypeOpenAICompatible, contract.TypeAISDKGatewayV3} {
		t.Run(string(kind), func(t *testing.T) {
			f := newServiceFixture(t, nil)
			calls := 0
			b := &delayedStreamBackend{do: func(*http.Request) (*http.Response, error) {
				calls++
				r := streamResponse(200, `{}`)
				r.Header.Set("Content-Type", "application/json")
				return r, nil
			}}
			useRealStreamConnector(t, f, kind, b, 1, false)
			w := httptest.NewRecorder()
			request, err := openai.DecodeEmbeddingRequest(strings.NewReader(`{"model":"[公益]care/model","input":"hello"}`), 0)
			if err != nil {
				t.Fatal(err)
			}
			defer request.Clear()
			f.service.Embeddings(context.Background(), w, 1, request, nil, "application/json", "en")
			if calls != 1 || len(f.claims.outcomes) != 1 || w.Code < 400 {
				t.Fatalf("calls=%d outcomes=%+v status=%d", calls, f.claims.outcomes, w.Code)
			}
			outcome := f.claims.outcomes[0]
			if outcome.StreakDisposition != contract.StreakSuccess || outcome.ProtocolSuccess || outcome.ResponseStarted {
				t.Fatalf("outcome=%+v", outcome)
			}
		})
	}
}

func TestCharityHTTPAcceptanceStopsRetryAndChargesStreamsWithoutProtocolSuccess(t *testing.T) {
	for _, kind := range []contract.Type{contract.TypeOpenAICompatible, contract.TypeAnthropicCompatible, contract.TypeAISDKGatewayV3} {
		for _, scenario := range []string{"empty stream", "malformed event", "partial output", "read timeout", "caller canceled", "checkpoint failure", "invalid JSON", "buffered partial output"} {
			t.Run(fmt.Sprintf("%s/%s", kind, scenario), func(t *testing.T) {
				f := newServiceFixture(t, nil)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				calls, marks := 0, 0
				f.service.claims = embeddingMarkRail{fakeClaimRail: f.claims, mark: func() error {
					marks++
					if scenario == "checkpoint failure" {
						return errors.New("checkpoint unavailable")
					}
					return nil
				}}
				b := &delayedStreamBackend{do: func(*http.Request) (*http.Response, error) {
					calls++
					r := streamResponse(200, "")
					switch scenario {
					case "malformed event":
						r.Body = io.NopCloser(strings.NewReader("data: not-json\n\n"))
					case "partial output", "buffered partial output":
						r.Body = io.NopCloser(strings.NewReader(incompleteStream(kind)))
					case "read timeout":
						r.Body = failedResponseBody{context.DeadlineExceeded}
					case "caller canceled":
						cancel()
						r.Body = failedResponseBody{context.Canceled}
					case "invalid JSON":
						r.Header.Set("Content-Type", "application/json")
						r.Body = io.NopCloser(strings.NewReader(`{"unexpected":"value"}`))
					}
					return r, nil
				}}
				useRealStreamConnector(t, f, kind, b, 1, false)
				request := streamTestRequest(t).chat
				if scenario == "invalid JSON" {
					forceTransport(f, transportpolicy.ForceNonStream)
				}
				if scenario == "buffered partial output" {
					request.Clear()
					request = decodeChatForTest(t, `{"model":"[公益]care/model","messages":[{"role":"user","content":"hello"}]}`)
					forceTransport(f, transportpolicy.ForceStream)
				}
				defer request.Clear()
				w := httptest.NewRecorder()
				f.service.Chat(ctx, w, 1, request, nil, "application/json", "en")
				if calls != 1 || len(f.claims.outcomes) != 1 {
					t.Fatalf("calls=%d outcomes=%+v", calls, f.claims.outcomes)
				}
				outcome := f.claims.outcomes[0]
				billableStream := scenario != "invalid JSON"
				if outcome.UpstreamStatus != 200 || outcome.ProtocolSuccess || outcome.StreakDisposition != contract.StreakSuccess || outcome.FailureOrigin == contract.OriginNone || outcome.ResponseStarted != billableStream || (marks == 1) != billableStream {
					t.Fatalf("marks=%d outcome=%+v", marks, outcome)
				}
				if scenario != "caller canceled" && (!strings.Contains(w.Body.String(), `"error"`) || strings.Contains(w.Body.String(), "[DONE]")) {
					t.Fatalf("error hidden or marked complete: %s", w.Body)
				}
			})
		}
	}
}
