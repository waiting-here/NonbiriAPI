package credits

import "math/big"

// ModalityUsage has the same value layout as the normalized connector report.
type ModalityUsage struct {
	Input         [3][4]int64
	Output        [4]int64
	InputPresent  bool
	OutputPresent bool
	InputReason   string
	OutputReason  string
}

// ModalityPricing uses milli-credits per million tokens for charges or rewards.
// A nil modality has no configured prices; a non-nil zero price is free.
type ModalityPricing struct {
	InputEnabled  bool         `json:"input_enabled"`
	OutputEnabled bool         `json:"output_enabled"`
	Text          *TokenPrices `json:"text,omitempty"`
	Image         *TokenPrices `json:"image,omitempty"`
	Audio         *TokenPrices `json:"audio,omitempty"`
	Video         *TokenPrices `json:"video,omitempty"`
}

type ModalityPricingResult struct {
	InputMode      string `json:"input_mode"`
	OutputMode     string `json:"output_mode"`
	InputFallback  string `json:"input_fallback,omitempty"`
	OutputFallback string `json:"output_fallback,omitempty"`
}

func (p ModalityPricing) Validate() error {
	for _, rate := range p.rates() {
		if rate != nil {
			if err := rate.validate(); err != nil {
				return err
			}
		}
	}
	return nil
}

func (p ModalityPricing) rates() [4]*TokenPrices {
	return [4]*TokenPrices{p.Text, p.Image, p.Audio, p.Video}
}

func boundedModalityReason(reason string) string {
	switch reason {
	case "missing_details", "invalid_details", "incomplete_details", "unknown_modality", "cache_details_missing", "total_mismatch", "reasoning_details_missing":
		return reason
	default:
		return "missing_details"
	}
}

// PriceTokenUsageWithModalities independently selects each side and rounds the
// sum once. Invalid classifications fall back without altering valid totals.
func PriceTokenUsageWithModalities(usage TokenUsage, prices TokenPrices, modalities ModalityUsage, pricing ModalityPricing) (int64, ModalityPricingResult, error) {
	decision := ModalityPricingResult{InputMode: "aggregate", OutputMode: "aggregate"}
	if err := usage.validate(); err != nil {
		return 0, decision, err
	}
	if err := prices.validate(); err != nil {
		return 0, decision, err
	}
	if err := pricing.Validate(); err != nil {
		return 0, decision, err
	}
	rates := pricing.rates()
	input := pricing.InputEnabled && modalities.InputPresent
	output := pricing.OutputEnabled && modalities.OutputPresent
	if pricing.InputEnabled && !input {
		decision.InputFallback = boundedModalityReason(modalities.InputReason)
	}
	if pricing.OutputEnabled && !output {
		decision.OutputFallback = boundedModalityReason(modalities.OutputReason)
	}
	inputTotals := [3]int64{usage.UncachedInput, usage.CacheWriteInput, usage.CacheReadInput}
	if input {
		for b, tokens := range modalities.Input {
			if !modalitySumMatches(tokens, inputTotals[b]) {
				input = false
				decision.InputFallback = "total_mismatch"
				break
			}
			for m, n := range tokens {
				if n > 0 && rates[m] == nil {
					input = false
					decision.InputFallback = "missing_price"
				}
			}
		}
	}
	if output {
		if !modalitySumMatches(modalities.Output, usage.Output) {
			output = false
			decision.OutputFallback = "total_mismatch"
		}
		for m, n := range modalities.Output {
			if n > 0 && rates[m] == nil {
				output = false
				decision.OutputFallback = "missing_price"
			}
		}
	}
	sum := new(big.Int)
	add := func(tokens, rate int64) { sum.Add(sum, new(big.Int).Mul(big.NewInt(tokens), big.NewInt(rate))) }
	if input {
		decision.InputMode = "modality"
		for m, rate := range rates {
			if rate != nil {
				add(modalities.Input[0][m], rate.UncachedInput)
				add(modalities.Input[1][m], rate.CacheWriteInput)
				add(modalities.Input[2][m], rate.CacheReadInput)
			}
		}
	} else {
		add(usage.UncachedInput, prices.UncachedInput)
		add(usage.CacheWriteInput, prices.CacheWriteInput)
		add(usage.CacheReadInput, prices.CacheReadInput)
	}
	if output {
		decision.OutputMode = "modality"
		for m, rate := range rates {
			if rate != nil {
				add(modalities.Output[m], rate.Output)
			}
		}
	} else {
		add(usage.Output, prices.Output)
	}
	v, err := ceilDivBig(sum, millionMilli)
	if err != nil {
		return 0, decision, err
	}
	amount, err := bigToInt64(v)
	return amount, decision, err
}

func modalitySumMatches(tokens [4]int64, total int64) bool {
	remaining := total
	for _, n := range tokens {
		if n < 0 || n > remaining {
			return false
		}
		remaining -= n
	}
	return remaining == 0
}

// PriceSearchUnits prices millionths of a search unit at a per-unit milli rate.
func PriceSearchUnits(micros, perUnitMilli int64) (int64, error) {
	if micros < 0 || perUnitMilli < 0 {
		return 0, ErrInvalidAmount
	}
	v, err := ceilDivBig(new(big.Int).Mul(big.NewInt(micros), big.NewInt(perUnitMilli)), millionMilli)
	if err != nil {
		return 0, err
	}
	return bigToInt64(v)
}
