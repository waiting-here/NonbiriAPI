package claim

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

func TestTerminalOutcomePersistsAtomicallyAndRejectsConflictingReplay(t *testing.T) {
	f := newClaimFixture(t)
	owner := f.seedUser("outcome-owner", false)
	key := f.seedKey(owner, "outcome")
	request := f.acceptSelf(owner, 1)
	handle, err := f.service.Claim(context.Background(), ClaimInput{RequestID: request.ID, ActorUserID: owner, AttemptSeq: 1, Purpose: PurposeSelf, Candidate: key.candidate})
	if err != nil {
		t.Fatal(err)
	}
	dispatch, err := f.service.TakeForDispatch(context.Background(), handle)
	if err != nil {
		t.Fatal(err)
	}
	dispatch.Clear()
	outcome := AttemptOutcome{Kind: ResultResponse, UpstreamStatus: 503, StreakDisposition: contract.StreakUpstreamFailure, FailureOrigin: contract.OriginUpstreamResponse}
	_, err = f.db.Exec("CREATE TEMP TRIGGER reject_completion AFTER UPDATE OF state ON dispatch_claims WHEN NEW.state='committed' BEGIN SELECT RAISE(ABORT,'completion failed'); END")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.service.CompleteAttempt(context.Background(), handle, outcome); err == nil {
		t.Fatal("injected failure succeeded")
	}
	var disposition, origin sql.NullString
	var state string
	var attempts int
	if err = f.db.QueryRow("SELECT state,streak_disposition,failure_origin FROM dispatch_claims WHERE id=?", handle.ClaimID()).Scan(&state, &disposition, &origin); err != nil || state != "dispatched" || disposition.Valid || origin.Valid {
		t.Fatal(state, disposition, origin, err)
	}
	if err = f.db.QueryRow("SELECT count(*) FROM request_attempts WHERE claim_id=?", handle.ClaimID()).Scan(&attempts); err != nil || attempts != 0 {
		t.Fatal(attempts, err)
	}
	if _, err = f.db.Exec("DROP TRIGGER reject_completion"); err != nil {
		t.Fatal(err)
	}
	completed, err := f.service.CompleteAttempt(context.Background(), handle, outcome)
	if err != nil || completed.StreakDisposition != outcome.StreakDisposition || completed.FailureOrigin != outcome.FailureOrigin {
		t.Fatalf("%+v %v", completed, err)
	}
	if replay, err := f.service.CompleteAttempt(context.Background(), handle, outcome); err != nil || replay != completed {
		t.Fatalf("same replay %+v %v", replay, err)
	}
	outcome.FailureOrigin = contract.OriginUpstreamProtocol
	if _, err = f.service.CompleteAttempt(context.Background(), handle, outcome); !errors.Is(err, ErrConflict) {
		t.Fatal("conflicting origin accepted", err)
	}
	f.requireCapacity(request.ID, 1, 1, 1)
}

func TestTerminalReplayCannotChangeResponseBillingFact(t *testing.T) {
	for _, checkpoint := range []bool{false, true} {
		f := newClaimFixture(t)
		caller := f.seedUser("replay-caller", false)
		donor := f.seedUser("replay-donor", false)
		key := f.seedKey(donor, "replay")
		donation := f.seedDonationKey(donor, key, "replay", 0)
		request := f.acceptCharity(caller, 1)
		handle := mustDeletionClaim(t, f, request, key, 1, PurposeCharity, donation)
		grant, err := f.service.TakeForDispatch(context.Background(), handle)
		if err != nil {
			t.Fatal(err)
		}
		grant.Clear()
		if checkpoint {
			if err := f.service.MarkResponseStarted(context.Background(), handle); err != nil {
				t.Fatal(err)
			}
		}
		outcome := AttemptOutcome{Kind: ResultSynthetic, UpstreamStatus: 502}
		completed, err := f.service.CompleteAttempt(context.Background(), handle, outcome)
		if err != nil {
			t.Fatal(err)
		}
		if replay, err := f.service.CompleteAttempt(context.Background(), handle, outcome); err != nil || replay != completed {
			t.Fatalf("same replay = %+v, %v", replay, err)
		}
		outcome.ResponseStarted = true
		_, err = f.service.CompleteAttempt(context.Background(), handle, outcome)
		if checkpoint && err != nil || !checkpoint && !errors.Is(err, ErrConflict) {
			t.Fatalf("checkpoint=%t replay=%v", checkpoint, err)
		}
	}
}
