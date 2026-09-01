package accountstream

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

const ringAge = 5 * time.Minute

type hubConfig struct {
	now           func() time.Time
	newID         func(string) (string, error)
	maxGlobal     int64
	maxPerAccount int
	queueSize     int
	maxRingEvents int
	maxRingBytes  int
	ringAge       time.Duration
	heartbeat     time.Duration
	writeTimeout  time.Duration
	snapshots     SnapshotAdapter
	epochs        IdentityEpochGuard
}

func defaultConfig(snapshots SnapshotAdapter, epochs IdentityEpochGuard) hubConfig {
	return hubConfig{
		now:           time.Now,
		newID:         db.GenerateOpaqueID,
		maxGlobal:     MaxGlobalConnections,
		maxPerAccount: MaxConnectionsPerAccount,
		queueSize:     SubscriberQueueSize,
		maxRingEvents: MaxRingEvents,
		maxRingBytes:  MaxRingBytes,
		ringAge:       ringAge,
		heartbeat:     15 * time.Second,
		writeTimeout:  15 * time.Second,
		snapshots:     snapshots,
		epochs:        epochs,
	}
}

type ringEntry struct {
	frame     Frame
	bytes     int
	createdAt time.Time
}

type discardedEntry struct {
	reason    GapReason
	discarded time.Time
}

type queuedFrame struct {
	frame      Frame
	closeAfter bool
	sequence   uint64
}

type accountState struct {
	mu                   sync.Mutex
	ring                 []ringEntry
	ringBytes            int
	discarded            map[string]discardedEntry
	subscribers          map[*Subscription]struct{}
	activitiesGeneration uint64
	rpsEpochKnown        bool
	rpsEpoch             *string
}

type Hub struct {
	mu                    sync.Mutex
	accounts              map[int64]*accountState
	forgotten             map[int64]struct{}
	activitySubscriptions map[int64]int
	activitiesBarrier     sync.RWMutex
	activitiesGeneration  atomic.Uint64
	connections           atomic.Int64
	closed                atomic.Bool
	config                hubConfig
}

func New(snapshots SnapshotAdapter, epochs IdentityEpochGuard) (*Hub, error) {
	return newHub(defaultConfig(snapshots, epochs))
}

func newHub(config hubConfig) (*Hub, error) {
	if config.now == nil || config.newID == nil || config.snapshots == nil || config.maxGlobal <= 0 ||
		config.maxPerAccount <= 0 || config.queueSize < 4 || config.maxRingEvents < 4 ||
		config.maxRingBytes <= 0 || config.ringAge <= 0 || config.heartbeat <= 0 || config.writeTimeout <= 0 {
		return nil, errors.New("invalid account event hub configuration")
	}
	hub := &Hub{
		accounts:              make(map[int64]*accountState),
		forgotten:             make(map[int64]struct{}),
		activitySubscriptions: make(map[int64]int),
		config:                config,
	}
	hub.activitiesGeneration.Store(1)
	return hub, nil
}

func (hub *Hub) account(accountID int64) (*accountState, error) {
	if hub == nil || accountID <= 0 || hub.closed.Load() {
		return nil, ErrClosed
	}
	hub.mu.Lock()
	defer hub.mu.Unlock()
	if hub.closed.Load() {
		return nil, ErrClosed
	}
	if _, forgotten := hub.forgotten[accountID]; forgotten {
		return nil, ErrClosed
	}
	state := hub.accounts[accountID]
	if state == nil {
		state = &accountState{
			discarded:   make(map[string]discardedEntry),
			subscribers: make(map[*Subscription]struct{}),
		}
		hub.accounts[accountID] = state
	}
	return state, nil
}

func (hub *Hub) accountForgotten(accountID int64) bool {
	if hub == nil || accountID <= 0 {
		return true
	}
	hub.mu.Lock()
	defer hub.mu.Unlock()
	_, forgotten := hub.forgotten[accountID]
	return forgotten
}

func (hub *Hub) reserveConnection() bool {
	for {
		current := hub.connections.Load()
		if current >= hub.config.maxGlobal {
			return false
		}
		if hub.connections.CompareAndSwap(current, current+1) {
			return true
		}
	}
}

func (hub *Hub) releaseConnection() {
	hub.connections.Add(-1)
}

