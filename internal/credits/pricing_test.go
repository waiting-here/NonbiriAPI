package credits

import (
	"errors"
	"math"
	"testing"
)

// TestPriceTokenUsageSumsBeforeCeiling pins the frozen "sum first, divide
// once, round up once" rule: a split of the same raw total must price
// identically regardless of how it is spread across buckets, and the single
// ceil must not be applied per bucket.
func TestPriceTokenUsageSumsBeforeCeiling(t *testing.T) {
	prices := TokenPrices{UncachedInput: 50, CacheWriteInput: 62, CacheReadInput: 5, Output: 250}
	// 2 tokens × 50 + 1 token × 250 = 350 milli per million tokens → raw 350/1e6
	// milli-credit → ceil to 1.
	got, err := PriceTokenUsage(TokenUsage{UncachedInput: 2, Output: 1}, prices)
	if err != nil || got != 1 {
		t.Fatalf("tiny usage price = (%d, %v), want (1, nil)", got, err)
	}

	// Splitting one bucket into two changes nothing (sum is identical).
	a, err := PriceTokenUsage(TokenUsage{UncachedInput: 3, Output: 0}, prices)
	if err != nil {
		t.Fatal(err)
	}
	b, err := PriceTokenUsage(TokenUsage{UncachedInput: 1, Output: 2}, TokenPrices{UncachedInput: 50, Output: 50})
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatalf("split buckets changed the price: %d vs %d", a, b)
	}

	// Per-bucket ceiling would give a larger result here: each of two buckets
	// rounds up independently under the forbidden rule; the frozen formula
	// sums 1+1 = 2 raw micro-units and ceils once.
	split, err := PriceTokenUsage(
		TokenUsage{UncachedInput: 1, Output: 1},
		TokenPrices{UncachedInput: 1, Output: 1})
	if err != nil || split != 1 {
		t.Fatalf("sub-unit sum = (%d, %v), want (1, nil): two sub-unit buckets must not double-round", split, err)
	}
}

func TestPriceTokenUsageExactAndZero(t *testing.T) {
	// Unit prices are per million tokens: 3M uncached @1M milli/MTok = 3,000,000
	// milli-credits; 4M output @2M milli/MTok = 8,000,000.
	prices := TokenPrices{UncachedInput: 1_000_000, Output: 2_000_000}
	got, err := PriceTokenUsage(TokenUsage{UncachedInput: 3_000_000, Output: 4_000_000}, prices)
	if err != nil || got != 11_000_000 {
		t.Fatalf("exact price = (%d, %v), want (11000000, nil)", got, err)
	}
	zero, err := PriceTokenUsage(TokenUsage{}, prices)
	if err != nil || zero != 0 {
		t.Fatalf("zero usage = (%d, %v), want (0, nil)", zero, err)
	}
	// Zero-priced buckets contribute nothing even with huge token counts.
	free, err := PriceTokenUsage(TokenUsage{Output: math.MaxInt64}, TokenPrices{})
	if err != nil || free != 0 {
		t.Fatalf("free bucket = (%d, %v), want (0, nil)", free, err)
	}
}

func TestPriceTokenUsageRejectsInvalidAndOverflows(t *testing.T) {
	if _, err := PriceTokenUsage(TokenUsage{UncachedInput: -1}, TokenPrices{}); !errors.Is(err, ErrInvalidAmount) {
		t.Fatalf("negative usage = %v, want ErrInvalidAmount", err)
	}
	if _, err := PriceTokenUsage(TokenUsage{}, TokenPrices{Output: -5}); !errors.Is(err, ErrInvalidAmount) {
		t.Fatalf("negative price = %v, want ErrInvalidAmount", err)
	}
	// MaxInt64 tokens × MaxInt64 price overflows any int64 → fail closed.
	if _, err := PriceTokenUsage(
		TokenUsage{UncachedInput: math.MaxInt64},
		TokenPrices{UncachedInput: math.MaxInt64}); !errors.Is(err, ErrOverflow) {
		t.Fatalf("overflowing usage = %v, want ErrOverflow", err)
	}
}

func TestApplyDiscountPercent(t *testing.T) {
	cases := []struct {
		original int64
		percent  int
		want     int64
	}{
		{0, 50, 0},
		{1000, 0, 0}, // 0% is strictly zero
		{1000, 100, 1000},
		{1001, 100, 1001},
		{7, 50, 4},     // ceil(3.5)
		{999, 33, 330}, // ceil(329.67)
		{40_000_000, 50, 20_000_000},
		{1, 1, 1}, // ceil(0.01) — never rounds to zero below 0%
	}
	for _, c := range cases {
		got, err := ApplyDiscountPercent(c.original, c.percent)
		if err != nil || got != c.want {
			t.Fatalf("ApplyDiscountPercent(%d,%d%%) = (%d, %v), want (%d, nil)",
				c.original, c.percent, got, err, c.want)
		}
	}
	if _, err := ApplyDiscountPercent(100, -1); !errors.Is(err, ErrInvalidAmount) {
		t.Fatalf("percent -1 = %v, want ErrInvalidAmount", err)
	}
	if _, err := ApplyDiscountPercent(100, 101); !errors.Is(err, ErrInvalidAmount) {
		t.Fatalf("percent 101 = %v, want ErrInvalidAmount", err)
	}
	if _, err := ApplyDiscountPercent(-1, 10); !errors.Is(err, ErrInvalidAmount) {
		t.Fatalf("negative original = %v, want ErrInvalidAmount", err)
	}
	// For 0..100 percent on a non-negative original the discounted result can
	// never exceed the original, so the extreme boundary stays representable.
	if v, err := ApplyDiscountPercent(math.MaxInt64, 100); err != nil || v != math.MaxInt64 {
		t.Fatalf("discount identity at max = (%d, %v)", v, err)
	}
}

// TestReserveFormulaFromClarification pins accepted C1.3: the user reserve is
// ceil(global_token_reserve × discount / 100); 0% yields exactly 0.
func TestReserveFormulaFromClarification(t *testing.T) {
	reserve := int64(10_000_000) // example global reserve in milli-credits
	for _, c := range []struct {
		pct  int
		want int64
	}{
		{100, 10_000_000},
		{80, 8_000_000},
		{33, 3_300_000}, // ceil(3.3m)
		{0, 0},
	} {
		got, err := ApplyDiscountPercent(reserve, c.pct)
		if err != nil || got != c.want {
			t.Fatalf("reserve at %d%% = (%d, %v), want (%d, nil)", c.pct, got, err, c.want)
		}
	}
}
