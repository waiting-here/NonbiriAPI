package debug

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func largeDebugEventTrace(size int, sequence uint64, now int64) DebugTrace {
	body := strings.Repeat("x", size)
	return DebugTrace{
		TraceID: testOpaqueID("dbt_", sequence), Revision: "1", State: TraceCapturing,
		Request: DebugRequest{
			RouteKind: RouteOpenAIChat, Model: "large-event", Body: DebugBody{
				MediaType: "application/json", ByteCount: int64(len(body)), Text: &body,
			},
		},
		CreatedAt: now, UpdatedAt: now,
	}
}

func publishDebugEventTrace(t *testing.T, hub *Hub, userID int64, trace DebugTrace) *eventRecord {
	t.Helper()
	hub.mu.Lock()
	defer hub.mu.Unlock()
	current := hub.activeByUser[userID]
	if current == nil {
		t.Fatal("missing debug session")
	}
	record, err := hub.publishLocked(current, EventTraceUpsert, trace)
	if err != nil {
		t.Fatalf("publish trace event: %v", err)
	}
	return record
}

func popQueuedDebugEvent(t *testing.T, subscription *Subscription) queuedEvent {
	t.Helper()
	select {
	case queued := <-subscription.queue:
		return queued
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for queued debug event")
		return queuedEvent{}
	}
}

func addDryTrace(t *testing.T, hub *Hub, userID int64) *TraceHandle {
	t.Helper()
	decision, err := hub.DecideAfterAdmission(context.Background(), CaptureInput{
		UserID: userID, RouteKind: RouteOpenAIChat, Model: "m", MediaType: "application/json",
		Body: []byte(`{}`), IdentityCertain: true,
	})
	if err != nil || !decision.DryIntercepted() || decision.Trace == nil {
		t.Fatalf("dry trace = (%+v,%v)", decision, err)
	}
	return decision.Trace
}

func currentEventIDs(t *testing.T, hub *Hub, userID int64) []string {
	t.Helper()
	hub.mu.Lock()
	defer hub.mu.Unlock()
	current := hub.activeByUser[userID]
	if current == nil {
		t.Fatal("missing current debug session")
	}
	result := make([]string, len(current.events))
	for index, record := range current.events {
		result[index] = record.event.EventID
	}
	return result
}

func requireGapSnapshot(t *testing.T, subscription *Subscription, reason GapReason) {
	t.Helper()
	gap := mustNextDebug(t, subscription)
	if gap.Kind != EventGap {
		t.Fatalf("first recovery event = %s, want gap", gap.Kind)
	}
	gapData := decodeDebugData[GapData](t, gap)
	if gapData.Reason != reason {
		t.Fatalf("gap reason = %s, want %s", gapData.Reason, reason)
	}
	snapshot := mustNextDebug(t, subscription)
	if snapshot.Kind != EventSnapshot {
		t.Fatalf("gap successor = %s, want snapshot", snapshot.Kind)
	}
}

