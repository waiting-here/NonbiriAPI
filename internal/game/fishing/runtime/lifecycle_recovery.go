package runtime

import (
	"context"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

// RecoveryResult is the closed, bounded result consumed by the lifecycle
// coordinator's Fishing recovery slot.
type RecoveryResult struct {
	Processed int
	More      bool
}

// RecoverBeforeListenAt performs one bounded Fishing recovery batch using the
// coordinator's frozen decision time. Settlement, retry checkpointing, rank
// materialization, and response recovery remain owned by their existing
// Fishing primitives.
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
	if err := workerCtx.Err(); err != nil {
		return RecoveryResult{}, err
	}

	startup := !service.recovered.Load()
	if startup && !service.recoveryValidated.Load() {
		if err := service.ValidatePersistedState(workerCtx); err != nil {
			return RecoveryResult{}, err
		}
	}

	processed, err := service.recoverFishingDueBatch(workerCtx, decisionNow, limit)
	if err != nil {
		return RecoveryResult{}, err
	}
	more, err := service.fishingRecoveryPending(workerCtx, decisionNow)
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
	if err := service.validateFishingRecoveryState(ctx); err != nil {
		return err
	}
	service.recoveryValidated.Store(true)
	return nil
}

// validateFishingRecoveryState preserves RecoverBeforeListen's full persisted
// ledger/capacity validation and Fishing's exact one-row reservation shape.
func (service *Service) validateFishingRecoveryState(ctx context.Context) error {
	tx, err := service.database.BeginTx(ctx, nil)
	if err != nil {
		return classifyDB(err)
	}
	reservations, err := ledger.RecoverNonterminal(ctx, tx)
	if err == nil {
		for _, reservation := range reservations {
			if reservation.Domain == "fishing_batch" && reservation.Rows.Decimal() != "1" {
				err = ErrInvariant
				break
			}
		}
	}
	if rollbackErr := tx.Rollback(); err == nil && rollbackErr != nil {
		err = rollbackErr
	}
	return err
}

func (service *Service) recoverFishingDueBatch(ctx context.Context, decisionNow int64, limit int) (int, error) {
	rows, err := service.database.QueryContext(ctx, `
SELECT id FROM game_fishing_batches
WHERE state='reserved' AND retry_exhausted=0 AND next_attempt_at<=?
ORDER BY next_attempt_at,id LIMIT ?`, decisionNow, limit)
	if err != nil {
		return 0, classifyDB(err)
	}
	ids := make([]string, 0, limit)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return 0, classifyDB(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return 0, classifyDB(err)
	}
	if err := rows.Close(); err != nil {
		return 0, classifyDB(err)
	}

	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		if _, settleErr := service.settle(ctx, id, 0, decisionNow, true); settleErr != nil {
			if scheduleErr := service.scheduleFailure(ctx, id, decisionNow, settleErr); scheduleErr != nil {
				return 0, scheduleErr
			}
		}
	}
	return len(ids), nil
}

func (service *Service) fishingRecoveryPending(ctx context.Context, decisionNow int64) (bool, error) {
	var pending int
	if err := service.database.QueryRowContext(ctx, `SELECT EXISTS(
SELECT 1 FROM game_fishing_batches
WHERE state='reserved' AND retry_exhausted=0 AND next_attempt_at<=?
)`, decisionNow).Scan(&pending); err != nil {
		return false, classifyDB(err)
	}
	if pending != 0 && pending != 1 {
		return false, ErrInvariant
	}
	return pending == 1, nil
}

var _ interface {
	RecoverBeforeListenAt(context.Context, int64, int, time.Time) (RecoveryResult, error)
} = (*Service)(nil)
