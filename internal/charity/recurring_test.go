package charity

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/claim"
	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/donationquota"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

func deleteBillingIdentity(t *testing.T, tx *sql.Tx, userID int64) {
	t.Helper()
	ctx := context.Background()
	wallet, err := ledger.UserAccount(ctx, tx, userID)
	if err != nil {
		t.Fatal(err)
	}
	external, err := ledger.CodedAccount(ctx, tx, "external")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := ledger.NewAccountDeleteZero(ledger.Meta{OperationID: mustOpaqueID(t, "op_"), ActorUserID: userID, CreatedAt: charityTestNow + 30}, wallet.ID, external.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Apply(ctx, tx, plan); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`DELETE FROM credit_accounts WHERE id=? AND balance_sign=0`, wallet.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`DELETE FROM users WHERE id=?`, userID); err != nil {
		t.Fatal(err)
	}
}

func TestRecurringCallerDeletionKeepsSentUsageAndReleasesUnsentReservation(t *testing.T) {
	e := newCharityTestEnv(t)
	rail := e.billingRail(t)
	e.setQuota(t, quotaRule("calls", "5"), quotaRule("tokens", "20"))
	request := e.billingRequest(t, rail, e.requestModel, 2400, 2)
	sent := e.billingDispatch(t, rail, request.ID, 1)
	unsent, err := rail.Claim(context.Background(), e.quotaClaimInput(request.ID, 2))
	if err != nil {
		t.Fatal(err)
	}
	tx := beginTestTx(t, e.store.DB())
	if err := rail.PrepareAccountDeletion(context.Background(), tx, e.callerID, charityTestNow+30); err != nil {
		t.Fatal(err)
	}
	deleteBillingIdentity(t, tx, e.callerID)
	commitTestTx(t, tx)
	if _, err := rail.ReleaseUndispatched(context.Background(), unsent); err != nil {
		t.Fatal(err)
	}
	if v := e.quotaViews(t); v[0].Reserved != "1" || v[1].Reserved != "5" {
		t.Fatal(v)
	}
	if err := rail.MarkResponseStarted(context.Background(), sent); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := rail.RecoverNonterminal(context.Background(), 100); err != nil {
			t.Fatal(err)
		}
	}
	v := e.quotaViews(t)
	if v[0].Used != "1" || v[1].Used != "5" || v[0].Reserved != "0" || v[1].Reserved != "0" {
		t.Fatal(v)
	}
	var attached int
	if err := e.store.DB().QueryRow(`SELECT (SELECT COUNT(*) FROM users WHERE id=?)+(SELECT COUNT(*) FROM logical_requests WHERE user_id=?)+(SELECT COUNT(*) FROM charity_reservations WHERE user_id=?)`, e.callerID, e.callerID, e.callerID).Scan(&attached); err != nil || attached != 0 {
		t.Fatal(attached, err)
	}
	e.billingBalance(t, e.donorID, 10000)
}

func TestPersonalAndLiveCallsNeverConsumeRecurringDonationLimits(t *testing.T) {
	e := newCharityTestEnv(t)
	rail := e.billingRail(t)
	e.setQuota(t, quotaRule("calls", "100"), quotaRule("tokens", "0"))
	for n := range 30 {
		request, err := rail.Accept(context.Background(), claim.AcceptInput{UserID: e.donorID, Route: claim.RouteOpenAIChat, ModelSnapshot: "personal/model", AttemptLimit: 1, ReservedMilli: 1})
		if err != nil {
			t.Fatal(err)
		}
		input := e.quotaClaimInput(request.ID, 1)
		input.ActorUserID, input.DonationKeyID, input.Purpose = e.donorID, 0, claim.PurposeSelf
		if n%2 == 1 {
			input.Purpose = claim.PurposeDebugLive
		}
		handle, err := rail.Claim(context.Background(), input)
		if err != nil {
			t.Fatal(err)
		}
		dispatch, err := rail.TakeForDispatch(context.Background(), handle)
		if err != nil {
			t.Fatal(err)
		}
		dispatch.Clear()
		if _, err := rail.CompleteAttempt(context.Background(), handle, claim.AttemptOutcome{Kind: claim.ResultResponse, ProtocolSuccess: true, ResponseStarted: true, UpstreamStatus: 200}); err != nil {
			t.Fatal(err)
		}
		if _, err := rail.CompleteRequest(context.Background(), claim.CompleteRequestInput{RequestID: request.ID, Caller: claim.CallerResult{Class: claim.ResultSuccess, Status: 200}, Disposition: claim.AccountingCommit, ActualChargeMilli: 1}); err != nil {
			t.Fatal(err)
		}
	}
	v := e.quotaViews(t)
	if v[0].Used != "0" || v[0].Reserved != "0" || v[0].Remaining != "100" || v[0].State != "waiting_first_success" || v[0].PeriodStart != nil {
		t.Fatal(v)
	}
	var receipts int
	if err := e.store.DB().QueryRow(`SELECT COUNT(*) FROM donation_quota_receipts`).Scan(&receipts); err != nil || receipts != 0 {
		t.Fatal(receipts, err)
	}
	e.billingBalance(t, e.donorID, 9970)
}

