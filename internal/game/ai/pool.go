package ai

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"
)

type Job struct {
	Request Request
	Source  Source
	Budget  time.Duration
	// Complete commits an accepted action or the game's fallback. It runs once,
	// after source capacity is released, unless that source has not returned.
	Complete func(Result, error)
}

type decision struct {
	job        Job
	ctx        context.Context
	deadline   time.Time
	cancel     context.CancelFunc
	stop       func() bool
	delivering bool
	started    bool
}

// Pool bounds computation and queued/committing windows separately. A source
// that ignores cancellation retains its compute slot until it really returns.
// Use separate pools for sources with different latency/resource profiles.
type Pool struct {
	mu                         sync.Mutex
	workers, capacity, running int
	closed                     bool
	jobs                       map[string]*decision
	pending                    []*decision
	done                       chan struct{}
	doneOnce                   sync.Once
}

func NewPool(workers, capacity int) (*Pool, error) {
	if workers < 1 || capacity < workers {
		return nil, ErrCapacity
	}
	return &Pool{workers: workers, capacity: capacity, jobs: map[string]*decision{}, done: make(chan struct{})}, nil
}

// Submit uses a stable window identity, never a revision advanced by an
// unrelated actor. Duplicate windows stay reserved through Complete.
func (p *Pool) Submit(ctx context.Context, job Job) (bool, error) {
	if job.Source == nil || job.Complete == nil || job.Request.DecisionID == "" || job.Request.ProtocolVersion != ProtocolVersion || !job.Source.Supports(job.Request.Capability) || job.Budget <= 0 || job.Request.Deadline.IsZero() {
		return false, ErrUnsupported
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return false, ErrClosed
	}
	if _, exists := p.jobs[job.Request.DecisionID]; exists {
		return false, nil
	}
	if len(p.jobs) >= p.capacity {
		return false, ErrCapacity
	}
	deadline := time.Now().Add(job.Budget)
	if job.Request.Deadline.Before(deadline) {
		deadline = job.Request.Deadline
	}
	work, cancel := context.WithDeadline(ctx, deadline)
	d := &decision{job: job, ctx: work, deadline: deadline, cancel: cancel}
	p.jobs[job.Request.DecisionID] = d
	// One cancellation callback per admitted window is bounded by capacity.
	d.stop = context.AfterFunc(work, func() { p.deliver(d, Result{}, work.Err()) })
	p.pending = append(p.pending, d)
	slices.SortStableFunc(p.pending, func(a, b *decision) int { return a.deadline.Compare(b.deadline) })
	p.kickLocked()
	return true, nil
}

func (p *Pool) kickLocked() {
	for p.running < p.workers && len(p.pending) > 0 && !p.closed {
		d := p.pending[0]
		p.pending = p.pending[1:]
		if d.delivering {
			continue
		}
		p.running++
		d.started = true
		go p.run(d)
	}
}

func invoke(d *decision) (result Result, err error) {
	defer func() {
		if recover() != nil {
			result = Result{}
			err = ErrSourceFailure
		}
	}()
	if err = d.ctx.Err(); err != nil {
		return
	}
	return d.job.Source.Decide(d.ctx, d.job.Request)
}

func (p *Pool) run(d *decision) {
	result, err := invoke(d)
	if err == nil {
		err = d.ctx.Err()
	}
	p.mu.Lock()
	p.running--
	p.kickLocked()
	p.checkDoneLocked()
	p.mu.Unlock()
	p.deliver(d, result, err)
}

func (p *Pool) deliver(d *decision, result Result, err error) {
	p.mu.Lock()
	if d.delivering {
		p.mu.Unlock()
		return
	}
	d.delivering = true
	if errors.Is(err, context.DeadlineExceeded) {
		stage := ErrQueueTimeout
		if d.started {
			stage = ErrComputeTimeout
		}
		err = errors.Join(stage, err)
	}
	d.stop()
	d.cancel()
	p.pending = slices.DeleteFunc(p.pending, func(item *decision) bool { return item == d })
	p.kickLocked()
	p.mu.Unlock()
	if err == nil && (result.ProtocolVersion != d.job.Request.ProtocolVersion || result.DecisionID != d.job.Request.DecisionID || result.ActionSchema != d.job.Request.ActionSchema || result.Action == nil) {
		err = ErrInvalidResult
	}
	d.job.Complete(result, err)
	p.mu.Lock()
	delete(p.jobs, d.job.Request.DecisionID)
	p.checkDoneLocked()
	p.mu.Unlock()
}

func (p *Pool) checkDoneLocked() {
	if p.closed && len(p.jobs) == 0 && p.running == 0 {
		p.doneOnce.Do(func() { close(p.done) })
	}
}

// Close stops admission and cancels all windows. Its caller chooses how long
// to wait for sources that fail to cooperate; no replacement worker is added.
func (p *Pool) Close(ctx context.Context) error {
	p.mu.Lock()
	p.closed = true
	for _, d := range p.jobs {
		d.cancel()
	}
	p.checkDoneLocked()
	p.mu.Unlock()
	select {
	case <-p.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
