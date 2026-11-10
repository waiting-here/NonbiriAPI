package claim

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/dbtest"
)

type queuedClaimResult struct {
	handle Handle
	err    error
}

func configureQueue(t *testing.T, f *claimFixture, keyID, concurrency, rpm, concurrencyCap, rpmCap int64, concurrencyMode, rpmMode string) {
	t.Helper()
	_, err := f.db.Exec(`INSERT INTO endpoint_key_limits(endpoint_key_id,max_concurrency,max_rpm,concurrency_overflow,rpm_overflow,concurrency_queue_limit,rpm_queue_limit)
VALUES(?,?,?,?,?,?,?) ON CONFLICT(endpoint_key_id) DO UPDATE SET max_concurrency=excluded.max_concurrency,max_rpm=excluded.max_rpm,
concurrency_overflow=excluded.concurrency_overflow,rpm_overflow=excluded.rpm_overflow,concurrency_queue_limit=excluded.concurrency_queue_limit,rpm_queue_limit=excluded.rpm_queue_limit`, keyID, concurrency, rpm, concurrencyMode, rpmMode, concurrencyCap, rpmCap)
	if err != nil {
		t.Fatal(err)
	}
	f.service.NotifyKey(keyID)
}

func queueSize(s *Service, keyID int64) int {
	s.keyQueue.mu.Lock()
	defer s.keyQueue.mu.Unlock()
	if entry := s.keyQueue.keys[keyID]; entry != nil {
		return entry.waiters.Len()
	}
	return 0
}

func awaitQueueSize(t *testing.T, s *Service, keyID int64, want int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for queueSize(s, keyID) != want {
		select {
		case <-ctx.Done():
			t.Fatalf("queue size=%d want %d", queueSize(s, keyID), want)
		case <-ticker.C:
		}
	}
}

func startQueuedClaim(t *testing.T, f *claimFixture, ctx context.Context, input ClaimInput) <-chan queuedClaimResult {
	t.Helper()
	_, err := f.service.Claim(ctx, input)
	candidates := KeyQueueCandidates(err)
	if len(candidates) != 1 {
		t.Fatalf("expected an explicit queue candidate: %v", err)
	}
	done := make(chan queuedClaimResult, 1)
	go func() { handle, err := f.service.WaitForKey(ctx, candidates); done <- queuedClaimResult{handle, err} }()
	return done
}

func receiveQueuedClaim(t *testing.T, done <-chan queuedClaimResult) queuedClaimResult {
	t.Helper()
	select {
	case result := <-done:
		return result
	case <-time.After(5 * time.Second):
		t.Fatal("queued claim did not finish")
		return queuedClaimResult{}
	}
}

func TestKeyQueueFIFOSharedPurposesAndDispatchHandoff(t *testing.T) {
	f := newClaimFixture(t)
	owner, caller := f.seedUser("queue-owner", false), f.seedUser("queue-caller", false)
	key := f.seedKey(owner, "queue-shared")
	donation := f.seedDonationKey(owner, key, "queue-shared", 1)
	configureQueue(t, f, key.keyID, 1, 0, 2, 0, "queue", "reject")
	active, err := f.service.Claim(t.Context(), selfLimitInput(f, owner, key))
	if err != nil {
		t.Fatal(err)
	}
	dispatchLimitClaim(t, f, active)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	first := startQueuedClaim(t, f, ctx, selfLimitInput(f, owner, key))
	awaitQueueSize(t, f.service, key.keyID, 1)
	request := f.acceptCharity(caller, 1)
	second := startQueuedClaim(t, f, ctx, ClaimInput{RequestID: request.ID, ActorUserID: caller, AttemptSeq: 1, Purpose: PurposeCharity, Candidate: key.charityCandidates[1], DonationKeyID: donation})
	awaitQueueSize(t, f.service, key.keyID, 2)
	debug := selfLimitInput(f, owner, key)
	debug.Purpose = PurposeDebugLive
	_, limited := f.service.Claim(ctx, debug)
	if _, err := f.service.WaitForKey(ctx, KeyQueueCandidates(limited)); !errors.Is(err, ErrKeyRateLimited) {
		t.Fatalf("full shared queue: %v", err)
	}
	// More than one physical slot is now available. The first promoted waiter
	// must still complete dispatch handoff before the second can claim.
	configureQueue(t, f, key.keyID, 3, 0, 1, 0, "queue", "reject")
	a := receiveQueuedClaim(t, first)
	if a.err != nil {
		t.Fatal(a.err)
	}
	select {
	case b := <-second:
		t.Fatalf("second waiter overtook dispatch handoff: %v", b.err)
	default:
	}
	if _, err := f.service.Claim(ctx, debug); len(KeyQueueCandidates(err)) != 1 {
		t.Fatalf("newcomer bypassed handoff: %v", err)
	}
	// Existing second ticket survives the reduced queue cap; a new one does not.
	if _, err := f.service.WaitForKey(ctx, KeyQueueCandidates(limited)); !errors.Is(err, ErrKeyRateLimited) {
		t.Fatalf("handoff cap: %v", err)
	}
	dispatchLimitClaim(t, f, a.handle)
	b := receiveQueuedClaim(t, second)
	if b.err != nil {
		t.Fatal(b.err)
	}
	dispatchLimitClaim(t, f, b.handle)
	completeLimitClaim(t, f, active)
	completeLimitClaim(t, f, a.handle)
	completeLimitClaim(t, f, b.handle)
	awaitQueueSize(t, f.service, key.keyID, 0)
	if len(KeyQueueCandidates(ErrKeyRateLimited)) != 0 {
		t.Fatal("ordinary backoff sentinel became queueable")
	}
}

