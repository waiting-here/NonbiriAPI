package egress

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestResponseWaitDefaultsAndTimeoutClassification(t *testing.T) {
	stack, err := NewStack(StackOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if stack.responseHeaderTimeout != 900*time.Second || stack.requestTimeout != 1200*time.Second {
		t.Fatalf("timeouts headers=%v total=%v", stack.responseHeaderTimeout, stack.requestTimeout)
	}
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer server.Close()
	client := newLoopbackClient(t, server.URL, func(o *StackOptions) { o.ResponseHeaderTimeout = 40 * time.Millisecond })
	request, _ := http.NewRequest(http.MethodGet, server.URL, nil)
	_, err = client.Do(request)
	if !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		t.Fatalf("header timeout lost original cause: %v", err)
	}
}

func TestHeaderWaitDoesNotLimitOutputAfterResponseStarts(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		select {
		case <-time.After(150 * time.Millisecond):
			_, _ = io.WriteString(w, "finished")
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	client := newLoopbackClient(t, server.URL, func(o *StackOptions) { o.ResponseHeaderTimeout = 50 * time.Millisecond; o.RequestTimeout = time.Second })
	request, _ := http.NewRequest(http.MethodGet, server.URL, nil)
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || string(body) != "finished" {
		t.Fatalf("output after header wait window: %q %v", body, err)
	}
}
