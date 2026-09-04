package charity

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"math/big"
	"path/filepath"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/claim"
	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbfixture"
	"github.com/waiting-here/NonbiriAPI/internal/donation"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

const charityTestNow int64 = 1_700_000_000

type charityTestEnv struct {
	store        *db.Store
	service      *Service
	donation     *donation.Service
	donorID      int64
	callerID     int64
	endpointID   int64
	endpointKey  int64
	secretID     int64
	donationID   int64
	donationKey  int64
	requestModel int64
	tokenModel   int64
}

func newCharityTestEnv(t *testing.T) *charityTestEnv {
	t.Helper()
	master := bytes.Repeat([]byte{0x43}, secret.MasterKeyBytes)
	vault, err := secret.New(master)
	clear(master)
	if err != nil {
		t.Fatalf("secret.New: %v", err)
	}
	t.Cleanup(func() { _ = vault.Close() })
	path := filepath.Join(t.TempDir(), "charity.sqlite")
	dbfixture.Materialize(t, path)
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.DB().Exec(`UPDATE site_config SET value='1'
WHERE key IN ('charity_enabled','donation_accept_enabled')`); err != nil {
		t.Fatalf("enable charity config: %v", err)
	}
	if _, err := store.DB().Exec(`INSERT INTO site_config(key,value,updated_at)
VALUES('charity_token_reserve_milli','5',?) ON CONFLICT(key) DO UPDATE SET value='5',updated_at=excluded.updated_at`,
		charityTestNow); err != nil {
		t.Fatalf("set charity token reserve: %v", err)
	}
	donationService, err := donation.New(donation.Config{Store: store, Now: func() time.Time {
		return time.Unix(charityTestNow, 0)
	}})
	if err != nil {
		t.Fatalf("donation.New: %v", err)
	}
	service, err := New(Config{Store: store, KeyDeletion: donationService, Now: func() time.Time {
		return time.Unix(charityTestNow, 0)
	}})
	if err != nil {
		t.Fatalf("charity.New: %v", err)
	}
	environment := &charityTestEnv{store: store, service: service, donation: donationService}
	environment.donorID = environment.seedUser(t, "charity-donor")
	environment.callerID = environment.seedUser(t, "charity-caller")
	environment.seedDonationRoute(t)
	environment.requestModel = environment.seedModel(t, "request", "per_request")
	environment.tokenModel = environment.seedModel(t, "token", "per_token")
	return environment
}

func (environment *charityTestEnv) seedUser(t *testing.T, discord string) int64 {
	t.Helper()
	zero := db.EncodeU128(db.U128{})
	result, err := environment.store.DB().Exec(`INSERT INTO users(
discord_id,username,is_admin,donation_credit_mag,total_requests,total_uncached_input_tokens,
total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,total_unknown_usage_requests,
revision,created_at,updated_at) VALUES(?,?,0,?,?,?,?,?,?,?,?,?,?)`, discord, discord, zero,
		zero, zero, zero, zero, zero, zero, zero, charityTestNow, charityTestNow)
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	id, _ := result.LastInsertId()
	return id
}

func (environment *charityTestEnv) seedDonationRoute(t *testing.T) {
	t.Helper()
	baseURL := "https://charity.example.test/v1"
	result, err := environment.store.DB().Exec(`INSERT INTO endpoints(
user_id,connector_type,base_url,note,enabled,revision,created_at,updated_at)
VALUES(?,'openai-compatible',?,'private endpoint',1,1,?,?)`, environment.donorID, baseURL,
		charityTestNow, charityTestNow)
	if err != nil {
		t.Fatalf("seed endpoint: %v", err)
	}
	environment.endpointID, _ = result.LastInsertId()
	contextID, fingerprint := make([]byte, 16), make([]byte, 32)
	contextID[15], fingerprint[31] = 1, 1
	result, err = environment.store.DB().Exec(`INSERT INTO endpoint_key_secrets(
context_id,canonical_base_url,connector_type,encrypted_secret,created_at)
VALUES(?,?,'openai-compatible','test-envelope',?)`, contextID, baseURL, charityTestNow)
	if err != nil {
		t.Fatalf("seed endpoint secret: %v", err)
	}
	environment.secretID, _ = result.LastInsertId()
	result, err = environment.store.DB().Exec(`INSERT INTO endpoint_keys(
endpoint_id,secret_ref_id,secret_fingerprint,display_head,display_tail,note,enabled,force_store_false,
revision,created_at,updated_at) VALUES(?,?,?,'head','tail','private key',1,0,1,?,?)`,
		environment.endpointID, environment.secretID, fingerprint, charityTestNow, charityTestNow)
	if err != nil {
		t.Fatalf("seed endpoint key: %v", err)
	}
	environment.endpointKey, _ = result.LastInsertId()
	result, err = environment.store.DB().Exec(`INSERT INTO donations(
user_id,status,revision,description,review_note,reviewed_by_role,created_at,updated_at)
VALUES(?,'approved',1,'donation','', 'admin',?,?)`, environment.donorID, charityTestNow, charityTestNow)
	if err != nil {
		t.Fatalf("seed donation: %v", err)
	}
	environment.donationID, _ = result.LastInsertId()
	zero, one := u128Blob(t, 0), u128Blob(t, 1)
	result, err = environment.store.DB().Exec(`INSERT INTO donation_keys(
donation_id,endpoint_key_id,display_head,display_tail,canonical_base_url,connector_type,
price_used_mag,price_reserved_mag,calls_used,calls_reserved,tokens_used,tokens_reserved,
token_reserve,enabled,failure_streak,streak_generation,next_claim_seq,next_fold_seq,safe_note,created_at,updated_at,
authorized_expires_at,expires_at,source_endpoint_key_id,report_fingerprint)
VALUES(?,?,?,?,?,'openai-compatible',?,?,?,?,?,?,5,1,?,?,?,?,'private label',?,?,NULL,NULL,?,?)`,
		environment.donationID, environment.endpointKey, "head", "tail", baseURL,
		zero, zero, zero, zero, zero, zero, zero, one, one, one, charityTestNow, charityTestNow,
		environment.endpointKey, fingerprint)
	if err != nil {
		t.Fatalf("seed donation key: %v", err)
	}
	environment.donationKey, _ = result.LastInsertId()
	if _, err := environment.store.DB().Exec(`INSERT INTO donation_key_memberships(
endpoint_key_id,donation_key_id,donation_id,created_at) VALUES(?,?,?,?)`, environment.endpointKey,
		environment.donationKey, environment.donationID, charityTestNow); err != nil {
		t.Fatalf("seed donation membership: %v", err)
	}
	if _, err := environment.store.DB().Exec(`INSERT INTO model_pair_catalog(
endpoint_key_id,normalized_model_id,automatic_supports,manual_supports,automatic_revision,pair_revision,updated_at)
VALUES(?,'upstream-model',1,0,1,1,?)`, environment.endpointKey, charityTestNow); err != nil {
		t.Fatalf("seed model pair: %v", err)
	}
}