func TestKeyQueueMixedModesAndRPMBoundary(t *testing.T) {
	f := newClaimFixture(t)
	owner := f.seedUser("queue-rpm", false)
	key := f.seedKey(owner, "queue-rpm")
	configureQueue(t, f, key.keyID, 1, 1, 5, 1, "queue", "reject")
	active, err := f.service.Claim(t.Context(), selfLimitInput(f, owner, key))
	if err != nil {
		t.Fatal(err)
	}
	dispatchLimitClaim(t, f, active)
	input := selfLimitInput(f, owner, key)
	if _, err := f.service.Claim(t.Context(), input); !errors.Is(err, ErrKeyRateLimited) || len(KeyQueueCandidates(err)) != 0 {
		t.Fatalf("reject must win mixed blocking: %v", err)
	}
	configureQueue(t, f, key.keyID, 1, 1, 5, 1, "queue", "queue")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := startQueuedClaim(t, f, ctx, input)
	awaitQueueSize(t, f.service, key.keyID, 1)
	other := selfLimitInput(f, owner, key)
	_, limited := f.service.Claim(ctx, other)
	if _, err := f.service.WaitForKey(ctx, KeyQueueCandidates(limited)); !errors.Is(err, ErrKeyRateLimited) {
		t.Fatalf("RPM queue cap must constrain shared queue: %v", err)
	}
	completeLimitClaim(t, f, active)
	f.clock.Store(1059)
	f.service.NotifyKey(key.keyID)
	state, err := f.service.probeKeyQueue(ctx, input)
	if err != nil || state.blocked != keyRPM {
		t.Fatalf("RPM before boundary: %+v %v", state, err)
	}
	f.clock.Store(1060)
	f.service.NotifyKey(key.keyID)
	got := receiveQueuedClaim(t, done)
	if got.err != nil {
		t.Fatal(got.err)
	}
	if _, err := f.service.ReleaseUndispatched(ctx, got.handle); err != nil {
		t.Fatal(err)
	}
	var dispatches int
	if err := f.db.QueryRow(`SELECT count(*) FROM dispatch_claims WHERE dispatched_at IS NOT NULL`).Scan(&dispatches); err != nil || dispatches != 1 {
		t.Fatalf("waiting consumed RPM: %d %v", dispatches, err)
	}
}

func TestKeyQueueWaitUsesReaderAndCancellationRemovesTicket(t *testing.T) {
	f := newClaimFixture(t)
	owner := f.seedUser("queue-reader", false)
	key := f.seedKey(owner, "queue-reader")
	configureQueue(t, f, key.keyID, 1, 0, 0, 0, "queue", "reject")
	active, err := f.service.Claim(t.Context(), selfLimitInput(f, owner, key))
	if err != nil {
		t.Fatal(err)
	}
	input := selfLimitInput(f, owner, key)
	_, limited := f.service.Claim(t.Context(), input)
	dbtest.WithoutWriter(t, f.db, f.store.ReadDB(), func(ctx context.Context) {
		waiting, cancel := context.WithCancel(ctx)
		done := make(chan queuedClaimResult, 1)
		go func() {
			h, e := f.service.WaitForKey(waiting, KeyQueueCandidates(limited))
			done <- queuedClaimResult{h, e}
		}()
		awaitQueueSize(t, f.service, key.keyID, 1)
		if _, err := f.service.probeKeyQueue(ctx, input); err != nil {
			t.Fatal(err)
		}
		cancel()
		if got := receiveQueuedClaim(t, done); !errors.Is(got.err, context.Canceled) {
			t.Fatalf("cancellation: %v", got.err)
		}
		awaitQueueSize(t, f.service, key.keyID, 0)
	})
	if _, err := f.service.ReleaseUndispatched(t.Context(), active); err != nil {
		t.Fatal(err)
	}
}

