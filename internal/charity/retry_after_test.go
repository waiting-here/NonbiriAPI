package charity

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
	"sync"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/claim"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

func (e *charityTestEnv) retryClaimInput(request string) claim.ClaimInput {
	return claim.ClaimInput{RequestID: request, ActorUserID: e.callerID, AttemptSeq: 1, Purpose: claim.PurposeCharity, DonationKeyID: e.donationKey, Candidate: claim.Candidate{EndpointID: e.endpointID, EndpointKeyID: e.endpointKey, ConnectorType: contract.TypeOpenAICompatible, CanonicalBaseURL: "https://charity.example.test/v1", UpstreamModelID: "upstream-model"}}
}

func TestRetryAfterBlocksNewAndPendingDispatchAcrossModels(t *testing.T) {
	e := newCharityTestEnv(t)
	rail := e.billingRail(t)
	ctx := context.Background()
	first := e.billingRequest(t, rail, e.requestModel, 2400, 1)
	active := e.billingDispatch(t, rail, first.ID, 1)
	second := e.billingRequest(t, rail, e.tokenModel, 5, 1)
	pending, err := rail.Claim(ctx, e.retryClaimInput(second.ID))
	if err != nil {
		t.Fatal(err)
	}
	third := e.billingRequest(t, rail, e.tokenModel, 5, 1)
	observation := contract.ResponseObservation{HasRetryAfter: true, Status: 503, ObservedAt: charityTestNow + 30, RateLimitedUntil: charityTestNow + 90}
	if err := rail.ObserveResponseHeaders(ctx, active, observation); err != nil {
		t.Fatal(err)
	}
	if _, err := rail.Claim(ctx, e.retryClaimInput(third.ID)); !errors.Is(err, claim.ErrKeyRateLimited) {
		t.Fatalf("new claim=%v", err)
	}
	if _, err := rail.TakeForDispatch(ctx, pending); !errors.Is(err, claim.ErrKeyRateLimited) {
		t.Fatalf("pending dispatch=%v", err)
	}
	var state string
	if err := e.store.DB().QueryRow("SELECT state FROM dispatch_claims WHERE id=?", pending.ClaimID()).Scan(&state); err != nil || state != "claimed" {
		t.Fatal(state, err)
	}
	// The same physical key remains usable by its owner.
	personal, err := rail.Accept(ctx, claim.AcceptInput{UserID: e.donorID, Route: claim.RouteOpenAIChat, ModelSnapshot: "private", AttemptLimit: 1})
	if err != nil {
		t.Fatal(err)
	}
	ownInput := e.retryClaimInput(personal.ID)
	ownInput.ActorUserID = e.donorID
	ownInput.Purpose = claim.PurposeSelf
	ownInput.DonationKeyID = 0
	own, err := rail.Claim(ctx, ownInput)
	if err != nil {
		t.Fatal(err)
	}
	grant, err := rail.TakeForDispatch(ctx, own)
	if err != nil {
		t.Fatal(err)
	}
	grant.Clear()
	if err := rail.ObserveResponseHeaders(ctx, own, observation); !errors.Is(err, claim.ErrInvalidInput) {
		t.Fatalf("personal observer=%v", err)
	}
	// Already dispatched work completes normally; the response did not start billing.
	outcome := claim.AttemptOutcome{Kind: claim.ResultResponse, UpstreamStatus: 503, StreakDisposition: contract.StreakUpstreamFailure, FailureOrigin: contract.OriginUpstreamResponse}
	for range 2 {
		if _, err := rail.CompleteAttempt(ctx, active, outcome); err != nil {
			t.Fatal(err)
		}
	}
	var starts, price int
	if err := e.store.DB().QueryRow("SELECT count(*) FROM dispatch_response_starts WHERE claim_id=?", active.ClaimID()).Scan(&starts); err != nil {
		t.Fatal(err)
	}
	if err := e.store.DB().QueryRow("SELECT price_actual_milli FROM donation_usage_reservations WHERE claim_id=?", active.ClaimID()).Scan(&price); err != nil {
		t.Fatal(err)
	}
	if starts != 0 || price != 0 {
		t.Fatal("retry response charged", starts, price)
	}
	if _, err := rail.ReleaseUndispatched(ctx, pending); err != nil {
		t.Fatal(err)
	}
}

