package adapters

import (
	"context"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/game/linklink"
	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
)

type linkLinkRecoveryOwner interface {
	RecoverBeforeListenAt(context.Context, int64, int, time.Time) (linklink.RecoveryResult, error)
}

type LinkLinkRecoveryAdapter struct {
	owner linkLinkRecoveryOwner
}

func NewLinkLinkRecovery(owner *linklink.Service) *LinkLinkRecoveryAdapter {
	if owner == nil {
		return &LinkLinkRecoveryAdapter{}
	}
	return newLinkLinkRecovery(owner)
}

func newLinkLinkRecovery(owner linkLinkRecoveryOwner) *LinkLinkRecoveryAdapter {
	return &LinkLinkRecoveryAdapter{owner: owner}
}

func (adapter *LinkLinkRecoveryAdapter) RecoverBeforeListener(
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
	_ linkLinkRecoveryOwner     = (*linklink.Service)(nil)
	_ lifecycle.RecoveryAdapter = (*LinkLinkRecoveryAdapter)(nil)
)