func TestLastEventIDReplayAndGapClassification(t *testing.T) {
	clock := newDebugTestClock(70_000)
	verifier := &debugTestVerifier{state: IdentityActive}
	hub, ids := newDebugTestHub(t, clock, verifier)
	mustStartDebug(t, hub, 70, "binding-70")
	addDryTrace(t, hub, 70)
	events := currentEventIDs(t, hub, 70)
	if len(events) != 2 {
		t.Fatalf("capture event count = %d", len(events))
	}

	replay, err := hub.Subscribe(context.Background(), 70, "binding-70", events[0])
	if err != nil {
		t.Fatalf("replay Subscribe: %v", err)
	}
	replayed := mustNextDebug(t, replay)
	if replayed.EventID != events[1] || replayed.Kind != EventTraceUpsert {
		t.Fatalf("replayed = %+v", replayed)
	}
	_ = replay.Close()

	latest, err := hub.Subscribe(context.Background(), 70, "binding-70", events[1])
	if err != nil {
		t.Fatalf("latest Subscribe: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := latest.Next(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("latest cursor Next = %v", err)
	}
	_ = latest.Close()

	malformed, err := hub.Subscribe(context.Background(), 70, "binding-70", "not-an-event")
	if err != nil {
		t.Fatal(err)
	}
	requireGapSnapshot(t, malformed, GapCursorInvalid)
	_ = malformed.Close()

	unknownCurrent, err := ids.event()
	if err != nil {
		t.Fatal(err)
	}
	unknown, err := hub.Subscribe(context.Background(), 70, "binding-70", unknownCurrent)
	if err != nil {
		t.Fatal(err)
	}
	requireGapSnapshot(t, unknown, GapCursorInvalid)
	_ = unknown.Close()

	mustStartDebug(t, hub, 71, "binding-71")
	addDryTrace(t, hub, 71)
	foreignID := currentEventIDs(t, hub, 71)[0]
	foreign, err := hub.Subscribe(context.Background(), 70, "binding-70", foreignID)
	if err != nil {
		t.Fatal(err)
	}
	requireGapSnapshot(t, foreign, GapCursorInvalid)
	_ = foreign.Close()

	oldProcessID := testOpaqueID("dbe_", 900_000)
	restart, err := hub.Subscribe(context.Background(), 70, "binding-70", oldProcessID)
	if err != nil {
		t.Fatal(err)
	}
	requireGapSnapshot(t, restart, GapProcessRestart)
	_ = restart.Close()
}

func TestRingExpiredEvictedAndSlowConsumerRecovery(t *testing.T) {
	t.Run("expired", func(t *testing.T) {
		clock := newDebugTestClock(80_000)
		hub, _ := newDebugTestHub(t, clock, &debugTestVerifier{state: IdentityActive})
		mustStartDebug(t, hub, 80, "binding-80")
		addDryTrace(t, hub, 80)
		cursor := currentEventIDs(t, hub, 80)[0]
		clock.Advance(ringAge)
		subscription, err := hub.Subscribe(context.Background(), 80, "binding-80", cursor)
		if err != nil {
			t.Fatal(err)
		}
		requireGapSnapshot(t, subscription, GapRingExpired)
	})

	t.Run("evicted", func(t *testing.T) {
		clock := newDebugTestClock(90_000)
		hub, _ := newDebugTestHub(t, clock, &debugTestVerifier{state: IdentityActive})
		mustStartDebug(t, hub, 90, "binding-90")
		addDryTrace(t, hub, 90)
		cursor := currentEventIDs(t, hub, 90)[0]
		hub.mu.Lock()
		hub.discardOldestEventLocked(hub.activeByUser[90], GapRingEvicted, clock.Now())
		hub.mu.Unlock()
		subscription, err := hub.Subscribe(context.Background(), 90, "binding-90", cursor)
		if err != nil {
			t.Fatal(err)
		}
		requireGapSnapshot(t, subscription, GapRingEvicted)
	})

	t.Run("slow", func(t *testing.T) {
		clock := newDebugTestClock(100_000)
		hub, _ := newDebugTestHub(t, clock, &debugTestVerifier{state: IdentityActive})
		mustStartDebug(t, hub, 100, "binding-100")
		subscription, err := hub.Subscribe(context.Background(), 100, "binding-100", "")
		if err != nil {
			t.Fatal(err)
		}
		for index := 0; index < SubscriberQueueSize/2+1; index++ {
			addDryTrace(t, hub, 100)
		}
		requireGapSnapshot(t, subscription, GapSlowConsumer)
		metadata, err := hub.Metadata(100)
		if err != nil || !metadata.Active || metadata.ConnectedSubscribers != 1 {
			t.Fatalf("slow recovery metadata = (%+v,%v)", metadata, err)
		}
		addDryTrace(t, hub, 100)
		if next := mustNextDebug(t, subscription); next.Kind != EventTraceUpsert {
			t.Fatalf("post-recovery event = %s", next.Kind)
		}
	})
}

func TestSubscriberQueueCarriesOnlyEventIdentity(t *testing.T) {
	typeOfQueueItem := reflect.TypeOf(queuedEvent{})
	for index := 0; index < typeOfQueueItem.NumField(); index++ {
		field := typeOfQueueItem.Field(index)
		if field.Type.Kind() == reflect.Pointer || field.Type == reflect.TypeOf(eventRecord{}) {
			t.Fatalf("queued field %s retains an event record: %s", field.Name, field.Type)
		}
	}
}

func TestEvictedQueuedLargeEventRecoversWithSlowConsumerSnapshot(t *testing.T) {
	clock := newDebugTestClock(101_000)
	hub, _ := newDebugTestHub(t, clock, &debugTestVerifier{state: IdentityActive})
	mustStartDebug(t, hub, 101, "binding-101")
	subscription, err := hub.Subscribe(context.Background(), 101, "binding-101", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = subscription.Close() })
	_ = mustNextDebug(t, subscription)

	record := publishDebugEventTrace(t, hub, 101, largeDebugEventTrace(400*1024, 101, clock.Now().Unix()))
	hub.mu.Lock()
	current := hub.activeByUser[101]
	for record.inRing {
		if !hub.discardOldestEventLocked(current, GapRingEvicted, clock.Now()) {
			hub.mu.Unlock()
			t.Fatal("large queued event could not be evicted")
		}
	}
	if current.eventIndex[record.event.EventID] != nil || current.retainedBytes != 0 {
		hub.mu.Unlock()
		t.Fatalf("unpinned eviction retained record: index=%v retained=%d",
			current.eventIndex[record.event.EventID] != nil, current.retainedBytes)
	}
	hub.mu.Unlock()

	requireGapSnapshot(t, subscription, GapSlowConsumer)
}

