package db

// Charity model repository tests: the '[公益]' full_name invariant and its
// uniqueness, the single-interpretable-price-table rule, the fail-closed token
// reserve requirement, discount bounds and [start,end) semantics, the binding
// candidate predicate (approved+enabled+unexpired donation, enabled claimed
// key, live endpoint/key, fetched upstream id), and the last-100 ring buffer.

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func newCharityTestStore(t *testing.T) *Store {
	t.Helper()
	st := openTestStore(t, filepath.Join(t.TempDir(), "charity.db"))
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func perRequestModel(name string) CharityModel {
	return CharityModel{
		Provider: "donor", Model: name, Enabled: true,
		PricingMode:      CharityPricingPerRequest,
		RequestUserPrice: 500, RequestDonorReward: 100,
	}
}

func setTokenReserveConfig(t *testing.T, st *Store, value string) {
	t.Helper()
	if value == "" {
		if _, err := st.DB().Exec(`DELETE FROM site_config WHERE key='charity_token_reserve_milli'`); err != nil {
			t.Fatalf("clear token reserve: %v", err)
		}
		return
	}
	setCheckinConfig(t, st, "charity_token_reserve_milli", value)
}

// TestCharityModelFullNamePrefixAndUniqueness pins the derived routing key:
// full_name is always '[公益]'provider'/'model', a duplicate is ErrConflict,
// and no personal-model namespace can collide because the prefix is fixed.
func TestCharityModelFullNamePrefixAndUniqueness(t *testing.T) {
	st := newCharityTestStore(t)
	ctx := context.Background()
	m, err := st.CreateCharityModel(ctx, perRequestModel("gpt-x"), 0, 100)
	if err != nil {
		t.Fatalf("CreateCharityModel: %v", err)
	}
	if m.FullName != "[公益]donor/gpt-x" {
		t.Fatalf("full_name = %q", m.FullName)
	}
	if _, err := st.CreateCharityModel(ctx, perRequestModel("gpt-x"), 0, 101); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate = %v, want ErrConflict", err)
	}
	// A different provider with the same model name is fine.
	m2 := perRequestModel("gpt-x")
	m2.Provider = "other"
	m2, err = st.CreateCharityModel(ctx, m2, 0, 102)
	if err != nil {
		t.Fatalf("different provider: %v", err)
	}
	// Renaming onto an existing routing key conflicts.
	if _, err := st.UpdateCharityModel(ctx, m2.ID, CharityModelUpdate{Provider: strPtrDB("donor")}, 103); !errors.Is(err, ErrConflict) {
		t.Fatalf("rename collision = %v, want ErrConflict", err)
	}
}

// TestCharityModelSinglePriceTableAndValidation verifies that the non-current
// price table is zeroed on write (no double interpretation) and that negative
// prices / bad modes / bad discounts are rejected.
func TestCharityModelSinglePriceTableAndValidation(t *testing.T) {
	st := newCharityTestStore(t)
	ctx := context.Background()

	m := perRequestModel("mixed")
	m.UncachedUserPrice = -1
	if _, err := st.CreateCharityModel(ctx, m, 0, 100); !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("negative price = %v, want ErrInvalidValue", err)
	}

	m = perRequestModel("mixed")
	m.UncachedUserPrice = 7 // wrong-table field must not survive
	got, err := st.CreateCharityModel(ctx, m, 0, 100)
	if err != nil {
		t.Fatalf("CreateCharityModel: %v", err)
	}
	if got.UncachedUserPrice != 0 || got.UncachedDonorReward != 0 {
		t.Fatalf("per-request model kept token-table fields: %+v", got)
	}

	tokenModel := CharityModel{
		Provider: "donor", Model: "tok", PricingMode: CharityPricingPerToken,
		UncachedUserPrice: 1000, OutputDonorReward: 50,
	}
	gotTok, err := st.CreateCharityModel(ctx, tokenModel, 0, 101)
	if err != nil {
		t.Fatalf("token create: %v", err)
	}
	if gotTok.RequestUserPrice != 0 || gotTok.RequestDonorReward != 0 {
		t.Fatalf("per-token model kept request-table fields: %+v", gotTok)
	}

	badMode := perRequestModel("bad")
	badMode.PricingMode = "per_character"
	if _, err := st.CreateCharityModel(ctx, badMode, 0, 102); !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("bad mode = %v, want ErrInvalidValue", err)
	}
	discounted := perRequestModel("disc")
	discounted.DiscountPercent = 101
	discounted.DiscountEnabled = true
	if _, err := st.CreateCharityModel(ctx, discounted, 0, 103); !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("discount > 100 = %v, want ErrInvalidValue", err)
	}

	// PATCH path validates too.
	if _, err := st.UpdateCharityModel(ctx, got.ID, CharityModelUpdate{DiscountPercent: intPtr(-1)}, 104); !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("discount < 0 patch = %v, want ErrInvalidValue", err)
	}
}

