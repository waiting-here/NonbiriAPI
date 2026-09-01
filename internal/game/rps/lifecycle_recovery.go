package rps

import (
	"context"
	"time"
)

// RecoveryResult is the closed, bounded result consumed by lifecycle's RPS
// startup and maintenance rail.
type RecoveryResult struct {
	Processed int
	More      bool
}

// RecoverBeforeListenAt performs one bounded recovery batch using the
// coordinator's frozen decision time. Existing queue, reducer, and terminal
// transactions remain the only owners of their state transitions.
func (service *Service) RecoverBeforeListenAt(
	ctx context.Context,
	decisionNow int64,
	limit int,
	budgetDeadline time.Time,
) (RecoveryResult, error) {
	if service == nil || service.closed.Load() {
		return RecoveryResult{}, ErrClosed
	}
	if ctx == nil || decisionNow < 0 || decisionNow > 253402300799 ||
		limit < 1 || limit > workerBatchSize || budgetDeadline.IsZero() {
		return RecoveryResult{}, ErrInvalidRequest
	}

	workerCtx, cancel := context.WithDeadline(ctx, budgetDeadline)
	defer cancel()
	startup := !service.recovered.Load()
	processed := 0
	if startup && !service.recoveryValidated.Load() {
		if err := service.ValidatePersistedState(workerCtx); err != nil {
			return RecoveryResult{}, err
		}
	}

	if startup {
		count, err := service.clearStartupLeasesBatch(workerCtx, limit-processed)
		if err != nil {
			return RecoveryResult{}, err
		}
		processed += count
	}
	if processed < limit {
		count, err := service.recoverHealthEpochBatch(workerCtx, decisionNow, limit-processed)
		if err != nil {
			return RecoveryResult{}, err
		}
		processed += count
	}
	if processed < limit {
		count, err := service.sweepQueuesBatch(workerCtx, decisionNow, limit-processed)
		if err != nil {
			return RecoveryResult{}, err
		}
		processed += count
	}
	if processed < limit {
		count, err := service.runDeadlineSessionsBatch(workerCtx, decisionNow, limit-processed)
		if err != nil {
			return RecoveryResult{}, err
		}
		processed += count
	}
	if processed < limit {
		count, err := service.runTerminalRetriesBatch(workerCtx, decisionNow, limit-processed)
		if err != nil {
			return RecoveryResult{}, err
		}
		processed += count
	}

	more, err := service.recoveryWorkPending(workerCtx, decisionNow, startup)
	if err != nil {
		return RecoveryResult{}, err
	}
	if startup && !more {
		service.recovered.Store(true)
	}
	return RecoveryResult{Processed: processed, More: more}, nil
}

// ValidatePersistedState performs the one-time startup validation that is
// intentionally separate from lifecycle's bounded mutation batches.
func (service *Service) ValidatePersistedState(ctx context.Context) error {
	if service == nil || service.closed.Load() {
		return ErrClosed
	}
	if ctx == nil {
		return ErrInvalidRequest
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	service.recoveryValidated.Store(false)
	if err := service.validatePersistedState(ctx); err != nil {
		return err
	}
	service.recoveryValidated.Store(true)
	return nil
}

func (service *Service) clearStartupLeasesBatch(ctx context.Context, limit int) (int, error) {
	tx, err := service.database.BeginTx(ctx, nil)
	if err != nil {
		return 0, classifyDB(err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `DELETE FROM game_online_leases WHERE rowid IN (
SELECT rowid FROM game_online_leases WHERE substr(session_id,1,4)='rps_'
ORDER BY session_id,user_id LIMIT ?)`, limit)
	if err != nil {
		return 0, classifyDB(err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return 0, classifyDB(err)
	}
	if err := tx.Commit(); err != nil {
		return 0, classifyDB(err)
	}
	service.leaseMu.Lock()
	clear(service.leaseBindings)
	service.leaseMu.Unlock()
	return int(changed), nil
}

func (service *Service) recoveryWorkPending(ctx context.Context, decisionNow int64, startup bool) (bool, error) {
	if startup {
		pending, err := service.recoveryExists(ctx, `SELECT EXISTS(
SELECT 1 FROM game_online_leases WHERE substr(session_id,1,4)='rps_')`)
		if err != nil || pending {
			return pending, err
		}
	}
	pending, err := service.recoveryExists(ctx, `SELECT EXISTS(
SELECT 1 FROM game_rps_sessions WHERE health_epoch<>?)`, service.healthEpoch)
	if err != nil || pending {
		return pending, err
	}
	queueIDs, err := service.recoveryQueueIDs(ctx, decisionNow, 1)
	if err != nil || len(queueIDs) != 0 {
		return len(queueIDs) != 0, err
	}
	pending, err = service.recoveryExists(ctx, `SELECT EXISTS(
SELECT 1 FROM game_rps_sessions WHERE state='started' AND phase_deadline<=?)`, decisionNow)
	if err != nil || pending {
		return pending, err
	}
	return service.recoveryExists(ctx, `SELECT EXISTS(
SELECT 1 FROM game_rps_sessions
WHERE state='terminal_processing' AND (terminal_next_retry_at IS NULL OR terminal_next_retry_at<=?))`, decisionNow)
}

func (service *Service) recoveryExists(ctx context.Context, query string, args ...any) (bool, error) {
	var exists int
	if err := service.database.QueryRowContext(ctx, query, args...).Scan(&exists); err != nil {
		return false, classifyDB(err)
	}
	if exists != 0 && exists != 1 {
		return false, ErrInvariant
	}
	return exists == 1, nil
}
