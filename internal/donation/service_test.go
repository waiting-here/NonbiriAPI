package donation

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbtest"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

const donationTestNow int64 = 1_700_000_000

type donationTestAuth struct {
	denyOwner   atomic.Bool
	denyAdmin   atomic.Bool
	denySteward atomic.Bool
}

func (auth *donationTestAuth) AuthorizeUserMutation(ctx context.Context, tx *sql.Tx, userID int64) error {
	if auth.denyOwner.Load() {
		return ErrForbidden
	}
	return requireTestUser(ctx, tx, userID, false)
}

func (auth *donationTestAuth) AuthorizeAdminMutation(ctx context.Context, tx *sql.Tx, userID int64) error {
	if auth.denyAdmin.Load() {
		return ErrForbidden
	}
	var admin int
	if err := tx.QueryRowContext(ctx, `SELECT is_admin FROM users WHERE id=?`, userID).Scan(&admin); errors.Is(err, sql.ErrNoRows) {
		return ErrUnauthorized
	} else if err != nil {
		return err
	}
	if admin != 1 {
		return ErrForbidden
	}
	return nil
}

func (auth *donationTestAuth) AuthorizeStewardMutation(ctx context.Context, tx *sql.Tx, userID int64) error {
	if auth.denySteward.Load() {
		return ErrForbidden
	}
	return requireTestUser(ctx, tx, userID, true)
}

func requireTestUser(ctx context.Context, tx *sql.Tx, userID int64, steward bool) error {
	var admin, banned int
	var level sql.NullInt64
	err := tx.QueryRowContext(ctx, `SELECT is_admin,is_banned,level FROM users WHERE id=?`, userID).Scan(&admin, &banned, &level)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrUnauthorized
	}
	if err != nil {
		return err
	}
	if admin == 1 || banned == 1 || steward && (!level.Valid || level.Int64 != 5) {
		return ErrForbidden
	}
	return nil
}

type donationTestEnv struct {
	store   *db.Store
	vault   *secret.Vault
	service *Service
	auth    *donationTestAuth
	clock   *atomic.Int64
}

func newDonationTestEnv(t *testing.T) *donationTestEnv {
	t.Helper()
	master := bytes.Repeat([]byte{0x41}, secret.MasterKeyBytes)
	vault, err := secret.New(master)
	clear(master)
	if err != nil {
		t.Fatalf("secret.New: %v", err)
	}
	t.Cleanup(func() { _ = vault.Close() })
	path := filepath.Join(t.TempDir(), "donation.sqlite")
	dbtest.EnsureOwnerOnlyParent(t, path)
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.DB().Exec(`UPDATE site_config SET value='1' WHERE key IN ('donation_accept_enabled','charity_enabled')`); err != nil {
		t.Fatalf("enable donation config: %v", err)
	}
	clock := &atomic.Int64{}
	clock.Store(donationTestNow)
	auth := &donationTestAuth{}
	service, err := New(Config{Store: store, OwnerAuth: auth, RoleAuth: auth, CursorKeys: vault,
		Now: func() time.Time { return time.Unix(clock.Load(), 0) }})
	if err != nil {
		t.Fatalf("donation.New: %v", err)
	}
	return &donationTestEnv{store: store, vault: vault, service: service, auth: auth, clock: clock}
}

