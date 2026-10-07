package claim

import (
	"context"
	"errors"
	"testing"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

func TestRecoveryRetainsAcceptedHTTPStreamWithoutInventingCompletion(t *testing.T) {
	f := newClaimFixture(t)
	caller := f.seedUser("stream-caller", false)
	donor := f.seedUser("stream-donor", false)
	key := f.seedKey(donor, "stream")
	donation := f.seedDonationKey(donor, key, "stream", 0)
	request := f.acceptCharity(caller, 1)
	handle := mustDeletionClaim(t, f, request, key, 1, PurposeCharity, donation)
	dispatch, err := f.service.TakeForDispatch(context.Background(), handle)
	if err != nil {
		t.Fatal(err)
	}
	dispatch.Clear()
	if err := f.service.MarkResponseStarted(context.Background(), handle, 200); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.RecoverNonterminal(context.Background(), MaxRecoveryBatch); err != nil {
		t.Fatal(err)
	}
	want := AttemptOutcome{Kind: ResultResponse, UpstreamStatus: 200, Diagnostic: "upstream stream outcome unavailable after restart", StreakDisposition: contract.StreakSuccess, FailureOrigin: contract.OriginRecoveryUnknown, ResponseStarted: true}
	result, err := f.service.CompleteAttempt(context.Background(), handle, want)
	if err != nil || result.StreakDisposition != want.StreakDisposition || result.UpstreamStatus != 200 || result.FailureOrigin != want.FailureOrigin {
		t.Fatalf("recovered response=%+v %v", result, err)
	}
	var status int
	if err := f.db.QueryRow("SELECT http_status FROM dispatch_response_starts WHERE claim_id=?", handle.ClaimID()).Scan(&status); err != nil || status != 200 {
		t.Fatal("lost accepted response", status, err)
	}
}

func TestResponseCheckpointAdmissionConcurrencyAndCascade(t *testing.T) {
	f := newClaimFixture(t)
	caller := f.seedUser("checkpoint-caller", false)
	donor := f.seedUser("checkpoint-donor", false)
	key := f.seedKey(donor, "checkpoint")
	donation := f.seedDonationKey(donor, key, "checkpoint", 0)
	request := f.acceptCharity(caller, 1)
	handle := mustDeletionClaim(t, f, request, key, 1, PurposeCharity, donation)
	if err := f.service.MarkResponseStarted(context.Background(), handle); !errors.Is(err, ErrNotDispatched) {
		t.Fatalf("undispatched mark=%v", err)
	}
	grant, err := f.service.TakeForDispatch(context.Background(), handle)
	if err != nil {
		t.Fatal(err)
	}
	grant.Clear()
	start := make(chan struct{})
	results := make(chan error, 12)
	for range 12 {
		go func() { <-start; results <- f.service.MarkResponseStarted(context.Background(), handle) }()
	}
	close(start)
	for range 12 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM dispatch_response_starts WHERE claim_id=?`, handle.ClaimID()).Scan(&count); err != nil || count != 1 {
		t.Fatalf("concurrent markers=%d %v", count, err)
	}
	if _, err := f.service.CompleteAttempt(context.Background(), handle, AttemptOutcome{Kind: ResultSynthetic, UpstreamStatus: 502}); err != nil {
		t.Fatal(err)
	}
	if err := f.service.MarkResponseStarted(context.Background(), handle); !errors.Is(err, ErrNotDispatched) {
		t.Fatalf("late checkpoint=%v", err)
	}
	if _, err := f.service.CompleteRequest(context.Background(), CompleteRequestInput{RequestID: request.ID, Caller: CallerResult{Class: ResultCancelled}, Disposition: AccountingCommit}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`DELETE FROM logical_requests WHERE id=?`, request.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM dispatch_response_starts`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("orphan response marker=%d %v", count, err)
	}
}