func intPtr(v int) *int { return &v }

// TestCharityTokenReserveFailClosed proves a per-token model cannot be created
// or flipped enabled while charity_token_reserve_milli is unset or non-positive;
// per-request models are unaffected; a later-configured reserve unblocks enable.
func TestCharityTokenReserveFailClosed(t *testing.T) {
	st := newCharityTestStore(t)
	ctx := context.Background()
	setTokenReserveConfig(t, st, "") // unset

	token := func() CharityModel {
		return CharityModel{
			Provider: "donor", Model: "tok", PricingMode: CharityPricingPerToken,
			UncachedUserPrice: 1000,
		}
	}
	enabledToken := token()
	enabledToken.Enabled = true
	if _, err := st.CreateCharityModel(ctx, enabledToken, 0, 100); !errors.Is(err, ErrCharityTokenReserveMissing) {
		t.Fatalf("unset reserve create = %v, want ErrCharityTokenReserveMissing", err)
	}
	setTokenReserveConfig(t, st, "0")
	if _, err := st.CreateCharityModel(ctx, enabledToken, 0, 101); !errors.Is(err, ErrCharityTokenReserveMissing) {
		t.Fatalf("zero reserve create = %v, want fail closed", err)
	}

	// Disabled token model can exist; enabling fails closed until configured.
	disabled := token()
	disabled.Enabled = false
	m, err := st.CreateCharityModel(ctx, disabled, 0, 102)
	if err != nil {
		t.Fatalf("disabled create: %v", err)
	}
	on := true
	if _, err := st.UpdateCharityModel(ctx, m.ID, CharityModelUpdate{Enabled: &on}, 103); !errors.Is(err, ErrCharityTokenReserveMissing) {
		t.Fatalf("enable without reserve = %v, want fail closed", err)
	}
	setTokenReserveConfig(t, st, "25000000")
	if _, err := st.UpdateCharityModel(ctx, m.ID, CharityModelUpdate{Enabled: &on}, 104); err != nil {
		t.Fatalf("enable with configured reserve: %v", err)
	}

	// Per-request models never touch the reserve gate.
	setTokenReserveConfig(t, st, "")
	if _, err := st.CreateCharityModel(ctx, perRequestModel("plain"), 0, 105); err != nil {
		t.Fatalf("per-request create without reserve: %v", err)
	}
}

// TestCharityDiscountIntervalSemantics exercises EffectiveDiscountPercent over
// the [start,end) interval edges: before start, at start, at end-1, at end,
// and while disabled — always 100 outside the effective window.
func TestCharityDiscountIntervalSemantics(t *testing.T) {
	start, end := int64(1000), int64(2000)
	m := perRequestModel("d")
	m.DiscountPercent = 50
	m.DiscountEnabled = true
	m.DiscountStartAt = &start
	m.DiscountEndAt = &end

	cases := []struct {
		now  int64
		want int
	}{
		{-1, 100}, {999, 100}, {1000, 50}, {1500, 50}, {1999, 50}, {2000, 100}, {5000, 100},
	}
	for _, c := range cases {
		if got := m.EffectiveDiscountPercent(c.now); got != c.want {
			t.Fatalf("EffectiveDiscountPercent(%d) = %d, want %d", c.now, got, c.want)
		}
	}
	m.DiscountEnabled = false
	if got := m.EffectiveDiscountPercent(1500); got != 100 {
		t.Fatalf("disabled discount = %d, want 100", got)
	}
	// Open-ended intervals: only start matters.
	m.DiscountEnabled = true
	m.DiscountEndAt = nil
	if got := m.EffectiveDiscountPercent(99999); got != 50 {
		t.Fatalf("open-ended discount = %d, want 50", got)
	}
}

