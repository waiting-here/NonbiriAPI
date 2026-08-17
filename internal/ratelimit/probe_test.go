package ratelimit

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func probeTestConfig() ProbeLimiterConfig {
	return ProbeLimiterConfig{
		Window:             10 * time.Second,
		DefaultLimit:       2,
		MaxUsers:           4,
		MaxAttemptsPerUser: 4,
	}
}

func TestProbeLimiterPerUserAndFailedAttemptAccounting(t *testing.T) {
	clock := newFakeClock()
	limiter, err := NewProbeLimiter(probeTestConfig(), WithClock(clock))
	if err != nil {
		t.Fatal(err)
	}
	defer limiter.Close()

	for i := 0; i < 2; i++ {
		decision, err := limiter.Allow(1, 0)
		if err != nil || !decision.Allowed || decision.Count != i+1 {
			t.Fatalf("user one attempt %d = %#v, %v", i+1, decision, err)
		}
	}
	if decision, err := limiter.Allow(1, 0); err != nil || decision.Allowed || decision.Reason != ProbeLimit {
		t.Fatalf("user one cap = %#v, %v", decision, err)
	}
	if decision, err := limiter.Allow(2, 0); err != nil || !decision.Allowed {
		t.Fatalf("user two must be independent = %#v, %v", decision, err)
	}
}

func TestProbeLimiterCustomCapBoundaryAndCleanup(t *testing.T) {
	clock := newFakeClock()
	config := probeTestConfig()
	config.MaxUsers = 2
	limiter, err := NewProbeLimiter(config, WithClock(clock))
	if err != nil {
		t.Fatal(err)
	}
	defer limiter.Close()

	if decision, err := limiter.Allow(1, 1); err != nil || !decision.Allowed || decision.Limit != 1 {
		t.Fatalf("custom cap = %#v, %v", decision, err)
	}
	if decision, err := limiter.Check(1, 1); err != nil || decision.Allowed || decision.RetryAfter != 10*time.Second {
		t.Fatalf("check after custom cap = %#v, %v", decision, err)
	}
	if decision, err := limiter.Allow(2, 0); err != nil || !decision.Allowed {
		t.Fatalf("second user = %#v, %v", decision, err)
	}
	if decision, err := limiter.Allow(3, 0); !errors.Is(err, ErrCapacity) || decision.Allowed || decision.Reason != ProbeCapacity {
		t.Fatalf("probe capacity = %#v, %v", decision, err)
	}
	clock.Advance(10 * time.Second)
	if decision, err := limiter.Allow(3, 0); err != nil || !decision.Allowed {
		t.Fatalf("expired users were not cleaned = %#v, %v", decision, err)
	}
	if decision, err := limiter.Allow(1, 0); err != nil || !decision.Allowed {
		t.Fatalf("exact window boundary did not expire = %#v, %v", decision, err)
	}
}

func TestProbeLimiterConcurrentPerUserCap(t *testing.T) {
	clock := newFakeClock()
	config := probeTestConfig()
	config.DefaultLimit = 16
	config.MaxAttemptsPerUser = 16
	limiter, err := NewProbeLimiter(config, WithClock(clock))
	if err != nil {
		t.Fatal(err)
	}
	defer limiter.Close()

	const attempts = 100
	results := make(chan ProbeDecision, attempts)
	errs := make(chan error, attempts)
	var wg sync.WaitGroup
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			decision, err := limiter.Allow(1, 0)
			results <- decision
			errs <- err
		}()
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
			t.Fatal(err)
		}
	}
	if allowed != config.DefaultLimit {
		t.Fatalf("allowed concurrent probes = %d, want %d", allowed, config.DefaultLimit)
	}
}

func TestProbeLimiterBoundsAndClose(t *testing.T) {
	if _, err := NewProbeLimiter(ProbeLimiterConfig{DefaultLimit: 1, MaxAttemptsPerUser: 1}, WithClockFunc(nil)); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("nil clock/config = %v", err)
	}
	limiter, err := NewProbeLimiter(probeTestConfig())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := limiter.Allow(0, 0); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("zero user id = %v", err)
	}
	if _, err := limiter.Allow(1, 99); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("overlarge custom cap = %v", err)
	}
	if err := limiter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := limiter.Shutdown(); err != nil {
		t.Fatal(err)
	}
	if _, err := limiter.Allow(1, 0); !errors.Is(err, ErrClosed) {
		t.Fatalf("allow after close = %v", err)
	}
}
