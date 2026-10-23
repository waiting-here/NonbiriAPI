package charity

import (
	"github.com/waiting-here/NonbiriAPI/internal/claim"
	"github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/credits"
	"github.com/waiting-here/NonbiriAPI/internal/usageinfo"
)

func tokenUsage(usage contract.Usage) credits.TokenUsage {
	return credits.TokenUsage{
		UncachedInput: usage.UncachedInputTokens, CacheWriteInput: usage.CacheWriteInputTokens,
		CacheReadInput: usage.CacheReadInputTokens, Output: usage.OutputTokens,
	}
}

func priceUsage(mode string, usage contract.Usage, scalar int64, prices credits.TokenPrices, modalities credits.ModalityPricing) (int64, usageinfo.Pricing, error) {
	decision := usageinfo.Pricing{Mode: mode}
	var amount int64
	var err error
	switch mode {
	case "per_request":
		amount = scalar
	case "per_search_unit":
		amount, err = credits.PriceSearchUnits(usage.SearchUnitsMicros, scalar)
	case "per_token":
		var selected credits.ModalityPricingResult
		amount, selected, err = credits.PriceTokenUsageWithModalities(tokenUsage(usage), prices, credits.ModalityUsage(usage.Modalities), modalities)
		decision.InputMode, decision.OutputMode = selected.InputMode, selected.OutputMode
		decision.InputFallback, decision.OutputFallback = selected.InputFallback, selected.OutputFallback
	default:
		return 0, usageinfo.Pricing{}, claim.ErrInvariant
	}
	if err != nil || !validMoney(amount) {
		return 0, decision, claim.ErrInvariant
	}
	return amount, decision, nil
}

// Old receipts without classified pricing retain their empty decision. Charge
// receipts depend only on charge policies, never on the donor's reward policy.
func recordsPricing(mode string, policy credits.ContextPricing, reward bool) bool {
	if mode == "per_search_unit" {
		return true
	}
	if reward {
		return policy.DonorModalities != nil || policy.LongContext != nil &&
			(policy.LongContext.DonorModalities != nil || policy.LongContext.DonorPrices != nil || policy.LongContext.DonorReward != nil)
	}
	return policy.UserModalities != nil || policy.LongContext != nil && policy.LongContext.UserModalities != nil
}

func equalActual(left, right claim.CharityActual) bool {
	if left.PriceMilli != right.PriceMilli || left.RewardMilli != right.RewardMilli {
		return false
	}
	a, err := usageinfo.DecodePricing(left.PricingDetails)
	if err != nil {
		return false
	}
	b, err := usageinfo.DecodePricing(right.PricingDetails)
	return err == nil && a == b
}
