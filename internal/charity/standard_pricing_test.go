package charity

import (
	"errors"
	"math"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/claim"
	"github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/credits"
	"github.com/waiting-here/NonbiriAPI/internal/usageinfo"
)

func searchModel(t *testing.T, e *charityTestEnv, price, reward int64) {
	t.Helper()
	if _, err := e.store.DB().Exec(`UPDATE charity_models SET pricing_mode='per_search_unit',request_user_price=?,request_donor_reward=? WHERE id=?`, price, reward, e.tokenModel); err != nil {
		t.Fatal(err)
	}
}

func finishBillingRequest(t *testing.T, e *charityTestEnv, rail *claim.Service, requestID string, failed bool) {
	t.Helper()
	caller := claim.CallerResult{Class: claim.ResultSuccess, Status: 200}
	if failed {
		caller = claim.CallerResult{Class: claim.ResultFailed, Status: 502, ErrorCode: "upstream"}
	}
	for range 2 {
		if _, err := rail.CompleteRequest(t.Context(), claim.CompleteRequestInput{RequestID: requestID, Caller: caller, Disposition: claim.AccountingCommit}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := rail.RecoverNonterminal(t.Context(), 100); err != nil {
		t.Fatal(err)
	}
}

func TestSearchUnitSettlementIndependentOfTokens(t *testing.T) {
	for _, tc := range []struct {
		name                          string
		usage                         contract.Usage
		failed                        bool
		price, charge, reward, tokens int64
	}{
		{"fractional failed response", contract.Usage{SearchUnitsMicros: 2500000, SearchUnitsPresent: true}, true, 8, 7, 5, 5},
		{"known zero", contract.Usage{SearchUnitsPresent: true}, false, 0, 0, 0, 5},
		{"tokens cannot substitute units", contract.Usage{Present: true, UncachedInputTokens: 2}, false, 5, 4, 0, 2},
		{"invalid units remain unknown", contract.Usage{Present: true, SearchUnitsPresent: true, SearchUnitsMicros: -1}, false, 5, 4, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newCharityTestEnv(t)
			rail := e.billingRail(t)
			searchModel(t, e, 3, 2)
			request := e.billingRequest(t, rail, e.tokenModel, 5, 1)
			// The scalar rates and mode belong to the accepted reservation.
			searchModel(t, e, 999, 999)
			handle := e.billingDispatch(t, rail, request.ID, 1)
			outcome := claim.AttemptOutcome{Kind: claim.ResultResponse, UpstreamStatus: 200, ResponseStarted: true, ProtocolSuccess: !tc.failed, Usage: tc.usage}
			for range 2 {
				if _, err := rail.CompleteAttempt(t.Context(), handle, outcome); err != nil {
					t.Fatal(err)
				}
			}
			finishBillingRequest(t, e, rail, request.ID, tc.failed)
			e.billingBalance(t, e.callerID, 10000-tc.charge)
			e.billingBalance(t, e.donorID, 10000+tc.reward)
			var price, reward, tokens, charge int64
			var success bool
			var donorDetails, callerDetails string
			if err := e.store.DB().QueryRow(`SELECT u.price_actual_milli,u.reward_actual_milli,u.tokens_actual,u.protocol_success,u.pricing_details,cr.user_charge_milli,l.usage_details
FROM donation_usage_reservations u JOIN dispatch_claims c ON c.id=u.claim_id
JOIN charity_reservations cr ON cr.logical_request_id=c.logical_request_id
JOIN request_logs l ON l.logical_request_id=c.logical_request_id WHERE c.id=?`, handle.ClaimID()).Scan(&price, &reward, &tokens, &success, &donorDetails, &charge, &callerDetails); err != nil {
				t.Fatal(err)
			}
			if price != tc.price || reward != tc.reward || charge != tc.charge || tokens != tc.tokens || success == tc.failed {
				t.Fatalf("price/reward/charge/tokens/success=%d/%d/%d/%d/%t", price, reward, charge, tokens, success)
			}
			caller, err := usageinfo.PricingFromUsage(callerDetails)
			donor, donorErr := usageinfo.DecodePricing(donorDetails)
			want := usageinfo.Pricing{Mode: "per_search_unit"}
			if err != nil || donorErr != nil || caller != want || donor != want {
				t.Fatal("search pricing receipts", caller, donor, err, donorErr)
			}
		})
	}
}

func TestSearchUnitRequestAggregationAndMissingReports(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		usages                []contract.Usage
		price, reward, charge int64
		known                 bool
		micros                int64
	}{
		{"one final ceiling", []contract.Usage{{SearchUnitsPresent: true, SearchUnitsMicros: 333333}, {SearchUnitsPresent: true, SearchUnitsMicros: 333333}}, 1, 2, 1, true, 666666},
		{"missing first", []contract.Usage{{}, {SearchUnitsPresent: true, SearchUnitsMicros: 1000000}}, 5, 1, 4, false, 0},
		{"missing second", []contract.Usage{{SearchUnitsPresent: true, SearchUnitsMicros: 1000000}, {}}, 5, 1, 4, false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newCharityTestEnv(t)
			rail := e.billingRail(t)
			searchModel(t, e, 1, 1)
			request := e.billingRequest(t, rail, e.tokenModel, 5, 2)
			for i, usage := range tc.usages {
				handle := e.billingDispatch(t, rail, request.ID, i+1)
				if _, err := rail.CompleteAttempt(t.Context(), handle, claim.AttemptOutcome{Kind: claim.ResultResponse, UpstreamStatus: 200, ResponseStarted: true, ProtocolSuccess: true, Usage: usage}); err != nil {
					t.Fatal(err)
				}
			}
			finishBillingRequest(t, e, rail, request.ID, false)
			e.billingBalance(t, e.callerID, 10000-tc.charge)
			e.billingBalance(t, e.donorID, 10000+tc.reward)
			var original int64
			var details string
			if err := e.store.DB().QueryRow(`SELECT original_charge_milli,usage_details FROM charity_reservations WHERE logical_request_id=?`, request.ID).Scan(&original, &details); err != nil {
				t.Fatal(err)
			}
			usage, err := usageinfo.Apply(contract.Usage{}, details)
			if err != nil || original != tc.price || usage.SearchUnitsPresent != tc.known || usage.SearchUnitsMicros != tc.micros {
				t.Fatal("aggregate search receipt", original, usage, err)
			}
		})
	}
}

