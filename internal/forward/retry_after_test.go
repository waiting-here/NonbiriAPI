package forward

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/backend"
	"github.com/waiting-here/NonbiriAPI/internal/config"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
	"github.com/waiting-here/NonbiriAPI/internal/egress"
)

type retryHeaderBackend struct {
	status                int
	stream                bool
	hint                  string
	observed              *bool
	reads                 int
	readBeforeObservation bool
	reject                bool
}

func (b *retryHeaderBackend) Open(string) (backend.EndpointClient, error) { return b, nil }
func (*retryHeaderBackend) MaxResponseBytes() int64                       { return 1 << 20 }
func (*retryHeaderBackend) BaseURL() string                               { return "https://retry.example/v1" }
func (b *retryHeaderBackend) Do(*http.Request) (*http.Response, error) {
	headers := http.Header{"Content-Type": {"application/json"}, "Retry-After": {b.hint}}
	if b.stream {
		headers.Set("Content-Type", "text/event-stream")
	}
	response := &http.Response{StatusCode: b.status, Header: headers, Body: b, ContentLength: -1}
	if b.reject {
		return response, errors.New("redirect blocked")
	}
	return response, nil
}
func (b *retryHeaderBackend) Read([]byte) (int, error) {
	b.reads++
	if !*b.observed {
		b.readBeforeObservation = true
		return 0, errors.New("body read before observation")
	}
	return 0, io.EOF
}
func (*retryHeaderBackend) Close() error { return nil }

func TestRetryAfterObservedByRealAdaptersBeforeBodyWithoutBillingErrors(t *testing.T) {
	for _, operation := range []string{"chat", "embedding"} {
		for _, status := range []int{200, 429, 503, 302} {
			t.Run(operation+"/"+http.StatusText(status), func(t *testing.T) {
				observed := false
				b := &retryHeaderBackend{status: status, hint: "60", observed: &observed, reject: status == 302}
				a, err := openai.NewAdapter(openai.AdapterConfig{Backend: b})
				if err != nil {
					t.Fatal(err)
				}
				marks := 0
				writer, checkpoint := checkpointResponseWriter(httptest.NewRecorder(), func() error { marks++; return nil })
				checkpoint.acceptedStreams = true
				checkpoint.now = func() time.Time { return time.Unix(1700000000, 0) }
				checkpoint.observe = func(value contract.ResponseObservation) error {
					if value.Status != status || value.RateLimitedUntil != 1700000060 || value.Stream {
						t.Fatalf("observation=%+v", value)
					}
					observed = true
					return nil
				}
				target := openai.NewTarget(b.BaseURL(), "m", openai.NewCredential([]byte("test"), []byte("test")))
				var result contract.AttemptResult
				if operation == "chat" {
					request := decodeChatForTest(t, `{"model":"p/m","messages":[{"role":"user","content":"test"}]}`)
					result = a.AttemptWithPolicy(context.Background(), writer, target, request, contract.AttemptPolicy{SafetyIdentifier: "nbu_test"})
				} else {
					request, err := openai.DecodeEmbeddingRequest(strings.NewReader(`{"model":"p/m","input":"test"}`), 0)
					if err != nil {
						t.Fatal(err)
					}
					defer request.Clear()
					result = a.AttemptEmbedding(context.Background(), writer, target, request, contract.AttemptPolicy{SafetyIdentifier: "nbu_test"})
				}
				if b.readBeforeObservation || !observed || marks != 0 || checkpoint.started || result.Success {
					t.Fatalf("observed=%t marks=%d started=%t result=%+v", observed, marks, checkpoint.started, result)
				}
			})
		}
	}
}

func TestRetryAfterIgnoresPersonalAndInvalidHints(t *testing.T) {
	for _, tc := range []struct {
		charity bool
		value   string
	}{{false, "60"}, {true, "bad"}, {true, "-1"}} {
		writer := &responseStartWriter{ResponseWriter: httptest.NewRecorder(), acceptedStreams: tc.charity, now: func() time.Time { return time.Unix(1700000000, 0) }, observe: func(contract.ResponseObservation) error { t.Fatal("unexpected hint"); return nil }}
		if err := writer.ObserveUpstreamResponse(503, false, http.Header{"Retry-After": {tc.value}}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRetryAfterRealEgressRedirectReachesCheckpoint(t *testing.T) {
	for _, operation := range []string{"chat", "embedding"} {
		t.Run(operation, func(t *testing.T) {
			var targetHits atomic.Int32
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { targetHits.Add(1); w.WriteHeader(200) }))
			defer target.Close()
			source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Retry-After", "60")
				http.Redirect(w, r, target.URL+"/private", http.StatusFound)
			}))
			defer source.Close()
			stack, err := egress.NewStack(egress.StackOptions{AllowedOrigins: []string{source.URL}, Resolver: errorTestResolver{}, RequestTimeout: time.Second, Concurrency: egress.ConcurrencyLimits{Global: 1, PerEndpoint: 1}})
			if err != nil {
				t.Fatal(err)
			}
			if err := stack.AddSelfOrigins(context.Background(), &config.Config{SiteBaseURL: "https://gateway.example", UserHost: "gateway.example", AdminHost: "admin.gateway.example", ListenAddr: "127.0.0.1:1"}); err != nil {
				t.Fatal(err)
			}
			defer stack.CloseIdleConnections()
			local, err := backend.NewLocal(stack)
			if err != nil {
				t.Fatal(err)
			}
			adapter, err := openai.NewAdapter(openai.AdapterConfig{Backend: local})
			if err != nil {
				t.Fatal(err)
			}
			writer, checkpoint := checkpointResponseWriter(httptest.NewRecorder(), func() error { t.Fatal("redirect established billing"); return nil })
			checkpoint.acceptedStreams = true
			checkpoint.now = func() time.Time { return time.Unix(1700000000, 0) }
			observations := 0
			checkpoint.observe = func(value contract.ResponseObservation) error {
				observations++
				if value.Status != 302 || value.RateLimitedUntil != 1700000060 || value.Stream {
					t.Fatalf("lost redirect hint: %+v", value)
				}
				return nil
			}
			upstream := openai.NewTarget(source.URL+"/v1", "model", openai.NewCredential([]byte("test-key"), []byte("test-envelope")))
			var result contract.AttemptResult
			if operation == "chat" {
				request := decodeChatForTest(t, `{"model":"p/m","messages":[{"role":"user","content":"test"}]}`)
				result = adapter.AttemptWithPolicy(context.Background(), writer, upstream, request, contract.AttemptPolicy{SafetyIdentifier: "nbu_test"})
			} else {
				request, err := openai.DecodeEmbeddingRequest(strings.NewReader(`{"model":"p/m","input":"test"}`), 0)
				if err != nil {
					t.Fatal(err)
				}
				defer request.Clear()
				result = adapter.AttemptEmbedding(context.Background(), writer, upstream, request, contract.AttemptPolicy{SafetyIdentifier: "nbu_test"})
			}
			if observations != 1 || result.UpstreamStatus != 302 || result.FailureOrigin != contract.OriginPlatform || result.Success || checkpoint.started || targetHits.Load() != 0 {
				t.Fatalf("observations=%d targetHits=%d result=%+v", observations, targetHits.Load(), result)
			}
		})
	}
}