func (environment *charityTestEnv) seedModel(t *testing.T, model, mode string) int64 {
	t.Helper()
	requestPrice, requestReward := int64(0), int64(0)
	uncachedPrice, uncachedReward := int64(0), int64(0)
	if mode == "per_request" {
		requestPrice, requestReward = 3000, 1250
	} else {
		uncachedPrice, uncachedReward = 4_000_000, 2_000_000
	}
	result, err := environment.store.DB().Exec(`INSERT INTO charity_models(
provider,model,full_name,enabled,pricing_mode,request_user_price,request_donor_reward,
uncached_user_price,uncached_donor_reward,discount_percent,discount_enabled,discount_start_at,
discount_end_at,revision,binding_revision,created_at,updated_at)
VALUES('provider',?,?,1,?,?,?,?,?,80,1,?,?,1,1,?,?)`, model, "[公益]provider/"+model, mode,
		requestPrice, requestReward, uncachedPrice, uncachedReward, charityTestNow-1, charityTestNow+1000,
		charityTestNow, charityTestNow)
	if err != nil {
		t.Fatalf("seed charity model: %v", err)
	}
	modelID, _ := result.LastInsertId()
	if _, err := environment.store.DB().Exec(`INSERT INTO charity_model_bindings(
charity_model_id,donation_key_id,endpoint_key_id,upstream_model_id,ord,created_at,updated_at)
VALUES(?,?,?,'upstream-model',0,?,?)`, modelID, environment.donationKey, environment.endpointKey,
		charityTestNow, charityTestNow); err != nil {
		t.Fatalf("seed charity binding: %v", err)
	}
	return modelID
}

func (environment *charityTestEnv) accept(t *testing.T, modelID, reserved int64, attemptLimit int) string {
	t.Helper()
	requestID := mustOpaqueID(t, "req_")
	tx := beginTestTx(t, environment.store.DB())
	remaining := u128Blob(t, int64(attemptLimit+1))
	if _, err := tx.Exec(`INSERT INTO logical_requests(
id,user_id,route_kind,model_snapshot,state,attempt_limit,accounting_state,account_reserved_milli,
settlement_destination,ledger_rows_remaining,created_at)
VALUES(?,?, 'charity_chat_completions','[公益]provider/model','accepted',?,'reserved',?,'user',?,?)`,
		requestID, environment.callerID, attemptLimit, reserved, remaining, charityTestNow); err != nil {
		t.Fatalf("persist logical request: %v", err)
	}
	if err := environment.service.AcceptRequest(context.Background(), tx, claim.CharityAcceptance{
		RequestID: requestID, UserID: environment.callerID, CharityModelID: modelID,
		ModelSnapshot: "[公益]provider/model", ReservedMilli: reserved, AttemptLimit: attemptLimit,
		AcceptedAt: charityTestNow,
	}); err != nil {
		t.Fatalf("AcceptRequest: %v", err)
	}
	commitTestTx(t, tx)
	return requestID
}

type charityTestClaim struct {
	id          string
	reservation claim.CharityReservation
}

func (environment *charityTestEnv) claim(t *testing.T, requestID string, attemptSeq int, dispatched bool) charityTestClaim {
	t.Helper()
	claimID := mustOpaqueID(t, "clm_")
	tx := beginTestTx(t, environment.store.DB())
	reservation, err := environment.service.Claim(context.Background(), tx, claim.CharityClaimInput{
		RequestID: requestID, ClaimID: claimID, ActorUserID: environment.callerID, AttemptSeq: attemptSeq,
		DonationKeyID: environment.donationKey, EndpointID: environment.endpointID,
		EndpointKeyID: environment.endpointKey, UpstreamModelID: "upstream-model",
		ClaimedAt: charityTestNow + int64(attemptSeq),
	})
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	state := "claimed"
	var dispatchedAt any
	if dispatched {
		state, dispatchedAt = "dispatched", charityTestNow+int64(attemptSeq)
	}
	if _, err := tx.Exec(`INSERT INTO dispatch_claims(
id,logical_request_id,attempt_seq,purpose,endpoint_key_id,secret_ref_id,donation_key_id,
streak_generation,claim_now,state,frozen_price_milli,frozen_reward_milli,receiver_user_id,
reserved_price_milli,reserved_calls,reserved_tokens,donor_reward_state,dispatched_at)
VALUES(?,?,?,'charity',?,?,?,?,?,?,?,?,?,?,?,?,'pending',?)`, claimID, requestID, attemptSeq,
		environment.endpointKey, environment.secretID, reservation.DonationKeyID, reservation.StreakGeneration,
		charityTestNow+int64(attemptSeq), state, reservation.FrozenPriceMilli, reservation.FrozenRewardMilli,
		reservation.ReceiverUserID, reservation.ReservedPriceMilli, reservation.ReservedCalls,
		reservation.ReservedTokens, dispatchedAt); err != nil {
		t.Fatalf("persist dispatch claim: %v", err)
	}
	commitTestTx(t, tx)
	return charityTestClaim{id: claimID, reservation: reservation}
}

