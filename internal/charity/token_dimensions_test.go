package charity

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/claim"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/donationquota"
)

func (e *charityTestEnv) splitReserve(t *testing.T, input, output int64) {
	t.Helper()
	if _, err := e.store.DB().Exec(`UPDATE donation_keys SET input_token_reserve=?,output_token_reserve=? WHERE id=?`, input, output, e.donationKey); err != nil {
		t.Fatal(err)
	}
}

func (e *charityTestEnv) tokenTotals(t *testing.T, total, input, output, unknown, inflight int64) {
	t.Helper()
	var values [6][]byte
	if err := e.store.DB().QueryRow(`SELECT tokens_used,input_tokens_used,output_tokens_used,unattributed_total_tokens,input_tokens_reserved,output_tokens_reserved FROM donation_keys WHERE id=?`, e.donationKey).
		Scan(&values[0], &values[1], &values[2], &values[3], &values[4], &values[5]); err != nil {
		t.Fatal(err)
	}
	for n, want := range []int64{total, input, output, unknown, inflight, inflight} {
		assertU128(t, values[n], want, "token dimension")
	}
}

func TestTokenDimensionsSettleNormalizedUsageWithoutCapping(t *testing.T) {
	e := newCharityTestEnv(t)
	rail := e.billingRail(t)
	e.splitReserve(t, 2, 3)
	e.setQuota(t, quotaRule("tokens", "5"), quotaRule("input_tokens", "2"), quotaRule("output_tokens", "3"))
	request := e.billingRequest(t, rail, e.requestModel, 2400, 2)
	handle := e.billingDispatch(t, rail, request.ID, 1)
	if err := rail.MarkResponseStarted(context.Background(), handle); err != nil {
		t.Fatal(err)
	}
	outcome := claim.AttemptOutcome{Kind: claim.ResultResponse, ProtocolSuccess: true, ResponseStarted: true, UpstreamStatus: 200,
		Usage: contract.Usage{Present: true, UncachedInputTokens: 1, CacheWriteInputTokens: 2, CacheReadInputTokens: 3, OutputTokens: 9}}
	for range 2 {
		if _, err := rail.CompleteAttempt(context.Background(), handle, outcome); err != nil {
			t.Fatal(err)
		}
	}
	e.tokenTotals(t, 15, 6, 9, 0, 0)
	for n, view := range e.quotaViews(t) {
		want := []string{"15", "6", "9"}[n]
		if view.Used != want || view.Reserved != "0" || view.Remaining != "0" || view.EffectiveAt != charityTestNow+30 {
			t.Fatal(view)
		}
	}
	if _, err := rail.Claim(context.Background(), e.quotaClaimInput(request.ID, 2)); !errors.Is(err, donationquota.ErrLimited) {
		t.Fatal("overspend must stop the next attempt", err)
	}
}

func TestTokenDimensionsRecoveryUsesFrozenWideVector(t *testing.T) {
	for _, started := range []bool{false, true} {
		t.Run(map[bool]string{false: "unsent", true: "started"}[started], func(t *testing.T) {
			e := newCharityTestEnv(t)
			rail := e.billingRail(t)
			e.splitReserve(t, math.MaxInt64-3, 3)
			request := e.billingRequest(t, rail, e.requestModel, 2400, 1)
			handle, err := rail.Claim(context.Background(), e.quotaClaimInput(request.ID, 1))
			if err != nil {
				t.Fatal(err)
			}
			if started {
				grant, err := rail.TakeForDispatch(context.Background(), handle)
				if err != nil {
					t.Fatal(err)
				}
				grant.Clear()
				if err := rail.MarkResponseStarted(context.Background(), handle); err != nil {
					t.Fatal(err)
				}
			}
			// Future configuration cannot change the already accepted snapshot.
			e.splitReserve(t, 1, 1)
			for range 2 {
				if _, err := rail.RecoverNonterminal(context.Background(), 100); err != nil {
					t.Fatal(err)
				}
			}
			if started {
				e.tokenTotals(t, math.MaxInt64, math.MaxInt64-3, 3, 0, 0)
			} else {
				e.tokenTotals(t, 0, 0, 0, 0, 0)
			}
		})
	}
}

func TestTokenDimensionsReconcileAndRollbackAllOccupancy(t *testing.T) {
	e := newCharityTestEnv(t)
	rail := e.billingRail(t)
	e.splitReserve(t, 2, 3)
	request := e.billingRequest(t, rail, e.requestModel, 2400, 2)
	handle, err := rail.Claim(context.Background(), e.quotaClaimInput(request.ID, 1))
	if err != nil {
		t.Fatal(err)
	}
	e.splitReserve(t, 4, 1)
	e.setQuota(t, quotaRule("input_tokens", "4"), quotaRule("output_tokens", "1"))
	grant, err := rail.TakeForDispatch(context.Background(), handle)
	if err != nil {
		t.Fatal(err)
	}
	grant.Clear()
	var i, o int64
	if err := e.store.DB().QueryRow(`SELECT reserved_input_tokens,reserved_output_tokens FROM dispatch_claims WHERE id=?`, handle.ClaimID()).Scan(&i, &o); err != nil || i != 4 || o != 1 {
		t.Fatal(i, o, err)
	}
	if err := rail.MarkResponseStarted(context.Background(), handle); err != nil {
		t.Fatal(err)
	}
	if _, err := rail.CompleteAttempt(context.Background(), handle, claim.AttemptOutcome{Kind: claim.ResultSynthetic, UpstreamStatus: 502}); err != nil {
		t.Fatal(err)
	}
	e.tokenTotals(t, 5, 4, 1, 0, 0)
	// A zero dimension blocks even an attempt whose reservation there is zero.
	if _, err := e.store.DB().Exec(`UPDATE donation_keys SET input_token_reserve=0,output_token_reserve=1,input_token_limit_mag=zeroblob(16) WHERE id=?`, e.donationKey); err != nil {
		t.Fatal(err)
	}
	if _, err := rail.Claim(context.Background(), e.quotaClaimInput(request.ID, 2)); !errors.Is(err, donationquota.ErrLimited) {
		t.Fatal(err)
	}
	e.tokenTotals(t, 5, 4, 1, 0, 0)
}

