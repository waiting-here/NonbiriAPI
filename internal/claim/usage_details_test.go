package claim

import (
	"context"
	"errors"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/usageinfo"
)

func TestUsageDetailsPersistReplayAndAggregateStrictly(t *testing.T) {
	f := newClaimFixture(t)
	owner := f.seedUser("details-owner", false)
	key := f.seedKey(owner, "details")
	request := f.acceptSelf(owner, 2)
	first := contract.Usage{Present: true, UncachedInputTokens: 3, OutputTokens: 2, SearchUnitsPresent: true, SearchUnitsMicros: 1250001, Modalities: contract.ModalityUsage{InputPresent: true, OutputPresent: true, Input: [3][4]int64{{1, 2, 0, 0}}, Output: [4]int64{0, 0, 2, 0}}}
	for i, u := range []contract.Usage{first, {Present: true, SearchUnitsPresent: true}} {
		h, err := f.service.Claim(t.Context(), ClaimInput{RequestID: request.ID, ActorUserID: owner, AttemptSeq: i + 1, Purpose: PurposeSelf, PersonalModelID: key.modelID, Candidate: key.candidate})
		if err != nil {
			t.Fatal(err)
		}
		grant, err := f.service.TakeForDispatch(t.Context(), h)
		if err != nil {
			t.Fatal(err)
		}
		grant.Clear()
		outcome := AttemptOutcome{Kind: ResultResponse, UpstreamStatus: 503, Usage: u}
		attempt, err := f.service.CompleteAttempt(t.Context(), h, outcome)
		if err != nil || attempt.Usage != usageinfo.Normalize(u) {
			t.Fatalf("persist: %+v %v", attempt, err)
		}
		if replay, err := f.service.CompleteAttempt(t.Context(), h, outcome); err != nil || replay != attempt {
			t.Fatalf("replay: %+v %v", replay, err)
		}
		outcome.Usage.SearchUnitsMicros++
		if _, err := f.service.CompleteAttempt(t.Context(), h, outcome); !errors.Is(err, ErrConflict) {
			t.Fatal("changed units replay", err)
		}
	}
	var encoded string
	var count int
	if err := f.db.QueryRow(`SELECT attempt_count,usage_details FROM request_logs WHERE logical_request_id=?`, request.ID).Scan(&count, &encoded); err != nil {
		t.Fatal(err)
	}
	combined, err := usageinfo.Apply(first, encoded)
	if err != nil || count != 2 || !combined.SearchUnitsPresent || combined.SearchUnitsMicros != 1250001 || combined.Modalities.InputPresent || combined.Modalities.OutputPresent || combined.Modalities.InputReason != contract.ModalityMissing {
		t.Fatalf("aggregate: %+v %v", combined, err)
	}
}

func TestSearchUnitRewardDoesNotRequireKnownTokens(t *testing.T) {
	for _, admin := range []bool{false, true} {
		f := newClaimFixture(t)
		caller := f.seedUser("unit-caller", false)
		donor := f.seedUser("unit-donor", admin)
		key := f.seedKey(donor, "units")
		donation := f.seedDonationKey(donor, key, "units", 17)
		request := f.acceptCharity(caller, 1)
		h := mustDeletionClaim(t, f, request, key, 1, PurposeCharity, donation)
		grant, err := f.service.TakeForDispatch(context.Background(), h)
		if err != nil {
			t.Fatal(err)
		}
		grant.Clear()
		outcome := AttemptOutcome{Kind: ResultResponse, UpstreamStatus: 200, ProtocolSuccess: true, ResponseStarted: true, Usage: contract.Usage{SearchUnitsPresent: true, SearchUnitsMicros: 1500000}}
		got, err := f.service.CompleteAttempt(t.Context(), h, outcome)
		if err != nil {
			t.Fatal(err)
		}
		want := int64(17)
		if admin {
			want = 0
		}
		if got.RewardActualMilli != want || got.Usage.Present || !got.Usage.SearchUnitsPresent {
			t.Fatalf("admin=%v attempt=%+v", admin, got)
		}
		if replay, err := f.service.CompleteAttempt(t.Context(), h, outcome); err != nil || replay != got {
			t.Fatalf("unit replay: %+v %v", replay, err)
		}
	}
}
