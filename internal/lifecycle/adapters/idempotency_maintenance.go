package adapters

import (
	"context"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
)

type IdempotencyMaintenanceOwner interface {
	Recover(context.Context, int64, int, time.Time) (idempotency.MaintenanceResult, error)
	Retain(context.Context, int64, int, time.Time) (idempotency.MaintenanceResult, error)
}

// IdempotencyMaintenanceAdapter fills both fixed lifecycle idempotency slots.
type IdempotencyMaintenanceAdapter struct {
	owner IdempotencyMaintenanceOwner
}

func NewIdempotencyMaintenance(owner IdempotencyMaintenanceOwner) *IdempotencyMaintenanceAdapter {
	return &IdempotencyMaintenanceAdapter{owner: owner}
}

func (adapter *IdempotencyMaintenanceAdapter) RecoverBeforeListener(
	ctx context.Context,
	decisionNow int64,
	limit int,
	budgetDeadline time.Time,
) (lifecycle.WorkResult, error) {
	if adapter == nil || adapter.owner == nil {
		return lifecycle.WorkResult{}, lifecycle.ErrUnavailable
	}
	result, err := adapter.owner.Recover(ctx, decisionNow, limit, budgetDeadline)
	if err != nil {
		return lifecycle.WorkResult{}, err
	}
	return lifecycle.WorkResult{Processed: result.Processed, More: result.More}, nil
}

func (adapter *IdempotencyMaintenanceAdapter) Retain(
	ctx context.Context,
	decisionNow int64,
	limit int,
	budgetDeadline time.Time,
) (lifecycle.WorkResult, error) {
	if adapter == nil || adapter.owner == nil {
		return lifecycle.WorkResult{}, lifecycle.ErrUnavailable
	}
	result, err := adapter.owner.Retain(ctx, decisionNow, limit, budgetDeadline)
	if err != nil {
		return lifecycle.WorkResult{}, err
	}
	return lifecycle.WorkResult{Processed: result.Processed, More: result.More}, nil
}

var (
	_ IdempotencyMaintenanceOwner = (*idempotency.Maintenance)(nil)
	_ lifecycle.RecoveryAdapter   = (*IdempotencyMaintenanceAdapter)(nil)
	_ lifecycle.RetentionAdapter  = (*IdempotencyMaintenanceAdapter)(nil)
)
