package claim

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

func setTestKeyLimits(t *testing.T, f *claimFixture, keyID, concurrency, rpm int64) {
	t.Helper()
	if _, err := f.db.Exec(`INSERT INTO endpoint_key_limits(endpoint_key_id,max_concurrency,max_rpm) VALUES(?,?,?) ON CONFLICT(endpoint_key_id) DO UPDATE SET max_concurrency=excluded.max_concurrency,max_rpm=excluded.max_rpm`, keyID, concurrency, rpm); err != nil {
		t.Fatal(err)
	}
}

func selfLimitInput(f *claimFixture, userID int64, key testKey) ClaimInput {
	request := f.acceptSelf(userID, 1)
	return ClaimInput{RequestID: request.ID, ActorUserID: userID, AttemptSeq: 1, Purpose: PurposeSelf, Candidate: key.candidate}
}

func dispatchLimitClaim(t *testing.T, f *claimFixture, handle Handle) {
	t.Helper()
	grant, err := f.service.TakeForDispatch(context.Background(), handle)
	if err != nil {
		t.Fatal(err)
	}
	grant.Clear()
}

func completeLimitClaim(t *testing.T, f *claimFixture, handle Handle) {
	t.Helper()
	if _, err := f.service.CompleteAttempt(context.Background(), handle, AttemptOutcome{Kind: ResultSynthetic, UpstreamStatus: 502, Diagnostic: "canceled stream", ResponseStarted: true}); err != nil {
		t.Fatal(err)
	}
}

func TestKeyConcurrencyAdmissionIsAtomicAcrossPersonalCharityAndDebug(t *testing.T) {
	for _, limit := range []int64{0, 1, 3} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			f := newClaimFixture(t)
			owner := f.seedUser("limit-owner", false)
			caller := f.seedUser("limit-caller", false)
			key := f.seedKey(owner, "shared-limit")
			donationID := f.seedDonationKey(owner, key, "shared-limit", 3)
			setTestKeyLimits(t, f, key.keyID, limit, 0)
			const workers = 18
			inputs := make([]ClaimInput, workers)
			for i := range inputs {
				if i%3 == 0 {
					request := f.acceptCharity(caller, 1)
					inputs[i] = ClaimInput{RequestID: request.ID, ActorUserID: caller, AttemptSeq: 1, Purpose: PurposeCharity, Candidate: key.candidate, DonationKeyID: donationID}
				} else {
					inputs[i] = selfLimitInput(f, owner, key)
					if i%3 == 2 {
						inputs[i].Purpose = PurposeDebugLive
					}
				}
			}
			handles := make([]Handle, workers)
			errs := make([]error, workers)
			start := make(chan struct{})
			var wg sync.WaitGroup
			for i := range inputs {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					<-start
					handles[i], errs[i] = f.service.Claim(context.Background(), inputs[i])
				}(i)
			}
			close(start)
			wg.Wait()
			admitted := 0
			var winner Handle
			for i, err := range errs {
				if err == nil {
					admitted++
					winner = handles[i]
				} else if !errors.Is(err, ErrKeyRateLimited) {
					t.Fatalf("unexpected admission error: %v", err)
				}
			}
			want := workers
			if limit > 0 {
				want = int(limit)
			}
			if admitted != want {
				t.Fatalf("admitted=%d want=%d", admitted, want)
			}
			var count int
			if err := f.db.QueryRow(`SELECT COUNT(*) FROM dispatch_claims`).Scan(&count); err != nil || count != want {
				t.Fatalf("persisted=%d err=%v", count, err)
			}
			dispatchLimitClaim(t, f, winner)
			input := selfLimitInput(f, owner, key)
			if limit > 0 {
				if _, err := f.service.Claim(context.Background(), input); !errors.Is(err, ErrKeyRateLimited) {
					t.Fatalf("stream should retain concurrency: %v", err)
				}
			}
			completeLimitClaim(t, f, winner)
			if _, err := f.service.Claim(context.Background(), input); err != nil {
				t.Fatalf("completion did not free slot: %v", err)
			}
		})
	}
}

