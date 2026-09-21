package claim

import (
	"context"
	"database/sql"
	"errors"
	"sync/atomic"
	"testing"
)

func TestDelegatedDiscoveryReauthorizesBeforeCredentialDispatch(t *testing.T) {
	f := newClaimFixture(t)
	owner := f.seedUser("delegated-discovery", false)
	key := f.seedKey(owner, "delegated-discovery")
	key.candidate.UpstreamModelID = ""
	var allowed atomic.Bool
	var checks atomic.Int64
	guard := func(ctx context.Context, tx *sql.Tx) error {
		checks.Add(1)
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id=?)`, owner).Scan(&exists); err != nil {
			return err
		}
		if !allowed.Load() || !exists {
			return ErrForbidden
		}
		return nil
	}
	input := DiscoveryClaimInput{ActorUserID: owner, Candidate: key.candidate, Authorize: guard}
	if _, _, err := f.service.ClaimDiscovery(context.Background(), input); !errors.Is(err, ErrForbidden) {
		t.Fatalf("accept denial: %v", err)
	}
	allowed.Store(true)
	_, handle, err := f.service.ClaimDiscovery(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	allowed.Store(false)
	if _, err := f.service.TakeForDispatch(context.Background(), handle); !errors.Is(err, ErrForbidden) {
		t.Fatalf("dispatch denial: %v", err)
	}
	if f.codec.openCount() != 0 || checks.Load() != 3 {
		t.Fatalf("credential opened or guard missed: %d %d", f.codec.openCount(), checks.Load())
	}
	var state string
	if err := f.db.QueryRow(`SELECT state FROM dispatch_claims WHERE id=?`, handle.ClaimID()).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "claimed" {
		t.Fatalf("denied dispatch changed state %s", state)
	}
	if _, err := f.service.ReleaseUndispatched(context.Background(), handle); err != nil {
		t.Fatal(err)
	}
}
