package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

const namedStreamUsage = `{"id":"chunk-usage","object":"chat.completion.chunk","created":1,"model":"upstream","choices":[],"usage":{"prompt_tokens":7,"completion_tokens":11,"total_tokens":18}}`
const namedStreamOrdinary = `{"id":"chunk-ordinary","object":"chat.completion.chunk","created":1,"model":"upstream","choices":[{"index":0,"delta":{"role":"assistant","content":"prefix"},"finish_reason":null}]}`
const namedStreamTool = `{"id":"chunk-tool","object":"chat.completion.chunk","created":1,"model":"upstream","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call-1","type":"function","function":{"name":"ping","arguments":"{}"}}]},"finish_reason":null}]}`
const namedStreamToolFinish = `{"id":"chunk-finish","object":"chat.completion.chunk","created":1,"model":"upstream","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`

func namedStreamPolicy(flatten bool) connectorcontract.AttemptPolicy {
	return connectorcontract.AttemptPolicy{SafetyIdentifier: "nbu_safe", FlattenToolCalls: flatten}
}

func namedStreamFrames(flatten bool) []string {
	if flatten {
		return []string{
			"data: " + namedStreamOrdinary + "\n\n",
			"data: " + namedStreamTool + "\n\n",
			"data: " + namedStreamToolFinish + "\n\n",
			"data: " + namedStreamUsage + "\n\n",
		}
	}
	return []string{"data: " + validChunk + "\n\n", "data: " + namedStreamUsage + "\n\n"}
}

func TestAdapterNamedStreamCompletionPreservesUsageAndToolFlattening(t *testing.T) {
	terminals := []struct {
		name  string
		frame string
	}{
		{"standard", "data: [DONE]\n\n"},
		{"message", "event: message\ndata: [DONE]\n\n"},
		{"done", "event: done\ndata: [DONE]\n\n"},
	}
	for _, flatten := range []bool{false, true} {
		for _, terminal := range terminals {
			t.Run(fmt.Sprintf("flatten=%t/%s", flatten, terminal.name), func(t *testing.T) {
				frames := append(namedStreamFrames(flatten), terminal.frame)
				server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
					writeSSE(writer, frames...)
				}))
				defer server.Close()
				adapter := adapterForServer(t, server.URL, nil, nil)
				recorder := httptest.NewRecorder()
				result := adapter.AttemptWithPolicy(context.Background(), recorder,
					testTarget(server.URL, []byte("mock-key"), []byte("mock-cipher")), streamRequest(t), namedStreamPolicy(flatten))
				if !result.Success || !result.Committed || result.Failure != FailureNone {
					t.Fatalf("result=%+v body=%q", result, recorder.Body.String())
				}
				if !result.Usage.Present || result.Usage.UncachedInputTokens != 7 || result.Usage.OutputTokens != 11 {
					t.Fatalf("usage=%+v", result.Usage)
				}
				body := recorder.Body.String()
				if strings.Count(body, "data: [DONE]\n\n") != 1 || !strings.HasSuffix(body, "data: [DONE]\n\n") || strings.Contains(body, "event: done") {
					t.Fatalf("terminal framing=%q", body)
				}
				if flatten {
					var toolBlock string
					for _, line := range strings.Split(body, "\n") {
						if !strings.HasPrefix(line, "data: {") {
							continue
						}
						var chunk struct {
							Choices []struct {
								Delta struct {
									Content string `json:"content"`
								} `json:"delta"`
							} `json:"choices"`
						}
						if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &chunk); err != nil {
							t.Fatal(err)
						}
						for _, choice := range chunk.Choices {
							if strings.Contains(choice.Delta.Content, "<mx_tool") {
								toolBlock = choice.Delta.Content
							}
						}
					}
					if !strings.Contains(toolBlock, `<mx_tool name="ping" id="call-1">`) || !strings.Contains(body, `"finish_reason":"stop"`) || strings.Contains(body, `"tool_calls"`) || strings.Count(body, `"usage"`) != 1 {
						t.Fatalf("tool flattening or usage changed: %q", body)
					}
				} else if !strings.Contains(body, `"content":"ok"`) {
					t.Fatalf("ordinary chunk missing: %q", body)
				}
			})
		}
	}
}

