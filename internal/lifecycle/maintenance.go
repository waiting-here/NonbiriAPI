package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// RecoverBeforeListener runs the full fixed recovery order synchronously.
// Any failed domain keeps listener startup closed, but later domains are still
// attempted so one failure does not hide another recoverable backlog.
func (coordinator *Coordinator) RecoverBeforeListener(ctx context.Context) error {
	if coordinator == nil || ctx == nil {
		return ErrInvalid
	}
	if coordinator.closed.Load() {
		return ErrClosed
	}
	decisionNow := coordinator.now().Unix()
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

func (coordinator *Coordinator) runRecovery(ctx context.Context, decisionNow int64) error {
	if decisionNow < 0 || decisionNow > maximumUnixSecond {
		return ErrInvariant
	}
	var failures []error
	if err := coordinator.expireAllDueHolds(ctx, decisionNow); err != nil {
		failures = append(failures, fmt.Errorf("lifecycle: expire legal holds: %w", err))
	}
	for index, adapter := range coordinator.recovery.ordered() {
		if err := drainRecoveryAdapter(ctx, adapter, decisionNow); err != nil {
			failures = append(failures, fmt.Errorf("lifecycle: recovery adapter %d: %w", index, err))
		}
	}
	return errors.Join(failures...)
}

func drainRecoveryAdapter(ctx context.Context, adapter RecoveryAdapter, decisionNow int64) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		deadline := time.Now().Add(WorkerBudget)
		batchCtx, cancel := context.WithDeadline(ctx, deadline)
		result, err := adapter.RecoverBeforeListener(batchCtx, decisionNow, WorkerBatchLimit, deadline)
		cancel()
		if err != nil {
			return err
		}
		if result.Processed < 0 || result.Processed > WorkerBatchLimit || result.More && result.Processed == 0 {
			return ErrInvariant
		}
		if !result.More {
			return nil
		}
	}
}

func (coordinator *Coordinator) runRetention(ctx context.Context, decisionNow int64) error {
	var failures []error
	adapters := coordinator.retention.ordered()
	for index, adapter := range adapters {
		if err := drainRetentionAdapter(ctx, adapter, decisionNow); err != nil {
			failures = append(failures, fmt.Errorf("lifecycle: retention adapter %d: %w", index, err))
		}
		if index == 1 {
			for {
				result, err := coordinator.retainEndedHolds(ctx, decisionNow, WorkerBatchLimit, time.Now().Add(WorkerBudget))
				if err != nil {
					failures = append(failures, fmt.Errorf("lifecycle: retain legal holds: %w", err))
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

func drainRetentionAdapter(ctx context.Context, adapter RetentionAdapter, decisionNow int64) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		deadline := time.Now().Add(WorkerBudget)
		batchCtx, cancel := context.WithDeadline(ctx, deadline)
		result, err := adapter.Retain(batchCtx, decisionNow, WorkerBatchLimit, deadline)
		cancel()
		if err != nil {
			return err
		}
		if result.Processed < 0 || result.Processed > WorkerBatchLimit || result.More && result.Processed == 0 {
			return ErrInvariant
		}
		if !result.More {
			return nil
		}
	}
}
