package accountstream

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRPSPublishEpochGuardAndReconnect(t *testing.T) {
	authority := newTestAuthority(1)
	authority.setEpoch(1, stringPointer("1"))
	hub, _ := newTestHub(t, authority, nil)
	subscription, err := hub.Subscribe(context.Background(), SubscribeRequest{AccountID: 1, Channels: []Channel{ChannelRPS}})
	if err != nil {
		t.Fatal(err)
	}
	initial := nextFrame(t, subscription)
	if initial.Type != TypeSnapshot || initial.IdentityEpoch == nil || *initial.IdentityEpoch != "1" {
		t.Fatalf("initial RPS snapshot=%+v", initial)
	}
	old, err := hub.PublishCommitted(context.Background(), 1, published(ChannelRPS, TypeDelta, "2", stringPointer("1"), "old"))
	if err != nil {
		t.Fatal(err)
	}
	if nextFrame(t, subscription).ID != old.ID {
		t.Fatal("old epoch event was not delivered before identity change")
	}
	_ = subscription.Close()

	authority.setEpoch(1, stringPointer("2"))
	if _, err := hub.PublishCommitted(context.Background(), 1, published(ChannelRPS, TypeDelta, "3", stringPointer("1"), "stale")); !errors.Is(err, ErrStaleIdentityEpoch) {
		t.Fatalf("stale publish error=%v", err)
	}

	// Even before a purge call, reconnect rechecks the authoritative epoch and
	// cannot replay an old-identity ring frame.
	reconnect, err := hub.Subscribe(context.Background(), SubscribeRequest{
		AccountID: 1, Channels: []Channel{ChannelRPS}, LastEventID: old.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	gap := decodedGap(t, nextFrame(t, reconnect))
	if gap.Reason != GapRingEvicted {
		t.Fatalf("epoch mismatch gap=%+v", gap)
	}
	snapshot := nextFrame(t, reconnect)
	if snapshot.Type != TypeSnapshot || snapshot.IdentityEpoch == nil || *snapshot.IdentityEpoch != "2" {
		t.Fatalf("epoch replacement snapshot=%+v", snapshot)
	}
	_ = reconnect.Close()
}

func TestPurgeAccountsReplacesAllQueuedIdentityFrames(t *testing.T) {
	authority := newTestAuthority(1, 2)
	authority.setEpoch(1, stringPointer("1"))
	authority.setEpoch(2, stringPointer("1"))
	hub, _ := newTestHub(t, authority, nil)

	subscriptions := make(map[int64]*Subscription)
	oldIDs := make(map[int64]string)
	for _, accountID := range []int64{1, 2} {
		subscription, err := hub.Subscribe(context.Background(), SubscribeRequest{
			AccountID: accountID, Channels: []Channel{ChannelActivities, ChannelRPS},
		})
		if err != nil {
			t.Fatal(err)
		}
		subscriptions[accountID] = subscription
		_ = nextFrame(t, subscription)
		_ = nextFrame(t, subscription)
		frame, err := hub.PublishCommitted(context.Background(), accountID, published(ChannelRPS, TypeDelta, "2", stringPointer("1"), "old-identity"))
		if err != nil {
			t.Fatal(err)
		}
		oldIDs[accountID] = frame.ID
		// Leave the old frame queued; purge must remove it synchronously.
	}
	authority.setEpoch(1, stringPointer("2"))
	authority.setEpoch(2, stringPointer("2"))
	if err := hub.PurgeAccounts(context.Background(), []int64{2, 1, 2}); err != nil {
		t.Fatalf("purge accounts: %v", err)
	}

	for _, accountID := range []int64{1, 2} {
		subscription := subscriptions[accountID]
		for _, channel := range []Channel{ChannelActivities, ChannelRPS} {
			gapFrame := nextFrame(t, subscription)
			gap := decodedGap(t, gapFrame)
			if gapFrame.Channel != channel || gap.Reason != GapRingEvicted {
				t.Fatalf("account %d channel %s gap=%+v payload=%+v", accountID, channel, gapFrame, gap)
			}
			snapshot := nextFrame(t, subscription)
			if snapshot.Channel != channel || snapshot.Type != TypeSnapshot {
				t.Fatalf("account %d replacement snapshot=%+v", accountID, snapshot)
			}
			if channel == ChannelRPS && (snapshot.IdentityEpoch == nil || *snapshot.IdentityEpoch != "2") {
				t.Fatalf("account %d RPS epoch=%v", accountID, snapshot.IdentityEpoch)
			}
		}
		_ = subscription.Close()

		replay, err := hub.Subscribe(context.Background(), SubscribeRequest{
			AccountID: accountID, Channels: []Channel{ChannelRPS}, LastEventID: oldIDs[accountID],
		})
		if err != nil {
			t.Fatal(err)
		}
		gap := decodedGap(t, nextFrame(t, replay))
		if gap.Reason != GapRingEvicted {
			t.Fatalf("old cursor account %d gap=%+v", accountID, gap)
		}
		if frame := nextFrame(t, replay); frame.IdentityEpoch == nil || *frame.IdentityEpoch != "2" {
			t.Fatalf("old cursor account %d snapshot=%+v", accountID, frame)
		}
		_ = replay.Close()
	}
}

func TestPurgeAccountsSnapshotFailureIsAllOrNothing(t *testing.T) {
	authority := newTestAuthority(1, 2)
	authority.setEpoch(1, stringPointer("1"))
	authority.setEpoch(2, stringPointer("1"))
	hub, _ := newTestHub(t, authority, nil)
	frames := make(map[int64]Frame)
	for _, accountID := range []int64{1, 2} {
		frame, err := hub.PublishCommitted(context.Background(), accountID, published(ChannelRPS, TypeDelta, "1", stringPointer("1"), "retained"))
		if err != nil {
			t.Fatal(err)
		}
		frames[accountID] = frame
	}
	authority.mu.Lock()
	authority.fail["2/rps"] = errors.New("snapshot unavailable")
	authority.mu.Unlock()
	if err := hub.PurgeAccounts(context.Background(), []int64{1, 2}); err == nil {
		t.Fatal("purge succeeded despite snapshot failure")
	}
	for _, accountID := range []int64{1, 2} {
		state, err := hub.account(accountID)
		if err != nil {
			t.Fatal(err)
		}
		state.mu.Lock()
		found := false
		for _, entry := range state.ring {
			if entry.frame.ID == frames[accountID].ID {
				found = true
			}
		}
		state.mu.Unlock()
		if !found {
			t.Fatalf("account %d was partially purged", accountID)
		}
	}
}

func TestIdentityPurgeClosesAlreadySlowOldEpochSubscriber(t *testing.T) {
	authority := newTestAuthority(1)
	authority.setEpoch(1, stringPointer("1"))
	hub, _ := newTestHub(t, authority, func(config *hubConfig) { config.queueSize = 4 })
	subscription, err := hub.Subscribe(context.Background(), SubscribeRequest{AccountID: 1, Channels: []Channel{ChannelRPS}})
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 6; index++ {
		if _, err := hub.PublishCommitted(context.Background(), 1, published(ChannelRPS, TypeDelta, fmt.Sprint(index+2), stringPointer("1"), fmt.Sprint(index))); err != nil {
			t.Fatal(err)
		}
	}
	authority.setEpoch(1, stringPointer("2"))
	if err := hub.PurgeAccounts(context.Background(), []int64{1}); err != nil {
		t.Fatal(err)
	}
	if _, err := subscription.Next(context.Background()); !errors.Is(err, ErrClosed) {
		t.Fatalf("old-epoch slow subscriber survived purge: %v", err)
	}
}

type blockingEpochGuard struct {
	authority *testAuthority
	entered   chan struct{}
	release   chan struct{}
	calls     atomic.Int64
}

func (guard *blockingEpochGuard) CurrentIdentityEpoch(ctx context.Context, accountID int64) (*string, error) {
	if guard.calls.Add(1) == 1 {
		old := guard.authority.epoch(accountID)
		close(guard.entered)
		select {
		case <-guard.release:
			return old, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return guard.authority.CurrentIdentityEpoch(ctx, accountID)
}

func TestPublishRechecksEpochAfterWaitingForAccountLock(t *testing.T) {
	authority := newTestAuthority(1)
	authority.setEpoch(1, stringPointer("1"))
	guard := &blockingEpochGuard{authority: authority, entered: make(chan struct{}), release: make(chan struct{})}
	clock := &testClock{now: time.Unix(1_800_200_000, 0)}
	config := defaultConfig(authority, guard)
	config.now = clock.Now
	config.newID = deterministicIDs()
	hub, err := newHub(config)
	if err != nil {
		t.Fatal(err)
	}
	defer hub.Close()

	result := make(chan error, 1)
	go func() {
		_, publishErr := hub.PublishCommitted(context.Background(), 1, published(ChannelRPS, TypeDelta, "1", stringPointer("1"), "racing"))
		result <- publishErr
	}()
	select {
	case <-guard.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("publish did not reach first epoch check")
	}
	authority.setEpoch(1, stringPointer("2"))
	close(guard.release)
	select {
	case err := <-result:
		if !errors.Is(err, ErrStaleIdentityEpoch) {
			t.Fatalf("racing publish error=%v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("racing publish did not finish")
	}
	state, err := hub.account(1)
	if err != nil {
		t.Fatal(err)
	}
	state.mu.Lock()
	ringLength := len(state.ring)
	state.mu.Unlock()
	if ringLength != 0 {
		t.Fatalf("stale publish appended %d frames", ringLength)
	}
}

func TestConcurrentPublishPurgeReplayStress(t *testing.T) {
	authority := newTestAuthority(1, 2)
	authority.setEpoch(1, stringPointer("1"))
	authority.setEpoch(2, stringPointer("1"))
	hub, _ := newTestHub(t, authority, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var subscriptions []*Subscription
	var consumers sync.WaitGroup
	for _, accountID := range []int64{1, 2} {
		for connection := 0; connection < 2; connection++ {
			subscription, err := hub.Subscribe(ctx, SubscribeRequest{
				AccountID: accountID, Channels: []Channel{ChannelActivities, ChannelRPS},
			})
			if err != nil {
				t.Fatal(err)
			}
			subscriptions = append(subscriptions, subscription)
			consumers.Add(1)
			go func(subscription *Subscription) {
				defer consumers.Done()
				for {
					if _, err := subscription.Next(ctx); err != nil {
						return
					}
				}
			}(subscription)
		}
	}

	unexpected := make(chan error, 64)
	var publishers sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		publishers.Add(1)
		go func(worker int) {
			defer publishers.Done()
			for iteration := 0; iteration < 250; iteration++ {
				accountID := int64(iteration%2 + 1)
				if _, err := hub.PublishCommitted(ctx, accountID, published(ChannelActivities, TypeDelta, fmt.Sprint(iteration+1), nil, fmt.Sprintf("a-%d-%d", worker, iteration))); err != nil && !errors.Is(err, ErrClosed) {
					select {
					case unexpected <- err:
					default:
					}
					return
				}
				epoch := authority.epoch(accountID)
				_, err := hub.PublishCommitted(ctx, accountID, published(ChannelRPS, TypeDelta, fmt.Sprint(iteration+1), epoch, fmt.Sprintf("r-%d-%d", worker, iteration)))
				if err != nil && !errors.Is(err, ErrStaleIdentityEpoch) && !errors.Is(err, ErrClosed) {
					select {
					case unexpected <- err:
					default:
					}
					return
				}
			}
		}(worker)
	}
	for epoch := 2; epoch <= 20; epoch++ {
		value := fmt.Sprint(epoch)
		authority.setEpoch(1, &value)
		authority.setEpoch(2, &value)
		if err := hub.PurgeAccounts(ctx, []int64{2, 1}); err != nil {
			t.Fatalf("stress purge epoch %d: %v", epoch, err)
		}
	}
	publishers.Wait()
	select {
	case err := <-unexpected:
		t.Fatalf("stress publisher: %v", err)
	default:
	}
	for _, subscription := range subscriptions {
		_ = subscription.Close()
	}
	cancel()
	consumers.Wait()
}
