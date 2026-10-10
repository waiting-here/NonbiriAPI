package credits

import (
	"encoding/json"
	"math/big"
)

// ContextPricing is frozen at request acceptance. Only reported input usage
// can select alternate prices or incur a penalty.
type ContextPricing struct {
	LongContext *LongContextPrice `json:"long_context,omitempty"`
	Penalty     *ContextPenalty   `json:"context_penalty,omitempty"`
}
type LongContextPrice struct {
	InputThreshold int64    `json:"input_threshold"`
	RequestPrice   int64    `json:"user_price_milli"`
	UserPrices     [4]int64 `json:"user_prices_milli"`
}
type ContextPenalty struct {
	InputThreshold int64 `json:"input_threshold"`
	Fine           int64 `json:"fine_milli"`
}

func DecodeContextPricing(encoded string) (ContextPricing, error) {
	var p ContextPricing
	if err := json.Unmarshal([]byte(encoded), &p); err != nil {
		return p, ErrInvalidAmount
	}
	if p.LongContext != nil {
		if p.LongContext.InputThreshold <= 0 || p.LongContext.RequestPrice < 0 {
			return p, ErrInvalidAmount
		}
		for _, v := range p.LongContext.UserPrices {
			if v < 0 {
				return p, ErrInvalidAmount
			}
		}
	}
	if p.Penalty != nil && (p.Penalty.InputThreshold <= 0 || p.Penalty.Fine < 0) {
		return p, ErrInvalidAmount
	}
	return p, nil
}

func (p ContextPricing) Encode() string {
	encoded, _ := json.Marshal(p)
	return string(encoded)
}

// InputReaches includes all mutually exclusive input buckets, never output.
// Big integers avoid overflowing when valid int64 buckets are added together.
func (u TokenUsage) InputReaches(threshold int64) bool {
	if threshold <= 0 || u.validate() != nil {
		return false
	}
	total := new(big.Int).Add(big.NewInt(u.UncachedInput), big.NewInt(u.CacheWriteInput))
	total.Add(total, big.NewInt(u.CacheReadInput))
	return total.Cmp(big.NewInt(threshold)) >= 0
}

func (p ContextPricing) SelectPrices(usage TokenUsage, request int64, tokens TokenPrices) (int64, TokenPrices) {
	if p.LongContext == nil || !usage.InputReaches(p.LongContext.InputThreshold) {
		return request, tokens
	}
	rates := p.LongContext.UserPrices
	return p.LongContext.RequestPrice, TokenPrices{
		UncachedInput: rates[0], CacheWriteInput: rates[1], CacheReadInput: rates[2], Output: rates[3],
	}
}

func (p ContextPricing) Fine(usage TokenUsage) int64 {
	if p.Penalty != nil && usage.InputReaches(p.Penalty.InputThreshold) {
		return p.Penalty.Fine
	}
	return 0
}
