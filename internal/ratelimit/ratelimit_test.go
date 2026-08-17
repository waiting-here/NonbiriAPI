package ratelimit

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeClock struct {
	mu  sync.RWMutex
	now time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Unix(1_700_000_000, 0)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

func (c *fakeClock) Set(now time.Time) {
	c.mu.Lock()
	c.now = now
	c.mu.Unlock()
}

func rpmTestConfig() RPMConfig {
	return RPMConfig{
		Window:       10 * time.Second,
		GlobalLimit:  3,
		PerUserLimit: 2,
		MaxUserKeys:  8,
		MaxEvents:    8,
		MaxKeyBytes:  16,
	}
}

func TestRPMWindowBoundaryAndAtomicReservation(t *testing.T) {
	clock := newFakeClock()
	limiter, err := NewRPM(rpmTestConfig(), WithClock(clock))
	if err != nil {
		t.Fatal(err)
	}
	defer limiter.Close()

	if decision, err := limiter.Check("u1"); err != nil || !decision.Allowed || decision.GlobalCount != 0 {
		t.Fatalf("initial check = %#v, %v", decision, err)
	}
	if decision, err := limiter.Record("u1"); err != nil || !decision.Allowed || decision.GlobalCount != 1 || decision.UserCount != 1 {
		t.Fatalf("first record = %#v, %v", decision, err)
	}
	reservation, decision, err := limiter.Reserve(context.Background(), "u1")
	if err != nil || !decision.Allowed || decision.GlobalCount != 2 || decision.UserCount != 2 {
		t.Fatalf("reservation = %#v, %v, %v", reservation, decision, err)
	}
	if !reservation.Active() {
		t.Fatal("new reservation should be active")
	}
	if decision, err := limiter.Record("u1"); err != nil || decision.Allowed || decision.Reason != RPMUserLimit {
		t.Fatalf("user cap after reservation = %#v, %v", decision, err)
	}
	if !reservation.Release() || reservation.Release() || reservation.Commit() {
		t.Fatal("release must win exactly once and be idempotent")
	}
	if reservation.Active() {
		t.Fatal("released reservation remains active")
	}
	if decision, err := limiter.Record("u1"); err != nil || !decision.Allowed || decision.UserCount != 2 {
		t.Fatalf("record after release = %#v, %v", decision, err)
	}
	if decision, err := limiter.Record("u2"); err != nil || !decision.Allowed || decision.GlobalCount != 3 {
		t.Fatalf("global fill = %#v, %v", decision, err)
	}
	if decision, err := limiter.Record("u3"); err != nil || decision.Allowed || decision.Reason != RPMGlobalLimit {
		t.Fatalf("global denial = %#v, %v", decision, err)
	}

	clock.Advance(10 * time.Second)
	if decision, err := limiter.Record("u3"); err != nil || !decision.Allowed {
		t.Fatalf("event at exact window boundary should expire = %#v, %v", decision, err)
	}
}

func TestRPMCommitConsumesAndCancellationDoesNot(t *testing.T) {
	clock := newFakeClock()
	config := rpmTestConfig()
	config.GlobalLimit = 2
	config.PerUserLimit = 2
	limiter, err := NewRPM(config, WithClock(clock))
	if err != nil {
		t.Fatal(err)
	}
	defer limiter.Close()

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if reservation, _, err := limiter.Reserve(cancelled, "cancelled"); !errors.Is(err, context.Canceled) || reservation != nil {
		t.Fatalf("cancelled reserve = %v, %v", reservation, err)
	}
	reservation, _, err := limiter.TryReserve("u")
	if err != nil {
		t.Fatal(err)
	}
	if reservation == nil || !reservation.Commit() || reservation.Commit() || reservation.Release() {
		t.Fatal("commit must be idempotent and terminal")
	}
	if decision, err := limiter.Record("u"); err != nil || !decision.Allowed {
		t.Fatalf("second event after committed reservation = %#v, %v", decision, err)
	}
	if decision, err := limiter.Record("u"); err != nil || decision.Allowed {
		t.Fatalf("third event should hit the cap = %#v, %v", decision, err)
	}
	clock.Advance(10 * time.Second)
	if decision, err := limiter.Record("u"); err != nil || !decision.Allowed {
		t.Fatalf("committed event did not expire = %#v, %v", decision, err)
	}
}

func TestRPMGlobalLimitIsSharedAcrossConcurrentCallers(t *testing.T) {
	clock := newFakeClock()
	config := rpmTestConfig()
	config.GlobalLimit = 32
	config.PerUserLimit = 32
	config.MaxUserKeys = 128
	config.MaxEvents = 32
	limiter, err := NewRPM(config, WithClock(clock))
	if err != nil {
		t.Fatal(err)
	}
	defer limiter.Close()

	const callers = 128
	results := make(chan RPMDecision, callers)
	errs := make(chan error, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			decision, err := limiter.Record("user-" + strconv.Itoa(i+1))
			results <- decision
			errs <- err
		}(i)
	}
	wg.Wait()
	close(results)
	close(errs)

	allowed := 0
	for decision := range results {
		if decision.Allowed {
			allowed++
		}
	}
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent record error: %v", err)
		}
	}
	if allowed != config.GlobalLimit {
		t.Fatalf("allowed concurrent records = %d, want %d", allowed, config.GlobalLimit)
	}
}