func (environment *charityTestEnv) completeAttempt(
	t *testing.T,
	requestID string,
	claimed charityTestClaim,
	input claim.CharityAttemptInput,
	rewardState claim.RewardState,
	want claim.CharityActual,
) {
	t.Helper()
	input.RequestID, input.ClaimID = requestID, claimed.id
	input.DonationKeyID = &environment.donationKey
	tx := beginTestTx(t, environment.store.DB())
	if input.ReceiverUserID == nil {
		if _, err := tx.Exec(`UPDATE dispatch_claims SET receiver_user_id=NULL WHERE id=?`, claimed.id); err != nil {
			t.Fatalf("deidentify reward receiver: %v", err)
		}
	}
	actual, err := environment.service.PrepareAttempt(context.Background(), tx, input)
	if err != nil {
		t.Fatalf("PrepareAttempt: %v", err)
	}
	if actual != want {
		t.Fatalf("PrepareAttempt actual = %+v, want %+v", actual, want)
	}
	completion := claim.CharityAttemptCompletion{Attempt: input, Actual: actual, RewardState: rewardState}
	if err := environment.service.CompleteAttempt(context.Background(), tx, completion); err != nil {
		t.Fatalf("CompleteAttempt: %v", err)
	}
	if err := environment.service.CompleteAttempt(context.Background(), tx, completion); err != nil {
		t.Fatalf("CompleteAttempt replay: %v", err)
	}
	var receiver any = input.ReceiverUserID
	if input.ReceiverUserID != nil {
		receiver = *input.ReceiverUserID
	}
	if _, err := tx.Exec(`UPDATE dispatch_claims SET state='committed',secret_ref_id=NULL,
receiver_user_id=?,donor_reward_actual_milli=?,donor_reward_state=?,terminal_at=? WHERE id=?`, receiver,
		actual.RewardMilli, rewardState, input.CompletedAt, claimed.id); err != nil {
		t.Fatalf("terminalize dispatch claim: %v", err)
	}
	commitTestTx(t, tx)
}

func TestPerRequestSettlementRewardAndReceiverDeletion(t *testing.T) {
	environment := newCharityTestEnv(t)
	requestID := environment.accept(t, environment.requestModel, 2400, 1)
	claimed := environment.claim(t, requestID, 1, true)
	if claimed.reservation.ReservedPriceMilli != 3000 || claimed.reservation.FrozenPriceMilli != 3000 ||
		claimed.reservation.FrozenRewardMilli != 1250 || claimed.reservation.ReservedCalls != 1 {
		t.Fatalf("unexpected frozen reservation: %+v", claimed.reservation)
	}
	input := claim.CharityAttemptInput{
		ReceiverUserID: &environment.donorID, Usage: connectorcontract.Usage{Present: true},
		ProtocolSuccess: true, ResponseStarted: false, CompletedAt: charityTestNow + 10,
	}
	environment.completeAttempt(t, requestID, claimed, input, claim.RewardPosted,
		claim.CharityActual{PriceMilli: 3000, RewardMilli: 1250})

	var rewardBlob, donorCredit, priceUsed, callsUsed, priceReserved []byte
	if err := environment.store.DB().QueryRow(`SELECT donor_reward_total_mag FROM charity_reservations
WHERE logical_request_id=?`, requestID).Scan(&rewardBlob); err != nil {
		t.Fatal(err)
	}
	if err := environment.store.DB().QueryRow(`SELECT donation_credit_mag FROM users WHERE id=?`, environment.donorID).Scan(&donorCredit); err != nil {
		t.Fatal(err)
	}
	if err := environment.store.DB().QueryRow(`SELECT price_used_mag,calls_used,price_reserved_mag FROM donation_keys
WHERE id=?`, environment.donationKey).Scan(&priceUsed, &callsUsed, &priceReserved); err != nil {
		t.Fatal(err)
	}
	assertU128(t, rewardBlob, 1250, "posted reward aggregate")
	assertU128(t, donorCredit, 0, "charity settlement must not post donor ledger credit")
	assertU128(t, priceUsed, 3000, "raw donor price")
	assertU128(t, callsUsed, 0, "response-not-started calls")
	assertU128(t, priceReserved, 0, "released price reservation")

	charge, err := environment.service.CalculateRequestCharge(context.Background(), requestID, claim.AccountingCommit)
	if err != nil || charge != 2400 {
		t.Fatalf("CalculateRequestCharge = %d, %v; want 2400", charge, err)
	}
	tx := beginTestTx(t, environment.store.DB())
	if err := environment.service.CompleteRequest(context.Background(), tx, claim.CharityRequestCompletion{
		RequestID: requestID, Caller: claim.CallerResult{Class: claim.ResultSuccess, Status: 200},
		Disposition: claim.AccountingCommit, CompletedAt: charityTestNow + 11,
	}); err != nil {
		t.Fatalf("CompleteRequest: %v", err)
	}
	commitTestTx(t, tx)
	var state string
	var original, charged int64
	if err := environment.store.DB().QueryRow(`SELECT state,original_charge_milli,user_charge_milli
FROM charity_reservations WHERE logical_request_id=?`, requestID).Scan(&state, &original, &charged); err != nil {
		t.Fatal(err)
	}
	if state != "committed" || original != 3000 || charged != 2400 {
		t.Fatalf("request terminal economics = %q/%d/%d", state, original, charged)
	}

	deletedRequest := environment.accept(t, environment.requestModel, 2400, 1)
	deletedClaim := environment.claim(t, deletedRequest, 1, true)
	deletedInput := claim.CharityAttemptInput{Usage: connectorcontract.Usage{Present: true},
		ProtocolSuccess: true, ResponseStarted: true, CompletedAt: charityTestNow + 20}
	environment.completeAttempt(t, deletedRequest, deletedClaim, deletedInput, claim.RewardReceiverDeleted,
		claim.CharityActual{PriceMilli: 3000, RewardMilli: 1250})
	if err := environment.store.DB().QueryRow(`SELECT donor_reward_total_mag FROM charity_reservations
WHERE logical_request_id=?`, deletedRequest).Scan(&rewardBlob); err != nil {
		t.Fatal(err)
	}
	assertU128(t, rewardBlob, 0, "receiver-deleted reward aggregate")
}