func TestPinnedEvictedEventRemainsAccountedUntilRelease(t *testing.T) {
	clock := newDebugTestClock(101_500)
	hub, _ := newDebugTestHub(t, clock, &debugTestVerifier{state: IdentityActive})
	mustStartDebug(t, hub, 1015, "binding-1015")
	subscription, err := hub.Subscribe(context.Background(), 1015, "binding-1015", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = subscription.Close() })
	_ = mustNextDebug(t, subscription)

	record := publishDebugEventTrace(t, hub, 1015, largeDebugEventTrace(400*1024, 1015, clock.Now().Unix()))
	queued := popQueuedDebugEvent(t, subscription)
	if queued.eventID != record.event.EventID {
		t.Fatalf("queued event = %q, want %q", queued.eventID, record.event.EventID)
	}
	pin, ok := subscription.pinQueued(queued)
	if !ok {
		t.Fatal("large queued event was not pinned")
	}

	hub.mu.Lock()
	current := hub.activeByUser[1015]
	for len(current.events) > 0 && current.events[0] != record {
		hub.discardOldestEventLocked(current, GapRingEvicted, clock.Now())
	}
	beforeSession, beforeGlobal := current.bytes(), hub.totalBytesLocked()
	if !hub.discardOldestEventLocked(current, GapRingEvicted, clock.Now()) {
		hub.mu.Unlock()
		t.Fatal("pinned event could not leave the ring")
	}
	if record.inRing || record.pinCount != 1 || current.retainedEvents[record.event.EventID] != record ||
		current.retainedBytes != record.wireBytes || current.bytes() != beforeSession ||
		hub.totalBytesLocked() != beforeGlobal {
		hub.mu.Unlock()
		t.Fatalf("pinned eviction accounting: in_ring=%v pins=%d retained=%d session=%d/%d global=%d/%d",
			record.inRing, record.pinCount, current.retainedBytes, current.bytes(), beforeSession,
			hub.totalBytesLocked(), beforeGlobal)
	}
	hub.mu.Unlock()

	subscription.releasePin(pin)
	hub.mu.Lock()
	if record.pinCount != 0 || current.retainedBytes != 0 || current.eventIndex[record.event.EventID] != nil ||
		current.bytes() != beforeSession-record.wireBytes || hub.totalBytesLocked() != beforeGlobal-int64(record.wireBytes) {
		hub.mu.Unlock()
		t.Fatalf("pin release accounting: pins=%d retained=%d session=%d global=%d",
			record.pinCount, current.retainedBytes, current.bytes(), hub.totalBytesLocked())
	}
	hub.mu.Unlock()
}