func classifiedBillingUsage() contract.Usage {
	return contract.Usage{Present: true, UncachedInputTokens: 3, CacheWriteInputTokens: 2, CacheReadInputTokens: 1, OutputTokens: 4,
		Modalities: contract.ModalityUsage{InputPresent: true, OutputPresent: true,
			Input: [3][4]int64{{1, 2, 0, 0}, {0, 2, 0, 0}, {1, 0, 0, 0}}, Output: [4]int64{1, 0, 3, 0}}}
}

func classifiedBillingPolicy() credits.ContextPricing {
	return credits.ContextPricing{
		UserModalities: &credits.ModalityPricing{InputEnabled: true, OutputEnabled: true,
			Text:  &credits.TokenPrices{UncachedInput: 1000000, CacheReadInput: 1000000, Output: 2000000},
			Image: &credits.TokenPrices{UncachedInput: 2000000, CacheWriteInput: 3000000}, Audio: &credits.TokenPrices{Output: 7000000}},
		DonorModalities: &credits.ModalityPricing{InputEnabled: true, OutputEnabled: true,
			Text:  &credits.TokenPrices{UncachedInput: 500000, CacheReadInput: 500000, Output: 1000000},
			Image: &credits.TokenPrices{UncachedInput: 1000000, CacheWriteInput: 1000000}, Audio: &credits.TokenPrices{Output: 2000000}},
	}
}

