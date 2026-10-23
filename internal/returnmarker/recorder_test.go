package returnmarker

import (
	"context"
	"errors"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRecorderBoundsAdmissionAndDeduplicatesSources(t *testing.T) {
	m := testMarker(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := &Recorder{marker: m, ctx: ctx, queue: make(chan Event, queueCapacity), seen: make(map[string]int64)}
	request := markedRequest(t, m)
	for range 50 {
		r.Observe(request, 1, "")
	}
	if len(r.queue) != 1 {
		t.Fatal("same source was not deduplicated")
	}
	for user := int64(2); user <= queueCapacity+2; user++ {
		r.Observe(request, user, "")
	}
	if len(r.queue) != queueCapacity || len(r.seen) != queueCapacity {
		t.Fatal("queue bound changed")
	}
	for len(r.queue) > 0 {
		<-r.queue
	}
	for user := int64(queueCapacity + 1); user <= dedupCapacity; user++ {
		r.Observe(request, user, "")
		<-r.queue
	}
	r.Observe(request, dedupCapacity+1, "")
	if len(r.queue) != 0 || len(r.seen) != dedupCapacity {
		t.Fatal("dedup storage was not bounded")
	}
	// Invalid and canceled ingress must not consume admission capacity.
	r2 := &Recorder{marker: m, ctx: ctx, queue: make(chan Event, queueCapacity), seen: make(map[string]int64)}
	r2.Observe(httptest.NewRequest("GET", "https://site.example/v1/models", nil), 1, "")
	requestCtx, requestCancel := context.WithCancel(context.Background())
	requestCancel()
	r2.Observe(request.WithContext(requestCtx), 1, "")
	if len(r2.queue) != 0 {
		t.Fatal("invalid or canceled request admitted")
	}
}

func TestRecorderWriteBudgetRateAndShutdown(t *testing.T) {
	m := testMarker(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	writes := make(chan time.Time, 3)
	var count atomic.Int64
	r := &Recorder{marker: m, ctx: ctx, cancel: cancel, done: make(chan struct{}), queue: make(chan Event, queueCapacity), seen: make(map[string]int64), interval: 20 * time.Millisecond, budget: 10 * time.Millisecond}
	r.persist = func(ctx context.Context, event Event) error {
		count.Add(1)
		writes <- time.Now()
		<-ctx.Done()
		return ctx.Err()
	}
	go r.run()
	request := markedRequest(t, m)
	r.Observe(request, 1, "")
	r.Observe(request, 2, "")
	wait := func() time.Time {
		t.Helper()
		select {
		case at := <-writes:
			return at
		case <-time.After(time.Second):
			t.Fatal("worker stalled")
			return time.Time{}
		}
	}
	first, second := wait(), wait()
	if second.Sub(first) < r.interval {
		t.Fatal("write rate was not bounded")
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	_ = r.Close()
	for range 20 {
		r.Observe(request, 3, "")
	}
	if count.Load() != 2 {
		t.Fatal("shutdown admitted more work")
	}
}

func TestRecorderFailureDoesNotRetryAndConcurrentCloseIsSafe(t *testing.T) {
	m := testMarker(t)
	written := make(chan struct{}, 1)
	r := NewRecorder(context.Background(), m, func(context.Context, Event) error { written <- struct{}{}; return errors.New("write failed") })
	request := markedRequest(t, m)
	r.Observe(request, 1, "")
	select {
	case <-written:
	case <-time.After(time.Second):
		t.Fatal("write not attempted")
	}
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() {
			for range 100 {
				r.Observe(request, 1, "")
			}
			_ = r.Close()
		})
	}
	wg.Wait()
	select {
	case <-written:
		t.Fatal("failed write was retried")
	default:
	}
}
