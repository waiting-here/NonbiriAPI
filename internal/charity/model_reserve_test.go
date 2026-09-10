package charity

import (
	"context"
	"errors"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/claim"
)

func TestModelTokenReserveRemainsFrozenThroughChangesAndSettlement(t *testing.T) {
	env := newCharityTestEnv(t)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := env.store.DB().Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO charity_model_token_reserves(model_id,amount_milli) VALUES(?,7),(?,99)`, env.tokenModel, env.requestModel)
	request := env.accept(t, env.tokenModel, 7, 1)
	// Per-request pricing ignores the stored Token-only override.
	env.accept(t, env.requestModel, 2400, 1)
	exec(`UPDATE charity_model_token_reserves SET amount_milli=11 WHERE model_id=?`, env.tokenModel)
	exec(`UPDATE site_config SET value='17' WHERE key='charity_token_reserve_milli'`)
	claimed := env.claim(t, request, 1, true)
	if claimed.reservation.ReservedPriceMilli != 7 || claimed.reservation.ReservedTokens != 5 {
		t.Fatalf("frozen reservation %+v", claimed.reservation)
	}
	env.completeAttempt(t, request, claimed, claim.CharityAttemptInput{ReceiverUserID: &env.donorID, UsageUnknown: true, SuppressReward: true, ResponseStarted: true, CompletedAt: charityTestNow + 10}, claim.RewardZero, claim.CharityActual{PriceMilli: 7})
	charge, err := env.service.CalculateRequestCharge(context.Background(), request, claim.AccountingCommit)
	if err != nil || charge != 6 {
		t.Fatalf("frozen unknown-usage charge %d %v", charge, err)
	}
	// A stale preflight amount is rejected before a reservation can be inserted.
	tx := beginTestTx(t, env.store.DB())
	err = env.service.AcceptRequest(context.Background(), tx, claim.CharityAcceptance{RequestID: mustOpaqueID(t, "req_"), UserID: env.callerID, CharityModelID: env.tokenModel, ModelSnapshot: "model", ReservedMilli: 7, AttemptLimit: 1, AcceptedAt: charityTestNow})
	_ = tx.Rollback()
	if !errors.Is(err, claim.ErrConflict) {
		t.Fatalf("stale amount accepted: %v", err)
	}
	failed := env.accept(t, env.tokenModel, 11, 1)
	failedClaim := env.claim(t, failed, 1, true)
	env.completeAttempt(t, failed, failedClaim, claim.CharityAttemptInput{ReceiverUserID: &env.donorID, UsageUnknown: true, SuppressReward: true, CompletedAt: charityTestNow + 20}, claim.RewardZero, claim.CharityActual{})
	charge, err = env.service.CalculateRequestCharge(context.Background(), failed, claim.AccountingCommit)
	if err != nil || charge != 0 {
		t.Fatalf("failure before a response charged %d %v", charge, err)
	}
	exec(`DELETE FROM charity_model_token_reserves WHERE model_id=?`, env.tokenModel)
	inherited := env.accept(t, env.tokenModel, 17, 1)
	var amount int64
	if err := env.store.DB().QueryRow(`SELECT token_reserve_milli FROM charity_reservations WHERE logical_request_id=?`, inherited).Scan(&amount); err != nil || amount != 17 {
		t.Fatalf("global inheritance %d %v", amount, err)
	}
}
