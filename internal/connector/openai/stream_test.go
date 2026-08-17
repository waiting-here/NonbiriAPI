package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/egress"
)

func writeSSE(writer http.ResponseWriter, frames ...string) {
	writer.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	writer.WriteHeader(http.StatusOK)
	if flusher, ok := writer.(http.Flusher); ok {
		flusher.Flush()
	}
	for _, frame := range frames {
		if dataAt := strings.Index(frame, "data: "); dataAt >= 0 && strings.HasSuffix(frame, "\n\n") {
			payload := strings.TrimSuffix(frame[dataAt+len("data: "):], "\n\n")
			if compact := compactTestJSON(payload); compact != "" {
				frame = frame[:dataAt] + "data: " + compact + "\n\n"
			}
		}
		_, _ = io.WriteString(writer, frame)
		if flusher, ok := writer.(http.Flusher); ok {
			flusher.Flush()
		}
	}
}

func compactTestJSON(value string) string {
	var output bytes.Buffer
	if json.Compact(&output, []byte(value)) != nil {
		return ""
	}
	return output.String()
}

func streamRequest(t *testing.T) *ChatRequest {
	t.Helper()
	return decodeAdapterRequest(t, `{"model":"platform/model","messages":[],"stream":true}`)
}

func TestAdapterStreamValidChunksUsageAndDone(t *testing.T) {
	usageChunk := `{"id":"chatcmpl-1","object":"chat.completion.chunk","created":1700000000,"model":"upstream/model","choices":[],"usage":{"prompt_tokens":7,"completion_tokens":11,"total_tokens":18}}`
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Accept") != "text/event-stream" {
			t.Errorf("Accept=%q", request.Header.Get("Accept"))
		}
		writer.Header().Set("Set-Cookie", "upstream=private")
		writer.Header().Set("Location", "https://unsafe.example")
		writeSSE(writer,
			": ignored comment\n\n",
			"data: "+validChunk+"\n\n",
			"data: "+usageChunk+"\n\n",
			"data: [DONE]\n\n",
		)
	}))
	defer server.Close()
	adapter := adapterForServer(t, server.URL, nil, nil)
	recorder := httptest.NewRecorder()
	result := adapter.Attempt(context.Background(), recorder,
		testTarget(server.URL, []byte("sk-stream"), []byte("cipher-stream")), streamRequest(t), "nbu_safe")
	if !result.Success || !result.Committed || result.Failure != FailureNone {
		t.Fatalf("result=%+v body=%s", result, recorder.Body.String())
	}
	if !result.Usage.Present || result.Usage.TotalTokens != 18 {
		t.Fatalf("usage=%+v", result.Usage)
	}
	body := recorder.Body.String()
	if strings.Count(body, "data: ") != 3 || !strings.HasSuffix(body, "data: [DONE]\n\n") || strings.Contains(body, "ignored comment") {
		t.Fatalf("stream framing=%q", body)
	}
	if strings.Contains(body, "\ndata: {\n") || strings.Contains(body, "\n  \"") {
		t.Fatalf("chunk was not compacted to one safe data line: %q", body)
	}
	if recorder.Header().Get("Content-Type") != "text/event-stream" || recorder.Header().Get("Cache-Control") != "no-store" || recorder.Header().Get("X-Accel-Buffering") != "no" || recorder.Header().Get("Set-Cookie") != "" || recorder.Header().Get("Location") != "" {
		t.Fatalf("headers=%v", recorder.Header())
	}
}

func TestAdapterStreamStatusAndContentTypeFailBeforeCommit(t *testing.T) {
	for _, test := range []struct {
		name        string
		status      int
		contentType string
		body        string
	}{
		{"status", http.StatusServiceUnavailable, "text/event-stream", "RAW-STATUS-SECRET"},
		{"content type", http.StatusOK, "application/json", "data: " + validChunk + "\n\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.Header().Set("Content-Type", test.contentType)
				writer.WriteHeader(test.status)
				_, _ = io.WriteString(writer, test.body)
			}))
			defer server.Close()
			adapter := adapterForServer(t, server.URL, nil, nil)
			recorder := httptest.NewRecorder()
			result := adapter.Attempt(context.Background(), recorder,
				testTarget(server.URL, []byte("sk-status"), []byte("cipher-status")), streamRequest(t), "nbu_safe")
			if result.Success || result.Committed || result.Failure != FailureUpstream || recorder.Body.Len() != 0 {
				t.Fatalf("result=%+v body=%q", result, recorder.Body.String())
			}
			for _, forbidden := range []string{"RAW-STATUS-SECRET", "sk-status", "cipher-status"} {
				if strings.Contains(result.Diagnostic+recorder.Body.String(), forbidden) {
					t.Fatalf("precommit failure leaked %q", forbidden)
				}
			}
		})
	}
}

