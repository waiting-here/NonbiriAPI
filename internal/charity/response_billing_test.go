package charity

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/claim"
	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

type billingAcceptance struct{}

func (billingAcceptance) AuthorizeChatAcceptance(context.Context, *sql.Tx, int64, int64) error {
	return nil
}

func (e *charityTestEnv) billingRail(t *testing.T) *claim.Service {
	t.Helper()
	ctx := context.Background()
	vault, err := secret.New(bytes.Repeat([]byte{0x43}, secret.MasterKeyBytes))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = vault.Close() })
	tx := beginTestTx(t, e.store.DB())
	for _, userID := range []int64{e.callerID, e.donorID} {
		account, err := ledger.CreateUserAccount(ctx, tx, userID, charityTestNow)
		if err != nil {
			t.Fatal(err)
		}
		external, err := ledger.CodedAccount(ctx, tx, "external")
		if err != nil {
			t.Fatal(err)
		}
		plan, err := ledger.NewAdminUserAdjustment(ledger.Meta{OperationID: mustOpaqueID(t, "op_"), ActorUserID: userID, CreatedAt: charityTestNow}, account.ID, external.ID, ledger.AmountFromMilli(10000), 0, ledger.Amount{}, "test funding")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ledger.Apply(ctx, tx, plan); err != nil {
			t.Fatal(err)
		}
	}
	commitTestTx(t, tx)
	rail, err := claim.New(claim.Dependencies{DB: e.store.DB(), Secrets: vault, Charity: e.service, Accounting: claim.NewLedgerAccounting(), Acceptance: billingAcceptance{}, Now: func() time.Time { return time.Unix(charityTestNow+30, 0) }})
	if err != nil {
		t.Fatal(err)
	}
	return rail
}

func (e *charityTestEnv) billingRequest(t *testing.T, rail *claim.Service, model, reserve int64, attempts int) claim.Request {
	t.Helper()
	now := charityTestNow + 30
	request, err := rail.Accept(context.Background(), claim.AcceptInput{UserID: e.callerID, Route: claim.RouteCharityChat, ModelSnapshot: "[公益]provider/model", AttemptLimit: attempts, ReservedMilli: reserve, CharityModelID: model, CharityDecisionNow: &now})
	if err != nil {
		t.Fatal(err)
	}
	return request
}

func (e *charityTestEnv) billingDispatch(t *testing.T, rail *claim.Service, requestID string, seq int) claim.Handle {
	t.Helper()
	handle, err := rail.Claim(context.Background(), claim.ClaimInput{RequestID: requestID, ActorUserID: e.callerID, AttemptSeq: seq, Purpose: claim.PurposeCharity, DonationKeyID: e.donationKey, Candidate: claim.Candidate{EndpointID: e.endpointID, EndpointKeyID: e.endpointKey, ConnectorType: connectorcontract.TypeOpenAICompatible, CanonicalBaseURL: "https://charity.example.test/v1", UpstreamModelID: "upstream-model"}})
	if err != nil {
		t.Fatal(err)
	}
	grant, err := rail.TakeForDispatch(context.Background(), handle)
	if err != nil {
		t.Fatal(err)
	}
	grant.Clear()
	return handle
}