func TestRPMPerUserOverrideIsAtomic(t *testing.T) {
	clock := newFakeClock()
	limiter, err := NewRPM(rpmTestConfig(), WithClock(clock))
	if err != nil {
		t.Fatal(err)
	}
	defer limiter.Close()
	if decision, err := limiter.RecordWithLimit("u", 1); err != nil || !decision.Allowed {
		t.Fatalf("override first record = %#v, %v", decision, err)
	}
	if decision, err := limiter.RecordWithLimit("u", 1); err != nil || decision.Allowed || decision.Reason != RPMUserLimit {
		t.Fatalf("override user limit = %#v, %v", decision, err)
	}
	if decision, err := limiter.Record("v"); err != nil || !decision.Allowed {
		t.Fatalf("default limit should remain independent = %#v, %v", decision, err)
	}
}

func TestRPMCapacityAndBounds(t *testing.T) {
	clock := newFakeClock()
	config := rpmTestConfig()
	config.MaxUserKeys = 1
	config.MaxKeyBytes = 4
	limiter, err := NewRPM(config, WithClock(clock))
	if err != nil {
		t.Fatal(err)
	}
	defer limiter.Close()

	if _, err := limiter.Record("long-key"); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("overlong key error = %v", err)
	}
	if decision, err := limiter.Record("u1"); err != nil || !decision.Allowed {
		t.Fatalf("first bounded key = %#v, %v", decision, err)
	}
	if decision, err := limiter.Record("u2"); !errors.Is(err, ErrCapacity) || decision.Allowed || decision.Reason != RPMCapacity {
		t.Fatalf("capacity result = %#v, %v", decision, err)
	}
	clock.Advance(10 * time.Second)
	if decision, err := limiter.Record("u2"); err != nil || !decision.Allowed {
		t.Fatalf("expired key should be cleaned = %#v, %v", decision, err)
	}
}

func TestRPMConfigAndClose(t *testing.T) {
	if _, err := NewRPM(RPMConfig{GlobalLimit: 1, PerUserLimit: 1, MaxEvents: 0}, WithClockFunc(nil)); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("nil clock/config error = %v", err)
	}
	limiter, err := NewRPM(rpmTestConfig())
	if err != nil {
		t.Fatal(err)
	}
	if err := limiter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := limiter.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := limiter.Record("u"); !errors.Is(err, ErrClosed) {
		t.Fatalf("record after close = %v", err)
	}
	if _, _, err := limiter.TryReserve("u"); !errors.Is(err, ErrClosed) {
		t.Fatalf("reserve after close = %v", err)
	}
}

func TestOpaqueKeyValidationRejectsControlAndDoesNotLogValue(t *testing.T) {
	clock := newFakeClock()
	config := rpmTestConfig()
	config.MaxKeyBytes = 32
	limiter, err := NewRPM(config, WithClock(clock))
	if err != nil {
		t.Fatal(err)
	}
	defer limiter.Close()
	for _, key := range []string{"", "bad\nkey", string([]byte{0xff})} {
		_, err := limiter.Record(key)
		if !errors.Is(err, ErrInvalidKey) {
			t.Errorf("key %q error = %v", key, err)
		}
		if key != "" && strings.Contains(err.Error(), key) {
			t.Errorf("invalid key was echoed in error: %q", key)
		}
	}
}
