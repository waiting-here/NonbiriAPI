package charityrouting

// Pricing settlement tests: the frozen §5.1 formula and §5.2 reserves, plus
// the §5.3 settlement branches (actual, unknown, discount 0/100, time-window
// boundaries). Pure unit tests over computeCommitPlan / ComputeCharityReserves
// with no DB.

import (
	"errors"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
	"github.com/waiting-here/NonbiriAPI/internal/credits"
	"github.com/waiting-here/NonbiriAPI/internal/db"
)

func tokenSnapshot(discount int) db.CharityPricingSnapshot {
	return db.CharityPricingSnapshot{
		PricingMode:       db.CharityPricingPerToken,
		DiscountPercent:   discount,
		TokenReserveMilli: 1000,
		UncachedUserPrice: 1_000_000, CacheWriteUserPrice: 1_000_000, CacheReadUserPrice: 1_000_000, OutputUserPrice: 1_000_000,
		UncachedDonorReward: 200_000, CacheWriteDonorReward: 200_000, CacheReadDonorReward: 200_000, OutputDonorReward: 200_000,
	}
}

func requestSnapshot(discount int) db.CharityPricingSnapshot {
	return db.CharityPricingSnapshot{
		PricingMode:        db.CharityPricingPerRequest,
		DiscountPercent:    discount,
		RequestUserPrice:   500,
		RequestDonorReward: 100,
	}
}

func TestComputeCharityReservesPerTokenDiscountBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name     string
		discount int
		wantUser int64
		wantKey  int64
	}{
		{"zero", 0, 0, 1000},
		{"fifty", 50, 500, 1000},
		{"hundred", 100, 1000, 1000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			user, key, err := db.ComputeCharityReserves(tokenSnapshot(tc.discount))
			if err != nil {
				t.Fatal(err)
			}
			if user != tc.wantUser || key != tc.wantKey {
				t.Fatalf("reserves = %d/%d, want %d/%d", user, key, tc.wantUser, tc.wantKey)
			}
		})
	}
}

func TestComputeCharityReservesPerRequestDiscountBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name     string
		discount int
		wantUser int64
		wantKey  int64
	}{
		{"zero", 0, 0, 500},
		{"fifty", 50, 250, 500},
		{"hundred", 100, 500, 500},
	} {
		t.Run(tc.name, func(t *testing.T) {
			user, key, err := db.ComputeCharityReserves(requestSnapshot(tc.discount))
			if err != nil {
				t.Fatal(err)
			}
			if user != tc.wantUser || key != tc.wantKey {
				t.Fatalf("reserves = %d/%d, want %d/%d", user, key, tc.wantUser, tc.wantKey)
			}
		})
	}
}

func TestComputeCommitPlanPerTokenSumsBeforeSingleCeil(t *testing.T) {
	snap := tokenSnapshot(50)
	// Four buckets each 1 token; unit price 1_000_000 milli/million.
	// raw_original = 4*1_000_000 = 4_000_000; original = ceil(4_000_000/1_000_000) = 4.
	// user_actual = ceil(4 * 50 / 100) = 2.
	// raw_reward = 4*200_000 = 800_000; reward = ceil(800_000/1_000_000) = 1.
	usage := openai.Usage{UncachedInputTokens: 1, CacheWriteInputTokens: 1, CacheReadInputTokens: 1, OutputTokens: 1, Present: true}
	plan, err := computeCommitPlan(snap, 500, 1000, openai.AttemptResult{Usage: usage, Committed: true})
	if err != nil {
		t.Fatal(err)
	}
	if plan.OriginalCharge != 4 {
		t.Fatalf("original = %d, want 4 (single ceil after sum)", plan.OriginalCharge)
	}
	if plan.UserCharge != 2 {
		t.Fatalf("user_charge = %d, want 2", plan.UserCharge)
	}
	if plan.DonorReward != 1 {
		t.Fatalf("donor_reward = %d, want 1", plan.DonorReward)
	}
	if plan.UsageUnknown {
		t.Fatalf("usage_unknown = true, want false")
	}
}

func TestComputeCommitPlanPerRequestDiscountAndReward(t *testing.T) {
	snap := requestSnapshot(50)
	plan, err := computeCommitPlan(snap, 250, 500, openai.AttemptResult{
		Usage: openai.Usage{Present: true}, Committed: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.OriginalCharge != 500 {
		t.Fatalf("original = %d, want 500", plan.OriginalCharge)
	}
	if plan.UserCharge != 250 {
		t.Fatalf("user_charge = %d, want 250 (50%%)", plan.UserCharge)
	}
	if plan.DonorReward != 100 {
		t.Fatalf("donor_reward = %d, want 100 (undiscounted)", plan.DonorReward)
	}
}

func TestComputeCommitPlanUnknownUsageRewardZero(t *testing.T) {
	snap := requestSnapshot(50)
	plan, err := computeCommitPlan(snap, 250, 500, openai.AttemptResult{Committed: true}) // no Usage.Present
	if err != nil {
		t.Fatal(err)
	}
	if !plan.UsageUnknown {
		t.Fatalf("usage_unknown = false, want true")
	}
	if plan.DonorReward != 0 {
		t.Fatalf("donor_reward = %d, want 0 (unknown)", plan.DonorReward)
	}
	if plan.OriginalCharge != 500 || plan.UserCharge != 250 {
		t.Fatalf("unknown plan charges = %d/%d, want 500/250 (keeps reserve)", plan.OriginalCharge, plan.UserCharge)
	}
}

func TestComputeCommitPlanInvalidModeFailsClosed(t *testing.T) {
	snap := db.CharityPricingSnapshot{PricingMode: "bogus"}
	if _, err := computeCommitPlan(snap, 0, 0, openai.AttemptResult{Usage: openai.Usage{Present: true}}); !errors.Is(err, credits.ErrInvalidAmount) {
		t.Fatalf("bogus mode = %v, want ErrInvalidAmount", err)
	}
}