func TestNextHandoffAndDrainReleasePins(t *testing.T) {
	t.Run("next handoff", func(t *testing.T) {
		clock := newDebugTestClock(101_700)
		hub, _ := newDebugTestHub(t, clock, &debugTestVerifier{state: IdentityActive})
		mustStartDebug(t, hub, 1017, "binding-1017")
		subscription, err := hub.Subscribe(context.Background(), 1017, "binding-1017", "")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = subscription.Close() })
		_ = mustNextDebug(t, subscription)
		record := publishDebugEventTrace(t, hub, 1017, largeDebugEventTrace(32*1024, 1017, clock.Now().Unix()))
		if event := mustNextDebug(t, subscription); event.EventID != record.event.EventID {
			t.Fatalf("Next event = %q, want %q", event.EventID, record.event.EventID)
		}
		hub.mu.Lock()
		pins, retained := len(subscription.pins), subscription.session.retainedBytes
		recordPins := record.pinCount
		hub.mu.Unlock()
		if pins != 0 || recordPins != 0 || retained != 0 {
			t.Fatalf("Next handoff retained pins: subscription=%d record=%d bytes=%d", pins, recordPins, retained)
		}
	})

	t.Run("drain", func(t *testing.T) {
		clock := newDebugTestClock(101_800)
		hub, _ := newDebugTestHub(t, clock, &debugTestVerifier{state: IdentityActive})
		mustStartDebug(t, hub, 1018, "binding-1018")
		subscription, err := hub.Subscribe(context.Background(), 1018, "binding-1018", "")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = subscription.Close() })
		_ = mustNextDebug(t, subscription)
		record := publishDebugEventTrace(t, hub, 1018, largeDebugEventTrace(128*1024, 1018, clock.Now().Unix()))
		queued := popQueuedDebugEvent(t, subscription)
		if _, ok := subscription.pinQueued(queued); !ok {
			t.Fatal("event was not pinned before drain")
		}
		hub.mu.Lock()
		current := hub.activeByUser[1018]
		for len(current.events) > 0 && current.events[0] != record {
			hub.discardOldestEventLocked(current, GapRingEvicted, clock.Now())
		}
		hub.discardOldestEventLocked(current, GapRingEvicted, clock.Now())
		if current.retainedBytes != record.wireBytes {
			hub.mu.Unlock()
			t.Fatalf("pre-drain retained bytes = %d, want %d", current.retainedBytes, record.wireBytes)
		}
		subscription.drainLocked()
		if len(subscription.pins) != 0 || record.pinCount != 0 || current.retainedBytes != 0 {
			hub.mu.Unlock()
			t.Fatalf("drain retained pins: subscription=%d record=%d bytes=%d",
				len(subscription.pins), record.pinCount, current.retainedBytes)
		}
		hub.mu.Unlock()
	})
}

func TestGapFirstAvailableEventIDRemainsInRingAtCapacity(t *testing.T) {
	clock := newDebugTestClock(102_000)
	hub, _ := newDebugTestHub(t, clock, &debugTestVerifier{state: IdentityActive})
	mustStartDebug(t, hub, 102, "binding-102")
	for index := 0; index < MaxSessionEvents/2; index++ {
		addDryTrace(t, hub, 102)
	}
	full := currentEventIDs(t, hub, 102)
	if len(full) != MaxSessionEvents {
		t.Fatalf("full ring events = %d", len(full))
	}
	cursor := full[0]
	addDryTrace(t, hub, 102)

	subscription, err := hub.Subscribe(context.Background(), 102, "binding-102", cursor)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = subscription.Close() })
	gapEvent := mustNextDebug(t, subscription)
	if gapEvent.Kind != EventGap {
		t.Fatalf("recovery first event = %s", gapEvent.Kind)
	}
	gap := decodeDebugData[GapData](t, gapEvent)
	if gap.Reason != GapRingEvicted || gap.FirstAvailableEventID == nil {
		t.Fatalf("ring-boundary gap = %+v", gap)
	}
	ring := currentEventIDs(t, hub, 102)
	found := false
	for _, eventID := range ring {
		if eventID == *gap.FirstAvailableEventID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("first available event %q was evicted while appending recovery pair", *gap.FirstAvailableEventID)
	}
	snapshot := decodeDebugData[SnapshotData](t, mustNextDebug(t, subscription))
	if snapshot.FirstEventID == nil || *snapshot.FirstEventID != *gap.FirstAvailableEventID {
		t.Fatalf("gap/snapshot first event mismatch: gap=%+v snapshot=%+v", gap, snapshot)
	}
}