func TestLegacyDispatchWithoutReceiptsDoesNotAcquireNewRulesDuringRecovery(t *testing.T) {
	e := newCharityTestEnv(t)
	rail := e.billingRail(t)
	request := e.billingRequest(t, rail, e.requestModel, 2400, 1)
	handle := e.billingDispatch(t, rail, request.ID, 1)
	e.setQuota(t, quotaRule("calls", "0"), quotaRule("tokens", "0"))
	if err := rail.MarkResponseStarted(context.Background(), handle); err != nil {
		t.Fatal(err)
	}
	if _, err := rail.RecoverNonterminal(context.Background(), 100); err != nil {
		t.Fatal(err)
	}
	for _, v := range e.quotaViews(t) {
		if v.Used != "0" || v.Reserved != "0" || v.PeriodStart != nil {
			t.Fatal(v)
		}
	}
	e.billingBalance(t, e.callerID, 7600)
}

func TestDonationRetentionWaitsForSettlementAndBoundedQuotaCollection(t *testing.T) {
	e := newCharityTestEnv(t)
	rail := e.billingRail(t)
	e.setQuota(t, quotaRule("calls", "5"), quotaRule("tokens", "20"))
	request := e.billingRequest(t, rail, e.requestModel, 2400, 1)
	handle := e.billingDispatch(t, rail, request.ID, 1)
	tx := beginTestTx(t, e.store.DB())
	if err := e.donation.PrepareAccountDeletion(context.Background(), tx, e.donorID, charityTestNow+30); err != nil {
		t.Fatal(err)
	}
	deleteBillingIdentity(t, tx, e.donorID)
	commitTestTx(t, tx)
	later := int64(charityTestNow + 30 + 401*86400)
	if n, err := e.donation.Cleanup(context.Background(), later, 100); err != nil || n != 0 {
		t.Fatal("collected unfinished donation", n, err)
	}
	if err := rail.MarkResponseStarted(context.Background(), handle); err != nil {
		t.Fatal(err)
	}
	if _, err := rail.RecoverNonterminal(context.Background(), 100); err != nil {
		t.Fatal(err)
	}
	if n, err := e.donation.Cleanup(context.Background(), later, 100); err != nil || n != 1 {
		t.Fatal("did not retire quota", n, err)
	}
	var count int
	if err := e.store.DB().QueryRow(`SELECT COUNT(*) FROM donations WHERE id=?`, e.donationID).Scan(&count); err != nil || count != 1 {
		t.Fatal("parent removed before quota collection", count, err)
	}
	for range 20 {
		tx := beginTestTx(t, e.store.DB())
		n, err := donationquota.Cleanup(context.Background(), tx, later, 1)
		if err != nil || n > 1 {
			_ = tx.Rollback()
			t.Fatal(n, err)
		}
		commitTestTx(t, tx)
		if err := e.service.ValidateRecurringState(context.Background()); err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			break
		}
	}
	if n, err := e.donation.Cleanup(context.Background(), later, 100); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if err := e.store.DB().QueryRow(`SELECT (SELECT COUNT(*) FROM donations WHERE id=?)+(SELECT rows_used+rows_held FROM donation_quota_capacity WHERE id=1)`, e.donationID).Scan(&count); err != nil || count != 0 {
		t.Fatal("retained parent or leaked quota capacity", count, err)
	}
	e.billingBalance(t, e.callerID, 7600)
}

