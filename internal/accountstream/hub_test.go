package accountstream

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func (clock *testClock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.now
}

func (clock *testClock) Advance(duration time.Duration) {
	clock.mu.Lock()
	clock.now = clock.now.Add(duration)
	clock.mu.Unlock()
}

type testAuthority struct {
	mu       sync.Mutex
	epochs   map[int64]*string
	revision map[int64]string
	fail     map[string]error
}

type snapshotCall struct {
	accountID    int64
	channel      Channel
	contextValue string
}

type snapshotContextKey struct{}

type blockingSnapshotAdapter struct {
	delegate SnapshotAdapter
	block    atomic.Bool
	started  chan snapshotCall
	release  chan struct{}
}

func (adapter *blockingSnapshotAdapter) Snapshot(ctx context.Context, accountID int64, channel Channel) (Snapshot, error) {
	if adapter.block.Load() {
		adapter.started <- snapshotCall{
			accountID: accountID, channel: channel,
			contextValue: fmt.Sprint(ctx.Value(snapshotContextKey{})),
		}
		select {
		case <-adapter.release:
		case <-ctx.Done():
			return Snapshot{}, ctx.Err()
		}
	}
	return adapter.delegate.Snapshot(ctx, accountID, channel)
}

func newTestAuthority(accountIDs ...int64) *testAuthority {
	authority := &testAuthority{
		epochs: make(map[int64]*string), revision: make(map[int64]string), fail: make(map[string]error),
	}
	for _, accountID := range accountIDs {
		authority.revision[accountID] = "1"
	}
	return authority
}

func cloneString(value *string) *string {
	if value == nil {
		return nil
	}
	copyValue := *value
	return &copyValue
}

func (authority *testAuthority) setEpoch(accountID int64, epoch *string) {
	authority.mu.Lock()
	authority.epochs[accountID] = cloneString(epoch)
	authority.mu.Unlock()
}

func (authority *testAuthority) setRevision(accountID int64, revision string) {
	authority.mu.Lock()
	authority.revision[accountID] = revision
	authority.mu.Unlock()
}

func (authority *testAuthority) setFailure(accountID int64, channel Channel, err error) {
	authority.mu.Lock()
	key := fmt.Sprintf("%d/%s", accountID, channel)
	if err == nil {
		delete(authority.fail, key)
	} else {
		authority.fail[key] = err
	}
	authority.mu.Unlock()
}

func (authority *testAuthority) epoch(accountID int64) *string {
	authority.mu.Lock()
	defer authority.mu.Unlock()
	return cloneString(authority.epochs[accountID])
}

func (authority *testAuthority) CurrentIdentityEpoch(_ context.Context, accountID int64) (*string, error) {
	return authority.epoch(accountID), nil
}

func (authority *testAuthority) Snapshot(_ context.Context, accountID int64, channel Channel) (Snapshot, error) {
	authority.mu.Lock()
	defer authority.mu.Unlock()
	if err := authority.fail[fmt.Sprintf("%d/%s", accountID, channel)]; err != nil {
		return Snapshot{}, err
	}
	revision := authority.revision[accountID]
	if revision == "" {
		revision = "1"
	}
	var epoch *string
	if channel == ChannelRPS {
		epoch = cloneString(authority.epochs[accountID])
	}
	payload, err := json.Marshal(struct {
		AccountID int64   `json:"account_id"`
		Channel   Channel `json:"channel"`
		Revision  string  `json:"revision"`
		Epoch     *string `json:"identity_epoch"`
	}{AccountID: accountID, Channel: channel, Revision: revision, Epoch: epoch})
	if err != nil {
		return Snapshot{}, err
	}
	return Snapshot{Revision: &revision, IdentityEpoch: epoch, Data: payload}, nil
}

func deterministicIDs() func(string) (string, error) {
	var sequence atomic.Uint64
	return func(prefix string) (string, error) {
		var material [16]byte
		binary.BigEndian.PutUint64(material[8:], sequence.Add(1))
		return prefix + base64.RawURLEncoding.EncodeToString(material[:]), nil
	}
}

func newTestHub(t *testing.T, authority *testAuthority, configure func(*hubConfig)) (*Hub, *testClock) {
	t.Helper()
	clock := &testClock{now: time.Unix(1_800_200_000, 0)}
	config := defaultConfig(authority, authority)
	config.now = clock.Now
	config.newID = deterministicIDs()
	if configure != nil {
		configure(&config)
	}
	hub, err := newHub(config)
	if err != nil {
		t.Fatalf("new hub: %v", err)
	}
	t.Cleanup(func() { _ = hub.Close() })
	return hub, clock
}

