package openai

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

const policyToolChunk = `{"id":"flatten-tool","object":"chat.completion.chunk","created":1,"model":"upstream","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call-1","type":"function","function":{"name":"write_file","arguments":"{}"}}]},"finish_reason":null}]}`

func TestFlattenStreamCleanEOFBeforeDoneIsPreCommitFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writeSSE(writer, "data: "+policyToolChunk+"\n\n")
	}))
	defer server.Close()
	adapter := adapterForServer(t, server.URL, nil, nil)
	recorder := httptest.NewRecorder()
	result := adapter.AttemptWithPolicy(context.Background(), recorder,
		testTarget(server.URL, []byte("sk-clean-eof"), []byte("cipher-clean-eof")),
		streamRequest(t), connectorcontract.AttemptPolicy{SafetyIdentifier: "nbu_clean_eof", FlattenToolCalls: true})
	if result.Success || result.Committed || result.Failure != FailureUpstream {
		t.Fatalf("clean EOF result=%+v body=%q", result, recorder.Body.String())
	}
	if recorder.Body.Len() != 0 {
		t.Fatalf("pre-commit clean EOF wrote caller bytes: %q", recorder.Body.String())
	}
}

func TestFlattenStreamMalformedBeforeCommitIsPreCommitFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writeSSE(writer, "data: "+policyToolChunk+"\n\n", "data: {bad}\n\n")
	}))
	defer server.Close()
	adapter := adapterForServer(t, server.URL, nil, nil)
	recorder := httptest.NewRecorder()
	result := adapter.AttemptWithPolicy(context.Background(), recorder,
		testTarget(server.URL, []byte("sk-malformed-pre"), []byte("cipher-malformed-pre")),
		streamRequest(t), connectorcontract.AttemptPolicy{SafetyIdentifier: "nbu_malformed_pre", FlattenToolCalls: true})
	if result.Success || result.Committed || result.Failure != FailureUpstream {
		t.Fatalf("malformed pre-commit result=%+v body=%q", result, recorder.Body.String())
	}
	if recorder.Body.Len() != 0 {
		t.Fatalf("pre-commit malformed stream wrote caller bytes: %q", recorder.Body.String())
	}
}

func TestFlattenStreamMovesCombinedUsageToTerminalUsageChunk(t *testing.T) {
	finishWithUsage := `{"id":"flatten-finish","object":"chat.completion.chunk","created":1,"model":"upstream","system_fingerprint":"fp_test","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writeSSE(writer, "data: "+policyToolChunk+"\n\n", "data: "+finishWithUsage+"\n\n", "data: [DONE]\n\n")
	}))
	defer server.Close()
	adapter := adapterForServer(t, server.URL, nil, nil)
	recorder := httptest.NewRecorder()
	result := adapter.AttemptWithPolicy(context.Background(), recorder,
		testTarget(server.URL, []byte("sk-combined-usage"), []byte("cipher-combined-usage")),
		streamRequest(t), connectorcontract.AttemptPolicy{SafetyIdentifier: "nbu_combined_usage", FlattenToolCalls: true})
	if !result.Success || !result.Committed || !result.Usage.Present || result.Usage.UncachedInputTokens != 3 || result.Usage.OutputTokens != 2 {
		t.Fatalf("combined usage result=%+v body=%q", result, recorder.Body.String())
	}
	body := recorder.Body.String()
	toolAt := strings.Index(body, "mx_tool")
	finishAt := strings.Index(body, `"finish_reason":"stop"`)
	usageAt := strings.Index(body, `"choices":[],"created":1,"id":"flatten-finish"`)
	doneAt := strings.Index(body, "data: [DONE]")
	if toolAt < 0 || finishAt <= toolAt || usageAt <= finishAt || doneAt <= usageAt {
		t.Fatalf("flattened terminal order invalid: %q", body)
	}
	if strings.Count(body, `"prompt_tokens":3`) != 1 || strings.Count(body, `"system_fingerprint":"fp_test"`) != 1 || strings.Contains(body, `"tool_calls"`) {
		t.Fatalf("combined usage duplicated or structured tools leaked: %q", body)
	}
}

func TestFlattenStreamRejectsNonStringContentBeforeCommit(t *testing.T) {
	invalidContent := `{"id":"flatten-invalid-content","object":"chat.completion.chunk","created":1,"model":"upstream","choices":[{"index":0,"delta":{"content":42,"tool_calls":[{"index":0,"id":"call-1","type":"function","function":{"name":"write_file","arguments":"{}"}}]},"finish_reason":null}]}`
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writeSSE(writer, "data: "+invalidContent+"\n\n", "data: [DONE]\n\n")
	}))
	defer server.Close()
	adapter := adapterForServer(t, server.URL, nil, nil)
	recorder := httptest.NewRecorder()
	result := adapter.AttemptWithPolicy(context.Background(), recorder,
		testTarget(server.URL, []byte("sk-invalid-content"), []byte("cipher-invalid-content")),
		streamRequest(t), connectorcontract.AttemptPolicy{SafetyIdentifier: "nbu_invalid_content", FlattenToolCalls: true})
	if result.Success || result.Committed || result.Failure != FailureUpstream || recorder.Body.Len() != 0 {
		t.Fatalf("non-string content result=%+v body=%q", result, recorder.Body.String())
	}
}

func TestFlattenStreamCallerCancelDiscardsBufferedToolCall(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		writer.WriteHeader(http.StatusOK)
		if flusher, ok := writer.(http.Flusher); ok {
			flusher.Flush()
		}
		_, _ = io.WriteString(writer, "data: "+policyToolChunk+"\n\n")
		if flusher, ok := writer.(http.Flusher); ok {
			flusher.Flush()
		}
		close(started)
		<-request.Context().Done()
	}))
	defer server.Close()
	adapter := adapterForServer(t, server.URL, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	recorder := httptest.NewRecorder()
	resultCh := make(chan AttemptResult, 1)
	go func() {
		resultCh <- adapter.AttemptWithPolicy(ctx, recorder,
			testTarget(server.URL, []byte("sk-cancel"), []byte("cipher-cancel")),
			streamRequest(t), connectorcontract.AttemptPolicy{SafetyIdentifier: "nbu_cancel", FlattenToolCalls: true})
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("upstream did not send buffered tool call")
	}
	cancel()
	select {
	case result := <-resultCh:
		if result.Success || result.Committed || result.Failure != FailureCanceled {
			t.Fatalf("caller-cancel result=%+v body=%q", result, recorder.Body.String())
		}
		if strings.Contains(recorder.Body.String(), "mx_tool") {
			t.Fatalf("cancelled buffered tool call reached caller: %q", recorder.Body.String())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("adapter did not stop after caller cancellation")
	}
}
