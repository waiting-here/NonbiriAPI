package debug

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

type queuedEvent struct {
	eventID    string
	closeAfter bool
	sequence   uint64
}

type pinnedEvent struct {
	record *eventRecord
	token  uint64
}

type Subscription struct {
	hub       *Hub
	session   *session
	userID    int64
	sessionID string
	queue     chan queuedEvent

	pendingMu sync.Mutex
	pending   []queuedEvent

	deliveryMu sync.Mutex
	sequence   atomic.Uint64
	closedFlag atomic.Bool
	closed     bool // guarded by hub.mu
	pins       map[uint64]*eventRecord
	nextPin    uint64
}

func (hub *Hub) Subscribe(ctx context.Context, userID int64, identityBinding, lastEventID string) (*Subscription, error) {
	if hub == nil || ctx == nil || userID <= 0 || identityBinding == "" {
		return nil, ErrInvalid
	}
	hub.mu.Lock()
	defer hub.mu.Unlock()
	if hub.closed {
		return nil, ErrClosed
	}
	current := hub.activeByUser[userID]
	if current == nil || current.ended {
		return nil, ErrNoActiveSession
	}
	if hub.expireIfDueLocked(current, hub.config.now()) {
		return nil, ErrNoActiveSession
	}
	if current.identityBinding != identityBinding {
		return nil, ErrConflict
	}
	if len(current.subscribers) >= MaxSubscribers {
		return nil, ErrCapacity
	}
	now := hub.config.now()
	hub.cleanupRingLocked(current, now)
	subscription := &Subscription{
		hub: hub, session: current, userID: userID, sessionID: current.id,
		queue: make(chan queuedEvent, SubscriberQueueSize), pins: make(map[uint64]*eventRecord),
	}
	current.subscribers[subscription] = struct{}{}
	hub.touchLocked(current, now)
	initial, err := hub.initialEventsLocked(current, lastEventID)
	if err != nil {
		delete(current.subscribers, subscription)
		return nil, err
	}
	subscription.replacePendingLocked(initial)
	return subscription, nil
}

func (hub *Hub) initialEventsLocked(current *session, cursor string) ([]queuedEvent, error) {
	if cursor == "" {
		snapshot, err := hub.newEventLocked(current, EventSnapshot, hub.snapshotDataLocked(current))
		if err != nil {
			return nil, err
		}
		if err := hub.appendEventLocked(current, snapshot); err != nil {
			return nil, err
		}
		return []queuedEvent{{eventID: snapshot.event.EventID}}, nil
	}
	reason := GapReason("")
	if !validOID(cursor, "dbe_") {
		reason = GapCursorInvalid
	} else {
		for index, record := range current.events {
			if record.event.EventID != cursor {
				continue
			}
			remaining := current.events[index+1:]
			if len(remaining) > SubscriberQueueSize {
				reason = GapSlowConsumer
				break
			}
			frames := make([]queuedEvent, 0, len(remaining))
			for _, item := range remaining {
				frames = append(frames, queuedEvent{eventID: item.event.EventID})
			}
			return frames, nil
		}
		if reason == "" {
			if discarded, exists := current.discarded[cursor]; exists {
				reason = discarded.reason
			} else if known, exists := hub.knownEvents[cursor]; exists {
				if known.userID != current.userID || known.sessionID != current.id ||
					known.generation != fmt.Sprintf("%d", current.generation) {
					reason = GapCursorInvalid
				} else if known.reason != "" {
					reason = known.reason
				} else {
					reason = GapCursorInvalid
				}
			} else if hub.config.eventIDIsCurrent(cursor) {
				reason = GapCursorInvalid
			} else {
				reason = GapProcessRestart
			}
		}
	}
	gap, snapshot, err := hub.appendRecoveryLocked(current, reason)
	if err != nil {
		return nil, err
	}
	return []queuedEvent{{eventID: gap.event.EventID}, {eventID: snapshot.event.EventID}}, nil
}

func (subscription *Subscription) replacePendingLocked(events []queuedEvent) {
	sequence := subscription.sequence.Load()
	for index := range events {
		events[index].sequence = sequence
	}
	subscription.pendingMu.Lock()
	subscription.pending = append(subscription.pending[:0], events...)
	subscription.pendingMu.Unlock()
}

func (subscription *Subscription) popPending() (queuedEvent, bool) {
	subscription.pendingMu.Lock()
	defer subscription.pendingMu.Unlock()
	if len(subscription.pending) == 0 {
		return queuedEvent{}, false
	}
	item := subscription.pending[0]
	subscription.pending[0] = queuedEvent{}
	subscription.pending = subscription.pending[1:]
	return item, true
}

