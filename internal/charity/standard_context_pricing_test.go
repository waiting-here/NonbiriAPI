package charity

import (
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/claim"
	"github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/credits"
	"github.com/waiting-here/NonbiriAPI/internal/usageinfo"
)

func TestLongContextRewardAndModalitySettlement(t *testing.T) {
	for _, tc := range []struct {
		name                     string
		rewards                  *[4]int64
		classified               bool
		original, charge, reward int64
	}{
		{"old tier retains base reward classification", nil, false, 81, 67, 12},
		{"aggregate reward override", &[4]int64{3000000, 2000000, 1000000, 4000000}, false, 81, 67, 30},
		{"explicit free reward override", &[4]int64{}, false, 81, 67, 0},
		{"classified overrides", &[4]int64{}, true, 59, 49, 21},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newCharityTestEnv(t)
			rail := e.billingRail(t)
			policy := classifiedBillingPolicy()
			policy.LongContext = &credits.LongContextPrice{InputThreshold: 6, UserPrices: [4]int64{9000000, 8000000, 7000000, 6000000}, DonorPrices: tc.rewards}
			policy.Penalty = &credits.ContextPenalty{InputThreshold: 6, Fine: 7}
			if tc.classified {
				policy.LongContext.UserModalities = &credits.ModalityPricing{InputEnabled: true, OutputEnabled: true,
					Text:  &credits.TokenPrices{UncachedInput: 2000000, CacheReadInput: 2000000, Output: 3000000},
					Image: &credits.TokenPrices{UncachedInput: 4000000, CacheWriteInput: 5000000}, Audio: &credits.TokenPrices{Output: 9000000}}
				policy.LongContext.DonorModalities = &credits.ModalityPricing{InputEnabled: true, OutputEnabled: true,
					Text:  &credits.TokenPrices{UncachedInput: 1000000, CacheReadInput: 1000000, Output: 2000000},
					Image: &credits.TokenPrices{UncachedInput: 2000000, CacheWriteInput: 2000000}, Audio: &credits.TokenPrices{Output: 3000000}}
			}
			setClassifiedBillingPolicy(t, e, policy)
			request := e.billingRequest(t, rail, e.tokenModel, 5, 1)
			setClassifiedBillingPolicy(t, e, credits.ContextPricing{})
			handle := e.billingDispatch(t, rail, request.ID, 1)
			if _, err := rail.CompleteAttempt(t.Context(), handle, claim.AttemptOutcome{Kind: claim.ResultResponse, UpstreamStatus: 200, ResponseStarted: true, ProtocolSuccess: true, Usage: classifiedBillingUsage()}); err != nil {
				t.Fatal(err)
			}
			finishBillingRequest(t, e, rail, request.ID, false)
			e.billingBalance(t, e.callerID, 10000-tc.charge)
			e.billingBalance(t, e.donorID, 10000+tc.reward)
			var original, price int64
			var actions int
			if err := e.store.DB().QueryRow(`SELECT cr.original_charge_milli,u.price_actual_milli,l.automatic_actions FROM charity_reservations cr
JOIN dispatch_claims c ON c.logical_request_id=cr.logical_request_id JOIN donation_usage_reservations u ON u.claim_id=c.id
JOIN request_logs l ON l.logical_request_id=cr.logical_request_id WHERE cr.logical_request_id=?`, request.ID).Scan(&original, &price, &actions); err != nil {
				t.Fatal(err)
			}
			if original != tc.original || price != original-7 || actions != 32 {
				t.Fatal("fine ordering", original, price, actions)
			}
		})
	}
}

