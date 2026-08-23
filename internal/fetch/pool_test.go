package fetch

import (
	"context"
	"errors"
	"sync"
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
	p := newPool(context.Background(), 1, 2, 8, b.run)
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
	p := newPool(context.Background(), 1, 2, 8, b.run)
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
	p := newPool(context.Background(), 1, 2, 8, b.run)

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
	p := newPool(ctx, 1, 2, 8, b.run)
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
	p := newPool(ctx, 1, 2, 8, func(context.Context, jobKey) {})
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

// TestPoolPerUserCapAndRoundRobin asserts the per-user fairness and bound
// contract: a single user submitting far more combos than its bound occupies
// at most its own cap (8), the overflow is busy, and a second user's first
// task still queues and gets an execution turn via round-robin (not starved
// behind all of the first user's queued jobs). A single worker makes the
// dispatch order deterministic.
func TestPoolPerUserCapAndRoundRobin(t *testing.T) {
	const userA, userB int64 = 1, 2
	var mu sync.Mutex
	order := []int64{} // userIDs in execution-start order
	running := atomic.Int64{}
	release := make(chan struct{})
	run := func(ctx context.Context, job jobKey) {
		mu.Lock()
		order = append(order, job.userID)
		mu.Unlock()
		running.Add(1)
		select {
		case <-ctx.Done():
		case <-release:
		}
		running.Add(-1)
	}
	p := newPool(context.Background(), 1, 32, 8, run)
	defer p.Close()

	// User A submits 36 distinct combos: at most 8 are admitted (per-user
	// pending+running cap), the remaining 28 are busy.
	busy := 0
	for i := 0; i < 36; i++ {
		err := p.Submit(jobKey{userID: userA, endpointID: int64(i + 1), keyID: 1})
		switch {
		case err == nil:
		case errors.Is(err, ErrPoolBusy):
			busy++
		default:
			t.Fatalf("submit A[%d]: %v", i, err)
		}
	}
	if got := p.pendingForUser(userA); got != 8 {
		t.Fatalf("user A inUse = %d, want 8", got)
	}
	if busy != 28 {
		t.Fatalf("busy = %d, want 28", busy)
	}
	if got := p.Pending(); got != 8 {
		t.Fatalf("pending = %d, want 8", got)
	}
	// Wait until the single worker is running A's first job.
	waitForCond(t, func() bool { return running.Load() == 1 }, "first job running")

	// User B submits one combo while A saturates only its own bound: B must
	// still be admitted (global queue has room and B is under its own bound).
	if err := p.Submit(jobKey{userID: userB, endpointID: 1, keyID: 1}); err != nil {
		t.Fatalf("submit B: %v", err)
	}
	if got := p.pendingForUser(userB); got != 1 {
		t.Fatalf("user B inUse = %d, want 1", got)
	}
	if got := p.pendingForUser(userA); got != 8 {
		t.Fatalf("user A inUse after B = %d, want 8 (B does not steal A's slot)", got)
	}

	close(release)
	waitForCond(t, func() bool { return p.Pending() == 0 }, "pool drain")

	mu.Lock()
	counts := map[int64]int{}
	bIndex := -1
	for i, u := range order {
		counts[u]++
		if u == userB && bIndex == -1 {
			bIndex = i
		}
	}
	last := len(order) - 1
	mu.Unlock()
	if counts[userA] != 8 {
		t.Errorf("user A executed %d, want 8", counts[userA])
	}
	if counts[userB] != 1 {
		t.Errorf("user B executed %d, want 1", counts[userB])
	}
	if bIndex == -1 {
		t.Fatalf("user B never executed (starved)")
	}
	if bIndex >= last {
		t.Fatalf("user B executed last (index %d of %d): round-robin did not interleave B before draining A", bIndex, last)
	}
}

// TestPoolMultiUserFairnessAndBounds exercises dedup, the per-user and global
// bounds, and execution fairness across several users; it asserts no goroutine
// leak (all counts return to zero) and no starvation (each user runs its full
// admitted set). Run under -race -shuffle -count=10 to cover dispatch/close
// interleavings.
func TestPoolMultiUserFairnessAndBounds(t *testing.T) {
	const (
		users      = 4
		perUserCap = 4
		queuedCap  = 16
		workers    = 4
	)
	var mu sync.Mutex
	ran := map[int64]int{}
	running := atomic.Int64{}
	release := make(chan struct{})
	run := func(ctx context.Context, job jobKey) {
		running.Add(1)
		mu.Lock()
		ran[job.userID]++
		mu.Unlock()
		select {
		case <-ctx.Done():
		case <-release:
		}
		running.Add(-1)
	}
	p := newPool(context.Background(), workers, queuedCap, perUserCap, run)
	defer p.Close()

	type result struct{ admitted, busy int }
	results := make(map[int64]result)
	for u := int64(1); u <= users; u++ {
		admitted, busy := 0, 0
		for i := 0; i < 6; i++ {
			err := p.Submit(jobKey{userID: u, endpointID: int64(i + 1), keyID: 1})
			switch {
			case err == nil:
				admitted++
			case errors.Is(err, ErrPoolBusy):
				busy++
			default:
				t.Fatalf("submit user %d combo %d: %v", u, i, err)
			}
		}
		// Duplicate combos merge (nil) without creating new work or new slots.
		for i := 0; i < 2; i++ {
			if err := p.Submit(jobKey{userID: u, endpointID: 1, keyID: 1}); err != nil {
				t.Fatalf("dup submit user %d: %v", u, err)
			}
		}
		results[u] = result{admitted, busy}
	}
	for u := int64(1); u <= users; u++ {
		r := results[u]
		if r.admitted != perUserCap {
			t.Errorf("user %d admitted = %d, want %d", u, r.admitted, perUserCap)
		}
		if r.busy != 6-perUserCap {
			t.Errorf("user %d busy = %d, want %d", u, r.busy, 6-perUserCap)
		}
		if got := p.pendingForUser(u); got != perUserCap {
			t.Errorf("user %d inUse = %d, want %d", u, got, perUserCap)
		}
	}
	if got := p.Pending(); got != users*perUserCap {
		t.Errorf("pending = %d, want %d", got, users*perUserCap)
	}
	if got := p.queuedCount(); got > queuedCap {
		t.Errorf("queued = %d, exceeds cap %d", got, queuedCap)
	}

	close(release)
	waitForCond(t, func() bool { return p.Pending() == 0 }, "pool drain")

	mu.Lock()
	total := 0
	for u := int64(1); u <= users; u++ {
		if ran[u] != perUserCap {
			t.Errorf("user %d executed %d, want %d (starvation)", u, ran[u], perUserCap)
		}
		total += ran[u]
	}
	mu.Unlock()
	if total != users*perUserCap {
		t.Errorf("total executed = %d, want %d", total, users*perUserCap)
	}
	if got := running.Load(); got != 0 {
		t.Errorf("running = %d after drain, want 0 (goroutine leak)", got)
	}
}

// TestPoolCloseRaceSubmitAndRun hammers Submit from many goroutines while Close
// runs concurrently, then verifies no panic, no data race, idempotent Close,
// and that every counter converges to zero. Repeated with -race -shuffle.
func TestPoolCloseRaceSubmitAndRun(t *testing.T) {
	run := func(ctx context.Context, job jobKey) {
		// Honor cancellation so Close joins promptly; never block indefinitely.
		<-ctx.Done()
	}
	p := newPool(context.Background(), 4, 32, 8, run)

	const submitters = 32
	var wg sync.WaitGroup
	wg.Add(submitters)
	for i := 0; i < submitters; i++ {
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				_ = p.Submit(jobKey{userID: int64(i%4 + 1), endpointID: int64(i + 1), keyID: int64(j + 1)})
			}
		}(i)
	}
	// Let some submits land, then close mid-flight.
	time.Sleep(2 * time.Millisecond)
	p.Close()
	wg.Wait()

	if got := p.Pending(); got != 0 {
		t.Errorf("pending after close = %d, want 0", got)
	}
	if got := p.queuedCount(); got != 0 {
		t.Errorf("queued after close = %d, want 0", got)
	}
	for u := int64(1); u <= 4; u++ {
		if got := p.pendingForUser(u); got != 0 {
			t.Errorf("user %d inUse after close = %d, want 0", u, got)
		}
	}
	// Idempotent close.
	p.Close()
}

// TestPoolParentCancelResetsCounts asserts that cancelling the parent context
// (without Close) drops queued jobs, cancels the running job, and converges
// every counter to zero.
func TestPoolParentCancelResetsCounts(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	run := func(c context.Context, job jobKey) { <-c.Done() }
	p := newPool(ctx, 2, 32, 8, run)
	defer p.Close()

	for i := 0; i < 8; i++ {
		if err := p.Submit(jobKey{userID: 1, endpointID: int64(i + 1), keyID: 1}); err != nil {
			t.Fatalf("submit %d: %v", i, err)
		}
	}
	waitForCond(t, func() bool { return p.Pending() == 8 }, "jobs admitted")
	cancel()
	waitForCond(t, func() bool { return p.Pending() == 0 && p.queuedCount() == 0 }, "drain after parent cancel")
	if got := p.pendingForUser(1); got != 0 {
		t.Errorf("user inUse after parent cancel = %d, want 0", got)
	}
}
