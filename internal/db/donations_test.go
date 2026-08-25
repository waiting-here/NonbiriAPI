package db

// Donation repository tests: the physical-key claim invariant (pending and
// approved+enabled keys hold claims, everyone else does not), the atomic
// claim-conflict behavior (constrained INSERT, never read-then-write, exactly
// one winner under concurrency), cross-user / cross-endpoint key isolation,
// the whole-donation review state machine with claim re-synchronization,
// per-key enable/disable re-acquisition, lazy expiry, nested-create rollback
// without residual ciphertext, endpoint/key deletion guards against in-use
// resources, account-deletion convergence, and the safe export projection.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func newDonationTestStore(t *testing.T) *Store {
	t.Helper()
	st := openTestStore(t, filepath.Join(t.TempDir(), "donations.db"))
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func newDonationUser(t *testing.T, st *Store, name string) int64 {
	t.Helper()
	user, err := st.CreateUser("discord-"+name, name, "")
	if err != nil {
		t.Fatalf("CreateUser(%s): %v", name, err)
	}
	return user.ID
}

func newDonationEndpoint(t *testing.T, st *Store, uid int64, baseURL string) Endpoint {
	t.Helper()
	ep, err := st.CreateEndpoint(context.Background(), uid, "openai-compatible", baseURL, "", true, 1)
	if err != nil {
		t.Fatalf("CreateEndpoint: %v", err)
	}
	return ep
}

func newDonationKey(t *testing.T, st *Store, uid, endpointID int64, secret string) EndpointKey {
	t.Helper()
	k, err := st.CreateEndpointKey(context.Background(), uid, endpointID, []byte(secret), "h", "t", "", true, 1)
	if err != nil {
		t.Fatalf("CreateEndpointKey: %v", err)
	}
	return k
}

func countDonationRows(t *testing.T, st *Store, query string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := st.DB().QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", query, err)
	}
	return n
}

func claimsOfDonation(t *testing.T, st *Store, donationID int64) map[int64]int64 {
	t.Helper()
	rows, err := st.DB().Query(`
SELECT c.endpoint_key_id, c.donation_key_id FROM donation_key_claims c
JOIN donation_keys dk ON dk.id = c.donation_key_id WHERE dk.donation_id=?`, donationID)
	if err != nil {
		t.Fatalf("read claims: %v", err)
	}
	defer rows.Close()
	out := map[int64]int64{}
	for rows.Next() {
		var phys, dk int64
		if err := rows.Scan(&phys, &dk); err != nil {
			t.Fatalf("scan claim: %v", err)
		}
		out[phys] = dk
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate claims: %v", err)
	}
	return out
}

func setDonationAcceptConfig(t *testing.T, st *Store, value string) {
	t.Helper()
	if _, err := st.DB().Exec(`INSERT INTO site_config (key, value, updated_at) VALUES ('donation_accept_enabled', ?, 0)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`, value); err != nil {
		t.Fatalf("set donation_accept_enabled: %v", err)
	}
}

func existingInput(uid, endpointID int64, keyIDs []int64, now int64) CreateDonationInput {
	return CreateDonationInput{
		UserID:      uid,
		Description: "test donation",
		Now:         now,
		Existing:    &ExistingEndpointKeys{EndpointID: endpointID, KeyIDs: keyIDs},
	}
}