func (environment *donationTestEnv) seedUser(t *testing.T, discord string, level *int64, admin bool) int64 {
	t.Helper()
	zero := make([]byte, 16)
	var discordValue any = discord
	if discord == "" {
		discordValue = nil
	}
	result, err := environment.store.DB().Exec(`INSERT INTO users(
discord_id,username,is_admin,donation_credit_mag,level,total_requests,total_uncached_input_tokens,
total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,total_unknown_usage_requests,
revision,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		discordValue, "tester", boolInt(admin), zero, level, zero, zero, zero, zero, zero, zero, zero,
		donationTestNow, donationTestNow)
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("seed user id: %v", err)
	}
	return id
}

func (environment *donationTestEnv) seedEndpointKey(t *testing.T, userID int64, suffix byte) (int64, int64) {
	t.Helper()
	now := environment.clock.Load()
	baseURL := fmt.Sprintf("https://%c.example.test/v1", suffix)
	result, err := environment.store.DB().Exec(`INSERT INTO endpoints(
user_id,connector_type,base_url,note,enabled,revision,created_at,updated_at)
VALUES(?,'openai-compatible',?,'private endpoint note',1,1,?,?)`, userID, baseURL, now, now)
	if err != nil {
		t.Fatalf("seed endpoint: %v", err)
	}
	endpointID, _ := result.LastInsertId()
	contextID, fingerprint := make([]byte, 16), make([]byte, 32)
	contextID[15], fingerprint[31] = suffix, suffix
	result, err = environment.store.DB().Exec(`INSERT INTO endpoint_key_secrets(
context_id,canonical_base_url,connector_type,encrypted_secret,created_at)
VALUES(?,?,'openai-compatible','test-envelope',?)`, contextID, baseURL, now)
	if err != nil {
		t.Fatalf("seed secret: %v", err)
	}
	secretID, _ := result.LastInsertId()
	result, err = environment.store.DB().Exec(`INSERT INTO endpoint_keys(
endpoint_id,secret_ref_id,secret_fingerprint,display_head,display_tail,note,enabled,force_store_false,revision,created_at,updated_at)
VALUES(?,?,?,'head','tail','private key note',1,0,1,?,?)`, endpointID, secretID, fingerprint, now, now)
	if err != nil {
		t.Fatalf("seed endpoint key: %v", err)
	}
	keyID, _ := result.LastInsertId()
	return endpointID, keyID
}

func donationMutation(t *testing.T, seed byte, method, route string, ids []int64, body any) resources.ControlMutation {
	t.Helper()
	canonical, err := idempotency.CanonicalJSON(body)
	if err != nil {
		t.Fatalf("canonical mutation: %v", err)
	}
	pathIDs := make([]string, len(ids))
	for index, id := range ids {
		pathIDs[index] = fmt.Sprintf("%d", id)
	}
	return resources.ControlMutation{IdempotencyKey: strings.Repeat(string(seed), 22), Method: method,
		Route: route, PathIDs: pathIDs, CanonicalBody: canonical}
}

func (environment *donationTestEnv) createDonation(t *testing.T, userID int64, keyIDs ...int64) Donation {
	t.Helper()
	mutation := donationMutation(t, 'A', http.MethodPost, routeDonations, nil,
		map[string]any{"description": "donor description", "endpoint_key_ids": keyIDs, "ownership_authorized": true})
	result, err := environment.service.Create(context.Background(), userID, mutation,
		CreateInput{Description: "donor description", EndpointKeyIDs: keyIDs, OwnershipAuthorized: true})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return result.Value
}

func parseTestID(t *testing.T, value string) int64 {
	t.Helper()
	id, err := strconvParse(value)
	if err != nil {
		t.Fatalf("parse id %q: %v", value, err)
	}
	return id
}

func strconvParse(value string) (int64, error) { return parseCanonicalID(value) }

func TestCreateMembershipIsolationAndSequenceStartsAtOne(t *testing.T) {
	environment := newDonationTestEnv(t)
	owner := environment.seedUser(t, "owner", nil, false)
	foreign := environment.seedUser(t, "foreign", nil, false)
	_, keyA := environment.seedEndpointKey(t, owner, 'a')
	_, keyB := environment.seedEndpointKey(t, owner, 'b')
	_, foreignKey := environment.seedEndpointKey(t, foreign, 'c')

	duplicate := donationMutation(t, 'B', http.MethodPost, routeDonations, nil, map[string]any{"duplicate": true})
	if _, err := environment.service.Create(context.Background(), owner, duplicate,
		CreateInput{Description: "x", EndpointKeyIDs: []int64{keyA, keyA}, OwnershipAuthorized: true}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("duplicate create error = %v, want invalid request", err)
	}
	mixed := donationMutation(t, 'C', http.MethodPost, routeDonations, nil, map[string]any{"mixed": true})
	if _, err := environment.service.Create(context.Background(), owner, mixed,
		CreateInput{Description: "x", EndpointKeyIDs: []int64{keyA, foreignKey}, OwnershipAuthorized: true}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("mixed create error = %v, want non-leaking not found", err)
	}
	var count int
	if err := environment.store.DB().QueryRow(`SELECT COUNT(*) FROM donations`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("donation count after rejected creates = %d, err=%v", count, err)
	}

	donation := environment.createDonation(t, owner, keyA, keyB)
	if donation.Status != "pending" || donation.Revision != "1" || len(donation.Keys) != 2 {
		t.Fatalf("unexpected donation projection: %+v", donation)
	}
	one := db.EncodeU128(db.U128{15: 1})
	rows, err := environment.store.DB().Query(`SELECT next_claim_seq,next_fold_seq FROM donation_keys WHERE donation_id=? ORDER BY id`, parseTestID(t, donation.ID))
	if err != nil {
		t.Fatalf("read sequences: %v", err)
	}
	defer rows.Close()
	seen := 0
	for rows.Next() {
		var claimSeq, foldSeq []byte
		if err := rows.Scan(&claimSeq, &foldSeq); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(claimSeq, one) || !bytes.Equal(foldSeq, one) {
			t.Fatalf("sequence = %x/%x, want one", claimSeq, foldSeq)
		}
		seen++
	}
	if seen != 2 {
		t.Fatalf("sequence rows = %d, want 2", seen)
	}
}

func TestReviewSuspensionPrivacyAndFinalTransactionAuthorization(t *testing.T) {
	environment := newDonationTestEnv(t)
	ownerLevel := int64(5)
	owner := environment.seedUser(t, "123456789", &ownerLevel, false)
	environment.seedUser(t, "", nil, true)
	_, endpointKeyID := environment.seedEndpointKey(t, owner, 'd')
	donation := environment.createDonation(t, owner, endpointKeyID)
	donationID, donationKeyID := parseTestID(t, donation.ID), parseTestID(t, donation.Keys[0].ID)

	caseID, err := db.GenerateOpaqueID("rpc_")
	if err != nil {
		t.Fatal(err)
	}
	fingerprint := make([]byte, 32)
	fingerprint[0] = 1
	if _, err := environment.store.DB().Exec(`INSERT INTO report_cases(
id,fingerprint,connector_type,canonical_base_url,status,progress_state,material_version,target_version,
deadline,material_count,target_count,distinct_owner_count,created_at)
VALUES(?,?,'openai-compatible','https://reported.example.test/v1','pending_review','complete',1,1,?,0,0,0,?)`,
		caseID, fingerprint, donationTestNow+1000, donationTestNow); err != nil {
		t.Fatalf("seed report case: %v", err)
	}
	if _, err := environment.store.DB().Exec(`INSERT INTO endpoint_key_suspensions(
endpoint_key_id,reason_type,report_case_id,created_at) VALUES(?,'report_case',?,?)`, endpointKeyID, caseID, donationTestNow); err != nil {
		t.Fatalf("seed suspension: %v", err)
	}
	review := ReviewInput{Decision: "approve", ExpectedRevision: 1, Reason: "safe review", KeySettings: []KeySetting{{
		DonationKeyID: donationKeyID, PriceLimit: pointerString("10"), CallsLimit: pointerString("10"),
		TokensLimit: pointerString("100"), TokenReserve: 10, Enabled: true, SafeNote: "reviewer label",
	}}}
	mutation := donationMutation(t, 'D', http.MethodPost, routeAdminReview, []int64{donationID}, map[string]any{"review": 1})
	if _, err := environment.service.ReviewAdmin(context.Background(), mutation, donationID, review); !errors.Is(err, ErrConflict) {
		t.Fatalf("suspended approval error = %v, want conflict", err)
	}
	var status string
	var revision int64
	if err := environment.store.DB().QueryRow(`SELECT status,revision FROM donations WHERE id=?`, donationID).Scan(&status, &revision); err != nil || status != "pending" || revision != 1 {
		t.Fatalf("suspended approval mutated state: status=%s revision=%d err=%v", status, revision, err)
	}
	if _, err := environment.store.DB().Exec(`DELETE FROM endpoint_key_suspensions WHERE endpoint_key_id=?`, endpointKeyID); err != nil {
		t.Fatal(err)
	}
	mutation = donationMutation(t, 'E', http.MethodPost, routeAdminReview, []int64{donationID}, map[string]any{"review": 2})
	approved, err := environment.service.ReviewAdmin(context.Background(), mutation, donationID, review)
	if err != nil {
		t.Fatalf("ReviewAdmin: %v", err)
	}
	if approved.Value.Keys[0].SafeNote != "reviewer label" || approved.Value.Owner == nil || approved.Value.Owner.DiscordID == nil {
		t.Fatalf("admin projection missing review facts: %+v", approved.Value)
	}
	ownerView, err := environment.service.GetOwner(context.Background(), owner, donationID)
	if err != nil {
		t.Fatal(err)
	}
	ownerJSON, _ := json.Marshal(ownerView)
	if bytes.Contains(ownerJSON, []byte("safe_note")) || bytes.Contains(ownerJSON, []byte("reviewer label")) {
		t.Fatalf("owner projection leaked safe note: %s", ownerJSON)
	}
	stewardView, err := environment.service.GetSteward(context.Background(), owner, donationID)
	if err != nil {
		t.Fatal(err)
	}
	stewardJSON, _ := json.Marshal(stewardView)
	if bytes.Contains(stewardJSON, []byte("discord_id")) || !bytes.Contains(stewardJSON, []byte("reviewer label")) {
		t.Fatalf("steward privacy projection mismatch: %s", stewardJSON)
	}

	environment.auth.denySteward.Store(true)
	enabled := false
	keyMutation := donationMutation(t, 'F', http.MethodPatch, routeStewardKey, []int64{donationID, donationKeyID}, map[string]any{"enabled": false})
	if _, err := environment.service.ManageKeySteward(context.Background(), owner, donationID, donationKeyID, keyMutation,
		KeyManagementInput{ExpectedRevision: 2, Enabled: &enabled}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("revoked steward mutation error = %v, want forbidden", err)
	}
}

func TestEndpointKeyDeletionPartialThenFinalAndGenerationRestart(t *testing.T) {
	environment := newDonationTestEnv(t)
	owner := environment.seedUser(t, "owner-delete", nil, false)
	_, keyA := environment.seedEndpointKey(t, owner, 'e')
	_, keyB := environment.seedEndpointKey(t, owner, 'f')
	donation := environment.createDonation(t, owner, keyA, keyB)
	donationID := parseTestID(t, donation.ID)

	deleteInTx := func(keyID int64) {
		t.Helper()
		tx, err := environment.store.DB().Begin()
		if err != nil {
			t.Fatal(err)
		}
		if err := environment.service.PrepareEndpointKeyDeletion(context.Background(), tx, owner, []int64{keyID}, donationTestNow); err != nil {
			_ = tx.Rollback()
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	deleteInTx(keyA)
	var status string
	var revision int64
	var memberships int
	if err := environment.store.DB().QueryRow(`SELECT status,revision FROM donations WHERE id=?`, donationID).Scan(&status, &revision); err != nil {
		t.Fatal(err)
	}
	if err := environment.store.DB().QueryRow(`SELECT COUNT(*) FROM donation_key_memberships WHERE donation_id=?`, donationID).Scan(&memberships); err != nil {
		t.Fatal(err)
	}
	if status != "pending" || revision != 2 || memberships != 1 {
		t.Fatalf("partial delete = status %s rev %d members %d", status, revision, memberships)
	}
	deleteInTx(keyB)
	if err := environment.store.DB().QueryRow(`SELECT status,revision FROM donations WHERE id=?`, donationID).Scan(&status, &revision); err != nil {
		t.Fatal(err)
	}
	if status != "deleted" || revision != 3 {
		t.Fatalf("final delete = status %s rev %d", status, revision)
	}

	// Independently exercise failure-disabled restart on a live approved key.
	_, keyC := environment.seedEndpointKey(t, owner, 'g')
	live := environment.createDonationWithSeed(t, owner, 'G', keyC)
	liveID, liveKeyID := parseTestID(t, live.ID), parseTestID(t, live.Keys[0].ID)
	environment.seedUser(t, "", nil, true)
	review := ReviewInput{Decision: "approve", ExpectedRevision: 1, Reason: "ok", KeySettings: []KeySetting{{DonationKeyID: liveKeyID, Enabled: true}}}
	if _, err := environment.service.ReviewAdmin(context.Background(), donationMutation(t, 'H', http.MethodPost, routeAdminReview, []int64{liveID}, map[string]any{"approve": true}), liveID, review); err != nil {
		t.Fatal(err)
	}
	ten := db.EncodeU128(db.U128{15: 10})
	if _, err := environment.store.DB().Exec(`UPDATE donation_keys SET failure_disabled=1,failure_streak=? WHERE id=?`, ten, liveKeyID); err != nil {
		t.Fatal(err)
	}
	enabled := true
	if _, err := environment.service.ManageKeyAdmin(context.Background(), liveID, liveKeyID,
		donationMutation(t, 'I', http.MethodPatch, routeAdminKey, []int64{liveID, liveKeyID}, map[string]any{"enabled": true}),
		KeyManagementInput{ExpectedRevision: 2, Enabled: &enabled}); err != nil {
		t.Fatal(err)
	}
	var generation, claimSeq, foldSeq, streak []byte
	var disabled int
	if err := environment.store.DB().QueryRow(`SELECT streak_generation,next_claim_seq,next_fold_seq,failure_streak,failure_disabled
FROM donation_keys WHERE id=?`, liveKeyID).Scan(&generation, &claimSeq, &foldSeq, &streak, &disabled); err != nil {
		t.Fatal(err)
	}
	if testU128Decimal(t, generation) != "2" || testU128Decimal(t, claimSeq) != "1" ||
		testU128Decimal(t, foldSeq) != "1" || testU128Decimal(t, streak) != "0" || disabled != 0 {
		t.Fatalf("restart state generation=%x claim=%x fold=%x streak=%x disabled=%d", generation, claimSeq, foldSeq, streak, disabled)
	}
}

func (environment *donationTestEnv) createDonationWithSeed(t *testing.T, userID int64, seed byte, keyIDs ...int64) Donation {
	t.Helper()
	mutation := donationMutation(t, seed, http.MethodPost, routeDonations, nil, map[string]any{"seed": string(seed)})
	result, err := environment.service.Create(context.Background(), userID, mutation,
		CreateInput{Description: "donor description", EndpointKeyIDs: keyIDs, OwnershipAuthorized: true})
	if err != nil {
		t.Fatal(err)
	}
	return result.Value
}

func pointerString(value string) *string { return &value }

func testU128Decimal(t *testing.T, value []byte) string {
	t.Helper()
	decoded, err := db.DecodeU128(value)
	if err != nil {
		t.Fatalf("decode U128: %v", err)
	}
	return decoded.Decimal()
}

func TestStewardOwnershipPrecedesDueExpiryAndMutationAcceptance(t *testing.T) {
	environment := newDonationTestEnv(t)
	levelFive := int64(5)
	owner := environment.seedUser(t, "due-owner", &levelFive, false)
	foreignSteward := environment.seedUser(t, "foreign-steward", &levelFive, false)
	environment.seedUser(t, "", nil, true)
	_, endpointKeyID := environment.seedEndpointKey(t, owner, 'i')
	donation := environment.createDonationWithSeed(t, owner, 'M', endpointKeyID)
	donationID, donationKeyID := parseTestID(t, donation.ID), parseTestID(t, donation.Keys[0].ID)
	expires := donationTestNow + 10
	approval := ReviewInput{Decision: "approve", ExpectedRevision: 1, Reason: "expires soon", ExpiresAt: &expires,
		KeySettings: []KeySetting{{DonationKeyID: donationKeyID, Enabled: true, SafeNote: "unchanged"}}}
	if _, err := environment.service.ReviewAdmin(context.Background(),
		donationMutation(t, 'N', http.MethodPost, routeAdminReview, []int64{donationID}, map[string]any{"expires": expires}),
		donationID, approval); err != nil {
		t.Fatal(err)
	}
	environment.clock.Store(expires)

	var reviewsBefore int
	if err := environment.store.DB().QueryRow(`SELECT COUNT(*) FROM donation_reviews WHERE donation_id=?`, donationID).
		Scan(&reviewsBefore); err != nil {
		t.Fatal(err)
	}
	reviewMutation := donationMutation(t, 'O', http.MethodPost, routeStewardReview, []int64{donationID},
		map[string]any{"foreign_review": true})
	foreignReview := ReviewInput{Decision: "approve", ExpectedRevision: 2, Reason: "foreign",
		KeySettings: []KeySetting{{DonationKeyID: donationKeyID, Enabled: false}}}
	if _, err := environment.service.ReviewSteward(context.Background(), foreignSteward, donationID,
		reviewMutation, foreignReview); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign due review error = %v, want not found", err)
	}
	disable := false
	keyMutation := donationMutation(t, 'P', http.MethodPatch, routeStewardKey, []int64{donationID, donationKeyID},
		map[string]any{"foreign_key": true})
	if _, err := environment.service.ManageKeySteward(context.Background(), foreignSteward, donationID, donationKeyID,
		keyMutation, KeyManagementInput{ExpectedRevision: 2, Enabled: &disable}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign due key mutation error = %v, want not found", err)
	}

	var status string
	var revision, memberships, enabled, reviewsAfter int
	if err := environment.store.DB().QueryRow(`SELECT status,revision FROM donations WHERE id=?`, donationID).
		Scan(&status, &revision); err != nil {
		t.Fatal(err)
	}
	if err := environment.store.DB().QueryRow(`SELECT COUNT(*) FROM donation_key_memberships WHERE donation_id=?`, donationID).
		Scan(&memberships); err != nil {
		t.Fatal(err)
	}
	if err := environment.store.DB().QueryRow(`SELECT enabled FROM donation_keys WHERE id=?`, donationKeyID).
		Scan(&enabled); err != nil {
		t.Fatal(err)
	}
	if err := environment.store.DB().QueryRow(`SELECT COUNT(*) FROM donation_reviews WHERE donation_id=?`, donationID).
		Scan(&reviewsAfter); err != nil {
		t.Fatal(err)
	}
	if status != "approved" || revision != 2 || memberships != 1 || enabled != 1 || reviewsAfter != reviewsBefore {
		t.Fatalf("foreign requests changed donation: status=%q revision=%d memberships=%d enabled=%d reviews=%d/%d",
			status, revision, memberships, enabled, reviewsBefore, reviewsAfter)
	}
	if count := mutationRecordCount(t, environment.store.DB(), idempotency.ScopeControlMutation,
		string(reviewerSteward), foreignSteward, reviewMutation.IdempotencyKey); count != 0 {
		t.Fatalf("foreign review idempotency records = %d, want 0", count)
	}
	if count := mutationRecordCount(t, environment.store.DB(), idempotency.ScopeControlMutation,
		string(reviewerSteward), foreignSteward, keyMutation.IdempotencyKey); count != 0 {
		t.Fatalf("foreign key idempotency records = %d, want 0", count)
	}

	_, ownEndpointKeyID := environment.seedEndpointKey(t, foreignSteward, 'j')
	ownDonation := environment.createDonationWithSeed(t, foreignSteward, 'Q', ownEndpointKeyID)
	ownDonationID := parseTestID(t, ownDonation.ID)
	ownDonationKeyID := parseTestID(t, ownDonation.Keys[0].ID)
	ownMutation := donationMutation(t, 'R', http.MethodPost, routeStewardReview, []int64{ownDonationID},
		map[string]any{"owned_review": true})
	ownReview := ReviewInput{Decision: "approve", ExpectedRevision: 1, Reason: "owned",
		KeySettings: []KeySetting{{DonationKeyID: ownDonationKeyID, Enabled: true}}}
	first, err := environment.service.ReviewSteward(context.Background(), foreignSteward, ownDonationID, ownMutation, ownReview)
	if err != nil || first.Replayed {
		t.Fatalf("owned steward review = %+v, %v", first, err)
	}
	replayed, err := environment.service.ReviewSteward(context.Background(), foreignSteward, ownDonationID, ownMutation, ownReview)
	if err != nil || !replayed.Replayed || replayed.Status != first.Status || !bytes.Equal(replayed.Body, first.Body) {
		t.Fatalf("owned steward replay = %+v, %v", replayed, err)
	}
	if _, err := environment.store.DB().Exec(`UPDATE donations SET expires_at=? WHERE id=?`,
		environment.clock.Load(), ownDonationID); err != nil {
		t.Fatal(err)
	}
	dueReplay, err := environment.service.ReviewSteward(context.Background(), foreignSteward, ownDonationID, ownMutation, ownReview)
	if err != nil || !dueReplay.Replayed || !bytes.Equal(dueReplay.Body, first.Body) {
		t.Fatalf("owned due steward replay = %+v, %v", dueReplay, err)
	}
	var ownStatus string
	if err := environment.store.DB().QueryRow(`SELECT status FROM donations WHERE id=?`, ownDonationID).Scan(&ownStatus); err != nil {
		t.Fatal(err)
	}
	if ownStatus != "approved" {
		t.Fatalf("exact replay materialized expiry: status=%q", ownStatus)
	}
	environment.auth.denySteward.Store(true)
	if _, err := environment.service.ReviewSteward(context.Background(), foreignSteward, ownDonationID,
		ownMutation, ownReview); !errors.Is(err, ErrForbidden) {
		t.Fatalf("revoked steward replay error = %v, want forbidden", err)
	}
	environment.auth.denySteward.Store(false)
	ownKeyMutation := donationMutation(t, 'S', http.MethodPatch, routeStewardKey,
		[]int64{ownDonationID, ownDonationKeyID}, map[string]any{"owned_due_key": true})
	if _, err := environment.service.ManageKeySteward(context.Background(), foreignSteward, ownDonationID,
		ownDonationKeyID, ownKeyMutation, KeyManagementInput{ExpectedRevision: 2, Enabled: &disable}); !errors.Is(err, ErrConflict) {
		t.Fatalf("owned due key mutation error = %v, want conflict", err)
	}
	if err := environment.store.DB().QueryRow(`SELECT status FROM donations WHERE id=?`, ownDonationID).Scan(&ownStatus); err != nil {
		t.Fatal(err)
	}
	if ownStatus != "expired" {
		t.Fatalf("owned due key mutation status = %q, want expired", ownStatus)
	}
	if count := mutationRecordCount(t, environment.store.DB(), idempotency.ScopeControlMutation,
		string(reviewerSteward), foreignSteward, ownKeyMutation.IdempotencyKey); count != 0 {
		t.Fatalf("owned due key idempotency records = %d, want 0", count)
	}
}

func TestTerminateExpiryPrecedenceReplayAndAuthorization(t *testing.T) {
	environment := newDonationTestEnv(t)
	owner := environment.seedUser(t, "terminate-owner", nil, false)
	environment.seedUser(t, "", nil, true)
	expires := donationTestNow + 10

	seedApproved := func(endpointSuffix, createSeed, reviewSeed byte) (int64, int64) {
		t.Helper()
		_, endpointKeyID := environment.seedEndpointKey(t, owner, endpointSuffix)
		donation := environment.createDonationWithSeed(t, owner, createSeed, endpointKeyID)
		donationID := parseTestID(t, donation.ID)
		donationKeyID := parseTestID(t, donation.Keys[0].ID)
		review := ReviewInput{Decision: "approve", ExpectedRevision: 1, Reason: "termination boundary", ExpiresAt: &expires,
			KeySettings: []KeySetting{{DonationKeyID: donationKeyID, Enabled: true}}}
		if _, err := environment.service.ReviewAdmin(context.Background(),
			donationMutation(t, reviewSeed, http.MethodPost, routeAdminReview, []int64{donationID}, map[string]any{"expires": expires}),
			donationID, review); err != nil {
			t.Fatal(err)
		}
		return donationID, donationKeyID
	}
	beforeID, _ := seedApproved('k', 'S', 'T')
	equalID, equalKeyID := seedApproved('l', 'U', 'V')
	afterID, afterKeyID := seedApproved('m', 'W', 'X')
	input := TerminateInput{ExpectedRevision: 2, Confirmation: "terminate"}

	environment.clock.Store(expires - 1)
	beforeMutation := donationMutation(t, 'Y', http.MethodPost, routeTerminate, []int64{beforeID},
		map[string]any{"confirmation": input.Confirmation, "expected_revision": input.ExpectedRevision})
	terminated, err := environment.service.Terminate(context.Background(), owner, beforeID, beforeMutation, input)
	if err != nil || terminated.Replayed || terminated.Value.Status != "deleted" || terminated.Value.Revision != "3" {
		t.Fatalf("terminate at expiry -1 = %+v, %v", terminated, err)
	}
	environment.clock.Store(expires)
	replayed, err := environment.service.Terminate(context.Background(), owner, beforeID, beforeMutation, input)
	if err != nil || !replayed.Replayed || replayed.Status != terminated.Status || !bytes.Equal(replayed.Body, terminated.Body) {
		t.Fatalf("terminate replay at expiry = %+v, %v", replayed, err)
	}
	if count := mutationRecordCount(t, environment.store.DB(), idempotency.ScopeDonation,
		"user", owner, beforeMutation.IdempotencyKey); count != 1 {
		t.Fatalf("successful terminate idempotency records = %d, want 1", count)
	}
	environment.auth.denyOwner.Store(true)
	if _, err := environment.service.Terminate(context.Background(), owner, beforeID, beforeMutation, input); !errors.Is(err, ErrForbidden) {
		t.Fatalf("revoked owner replay error = %v, want forbidden", err)
	}
	environment.auth.denyOwner.Store(false)

	assertExpiryWins := func(donationID, donationKeyID int64, mutation resources.ControlMutation, decisionNow int64) {
		t.Helper()
		environment.clock.Store(decisionNow)
		if _, err := environment.service.Terminate(context.Background(), owner, donationID, mutation, input); !errors.Is(err, ErrConflict) {
			t.Fatalf("terminate at %d error = %v, want conflict", decisionNow, err)
		}
		var status, endedReason string
		var revision, memberships, expiryReviews, terminateReviews int
		if err := environment.store.DB().QueryRow(`SELECT status,revision FROM donations WHERE id=?`, donationID).
			Scan(&status, &revision); err != nil {
			t.Fatal(err)
		}
		if err := environment.store.DB().QueryRow(`SELECT ended_reason FROM donation_keys WHERE id=?`, donationKeyID).
			Scan(&endedReason); err != nil {
			t.Fatal(err)
		}
		if err := environment.store.DB().QueryRow(`SELECT COUNT(*) FROM donation_key_memberships WHERE donation_id=?`, donationID).
			Scan(&memberships); err != nil {
			t.Fatal(err)
		}
		if err := environment.store.DB().QueryRow(`SELECT COUNT(*) FROM donation_reviews WHERE donation_id=? AND action='expire'`, donationID).
			Scan(&expiryReviews); err != nil {
			t.Fatal(err)
		}
		if err := environment.store.DB().QueryRow(`SELECT COUNT(*) FROM donation_reviews WHERE donation_id=? AND action='terminate'`, donationID).
			Scan(&terminateReviews); err != nil {
			t.Fatal(err)
		}
		if status != "expired" || revision != 3 || endedReason != "expired" || memberships != 0 ||
			expiryReviews != 1 || terminateReviews != 0 {
			t.Fatalf("expiry winner state=%q revision=%d ended=%q memberships=%d reviews expire/terminate=%d/%d",
				status, revision, endedReason, memberships, expiryReviews, terminateReviews)
		}
		if count := mutationRecordCount(t, environment.store.DB(), idempotency.ScopeDonation,
			"user", owner, mutation.IdempotencyKey); count != 0 {
			t.Fatalf("due terminate idempotency records = %d, want 0", count)
		}
		if _, err := environment.service.Terminate(context.Background(), owner, donationID, mutation, input); !errors.Is(err, ErrConflict) {
			t.Fatalf("due terminate replay error = %v, want stable conflict", err)
		}
		if count := mutationRecordCount(t, environment.store.DB(), idempotency.ScopeDonation,
			"user", owner, mutation.IdempotencyKey); count != 0 {
			t.Fatalf("repeated due terminate idempotency records = %d, want 0", count)
		}
		authoritative, err := environment.service.GetOwner(context.Background(), owner, donationID)
		if err != nil || authoritative.Status != "expired" || authoritative.Revision != "3" {
			t.Fatalf("authoritative expired projection = %+v, %v", authoritative, err)
		}
	}

	equalMutation := donationMutation(t, 'Z', http.MethodPost, routeTerminate, []int64{equalID},
		map[string]any{"confirmation": input.Confirmation, "expected_revision": input.ExpectedRevision})
	assertExpiryWins(equalID, equalKeyID, equalMutation, expires)
	afterMutation := donationMutation(t, 'a', http.MethodPost, routeTerminate, []int64{afterID},
		map[string]any{"confirmation": input.Confirmation, "expected_revision": input.ExpectedRevision})
	assertExpiryWins(afterID, afterKeyID, afterMutation, expires+1)
}

func mutationRecordCount(
	t *testing.T,
	database *sql.DB,
	scope idempotency.Scope,
	actorKind string,
	actorID int64,
	key string,
) int {
	t.Helper()
	actorHash, err := idempotency.ActorScopeHash(actorKind, fmt.Sprintf("%d", actorID))
	if err != nil {
		t.Fatal(err)
	}
	keyHash, err := idempotency.KeyHash(key)
	if err != nil {
		t.Fatal(err)
	}
	var count int
	if err := database.QueryRow(`SELECT COUNT(*) FROM idempotency_records
WHERE scope=? AND actor_scope_hash=? AND key_hash=?`, string(scope), actorHash[:], keyHash[:]).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestExpiryAccountDeletionAndRetention(t *testing.T) {
	environment := newDonationTestEnv(t)
	owner := environment.seedUser(t, "owner-expiry", nil, false)
	environment.seedUser(t, "", nil, true)
	_, keyID := environment.seedEndpointKey(t, owner, 'h')
	donation := environment.createDonation(t, owner, keyID)
	donationID, donationKeyID := parseTestID(t, donation.ID), parseTestID(t, donation.Keys[0].ID)
	expires := donationTestNow + 1
	review := ReviewInput{Decision: "approve", ExpectedRevision: 1, Reason: "expiry", ExpiresAt: &expires,
		KeySettings: []KeySetting{{DonationKeyID: donationKeyID, Enabled: true, SafeNote: "scrub me"}}}
	if _, err := environment.service.ReviewAdmin(context.Background(),
		donationMutation(t, 'J', http.MethodPost, routeAdminReview, []int64{donationID}, map[string]any{"expires": expires}), donationID, review); err != nil {
		t.Fatal(err)
	}
	environment.clock.Store(expires - 1)
	if value, err := environment.service.GetOwner(context.Background(), owner, donationID); err != nil || value.Status != "approved" {
		t.Fatalf("expiry -1 = %+v, %v", value, err)
	}
	environment.clock.Store(expires)
	if value, err := environment.service.GetOwner(context.Background(), owner, donationID); err != nil || value.Status != "expired" {
		t.Fatalf("expiry equality = %+v, %v", value, err)
	}
	exportTx, err := environment.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	exported, err := environment.service.ExportUser(context.Background(), exportTx, owner, 10)
	if err != nil {
		_ = exportTx.Rollback()
		t.Fatal(err)
	}
	if err := exportTx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if len(exported) != 1 || exported[0].ReviewResult == nil || exported[0].ReviewResult.Reason != "expiry" {
		t.Fatalf("exported review result = %+v", exported)
	}
	exportJSON, err := json.Marshal(exported)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(exportJSON, []byte("safe_note")) || bytes.Contains(exportJSON, []byte("scrub me")) {
		t.Fatalf("owner export leaked safe note: %s", exportJSON)
	}
	var memberships int
	if err := environment.store.DB().QueryRow(`SELECT COUNT(*) FROM donation_key_memberships WHERE donation_id=?`, donationID).Scan(&memberships); err != nil || memberships != 0 {
		t.Fatalf("expired memberships = %d, err=%v", memberships, err)
	}

	tx, err := environment.store.DB().Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := environment.service.PrepareAccountDeletion(context.Background(), tx, owner, expires+1); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var user sql.NullInt64
	var description, note string
	if err := environment.store.DB().QueryRow(`SELECT user_id,description FROM donations WHERE id=?`, donationID).Scan(&user, &description); err != nil {
		t.Fatal(err)
	}
	if err := environment.store.DB().QueryRow(`SELECT safe_note FROM donation_keys WHERE id=?`, donationKeyID).Scan(&note); err != nil {
		t.Fatal(err)
	}
	if user.Valid || description != "" || note != "" {
		t.Fatalf("account deletion did not scrub: user=%v description=%q note=%q", user, description, note)
	}
	claimID, err := db.GenerateOpaqueID("clm_")
	if err != nil {
		t.Fatal(err)
	}
	one := db.EncodeU128(db.U128{15: 1})
	if _, err := environment.store.DB().Exec(`INSERT INTO donation_usage_reservations(
claim_id,donation_key_id,streak_generation,claim_seq,price_reserved_milli,calls_reserved,tokens_reserved,state,created_at)
VALUES(?,?,?,?,0,0,0,'reserved',?)`, claimID, donationKeyID, one, one, expires); err != nil {
		t.Fatal(err)
	}
	cleaned, err := environment.service.Cleanup(context.Background(), expires+terminalRetention, 10)
	if err != nil || cleaned != 0 {
		t.Fatalf("Cleanup with reserved usage = %d, %v", cleaned, err)
	}
	if _, err := environment.store.DB().Exec(`UPDATE donation_usage_reservations
SET state='released',finalized_at=? WHERE claim_id=?`, expires+1, claimID); err != nil {
		t.Fatal(err)
	}
	cleaned, err = environment.service.Cleanup(context.Background(), expires+terminalRetention, 10)
	if err != nil || cleaned != 1 {
		t.Fatalf("Cleanup = %d, %v", cleaned, err)
	}
}
