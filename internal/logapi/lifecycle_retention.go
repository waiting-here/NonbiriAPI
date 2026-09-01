package logapi

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// LifecycleRetentionResult reports one bounded request-log aggregate cleanup.
// Processed counts roots; request_attempts follow their root by cascade.
type LifecycleRetentionResult struct {
	RequestLogsDeleted int
	Processed          int
	More               bool
}

// RetainLifecycleRequestLogs removes terminal request-log aggregates whose
// frozen ordinary deadline has elapsed. Nonterminal and actively held roots
// are never age-cleaned by this seam.
func (repository *Repository) RetainLifecycleRequestLogs(
	ctx context.Context,
	decisionNow int64,
	limit int,
	budgetDeadline time.Time,
) (LifecycleRetentionResult, error) {
	if repository == nil || repository.db == nil || ctx == nil ||
		decisionNow < 0 || decisionNow > maxUnixSecond || limit < 1 || limit > 100 || budgetDeadline.IsZero() {
		return LifecycleRetentionResult{}, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return LifecycleRetentionResult{}, err
	}
	workerCtx, cancel := context.WithDeadline(ctx, budgetDeadline)
	defer cancel()
	if err := workerCtx.Err(); err != nil {
		return LifecycleRetentionResult{}, err
	}

	tx, err := repository.db.BeginTx(workerCtx, nil)
	if err != nil {
		return LifecycleRetentionResult{}, fmt.Errorf("logapi: begin request-log retention: %w", err)
	}
	defer tx.Rollback()

	cutoff := decisionNow - requestLogRetentionSeconds
	rows, err := tx.QueryContext(workerCtx, `
SELECT l.id
FROM request_logs l
WHERE l.completed_at IS NOT NULL
  AND l.completed_at<=?
  AND NOT EXISTS(
    SELECT 1 FROM legal_holds h
    WHERE h.object_kind='request_log'
      AND h.object_ref=CAST(l.id AS TEXT)
      AND h.state='active'
  )
ORDER BY l.completed_at,l.id
LIMIT ?`, cutoff, limit)
	if err != nil {
		return LifecycleRetentionResult{}, fmt.Errorf("logapi: select retained request logs: %w", err)
	}
	ids := make([]int64, 0, limit)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return LifecycleRetentionResult{}, fmt.Errorf("logapi: scan retained request log: %w", err)
		}
		if id <= 0 {
			_ = rows.Close()
			return LifecycleRetentionResult{}, ErrInvariant
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return LifecycleRetentionResult{}, fmt.Errorf("logapi: close retained request logs: %w", err)
	}
	if err := rows.Err(); err != nil {
		return LifecycleRetentionResult{}, fmt.Errorf("logapi: iterate retained request logs: %w", err)
	}

	for _, id := range ids {
		result, err := tx.ExecContext(workerCtx, `DELETE FROM request_logs WHERE id=?`, id)
		if err != nil {
			return LifecycleRetentionResult{}, fmt.Errorf("logapi: delete retained request-log aggregate: %w", err)
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return LifecycleRetentionResult{}, fmt.Errorf("logapi: count retained request-log aggregate: %w", err)
		}
		if changed != 1 {
			return LifecycleRetentionResult{}, ErrInvariant
		}
	}

	more, err := hasRetainedRequestLogs(workerCtx, tx, cutoff)
	if err != nil {
		return LifecycleRetentionResult{}, err
	}
	if err := workerCtx.Err(); err != nil {
		return LifecycleRetentionResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return LifecycleRetentionResult{}, fmt.Errorf("logapi: commit request-log retention: %w", err)
	}
	return LifecycleRetentionResult{
		RequestLogsDeleted: len(ids),
		Processed:          len(ids),
		More:               more,
	}, nil
}

func hasRetainedRequestLogs(ctx context.Context, tx *sql.Tx, cutoff int64) (bool, error) {
	var exists int
	if err := tx.QueryRowContext(ctx, `
SELECT EXISTS(
 SELECT 1
 FROM request_logs l
 WHERE l.completed_at IS NOT NULL
   AND l.completed_at<=?
   AND NOT EXISTS(
     SELECT 1 FROM legal_holds h
     WHERE h.object_kind='request_log'
       AND h.object_ref=CAST(l.id AS TEXT)
       AND h.state='active'
   )
)`, cutoff).Scan(&exists); err != nil {
		return false, fmt.Errorf("logapi: inspect remaining request-log retention: %w", err)
	}
	if exists != 0 && exists != 1 {
		return false, ErrInvariant
	}
	return exists == 1, nil
}