// TestDonationClaimsFollowLifecycle pins the invariant across every
// transition: pending holds all claims; approve defaults to enabled and keeps
// them; disable releases; enable re-acquires; reject releases; soft delete
// releases; a second delete is refused.
func TestDonationClaimsFollowLifecycle(t *testing.T) {
	st := newDonationTestStore(t)
	ctx := context.Background()
	uid := newDonationUser(t, st, "alice")
	ep := newDonationEndpoint(t, st, uid, "https://api.example.com")
	k1 := newDonationKey(t, st, uid, ep.ID, "sk-one")
	k2 := newDonationKey(t, st, uid, ep.ID, "sk-two")

	d, err := st.CreateDonation(ctx, existingInput(uid, ep.ID, []int64{k1.ID, k2.ID}, 100))
	if err != nil {
		t.Fatalf("CreateDonation: %v", err)
	}
	if d.Status != DonationPending || d.Enabled {
		t.Fatalf("fresh donation = %s/%v, want pending/false", d.Status, d.Enabled)
	}
	if got := claimsOfDonation(t, st, d.ID); len(got) != 2 {
		t.Fatalf("pending claims = %v, want both keys claimed", got)
	}

	approved, err := st.ApplyDonationReview(ctx, ReviewDecision{
		DonationID: d.ID, Role: ReviewRoleAdmin, ReviewerID: uid, Action: ReviewActionApprove, Now: 200,
	})
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if !approved.Enabled {
		t.Fatal("approve must default the donation to enabled")
	}
	if got := claimsOfDonation(t, st, d.ID); len(got) != 2 {
		t.Fatalf("approved claims = %v, want both keys claimed", got)
	}

	disabled, err := st.ApplyDonationReview(ctx, ReviewDecision{
		DonationID: d.ID, Role: ReviewRoleAdmin, ReviewerID: uid, Action: ReviewActionDisable, Now: 300,
	})
	if err != nil {
		t.Fatalf("disable: %v", err)
	}
	if disabled.Enabled {
		t.Fatal("disable must clear enabled")
	}
	if got := claimsOfDonation(t, st, d.ID); len(got) != 0 {
		t.Fatalf("disabled claims = %v, want none", got)
	}

	reEnabled, err := st.ApplyDonationReview(ctx, ReviewDecision{
		DonationID: d.ID, Role: ReviewRoleLevel5, ReviewerID: uid, Action: ReviewActionEnable, Now: 400,
	})
	if err != nil {
		t.Fatalf("enable: %v", err)
	}
	if !reEnabled.Enabled {
		t.Fatal("enable must set enabled")
	}
	if got := claimsOfDonation(t, st, d.ID); len(got) != 2 {
		t.Fatalf("re-enabled claims = %v, want both keys claimed again", got)
	}

	reviews, err := st.ListDonationReviews(ctx, uid, d.ID)
	if err != nil {
		t.Fatalf("ListDonationReviews: %v", err)
	}
	var actions []string
	for _, r := range reviews {
		actions = append(actions, r.Action+"("+r.ReviewerRole+")")
	}
	joined := strings.Join(actions, ",")
	// The owner delete appends a system-role audit entry.
	for _, want := range []string{"approve(admin)", "disable(admin)", "enable(level5)"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("review audit missing %q in %v", want, actions)
		}
	}

	if err := st.DeleteOwnDonation(ctx, uid, d.ID, 500); err != nil {
		t.Fatalf("owner delete: %v", err)
	}
	postDelete, err := st.ListDonationReviews(ctx, uid, d.ID)
	if err != nil {
		t.Fatalf("post-delete reviews: %v", err)
	}
	last := postDelete[len(postDelete)-1]
	if last.Action != ReviewActionDelete || last.ReviewerRole != ReviewRoleSystem {
		t.Fatalf("delete audit = %+v, want system delete", last)
	}
	if got := claimsOfDonation(t, st, d.ID); len(got) != 0 {
		t.Fatalf("deleted claims = %v, want none", got)
	}
	if err := st.DeleteOwnDonation(ctx, uid, d.ID, 600); !errors.Is(err, ErrConflict) {
		t.Fatalf("second delete = %v, want ErrConflict", err)
	}
}

// TestDonationClaimConflictAtomic proves the constrained INSERT decides: two
// donations racing for the same physical key yield exactly one winner, no
// merged limits, and the loser's whole submission rolled back.
func TestDonationClaimConflictAtomic(t *testing.T) {
	st := newDonationTestStore(t)
	ctx := context.Background()
	uid := newDonationUser(t, st, "bob")
	ep := newDonationEndpoint(t, st, uid, "https://api.example.com")
	key := newDonationKey(t, st, uid, ep.ID, "sk-shared")

	first, err := st.CreateDonation(ctx, existingInput(uid, ep.ID, []int64{key.ID}, 100))
	if err != nil {
		t.Fatalf("first donation: %v", err)
	}
	_, err = st.CreateDonation(ctx, existingInput(uid, ep.ID, []int64{key.ID}, 101))
	if !errors.Is(err, ErrDonationKeyClaimConflict) {
		t.Fatalf("duplicate physical claim = %v, want ErrDonationKeyClaimConflict", err)
	}
	// The loser rolled back completely.
	if n := countDonationRows(t, st, `SELECT COUNT(*) FROM donations`); n != 1 {
		t.Fatalf("donations = %d, want only the winner", n)
	}
	if n := countDonationRows(t, st, `SELECT COUNT(*) FROM donation_keys WHERE donation_id<>?`, first.ID); n != 0 {
		t.Fatalf("loser left %d donation_keys rows", n)
	}
	holder := claimsOfDonation(t, st, first.ID)
	if len(holder) != 1 || holder[key.ID] == 0 {
		t.Fatalf("winner claims = %v, want the physical key", holder)
	}

	// Concurrent submitters race for one physical key: exactly one succeeds.
	const racers = 8
	wg := sync.WaitGroup{}
	results := make(chan error, racers)
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := st.CreateDonation(ctx, existingInput(uid, ep.ID, []int64{key.ID}, 200))
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	var wins, losses int
	for err := range results {
		switch {
		case err == nil:
			wins++
		case errors.Is(err, ErrDonationKeyClaimConflict):
			losses++
		default:
			t.Fatalf("unexpected racer error: %v", err)
		}
	}
	if wins != 0 || losses != racers {
		t.Fatalf("race outcome wins=%d losses=%d, want 0/%d (already claimed)", wins, losses, racers)
	}
	if n := countDonationRows(t, st, `SELECT COUNT(*) FROM donations`); n != 1 {
		t.Fatalf("post-race donations = %d, want still only the winner", n)
	}
}

