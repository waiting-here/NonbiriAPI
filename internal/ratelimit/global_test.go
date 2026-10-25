package ratelimit

import (
	"context"
	"testing"
	"time"
)

func TestGlobalOnlySharesWindowWithoutPersonalBucketAndStartsAtDispatch(t *testing.T) {
	clock := newFakeClock()
	config := rpmTestConfig()
	config.GlobalLimit = 2
	config.PerUserLimit = 1
	config.MaxUserKeys = 1
	limiter, err := NewRPM(config, WithClock(clock))
	if err != nil {
		t.Fatal(err)
	}
	defer limiter.Close()
	first, decision, err := limiter.ReserveGlobal(context.Background())
	if err != nil || !decision.Allowed {
		t.Fatal(decision, err)
	}
	user, decision, err := limiter.Reserve(context.Background(), "person")
	if err != nil || !decision.Allowed {
		t.Fatal("global request consumed user bucket", decision, err)
	}
	if next, decision, err := limiter.ReserveGlobal(context.Background()); err != nil || next != nil || decision.Reason != RPMGlobalLimit {
		t.Fatal("separate global counter", decision, err)
	}
	user.Release()
	clock.Advance(20 * time.Second)
	held, decision, err := limiter.ReserveGlobal(context.Background())
	if err != nil || held == nil || decision.GlobalCount != 2 {
		t.Fatal("pruning removed a still-waiting global call", decision, err)
	}
	if third, decision, err := limiter.ReserveGlobal(context.Background()); err != nil || third != nil || decision.Reason != RPMGlobalLimit {
		t.Fatal("expired provisional deadline bypassed cap", decision, err)
	}
	held.Release()

	if current, _, _ := limiter.SampleGlobal(); current != 1 {
		t.Fatal("waiting reservation expired before dispatch", current)
	}
	first.Commit()
	clock.Advance(9 * time.Second)
	if current, _, _ := limiter.SampleGlobal(); current != 1 {
		t.Fatal("window did not start at dispatch", current)
	}
	clock.Advance(2 * time.Second)
	if current, _, _ := limiter.SampleGlobal(); current != 0 {
		t.Fatal("dispatched hit never expired", current)
	}
}
