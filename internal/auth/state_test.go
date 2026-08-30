package auth

import (
	"bytes"
	"errors"
	"testing"
	"time"
)

func TestStateManagerRouteBindingReplayExpiryAndCapacity(t *testing.T) {
	now := time.Unix(authTestNow, 0)
	manager, err := newStateManager(bytes.Repeat([]byte{7}, 32), time.Minute, 1, func() time.Time { return now })
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
