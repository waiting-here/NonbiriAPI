package egress

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestClientErrorRetainsReceivedStatusAndHeaders(t *testing.T) {
	for _, class := range []FailureClass{FailureNetwork, FailurePlatform, FailureCanceled, FailureTimeout} {
		t.Run(string(class), func(t *testing.T) {
			var hits atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				w.Header().Set("Retry-After", "60")
				http.Redirect(w, r, "/unfollowed", http.StatusFound)
			}))
			defer server.Close()
			client := newLoopbackClient(t, server.URL, nil)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			// http.Client returns response+error when its redirect callback rejects.
			// Exercise each existing error classification without discarding that response.
			client.httpClient.CheckRedirect = func(*http.Request, []*http.Request) error {
				switch class {
				case FailurePlatform:
					return executionFailure(FailurePlatform, errors.New("policy refusal"))
				case FailureCanceled:
					cancel()
					return context.Canceled
				case FailureTimeout:
					return context.DeadlineExceeded
				default:
					return io.ErrUnexpectedEOF
				}
			}
			request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
			if err != nil {
				t.Fatal(err)
			}
			response, err := client.Do(request)
			if ExecutionFailure(err) != class || response == nil || response.StatusCode != 302 || response.Header.Get("Retry-After") != "60" {
				t.Fatalf("class=%s response=%+v err=%v", ExecutionFailure(err), response, err)
			}
			if n, err := response.Body.Read(make([]byte, 1)); n != 0 || err == nil {
				t.Fatal("error body was not closed", n, err)
			}
			if hits.Load() != 1 {
				t.Fatal("redirect was followed", hits.Load())
			}
		})
	}
}
