package debug

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestForgetAccountPermanentlyDropsAllDebugMemory(t *testing.T) {
	clock := newDebugTestClock(180_000)
	hub, _ := newDebugTestHub(t, clock, &debugTestVerifier{state: IdentityActive})

	first := mustStartDebug(t, hub, 1, "binding-1")
	if _, err := hub.ChangeMode(1, first.Revision, ModeLive, true); err != nil {
		t.Fatal(err)
	}
	oldTrace, err := hub.DecideAfterAdmission(context.Background(), CaptureInput{
		UserID: 1, RouteKind: RouteOpenAIChat, Model: "old", MediaType: "application/json",
		Body: []byte(`{"secret":"old"}`), IdentityCertain: true,
	})
	if err != nil || oldTrace.Trace == nil {
		t.Fatalf("old trace = (%+v,%v)", oldTrace, err)
	}
	oldSubscription, err := hub.Subscribe(context.Background(), 1, "binding-1", "")
	if err != nil {
		t.Fatal(err)
	}
	_ = mustNextDebug(t, oldSubscription)
	oldSession := oldSubscription.session

	replacement, err := hub.Replace(1, "binding-1", "2", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := hub.ChangeMode(1, replacement.Revision, ModeLive, true); err != nil {
		t.Fatal(err)
	}
	currentTrace, err := hub.DecideAfterAdmission(context.Background(), CaptureInput{
		UserID: 1, RouteKind: RouteOpenAIChat, Model: "current", MediaType: "application/json",
		Body: []byte(`{"secret":"current"}`), IdentityCertain: true,
	})
	if err != nil || currentTrace.Trace == nil {
		t.Fatalf("current trace = (%+v,%v)", currentTrace, err)
	}
	traceContext := currentTrace.Trace.Context()
	currentSubscription, err := hub.Subscribe(context.Background(), 1, "binding-1", "")
	if err != nil {
		t.Fatal(err)
	}
	_ = mustNextDebug(t, currentSubscription)
	currentSession := currentSubscription.session

	mustStartDebug(t, hub, 2, "binding-2")
	addDryTrace(t, hub, 2)

	if err := hub.ForgetAccount(1); err != nil {
		t.Fatalf("ForgetAccount: %v", err)
	}
	select {
	case <-traceContext.Done():
	case <-time.After(time.Second):
		t.Fatal("forget did not cancel the live trace")
	}
	if !errors.Is(context.Cause(traceContext), ErrSessionEnded) {
		t.Fatalf("trace cancellation cause = %v", context.Cause(traceContext))
	}
	for name, subscription := range map[string]*Subscription{
		"old": oldSubscription, "current": currentSubscription,
	} {
		if _, err := subscription.Next(context.Background()); !errors.Is(err, ErrClosed) {
			t.Fatalf("%s subscription remained readable: %v", name, err)
		}
	}
	if metadata, err := hub.Metadata(1); err != nil || metadata.Active {
		t.Fatalf("forgotten metadata = (%+v,%v)", metadata, err)
	}
	if _, _, err := hub.Start(1, "binding-1"); !errors.Is(err, ErrNoActiveSession) {
		t.Fatalf("forgotten account restarted Debug: %v", err)
	}
	if err := hub.ForgetAccount(1); err != nil {
		t.Fatalf("repeated ForgetAccount: %v", err)
	}

	hub.mu.Lock()
	defer hub.mu.Unlock()
	if _, active := hub.activeByUser[1]; active {
		t.Fatal("forgotten account remained active")
	}
	if _, forgotten := hub.forgotten[1]; !forgotten {
		t.Fatal("forgotten account has no tombstone")
	}
	for current := range hub.sessions {
		if current.userID == 1 {
			t.Fatal("forgotten account retained a session")
		}
	}
	for _, known := range hub.knownEvents {
		if known.userID == 1 {
			t.Fatal("forgotten account retained a cursor record")
		}
	}
	for _, discarded := range []*session{oldSession, currentSession} {
		if discarded.userID != 0 || discarded.id != "" || discarded.identityBinding != "" || !discarded.ended ||
			len(discarded.traces) != 0 || len(discarded.events) != 0 || len(discarded.eventIndex) != 0 ||
			len(discarded.retainedEvents) != 0 || len(discarded.discarded) != 0 || len(discarded.subscribers) != 0 ||
			discarded.traceBytes != 0 || discarded.eventBytes != 0 || discarded.retainedBytes != 0 || discarded.inflight != 0 {
			t.Fatalf("forgotten session retained state: %+v", discarded)
		}
	}
	if other := hub.activeByUser[2]; other == nil || other.ended || len(other.traces) != 1 {
		t.Fatalf("unrelated account changed: %+v", other)
	}
}

func TestForgetAccountRejectsInvalidIdentityWithoutMutation(t *testing.T) {
	clock := newDebugTestClock(181_000)
	hub, _ := newDebugTestHub(t, clock, &debugTestVerifier{state: IdentityActive})
	if err := hub.ForgetAccount(0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid identity error = %v", err)
	}
	hub.mu.Lock()
	forgotten := len(hub.forgotten)
	hub.mu.Unlock()
	if forgotten != 0 {
		t.Fatalf("invalid forget created %d tombstones", forgotten)
	}
}
