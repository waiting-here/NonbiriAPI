package ratelimit

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func charityLimiter(t *testing.T, clock *fakeClock) *RPM {
	t.Helper()
	config := DefaultRPMConfig()
	config.GlobalLimit, config.PerUserLimit, config.CharityPerUserLimit = 100, 50, 2
	r, err := NewRPM(config, WithClock(clock))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	return r
}
func reserveModel(t *testing.T, r *RPM, charity bool) *RPMReservation {
	t.Helper()
	p, d, err := r.ReserveModelObserved(context.Background(), "user", charity, 0, nil)
	if err != nil || !d.Allowed || p == nil {
		t.Fatalf("reserve: %+v %v", d, err)
	}
	return p
}
func TestCharityWindowExcludesPersonalCallsAndRestoresItsClassification(t *testing.T) {
	clock := newFakeClock()
	r := charityLimiter(t, clock)
	for range 3 {
		reserveModel(t, r, false).Commit()
	}
	for range 2 {
		reserveModel(t, r, true).Commit()
	}
	_, facts, err := r.SnapshotUser("user")
	if err != nil || len(facts) != 5 {
		t.Fatalf("snapshot: %+v %v", facts, err)
	}
	restored := charityLimiter(t, clock)
	if err := restored.MergeUser("user", facts); err != nil {
		t.Fatal(err)
	}
	for _, limiter := range []*RPM{r, restored} {
		p, d, err := limiter.ReserveModelObserved(context.Background(), "user", true, 0, nil)
		if err != nil || p != nil || d.Allowed || d.Reason != RPMCharityUserLimit || d.CharityCount != 2 || d.UserCount != 5 || d.RetryAfter != time.Minute {
			t.Fatalf("charity limit: %+v %v", d, err)
		}
		limits := limiter.Limits()
		limits.CharityPerUserLimit = 3
		if err := limiter.SetLimits(limits); err != nil {
			t.Fatal(err)
		}
		p = reserveModel(t, limiter, true)
		p.Release()
	}
	clock.Advance(time.Minute)
	reserveModel(t, r, true).Release()
}
func TestConcurrentCharityAdmissionsUseOneSharedCounter(t *testing.T) {
	r := charityLimiter(t, newFakeClock())
	var accepted atomic.Int32
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			p, d, err := r.ReserveModelObserved(context.Background(), "user", true, 0, nil)
			if err != nil {
				t.Error(err)
				return
			}
			if d.Allowed {
				accepted.Add(1)
				p.Commit()
			}
		})
	}
	wg.Wait()
	if accepted.Load() != 2 {
		t.Fatalf("charity cap overshot: %d", accepted.Load())
	}
	_, facts, err := r.SnapshotUser("user")
	if err != nil || len(facts) != 2 || !facts[0].Charity || !facts[1].Charity {
		t.Fatalf("facts: %+v %v", facts, err)
	}
}
func TestCharityExhaustionTakesPriorityAndPersonalRemainsGlobal(t *testing.T) {
	r := charityLimiter(t, newFakeClock())
	limits := r.Limits()
	limits.GlobalLimit, limits.PerUserLimit, limits.CharityPerUserLimit = 2, 2, 2
	if err := r.SetLimits(limits); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		reserveModel(t, r, true).Commit()
	}
	for _, tc := range []struct {
		charity bool
		want    RPMReason
	}{{true, RPMCharityUserLimit}, {false, RPMGlobalLimit}} {
		p, d, err := r.ReserveModelObserved(context.Background(), "user", tc.charity, 0, nil)
		if err != nil || p != nil || d.Allowed || d.Reason != tc.want {
			t.Fatalf("priority: %+v %v", d, err)
		}
	}
	limits.GlobalLimit = 10
	r.SetLimits(limits)
	_, d, _ := r.ReserveModelObserved(context.Background(), "user", false, 1, nil)
	if d.Reason != RPMUserLimit {
		t.Fatalf("personal reason: %+v", d)
	}
}
