package fetch

import (
	"context"
	"errors"
	"sync"
)

var (
	// ErrPoolBusy reports that the bounded refresh queue or a per-user bound is
	// full. Callers map it to the rate_limited contract code; the automatic
	// fetch hook logs it and drops the fetch rather than spawning unbounded
	// work.
	ErrPoolBusy = errors.New("fetch pool is busy")
	// ErrPoolClosed reports submission after the pool was closed (process
	// shutdown). Callers map it to service_unavailable.
	ErrPoolClosed = errors.New("fetch pool is closed")
)

// jobKey identifies one (Endpoint, Key) combo owned by one user. It is the
// dedup key: only one fetch may be pending or in flight per combo, so a
// double manual refresh or a hook racing a refresh cannot spawn duplicate
// goroutines.
type jobKey struct {
	userID     int64
	endpointID int64
	keyID      int64
}

// userBucket holds one user's queued (pending) fetch jobs and the count of
// pending+running jobs for that user (inUse), which enforces the per-user
// bound. A bucket exists only while a user has at least one pending or
// running job; the round-robin ring references a user only while it has
// pending work to dispatch.
type userBucket struct {
	pending []jobKey
	inUse   int // pending + running for this user
}

// pool runs bounded asynchronous fetch jobs with per-user fairness. A fixed
// number of workers drain jobs chosen round-robin across users that have
// pending work, so one user cannot starve another even when its own jobs fill
// the worker pool. Each combo has at most one queued or running job (dedup).
//
// Bounds:
//   - workers: concurrent executions.
//   - queuedCap: total pending (not yet dispatched) jobs across all users.
//   - perUserCap: pending+running jobs per user.
//
// The pool owns a base context: Close cancels it (propagating cancellation
// into in-flight egress requests), drops queued jobs, and waits for workers
// to finish. Close is idempotent and resets every counter to zero. Cancelling
// the parent context (without Close) triggers the same shutdown via a single
// lifecycle watcher goroutine, so queued jobs are dropped and running jobs
// observe cancellation. Submit never blocks and never spawns goroutines.
type pool struct {
	ctx    context.Context
	cancel context.CancelFunc

	mu   sync.Mutex
	cond *sync.Cond

	inflight map[jobKey]struct{}
	buckets  map[int64]*userBucket
	rrOrder  []int64 // users with >=1 pending job, in round-robin dispatch order
	rrIdx    int     // next index in rrOrder to dispatch from
	queued   int     // total pending (not running) across all users
	closed   bool

	workers    int
	queuedCap  int
	perUserCap int
	run        func(context.Context, jobKey)

	wg          sync.WaitGroup // workers only
	watcherDone chan struct{}
}

// newPool starts workers (clamped >=1) draining a bounded queue of capacity
// queuedCap (clamped >=1) with a per-user pending+running cap of perUserCap
// (clamped >=1). run is invoked for every executed job with the pool's base
// context; it must honor context cancellation. The returned pool must be
// Closed so queued work is dropped, in-flight fetches observe cancellation,
// and the watcher goroutine exits.
func newPool(ctx context.Context, workers, queuedCap, perUserCap int, run func(context.Context, jobKey)) *pool {
	if workers < 1 {
		workers = 1
	}
	if queuedCap < 1 {
		queuedCap = 1
	}
	if perUserCap < 1 {
		perUserCap = 1
	}
	poolCtx, cancel := context.WithCancel(ctx)
	p := &pool{
		ctx:         poolCtx,
		cancel:      cancel,
		inflight:    make(map[jobKey]struct{}),
		buckets:     make(map[int64]*userBucket),
		workers:     workers,
		queuedCap:   queuedCap,
		perUserCap:  perUserCap,
		run:         run,
		watcherDone: make(chan struct{}),
	}
	p.cond = sync.NewCond(&p.mu)
	for i := 0; i < workers; i++ {
		p.wg.Add(1)
		go p.worker()
	}
	go p.watcher()
	return p
}

// watcher bridges parent-context cancellation into pool shutdown so a
// cancelled parent drops queued jobs and cancels running jobs even without an
// explicit Close. It is a single lifecycle goroutine started at construction
// (never per Submit) and joined by Close.
func (p *pool) watcher() {
	defer close(p.watcherDone)
	<-p.ctx.Done()
	p.shutdown()
}

// worker executes queued jobs in round-robin user order until the pool is
// shut down. A job dequeued after shutdown is never run: the wait condition
// holds the mutex across the predicate check and cond.Wait, so no wake-up is
// lost, and the post-wait closed/cancel check refuses to dispatch. The mutex
// is released before run is invoked, so an in-flight job never blocks other
// submissions or dispatches.
func (p *pool) worker() {
	defer p.wg.Done()
	for {
		p.mu.Lock()
		for !p.closed && p.ctx.Err() == nil && p.queued == 0 {
			p.cond.Wait()
		}
		if p.closed || p.ctx.Err() != nil {
			p.mu.Unlock()
			return
		}
		// queued > 0 here, so rrOrder is non-empty (Submit adds a user to the
		// ring exactly when it adds the user's first pending job).
		job := p.dispatchLocked()
		p.mu.Unlock()
		p.run(p.ctx, job)
		p.finish(job)
	}
}