func TestLongContextScalarRewards(t *testing.T) {
	for _, mode := range []string{"per_request", "per_search_unit"} {
		t.Run(mode, func(t *testing.T) {
			e := newCharityTestEnv(t)
			rail := e.billingRail(t)
			model, reserve := e.requestModel, int64(2400)
			longPrice, longReward, wantCharge, wantReward := int64(7000), int64(500), int64(5607), int64(500)
			if mode == "per_search_unit" {
				searchModel(t, e, 3, 2)
				model, reserve, longPrice, longReward, wantCharge, wantReward = e.tokenModel, 5, 9, 4, 22, 8
			}
			policy := credits.ContextPricing{LongContext: &credits.LongContextPrice{InputThreshold: 6, RequestPrice: longPrice, DonorReward: &longReward}, Penalty: &credits.ContextPenalty{InputThreshold: 6, Fine: 7}}
			if _, err := e.store.DB().Exec(`UPDATE charity_models SET context_pricing=? WHERE id=?`, policy.Encode(), model); err != nil {
				t.Fatal(err)
			}
			request := e.billingRequest(t, rail, model, reserve, 1)
			handle := e.billingDispatch(t, rail, request.ID, 1)
			usage := contract.Usage{Present: true, UncachedInputTokens: 6, SearchUnitsPresent: true, SearchUnitsMicros: 2000000}
			if _, err := rail.CompleteAttempt(t.Context(), handle, claim.AttemptOutcome{Kind: claim.ResultResponse, UpstreamStatus: 200, ProtocolSuccess: true, ResponseStarted: true, Usage: usage}); err != nil {
				t.Fatal(err)
			}
			finishBillingRequest(t, e, rail, request.ID, false)
			e.billingBalance(t, e.callerID, 10000-wantCharge)
			e.billingBalance(t, e.donorID, 10000+wantReward)
		})
	}
}

func TestClassifiedRequestRoundsOnceAndSelectsAggregateTier(t *testing.T) {
	for _, tier := range []bool{false, true} {
		t.Run(map[bool]string{false: "round after all attempts and buckets", true: "aggregate input selects long tier"}[tier], func(t *testing.T) {
			e := newCharityTestEnv(t)
			rail := e.billingRail(t)
			rates := &credits.ModalityPricing{InputEnabled: true, OutputEnabled: true,
				Text: &credits.TokenPrices{UncachedInput: 100000}, Image: &credits.TokenPrices{UncachedInput: 100000}, Audio: &credits.TokenPrices{Output: 100000}}
			policy := credits.ContextPricing{UserModalities: rates, DonorModalities: rates}
			if tier {
				policy.LongContext = &credits.LongContextPrice{InputThreshold: 4,
					UserModalities: &credits.ModalityPricing{InputEnabled: true, OutputEnabled: true,
						Text: &credits.TokenPrices{UncachedInput: 1000000}, Image: &credits.TokenPrices{UncachedInput: 1000000}, Audio: &credits.TokenPrices{Output: 1000000}},
					DonorPrices: &[4]int64{5000000, 0, 0, 5000000}}
			}
			setClassifiedBillingPolicy(t, e, policy)
			request := e.billingRequest(t, rail, e.tokenModel, 5, 2)
			for seq := 1; seq <= 2; seq++ {
				handle := e.billingDispatch(t, rail, request.ID, seq)
				usage := contract.Usage{Present: true, UncachedInputTokens: 2, OutputTokens: 1, Modalities: contract.ModalityUsage{InputPresent: true, OutputPresent: true, Input: [3][4]int64{{1, 1, 0, 0}}, Output: [4]int64{0, 0, 1, 0}}}
				if _, err := rail.CompleteAttempt(t.Context(), handle, claim.AttemptOutcome{Kind: claim.ResultResponse, UpstreamStatus: 200, ProtocolSuccess: true, ResponseStarted: true, Usage: usage}); err != nil {
					t.Fatal(err)
				}
			}
			finishBillingRequest(t, e, rail, request.ID, false)
			charge, original := int64(1), int64(1)
			if tier {
				charge, original = 5, 6
			}
			e.billingBalance(t, e.callerID, 10000-charge)
			e.billingBalance(t, e.donorID, 10002)
			var got, attemptPrices int64
			if err := e.store.DB().QueryRow(`SELECT cr.original_charge_milli,SUM(u.price_actual_milli) FROM charity_reservations cr
JOIN dispatch_claims c ON c.logical_request_id=cr.logical_request_id JOIN donation_usage_reservations u ON u.claim_id=c.id WHERE cr.logical_request_id=?`, request.ID).Scan(&got, &attemptPrices); err != nil {
				t.Fatal(err)
			}
			if got != original || attemptPrices != 2 {
				t.Fatal("request vs attempt rounding", got, attemptPrices)
			}
		})
	}
}

