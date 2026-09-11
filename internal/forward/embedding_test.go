package forward

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/claim"
	"github.com/waiting-here/NonbiriAPI/internal/connector"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
	"github.com/waiting-here/NonbiriAPI/internal/debug"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

func callDebugModelForTest(t *testing.T, f *serviceFixture, w http.ResponseWriter, embedding, charity bool) {
	t.Helper()
	model := "provider/model"
	if charity {
		model = "[公益]care/model"
	}
	if embedding {
		body := `{"model":"` + model + `","input":[[1],[2,3]],"encoding_format":"base64"}`
		request, err := openai.DecodeEmbeddingRequest(strings.NewReader(body), 0)
		if err != nil {
			t.Fatal(err)
		}
		defer request.Clear()
		f.service.Embeddings(context.Background(), w, 1, request, []byte(body), "application/json", "en")
		return
	}
	body := `{"model":"` + model + `","messages":[{"role":"user","content":"hello"}],"stream":false}`
	request := decodeChatForTest(t, body)
	defer request.Clear()
	f.service.Chat(context.Background(), w, 1, request, []byte(body), "application/json", "en")
}

func TestEmbeddingIngressStrictnessAndDryAdmission(t *testing.T) {
	for _, charity := range []bool{false, true} {
		model := "provider/model"
		if charity {
			model = "[公益]care/model"
		}
		for _, tc := range []struct {
			name, method, path, body, media, encoding string
			status                                    int
			unauthenticated                           bool
		}{
			{name: "dry text", body: `" "`, status: 422},
			{name: "dry token IDs", body: `[1,2]`, status: 422},
			{name: "dry text batch", body: `["a","b"]`, status: 422},
			{name: "dry token batch", body: `[[1],[2,3]]`, status: 422},
			{name: "unauthenticated", body: `"a"`, status: 401, unauthenticated: true},
			{name: "method", method: "GET", status: 405},
			{name: "trailing slash", path: "/v1/embeddings/", status: 404},
			{name: "encoded path", path: "/v1/%65mbeddings", status: 404},
			{name: "query", path: "/v1/embeddings?x=1", status: 400},
			{name: "empty query", path: "/v1/embeddings?", status: 400},
			{name: "mixed input", body: `["a",1]`, status: 400},
			{name: "chat body", body: `null`, status: 400},
			{name: "bad media", body: `"a"`, media: "text/plain", status: 400},
			{name: "compressed", body: `"a"`, encoding: "gzip", status: 400},
			{name: "oversized", body: `"` + strings.Repeat("x", int(openai.MaxRequestBodyBytes)) + `"`, status: 413},
		} {
			t.Run(fmt.Sprintf("charity=%t/%s", charity, tc.name), func(t *testing.T) {
				capture := &fakeDebugCapture{decision: debug.CaptureDecision{Active: true, Mode: debug.ModeDry, Language: "en"}}
				f := newServiceFixture(t, capture)
				method, path := tc.method, tc.path
				if method == "" {
					method = "POST"
				}
				if path == "" {
					path = "/v1/embeddings"
				}
				r := httptest.NewRequest(method, path, strings.NewReader(`{"model":"`+model+`","input":`+tc.body+`}`))
				if !tc.unauthenticated {
					r = withCallerIdentity(r, resources.CallerIdentity{UserID: 1, Generation: 1})
				}
				if tc.media != "" {
					r.Header.Set("Content-Type", tc.media)
				}
				if tc.encoding != "" {
					r.Header.Set("Content-Encoding", tc.encoding)
				}
				w := httptest.NewRecorder()
				NewHandler(f.service).ServeHTTP(w, r)
				if w.Code != tc.status {
					t.Fatalf("status=%d want=%d body=%s", w.Code, tc.status, w.Body.String())
				}
				if len(f.claims.events) != 0 || f.personal.snapCalls != 0 || f.charity.snapCalls != 0 || f.openAI.calls != 0 || f.charges.calls != 0 {
					t.Fatal("rejected/dry request reached execution")
				}
				if tc.status == 422 && !strings.Contains(w.Body.String(), httperr.CodeDebugDryRunIntercepted) {
					t.Fatal("not a dry interception")
				}
			})
		}
	}
}