func TestSlowConsumerTerminalSnapshotHasAtomicInflightState(t *testing.T) {
	clock := newDebugTestClock(105_000)
	hub, _ := newDebugTestHub(t, clock, &debugTestVerifier{state: IdentityActive})
	mustStartDebug(t, hub, 105, "binding-105")
	if _, err := hub.ChangeMode(105, "1", ModeLive, true); err != nil {
		t.Fatal(err)
	}
	decision, err := hub.DecideAfterAdmission(context.Background(), CaptureInput{
		UserID: 105, RouteKind: RouteOpenAIChat, Model: "m", MediaType: "application/json",
		Body: []byte(`{}`), IdentityCertain: true,
	})
	if err != nil || decision.Trace == nil || decision.Mode != ModeLive {
		t.Fatalf("live trace = (%+v,%v)", decision, err)
	}
	events := currentEventIDs(t, hub, 105)
	subscription, err := hub.Subscribe(context.Background(), 105, "binding-105", events[len(events)-1])
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = subscription.Close() })
	if err := decision.Trace.MarkDispatched(); err != nil {
		t.Fatal(err)
	}
	status := http.StatusOK
	for index := 0; index < SubscriberQueueSize; index++ {
		clock.Advance(time.Second)
		if err := decision.Trace.RecordUpstream(DebugUpstreamResult{
			ResultKind: ResultResponse, StatusCode: &status, Usage: ZeroLogUsage(), CompletedAt: clock.Now().Unix(),
		}); err != nil {
			t.Fatalf("RecordUpstream %d: %v", index, err)
		}
	}
	clock.Advance(time.Second)
	if err := decision.Trace.CompleteLiveCaptured("en"); err != nil {
		t.Fatal(err)
	}

	gap := mustNextDebug(t, subscription)
	if gap.Kind != EventGap || decodeDebugData[GapData](t, gap).Reason != GapSlowConsumer {
		t.Fatalf("terminal recovery gap = %+v", gap)
	}
	snapshotEvent := mustNextDebug(t, subscription)
	if snapshotEvent.Kind != EventSnapshot {
		t.Fatalf("terminal recovery successor = %s", snapshotEvent.Kind)
	}
	snapshot := decodeDebugData[SnapshotData](t, snapshotEvent)
	if snapshot.Session.InflightCount != 0 || len(snapshot.Traces) != 1 ||
		snapshot.Traces[0].State != TraceTerminal || snapshot.Traces[0].CallerResult == nil {
		t.Fatalf("terminal recovery snapshot is not atomic: %+v", snapshot)
	}
}