// dispatchLocked pops the next pending job in round-robin user order and
// advances the ring cursor. The caller must hold p.mu and have verified
// p.queued > 0. The owning user's bucket is kept while it still has running
// jobs (inUse > 0); it only leaves the round-robin ring while it has no
// pending job left to dispatch.
func (p *pool) dispatchLocked() jobKey {
	uid := p.rrOrder[p.rrIdx]
	b := p.buckets[uid]
	job := b.pending[0]
	b.pending = b.pending[1:]
	p.queued--
	if len(b.pending) == 0 {
		// No more pending work for this user: drop it from the ring. The
		// bucket stays until its running jobs finish (finish drops it when
		// inUse reaches 0).
		p.rrOrder = append(p.rrOrder[:p.rrIdx], p.rrOrder[p.rrIdx+1:]...)
		if len(p.rrOrder) == 0 {
			p.rrIdx = 0
		} else {
			p.rrIdx %= len(p.rrOrder)
		}
	} else {
		p.rrIdx = (p.rrIdx + 1) % len(p.rrOrder)
	}
	return job
}

// Submit queues job. It returns nil when the job is queued or already pending
// (merged: the combo is being fetched or is queued, so nothing new is
// needed), ErrPoolBusy when the per-user or global bound is reached,
// ErrPoolClosed after Close, or the context error when the pool context is
// already cancelled. Submit never blocks and never spawns goroutines.
func (p *pool) Submit(job jobKey) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return ErrPoolClosed
	}
	if _, ok := p.inflight[job]; ok {
		return nil // already pending or running: merged, no new work.
	}
	if err := p.ctx.Err(); err != nil {
		return err
	}
	b := p.buckets[job.userID]
	if b != nil && b.inUse >= p.perUserCap {
		return ErrPoolBusy // this user already holds its pending+running cap.
	}
	if p.queued >= p.queuedCap {
		return ErrPoolBusy // global pending bound reached.
	}
	p.inflight[job] = struct{}{}
	if b == nil {
		b = &userBucket{}
		p.buckets[job.userID] = b
	}
	hadPending := len(b.pending) > 0
	b.pending = append(b.pending, job)
	b.inUse++
	p.queued++
	if !hadPending {
		// (Re)enter the round-robin ring so this user gets a dispatch turn.
		p.rrOrder = append(p.rrOrder, job.userID)
	}
	p.cond.Signal()
	return nil
}

// finish releases the in-flight mark for job after it was executed or dropped.
// It also decrements the owning user's pending+running count and removes the
// bucket once the user has no remaining work.
func (p *pool) finish(job jobKey) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.inflight, job)
	if b := p.buckets[job.userID]; b != nil {
		b.inUse--
		if b.inUse <= 0 {
			delete(p.buckets, job.userID)
		}
	}
}

// shutdown is the single idempotent shutdown path used by Close and the
// parent-cancellation watcher. It refuses new submissions, cancels the base
// context (in-flight fetches observe it), wakes waiting workers, joins them,
// then drops any remaining queued jobs and resets every counter to zero.
func (p *pool) shutdown() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	p.mu.Unlock()

	// Cancel first so in-flight run(ctx, ...) observes cancellation while the
	// workers are still alive to be joined.
	p.cancel()

	// Wake every worker blocked in cond.Wait so they observe closed and
	// return. The lock is held around Broadcast to pair with the waiters'
	// predicate check (no lost wake-up).
	p.mu.Lock()
	p.cond.Broadcast()
	p.mu.Unlock()

	// Wait for workers to finish their current job (cancelled ctx returns
	// promptly) and return. finish has already released their running jobs'
	// counts before each worker exits.
	p.wg.Wait()

	// All workers have returned. Clear any queued jobs that were never
	// dispatched so every counter converges to zero (queued, per-user inUse,
	// inflight).
	p.mu.Lock()
	p.inflight = make(map[jobKey]struct{})
	p.buckets = make(map[int64]*userBucket)
	p.rrOrder = nil
	p.rrIdx = 0
	p.queued = 0
	p.mu.Unlock()
}

// Close stops the pool: no new submissions are accepted, the base context is
// cancelled (in-flight fetches observe the cancellation), queued jobs are
// dropped, and Close waits for the workers and the parent-cancellation
// watcher to return. Close is idempotent.
func (p *pool) Close() {
	p.shutdown()
	<-p.watcherDone
}

// closedState reports whether the pool has entered shutdown. It is used to
// fail a manual admission before touching its separate rate limiter.
func (p *pool) closedState() bool {
	if p == nil {
		return true
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.closed
}

// Pending reports how many jobs are queued or in flight; it is a test helper.
func (p *pool) Pending() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.inflight)
}

// pendingForUser reports how many jobs are queued or in flight for one user;
// it is a test helper for the per-user bound.
func (p *pool) pendingForUser(userID int64) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	if b := p.buckets[userID]; b != nil {
		return b.inUse
	}
	return 0
}

// queuedCount reports the total number of pending (not yet dispatched) jobs;
// it is a test helper for the global queued bound.
func (p *pool) queuedCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.queued
}