func TestKeyRPMReservationsSlidingBoundaryAndDelayedDispatch(t *testing.T) {
	f := newClaimFixture(t)
	owner := f.seedUser("rpm", false)
	key := f.seedKey(owner, "rpm")
	ctx := context.Background()
	setTestKeyLimits(t, f, key.keyID, 0, 2)
	claimOne := func(input ClaimInput) Handle {
		t.Helper()
		h, err := f.service.Claim(ctx, input)
		if err != nil {
			t.Fatal(err)
		}
		return h
	}
	first := claimOne(selfLimitInput(f, owner, key))
	second := claimOne(selfLimitInput(f, owner, key))
	thirdInput := selfLimitInput(f, owner, key)
	if _, err := f.service.Claim(ctx, thirdInput); !errors.Is(err, ErrKeyRateLimited) {
		t.Fatalf("pending slots not reserved: %v", err)
	}
	if _, err := f.service.ReleaseUndispatched(ctx, second); err != nil {
		t.Fatal(err)
	}
	second = claimOne(thirdInput)
	dispatchLimitClaim(t, f, first)
	completeLimitClaim(t, f, first)
	f.clock.Store(1030)
	dispatchLimitClaim(t, f, second)
	completeLimitClaim(t, f, second)
	next := selfLimitInput(f, owner, key)
	f.clock.Store(1059)
	if _, err := f.service.Claim(ctx, next); !errors.Is(err, ErrKeyRateLimited) {
		t.Fatalf("early RPM release: %v", err)
	}
	f.clock.Store(1060)
	third := claimOne(next)
	f.clock.Store(1200)
	fourth := claimOne(selfLimitInput(f, owner, key))
	blocked := selfLimitInput(f, owner, key)
	if _, err := f.service.Claim(ctx, blocked); !errors.Is(err, ErrKeyRateLimited) {
		t.Fatalf("delayed pending claims expired: %v", err)
	}
	dispatchLimitClaim(t, f, third)
	completeLimitClaim(t, f, third)
	if _, err := f.service.ReleaseUndispatched(ctx, fourth); err != nil {
		t.Fatal(err)
	}
	f.clock.Store(1199)
	claimOne(blocked)
	if _, err := f.service.Claim(ctx, selfLimitInput(f, owner, key)); !errors.Is(err, ErrKeyRateLimited) {
		t.Fatalf("clock rollback lost recent dispatch: %v", err)
	}
}

func TestKeyLimitsChangesPreserveAdmittedClaimsAndDiscoveryIsSeparate(t *testing.T) {
	f := newClaimFixture(t)
	owner := f.seedUser("changes", false)
	key := f.seedKey(owner, "changes")
	ctx := context.Background()
	one, err := f.service.Claim(ctx, selfLimitInput(f, owner, key))
	if err != nil {
		t.Fatal(err)
	}
	two, err := f.service.Claim(ctx, selfLimitInput(f, owner, key))
	if err != nil {
		t.Fatal(err)
	}
	setTestKeyLimits(t, f, key.keyID, 1, 1)
	next := selfLimitInput(f, owner, key)
	if _, err := f.service.Claim(ctx, next); !errors.Is(err, ErrKeyRateLimited) {
		t.Fatal(err)
	}
	dispatchLimitClaim(t, f, one)
	dispatchLimitClaim(t, f, two)
	discovery := key.candidate
	discovery.UpstreamModelID = ""
	if _, _, err := f.service.ClaimDiscovery(ctx, DiscoveryClaimInput{ActorUserID: owner, Candidate: discovery}); err != nil {
		t.Fatalf("discovery should retain separate protection: %v", err)
	}
	completeLimitClaim(t, f, one)
	completeLimitClaim(t, f, two)
	if _, err := f.service.Claim(ctx, next); !errors.Is(err, ErrKeyRateLimited) {
		t.Fatalf("earlier unlimited dispatches escaped new RPM: %v", err)
	}
	setTestKeyLimits(t, f, key.keyID, 1, 0)
	if _, err := f.service.Claim(ctx, next); err != nil {
		t.Fatalf("discovery incorrectly occupies slot: %v", err)
	}
	other := f.seedUser("other", false)
	bad := selfLimitInput(f, other, key)
	if _, err := f.service.Claim(ctx, bad); !errors.Is(err, ErrNotFound) {
		t.Fatalf("limits bypassed ownership: %v", err)
	}
}

func TestKeyLimitsSurviveServiceRestartAndRecoverPendingWork(t *testing.T) {
	for _, dispatched := range []bool{false, true} {
		t.Run(fmt.Sprint(dispatched), func(t *testing.T) {
			f := newClaimFixture(t)
			owner := f.seedUser("restart", false)
			key := f.seedKey(owner, "restart")
			ctx := context.Background()
			setTestKeyLimits(t, f, key.keyID, 1, 1)
			handle, err := f.service.Claim(ctx, selfLimitInput(f, owner, key))
			if err != nil {
				t.Fatal(err)
			}
			if dispatched {
				dispatchLimitClaim(t, f, handle)
			}
			f.clock.Store(1001)
			restarted, err := New(Dependencies{DB: f.db, Secrets: f.codec, Accounting: f.accounting, Charity: f.charity, Acceptance: allowAcceptanceGate{}, Now: func() time.Time { return time.Unix(f.clock.Load(), 0) }})
			if err != nil {
				t.Fatal(err)
			}
			f.service = restarted
			if _, err := restarted.RecoverNonterminal(ctx, 100); err != nil {
				t.Fatal(err)
			}
			next := selfLimitInput(f, owner, key)
			_, err = restarted.Claim(ctx, next)
			if dispatched {
				if !errors.Is(err, ErrKeyRateLimited) {
					t.Fatalf("restart cleared RPM: %v", err)
				}
				f.clock.Store(1060)
				_, err = restarted.Claim(ctx, next)
			}
			if err != nil {
				t.Fatalf("recovery left concurrency stuck: %v", err)
			}
		})
	}
}