func TestAdapterNamedStreamCompletionRejectsOtherTerminations(t *testing.T) {
	finishWithoutDone := `{"id":"finish","object":"chat.completion.chunk","created":1,"model":"upstream","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`
	tests := []struct {
		name           string
		frames         []string
		wantCommitted  bool
		wantDiagnostic string
	}{
		{"empty 200", nil, false, "ended before completion"},
		{"named done without chunk", []string{"event: done\ndata: [DONE]\n\n"}, false, "ended without a chunk"},
		{"done carrying a chunk", []string{"data: " + validChunk + "\n\n", "event: done\ndata: " + validChunk + "\n\n", "data: [DONE]\n\n"}, true, "event type was invalid"},
		{"done nonexact marker", []string{"data: " + validChunk + "\n\n", "event: done\ndata: [DONE]suffix\n\n"}, true, "event type was invalid"},
		{"error with done payload", []string{"data: " + validChunk + "\n\n", "event: error\ndata: [DONE]\n\n"}, true, "reported an error"},
		{"unknown named terminator", []string{"data: " + validChunk + "\n\n", "event: completed\ndata: [DONE]\n\n"}, true, "event type was invalid"},
		{"finish reason and clean EOF", []string{"data: " + validChunk + "\n\n", "data: " + finishWithoutDone + "\n\n"}, true, "ended before completion"},
	}
	for _, flatten := range []bool{false, true} {
		for _, test := range tests {
			t.Run(fmt.Sprintf("flatten=%t/%s", flatten, test.name), func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
					writeSSE(writer, test.frames...)
				}))
				defer server.Close()
				adapter := adapterForServer(t, server.URL, nil, nil)
				recorder := httptest.NewRecorder()
				result := adapter.AttemptWithPolicy(context.Background(), recorder,
					testTarget(server.URL, []byte("mock-key"), []byte("mock-cipher")), streamRequest(t), namedStreamPolicy(flatten))
				if result.Success || result.Failure != FailureUpstream || result.Committed != test.wantCommitted || !strings.Contains(result.Diagnostic, test.wantDiagnostic) {
					t.Fatalf("result=%+v body=%q", result, recorder.Body.String())
				}
				body := recorder.Body.String()
				if strings.Contains(body, "data: [DONE]") || strings.Contains(body, "mock-key") || strings.Contains(body, "mock-cipher") {
					t.Fatalf("failure exposed terminal or credential: %q", body)
				}
				if test.wantCommitted && !strings.Contains(body, `"error"`) {
					t.Fatalf("committed failure lacked error frame: %q", body)
				}
			})
		}
	}
}

type namedStreamTerminalFailWriter struct{ *httptest.ResponseRecorder }

func (writer *namedStreamTerminalFailWriter) Write(frame []byte) (int, error) {
	if string(frame) == "data: [DONE]\n\n" {
		return 0, errors.New("mock client write failure")
	}
	return writer.ResponseRecorder.Write(frame)
}

func TestAdapterNamedStreamCompletionTerminalWriteFailure(t *testing.T) {
	for _, flatten := range []bool{false, true} {
		t.Run(fmt.Sprintf("flatten=%t", flatten), func(t *testing.T) {
			frames := append(namedStreamFrames(flatten), "event: done\ndata: [DONE]\n\n")
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writeSSE(writer, frames...)
			}))
			defer server.Close()
			adapter := adapterForServer(t, server.URL, nil, nil)
			writer := &namedStreamTerminalFailWriter{httptest.NewRecorder()}
			result := adapter.AttemptWithPolicy(context.Background(), writer,
				testTarget(server.URL, []byte("mock-key"), []byte("mock-cipher")), streamRequest(t), namedStreamPolicy(flatten))
			if result.Success || result.Failure != FailureSink || !result.SinkFailed || !result.Committed || !result.Usage.Present {
				t.Fatalf("result=%+v body=%q", result, writer.Body.String())
			}
			if strings.Contains(writer.Body.String(), "data: [DONE]") {
				t.Fatalf("failed terminal write appeared in output: %q", writer.Body.String())
			}
		})
	}
}

func TestAdapterNamedStreamCompletionCancellation(t *testing.T) {
	for _, flatten := range []bool{false, true} {
		t.Run(fmt.Sprintf("flatten=%t", flatten), func(t *testing.T) {
			upstreamCanceled := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writeSSE(writer, "data: "+validChunk+"\n\n")
				<-request.Context().Done()
				close(upstreamCanceled)
			}))
			defer server.Close()
			adapter := adapterForServer(t, server.URL, nil, nil)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			writer := &signalingRecorder{ResponseRecorder: httptest.NewRecorder(), wrote: make(chan struct{})}
			finished := make(chan AttemptResult, 1)
			request := streamRequest(t)
			go func() {
				finished <- adapter.AttemptWithPolicy(ctx, writer,
					testTarget(server.URL, []byte("mock-key"), []byte("mock-cipher")), request, namedStreamPolicy(flatten))
			}()
			select {
			case <-writer.wrote:
			case <-time.After(2 * time.Second):
				t.Fatal("first chunk was not forwarded")
			}
			cancel()
			select {
			case result := <-finished:
				if result.Success || result.Failure != FailureCanceled || !result.Committed {
					t.Fatalf("result=%+v body=%q", result, writer.Body.String())
				}
			case <-time.After(2 * time.Second):
				t.Fatal("cancellation did not stop the adapter")
			}
			select {
			case <-upstreamCanceled:
			case <-time.After(2 * time.Second):
				t.Fatal("upstream did not observe cancellation")
			}
			if strings.Contains(writer.Body.String(), "data: [DONE]") || strings.Contains(writer.Body.String(), `"error"`) {
				t.Fatalf("cancellation wrote a terminal or error frame: %q", writer.Body.String())
			}
		})
	}
}