func TestRetryAfterPreservesFailureOriginAndBillingThroughRecovery(t *testing.T) {
	for _, status := range []int{200, 401, 429, 503} {
		for _, recovery := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d/recovery=%t", status, recovery), func(t *testing.T) {
				e := newCharityTestEnv(t)
				rail := e.billingRail(t)
				ctx := context.Background()
				if _, err := e.store.DB().Exec("UPDATE donation_keys SET failure_streak=?,failure_disable_threshold=3 WHERE id=?", u128Blob(t, 2), e.donationKey); err != nil {
					t.Fatal(err)
				}
				request := e.billingRequest(t, rail, e.requestModel, 2400, 1)
				handle := e.billingDispatch(t, rail, request.ID, 1)
				// An expired hint still proves this response must not count as a new failure.
				if err := rail.ObserveResponseHeaders(ctx, handle, contract.ResponseObservation{HasRetryAfter: true, Status: status, ObservedAt: charityTestNow + 30, RateLimitedUntil: charityTestNow + 30}); err != nil {
					t.Fatal(err)
				}
				origin := contract.OriginUpstreamResponse
				if status == 200 {
					origin = contract.OriginUpstreamProtocol
				}
				if recovery {
					if _, err := rail.RecoverNonterminal(ctx, 100); err != nil {
						t.Fatal(err)
					}
				} else {
					outcome := claim.AttemptOutcome{Kind: claim.ResultResponse, UpstreamStatus: status, StreakDisposition: contract.StreakUpstreamFailure, FailureOrigin: origin}
					for range 2 {
						if _, err := rail.CompleteAttempt(ctx, handle, outcome); err != nil {
							t.Fatal(err)
						}
					}
				}
				var streak []byte
				var disabled bool
				if err := e.store.DB().QueryRow("SELECT failure_streak,failure_disabled FROM donation_keys WHERE id=?", e.donationKey).Scan(&streak, &disabled); err != nil {
					t.Fatal(err)
				}
				want := int64(2)
				if status == 200 {
					want = 0
				}
				assertU128(t, streak, want, "header outcome")
				if disabled {
					t.Fatal("retry hint disabled key")
				}
				var disposition, storedOrigin string
				var price, starts, cooldowns int
				if err := e.store.DB().QueryRow("SELECT streak_disposition,failure_origin,price_actual_milli FROM donation_usage_reservations WHERE claim_id=?", handle.ClaimID()).Scan(&disposition, &storedOrigin, &price); err != nil {
					t.Fatal(err)
				}
				wantDisposition := "neutral"
				if status == 200 {
					wantDisposition = "success"
				}
				if recovery {
					origin = contract.OriginRecoveryUnknown
				}
				if disposition != wantDisposition || storedOrigin != string(origin) || price != 0 {
					t.Fatal(disposition, storedOrigin, price)
				}
				if err := e.store.DB().QueryRow("SELECT count(*) FROM dispatch_response_starts WHERE claim_id=?", handle.ClaimID()).Scan(&starts); err != nil {
					t.Fatal(err)
				}
				if err := e.store.DB().QueryRow("SELECT count(*) FROM donation_key_cooldowns").Scan(&cooldowns); err != nil {
					t.Fatal(err)
				}
				if starts != 0 || cooldowns != 0 {
					t.Fatal("expired/nonstream hint created billing or active cooldown", starts, cooldowns)
				}
			})
		}
	}
}

