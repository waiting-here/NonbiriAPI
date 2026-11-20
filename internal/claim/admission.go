package claim

import (
	"context"
	"errors"

	"github.com/waiting-here/NonbiriAPI/internal/donationquota"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	"github.com/waiting-here/NonbiriAPI/internal/requestattempt"
)

// AdmissionReason identifies the decision independently of the sentinel that controls routing.
func AdmissionReason(err error) requestattempt.AdmissionCause {
	if reason := requestattempt.AdmissionCauseOf(err); reason != "" {
		return reason
	}
	var quota *ModelQuotaLimit
	switch {
	case errors.As(err, &quota):
		return requestattempt.AdmissionCooldown
	case errors.Is(err, ErrKeyRateLimited):
		return requestattempt.AdmissionUnspecifiedLimit
	case errors.Is(err, donationquota.ErrLimited):
		return requestattempt.AdmissionBudget
	case errors.Is(err, donationquota.ErrCapacity):
		return requestattempt.AdmissionQuotaStore
	case errors.Is(err, ErrRoutingBusy):
		return requestattempt.AdmissionRouting
	case errors.Is(err, ledger.ErrCapacityExhausted):
		return requestattempt.AdmissionLedgerSpace
	case errors.Is(err, ledger.ErrRetryable):
		return requestattempt.AdmissionLedgerBusy
	case errors.Is(err, ErrNotFound):
		return requestattempt.AdmissionUnavailable
	default:
		return ""
	}
}

func observeCandidateAdmission(ctx context.Context, input ClaimInput, stage string, err error) {
	a := input.Candidate.Authorization
	reason := AdmissionReason(err).RouteReason()
	var quota *ModelQuotaLimit
	if reason != requestattempt.AdmissionMixed.RouteReason() && errors.As(err, &quota) {
		requestattempt.ObserveRecheck(ctx, quota.BindingID, quota.NativeService, quota.Reason, stage)
		return
	}
	if reason != "" {
		requestattempt.ObserveRecheck(ctx, a.BindingID, a.NativeService, reason, stage)
	}
}
