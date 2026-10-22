package charity

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/claim"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/routing"
)

func connectionInput(t *testing.T, e *backoffEnv, route claim.RouteKind) claim.ClaimInput {
	t.Helper()
	now := e.clock.Load()
	r, err := e.rail.Accept(t.Context(), claim.AcceptInput{UserID: e.callerID, Route: route, ModelSnapshot: "[公益]provider/token", AttemptLimit: 1, ReservedMilli: 5, CharityModelID: e.tokenModel, CharityDecisionNow: &now})
	if err != nil {
		t.Fatal(err)
	}
	input := e.retryClaimInput(t, r.ID)
	tx := beginTestTx(t, e.store.DB())
	input.Candidate.Authorization = e.authorization(t, tx, r.ID)
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	return input
}

func connectionDispatch(t *testing.T, e *backoffEnv, route claim.RouteKind) (claim.Handle, *claim.Dispatch, routing.TargetAuthorization) {
	t.Helper()
	input := connectionInput(t, e, route)
	h, err := e.rail.Claim(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	d, err := e.rail.TakeForDispatch(t.Context(), h)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(d.Clear)
	return h, d, input.Candidate.Authorization
}

func TestConnectionHealthSeparatesServicesAndOrdersOutcomesWithinSharedQuota(t *testing.T) {
	e := newBackoffEnv(t)
	if _, err := e.store.DB().Exec(`UPDATE charity_models SET model_types=3;
UPDATE donation_keys SET call_limit_mag=?,failure_streak=?,failure_disable_threshold='2' WHERE id=?`, u128Blob(t, 3), u128Blob(t, 1), e.donationKey); err != nil {
		t.Fatal(err)
	}
	first, _, chat := connectionDispatch(t, e, claim.RouteCharityChat)
	second, _, embeddings := connectionDispatch(t, e, claim.RouteCharityEmbeddings)
	third, _, _ := connectionDispatch(t, e, claim.RouteCharityChat)
	if _, err := e.rail.Claim(t.Context(), connectionInput(t, e, claim.RouteCharityEmbeddings)); !errors.Is(err, claim.ErrNotFound) {
		t.Fatalf("service selection bypassed shared call limit: %v", err)
	}
	failure := claim.AttemptOutcome{Kind: claim.ResultResponse, UpstreamStatus: 503, StreakDisposition: contract.StreakUpstreamFailure, FailureOrigin: contract.OriginUpstreamResponse}
	e.complete(t, third, failure)
	var streak, cursor []byte
	var disabled bool
	if err := e.store.DB().QueryRow(`SELECT failure_streak,next_fold_seq,failure_disabled FROM donation_service_health WHERE use_id=?`, chat.UseID).Scan(&streak, &cursor, &disabled); err != nil {
		t.Fatal(err)
	}
	assertU128(t, streak, 0, "later service failure waits for predecessor")
	assertU128(t, cursor, 1, "service cursor waits for predecessor")
	if disabled {
		t.Fatal("out-of-order service event disabled use early")
	}
	e.complete(t, second, claim.AttemptOutcome{Kind: claim.ResultResponse, UpstreamStatus: 200, ProtocolSuccess: true, ResponseStarted: true, Usage: contract.Usage{Present: true}})
	e.complete(t, first, failure)
	if err := e.store.DB().QueryRow(`SELECT failure_streak,next_fold_seq,failure_disabled FROM donation_service_health WHERE use_id=?`, chat.UseID).Scan(&streak, &cursor, &disabled); err != nil {
		t.Fatal(err)
	}
	assertU128(t, streak, 2, "chat failures folded in claim order")
	assertU128(t, cursor, 3, "chat cursor advanced twice")
	if !disabled {
		t.Fatal("chat threshold did not disable the affected use")
	}
	if err := e.store.DB().QueryRow(`SELECT failure_streak,next_fold_seq,failure_disabled FROM donation_service_health WHERE use_id=?`, embeddings.UseID).Scan(&streak, &cursor, &disabled); err != nil {
		t.Fatal(err)
	}
	assertU128(t, streak, 0, "embeddings does not inherit chat failures")
	assertU128(t, cursor, 2, "embeddings cursor remains independent")
	if disabled {
		t.Fatal("chat failure disabled embeddings")
	}
	var used, reserved []byte
	if err := e.store.DB().QueryRow(`SELECT failure_streak,next_fold_seq,calls_used,calls_reserved FROM donation_keys WHERE id=?`, e.donationKey).Scan(&streak, &cursor, &used, &reserved); err != nil {
		t.Fatal(err)
	}
	assertU128(t, streak, 1, "service outcomes preserve unknown legacy streak")
	assertU128(t, cursor, 4, "legacy cursor advances over all service markers")
	assertU128(t, used, 1, "shared original member charged once")
	assertU128(t, reserved, 0, "all original reservations released")
	var members, reservations int
	if err := e.store.DB().QueryRow(`SELECT (SELECT count(*) FROM donation_key_memberships),(SELECT count(*) FROM donation_usage_reservations WHERE donation_key_id=?)`, e.donationKey).Scan(&members, &reservations); err != nil || members != 1 || reservations != 3 {
		t.Fatalf("shared accounting identity changed: members=%d reservations=%d err=%v", members, reservations, err)
	}
	if _, err := e.rail.Claim(t.Context(), connectionInput(t, e, claim.RouteCharityChat)); !errors.Is(err, claim.ErrNotFound) {
		t.Fatalf("disabled chat still claims: %v", err)
	}
	embeddingInput := connectionInput(t, e, claim.RouteCharityEmbeddings)
	available, err := e.rail.Claim(t.Context(), embeddingInput)
	if err != nil {
		t.Fatalf("unaffected embeddings unavailable: %v", err)
	}
	if _, err := e.rail.ReleaseUndispatched(t.Context(), available); err != nil {
		t.Fatal(err)
	}
}

func TestConnectionHealthLegacyTrialKeepsReceiptScopeAndNewBackoffStaysLocal(t *testing.T) {
	e := newBackoffEnv(t)
	now := e.clock.Load()
	if _, err := e.store.DB().Exec(`UPDATE charity_models SET model_types=3;
UPDATE donation_keys SET failure_streak=? WHERE id=?`, u128Blob(t, 1), e.donationKey); err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.DB().Exec(`INSERT INTO donation_key_backoffs(donation_key_id,generation,episode,period,source,stage,retry_at,updated_at) VALUES(?,1,1,'five_hour','builtin',1,?,?)`, e.donationKey, now, now); err != nil {
		t.Fatal(err)
	}
	trial, dispatch, chat := connectionDispatch(t, e, claim.RouteCharityChat)
	if !dispatch.IsBackoffTrial() {
		t.Fatal("ready legacy gate did not grant a trial")
	}
	var receiptHealth sql.NullInt64
	var markerHealth int64
	if err := e.store.DB().QueryRow(`SELECT f.health_id_snapshot,m.health_id_snapshot FROM dispatch_smart_backoff_facts f JOIN dispatch_service_health m ON m.claim_id=f.claim_id WHERE f.claim_id=?`, trial.ClaimID()).Scan(&receiptHealth, &markerHealth); err != nil || receiptHealth.Valid || markerHealth <= 0 {
		t.Fatalf("legacy trial was reassigned to service scope: receipt=%+v marker=%d err=%v", receiptHealth, markerHealth, err)
	}
	if err := e.rail.ObserveResponseHeaders(t.Context(), trial, contract.ResponseObservation{Status: 200, ObservedAt: now}); err != nil {
		t.Fatal(err)
	}
	e.complete(t, trial, claim.AttemptOutcome{Kind: claim.ResultResponse, UpstreamStatus: 200, ProtocolSuccess: true, Usage: contract.Usage{Present: true}})
	var legacyRetry sql.NullInt64
	var streak []byte
	if err := e.store.DB().QueryRow(`SELECT b.retry_at,k.failure_streak FROM donation_key_backoffs b JOIN donation_keys k ON k.id=b.donation_key_id WHERE k.id=?`, e.donationKey).Scan(&legacyRetry, &streak); err != nil || legacyRetry.Valid {
		t.Fatalf("accepted legacy trial left its gate active: %+v %v", legacyRetry, err)
	}
	assertU128(t, streak, 1, "backoff trial leaves unrelated legacy failure streak intact")
	current, _, _ := connectionDispatch(t, e, claim.RouteCharityChat)
	e.complete(t, current, quotaOutcome("five_hour"))
	var serviceRetry int64
	if err := e.store.DB().QueryRow(`SELECT h.backoff_retry_at,f.health_id_snapshot FROM donation_service_health h JOIN dispatch_smart_backoff_facts f ON f.health_id_snapshot=h.id WHERE h.use_id=? AND f.claim_id=?`, chat.UseID, current.ClaimID()).Scan(&serviceRetry, &receiptHealth); err != nil || serviceRetry <= now || !receiptHealth.Valid {
		t.Fatalf("new quota backoff lost exact service scope: retry=%d health=%+v err=%v", serviceRetry, receiptHealth, err)
	}
	if err := e.store.DB().QueryRow(`SELECT retry_at FROM donation_key_backoffs WHERE donation_key_id=?`, e.donationKey).Scan(&legacyRetry); err != nil || legacyRetry.Valid {
		t.Fatalf("new service backoff contaminated legacy gate: %+v %v", legacyRetry, err)
	}
	if _, err := e.rail.Claim(t.Context(), connectionInput(t, e, claim.RouteCharityChat)); !errors.Is(err, claim.ErrKeyRateLimited) {
		t.Fatalf("chat backoff not enforced: %v", err)
	}
	other, _, _ := connectionDispatch(t, e, claim.RouteCharityEmbeddings)
	e.complete(t, other, claim.AttemptOutcome{Kind: claim.ResultResponse, UpstreamStatus: 200, ProtocolSuccess: true, Usage: contract.Usage{Present: true}})
}