func setClassifiedBillingPolicy(t *testing.T, e *charityTestEnv, policy credits.ContextPricing) {
	t.Helper()
	if _, err := e.store.DB().Exec(`UPDATE charity_models SET context_pricing=?,
uncached_user_price=4000000,cache_write_user_price=6000000,cache_read_user_price=2000000,output_user_price=5000000,
uncached_donor_reward=2000000,cache_write_donor_reward=1000000,cache_read_donor_reward=1000000,output_donor_reward=1000000 WHERE id=?`, policy.Encode(), e.tokenModel); err != nil {
		t.Fatal(err)
	}
}

func TestClassifiedSettlementUsesIndependentSidesAndRewards(t *testing.T) {
	modality := usageinfo.Pricing{Mode: "per_token", InputMode: "modality", OutputMode: "modality"}
	inputFallback := modality
	inputFallback.InputMode, inputFallback.InputFallback = "aggregate", contract.ModalityCacheUnknown
	outputFallback := modality
	outputFallback.OutputMode, outputFallback.OutputFallback = "aggregate", contract.ModalityReasoningUnknown
	missingPrice := modality
	missingPrice.InputMode, missingPrice.InputFallback = "aggregate", "missing_price"
	missingReward := modality
	missingReward.OutputMode, missingReward.OutputFallback = "aggregate", "missing_price"
	for _, tc := range []struct {
		name                     string
		change                   func(*contract.Usage, *credits.ContextPricing)
		original, charge, reward int64
		caller, donor            usageinfo.Pricing
	}{
		{"classified", nil, 35, 28, 12, modality, modality},
		{"input cache split missing", func(u *contract.Usage, _ *credits.ContextPricing) {
			u.Modalities.InputPresent = false
			u.Modalities.InputReason = contract.ModalityCacheUnknown
		}, 49, 40, 16, inputFallback, inputFallback},
		{"output reasoning split missing", func(u *contract.Usage, _ *credits.ContextPricing) {
			u.Modalities.OutputPresent = false
			u.Modalities.OutputReason = contract.ModalityReasoningUnknown
		}, 32, 26, 9, outputFallback, outputFallback},
		{"user rate missing", func(_ *contract.Usage, p *credits.ContextPricing) { p.UserModalities.Image = nil }, 49, 40, 12, missingPrice, modality},
		{"reward rate missing", func(_ *contract.Usage, p *credits.ContextPricing) { p.DonorModalities.Audio = nil }, 35, 28, 9, modality, missingReward},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newCharityTestEnv(t)
			rail := e.billingRail(t)
			usage, policy := classifiedBillingUsage(), classifiedBillingPolicy()
			if tc.change != nil {
				tc.change(&usage, &policy)
			}
			setClassifiedBillingPolicy(t, e, policy)
			request := e.billingRequest(t, rail, e.tokenModel, 5, 1)
			setClassifiedBillingPolicy(t, e, credits.ContextPricing{})
			handle := e.billingDispatch(t, rail, request.ID, 1)
			if _, err := rail.CompleteAttempt(t.Context(), handle, claim.AttemptOutcome{Kind: claim.ResultResponse, UpstreamStatus: 200, ProtocolSuccess: true, ResponseStarted: true, Usage: usage}); err != nil {
				t.Fatal(err)
			}
			finishBillingRequest(t, e, rail, request.ID, false)
			e.billingBalance(t, e.callerID, 10000-tc.charge)
			e.billingBalance(t, e.donorID, 10000+tc.reward)
			var original int64
			var callerDetails, donorDetails string
			if err := e.store.DB().QueryRow(`SELECT cr.original_charge_milli,l.usage_details,u.pricing_details FROM charity_reservations cr
JOIN request_logs l ON l.logical_request_id=cr.logical_request_id JOIN dispatch_claims c ON c.logical_request_id=cr.logical_request_id
JOIN donation_usage_reservations u ON u.claim_id=c.id WHERE cr.logical_request_id=?`, request.ID).Scan(&original, &callerDetails, &donorDetails); err != nil {
				t.Fatal(err)
			}
			caller, err := usageinfo.PricingFromUsage(callerDetails)
			donor, donorErr := usageinfo.DecodePricing(donorDetails)
			if original != tc.original || err != nil || donorErr != nil || caller != tc.caller || donor != tc.donor {
				t.Fatal("separate pricing decisions", original, caller, donor, err, donorErr)
			}
		})
	}
}

