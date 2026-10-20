package charity

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/charitydiscount"
	"github.com/waiting-here/NonbiriAPI/internal/claim"
	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

func setRecurringDiscount(t *testing.T, e *charityTestEnv, model int64, percent int) {
	t.Helper()
	config := charitydiscount.Default()
	config.Enabled, config.DefaultPercent = true, percent
	encoded, err := charitydiscount.Encode(config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.DB().Exec("UPDATE charity_models SET discount_enabled=0,recurring_discount=? WHERE id=?", encoded, model); err != nil {
		t.Fatal(err)
	}
}

func TestRecurringDiscountRetryAndRecoveryUseAcceptedSnapshot(t *testing.T) {
	for _, token := range []bool{false, true} {
		for _, recoverRequest := range []bool{false, true} {
			t.Run(fmt.Sprintf("token=%t/recovery=%t", token, recoverRequest), func(t *testing.T) {
				e := newCharityTestEnv(t)
				rail := e.billingRail(t)
				model, reserve := e.requestModel, int64(1500)
				if token {
					model, reserve = e.tokenModel, 5
				}
				setRecurringDiscount(t, e, model, 50)
				request := e.billingRequest(t, rail, model, reserve, 2)
				first := e.billingDispatch(t, rail, request.ID, 1)
				if _, err := rail.CompleteAttempt(t.Context(), first, claim.AttemptOutcome{Kind: claim.ResultSynthetic, UpstreamStatus: 502}); err != nil {
					t.Fatal(err)
				}
				// A later attempt and startup recovery must use the accepted price.
				setRecurringDiscount(t, e, model, 100)
				second := e.billingDispatch(t, rail, request.ID, 2)
				if err := rail.MarkResponseStarted(t.Context(), second); err != nil {
					t.Fatal(err)
				}
				if recoverRequest {
					for range 2 {
						if _, err := rail.RecoverNonterminal(t.Context(), 100); err != nil {
							t.Fatal(err)
						}
					}
				} else {
					if _, err := rail.CompleteAttempt(t.Context(), second, claim.AttemptOutcome{Kind: claim.ResultResponse, UpstreamStatus: 200, ProtocolSuccess: true, ResponseStarted: true, Usage: connectorcontract.Usage{Present: true, UncachedInputTokens: 1}}); err != nil {
						t.Fatal(err)
					}
					for range 2 {
						if _, err := rail.CompleteRequest(t.Context(), claim.CompleteRequestInput{RequestID: request.ID, Caller: claim.CallerResult{Class: claim.ResultSuccess, Status: 200}, Disposition: claim.AccountingCommit}); err != nil {
							t.Fatal(err)
						}
					}
				}
				charge, reward := int64(1500), int64(1250)
				if token {
					charge, reward = 2, 2
					if recoverRequest {
						charge = 3
					}
				}
				if recoverRequest {
					reward = 0
				}
				e.billingBalance(t, e.callerID, 10000-charge)
				e.billingBalance(t, e.donorID, 10000+reward)
				var percent int
				var actual, frozenReserve int64
				if err := e.store.DB().QueryRow("SELECT discount_percent,user_charge_milli,user_reserved_milli FROM charity_reservations WHERE logical_request_id=?", request.ID).Scan(&percent, &actual, &frozenReserve); err != nil || percent != 50 || actual != charge || frozenReserve != reserve {
					t.Fatal(percent, actual, frozenReserve, err)
				}
			})
		}
	}
}

func TestRecurringDiscountAcceptanceUsesDecisionInstantAndRevalidatesReserve(t *testing.T) {
	e := newCharityTestEnv(t)
	rail := e.billingRail(t)
	// The supplied admission clock has crossed a minute; the rail's wall clock
	// is still in the preceding full-price minute.
	at := (charityTestNow+30)/60*60 + 60
	local := time.Unix(at, 0).UTC()
	config := charitydiscount.Default()
	config.Enabled = true
	config.Periods = []charitydiscount.Period{{Weekdays: []int{(int(local.Weekday())+6)%7 + 1}, StartTime: local.Format("15:04"), EndTime: local.Add(time.Minute).Format("15:04"), Percent: 50}}
	encoded, err := charitydiscount.Encode(config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.DB().Exec("UPDATE charity_models SET discount_enabled=0,recurring_discount=? WHERE id=?", encoded, e.requestModel); err != nil {
		t.Fatal(err)
	}
	input := claim.AcceptInput{UserID: e.callerID, Route: claim.RouteCharityChat, ModelSnapshot: "[公益]provider/model", AttemptLimit: 1, ReservedMilli: 1500, CharityModelID: e.requestModel, CharityDecisionNow: &at}
	request, err := rail.Accept(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	var percent int
	if err := e.store.DB().QueryRow("SELECT discount_percent FROM charity_reservations WHERE logical_request_id=?", request.ID).Scan(&percent); err != nil || percent != 50 {
		t.Fatal(percent, err)
	}
	// If configuration changes after preflight, the transaction rejects a
	// stale amount instead of silently accepting a different reservation.
	setRecurringDiscount(t, e, e.requestModel, 100)
	if _, err := rail.Accept(t.Context(), input); !errors.Is(err, claim.ErrConflict) {
		t.Fatal("stale reserve accepted", err)
	}
}

func TestRecurringDiscountFreeTokensKeepReserveAndOutputFloor(t *testing.T) {
	e := newCharityTestEnv(t)
	setRecurringDiscount(t, e, e.tokenModel, 0)
	if _, err := e.store.DB().Exec("UPDATE charity_models SET output_user_price=2000000 WHERE id=?", e.tokenModel); err != nil {
		t.Fatal(err)
	}
	request := e.acceptWithOutputFloor(t, e.tokenModel, 200, 1, 100)
	var tokenReserve, userReserve, percent int64
	if err := e.store.DB().QueryRow("SELECT token_reserve_milli,user_reserved_milli,discount_percent FROM charity_reservations WHERE logical_request_id=?", request).Scan(&tokenReserve, &userReserve, &percent); err != nil || tokenReserve != 200 || userReserve != 200 || percent != 0 {
		t.Fatal(tokenReserve, userReserve, percent, err)
	}
	candidate := e.claim(t, request, 1, false, 100)
	if candidate.reservation.ReservedPriceMilli != 200 || candidate.reservation.ReservedTokens != 100 {
		t.Fatal(candidate.reservation)
	}
}