// TestCharityBindingCandidatePredicate covers every rejection arm of the
// INSERT..SELECT predicate plus the success path and duplicate conflict.
func TestCharityBindingCandidatePredicate(t *testing.T) {
	st := newCharityTestStore(t)
	ctx := context.Background()
	uid := newDonationUser(t, st, "donor")
	ep := newDonationEndpoint(t, st, uid, "https://api.example.com")
	key := newDonationKey(t, st, uid, ep.ID, "sk-bind")
	if err := st.ReplaceFetchedModels(ctx, uid, ep.ID, key.ID, []FetchedModel{{UpstreamModelID: "up/model-a", Provider: "p"}}, 10); err != nil {
		t.Fatalf("ReplaceFetchedModels: %v", err)
	}

	model, err := st.CreateCharityModel(ctx, perRequestModel("bind"), 0, 20)
	if err != nil {
		t.Fatalf("CreateCharityModel: %v", err)
	}
	createBinding := func(dkID int64, now int64) error {
		_, err := st.CreateCharityBinding(ctx, model.ID, dkID, "up/model-a", 0, now)
		return err
	}

	// No donation yet: candidate missing.
	if err := createBinding(99999, 30); !errors.Is(err, ErrNotFound) {
		t.Fatalf("no donation = %v, want ErrNotFound", err)
	}

	in := existingInput(uid, ep.ID, []int64{key.ID}, 40)
	d, err := st.CreateDonation(ctx, in)
	if err != nil {
		t.Fatalf("CreateDonation: %v", err)
	}
	// Pending donation: still not a valid candidate (must be approved).
	if err := createBinding(donationKeyIDForTest(t, st, d.ID), 50); !errors.Is(err, ErrNotFound) {
		t.Fatalf("pending donation binding = %v, want ErrNotFound", err)
	}
	if _, err := st.ApplyDonationReview(ctx, ReviewDecision{
		DonationID: d.ID, Role: ReviewRoleAdmin, ReviewerID: uid, Action: ReviewActionApprove, Now: 60,
	}); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if err := createBinding(donationKeyIDForTest(t, st, d.ID), 70); err != nil {
		t.Fatalf("valid binding rejected: %v", err)
	}
	dkID := donationKeyIDForTest(t, st, d.ID)
	if err := createBinding(dkID, 71); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate triple = %v, want ErrConflict", err)
	}

	// Unknown upstream id fails the fetched-model join.
	_, err = st.CreateCharityBinding(ctx, model.ID, dkID, "up/never-fetched", 1, 72)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown upstream = %v, want ErrNotFound", err)
	}

	// Disabling the donation key invalidates further writes but keeps the
	// existing row readable (the routing rail re-validates at read time).
	keys, _ := st.ListOwnDonationKeys(ctx, uid, d.ID)
	off := false
	if _, err := st.ApplyDonationReview(ctx, ReviewDecision{
		DonationID: d.ID, Role: ReviewRoleAdmin, ReviewerID: uid, Action: ReviewActionUpdate, Now: 80,
		KeyUpdates: []DonationKeyUpdate{{DonationKeyID: keys[0].ID, Enabled: &off}},
	}); err != nil {
		t.Fatalf("key disable: %v", err)
	}
	_, err = st.CreateCharityBinding(ctx, model.ID, keys[0].ID, "up/model-a", 2, 82)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("disabled key binding = %v, want ErrNotFound", err)
	}
}

// helpers to resolve donation/donation-key ids without widening repo surface
func donationIDForTest(t *testing.T, st *Store, uid int64) int64 {
	t.Helper()
	var id int64
	if err := st.DB().QueryRow(`SELECT id FROM donations WHERE user_id=? ORDER BY id DESC LIMIT 1`, uid).Scan(&id); err != nil {
		t.Fatalf("resolve donation id: %v", err)
	}
	return id
}

func donationKeyIDForTest(t *testing.T, st *Store, donationID int64) int64 {
	t.Helper()
	var id int64
	if err := st.DB().QueryRow(`SELECT id FROM donation_keys WHERE donation_id=? ORDER BY id LIMIT 1`, donationID).Scan(&id); err != nil {
		t.Fatalf("resolve donation key id: %v", err)
	}
	return id
}

// TestCharityExpiryBlocksBinding asserts an expired approved donation stops
// being a valid binding candidate.
func TestCharityExpiryBlocksBinding(t *testing.T) {
	st := newCharityTestStore(t)
	ctx := context.Background()
	uid := newDonationUser(t, st, "expired-donor")
	ep := newDonationEndpoint(t, st, uid, "https://api.example.com")
	key := newDonationKey(t, st, uid, ep.ID, "sk-exp-bind")
	if err := st.ReplaceFetchedModels(ctx, uid, ep.ID, key.ID, []FetchedModel{{UpstreamModelID: "up/m", Provider: "p"}}, 10); err != nil {
		t.Fatalf("ReplaceFetchedModels: %v", err)
	}
	expiry := int64(100)
	in := existingInput(uid, ep.ID, []int64{key.ID}, 50)
	in.ExpiresAt = &expiry
	d, err := st.CreateDonation(ctx, in)
	if err != nil {
		t.Fatalf("CreateDonation: %v", err)
	}
	if _, err := st.ApplyDonationReview(ctx, ReviewDecision{
		DonationID: d.ID, Role: ReviewRoleAdmin, ReviewerID: uid, Action: ReviewActionApprove, Now: 60,
	}); err != nil {
		t.Fatalf("approve: %v", err)
	}
	model, err := st.CreateCharityModel(ctx, perRequestModel("exp"), 0, 61)
	if err != nil {
		t.Fatalf("CreateCharityModel: %v", err)
	}
	dkID := donationKeyIDForTest(t, st, d.ID)
	if _, err := st.CreateCharityBinding(ctx, model.ID, dkID, "up/m", 0, 90); err != nil {
		t.Fatalf("binding before expiry: %v", err)
	}
	// After expiry the same write is refused.
	if _, err := st.CreateCharityBinding(ctx, model.ID, dkID, "up/m", 1, 500); !errors.Is(err, ErrNotFound) {
		t.Fatalf("binding after expiry = %v, want ErrNotFound", err)
	}
}

