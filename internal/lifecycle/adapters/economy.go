// Package adapters binds domain-owned lifecycle seams to the closed account
// lifecycle contracts without exposing domain SQL or broad response objects.
package adapters

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/activities"
	"github.com/waiting-here/NonbiriAPI/internal/charity"
	"github.com/waiting-here/NonbiriAPI/internal/donation"
	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
)

// ActivityLifecycleOwner is the complete activity-domain boundary needed by
// account lifecycle. The domain retains all SQL and settlement algorithms.
type ActivityLifecycleOwner interface {
	ExportUserTx(context.Context, *sql.Tx, int64, int) (activities.UserExport, error)
	PrepareUserDeletion(context.Context, *sql.Tx, int64, int64) error
}

// DonationLifecycleOwner is the complete donation-domain boundary needed by
// account lifecycle. Cleanup owns its own bounded transaction.
type DonationLifecycleOwner interface {
	ExportUserTx(context.Context, *sql.Tx, int64, int64, int) ([]donation.ExportDonation, error)
	PrepareAccountDeletion(context.Context, *sql.Tx, int64, int64) error
	Cleanup(context.Context, int64, int) (int, error)
}

// CharityLifecycleOwner exposes only the consumer aggregate and bounded
// terminal cleanup. Claim/ledger own consumer deletion handoff separately.
type CharityLifecycleOwner interface {
	ExportConsumerTx(context.Context, *sql.Tx, int64, int64, int) (charity.ConsumerExport, error)
	Cleanup(context.Context, int64, int) (int, error)
}

type ActivityAdapter struct {
	owner ActivityLifecycleOwner
}

func NewActivity(owner ActivityLifecycleOwner) *ActivityAdapter {
	return &ActivityAdapter{owner: owner}
}

func (adapter *ActivityAdapter) ExportActivities(
	ctx context.Context,
	tx *sql.Tx,
	request lifecycle.ExportRequest,
) ([]lifecycle.WelfareExport, []lifecycle.ThursdayExport, error) {
	if adapter == nil || adapter.owner == nil {
		return nil, nil, lifecycle.ErrUnavailable
	}
	value, err := adapter.owner.ExportUserTx(ctx, tx, request.UserID, request.Limit)
	if err != nil {
		if errors.Is(err, activities.ErrResourceLimit) {
			return nil, nil, lifecycle.ErrTooLarge
		}
		return nil, nil, err
	}
	welfare := make([]lifecycle.WelfareExport, len(value.WelfareClaims))
	for index, item := range value.WelfareClaims {
		welfare[index] = lifecycle.WelfareExport{
			SiteDay: item.SiteDay, Threshold: item.Threshold, Cap: item.Cap,
			Awarded: item.Awarded, CreatedAt: item.CreatedAt,
		}
	}
	thursday := make([]lifecycle.ThursdayExport, len(value.Thursday))
	for index, item := range value.Thursday {
		thursday[index] = lifecycle.ThursdayExport{
			PeriodID: item.PeriodID, PeriodKey: item.PeriodKey, Count: item.Count,
			Contributed: item.Contributed, Eligible: item.Eligible, Settled: item.Settled,
			Payout: item.Payout, UnpaidReason: cloneString(item.UnpaidReason),
			CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt,
		}
	}
	return welfare, thursday, nil
}

func (adapter *ActivityAdapter) PrepareDelete(
	ctx context.Context,
	tx *sql.Tx,
	request lifecycle.DeleteRequest,
) (lifecycle.DeleteFinalizer, error) {
	if adapter == nil || adapter.owner == nil {
		return nil, lifecycle.ErrUnavailable
	}
	if err := adapter.owner.PrepareUserDeletion(ctx, tx, request.UserID, request.DecisionNow); err != nil {
		return nil, err
	}
	return nil, nil
}

type DonationAdapter struct {
	owner DonationLifecycleOwner
}

func NewDonation(owner DonationLifecycleOwner) *DonationAdapter {
	return &DonationAdapter{owner: owner}
}

func (adapter *DonationAdapter) ExportDonations(
	ctx context.Context,
	tx *sql.Tx,
	request lifecycle.ExportRequest,
) ([]lifecycle.DonationExport, error) {
	if adapter == nil || adapter.owner == nil {
		return nil, lifecycle.ErrUnavailable
	}
	values, err := adapter.owner.ExportUserTx(ctx, tx, request.UserID, request.DecisionNow, request.Limit)
	if err != nil {
		if errors.Is(err, donation.ErrResourceLimit) {
			return nil, lifecycle.ErrTooLarge
		}
		return nil, err
	}
	out := make([]lifecycle.DonationExport, len(values))
	for index, value := range values {
		out[index] = lifecycle.DonationExport{
			ID: value.ID, Status: value.Status, Description: value.Description,
			ReviewResult: mapDonationReview(value.ReviewResult),
			ExpiresAt:    cloneInt64(value.ExpiresAt),
			Keys:         mapDonationKeys(value.Keys),
			CreatedAt:    value.CreatedAt,
			UpdatedAt:    value.UpdatedAt,
		}
	}
	return out, nil
}

func (adapter *DonationAdapter) PrepareDelete(
	ctx context.Context,
	tx *sql.Tx,
	request lifecycle.DeleteRequest,
) (lifecycle.DeleteFinalizer, error) {
	if adapter == nil || adapter.owner == nil {
		return nil, lifecycle.ErrUnavailable
	}
	if err := adapter.owner.PrepareAccountDeletion(ctx, tx, request.UserID, request.DecisionNow); err != nil {
		return nil, err
	}
	return nil, nil
}