func TestClassifiedAggregationDoesNotInferMissingZero(t *testing.T) {
	for _, preResponse := range []bool{false, true} {
		t.Run(map[bool]string{false: "reported zero lacks classification", true: "pre-response report excluded"}[preResponse], func(t *testing.T) {
			e := newCharityTestEnv(t)
			rail := e.billingRail(t)
			setClassifiedBillingPolicy(t, e, classifiedBillingPolicy())
			request := e.billingRequest(t, rail, e.tokenModel, 5, 2)
			first := e.billingDispatch(t, rail, request.ID, 1)
			usage := contract.Usage{Present: true}
			status := 200
			if preResponse {
				usage.UncachedInputTokens, status = 100, 503
			}
			if _, err := rail.CompleteAttempt(t.Context(), first, claim.AttemptOutcome{Kind: claim.ResultResponse, UpstreamStatus: status, ResponseStarted: !preResponse, Usage: usage}); err != nil {
				t.Fatal(err)
			}
			second := e.billingDispatch(t, rail, request.ID, 2)
			if _, err := rail.CompleteAttempt(t.Context(), second, claim.AttemptOutcome{Kind: claim.ResultResponse, UpstreamStatus: 200, ProtocolSuccess: true, ResponseStarted: true, Usage: classifiedBillingUsage()}); err != nil {
				t.Fatal(err)
			}
			finishBillingRequest(t, e, rail, request.ID, false)
			wantCharge := int64(37) // ceil(46 * 80%).
			wantMode := "aggregate"
			wantLogTokens := int64(3)
			if preResponse {
				wantCharge, wantMode, wantLogTokens = 28, "modality", 103
			}
			e.billingBalance(t, e.callerID, 10000-wantCharge)
			var logTokens, billableTokens int64
			var logDetails, billableDetails string
			if err := e.store.DB().QueryRow(`SELECT l.uncached_input_tokens,l.usage_details,cr.usage_uncached_input_tokens,cr.usage_details
FROM request_logs l JOIN charity_reservations cr ON cr.logical_request_id=l.logical_request_id WHERE l.logical_request_id=?`, request.ID).Scan(&logTokens, &logDetails, &billableTokens, &billableDetails); err != nil {
				t.Fatal(err)
			}
			logPricing, err := usageinfo.PricingFromUsage(logDetails)
			billPricing, billErr := usageinfo.PricingFromUsage(billableDetails)
			if err != nil || billErr != nil || logPricing != billPricing || billPricing.InputMode != wantMode || logTokens != wantLogTokens || billableTokens != 3 {
				t.Fatal("log and billing aggregates", logTokens, billableTokens, logPricing, billPricing, err, billErr)
			}
		})
	}
}

func TestUsageAggregationOverflowRemainsConservative(t *testing.T) {
	e := newCharityTestEnv(t)
	rail := e.billingRail(t)
	searchModel(t, e, 0, 0)
	request := e.billingRequest(t, rail, e.tokenModel, 5, 2)
	for i, count := range []int64{math.MaxInt64, 1} {
		handle := e.billingDispatch(t, rail, request.ID, i+1)
		if _, err := rail.CompleteAttempt(t.Context(), handle, claim.AttemptOutcome{Kind: claim.ResultResponse, UpstreamStatus: 200, ProtocolSuccess: true, ResponseStarted: true, Usage: contract.Usage{SearchUnitsPresent: true, SearchUnitsMicros: count}}); err != nil {
			t.Fatal(err)
		}
	}
	finishBillingRequest(t, e, rail, request.ID, false)
	e.billingBalance(t, e.callerID, 9996)
	e.billingBalance(t, e.donorID, 10000)
}

