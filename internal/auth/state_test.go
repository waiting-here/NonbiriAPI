package auth

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestStateManagerRandomnessBindingAndReplay(t *testing.T) {
	manager, err := NewStateManager(time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()

	first, err := manager.Issue(StationUser, OAuthIntentLogin)
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.Issue(StationUser, OAuthIntentLogin)
	if err != nil {
		t.Fatal(err)
	}
	if first == second || len(first) < 64 || len(second) < 64 {
		t.Fatalf("states are not high-entropy and distinct: %q / %q", first, second)
	}
	if err := manager.Consume(first, first, StationAdmin, OAuthIntentLogin); !errors.Is(err, ErrStateInvalid) {
		t.Fatalf("station mismatch error=%v, want ErrStateInvalid", err)
	}
	if err := manager.Consume(first, first, StationUser, "elevate"); !errors.Is(err, ErrStateInvalid) {
		t.Fatalf("intent mismatch error=%v, want ErrStateInvalid", err)
	}
	if err := manager.Consume(first, first+"x", StationUser, OAuthIntentLogin); !errors.Is(err, ErrStateInvalid) {
		t.Fatalf("cookie mismatch error=%v, want ErrStateInvalid", err)
	}
	if err := manager.Consume(first, first, StationUser, OAuthIntentLogin); err != nil {
		t.Fatalf("valid consume: %v", err)
	}
	if err := manager.Consume(first, first, StationUser, OAuthIntentLogin); !errors.Is(err, ErrStateReplay) {
		t.Fatalf("replay error=%v, want ErrStateReplay", err)
	}
	if strings.Contains(first, " ") || strings.ContainsAny(first, "\r\n\x00") {
		t.Fatalf("state has unsafe cookie characters: %q", first)
	}
}

func TestStateManagerSupportsSubsecondTestTTLs(t *testing.T) {
	manager, err := NewStateManager(100 * time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	state, err := manager.Issue(StationUser, OAuthIntentLogin)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Consume(state, state, StationUser, OAuthIntentLogin); err != nil {
		t.Fatalf("subsecond state was not immediately usable: %v", err)
	}
}

func TestStateManagerConcurrentConsumeIsOneUse(t *testing.T) {
	manager, err := NewStateManager(time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	state, err := manager.Issue(StationUser, OAuthIntentLogin)
	if err != nil {
		t.Fatal(err)
	}
	const attempts = 32
	results := make(chan error, attempts)
	var wg sync.WaitGroup
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- manager.Consume(state, state, StationUser, OAuthIntentLogin)
		}()
	}
	wg.Wait()
	close(results)
	consumed := 0
	for err := range results {
		if err == nil {
			consumed++
		} else if !errors.Is(err, ErrStateReplay) {
			t.Fatalf("concurrent consume error=%v, want replay after one success", err)
		}
	}
	if consumed != 1 {
		t.Fatalf("concurrent consume successes=%d, want 1", consumed)
	}
}

func TestStateManagerHMACTamperAndExpiry(t *testing.T) {
	key := []byte("01234567890123456789012345678901")
	manager, err := NewStateManagerWithKey(key, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	clock := time.Unix(1000, 0).UTC()
	if err := manager.SetClock(func() time.Time { return clock }); err != nil {
		t.Fatal(err)
	}
	state, err := manager.Issue(StationUser, OAuthIntentLogin)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(state, ".")
	if len(parts) != 2 {
		t.Fatalf("state parts=%d, want 2", len(parts))
	}
	tampered := parts[0] + "." + strings.Repeat("A", len(parts[1]))
	if err := manager.Consume(tampered, tampered, StationUser, OAuthIntentLogin); !errors.Is(err, ErrStateInvalid) {
		t.Fatalf("tampered state error=%v, want ErrStateInvalid", err)
	}
	if err := manager.Consume(state, state, StationUser, OAuthIntentLogin); err != nil {
		t.Fatalf("tamper consumed valid state: %v", err)
	}

	expired, err := manager.Issue(StationUser, OAuthIntentLogin)
	if err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(11 * time.Second)
	if err := manager.Consume(expired, expired, StationUser, OAuthIntentLogin); !errors.Is(err, ErrStateExpired) {
		t.Fatalf("expired state error=%v, want ErrStateExpired", err)
	}
}

func TestStateManagerCloseWipesAvailability(t *testing.T) {
	manager, err := NewStateManager(time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	state, err := manager.Issue(StationUser, OAuthIntentLogin)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Issue(StationUser, OAuthIntentLogin); !errors.Is(err, ErrStateInvalid) {
		t.Fatalf("issue after close=%v, want ErrStateInvalid", err)
	}
	if err := manager.Consume(state, state, StationUser, OAuthIntentLogin); !errors.Is(err, ErrStateInvalid) {
		t.Fatalf("consume after close=%v, want ErrStateInvalid", err)
	}
}

func TestStateManagerRejectsInvalidInputs(t *testing.T) {
	if _, err := NewStateManager(0); err == nil {
		t.Fatal("zero TTL unexpectedly accepted")
	}
	manager, err := NewStateManager(time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	cases := [][2]string{{"", OAuthIntentLogin}, {StationUser, "bad intent"}, {"other", OAuthIntentLogin}}
	for _, tc := range cases {
		if _, err := manager.Issue(tc[0], tc[1]); !errors.Is(err, ErrStateInvalid) {
			t.Fatalf("Issue(%q,%q)=%v, want ErrStateInvalid", tc[0], tc[1], err)
		}
	}
	if err := manager.Consume(strings.Repeat("x", maxOAuthStateBytes+1), "x", StationUser, OAuthIntentLogin); !errors.Is(err, ErrStateInvalid) {
		t.Fatalf("overlong consume=%v, want ErrStateInvalid", err)
	}
}
