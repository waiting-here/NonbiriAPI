package adapters

import (
	"context"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/auth"
	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
)

// AuthSessionRetentionOwner keeps session SQL and its transaction inside the
// auth domain while accepting lifecycle's frozen time and bounded budget.
type AuthSessionRetentionOwner interface {
	RetainSessionsAt(context.Context, int64, int, time.Time) (auth.LifecycleRetentionResult, error)
}

type AuthSessionRetentionAdapter struct {
	owner AuthSessionRetentionOwner
}

func NewAuthSessionRetention(owner AuthSessionRetentionOwner) *AuthSessionRetentionAdapter {
	return &AuthSessionRetentionAdapter{owner: owner}
}

func (adapter *AuthSessionRetentionAdapter) Retain(
	ctx context.Context,
	decisionNow int64,
	limit int,
	budgetDeadline time.Time,
) (lifecycle.WorkResult, error) {
	if adapter == nil || adapter.owner == nil {
		return lifecycle.WorkResult{}, lifecycle.ErrUnavailable
	}
	result, err := adapter.owner.RetainSessionsAt(ctx, decisionNow, limit, budgetDeadline)
	if err != nil {
		return lifecycle.WorkResult{}, err
	}
	return lifecycle.WorkResult{Processed: result.Processed, More: result.More}, nil
}

var (
	_ AuthSessionRetentionOwner  = (*auth.Runtime)(nil)
	_ lifecycle.RetentionAdapter = (*AuthSessionRetentionAdapter)(nil)
)
