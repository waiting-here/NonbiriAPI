package adapters

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/donation"
	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
)

// DonationRecoveryOwner is the bounded donation expiry seam. Donation keeps
// the expiry state machine and transaction; lifecycle supplies one frozen
// decision time and the worker budget.
type DonationRecoveryOwner interface {
	MaterializeExpiries(context.Context, int64, int) (int, error)
}

type DonationRecoveryAdapter struct {
	owner DonationRecoveryOwner
}

func NewDonationRecovery(owner DonationRecoveryOwner) *DonationRecoveryAdapter {
	return &DonationRecoveryAdapter{owner: owner}
}

func (adapter *DonationRecoveryAdapter) RecoverBeforeListener(
	ctx context.Context,
	decisionNow int64,
	limit int,
	budgetDeadline time.Time,
) (lifecycle.WorkResult, error) {
	if adapter == nil || adapter.owner == nil {
		return lifecycle.WorkResult{}, lifecycle.ErrUnavailable
	}
	if ctx == nil || decisionNow < 0 || decisionNow > maxUnixSecond ||
		limit < 1 || limit > lifecycle.WorkerBatchLimit || budgetDeadline.IsZero() {
		return lifecycle.WorkResult{}, lifecycle.ErrInvalid
	}
	if ctx.Err() != nil {
		return lifecycle.WorkResult{}, lifecycle.ErrUnavailable
	}
	workerCtx, cancel := context.WithDeadline(ctx, budgetDeadline)
	defer cancel()
	processed, err := adapter.owner.MaterializeExpiries(workerCtx, decisionNow, limit)
	if err != nil {
		return lifecycle.WorkResult{}, translateDonationRecoveryError(err)
	}
	return lifecycle.WorkResult{Processed: processed, More: processed == limit}, nil
}

func translateDonationRecoveryError(err error) error {
	if err == nil {
		return nil
	}
	var target error
	switch {
	case errors.Is(err, donation.ErrInvalidRequest):
		target = lifecycle.ErrInvalid
	case errors.Is(err, donation.ErrConflict):
		target = lifecycle.ErrConflict
	case errors.Is(err, donation.ErrInvariant):
		target = lifecycle.ErrInvariant
	case errors.Is(err, donation.ErrUnavailable), errors.Is(err, context.Canceled),
		errors.Is(err, context.DeadlineExceeded):
		target = lifecycle.ErrUnavailable
	default:
		target = lifecycle.ErrUnavailable
	}
	return fmt.Errorf("%w: %v", target, err)
}

var (
	_ DonationRecoveryOwner     = (*donation.Service)(nil)
	_ lifecycle.RecoveryAdapter = (*DonationRecoveryAdapter)(nil)
)
