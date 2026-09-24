package ratelimit

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestObservedReservationKeepsAdmissionLimitAndSingleWinner(t *testing.T) {
	now := time.Date(2026, 9, 24, 12, 0, 59, 0, time.UTC)
	var mu sync.Mutex
	clock := ClockFunc(func() time.Time { mu.Lock(); defer mu.Unlock(); return now })
	r, err := NewRPM(RPMConfig{GlobalLimit: 100, PerUserLimit: 80}, WithClock(clock))
	if err != nil {
		t.Fatal(err)
	}
	var events []RPMObservation
	observer := RPMObserverFunc(func(value RPMObservation) { events = append(events, value) })
	reservation, decision, err := r.ReserveObserved(context.Background(), "7", 0, observer)
	if err != nil || !decision.Allowed {
		t.Fatalf("reserve: %+v %v", decision, err)
	}
	mu.Lock()
	now = now.Add(2 * time.Second)
	mu.Unlock()
	if err := r.SetLimits(RPMLimits{GlobalLimit: 100, PerUserLimit: 10}); err != nil {
		t.Fatal(err)
	}
	var done sync.WaitGroup
	done.Add(2)
	go func() { defer done.Done(); reservation.Commit() }()
	go func() { defer done.Done(); reservation.Release() }()
	done.Wait()
	if len(events) != 2 || events[0].Event != "reserve" || events[1].Decision.UserLimit != 80 || !events[1].AdmittedAt.Equal(events[0].At) || events[1].At.Minute() != 1 {
		t.Fatalf("wrong observations: %+v", events)
	}
	if reservation.Commit() || reservation.Release() {
		t.Fatal("terminal handle reused")
	}
}

func TestObservedDenialAndConfigurationUseAuthoritativeSnapshot(t *testing.T) {
	now := time.Unix(600, 0)
	r, err := NewRPM(RPMConfig{GlobalLimit: 3, PerUserLimit: 1}, WithClockFunc(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	var events []RPMObservation
	o := RPMObserverFunc(func(v RPMObservation) { events = append(events, v) })
	p, _, _ := r.ReserveObserved(context.Background(), "1", 0, o)
	p.Commit()
	_, d, err := r.ReserveObserved(context.Background(), "1", 0, o)
	if err != nil || d.Allowed || d.Reason != RPMUserLimit {
		t.Fatalf("denial %+v %v", d, err)
	}
	if err := r.SetLimitsObserved(RPMLimits{GlobalLimit: 3, PerUserLimit: 2}, o); err != nil {
		t.Fatal(err)
	}
	if len(events) != 4 || events[2].Event != "denied" || events[2].Decision.UserLimit != 1 || events[3].Event != "configuration" || events[3].Decision.UserLimit != 2 {
		t.Fatalf("events %+v", events)
	}
}