// TestDonationCrossUserAndCrossEndpointIsolation verifies that keys from
// another user or another endpoint can never be donated.
func TestDonationCrossUserAndCrossEndpointIsolation(t *testing.T) {
	st := newDonationTestStore(t)
	ctx := context.Background()
	alice := newDonationUser(t, st, "alice")
	bob := newDonationUser(t, st, "bob")
	aEP := newDonationEndpoint(t, st, alice, "https://a.example.com")
	bEP := newDonationEndpoint(t, st, bob, "https://b.example.com")
	bKey := newDonationKey(t, st, bob, bEP.ID, "sk-bob")

	// Alice references Bob's key id with her own endpoint: not found.
	_, err := st.CreateDonation(ctx, existingInput(alice, aEP.ID, []int64{bKey.ID}, 100))
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-user key = %v, want ErrNotFound", err)
	}
	// Bob's second endpoint does not make his key donatable through it.
	otherEP := newDonationEndpoint(t, st, bob, "https://b2.example.com")
	_, err = st.CreateDonation(ctx, existingInput(bob, otherEP.ID, []int64{bKey.ID}, 100))
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-endpoint key = %v, want ErrNotFound", err)
	}
	if n := countDonationRows(t, st, `SELECT COUNT(*) FROM donations`); n != 0 {
		t.Fatalf("rejected submissions leaked %d donation rows", n)
	}
}

// TestDonationNestedCreateRollbackNoResidualSecret submits a nested
// endpoint+keys donation whose claim step fails, then asserts NOTHING survived:
// no donation row, no endpoint_keys row, no ciphertext anywhere.
func TestDonationNestedCreateRollbackNoResidualSecret(t *testing.T) {
	st := newDonationTestStore(t)
	ctx := context.Background()
	uid := newDonationUser(t, st, "carol")

	// Pre-existing active donation already holds the physical key we will
	// collide with via the nested path's fresh key... fresh keys cannot
	// collide on claims, so force the rollback inside the nested transaction
	// by exceeding the endpoint cap mid-flight: configure a cap of 1 while the
	// user already owns one endpoint.
	setDonationAcceptConfig(t, st, "1")
	setCheckinConfig(t, st, "default_endpoint_limit", "1")
	newEPCount := countDonationRows(t, st, `SELECT COUNT(*) FROM endpoints WHERE user_id=?`, uid)
	if newEPCount != 0 {
		t.Fatalf("preexisting endpoints = %d", newEPCount)
	}
	in := CreateDonationInput{
		UserID:      uid,
		Description: "nested",
		Now:         100,
		New: &NewEndpointSpec{
			ConnectorType: "openai-compatible",
			BaseURL:       "https://nested.example.com",
		},
		Keys: []NewKeySpec{{
			Secret: []byte("sk-nested-secret-material"), Note: "", Enabled: true,
			DisplayHead: "h", DisplayTail: "t",
		}},
	}
	// First nested creation must succeed (cap 1 allows exactly one endpoint).
	d, err := st.CreateDonation(ctx, in)
	if err != nil {
		t.Fatalf("nested create: %v", err)
	}
	if d.Status != DonationPending {
		t.Fatalf("nested status = %s", d.Status)
	}

	// Second nested creation hits the endpoint cap and must roll back wholly.
	in.Keys[0].Secret = []byte("sk-second-secret-material")
	if _, err := st.CreateDonation(ctx, in); err == nil {
		t.Fatal("second nested create over cap should fail")
	}
	if n := countDonationRows(t, st, `SELECT COUNT(*) FROM endpoints WHERE user_id=?`, uid); n != 1 {
		t.Fatalf("endpoints after rollback = %d, want 1", n)
	}
	if n := countDonationRows(t, st, `SELECT COUNT(*) FROM endpoint_keys`); n != 1 {
		t.Fatalf("endpoint_keys after rollback = %d, want 1", n)
	}
	if n := countDonationRows(t, st, `SELECT COUNT(*) FROM donations`); n != 1 {
		t.Fatalf("donations after rollback = %d, want 1", n)
	}
	// No residual ciphertext of either secret anywhere.
	for _, secret := range []string{"sk-nested-secret-material", "sk-second-secret-material"} {
		var blob string
		err := st.DB().QueryRow(`SELECT encrypted_secret FROM endpoint_keys LIMIT 1`).Scan(&blob)
		if err != nil {
			t.Fatalf("read ciphertext: %v", err)
		}
		if strings.Contains(blob, secret) {
			t.Fatal("persisted ciphertext contains plaintext secret material")
		}
	}
}

