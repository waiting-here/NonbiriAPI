package activities

import (
	"context"
	"time"
)

// ThursdayRecoveryResult is the closed bounded result consumed by the
// cross-domain lifecycle coordinator.
type ThursdayRecoveryResult struct {
	Processed int
	More      bool
}

// RecoverThursday advances one durable Thursday settlement checkpoint using
// the coordinator's frozen decision time. Activities retains ownership of the
// transaction, ledger plans, and settlement reducer.
func (service *Service) RecoverThursday(
	ctx context.Context,
	decisionNow int64,
	limit int,
	budgetDeadline time.Time,
) (ThursdayRecoveryResult, error) {
	if service == nil || service.repository == nil || ctx == nil || decisionNow < 0 ||
		decisionNow > maxUnixSecond || limit < 1 || limit > SettlementBatchSize || budgetDeadline.IsZero() {
		return ThursdayRecoveryResult{}, ErrInvalidRequest
	}
	workerCtx, cancel := context.WithDeadline(ctx, budgetDeadline)
	defer cancel()

	result, facts, err := service.repository.runSettlementStepAt(workerCtx, decisionNow, limit)
	if err != nil {
		return ThursdayRecoveryResult{}, err
	}
	if !result.Changed {
		return ThursdayRecoveryResult{}, nil
	}

	processed := result.ProcessedRows
	if processed == 0 {
		// Freezing a due period and finalizing an empty period are each one
		// durable unit even though neither consumes a participant row.
		processed = 1
	}
	service.PublishCommittedFacts(workerCtx, facts)
	return ThursdayRecoveryResult{Processed: processed, More: result.More}, nil
}
