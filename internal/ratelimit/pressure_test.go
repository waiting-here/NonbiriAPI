package ratelimit

import (
	"strconv"
	"testing"
	"time"
)

func TestGlobalPressureKeepsBurstAndDoesNotConsumeQuota(t *testing.T) {
	clock := newFakeClock()
	limiter, err := NewRPM(DefaultRPMConfig(), WithClock(clock))
	if err != nil {
		t.Fatal(err)
	}
	defer limiter.Close()
	first, _, err := limiter.Reserve(t.Context(), "caller")
	if err != nil || first == nil {
		t.Fatal(err)
	}
	second, _, err := limiter.Reserve(t.Context(), "caller")
	if err != nil || second == nil {
		t.Fatal(err)
	}
	first.Commit()
	second.Release()
	current, peak, limit := limiter.SampleGlobal()
	if current != 1 || peak != 2 || limit != 600 {
		t.Fatal(current, peak, limit)
	}
	if err := limiter.SetLimits(RPMLimits{GlobalLimit: 10, PerUserLimit: 5}); err != nil {
		t.Fatal(err)
	}
	clock.Advance(time.Minute)
	current, peak, limit = limiter.SampleGlobal()
	if current != 0 || peak != 1 || limit != 10 {
		t.Fatal(current, peak, limit)
	}
	current, peak, _ = limiter.SampleGlobal()
	if current != 0 || peak != 0 {
		t.Fatal("observation consumed quota", current, peak)
	}
}

func BenchmarkGlobalPressureSample(b *testing.B) {
	limiter, err := NewRPM(DefaultRPMConfig(), WithClock(newFakeClock()))
	if err != nil {
		b.Fatal(err)
	}
	defer limiter.Close()
	for i := range 600 {
		if decision, err := limiter.Record(strconv.Itoa(i)); err != nil || !decision.Allowed {
			b.Fatal(decision, err)
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		limiter.SampleGlobal()
	}
}
