package charity

import (
	"context"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/claim"
	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/credits"
)

func TestContextPricingSettlement(t *testing.T) {
	testContextPricingSettlement(t, false)
}

func TestRecurringDiscountContextPricingSettlement(t *testing.T) {
	testContextPricingSettlement(t, true)
}

func testContextPricingSettlement(t *testing.T, recurring bool) {
	known := connectorcontract.Usage{Present: true, UncachedInputTokens: 5, CacheWriteInputTokens: 2, CacheReadInputTokens: 3, OutputTokens: 4}
	below := known
	below.CacheReadInputTokens = 2
	for _, tc := range []struct {
		name                            string
		request, long, penalty          bool
		discount                        int
		usage                           connectorcontract.Usage
		price, original, charge, reward int64
	}{
		{"both at threshold", false, true, true, 80, known, 38, 738, 731, 10},
		{"below threshold", false, true, true, 80, below, 20, 20, 16, 10},
		{"long only", false, true, false, 80, known, 38, 38, 31, 10},
		{"penalty only", false, false, true, 80, known, 20, 720, 716, 10},
		{"free usage still incurs fine", false, true, true, 0, known, 38, 738, 700, 10},
		{"unknown tokens", false, true, true, 80, connectorcontract.Usage{}, 5, 5, 4, 0},
		{"request with usage", true, true, true, 80, known, 7000, 7700, 6300, 1250},
		{"request below threshold", true, true, true, 80, below, 3000, 3000, 2400, 1250},
		{"request without usage", true, true, true, 80, connectorcontract.Usage{}, 3000, 3000, 2400, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newCharityTestEnv(t)
			rail := e.billingRail(t)
			model, reserve := e.tokenModel, int64(5)
			if tc.request {
				model, reserve = e.requestModel, 2400
			}
			policy := credits.ContextPricing{}
			if tc.long {
				policy.LongContext = &credits.LongContextPrice{InputThreshold: 10, RequestPrice: 7000, UserPrices: [4]int64{1000000, 2000000, 3000000, 5000000}}
			}
			if tc.penalty {
				policy.Penalty = &credits.ContextPenalty{InputThreshold: 10, Fine: 700}
			}
			if _, err := e.store.DB().Exec("UPDATE charity_models SET context_pricing=?,discount_percent=? WHERE id=?", policy.Encode(), tc.discount, model); err != nil {
				t.Fatal(err)
			}
			if recurring {
				setRecurringDiscount(t, e, model, tc.discount)
			}
			request := e.billingRequest(t, rail, model, reserve, 1)
			if recurring {
				setRecurringDiscount(t, e, model, 100)
			}
			// Editing live prices after acceptance must not rewrite the receipt.
			if _, err := e.store.DB().Exec("UPDATE charity_models SET context_pricing='{}',discount_percent=100 WHERE id=?", model); err != nil {
				t.Fatal(err)
			}
			handle := e.billingDispatch(t, rail, request.ID, 1)
			ctx := context.Background()
			if err := rail.MarkResponseStarted(ctx, handle); err != nil {
				t.Fatal(err)
			}
			if _, err := rail.CompleteAttempt(ctx, handle, claim.AttemptOutcome{Kind: claim.ResultResponse, UpstreamStatus: 200, ProtocolSuccess: true, ResponseStarted: true, Usage: tc.usage}); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				if _, err := rail.CompleteRequest(ctx, claim.CompleteRequestInput{RequestID: request.ID, Caller: claim.CallerResult{Class: claim.ResultSuccess, Status: 200}, Disposition: claim.AccountingCommit}); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := rail.RecoverNonterminal(ctx, 100); err != nil {
				t.Fatal(err)
			}
			var actions int
			if err := e.store.DB().QueryRow("SELECT automatic_actions FROM request_logs WHERE logical_request_id=?", request.ID).Scan(&actions); err != nil {
				t.Fatal(err)
			}
			wantAction := 0
			if tc.penalty && tc.usage.Present && tc.usage.UncachedInputTokens+tc.usage.CacheWriteInputTokens+tc.usage.CacheReadInputTokens >= 10 {
				wantAction = 32
			}
			if actions != wantAction {
				t.Fatal("context fine action", actions, wantAction)
			}
			e.billingBalance(t, e.callerID, 10000-tc.charge)
			e.billingBalance(t, e.donorID, 10000+tc.reward)
			var price, original, charge, reward int64
			if err := e.store.DB().QueryRow(`SELECT u.price_actual_milli,cr.original_charge_milli,cr.user_charge_milli,c.donor_reward_actual_milli
FROM charity_reservations cr JOIN dispatch_claims c ON c.logical_request_id=cr.logical_request_id
JOIN donation_usage_reservations u ON u.claim_id=c.id WHERE cr.logical_request_id=?`, request.ID).Scan(&price, &original, &charge, &reward); err != nil {
				t.Fatal(err)
			}
			if price != tc.price || original != tc.original || charge != tc.charge || reward != tc.reward {
				t.Fatalf("price/original/charge/reward=%d/%d/%d/%d; want %d/%d/%d/%d", price, original, charge, reward, tc.price, tc.original, tc.charge, tc.reward)
			}
			var used []byte
			if err := e.store.DB().QueryRow("SELECT price_used_mag FROM donation_keys WHERE id=?", e.donationKey).Scan(&used); err != nil {
				t.Fatal(err)
			}
			assertU128(t, used, tc.price, "key price excludes penalty")
		})
	}
}

func TestContextPenaltyIsOncePerLogicalRequest(t *testing.T) {
	e := newCharityTestEnv(t)
	rail := e.billingRail(t)
	p := credits.ContextPricing{Penalty: &credits.ContextPenalty{InputThreshold: 10, Fine: 700}}
	if _, err := e.store.DB().Exec("UPDATE charity_models SET context_pricing=? WHERE id=?", p.Encode(), e.tokenModel); err != nil {
		t.Fatal(err)
	}
	request := e.billingRequest(t, rail, e.tokenModel, 5, 2)
	for seq := 1; seq <= 2; seq++ {
		h := e.billingDispatch(t, rail, request.ID, seq)
		if err := rail.MarkResponseStarted(t.Context(), h); err != nil {
			t.Fatal(err)
		}
		if _, err := rail.CompleteAttempt(t.Context(), h, claim.AttemptOutcome{Kind: claim.ResultResponse, UpstreamStatus: 200, ProtocolSuccess: true, ResponseStarted: true, Usage: connectorcontract.Usage{Present: true, CacheReadInputTokens: 10}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := rail.CompleteRequest(t.Context(), claim.CompleteRequestInput{RequestID: request.ID, Caller: claim.CallerResult{Class: claim.ResultSuccess, Status: 200}, Disposition: claim.AccountingCommit}); err != nil {
		t.Fatal(err)
	}
	e.billingBalance(t, e.callerID, 9300)
}