func TestDispatchRefreshesTokenFallbackAndRollsBackRejectedChanges(t *testing.T) {
	for _, test := range []struct {
		name    string
		next    int64
		limit   string
		total   bool
		limited bool
	}{
		{"increase", 8, "10", false, false},
		{"decrease", 2, "10", false, false},
		{"recurring rejects increase", 8, "6", false, true},
		{"total rejects increase", 8, "10", true, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			e := newCharityTestEnv(t)
			rail := e.billingRail(t)
			e.setQuota(t, quotaRule("tokens", test.limit))
			request := e.billingRequest(t, rail, e.requestModel, 2400, 1)
			handle, err := rail.Claim(context.Background(), e.quotaClaimInput(request.ID, 1))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := e.store.DB().Exec(`UPDATE donation_keys SET token_reserve=? WHERE id=?`, test.next, e.donationKey); err != nil {
				t.Fatal(err)
			}
			if test.total {
				limit, _ := db.U128FromBig(big.NewInt(6))
				if _, err := e.store.DB().Exec(`UPDATE donation_keys SET token_limit_mag=? WHERE id=?`, db.EncodeU128(limit), e.donationKey); err != nil {
					t.Fatal(err)
				}
			}
			dispatch, err := rail.TakeForDispatch(context.Background(), handle)
			want := test.next
			if test.limited {
				if !errors.Is(err, donationquota.ErrLimited) {
					t.Fatal(err)
				}
				want = 5
			} else {
				if err != nil {
					t.Fatal(err)
				}
				dispatch.Clear()
			}
			var claimTokens, usageTokens int64
			var totalReserved []byte
			if err := e.store.DB().QueryRow(`SELECT c.reserved_tokens,u.tokens_reserved,k.tokens_reserved FROM dispatch_claims c JOIN donation_usage_reservations u ON u.claim_id=c.id JOIN donation_keys k ON k.id=u.donation_key_id WHERE c.logical_request_id=?`, request.ID).Scan(&claimTokens, &usageTokens, &totalReserved); err != nil {
				t.Fatal(err)
			}
			if claimTokens != want || usageTokens != want {
				t.Fatal(claimTokens, usageTokens, want)
			}
			assertU128(t, totalReserved, want, "dispatch total token reservation")
			if v := e.quotaViews(t); v[0].Reserved != strconv.FormatInt(want, 10) {
				t.Fatal(v)
			}
			if test.limited {
				if _, err := rail.ReleaseUndispatched(context.Background(), handle); err != nil {
					t.Fatal(err)
				}
			} else {
				// After dispatch, another edit cannot change this attempt's fallback.
				if _, err := e.store.DB().Exec(`UPDATE donation_keys SET token_reserve=1 WHERE id=?`, e.donationKey); err != nil {
					t.Fatal(err)
				}
				if err := rail.MarkResponseStarted(context.Background(), handle); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := rail.RecoverNonterminal(context.Background(), 100); err != nil {
				t.Fatal(err)
			}
			used := "0"
			if !test.limited {
				used = strconv.FormatInt(want, 10)
			}
			if v := e.quotaViews(t); v[0].Used != used || v[0].Reserved != "0" {
				t.Fatal(v)
			}
		})
	}
}

func quotaRule(metric, limit string) donationquota.RuleInput {
	alignment := "first_success"
	return donationquota.RuleInput{Mode: "reset", Interval: "5h", Alignment: &alignment, TimeZone: "UTC", Metric: metric, Limit: limit}
}