func (subscription *Subscription) tryEnqueueLocked(event queuedEvent) bool {
	if subscription == nil || subscription.closed || subscription.closedFlag.Load() || event.eventID == "" {
		return false
	}
	event.sequence = subscription.sequence.Load()
	select {
	case subscription.queue <- event:
		return true
	default:
		return false
	}
}

func (subscription *Subscription) enqueueLocked(event queuedEvent) bool {
	return subscription.tryEnqueueLocked(event)
}

func (subscription *Subscription) drainLocked() {
	if subscription == nil {
		return
	}
	subscription.sequence.Add(1)
	subscription.replacePendingLocked(nil)
	for {
		select {
		case <-subscription.queue:
		default:
			subscription.deliveryMu.Lock()
			subscription.releaseAllPinsLocked()
			subscription.deliveryMu.Unlock()
			return
		}
	}
}

func (subscription *Subscription) closeLocked() {
	if subscription == nil || subscription.closed {
		return
	}
	subscription.closed = true
	subscription.closedFlag.Store(true)
	subscription.sequence.Add(1)
	subscription.replacePendingLocked(nil)
	for {
		select {
		case <-subscription.queue:
		default:
			subscription.deliveryMu.Lock()
			subscription.releaseAllPinsLocked()
			subscription.deliveryMu.Unlock()
			delete(subscription.session.subscribers, subscription)
			close(subscription.queue)
			subscription.hub.maybeReleaseEndedSessionLocked(subscription.session)
			return
		}
	}
}

func (subscription *Subscription) releaseAllPinsLocked() {
	for token, record := range subscription.pins {
		delete(subscription.pins, token)
		subscription.hub.releaseEventPinLocked(subscription.session, record)
	}
}

func (subscription *Subscription) Close() error {
	if subscription == nil || subscription.hub == nil {
		return nil
	}
	subscription.hub.mu.Lock()
	subscription.closeLocked()
	subscription.hub.mu.Unlock()
	return nil
}

func (subscription *Subscription) pinQueued(event queuedEvent) (pinnedEvent, bool) {
	if subscription == nil || subscription.hub == nil {
		return pinnedEvent{}, false
	}
	hub := subscription.hub
	hub.mu.Lock()
	defer hub.mu.Unlock()
	if subscription.closed || subscription.closedFlag.Load() ||
		event.sequence != subscription.sequence.Load() {
		return pinnedEvent{}, false
	}
	record := subscription.session.eventIndex[event.eventID]
	if record == nil {
		hub.recoverSlowLocked(subscription.session, subscription)
		return pinnedEvent{}, false
	}
	if len(subscription.pins) >= SubscriberQueueSize+1 || subscription.nextPin == ^uint64(0) {
		hub.recoverSlowLocked(subscription.session, subscription)
		return pinnedEvent{}, false
	}
	subscription.nextPin++
	record.pinCount++
	subscription.pins[subscription.nextPin] = record
	return pinnedEvent{record: record, token: subscription.nextPin}, true
}

func (subscription *Subscription) releasePin(pin pinnedEvent) {
	if subscription == nil || subscription.hub == nil || pin.record == nil || pin.token == 0 {
		return
	}
	hub := subscription.hub
	hub.mu.Lock()
	if subscription.pins[pin.token] == pin.record {
		delete(subscription.pins, pin.token)
		hub.releaseEventPinLocked(subscription.session, pin.record)
	}
	hub.maybeReleaseEndedSessionLocked(subscription.session)
	hub.mu.Unlock()
}

func (subscription *Subscription) Next(ctx context.Context) (EventEnvelope, error) {
	if subscription == nil || subscription.hub == nil || ctx == nil {
		return EventEnvelope{}, ErrClosed
	}
	for {
		if subscription.closedFlag.Load() {
			return EventEnvelope{}, ErrClosed
		}
		queued, ok := subscription.popPending()
		if !ok {
			select {
			case <-ctx.Done():
				return EventEnvelope{}, ctx.Err()
			case queued, ok = <-subscription.queue:
				if !ok {
					return EventEnvelope{}, ErrClosed
				}
			}
		}
		pin, resolved := subscription.pinQueued(queued)
		if !resolved {
			continue
		}
		subscription.deliveryMu.Lock()
		if queued.sequence != subscription.sequence.Load() || subscription.closedFlag.Load() {
			subscription.deliveryMu.Unlock()
			subscription.releasePin(pin)
			continue
		}
		event := pin.record.event.clone()
		closeAfter := queued.closeAfter
		subscription.deliveryMu.Unlock()
		subscription.releasePin(pin)
		if closeAfter {
			_ = subscription.Close()
		}
		return event, nil
	}
}

func setWriteDeadline(controller *http.ResponseController, timeout time.Duration) error {
	err := controller.SetWriteDeadline(time.Now().Add(timeout))
	if err != nil && !errors.Is(err, http.ErrNotSupported) {
		return err
	}
	return nil
}

