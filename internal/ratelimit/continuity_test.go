package ratelimit

import (
	"errors"
	"testing"
	"time"
)

func TestRecoveredWindowDoesNotDoubleCountOrConsumeGlobalBudget(t *testing.T) {
	clock := newFakeClock()
	limiter, _ := NewRPM(rpmTestConfig(), WithClock(clock))
	defer limiter.Close()
	limiter.Record("identity")
	limiter.Record("identity")
	_, facts, err := limiter.SnapshotUser("identity")
	if err != nil {
		t.Fatal(err)
	}
	if err := limiter.MergeUser("identity", facts); err != nil {
		t.Fatal(err)
	}
	state, _ := limiter.Check("identity")
	if state.UserCount != 2 || state.GlobalCount != 2 {
		t.Fatalf("live merge: %+v", state)
	}
	restarted, _ := NewRPM(rpmTestConfig(), WithClock(clock))
	defer restarted.Close()
	for range 3 {
		if err := restarted.MergeUser("identity", facts); err != nil {
			t.Fatal(err)
		}
	}
	state, _ = restarted.Check("identity")
	if state.Allowed || state.UserCount != 2 || state.GlobalCount != 0 || state.Reason != RPMUserLimit {
		t.Fatalf("restart: %+v", state)
	}
	other, _ := restarted.Check("unrelated")
	if !other.Allowed {
		t.Fatal("unrelated identity denied")
	}
	clock.Advance(10 * time.Second)
	state, _ = restarted.Check("identity")
	if !state.Allowed || state.UserCount != 0 {
		t.Fatal("original expiry extended", state)
	}
}

func TestRecoveryValidationIsAtomicAndBounded(t *testing.T) {
	clock := newFakeClock()
	limiter, _ := NewRPM(rpmTestConfig(), WithClock(clock))
	defer limiter.Close()
	fact := WindowFact{ID: "a", At: clock.Now(), Expires: clock.Now().Add(time.Second)}
	bad := fact
	bad.ID = ""
	if err := limiter.MergeUser("identity", []WindowFact{fact, bad}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatal(err)
	}
	state, _ := limiter.Check("identity")
	if state.UserCount != 0 {
		t.Fatal("partial import escaped validation")
	}
	for index := range 8 {
		item := fact
		item.ID = string(rune('a' + index))
		if err := limiter.MergeUser("identity", []WindowFact{item}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := limiter.Record("other"); !errors.Is(err, ErrCapacity) {
		t.Fatal("import bypassed global storage bound", err)
	}
	clock.Advance(time.Second)
	if state, err := limiter.Record("other"); err != nil || !state.Allowed {
		t.Fatal("expired recovery retained capacity", state, err)
	}
}
