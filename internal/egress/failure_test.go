package egress

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"sync"
	"testing"
	"time"
)

func TestExecutionFailureSeparatesQueueTimeoutAndOutboundTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer server.Close()
	client := newLoopbackClient(t, server.URL, func(o *StackOptions) { o.Concurrency = ConcurrencyLimits{Global: 1, PerEndpoint: 1} })
	permit, err := client.gate.Acquire(context.Background(), client.baseURL)
	if err != nil {
		t.Fatal(err)
	}
	queued, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	request, _ := http.NewRequestWithContext(queued, http.MethodGet, server.URL, nil)
	_, err = client.Do(request)
	cancel()
	permit.Release()
	if !errors.Is(err, context.DeadlineExceeded) || ExecutionFailure(err) != FailurePlatform {
		t.Fatalf("queue %v %s", err, ExecutionFailure(err))
	}
	outbound, stop := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer stop()
	request, _ = http.NewRequestWithContext(outbound, http.MethodGet, server.URL, nil)
	_, err = client.Do(request)
	if !errors.Is(err, context.DeadlineExceeded) || ExecutionFailure(err) != FailureTimeout {
		t.Fatalf("outbound %v %s", err, ExecutionFailure(err))
	}
}
func TestExecutionFailureBlockedDNSIsPlatform(t *testing.T) {
	p, _ := NewEgressPolicy(nil)
	p.resolver = &staticResolver{addresses: []netip.Addr{netip.MustParseAddr("127.0.0.1")}}
	_, err := p.dialContext("https", "upstream.example", "443")(context.Background(), "tcp", "upstream.example:443")
	if ExecutionFailure(err) != FailurePlatform {
		t.Fatalf("%v %s", err, ExecutionFailure(err))
	}
}

type closeEOFBody struct {
	entered, closed chan struct{}
	once            sync.Once
}

func (b *closeEOFBody) Read([]byte) (int, error) { close(b.entered); <-b.closed; return 0, io.EOF }
func (b *closeEOFBody) Close() error             { b.once.Do(func() { close(b.closed) }); return nil }
func TestManagedBodyCancellationCannotBecomeCleanEOF(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	body := &closeEOFBody{entered: make(chan struct{}), closed: make(chan struct{})}
	managed := newManagedResponseBody(body, 1024, ctx, cancel, func() {})
	done := make(chan error, 1)
	go func() { _, err := managed.Read(make([]byte, 32)); done <- err }()
	<-body.entered
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) || errors.Is(err, io.EOF) || ExecutionFailure(err) != FailureCanceled {
			t.Fatalf("canceled read=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled read did not close")
	}
}
