package ratelimit

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
)

func TestServiceClassWindowsRemainIndependentAndRecoverable(t *testing.T) {
	clock := newFakeClock()
	config := DefaultRPMConfig()
	config.CharityPerUserLimit, config.LightCharityPerUserLimit = 1, 2
	r, err := NewRPM(config, WithClock(clock))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	reserve := func(limiter *RPM, light, charity bool, global int) (*RPMReservation, RPMDecision) {
		t.Helper()
		p, d, err := limiter.ReserveClassifiedObserved(context.Background(), "user", global, charity, light, 0, nil)
		if err != nil {
			t.Fatal(err)
		}
		return p, d
	}
	for _, light := range []bool{false, true, true} {
		p, d := reserve(r, light, true, 0)
		if p == nil || !d.Allowed {
			t.Fatalf("independent admission: %+v", d)
		}
		p.Commit()
	}
	for _, light := range []bool{false, true} {
		p, d := reserve(r, light, true, 0)
		if p != nil || d.Reason != RPMCharityUserLimit || d.Light != light {
			t.Fatalf("class exhaustion: %+v", d)
		}
	}
	p, d := reserve(r, true, false, 0)
	if p == nil || !d.Allowed {
		t.Fatalf("personal calls consumed charity cap: %+v", d)
	}
	p.Release()
	_, facts, err := r.SnapshotUser("user")
	if err != nil || len(facts) != 3 {
		t.Fatalf("snapshot: %v %v", facts, err)
	}
	restored, err := NewRPM(config, WithClock(clock))
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	for range 2 {
		if err := restored.MergeUser("user", facts); err != nil {
			t.Fatal(err)
		}
	}
	for _, light := range []bool{false, true} {
		p, d := reserve(restored, light, true, 0)
		if p != nil || d.Reason != RPMCharityUserLimit || d.UserCount != 3 {
			t.Fatalf("restored class: %+v", d)
		}
		p, d = reserve(restored, light, false, 3)
		if p != nil || d.Reason != RPMUserLimit {
			t.Fatalf("combined override: %+v", d)
		}
	}
	changed := append([]WindowFact(nil), facts...)
	changed[0].Light = !changed[0].Light
	if err := restored.MergeUser("user", changed); err != ErrInvalidConfig {
		t.Fatalf("changed replay classification: %v", err)
	}
	limits := restored.Limits()
	limits.LightCharityPerUserLimit = 3
	if err := restored.SetLimits(limits); err != nil {
		t.Fatal(err)
	}
	p, d = reserve(restored, true, true, 0)
	if p == nil || d.CharityCount != 3 || d.UserCount != 4 {
		t.Fatalf("live override erased events: %+v", d)
	}
	p.Release()
}

func TestServiceClassConcurrentReservationsShareCombinedCaps(t *testing.T) {
	config := DefaultRPMConfig()
	config.CharityPerUserLimit, config.LightCharityPerUserLimit = 2, 3
	r, err := NewRPM(config)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	var counts [2]atomic.Int32
	var wg sync.WaitGroup
	for i := range 40 {
		wg.Go(func() {
			light := i%2 == 1
			p, d, err := r.ReserveClassifiedObserved(context.Background(), "user", 0, true, light, 0, nil)
			if err != nil {
				t.Error(err)
				return
			}
			if d.Allowed {
				counts[i%2].Add(1)
				p.Commit()
			} else if d.Reason != RPMCharityUserLimit {
				t.Errorf("wrong denial: %+v", d)
			}
		})
	}
	wg.Wait()
	if counts[0].Load() != 2 || counts[1].Load() != 3 {
		t.Fatalf("independent caps: %d %d", counts[0].Load(), counts[1].Load())
	}
	limits := r.Limits()
	limits.GlobalLimit = 5
	if err := r.SetLimits(limits); err != nil {
		t.Fatal(err)
	}
	p, d, err := r.ReserveClassifiedObserved(context.Background(), "other-user", 0, false, true, 0, nil)
	if err != nil || p != nil || d.Reason != RPMGlobalLimit || d.GlobalCount != 5 {
		t.Fatalf("combined site cap: %+v %v", d, err)
	}
}
