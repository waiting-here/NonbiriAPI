package openai

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

func TestMockOutboundChatBillsBucketsDespiteReportedTotal(t *testing.T) {
	response := strings.Replace(validCompletion, `"total_tokens":5`, `"total_tokens":99`, 1)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/chat/completions" {
			t.Errorf("outbound path = %q", request.URL.Path)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, response)
	}))
	defer server.Close()

	adapter := adapterForServer(t, server.URL, nil, nil)
	writer := httptest.NewRecorder()
	result := adapter.Attempt(context.Background(), writer,
		testTarget(server.URL+"/v1", []byte("sk-mismatch"), []byte("cipher-mismatch")),
		decodeAdapterRequest(t, `{"model":"public/model","messages":[]}`), "nbu_safe")
	if !result.Success || !result.Committed || !result.Usage.Present || !result.Usage.TotalMismatch ||
		result.Usage.UncachedInputTokens != 2 || result.Usage.OutputTokens != 3 {
		t.Fatalf("result=%+v", result)
	}
	if !strings.Contains(writer.Body.String(), `"total_tokens":99`) {
		t.Fatal("upstream response was unexpectedly rewritten")
	}
}

func TestMockOutboundEmbeddingBillsPromptDespiteReportedTotal(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/embeddings" {
			t.Errorf("outbound path = %q", request.URL.Path)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, embeddingResponse("float", `{"prompt_tokens":3,"total_tokens":99}`))
	}))
	defer server.Close()

	request, err := DecodeEmbeddingRequest(strings.NewReader(`{"model":"public/model","input":["one","two"]}`), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer request.Clear()
	writer := &embeddingCheckpointWriter{ResponseRecorder: httptest.NewRecorder()}
	result := adapterForServer(t, server.URL, nil, nil).AttemptEmbedding(context.Background(), writer,
		testTarget(server.URL+"/v1", []byte("sk-mismatch"), []byte("cipher-mismatch")),
		request, connectorcontract.AttemptPolicy{SafetyIdentifier: "nbu_safe"})
	if !result.Success || !result.Committed || !writer.marked || !result.Usage.Present ||
		!result.Usage.TotalMismatch || result.Usage.UncachedInputTokens != 3 || result.Usage.OutputTokens != 0 {
		t.Fatalf("result=%+v", result)
	}
}

func TestMockOutboundStreamMismatchStickyAcrossLaterSnapshots(t *testing.T) {
	for _, flatten := range []bool{false, true} {
		for _, later := range []struct {
			name        string
			usage       string
			wantPresent bool
		}{
			{"valid", `{"prompt_tokens":2,"completion_tokens":4,"total_tokens":6}`, true},
			{"malformed", `{"prompt_tokens":2,"completion_tokens":-1,"total_tokens":1}`, false},
		} {
			t.Run(later.name+"/flatten="+map[bool]string{false: "false", true: "true"}[flatten], func(t *testing.T) {
				chunk := func(usage string) string {
					return `data: {"id":"chatcmpl-1","object":"chat.completion.chunk","created":1700000000,"model":"upstream/model","choices":[],"usage":` + usage + "}\n\n"
				}
				server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
					writeSSE(writer,
						"data: "+validChunk+"\n\n",
						chunk(`{"prompt_tokens":2,"completion_tokens":3,"total_tokens":99}`),
						chunk(later.usage),
						`data: {"id":"chatcmpl-1","object":"chat.completion.chunk","created":1700000000,"model":"upstream/model","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`+"\n\n",
						"data: [DONE]\n\n",
					)
				}))
				defer server.Close()
				writer := httptest.NewRecorder()
				result := adapterForServer(t, server.URL, nil, nil).AttemptWithPolicy(context.Background(), writer,
					testTarget(server.URL, []byte("sk-mismatch"), []byte("cipher-mismatch")),
					streamRequest(t), connectorcontract.AttemptPolicy{SafetyIdentifier: "nbu_safe", FlattenToolCalls: flatten})
				if !result.Success || !result.Committed || !result.Usage.TotalMismatch || result.Usage.Present != later.wantPresent {
					t.Fatalf("result=%+v body=%s", result, writer.Body.String())
				}
				if later.wantPresent && (result.Usage.UncachedInputTokens != 2 || result.Usage.OutputTokens != 4) {
					t.Fatalf("billable buckets=%+v", result.Usage)
				}
			})
		}
	}
}