func writeSSEEvent(writer http.ResponseWriter, controller *http.ResponseController, timeout time.Duration, event EventEnvelope) error {
	if event.Version != 2 || !validOID(event.EventID, "dbe_") || !validOID(event.SessionID, "dbs_") ||
		!canonicalPositive(event.Generation) || event.OccurredAt < 0 || event.OccurredAt > maxUnixSecond ||
		validateEventData(event.Kind, event.Data) != nil {
		return ErrInvalid
	}
	data, err := json.Marshal(event)
	if err != nil || len(data) > MaxEventBytes {
		return ErrInvalid
	}
	if err := setWriteDeadline(controller, timeout); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(writer, "id: %s\nevent: %s\ndata: %s\n\n", event.EventID, event.Kind, data); err != nil {
		return err
	}
	return controller.Flush()
}

func writeHeartbeat(writer http.ResponseWriter, controller *http.ResponseController, timeout time.Duration) error {
	if err := setWriteDeadline(controller, timeout); err != nil {
		return err
	}
	if _, err := writer.Write([]byte(": heartbeat\n\n")); err != nil {
		return err
	}
	return controller.Flush()
}

func (subscription *Subscription) writeQueued(writer http.ResponseWriter, controller *http.ResponseController, queued queuedEvent) (bool, bool, error) {
	pin, resolved := subscription.pinQueued(queued)
	if !resolved {
		return false, false, nil
	}
	subscription.deliveryMu.Lock()
	if queued.sequence != subscription.sequence.Load() || subscription.closedFlag.Load() {
		subscription.deliveryMu.Unlock()
		subscription.releasePin(pin)
		return false, false, nil
	}
	err := writeSSEEvent(writer, controller, subscription.hub.config.writeTimeout, pin.record.event)
	subscription.deliveryMu.Unlock()
	subscription.releasePin(pin)
	if err != nil {
		return true, false, err
	}
	return true, queued.closeAfter, nil
}

func (subscription *Subscription) recheckIdentity(ctx context.Context) {
	if subscription == nil || subscription.hub == nil {
		return
	}
	hub := subscription.hub
	hub.mu.Lock()
	if hub.closed || subscription.closed {
		hub.mu.Unlock()
		return
	}
	current := hub.activeByUser[subscription.userID]
	if current == nil || current.id != subscription.sessionID {
		hub.mu.Unlock()
		return
	}
	binding, verifier := current.identityBinding, hub.config.verifier
	hub.mu.Unlock()
	if verifier == nil {
		return
	}
	state, err := verifier.VerifyDebugIdentity(ctx, subscription.userID, binding)
	if err != nil || state == IdentityActive || state == IdentityUncertain {
		return
	}
	hub.mu.Lock()
	defer hub.mu.Unlock()
	current = hub.activeByUser[subscription.userID]
	if current == nil || current.id != subscription.sessionID {
		return
	}
	switch state {
	case IdentityRevoked:
		hub.endSessionLocked(current, EndAuthRevoked)
	case IdentityBanned:
		hub.endSessionLocked(current, EndAccountBanned)
	case IdentityDeleted:
		hub.endSessionLocked(current, EndAccountDeleted)
	}
}

// Stream writes the dedicated v2 SSE stream. Observer disconnect only closes
// this subscription; it never stops the Debug session or any captured request.
func (subscription *Subscription) Stream(ctx context.Context, writer http.ResponseWriter) error {
	if subscription == nil || subscription.hub == nil || ctx == nil || writer == nil {
		return ErrInvalid
	}
	defer subscription.Close()
	writer.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("X-Accel-Buffering", "no")
	writer.Header().Set("X-Nonbiri-Event-Version", "2")
	controller := http.NewResponseController(writer)
	heartbeat := time.NewTicker(subscription.hub.config.heartbeat)
	defer heartbeat.Stop()

	for {
		if queued, ok := subscription.popPending(); ok {
			written, closed, err := subscription.writeQueued(writer, controller, queued)
			if err != nil {
				return err
			}
			if !written {
				continue
			}
			if closed {
				return nil
			}
			continue
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-heartbeat.C:
			subscription.recheckIdentity(ctx)
			if subscription.closedFlag.Load() {
				return nil
			}
			if err := writeHeartbeat(writer, controller, subscription.hub.config.writeTimeout); err != nil {
				return err
			}
		case queued, ok := <-subscription.queue:
			if !ok {
				return nil
			}
			written, closed, err := subscription.writeQueued(writer, controller, queued)
			if err != nil {
				return err
			}
			if !written {
				continue
			}
			if closed {
				return nil
			}
		}
	}
}
