package agentrequest

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestRequestFailureDiagnosticsDistinguishHTTPBodyAndProtocol(t *testing.T) {
	for _, tc := range []struct {
		name, body, category, code     string
		status                         int
		partial, responses, eof, usage bool
	}{
		{"partial", `{"choices":[`, "network", "unexpected_eof", 200, true, false, false, false},
		{"invalid_json", `{"choices":invalid}`, "protocol", "invalid_json", 200, false, false, true, false},
		{"missing_terminal", "data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n", "protocol", "invalid_response", 200, false, true, true, false},
		{"rate_limit", `{"error":{"message":"limited"},"usage":{"prompt_tokens":12,"completion_tokens":1}}`, "upstream", "upstream_error", 429, false, false, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("X-Request-ID", "assistant-failure-id")
				if tc.partial {
					w.Header().Set("Content-Length", "1000")
				}
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			})
			if tc.responses {
				p := responsesProfile(t, f, "response-model")
				bindProfile(t, f, "default", 0, p.ID)
			}
			if err := f.runtime.Start(); err != nil {
				t.Fatal(err)
			}
			request := submit(t, f.runtime, tc.name)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			if _, err := f.runtime.Wait(ctx, request.ID); !errors.Is(err, ErrUpstream) {
				t.Fatalf("failure result: %v", err)
			}
			detail, err := f.runtime.Detail(ctx, request.ID)
			if err != nil {
				t.Fatal(err)
			}
			facts := detail.ErrorDiagnostics
			if facts == nil || facts.Category != tc.category || facts.Code != tc.code || facts.UpstreamStatus != tc.status || !facts.HeadersReceived || facts.BodyEOF != tc.eof || facts.ProtocolComplete || facts.UsagePresent != tc.usage || len(facts.Correlation) != 1 || facts.Correlation[0].Value != "assistant-failure-id" {
				t.Fatalf("stored facts: %+v", facts)
			}
			encoded, _ := json.Marshal(facts)
			if strings.Contains(string(encoded), fixtureKey) || strings.Contains(string(encoded), tc.body) {
				t.Fatal("body or credential entered structured facts")
			}
		})
	}
}
