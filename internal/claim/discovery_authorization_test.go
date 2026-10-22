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
	input := f.discoveryInput(DiscoveryClaimInput{ActorUserID: owner, Candidate: key.candidate, Authorize: guard})
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

func TestConnectionDiscoveryRejectsSupersededOperationAtCreationAndDispatch(t *testing.T) {
	f := newClaimFixture(t)
	owner := f.seedUser("current-operation", false)
	key := f.seedKey(owner, "current-operation")
	candidate := key.candidate
	candidate.UpstreamModelID = ""
	input := f.discoveryInput(DiscoveryClaimInput{ActorUserID: owner, Candidate: candidate})
	_, handle, err := f.service.ClaimDiscovery(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`UPDATE discovery_runs SET state='failed',safe_class='interrupted',completed_at=? WHERE operation_id=?`, f.clock.Load(), input.OperationID); err != nil {
		t.Fatal(err)
	}
	next := f.discoveryInput(DiscoveryClaimInput{ActorUserID: owner, Candidate: candidate})
	if next.OperationID == input.OperationID {
		t.Fatal("fixture did not replace the current operation")
	}
	if _, _, err := f.service.ClaimDiscovery(context.Background(), input); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stale operation created a claim: %v", err)
	}
	if _, err := f.service.TakeForDispatch(context.Background(), handle); !errors.Is(err, ErrNotFound) || f.codec.openCount() != 0 {
		t.Fatalf("stale operation dispatched: opens=%d err=%v", f.codec.openCount(), err)
	}
	if _, err := f.service.ReleaseUndispatched(context.Background(), handle); err != nil {
		t.Fatal(err)
	}
	_, current, err := f.service.ClaimDiscovery(context.Background(), next)
	if err != nil {
		t.Fatal(err)
	}
	grant, err := f.service.TakeForDispatch(context.Background(), current)
	if err != nil {
		t.Fatal(err)
	}
	grant.Clear()
}

func TestConnectionDiscoveryCompletionPreservesOptionalEntryIdentity(t *testing.T) {
	for _, independent := range []bool{false, true} {
		t.Run(map[bool]string{false: "entry", true: "independent"}[independent], func(t *testing.T) {
			f := newClaimFixture(t)
			owner := f.seedUser("discovery-completion", false)
			key := f.seedKey(owner, "discovery-completion")
			candidate := key.candidate
			candidate.UpstreamModelID = ""
			input := f.discoveryInput(DiscoveryClaimInput{ActorUserID: owner, Candidate: candidate})
			if independent {
				if _, err := f.db.Exec(`DELETE FROM endpoints WHERE id=?`, key.endpointID); err != nil {
					t.Fatal(err)
				}
				input.Candidate.EndpointID, input.Candidate.EndpointKeyID = 0, 0
			}
			_, handle, err := f.service.ClaimDiscovery(t.Context(), input)
			if err != nil {
				t.Fatal(err)
			}
			grant, err := f.service.TakeForDispatch(t.Context(), handle)
			if err != nil {
				t.Fatal(err)
			}
			grant.Clear()
			if _, err := f.service.CompleteAttempt(t.Context(), handle, AttemptOutcome{Kind: ResultResponse, UpstreamStatus: 200, ProtocolSuccess: true}); err != nil {
				t.Fatal(err)
			}
			var endpoint, entry sql.NullInt64
			if err := f.db.QueryRow(`SELECT endpoint_id_snapshot,endpoint_key_id_snapshot FROM request_attempts WHERE claim_id=?`, handle.ClaimID()).Scan(&endpoint, &entry); err != nil {
				t.Fatal(err)
			}
			if independent && (endpoint.Valid || entry.Valid) {
				t.Fatalf("independent source persisted entry identities: %v/%v", endpoint, entry)
			}
			if !independent && (!endpoint.Valid || !entry.Valid || endpoint.Int64 != key.endpointID || entry.Int64 != key.keyID) {
				t.Fatalf("entry identity changed: %v/%v", endpoint, entry)
			}
		})
	}
}
