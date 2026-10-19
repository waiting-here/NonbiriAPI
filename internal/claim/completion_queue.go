package claim

import (
	"container/list"
	"context"
	"database/sql"
	"errors"
	"strconv"
	"sync"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/observability"
)

const (
	completionQueueLimit = 256
	completionBatchLimit = 4
	completionHoldBudget = 8 * time.Millisecond
)

type completionResult struct {
	value any
	err   error
}

type completionJob struct {
	ctx       context.Context
	operation string
	run       func(context.Context, *sql.Tx) (any, error)
	done      chan completionResult
	queued    func(error)
	element   *list.Element // protected by completionQueue.mu
}

type completionQueue struct {
	mu        sync.Mutex
	pending   list.List
	running   bool
	closed    bool
	fallbacks int
	closedCh  chan struct{}
	joined    bool
}

func (s *Service) completionContext(ctx context.Context) context.Context {
	if s.performance != nil {
		return s.performance.Context(ctx)
	}
	return ctx
}

// submitCompletion waits for durable completion. Only transaction-local,
// idempotent bodies belong here; acceptance and dispatch remain independent.
func (s *Service) submitCompletion(ctx context.Context, operation string, run func(context.Context, *sql.Tx) (any, error)) (_ any, resultErr error) {
	ctx = s.completionContext(ctx)
	finished := observability.DatabaseOperation(ctx, operation)
	defer func() { finished(resultErr) }()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	q := &s.completion
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		return nil, ErrDependencyUnavailable
	}
	if q.pending.Len() >= completionQueueLimit {
		// Preserve the ordinary synchronous path instead of rejecting settlement.
		q.fallbacks++
		q.mu.Unlock()
		defer func() {
			q.mu.Lock()
			q.fallbacks--
			q.finishClose()
			q.mu.Unlock()
		}()
		observability.CompletionStage(ctx, "overflow")(nil)
		result := s.completeOne(&completionJob{ctx: ctx, operation: operation, run: run})
		return result.value, result.err
	}
	job := &completionJob{ctx: ctx, operation: operation, run: run,
		done: make(chan completionResult, 1), queued: observability.DatabasePhase(ctx, operation, "queue")}
	job.element = q.pending.PushBack(job)
	if !q.running {
		q.running = true
		go s.completeQueued()
	}
	q.mu.Unlock()
	select {
	case result := <-job.done:
		return result.value, result.err
	case <-ctx.Done():
		q.mu.Lock()
		if job.element != nil {
			q.pending.Remove(job.element)
			job.element = nil
		}
		q.mu.Unlock()
		job.queued(ctx.Err())
		return nil, ctx.Err()
	}
}

func (q *completionQueue) take() *completionJob {
	entry := q.pending.Front()
	if entry == nil {
		return nil
	}
	q.pending.Remove(entry)
	job := entry.Value.(*completionJob)
	job.element = nil
	return job
}

func (s *Service) completeQueued() {
	q := &s.completion
	for {
		q.mu.Lock()
		first := q.take()
		if first == nil {
			q.running = false
			q.finishClose()
			q.mu.Unlock()
			return
		}
		q.mu.Unlock()
		s.completeBatch(first)
	}
}

func executeCompletion(job *completionJob, tx *sql.Tx) completionResult {
	if job.queued != nil {
		job.queued(job.ctx.Err())
	}
	finished := observability.DatabasePhase(job.ctx, job.operation, "execute")
	var result completionResult
	if result.err = job.ctx.Err(); result.err == nil {
		result.value, result.err = job.run(job.ctx, tx)
		if result.err == nil {
			result.err = job.ctx.Err()
		}
	}
	finished(result.err)
	return result
}

func commitCompletion(ctx context.Context, tx *sql.Tx, operation string) error {
	finished := observability.DatabasePhase(ctx, operation, "commit")
	err := tx.Commit()
	finished(err)
	return err
}

