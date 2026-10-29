package chatwire_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/connector/chatwire"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

func TestFailedResponsesKeepIndependentUsageOnJSONAndSSE(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, usage := range []string{`,"usage":{"input_tokens":10,"output_tokens":2,"total_tokens":12}`, ""} {
			raw := `{"id":"response","object":"response","status":"failed","error":{"code":"server_error","message":"provider failed"},"output":[]` + usage + `}`
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if stream {
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprint(w, `data: {"type":"response.failed","sequence_number":1,"response":`+raw+"}\n\n")
				} else {
					fmt.Fprint(w, raw)
				}
			}))
			a, err := chatwire.New(outbound(t, server.URL), protocol(contract.TypeOpenAIResponses))
			if err != nil {
				t.Fatal(err)
			}
			w := &observedWriter{ResponseRecorder: httptest.NewRecorder()}
			result := a.Attempt(context.Background(), w, contract.NewTarget(contract.TypeOpenAIResponses, server.URL, "private"), contract.NewShortLivedSecret([]byte("fixture-secret"), nil), chat(t, stream), contract.AttemptPolicy{SafetyIdentifier: "nbu_fixture"})
			server.Close()
			if result.Success || result.Usage.Present != (usage != "") || usage != "" && (result.Usage.UncachedInputTokens != 10 || result.Usage.OutputTokens != 2) {
				t.Fatalf("stream=%v result=%+v", stream, result)
			}
		}
	}
}

func TestGeminiContinuationMakesOnePhysicalCallAndKeepsUsage(t *testing.T) {
	for _, stream := range []bool{false, true} {
		calls := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			raw := `{"candidates":[{"finishReason":"CONTINUATION","continuationToken":"opaque"}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":2,"totalTokenCount":12}}`
			w.Header().Set("Content-Type", "application/json")
			if stream {
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "data: "+raw+"\n\n")
			} else {
				fmt.Fprint(w, raw)
			}
		}))
		a, err := chatwire.New(outbound(t, server.URL), protocol(contract.TypeGoogleGemini))
		if err != nil {
			t.Fatal(err)
		}
		w := &observedWriter{ResponseRecorder: httptest.NewRecorder()}
		result := a.Attempt(context.Background(), w, contract.NewTarget(contract.TypeGoogleGemini, server.URL, "gemini"), contract.NewShortLivedSecret([]byte("fixture-secret"), nil), chat(t, stream), contract.AttemptPolicy{SafetyIdentifier: "nbu_fixture"})
		server.Close()
		if result.Success || calls != 1 || !result.Usage.Present || !strings.Contains(result.Diagnostic, "automatic continuation is not supported") {
			t.Fatalf("calls=%d result=%+v", calls, result)
		}
	}
}