func (hub *Hub) registerActivitySubscription(accountID int64) (uint64, error) {
	if hub == nil || accountID <= 0 || hub.closed.Load() {
		return 0, ErrClosed
	}
	hub.mu.Lock()
	defer hub.mu.Unlock()
	if hub.closed.Load() {
		return 0, ErrClosed
	}
	hub.activitySubscriptions[accountID]++
	return hub.activitiesGeneration.Load(), nil
}

func (hub *Hub) unregisterActivitySubscription(accountID int64) {
	if hub == nil || accountID <= 0 {
		return
	}
	hub.mu.Lock()
	defer hub.mu.Unlock()
	remaining := hub.activitySubscriptions[accountID] - 1
	if remaining <= 0 {
		delete(hub.activitySubscriptions, accountID)
		return
	}
	hub.activitySubscriptions[accountID] = remaining
}

// PrepareActivitiesPublish captures the generation that a caller must bind to
// every subsequently rebuilt projection. A global change advances the
// generation in O(1) and returns only accounts with a current activities
// subscription; disconnected accounts recover through gap+snapshot on their
// next subscription.
func (hub *Hub) PrepareActivitiesPublish(global bool) (ActivitiesPublishPlan, error) {
	if hub == nil || hub.closed.Load() {
		return ActivitiesPublishPlan{}, ErrClosed
	}
	if global {
		hub.activitiesBarrier.Lock()
		defer hub.activitiesBarrier.Unlock()
	}
	hub.mu.Lock()
	defer hub.mu.Unlock()
	if hub.closed.Load() {
		return ActivitiesPublishPlan{}, ErrClosed
	}
	generation := hub.activitiesGeneration.Load()
	if global {
		generation++
		hub.activitiesGeneration.Store(generation)
	}
	plan := ActivitiesPublishPlan{Generation: generation}
	if global {
		plan.ActiveAccountIDs = make([]int64, 0, len(hub.activitySubscriptions))
		for accountID := range hub.activitySubscriptions {
			plan.ActiveAccountIDs = append(plan.ActiveAccountIDs, accountID)
		}
		sort.Slice(plan.ActiveAccountIDs, func(i, j int) bool {
			return plan.ActiveAccountIDs[i] < plan.ActiveAccountIDs[j]
		})
	}
	return plan, nil
}

func canonicalUnsigned(value string) bool {
	if value == "" || (len(value) > 1 && value[0] == '0') {
		return false
	}
	for i := range value {
		if value[i] < '0' || value[i] > '9' {
			return false
		}
	}
	return true
}

func validPointer(value *string) bool {
	return value == nil || canonicalUnsigned(*value)
}