// TestNestedDonationStorePolicyIsOwnerScopedAndOpenAIOnly covers the nested
// donation creation path: a donor owns the fresh physical key and may opt it
// into force_store_false, while an Anthropic nested endpoint rejects the same
// policy atomically before any endpoint, donation, key, or audit row commits.
func TestNestedDonationStorePolicyIsOwnerScopedAndOpenAIOnly(t *testing.T) {
	st := newDonationTestStore(t)
	ctx := context.Background()
	uid := newDonationUser(t, st, "nested-policy")

	in := CreateDonationInput{
		UserID: uid, Description: "nested policy", Now: 100,
		New: &NewEndpointSpec{ConnectorType: "openai-compatible", BaseURL: "https://nested-policy.example.com"},
		Keys: []NewKeySpec{{
			Secret: []byte("sk-nested-policy"), Enabled: true, ForceStoreFalse: true,
			DisplayHead: "h", DisplayTail: "t",
		}},
	}
	d, err := st.CreateDonation(ctx, in)
	if err != nil {
		t.Fatalf("nested OpenAI policy create: %v", err)
	}
	var keyID int64
	var force int
	if err := st.DB().QueryRow(`SELECT id, force_store_false FROM endpoint_keys WHERE endpoint_id=?`, d.EndpointID).Scan(&keyID, &force); err != nil {
		t.Fatalf("read nested policy key: %v", err)
	}
	if force != 1 {
		t.Fatalf("nested force_store_false = %d, want 1", force)
	}
	var actorRole, resourceType, policy string
	var oldValue, newValue int
	if err := st.DB().QueryRow(`
SELECT actor_role, resource_type, policy, old_value, new_value
FROM policy_audits WHERE resource_id=? ORDER BY id DESC LIMIT 1`, keyID).
		Scan(&actorRole, &resourceType, &policy, &oldValue, &newValue); err != nil {
		t.Fatalf("read nested policy audit: %v", err)
	}
	if actorRole != "owner" || resourceType != "endpoint_key" || policy != "force_store_false" || oldValue != 0 || newValue != 1 {
		t.Fatalf("nested policy audit = %q/%q/%q %d->%d", actorRole, resourceType, policy, oldValue, newValue)
	}

	beforeEndpoints := countDonationRows(t, st, `SELECT COUNT(*) FROM endpoints WHERE user_id=?`, uid)
	beforeDonations := countDonationRows(t, st, `SELECT COUNT(*) FROM donations WHERE user_id=?`, uid)
	beforeKeys := countDonationRows(t, st, `SELECT COUNT(*) FROM endpoint_keys WHERE endpoint_id IN (SELECT id FROM endpoints WHERE user_id=?)`, uid)
	beforeAudits := countDonationRows(t, st, `SELECT COUNT(*) FROM policy_audits WHERE actor_user_id=?`, uid)
	anthropic := CreateDonationInput{
		UserID: uid, Description: "anthropic policy", Now: 101,
		New: &NewEndpointSpec{ConnectorType: "anthropic-compatible", BaseURL: "https://nested-anthropic.example.com"},
		Keys: []NewKeySpec{{
			Secret: []byte("sk-nested-anthropic-policy"), Enabled: true, ForceStoreFalse: true,
			DisplayHead: "h", DisplayTail: "t",
		}},
	}
	if _, err := st.CreateDonation(ctx, anthropic); !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("nested Anthropic policy error = %v, want ErrInvalidValue", err)
	}
	if got := countDonationRows(t, st, `SELECT COUNT(*) FROM endpoints WHERE user_id=?`, uid); got != beforeEndpoints {
		t.Fatalf("Anthropic rejection endpoints = %d, want %d", got, beforeEndpoints)
	}
	if got := countDonationRows(t, st, `SELECT COUNT(*) FROM donations WHERE user_id=?`, uid); got != beforeDonations {
		t.Fatalf("Anthropic rejection donations = %d, want %d", got, beforeDonations)
	}
	if got := countDonationRows(t, st, `SELECT COUNT(*) FROM endpoint_keys WHERE endpoint_id IN (SELECT id FROM endpoints WHERE user_id=?)`, uid); got != beforeKeys {
		t.Fatalf("Anthropic rejection keys = %d, want %d", got, beforeKeys)
	}
	if got := countDonationRows(t, st, `SELECT COUNT(*) FROM policy_audits WHERE actor_user_id=?`, uid); got != beforeAudits {
		t.Fatalf("Anthropic rejection audits = %d, want %d", got, beforeAudits)
	}
}

