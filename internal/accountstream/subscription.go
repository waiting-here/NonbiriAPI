package accountstream

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

type Subscription struct {
	hub                *Hub
	account            *accountState
	accountID          int64
	topics             map[Channel]bool
	queue              chan queuedFrame
	closed             atomic.Bool
	sequence           atomic.Uint64
	closing            bool // guarded by account.mu
	activityRegistered bool

	pendingMu  sync.Mutex
	pending    []queuedFrame
	deliveryMu sync.Mutex
}

func normalizeChannels(channels []Channel) (map[Channel]bool, []Channel, error) {
	topics := make(map[Channel]bool, len(channels))
	for _, channel := range channels {
		if !channel.valid() {
			return nil, nil, ErrInvalidEvent
		}
		topics[channel] = true
	}
	if len(topics) == 0 {
		return nil, nil, ErrInvalidEvent
	}
	ordered := make([]Channel, 0, len(topics))
	for _, channel := range []Channel{ChannelActivities, ChannelRPS} {
		if topics[channel] {
			ordered = append(ordered, channel)
		}
	}
	return topics, ordered, nil
}

func (hub *Hub) Subscribe(ctx context.Context, request SubscribeRequest) (*Subscription, error) {
	if hub == nil || ctx == nil || hub.closed.Load() {
		return nil, ErrClosed
	}
	if request.AccountID <= 0 {
		return nil, ErrInvalidEvent
	}
	topics, ordered, err := normalizeChannels(request.Channels)
	if err != nil {
		return nil, err
	}
	if !hub.reserveConnection() {
		return nil, ErrCapacity
	}
	reserved := true
	defer func() {
		if reserved {
			hub.releaseConnection()
		}
	}()
	state, err := hub.account(request.AccountID)
	if err != nil {
		return nil, err
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if hub.closed.Load() || hub.accountForgotten(request.AccountID) {
		return nil, ErrClosed
	}
	if len(state.subscribers) >= hub.config.maxPerAccount {
		return nil, ErrCapacity
	}
	hub.cleanupLocked(state, hub.config.now())

	subscription := &Subscription{
		hub: hub, account: state, accountID: request.AccountID,
		topics: topics, queue: make(chan queuedFrame, hub.config.queueSize),
	}
	activitiesGeneration := hub.activitiesGeneration.Load()
	if topics[ChannelActivities] {
		activitiesGeneration, err = hub.registerActivitySubscription(request.AccountID)
		if err != nil {
			return nil, err
		}
		subscription.activityRegistered = true
	}
	initial, err := hub.initialFramesLocked(ctx, request.AccountID, state, ordered, request.LastEventID, activitiesGeneration)
	if err != nil {
		if subscription.activityRegistered {
			hub.unregisterActivitySubscription(request.AccountID)
			subscription.activityRegistered = false
		}
		return nil, err
	}
	subscription.replacePending(initial)
	state.subscribers[subscription] = struct{}{}
	reserved = false
	return subscription, nil
}

func (hub *Hub) initialFramesLocked(ctx context.Context, accountID int64, state *accountState, channels []Channel, lastEventID string, activitiesGeneration uint64) ([]queuedFrame, error) {
	if lastEventID == "" {
		return hub.snapshotSetLocked(ctx, accountID, state, channels, nil, "", activitiesGeneration)
	}
	if !dbValidateSSE(lastEventID) {
		return hub.snapshotSetLocked(ctx, accountID, state, channels, nil, GapProcessRestart, activitiesGeneration)
	}
	if containsChannel(channels, ChannelActivities) && state.activitiesGeneration != activitiesGeneration {
		return hub.snapshotSetLocked(ctx, accountID, state, channels, &lastEventID, GapRingEvicted, activitiesGeneration)
	}
	for index := range state.ring {
		if state.ring[index].frame.ID != lastEventID {
			continue
		}
		frames := make([]queuedFrame, 0, len(state.ring)-index-1)
		var currentEpoch *string
		if containsChannel(channels, ChannelRPS) {
			var epochErr error
			currentEpoch, epochErr = hub.currentEpoch(ctx, accountID)
			if epochErr != nil {
				return nil, epochErr
			}
			if (!state.rpsEpochKnown && currentEpoch != nil) ||
				(state.rpsEpochKnown && !samePointer(state.rpsEpoch, currentEpoch)) {
				return hub.snapshotSetLocked(ctx, accountID, state, channels, &lastEventID, GapRingEvicted, activitiesGeneration)
			}
			cursor := state.ring[index].frame
			if cursor.Channel == ChannelRPS && cursor.Type != TypeGap && !samePointer(cursor.IdentityEpoch, currentEpoch) {
				return hub.snapshotSetLocked(ctx, accountID, state, channels, &lastEventID, GapRingEvicted, activitiesGeneration)
			}
		}
		for _, entry := range state.ring[index+1:] {
			for _, channel := range channels {
				if entry.frame.Channel == channel {
					if channel == ChannelRPS && entry.frame.Type != TypeGap && !samePointer(entry.frame.IdentityEpoch, currentEpoch) {
						return hub.snapshotSetLocked(ctx, accountID, state, channels, &lastEventID, GapRingEvicted, activitiesGeneration)
					}
					frames = append(frames, queuedFrame{frame: entry.frame.clone()})
					break
				}
			}
		}
		return frames, nil
	}
	reason := GapProcessRestart
	if discarded, ok := state.discarded[lastEventID]; ok {
		reason = discarded.reason
	}
	return hub.snapshotSetLocked(ctx, accountID, state, channels, &lastEventID, reason, activitiesGeneration)
}

func containsChannel(channels []Channel, wanted Channel) bool {
	for _, channel := range channels {
		if channel == wanted {
			return true
		}
	}
	return false
}

func (hub *Hub) currentEpoch(ctx context.Context, accountID int64) (*string, error) {
	if hub.config.epochs == nil {
		return nil, errors.New("identity epoch guard is required for RPS replay")
	}
	epoch, err := hub.config.epochs.CurrentIdentityEpoch(ctx, accountID)
	if err != nil {
		return nil, fmt.Errorf("guard RPS replay identity epoch: %w", err)
	}
	if !validPointer(epoch) {
		return nil, errors.New("identity epoch guard returned an invalid epoch")
	}
	return epoch, nil
}

func dbValidateSSE(value string) bool {
	return db.ValidateOpaqueID(value, "sse_")
}

func cloneStringPointer(value *string) *string {
	if value == nil {
		return nil
	}
	copyValue := *value
	return &copyValue
}

func (hub *Hub) snapshotSetLocked(ctx context.Context, accountID int64, state *accountState, channels []Channel, cursor *string, reason GapReason, activitiesGeneration uint64) ([]queuedFrame, error) {
	frames := make([]queuedFrame, 0, len(channels)*2)
	for _, channel := range channels {
		if reason != "" {
			gap, err := hub.gapFrame(channel, reason, cursor)
			if err != nil {
				return nil, err
			}
			frames = append(frames, queuedFrame{frame: gap})
		}
		snapshot, err := hub.snapshotFrame(ctx, accountID, channel)
		if err != nil {
			return nil, err
		}
		frames = append(frames, queuedFrame{frame: snapshot})
	}
	now := hub.config.now()
	for _, frame := range frames {
		hub.appendLocked(state, frame.frame, now)
		if frame.frame.Channel == ChannelActivities {
			state.activitiesGeneration = activitiesGeneration
		}
	}
	return frames, nil
}

func (subscription *Subscription) replacePending(frames []queuedFrame) {
	subscription.pendingMu.Lock()
	for index := range subscription.pending {
		subscription.pending[index] = queuedFrame{}
	}
	subscription.pending = append(subscription.pending[:0], frames...)
	subscription.pendingMu.Unlock()
}

func (subscription *Subscription) popPending() (queuedFrame, bool) {
	subscription.pendingMu.Lock()
	defer subscription.pendingMu.Unlock()
	if len(subscription.pending) == 0 {
		return queuedFrame{}, false
	}
	frame := subscription.pending[0]
	subscription.pending[0] = queuedFrame{}
	subscription.pending = subscription.pending[1:]
	return frame, true
}

func (subscription *Subscription) Next(ctx context.Context) (Frame, error) {
	if subscription == nil || ctx == nil || subscription.closed.Load() {
		return Frame{}, ErrClosed
	}
	for {
		queued, ok := subscription.popPending()
		if !ok {
			select {
			case <-ctx.Done():
				return Frame{}, ctx.Err()
			case queued, ok = <-subscription.queue:
				if !ok {
					return Frame{}, ErrClosed
				}
			}
		}
		subscription.deliveryMu.Lock()
		current := subscription.sequence.Load()
		if queued.sequence != current || subscription.closed.Load() {
			subscription.deliveryMu.Unlock()
			continue
		}
		frame := queued.frame.clone()
		closeAfter := queued.closeAfter
		subscription.deliveryMu.Unlock()
		if closeAfter {
			_ = subscription.Close()
		}
		return frame, nil
	}
}

func (subscription *Subscription) closeLocked(state *accountState) {
	if !subscription.closed.CompareAndSwap(false, true) {
		return
	}
	subscription.deliveryMu.Lock()
	subscription.sequence.Add(1)
	subscription.deliveryMu.Unlock()
	delete(state.subscribers, subscription)
	if subscription.activityRegistered {
		subscription.hub.unregisterActivitySubscription(subscription.accountID)
		subscription.activityRegistered = false
	}
	// Release capacity before closing the queue. A reader can observe a closed
	// channel immediately, so publishing the close first would let Next return
	// ErrClosed while a replacement subscription still sees stale capacity.
	subscription.hub.releaseConnection()
	close(subscription.queue)
}

// discardLocked is the identity-discarding variant of closeLocked. The caller
// holds account.mu; queued and pending frames are removed before the
// subscription is closed so no stale identity remains deliverable.
func (subscription *Subscription) discardLocked(state *accountState) {
	if !subscription.closed.CompareAndSwap(false, true) {
		return
	}
	subscription.deliveryMu.Lock()
	subscription.sequence.Add(1)
	subscription.replacePending(nil)
	for {
		select {
		case <-subscription.queue:
		default:
			goto drained
		}
	}
drained:
	delete(state.subscribers, subscription)
	if subscription.activityRegistered {
		subscription.hub.unregisterActivitySubscription(subscription.accountID)
		subscription.activityRegistered = false
	}
	subscription.hub.releaseConnection()
	close(subscription.queue)
	subscription.deliveryMu.Unlock()
}

func (subscription *Subscription) Close() error {
	if subscription == nil || subscription.closed.Load() {
		return nil
	}
	if subscription.account == nil || subscription.hub == nil {
		return errors.New("invalid account event subscription")
	}
	subscription.account.mu.Lock()
	subscription.closeLocked(subscription.account)
	subscription.account.mu.Unlock()
	return nil
}

func (subscription *Subscription) String() string {
	if subscription == nil {
		return "accountstream.Subscription<nil>"
	}
	return fmt.Sprintf("accountstream.Subscription<account=%d>", subscription.accountID)
}
