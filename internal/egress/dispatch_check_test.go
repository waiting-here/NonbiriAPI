package egress

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestDispatchCheckRevalidatesAfterGlobalWait(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer server.Close()
	client := newLoopbackClient(t, server.URL, func(o *StackOptions) { o.Concurrency = ConcurrencyLimits{Global: 1, PerEndpoint: 1} })
	permit, err := client.gate.Acquire(context.Background(), client.baseURL)
	if err != nil {
		t.Fatal(err)
	}
	denied := errors.New("authorization revoked")
	var revoked atomic.Bool
	ctx := WithDispatchCheck(context.Background(), func(context.Context) error {
		if revoked.Load() {
			return denied
		}
		return nil
	})
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
	done := make(chan error, 1)
	go func() {
		response, err := client.Do(request)
		if response != nil && response.Body != nil {
			response.Body.Close()
		}
		done <- err
	}()
	revoked.Store(true)
	permit.Release()
	select {
	case err = <-done:
		if !errors.Is(err, denied) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("dispatch check blocked")
	}
	current, _, _ := client.gate.SampleGlobal()
	if calls.Load() != 0 || current != 0 {
		t.Fatal("revoked request sent or permit leaked", calls.Load(), current)
	}
}
