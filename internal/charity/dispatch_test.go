package charity

import (
	"context"
	"errors"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/claim"
	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

func TestDispatchRevalidatesModelAndCurrentCaller(t *testing.T) {
	for _, route := range []claim.RouteKind{claim.RouteCharityChat, claim.RouteCharityEmbeddings} {
		t.Run(string(route), func(t *testing.T) { testDispatchRevalidatesModelAndCaller(t, route) })
	}
}

func testDispatchRevalidatesModelAndCaller(t *testing.T, route claim.RouteKind) {
	for _, test := range []struct {
		name, query string
		args        func(*charityTestEnv) []any
		want        error
	}{
		{"empty set", `UPDATE charity_model_access SET allowed_level_mask=0 WHERE model_id=?`, func(e *charityTestEnv) []any { return []any{e.requestModel} }, claim.ErrForbidden},
		{"model disabled", `UPDATE charity_models SET enabled=0 WHERE id=?`, func(e *charityTestEnv) []any { return []any{e.requestModel} }, claim.ErrModelUnavailable},
		{"site disabled", `UPDATE site_config SET value='0' WHERE key='charity_enabled'`, func(*charityTestEnv) []any { return nil }, claim.ErrModelUnavailable},
		{"changed effective level", `UPDATE users SET level=2 WHERE id=?`, func(e *charityTestEnv) []any { return []any{e.callerID} }, claim.ErrForbidden},
		{"steward has no exemption", `UPDATE users SET level=5 WHERE id=?`, func(e *charityTestEnv) []any { return []any{e.callerID} }, claim.ErrForbidden},
	} {
		t.Run(test.name, func(t *testing.T) {
			e := newCharityTestEnv(t)
			if _, err := e.store.DB().Exec(`UPDATE users SET level=1 WHERE id=?`, e.callerID); err != nil {
				t.Fatal(err)
			}
			if _, err := e.store.DB().Exec(`UPDATE charity_model_access SET allowed_level_mask=1 WHERE model_id=?`, e.requestModel); err != nil {
				t.Fatal(err)
			}
			requestID := e.accept(t, e.requestModel, 2400, 2, route)
			claimed := e.claim(t, requestID, 1, false)
			if _, err := e.store.DB().Exec(test.query, test.args(e)...); err != nil {
				t.Fatal(err)
			}
			tx := beginTestTx(t, e.store.DB())
			err := e.service.PrepareDispatch(context.Background(), tx, claim.CharityDispatch{RequestID: requestID, ClaimID: claimed.id, ActorUserID: e.callerID, DispatchedAt: charityTestNow + 2})
			if !errors.Is(err, test.want) {
				t.Fatalf("dispatch error = %v want %v", err, test.want)
			}
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestAlreadySentCharityCompletesButRetryUsesNewAccess(t *testing.T) {
	for _, route := range []claim.RouteKind{claim.RouteCharityChat, claim.RouteCharityEmbeddings} {
		t.Run(string(route), func(t *testing.T) { testAlreadySentCharityCompletes(t, route) })
	}
}

func testAlreadySentCharityCompletes(t *testing.T, route claim.RouteKind) {
	e := newCharityTestEnv(t)
	requestID := e.accept(t, e.requestModel, 2400, 2, route)
	claimed := e.claim(t, requestID, 1, true)
	if _, err := e.store.DB().Exec(`UPDATE charity_model_access SET allowed_level_mask=0 WHERE model_id=?`, e.requestModel); err != nil {
		t.Fatal(err)
	}
	e.completeAttempt(t, requestID, claimed, claim.CharityAttemptInput{
		ReceiverUserID: &e.donorID, Usage: connectorcontract.Usage{Present: true}, ProtocolSuccess: true, ResponseStarted: true, CompletedAt: charityTestNow + 10,
	}, claim.RewardPosted, claim.CharityActual{PriceMilli: 3000, RewardMilli: 1250})
	tx := beginTestTx(t, e.store.DB())
	_, err := e.service.Claim(context.Background(), tx, claim.CharityClaimInput{
		RequestID: requestID, ClaimID: mustOpaqueID(t, "clm_"), ActorUserID: e.callerID, AttemptSeq: 2,
		DonationKeyID: e.donationKey, EndpointID: e.endpointID, EndpointKeyID: e.endpointKey, UpstreamModelID: "upstream-model", ClaimedAt: charityTestNow + 11,
	})
	if !errors.Is(err, claim.ErrForbidden) {
		t.Fatalf("retry error %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	charge, err := e.service.CalculateRequestCharge(context.Background(), requestID, claim.AccountingCommit)
	if err != nil || charge != 2400 {
		t.Fatalf("original sent charge %d error %v", charge, err)
	}
}