func samePointer(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func (hub *Hub) validateEvent(event PublishedEvent) error {
	if !event.Channel.valid() || (event.Type != TypeSnapshot && event.Type != TypeDelta) ||
		!validPointer(event.Revision) || !validPointer(event.IdentityEpoch) ||
		len(event.Data) == 0 || !json.Valid(event.Data) {
		return ErrInvalidEvent
	}
	if event.Channel == ChannelActivities && event.IdentityEpoch != nil {
		return ErrInvalidEvent
	}
	if event.Channel == ChannelActivities && event.Revision == nil {
		return ErrInvalidEvent
	}
	if event.Channel == ChannelRPS && event.IdentityEpoch != nil && event.Revision == nil {
		return ErrInvalidEvent
	}
	limit := MaxDeltaBytes
	if event.Type == TypeSnapshot {
		limit = MaxSnapshotBytes
	}
	if len(event.Data) > limit {
		return ErrInvalidEvent
	}
	return nil
}

func wireEnvelopeBytes(frame Frame) int {
	encoded, err := json.Marshal(frame)
	if err != nil {
		return MaxSnapshotBytes + 1
	}
	return len(encoded)
}

func (hub *Hub) epochMatches(ctx context.Context, accountID int64, expected *string) (bool, error) {
	if hub.config.epochs == nil {
		return false, errors.New("identity epoch guard is required for RPS events")
	}
	current, err := hub.config.epochs.CurrentIdentityEpoch(ctx, accountID)
	if err != nil {
		return false, err
	}
	if !validPointer(current) {
		return false, errors.New("identity epoch guard returned an invalid epoch")
	}
	return samePointer(current, expected), nil
}

// PublishCommitted publishes a complete event only after the caller's domain
// transaction commits. RPS identity epoch is checked twice so a publish that
// races account deletion/purge cannot reintroduce an old identity frame.
func (hub *Hub) PublishCommitted(ctx context.Context, accountID int64, event PublishedEvent) (Frame, error) {
	if event.Channel == ChannelActivities {
		plan, err := hub.PrepareActivitiesPublish(false)
		if err != nil {
			return Frame{}, err
		}
		return hub.PublishActivitiesCommitted(ctx, accountID, plan, event)
	}
	return hub.publishCommitted(ctx, accountID, 0, event)
}

// PublishActivitiesCommitted rejects a projection if any global activity
// mutation committed after its plan was captured. This prevents an older
// projection from making an account appear current after a global invalidation.
func (hub *Hub) PublishActivitiesCommitted(
	ctx context.Context,
	accountID int64,
	plan ActivitiesPublishPlan,
	event PublishedEvent,
) (Frame, error) {
	if event.Channel != ChannelActivities || plan.Generation == 0 {
		return Frame{}, ErrInvalidEvent
	}
	if hub == nil || hub.activitiesGeneration.Load() != plan.Generation {
		return Frame{}, ErrStaleActivitiesGeneration
	}
	return hub.publishCommitted(ctx, accountID, plan.Generation, event)
}

func (hub *Hub) publishCommitted(ctx context.Context, accountID int64, activitiesGeneration uint64, event PublishedEvent) (Frame, error) {
	if ctx == nil || accountID <= 0 {
		return Frame{}, ErrInvalidEvent
	}
	if err := hub.validateEvent(event); err != nil {
		return Frame{}, err
	}
	if event.Channel == ChannelActivities {
		hub.activitiesBarrier.RLock()
		defer hub.activitiesBarrier.RUnlock()
		if hub.activitiesGeneration.Load() != activitiesGeneration {
			return Frame{}, ErrStaleActivitiesGeneration
		}
	}
	if event.Channel == ChannelRPS {
		matches, err := hub.epochMatches(ctx, accountID, event.IdentityEpoch)
		if err != nil {
			return Frame{}, fmt.Errorf("guard account identity epoch: %w", err)
		}
		if !matches {
			return Frame{}, ErrStaleIdentityEpoch
		}
	}
	state, err := hub.account(accountID)
	if err != nil {
		return Frame{}, err
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if hub.closed.Load() || hub.accountForgotten(accountID) {
		return Frame{}, ErrClosed
	}
	if event.Channel == ChannelActivities && hub.activitiesGeneration.Load() != activitiesGeneration {
		return Frame{}, ErrStaleActivitiesGeneration
	}
	if event.Channel == ChannelRPS {
		matches, guardErr := hub.epochMatches(ctx, accountID, event.IdentityEpoch)
		if guardErr != nil {
			return Frame{}, fmt.Errorf("recheck account identity epoch: %w", guardErr)
		}
		if !matches {
			return Frame{}, ErrStaleIdentityEpoch
		}
	}
	frame, err := hub.newFrame(event.Channel, event.Type, event.Revision, event.IdentityEpoch, event.Data)
	if err != nil {
		return Frame{}, err
	}
	hub.appendLocked(state, frame, hub.config.now())
	if event.Channel == ChannelActivities {
		state.activitiesGeneration = activitiesGeneration
	}
	for subscription := range state.subscribers {
		if !subscription.topics[event.Channel] || subscription.closing || subscription.closed.Load() {
			continue
		}
		select {
		case subscription.queue <- queuedFrame{frame: frame.clone(), sequence: subscription.sequence.Load()}:
		default:
			hub.slowConsumerLocked(ctx, accountID, state, subscription, frame)
		}
	}
	return frame.clone(), nil
}

// ForgetAccounts permanently closes and discards every process-local frame
// for deleted account identities. Unlike PurgeAccounts it deliberately does
// not request a replacement snapshot: a deleted account is no longer a valid
// snapshot subject. The tombstone also rejects delayed post-commit publishers
// that obtained their account state before deletion completed.
func (hub *Hub) ForgetAccounts(ctx context.Context, accountIDs []int64) error {
	if hub == nil || ctx == nil || hub.closed.Load() {
		return ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	unique := make(map[int64]struct{}, len(accountIDs))
	for _, accountID := range accountIDs {
		if accountID <= 0 {
			return ErrInvalidEvent
		}
		unique[accountID] = struct{}{}
	}
	ids := make([]int64, 0, len(unique))
	for accountID := range unique {
		ids = append(ids, accountID)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	type forgetTarget struct {
		accountID int64
		state     *accountState
	}
	targets := make([]forgetTarget, 0, len(ids))
	hub.mu.Lock()
	if hub.closed.Load() {
		hub.mu.Unlock()
		return ErrClosed
	}
	for _, accountID := range ids {
		hub.forgotten[accountID] = struct{}{}
		if state := hub.accounts[accountID]; state != nil {
			targets = append(targets, forgetTarget{accountID: accountID, state: state})
		}
	}
	hub.mu.Unlock()

	for _, target := range targets {
		target.state.mu.Lock()
		for subscription := range target.state.subscribers {
			subscription.discardLocked(target.state)
		}
		for index := range target.state.ring {
			target.state.ring[index] = ringEntry{}
		}
		target.state.ring = nil
		target.state.ringBytes = 0
		clear(target.state.discarded)
		target.state.activitiesGeneration = 0
		target.state.rpsEpochKnown = false
		target.state.rpsEpoch = nil
		target.state.mu.Unlock()
	}
	return nil
}

func (hub *Hub) newFrame(channel Channel, eventType EventType, revision, epoch *string, data json.RawMessage) (Frame, error) {
	id, err := hub.config.newID("sse_")
	if err != nil {
		return Frame{}, fmt.Errorf("generate account event id: %w", err)
	}
	if !db.ValidateOpaqueID(id, "sse_") {
		return Frame{}, errors.New("account event id generator returned a non-canonical id")
	}
	now := hub.config.now().Unix()
	if now < 0 || now > 253402300799 {
		return Frame{}, errors.New("account event clock is outside UTC range")
	}
	frame := Frame{
		ID: id, Version: 1, Channel: channel, Type: eventType,
		Revision: revision, IdentityEpoch: epoch, OccurredAt: now,
		Data: append(json.RawMessage(nil), data...),
	}
	limit := MaxDeltaBytes
	if eventType == TypeSnapshot {
		limit = MaxSnapshotBytes
	}
	if wireEnvelopeBytes(frame) > limit {
		return Frame{}, ErrInvalidEvent
	}
	return frame.clone(), nil
}

func frameBytes(frame Frame) int {
	data, err := json.Marshal(frame)
	if err != nil {
		return MaxSnapshotBytes + 1
	}
	return len(data) + len(frame.ID) + len(frame.Type) + 21
}

func (hub *Hub) rememberDiscardedLocked(state *accountState, entry ringEntry, reason GapReason, now time.Time) {
	state.discarded[entry.frame.ID] = discardedEntry{reason: reason, discarded: now}
	for id, discarded := range state.discarded {
		if now.Sub(discarded.discarded) > hub.config.ringAge || len(state.discarded) > hub.config.maxRingEvents*2 {
			delete(state.discarded, id)
		}
	}
}

func (hub *Hub) removeOldestLocked(state *accountState, reason GapReason, now time.Time) {
	oldest := state.ring[0]
	state.ring = state.ring[1:]
	state.ringBytes -= oldest.bytes
	hub.rememberDiscardedLocked(state, oldest, reason, now)
}

func (hub *Hub) cleanupLocked(state *accountState, now time.Time) {
	for len(state.ring) > 0 && now.Sub(state.ring[0].createdAt) >= hub.config.ringAge {
		hub.removeOldestLocked(state, GapRingExpired, now)
	}
}

func (hub *Hub) appendLocked(state *accountState, frame Frame, now time.Time) {
	hub.cleanupLocked(state, now)
	entry := ringEntry{frame: frame.clone(), bytes: frameBytes(frame), createdAt: now}
	state.ring = append(state.ring, entry)
	state.ringBytes += entry.bytes
	if frame.Channel == ChannelRPS && frame.Type != TypeGap {
		state.rpsEpochKnown = true
		state.rpsEpoch = cloneStringPointer(frame.IdentityEpoch)
	}
	for len(state.ring) > hub.config.maxRingEvents || state.ringBytes > hub.config.maxRingBytes {
		hub.removeOldestLocked(state, GapRingEvicted, now)
	}
}

type gapPayload struct {
	Reason      GapReason `json:"reason"`
	LastEventID *string   `json:"last_event_id"`
}

func (hub *Hub) gapFrame(channel Channel, reason GapReason, lastEventID *string) (Frame, error) {
	payload, err := json.Marshal(gapPayload{Reason: reason, LastEventID: lastEventID})
	if err != nil {
		return Frame{}, err
	}
	return hub.newFrame(channel, TypeGap, nil, nil, payload)
}

func (hub *Hub) snapshotFrame(ctx context.Context, accountID int64, channel Channel) (Frame, error) {
	snapshot, err := hub.config.snapshots.Snapshot(ctx, accountID, channel)
	if err != nil {
		return Frame{}, fmt.Errorf("%w: %v", ErrSnapshot, err)
	}
	event := PublishedEvent{
		Channel: channel, Type: TypeSnapshot, Revision: snapshot.Revision,
		IdentityEpoch: snapshot.IdentityEpoch, Data: snapshot.Data,
	}
	if err := hub.validateEvent(event); err != nil {
		return Frame{}, fmt.Errorf("%w: invalid %s snapshot", ErrSnapshot, channel)
	}
	if channel == ChannelRPS {
		matches, guardErr := hub.epochMatches(ctx, accountID, snapshot.IdentityEpoch)
		if guardErr != nil {
			return Frame{}, fmt.Errorf("%w: guard %s snapshot: %v", ErrSnapshot, channel, guardErr)
		}
		if !matches {
			return Frame{}, fmt.Errorf("%w: stale %s snapshot identity epoch", ErrSnapshot, channel)
		}
	}
	return hub.newFrame(channel, TypeSnapshot, snapshot.Revision, snapshot.IdentityEpoch, snapshot.Data)
}

func (hub *Hub) slowConsumerLocked(ctx context.Context, accountID int64, state *accountState, subscription *Subscription, latest Frame) {
	if subscription.closing || subscription.closed.Load() {
		return
	}
	subscription.closing = true
	sequence := subscription.sequence.Add(1)
	subscription.replacePending(nil)
	for {
		select {
		case <-subscription.queue:
		default:
			goto drained
		}
	}
drained:
	// A published delta cannot be relabelled as a snapshot: even a complete
	// projection may already have been superseded by a later domain commit.
	// Fetch the current authoritative projection outside the account lock so a
	// slow subscriber never blocks this or another committed publisher. The
	// publish request may end as soon as this method returns, but the already
	// triggered recovery still has to emit gap+snapshot. Preserve context values
	// while giving that recovery its own fixed, bounded lifetime.
	finishContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), hub.config.writeTimeout)
	go func() {
		defer cancel()
		hub.finishSlowConsumer(finishContext, accountID, state, subscription, latest.Channel, latest.ID, sequence)
	}()
}

func (hub *Hub) finishSlowConsumer(
	ctx context.Context,
	accountID int64,
	state *accountState,
	subscription *Subscription,
	channel Channel,
	lastEventID string,
	sequence uint64,
) {
	snapshot, snapshotErr := hub.snapshotFrame(ctx, accountID, channel)
	last := lastEventID
	gap, gapErr := hub.gapFrame(channel, GapSlowConsumer, &last)

	state.mu.Lock()
	defer state.mu.Unlock()
	if subscription.closed.Load() || !subscription.closing || subscription.sequence.Load() != sequence {
		return
	}
	if _, subscribed := state.subscribers[subscription]; !subscribed {
		return
	}
	if hub.closed.Load() || snapshotErr != nil || gapErr != nil {
		subscription.closeLocked(state)
		return
	}
	subscription.queue <- queuedFrame{frame: gap, sequence: sequence}
	subscription.queue <- queuedFrame{frame: snapshot, closeAfter: true, sequence: sequence}
}

// PurgeAccounts synchronously removes every queued/replayable frame for the
// account set after an identity-changing transaction commits, then publishes
// a gap and complete authoritative snapshot for both closed channels. RPS
// publishers racing the purge are serialized by the account lock and recheck
// their epoch before insertion.
func (hub *Hub) PurgeAccounts(ctx context.Context, accountIDs []int64) error {
	if hub == nil || ctx == nil || hub.closed.Load() {
		return ErrClosed
	}
	unique := make(map[int64]struct{}, len(accountIDs))
	for _, accountID := range accountIDs {
		if accountID <= 0 {
			return ErrInvalidEvent
		}
		unique[accountID] = struct{}{}
	}
	ids := make([]int64, 0, len(unique))
	for accountID := range unique {
		ids = append(ids, accountID)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	type purgeTarget struct {
		accountID int64
		state     *accountState
		frames    []Frame
	}
	targets := make([]purgeTarget, 0, len(ids))
	for _, accountID := range ids {
		state, err := hub.account(accountID)
		if err != nil {
			return err
		}
		targets = append(targets, purgeTarget{accountID: accountID, state: state})
	}
	for index := range targets {
		targets[index].state.mu.Lock()
	}
	defer func() {
		for index := len(targets) - 1; index >= 0; index-- {
			targets[index].state.mu.Unlock()
		}
	}()
	if hub.closed.Load() {
		return ErrClosed
	}
	for _, target := range targets {
		if hub.accountForgotten(target.accountID) {
			return ErrClosed
		}
	}

	activitiesGeneration := hub.activitiesGeneration.Load()
	// Build every account's authoritative replacement before discarding any
	// old frame. A snapshot failure therefore leaves the whole account set
	// unchanged instead of exposing a partially purged participant set.
	for index := range targets {
		lastEventID := latestEventID(targets[index].state)
		frames, err := hub.purgeFramesLocked(ctx, targets[index].accountID, lastEventID)
		if err != nil {
			return err
		}
		targets[index].frames = frames
	}
	for index := range targets {
		hub.applyPurgeLocked(targets[index].state, targets[index].frames, activitiesGeneration)
	}
	return nil
}

func latestEventID(state *accountState) *string {
	if len(state.ring) > 0 {
		last := state.ring[len(state.ring)-1].frame.ID
		return &last
	}
	return nil
}

func (hub *Hub) purgeFramesLocked(ctx context.Context, accountID int64, lastEventID *string) ([]Frame, error) {
	frames := make([]Frame, 0, 4)
	for _, channel := range []Channel{ChannelActivities, ChannelRPS} {
		gap, err := hub.gapFrame(channel, GapRingEvicted, lastEventID)
		if err != nil {
			return nil, err
		}
		snapshot, err := hub.snapshotFrame(ctx, accountID, channel)
		if err != nil {
			return nil, err
		}
		frames = append(frames, gap, snapshot)
	}
	return frames, nil
}

func (hub *Hub) applyPurgeLocked(state *accountState, frames []Frame, activitiesGeneration uint64) {
	now := hub.config.now()
	for len(state.ring) > 0 {
		hub.removeOldestLocked(state, GapRingEvicted, now)
	}
	for subscription := range state.subscribers {
		if subscription.closed.Load() {
			continue
		}
		subscription.deliveryMu.Lock()
		subscription.sequence.Add(1)
		subscription.replacePending(nil)
		for {
			select {
			case <-subscription.queue:
			default:
				goto queueDrained
			}
		}
	queueDrained:
		if subscription.closing && subscription.closed.CompareAndSwap(false, true) {
			delete(state.subscribers, subscription)
			close(subscription.queue)
			subscription.hub.releaseConnection()
		}
		subscription.deliveryMu.Unlock()
	}
	for _, frame := range frames {
		hub.appendLocked(state, frame, now)
		if frame.Channel == ChannelActivities {
			state.activitiesGeneration = activitiesGeneration
		}
		for subscription := range state.subscribers {
			if !subscription.topics[frame.Channel] || subscription.closing || subscription.closed.Load() {
				continue
			}
			subscription.queue <- queuedFrame{frame: frame.clone(), sequence: subscription.sequence.Load()}
		}
	}
}

func (hub *Hub) Close() error {
	if hub == nil || !hub.closed.CompareAndSwap(false, true) {
		return nil
	}
	hub.mu.Lock()
	states := make([]*accountState, 0, len(hub.accounts))
	for _, state := range hub.accounts {
		states = append(states, state)
	}
	hub.mu.Unlock()
	for _, state := range states {
		state.mu.Lock()
		for subscription := range state.subscribers {
			subscription.closeLocked(state)
		}
		state.mu.Unlock()
	}
	return nil
}
