package lifecycle

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/observability"
)

type lifecycleStageError struct {
	phase  string
	domain string
	index  int
	err    error
}

func (stage lifecycleStageError) Error() string {
	if stage.index < 0 {
		return fmt.Sprintf("lifecycle: %s: %v", stage.phase, stage.err)
	}
	return fmt.Sprintf("lifecycle: %s adapter %d (%s): %v", stage.phase, stage.index, stage.domain, stage.err)
}

func (stage lifecycleStageError) Unwrap() error { return stage.err }

// FailureDomain returns the coordinator-owned domain label for a failed phase.
// It does not expose the underlying error, which can contain private data.
func FailureDomain(err error) string {
	var stage lifecycleStageError
	if errors.As(err, &stage) {
		return stage.domain
	}
	return ""
}

// RecoverBeforeListener runs the full fixed recovery order synchronously.
// Any failed domain keeps listener startup closed, but later domains are still
// attempted so one failure does not hide another recoverable backlog.
func (coordinator *Coordinator) RecoverBeforeListener(ctx context.Context) error {
	if coordinator == nil {
		return ErrInvalid
	}
	return coordinator.RecoverBeforeListenerAt(ctx, coordinator.now().Unix())
}

// RecoverBeforeListenerAt shares a fixed decision time with startup prerequisites.
// It has the same fail-closed behavior and fixed domain order as ordinary recovery.
func (coordinator *Coordinator) RecoverBeforeListenerAt(ctx context.Context, decisionNow int64) error {
	if coordinator == nil || ctx == nil {
		return ErrInvalid
	}
	if coordinator.closed.Load() {
		return ErrClosed
	}
	runErr := coordinator.runRecovery(ctx, decisionNow)
	return errors.Join(runErr, coordinator.recordWorkerOutcome(ctx, lifecycleRecoveryWorkerKey, decisionNow, runErr))
}

// RunDue merges with an overlapping recovery pass and otherwise serializes
// behind it. A merged caller observes the result of the pass that covered it.
func (coordinator *Coordinator) RunDue(ctx context.Context) error {
	return coordinator.runScheduled(ctx, false)
}

// RunMaintenance performs recovery before the six-hour retention order.
func (coordinator *Coordinator) RunMaintenance(ctx context.Context) error {
	return coordinator.runScheduled(ctx, true)
}

func (coordinator *Coordinator) runScheduled(ctx context.Context, retention bool) error {
	if coordinator == nil || ctx == nil {
		return ErrInvalid
	}
	if coordinator.closed.Load() {
		return ErrClosed
	}
	coordinator.runMu.Lock()
	observedGeneration := coordinator.runGeneration
	coordinator.runMu.Unlock()

	return coordinator.runObservedSchedule(ctx, retention, observedGeneration)
}

// runObservedSchedule covers a caller that arrived at observedGeneration.
// Gate acquisition can happen after that generation has already completed.
func (coordinator *Coordinator) runObservedSchedule(ctx context.Context, retention bool, observedGeneration uint64) error {
	coordinator.runGate.Lock()
	defer coordinator.runGate.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if coordinator.closed.Load() {
		return ErrClosed
	}
	coordinator.runMu.Lock()
	if retention && coordinator.lastRetentionGeneration > observedGeneration {
		result := coordinator.lastRunResult
		coordinator.runMu.Unlock()
		return result
	}
	if !retention && coordinator.lastRecoveryGeneration > observedGeneration {
		result := coordinator.lastRunResult
		coordinator.runMu.Unlock()
		return result
	}
	coordinator.runMu.Unlock()

	now := coordinator.now().Unix()
	var recoveryErr, retentionErr, recoveryStateErr, retentionStateErr error
	runRecovery, runRetention := true, retention
	if now < 0 || now > maximumUnixSecond {
		recoveryErr = ErrInvariant
	} else {
		if !retention {
			var dueErr error
			runRecovery, dueErr = coordinator.workerDue(ctx, lifecycleRecoveryWorkerKey, now)
			if dueErr != nil {
				return dueErr
			}
			runRetention, dueErr = coordinator.workerDue(ctx, lifecycleRetentionWorkerKey, now)
			if dueErr != nil {
				return dueErr
			}
			if runRetention {
				runRecovery = true
			}
			if !runRecovery && !runRetention {
				return nil
			}
		}
		if runRecovery {
			recoveryErr = coordinator.runRecovery(ctx, now)
			recoveryStateErr = coordinator.recordWorkerOutcome(ctx, lifecycleRecoveryWorkerKey, now, recoveryErr)
		}
		if runRetention {
			retentionErr = coordinator.runRetention(ctx, now)
			retentionStateErr = coordinator.recordWorkerOutcome(ctx, lifecycleRetentionWorkerKey, now, retentionErr)
		}
	}
	overall := errors.Join(recoveryErr, recoveryStateErr, retentionErr, retentionStateErr)
	coordinator.runMu.Lock()
	coordinator.runGeneration++
	if runRecovery {
		coordinator.lastRecoveryGeneration = coordinator.runGeneration
		coordinator.lastRecoveryResult = errors.Join(recoveryErr, recoveryStateErr)
	}
	if runRetention {
		coordinator.lastRetentionGeneration = coordinator.runGeneration
		coordinator.lastRetentionResult = overall
	}
	coordinator.lastRunResult = overall
	coordinator.runMu.Unlock()
	return overall
}

