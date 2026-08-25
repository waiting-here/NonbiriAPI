package ratelimit

import (
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"
)

func ipTestConfig() IPThrottleConfig {
	return IPThrottleConfig{
		Limit:         2,
		Window:        10 * time.Second,
		Penalty:       5 * time.Second,
		MaxKeys:       4,
		MaxHitsPerKey: 4,
		MaxKeyBytes:   16,
	}
}

func TestIPThrottlePenaltyRetryAfterAndFreshWindow(t *testing.T) {
	clock := newFakeClock()
	throttle, err := NewIPThrottle(ipTestConfig(), WithClock(clock))
	if err != nil {
		t.Fatal(err)
	}
	defer throttle.Close()

	for i := 0; i < 2; i++ {
		decision, err := throttle.Allow("client-a")
		if err != nil || !decision.Allowed || decision.Count != i+1 {
			t.Fatalf("allowed attempt %d = %#v, %v", i+1, decision, err)
		}
	}
	decision, err := throttle.Allow("client-a")
	if err != nil || decision.Allowed || decision.Reason != IPPenalty || decision.RetryAfterSeconds != 5 {
		t.Fatalf("penalty = %#v, %v", decision, err)
	}
	clock.Advance(4 * time.Second)
	if retry, err := throttle.RetryAfterSeconds("client-a"); err != nil || retry != 1 {
		t.Fatalf("retry after four seconds = %d, %v", retry, err)
	}
	if decision, err := throttle.Allow("client-a"); err != nil || decision.Allowed {
		t.Fatalf("penalty should still deny = %#v, %v", decision, err)
	}
	clock.Advance(time.Second)
	if retry, err := throttle.RetryAfterSeconds("client-a"); err != nil || retry != 0 {
		t.Fatalf("expired retry = %d, %v", retry, err)
	}
	if decision, err := throttle.Allow("client-a"); err != nil || !decision.Allowed || decision.Count != 1 {
		t.Fatalf("fresh window after penalty = %#v, %v", decision, err)
	}
}

func TestIPThrottleExactWindowAndKeyIsolation(t *testing.T) {
	clock := newFakeClock()
	config := ipTestConfig()
	config.Limit = 1
	config.Penalty = time.Hour
	throttle, err := NewIPThrottle(config, WithClock(clock))
	if err != nil {
		t.Fatal(err)
	}
	defer throttle.Close()

	if decision, err := throttle.Allow("a"); err != nil || !decision.Allowed {
		t.Fatal(decision, err)
	}
	clock.Advance(10 * time.Second)
	if decision, err := throttle.Allow("a"); err != nil || !decision.Allowed {
		t.Fatalf("exact window boundary = %#v, %v", decision, err)
	}
	if decision, err := throttle.Allow("b"); err != nil || !decision.Allowed {
		t.Fatalf("different key was affected = %#v, %v", decision, err)
	}
}

func TestIPThrottleDisabledStillBoundsInput(t *testing.T) {
	clock := newFakeClock()
	config := DefaultIPThrottleConfig()
	config.MaxKeyBytes = 4
	throttle, err := NewIPThrottle(config, WithClock(clock))
	if err != nil {
		t.Fatal(err)
	}
	defer throttle.Close()
	for i := 0; i < 20; i++ {
		decision, err := throttle.Allow("ok")
		if err != nil || !decision.Allowed || decision.Reason != IPDisabled {
			t.Fatalf("disabled call %d = %#v, %v", i, decision, err)
		}
	}
	if _, err := throttle.Allow("too-long"); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("disabled overlong key error = %v", err)
	}
}