func TestTokenDimensionsLegacyUnknownIsNotKnownZero(t *testing.T) {
	e := newCharityTestEnv(t)
	rail := e.billingRail(t)
	request := e.billingRequest(t, rail, e.requestModel, 2400, 1)
	handle := e.billingDispatch(t, rail, request.ID, 1)
	if err := rail.MarkResponseStarted(context.Background(), handle); err != nil {
		t.Fatal(err)
	}
	if _, err := rail.RecoverNonterminal(context.Background(), 100); err != nil {
		t.Fatal(err)
	}
	e.tokenTotals(t, 5, 0, 0, 5, 0)
	var input, output *int64
	if err := e.store.DB().QueryRow(`SELECT input_tokens_actual,output_tokens_actual FROM donation_usage_reservations WHERE claim_id=?`, handle.ClaimID()).Scan(&input, &output); err != nil || input != nil || output != nil {
		t.Fatal(input, output, err)
	}
}

func TestTokenDimensionsConcurrentClaimsShareLastOutputCapacity(t *testing.T) {
	e := newCharityTestEnv(t)
	rail := e.billingRail(t)
	e.splitReserve(t, 2, 3)
	limit, _ := db.ParseU128Decimal("3")
	if _, err := e.store.DB().Exec(`UPDATE donation_keys SET output_token_limit_mag=? WHERE id=?`, db.EncodeU128(limit), e.donationKey); err != nil {
		t.Fatal(err)
	}
	request := e.billingRequest(t, rail, e.requestModel, 2400, 8)
	var group sync.WaitGroup
	results := make(chan error, 8)
	handles := make(chan claim.Handle, 8)
	for n := 1; n <= 8; n++ {
		group.Go(func() {
			h, err := rail.Claim(context.Background(), e.quotaClaimInput(request.ID, n))
			results <- err
			if err == nil {
				handles <- h
			}
		})
	}
	group.Wait()
	close(results)
	close(handles)
	winners := 0
	for err := range results {
		if err == nil {
			winners++
		} else if !errors.Is(err, donationquota.ErrLimited) {
			t.Fatal(err)
		}
	}
	if winners != 1 {
		t.Fatal("last output slot winners", winners)
	}
	for h := range handles {
		if _, err := rail.ReleaseUndispatched(context.Background(), h); err != nil {
			t.Fatal(err)
		}
	}
	e.tokenTotals(t, 0, 0, 0, 0, 0)
}

func TestTokenDimensionsIndependentCumulativeLimits(t *testing.T) {
	for mask := 0; mask < 8; mask++ {
		t.Run(fmt.Sprint(mask), func(t *testing.T) {
			e := newCharityTestEnv(t)
			rail := e.billingRail(t)
			e.splitReserve(t, 2, 3)
			var limits [3]any
			for n, value := range []string{"5", "2", "3"} {
				if mask&(1<<n) != 0 {
					v, _ := db.ParseU128Decimal(value)
					limits[n] = db.EncodeU128(v)
				}
			}
			if _, err := e.store.DB().Exec(`UPDATE donation_keys SET token_limit_mag=?,input_token_limit_mag=?,output_token_limit_mag=? WHERE id=?`, limits[0], limits[1], limits[2], e.donationKey); err != nil {
				t.Fatal(err)
			}
			request := e.billingRequest(t, rail, e.requestModel, 2400, 2)
			first, err := rail.Claim(context.Background(), e.quotaClaimInput(request.ID, 1))
			if err != nil {
				t.Fatal(err)
			}
			second, err := rail.Claim(context.Background(), e.quotaClaimInput(request.ID, 2))
			if mask == 0 {
				if err != nil {
					t.Fatal(err)
				}
				if _, err := rail.ReleaseUndispatched(context.Background(), second); err != nil {
					t.Fatal(err)
				}
			} else if !errors.Is(err, donationquota.ErrLimited) && !errors.Is(err, claim.ErrNotFound) {
				t.Fatal("configured dimension did not stop admission", err)
			}
			if _, err := rail.ReleaseUndispatched(context.Background(), first); err != nil {
				t.Fatal(err)
			}
			e.tokenTotals(t, 0, 0, 0, 0, 0)
		})
	}
}
