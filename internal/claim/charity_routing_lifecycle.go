package claim

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
)

// CharityRoutingLifecycle owns only local routing state. Physical dispatch
// buckets are intentionally independent of caller deletion until their own
// 300-second window expires.
type CharityRoutingLifecycle struct{ db *sql.DB }

func NewCharityRoutingLifecycle(database *sql.DB) (*CharityRoutingLifecycle, error) {
	if database == nil {
		return nil, ErrDependencyUnavailable
	}
	return &CharityRoutingLifecycle{db: database}, nil
}

func (owner *CharityRoutingLifecycle) PrepareDelete(ctx context.Context, tx *sql.Tx, request lifecycle.DeleteRequest) (lifecycle.DeleteFinalizer, error) {
	if owner == nil || owner.db == nil || ctx == nil || tx == nil || request.UserID <= 0 {
		return nil, ErrInvalidInput
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM charity_key_affinities WHERE user_id=?`, request.UserID); err != nil {
		return nil, fmt.Errorf("claim: delete caller charity associations: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE charity_dispatch_receipts SET user_id=NULL WHERE user_id=?`, request.UserID); err != nil {
		return nil, fmt.Errorf("claim: unlink caller routing receipts: %w", err)
	}
	return nil, nil
}

func (owner *CharityRoutingLifecycle) RecoverBeforeListener(ctx context.Context, decisionNow int64, limit int, budgetDeadline time.Time) (lifecycle.WorkResult, error) {
	return owner.maintain(ctx, decisionNow, limit, budgetDeadline)
}

func (owner *CharityRoutingLifecycle) Retain(ctx context.Context, decisionNow int64, limit int, budgetDeadline time.Time) (lifecycle.WorkResult, error) {
	return owner.maintain(ctx, decisionNow, limit, budgetDeadline)
}

func (owner *CharityRoutingLifecycle) maintain(ctx context.Context, decisionNow int64, limit int, budgetDeadline time.Time) (lifecycle.WorkResult, error) {
	if owner == nil || owner.db == nil || ctx == nil || decisionNow < 0 || decisionNow > maxUnixSecond ||
		limit < 1 || limit > routingCleanupBatch {
		return lifecycle.WorkResult{}, ErrInvalidInput
	}
	workCtx := ctx
	var cancel context.CancelFunc
	if !budgetDeadline.IsZero() {
		workCtx, cancel = context.WithDeadline(ctx, budgetDeadline)
		defer cancel()
	}
	tx, err := owner.db.BeginTx(workCtx, nil)
	if err != nil {
		return lifecycle.WorkResult{}, fmt.Errorf("claim: begin charity routing cleanup: %w", err)
	}
	defer tx.Rollback()
	processed, err := cleanupRoutingLimitedTx(workCtx, tx, decisionNow, limit)
	if err != nil {
		return lifecycle.WorkResult{}, err
	}
	more, err := hasRoutingCleanupWorkTx(workCtx, tx, decisionNow)
	if err != nil {
		return lifecycle.WorkResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return lifecycle.WorkResult{}, fmt.Errorf("claim: commit charity routing cleanup: %w", err)
	}
	return lifecycle.WorkResult{Processed: processed, More: more}, nil
}

var _ lifecycle.CharityRoutingLifecycle = (*CharityRoutingLifecycle)(nil)
