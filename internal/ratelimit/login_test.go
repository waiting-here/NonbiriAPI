package ratelimit

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func loginTestConfig() LoginThrottleConfig {
	return LoginThrottleConfig{
		MaxFailures:       3,
		Window:            10 * time.Second,
		LockDuration:      5 * time.Second,
		MaxEntries:        4,
		MaxFailuresPerKey: 4,
		MaxComponentBytes: 16,
	}
}

func TestLoginThrottleThresholdLockSuccessAndExpiry(t *testing.T) {
	clock := newFakeClock()
	throttle, err := NewLoginThrottle(loginTestConfig(), WithClock(clock))
	if err != nil {
		t.Fatal(err)
	}
	defer throttle.Close()

	for i := 0; i < 2; i++ {
		decision, err := throttle.Failure("  client-a ", "Admin")
		if err != nil || !decision.Allowed || decision.Locked || decision.FailureCount != i+1 {
			t.Fatalf("failure %d = %#v, %v", i+1, decision, err)
		}
	}
	decision, err := throttle.Failure("client-a", "admin")
	if err != nil || decision.Allowed || !decision.Locked || decision.Reason != LoginLocked || decision.RetryAfterSeconds != 5 {
		t.Fatalf("threshold lock = %#v, %v", decision, err)
	}
	clock.Advance(time.Second)
	before, err := throttle.RetryAfterSeconds("client-a", "ADMIN")
	if err != nil || before != 4 {
		t.Fatalf("retry after one second = %d, %v", before, err)
	}
	if decision, err := throttle.Failure("client-a", "admin"); err != nil || !decision.Locked {
		t.Fatalf("locked failure should remain locked = %#v, %v", decision, err)
	}
	clock.Advance(4 * time.Second)
	if locked, err := throttle.Locked("client-a", "admin"); err != nil || locked {
		t.Fatalf("lock should expire at exact boundary = %v, %v", locked, err)
	}
	if decision, err := throttle.Failure("client-a", "admin"); err != nil || !decision.Allowed || decision.FailureCount != 1 {
		t.Fatalf("post-lock failure = %#v, %v", decision, err)
	}
}

func TestLoginThrottleSuccessClearsNormalizedPairOnly(t *testing.T) {
	clock := newFakeClock()
	throttle, err := NewLoginThrottle(loginTestConfig(), WithClock(clock))
	if err != nil {
		t.Fatal(err)
	}
	defer throttle.Close()

	for i := 0; i < 2; i++ {
		if _, err := throttle.Failure("client-a", "Admin"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := throttle.Failure("client-a", "other"); err != nil {
		t.Fatal(err)
	}
	if err := throttle.Success(" client-a ", " admin "); err != nil {
		t.Fatal(err)
	}
	if decision, err := throttle.Check("client-a", "ADMIN"); err != nil || !decision.Allowed || decision.FailureCount != 0 {
		t.Fatalf("success did not clear normalized pair = %#v, %v", decision, err)
	}
	if decision, err := throttle.Check("client-a", "other"); err != nil || !decision.Allowed || decision.FailureCount != 1 {
		t.Fatalf("success affected another username = %#v, %v", decision, err)
	}
	for i := 0; i < 2; i++ {
		if decision, err := throttle.Failure("client-a", "ADMIN"); err != nil || !decision.Allowed {
			t.Fatalf("cleared pair failure %d = %#v, %v", i, decision, err)
		}
	}
}

func TestLoginThrottleWindowBoundaryAndKeyIsolation(t *testing.T) {
	clock := newFakeClock()
	config := loginTestConfig()
	config.MaxFailures = 2
	throttle, err := NewLoginThrottle(config, WithClock(clock))
	if err != nil {
		t.Fatal(err)
	}
	defer throttle.Close()

	if _, err := throttle.Failure("a|b", "c"); err != nil {
		t.Fatal(err)
	}
	if _, err := throttle.Failure("a", "b|c"); err != nil {
		t.Fatal(err)
	}
	if decision, err := throttle.Check("a|b", "c"); err != nil || decision.FailureCount != 1 {
		t.Fatalf("structured key collision = %#v, %v", decision, err)
	}
	clock.Advance(10 * time.Second)
	if decision, err := throttle.Failure("a|b", "c"); err != nil || !decision.Allowed || decision.FailureCount != 1 {
		t.Fatalf("exact failure-window boundary = %#v, %v", decision, err)
	}
}

func TestLoginThrottleBoundsCapacityAndControls(t *testing.T) {
	clock := newFakeClock()
	config := loginTestConfig()
	config.MaxEntries = 2
	config.MaxComponentBytes = 4
	throttle, err := NewLoginThrottle(config, WithClock(clock))
	if err != nil {
		t.Fatal(err)
	}
	defer throttle.Close()

	for _, pair := range [][2]string{{"a", "u1"}, {"b", "u2"}} {
		if _, err := throttle.Failure(pair[0], pair[1]); err != nil {
			t.Fatal(err)
		}
	}
	if decision, err := throttle.Failure("c", "u3"); !errors.Is(err, ErrCapacity) || !decision.Locked || decision.Reason != LoginCapacity {
		t.Fatalf("login capacity = %#v, %v", decision, err)
	}
	if _, err := throttle.Failure("a\n", "u"); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("control identity error = %v", err)
	}
	if _, err := throttle.Failure("longer", "u"); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("overlong identity error = %v", err)
	}
	clock.Advance(10 * time.Second)
	if decision, err := throttle.Failure("c", "u3"); err != nil || !decision.Allowed {
		t.Fatalf("expired login entries were not purged = %#v, %v", decision, err)
	}
}

func TestLoginThrottleConcurrentFailuresAreSerialized(t *testing.T) {
	clock := newFakeClock()
	config := loginTestConfig()
	config.MaxFailures = 10
	config.MaxFailuresPerKey = 10
	throttle, err := NewLoginThrottle(config, WithClock(clock))
	if err != nil {
		t.Fatal(err)
	}
	defer throttle.Close()

	const attempts = 100
	results := make(chan LoginDecision, attempts)
	errs := make(chan error, attempts)
	var wg sync.WaitGroup
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			decision, err := throttle.Failure("same", "user")
			results <- decision
			errs <- err
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	locked := 0
	for decision := range results {
		if decision.Locked {
			locked++
		}
	}
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if locked != attempts-config.MaxFailures+1 {
		t.Fatalf("locked concurrent failures = %d, want %d", locked, attempts-config.MaxFailures+1)
	}
}

func TestLoginThrottleCloseIsIdempotent(t *testing.T) {
	throttle, err := NewLoginThrottle(loginTestConfig())
	if err != nil {
		t.Fatal(err)
	}
	if err := throttle.Close(); err != nil {
		t.Fatal(err)
	}
	if err := throttle.Shutdown(); err != nil {
		t.Fatal(err)
	}
	if _, err := throttle.Check("a", "u"); !errors.Is(err, ErrClosed) {
		t.Fatalf("check after close = %v", err)
	}
	if err := throttle.Success("a", "u"); !errors.Is(err, ErrClosed) {
		t.Fatalf("success after close = %v", err)
	}
}
