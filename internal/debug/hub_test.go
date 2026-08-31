package debug

import (
	"context"
	"encoding/base64"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestStartAttachModeStopAndReplaceLifecycle(t *testing.T) {
	clock := newDebugTestClock(10_000)
	verifier := &debugTestVerifier{state: IdentityActive}
	hub, _ := newDebugTestHub(t, clock, verifier)
	first := mustStartDebug(t, hub, 10, "binding-10")
	attached, created, err := hub.Start(10, "binding-10")
	if err != nil || created || attached.ID != first.ID || attached.Revision != first.Revision {
		t.Fatalf("attach = (%+v,%v,%v), first=%+v", attached, created, err, first)
	}
	if _, _, err := hub.Start(10, "different-binding"); !errors.Is(err, ErrConflict) {
		t.Fatalf("cross-binding attach = %v", err)
	}
	if _, err := hub.ChangeMode(10, "1", ModeLive, false); !errors.Is(err, ErrInvalid) {
		t.Fatalf("live without confirmation = %v", err)
	}
	live, err := hub.ChangeMode(10, "1", ModeLive, true)
	if err != nil || live.Mode != ModeLive || live.Revision != "2" {
		t.Fatalf("live mode = (%+v,%v)", live, err)
	}
	same, err := hub.ChangeMode(10, "2", ModeLive, true)
	if err != nil || same.Revision != "2" {
		t.Fatalf("same mode = (%+v,%v)", same, err)
	}

	decision, err := hub.DecideAfterAdmission(context.Background(), CaptureInput{
		UserID: 10, RouteKind: RouteOpenAIChat, Model: "m", MediaType: "application/json",
		Body: []byte(`{}`), IdentityCertain: true,
	})
	if err != nil || decision.Mode != ModeLive || decision.Trace == nil {
		t.Fatalf("live trace = (%+v,%v)", decision, err)
	}
	if err := decision.Trace.MarkDispatched(); err != nil {
		t.Fatalf("MarkDispatched: %v", err)
	}
	if err := hub.Stop(10, "2", false); !errors.Is(err, ErrConflict) {
		t.Fatalf("unconfirmed stop = %v", err)
	}
	if _, err := hub.Replace(10, "binding-10", "2", false); !errors.Is(err, ErrConflict) {
		t.Fatalf("unconfirmed replace = %v", err)
	}

	subscription, err := hub.Subscribe(context.Background(), 10, "binding-10", "")
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	_ = mustNextDebug(t, subscription)
	replacement, err := hub.Replace(10, "binding-10", "2", true)
	if err != nil || replacement.ID == first.ID || replacement.Mode != ModeDry || replacement.Revision != "1" {
		t.Fatalf("replacement = (%+v,%v)", replacement, err)
	}
	select {
	case <-decision.Trace.Context().Done():
	case <-time.After(time.Second):
		t.Fatal("replace did not cancel old live trace")
	}
	end := mustNextDebug(t, subscription)
	if end.Kind != EventSessionEnd {
		t.Fatalf("old subscription event = %s", end.Kind)
	}
	endData := decodeDebugData[SessionEndData](t, end)
	if endData.Reason != EndReplaced || endData.CancelledInflightCount != 1 {
		t.Fatalf("replacement end = %+v", endData)
	}
	if _, err := subscription.Next(context.Background()); !errors.Is(err, ErrClosed) {
		t.Fatalf("Next after session_end = %v", err)
	}
	if err := hub.Stop(10, "1", false); err != nil {
		t.Fatalf("Stop replacement: %v", err)
	}
	metadata, err := hub.Metadata(10)
	if err != nil || metadata.Active {
		t.Fatalf("metadata after stop = (%+v,%v)", metadata, err)
	}
}

func TestReplaceRaceNeverCreatesOrdinaryPassWindow(t *testing.T) {
	clock := newDebugTestClock(11_000)
	verifier := &debugTestVerifier{
		state: IdentityActive, blockOnce: true,
		entered: make(chan struct{}), release: make(chan struct{}),
	}
	hub, _ := newDebugTestHub(t, clock, verifier)
	mustStartDebug(t, hub, 11, "binding-11")
	if _, err := hub.ChangeMode(11, "1", ModeLive, true); err != nil {
		t.Fatalf("ChangeMode: %v", err)
	}
	type result struct {
		decision CaptureDecision
		err      error
	}
	resultChannel := make(chan result, 1)
	go func() {
		decision, err := hub.DecideAfterAdmission(context.Background(), CaptureInput{
			UserID: 11, RouteKind: RouteOpenAIChat, Model: "m", MediaType: "application/json",
			Body: []byte(`{}`), IdentityCertain: true,
		})
		resultChannel <- result{decision: decision, err: err}
	}()
	waitDebug(t, verifier.entered)
	replacement, err := hub.Replace(11, "binding-11", "2", false)
	if err != nil {
		t.Fatalf("Replace: %v", err)
	}
	close(verifier.release)
	resultValue := <-resultChannel
	if resultValue.err != nil || !resultValue.decision.Active || resultValue.decision.Mode != ModeDry ||
		resultValue.decision.Trace == nil || !resultValue.decision.DryIntercepted() {
		t.Fatalf("racing decision = (%+v,%v), replacement=%+v", resultValue.decision, resultValue.err, replacement)
	}
	if resultValue.decision.Trace.sessionID != replacement.ID {
		t.Fatalf("racing trace session = %s, want replacement %s", resultValue.decision.Trace.sessionID, replacement.ID)
	}
}

func TestIdleAbsoluteAndIdentityLifecycle(t *testing.T) {
	t.Run("idle exact boundary", func(t *testing.T) {
		clock := newDebugTestClock(20_000)
		hub, _ := newDebugTestHub(t, clock, &debugTestVerifier{state: IdentityActive})
		mustStartDebug(t, hub, 20, "binding-20")
		subscription, err := hub.Subscribe(context.Background(), 20, "binding-20", "")
		if err != nil {
			t.Fatal(err)
		}
		_ = mustNextDebug(t, subscription)
		clock.Advance(idleTTL)
		hub.Sweep()
		end := decodeDebugData[SessionEndData](t, mustNextDebug(t, subscription))
		if end.Reason != EndIdleExpired || end.CancelledInflightCount != 0 {
			t.Fatalf("idle end = %+v", end)
		}
	})

	t.Run("inflight crosses idle but not absolute", func(t *testing.T) {
		clock := newDebugTestClock(30_000)
		verifier := &debugTestVerifier{state: IdentityActive}
		hub, _ := newDebugTestHub(t, clock, verifier)
		mustStartDebug(t, hub, 30, "binding-30")
		if _, err := hub.ChangeMode(30, "1", ModeLive, true); err != nil {
			t.Fatal(err)
		}
		decision, err := hub.DecideAfterAdmission(context.Background(), CaptureInput{
			UserID: 30, RouteKind: RouteOpenAIChat, Model: "m", MediaType: "application/json",
			Body: []byte(`{}`), IdentityCertain: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		clock.Advance(idleTTL)
		hub.Sweep()
		if metadata, _ := hub.Metadata(30); !metadata.Active {
			t.Fatal("idle terminated an inflight trace")
		}
		clock.Advance(absoluteTTL - idleTTL)
		hub.Sweep()
		if metadata, _ := hub.Metadata(30); metadata.Active {
			t.Fatal("absolute boundary did not terminate inflight trace")
		}
		select {
		case <-decision.Trace.Context().Done():
		case <-time.After(time.Second):
			t.Fatal("absolute expiry did not cancel trace")
		}
	})

	for _, test := range []struct {
		name   string
		state  IdentityState
		reason EndReason
	}{{"revoked", IdentityRevoked, EndAuthRevoked}, {"banned", IdentityBanned, EndAccountBanned}, {"deleted", IdentityDeleted, EndAccountDeleted}} {
		t.Run(test.name, func(t *testing.T) {
			clock := newDebugTestClock(40_000)
			verifier := &debugTestVerifier{state: IdentityActive}
			hub, _ := newDebugTestHub(t, clock, verifier)
			mustStartDebug(t, hub, 40, "binding-40")
			verifier.set(test.state, nil)
			_, err := hub.DecideAfterAdmission(context.Background(), CaptureInput{
				UserID: 40, RouteKind: RouteOpenAIChat, Model: "m", MediaType: "application/json",
				Body: []byte(`{}`), IdentityCertain: true,
			})
			if err != nil {
				t.Fatal(err)
			}
			hub.mu.Lock()
			_, exists := hub.activeByUser[40]
			hub.mu.Unlock()
			if exists {
				t.Fatalf("%s identity remained active", test.reason)
			}
		})
	}
}

func TestHubHardBudgetsAndDeterministicEviction(t *testing.T) {
	clock := newDebugTestClock(50_000)
	verifier := &debugTestVerifier{state: IdentityActive}
	hub, _ := newDebugTestHub(t, clock, verifier)
	for userID := int64(1); userID <= MaxGlobalSessions; userID++ {
		mustStartDebug(t, hub, userID, "binding")
	}
	if _, _, err := hub.Start(MaxGlobalSessions+1, "binding"); !errors.Is(err, ErrCapacity) {
		t.Fatalf("65th session = %v", err)
	}

	// Reuse one fresh Hub for trace/event boundaries so the global-session test
	// does not couple to trace setup.
	hub2, _ := newDebugTestHub(t, clock, verifier)
	mustStartDebug(t, hub2, 100, "binding-100")
	if _, err := hub2.ChangeMode(100, "1", ModeLive, true); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < MaxSessionTraces; index++ {
		decision, err := hub2.DecideAfterAdmission(context.Background(), CaptureInput{
			UserID: 100, RouteKind: RouteOpenAIChat, Model: "m", MediaType: "application/json",
			Body: []byte(`{}`), IdentityCertain: true,
		})
		if err != nil || decision.Trace == nil || decision.Mode != ModeLive {
			t.Fatalf("trace %d = (%+v,%v)", index, decision, err)
		}
	}
	overflow, err := hub2.DecideAfterAdmission(context.Background(), CaptureInput{
		UserID: 100, RouteKind: RouteOpenAIChat, Model: "m", MediaType: "application/json",
		Body: []byte(`{}`), IdentityCertain: true,
	})
	if err != nil || overflow.Mode != ModeDry || overflow.Trace != nil || !overflow.Active {
		t.Fatalf("trace overflow = (%+v,%v)", overflow, err)
	}
	hub2.mu.Lock()
	current := hub2.activeByUser[100]
	if len(current.traces) != MaxSessionTraces || current.inflight != MaxSessionTraces ||
		len(current.events) > MaxSessionEvents || current.bytes() > MaxSessionBytes {
		t.Fatalf("bounded session = traces:%d inflight:%d events:%d bytes:%d",
			len(current.traces), current.inflight, len(current.events), current.bytes())
	}
	hub2.mu.Unlock()

	// Exercise the 128 MiB process-wide boundary without allocating 128 MiB of
	// fixture bodies: accounted session bytes are the authority used by the Hub.
	hub3, _ := newDebugTestHub(t, clock, verifier)
	for userID := int64(1); userID <= 33; userID++ {
		mustStartDebug(t, hub3, userID, "global-binding")
	}
	hub3.mu.Lock()
	for userID := int64(1); userID <= 32; userID++ {
		hub3.activeByUser[userID].traceBytes = MaxSessionBytes
	}
	if got := hub3.totalBytesLocked(); got != MaxGlobalBytes {
		hub3.mu.Unlock()
		t.Fatalf("fixture global bytes = %d", got)
	}
	hub3.mu.Unlock()
	globalOverflow, err := hub3.DecideAfterAdmission(context.Background(), CaptureInput{
		UserID: 33, RouteKind: RouteOpenAIChat, Model: "m", MediaType: "application/json",
		Body: []byte(`{}`), IdentityCertain: true,
	})
	if err != nil || !globalOverflow.Active || globalOverflow.Mode != ModeDry || globalOverflow.Trace != nil {
		t.Fatalf("global trace overflow = (%+v,%v)", globalOverflow, err)
	}
	hub3.mu.Lock()
	globalBytes := hub3.totalBytesLocked()
	hub3.mu.Unlock()
	if globalBytes > MaxGlobalBytes {
		t.Fatalf("global bytes exceeded cap: %d", globalBytes)
	}
}

func TestRetainedEventBytesEnforceSessionAndGlobalCaps(t *testing.T) {
	t.Run("session", func(t *testing.T) {
		clock := newDebugTestClock(55_000)
		hub, _ := newDebugTestHub(t, clock, &debugTestVerifier{state: IdentityActive})
		mustStartDebug(t, hub, 550, "binding-550")
		subscription, err := hub.Subscribe(context.Background(), 550, "binding-550", "")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = subscription.Close() })
		_ = mustNextDebug(t, subscription)
		record := publishDebugEventTrace(t, hub, 550, largeDebugEventTrace(400*1024, 550, clock.Now().Unix()))
		queued := popQueuedDebugEvent(t, subscription)
		pin, ok := subscription.pinQueued(queued)
		if !ok {
			t.Fatal("event was not pinned")
		}

		hub.mu.Lock()
		current := hub.activeByUser[550]
		for len(current.events) > 0 && current.events[0] != record {
			hub.discardOldestEventLocked(current, GapRingEvicted, clock.Now())
		}
		hub.discardOldestEventLocked(current, GapRingEvicted, clock.Now())
		current.traceBytes = MaxSessionBytes - current.retainedBytes
		candidate, buildErr := hub.newEventLocked(current, EventGap, GapData{Reason: GapSlowConsumer})
		if buildErr != nil {
			hub.mu.Unlock()
			t.Fatal(buildErr)
		}
		appendErr := hub.appendEventLocked(current, candidate)
		if !errors.Is(appendErr, ErrCapacity) || current.bytes() != MaxSessionBytes ||
			current.eventCount() != 1 {
			hub.mu.Unlock()
			t.Fatalf("session cap = err:%v bytes:%d events:%d", appendErr, current.bytes(), current.eventCount())
		}
		hub.mu.Unlock()

		subscription.releasePin(pin)
		hub.mu.Lock()
		if err := hub.appendEventLocked(current, candidate); err != nil || current.bytes() > MaxSessionBytes ||
			current.eventCount() > MaxSessionEvents {
			hub.mu.Unlock()
			t.Fatalf("append after release = err:%v bytes:%d events:%d", err, current.bytes(), current.eventCount())
		}
		hub.mu.Unlock()
	})

	t.Run("global", func(t *testing.T) {
		clock := newDebugTestClock(56_000)
		hub, _ := newDebugTestHub(t, clock, &debugTestVerifier{state: IdentityActive})
		for userID := int64(1); userID <= 33; userID++ {
			mustStartDebug(t, hub, userID, "global-retained")
		}
		subscription, err := hub.Subscribe(context.Background(), 1, "global-retained", "")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = subscription.Close() })
		_ = mustNextDebug(t, subscription)
		record := publishDebugEventTrace(t, hub, 1, largeDebugEventTrace(400*1024, 560, clock.Now().Unix()))
		queued := popQueuedDebugEvent(t, subscription)
		pin, ok := subscription.pinQueued(queued)
		if !ok {
			t.Fatal("global event was not pinned")
		}

		hub.mu.Lock()
		first := hub.activeByUser[1]
		for len(first.events) > 0 && first.events[0] != record {
			hub.discardOldestEventLocked(first, GapRingEvicted, clock.Now())
		}
		hub.discardOldestEventLocked(first, GapRingEvicted, clock.Now())
		first.traceBytes = MaxSessionBytes - first.retainedBytes
		for userID := int64(2); userID <= 32; userID++ {
			hub.activeByUser[userID].traceBytes = MaxSessionBytes
		}
		if total := hub.totalBytesLocked(); total != MaxGlobalBytes {
			hub.mu.Unlock()
			t.Fatalf("global fixture bytes = %d, want %d", total, MaxGlobalBytes)
		}
		candidate, buildErr := hub.newEventLocked(hub.activeByUser[33], EventGap, GapData{Reason: GapSlowConsumer})
		if buildErr != nil {
			hub.mu.Unlock()
			t.Fatal(buildErr)
		}
		appendErr := hub.appendEventLocked(hub.activeByUser[33], candidate)
		if !errors.Is(appendErr, ErrCapacity) || hub.totalBytesLocked() != MaxGlobalBytes {
			hub.mu.Unlock()
			t.Fatalf("global cap = err:%v bytes:%d", appendErr, hub.totalBytesLocked())
		}
		hub.mu.Unlock()

		subscription.releasePin(pin)
		hub.mu.Lock()
		if err := hub.appendEventLocked(hub.activeByUser[33], candidate); err != nil ||
			hub.totalBytesLocked() > MaxGlobalBytes {
			hub.mu.Unlock()
			t.Fatalf("global append after release = err:%v bytes:%d", err, hub.totalBytesLocked())
		}
		hub.mu.Unlock()
	})
}

