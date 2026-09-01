package maintenance

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// LifecycleRetention owns the maintenance-event retention transaction without
// adding database access to the request-facing maintenance Service.
type LifecycleRetention struct {
	database *sql.DB
}

type LifecycleRetentionResult struct {
	ActorsDeidentified int
	EventsDeleted      int
	Processed          int
	More               bool
}

func NewLifecycleRetention(database *sql.DB) (*LifecycleRetention, error) {
	if database == nil {
		return nil, errors.New("maintenance lifecycle retention database is required")
	}
	return &LifecycleRetention{database: database}, nil
}

// RetainEvents first removes due actor identity. Only a pass with no actor
// work deletes resolved event facts, so one aggregate is never charged twice
// to a single total budget. Active holds block only the fact deletion.
func (owner *LifecycleRetention) RetainEvents(
	ctx context.Context,
	decisionNow int64,
	limit int,
	budgetDeadline time.Time,
) (LifecycleRetentionResult, error) {
	if owner == nil || owner.database == nil || ctx == nil ||
		decisionNow < 0 || decisionNow > maxUnixSecond || limit < 1 || limit > 100 || budgetDeadline.IsZero() {
		return LifecycleRetentionResult{}, ErrInvalidMutation
	}
	if err := ctx.Err(); err != nil {
		return LifecycleRetentionResult{}, err
	}
	workerCtx, cancel := context.WithDeadline(ctx, budgetDeadline)
	defer cancel()
	if err := workerCtx.Err(); err != nil {
		return LifecycleRetentionResult{}, err
	}

	tx, err := owner.database.BeginTx(workerCtx, nil)
	if err != nil {
		return LifecycleRetentionResult{}, fmt.Errorf("maintenance: begin event retention: %w", err)
	}
	defer tx.Rollback()

	deidentified, err := deidentifyLifecycleActors(workerCtx, tx, decisionNow, limit)
	if err != nil {
		return LifecycleRetentionResult{}, err
	}
	result := LifecycleRetentionResult{
		ActorsDeidentified: deidentified,
		Processed:          deidentified,
	}
	if deidentified == 0 {
		deleted, err := deleteLifecycleEvents(workerCtx, tx, decisionNow, limit)
		if err != nil {
			return LifecycleRetentionResult{}, err
		}
		result.EventsDeleted = deleted
		result.Processed = deleted
	}
	more, err := hasLifecycleEventWork(workerCtx, tx, decisionNow)
	if err != nil {
		return LifecycleRetentionResult{}, err
	}
	result.More = more
	if err := workerCtx.Err(); err != nil {
		return LifecycleRetentionResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return LifecycleRetentionResult{}, fmt.Errorf("maintenance: commit event retention: %w", err)
	}
	return result, nil
}

func deidentifyLifecycleActors(ctx context.Context, tx *sql.Tx, decisionNow int64, limit int) (int, error) {
	result, err := tx.ExecContext(ctx, `
UPDATE maintenance_events
SET actor_user_id=NULL,actor_discord_id=NULL,actor_role=NULL
WHERE id IN (
 SELECT id FROM maintenance_events
 WHERE actor_user_id IS NOT NULL
   AND resolved_at IS NOT NULL
   AND deidentify_at IS NOT NULL
   AND deidentify_at<=?
 ORDER BY deidentify_at,id
 LIMIT ?
)`, decisionNow, limit)
	if err != nil {
		return 0, fmt.Errorf("maintenance: deidentify event actors: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("maintenance: count deidentified event actors: %w", err)
	}
	if changed < 0 || changed > int64(limit) {
		return 0, ErrInvariant
	}
	return int(changed), nil
}

func deleteLifecycleEvents(ctx context.Context, tx *sql.Tx, decisionNow int64, limit int) (int, error) {
	result, err := tx.ExecContext(ctx, `
DELETE FROM maintenance_events
WHERE id IN (
 SELECT e.id FROM maintenance_events e
 WHERE e.resolved_at IS NOT NULL
   AND e.retain_until IS NOT NULL
   AND e.retain_until<=?
   AND NOT EXISTS(
     SELECT 1 FROM legal_holds h
     WHERE h.object_kind='maintenance_event'
       AND h.object_ref=e.id
       AND h.state='active'
   )
 ORDER BY e.retain_until,e.id
 LIMIT ?
)`, decisionNow, limit)
	if err != nil {
		return 0, fmt.Errorf("maintenance: delete retained event facts: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("maintenance: count retained event facts: %w", err)
	}
	if changed < 0 || changed > int64(limit) {
		return 0, ErrInvariant
	}
	return int(changed), nil
}

func hasLifecycleEventWork(ctx context.Context, tx *sql.Tx, decisionNow int64) (bool, error) {
	var exists int
	if err := tx.QueryRowContext(ctx, `
SELECT EXISTS(
 SELECT 1 FROM maintenance_events e
 WHERE (e.actor_user_id IS NOT NULL AND e.resolved_at IS NOT NULL
        AND e.deidentify_at IS NOT NULL AND e.deidentify_at<=?)
    OR (e.resolved_at IS NOT NULL AND e.retain_until IS NOT NULL AND e.retain_until<=?
        AND NOT EXISTS(
          SELECT 1 FROM legal_holds h
          WHERE h.object_kind='maintenance_event'
            AND h.object_ref=e.id
            AND h.state='active'
        ))
)`, decisionNow, decisionNow).Scan(&exists); err != nil {
		return false, fmt.Errorf("maintenance: inspect remaining event retention: %w", err)
	}
	if exists != 0 && exists != 1 {
		return false, ErrInvariant
	}
	return exists == 1, nil
}