func TestOutputReservationCoversOnlyEnabledUserTokenRates(t *testing.T) {
	for _, tc := range []struct {
		name   string
		policy credits.ContextPricing
		want   int64
	}{
		{"base audio", credits.ContextPricing{UserModalities: &credits.ModalityPricing{OutputEnabled: true, Audio: &credits.TokenPrices{Output: 7000000}}}, 700},
		{"long fallback", credits.ContextPricing{LongContext: &credits.LongContextPrice{InputThreshold: 1, UserPrices: [4]int64{0, 0, 0, 11000000}}}, 1100},
		{"long video", credits.ContextPricing{LongContext: &credits.LongContextPrice{InputThreshold: 1, UserModalities: &credits.ModalityPricing{OutputEnabled: true, Video: &credits.TokenPrices{Output: 13000000}}}}, 1300},
		{"input and donor rates excluded", credits.ContextPricing{UserModalities: &credits.ModalityPricing{InputEnabled: true, Audio: &credits.TokenPrices{Output: 99000000}}, DonorModalities: &credits.ModalityPricing{OutputEnabled: true, Audio: &credits.TokenPrices{Output: 99000000}}}, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newCharityTestEnv(t)
			if _, err := e.store.DB().Exec(`UPDATE charity_models SET output_user_price=2000000,context_pricing=? WHERE id=?`, tc.policy.Encode(), e.tokenModel); err != nil {
				t.Fatal(err)
			}
			request := e.acceptWithOutputFloor(t, e.tokenModel, tc.want, 1, 100)
			claimed := e.claim(t, request, 1, false, 100)
			if claimed.reservation.ReservedPriceMilli != tc.want {
				t.Fatal(claimed.reservation)
			}
		})
	}
	t.Run("search units ignore output price", func(t *testing.T) {
		e := newCharityTestEnv(t)
		searchModel(t, e, 3, 2)
		policy := credits.ContextPricing{LongContext: &credits.LongContextPrice{InputThreshold: 1, UserPrices: [4]int64{0, 0, 0, 99000000}}}
		if _, err := e.store.DB().Exec(`UPDATE charity_models SET context_pricing=? WHERE id=?`, policy.Encode(), e.tokenModel); err != nil {
			t.Fatal(err)
		}
		request := e.acceptWithOutputFloor(t, e.tokenModel, 5, 1, 100)
		claimed := e.claim(t, request, 1, false, 100)
		if claimed.reservation.ReservedPriceMilli != 5 {
			t.Fatal(claimed.reservation)
		}
	})
}

func TestAttemptInputNormalizesDetailsWithoutInventingTotals(t *testing.T) {
	input := claim.CharityAttemptInput{RequestID: mustOpaqueID(t, "req_"), ClaimID: mustOpaqueID(t, "clm_"), CompletedAt: charityTestNow,
		UsageUnknown: true, Usage: contract.Usage{SearchUnitsPresent: true, SearchUnitsMicros: 500000}}
	if !validAttemptInput(normalizeAttemptInput(input)) {
		t.Fatal("independently known units rejected")
	}
	input.Usage.UncachedInputTokens = 1
	if validAttemptInput(normalizeAttemptInput(input)) {
		t.Fatal("nonzero unknown token totals accepted")
	}
	input.Usage, input.UsageUnknown = classifiedBillingUsage(), false
	input.Usage.Modalities.InputPresent, input.Usage.Modalities.InputReason = false, "untrusted detail"
	normalized := normalizeAttemptInput(input)
	if !validAttemptInput(normalized) || normalized.Usage.Modalities.InputReason != contract.ModalityInvalid {
		t.Fatal(normalized)
	}
	if _, err := usageinfo.Apply(normalized.Usage, usageinfo.Encode(normalized.Usage)); err != nil {
		t.Fatal(err)
	}
}