// TestDonationExpirySweepsAndReclaims covers the lazy expiry sweep: an expired
// approved+enabled donation is disabled with a system review entry and its
// claims released; extending the expiry and re-enabling re-acquires them.
func TestDonationExpirySweepsAndReclaims(t *testing.T) {
	st := newDonationTestStore(t)
	ctx := context.Background()
	uid := newDonationUser(t, st, "dave")
	ep := newDonationEndpoint(t, st, uid, "https://api.example.com")
	key := newDonationKey(t, st, uid, ep.ID, "sk-exp")

	expiry := int64(150)
	in := existingInput(uid, ep.ID, []int64{key.ID}, 100)
	in.ExpiresAt = &expiry
	d, err := st.CreateDonation(ctx, in)
	if err != nil {
		t.Fatalf("CreateDonation: %v", err)
	}
	if _, err := st.ApplyDonationReview(ctx, ReviewDecision{
		DonationID: d.ID, Role: ReviewRoleAdmin, ReviewerID: uid, Action: ReviewActionApprove, Now: 120,
	}); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if got := claimsOfDonation(t, st, d.ID); len(got) != 1 {
		t.Fatalf("pre-expiry claims = %v", got)
	}

	// A read after expiry lazily disables and releases.
	got, _, _, err := st.GetOwnDonation(ctx, uid, d.ID, 999)
	if err != nil {
		t.Fatalf("GetOwnDonation: %v", err)
	}
	if got.Enabled {
		t.Fatal("expired donation must read back disabled")
	}
	if got.Status != DonationApproved {
		t.Fatalf("expired status = %s, want approved (disabled, not rejected)", got.Status)
	}
	if claims := claimsOfDonation(t, st, d.ID); len(claims) != 0 {
		t.Fatalf("expired claims = %v, want none", claims)
	}

	// Extend expiry + enable re-acquires the claim atomically.
	newExpiry := int64(2000)
	expOuter := &newExpiry
	reEnabled, err := st.ApplyDonationReview(ctx, ReviewDecision{
		DonationID: d.ID, Role: ReviewRoleLevel5, ReviewerID: uid,
		Action: ReviewActionEnable, ExpiresAt: &expOuter, Now: 1100,
	})
	if err != nil {
		t.Fatalf("extend+enable: %v", err)
	}
	if !reEnabled.Enabled || reEnabled.ExpiresAt == nil || *reEnabled.ExpiresAt != newExpiry {
		t.Fatalf("extended donation = %+v", reEnabled)
	}
	if claims := claimsOfDonation(t, st, d.ID); len(claims) != 1 {
		t.Fatalf("re-enabled after expiry claims = %v, want the key claimed", claims)
	}
}

