package claim

import (
	"context"
	"errors"
	"testing"
)

func TestCharityDispatchAdmissionSharesMarkerTransaction(t *testing.T) {
	f := newClaimFixture(t)
	caller := f.seedUser("caller", false)
	donor := f.seedUser("donor", false)
	key := f.seedKey(donor, "admission")
	donationKey := f.seedDonationKey(donor, key, "admission", 5)
	request := f.acceptCharity(caller, 2)
	handle := mustDeletionClaim(t, f, request, key, 1, PurposeCharity, donationKey)
	for _, failure := range []error{ErrForbidden, ErrModelUnavailable} {
		f.charity.mu.Lock()
		f.charity.dispatchErr = failure
		f.charity.mu.Unlock()
		grant, err := f.service.TakeForDispatch(context.Background(), handle)
		if !errors.Is(err, failure) || grant != nil || f.codec.openCount() != 0 {
			t.Fatalf("rejected grant %v error %v opens %d", grant, err, f.codec.openCount())
		}
		var state string
		var facts int
		if err := f.db.QueryRow(`SELECT state FROM dispatch_claims WHERE id=?`, handle.ClaimID()).Scan(&state); err != nil {
			t.Fatal(err)
		}
		if err := f.db.QueryRow(`SELECT COUNT(*) FROM claim_test_charity_facts WHERE kind='dispatch'`).Scan(&facts); err != nil {
			t.Fatal(err)
		}
		if state != "claimed" || facts != 0 {
			t.Fatalf("admission failure did not roll back marker/facts: %s/%d", state, facts)
		}
	}
	f.charity.mu.Lock()
	f.charity.dispatchErr = nil
	f.charity.mu.Unlock()
	grant, err := f.service.TakeForDispatch(context.Background(), handle)
	if err != nil || grant == nil {
		t.Fatalf("allowed dispatch: %v", err)
	}
	grant.Clear()
	f.charity.mu.Lock()
	last := f.charity.dispatches[len(f.charity.dispatches)-1]
	count := len(f.charity.dispatches)
	f.charity.dispatchErr = ErrForbidden
	f.charity.mu.Unlock()
	if last.RequestID != request.ID || last.ClaimID != handle.ClaimID() || last.ActorUserID != caller || last.DispatchedAt != f.clock.Load() {
		t.Fatalf("dispatch authority: %+v", last)
	}
	if _, err := f.service.TakeForDispatch(context.Background(), handle); !errors.Is(err, ErrAlreadyDispatched) {
		t.Fatalf("dispatched rechecked as new: %v", err)
	}
	f.charity.mu.Lock()
	after := len(f.charity.dispatches)
	f.charity.mu.Unlock()
	if after != count || f.codec.openCount() != 1 {
		t.Fatal("replay repeated admission or credential open")
	}
}