func TestAdapterStreamTerminationFailuresNeverSynthesizeDone(t *testing.T) {
	const rawSecret = "raw-upstream-error-secret"
	tests := []struct {
		name          string
		frames        []string
		wantCommitted bool
		wantError     bool
		config        func(*AdapterConfig)
	}{
		{name: "empty 200"},
		{name: "done only", frames: []string{"data: [DONE]\n\n"}},
		{name: "missing done", frames: []string{"data: " + validChunk + "\n\n"}, wantCommitted: true, wantError: true},
		{name: "bad first JSON", frames: []string{"data: {bad}\n\n"}},
		{name: "invalid UTF-8", frames: []string{"data: " + string([]byte{0xff}) + "\n\n"}},
		{name: "upstream error object", frames: []string{`data: {"error":{"message":"` + rawSecret + `"}}` + "\n\n"}},
		{name: "custom event", frames: []string{"event: error\ndata: " + validChunk + "\n\n"}},
		{name: "line limit", frames: []string{"data: " + strings.Repeat("x", 256) + "\n\n"}, config: func(config *AdapterConfig) { config.MaxSSELineBytes = 64 }},
		{name: "event limit", frames: []string{"data: " + validChunk + "\n\n"}, config: func(config *AdapterConfig) { config.MaxSSEEventBytes = 64 }},
		{name: "cumulative limit", frames: []string{":" + strings.Repeat("x", 70) + "\n:" + strings.Repeat("y", 70) + "\n"}, config: func(config *AdapterConfig) {
			config.MaxStreamBytes = 128
			config.MaxSSELineBytes = 100
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writeSSE(writer, test.frames...)
			}))
			defer server.Close()
			adapter := adapterForServer(t, server.URL, test.config, nil)
			recorder := httptest.NewRecorder()
			result := adapter.Attempt(context.Background(), recorder,
				testTarget(server.URL, []byte("sk-stream"), []byte("cipher-stream")), streamRequest(t), "nbu_safe")
			if result.Success || result.Failure != FailureUpstream || result.Committed != test.wantCommitted {
				t.Fatalf("result=%+v body=%q", result, recorder.Body.String())
			}
			body := recorder.Body.String()
			if strings.Contains(body, "[DONE]") {
				t.Fatalf("failure synthesized/forwarded terminal marker: %q", body)
			}
			if test.wantError != strings.Contains(body, `"error"`) {
				t.Fatalf("error frame presence=%v want=%v body=%q", strings.Contains(body, `"error"`), test.wantError, body)
			}
			for _, forbidden := range []string{rawSecret, "{bad}", "cipher-stream", "sk-stream"} {
				if strings.Contains(body+result.Diagnostic, forbidden) {
					t.Fatalf("stream failure leaked %q: body=%q diag=%q", forbidden, body, result.Diagnostic)
				}
			}
		})
	}
}

func TestAdapterStreamRejectsSensitiveReflectionBeforeAndAfterCommit(t *testing.T) {
	const secret = "sk-reflected-stream-secret"
	const ciphertext = "nbsec:v1:reflected-stream-cipher"
	for _, test := range []struct {
		name          string
		material      string
		afterCommit   bool
		wantCommitted bool
	}{
		{"secret first", secret, false, false},
		{"cipher first", ciphertext, false, false},
		{"secret after chunk", secret, true, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			reflected := strings.Replace(validChunk, `"ok"`, fmt.Sprintf("%q", test.material), 1)
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				frames := []string{}
				if test.afterCommit {
					frames = append(frames, "data: "+validChunk+"\n\n")
				}
				frames = append(frames, "data: "+reflected+"\n\n", "data: [DONE]\n\n")
				writeSSE(writer, frames...)
			}))
			defer server.Close()
			adapter := adapterForServer(t, server.URL, nil, nil)
			recorder := httptest.NewRecorder()
			result := adapter.Attempt(context.Background(), recorder,
				testTarget(server.URL, []byte(secret), []byte(ciphertext)), streamRequest(t), "nbu_safe")
			if result.Success || result.Failure != FailureUpstream || result.Committed != test.wantCommitted {
				t.Fatalf("result=%+v body=%q", result, recorder.Body.String())
			}
			if strings.Contains(recorder.Body.String(), test.material) || strings.Contains(result.Diagnostic, test.material) || strings.Contains(recorder.Body.String(), "[DONE]") {
				t.Fatalf("sensitive stream leaked or terminated successfully: %q / %q", recorder.Body.String(), result.Diagnostic)
			}
			if test.afterCommit && !strings.Contains(recorder.Body.String(), `"error"`) {
				t.Fatalf("post-commit rejection lacked safe error frame: %q", recorder.Body.String())
			}
		})
	}
}

type signalingRecorder struct {
	*httptest.ResponseRecorder
	wrote chan struct{}
	once  sync.Once
}

