package charityrouting

import (
	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/credits"
	"github.com/waiting-here/NonbiriAPI/internal/db"
)

// Settlement math (frozen implementation contract §5.1/§5.3): the four token
// buckets are summed against their unit prices BEFORE the single ceil division
// by one million; the discount applies to the already-rounded original and
// rounds up again; donor rewards use their own price table and are never
// discounted. Per-request mode uses the exact fixed pair (reward undiscounted).
// No float ever participates; every step fails closed on overflow.

// computeCommitPlan derives the settlement values for one dispatched attempt
// from the persisted snapshot plus the connector's usage report.
//
// A valid usage object commits the ACTUAL charge regardless of protocol
// success ("完整合法 usage（成功或异常结束）"); a missing or malformed usage
// object commits under unknown semantics: the user keeps paying the discounted
// reserve, the key consumes the undiscounted reserve, reward 0.
func computeCommitPlan(snapshot db.CharityPricingSnapshot, userReserve, keyReserve int64, result connectorcontract.AttemptResult) (db.CommitPlan, error) {
	if !result.Usage.Present {
		return db.CommitPlan{
			OriginalCharge: keyReserve,
			UserCharge:     userReserve,
			DonorReward:    0,
			UsageUnknown:   true,
		}, nil
	}
	usage := credits.TokenUsage{
		UncachedInput:   result.Usage.UncachedInputTokens,
		CacheWriteInput: result.Usage.CacheWriteInputTokens,
		CacheReadInput:  result.Usage.CacheReadInputTokens,
		Output:          result.Usage.OutputTokens,
	}
	userPrices := credits.TokenPrices{
		UncachedInput:   snapshot.UncachedUserPrice,
		CacheWriteInput: snapshot.CacheWriteUserPrice,
		CacheReadInput:  snapshot.CacheReadUserPrice,
		Output:          snapshot.OutputUserPrice,
	}
	rewardPrices := credits.TokenPrices{
		UncachedInput:   snapshot.UncachedDonorReward,
		CacheWriteInput: snapshot.CacheWriteDonorReward,
		CacheReadInput:  snapshot.CacheReadDonorReward,
		Output:          snapshot.OutputDonorReward,
	}
	switch snapshot.PricingMode {
	case db.CharityPricingPerRequest:
		original := snapshot.RequestUserPrice
		reward := snapshot.RequestDonorReward
		discounted, err := credits.ApplyDiscountPercent(original, snapshot.DiscountPercent)
		if err != nil {
			return db.CommitPlan{}, err
		}
		plan := db.CommitPlan{
			OriginalCharge:        original,
			UserCharge:            discounted,
			DonorReward:           reward,
			UncachedInputTokens:   usage.UncachedInput,
			CacheWriteInputTokens: usage.CacheWriteInput,
			CacheReadInputTokens:  usage.CacheReadInput,
			OutputTokens:          usage.Output,
		}
		return plan, nil
	case db.CharityPricingPerToken:
		original, err := credits.PriceTokenUsage(usage, userPrices)
		if err != nil {
			return db.CommitPlan{}, err
		}
		reward, err := credits.PriceTokenUsage(usage, rewardPrices)
		if err != nil {
			return db.CommitPlan{}, err
		}
		discounted, err := credits.ApplyDiscountPercent(original, snapshot.DiscountPercent)
		if err != nil {
			return db.CommitPlan{}, err
		}
		return db.CommitPlan{
			OriginalCharge:        original,
			UserCharge:            discounted,
			DonorReward:           reward,
			UncachedInputTokens:   usage.UncachedInput,
			CacheWriteInputTokens: usage.CacheWriteInput,
			CacheReadInputTokens:  usage.CacheReadInput,
			OutputTokens:          usage.Output,
		}, nil
	default:
		return db.CommitPlan{}, credits.ErrInvalidAmount
	}
}
