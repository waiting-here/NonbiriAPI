package lifecycle

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/worker"
)

const (
	lifecycleRecoveryWorkerKey  = "lifecycle_recovery_v1"
	lifecycleRetentionWorkerKey = "lifecycle_retention_v1"
)

func (coordinator *Coordinator) workerDue(ctx context.Context, workerKey string, decisionNow int64) (bool, error) {
	checkpoint, err := coordinator.loadOrInitializeWorker(ctx, workerKey, decisionNow)
	if err != nil {
		return false, err
	}
	return checkpoint.NextAttemptAt <= decisionNow, nil
}

func (coordinator *Coordinator) loadOrInitializeWorker(
	ctx context.Context,
	workerKey string,
	decisionNow int64,
) (worker.Checkpoint, error) {
	if coordinator == nil || ctx == nil || workerKey == "" || decisionNow < 0 || decisionNow > maximumUnixSecond {
		return worker.Checkpoint{}, ErrInvalid
	}
	tx, err := coordinator.database.BeginTx(ctx, nil)
	if err != nil {
		return worker.Checkpoint{}, fmt.Errorf("lifecycle: begin worker checkpoint: %w", err)
	}
	defer tx.Rollback()
	checkpoint, err := worker.Load(ctx, tx, workerKey)
	if errors.Is(err, sql.ErrNoRows) {
		checkpoint, err = worker.Initialize(ctx, tx, worker.Checkpoint{
			WorkerKey: workerKey, Cursor: "", Generation: 1,
			NextAttemptAt: 0, UpdatedAt: decisionNow,
		})
	}
	if err != nil {
		return worker.Checkpoint{}, fmt.Errorf("lifecycle: load worker checkpoint: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return worker.Checkpoint{}, fmt.Errorf("lifecycle: commit worker checkpoint: %w", err)
	}
	return checkpoint, nil
}

func (coordinator *Coordinator) recordWorkerOutcome(
	ctx context.Context,
	workerKey string,
	decisionNow int64,
	runErr error,
) error {
	if ctx != nil && ctx.Err() != nil {
		return nil
	}
	previous, err := coordinator.loadOrInitializeWorker(ctx, workerKey, decisionNow)
	if err != nil {
		return err
	}
	var next worker.Checkpoint
	if runErr == nil {
		interval := MaintenanceInterval
		if workerKey == lifecycleRecoveryWorkerKey {
			interval = WorkerSweepInterval
		}
		nextAt := decisionNow + int64(interval.Seconds())
		if nextAt < decisionNow || nextAt > maximumUnixSecond {
			nextAt = maximumUnixSecond
		}
		next, err = worker.Advance(previous, previous.Cursor, previous.Generation, nextAt, decisionNow)
		if err == nil {
			// Only a completed lifecycle pass establishes the last success.
			next.LastSuccessAt = &decisionNow
		}
	} else {
		next, err = worker.Retry(previous, classifyLifecycleWorkerError(runErr), decisionNow)
	}
	if err != nil {
		return fmt.Errorf("lifecycle: build worker checkpoint: %w", err)
	}
	tx, err := coordinator.database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("lifecycle: begin worker outcome: %w", err)
	}
	defer tx.Rollback()
	swapped, err := worker.CompareAndSet(ctx, tx, previous, next)
	if err != nil {
		return fmt.Errorf("lifecycle: update worker checkpoint: %w", err)
	}
	if !swapped {
		return ErrConflict
	}
	if runErr == nil {
		if _, err := tx.ExecContext(ctx, `UPDATE admin_alerts
SET resolved=1,resolved_at=COALESCE(resolved_at,?),resolution_kind='worker_recovered'
WHERE kind='worker_checkpoint_failed' AND ref=? AND resolved=0`, decisionNow, workerKey); err != nil {
			return fmt.Errorf("lifecycle: resolve worker alert: %w", err)
		}
	} else {
		message := "Account lifecycle worker failed: " + string(next.LastError)
		phase, domain := "recovery", "coordinator"
		if workerKey == lifecycleRetentionWorkerKey {
			phase = "retention"
		}
		var stage lifecycleStageError
		if errors.As(runErr, &stage) {
			phase, domain = stage.phase, stage.domain
		}
		type target struct {
			Kind string `json:"kind"`
			ID   string `json:"id"`
		}
		type fact struct {
			Key   string `json:"key"`
			Value string `json:"value"`
		}
		contextBody, err := json.Marshal(struct {
			Targets []target `json:"targets"`
			Facts   []fact   `json:"occurred_facts"`
		}{
			Targets: []target{{Kind: "worker_checkpoint", ID: workerKey}},
			Facts: []fact{
				{Key: "worker_module", Value: "account_lifecycle"},
				{Key: "worker_stage", Value: phase},
				{Key: "worker_domain", Value: domain},
				{Key: "safe_error_code", Value: string(next.LastError)},
				{Key: "retry_attempt", Value: fmt.Sprint(next.AttemptCount)},
			},
		})
		if err != nil {
			return fmt.Errorf("lifecycle: encode worker alert: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO admin_alerts(kind,message,ref,created_at,resolved,context_version,context_json)
SELECT 'worker_checkpoint_failed',?,?,?,0,1,?
WHERE NOT EXISTS(SELECT 1 FROM admin_alerts
 WHERE kind='worker_checkpoint_failed' AND ref=? AND resolved=0)`, message, workerKey, decisionNow, string(contextBody), workerKey); err != nil {
			return fmt.Errorf("lifecycle: write worker alert: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("lifecycle: commit worker outcome: %w", err)
	}
	return nil
}

func classifyLifecycleWorkerError(err error) worker.ErrorClass {
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
