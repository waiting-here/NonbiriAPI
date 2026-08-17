package forward

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/egress"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
)

func forwardChunk(content string) string {
	return `{"id":"chatcmpl-stream","object":"chat.completion.chunk","created":1,"model":"upstream/model","choices":[{"index":0,"delta":{"content":` + strconv.Quote(content) + `}}]}`
}

func serveForwardSSE(writer http.ResponseWriter, frames ...string) {
	writer.Header().Set("Content-Type", "text/event-stream")
	writer.WriteHeader(http.StatusOK)
	writer.(http.Flusher).Flush()
	for _, frame := range frames {
		_, _ = io.WriteString(writer, frame)
		writer.(http.Flusher).Flush()
	}
}

func TestForwardStreamingHTTPContract(t *testing.T) {
	usage := `{"id":"chatcmpl-stream","object":"chat.completion.chunk","created":1,"model":"upstream/model","choices":[],"usage":{"prompt_tokens":4,"completion_tokens":6,"total_tokens":10}}`
	tests := []struct {
		name       string
		frames     []string
		wantStatus int
		wantCode   string
		wantDone   bool
		wantSSEErr bool
	}{
		{
			name: "valid", frames: []string{"data: " + forwardChunk("hello") + "\n\n", "data: " + usage + "\n\n", "data: [DONE]\n\n"},
			wantStatus: http.StatusOK, wantDone: true,
		},
		{name: "empty before commit", wantStatus: http.StatusBadGateway, wantCode: httperr.CodeUpstream},
		{name: "invalid first event", frames: []string{"data: {bad}\n\n"}, wantStatus: http.StatusBadGateway, wantCode: httperr.CodeUpstream},
		{
			name: "clean EOF after commit", frames: []string{"data: " + forwardChunk("partial") + "\n\n"},
			wantStatus: http.StatusOK, wantSSEErr: true,
		},
		{
			name: "error object after commit", frames: []string{"data: " + forwardChunk("partial") + "\n\n", `data: {"error":{"message":"RAW-STREAM-ERROR"}}` + "\n\n"},
			wantStatus: http.StatusOK, wantSSEErr: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				serveForwardSSE(writer, test.frames...)
			}))
			defer upstream.Close()
			var usageRecords []UsageRecord
			fixture := newForwardFixture(t, []string{upstream.URL}, Hooks{Usage: func(record UsageRecord) {
				usageRecords = append(usageRecords, record)
			}}, nil, nil)
			user := fixture.addUser(t, "stream-"+strings.ReplaceAll(test.name, " ", "-"))
			_ = fixture.addRoute(t, user.id, upstream.URL, "p", "m", "upstream/model", "sk-stream-handler", 0)
			response := performCaller(fixture.handler, callerRequest(http.MethodPost, "/v1/chat/completions", user.key,
				`{"model":"p/m","messages":[],"stream":true}`))
			if response.Code != test.wantStatus {
				t.Fatalf("status=%d want=%d body=%q", response.Code, test.wantStatus, response.Body.String())
			}
			body := response.Body.String()
			if test.wantCode != "" && responseCode(t, response) != test.wantCode {
				t.Fatalf("error body=%s", body)
			}
			if strings.Contains(body, "RAW-STREAM-ERROR") || strings.Contains(body, "sk-stream-handler") {
				t.Fatalf("stream leaked upstream/secret text: %q", body)
			}
			if strings.Contains(body, "data: [DONE]") != test.wantDone {
				t.Fatalf("DONE presence mismatch body=%q", body)
			}
			if test.wantStatus == http.StatusOK && strings.Contains(body, `"error"`) != test.wantSSEErr {
				t.Fatalf("SSE error presence mismatch body=%q", body)
			}
			if test.wantDone {
				if len(usageRecords) != 1 || !usageRecords[0].Usage.Present || usageRecords[0].Usage.TotalTokens != 10 || usageRecords[0].UsageUnknown {
					t.Fatalf("usage records=%+v", usageRecords)
				}
			}
			if test.wantSSEErr {
				if len(usageRecords) != 1 || !usageRecords[0].UsageUnknown {
					t.Fatalf("truncated stream usage record=%+v", usageRecords)
				}
			}
		})
	}
}

func TestForwardStreamingClientCancellationAndConcurrentPermitRelease(t *testing.T) {
	starts := make(chan struct{}, 2)
	upstreamCanceled := make(chan struct{})
	release := make(chan struct{})
	var canceledOnce sync.Once
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		serveForwardSSE(writer, "data: "+forwardChunk("partial")+"\n\n")
		starts <- struct{}{}
		select {
		case <-request.Context().Done():
			canceledOnce.Do(func() { close(upstreamCanceled) })
		case <-release:
		}
	}))
	defer func() {
		close(release)
		upstream.Close()
	}()
	fixture := newForwardFixture(t, []string{upstream.URL}, Hooks{}, nil, func(options *egress.StackOptions) {
		options.RequestTimeout = 30 * time.Second
		options.Concurrency = egress.ConcurrencyLimits{Global: 1, PerEndpoint: 1}
	})
	user := fixture.addUser(t, "stream-cancel")
	_ = fixture.addRoute(t, user.id, upstream.URL, "p", "m", "upstream/model", "sk-cancel-handler", 0)

	request := callerRequest(http.MethodPost, "/v1/chat/completions", user.key, `{"model":"p/m","stream":true}`)
	ctx, cancel := context.WithCancel(request.Context())
	request = request.WithContext(ctx)
	recorder := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		fixture.handler.ServeHTTP(recorder, request)
		close(done)
	}()
	select {
	case <-starts:
	case <-time.After(time.Second):
		t.Fatal("upstream stream did not start")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("handler did not return after cancellation")
	}
	select {
	case <-upstreamCanceled:
	case <-time.After(time.Second):
		t.Fatal("upstream did not observe caller cancellation")
	}
	if strings.Contains(recorder.Body.String(), "[DONE]") || strings.Contains(recorder.Body.String(), `"error"`) {
		t.Fatalf("canceled stream wrote terminal/error frame: %q", recorder.Body.String())
	}

	// A fresh stream can acquire the sole permit after cancellation. It is
	// canceled immediately as well; the important assertion is that it starts.
	secondRequest := callerRequest(http.MethodPost, "/v1/chat/completions", user.key, `{"model":"p/m","stream":true}`)
	secondCtx, secondCancel := context.WithCancel(secondRequest.Context())
	secondRequest = secondRequest.WithContext(secondCtx)
	secondDone := make(chan struct{})
	go func() {
		fixture.handler.ServeHTTP(httptest.NewRecorder(), secondRequest)
		close(secondDone)
	}()
	select {
	case <-starts:
	case <-time.After(time.Second):
		secondCancel()
		t.Fatal("second request could not acquire the released permit")
	}
	secondCancel()
	select {
	case <-secondDone:
	case <-time.After(time.Second):
		t.Fatal("second request did not release the sole permit")
	}
}