func (adapter *DonationAdapter) Retain(
	ctx context.Context,
	decisionNow int64,
	limit int,
	budget time.Time,
) (lifecycle.WorkResult, error) {
	if adapter == nil || adapter.owner == nil {
		return lifecycle.WorkResult{}, lifecycle.ErrUnavailable
	}
	workerCtx, cancel := context.WithDeadline(ctx, budget)
	defer cancel()
	processed, err := adapter.owner.Cleanup(workerCtx, decisionNow, limit)
	if err != nil {
		return lifecycle.WorkResult{}, err
	}
	return lifecycle.WorkResult{Processed: processed, More: processed == limit}, nil
}

type CharityAdapter struct {
	owner CharityLifecycleOwner
}

func NewCharity(owner CharityLifecycleOwner) *CharityAdapter {
	return &CharityAdapter{owner: owner}
}

func (adapter *CharityAdapter) ExportCharity(
	ctx context.Context,
	tx *sql.Tx,
	request lifecycle.ExportRequest,
) (lifecycle.CharityExport, error) {
	if adapter == nil || adapter.owner == nil {
		return lifecycle.CharityExport{}, lifecycle.ErrUnavailable
	}
	value, err := adapter.owner.ExportConsumerTx(ctx, tx, request.UserID, request.DecisionNow, request.Limit)
	if err != nil {
		if errors.Is(err, charity.ErrResourceLimit) {
			return lifecycle.CharityExport{}, lifecycle.ErrTooLarge
		}
		return lifecycle.CharityExport{}, err
	}
	return lifecycle.CharityExport{
		RequestCount: value.RequestCount, OriginalCharge: value.OriginalCharge,
		Charged: value.Charged, Saved: value.Saved, DonorReward: value.DonorReward,
		UncachedInput: value.UncachedInput, CacheWriteInput: value.CacheWriteInput,
		CacheReadInput: value.CacheReadInput, Output: value.Output,
		UsageUnknownCount: value.UsageUnknownCount,
	}, nil
}

func (adapter *CharityAdapter) Retain(
	ctx context.Context,
	decisionNow int64,
	limit int,
	budget time.Time,
) (lifecycle.WorkResult, error) {
	if adapter == nil || adapter.owner == nil {
		return lifecycle.WorkResult{}, lifecycle.ErrUnavailable
	}
	workerCtx, cancel := context.WithDeadline(ctx, budget)
	defer cancel()
	processed, err := adapter.owner.Cleanup(workerCtx, decisionNow, limit)
	if err != nil {
		return lifecycle.WorkResult{}, err
	}
	return lifecycle.WorkResult{Processed: processed, More: processed == limit}, nil
}

func mapDonationReview(value *donation.ReviewResult) *lifecycle.DonationReviewExport {
	if value == nil {
		return nil
	}
	return &lifecycle.DonationReviewExport{
		Decision: value.Decision, Reason: value.Reason, ReviewedAt: value.ReviewedAt,
	}
}

func mapDonationKeys(values []donation.DonationKey) []lifecycle.DonationKeyExport {
	out := make([]lifecycle.DonationKeyExport, len(values))
	for index, value := range values {
		out[index] = lifecycle.DonationKeyExport{
			ID: value.ID, EndpointKeyID: cloneString(value.EndpointKeyID),
			DisplayHead: value.DisplayHead, DisplayTail: value.DisplayTail,
			BaseURL: value.SafeSource.BaseURL, ConnectorType: value.SafeSource.ConnectorType,
			PhysicalEnabled: value.PhysicalEnabled, CharityState: value.CharityState,
			Limits: lifecycle.DonationLimitsExport{
				Price: cloneString(value.Limits.Price), Calls: cloneString(value.Limits.Calls),
				Tokens: cloneString(value.Limits.Tokens),
			},
			Usage: lifecycle.DonationUsageExport{
				PriceUsed: value.Usage.PriceUsed, PriceInflight: value.Usage.PriceInflight,
				CallsUsed: value.Usage.CallsUsed, CallsInflight: value.Usage.CallsInflight,
				TokensUsed: value.Usage.TokensUsed, TokensInflight: value.Usage.TokensInflight,
			},
			TokenReserve: value.TokenReserve,
			Streak: lifecycle.DonationStreakExport{
				Generation: value.Streak.Generation, Count: value.Streak.Count,
				FailureDisabled: value.Streak.FailureDisabled,
			},
			EndedReason: cloneString(value.EndedReason),
		}
	}
	return out
}

func cloneString(value *string) *string {
	if value == nil {
		return nil
	}
	out := *value
	return &out
}

func cloneInt64(value *int64) *int64 {
	if value == nil {
		return nil
	}
	out := *value
	return &out
}

var (
	_ lifecycle.ActivityExporter = (*ActivityAdapter)(nil)
	_ lifecycle.DeleteAdapter    = (*ActivityAdapter)(nil)
	_ lifecycle.DonationExporter = (*DonationAdapter)(nil)
	_ lifecycle.DeleteAdapter    = (*DonationAdapter)(nil)
	_ lifecycle.RetentionAdapter = (*DonationAdapter)(nil)
	_ lifecycle.CharityExporter  = (*CharityAdapter)(nil)
	_ lifecycle.RetentionAdapter = (*CharityAdapter)(nil)
)
