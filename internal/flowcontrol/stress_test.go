package flowcontrol

import (
	"context"
	"fmt"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/ratelimit"
)

// stressConfig models a small site: 64 users, per-user cap 8, global cap 64,
// short window so window expiry and prune run under the test.
func stressConfig() ratelimit.RPMConfig {
	return ratelimit.RPMConfig{
		Window:       200 * time.Millisecond,
		GlobalLimit:  64,
		PerUserLimit: 8,
		MaxUserKeys:  4096,
		MaxEvents:    4096,
		MaxKeyBytes:  64,
	}
}

// TestStressConcurrentAdmitCommitRelease hammers one shared Controller from
// many goroutines across many users. It asserts the window invariants hold at
// every sampled point, that every reservation reaches a terminal state, and
// that all state is pruned (bounded cleanup) once the window expires. Run
// under -race by scripts/race-check.sh.
func TestStressConcurrentAdmitCommitRelease(t *testing.T) {
	controller, err := New(Config{RPM: stressConfig()})
	if err != nil {
		t.Fatal(err)
	}
	defer controller.Close()

	const (
		users      = 64
		goroutines = 64
		iterations = 250
	)
	var (
		admitted atomic.Int64
		denied   atomic.Int64
		wait     sync.WaitGroup
	)
	limits := controller.Limits()

	check := func(userID int64) {
		// Sampling the limiter from the same mutex keeps every observation
		// consistent; the invariant must hold at every instant.
		decision, err := controller.limiter.Check(fmt.Sprintf("%d", userID))
		if err != nil {
			t.Errorf("check: %v", err)
			return
		}
		if decision.GlobalCount > limits.GlobalLimit || decision.UserCount > limits.PerUserLimit {
			t.Errorf("invariant broken: %#v", decision)
		}
	}

	for g := 0; g < goroutines; g++ {
		wait.Add(1)
		go func(seed int64) {
			defer wait.Done()
			local := rand.New(rand.NewSource(seed))
			for i := 0; i < iterations; i++ {
				userID := local.Int63n(users) + 1
				reservation, retryAfter, err := controller.Admit(context.Background(), userID)
				if err != nil {
					if err == ErrRateLimited {
						denied.Add(1)
						if retryAfter <= 0 || retryAfter > MaxRetryAfter {
							t.Errorf("out-of-bounds retry-after %v", retryAfter)
						}
					} else if err == ErrConcurrencyLimited {
						denied.Add(1)
						if retryAfter != 0 {
							t.Errorf("concurrency retry-after %v", retryAfter)
						}
					} else {
						t.Errorf("unexpected admit error: %v", err)
					}
					continue
				}
				if reservation == nil || !reservation.Active() {
					t.Error("admitted reservation must be active")
					continue
				}
				admitted.Add(1)
				// Commit most, release some; terminal action must win exactly
				// once and render the handle inert.
				var won bool
				if local.Intn(4) == 0 {
					won = reservation.Release()
				} else {
					won = reservation.Commit()
				}
				if !won || reservation.Active() {
					t.Error("terminal action must win exactly once")
					continue
				}
				if reservation.Commit() || reservation.Release() {
					t.Error("second terminal action must be a no-op")
				}
				if local.Intn(97) == 0 {
					check(local.Int63n(users) + 1)
				}
			}
		}(int64(g) + 1)
	}
	wait.Wait()

	if admitted.Load() == 0 {
		t.Fatal("stress run admitted nothing")
	}
	if admitted.Load()+denied.Load() != goroutines*iterations {
		t.Fatalf("accounting mismatch: admitted=%d denied=%d", admitted.Load(), denied.Load())
	}

	// Bounded cleanup: after the window expires every event must be pruned
	// and every per-user key removed, so repeated stress runs cannot grow the
	// state store.
	deadline := time.Now().Add(2 * time.Second)
	for {
		decision, err := controller.limiter.Check("1")
		if err != nil {
			t.Fatal(err)
		}
		if decision.GlobalCount == 0 && decision.UserCount == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("window state did not prune: %#v", decision)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestStressCapacityBoundedKeys verifies the per-user key store stays bounded
// under load: identities beyond MaxUserKeys are refused, never evicting a
// live identity, and cleanup reopens capacity after the window.
func TestStressCapacityBoundedKeys(t *testing.T) {
	config := stressConfig()
	config.MaxUserKeys = 32
	config.GlobalLimit = 10000
	config.PerUserLimit = 10000
	config.MaxEvents = 20000
	controller, err := New(Config{RPM: config})
	if err != nil {
		t.Fatal(err)
	}
	defer controller.Close()

	const distinctUsers = 64
	admitted := make([]*Reservation, 0, distinctUsers)
	for userID := int64(1); userID <= distinctUsers; userID++ {
		reservation, _, err := controller.Admit(context.Background(), userID)
		if userID <= 32 {
			if err != nil || reservation == nil {
				t.Fatalf("user %d should admit: %v", userID, err)
			}
			admitted = append(admitted, reservation)
			continue
		}
		if err != ErrRateLimited {
			t.Fatalf("user %d should be capacity-refused, got %v", userID, err)
		}
	}
	for _, reservation := range admitted {
		if !reservation.Active() {
			t.Fatal("capacity pressure must not evict live reservations")
		}
		reservation.Release()
	}
	// Capacity reopens as soon as the released identities leave the store.
	if reservation, _, err := controller.Admit(context.Background(), 64); err != nil || reservation == nil {
		t.Fatalf("capacity must reopen after release: %v", err)
	} else {
		reservation.Release()
	}
}

// TestStressMiddlewareLifecycle drives the full middleware path concurrently
// (tracked writer, commit/release decision, 429 fast path) under -race.
func TestStressMiddlewareLifecycle(t *testing.T) {
	controller, err := New(Config{RPM: stressConfig()})
	if err != nil {
		t.Fatal(err)
	}
	defer controller.Close()
	middleware, err := NewMiddleware(controller, func(_ *http.Request) (int64, error) {
		return 1, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := middleware.Wrap(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		if time.Now().UnixNano()%3 == 0 {
			writer.WriteHeader(http.StatusNoContent)
			return
		}
		_, _ = writer.Write([]byte("stream-frame"))
	}))

	var wait sync.WaitGroup
	for g := 0; g < 32; g++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for i := 0; i < 200; i++ {
				request := httptest.NewRequest(http.MethodPost, "https://gateway.example/v1/chat/completions", nil)
				recorder := httptest.NewRecorder()
				handler.ServeHTTP(recorder, request)
				if recorder.Code != http.StatusOK && recorder.Code != http.StatusTooManyRequests && recorder.Code != http.StatusNoContent {
					t.Errorf("unexpected status %d", recorder.Code)
					return
				}
			}
		}()
	}
	wait.Wait()
}
