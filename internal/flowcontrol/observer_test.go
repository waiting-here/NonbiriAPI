package flowcontrol

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/requestattempt"
)

type recordedObservations struct {
	mu     sync.Mutex
	values []Observation
}

func (o *recordedObservations) Observe(v Observation) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.values = append(o.values, v)
}
func TestObserverUsesAuthoritativeAdmissionAndExistingIdentity(t *testing.T) {
	clock := newFakeClock()
	observer := &recordedObservations{}
	controller, err := newWithClock(Config{RPM: testRPMConfig(), Observer: observer}, clock)
	if err != nil {
		t.Fatal(err)
	}
	defer controller.Close()
	ctx, id, err := requestattempt.New(context.Background(), 19, "POST", "/v1/chat/completions")
	if err != nil {
		t.Fatal(err)
	}
	reservation, _, err := controller.Admit(ctx, 19)
	if err != nil {
		t.Fatal(err)
	}
	clock.Advance(time.Second)
	if !reservation.Commit() || reservation.Release() {
		t.Fatal("terminal transition is not idempotent")
	}
	controller.NotifyUserLimitsChanged(19)
	want := []string{"concurrency_acquire", "rpm_reserve", "rpm_commit", "concurrency_release", "limits_changed"}
	if len(observer.values) != len(want) {
		t.Fatalf("events: %+v", observer.values)
	}
	for i, value := range observer.values {
		if value.Event != want[i] || value.UserID != 19 {
			t.Fatalf("event %d: %+v", i, value)
		}
		if i < 4 && value.RequestID != id {
			t.Fatal("request identity drift")
		}
	}
	if observer.values[0].Active != 1 || observer.values[0].ConcurrencyLimit != 5 || observer.values[1].RPMLimit != 2 || observer.values[3].Active != 0 {
		t.Fatalf("snapshots: %+v", observer.values)
	}
	if !observer.values[1].AdmittedAt.Equal(observer.values[2].AdmittedAt) {
		t.Fatal("settlement moved admission minute")
	}
	controller.ObserveAuditBoundary()
	if len(observer.values) != 7 || observer.values[5].Event != "concurrency_boundary" || observer.values[6].Event != "rpm_boundary" || observer.values[5].At.After(observer.values[6].At) {
		t.Fatalf("independent admission fences: %+v", observer.values)
	}
}
