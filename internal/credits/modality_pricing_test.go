package credits

import (
	"errors"
	"math"
	"testing"
)

func TestModalityPricingIndependentSidesAndSingleRounding(t *testing.T) {
	u := TokenUsage{UncachedInput: 3, CacheWriteInput: 1, CacheReadInput: 2, Output: 4}
	m := ModalityUsage{InputPresent: true, OutputPresent: true, Input: [3][4]int64{{1, 2, 0, 0}, {0, 1, 0, 0}, {1, 1, 0, 0}}, Output: [4]int64{0, 0, 4, 0}}
	p := ModalityPricing{InputEnabled: true, OutputEnabled: true, Text: &TokenPrices{UncachedInput: 1, CacheReadInput: 2}, Image: &TokenPrices{UncachedInput: 3, CacheWriteInput: 4, CacheReadInput: 5}, Audio: &TokenPrices{Output: 6}}
	fallback := TokenPrices{UncachedInput: 1_000_000, CacheWriteInput: 1_000_000, CacheReadInput: 1_000_000, Output: 1_000_000}
	got, decision, err := PriceTokenUsageWithModalities(u, fallback, m, p)
	if err != nil || got != 1 || decision.InputMode != "modality" || decision.OutputMode != "modality" {
		t.Fatal(got, decision, err)
	}
	m.InputPresent = false
	m.InputReason = "cache_details_missing"
	got, decision, err = PriceTokenUsageWithModalities(u, fallback, m, p)
	if err != nil || got != 7 || decision.InputMode != "aggregate" || decision.OutputMode != "modality" || decision.InputFallback != "cache_details_missing" {
		t.Fatal(got, decision, err)
	}
	m.InputPresent = true
	p.Audio = nil
	got, decision, err = PriceTokenUsageWithModalities(u, fallback, m, p)
	if err != nil || got != 5 || decision.InputMode != "modality" || decision.OutputFallback != "missing_price" {
		t.Fatal(got, decision, err)
	}
}

func TestModalityPricingFallbackAndZero(t *testing.T) {
	u := TokenUsage{UncachedInput: 2, Output: 3}
	p := ModalityPricing{InputEnabled: true, OutputEnabled: true, Text: &TokenPrices{}}
	for _, m := range []ModalityUsage{
		{InputPresent: true, OutputPresent: true, Input: [3][4]int64{{1, 0, 0, 0}}, Output: [4]int64{-1, 4, 0, 0}},
		{InputReason: "upstream-secret-that-must-not-be-logged", OutputReason: "invalid_details"},
	} {
		got, decision, err := PriceTokenUsageWithModalities(u, TokenPrices{UncachedInput: 1_000_000, Output: 1_000_000}, m, p)
		if err != nil || got != 5 || decision.InputMode != "aggregate" || decision.OutputMode != "aggregate" || decision.InputFallback == m.InputReason && len(m.InputReason) > 40 {
			t.Fatal(got, decision, err)
		}
	}
	m := ModalityUsage{InputPresent: true, OutputPresent: true, Input: [3][4]int64{{2}}, Output: [4]int64{3}}
	got, decision, err := PriceTokenUsageWithModalities(u, TokenPrices{UncachedInput: 1_000_000, Output: 1_000_000}, m, p)
	if err != nil || got != 0 || decision.InputMode != "modality" || decision.OutputMode != "modality" {
		t.Fatal(got, decision, err)
	}
	// No configured modality is necessary for a complete explicit zero report.
	got, decision, err = PriceTokenUsageWithModalities(TokenUsage{}, TokenPrices{}, ModalityUsage{InputPresent: true, OutputPresent: true}, ModalityPricing{InputEnabled: true, OutputEnabled: true})
	if err != nil || got != 0 || decision.InputMode != "modality" || decision.OutputMode != "modality" {
		t.Fatal(got, decision, err)
	}
}