func stringPointer(value string) *string { return &value }

func published(channel Channel, eventType EventType, revision string, epoch *string, marker string) PublishedEvent {
	return PublishedEvent{
		Channel: channel, Type: eventType, Revision: stringPointer(revision), IdentityEpoch: cloneString(epoch),
		Data: json.RawMessage(fmt.Sprintf(`{"marker":%q}`, marker)),
	}
}

func nextFrame(t *testing.T, subscription *Subscription) Frame {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	frame, err := subscription.Next(ctx)
	if err != nil {
		t.Fatalf("next frame: %v", err)
	}
	return frame
}

func decodedGap(t *testing.T, frame Frame) gapPayload {
	t.Helper()
	if frame.Type != TypeGap || frame.Revision != nil || frame.IdentityEpoch != nil {
		t.Fatalf("not a canonical gap: %+v", frame)
	}
	var payload gapPayload
	if err := json.Unmarshal(frame.Data, &payload); err != nil {
		t.Fatalf("decode gap: %v", err)
	}
	return payload
}

func TestDefaultHubBudgetAndEventValidation(t *testing.T) {
	authority := newTestAuthority(1)
	hub, _ := newTestHub(t, authority, nil)
	if hub.config.maxGlobal != MaxGlobalConnections || hub.config.maxPerAccount != MaxConnectionsPerAccount ||
		hub.config.queueSize != SubscriberQueueSize || hub.config.maxRingEvents != MaxRingEvents ||
		hub.config.maxRingBytes != MaxRingBytes || hub.config.ringAge != 5*time.Minute ||
		hub.config.heartbeat != 15*time.Second || hub.config.writeTimeout != 15*time.Second {
		t.Fatalf("default config=%+v", hub.config)
	}

	invalid := []PublishedEvent{
		published("unknown", TypeDelta, "1", nil, "bad-channel"),
		published(ChannelActivities, TypeGap, "1", nil, "external-gap"),
		published(ChannelActivities, TypeDelta, "01", nil, "bad-revision"),
		published(ChannelActivities, TypeDelta, "1", stringPointer("2"), "activity-epoch"),
		{Channel: ChannelActivities, Type: TypeDelta, Revision: stringPointer("1"), Data: json.RawMessage(`{`)},
	}
	for index, event := range invalid {
		if _, err := hub.PublishCommitted(context.Background(), 1, event); !errors.Is(err, ErrInvalidEvent) {
			t.Fatalf("invalid event %d error=%v", index, err)
		}
	}
	large := make([]byte, MaxDeltaBytes)
	for index := range large {
		large[index] = 'a'
	}
	large[0], large[len(large)-1] = '"', '"'
	if _, err := hub.PublishCommitted(context.Background(), 1, PublishedEvent{
		Channel: ChannelActivities, Type: TypeDelta, Revision: stringPointer("1"), Data: large,
	}); !errors.Is(err, ErrInvalidEvent) {
		t.Fatalf("envelope-over-limit error=%v", err)
	}

	revision := "2"
	payload := json.RawMessage(`{"marker":"immutable"}`)
	frame, err := hub.PublishCommitted(context.Background(), 1, PublishedEvent{
		Channel: ChannelActivities, Type: TypeDelta, Revision: &revision, Data: payload,
	})
	if err != nil {
		t.Fatal(err)
	}
	revision = "999"
	payload[11] = 'X'
	if frame.Revision == nil || *frame.Revision != "2" || string(frame.Data) != `{"marker":"immutable"}` {
		t.Fatalf("published frame aliased caller memory: %+v", frame)
	}
}

