package forward

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/backend"
	"github.com/waiting-here/NonbiriAPI/internal/connector/anthropic"
	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
)

type billingBackend struct {
	status            int
	contentType, body string
	err               error
}

func (b billingBackend) Open(string) (backend.EndpointClient, error) { return b, nil }
func (billingBackend) MaxResponseBytes() int64                       { return 32 << 20 }
func (billingBackend) BaseURL() string                               { return "https://billing.example/v1" }
func (b billingBackend) Do(*http.Request) (*http.Response, error) {
	if b.err != nil {
		return nil, b.err
	}
	return &http.Response{StatusCode: b.status, Header: http.Header{"Content-Type": {b.contentType}}, Body: io.NopCloser(strings.NewReader(b.body)), ContentLength: -1}, nil
}

func TestBillingCheckpointTracksValidatedProtocolOutput(t *testing.T) {
	const completion = `{"id":"c1","object":"chat.completion","created":1,"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`
	const chunk = `{"id":"c1","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"content":"x"},"finish_reason":null}]}`
	const tool = `{"id":"c1","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"test","arguments":"{}"}}]},"finish_reason":null}]}`
	const anthropicJSON = `{"id":"msg_1","type":"message","role":"assistant","model":"m","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","stop_sequence":null,"usage":{"input_tokens":1,"output_tokens":1}}`
	const anthropicStart = "event: message_start\ndata: " + `{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"m","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":1,"output_tokens":0}}}` + "\n\n"
	for _, tc := range []struct {
		name, body                                   string
		status                                       int
		stream, flatten, anthropic, started, success bool
		transport                                    error
	}{
		{name: "no headers", transport: context.DeadlineExceeded},
		{name: "HTTP error", status: 502, body: completion},
		{name: "empty JSON", status: 200},
		{name: "invalid JSON", status: 200, body: `{}`},
		{name: "SSE heartbeat", status: 200, stream: true, body: ": heartbeat\n\n"},
		{name: "SSE error", status: 200, stream: true, body: "data: {\"error\":{\"message\":\"bad\"}}\n\n"},
		{name: "SSE done only", status: 200, stream: true, body: "data: [DONE]\n\n"},
		{name: "valid JSON", status: 200, body: completion, started: true, success: true},
		{name: "one token then EOF", status: 200, stream: true, body: "data: " + chunk + "\n\n", started: true},
		{name: "complete SSE", status: 200, stream: true, body: "data: " + chunk + "\n\ndata: [DONE]\n\n", started: true, success: true},
		{name: "buffered tool then EOF", status: 200, stream: true, flatten: true, body: "data: " + tool + "\n\n", started: true},
		{name: "Anthropic heartbeat", status: 200, anthropic: true, stream: true, body: "event: ping\ndata: {\"type\":\"ping\"}\n\n"},
		{name: "Anthropic error", status: 200, anthropic: true, stream: true, body: "event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\"}}\n\n"},
		{name: "Anthropic started then EOF", status: 200, anthropic: true, stream: true, body: anthropicStart, started: true},
		{name: "Anthropic valid JSON", status: 200, anthropic: true, body: anthropicJSON, started: true, success: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			contentType := "application/json"
			if tc.stream {
				contentType = "text/event-stream"
			}
			b := billingBackend{status: tc.status, contentType: contentType, body: tc.body, err: tc.transport}
			request := decodeChatForTest(t, `{"model":"p/m","stream":`+map[bool]string{true: "true", false: "false"}[tc.stream]+`,"messages":[{"role":"user","content":"test"}]}`)
			recorder := httptest.NewRecorder()
			marks := 0
			writer, checkpoint := checkpointResponseWriter(recorder, func() error { marks++; return nil })
			var result connectorcontract.AttemptResult
			if tc.anthropic {
				a, err := anthropic.NewAdapter(anthropic.AdapterConfig{Backend: b})
				if err != nil {
					t.Fatal(err)
				}
				result = a.Attempt(context.Background(), writer, anthropic.NewTarget(b.BaseURL(), "m", anthropic.NewCredential([]byte("test-credential"), []byte("test-envelope"))), request, "nbu_test")
			} else {
				a, err := openai.NewAdapter(openai.AdapterConfig{Backend: b})
				if err != nil {
					t.Fatal(err)
				}
				result = a.AttemptWithPolicy(context.Background(), writer, openai.NewTarget(b.BaseURL(), "m", openai.NewCredential([]byte("test-credential"), []byte("test-envelope"))), request, connectorcontract.AttemptPolicy{SafetyIdentifier: "nbu_test", FlattenToolCalls: tc.flatten})
			}
			if checkpoint.started != tc.started || result.Success != tc.success || marks > 1 {
				t.Fatalf("started=%t marks=%d result=%+v", checkpoint.started, marks, result)
			}
			if tc.flatten && recorder.Body.Len() != 0 {
				t.Fatal("incomplete buffered tool leaked to caller")
			}
		})
	}
}

type failedBillingSink struct{ header http.Header }

func (w *failedBillingSink) Header() http.Header     { return w.header }
func (*failedBillingSink) WriteHeader(int)           {}
func (*failedBillingSink) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestBillingCheckpointFailureAndDownstreamFailure(t *testing.T) {
	for _, checkpointFails := range []bool{false, true} {
		marks := 0
		writer, state := checkpointResponseWriter(&failedBillingSink{header: make(http.Header)}, func() error {
			marks++
			if checkpointFails {
				return errors.New("checkpoint unavailable")
			}
			return nil
		})
		for range 2 {
			if n, err := writer.Write([]byte("x")); n != 0 || err == nil {
				t.Fatalf("write=%d %v", n, err)
			}
		}
		if marks != 1 || state.started == checkpointFails {
			t.Fatalf("checkpoint failure=%t state=%t marks=%d", checkpointFails, state.started, marks)
		}
		if _, ok := writer.(http.Flusher); ok {
			t.Fatal("wrapper fabricated flushing support")
		}
	}
}

func TestForwardTimeoutCoversWholeLogicalRequest(t *testing.T) {
	if DefaultForwardTimeout != 1200*time.Second {
		t.Fatalf("logical timeout=%v", DefaultForwardTimeout)
	}
}
