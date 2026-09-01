package auth

import (
	"context"
	"fmt"
	"time"
)

const lifecycleRetentionBatchLimit = 100

// LifecycleRetentionResult reports one bounded session-retention batch.
// More is computed in the same transaction after the selected rows are
// removed, so callers can continue without inferring from a full batch.
type LifecycleRetentionResult struct {
	Processed int
	More      bool
}

// RetainSessionsAt removes at most limit browser sessions that are expired at
// the caller's frozen decision time. The auth package keeps ownership of the
// session query and transaction; lifecycle only supplies the bounded budget.
func (r *Runtime) RetainSessionsAt(
	ctx context.Context,
	decisionNow int64,
	limit int,
	budgetDeadline time.Time,
) (LifecycleRetentionResult, error) {
	if r == nil || r.db == nil || ctx == nil || decisionNow < 0 || decisionNow > maxUnixSecond ||
		limit < 1 || limit > lifecycleRetentionBatchLimit || budgetDeadline.IsZero() {
		return LifecycleRetentionResult{}, ErrLifecycleInvalid
	}
	workerCtx, cancel := context.WithDeadline(ctx, budgetDeadline)
	defer cancel()

	tx, err := r.db.BeginTx(workerCtx, nil)
	if err != nil {
		return LifecycleRetentionResult{}, fmt.Errorf("auth: retain sessions: begin: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	result, err := tx.ExecContext(workerCtx, `
DELETE FROM sessions
WHERE token_hash IN (
 SELECT token_hash FROM sessions
 WHERE expires_at<=?
 ORDER BY expires_at,token_hash
 LIMIT ?
)`, decisionNow, limit)
	if err != nil {
		return LifecycleRetentionResult{}, fmt.Errorf("auth: retain sessions: delete expired: %w", err)
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return LifecycleRetentionResult{}, fmt.Errorf("auth: retain sessions: rows affected: %w", err)
	}
	if deleted < 0 || deleted > int64(limit) {
		return LifecycleRetentionResult{}, fmt.Errorf("auth: retain sessions: invalid rows affected")
	}

	var more int
	if err := tx.QueryRowContext(workerCtx, `SELECT EXISTS(
 SELECT 1 FROM sessions WHERE expires_at<=?
)`, decisionNow).Scan(&more); err != nil {
		return LifecycleRetentionResult{}, fmt.Errorf("auth: retain sessions: check remaining: %w", err)
	}
	if more != 0 && more != 1 {
		return LifecycleRetentionResult{}, fmt.Errorf("auth: retain sessions: invalid remaining state")
	}
	if err := tx.Commit(); err != nil {
		return LifecycleRetentionResult{}, fmt.Errorf("auth: retain sessions: commit: %w", err)
	}
	committed = true
	return LifecycleRetentionResult{Processed: int(deleted), More: more == 1}, nil
}