func TestSubscribeReplayAndGapReasons(t *testing.T) {
	authority := newTestAuthority(1)
	hub, clock := newTestHub(t, authority, func(config *hubConfig) {
		config.maxRingEvents = 4
		config.maxRingBytes = MaxRingBytes
	})

	subscription, err := hub.Subscribe(context.Background(), SubscribeRequest{AccountID: 1, Channels: []Channel{ChannelActivities}})
	if err != nil {
		t.Fatal(err)
	}
	initial := nextFrame(t, subscription)
	if initial.Type != TypeSnapshot || initial.Channel != ChannelActivities || !db.ValidateOpaqueID(initial.ID, "sse_") {
		t.Fatalf("initial frame=%+v", initial)
	}
	first, err := hub.PublishCommitted(context.Background(), 1, published(ChannelActivities, TypeDelta, "2", nil, "first"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := hub.PublishCommitted(context.Background(), 1, published(ChannelActivities, TypeDelta, "3", nil, "second"))
	if err != nil {
		t.Fatal(err)
	}
	if nextFrame(t, subscription).ID != first.ID || nextFrame(t, subscription).ID != second.ID {
		t.Fatal("live publish order changed")
	}
	_ = subscription.Close()

	replay, err := hub.Subscribe(context.Background(), SubscribeRequest{
		AccountID: 1, Channels: []Channel{ChannelActivities}, LastEventID: first.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if frame := nextFrame(t, replay); frame.ID != second.ID || frame.Type != TypeDelta {
		t.Fatalf("replayed frame=%+v", frame)
	}
	_ = replay.Close()

	unknownID, err := db.GenerateOpaqueID("sse_")
	if err != nil {
		t.Fatal(err)
	}
	unknown, err := hub.Subscribe(context.Background(), SubscribeRequest{
		AccountID: 1, Channels: []Channel{ChannelActivities}, LastEventID: unknownID,
	})
	if err != nil {
		t.Fatal(err)
	}
	gap := decodedGap(t, nextFrame(t, unknown))
	if gap.Reason != GapProcessRestart || gap.LastEventID == nil || *gap.LastEventID != unknownID {
		t.Fatalf("unknown cursor gap=%+v", gap)
	}
	if nextFrame(t, unknown).Type != TypeSnapshot {
		t.Fatal("unknown cursor gap was not followed by snapshot")
	}
	_ = unknown.Close()

	malformed, err := hub.Subscribe(context.Background(), SubscribeRequest{
		AccountID: 1, Channels: []Channel{ChannelActivities}, LastEventID: "not-an-event-id",
	})
	if err != nil {
		t.Fatal(err)
	}
	gap = decodedGap(t, nextFrame(t, malformed))
	if gap.Reason != GapProcessRestart || gap.LastEventID != nil {
		t.Fatalf("malformed cursor gap=%+v", gap)
	}
	_ = nextFrame(t, malformed)
	_ = malformed.Close()

	// Create a cursor and age it exactly to the five-minute boundary.
	aged, err := hub.PublishCommitted(context.Background(), 1, published(ChannelActivities, TypeDelta, "4", nil, "aged"))
	if err != nil {
		t.Fatal(err)
	}
	clock.Advance(5 * time.Minute)
	expired, err := hub.Subscribe(context.Background(), SubscribeRequest{
		AccountID: 1, Channels: []Channel{ChannelActivities}, LastEventID: aged.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	gap = decodedGap(t, nextFrame(t, expired))
	if gap.Reason != GapRingExpired {
		t.Fatalf("expired cursor gap=%+v", gap)
	}
	if nextFrame(t, expired).Type != TypeSnapshot {
		t.Fatal("expired gap was not followed by snapshot")
	}
	_ = expired.Close()
}

func TestRingEvictionSlowConsumerCapacityAndClose(t *testing.T) {
	authority := newTestAuthority(1, 2, 3)
	hub, _ := newTestHub(t, authority, func(config *hubConfig) {
		config.maxGlobal = 2
		config.maxPerAccount = 1
		config.queueSize = 4
		config.maxRingEvents = 4
	})

	slow, err := hub.Subscribe(context.Background(), SubscribeRequest{AccountID: 1, Channels: []Channel{ChannelActivities}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := hub.Subscribe(context.Background(), SubscribeRequest{AccountID: 1, Channels: []Channel{ChannelActivities}}); !errors.Is(err, ErrCapacity) {
		t.Fatalf("per-account capacity error=%v", err)
	}
	secondAccount, err := hub.Subscribe(context.Background(), SubscribeRequest{AccountID: 2, Channels: []Channel{ChannelActivities}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := hub.Subscribe(context.Background(), SubscribeRequest{AccountID: 3, Channels: []Channel{ChannelActivities}}); !errors.Is(err, ErrCapacity) {
		t.Fatalf("global capacity error=%v", err)
	}

	var evictedCursor string
	for index := 0; index < 6; index++ {
		frame, publishErr := hub.PublishCommitted(context.Background(), 1, published(ChannelActivities, TypeDelta, fmt.Sprint(index+2), nil, fmt.Sprint(index)))
		if publishErr != nil {
			t.Fatal(publishErr)
		}
		if index == 0 {
			evictedCursor = frame.ID
		}
	}
	gap := decodedGap(t, nextFrame(t, slow))
	if gap.Reason != GapSlowConsumer {
		t.Fatalf("slow-consumer gap=%+v", gap)
	}
	if frame := nextFrame(t, slow); frame.Type != TypeSnapshot {
		t.Fatalf("slow-consumer follow-up=%+v", frame)
	}
	if _, err := slow.Next(context.Background()); !errors.Is(err, ErrClosed) {
		t.Fatalf("slow consumer remained open: %v", err)
	}

	if err := secondAccount.Close(); err != nil {
		t.Fatal(err)
	}
	reconnect, err := hub.Subscribe(context.Background(), SubscribeRequest{
		AccountID: 1, Channels: []Channel{ChannelActivities}, LastEventID: evictedCursor,
	})
	if err != nil {
		t.Fatal(err)
	}
	gap = decodedGap(t, nextFrame(t, reconnect))
	if gap.Reason != GapRingEvicted {
		t.Fatalf("evicted cursor gap=%+v", gap)
	}
	_ = nextFrame(t, reconnect)
	_ = reconnect.Close()

	third, err := hub.Subscribe(context.Background(), SubscribeRequest{AccountID: 3, Channels: []Channel{ChannelActivities}})
	if err != nil {
		t.Fatalf("capacity was not released: %v", err)
	}
	if err := hub.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := third.Next(context.Background()); !errors.Is(err, ErrClosed) {
		t.Fatalf("close did not terminate subscriber: %v", err)
	}
	if _, err := hub.PublishCommitted(context.Background(), 3, published(ChannelActivities, TypeDelta, "2", nil, "closed")); !errors.Is(err, ErrClosed) {
		t.Fatalf("publish after close error=%v", err)
	}
	if _, err := hub.Subscribe(context.Background(), SubscribeRequest{AccountID: 3, Channels: []Channel{ChannelActivities}}); !errors.Is(err, ErrClosed) {
		t.Fatalf("subscribe after close error=%v", err)
	}
}

func TestSlowConsumerIgnoresPublishCancellationAndFetchesLatestAuthoritativeSnapshot(t *testing.T) {
	authority := newTestAuthority(1)
	blocking := &blockingSnapshotAdapter{
		delegate: authority,
		started:  make(chan snapshotCall, 1),
		release:  make(chan struct{}),
	}
	hub, _ := newTestHub(t, authority, func(config *hubConfig) {
		config.queueSize = 4
		config.snapshots = blocking
	})
	subscription, err := hub.Subscribe(context.Background(), SubscribeRequest{
		AccountID: 1, Channels: []Channel{ChannelActivities},
	})
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 4; index++ {
		if _, err := hub.PublishCommitted(context.Background(), 1, published(
			ChannelActivities, TypeDelta, fmt.Sprint(index+2), nil, fmt.Sprintf("queued-%d", index),
		)); err != nil {
			t.Fatal(err)
		}
	}

	authority.setRevision(1, "99")
	blocking.block.Store(true)
	publishContext, cancelPublish := context.WithCancel(context.WithValue(
		context.Background(), snapshotContextKey{}, "publish-context",
	))
	type publishResult struct {
		frame Frame
		err   error
	}
	result := make(chan publishResult, 1)
	go func() {
		frame, publishErr := hub.PublishCommitted(publishContext, 1, published(
			ChannelActivities, TypeDelta, "6", nil, "stale-delta",
		))
		result <- publishResult{frame: frame, err: publishErr}
	}()

	var latest Frame
	select {
	case publishedResult := <-result:
		if publishedResult.err != nil {
			t.Fatal(publishedResult.err)
		}
		latest = publishedResult.frame
	case <-time.After(time.Second):
		t.Fatal("publisher blocked on slow-consumer snapshot")
	}
	// The request/handler may finish immediately after PublishCommitted. The
	// already-triggered recovery must retain values but not inherit cancellation.
	cancelPublish()
	select {
	case call := <-blocking.started:
		if call.accountID != 1 || call.channel != ChannelActivities || call.contextValue != "publish-context" {
			t.Fatalf("snapshot call=%+v", call)
		}
	case <-time.After(time.Second):
		t.Fatal("slow-consumer snapshot adapter was not called")
	}
	close(blocking.release)

	gap := decodedGap(t, nextFrame(t, subscription))
	if gap.Reason != GapSlowConsumer || gap.LastEventID == nil || *gap.LastEventID != latest.ID {
		t.Fatalf("slow-consumer gap=%+v latest=%s", gap, latest.ID)
	}
	snapshot := nextFrame(t, subscription)
	if snapshot.Channel != ChannelActivities || snapshot.Type != TypeSnapshot ||
		snapshot.Revision == nil || *snapshot.Revision != "99" || string(snapshot.Data) == string(latest.Data) {
		t.Fatalf("slow-consumer snapshot=%+v latest=%+v", snapshot, latest)
	}
	var payload struct {
		AccountID int64   `json:"account_id"`
		Channel   Channel `json:"channel"`
		Revision  string  `json:"revision"`
	}
	if err := json.Unmarshal(snapshot.Data, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.AccountID != 1 || payload.Channel != ChannelActivities || payload.Revision != "99" {
		t.Fatalf("authoritative snapshot payload=%+v", payload)
	}
	if _, err := subscription.Next(context.Background()); !errors.Is(err, ErrClosed) {
		t.Fatalf("slow consumer remained open: %v", err)
	}
}

func TestSlowConsumerSnapshotFailureClosesSafelyAndReleasesCapacity(t *testing.T) {
	authority := newTestAuthority(1)
	hub, _ := newTestHub(t, authority, func(config *hubConfig) {
		config.maxGlobal = 1
		config.maxPerAccount = 1
		config.queueSize = 4
	})
	subscription, err := hub.Subscribe(context.Background(), SubscribeRequest{
		AccountID: 1, Channels: []Channel{ChannelActivities},
	})
	if err != nil {
		t.Fatal(err)
	}
	authority.setFailure(1, ChannelActivities, errors.New("snapshot unavailable"))
	for index := 0; index < 5; index++ {
		if _, err := hub.PublishCommitted(context.Background(), 1, published(
			ChannelActivities, TypeDelta, fmt.Sprint(index+2), nil, fmt.Sprint(index),
		)); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := subscription.Next(ctx); !errors.Is(err, ErrClosed) {
		t.Fatalf("snapshot failure did not close slow consumer: %v", err)
	}

	authority.setFailure(1, ChannelActivities, nil)
	replacement, err := hub.Subscribe(context.Background(), SubscribeRequest{
		AccountID: 1, Channels: []Channel{ChannelActivities},
	})
	if err != nil {
		t.Fatalf("slow-consumer close did not release capacity: %v", err)
	}
	_ = replacement.Close()
}

func TestSlowConsumerSnapshotTimeoutClosesAndReleasesCapacity(t *testing.T) {
	authority := newTestAuthority(1)
	blocking := &blockingSnapshotAdapter{
		delegate: authority,
		started:  make(chan snapshotCall, 1),
		release:  make(chan struct{}),
	}
	defer close(blocking.release)
	hub, _ := newTestHub(t, authority, func(config *hubConfig) {
		config.maxGlobal = 1
		config.maxPerAccount = 1
		config.queueSize = 4
		config.writeTimeout = 25 * time.Millisecond
		config.snapshots = blocking
	})
	subscription, err := hub.Subscribe(context.Background(), SubscribeRequest{
		AccountID: 1, Channels: []Channel{ChannelActivities},
	})
	if err != nil {
		t.Fatal(err)
	}
	blocking.block.Store(true)
	for index := 0; index < 5; index++ {
		if _, err := hub.PublishCommitted(context.Background(), 1, published(
			ChannelActivities, TypeDelta, fmt.Sprint(index+2), nil, fmt.Sprint(index),
		)); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case <-blocking.started:
	case <-time.After(time.Second):
		t.Fatal("blocking snapshot adapter was not called")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := subscription.Next(ctx); !errors.Is(err, ErrClosed) {
		t.Fatalf("snapshot timeout did not close slow consumer: %v", err)
	}
	if connections := hub.connections.Load(); connections != 0 {
		t.Fatalf("snapshot timeout leaked %d connection(s)", connections)
	}

	blocking.block.Store(false)
	replacement, err := hub.Subscribe(context.Background(), SubscribeRequest{
		AccountID: 1, Channels: []Channel{ChannelActivities},
	})
	if err != nil {
		t.Fatalf("snapshot timeout did not release capacity: %v", err)
	}
	_ = replacement.Close()
}