func TestPerTokenActualMayExceedReserveAndUnknownIsConservative(t *testing.T) {
	environment := newCharityTestEnv(t)
	requestID := environment.accept(t, environment.tokenModel, 5, 1)
	claimed := environment.claim(t, requestID, 1, true)
	if claimed.reservation.ReservedPriceMilli != 5 || claimed.reservation.ReservedTokens != 5 {
		t.Fatalf("token reservation = %+v", claimed.reservation)
	}
	input := claim.CharityAttemptInput{
		ReceiverUserID:  &environment.donorID,
		Usage:           connectorcontract.Usage{UncachedInputTokens: 2, Present: true},
		ProtocolSuccess: true, ResponseStarted: true, CompletedAt: charityTestNow + 10,
	}
	environment.completeAttempt(t, requestID, claimed, input, claim.RewardPosted,
		claim.CharityActual{PriceMilli: 8, RewardMilli: 4})
	var priceUsed, tokensUsed []byte
	if err := environment.store.DB().QueryRow(`SELECT price_used_mag,tokens_used FROM donation_keys WHERE id=?`,
		environment.donationKey).Scan(&priceUsed, &tokensUsed); err != nil {
		t.Fatal(err)
	}
	assertU128(t, priceUsed, 8, "token actual above reserve")
	assertU128(t, tokensUsed, 2, "actual token count")
	charge, err := environment.service.CalculateRequestCharge(context.Background(), requestID, claim.AccountingCommit)
	if err != nil || charge != 5 {
		t.Fatalf("known token capped caller charge = %d, %v; want 5", charge, err)
	}

	unknownRequest := environment.accept(t, environment.tokenModel, 5, 1)
	unknownClaim := environment.claim(t, unknownRequest, 1, true)
	unknown := claim.CharityAttemptInput{
		ReceiverUserID: &environment.donorID, SuppressReward: true,
		Usage: connectorcontract.Usage{}, UsageUnknown: true, ProtocolSuccess: false,
		ResponseStarted: true, CompletedAt: charityTestNow + 20,
	}
	environment.completeAttempt(t, unknownRequest, unknownClaim, unknown, claim.RewardZero,
		claim.CharityActual{PriceMilli: 5, RewardMilli: 0})
	charge, err = environment.service.CalculateRequestCharge(context.Background(), unknownRequest, claim.AccountingCommit)
	if err != nil || charge != 4 {
		t.Fatalf("unknown conservative discounted charge = %d, %v; want 4", charge, err)
	}
}

func TestReleaseFoldsSequenceAndCapacity(t *testing.T) {
	environment := newCharityTestEnv(t)
	requestID := environment.accept(t, environment.requestModel, 2400, 1)
	claimed := environment.claim(t, requestID, 1, false)
	tx := beginTestTx(t, environment.store.DB())
	if err := environment.service.ReleaseUndispatched(context.Background(), tx, claim.CharityRelease{
		RequestID: requestID, ClaimID: claimed.id, DonationKeyID: &environment.donationKey,
		ReleasedAt: charityTestNow + 10,
	}); err != nil {
		t.Fatalf("ReleaseUndispatched: %v", err)
	}
	if err := environment.service.ReleaseUndispatched(context.Background(), tx, claim.CharityRelease{
		RequestID: requestID, ClaimID: claimed.id, DonationKeyID: &environment.donationKey,
		ReleasedAt: charityTestNow + 10,
	}); err != nil {
		t.Fatalf("ReleaseUndispatched replay: %v", err)
	}
	commitTestTx(t, tx)
	var priceReserved, callsReserved, tokensReserved, nextFold []byte
	if err := environment.store.DB().QueryRow(`SELECT price_reserved_mag,calls_reserved,tokens_reserved,next_fold_seq
FROM donation_keys WHERE id=?`, environment.donationKey).Scan(&priceReserved, &callsReserved, &tokensReserved, &nextFold); err != nil {
		t.Fatal(err)
	}
	assertU128(t, priceReserved, 0, "released price")
	assertU128(t, callsReserved, 0, "released calls")
	assertU128(t, tokensReserved, 0, "released tokens")
	assertU128(t, nextFold, 2, "released fold cursor")
}