func (e *charityTestEnv) billingBalance(t *testing.T, userID, want int64) {
	t.Helper()
	tx := beginTestTx(t, e.store.DB())
	defer tx.Rollback()
	account, err := ledger.UserAccount(context.Background(), tx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if got := account.Balance.Decimal(); got != fmt.Sprint(want) {
		t.Fatalf("balance=%s want=%d", got, want)
	}
	if err := ledger.ValidateRecovery(context.Background(), tx); err != nil {
		t.Fatal(err)
	}
}

func TestResponseBillingUsesDurableStartForCompletionAndRecovery(t *testing.T) {
	for _, token := range []bool{false, true} {
		for _, started := range []bool{false, true} {
			for _, recovery := range []bool{false, true} {
				t.Run(fmt.Sprintf("token=%t/start=%t/recovery=%t", token, started, recovery), func(t *testing.T) {
					e := newCharityTestEnv(t)
					rail := e.billingRail(t)
					model, reserve := e.requestModel, int64(2400)
					if token {
						model, reserve = e.tokenModel, 5
					}
					request := e.billingRequest(t, rail, model, reserve, 1)
					handle := e.billingDispatch(t, rail, request.ID, 1)
					if started {
						for range 2 {
							if err := rail.MarkResponseStarted(context.Background(), handle); err != nil {
								t.Fatal(err)
							}
						}
					}
					if recovery {
						for range 2 {
							if _, err := rail.RecoverNonterminal(context.Background(), 100); err != nil {
								t.Fatal(err)
							}
						}
					} else {
						// Completion deliberately lacks in-memory start evidence, just
						// as a lost downstream write or late cancellation can do.
						for range 2 {
							if _, err := rail.CompleteAttempt(context.Background(), handle, claim.AttemptOutcome{Kind: claim.ResultSynthetic, UpstreamStatus: 502}); err != nil {
								t.Fatal(err)
							}
						}
						for range 2 {
							if _, err := rail.CompleteRequest(context.Background(), claim.CompleteRequestInput{RequestID: request.ID, Caller: claim.CallerResult{Class: claim.ResultCancelled}, Disposition: claim.AccountingCommit, ActualChargeMilli: reserve}); err != nil {
								t.Fatal(err)
							}
						}
					}
					wantCharge, wantPrice, wantCalls, wantTokens := int64(0), int64(0), int64(0), int64(0)
					if started {
						wantCharge, wantPrice, wantCalls, wantTokens = 2400, 3000, 1, 5
						if token {
							wantCharge, wantPrice = 4, 5
						}
					}
					e.billingBalance(t, e.callerID, 10000-wantCharge)
					e.billingBalance(t, e.donorID, 10000)
					var actualPrice, actualCalls, actualTokens, charge int64
					if err := e.store.DB().QueryRow(`SELECT u.price_actual_milli,u.calls_actual,u.tokens_actual,cr.user_charge_milli FROM donation_usage_reservations u JOIN dispatch_claims c ON c.id=u.claim_id JOIN charity_reservations cr ON cr.logical_request_id=c.logical_request_id WHERE c.id=?`, handle.ClaimID()).Scan(&actualPrice, &actualCalls, &actualTokens, &charge); err != nil {
						t.Fatal(err)
					}
					if actualPrice != wantPrice || actualCalls != wantCalls || actualTokens != wantTokens || charge != wantCharge {
						t.Fatalf("economics price/calls/tokens/charge=%d/%d/%d/%d", actualPrice, actualCalls, actualTokens, charge)
					}
					if err := rail.MarkResponseStarted(context.Background(), handle); err == nil {
						t.Fatal("terminal claim accepted a late response checkpoint")
					}
					var reservedPrice, reservedCalls, reservedTokens, streak []byte
					if err := e.store.DB().QueryRow(`SELECT price_reserved_mag,calls_reserved,tokens_reserved,failure_streak FROM donation_keys WHERE id=?`, e.donationKey).Scan(&reservedPrice, &reservedCalls, &reservedTokens, &streak); err != nil {
						t.Fatal(err)
					}
					assertU128(t, reservedPrice, 0, "reserved price")
					assertU128(t, reservedCalls, 0, "reserved calls")
					assertU128(t, reservedTokens, 0, "reserved tokens")
					assertU128(t, streak, 1, "real dispatched failure")
				})
			}
		}
	}
}

func TestResponseBillingRetryIgnoresUnknownUsageBeforeResponse(t *testing.T) {
	e := newCharityTestEnv(t)
	rail := e.billingRail(t)
	if _, err := e.store.DB().Exec(`UPDATE site_config SET value='1000' WHERE key='charity_token_reserve_milli'`); err != nil {
		t.Fatal(err)
	}
	request := e.billingRequest(t, rail, e.tokenModel, 1000, 2)
	first := e.billingDispatch(t, rail, request.ID, 1)
	if _, err := rail.CompleteAttempt(context.Background(), first, claim.AttemptOutcome{Kind: claim.ResultSynthetic, UpstreamStatus: 502}); err != nil {
		t.Fatal(err)
	}
	second := e.billingDispatch(t, rail, request.ID, 2)
	if err := rail.MarkResponseStarted(context.Background(), second); err != nil {
		t.Fatal(err)
	}
	if _, err := rail.CompleteAttempt(context.Background(), second, claim.AttemptOutcome{Kind: claim.ResultResponse, UpstreamStatus: 200, ProtocolSuccess: true, ResponseStarted: true, Usage: connectorcontract.Usage{Present: true, UncachedInputTokens: 1}}); err != nil {
		t.Fatal(err)
	}
	if _, err := rail.CompleteRequest(context.Background(), claim.CompleteRequestInput{RequestID: request.ID, Caller: claim.CallerResult{Class: claim.ResultSuccess, Status: 200}, Disposition: claim.AccountingCommit, ActualChargeMilli: 5}); err != nil {
		t.Fatal(err)
	}
	e.billingBalance(t, e.callerID, 9996)
	e.billingBalance(t, e.donorID, 10002)
	var unknown int
	if err := e.store.DB().QueryRow(`SELECT usage_unknown FROM charity_reservations WHERE logical_request_id=?`, request.ID).Scan(&unknown); err != nil || unknown != 0 {
		t.Fatalf("pre-response failure poisoned billable usage: %d %v", unknown, err)
	}
}