func (s *Service) completeOne(job *completionJob) completionResult {
	tx, finish, err := observability.BeginMeasuredTx(job.ctx, s.db, nil, job.operation)
	if err != nil {
		return completionResult{err: err}
	}
	result := executeCompletion(job, tx)
	if result.err == nil {
		result.err = commitCompletion(job.ctx, tx, job.operation)
	}
	finish(result.err)
	return result
}

func (s *Service) completeBatch(first *completionJob) {
	// The head's existing settlement context bounds acquisition and the outer
	// transaction. Its cancellation may roll back peers, which retry separately
	// using their own original contexts after the writer has been released.
	tx, finish, err := observability.BeginMeasuredTx(first.ctx, s.db, nil, "claim_batch")
	if err != nil {
		first.queued(err)
		first.done <- completionResult{err: err}
		return
	}
	started := time.Now()
	q := &s.completion
	q.mu.Lock()
	isolate := q.pending.Len() > 0
	q.mu.Unlock()
	jobs := make([]*completionJob, 0, completionBatchLimit)
	results := make([]completionResult, 0, completionBatchLimit)
	job := first
	for job != nil {
		jobs = append(jobs, job)
		if isolate {
			_, err = tx.ExecContext(first.ctx, "SAVEPOINT completion_item")
		}
		result := completionResult{err: err}
		if err == nil {
			result = executeCompletion(job, tx)
			if isolate && result.err != nil {
				_, err = tx.ExecContext(first.ctx, "ROLLBACK TO completion_item")
			}
			if isolate && err == nil {
				_, err = tx.ExecContext(first.ctx, "RELEASE completion_item")
			}
		}
		results = append(results, result)
		if err != nil || !isolate || len(jobs) == completionBatchLimit {
			break
		}
		if time.Since(started) >= completionHoldBudget {
			observability.CompletionStage(first.ctx, "hold_budget")(nil)
			break
		}
		q.mu.Lock()
		job = q.take()
		q.mu.Unlock()
	}
	if err == nil {
		for _, item := range jobs {
			if canceled := item.ctx.Err(); canceled != nil {
				err = canceled
				break
			}
		}
	}
	if err == nil && !isolate && results[0].err != nil {
		// A lone failed body needs only the ordinary full rollback.
		finish(results[0].err)
		observability.CompletionStage(first.ctx, "batch_1")(results[0].err)
		first.done <- results[0]
		return
	}
	if err == nil {
		err = commitCompletion(first.ctx, tx, "claim_batch")
	}
	finish(err) // release before any independent retry
	observability.CompletionStage(first.ctx, "batch_"+strconv.Itoa(len(jobs)))(err)
	if err != nil {
		fallback := observability.CompletionStage(context.WithoutCancel(first.ctx), "fallback")
		var failed error
		for i, item := range jobs {
			item.queued(item.ctx.Err())
			results[i] = s.completeOne(item)
			failed = errors.Join(failed, results[i].err)
		}
		fallback(failed)
	}
	for i, item := range jobs {
		item.done <- results[i]
	}
}

// CloseContext stops new completion submissions and joins accepted work,
// including synchronous overflow. A timed-out close can be awaited again.
func (s *Service) CloseContext(ctx context.Context) (resultErr error) {
	if s == nil {
		return nil
	}
	if ctx == nil {
		return ErrInvalidInput
	}
	finished := observability.CompletionStage(s.completionContext(ctx), "close")
	defer func() { finished(resultErr) }()
	q := &s.completion
	q.mu.Lock()
	q.closed = true
	if q.closedCh == nil {
		q.closedCh = make(chan struct{})
	}
	q.finishClose()
	closed := q.closedCh
	q.mu.Unlock()
	select {
	case <-closed:
		return nil
	default:
	}
	select {
	case <-closed:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (q *completionQueue) finishClose() {
	if q.closed && !q.joined && !q.running && q.fallbacks == 0 {
		q.joined = true
		close(q.closedCh)
	}
}

func (s *Service) Close() error { return s.CloseContext(context.Background()) }