func TestTokenAggregationOverflowPreservesSaturatedCount(t *testing.T) {
	e := newCharityTestEnv(t)
	rail := e.billingRail(t)
	if _, err := e.store.DB().Exec(`UPDATE charity_models SET uncached_user_price=0,uncached_donor_reward=0 WHERE id=?`, e.tokenModel); err != nil {
		t.Fatal(err)
	}
	request := e.billingRequest(t, rail, e.tokenModel, 5, 2)
	for i, count := range []int64{math.MaxInt64, 1} {
		handle := e.billingDispatch(t, rail, request.ID, i+1)
		usage := contract.Usage{Present: true, UncachedInputTokens: count, Modalities: contract.ModalityUsage{InputPresent: true, OutputPresent: true, Input: [3][4]int64{{count}}}}
		if _, err := rail.CompleteAttempt(t.Context(), handle, claim.AttemptOutcome{Kind: claim.ResultResponse, UpstreamStatus: 200, ProtocolSuccess: true, ResponseStarted: true, Usage: usage}); err != nil {
			t.Fatal(err)
		}
	}
	finishBillingRequest(t, e, rail, request.ID, false)
	e.billingBalance(t, e.callerID, 9996)
	var tokens int64
	var unknown bool
	var details string
	if err := e.store.DB().QueryRow(`SELECT usage_uncached_input_tokens,usage_unknown,usage_details FROM charity_reservations WHERE logical_request_id=?`, request.ID).Scan(&tokens, &unknown, &details); err != nil {
		t.Fatal(err)
	}
	usage, err := usageinfo.Apply(contract.Usage{Present: !unknown, UncachedInputTokens: tokens}, details)
	if err != nil || tokens != math.MaxInt64 || !unknown || usage.Modalities.InputPresent {
		t.Fatal("saturated unknown aggregate", tokens, unknown, usage, err)
	}
}

func TestPricingReplayUsesCanonicalDecision(t *testing.T) {
	for _, classified := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy empty", true: "classified"}[classified], func(t *testing.T) {
			e := newCharityTestEnv(t)
			if classified {
				setClassifiedBillingPolicy(t, e, classifiedBillingPolicy())
			}
			request := e.accept(t, e.tokenModel, 5, 1)
			claimed := e.claim(t, request, 1, true)
			input := claim.CharityAttemptInput{RequestID: request, ClaimID: claimed.id, DonationKeyID: &e.donationKey, ReceiverUserID: &e.donorID, ResponseStarted: true, ProtocolSuccess: true, Usage: classifiedBillingUsage(), CompletedAt: charityTestNow + 10}
			tx := beginTestTx(t, e.store.DB())
			defer tx.Rollback()
			actual, err := e.service.PrepareAttempt(t.Context(), tx, input)
			if err != nil {
				t.Fatal(err)
			}
			completion := claim.CharityAttemptCompletion{Attempt: input, Actual: actual, RewardState: claim.RewardPosted}
			if err := e.service.CompleteAttempt(t.Context(), tx, completion); err != nil {
				t.Fatal(err)
			}
			completion.Actual.PricingDetails = " { } "
			if classified {
				completion.Actual.PricingDetails = `{"output_mode":"modality", "input_mode":"modality", "mode":"per_token"}`
			}
			if err := e.service.CompleteAttempt(t.Context(), tx, completion); err != nil {
				t.Fatal("canonical replay", err)
			}
			completion.Actual.PricingDetails = `{"mode":"per_request"}`
			if err := e.service.CompleteAttempt(t.Context(), tx, completion); !errors.Is(err, claim.ErrConflict) {
				t.Fatal("changed pricing decision accepted", err)
			}
		})
	}
}