func TestRetryAfterConcurrentObservationsOnlyExtendAndReplayDoesNotRestart(t *testing.T) {
	e := newCharityTestEnv(t)
	rail := e.billingRail(t)
	ctx := context.Background()
	handles := make([]claim.Handle, 3)
	for i := range handles {
		request := e.billingRequest(t, rail, e.tokenModel, 5, 1)
		handles[i] = e.billingDispatch(t, rail, request.ID, 1)
	}
	var wg sync.WaitGroup
	errs := make(chan error, len(handles))
	for i, handle := range handles {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- rail.ObserveResponseHeaders(ctx, handle, contract.ResponseObservation{HasRetryAfter: true, Status: 503, ObservedAt: charityTestNow + 30, RateLimitedUntil: charityTestNow + int64(60+i*60)})
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := rail.ObserveResponseHeaders(ctx, handles[0], contract.ResponseObservation{HasRetryAfter: true, Status: 200, Stream: true, ObservedAt: charityTestNow + 40, RateLimitedUntil: charityTestNow + 999}); err != nil {
		t.Fatal(err)
	}
	var until int64
	var starts int
	if err := e.store.DB().QueryRow("SELECT rate_limited_until FROM donation_key_cooldowns WHERE donation_key_id=?", e.donationKey).Scan(&until); err != nil || until != charityTestNow+180 {
		t.Fatal(until, err)
	}
	if err := e.store.DB().QueryRow("SELECT count(*) FROM dispatch_response_starts").Scan(&starts); err != nil || starts != 0 {
		t.Fatal("replay changed billing", starts, err)
	}
}

func TestRetryAfterDoesNotExemptAnUnobservedFailure(t *testing.T) {
	e := newCharityTestEnv(t)
	rail := e.billingRail(t)
	ctx := context.Background()
	if _, err := e.store.DB().Exec("UPDATE donation_keys SET failure_streak=?,failure_disable_threshold=3 WHERE id=?", u128Blob(t, 2), e.donationKey); err != nil {
		t.Fatal(err)
	}
	request := e.billingRequest(t, rail, e.requestModel, 2400, 1)
	handle := e.billingDispatch(t, rail, request.ID, 1)
	outcome := claim.AttemptOutcome{Kind: claim.ResultResponse, UpstreamStatus: 503, StreakDisposition: contract.StreakNeutral, FailureOrigin: contract.OriginUpstreamResponse}
	if _, err := rail.CompleteAttempt(ctx, handle, outcome); !errors.Is(err, claim.ErrInvalidInput) {
		t.Fatalf("unproven neutral=%v", err)
	}
	outcome.StreakDisposition = contract.StreakUpstreamFailure
	if _, err := rail.CompleteAttempt(ctx, handle, outcome); err != nil {
		t.Fatal(err)
	}
	var streak []byte
	var disabled bool
	if err := e.store.DB().QueryRow("SELECT failure_streak,failure_disabled FROM donation_keys WHERE id=?", e.donationKey).Scan(&streak, &disabled); err != nil {
		t.Fatal(err)
	}
	assertU128(t, streak, 3, "ordinary failure")
	if !disabled {
		t.Fatal("ordinary failure no longer disables key")
	}
}

func TestRetryAfterDeletedKeyCannotBeResurrectedAndFactsCascade(t *testing.T) {
	e := newCharityTestEnv(t)
	rail := e.billingRail(t)
	ctx := context.Background()
	first := e.billingRequest(t, rail, e.tokenModel, 5, 1)
	a := e.billingDispatch(t, rail, first.ID, 1)
	second := e.billingRequest(t, rail, e.tokenModel, 5, 1)
	b := e.billingDispatch(t, rail, second.ID, 1)
	observation := contract.ResponseObservation{HasRetryAfter: true, Status: 503, ObservedAt: charityTestNow + 30, RateLimitedUntil: charityTestNow + 90}
	if err := rail.ObserveResponseHeaders(ctx, a, observation); err != nil {
		t.Fatal(err)
	}
	tx := beginTestTx(t, e.store.DB())
	if err := e.donation.PrepareAccountDeletion(ctx, tx, e.donorID, charityTestNow+31); err != nil {
		t.Fatal(err)
	}
	commitTestTx(t, tx)
	observation.RateLimitedUntil = charityTestNow + 999
	if err := rail.ObserveResponseHeaders(ctx, b, observation); err != nil {
		t.Fatal(err)
	}
	var cooldowns, receipts int
	var until int64
	if err := e.store.DB().QueryRow("SELECT rate_limited_until FROM donation_key_cooldowns WHERE donation_key_id=?", e.donationKey).Scan(&until); err != nil || until != charityTestNow+90 {
		t.Fatal("ended key cooldown extended", until, err)
	}
	for _, handle := range []claim.Handle{a, b} {
		if _, err := rail.CompleteAttempt(ctx, handle, claim.AttemptOutcome{Kind: claim.ResultResponse, UpstreamStatus: 503, StreakDisposition: contract.StreakUpstreamFailure, FailureOrigin: contract.OriginUpstreamResponse}); err != nil {
			t.Fatal(err)
		}
		if _, err := e.store.DB().Exec("DELETE FROM dispatch_claims WHERE id=?", handle.ClaimID()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.store.DB().Exec("DELETE FROM donation_keys WHERE id=?", e.donationKey); err != nil {
		t.Fatal(err)
	}
	if err := e.store.DB().QueryRow("SELECT count(*) FROM donation_key_cooldowns").Scan(&cooldowns); err != nil || cooldowns != 0 {
		t.Fatal(cooldowns, err)
	}
	if err := e.store.DB().QueryRow("SELECT count(*) FROM dispatch_retry_after_observations").Scan(&receipts); err != nil || receipts != 0 {
		t.Fatal("orphan receipt", receipts, err)
	}
}

func TestRetryAfterStreamAcceptanceRetainsBillingAndFailureReset(t *testing.T) {
	e := newCharityTestEnv(t)
	rail := e.billingRail(t)
	ctx := context.Background()
	if _, err := e.store.DB().Exec("UPDATE donation_keys SET failure_streak=? WHERE id=?", u128Blob(t, 2), e.donationKey); err != nil {
		t.Fatal(err)
	}
	request := e.billingRequest(t, rail, e.requestModel, 2400, 1)
	handle := e.billingDispatch(t, rail, request.ID, 1)
	for range 2 {
		if err := rail.ObserveResponseHeaders(ctx, handle, contract.ResponseObservation{HasRetryAfter: true, Status: 200, Stream: true, ObservedAt: charityTestNow + 30, RateLimitedUntil: charityTestNow + 90}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := rail.RecoverNonterminal(ctx, 100); err != nil {
		t.Fatal(err)
	}
	e.billingBalance(t, e.callerID, 7600)
	var streak []byte
	var calls int
	var until int64
	if err := e.store.DB().QueryRow("SELECT failure_streak FROM donation_keys WHERE id=?", e.donationKey).Scan(&streak); err != nil {
		t.Fatal(err)
	}
	assertU128(t, streak, 0, "accepted stream after restart")
	if err := e.store.DB().QueryRow("SELECT calls_actual FROM donation_usage_reservations WHERE claim_id=?", handle.ClaimID()).Scan(&calls); err != nil || calls != 1 {
		t.Fatal(calls, err)
	}
	if err := e.store.DB().QueryRow("SELECT rate_limited_until FROM donation_key_cooldowns WHERE donation_key_id=?", e.donationKey).Scan(&until); err != nil || until != charityTestNow+90 {
		t.Fatal(until, err)
	}
}

func TestRetryAfterFinalAdmissionRecoversAtExpiry(t *testing.T) {
	e := newCharityTestEnv(t)
	rail := e.billingRail(t)
	ctx := context.Background()
	first := e.billingRequest(t, rail, e.tokenModel, 5, 1)
	active := e.billingDispatch(t, rail, first.ID, 1)
	second := e.billingRequest(t, rail, e.tokenModel, 5, 1)
	third := e.billingRequest(t, rail, e.tokenModel, 5, 1)
	if err := rail.ObserveResponseHeaders(ctx, active, contract.ResponseObservation{HasRetryAfter: true, Status: 503, ObservedAt: charityTestNow + 30, RateLimitedUntil: charityTestNow + 90}); err != nil {
		t.Fatal(err)
	}
	if _, err := rail.Claim(ctx, e.retryClaimInput(second.ID)); !errors.Is(err, claim.ErrKeyRateLimited) {
		t.Fatal(err)
	}
	vault, err := secret.New(bytes.Repeat([]byte{charityTestMasterKeyByte}, secret.MasterKeyBytes))
	if err != nil {
		t.Fatal(err)
	}
	defer vault.Close()
	after, err := claim.New(claim.Dependencies{DB: e.store.DB(), Secrets: vault, Charity: e.service, Accounting: claim.NewLedgerAccounting(), Acceptance: billingAcceptance{}, Now: func() time.Time { return time.Unix(charityTestNow+90, 0) }})
	if err != nil {
		t.Fatal(err)
	}
	handle, err := after.Claim(ctx, e.retryClaimInput(second.ID))
	if err != nil {
		t.Fatal("expired key not admitted", err)
	}
	grant, err := after.TakeForDispatch(ctx, handle)
	if err != nil {
		t.Fatal(err)
	}
	grant.Clear()
	if _, err := e.store.DB().Exec("UPDATE donation_keys SET enabled=0 WHERE id=?", e.donationKey); err != nil {
		t.Fatal(err)
	}
	if _, err := after.Claim(ctx, e.retryClaimInput(third.ID)); !errors.Is(err, claim.ErrNotFound) {
		t.Fatal("expiry revived disabled key", err)
	}
}