func TestObserverDisconnectIsSeparateFromSessionAndRequest(t *testing.T) {
	clock := newDebugTestClock(110_000)
	verifier := &debugTestVerifier{state: IdentityActive}
	hub, _ := newDebugTestHub(t, clock, verifier)
	mustStartDebug(t, hub, 110, "binding-110")
	if _, err := hub.ChangeMode(110, "1", ModeLive, true); err != nil {
		t.Fatal(err)
	}
	decision, err := hub.DecideAfterAdmission(context.Background(), CaptureInput{
		UserID: 110, RouteKind: RouteOpenAIChat, Model: "m", MediaType: "application/json",
		Body: []byte(`{}`), IdentityCertain: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err := hub.Subscribe(context.Background(), 110, "binding-110", "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := hub.Subscribe(context.Background(), 110, "binding-110", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := hub.Subscribe(context.Background(), 110, "binding-110", ""); !errors.Is(err, ErrCapacity) {
		t.Fatalf("third subscriber = %v", err)
	}
	_ = mustNextDebug(t, first)
	_ = mustNextDebug(t, second)
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	metadata, err := hub.Metadata(110)
	if err != nil || !metadata.Active || metadata.ConnectedSubscribers != 1 || metadata.InflightCount != 1 {
		t.Fatalf("after observer close = (%+v,%v)", metadata, err)
	}
	select {
	case <-decision.Trace.Context().Done():
		t.Fatal("observer close cancelled live request")
	default:
	}
	if err := decision.Trace.MarkDispatched(); err != nil {
		t.Fatal(err)
	}
	if err := decision.Trace.CompleteLiveCaptured("en"); err != nil {
		t.Fatal(err)
	}
	_ = second.Close()
}

type lockedDebugWriter struct {
	mu     sync.Mutex
	header http.Header
	body   bytes.Buffer
	status int
}

func newLockedDebugWriter() *lockedDebugWriter {
	return &lockedDebugWriter{header: make(http.Header)}
}

func (writer *lockedDebugWriter) Header() http.Header { return writer.header }
func (writer *lockedDebugWriter) WriteHeader(status int) {
	writer.mu.Lock()
	if writer.status == 0 {
		writer.status = status
	}
	writer.mu.Unlock()
}
func (writer *lockedDebugWriter) Write(data []byte) (int, error) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if writer.status == 0 {
		writer.status = http.StatusOK
	}
	return writer.body.Write(data)
}
func (writer *lockedDebugWriter) Flush() {}
func (writer *lockedDebugWriter) String() string {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	return writer.body.String()
}

type blockingDebugWriter struct {
	header  http.Header
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func newBlockingDebugWriter() *blockingDebugWriter {
	return &blockingDebugWriter{
		header: make(http.Header), entered: make(chan struct{}), release: make(chan struct{}),
	}
}

func (writer *blockingDebugWriter) Header() http.Header { return writer.header }
func (writer *blockingDebugWriter) WriteHeader(int)     {}
func (writer *blockingDebugWriter) Write(data []byte) (int, error) {
	writer.once.Do(func() { close(writer.entered) })
	<-writer.release
	return len(data), nil
}
func (writer *blockingDebugWriter) Flush() {}

func TestStreamCloseWaitsForPinnedWriteThenReleasesRetainedEvent(t *testing.T) {
	clock := newDebugTestClock(119_000)
	hub, _ := newDebugTestHub(t, clock, &debugTestVerifier{state: IdentityActive})
	mustStartDebug(t, hub, 119, "binding-119")
	subscription, err := hub.Subscribe(context.Background(), 119, "binding-119", "")
	if err != nil {
		t.Fatal(err)
	}
	_ = mustNextDebug(t, subscription)
	record := publishDebugEventTrace(t, hub, 119, largeDebugEventTrace(256*1024, 119, clock.Now().Unix()))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	writer := newBlockingDebugWriter()
	streamDone := make(chan error, 1)
	go func() { streamDone <- subscription.Stream(ctx, writer) }()
	waitDebug(t, writer.entered)

	hub.mu.Lock()
	current := hub.activeByUser[119]
	for len(current.events) > 0 && current.events[0] != record {
		hub.discardOldestEventLocked(current, GapRingEvicted, clock.Now())
	}
	if !hub.discardOldestEventLocked(current, GapRingEvicted, clock.Now()) ||
		current.retainedBytes != record.wireBytes || record.pinCount != 1 {
		hub.mu.Unlock()
		t.Fatalf("blocked write was not retained: bytes=%d pins=%d", current.retainedBytes, record.pinCount)
	}
	hub.mu.Unlock()

	closeDone := make(chan error, 1)
	go func() { closeDone <- subscription.Close() }()
	close(writer.release)
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close deadlocked with pinned stream write")
	}
	select {
	case err := <-streamDone:
		if err != nil {
			t.Fatalf("Stream: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Stream did not finish after close")
	}

	hub.mu.Lock()
	if len(subscription.pins) != 0 || record.pinCount != 0 || current.retainedBytes != 0 ||
		current.eventIndex[record.event.EventID] != nil {
		hub.mu.Unlock()
		t.Fatalf("close retained event: subscription=%d record=%d bytes=%d index=%v",
			len(subscription.pins), record.pinCount, current.retainedBytes,
			current.eventIndex[record.event.EventID] != nil)
	}
	hub.mu.Unlock()
}

func TestDedicatedSSEHeartbeatHasNoIDAndStreamClosesOnlyObserver(t *testing.T) {
	clock := newDebugTestClock(120_000)
	hub, _ := newDebugTestHub(t, clock, &debugTestVerifier{state: IdentityActive})
	hub.config.heartbeat = 2 * time.Millisecond
	mustStartDebug(t, hub, 120, "binding-120")
	subscription, err := hub.Subscribe(context.Background(), 120, "binding-120", "")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Millisecond)
	defer cancel()
	writer := newLockedDebugWriter()
	err = subscription.Stream(ctx, writer)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Stream = %v", err)
	}
	body := writer.String()
	if !strings.Contains(body, ": heartbeat\n\n") {
		t.Fatalf("missing heartbeat: %q", body)
	}
	for _, block := range strings.Split(body, "\n\n") {
		if strings.HasPrefix(block, ": heartbeat") && strings.Contains(block, "id:") {
			t.Fatalf("heartbeat allocated an ID: %q", block)
		}
	}
	metadata, err := hub.Metadata(120)
	if err != nil || !metadata.Active || metadata.ConnectedSubscribers != 0 {
		t.Fatalf("stream disconnect changed session = (%+v,%v)", metadata, err)
	}
}
