package charity

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/claim"
	"github.com/waiting-here/NonbiriAPI/internal/connectionstore/testseed"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/donationquota"
)

func (e *charityTestEnv) setModelQuota(t *testing.T, model string, limits donationquota.ModelLimits, rules ...donationquota.RuleInput) {
	t.Helper()
	tx := beginTestTx(t, e.store.DB())
	if err := donationquota.SetModelLimits(t.Context(), tx, e.donationKey, charityTestNow+30, model, limits); err != nil {
		t.Fatal(err)
	}
	if err := donationquota.ReplaceModel(t.Context(), tx, e.donationKey, charityTestNow+30, model, rules); err != nil {
		t.Fatal(err)
	}
	commitTestTx(t, tx)
}

func (e *charityTestEnv) modelUsage(t *testing.T, model string) donationquota.ModelView {
	t.Helper()
	value, err := donationquota.ModelUsage(t.Context(), e.store.DB(), e.donationKey, model)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestModelQuotasShareBindingsAndKeepOtherModelsAvailable(t *testing.T) {
	e := newCharityTestEnv(t)
	rail := e.billingRail(t)
	e.splitReserve(t, 2, 3)
	limit := "5"
	e.setModelQuota(t, "upstream-model", donationquota.ModelLimits{Tokens: &limit}, quotaRule("tokens", "5"))
	tx := beginTestTx(t, e.store.DB())
	if _, err := testseed.InsertCharityBindingTx(t.Context(), tx, testseed.BindingInput{ModelID: e.requestModel, KeyID: e.donationKey, UpstreamModelID: "other-model", Order: 1, At: charityTestNow}); err != nil {
		t.Fatal(err)
	}
	commitTestTx(t, tx)
	first := e.billingRequest(t, rail, e.requestModel, 2400, 1)
	h, err := rail.Claim(t.Context(), e.quotaClaimInput(t, first.ID, 1))
	if err != nil {
		t.Fatal(err)
	}
	// A second public model binds the same upstream name and must share its quota.
	second := e.billingRequest(t, rail, e.tokenModel, 5, 2)
	if _, err = rail.Claim(t.Context(), e.quotaClaimInput(t, second.ID, 1)); !errors.Is(err, donationquota.ErrLimited) {
		t.Fatal("same upstream bypassed quota", err)
	}
	third := e.billingRequest(t, rail, e.requestModel, 2400, 1)
	other := e.quotaClaimInput(t, third.ID, 1)
	tx = beginTestTx(t, e.store.DB())
	other.Candidate.Authorization = e.authorizationFor(t, tx, third.ID, e.donationKey, "other-model")
	tx.Rollback()
	other.Candidate.UpstreamModelID = "other-model"
	otherHandle, err := rail.Claim(t.Context(), other)
	if err != nil {
		t.Fatal("unrelated upstream blocked", err)
	}
	for _, handle := range []claim.Handle{h, otherHandle} {
		if _, err = rail.ReleaseUndispatched(t.Context(), handle); err != nil {
			t.Fatal(err)
		}
	}
	for _, model := range []string{"upstream-model", "other-model"} {
		if usage := e.modelUsage(t, model); usage.Used.Tokens != "0" || usage.Reserved.Tokens != "0" {
			t.Fatal(usage)
		}
	}
	if err = e.service.ValidateRecurringState(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestModelQuotasReconcileRecoveryAndLimitRemoval(t *testing.T) {
	for _, started := range []bool{false, true} {
		t.Run(map[bool]string{false: "undispatched", true: "started"}[started], func(t *testing.T) {
			e := newCharityTestEnv(t)
			rail := e.billingRail(t)
			e.splitReserve(t, 2, 3)
			limit := "5"
			e.setModelQuota(t, "upstream-model", donationquota.ModelLimits{Tokens: &limit}, quotaRule("tokens", "5"), quotaRule("input_tokens", "2"))
			request := e.billingRequest(t, rail, e.requestModel, 2400, 1)
			handle, err := rail.Claim(t.Context(), e.quotaClaimInput(t, request.ID, 1))
			if err != nil {
				t.Fatal(err)
			}
			if started {
				grant, err := rail.TakeForDispatch(t.Context(), handle)
				if err != nil {
					t.Fatal(err)
				}
				grant.Clear()
				if err = rail.MarkResponseStarted(t.Context(), handle); err != nil {
					t.Fatal(err)
				}
			} else {
				zero := "0"
				e.setModelQuota(t, "upstream-model", donationquota.ModelLimits{Tokens: &zero}, quotaRule("tokens", "0"))
				if _, err = rail.TakeForDispatch(t.Context(), handle); !errors.Is(err, donationquota.ErrLimited) {
					t.Fatal("lowered limit admitted dispatch", err)
				}
			}
			// Removing limits must preserve and settle the frozen original receipt.
			e.setModelQuota(t, "upstream-model", donationquota.ModelLimits{})
			for range 2 {
				if _, err = rail.RecoverNonterminal(t.Context(), 100); err != nil {
					t.Fatal(err)
				}
			}
			usage := e.modelUsage(t, "upstream-model")
			want := "0"
			if started {
				want = "5"
			}
			if usage.Used.Tokens != want || usage.Reserved.Tokens != "0" || usage.Limits.Tokens != nil {
				t.Fatal(usage)
			}
			if err = e.service.ValidateRecurringState(t.Context()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestModelQuotasConcurrentLastSlotAndActualOverspend(t *testing.T) {
	e := newCharityTestEnv(t)
	rail := e.billingRail(t)
	e.splitReserve(t, 2, 3)
	limit := "3"
	e.setModelQuota(t, "upstream-model", donationquota.ModelLimits{OutputTokens: &limit}, quotaRule("output_tokens", "3"))
	request := e.billingRequest(t, rail, e.requestModel, 2400, 8)
	inputs := make([]claim.ClaimInput, 8)
	for i := range inputs {
		inputs[i] = e.quotaClaimInput(t, request.ID, i+1)
	}
	var wg sync.WaitGroup
	handles := make(chan claim.Handle, 8)
	errs := make(chan error, 8)
	for _, input := range inputs {
		wg.Go(func() {
			h, err := rail.Claim(context.Background(), input)
			errs <- err
			if err == nil {
				handles <- h
			}
		})
	}
	wg.Wait()
	close(errs)
	close(handles)
	winners := 0
	for err := range errs {
		if err == nil {
			winners++
		} else if !errors.Is(err, donationquota.ErrLimited) {
			t.Fatal(err)
		}
	}
	if winners != 1 {
		t.Fatal(winners)
	}
	h := <-handles
	grant, err := rail.TakeForDispatch(t.Context(), h)
	if err != nil {
		t.Fatal(err)
	}
	grant.Clear()
	if err = rail.MarkResponseStarted(t.Context(), h); err != nil {
		t.Fatal(err)
	}
	outcome := claim.AttemptOutcome{Kind: claim.ResultResponse, ProtocolSuccess: true, ResponseStarted: true, UpstreamStatus: 200, Usage: contract.Usage{Present: true, UncachedInputTokens: 4, OutputTokens: 9}}
	for range 2 {
		if _, err = rail.CompleteAttempt(t.Context(), h, outcome); err != nil {
			t.Fatal(err)
		}
	}
	usage := e.modelUsage(t, "upstream-model")
	if usage.Used.Tokens != "13" || usage.Used.OutputTokens != "9" || usage.Reserved.Tokens != "0" || *usage.Remaining.OutputTokens != "0" {
		t.Fatal(usage)
	}
	if err = e.service.ValidateRecurringState(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestModelQuotasCombineAllKeyAndModelRules(t *testing.T) {
	e := newCharityTestEnv(t)
	rail := e.billingRail(t)
	whole, scoped := make([]donationquota.RuleInput, donationquota.MaxRules), make([]donationquota.RuleInput, donationquota.MaxRules)
	for i := range whole {
		whole[i] = quotaRule("tokens", "10")
		scoped[i] = quotaRule("tokens", "5")
	}
	e.setQuota(t, whole...)
	e.setModelQuota(t, "upstream-model", donationquota.ModelLimits{}, scoped...)
	e.setModelQuota(t, "unrelated-model", donationquota.ModelLimits{}, scoped...)
	request := e.billingRequest(t, rail, e.requestModel, 2400, 1)
	handle := e.billingDispatch(t, rail, request.ID, 1)
	var receipts int
	if err := e.store.DB().QueryRow(`SELECT COUNT(*) FROM donation_quota_receipts WHERE claim_id=?`, handle.ClaimID()).Scan(&receipts); err != nil || receipts != 32 {
		t.Fatal(receipts, err)
	}
	if err := rail.MarkResponseStarted(t.Context(), handle); err != nil {
		t.Fatal(err)
	}
	if _, err := rail.RecoverNonterminal(t.Context(), 100); err != nil {
		t.Fatal(err)
	}
	for _, model := range []string{"", "upstream-model", "unrelated-model"} {
		views, err := donationquota.ModelViews(t.Context(), e.store.DB(), e.donationKey, charityTestNow+30, model)
		if err != nil || len(views) != 16 {
			t.Fatal(model, len(views), err)
		}
		want := "5"
		if model == "unrelated-model" {
			want = "0"
		}
		for _, view := range views {
			if view.Used != want || view.Reserved != "0" {
				t.Fatal(model, view)
			}
		}
	}
	// A model's recurring period resets independently of the key's counters.
	views, err := donationquota.ModelViews(t.Context(), e.store.DB(), e.donationKey, charityTestNow+30+5*3600, "upstream-model")
	if err != nil || views[0].Remaining != "5" {
		t.Fatal(views, err)
	}
	if err := e.service.ValidateRecurringState(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestModelQuotasLegacyInFlightHasNoInventedModelUsage(t *testing.T) {
	e := newCharityTestEnv(t)
	rail := e.billingRail(t)
	request := e.billingRequest(t, rail, e.requestModel, 2400, 1)
	handle := e.billingDispatch(t, rail, request.ID, 1)
	if err := rail.MarkResponseStarted(t.Context(), handle); err != nil {
		t.Fatal(err)
	}
	// This is the state supplied by upgrading a claim created before model counters.
	if _, err := e.store.DB().Exec(`UPDATE donation_usage_reservations SET upstream_model_id='' WHERE claim_id=?`, handle.ClaimID()); err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.DB().Exec(`DELETE FROM donation_model_quotas WHERE donation_key_id=?`, e.donationKey); err != nil {
		t.Fatal(err)
	}
	zero := "0"
	e.setModelQuota(t, "upstream-model", donationquota.ModelLimits{Tokens: &zero})
	for range 2 {
		if _, err := rail.RecoverNonterminal(t.Context(), 100); err != nil {
			t.Fatal(err)
		}
	}
	e.tokenTotals(t, 5, 0, 0, 5, 0)
	usage := e.modelUsage(t, "upstream-model")
	if usage.Used.Tokens != "0" || usage.Reserved.Tokens != "0" {
		t.Fatal(usage)
	}
	if err := e.service.ValidateRecurringState(t.Context()); err != nil {
		t.Fatal(err)
	}
}