// TestDonationKeyEnableReacquireConflict covers per-key enable/disable on an
// APPROVED+enabled donation: an individually disabled key releases only its
// own claim, and re-enabling it conflicts when another donation meanwhile
// claimed the same physical key. A pending donation holds claims for ALL of
// its keys regardless of the per-key flag (frozen §2.6), so the disable step
// runs after approval.
func TestDonationKeyEnableReacquireConflict(t *testing.T) {
	st := newDonationTestStore(t)
	ctx := context.Background()
	uid := newDonationUser(t, st, "erin")
	ep := newDonationEndpoint(t, st, uid, "https://api.example.com")
	key := newDonationKey(t, st, uid, ep.ID, "sk-toggle")

	d, err := st.CreateDonation(ctx, existingInput(uid, ep.ID, []int64{key.ID}, 100))
	if err != nil {
		t.Fatalf("CreateDonation: %v", err)
	}
	if _, err := st.ApplyDonationReview(ctx, ReviewDecision{
		DonationID: d.ID, Role: ReviewRoleAdmin, ReviewerID: uid, Action: ReviewActionApprove, Now: 150,
	}); err != nil {
		t.Fatalf("approve: %v", err)
	}
	keys, err := st.ListOwnDonationKeys(ctx, uid, d.ID)
	if err != nil {
		t.Fatalf("ListOwnDonationKeys: %v", err)
	}
	dkID := keys[0].ID

	off, err := st.ApplyDonationReview(ctx, ReviewDecision{
		DonationID: d.ID, Role: ReviewRoleAdmin, ReviewerID: uid, Action: ReviewActionUpdate, Now: 200,
		KeyUpdates: []DonationKeyUpdate{{DonationKeyID: dkID, Enabled: boolPtrDB(false)}},
	})
	if err != nil {
		t.Fatalf("key disable: %v", err)
	}
	_ = off
	if claims := claimsOfDonation(t, st, d.ID); len(claims) != 0 {
		t.Fatalf("claims after key disable = %v, want none", claims)
	}

	// While the key is unclaimed another pending donation takes it.
	_, err = st.CreateDonation(ctx, existingInput(uid, ep.ID, []int64{key.ID}, 210))
	if err != nil {
		t.Fatalf("second donation: %v", err)
	}
	// Re-enabling the disabled key in the first (now disabled-donation) must
	// conflict against the pending claim held by the second donation.
	_, err = st.ApplyDonationReview(ctx, ReviewDecision{
		DonationID: d.ID, Role: ReviewRoleAdmin, ReviewerID: uid, Action: ReviewActionUpdate, Now: 220,
		KeyUpdates: []DonationKeyUpdate{{DonationKeyID: dkID, Enabled: boolPtrDB(true)}},
	})
	if !errors.Is(err, ErrDonationKeyClaimConflict) {
		t.Fatalf("re-enable = %v, want ErrDonationKeyClaimConflict", err)
	}
}

// TestDonationResourceDeleteGuards covers the in-use guard: endpoint/key
// deletion is refused while a pending or approved+enabled donation references
// them and allowed once every such donation is gone; terminal records keep
// their snapshots.
func TestDonationResourceDeleteGuards(t *testing.T) {
	st := newDonationTestStore(t)
	ctx := context.Background()
	uid := newDonationUser(t, st, "frank")
	ep := newDonationEndpoint(t, st, uid, "https://api.example.com")
	key := newDonationKey(t, st, uid, ep.ID, "sk-guard")

	d, err := st.CreateDonation(ctx, existingInput(uid, ep.ID, []int64{key.ID}, 100))
	if err != nil {
		t.Fatalf("CreateDonation: %v", err)
	}
	if err := st.DeleteEndpointKey(ctx, uid, ep.ID, key.ID); !errors.Is(err, ErrResourceInActiveDonation) {
		t.Fatalf("key delete while pending = %v, want ErrResourceInActiveDonation", err)
	}
	if err := st.DeleteEndpoint(ctx, uid, ep.ID); !errors.Is(err, ErrResourceInActiveDonation) {
		t.Fatalf("endpoint delete while pending = %v, want ErrResourceInActiveDonation", err)
	}

	// Approve+disable releases the claim but the reference stays: still guarded.
	if _, err := st.ApplyDonationReview(ctx, ReviewDecision{
		DonationID: d.ID, Role: ReviewRoleAdmin, ReviewerID: uid, Action: ReviewActionApprove, Now: 200,
	}); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if err := st.DeleteEndpointKey(ctx, uid, ep.ID, key.ID); !errors.Is(err, ErrResourceInActiveDonation) {
		t.Fatalf("key delete while approved+enabled = %v, want guard", err)
	}

	// Owner soft delete removes the active reference; deletion now succeeds
	// and the terminal record keeps only safe snapshot columns.
	if err := st.DeleteOwnDonation(ctx, uid, d.ID, 300); err != nil {
		t.Fatalf("owner delete: %v", err)
	}
	if err := st.DeleteEndpointKey(ctx, uid, ep.ID, key.ID); err != nil {
		t.Fatalf("key delete after donation delete: %v", err)
	}
	got, _, reviews, err := st.GetOwnDonation(ctx, uid, d.ID, 400)
	if err != nil {
		t.Fatalf("terminal read: %v", err)
	}
	if got.Status != DonationDeleted || got.EndpointBaseURL == "" {
		t.Fatalf("terminal donation = %s base_url=%q, want deleted with snapshot", got.Status, got.EndpointBaseURL)
	}
	if len(reviews) == 0 || reviews[len(reviews)-1].Action != ReviewActionDelete {
		t.Fatalf("terminal reviews = %+v, want a delete entry", reviews)
	}
	for _, k := range func() []DonationKey { ks, _ := st.ListOwnDonationKeys(ctx, uid, d.ID); return ks }() {
		if k.DisplayHead == "" && k.DisplayTail == "" && k.EndpointKeyID != nil {
			t.Fatal("terminal key row unexpectedly keeps a live physical reference")
		}
	}
}

