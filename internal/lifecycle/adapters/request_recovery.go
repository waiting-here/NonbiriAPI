package adapters

import (
	"context"
	"sync"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/claim"
	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

type discoveryRecoveryOwner interface {
	RecoverStaleDiscoveriesAt(context.Context, int64, int, time.Time) (resources.DiscoveryRecoveryResult, error)
}

type claimRecoveryOwner interface {
	RecoverNonterminalAt(context.Context, int64, int, time.Time) (claim.RecoveryReport, error)
}

type orphanSecretRecoveryOwner interface {
	MaintainOrphanSecretsAt(context.Context, int64, int, time.Time) (claim.MaintenanceReport, error)
}

type DiscoveryRecoveryAdapter struct {
	owner discoveryRecoveryOwner
}

func NewDiscoveryRecovery(owner *resources.Repository) *DiscoveryRecoveryAdapter {
	if owner == nil {
		return &DiscoveryRecoveryAdapter{}
	}
	return newDiscoveryRecovery(owner)
}

func newDiscoveryRecovery(owner discoveryRecoveryOwner) *DiscoveryRecoveryAdapter {
	return &DiscoveryRecoveryAdapter{owner: owner}
}

func (adapter *DiscoveryRecoveryAdapter) RecoverBeforeListener(
	ctx context.Context,
	decisionNow int64,
	limit int,
	budgetDeadline time.Time,
) (lifecycle.WorkResult, error) {
	if adapter == nil || adapter.owner == nil {
		return lifecycle.WorkResult{}, lifecycle.ErrUnavailable
	}
	result, err := adapter.owner.RecoverStaleDiscoveriesAt(ctx, decisionNow, limit, budgetDeadline)
	if err != nil {
		return lifecycle.WorkResult{}, err
	}
	return lifecycle.WorkResult{Processed: result.Processed, More: result.More}, nil
}

type ClaimRecoveryAdapter struct {
	owner    claimRecoveryOwner
	mu       sync.Mutex
	finished bool
}

func NewClaimRecovery(owner *claim.Service) *ClaimRecoveryAdapter {
	if owner == nil {
		return &ClaimRecoveryAdapter{}
	}
	return newClaimRecovery(owner)
}

func newClaimRecovery(owner claimRecoveryOwner) *ClaimRecoveryAdapter {
	return &ClaimRecoveryAdapter{owner: owner}
}

func (adapter *ClaimRecoveryAdapter) RecoverBeforeListener(
	ctx context.Context,
	decisionNow int64,
	limit int,
	budgetDeadline time.Time,
) (lifecycle.WorkResult, error) {
	if adapter == nil || adapter.owner == nil {
		return lifecycle.WorkResult{}, lifecycle.ErrUnavailable
	}
	if ctx == nil {
		return lifecycle.WorkResult{}, lifecycle.ErrInvalid
	}
	if limit < 1 || limit > claim.MaxRecoveryBatch {
		return lifecycle.WorkResult{}, claim.ErrInvalidInput
	}
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return lifecycle.WorkResult{}, err
	}
	// Only a new process may reconcile claims left by its predecessor. The
	// coordinator also visits this slot during maintenance while requests run.
	if adapter.finished {
		return lifecycle.WorkResult{}, nil
	}
	// Each item owns a separate transaction and may post several ledger rows.
	// A busy restart must not spend one short worker budget on up to 100 such
	// transactions. Yield after one durable item; the coordinator supplies a
	// fresh budget for the next while its overall startup deadline still applies.
	const batchLimit = 1
	report, err := adapter.owner.RecoverNonterminalAt(ctx, decisionNow, batchLimit, budgetDeadline)
	if err != nil {
		return lifecycle.WorkResult{}, err
	}
	processed := report.ReleasedClaims + report.CommittedClaims + report.CompletedRequests
	if report.ReleasedClaims < 0 || report.ReleasedClaims > batchLimit ||
		report.CommittedClaims < 0 || report.CommittedClaims > batchLimit ||
		report.CompletedRequests < 0 || report.CompletedRequests > batchLimit ||
		processed < 0 || processed > batchLimit || report.More && processed == 0 {
		return lifecycle.WorkResult{}, lifecycle.ErrInvariant
	}
	adapter.finished = !report.More
	return lifecycle.WorkResult{Processed: processed, More: report.More}, nil
}

type OrphanSecretRecoveryAdapter struct {
	owner orphanSecretRecoveryOwner
}

func NewOrphanSecretRecovery(owner *claim.Service) *OrphanSecretRecoveryAdapter {
	if owner == nil {
		return &OrphanSecretRecoveryAdapter{}
	}
	return newOrphanSecretRecovery(owner)
}

func newOrphanSecretRecovery(owner orphanSecretRecoveryOwner) *OrphanSecretRecoveryAdapter {
	return &OrphanSecretRecoveryAdapter{owner: owner}
}

func (adapter *OrphanSecretRecoveryAdapter) RecoverBeforeListener(
	ctx context.Context,
	decisionNow int64,
	limit int,
	budgetDeadline time.Time,
) (lifecycle.WorkResult, error) {
	if adapter == nil || adapter.owner == nil {
		return lifecycle.WorkResult{}, lifecycle.ErrUnavailable
	}
	report, err := adapter.owner.MaintainOrphanSecretsAt(ctx, decisionNow, limit, budgetDeadline)
	if err != nil {
		return lifecycle.WorkResult{}, err
	}
	return lifecycle.WorkResult{Processed: report.Marked + report.Deleted, More: report.More}, nil
}

// Retain reuses the same delete-first orphan-secret maintenance primitive.
// Running it after recovery is normally a no-op, while keeping the frozen
// six-hour retention registry complete and independently retryable.
func (adapter *OrphanSecretRecoveryAdapter) Retain(
	ctx context.Context,
	decisionNow int64,
	limit int,
	budgetDeadline time.Time,
) (lifecycle.WorkResult, error) {
	return adapter.RecoverBeforeListener(ctx, decisionNow, limit, budgetDeadline)
}

var (
	_ discoveryRecoveryOwner     = (*resources.Repository)(nil)
	_ claimRecoveryOwner         = (*claim.Service)(nil)
	_ orphanSecretRecoveryOwner  = (*claim.Service)(nil)
	_ lifecycle.RecoveryAdapter  = (*DiscoveryRecoveryAdapter)(nil)
	_ lifecycle.RecoveryAdapter  = (*ClaimRecoveryAdapter)(nil)
	_ lifecycle.RecoveryAdapter  = (*OrphanSecretRecoveryAdapter)(nil)
	_ lifecycle.RetentionAdapter = (*OrphanSecretRecoveryAdapter)(nil)
)
