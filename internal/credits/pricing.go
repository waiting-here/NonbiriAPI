package credits

// Frozen pricing formula (implementation contract §5.1, accepted C2/C4):
//
//	raw_original = Σ(tokens_bucket × user_unit_price_bucket)
//	original     = ceil(raw_original / 1_000_000)
//	user_actual  = ceil(original × discount_percent / 100)
//	raw_reward   = Σ(tokens_bucket × donor_unit_reward_bucket)
//	donor_reward = ceil(raw_reward / 1_000_000)
//
// The four token buckets are summed BEFORE the single division; per-bucket
// rounding is forbidden. The discount applies to the already-rounded original
// and rounds up again; 0% is strictly 0. Donor rewards use their own price
// table and are never discounted. Key caps accumulate against `original`
// (pre-discount), users pay `user_actual`.

import "math/big"

// TokenUsage is one request's neutral mutually exclusive four-bucket usage.
// Every bucket is non-negative int64; a negative bucket is malformed input
// (fail closed, never clamped).
type TokenUsage struct {
	UncachedInput   int64
	CacheWriteInput int64
	CacheReadInput  int64
	Output          int64
}

func (u TokenUsage) validate() error {
	if u.UncachedInput < 0 || u.CacheWriteInput < 0 || u.CacheReadInput < 0 || u.Output < 0 {
		return ErrInvalidAmount
	}
	return nil
}

// TokenPrices holds four unit prices (or donor rewards) in milli-credits per
// million tokens. Every value is non-negative.
type TokenPrices struct {
	UncachedInput   int64
	CacheWriteInput int64
	CacheReadInput  int64
	Output          int64
}

func (p TokenPrices) validate() error {
	if p.UncachedInput < 0 || p.CacheWriteInput < 0 || p.CacheReadInput < 0 || p.Output < 0 {
		return ErrInvalidAmount
	}
	return nil
}

// PriceTokenUsage computes Σ(bucket × unit_price) over the four buckets in
// exact big-integer arithmetic, then performs ONE ceil division by one
// million. The result is whole milli-credits; overflow beyond int64 fails
// closed. This is used both for the original user charge (user price table)
// and for the donor reward (reward table, never discounted).
func PriceTokenUsage(usage TokenUsage, prices TokenPrices) (int64, error) {
	if err := usage.validate(); err != nil {
		return 0, err
	}
	if err := prices.validate(); err != nil {
		return 0, err
	}
	sum := new(big.Int)
	buckets := []struct {
		tokens int64
		price  int64
	}{
		{usage.UncachedInput, prices.UncachedInput},
		{usage.CacheWriteInput, prices.CacheWriteInput},
		{usage.CacheReadInput, prices.CacheReadInput},
		{usage.Output, prices.Output},
	}
	for _, b := range buckets {
		if b.tokens == 0 || b.price == 0 {
			continue
		}
		sum.Add(sum, new(big.Int).Mul(big.NewInt(b.tokens), big.NewInt(b.price)))
	}
	v, err := ceilDivBig(sum, millionMilli)
	if err != nil {
		return 0, err
	}
	return bigToInt64(v)
}

// ApplyDiscountPercent computes ceil(original × percent / 100). percent must
// be within 0..100; 0 yields exactly 0 (a free request never charges a
// residual milli-credit) and 100 yields original unchanged. Intermediate
// multiplication is exact; a result beyond int64 fails closed.
func ApplyDiscountPercent(original int64, percent int) (int64, error) {
	if original < 0 || percent < 0 || percent > 100 {
		return 0, ErrInvalidAmount
	}
	v, err := ceilDivBig(new(big.Int).Mul(big.NewInt(original), big.NewInt(int64(percent))), hundred)
	if err != nil {
		return 0, err
	}
	return bigToInt64(v)
}
