package fetch

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// runBlocker is a run function that blocks until released, for concurrency
// tests. It records invocations and the context cancellation state.
type runBlocker struct {
	release chan struct{}
	runs    atomic.Int64
	cancels atomic.Int64
}

func newRunBlocker() *runBlocker {
	return &runBlocker{release: make(chan struct{})}
}

func (b *runBlocker) run(ctx context.Context, job jobKey) {
	b.runs.Add(1)
	select {
	case <-ctx.Done():
		b.cancels.Add(1)
	case <-b.release:
	}
}

func (b *runBlocker) releaseAll() {
	close(b.release)
}

// TestPoolDedupMergesSameCombo asserts submitting the same combo twice runs
// the job once (in-flight dedup), and a repeat submit while running returns
// nil.
func TestPoolDedupMergesSameCombo(t *testing.T) {
	b := newRunBlocker()
	p := newPool(context.Background(), 1, 2, b.run)
	defer p.Close()

	job := jobKey{userID: 1, endpointID: 2, keyID: 3}
	if err := p.Submit(job); err != nil {
		t.Fatalf("first submit: %v", err)
	}
	if err := p.Submit(job); err != nil {
		t.Fatalf("merged submit: %v", err)
	}
	if got := p.Pending(); got != 1 {
		t.Fatalf("pending = %d, want 1", got)
	}
	// Wait until the job is running, then merge again while in flight.
	deadline := time.Now().Add(3 * time.Second)
	for b.runs.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	if err := p.Submit(job); err != nil {
		t.Fatalf("in-flight merge: %v", err)
	}
	if err := p.Submit(job); err != nil {
		t.Fatalf("in-flight merge: %v", err)
	}
	b.releaseAll()
	waitForCond(t, func() bool { return p.Pending() == 0 }, "pool drain")
	if got := b.runs.Load(); got != 1 {
		t.Errorf("runs = %d, want 1", got)
	}
}

// TestPoolBoundedWorkersAndQueue asserts at most workers jobs run
// concurrently and the queue admits at most queueSize waiting jobs: the
// (workers+queueSize+1)-th submit is busy.
func TestPoolBoundedWorkersAndQueue(t *testing.T) {
	b := newRunBlocker()
	p := newPool(context.Background(), 1, 2, b.run)
	defer p.Close()

	var jobs []jobKey
	for i := 0; i < 3; i++ {
		jobs = append(jobs, jobKey{userID: 1, endpointID: int64(i + 1), keyID: 1})
	}
	if err := p.Submit(jobs[0]); err != nil {
		t.Fatalf("submit %v: %v", jobs[0], err)
	}
	// Wait until the worker has picked up the first job, so the queue really
	// holds the remaining capacity.
	waitForCond(t, func() bool { return b.runs.Load() == 1 }, "first job running")
	for _, j := range jobs[1:] {
		if err := p.Submit(j); err != nil {
			t.Fatalf("submit %v: %v", j, err)
		}
	}
	// One running, two queued; the fourth distinct combo is busy.
	full := jobKey{userID: 1, endpointID: 99, keyID: 1}
	if err := p.Submit(full); !errors.Is(err, ErrPoolBusy) {
		t.Fatalf("submit over capacity: err=%v, want busy", err)
	}
	if got := p.Pending(); got != 3 {
		t.Errorf("pending = %d, want 3", got)
	}
	b.releaseAll()
	waitForCond(t, func() bool { return p.Pending() == 0 }, "pool drain")
	if got := b.runs.Load(); got != 3 {
		t.Errorf("runs = %d, want 3", got)
	}
}

// TestPoolCloseSemantics asserts Close cancels the in-flight job context,
// drops queued jobs without running them, rejects new submits, and is
// idempotent.
func TestPoolCloseSemantics(t *testing.T) {
	b := newRunBlocker()
	p := newPool(context.Background(), 1, 2, b.run)

	// j1 runs (blocked), j2 waits in the queue.
	j1 := jobKey{userID: 1, endpointID: 1, keyID: 1}
	j2 := jobKey{userID: 1, endpointID: 2, keyID: 1}
	if err := p.Submit(j1); err != nil {
		t.Fatalf("submit j1: %v", err)
	}
	if err := p.Submit(j2); err != nil {
		t.Fatalf("submit j2: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for b.runs.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}

	p.Close()
	// In-flight job observed cancellation; queued job never ran.
	deadline = time.Now().Add(3 * time.Second)
	for b.cancels.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	if b.cancels.Load() == 0 {
		t.Fatalf("in-flight job did not observe cancellation")
	}
	if got := b.runs.Load(); got != 1 {
		t.Errorf("runs = %d, want 1 (queued job must not run)", got)
	}
	if got := p.Pending(); got != 0 {
		t.Errorf("pending after close = %d, want 0", got)
	}
	// No new submits after close.
	if err := p.Submit(jobKey{userID: 9, endpointID: 9, keyID: 9}); !errors.Is(err, ErrPoolClosed) {
		t.Errorf("submit after close: err=%v, want closed", err)
	}
	// Idempotent close.
	p.Close()
}

// TestPoolParentCancellationDropsQueued asserts cancelling the parent context
// (without Close) also drains queued jobs without running them.
func TestPoolParentCancellationDropsQueued(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	b := newRunBlocker()
	p := newPool(ctx, 1, 2, b.run)
	defer p.Close()

	j1 := jobKey{userID: 1, endpointID: 1, keyID: 1}
	j2 := jobKey{userID: 1, endpointID: 2, keyID: 1}
	if err := p.Submit(j1); err != nil {
		t.Fatalf("submit j1: %v", err)
	}
	if err := p.Submit(j2); err != nil {
		t.Fatalf("submit j2: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for b.runs.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	cancel()
	waitForCond(t, func() bool { return p.Pending() == 0 }, "pool drain after parent cancel")
	if got := b.runs.Load(); got != 1 {
		t.Errorf("runs = %d, want 1", got)
	}
}

// TestPoolContextCancelledSubmit asserts submitting after the parent context
// was cancelled returns the context error and leaves no phantom mark.
func TestPoolContextCancelledSubmit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	p := newPool(ctx, 1, 2, func(context.Context, jobKey) {})
	defer p.Close()
	cancel()

	job := jobKey{userID: 1, endpointID: 1, keyID: 1}
	if err := p.Submit(job); err == nil {
		t.Fatalf("submit after parent cancel: err=nil, want context error")
	}
	if got := p.Pending(); got != 0 {
		t.Errorf("pending = %d, want 0", got)
	}
}

func waitForCond(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	if !cond() {
		t.Fatalf("timeout waiting for %s", what)
	}
}
