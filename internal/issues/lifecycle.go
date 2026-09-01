package issues

import (
	"context"
	"fmt"
	"time"
)

// RecoverBeforeListener is a bounded hook for the root lifecycle. It performs
// one fixed retention batch and one fixed rebuild batch per selected account;
// it never starts a detached worker.
func (service *Service) RecoverBeforeListener(ctx context.Context, batch int) ([]RebuildResult, RetentionResult, error) {
	if service == nil || service.repository == nil || batch < 1 || batch > 100 {
		return nil, RetentionResult{}, ErrInvalidRequest
	}
	retention, err := service.RetainClosed(ctx, batch)
	if err != nil {
		return nil, RetentionResult{}, err
	}
	rebuilt, err := service.RebuildIncomplete(ctx, batch)
	if err != nil {
		return rebuilt, retention, err
	}
	return rebuilt, retention, nil
}

func (service *Service) RetainClosed(ctx context.Context, limit int) (RetentionResult, error) {
	if service == nil || service.repository == nil || limit < 1 || limit > 100 {
		return RetentionResult{}, ErrInvalidRequest
	}
	now, err := service.repository.nowUnix()
	if err != nil {
		return RetentionResult{}, err
	}
	return service.retainClosedAt(ctx, now, limit)
}

// RetainLifecycleClosed runs one issue-owned retention transaction with the
// coordinator's frozen time and deadline.
func (service *Service) RetainLifecycleClosed(
	ctx context.Context,
	decisionNow int64,
	limit int,
	budgetDeadline time.Time,
) (RetentionResult, error) {
	if service == nil || service.repository == nil || ctx == nil || ctx.Err() != nil ||
		decisionNow < 0 || decisionNow > maxUnixSecond || limit < 1 || limit > 100 || budgetDeadline.IsZero() {
		return RetentionResult{}, ErrInvalidRequest
	}
	workerCtx, cancel := context.WithDeadline(ctx, budgetDeadline)
	defer cancel()
	return service.retainClosedAt(workerCtx, decisionNow, limit)
}

func (service *Service) retainClosedAt(ctx context.Context, decisionNow int64, limit int) (RetentionResult, error) {
	tx, err := beginTx(ctx, service.repository.db)
	if err != nil {
		return RetentionResult{}, err
	}
	committed := false
	defer finishTx(tx, &committed)
	result, err := tx.ExecContext(ctx, `
DELETE FROM user_issues WHERE id IN (
 SELECT id FROM user_issues
 WHERE state='closed' AND retain_until<=?
 ORDER BY retain_until,id LIMIT ?
)`, decisionNow, limit)
	if err != nil {
		return RetentionResult{}, fmt.Errorf("issues: delete expired closed history: %w", err)
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return RetentionResult{}, fmt.Errorf("issues: count expired closed history: %w", err)
	}
	if err := commitTx(tx, &committed); err != nil {
		return RetentionResult{}, err
	}
	return RetentionResult{ClosedDeleted: int(deleted)}, nil
}
