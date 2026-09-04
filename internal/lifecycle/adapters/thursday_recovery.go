package adapters

import (
	"context"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/activities"
	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
)

// ThursdayRecoveryOwner keeps every settlement transaction and ledger
// transition inside the activities domain.
type ThursdayRecoveryOwner interface {
	RecoverThursday(context.Context, int64, int, time.Time) (activities.ThursdayRecoveryResult, error)
}

type ThursdayRecoveryAdapter struct {
	owner ThursdayRecoveryOwner
}

func NewThursdayRecovery(owner ThursdayRecoveryOwner) *ThursdayRecoveryAdapter {
	return &ThursdayRecoveryAdapter{owner: owner}
}

func (adapter *ThursdayRecoveryAdapter) RecoverBeforeListener(
	ctx context.Context,
	decisionNow int64,
	limit int,
	budgetDeadline time.Time,
) (lifecycle.WorkResult, error) {
	if adapter == nil || adapter.owner == nil {
		return lifecycle.WorkResult{}, lifecycle.ErrUnavailable
	}
	result, err := adapter.owner.RecoverThursday(ctx, decisionNow, limit, budgetDeadline)
	if err != nil {
		return lifecycle.WorkResult{}, err
	}
	return lifecycle.WorkResult{Processed: result.Processed, More: result.More}, nil
}

var (
	_ ThursdayRecoveryOwner     = (*activities.Service)(nil)
	_ lifecycle.RecoveryAdapter = (*ThursdayRecoveryAdapter)(nil)
)
