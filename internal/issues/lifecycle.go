package issues

import (
	"context"
	"fmt"
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
)`, now, limit)
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