func TestClaimRevalidatesFeatureGateAndRequestActor(t *testing.T) {
	environment := newCharityTestEnv(t)
	requestID := environment.accept(t, environment.requestModel, 2400, 1)
	if _, err := environment.store.DB().Exec(`UPDATE site_config SET value='0' WHERE key='charity_enabled'`); err != nil {
		t.Fatal(err)
	}
	tx := beginTestTx(t, environment.store.DB())
	_, err := environment.service.Claim(context.Background(), tx, claim.CharityClaimInput{
		RequestID: requestID, ClaimID: mustOpaqueID(t, "clm_"), ActorUserID: environment.callerID,
		AttemptSeq: 1, DonationKeyID: environment.donationKey, EndpointID: environment.endpointID,
		EndpointKeyID: environment.endpointKey, UpstreamModelID: "upstream-model", ClaimedAt: charityTestNow + 1,
	})
	if !errors.Is(err, claim.ErrNotFound) {
		t.Fatalf("disabled claim error = %v, want not found", err)
	}
	_ = tx.Rollback()
	if _, err := environment.store.DB().Exec(`UPDATE site_config SET value='1' WHERE key='charity_enabled'`); err != nil {
		t.Fatal(err)
	}
	if _, err := environment.store.DB().Exec(`UPDATE charity_reservations SET user_id=? WHERE logical_request_id=?`,
		environment.donorID, requestID); err != nil {
		t.Fatal(err)
	}
	tx = beginTestTx(t, environment.store.DB())
	_, err = environment.service.Claim(context.Background(), tx, claim.CharityClaimInput{
		RequestID: requestID, ClaimID: mustOpaqueID(t, "clm_"), ActorUserID: environment.callerID,
		AttemptSeq: 1, DonationKeyID: environment.donationKey, EndpointID: environment.endpointID,
		EndpointKeyID: environment.endpointKey, UpstreamModelID: "upstream-model", ClaimedAt: charityTestNow + 1,
	})
	if !errors.Is(err, claim.ErrNotFound) {
		t.Fatalf("mismatched request actor error = %v, want not found", err)
	}
	_ = tx.Rollback()
	var reserved []byte
	var rows int
	if err := environment.store.DB().QueryRow(`SELECT price_reserved_mag FROM donation_keys WHERE id=?`,
		environment.donationKey).Scan(&reserved); err != nil {
		t.Fatal(err)
	}
	if err := environment.store.DB().QueryRow(`SELECT COUNT(*) FROM donation_usage_reservations`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	assertU128(t, reserved, 0, "rejected claim capacity")
	if rows != 0 {
		t.Fatalf("rejected claim persisted %d reservations", rows)
	}
}

func TestClaimBindingRevocationOrderingUsesExactUpstreamModel(t *testing.T) {
	environment := newCharityTestEnv(t)
	const alternateModel = "alternate-model"
	if _, err := environment.store.DB().Exec(`INSERT INTO model_pair_catalog(
endpoint_key_id,normalized_model_id,manual_supports,pair_revision,updated_at)
VALUES(?,?,1,1,?)`, environment.endpointKey, alternateModel, charityTestNow); err != nil {
		t.Fatal(err)
	}
	if _, err := environment.store.DB().Exec(`INSERT INTO charity_model_bindings(
charity_model_id,donation_key_id,endpoint_key_id,upstream_model_id,ord,created_at,updated_at)
VALUES(?,?,?,?,1,?,?)`, environment.requestModel, environment.donationKey, environment.endpointKey,
		alternateModel, charityTestNow, charityTestNow); err != nil {
		t.Fatal(err)
	}

	claimedRequest := environment.accept(t, environment.requestModel, 2400, 1)
	frozenClaim := environment.claim(t, claimedRequest, 1, true)
	if _, err := environment.store.DB().Exec(`DELETE FROM model_pair_catalog
WHERE endpoint_key_id=? AND normalized_model_id='upstream-model'`, environment.endpointKey); err != nil {
		t.Fatal(err)
	}
	var alternateBindings int
	if err := environment.store.DB().QueryRow(`SELECT COUNT(*) FROM charity_model_bindings
WHERE charity_model_id=? AND donation_key_id=? AND endpoint_key_id=? AND upstream_model_id=?`,
		environment.requestModel, environment.donationKey, environment.endpointKey, alternateModel).
		Scan(&alternateBindings); err != nil {
		t.Fatal(err)
	}
	if alternateBindings != 1 {
		t.Fatalf("alternate binding count = %d", alternateBindings)
	}
	completion := claim.CharityAttemptInput{
		ReceiverUserID: &environment.donorID, Usage: connectorcontract.Usage{Present: true},
		ProtocolSuccess: true, ResponseStarted: true, CompletedAt: charityTestNow + 10,
	}
	environment.completeAttempt(t, claimedRequest, frozenClaim, completion, claim.RewardPosted,
		claim.CharityActual{PriceMilli: 3000, RewardMilli: 1250})

	staleRequest := environment.accept(t, environment.requestModel, 2400, 1)
	tx := beginTestTx(t, environment.store.DB())
	_, err := environment.service.Claim(context.Background(), tx, claim.CharityClaimInput{
		RequestID: staleRequest, ClaimID: mustOpaqueID(t, "clm_"), ActorUserID: environment.callerID,
		AttemptSeq: 1, DonationKeyID: environment.donationKey, EndpointID: environment.endpointID,
		EndpointKeyID: environment.endpointKey, UpstreamModelID: "upstream-model",
		ClaimedAt: charityTestNow + 20,
	})
	if !errors.Is(err, claim.ErrNotFound) {
		t.Fatalf("stale upstream claim error = %v, want not found", err)
	}
	_ = tx.Rollback()

	tx = beginTestTx(t, environment.store.DB())
	reservation, err := environment.service.Claim(context.Background(), tx, claim.CharityClaimInput{
		RequestID: staleRequest, ClaimID: mustOpaqueID(t, "clm_"), ActorUserID: environment.callerID,
		AttemptSeq: 1, DonationKeyID: environment.donationKey, EndpointID: environment.endpointID,
		EndpointKeyID: environment.endpointKey, UpstreamModelID: alternateModel,
		ClaimedAt: charityTestNow + 20,
	})
	if err != nil || reservation.DonationKeyID != environment.donationKey {
		t.Fatalf("alternate exact claim = %+v, %v", reservation, err)
	}
	_ = tx.Rollback()
}

func TestLateCompletionFromOldGenerationSettlesWithoutChangingCurrentStreak(t *testing.T) {
	environment := newCharityTestEnv(t)
	requestID := environment.accept(t, environment.requestModel, 2400, 1)
	claimed := environment.claim(t, requestID, 1, true)
	if _, err := environment.store.DB().Exec(`UPDATE donation_keys SET
streak_generation=?,next_claim_seq=?,next_fold_seq=?,failure_streak=?,failure_disabled=0
WHERE id=?`, u128Blob(t, 2), u128Blob(t, 1), u128Blob(t, 1), u128Blob(t, 0),
		environment.donationKey); err != nil {
		t.Fatal(err)
	}

	completion := claim.CharityAttemptInput{
		ReceiverUserID: &environment.donorID, SuppressReward: true,
		Usage: connectorcontract.Usage{Present: true}, ProtocolSuccess: false,
		ResponseStarted: true, CompletedAt: charityTestNow + 10,
	}
	environment.completeAttempt(t, requestID, claimed, completion, claim.RewardZero,
		claim.CharityActual{PriceMilli: 3000})

	var priceUsed, priceReserved, callsUsed, callsReserved []byte
	var generation, nextClaim, nextFold, streak []byte
	var disabled int
	if err := environment.store.DB().QueryRow(`SELECT
price_used_mag,price_reserved_mag,calls_used,calls_reserved,
streak_generation,next_claim_seq,next_fold_seq,failure_streak,failure_disabled
FROM donation_keys WHERE id=?`, environment.donationKey).Scan(
		&priceUsed, &priceReserved, &callsUsed, &callsReserved,
		&generation, &nextClaim, &nextFold, &streak, &disabled); err != nil {
		t.Fatal(err)
	}
	assertU128(t, priceUsed, 3000, "old generation settled price")
	assertU128(t, priceReserved, 0, "old generation released price")
	assertU128(t, callsUsed, 1, "old generation settled call")
	assertU128(t, callsReserved, 0, "old generation released call")
	assertU128(t, generation, 2, "current generation")
	assertU128(t, nextClaim, 1, "current generation claim cursor")
	assertU128(t, nextFold, 1, "current generation fold cursor")
	assertU128(t, streak, 0, "current generation failure streak")
	if disabled != 0 {
		t.Fatalf("current generation failure disabled = %d", disabled)
	}
}

func TestConcurrentClaimsReserveLastCapacityExactlyOnce(t *testing.T) {
	environment := newCharityTestEnv(t)
	firstRequest := environment.accept(t, environment.requestModel, 2400, 1)
	secondRequest := environment.accept(t, environment.requestModel, 2400, 1)
	if _, err := environment.store.DB().Exec(`UPDATE donation_keys SET call_limit_mag=? WHERE id=?`,
		u128Blob(t, 1), environment.donationKey); err != nil {
		t.Fatal(err)
	}
	environment.store.DB().SetMaxOpenConns(1)

	type claimAttempt struct {
		requestID string
		claimID   string
	}
	attempts := []claimAttempt{
		{requestID: firstRequest, claimID: mustOpaqueID(t, "clm_")},
		{requestID: secondRequest, claimID: mustOpaqueID(t, "clm_")},
	}
	start := make(chan struct{})
	results := make(chan error, len(attempts))
	for _, attempt := range attempts {
		attempt := attempt
		go func() {
			<-start
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			tx, err := environment.store.DB().BeginTx(ctx, nil)
			if err == nil {
				_, err = environment.service.Claim(ctx, tx, claim.CharityClaimInput{
					RequestID: attempt.requestID, ClaimID: attempt.claimID,
					ActorUserID: environment.callerID, AttemptSeq: 1,
					DonationKeyID: environment.donationKey, EndpointID: environment.endpointID,
					EndpointKeyID: environment.endpointKey, UpstreamModelID: "upstream-model",
					ClaimedAt: charityTestNow + 1,
				})
				if err == nil {
					err = tx.Commit()
				} else {
					_ = tx.Rollback()
				}
			}
			results <- err
		}()
	}
	close(start)

	succeeded, exhausted := 0, 0
	for range attempts {
		select {
		case err := <-results:
			switch {
			case err == nil:
				succeeded++
			case errors.Is(err, claim.ErrNotFound):
				exhausted++
			default:
				t.Fatalf("concurrent claim error = %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("concurrent claims did not finish within bounded contexts")
		}
	}
	if succeeded != 1 || exhausted != 1 {
		t.Fatalf("concurrent claim outcomes = success %d, exhausted %d", succeeded, exhausted)
	}

	var callsReserved, nextClaim []byte
	var reservations int
	if err := environment.store.DB().QueryRow(`SELECT calls_reserved,next_claim_seq
FROM donation_keys WHERE id=?`, environment.donationKey).Scan(&callsReserved, &nextClaim); err != nil {
		t.Fatal(err)
	}
	if err := environment.store.DB().QueryRow(`SELECT COUNT(*) FROM donation_usage_reservations`).
		Scan(&reservations); err != nil {
		t.Fatal(err)
	}
	assertU128(t, callsReserved, 1, "last call capacity reservation")
	assertU128(t, nextClaim, 2, "single successful claim cursor")
	if reservations != 1 {
		t.Fatalf("usage reservations = %d, want 1", reservations)
	}
}

func TestHundredClaimsAdvanceReservationsAndSequence(t *testing.T) {
	environment := newCharityTestEnv(t)
	requestID := environment.accept(t, environment.requestModel, 2400, claim.MaxAttempts)
	tx := beginTestTx(t, environment.store.DB())
	for attempt := 1; attempt <= claim.MaxAttempts; attempt++ {
		if _, err := environment.service.Claim(context.Background(), tx, claim.CharityClaimInput{
			RequestID: requestID, ClaimID: mustOpaqueID(t, "clm_"), ActorUserID: environment.callerID,
			AttemptSeq: attempt, DonationKeyID: environment.donationKey, EndpointID: environment.endpointID,
			EndpointKeyID: environment.endpointKey, UpstreamModelID: "upstream-model",
			ClaimedAt: charityTestNow + int64(attempt),
		}); err != nil {
			t.Fatalf("claim attempt %d: %v", attempt, err)
		}
	}
	commitTestTx(t, tx)

	var priceReserved, callsReserved, tokensReserved, nextClaim []byte
	var reservations int
	if err := environment.store.DB().QueryRow(`SELECT
price_reserved_mag,calls_reserved,tokens_reserved,next_claim_seq
FROM donation_keys WHERE id=?`, environment.donationKey).Scan(
		&priceReserved, &callsReserved, &tokensReserved, &nextClaim); err != nil {
		t.Fatal(err)
	}
	if err := environment.store.DB().QueryRow(`SELECT COUNT(*) FROM donation_usage_reservations`).
		Scan(&reservations); err != nil {
		t.Fatal(err)
	}
	assertU128(t, priceReserved, 300000, "hundred price reservations")
	assertU128(t, callsReserved, 100, "hundred call reservations")
	assertU128(t, tokensReserved, 500, "hundred token reservations")
	assertU128(t, nextClaim, 101, "hundred claim cursor")
	if reservations != claim.MaxAttempts {
		t.Fatalf("usage reservations = %d, want %d", reservations, claim.MaxAttempts)
	}
}

func TestOutOfOrderFailureFoldDisablesExactlyOnce(t *testing.T) {
	environment := newCharityTestEnv(t)
	one, eleven := u128Blob(t, 1), u128Blob(t, 11)
	if _, err := environment.store.DB().Exec(`UPDATE donation_keys SET next_claim_seq=?,next_fold_seq=?,
failure_streak=?,failure_disabled=0 WHERE id=?`, eleven, one, u128Blob(t, 0), environment.donationKey); err != nil {
		t.Fatal(err)
	}
	tx := beginTestTx(t, environment.store.DB())
	generation, err := db.DecodeU128(one)
	if err != nil {
		t.Fatal(err)
	}
	for sequence := int64(10); sequence >= 1; sequence-- {
		claimID := mustOpaqueID(t, "clm_")
		if _, err := tx.Exec(`INSERT INTO donation_usage_reservations(
claim_id,donation_key_id,streak_generation,claim_seq,price_reserved_milli,price_actual_milli,
reward_actual_milli,calls_reserved,calls_actual,tokens_reserved,tokens_actual,protocol_success,
usage_unknown,state,created_at,finalized_at)
VALUES(?,?,?,?,0,0,0,0,0,0,0,0,0,'committed',?,?)`, claimID, environment.donationKey, one,
			u128Blob(t, sequence), charityTestNow, charityTestNow+sequence); err != nil {
			t.Fatalf("seed result %d: %v", sequence, err)
		}
		if err := foldStreak(context.Background(), tx, environment.donationKey, generation, charityTestNow+sequence); err != nil {
			t.Fatalf("fold sequence %d: %v", sequence, err)
		}
		if sequence > 1 {
			var next []byte
			if err := tx.QueryRow(`SELECT next_fold_seq FROM donation_keys WHERE id=?`, environment.donationKey).Scan(&next); err != nil {
				t.Fatal(err)
			}
			assertU128(t, next, 1, "out-of-order cursor")
		}
	}
	commitTestTx(t, tx)
	var nextFold, streak []byte
	var disabled, alerts int
	if err := environment.store.DB().QueryRow(`SELECT next_fold_seq,failure_streak,failure_disabled
FROM donation_keys WHERE id=?`, environment.donationKey).Scan(&nextFold, &streak, &disabled); err != nil {
		t.Fatal(err)
	}
	if err := environment.store.DB().QueryRow(`SELECT COUNT(*) FROM admin_alerts
WHERE kind='donation_failure_disabled'`).Scan(&alerts); err != nil {
		t.Fatal(err)
	}
	assertU128(t, nextFold, 11, "final fold cursor")
	assertU128(t, streak, 10, "failure streak")
	if disabled != 1 || alerts != 1 {
		t.Fatalf("disabled/alerts = %d/%d, want 1/1", disabled, alerts)
	}
}

func TestCapacityBoundaryExpiryAndTerminalCleanup(t *testing.T) {
	zero := db.U128{}
	if _, err := reserveDimension(zero, zero, nil, big.NewInt(9)); err != nil {
		t.Fatalf("infinite capacity rejected: %v", err)
	}
	if _, err := reserveDimension(zero, zero, u128Blob(t, 0), big.NewInt(0)); !errors.Is(err, claim.ErrNotFound) {
		t.Fatalf("zero capacity error = %v, want not found", err)
	}
	reserved, err := reserveDimension(zero, zero, u128Blob(t, 9), big.NewInt(9))
	if err != nil || reserved.Big().Int64() != 9 {
		t.Fatalf("exact capacity = %s, %v", reserved.Decimal(), err)
	}
	if _, err := reserveDimension(zero, reserved, u128Blob(t, 9), big.NewInt(0)); !errors.Is(err, claim.ErrNotFound) {
		t.Fatalf("exhausted capacity error = %v, want not found", err)
	}

	environment := newCharityTestEnv(t)
	requestID := environment.accept(t, environment.requestModel, 2400, 1)
	if _, err := environment.store.DB().Exec(`UPDATE donation_keys SET expires_at=? WHERE id=?`, charityTestNow,
		environment.donationKey); err != nil {
		t.Fatal(err)
	}
	tx := beginTestTx(t, environment.store.DB())
	_, err = environment.service.Claim(context.Background(), tx, claim.CharityClaimInput{
		RequestID: requestID, ClaimID: mustOpaqueID(t, "clm_"), ActorUserID: environment.callerID,
		AttemptSeq: 1, DonationKeyID: environment.donationKey, EndpointID: environment.endpointID,
		EndpointKeyID: environment.endpointKey, UpstreamModelID: "upstream-model", ClaimedAt: charityTestNow,
	})
	if !errors.Is(err, claim.ErrNotFound) {
		t.Fatalf("expired claim error = %v, want not found", err)
	}
	commitTestTx(t, tx)
	var status string
	if err := environment.store.DB().QueryRow(`SELECT status FROM donations WHERE id=?`, environment.donationID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "expired" {
		t.Fatalf("expiry status = %q", status)
	}

	cleanupEnvironment := newCharityTestEnv(t)
	cleanupRequest := cleanupEnvironment.accept(t, cleanupEnvironment.requestModel, 2400, 1)
	cleanupClaim := cleanupEnvironment.claim(t, cleanupRequest, 1, false)
	terminalAt := charityTestNow + 10
	tx = beginTestTx(t, cleanupEnvironment.store.DB())
	if err := cleanupEnvironment.service.ReleaseUndispatched(context.Background(), tx, claim.CharityRelease{
		RequestID: cleanupRequest, ClaimID: cleanupClaim.id, DonationKeyID: &cleanupEnvironment.donationKey,
		ReleasedAt: terminalAt,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`UPDATE dispatch_claims SET state='released',secret_ref_id=NULL,
donor_reward_state='not_due',terminal_at=? WHERE id=?`, terminalAt, cleanupClaim.id); err != nil {
		t.Fatal(err)
	}
	if err := cleanupEnvironment.service.CompleteRequest(context.Background(), tx, claim.CharityRequestCompletion{
		RequestID: cleanupRequest, Caller: claim.CallerResult{Class: claim.ResultCancelled},
		Disposition: claim.AccountingRelease, CompletedAt: terminalAt,
	}); err != nil {
		t.Fatal(err)
	}
	commitTestTx(t, tx)
	cleaned, err := cleanupEnvironment.service.Cleanup(context.Background(), terminalAt+terminalRetention, 1)
	if err != nil || cleaned != 1 {
		t.Fatalf("first cleanup = %d, %v", cleaned, err)
	}
	cleaned, err = cleanupEnvironment.service.Cleanup(context.Background(), terminalAt+terminalRetention, 1)
	if err != nil || cleaned != 1 {
		t.Fatalf("second cleanup = %d, %v", cleaned, err)
	}
	var usageCount, requestCount int
	if err := cleanupEnvironment.store.DB().QueryRow(`SELECT COUNT(*) FROM donation_usage_reservations
WHERE claim_id=?`, cleanupClaim.id).Scan(&usageCount); err != nil {
		t.Fatal(err)
	}
	if err := cleanupEnvironment.store.DB().QueryRow(`SELECT COUNT(*) FROM charity_reservations
WHERE logical_request_id=?`, cleanupRequest).Scan(&requestCount); err != nil {
		t.Fatal(err)
	}
	if usageCount != 0 || requestCount != 0 {
		t.Fatalf("terminal cleanup left usage/request = %d/%d", usageCount, requestCount)
	}
}

func beginTestTx(t *testing.T, database *sql.DB) *sql.Tx {
	t.Helper()
	tx, err := database.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin transaction: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback() })
	return tx
}

func commitTestTx(t *testing.T, tx *sql.Tx) {
	t.Helper()
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit transaction: %v", err)
	}
}

func mustOpaqueID(t *testing.T, prefix string) string {
	t.Helper()
	id, err := db.GenerateOpaqueID(prefix)
	if err != nil {
		t.Fatalf("generate %s id: %v", prefix, err)
	}
	return id
}

func u128Blob(t *testing.T, value int64) []byte {
	t.Helper()
	u128, err := db.U128FromBig(big.NewInt(value))
	if err != nil {
		t.Fatalf("u128 %d: %v", value, err)
	}
	return db.EncodeU128(u128)
}

func assertU128(t *testing.T, blob []byte, want int64, label string) {
	t.Helper()
	value, err := db.DecodeU128(blob)
	if err != nil {
		t.Fatalf("%s decode: %v", label, err)
	}
	if value.Big().Cmp(big.NewInt(want)) != 0 {
		t.Fatalf("%s = %s, want %d", label, value.Decimal(), want)
	}
}