func TestKeyQueueConfigRevocationAndShutdown(t *testing.T) {
	for _, change := range []string{"reject", "disabled", "shutdown"} {
		t.Run(change, func(t *testing.T) {
			f := newClaimFixture(t)
			owner := f.seedUser("queue-change", false)
			key := f.seedKey(owner, "queue-change")
			configureQueue(t, f, key.keyID, 1, 0, 0, 0, "queue", "reject")
			active, err := f.service.Claim(t.Context(), selfLimitInput(f, owner, key))
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := startQueuedClaim(t, f, ctx, selfLimitInput(f, owner, key))
			awaitQueueSize(t, f.service, key.keyID, 1)
			want := ErrKeyRateLimited
			switch change {
			case "reject":
				configureQueue(t, f, key.keyID, 1, 0, 0, 0, "reject", "reject")
			case "disabled":
				if _, err := f.db.Exec(`UPDATE endpoint_keys SET enabled=0 WHERE id=?`, key.keyID); err != nil {
					t.Fatal(err)
				}
				f.service.NotifyKey(key.keyID)
				want = ErrNotFound
			case "shutdown":
				f.service.StopKeyWaiters()
				want = ErrDependencyUnavailable
			}
			if got := receiveQueuedClaim(t, done); !errors.Is(got.err, want) {
				t.Fatalf("change=%s error=%v want=%v", change, got.err, want)
			}
			awaitQueueSize(t, f.service, key.keyID, 0)
			if _, err := f.service.ReleaseUndispatched(ctx, active); err != nil {
				t.Fatal("settlement unavailable after queue stop", err)
			}
		})
	}
}

func TestBalancedClaimPrefersImmediatelyAvailableKeyToQueue(t *testing.T) {
	f, owner, model, keys := personalRoutingFixture(t, "cache_balanced")
	configureQueue(t, f, keys[0].keyID, 1, 0, 0, 0, "queue", "reject")
	active, err := f.service.Claim(t.Context(), selfLimitInput(f, owner, keys[0]))
	if err != nil {
		t.Fatal(err)
	}
	input := personalRoutingInput(t, f, owner, model, keys[0], keys[1])
	handle, err := f.service.Claim(t.Context(), input)
	if err != nil || handle.EndpointKeyID() != keys[1].keyID {
		t.Fatalf("available balanced fallback: key=%d %v", handle.EndpointKeyID(), err)
	}
	if _, err := f.service.ReleaseUndispatched(t.Context(), handle); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.ReleaseUndispatched(t.Context(), active); err != nil {
		t.Fatal(err)
	}
}

func TestBalancedAffinityQueuesBeforeIdleKeyAndFallsBackWhenFull(t *testing.T) {
	for _, charity := range []bool{false, true} {
		name := "personal"
		if charity {
			name = "charity"
		}
		t.Run(name, func(t *testing.T) {
			var f *claimFixture
			var keys []testKey
			var active Handle
			var next func() ClaimInput
			if charity {
				var caller int64
				var donations []int64
				f, caller, keys, donations = routingFixture(t, 300)
				active = routingClaim(t, f, caller, keys[0], donations[0])
				next = func() ClaimInput {
					request := f.acceptCharity(caller, 1)
					return ClaimInput{RequestID: request.ID, ActorUserID: caller, AttemptSeq: 1, Purpose: PurposeCharity, BalancedCandidates: []BalancedCandidate{{Candidate: keys[1].charityCandidates[1], DonationKeyID: donations[1]}, {Candidate: keys[0].charityCandidates[1], DonationKeyID: donations[0]}}}
				}
			} else {
				var owner, model int64
				f, owner, model, keys = personalRoutingFixture(t, "cache_balanced")
				active = personalRoutingClaim(t, f, owner, model, keys[0])
				next = func() ClaimInput { return personalRoutingInput(t, f, owner, model, keys[1], keys[0]) }
			}
			dispatchRoutingClaim(t, f, active)
			configureQueue(t, f, keys[0].keyID, 1, 0, 1, 0, "queue", "reject")
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := startQueuedClaim(t, f, ctx, next())
			awaitQueueSize(t, f.service, keys[0].keyID, 1)
			_, limited := f.service.Claim(ctx, next())
			fallback, err := f.service.WaitForKey(ctx, KeyQueueCandidates(limited))
			if err != nil || fallback.EndpointKeyID() != keys[1].keyID {
				t.Fatalf("full affinity queue did not use idle candidate: key=%d %v", fallback.EndpointKeyID(), err)
			}
			if _, err := f.service.ReleaseUndispatched(ctx, fallback); err != nil {
				t.Fatal(err)
			}
			select {
			case result := <-done:
				t.Fatalf("queued affinity migrated to idle key: %v", result.err)
			default:
			}
			completeLimitClaim(t, f, active)
			result := receiveQueuedClaim(t, done)
			if result.err != nil || result.handle.EndpointKeyID() != keys[0].keyID {
				t.Fatalf("affinity waiter key=%d error=%v", result.handle.EndpointKeyID(), result.err)
			}
			if _, err := f.service.ReleaseUndispatched(ctx, result.handle); err != nil {
				t.Fatal(err)
			}
		})
	}
}
