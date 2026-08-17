package ratelimit

import (
	"fmt"
	"testing"
	"time"
)

func TestLazyCleanupRemovesManyExpiredKeys(t *testing.T) {
	clock := newFakeClock()

	rpmConfig := RPMConfig{
		Window:       time.Second,
		GlobalLimit:  128,
		PerUserLimit: 1,
		MaxUserKeys:  128,
		MaxEvents:    128,
		MaxKeyBytes:  32,
	}
	rpm, err := NewRPM(rpmConfig, WithClock(clock))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 128; i++ {
		if decision, err := rpm.Record(fmt.Sprintf("u-%d", i)); err != nil || !decision.Allowed {
			t.Fatalf("rpm key %d = %#v, %v", i, decision, err)
		}
	}
	clock.Advance(time.Second)
	if err := rpm.Purge(); err != nil {
		t.Fatal(err)
	}
	if len(rpm.global) != 0 || len(rpm.users) != 0 {
		t.Fatalf("rpm expired state global=%d users=%d", len(rpm.global), len(rpm.users))
	}
	if err := rpm.Close(); err != nil {
		t.Fatal(err)
	}

	ipConfig := IPThrottleConfig{
		Limit:         1,
		Window:        time.Second,
		Penalty:       time.Second,
		MaxKeys:       128,
		MaxHitsPerKey: 1,
		MaxKeyBytes:   32,
	}
	ip, err := NewIPThrottle(ipConfig, WithClock(clock))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 128; i++ {
		if decision, err := ip.Allow(fmt.Sprintf("ip-%d", i)); err != nil || !decision.Allowed {
			t.Fatalf("ip key %d = %#v, %v", i, decision, err)
		}
	}
	clock.Advance(time.Second)
	if err := ip.Purge(); err != nil {
		t.Fatal(err)
	}
	if len(ip.entries) != 0 {
		t.Fatalf("ip expired state = %d", len(ip.entries))
	}
	if err := ip.Close(); err != nil {
		t.Fatal(err)
	}

	loginConfig := LoginThrottleConfig{
		MaxFailures:       1,
		Window:            time.Second,
		LockDuration:      time.Second,
		MaxEntries:        128,
		MaxFailuresPerKey: 1,
		MaxComponentBytes: 32,
	}
	login, err := NewLoginThrottle(loginConfig, WithClock(clock))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 128; i++ {
		if decision, err := login.Failure(fmt.Sprintf("client-%d", i), "user"); err != nil || !decision.Locked {
			t.Fatalf("login key %d = %#v, %v", i, decision, err)
		}
	}
	clock.Advance(time.Second)
	if err := login.Purge(); err != nil {
		t.Fatal(err)
	}
	if len(login.entries) != 0 {
		t.Fatalf("login expired state = %d", len(login.entries))
	}
	if err := login.Close(); err != nil {
		t.Fatal(err)
	}

	probeConfig := ProbeLimiterConfig{
		Window:             time.Second,
		DefaultLimit:       1,
		MaxUsers:           128,
		MaxAttemptsPerUser: 1,
	}
	probe, err := NewProbeLimiter(probeConfig, WithClock(clock))
	if err != nil {
		t.Fatal(err)
	}
	for i := int64(1); i <= 128; i++ {
		if decision, err := probe.Allow(i, 0); err != nil || !decision.Allowed {
			t.Fatalf("probe user %d = %#v, %v", i, decision, err)
		}
	}
	clock.Advance(time.Second)
	if err := probe.Purge(); err != nil {
		t.Fatal(err)
	}
	if len(probe.hits) != 0 {
		t.Fatalf("probe expired state = %d", len(probe.hits))
	}
	if err := probe.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestRPMRuntimeLimitsAreSharedWithoutResettingState(t *testing.T) {
	clock := newFakeClock()
	rpm, err := NewRPM(RPMConfig{
		Window:       time.Second,
		GlobalLimit:  4,
		PerUserLimit: 4,
		MaxUserKeys:  4,
		MaxEvents:    4,
	}, WithClock(clock))
	if err != nil {
		t.Fatal(err)
	}
	defer rpm.Close()
	for i := 0; i < 2; i++ {
		if decision, err := rpm.Record("u"); err != nil || !decision.Allowed {
			t.Fatalf("seed record = %#v, %v", decision, err)
		}
	}
	if err := rpm.SetLimits(RPMLimits{GlobalLimit: 2, PerUserLimit: 2}); err != nil {
		t.Fatal(err)
	}
	if decision, err := rpm.Record("u"); err != nil || decision.Allowed || decision.Reason != RPMGlobalLimit {
		t.Fatalf("lowered shared limit = %#v, %v", decision, err)
	}
	clock.Advance(time.Second)
	if decision, err := rpm.Record("u"); err != nil || !decision.Allowed {
		t.Fatalf("lowered limit did not recover after window = %#v, %v", decision, err)
	}
}