// TestCharityRingBufferRolling drives 250 outcomes through the fixed 100-slot
// buffer and checks the rolling counters against an independent simulation.
func TestCharityRingBufferRolling(t *testing.T) {
	st := newCharityTestStore(t)
	ctx := context.Background()
	model, err := st.CreateCharityModel(ctx, perRequestModel("ring"), 0, 1)
	if err != nil {
		t.Fatalf("CreateCharityModel: %v", err)
	}

	successOf := func(i int) bool { return i%3 != 0 } // deterministic pattern
	var windowSuccess, windowLen int
	for i := 0; i < 250; i++ {
		ok := successOf(i)
		if err := st.RecordCharityOutcome(ctx, model.ID, ok, int64(i)); err != nil {
			t.Fatalf("RecordCharityOutcome(%d): %v", i, err)
		}
		if windowLen == 100 {
			if successOf(i - 100) {
				windowSuccess--
			}
			windowLen--
		}
		windowLen++
		if ok {
			windowSuccess++
		}
		rate, err := st.GetCharitySuccessRate(ctx, model.ID)
		if err != nil {
			t.Fatalf("GetCharitySuccessRate: %v", err)
		}
		if rate.SampleCount != windowLen || rate.SuccessCount != windowSuccess {
			t.Fatalf("after %d outcomes: got (%d,%d), want (%d,%d)",
				i+1, rate.SampleCount, rate.SuccessCount, windowLen, windowSuccess)
		}
	}
	outcomes := countDonationRows(t, st, `SELECT COUNT(*) FROM charity_model_outcomes WHERE model_id=?`, model.ID)
	if outcomes != 100 {
		t.Fatalf("outcome rows = %d, want exactly 100 slots", outcomes)
	}
}

// TestCharityDeleteCascadesBindingsAndStats verifies management-plane delete
// removes bindings/stats/outcomes with the model (configuration object, not
// audit history).
func TestCharityDeleteCascadesBindingsAndStats(t *testing.T) {
	st := newCharityTestStore(t)
	ctx := context.Background()
	uid := newDonationUser(t, st, "cascade-donor")
	ep := newDonationEndpoint(t, st, uid, "https://api.example.com")
	key := newDonationKey(t, st, uid, ep.ID, "sk-cascade")
	if err := st.ReplaceFetchedModels(ctx, uid, ep.ID, key.ID, []FetchedModel{{UpstreamModelID: "up/c", Provider: "p"}}, 10); err != nil {
		t.Fatalf("ReplaceFetchedModels: %v", err)
	}
	d, err := st.CreateDonation(ctx, existingInput(uid, ep.ID, []int64{key.ID}, 20))
	if err != nil {
		t.Fatalf("CreateDonation: %v", err)
	}
	if _, err := st.ApplyDonationReview(ctx, ReviewDecision{
		DonationID: d.ID, Role: ReviewRoleAdmin, ReviewerID: uid, Action: ReviewActionApprove, Now: 30,
	}); err != nil {
		t.Fatalf("approve: %v", err)
	}
	model, err := st.CreateCharityModel(ctx, perRequestModel("cas"), 0, 31)
	if err != nil {
		t.Fatalf("CreateCharityModel: %v", err)
	}
	dkID := donationKeyIDForTest(t, st, d.ID)
	if _, err := st.CreateCharityBinding(ctx, model.ID, dkID, "up/c", 0, 32); err != nil {
		t.Fatalf("CreateCharityBinding: %v", err)
	}
	if err := st.RecordCharityOutcome(ctx, model.ID, true, 33); err != nil {
		t.Fatalf("RecordCharityOutcome: %v", err)
	}
	if err := st.DeleteCharityModel(ctx, model.ID); err != nil {
		t.Fatalf("DeleteCharityModel: %v", err)
	}
	for table, want := range map[string]int64{
		"charity_models": 0, "charity_model_bindings": 0,
		"charity_model_stats": 0, "charity_model_outcomes": 0,
	} {
		if n := countDonationRows(t, st, `SELECT COUNT(*) FROM `+table); n != want {
			t.Fatalf("%s rows after delete = %d, want %d", table, n, want)
		}
	}
	// The donated resources themselves are untouched by a model delete.
	if n := countDonationRows(t, st, `SELECT COUNT(*) FROM donations`); n != 1 {
		t.Fatalf("donations after model delete = %d", n)
	}
}
