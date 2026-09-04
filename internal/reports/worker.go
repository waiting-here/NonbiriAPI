package reports

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/worker"
)

const blockedRetryAt = maxUnixSecond

// RecoverBeforeListener makes crash-left running report operations eligible
// again, then performs one bounded due pass before public traffic is mounted.
func (repository *Repository) RecoverBeforeListener(ctx context.Context) (WorkerResult, error) {
	if err := repository.admit(); err != nil {
		return WorkerResult{}, err
	}
	defer repository.release()
	if ctx == nil || ctx.Err() != nil {
		return WorkerResult{}, ErrUnavailable
	}
	now, err := repository.nowUnix()
	if err != nil {
		return WorkerResult{}, err
	}
	tx, err := repository.beginTx(ctx)
	if err != nil {
		return WorkerResult{}, err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)
	if _, err := tx.ExecContext(ctx, `UPDATE accepted_operations SET state='accepted'
WHERE kind IN ('report_indexing','report_approved_processing') AND state='running'`); err != nil {
		return WorkerResult{}, fmt.Errorf("reports: recover running operations: %w", err)
	}
	if err := commit(tx, &committed); err != nil {
		return WorkerResult{}, err
	}
	return repository.runWorkerOnce(ctx, now)
}

// RecoverLifecycle performs one coordinator-owned, deadline-bounded report
// recovery pass at a frozen decision time. Crash-left running operation rows
// are first returned to accepted in bounded batches; report reducers run only
// after that queue is drained. Retention remains a separate lifecycle phase.
func (repository *Repository) RecoverLifecycle(
	ctx context.Context,
	decisionNow int64,
	limit int,
	budgetDeadline time.Time,
) (LifecycleWorkResult, error) {
	if err := repository.admit(); err != nil {
		return LifecycleWorkResult{}, err
	}
	defer repository.release()
	if ctx == nil || ctx.Err() != nil || decisionNow < 0 ||
		decisionNow > maxUnixSecond-caseRetentionSeconds || limit < 1 ||
		limit > workerBatchLimit || budgetDeadline.IsZero() {
		return LifecycleWorkResult{}, ErrInvalidRequest
	}
	workerCtx, cancel := context.WithDeadline(ctx, budgetDeadline)
	defer cancel()
	requeued, err := repository.requeueRunningLifecycleOperations(workerCtx, limit)
	if err != nil {
		return LifecycleWorkResult{}, err
	}
	if requeued > 0 {
		return LifecycleWorkResult{Processed: requeued, More: true}, nil
	}
	result, err := repository.runWorkerBatch(workerCtx, decisionNow, limit)
	if err != nil {
		return LifecycleWorkResult{}, err
	}
	return LifecycleWorkResult{Processed: result.CasesProcessed, More: result.More}, nil
}

func (repository *Repository) requeueRunningLifecycleOperations(ctx context.Context, limit int) (int, error) {
	tx, err := repository.beginTx(ctx)
	if err != nil {
		return 0, err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)
	result, err := tx.ExecContext(ctx, `UPDATE accepted_operations SET state='accepted'
WHERE id IN (
 SELECT id FROM accepted_operations
 WHERE kind IN ('report_indexing','report_approved_processing') AND state='running'
 ORDER BY created_at,id LIMIT ?
)`, limit)
	if err != nil {
		return 0, fmt.Errorf("reports: recover running lifecycle operations: %w", err)
	}
	processed, err := result.RowsAffected()
	if err != nil || processed < 0 || processed > int64(limit) {
		return 0, ErrInvariant
	}
	if err := commit(tx, &committed); err != nil {
		return 0, err
	}
	return int(processed), nil
}

// RunWorkerOnce performs at most one bounded pass. It is safe to call from a
// deterministic recovery loop or tests without starting the background loop.
func (repository *Repository) RunWorkerOnce(ctx context.Context) (WorkerResult, error) {
	if err := repository.admit(); err != nil {
		return WorkerResult{}, err
	}
	defer repository.release()
	if ctx == nil || ctx.Err() != nil {
		return WorkerResult{}, ErrUnavailable
	}
	now, err := repository.nowUnix()
	if err != nil {
		return WorkerResult{}, err
	}
	return repository.runWorkerOnce(ctx, now)
}