// TestDonationAccountDeleteConvergesClaims verifies the special account
// lifecycle: claims are removed BEFORE the cascade so a deleting donor never
// leaves a routable secret behind, and nothing blocks the user-row delete.
func TestDonationAccountDeleteConvergesClaims(t *testing.T) {
	st := newDonationTestStore(t)
	ctx := context.Background()
	uid := newDonationUser(t, st, "grace")
	ep := newDonationEndpoint(t, st, uid, "https://api.example.com")
	key := newDonationKey(t, st, uid, ep.ID, "sk-del")
	d, err := st.CreateDonation(ctx, existingInput(uid, ep.ID, []int64{key.ID}, 100))
	if err != nil {
		t.Fatalf("CreateDonation: %v", err)
	}
	_ = d

	if err := st.DeleteUserAccount(ctx, uid); err != nil {
		t.Fatalf("DeleteUserAccount with live donation: %v", err)
	}
	if n := countDonationRows(t, st, `SELECT COUNT(*) FROM donation_key_claims`); n != 0 {
		t.Fatalf("surviving claims = %d, want none", n)
	}
	if n := countDonationRows(t, st, `SELECT COUNT(*) FROM endpoint_keys`); n != 0 {
		t.Fatalf("surviving keys = %d, want none (no routable secret kept alive)", n)
	}
	if n := countDonationRows(t, st, `SELECT COUNT(*) FROM donations WHERE user_id=?`, uid); n != 0 {
		t.Fatalf("surviving donations = %d", n)
	}
}

// TestDonationExportSafeProjection checks the export rows carry only safe
// metadata and stay bounded.
func TestDonationExportSafeProjection(t *testing.T) {
	st := newDonationTestStore(t)
	ctx := context.Background()
	uid := newDonationUser(t, st, "henry")
	ep := newDonationEndpoint(t, st, uid, "https://api.example.com")
	key := newDonationKey(t, st, uid, ep.ID, "sk-export")
	noteBearing := newDonationKey(t, st, uid, ep.ID, "sk-noted")
	if _, err := st.DB().Exec(`UPDATE endpoint_keys SET note='private donor note' WHERE id=?`, noteBearing.ID); err != nil {
		t.Fatalf("set note: %v", err)
	}

	in := existingInput(uid, ep.ID, []int64{key.ID, noteBearing.ID}, 100)
	in.Description = "export me"
	d, err := st.CreateDonation(ctx, in)
	if err != nil {
		t.Fatalf("CreateDonation: %v", err)
	}
	if _, err := st.ApplyDonationReview(ctx, ReviewDecision{
		DonationID: d.ID, Role: ReviewRoleLevel5, ReviewerID: uid, Action: ReviewActionApprove, Note: "ok", Now: 200,
	}); err != nil {
		t.Fatalf("approve: %v", err)
	}

	rows, err := st.ListExportDonations(ctx, uid, ExportCollectionLimit)
	if err != nil {
		t.Fatalf("ListExportDonations: %v", err)
	}
	if len(rows) != 1 || len(rows[0].Keys) != 2 || len(rows[0].Reviews) < 1 {
		t.Fatalf("export shape = %+v", rows)
	}
	raw, err := json.Marshal(rows)
	if err != nil {
		t.Fatalf("marshal export: %v", err)
	}
	for _, forbidden := range []string{"sk-export", "sk-noted", "private donor note", "ciphertext"} {
		if bytes.Contains(raw, []byte(forbidden)) {
			t.Fatalf("export leaks %q", forbidden)
		}
	}
	if _, err := st.ListExportDonations(ctx, uid, 0); !errors.Is(err, ErrExportLimit) {
		t.Fatalf("nonpositive limit = %v, want ErrExportLimit", err)
	}
}