func TestIPThrottleCapacityAndExpiredCleanup(t *testing.T) {
	clock := newFakeClock()
	config := ipTestConfig()
	config.MaxKeys = 2
	throttle, err := NewIPThrottle(config, WithClock(clock))
	if err != nil {
		t.Fatal(err)
	}
	defer throttle.Close()
	for _, key := range []string{"a", "b"} {
		if decision, err := throttle.Allow(key); err != nil || !decision.Allowed {
			t.Fatalf("fill %q = %#v, %v", key, decision, err)
		}
	}
	if decision, err := throttle.Allow("c"); !errors.Is(err, ErrCapacity) || decision.Allowed || decision.Reason != IPCapacity {
		t.Fatalf("capacity = %#v, %v", decision, err)
	}
	clock.Advance(10 * time.Second)
	if decision, err := throttle.Allow("c"); err != nil || !decision.Allowed {
		t.Fatalf("expired entries were not purged = %#v, %v", decision, err)
	}
}

func TestIPThrottleRetryAfterMinimumAndConcurrentAdmission(t *testing.T) {
	clock := newFakeClock()
	config := ipTestConfig()
	config.Limit = 10
	config.Penalty = 500 * time.Millisecond
	config.MaxKeys = 1
	config.MaxHitsPerKey = 10
	throttle, err := NewIPThrottle(config, WithClock(clock))
	if err != nil {
		t.Fatal(err)
	}
	defer throttle.Close()

	const calls = 100
	results := make(chan IPDecision, calls)
	errs := make(chan error, calls)
	var wg sync.WaitGroup
	for i := 0; i < calls; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			decision, err := throttle.Allow("same")
			results <- decision
			errs <- err
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	allowed := 0
	penalized := 0
	for decision := range results {
		if decision.Allowed {
			allowed++
		} else if decision.Reason == IPPenalty {
			penalized++
		}
	}
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if allowed != config.Limit || penalized != calls-config.Limit {
		t.Fatalf("concurrent results allowed=%d penalized=%d", allowed, penalized)
	}
	if retry, err := throttle.RetryAfterSeconds("same"); err != nil || retry != 1 {
		t.Fatalf("subsecond retry must round up = %d, %v", retry, err)
	}
}

func TestIPThrottleCloseIsIdempotent(t *testing.T) {
	throttle, err := NewIPThrottle(ipTestConfig())
	if err != nil {
		t.Fatal(err)
	}
	if err := throttle.Close(); err != nil {
		t.Fatal(err)
	}
	if err := throttle.Shutdown(); err != nil {
		t.Fatal(err)
	}
	if _, err := throttle.Allow("a"); !errors.Is(err, ErrClosed) {
		t.Fatalf("allow after close = %v", err)
	}
	if _, err := throttle.RetryAfterSeconds("a"); !errors.Is(err, ErrClosed) {
		t.Fatalf("retry after close = %v", err)
	}
}

func TestIPThrottleConfigSnapshotReflectsConstruction(t *testing.T) {
	clock := newFakeClock()
	throttle, err := NewIPThrottle(ipTestConfig(), WithClock(clock))
	if err != nil {
		t.Fatal(err)
	}
	defer throttle.Close()
	config := throttle.Config()
	if config.Limit != 2 || config.Window != 10*time.Second || config.Penalty != 5*time.Second {
		t.Fatalf("config snapshot = %#v", config)
	}
}

func TestIPThrottleReconfigureChangesRateParameters(t *testing.T) {
	clock := newFakeClock()
	throttle, err := NewIPThrottle(IPThrottleConfig{
		Limit: 2, Window: 10 * time.Second, Penalty: 5 * time.Second,
		MaxKeys: 4, MaxHitsPerKey: 64, MaxKeyBytes: 16,
	}, WithClock(clock))
	if err != nil {
		t.Fatal(err)
	}
	defer throttle.Close()

	if err := throttle.Reconfigure(IPThrottleConfig{
		Limit: 5, Window: 20 * time.Second, Penalty: 10 * time.Second,
	}); err != nil {
		t.Fatalf("reconfigure: %v", err)
	}
	config := throttle.Config()
	if config.Limit != 5 || config.Window != 20*time.Second || config.Penalty != 10*time.Second {
		t.Fatalf("config after reconfigure = %#v", config)
	}
	for i := 0; i < 5; i++ {
		decision, err := throttle.Allow("ip")
		if err != nil || !decision.Allowed || decision.Limit != 5 {
			t.Fatalf("allowed attempt %d = %#v, %v", i+1, decision, err)
		}
	}
	decision, err := throttle.Allow("ip")
	if err != nil || decision.Allowed || decision.Reason != IPPenalty || decision.RetryAfterSeconds != 10 || decision.Limit != 5 {
		t.Fatalf("penalty after reconfigure = %#v, %v", decision, err)
	}
}