func (e *charityTestEnv) setQuota(t *testing.T, rules ...donationquota.RuleInput) []donationquota.RuleView {
	t.Helper()
	tx := beginTestTx(t, e.store.DB())
	if err := donationquota.Replace(context.Background(), tx, e.donationKey, charityTestNow+30, rules); err != nil {
		t.Fatal(err)
	}
	commitTestTx(t, tx)
	return e.quotaViews(t)
}
func (e *charityTestEnv) quotaViews(t *testing.T) []donationquota.RuleView {
	t.Helper()
	if err := e.service.ValidateRecurringState(context.Background()); err != nil {
		t.Fatal(err)
	}
	v, err := donationquota.Views(context.Background(), e.store.DB(), e.donationKey, charityTestNow+30)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func (e *charityTestEnv) quotaClaimInput(requestID string, seq int) claim.ClaimInput {
	return claim.ClaimInput{RequestID: requestID, ActorUserID: e.callerID, AttemptSeq: seq, Purpose: claim.PurposeCharity, DonationKeyID: e.donationKey, Candidate: claim.Candidate{EndpointID: e.endpointID, EndpointKeyID: e.endpointKey, ConnectorType: connectorcontract.TypeOpenAICompatible, CanonicalBaseURL: "https://charity.example.test/v1", UpstreamModelID: "upstream-model"}}
}

func TestRecurringLimitsUseDurableMarkerAndOriginalBillingDuringRecovery(t *testing.T) {
	for _, mode := range []string{"first_success", "calendar", "sliding"} {
		for _, started := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/started=%t", mode, started), func(t *testing.T) {
				e := newCharityTestEnv(t)
				rail := e.billingRail(t)
				rules := []donationquota.RuleInput{quotaRule("calls", "5"), quotaRule("credits", "5"), quotaRule("tokens", "10")}
				for i := range rules {
					if mode == "sliding" {
						rules[i].Mode = "sliding"
						rules[i].Alignment = nil
					} else if mode == "calendar" {
						alignment := "calendar"
						rules[i].Alignment = &alignment
						rules[i].Interval = "day"
					}
				}
				e.setQuota(t, rules...)
				request := e.billingRequest(t, rail, e.requestModel, 2400, 1)
				handle := e.billingDispatch(t, rail, request.ID, 1)
				if started {
					for i := 0; i < 2; i++ {
						if err := rail.MarkResponseStarted(context.Background(), handle); err != nil {
							t.Fatal(err)
						}
					}
				}
				e.quotaViews(t)
				for i := 0; i < 2; i++ {
					if _, err := rail.RecoverNonterminal(context.Background(), 100); err != nil {
						t.Fatal(err)
					}
				}
				v := e.quotaViews(t)
				want := []string{"0", "0", "0"}
				if started {
					want = []string{"1", "3", "5"}
				}
				for i, value := range v {
					if value.Used != want[i] || value.Reserved != "0" {
						t.Fatal(v)
					}
				}
				charge := int64(0)
				if started {
					charge = 2400
				}
				e.billingBalance(t, e.callerID, 10000-charge)
				e.billingBalance(t, e.donorID, 10000)
				if _, err := e.service.Cleanup(context.Background(), charityTestNow+31, 100); err != nil {
					t.Fatal(err)
				}
				e.quotaViews(t)
				var count int
				if err := e.store.DB().QueryRow(`SELECT COUNT(*) FROM donation_quota_receipts`).Scan(&count); err != nil || count != 0 {
					t.Fatal(count, err)
				}
			})
		}
	}
}

func TestRecurringDispatchChangeReleasesOwnReservationWithoutCharge(t *testing.T) {
	for _, route := range []claim.RouteKind{claim.RouteCharityChat, claim.RouteCharityEmbeddings} {
		t.Run(string(route), func(t *testing.T) { testRecurringDispatchChangeReleases(t, route) })
	}
}

func testRecurringDispatchChangeReleases(t *testing.T, route claim.RouteKind) {
	for _, original := range []bool{false, true} {
		t.Run(fmt.Sprint(original), func(t *testing.T) {
			e := newCharityTestEnv(t)
			rail := e.billingRail(t)
			var rules []donationquota.RuleView
			if original {
				rules = e.setQuota(t, quotaRule("calls", "1"))
			}
			request := e.billingRequest(t, rail, e.requestModel, 2400, 1, route)
			handle, err := rail.Claim(context.Background(), e.quotaClaimInput(request.ID, 1))
			if err != nil {
				t.Fatal(err)
			}
			changed := quotaRule("calls", "0")
			if original {
				changed.ID = rules[0].ID
			}
			e.setQuota(t, changed)
			if _, err := rail.TakeForDispatch(context.Background(), handle); !errors.Is(err, donationquota.ErrLimited) {
				t.Fatal(err)
			}
			if _, err := rail.ReleaseUndispatched(context.Background(), handle); err != nil {
				t.Fatal(err)
			}
			if _, err := rail.CompleteRequest(context.Background(), claim.CompleteRequestInput{RequestID: request.ID, Caller: claim.CallerResult{Class: claim.ResultFailed, Status: 429, ErrorCode: "rate_limited"}, Disposition: claim.AccountingRelease}); err != nil {
				t.Fatal(err)
			}
			e.billingBalance(t, e.callerID, 10000)
			e.billingBalance(t, e.donorID, 10000)
			v := e.quotaViews(t)
			if v[0].Used != "0" || v[0].Reserved != "0" {
				t.Fatal(v)
			}
			var count int
			if err := e.store.DB().QueryRow(`SELECT COUNT(*) FROM dispatch_response_starts`).Scan(&count); err != nil || count != 0 {
				t.Fatal(count, err)
			}
		})
	}
}

