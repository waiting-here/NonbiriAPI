package db

// Charity reservation repository tests: the frozen §5.2/§5.3/§5.4 state
// machine — atomic creation (key cap reserve + user debit + INSERT in one
// transaction), insufficient-credits refusal before dispatch, the three legal
// CAS transitions and their illegal shapes, settlement under actual and
// unknown usage, donor reward on both balances, donor-deleted reward
// abandonment, key cap overshoot settling past the cap, candidate swap
// atomicity, crash recovery per state, and account-deletion convergence.

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/credits"
)

func newCharityReservationTestStore(t *testing.T) *Store {
	t.Helper()
	st := openTestStore(t, filepath.Join(t.TempDir(), "charity-res.db"))
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// seedCharityRoute builds one fully-routable [公益] model backed by a single
// approved, enabled donation whose key is claimed against a live endpoint with
// a fetched upstream model. It returns the donor, the consumer (funded with
// creditsCents milli-credits), the model, and the donation key id.
func seedCharityRoute(t *testing.T, st *Store, pricingMode string, creditsCents int64) (donorID, consumerID, modelID, donationKeyID int64) {
	t.Helper()
	ctx := context.Background()
	enableCharity(t, st)
	donorID = newDonationUser(t, st, "donor")
	ep := newDonationEndpoint(t, st, donorID, "https://charity.example.com")
	key := newDonationKey(t, st, donorID, ep.ID, "sk-charity")
	if err := st.ReplaceFetchedModels(ctx, donorID, ep.ID, key.ID, []FetchedModel{{UpstreamModelID: "up/charity-model", Provider: "donor"}}, 10); err != nil {
		t.Fatalf("ReplaceFetchedModels: %v", err)
	}
	in := existingInput(donorID, ep.ID, []int64{key.ID}, 40)
	d, err := st.CreateDonation(ctx, in)
	if err != nil {
		t.Fatalf("CreateDonation: %v", err)
	}
	if _, err := st.ApplyDonationReview(ctx, ReviewDecision{
		DonationID: d.ID, Role: ReviewRoleAdmin, ReviewerID: donorID, Action: ReviewActionApprove, Now: 60,
	}); err != nil {
		t.Fatalf("approve donation: %v", err)
	}
	donationKeyID = donationKeyIDForTest(t, st, d.ID)
	m := CharityModel{
		Provider: "donor", Model: "charity-model", Enabled: true,
		PricingMode:      pricingMode,
		RequestUserPrice: 500, RequestDonorReward: 100,
		UncachedUserPrice: 1_000_000, CacheWriteUserPrice: 1_000_000, CacheReadUserPrice: 1_000_000, OutputUserPrice: 1_000_000,
		UncachedDonorReward: 200_000, CacheWriteDonorReward: 200_000, CacheReadDonorReward: 200_000, OutputDonorReward: 200_000,
	}
	if pricingMode == CharityPricingPerToken {
		setTokenReserveConfig(t, st, "1000")
	}
	created, err := st.CreateCharityModel(ctx, m, 0, 70)
	if err != nil {
		t.Fatalf("CreateCharityModel: %v", err)
	}
	modelID = created.ID
	if _, err := st.CreateCharityBinding(ctx, modelID, donationKeyID, "up/charity-model", 0, 80); err != nil {
		t.Fatalf("CreateCharityBinding: %v", err)
	}
	consumerID = newDonationUser(t, st, "consumer")
	setCheckinCredits(t, st, consumerID, creditsCents)
	return donorID, consumerID, modelID, donationKeyID
}

func enableCharity(t *testing.T, st *Store) {
	t.Helper()
	setCheckinConfig(t, st, "charity_enabled", "1")
}

func resolveFirstCandidate(t *testing.T, st *Store, fullName string) CharityCandidate {
	t.Helper()
	route, err := st.ResolveCharityRoute(context.Background(), fullName, 1000, 16)
	if err != nil {
		t.Fatalf("ResolveCharityRoute: %v", err)
	}
	if len(route.Candidates) == 0 {
		t.Fatalf("no candidates for %q", fullName)
	}
	return route.Candidates[0]
}

func TestCharityReservationCreatePerRequestSnapshotAndDebit(t *testing.T) {
	st := newCharityReservationTestStore(t)
	ctx := context.Background()
	_, consumerID, modelID, keyID := seedCharityRoute(t, st, CharityPricingPerRequest, 10_000)

	cand := resolveFirstCandidate(t, st, "[公益]donor/charity-model")
	if cand.DonationKeyID != keyID {
		t.Fatalf("candidate key = %d, want %d", cand.DonationKeyID, keyID)
	}
	res, snap, err := st.CreateCharityReservation(ctx, ReserveCharityInput{
		UserID: consumerID, FullName: "[公益]donor/charity-model",
		BindingID: cand.BindingID, DonationKeyID: cand.DonationKeyID,
		AttemptID: "attempt-1", BaseURL: cand.BaseURL, Now: 100,
	})
	if err != nil {
		t.Fatalf("CreateCharityReservation: %v", err)
	}
	if res.State != "reserved" {
		t.Fatalf("state = %q, want reserved", res.State)
	}
	if snap.PricingMode != CharityPricingPerRequest {
		t.Fatalf("snapshot mode = %q", snap.PricingMode)
	}
	if res.UserReserved != 500 || res.KeyReserved != 500 {
		t.Fatalf("reserves = %d/%d, want 500/500 (no discount)", res.UserReserved, res.KeyReserved)
	}
	if res.CharityModelID == nil || *res.CharityModelID != modelID {
		t.Fatalf("model snapshot = %v", res.CharityModelID)
	}
	// The user was debited the reserve; the donation key reserved counter rose.
	var got int64
	st.DB().QueryRow(`SELECT credits FROM users WHERE id=?`, consumerID).Scan(&got)
	if got != 10_000-500 {
		t.Fatalf("consumer credits = %d, want %d", got, 10_000-500)
	}
	var reserved int64
	st.DB().QueryRow(`SELECT credits_reserved FROM donation_keys WHERE id=?`, keyID).Scan(&reserved)
	if reserved != 500 {
		t.Fatalf("key reserved = %d, want 500", reserved)
	}
}

func TestCharityReservationInsufficientCreditsRefusesBeforeDispatch(t *testing.T) {
	st := newCharityReservationTestStore(t)
	ctx := context.Background()
	_, consumerID, _, _ := seedCharityRoute(t, st, CharityPricingPerRequest, 100) // 100 < 500 reserve

	cand := resolveFirstCandidate(t, st, "[公益]donor/charity-model")
	_, _, err := st.CreateCharityReservation(ctx, ReserveCharityInput{
		UserID: consumerID, FullName: "[公益]donor/charity-model",
		BindingID: cand.BindingID, DonationKeyID: cand.DonationKeyID,
		AttemptID: "attempt-poor", BaseURL: cand.BaseURL, Now: 100,
	})
	if !errors.Is(err, ErrInsufficientCredits) {
		t.Fatalf("insufficient = %v, want ErrInsufficientCredits", err)
	}
	// Nothing was written: no reservation, no debit, no key reserve.
	var n int64
	st.DB().QueryRow(`SELECT COUNT(*) FROM charity_reservations`).Scan(&n)
	if n != 0 {
		t.Fatalf("reservations = %d, want 0", n)
	}
	var got int64
	st.DB().QueryRow(`SELECT credits FROM users WHERE id=?`, consumerID).Scan(&got)
	if got != 100 {
		t.Fatalf("consumer credits = %d, want 100 (unchanged)", got)
	}
}

func TestCharityReservationDisabledRefuses(t *testing.T) {
	st := newCharityReservationTestStore(t)
	ctx := context.Background()
	_, consumerID, _, _ := seedCharityRoute(t, st, CharityPricingPerRequest, 10_000)
	setCheckinConfig(t, st, "charity_enabled", "0")

	cand := resolveFirstCandidate(t, st, "[公益]donor/charity-model")
	_, _, err := st.CreateCharityReservation(ctx, ReserveCharityInput{
		UserID: consumerID, FullName: "[公益]donor/charity-model",
		BindingID: cand.BindingID, DonationKeyID: cand.DonationKeyID,
		AttemptID: "attempt-off", BaseURL: cand.BaseURL, Now: 100,
	})
	if !errors.Is(err, ErrCharityDisabled) {
		t.Fatalf("disabled = %v, want ErrCharityDisabled", err)
	}
}

func TestCharityReservationDispatchedCommittedSettlement(t *testing.T) {
	st := newCharityReservationTestStore(t)
	ctx := context.Background()
	donorID, consumerID, modelID, keyID := seedCharityRoute(t, st, CharityPricingPerRequest, 10_000)

	cand := resolveFirstCandidate(t, st, "[公益]donor/charity-model")
	res, _, err := st.CreateCharityReservation(ctx, ReserveCharityInput{
		UserID: consumerID, FullName: "[公益]donor/charity-model",
		BindingID: cand.BindingID, DonationKeyID: cand.DonationKeyID,
		AttemptID: "attempt-commit", BaseURL: cand.BaseURL, Now: 100,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if applied, err := st.DispatchCharityReservation(ctx, res.ID, 110); err != nil || !applied {
		t.Fatalf("dispatch applied=%v err=%v", applied, err)
	}
	// Per-request: original=request price, user charge=same (no discount), reward=100.
	plan := CommitPlan{OriginalCharge: 500, UserCharge: 500, DonorReward: 100}
	committed, err := st.CommitCharityReservation(ctx, res.ID, plan, 120)
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if committed.State != "committed" {
		t.Fatalf("state = %q", committed.State)
	}
	// User settled to the actual charge (reserve == actual → net 0 delta).
	var got int64
	st.DB().QueryRow(`SELECT credits FROM users WHERE id=?`, consumerID).Scan(&got)
	if got != 10_000-500 {
		t.Fatalf("consumer credits = %d, want %d", got, 10_000-500)
	}
	// Donor reward added to BOTH credits and donation_credit.
	var donorCredits int64
	st.DB().QueryRow(`SELECT credits FROM users WHERE id=?`, donorID).Scan(&donorCredits)
	var donorDonation int64
	st.DB().QueryRow(`SELECT donation_credit FROM users WHERE id=?`, donorID).Scan(&donorDonation)
	if donorCredits != 100 || donorDonation != 100 {
		t.Fatalf("donor reward = %d/%d, want 100/100", donorCredits, donorDonation)
	}
	// Key reserved→used at original charge.
	var used, reserved int64
	st.DB().QueryRow(`SELECT credits_used, credits_reserved FROM donation_keys WHERE id=?`, keyID).Scan(&used, &reserved)
	if used != 500 || reserved != 0 {
		t.Fatalf("key used/reserved = %d/%d, want 500/0", used, reserved)
	}
	_ = modelID
}

func TestCharityReservationActualExceedsReserveDrivesNegative(t *testing.T) {
	st := newCharityReservationTestStore(t)
	ctx := context.Background()
	_, consumerID, _, _ := seedCharityRoute(t, st, CharityPricingPerRequest, 10_000)

	cand := resolveFirstCandidate(t, st, "[公益]donor/charity-model")
	res, _, _ := st.CreateCharityReservation(ctx, ReserveCharityInput{
		UserID: consumerID, FullName: "[公益]donor/charity-model",
		BindingID: cand.BindingID, DonationKeyID: cand.DonationKeyID,
		AttemptID: "attempt-over", BaseURL: cand.BaseURL, Now: 100,
	})
	st.DispatchCharityReservation(ctx, res.ID, 110)
	// Actual charge exceeds the reserve: user balance goes negative, key
	// used crosses its cap (frozen §5.3).
	plan := CommitPlan{OriginalCharge: 50_000, UserCharge: 50_000, DonorReward: 0}
	if _, err := st.CommitCharityReservation(ctx, res.ID, plan, 120); err != nil {
		t.Fatalf("commit: %v", err)
	}
	var got int64
	st.DB().QueryRow(`SELECT credits FROM users WHERE id=?`, consumerID).Scan(&got)
	// reserve 500 was debited; actual 50_000 → net delta = reserve - actual = -49500 → 10000-50000 = -40000
	if got != -40_000 {
		t.Fatalf("consumer credits = %d, want -40000 (negative allowed)", got)
	}
}

func TestCharityReservationUnknownUsageCommitsReserveRewardZero(t *testing.T) {
	st := newCharityReservationTestStore(t)
	ctx := context.Background()
	donorID, consumerID, _, _ := seedCharityRoute(t, st, CharityPricingPerRequest, 10_000)

	cand := resolveFirstCandidate(t, st, "[公益]donor/charity-model")
	res, _, _ := st.CreateCharityReservation(ctx, ReserveCharityInput{
		UserID: consumerID, FullName: "[公益]donor/charity-model",
		BindingID: cand.BindingID, DonationKeyID: cand.DonationKeyID,
		AttemptID: "attempt-unknown", BaseURL: cand.BaseURL, Now: 100,
	})
	st.DispatchCharityReservation(ctx, res.ID, 110)
	plan := CommitPlan{OriginalCharge: res.KeyReserved, UserCharge: res.UserReserved, DonorReward: 0, UsageUnknown: true}
	if _, err := st.CommitCharityReservation(ctx, res.ID, plan, 120); err != nil {
		t.Fatalf("commit: %v", err)
	}
	// User pays the discounted reserve; donor reward is 0.
	var got int64
	st.DB().QueryRow(`SELECT credits FROM users WHERE id=?`, consumerID).Scan(&got)
	if got != 10_000-500 {
		t.Fatalf("consumer credits = %d, want %d", got, 10_000-500)
	}
	var donorCredits int64
	st.DB().QueryRow(`SELECT credits FROM users WHERE id=?`, donorID).Scan(&donorCredits)
	if donorCredits != 0 {
		t.Fatalf("donor reward = %d, want 0 (unknown usage)", donorCredits)
	}
}

func TestCharityReservationRepeatedCommitIsNoOp(t *testing.T) {
	st := newCharityReservationTestStore(t)
	ctx := context.Background()
	_, consumerID, _, _ := seedCharityRoute(t, st, CharityPricingPerRequest, 10_000)
	cand := resolveFirstCandidate(t, st, "[公益]donor/charity-model")
	res, _, _ := st.CreateCharityReservation(ctx, ReserveCharityInput{
		UserID: consumerID, FullName: "[公益]donor/charity-model",
		BindingID: cand.BindingID, DonationKeyID: cand.DonationKeyID,
		AttemptID: "attempt-replay", BaseURL: cand.BaseURL, Now: 100,
	})
	st.DispatchCharityReservation(ctx, res.ID, 110)
	plan := CommitPlan{OriginalCharge: 500, UserCharge: 500, DonorReward: 100}
	if _, err := st.CommitCharityReservation(ctx, res.ID, plan, 120); err != nil {
		t.Fatal(err)
	}
	// Replay with different values: the first result wins; nothing re-applies.
	replay := CommitPlan{OriginalCharge: 999, UserCharge: 999, DonorReward: 999}
	committed, err := st.CommitCharityReservation(ctx, res.ID, replay, 130)
	if err != nil {
		t.Fatalf("replay commit: %v", err)
	}
	if committed.OriginalCharge != 500 || committed.UserCharge != 500 || committed.DonorReward != 100 {
		t.Fatalf("replay returned %d/%d/%d, want first result 500/500/100", committed.OriginalCharge, committed.UserCharge, committed.DonorReward)
	}
}

func TestCharityReservationIllegalTransitions(t *testing.T) {
	st := newCharityReservationTestStore(t)
	ctx := context.Background()
	_, consumerID, _, _ := seedCharityRoute(t, st, CharityPricingPerRequest, 10_000)
	cand := resolveFirstCandidate(t, st, "[公益]donor/charity-model")
	res, _, _ := st.CreateCharityReservation(ctx, ReserveCharityInput{
		UserID: consumerID, FullName: "[公益]donor/charity-model",
		BindingID: cand.BindingID, DonationKeyID: cand.DonationKeyID,
		AttemptID: "attempt-illegal", BaseURL: cand.BaseURL, Now: 100,
	})
	// reserved → committed is illegal (must dispatch first).
	if _, err := st.CommitCharityReservation(ctx, res.ID, CommitPlan{OriginalCharge: 500, UserCharge: 500}, 110); !errors.Is(err, credits.ErrIllegalTransition) {
		t.Fatalf("reserved→committed = %v, want illegal transition", err)
	}
	// dispatch then attempt release: dispatched must never be released.
	st.DispatchCharityReservation(ctx, res.ID, 110)
	if _, err := st.ReleaseCharityReservation(ctx, res.ID, 120); !errors.Is(err, credits.ErrIllegalTransition) {
		t.Fatalf("dispatched→released = %v, want illegal transition", err)
	}
}

func TestCharityReservationReleaseRefunds(t *testing.T) {
	st := newCharityReservationTestStore(t)
	ctx := context.Background()
	_, consumerID, _, keyID := seedCharityRoute(t, st, CharityPricingPerRequest, 10_000)
	cand := resolveFirstCandidate(t, st, "[公益]donor/charity-model")
	res, _, _ := st.CreateCharityReservation(ctx, ReserveCharityInput{
		UserID: consumerID, FullName: "[公益]donor/charity-model",
		BindingID: cand.BindingID, DonationKeyID: cand.DonationKeyID,
		AttemptID: "attempt-release", BaseURL: cand.BaseURL, Now: 100,
	})
	if applied, err := st.ReleaseCharityReservation(ctx, res.ID, 110); err != nil || !applied {
		t.Fatalf("release applied=%v err=%v", applied, err)
	}
	var got int64
	st.DB().QueryRow(`SELECT credits FROM users WHERE id=?`, consumerID).Scan(&got)
	if got != 10_000 {
		t.Fatalf("consumer credits = %d, want refunded to %d", got, 10_000)
	}
	var reserved int64
	st.DB().QueryRow(`SELECT credits_reserved FROM donation_keys WHERE id=?`, keyID).Scan(&reserved)
	if reserved != 0 {
		t.Fatalf("key reserved = %d, want 0", reserved)
	}
	// Repeated release is an idempotent no-op (already terminal).
	if applied, _ := st.ReleaseCharityReservation(ctx, res.ID, 120); applied {
		t.Fatalf("repeated release applied=true, want false")
	}
}

func TestCharityReservationDonorDeletedRewardAbandoned(t *testing.T) {
	st := newCharityReservationTestStore(t)
	ctx := context.Background()
	donorID, consumerID, _, _ := seedCharityRoute(t, st, CharityPricingPerRequest, 10_000)
	cand := resolveFirstCandidate(t, st, "[公益]donor/charity-model")
	res, _, _ := st.CreateCharityReservation(ctx, ReserveCharityInput{
		UserID: consumerID, FullName: "[公益]donor/charity-model",
		BindingID: cand.BindingID, DonationKeyID: cand.DonationKeyID,
		AttemptID: "attempt-deaddonor", BaseURL: cand.BaseURL, Now: 100,
	})
	st.DispatchCharityReservation(ctx, res.ID, 110)
	// Delete the donor account BEFORE settlement. The reservation's donor FK
	// is SET NULL; a late reward must not resurrect the donor.
	if err := st.DeleteUserAccount(ctx, donorID); err != nil {
		t.Fatalf("delete donor: %v", err)
	}
	plan := CommitPlan{OriginalCharge: 500, UserCharge: 500, DonorReward: 100}
	if _, err := st.CommitCharityReservation(ctx, res.ID, plan, 120); err != nil {
		t.Fatalf("commit with deleted donor: %v", err)
	}
	// The donor row is gone; the consumer settlement is final.
	var donorCount int64
	st.DB().QueryRow(`SELECT COUNT(*) FROM users WHERE id=?`, donorID).Scan(&donorCount)
	if donorCount != 0 {
		t.Fatalf("donor resurrected: count=%d", donorCount)
	}
	reloaded, _ := st.GetCharityReservation(ctx, res.ID)
	if reloaded.DonorUserID != nil {
		t.Fatalf("donor FK = %v, want NULL after donor delete", reloaded.DonorUserID)
	}
}

func TestCharityReservationRecoverReservedReleasesAndDispatchedCommitsUnknown(t *testing.T) {
	st := newCharityReservationTestStore(t)
	ctx := context.Background()
	_, consumerID, _, _ := seedCharityRoute(t, st, CharityPricingPerRequest, 10_000)
	cand := resolveFirstCandidate(t, st, "[公益]donor/charity-model")

	// A stalled RESERVED row recovers to RELEASED (refund).
	res1, _, _ := st.CreateCharityReservation(ctx, ReserveCharityInput{
		UserID: consumerID, FullName: "[公益]donor/charity-model",
		BindingID: cand.BindingID, DonationKeyID: cand.DonationKeyID,
		AttemptID: "attempt-stall-reserved", BaseURL: cand.BaseURL, Now: 100,
	})
	if applied, err := st.RecoverCharityReservation(ctx, res1.ID, 200); err != nil || !applied {
		t.Fatalf("recover reserved applied=%v err=%v", applied, err)
	}
	reloaded, _ := st.GetCharityReservation(ctx, res1.ID)
	if reloaded.State != "released" {
		t.Fatalf("recover reserved → %q, want released", reloaded.State)
	}
	var got int64
	st.DB().QueryRow(`SELECT credits FROM users WHERE id=?`, consumerID).Scan(&got)
	if got != 10_000 {
		t.Fatalf("recover reserved refund = %d, want %d", got, 10_000)
	}

	// A stalled DISPATCHED row recovers to COMMITTED unknown.
	res2, _, _ := st.CreateCharityReservation(ctx, ReserveCharityInput{
		UserID: consumerID, FullName: "[公益]donor/charity-model",
		BindingID: cand.BindingID, DonationKeyID: cand.DonationKeyID,
		AttemptID: "attempt-stall-dispatched", BaseURL: cand.BaseURL, Now: 300,
	})
	st.DispatchCharityReservation(ctx, res2.ID, 310)
	if applied, err := st.RecoverCharityReservation(ctx, res2.ID, 400); err != nil || !applied {
		t.Fatalf("recover dispatched applied=%v err=%v", applied, err)
	}
	reloaded2, _ := st.GetCharityReservation(ctx, res2.ID)
	if reloaded2.State != "committed" || !reloaded2.UsageUnknown {
		t.Fatalf("recover dispatched → %q unknown=%v, want committed/unknown", reloaded2.State, reloaded2.UsageUnknown)
	}
	// Recovery is idempotent: a second recover is a no-op.
	if applied, _ := st.RecoverCharityReservation(ctx, res2.ID, 500); applied {
		t.Fatalf("recover terminal applied=true, want false")
	}
}

func TestCharityReservationConsumerDeleteConverges(t *testing.T) {
	st := newCharityReservationTestStore(t)
	ctx := context.Background()
	_, consumerID, _, keyID := seedCharityRoute(t, st, CharityPricingPerRequest, 10_000)
	cand := resolveFirstCandidate(t, st, "[公益]donor/charity-model")
	// One reserved and one dispatched reservation for the consumer, both
	// holding the same donation key's reserve (per-request 500 each).
	res1, _, _ := st.CreateCharityReservation(ctx, ReserveCharityInput{
		UserID: consumerID, FullName: "[公益]donor/charity-model",
		BindingID: cand.BindingID, DonationKeyID: cand.DonationKeyID,
		AttemptID: "attempt-del-reserved", BaseURL: cand.BaseURL, Now: 100,
	})
	res2, _, _ := st.CreateCharityReservation(ctx, ReserveCharityInput{
		UserID: consumerID, FullName: "[公益]donor/charity-model",
		BindingID: cand.BindingID, DonationKeyID: cand.DonationKeyID,
		AttemptID: "attempt-del-dispatched", BaseURL: cand.BaseURL, Now: 110,
	})
	st.DispatchCharityReservation(ctx, res2.ID, 120)
	// Before delete the key holds both reserves (1000).
	var reservedBefore int64
	st.DB().QueryRow(`SELECT credits_reserved FROM donation_keys WHERE id=?`, keyID).Scan(&reservedBefore)
	if reservedBefore != 1000 {
		t.Fatalf("key reserved before delete = %d, want 1000", reservedBefore)
	}
	if err := st.DeleteUserAccount(ctx, consumerID); err != nil {
		t.Fatalf("delete consumer: %v", err)
	}
	// The consumer's reservation rows cascade (frozen §2.8 user_id CASCADE),
	// but the in-flight rows were converged FIRST so the donation key's
	// reserved counter was freed: the reserved row released its 500 and the
	// dispatched row settled (reserved→used at the original charge).
	var reserved, used int64
	st.DB().QueryRow(`SELECT credits_reserved, credits_used FROM donation_keys WHERE id=?`, keyID).Scan(&reserved, &used)
	if reserved != 0 {
		t.Fatalf("key reserved after consumer delete = %d, want 0 (converged)", reserved)
	}
	if used != 500 {
		t.Fatalf("key used after consumer delete = %d, want 500 (dispatched settled)", used)
	}
	if _, err := st.GetCharityReservation(ctx, res1.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("reserved row after delete = %v, want ErrNotFound (cascade)", err)
	}
}

func TestCharityReservationKeyCapOvershootRejected(t *testing.T) {
	st := newCharityReservationTestStore(t)
	ctx := context.Background()
	_, consumerID, _, keyID := seedCharityRoute(t, st, CharityPricingPerRequest, 100_000)
	// Cap the key at 700 milli-credits. One per-request reserve is 500
	// (admits); a second concurrent reserve would push used+reserved to 1000
	// and must be refused before dispatch (frozen §5.2).
	if _, err := st.DB().Exec(`UPDATE donation_keys SET credits_usage_cap=700 WHERE id=?`, keyID); err != nil {
		t.Fatal(err)
	}
	cand := resolveFirstCandidate(t, st, "[公益]donor/charity-model")
	if _, _, err := st.CreateCharityReservation(ctx, ReserveCharityInput{
		UserID: consumerID, FullName: "[公益]donor/charity-model",
		BindingID: cand.BindingID, DonationKeyID: cand.DonationKeyID,
		AttemptID: "cap-1", BaseURL: cand.BaseURL, Now: 100,
	}); err != nil {
		t.Fatalf("first reserve: %v", err)
	}
	_, _, err := st.CreateCharityReservation(ctx, ReserveCharityInput{
		UserID: consumerID, FullName: "[公益]donor/charity-model",
		BindingID: cand.BindingID, DonationKeyID: cand.DonationKeyID,
		AttemptID: "cap-2", BaseURL: cand.BaseURL, Now: 110,
	})
	if !errors.Is(err, ErrDonationKeyCapReached) {
		t.Fatalf("overshoot = %v, want ErrDonationKeyCapReached", err)
	}
	// The refused reserve wrote nothing: no second reservation, the key
	// reserved stays at 500.
	var n, reserved int64
	st.DB().QueryRow(`SELECT COUNT(*) FROM charity_reservations`).Scan(&n)
	if n != 1 {
		t.Fatalf("reservations = %d, want 1", n)
	}
	st.DB().QueryRow(`SELECT credits_reserved FROM donation_keys WHERE id=?`, keyID).Scan(&reserved)
	if reserved != 500 {
		t.Fatalf("key reserved = %d, want 500", reserved)
	}
}

func TestCharityReservationSettlePastCapAllowed(t *testing.T) {
	st := newCharityReservationTestStore(t)
	ctx := context.Background()
	_, consumerID, _, keyID := seedCharityRoute(t, st, CharityPricingPerRequest, 100_000)
	if _, err := st.DB().Exec(`UPDATE donation_keys SET credits_usage_cap=500 WHERE id=?`, keyID); err != nil {
		t.Fatal(err)
	}
	cand := resolveFirstCandidate(t, st, "[公益]donor/charity-model")
	res, _, _ := st.CreateCharityReservation(ctx, ReserveCharityInput{
		UserID: consumerID, FullName: "[公益]donor/charity-model",
		BindingID: cand.BindingID, DonationKeyID: cand.DonationKeyID,
		AttemptID: "cap-settle", BaseURL: cand.BaseURL, Now: 100,
	})
	st.DispatchCharityReservation(ctx, res.ID, 110)
	// Actual original exceeds both reserve and cap; frozen §5.3 settles in full
	// and the key's used crosses the cap (next admission refuses this key).
	if _, err := st.CommitCharityReservation(ctx, res.ID, CommitPlan{OriginalCharge: 5000, UserCharge: 5000}, 120); err != nil {
		t.Fatalf("commit past cap: %v", err)
	}
	var used int64
	st.DB().QueryRow(`SELECT credits_used FROM donation_keys WHERE id=?`, keyID).Scan(&used)
	if used != 5000 {
		t.Fatalf("key used = %d, want 5000 (past cap)", used)
	}
}

func TestCharityReservationSwapKeyAtomicAndUserDebitedOnce(t *testing.T) {
	st := newCharityReservationTestStore(t)
	ctx := context.Background()
	enableCharity(t, st)
	donorID := newDonationUser(t, st, "donor2")
	ep := newDonationEndpoint(t, st, donorID, "https://swap.example.com")
	k1 := newDonationKey(t, st, donorID, ep.ID, "sk-swap-1")
	k2 := newDonationKey(t, st, donorID, ep.ID, "sk-swap-2")
	for _, k := range []EndpointKey{k1, k2} {
		if err := st.ReplaceFetchedModels(ctx, donorID, ep.ID, k.ID, []FetchedModel{{UpstreamModelID: "up/swap", Provider: "donor"}}, 10); err != nil {
			t.Fatalf("fetch: %v", err)
		}
	}
	in := existingInput(donorID, ep.ID, []int64{k1.ID, k2.ID}, 40)
	d, err := st.CreateDonation(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ApplyDonationReview(ctx, ReviewDecision{DonationID: d.ID, Role: ReviewRoleAdmin, ReviewerID: donorID, Action: ReviewActionApprove, Now: 60}); err != nil {
		t.Fatal(err)
	}
	dk1 := donationKeyIDForTest(t, st, d.ID)
	// The second key id is the other row for this donation.
	var dk2 int64
	st.DB().QueryRow(`SELECT id FROM donation_keys WHERE donation_id=? AND id<>? ORDER BY id LIMIT 1`, d.ID, dk1).Scan(&dk2)
	m := CharityModel{Provider: "donor", Model: "swap", Enabled: true, PricingMode: CharityPricingPerRequest, RequestUserPrice: 500, RequestDonorReward: 100}
	created, err := st.CreateCharityModel(ctx, m, 0, 70)
	if err != nil {
		t.Fatal(err)
	}
	st.CreateCharityBinding(ctx, created.ID, dk1, "up/swap", 0, 80)
	st.CreateCharityBinding(ctx, created.ID, dk2, "up/swap", 1, 81)
	consumerID := newDonationUser(t, st, "swapconsumer")
	setCheckinCredits(t, st, consumerID, 100_000)

	route, err := st.ResolveCharityRoute(ctx, "[公益]donor/swap", 1000, 16)
	if err != nil {
		t.Fatal(err)
	}
	if len(route.Candidates) != 2 {
		t.Fatalf("candidates = %d, want 2", len(route.Candidates))
	}
	first := route.Candidates[0]
	second := route.Candidates[1]
	res, _, _ := st.CreateCharityReservation(ctx, ReserveCharityInput{
		UserID: consumerID, FullName: "[公益]donor/swap",
		BindingID: first.BindingID, DonationKeyID: first.DonationKeyID,
		AttemptID: "swap-1", BaseURL: first.BaseURL, Now: 100,
	})
	// User debited once (500). First key reserved 500.
	var userAfter1 int64
	st.DB().QueryRow(`SELECT credits FROM users WHERE id=?`, consumerID).Scan(&userAfter1)
	if userAfter1 != 100_000-500 {
		t.Fatalf("user after create = %d, want %d", userAfter1, 100_000-500)
	}
	// Swap to the second key: old key reserve released, new key reserve taken,
	// user NOT debited again.
	if err := st.SwapCharityReservationKey(ctx, res.ID, second, 500, 110); err != nil {
		t.Fatalf("swap: %v", err)
	}
	var userAfter2 int64
	st.DB().QueryRow(`SELECT credits FROM users WHERE id=?`, consumerID).Scan(&userAfter2)
	if userAfter2 != 100_000-500 {
		t.Fatalf("user after swap = %d, want unchanged %d", userAfter2, 100_000-500)
	}
	var r1, r2 int64
	st.DB().QueryRow(`SELECT credits_reserved FROM donation_keys WHERE id=?`, first.DonationKeyID).Scan(&r1)
	st.DB().QueryRow(`SELECT credits_reserved FROM donation_keys WHERE id=?`, second.DonationKeyID).Scan(&r2)
	if r1 != 0 || r2 != 500 {
		t.Fatalf("key reserves after swap = %d/%d, want 0/500", r1, r2)
	}
	reloaded, _ := st.GetCharityReservation(ctx, res.ID)
	if reloaded.DonationKeyID == nil || *reloaded.DonationKeyID != second.DonationKeyID {
		t.Fatalf("reservation key after swap = %v, want %d", reloaded.DonationKeyID, second.DonationKeyID)
	}
	// Swapping back to the same key is a no-op.
	if err := st.SwapCharityReservationKey(ctx, res.ID, second, 500, 120); err != nil {
		t.Fatalf("swap same key: %v", err)
	}
	// Swapping a dispatched reservation is illegal.
	st.DispatchCharityReservation(ctx, res.ID, 130)
	if err := st.SwapCharityReservationKey(ctx, res.ID, first, 500, 140); !errors.Is(err, credits.ErrIllegalTransition) {
		t.Fatalf("swap dispatched = %v, want illegal transition", err)
	}
}

func TestCharityReservationPerTokenFailsClosedWithoutReserveConfig(t *testing.T) {
	st := newCharityReservationTestStore(t)
	ctx := context.Background()
	_, consumerID, _, _ := seedCharityRoute(t, st, CharityPricingPerToken, 10_000)
	// Remove the global token reserve: token mode must fail closed.
	setTokenReserveConfig(t, st, "")
	cand := resolveFirstCandidate(t, st, "[公益]donor/charity-model")
	_, _, err := st.CreateCharityReservation(ctx, ReserveCharityInput{
		UserID: consumerID, FullName: "[公益]donor/charity-model",
		BindingID: cand.BindingID, DonationKeyID: cand.DonationKeyID,
		AttemptID: "attempt-notoken", BaseURL: cand.BaseURL, Now: 100,
	})
	if !errors.Is(err, ErrCharityTokenReserveUnconfigured) {
		t.Fatalf("no token reserve = %v, want ErrCharityTokenReserveUnconfigured", err)
	}
}

// TestCharityReservationConcurrentCapNoOvershoot runs 8 goroutines each
// creating a per-request reservation (500) against one donation key with a
// cap that admits only 3 (1500). The frozen §5.2 guarded UPDATE must refuse
// the losers atomically: the number of admitted reservations never exceeds the
// cap and the key's used+reserved never crosses it. Run under -race.
func TestCharityReservationConcurrentCapNoOvershoot(t *testing.T) {
	st := newCharityReservationTestStore(t)
	ctx := context.Background()
	_, consumerID, _, keyID := seedCharityRoute(t, st, CharityPricingPerRequest, 100_000_000)
	const cap = 1500
	if _, err := st.DB().Exec(`UPDATE donation_keys SET credits_usage_cap=? WHERE id=?`, cap, keyID); err != nil {
		t.Fatal(err)
	}
	cand := resolveFirstCandidate(t, st, "[公益]donor/charity-model")

	const goroutines = 8
	var wg sync.WaitGroup
	var admitted atomic.Int64
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(i int) {
			defer wg.Done()
			_, _, err := st.CreateCharityReservation(ctx, ReserveCharityInput{
				UserID: consumerID, FullName: "[公益]donor/charity-model",
				BindingID: cand.BindingID, DonationKeyID: cand.DonationKeyID,
				AttemptID: "race-" + strconv.Itoa(i), BaseURL: cand.BaseURL, Now: 100,
			})
			if err == nil {
				admitted.Add(1)
			} else if !errors.Is(err, ErrDonationKeyCapReached) && !errors.Is(err, ErrInsufficientCredits) {
				t.Errorf("unexpected race error: %v", err)
			}
		}(i)
	}
	wg.Wait()

	// At most 3 reservations admitted (cap 1500 / 500 each).
	if n := admitted.Load(); n > 3 {
		t.Fatalf("admitted = %d, want <= 3 (cap not crossed)", n)
	}
	var reserved, used int64
	st.DB().QueryRow(`SELECT credits_reserved, credits_used FROM donation_keys WHERE id=?`, keyID).Scan(&reserved, &used)
	if used+reserved > cap {
		t.Fatalf("used+reserved = %d, exceeds cap %d", used+reserved, cap)
	}
	var count int64
	st.DB().QueryRow(`SELECT COUNT(*) FROM charity_reservations`).Scan(&count)
	if count != admitted.Load() {
		t.Fatalf("reservation rows = %d, admitted = %d", count, admitted.Load())
	}
	// The consumer was debited exactly (admitted × 500).
	var credits int64
	st.DB().QueryRow(`SELECT credits FROM users WHERE id=?`, consumerID).Scan(&credits)
	if credits != 100_000_000-admitted.Load()*500 {
		t.Fatalf("consumer credits = %d, want %d", credits, 100_000_000-admitted.Load()*500)
	}
}

// TestCharityReservationConcurrentDispatchCommitBalance runs 8 goroutines that
// each create, dispatch, and commit a reservation against the same key with no
// cap (unlimited), then verifies the consumer balance equals the sum of all
// committed user charges and the key's used counter equals the sum of all
// original charges. Actual may drive the balance negative; the invariant is
// exactness under concurrency. Run under -race.
func TestCharityReservationConcurrentDispatchCommitBalance(t *testing.T) {
	st := newCharityReservationTestStore(t)
	ctx := context.Background()
	_, consumerID, _, keyID := seedCharityRoute(t, st, CharityPricingPerRequest, 4_000) // 4_000 = 8 × 500 reserve
	cand := resolveFirstCandidate(t, st, "[公益]donor/charity-model")
	const goroutines = 8
	const charge int64 = 500
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(i int) {
			defer wg.Done()
			res, _, err := st.CreateCharityReservation(ctx, ReserveCharityInput{
				UserID: consumerID, FullName: "[公益]donor/charity-model",
				BindingID: cand.BindingID, DonationKeyID: cand.DonationKeyID,
				AttemptID: "bal-" + strconv.Itoa(i), BaseURL: cand.BaseURL, Now: 100,
			})
			if err != nil {
				t.Errorf("create: %v", err)
				return
			}
			if _, err := st.DispatchCharityReservation(ctx, res.ID, 110); err != nil {
				t.Errorf("dispatch: %v", err)
				return
			}
			if _, err := st.CommitCharityReservation(ctx, res.ID, CommitPlan{OriginalCharge: charge, UserCharge: charge, DonorReward: 100}, 120); err != nil {
				t.Errorf("commit: %v", err)
			}
		}(i)
	}
	wg.Wait()
	// 8 calls each charge 500; the balance started at 4_000 and reserve ==
	// actual, so it ends at 0 (the reserve became the charge).
	var credits int64
	st.DB().QueryRow(`SELECT credits FROM users WHERE id=?`, consumerID).Scan(&credits)
	if credits != 0 {
		t.Fatalf("consumer credits = %d, want 0", credits)
	}
	var used int64
	st.DB().QueryRow(`SELECT credits_used FROM donation_keys WHERE id=?`, keyID).Scan(&used)
	if used != goroutines*charge {
		t.Fatalf("key used = %d, want %d", used, goroutines*charge)
	}
}
