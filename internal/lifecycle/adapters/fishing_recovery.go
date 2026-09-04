package adapters

import (
	"context"
	"time"

	gameRuntime "github.com/waiting-here/NonbiriAPI/internal/game/runtime"
	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
)

// FishingRecoveryOwner keeps settlement, retry, rank, and response recovery
// inside the Fishing runtime while lifecycle supplies frozen worker inputs.
type FishingRecoveryOwner interface {
	RecoverBeforeListenAt(context.Context, int64, int, time.Time) (gameRuntime.RecoveryResult, error)
}

type FishingRecoveryAdapter struct {
	owner FishingRecoveryOwner
}

func NewFishingRecovery(owner FishingRecoveryOwner) *FishingRecoveryAdapter {
	return &FishingRecoveryAdapter{owner: owner}
}

func (adapter *FishingRecoveryAdapter) RecoverBeforeListener(
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
	_ FishingRecoveryOwner      = (*gameRuntime.Service)(nil)
	_ lifecycle.RecoveryAdapter = (*FishingRecoveryAdapter)(nil)
)