func TestRecurringCapacityFailureKeepsOneSafeAlertAndNoNewClaim(t *testing.T) {
	e := newCharityTestEnv(t)
	rail := e.billingRail(t)
	e.setQuota(t, quotaRule("calls", "10"))
	request := e.billingRequest(t, rail, e.requestModel, 2400, 2)
	if _, err := e.store.DB().Exec(`UPDATE donation_quota_capacity SET rows_used=?`, donationquota.MaxRows); err != nil {
		t.Fatal(err)
	}
	for seq := 1; seq <= 2; seq++ {
		if _, err := rail.Claim(context.Background(), e.quotaClaimInput(request.ID, seq)); !errors.Is(err, donationquota.ErrCapacity) {
			t.Fatal(err)
		}
	}
	var alerts, claims int
	if err := e.store.DB().QueryRow(`SELECT (SELECT COUNT(*) FROM admin_alerts WHERE ref='charity_recurring_capacity'),(SELECT COUNT(*) FROM dispatch_claims)`).Scan(&alerts, &claims); err != nil || alerts != 1 || claims != 0 {
		t.Fatal(alerts, claims, err)
	}
	if _, err := e.store.DB().Exec(`UPDATE donation_quota_capacity SET rows_used=0`); err != nil {
		t.Fatal(err)
	}
	e.quotaViews(t)
	if _, err := rail.CompleteRequest(context.Background(), claim.CompleteRequestInput{RequestID: request.ID, Caller: claim.CallerResult{Class: claim.ResultFailed, Status: 503, ErrorCode: "service_unavailable"}, Disposition: claim.AccountingRelease}); err != nil {
		t.Fatal(err)
	}
	e.billingBalance(t, e.callerID, 10000)
}

func TestRecurringAlreadyDispatchedOldEpochSurvivesDonorDeletion(t *testing.T) {
	e := newCharityTestEnv(t)
	rail := e.billingRail(t)
	rules := e.setQuota(t, quotaRule("calls", "2"), quotaRule("credits", "10"))
	request := e.billingRequest(t, rail, e.requestModel, 2400, 1)
	handle := e.billingDispatch(t, rail, request.ID, 1)
	changed := rules[0].RuleInput
	changed.Interval = "day"
	e.setQuota(t, changed)
	tx := beginTestTx(t, e.store.DB())
	if err := e.donation.PrepareAccountDeletion(context.Background(), tx, e.donorID, charityTestNow+30); err != nil {
		t.Fatal(err)
	}
	deleteBillingIdentity(t, tx, e.donorID)
	commitTestTx(t, tx)
	if err := rail.MarkResponseStarted(context.Background(), handle); err != nil {
		t.Fatal(err)
	}
	if _, err := rail.RecoverNonterminal(context.Background(), 100); err != nil {
		t.Fatal(err)
	}
	v := e.quotaViews(t)
	if v[0].Used != "0" {
		t.Fatal(v)
	}
	var used []byte
	if err := e.store.DB().QueryRow(`SELECT used_mag FROM donation_quota_periods WHERE rule_id=? AND epoch=1`, *rules[0].ID).Scan(&used); err != nil {
		t.Fatal(err)
	}
	assertU128(t, used, 1, "retired donor quota")
	e.billingBalance(t, e.callerID, 7600)
	if err := func() error {
		tx, err := e.store.DB().BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
		if err != nil {
			return err
		}
		defer tx.Rollback()
		return donationquota.ValidateState(context.Background(), tx)
	}(); err != nil {
		t.Fatal(err)
	}
}
