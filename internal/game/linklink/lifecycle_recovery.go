package linklink

import (
	"context"
	"time"
)

// RecoveryResult is the closed, bounded result consumed by lifecycle's
// LinkLink startup and maintenance rail.
type RecoveryResult struct {
	Processed int
	More      bool
}

// RecoverBeforeListenAt performs one bounded recovery batch using the
// coordinator's frozen decision time. Persisted-row validation and the
// existing timeout reducer remain authoritative.
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

	processed, err := service.pruneLifecycleLeasesBatch(workerCtx, decisionNow, limit)
	if err != nil {
		return RecoveryResult{}, err
	}
	if processed < limit {
		count, runErr := service.runLifecycleDueBatch(workerCtx, decisionNow, limit-processed)
		if runErr != nil {
			return RecoveryResult{}, runErr
		}
		processed += count
	}

	more, err := service.lifecycleRecoveryPending(workerCtx, decisionNow)
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
	if err := service.validatePersistedSessions(ctx); err != nil {
		return err
	}
	service.recoveryValidated.Store(true)
	return nil
}

func (service *Service) pruneLifecycleLeasesBatch(ctx context.Context, decisionNow int64, limit int) (int, error) {
	tx, err := service.database.BeginTx(ctx, nil)
	if err != nil {
		return 0, classifyDB(err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
DELETE FROM game_online_leases WHERE rowid IN (
 SELECT rowid FROM game_online_leases
 WHERE substr(session_id,1,3)='ll_' AND (expires_at<=? OR health_epoch<>?)
 ORDER BY expires_at,session_id,rowid LIMIT ?
)`, decisionNow, service.healthEpoch, limit)
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
	service.forgetExpiredLeases(decisionNow)
	return int(changed), nil
}

func (service *Service) runLifecycleDueBatch(ctx context.Context, decisionNow int64, limit int) (int, error) {
	rows, err := service.database.QueryContext(ctx, `
SELECT id FROM game_linklink_sessions
WHERE deadline<=? ORDER BY deadline,id LIMIT ?`, decisionNow, limit)
	if err != nil {
		return 0, classifyDB(err)
	}
	ids := make([]string, 0, limit)
	for rows.Next() {
		var sessionID string
		if err := rows.Scan(&sessionID); err != nil {
			_ = rows.Close()
			return 0, classifyDB(err)
		}
		ids = append(ids, sessionID)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return 0, classifyDB(err)
	}
	if err := rows.Close(); err != nil {
		return 0, classifyDB(err)
	}
	for _, sessionID := range ids {
		if _, err := service.timeoutOne(ctx, sessionID, decisionNow); err != nil {
			return 0, err
		}
	}
	return len(ids), nil
}

func (service *Service) lifecycleRecoveryPending(ctx context.Context, decisionNow int64) (bool, error) {
	var pending int
	if err := service.database.QueryRowContext(ctx, `SELECT EXISTS(
SELECT 1 FROM game_online_leases
WHERE substr(session_id,1,3)='ll_' AND (expires_at<=? OR health_epoch<>?)
)`, decisionNow, service.healthEpoch).Scan(&pending); err != nil {
		return false, classifyDB(err)
	}
	if pending != 0 && pending != 1 {
		return false, ErrInvariant
	}
	if pending == 1 {
		return true, nil
	}
	if err := service.database.QueryRowContext(ctx, `SELECT EXISTS(
SELECT 1 FROM game_linklink_sessions WHERE deadline<=?
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
