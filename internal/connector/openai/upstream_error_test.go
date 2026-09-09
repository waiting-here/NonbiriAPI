package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
)

func TestAttemptExtractsSafeHTTPErrorWithoutCommitting(t *testing.T) {
	for _, status := range []int{200, 429, 503} {
		for _, stream := range []bool{false, true} {
			if status == 200 && stream {
				continue
			}
			t.Run(fmt.Sprintf("status=%d/stream=%t", status, stream), func(t *testing.T) {
				const secret, ciphertext = "reflected-credential-83", "mock-cipher-mock-cipher"
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.Header().Set("Location", "https://private.example/retry")
					w.Header().Set("Set-Cookie", "private=1")
					w.WriteHeader(status)
					_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{
						"code":    "quota_exhausted",
						"message": "Quota exhausted; retry later. " + r.Host + " private.example 203.0.113.7 [2001:db8::7] " + secret + " " + ciphertext + " upstream/model opaque-caller",
						"debug":   "private field must not be copied",
					}})
				}))
				defer server.Close()
				adapter := adapterForServer(t, server.URL, nil, nil)
				request := decodeAdapterRequest(t, fmt.Sprintf(`{"model":"public/model","messages":[],"stream":%t}`, stream))
				keyBytes, cipherBytes := []byte(secret), []byte(ciphertext)
				recorder := httptest.NewRecorder()
				result := adapter.Attempt(context.Background(), recorder, testTarget(server.URL, keyBytes, cipherBytes), request, "opaque-caller")
				if result.Failure != FailureUpstream || result.UpstreamStatus != status || result.Committed || result.Success || recorder.Body.Len() != 0 || result.Usage.Present {
					t.Fatalf("result=%+v body=%q", result, recorder.Body.String())
				}
				if result.ErrorDetail.Code() != "quota_exhausted" || !strings.Contains(result.ErrorDetail.Message(), "Quota exhausted; retry later.") {
					t.Fatalf("safe detail lost: %+v", result.ErrorDetail)
				}
				for _, forbidden := range []string{secret, ciphertext, "opaque-caller", "private.example", "127.0.0.1", "203.0.113.7", "2001:db8", "upstream/model", "private field"} {
					if strings.Contains(result.ErrorDetail.Message(), forbidden) || strings.Contains(result.Diagnostic, forbidden) {
						t.Fatalf("error exposed %q", forbidden)
					}
				}
				if recorder.Header().Get("Location") != "" || recorder.Header().Get("Set-Cookie") != "" || strings.Trim(string(keyBytes), "\x00") != "" || strings.Trim(string(cipherBytes), "\x00") != "" {
					t.Fatal("upstream headers or retained credentials escaped the boundary")
				}
			})
		}
	}
}

func TestAttemptReportedStreamErrorsBeforeAndAfterCommit(t *testing.T) {
	for _, flatten := range []bool{false, true} {
		for _, committed := range []bool{false, true} {
			for _, eventName := range []string{"message", "error"} {
				t.Run(fmt.Sprintf("flatten=%t/committed=%t/event=%s", flatten, committed, eventName), func(t *testing.T) {
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
						frames := []string{}
						if committed {
							frames = append(frames, "data: "+validChunk+"\n\n")
						}
						frames = append(frames, "event: "+eventName+"\ndata: "+`{"error":{"code":"overloaded","message":"Please retry later at https://private.example/path?token=mock-secret-mock-secret"}}`+"\n\n", "data: [DONE]\n\n")
						writeSSE(w, frames...)
					}))
					defer server.Close()
					adapter := adapterForServer(t, server.URL, nil, nil)
					recorder := httptest.NewRecorder()
					result := adapter.AttemptWithPolicy(context.Background(), recorder, testTarget(server.URL, []byte("mock-secret-mock-secret"), []byte("ciphertext-72")), streamRequest(t), connectorcontract.AttemptPolicy{FlattenToolCalls: flatten, SafetyIdentifier: "nbu_safe"})
					if result.Failure != FailureUpstream || result.Success || result.Committed != committed || result.UpstreamStatus != 200 || result.ErrorDetail.Code() != "overloaded" || !strings.Contains(result.ErrorDetail.Message(), "Please retry later") {
						t.Fatalf("result=%+v body=%q", result, recorder.Body.String())
					}
					if !committed {
						if recorder.Body.Len() != 0 {
							t.Fatal("pre-commit error wrote a response")
						}
						return
					}
					body := recorder.Body.String()
					if recorder.Code != 200 || strings.Count(body, `"error":`) != 1 || strings.Contains(body, "[DONE]") || strings.Contains(body, "private.example") || strings.Contains(body, "mock-secret-mock-secret") {
						t.Fatalf("stream terminal=%q", body)
					}
					frames := strings.Split(strings.TrimSpace(body), "\n\n")
					var envelope httperr.Envelope
					if err := json.Unmarshal([]byte(strings.TrimPrefix(frames[len(frames)-1], "data: ")), &envelope); err != nil {
						t.Fatal(err)
					}
					if envelope.Error.Source != httperr.SourceUpstream || envelope.Error.Code != httperr.CodeUpstream || envelope.Error.UpstreamCode != "overloaded" || !strings.Contains(envelope.Error.Message, "Please retry later") || envelope.Error.Diag != "" {
						t.Fatalf("envelope=%+v", envelope)
					}
				})
			}
		}
	}
}

func TestAttemptUnreadableErrorFallsBackAndCancellationStopsRead(t *testing.T) {
	for _, body := range []string{"", "<html>private.example</html>", strings.Repeat("X", 1025), `{"error":{"message":"ambiguous","message":"private.example"}}`} {
		t.Run(fmt.Sprintf("length=%d", len(body)), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(503); _, _ = io.WriteString(w, body) }))
			defer server.Close()
			adapter := adapterForServer(t, server.URL, func(c *AdapterConfig) { c.MaxJSONResponseBytes = 1024 }, nil)
			r := adapter.Attempt(context.Background(), httptest.NewRecorder(), testTarget(server.URL, []byte("credential"), []byte("ciphertext")), streamRequest(t), "nbu")
			if r.Failure != FailureUpstream || r.UpstreamStatus != 503 || r.ErrorDetail.Message() != "" || r.ErrorDetail.Code() != "" {
				t.Fatalf("result=%+v", r)
			}
		})
	}

	entered, stopped := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
		w.(http.Flusher).Flush()
		close(entered)
		<-r.Context().Done()
		close(stopped)
	}))
	defer server.Close()
	adapter := adapterForServer(t, server.URL, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request := streamRequest(t)
	result := make(chan AttemptResult, 1)
	go func() {
		result <- adapter.Attempt(ctx, httptest.NewRecorder(), testTarget(server.URL, []byte("credential"), []byte("ciphertext")), request, "nbu")
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("upstream did not start")
	}
	cancel()
	select {
	case r := <-result:
		if r.Failure != FailureCanceled || r.Committed || r.ErrorDetail.Message() != "" {
			t.Fatalf("result=%+v", r)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("error read ignored cancellation")
	}
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("upstream request was not canceled")
	}
}