func TestSlowRecoveryClosesWhenPinnedBudgetCannotFitGapSnapshot(t *testing.T) {
	clock := newDebugTestClock(57_000)
	hub, _ := newDebugTestHub(t, clock, &debugTestVerifier{state: IdentityActive})
	mustStartDebug(t, hub, 570, "binding-570")
	subscription, err := hub.Subscribe(context.Background(), 570, "binding-570", "")
	if err != nil {
		t.Fatal(err)
	}
	_ = mustNextDebug(t, subscription)

	hub.mu.Lock()
	current := hub.activeByUser[570]
	current.traceBytes = MaxSessionBytes
	if hub.recoverSlowLocked(current, subscription) || !subscription.closed ||
		len(current.subscribers) != 0 || current.bytes() > MaxSessionBytes {
		hub.mu.Unlock()
		t.Fatalf("unfit recovery = recovered:%v closed:%v subscribers:%d bytes:%d",
			!subscription.closed, subscription.closed, len(current.subscribers), current.bytes())
	}
	hub.mu.Unlock()
}

func TestConcurrentCaptureReplaceAndStopRemainLinearizable(t *testing.T) {
	clock := newDebugTestClock(60_000)
	verifier := &debugTestVerifier{state: IdentityActive}
	hub, _ := newDebugTestHub(t, clock, verifier)
	mustStartDebug(t, hub, 60, "binding-60")
	if _, err := hub.ChangeMode(60, "1", ModeLive, true); err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	results := make(chan CaptureDecision, 32)
	for index := 0; index < 32; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			decision, _ := hub.DecideAfterAdmission(context.Background(), CaptureInput{
				UserID: 60, RouteKind: RouteOpenAIChat, Model: "m", MediaType: "application/json",
				Body: []byte(`{}`), IdentityCertain: true,
			})
			results <- decision
		}()
	}
	wait.Wait()
	close(results)
	for decision := range results {
		if !decision.Active || decision.Trace == nil {
			t.Fatalf("capture escaped active session: %+v", decision)
		}
	}
	metadata, err := hub.Metadata(60)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := hub.Replace(60, "binding-60", metadata.Revision, true); err != nil {
		t.Fatalf("confirmed replace: %v", err)
	}
	if err := hub.Stop(60, "1", false); err != nil {
		t.Fatalf("stop replacement: %v", err)
	}
}