func TestIPThrottleReconfigureDisablesAdmission(t *testing.T) {
	clock := newFakeClock()
	throttle, err := NewIPThrottle(ipTestConfig(), WithClock(clock))
	if err != nil {
		t.Fatal(err)
	}
	defer throttle.Close()
	if err := throttle.Reconfigure(IPThrottleConfig{Limit: 0}); err != nil {
		t.Fatalf("reconfigure disabled: %v", err)
	}
	if config := throttle.Config(); config.Limit != 0 {
		t.Fatalf("disabled config = %#v", config)
	}
	for i := 0; i < 50; i++ {
		decision, err := throttle.Allow("ip")
		if err != nil || !decision.Allowed || decision.Reason != IPDisabled {
			t.Fatalf("disabled call %d = %#v, %v", i, decision, err)
		}
	}
}

func TestIPThrottleReconfigurePreservesInFlightPenalty(t *testing.T) {
	clock := newFakeClock()
	throttle, err := NewIPThrottle(IPThrottleConfig{
		Limit: 2, Window: 10 * time.Second, Penalty: 60 * time.Second,
		MaxKeys: 4, MaxHitsPerKey: 64, MaxKeyBytes: 16,
	}, WithClock(clock))
	if err != nil {
		t.Fatal(err)
	}
	defer throttle.Close()
	for i := 0; i < 2; i++ {
		if decision, err := throttle.Allow("ip"); err != nil || !decision.Allowed {
			t.Fatalf("fill attempt %d = %#v, %v", i+1, decision, err)
		}
	}
	if decision, err := throttle.Allow("ip"); err != nil || decision.Allowed || decision.Reason != IPPenalty || decision.RetryAfterSeconds != 60 {
		t.Fatalf("penalty started = %#v, %v", decision, err)
	}
	// Raising the limit mid-penalty must not lift the active block: an attacker
	// who triggered the penalty cannot buy out of it by racing a config update.
	if err := throttle.Reconfigure(IPThrottleConfig{Limit: 10, Window: 10 * time.Second, Penalty: 60 * time.Second}); err != nil {
		t.Fatalf("reconfigure: %v", err)
	}
	if decision, err := throttle.Allow("ip"); err != nil || decision.Allowed || decision.Reason != IPPenalty || decision.RetryAfterSeconds != 60 {
		t.Fatalf("penalty not preserved = %#v, %v", decision, err)
	}
	clock.Advance(60 * time.Second)
	for i := 0; i < 10; i++ {
		decision, err := throttle.Allow("ip")
		if err != nil || !decision.Allowed || decision.Limit != 10 {
			t.Fatalf("post-penalty attempt %d with new limit = %#v, %v", i+1, decision, err)
		}
	}
}