func TestEmbeddingSelectsOperationAndSkipsChatPolicies(t *testing.T) {
	for _, charity := range []bool{false, true} {
		for _, unsupported := range []bool{false, true} {
			t.Run(fmt.Sprintf("charity=%t/unsupported=%t", charity, unsupported), func(t *testing.T) {
				f := newServiceFixture(t, nil)
				f.personal.preflight.FlattenToolCalls = true
				f.personal.snapshot.FlattenToolCalls = true
				f.charity.preflight.FlattenToolCalls = true
				f.charity.snapshot.FlattenToolCalls = true
				model := "provider/model"
				candidate := f.personal.snapshot.Candidates[0]
				if charity {
					model = "[公益]care/model"
					candidate = f.charity.snapshot.Candidates[0]
				}
				candidate.Policy = contract.AttemptPolicy{FlattenToolCalls: true, ForceStoreFalse: true}
				other := candidate
				other.ConnectorType = contract.TypeAnthropicCompatible
				other.EndpointID++
				other.EndpointKeyID++
				candidates := []RouteCandidate{other}
				if !unsupported {
					candidates = append(candidates, candidate)
				}
				if charity {
					f.charity.snapshot.Candidates = candidates
				} else {
					f.personal.snapshot.Candidates = candidates
				}
				f.addDispatch(candidate)
				var captured *openai.EmbeddingRequest
				f.openAI.attempt = func(_ context.Context, in connector.AttemptInput) contract.AttemptResult {
					if in.Operation != contract.OperationEmbeddings || in.Ingress != nil || in.Embedding == nil || in.Policy.FlattenToolCalls || in.Policy.ForceStoreFalse {
						t.Fatal("wrong operation or chat policy")
					}
					captured = in.Embedding
					if value, ok := in.Embedding.RawField("store"); !ok || string(value) != "true" {
						t.Fatal("caller extension changed")
					}
					_, _ = in.Sink.Write([]byte(`{"object":"list"}`))
					return contract.AttemptResult{Success: true, Committed: true, Failure: contract.FailureNone, UpstreamStatus: 200, ClientStatus: 200, Usage: contract.Usage{Present: true, UncachedInputTokens: 1}}
				}
				r, err := openai.DecodeEmbeddingRequest(strings.NewReader(`{"model":"`+model+`","input":[[1],[2]],"store":true}`), 0)
				if err != nil {
					t.Fatal(err)
				}
				defer r.Clear()
				w := httptest.NewRecorder()
				f.service.Embeddings(context.Background(), w, 1, r, nil, "application/json", "en")
				if unsupported {
					if w.Code != 400 || len(f.claims.accepts) != 0 {
						t.Fatalf("unsupported: %d %+v", w.Code, f.claims.accepts)
					}
					return
				}
				if w.Code != 200 || f.openAI.calls != 1 || f.anthropic.calls != 0 || len(f.claims.accepts) != 1 || !f.claims.outcomes[0].ResponseStarted {
					t.Fatalf("execution %d events=%v", w.Code, f.claims.events)
				}
				want := claim.RouteOpenAIEmbeddings
				if charity {
					want = claim.RouteCharityEmbeddings
				}
				if f.claims.accepts[0].Route != want || captured.Model != "" || r.Model != model {
					t.Fatal("route or snapshot ownership changed")
				}
			})
		}
	}
}

type embeddingMarkRail struct {
	*fakeClaimRail
	mark func() error
}

func (r embeddingMarkRail) MarkResponseStarted(context.Context, claim.Handle) error { return r.mark() }

func TestEmbeddingCheckpointPreservesUsageAndStopsRetries(t *testing.T) {
	for _, state := range []string{"cancel-after-mark", "write-failure", "mark-failure"} {
		t.Run(state, func(t *testing.T) {
			f := newServiceFixture(t, nil)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			candidate := f.charity.snapshot.Candidates[0]
			f.charity.snapshot.Candidates = append(f.charity.snapshot.Candidates, candidate)
			f.addDispatch(candidate)
			f.addDispatch(candidate)
			marks := 0
			f.service.claims = embeddingMarkRail{f.claims, func() error {
				marks++
				if state == "mark-failure" {
					return errors.New("mark unavailable")
				}
				if state == "cancel-after-mark" {
					cancel()
				}
				return nil
			}}
			a, err := openai.NewAdapter(openai.AdapterConfig{Backend: billingBackend{status: 200, contentType: "application/json", body: `{"object":"list","model":"private","data":[{"object":"embedding","index":0,"embedding":[1]}],"usage":{"prompt_tokens":7,"total_tokens":7}}`}})
			if err != nil {
				t.Fatal(err)
			}
			f.openAI.attempt = func(ctx context.Context, in connector.AttemptInput) contract.AttemptResult {
				return a.AttemptEmbedding(ctx, in.Sink, openai.NewTarget("https://billing.example/v1", "private", openai.NewCredential([]byte("test-key"), []byte("test-cipher"))), in.Embedding, in.Policy)
			}
			r, err := openai.DecodeEmbeddingRequest(strings.NewReader(`{"model":"[公益]care/model","input":"a"}`), 0)
			if err != nil {
				t.Fatal(err)
			}
			defer r.Clear()
			w := httptest.NewRecorder()
			var sink http.ResponseWriter = w
			if state == "write-failure" {
				sink = &failedBillingSink{header: make(http.Header)}
			}
			f.service.Embeddings(ctx, sink, 1, r, nil, "application/json", "en")
			if marks != 1 || f.openAI.calls != 1 || len(f.claims.outcomes) != 1 || w.Body.Len() != 0 {
				t.Fatalf("marks=%d calls=%d outcomes=%d output=%s", marks, f.openAI.calls, len(f.claims.outcomes), w.Body.String())
			}
			out := f.claims.outcomes[0]
			if out.ResponseStarted != (state != "mark-failure") || !out.Usage.Present || out.Usage.UncachedInputTokens != 7 {
				t.Fatalf("lost accounting facts: %+v", out)
			}
		})
	}
}