func TestShutdownPublishesFinalSessionEndAndDropsSensitiveState(t *testing.T) {
	clock := newDebugTestClock(65_000)
	hub, _ := newDebugTestHub(t, clock, &debugTestVerifier{state: IdentityActive})
	metadata := mustStartDebug(t, hub, 65, "binding-65")
	if _, err := hub.ChangeMode(65, metadata.Revision, ModeLive, true); err != nil {
		t.Fatal(err)
	}
	decision, err := hub.DecideAfterAdmission(context.Background(), CaptureInput{
		UserID: 65, RouteKind: RouteOpenAIChat, Model: "m", MediaType: "application/json",
		Body: []byte(`{"owner":"temporary"}`), IdentityCertain: true,
	})
	if err != nil || decision.Trace == nil {
		t.Fatalf("live capture = (%+v,%v)", decision, err)
	}
	traceContext := decision.Trace.Context()
	subscription, err := hub.Subscribe(context.Background(), 65, "binding-65", "")
	if err != nil {
		t.Fatal(err)
	}
	_ = mustNextDebug(t, subscription)
	old := subscription.session
	if err := hub.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-traceContext.Done():
	case <-time.After(time.Second):
		t.Fatal("shutdown did not cancel inflight trace")
	}
	end := mustNextDebug(t, subscription)
	endData := decodeDebugData[SessionEndData](t, end)
	if end.Kind != EventSessionEnd || endData.Reason != EndShutdown || endData.CancelledInflightCount != 1 {
		t.Fatalf("shutdown event = %+v data=%+v", end, endData)
	}
	if len(old.traces) != 0 || old.traceBytes != 0 || old.inflight != 0 || len(old.events) != 0 ||
		old.eventBytes != 0 || old.retainedBytes != 0 {
		t.Fatalf("shutdown retained sensitive state: traces=%d bytes=%d inflight=%d events=%d",
			len(old.traces), old.traceBytes, old.inflight, len(old.events))
	}
	if _, err := subscription.Next(context.Background()); !errors.Is(err, ErrClosed) {
		t.Fatalf("subscription after shutdown = %v", err)
	}
}

func TestSealedEventIDSourceSeparatesProcessEpochs(t *testing.T) {
	current, err := newSealedEventIDSource()
	if err != nil {
		t.Fatalf("new current event source: %v", err)
	}
	other, err := newSealedEventIDSource()
	if err != nil {
		t.Fatalf("new other event source: %v", err)
	}
	eventID, err := current.New()
	if err != nil {
		t.Fatalf("New event ID: %v", err)
	}
	if !current.IsCurrent(eventID) {
		t.Fatalf("current source rejected its event ID %q", eventID)
	}
	if other.IsCurrent(eventID) {
		t.Fatalf("other process source accepted event ID %q", eventID)
	}

	raw, err := base64.RawURLEncoding.DecodeString(eventID[4:])
	if err != nil {
		t.Fatalf("decode event ID: %v", err)
	}
	raw[len(raw)-1] ^= 0xff
	tampered := "dbe_" + base64.RawURLEncoding.EncodeToString(raw)
	if current.IsCurrent(tampered) {
		t.Fatalf("current source accepted modified event ID %q", tampered)
	}
	if current.IsCurrent("dbe_not-an-oid") {
		t.Fatal("current source accepted malformed event ID")
	}
}
