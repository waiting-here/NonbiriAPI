package adapters

import (
	"context"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/game/rps"
	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
)

// RPSRecoveryOwner keeps queue, phase, and terminal state transitions inside
// the RPS domain while lifecycle supplies the frozen worker inputs.
type RPSRecoveryOwner interface {
	RecoverBeforeListenAt(context.Context, int64, int, time.Time) (rps.RecoveryResult, error)
}

type RPSRecoveryAdapter struct {
	owner RPSRecoveryOwner
}

func NewRPSRecovery(owner RPSRecoveryOwner) *RPSRecoveryAdapter {
	return &RPSRecoveryAdapter{owner: owner}
}

func (adapter *RPSRecoveryAdapter) RecoverBeforeListener(
	ctx context.Context,
	decisionNow int64,
	limit int,
	budgetDeadline time.Time,
) (lifecycle.WorkResult, error) {
	if adapter == nil || adapter.owner == nil {
		return lifecycle.WorkResult{}, lifecycle.ErrUnavailable
	}
	result, err := adapter.owner.RecoverBeforeListenAt(ctx, decisionNow, limit, budgetDeadline)
	if err != nil {
		return lifecycle.WorkResult{}, err
	}
	return lifecycle.WorkResult{Processed: result.Processed, More: result.More}, nil
}

var (
	_ RPSRecoveryOwner          = (*rps.Service)(nil)
	_ lifecycle.RecoveryAdapter = (*RPSRecoveryAdapter)(nil)
)