func (r *signalingRecorder) Write(body []byte) (int, error) {
	n, err := r.ResponseRecorder.Write(body)
	if n > 0 {
		r.once.Do(func() { close(r.wrote) })
	}
	return n, err
}

func TestAdapterStreamClientCancelPropagatesAndReleasesPermit(t *testing.T) {
	started := make(chan struct{})
	canceled := make(chan struct{})
	var requestCount atomic.Int32
	var startOnce, cancelOnce sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if requestCount.Add(1) == 1 {
			writeSSE(writer, "data: "+validChunk+"\n\n")
			startOnce.Do(func() { close(started) })
			<-request.Context().Done()
			cancelOnce.Do(func() { close(canceled) })
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, validCompletion)
	}))
	defer server.Close()
	adapter := adapterForServer(t, server.URL, nil, func(options *egress.StackOptions) {
		options.RequestTimeout = 30 * time.Second
		options.Concurrency = egress.ConcurrencyLimits{Global: 1, PerEndpoint: 1}
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan AttemptResult, 1)
	recorder := &signalingRecorder{ResponseRecorder: httptest.NewRecorder(), wrote: make(chan struct{})}
	request := streamRequest(t)
	go func() {
		done <- adapter.Attempt(ctx, recorder,
			testTarget(server.URL, []byte("sk-cancel"), []byte("cipher-cancel")), request, "nbu_safe")
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("stream did not start")
	}
	select {
	case <-recorder.wrote:
	case <-time.After(time.Second):
		t.Fatal("first stream frame was not committed")
	}
	cancel()
	select {
	case result := <-done:
		if result.Failure != FailureCanceled || !result.Committed || result.Success {
			t.Fatalf("cancel result=%+v body=%q", result, recorder.Body.String())
		}
	case <-time.After(time.Second):
		t.Fatal("adapter did not return after client cancellation")
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("upstream did not observe cancellation")
	}
	if strings.Contains(recorder.Body.String(), `"error"`) || strings.Contains(recorder.Body.String(), "[DONE]") {
		t.Fatalf("client cancellation wrote a terminal/error frame: %q", recorder.Body.String())
	}

	fastRequest := decodeAdapterRequest(t, `{"model":"platform/model","stream":false}`)
	fastResult := adapter.Attempt(context.Background(), httptest.NewRecorder(),
		testTarget(server.URL, []byte("sk-fast"), []byte("cipher-fast")), fastRequest, "nbu_safe")
	if !fastResult.Success || requestCount.Load() != 2 {
		t.Fatalf("permit was not released: result=%+v requests=%d", fastResult, requestCount.Load())
	}
}

func TestAdapterStreamBackpressureDeadlineCancelsUpstream(t *testing.T) {
	upstreamCanceled := make(chan struct{})
	var cancelOnce sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writeSSE(writer, "data: "+validChunk+"\n\n")
		<-request.Context().Done()
		cancelOnce.Do(func() { close(upstreamCanceled) })
	}))
	defer server.Close()
	adapter := adapterForServer(t, server.URL, func(config *AdapterConfig) {
		config.StreamWriteTimeout = 30 * time.Millisecond
	}, nil)
	writer := &deadlineWriter{header: make(http.Header)}
	result := adapter.Attempt(context.Background(), writer,
		testTarget(server.URL, []byte("sk-backpressure"), []byte("cipher-backpressure")), streamRequest(t), "nbu_safe")
	if result.Failure != FailureSink || result.Committed || !result.SinkFailed {
		t.Fatalf("backpressure result=%+v", result)
	}
	select {
	case <-upstreamCanceled:
	case <-time.After(time.Second):
		t.Fatal("closing backpressured stream did not cancel upstream")
	}
}

func TestAdapterStreamAbnormalEOFIsFailure(t *testing.T) {
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		hijacker, ok := writer.(http.Hijacker)
		if !ok {
			t.Error("hijacking unsupported")
			return
		}
		conn, buffer, err := hijacker.Hijack()
		if err != nil {
			t.Errorf("hijack: %v", err)
			return
		}
		defer conn.Close()
		payload := "data: " + compactTestJSON(validChunk) + "\n\n"
		_, _ = fmt.Fprintf(buffer, "HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\nTransfer-Encoding: chunked\r\n\r\n%x\r\n%s\r\n", len(payload)+64, payload)
		_ = buffer.Flush()
	}))
	server.Start()
	defer server.Close()
	adapter := adapterForServer(t, server.URL, nil, nil)
	recorder := httptest.NewRecorder()
	result := adapter.Attempt(context.Background(), recorder,
		testTarget(server.URL, []byte("sk-abnormal"), []byte("cipher-abnormal")), streamRequest(t), "nbu_safe")
	if result.Success || result.Failure != FailureUpstream {
		t.Fatalf("abnormal EOF result=%+v body=%q", result, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "[DONE]") {
		t.Fatalf("abnormal EOF synthesized DONE: %q", recorder.Body.String())
	}
}
