package claim

import (
	"context"
	"errors"
	"testing"
)

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