// StartWorker starts the single process-local report continuation loop.
func (repository *Repository) StartWorker(parent context.Context) error {
	if repository == nil || parent == nil || parent.Err() != nil {
		return ErrUnavailable
	}
	repository.lifecycleMu.Lock()
	if repository.closed {
		repository.lifecycleMu.Unlock()
		return ErrClosed
	}
	repository.workerMu.Lock()
	if repository.workerDone != nil {
		repository.workerMu.Unlock()
		repository.lifecycleMu.Unlock()
		return ErrConflict
	}
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	repository.workerCancel = cancel
	repository.workerDone = done
	repository.active.Add(1)
	repository.workerMu.Unlock()
	repository.lifecycleMu.Unlock()
	go repository.workerLoop(ctx, done)
	return nil
}

func (repository *Repository) workerLoop(ctx context.Context, done chan struct{}) {
	defer repository.release()
	defer close(done)
	ticker := time.NewTicker(repository.interval)
	defer ticker.Stop()
	for {
		now, err := repository.nowUnix()
		if err == nil {
			result, workErr := repository.runWorkerOnce(ctx, now)
			if workErr == nil && result.More {
				continue
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-repository.kick:
		case <-ticker.C:
		}
	}
}

func (repository *Repository) runWorkerOnce(ctx context.Context, now int64) (WorkerResult, error) {
	if ctx == nil || ctx.Err() != nil || now < 0 || now > maxUnixSecond-caseRetentionSeconds {
		return WorkerResult{}, ErrUnavailable
	}
	result, err := repository.runWorkerBatch(ctx, now, workerBatchLimit)
	if err != nil {
		return result, err
	}
	if err := repository.cleanupReportData(ctx, now); err != nil {
		return result, err
	}
	return result, nil
}

func (repository *Repository) runWorkerBatch(ctx context.Context, now int64, limit int) (WorkerResult, error) {
	if ctx == nil || ctx.Err() != nil || now < 0 || now > maxUnixSecond-caseRetentionSeconds ||
		limit < 1 || limit > workerBatchLimit {
		return WorkerResult{}, ErrUnavailable
	}
	result := WorkerResult{}
	remaining := limit
	processed, more, err := repository.processExpiries(ctx, now, remaining)
	if err != nil {
		return result, err
	}
	result.CasesProcessed += processed
	remaining -= processed
	result.More = result.More || more
	if remaining > 0 {
		processed, more, err = repository.processIndexingCases(ctx, now, remaining)
		if err != nil {
			return result, err
		}
		result.CasesProcessed += processed
		remaining -= processed
		result.More = result.More || more
	}
	if remaining > 0 {
		processed, more, err = repository.processApprovalCases(ctx, now, remaining)
		if err != nil {
			return result, err
		}
		result.CasesProcessed += processed
		remaining -= processed
		result.More = result.More || more
	}
	if remaining == 0 {
		result.More = true
	}
	return result, nil
}

func (repository *Repository) boundedWorkerContext(parent context.Context) (context.Context, context.CancelFunc) {
	limit := workerTransactionLimit
	if repository != nil && repository.workerLimit > 0 {
		limit = repository.workerLimit
	}
	return context.WithTimeout(parent, limit)
}

func classifyWorkerError(err error) worker.ErrorClass {
	if err == nil {
		return worker.ErrorNone
	}
	if errors.Is(err, ErrInvariant) {
		return worker.ErrorInvariant
	}
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "database is locked") || strings.Contains(message, "database is busy") ||
		strings.Contains(message, "sqlite_busy") || strings.Contains(message, "sql_busy") {
		return worker.ErrorDBBusy
	}
	return worker.ErrorRetryable
}

func retryAt(now, attempt int64, class worker.ErrorClass) (int64, error) {
	if attempt < 1 || now < 0 || now > maxUnixSecond {
		return 0, ErrInvariant
	}
	if class == worker.ErrorInvariant {
		return blockedRetryAt, nil
	}
	delay := worker.RetryDelay(attempt)
	if now > maxUnixSecond-delay {
		return 0, ErrInvariant
	}
	return now + delay, nil
}

func dueCaseIDs(
	ctx context.Context,
	database *sql.DB,
	query string,
	now int64,
	limit int,
) ([]string, bool, error) {
	if ctx == nil || database == nil || limit < 1 || limit > workerBatchLimit {
		return nil, false, ErrInvalidRequest
	}
	rows, err := database.QueryContext(ctx, query, now, limit+1)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	ids := make([]string, 0, limit+1)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, false, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	more := len(ids) > limit
	if more {
		ids = ids[:limit]
	}
	return ids, more, nil
}