func TestModalityPricingOverflowAndAggregateCompatibility(t *testing.T) {
	u := TokenUsage{UncachedInput: math.MaxInt64, Output: math.MaxInt64}
	m := ModalityUsage{InputPresent: true, OutputPresent: true, Input: [3][4]int64{{math.MaxInt64}}, Output: [4]int64{math.MaxInt64}}
	p := ModalityPricing{InputEnabled: true, OutputEnabled: true, Text: &TokenPrices{UncachedInput: math.MaxInt64, Output: math.MaxInt64}}
	if _, _, err := PriceTokenUsageWithModalities(u, TokenPrices{}, m, p); !errors.Is(err, ErrOverflow) {
		t.Fatal(err)
	}
	if _, _, err := PriceTokenUsageWithModalities(TokenUsage{}, TokenPrices{}, m, ModalityPricing{Text: &TokenPrices{Output: -1}}); !errors.Is(err, ErrInvalidAmount) {
		t.Fatal(err)
	}
	u = TokenUsage{UncachedInput: 12345, CacheWriteInput: 555, CacheReadInput: 123, Output: 4567}
	fallback := TokenPrices{UncachedInput: 123, CacheWriteInput: 456, CacheReadInput: 789, Output: 321}
	want, _ := PriceTokenUsage(u, fallback)
	got, decision, err := PriceTokenUsageWithModalities(u, fallback, m, ModalityPricing{})
	if err != nil || got != want || decision.InputFallback != "" || decision.OutputFallback != "" {
		t.Fatal(got, want, decision, err)
	}
}

func TestSearchUnitPriceExact(t *testing.T) {
	for _, tc := range []struct{ units, price, want int64 }{{1, 1, 1}, {1_000_001, 1000, 1001}, {1_234_567, 1_000_000, 1_234_567}, {math.MaxInt64, 0, 0}} {
		if got, err := PriceSearchUnits(tc.units, tc.price); err != nil || got != tc.want {
			t.Fatal(tc, got, err)
		}
	}
	if _, err := PriceSearchUnits(math.MaxInt64, math.MaxInt64); !errors.Is(err, ErrOverflow) {
		t.Fatal(err)
	}
	if _, err := PriceSearchUnits(-1, 0); !errors.Is(err, ErrInvalidAmount) {
		t.Fatal(err)
	}
}

func TestContextModalityAndRewardSelection(t *testing.T) {
	p, err := DecodeContextPricing(`{"user_modalities":{"input_enabled":true,"text":{"uncached_input":10}},"donor_modalities":{"output_enabled":true,"audio":{"output":20}},"long_context":{"input_threshold":10,"user_price_milli":1,"user_prices_milli":[2,3,4,5]}}`)
	if err != nil {
		t.Fatal(err)
	}
	base := TokenPrices{Output: 77}
	request, reward := p.SelectRewards(TokenUsage{UncachedInput: 10}, 99, base)
	if request != 99 || reward != base || !p.SelectModalityPricing(TokenUsage{UncachedInput: 10}, true).OutputEnabled || p.SelectModalityPricing(TokenUsage{UncachedInput: 10}, false).InputEnabled {
		t.Fatal(request, reward, p)
	}
	if !p.SelectModalityPricing(TokenUsage{UncachedInput: 9}, false).InputEnabled {
		t.Fatal("base modality lost")
	}
	zero := int64(0)
	prices := [4]int64{7, 8, 9, 10}
	p.LongContext.DonorReward = &zero
	p.LongContext.DonorPrices = &prices
	request, reward = p.SelectRewards(TokenUsage{CacheReadInput: 10}, 99, base)
	if request != 0 || reward.Output != 10 || p.SelectModalityPricing(TokenUsage{CacheReadInput: 10}, true).OutputEnabled {
		t.Fatal(request, reward)
	}
	p.LongContext.UserModalities = &ModalityPricing{InputEnabled: true, Video: &TokenPrices{UncachedInput: 42}}
	p.LongContext.DonorModalities = &ModalityPricing{OutputEnabled: true, Image: &TokenPrices{Output: 33}}
	roundtrip, err := DecodeContextPricing(p.Encode())
	if err != nil || roundtrip.SelectModalityPricing(TokenUsage{CacheWriteInput: 10}, false).Video.UncachedInput != 42 || roundtrip.SelectModalityPricing(TokenUsage{CacheWriteInput: 10}, true).Image.Output != 33 {
		t.Fatal(roundtrip, err)
	}
	for _, encoded := range []string{`{"user_modalities":{"text":{"output":-1}}}`, `{"long_context":{"input_threshold":10,"donor_reward_milli":-1}}`, `{"long_context":{"input_threshold":10,"donor_rewards_milli":[0,0,0,-1]}}`, `{"long_context":{"input_threshold":10,"donor_modalities":{"audio":{"output":-1}}}}`} {
		if _, err := DecodeContextPricing(encoded); err == nil {
			t.Fatal("accepted", encoded)
		}
	}
}
