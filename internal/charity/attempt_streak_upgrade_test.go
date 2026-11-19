package charity

import (
	"database/sql"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/claim"
	"github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

func changeUpgradeFixture(t *testing.T, database *sql.DB, wantRows int64, query string, args ...any) {
	t.Helper()
	result, err := database.Exec(query, args...)
	if err != nil {
		t.Fatal(err)
	}
	if rows, err := result.RowsAffected(); err != nil || rows != wantRows {
		t.Fatalf("fixture changed %d rows, want %d: %v", rows, wantRows, err)
	}
}

func TestAttemptStreakUpgradeUsesSurvivingEvidenceAndSettlesPending(t *testing.T) {
	e := newBackoffEnv(t)
	failure := claim.AttemptOutcome{Kind: claim.ResultResponse, UpstreamStatus: 503,
		StreakDisposition: contract.StreakUpstreamFailure, FailureOrigin: contract.OriginUpstreamResponse}
	finish := func(outcome claim.AttemptOutcome) claim.Handle {
		t.Helper()
		h, _ := e.dispatch(t)
		e.complete(t, h, outcome)
		return h
	}
	increment := finish(failure)
	prunedReservation := finish(failure)
	prunedMarker := finish(failure)
	wrongSeq := finish(failure)
	oldUnknown := finish(failure)
	neutral := finish(claim.AttemptOutcome{Kind: claim.ResultSynthetic, StreakDisposition: contract.StreakNeutral,
		FailureOrigin: contract.OriginPlatform})
	reset := finish(claim.AttemptOutcome{Kind: claim.ResultResponse, UpstreamStatus: 200, ProtocolSuccess: true})
	first, _ := e.dispatch(t)
	later := finish(failure)
	prunedBeforeFold := finish(failure)
	changeUpgradeFixture(t, e.store.DB(), 1, "DELETE FROM dispatch_service_health WHERE claim_id=?", prunedMarker.ClaimID())
	changeUpgradeFixture(t, e.store.DB(), 4, "DELETE FROM donation_usage_reservations WHERE claim_id IN (?,?,?,?)",
		prunedReservation.ClaimID(), prunedMarker.ClaimID(), neutral.ClaimID(), prunedBeforeFold.ClaimID())
	changeUpgradeFixture(t, e.store.DB(), 1, "UPDATE request_attempts SET attempt_seq=2 WHERE claim_id=?", wrongSeq.ClaimID())
	changeUpgradeFixture(t, e.store.DB(), 1, "UPDATE donation_usage_reservations SET streak_disposition='neutral',failure_origin='legacy_unknown' WHERE claim_id=?", oldUnknown.ClaimID())
	changeUpgradeFixture(t, e.store.DB(), 1, "UPDATE dispatch_claims SET streak_disposition='neutral',failure_origin='legacy_unknown' WHERE id=?", oldUnknown.ClaimID())

	e.rail = reopenAfterOfflineUpgrade(t, e.charityTestEnv)
	assertAttemptStreak(t, e.store.DB(), increment.ClaimID(), "incremented", "", "")
	assertAttemptStreak(t, e.store.DB(), prunedReservation.ClaimID(), "incremented", "", "")
	assertAttemptStreak(t, e.store.DB(), reset.ClaimID(), "reset", "", "")
	assertAttemptStreak(t, e.store.DB(), neutral.ClaimID(), "unchanged", "", "")
	for _, h := range []claim.Handle{prunedMarker, prunedBeforeFold, wrongSeq, oldUnknown} {
		assertAttemptStreak(t, e.store.DB(), h.ClaimID(), "", "", "")
	}
	assertAttemptStreak(t, e.store.DB(), later.ClaimID(), "pending", "", "")
	e.complete(t, first, failure)
	assertAttemptStreak(t, e.store.DB(), first.ClaimID(), "incremented", "0", "1")
	assertAttemptStreak(t, e.store.DB(), later.ClaimID(), "incremented", "1", "2")
}

func TestAttemptStreakUpgradeDistinguishesEndedAndResetServices(t *testing.T) {
	e := newBackoffEnv(t)
	if _, err := e.store.DB().Exec("UPDATE charity_models SET model_types=3"); err != nil {
		t.Fatal(err)
	}
	failure := claim.AttemptOutcome{Kind: claim.ResultResponse, UpstreamStatus: 503,
		StreakDisposition: contract.StreakUpstreamFailure, FailureOrigin: contract.OriginUpstreamResponse}
	reset, _, _ := connectionDispatch(t, e, claim.RouteCharityEmbeddings)
	e.complete(t, reset, failure)
	done, _, _ := connectionDispatch(t, e, claim.RouteCharityChat)
	e.complete(t, done, failure)
	connectionDispatch(t, e, claim.RouteCharityChat)
	blocked, _, _ := connectionDispatch(t, e, claim.RouteCharityChat)
	e.complete(t, blocked, failure)
	changeUpgradeFixture(t, e.store.DB(), 1, `UPDATE donation_service_health SET streak_generation=nbi_u128(2),
failure_streak=zeroblob(16),next_fold_seq=nbi_u128(1),next_claim_seq=nbi_u128(1) WHERE id=?`, e.serviceHealthID(t, reset))
	changeUpgradeFixture(t, e.store.DB(), 1, "UPDATE donation_keys SET enabled=0,ended_at=?,ended_reason='member_removed',report_match_until=? WHERE id=?",
		e.clock.Load(), e.clock.Load()+7776000, e.donationKey)

	reopenAfterOfflineUpgrade(t, e.charityTestEnv)
	assertAttemptStreak(t, e.store.DB(), done.ClaimID(), "incremented", "", "")
	assertAttemptStreak(t, e.store.DB(), blocked.ClaimID(), "", "", "")
	assertAttemptStreak(t, e.store.DB(), reset.ClaimID(), "", "", "")
}