func TestIPThrottleReconfigureRejectsInvalidConfig(t *testing.T) {
	clock := newFakeClock()
	throttle, err := NewIPThrottle(ipTestConfig(), WithClock(clock))
	if err != nil {
		t.Fatal(err)
	}
	defer throttle.Close()
	for _, config := range []IPThrottleConfig{
		{Limit: -1, Window: 10 * time.Second, Penalty: 5 * time.Second},
		{Limit: 1, Window: -1 * time.Second, Penalty: 5 * time.Second},
		{Limit: 1, Window: 10 * time.Second, Penalty: -1 * time.Second},
		// Larger than the construction-time MaxHitsPerKey (4 in ipTestConfig).
		{Limit: 100, Window: 10 * time.Second, Penalty: 5 * time.Second},
	} {
		if err := throttle.Reconfigure(config); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("reconfigure %#v error = %v, want ErrInvalidConfig", config, err)
		}
	}
	// A zero Window still uses the window default, while a live zero penalty is
	// explicit and removes the continuing penalty interval.
	if err := throttle.Reconfigure(IPThrottleConfig{Limit: 1}); err != nil {
		t.Fatalf("reconfigure with zero window/penalty = %v", err)
	}
	if config := throttle.Config(); config.Window != DefaultIPWindow || config.Penalty != 0 {
		t.Fatalf("default fallback = %#v", config)
	}
}

func TestIPThrottleReconfigureZeroPenaltyDeniesOnlyTriggeringAttempt(t *testing.T) {
	clock := newFakeClock()
	throttle, err := NewIPThrottle(IPThrottleConfig{
		Limit: 1, Window: 10 * time.Second, Penalty: 5 * time.Second,
		MaxKeys: 4, MaxHitsPerKey: 4, MaxKeyBytes: 64,
	}, WithClock(clock))
	if err != nil {
		t.Fatal(err)
	}
	defer throttle.Close()
	if err := throttle.Reconfigure(IPThrottleConfig{Limit: 1, Window: 10 * time.Second, Penalty: 0}); err != nil {
		t.Fatalf("reconfigure zero penalty: %v", err)
	}
	if decision, err := throttle.Allow("client"); err != nil || !decision.Allowed {
		t.Fatalf("first attempt = %#v, %v", decision, err)
	}
	if decision, err := throttle.Allow("client"); err != nil || decision.Allowed ||
		decision.Reason != IPPenalty || decision.RetryAfterSeconds != 0 {
		t.Fatalf("triggering attempt = %#v, %v", decision, err)
	}
	if decision, err := throttle.Allow("client"); err != nil || !decision.Allowed {
		t.Fatalf("attempt after zero penalty = %#v, %v", decision, err)
	}
}

func TestIPThrottleReconfigureAfterCloseFailsClosed(t *testing.T) {
	throttle, err := NewIPThrottle(ipTestConfig())
	if err != nil {
		t.Fatal(err)
	}
	if err := throttle.Close(); err != nil {
		t.Fatal(err)
	}
	if err := throttle.Reconfigure(IPThrottleConfig{Limit: 1}); !errors.Is(err, ErrClosed) {
		t.Fatalf("reconfigure after close = %v, want ErrClosed", err)
	}
}

func TestIPThrottleReconfigureConcurrentWithAdmission(t *testing.T) {
	clock := newFakeClock()
	throttle, err := NewIPThrottle(IPThrottleConfig{
		Limit: 64, Window: 10 * time.Second, Penalty: 5 * time.Second,
		MaxKeys: 8, MaxHitsPerKey: 64, MaxKeyBytes: 16,
	}, WithClock(clock))
	if err != nil {
		t.Fatal(err)
	}
	defer throttle.Close()

	const callers = 64
	var wg sync.WaitGroup
	wg.Add(callers)
	for i := 0; i < callers; i++ {
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_, _ = throttle.Allow("ip-" + strconv.Itoa(i%4))
			}
		}(i)
	}
	for i := 0; i < 100; i++ {
		limit := (i % 32) + 1
		_ = throttle.Reconfigure(IPThrottleConfig{
			Limit: limit, Window: 10 * time.Second, Penalty: 5 * time.Second,
		})
	}
	wg.Wait()
	if config := throttle.Config(); config.Limit < 1 || config.Limit > 32 {
		t.Fatalf("final config = %#v", config)
	}
}
