package auth

import (
	"bytes"
	"errors"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestStateManagerRouteBindingReplayExpiryAndCapacity(t *testing.T) {
	now := time.Unix(authTestNow, 0)
	manager, err := newStateManager(bytes.Repeat([]byte{7}, 32), time.Minute, 1, 1, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	state, err := manager.IssueBoundForRoute(StationUser, OAuthIntentElevate, "session-hash", "endpoint-detail", "ep_A")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Issue(StationUser, OAuthIntentLogin); !errors.Is(err, ErrStateCapacity) {
		t.Fatalf("capacity error=%v", err)
	}
	claims, err := manager.ConsumeClaims(state, state, StationUser)
	if err != nil {
		t.Fatal(err)
	}
	if claims.Intent != OAuthIntentElevate || claims.Binding != "session-hash" || claims.RouteID != "endpoint-detail" || claims.ResourceID != "ep_A" {
		t.Fatalf("claims=%+v", claims)
	}
	if _, err := manager.ConsumeClaims(state, state, StationUser); !errors.Is(err, ErrStateReplay) {
		t.Fatalf("replay error=%v", err)
	}
	expiring, err := manager.IssueForRoute(StationUser, OAuthIntentLogin, "account", "")
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	if _, err := manager.ConsumeClaims(expiring, expiring, StationUser); !errors.Is(err, ErrStateExpired) {
		t.Fatalf("expiry error=%v", err)
	}
}

func TestStateManagerRejectsRouteAndBindingConfusion(t *testing.T) {
	manager, err := NewStateManagerWithKey(bytes.Repeat([]byte{8}, 32), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	if _, err := manager.IssueForRoute(StationUser, OAuthIntentLogin, "unknown", ""); err != nil {
		t.Fatalf("state manager should carry opaque validated route identity: %v", err)
	}
	if _, err := manager.IssueForRoute(StationUser, OAuthIntentLogin, "home", "bad/segment"); !errors.Is(err, ErrStateInvalid) {
		t.Fatalf("resource error=%v", err)
	}
	state, err := manager.IssueBound(StationUser, OAuthIntentElevate, "binding-a")
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.ConsumeBound(state, state, StationUser, OAuthIntentElevate, "binding-b"); !errors.Is(err, ErrStateInvalid) {
		t.Fatalf("binding mismatch=%v", err)
	}
}

func TestStateManagerIntentCapacityAndRelease(t *testing.T) {
	for _, floodIntent := range []string{OAuthIntentLogin, OAuthIntentElevate} {
		t.Run(floodIntent, func(t *testing.T) {
			now := time.Unix(authTestNow, 0)
			manager, err := newStateManager(bytes.Repeat([]byte{9}, 32), time.Minute, 8, 6, func() time.Time { return now })
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = manager.Close() })
			otherIntent := OAuthIntentElevate
			if floodIntent == otherIntent {
				otherIntent = OAuthIntentLogin
			}
			issue := func(intent string) string {
				t.Helper()
				state, err := manager.Issue(StationUser, intent)
				if err != nil {
					t.Fatalf("issue %s: %v", intent, err)
				}
				return state
			}
			var flooded []string
			for range 6 {
				flooded = append(flooded, issue(floodIntent))
			}
			if _, err := manager.Issue(StationUser, floodIntent); !errors.Is(err, ErrStateCapacity) {
				t.Fatalf("intent cap: %v", err)
			}
			now = now.Add(30 * time.Second)
			firstOther := issue(otherIntent)
			issue(otherIntent)
			if _, err := manager.Issue(StationUser, otherIntent); !errors.Is(err, ErrStateCapacity) {
				t.Fatalf("total cap: %v", err)
			}
			if err := manager.Consume(firstOther, firstOther, StationUser, otherIntent); err != nil {
				t.Fatal(err)
			}
			if _, err := manager.Issue(StationUser, floodIntent); !errors.Is(err, ErrStateCapacity) {
				t.Fatalf("other intent release changed full intent: %v", err)
			}
			issue(otherIntent)
			if err := manager.Consume(flooded[0], firstOther, StationUser, floodIntent); !errors.Is(err, ErrStateInvalid) {
				t.Fatalf("wrong cookie: %v", err)
			}
			if err := manager.Consume(flooded[0], flooded[0], StationUser, floodIntent); err != nil {
				t.Fatal(err)
			}
			if err := manager.Consume(flooded[0], flooded[0], StationUser, floodIntent); !errors.Is(err, ErrStateReplay) {
				t.Fatalf("replayed state: %v", err)
			}
			issue(floodIntent)
			now = now.Add(30 * time.Second)
			if err := manager.Consume(flooded[1], flooded[1], StationUser, floodIntent); !errors.Is(err, ErrStateExpired) {
				t.Fatalf("expired state: %v", err)
			}
			for range 5 {
				issue(floodIntent)
			}
			if _, err := manager.Issue(StationUser, floodIntent); !errors.Is(err, ErrStateCapacity) {
				t.Fatalf("expiry did not restore the exact budget: %v", err)
			}
		})
	}
}

func TestStateManagerConcurrentCapacityAndConsumption(t *testing.T) {
	clock := &testClock{value: time.Unix(authTestNow, 0)}
	manager, err := newStateManager(bytes.Repeat([]byte{10}, 32), time.Minute, 16, 12, clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	states := make(chan string, 64)
	var group sync.WaitGroup
	for i := range 64 {
		group.Go(func() {
			intent := OAuthIntentLogin
			if i%2 == 0 {
				intent = OAuthIntentElevate
			}
			state, err := manager.Issue(StationUser, intent)
			if err == nil {
				states <- state
			} else if !errors.Is(err, ErrStateCapacity) {
				t.Errorf("concurrent issue: %v", err)
			}
		})
	}
	group.Wait()
	close(states)
	if len(states) != 16 {
		t.Fatalf("accepted %d states, want 16", len(states))
	}
	var consumed atomic.Int64
	for state := range states {
		for range 2 {
			group.Go(func() {
				if _, err := manager.ConsumeClaims(state, state, StationUser); err == nil {
					consumed.Add(1)
				} else if !errors.Is(err, ErrStateReplay) {
					t.Errorf("concurrent consume: %v", err)
				}
			})
		}
	}
	group.Wait()
	if consumed.Load() != 16 {
		t.Fatalf("consumed %d states, want 16", consumed.Load())
	}
	for range 12 {
		if _, err := manager.Issue(StationUser, OAuthIntentLogin); err != nil {
			t.Fatalf("released capacity: %v", err)
		}
	}
	for range 8 {
		group.Go(func() {
			clock.Add(time.Minute)
			state, err := manager.Issue(StationUser, OAuthIntentElevate)
			if err != nil && !errors.Is(err, ErrStateInvalid) {
				t.Errorf("issue during expiry/close: %v", err)
			}
			if err == nil {
				_, err = manager.ConsumeClaims(state, state, StationUser)
				if err != nil && !errors.Is(err, ErrStateExpired) && !errors.Is(err, ErrStateReplay) && !errors.Is(err, ErrStateInvalid) {
					t.Errorf("consume during expiry/close: %v", err)
				}
			}
		})
	}
	group.Go(func() { _ = manager.Close() })
	group.Wait()
	if _, err := manager.Issue(StationUser, OAuthIntentLogin); !errors.Is(err, ErrStateInvalid) {
		t.Fatalf("issue after close: %v", err)
	}
	if len(manager.pending) != 0 || len(manager.pendingByIntent) != 0 {
		t.Fatal("close retained pending states")
	}
}

func TestStateManagerReclaimsInactiveIntentCounters(t *testing.T) {
	for _, release := range []string{"consume", "expire"} {
		t.Run(release, func(t *testing.T) {
			now := time.Unix(authTestNow, 0)
			manager, err := newStateManager(bytes.Repeat([]byte{11}, 32), time.Minute, 2, 1, func() time.Time { return now })
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = manager.Close() })
			for i := range 4 {
				state, err := manager.Issue(StationUser, "custom_"+strconv.Itoa(i))
				if err != nil {
					t.Fatal(err)
				}
				if release == "consume" {
					if _, err := manager.ConsumeClaims(state, state, StationUser); err != nil {
						t.Fatal(err)
					}
				} else {
					now = now.Add(time.Minute)
				}
			}
			if _, err := manager.Issue(StationUser, OAuthIntentLogin); err != nil {
				t.Fatal(err)
			}
			if len(manager.pendingByIntent) != 1 || manager.pendingByIntent[OAuthIntentLogin] != 1 {
				t.Fatal("inactive intents retained counter entries")
			}
		})
	}
}