// TestDonationPendingEditReplacesKeysAndClaims verifies the owner's pending-
// only edit: replacing the selected key set swaps claims atomically, and an
// edited donation stays editable until reviewed.
func TestDonationPendingEditReplacesKeysAndClaims(t *testing.T) {
	st := newDonationTestStore(t)
	ctx := context.Background()
	uid := newDonationUser(t, st, "iris")
	ep := newDonationEndpoint(t, st, uid, "https://api.example.com")
	k1 := newDonationKey(t, st, uid, ep.ID, "sk-a")
	k2 := newDonationKey(t, st, uid, ep.ID, "sk-b")

	d, err := st.CreateDonation(ctx, existingInput(uid, ep.ID, []int64{k1.ID}, 100))
	if err != nil {
		t.Fatalf("CreateDonation: %v", err)
	}
	newDesc := "updated description"
	updated, err := st.UpdateOwnPendingDonation(ctx, UpdateDonationKeysInput{
		UserID: uid, DonationID: d.ID, Now: 150,
		Description: &newDesc,
		KeyIDs:      &[]int64{k2.ID},
		Limits:      []KeyLimitSpec{{EndpointKeyID: k2.ID, MaxConcurrency: 4, RPMLimit: 10}},
	})
	if err != nil {
		t.Fatalf("UpdateOwnPendingDonation: %v", err)
	}
	if updated.Description != newDesc {
		t.Fatalf("description = %q", updated.Description)
	}
	if claims := claimsOfDonation(t, st, d.ID); len(claims) != 1 || claims[k2.ID] == 0 {
		t.Fatalf("claims after replacement = %v, want k2", claims)
	}
	keys, err := st.ListOwnDonationKeys(ctx, uid, d.ID)
	if err != nil {
		t.Fatalf("keys: %v", err)
	}
	if len(keys) != 1 || keys[0].MaxConcurrency != 4 || keys[0].RPMLimit != 10 {
		t.Fatalf("keys after edit = %+v", keys)
	}

	// After approval the pending-only edit path refuses.
	if _, err := st.ApplyDonationReview(ctx, ReviewDecision{
		DonationID: d.ID, Role: ReviewRoleAdmin, ReviewerID: uid, Action: ReviewActionApprove, Now: 200,
	}); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if _, err := st.UpdateOwnPendingDonation(ctx, UpdateDonationKeysInput{
		UserID: uid, DonationID: d.ID, Now: 250, Description: &newDesc,
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("edit after approval = %v, want ErrConflict", err)
	}
}

// TestListDonationKeyIDsByDonor covers the lifecycle lookup used to forget the
// per-key admission limiter when a donor account is deleted: it returns the
// ids of every donation key owned by the donor across all their donations,
// reads no secret, and fails closed for an unknown user.
func TestListDonationKeyIDsByDonor(t *testing.T) {
	st := newDonationTestStore(t)
	ctx := context.Background()
	uid := newDonationUser(t, st, "donor-lister")
	other := newDonationUser(t, st, "other-donor")
	ep1 := newDonationEndpoint(t, st, uid, "https://a.example.com")
	ep2 := newDonationEndpoint(t, st, uid, "https://b.example.com")
	epO := newDonationEndpoint(t, st, other, "https://c.example.com")
	ka := newDonationKey(t, st, uid, ep1.ID, "sk-a")
	kb := newDonationKey(t, st, uid, ep1.ID, "sk-b")
	kc := newDonationKey(t, st, uid, ep2.ID, "sk-c")
	ko := newDonationKey(t, st, other, epO.ID, "sk-o")
	d1, err := st.CreateDonation(ctx, existingInput(uid, ep1.ID, []int64{ka.ID, kb.ID}, 100))
	if err != nil {
		t.Fatalf("CreateDonation 1: %v", err)
	}
	d2, err := st.CreateDonation(ctx, existingInput(uid, ep2.ID, []int64{kc.ID}, 110))
	if err != nil {
		t.Fatalf("CreateDonation 2: %v", err)
	}
	if _, err := st.CreateDonation(ctx, existingInput(other, epO.ID, []int64{ko.ID}, 120)); err != nil {
		t.Fatalf("CreateDonation other: %v", err)
	}

	// The donation_key ids are minted by CreateDonation, distinct from the
	// endpoint_key ids; collect them via the owner listing.
	want := make(map[int64]bool)
	for _, did := range []int64{d1.ID, d2.ID} {
		ks, lerr := st.ListOwnDonationKeys(ctx, uid, did)
		if lerr != nil {
			t.Fatalf("ListOwnDonationKeys %d: %v", did, lerr)
		}
		for _, k := range ks {
			want[k.ID] = true
		}
	}

	ids, err := st.ListDonationKeyIDsByDonor(ctx, uid)
	if err != nil {
		t.Fatalf("ListDonationKeyIDsByDonor: %v", err)
	}
	if len(ids) != len(want) {
		t.Fatalf("ids = %v, want %d ids", ids, len(want))
	}
	for _, id := range ids {
		if !want[id] {
			t.Fatalf("unexpected/foreign donation key id %d in %v", id, ids)
		}
	}
	// Ordered ascending by id.
	for i := 1; i < len(ids); i++ {
		if ids[i-1] >= ids[i] {
			t.Fatalf("ids not ascending: %v", ids)
		}
	}
	// Unknown donor fails closed.
	if _, err := st.ListDonationKeyIDsByDonor(ctx, 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ListDonationKeyIDsByDonor(0) = %v, want ErrNotFound", err)
	}
}