func (coordinator *Coordinator) runRecovery(ctx context.Context, decisionNow int64) (resultErr error) {
	finish := observability.DatabaseOperation(ctx, "lifecycle_recovery")
	defer func() { finish(resultErr) }()
	if decisionNow < 0 || decisionNow > maximumUnixSecond {
		return ErrInvariant
	}
	var failures []error
	holds := observability.LifecycleStage(ctx, "recovery", "legal_holds")
	holdErr := coordinator.expireAllDueHolds(ctx, decisionNow)
	holds(holdErr)
	if err := holdErr; err != nil {
		failures = append(failures, lifecycleStageError{phase: "expire legal holds", domain: "legal_holds", index: -1, err: err})
	}
	for index, stage := range coordinator.recovery.ordered() {
		done := observability.LifecycleStage(ctx, "recovery", stage.domain)
		err := drainMaintenanceBatches(ctx, decisionNow, stage.adapter.RecoverBeforeListener)
		done(err)
		if err != nil {
			failures = append(failures, lifecycleStageError{phase: "recovery", domain: stage.domain, index: index, err: err})
		}
	}
	return errors.Join(failures...)
}

func (coordinator *Coordinator) runRetention(ctx context.Context, decisionNow int64) (resultErr error) {
	finish := observability.DatabaseOperation(ctx, "lifecycle_retention")
	defer func() { finish(resultErr) }()
	var failures []error
	adapters := coordinator.retention.ordered()
	for index, stage := range adapters {
		done := observability.LifecycleStage(ctx, "retention", stage.domain)
		err := drainMaintenanceBatches(ctx, decisionNow, stage.adapter.Retain)
		done(err)
		if err != nil {
			failures = append(failures, lifecycleStageError{phase: "retention", domain: stage.domain, index: index, err: err})
		}
		if index == 1 {
			for {
				holds := observability.LifecycleStage(ctx, "retention", "legal_holds")
				result, err := coordinator.retainEndedHolds(ctx, decisionNow, WorkerBatchLimit, time.Now().Add(WorkerBudget))
				holds(err)
				if err != nil {
					failures = append(failures, lifecycleStageError{phase: "retain legal holds", domain: "legal_holds", index: -1, err: err})
					break
				}
				if !result.More {
					break
				}
			}
		}
	}
	return errors.Join(failures...)
}

// Each batch resumes from committed state at the same decision time. A heavily
// loaded process can lose its entire time slice before the first record.
func drainMaintenanceBatches(ctx context.Context, decisionNow int64, run func(context.Context, int64, int, time.Time) (WorkResult, error)) error {
	retried := false
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		deadline := time.Now().Add(WorkerBudget)
		batchCtx, cancel := context.WithDeadline(ctx, deadline)
		result, err := run(batchCtx, decisionNow, WorkerBatchLimit, deadline)
		childErr := batchCtx.Err()
		expired := !time.Now().Before(deadline)
		cancel()
		if parentErr := ctx.Err(); parentErr != nil {
			return parentErr
		}
		missedBudget := expired && batchMissedDeadline(result, err, childErr)
		if err != nil && !missedBudget {
			return err
		}
		if result.Processed < 0 || result.Processed > WorkerBatchLimit {
			return ErrInvariant
		}
		if missedBudget {
			if retried {
				return fmt.Errorf("two consecutive maintenance batches exhausted their time budget: %w", context.DeadlineExceeded)
			}
			retried = true
			continue
		}
		if !result.More {
			return nil
		}
		if result.Processed == 0 {
			return ErrInvariant
		}
		retried = false
	}
}

func batchMissedDeadline(result WorkResult, err, childErr error) bool {
	if err == nil {
		return result.More && result.Processed == 0
	}
	// A joined error may also report a domain or storage failure. Only a
	// single timeout, or a transaction closed by the expired child, is safe.
	for errors.Unwrap(err) != nil {
		err = errors.Unwrap(err)
	}
	return err == context.DeadlineExceeded || (err == sql.ErrTxDone && childErr == context.DeadlineExceeded)
}
