package fetch

import (
	"context"
	"errors"
	"sync"
)

var (
	// ErrPoolBusy reports that the bounded refresh queue is full. Callers map
	// it to the rate_limited contract code; the automatic fetch hook logs it
	// and drops the fetch rather than spawning unbounded work.
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

// pool runs bounded asynchronous fetch jobs: a fixed number of workers drain
// a bounded queue; each combo has at most one queued or running job. The pool
// owns a base context: Close cancels it (propagating cancellation into
// in-flight egress requests), drops queued jobs, and waits for workers to
// finish. The pool is safe for concurrent Submit and Close.
type pool struct {
	ctx    context.Context
	cancel context.CancelFunc

	queue chan jobKey
	run   func(context.Context, jobKey)

	mu       sync.Mutex
	inflight map[jobKey]struct{}
	closed   bool
	wg       sync.WaitGroup
}

// newPool starts workers goroutines (clamped to >= 1) draining a queue of
// capacity queueSize (clamped to >= 1). run is invoked for every executed
// job with the pool's base context; it must honor context cancellation.
func newPool(ctx context.Context, workers, queueSize int, run func(context.Context, jobKey)) *pool {
	if workers < 1 {
		workers = 1
	}
	if queueSize < 1 {
		queueSize = 1
	}
	poolCtx, cancel := context.WithCancel(ctx)
	p := &pool{
		ctx:      poolCtx,
		cancel:   cancel,
		queue:    make(chan jobKey, queueSize),
		run:      run,
		inflight: make(map[jobKey]struct{}),
	}
	for i := 0; i < workers; i++ {
		p.wg.Add(1)
		go p.worker()
	}
	return p
}

// worker executes queued jobs until the pool context is cancelled. A job
// dequeued after cancellation is dropped (never run) so Close has a
// deterministic guarantee: queued work does not execute after shutdown. The
// ctx.Done branch then drains any remaining queued jobs (releasing their
// in-flight marks) and exits.
func (p *pool) worker() {
	defer p.wg.Done()
	for {
		select {
		case job := <-p.queue:
			if p.ctx.Err() != nil {
				p.finish(job) // dropped: pool cancelled/closed.
				continue
			}
			p.run(p.ctx, job)
			p.finish(job)
		case <-p.ctx.Done():
			for {
				select {
				case job := <-p.queue:
					p.finish(job)
				default:
					return
				}
			}
		}
	}
}

// Submit queues job. It returns nil when the job is queued or already pending
// (merged: the combo is being fetched or is queued, so nothing new is
// needed), ErrPoolBusy when the bounded queue is full, ErrPoolClosed after
// Close, or the context error when the pool context is already cancelled.
// Submit never blocks and never spawns goroutines.
func (p *pool) Submit(job jobKey) error {
	// Keep the mutex held through the non-blocking enqueue. Close waits for
	// this critical section, so it cannot cancel and join the workers before a
	// successful send, which would strand an in-flight mark in a queue with no
	// remaining consumer.
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return ErrPoolClosed
	}
	if _, ok := p.inflight[job]; ok {
		return nil // already pending or running: merged.
	}
	if err := p.ctx.Err(); err != nil {
		return err
	}
	p.inflight[job] = struct{}{}
	select {
	case p.queue <- job:
		return nil
	default:
		delete(p.inflight, job)
		return ErrPoolBusy
	}
}

// finish releases the in-flight mark for job after it was executed or
// dropped.
func (p *pool) finish(job jobKey) {
	p.mu.Lock()
	delete(p.inflight, job)
	p.mu.Unlock()
}

// Close stops the pool: no new submissions are accepted, the base context is
// cancelled (in-flight fetches observe the cancellation), queued jobs are
// dropped, and Close waits for the workers to return. Close is idempotent.
func (p *pool) Close() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	p.mu.Unlock()
	p.cancel()
	p.wg.Wait()
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
