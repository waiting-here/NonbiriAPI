package debug

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

const (
	idleTTL          = 10 * time.Minute
	absoluteTTL      = time.Hour
	ringAge          = 5 * time.Minute
	heartbeatEvery   = 15 * time.Second
	writeTimeout     = 15 * time.Second
	sweepEvery       = time.Second
	knownEventLimit  = MaxGlobalSessions * MaxSessionEvents * 3
	discardedPerRing = MaxSessionEvents * 2
)

type IdentityState string

const (
	IdentityActive    IdentityState = "active"
	IdentityUncertain IdentityState = "uncertain"
	IdentityRevoked   IdentityState = "revoked"
	IdentityBanned    IdentityState = "banned"
	IdentityDeleted   IdentityState = "deleted"
)

// IdentityVerifier rechecks the browser session binding and account state.
// Capture calls it for every active Debug decision; SSE calls it at least once
// per heartbeat. An unavailable/uncertain answer never permits live dispatch.
type IdentityVerifier interface {
	VerifyDebugIdentity(context.Context, int64, string) (IdentityState, error)
}

type hubConfig struct {
	now              func() time.Time
	newOpaqueID      func(string) (string, error)
	newEventID       func() (string, error)
	eventIDIsCurrent func(string) bool
	verifier         IdentityVerifier
	ringAge          time.Duration
	heartbeat        time.Duration
	writeTimeout     time.Duration
	sweepInterval    time.Duration
	disableSweeper   bool
}

func defaultHubConfig(verifier IdentityVerifier) (hubConfig, error) {
	source, err := newSealedEventIDSource()
	if err != nil {
		return hubConfig{}, err
	}
	return hubConfig{
		now: time.Now, newOpaqueID: db.GenerateOpaqueID,
		newEventID: source.New, eventIDIsCurrent: source.IsCurrent,
		verifier: verifier, ringAge: ringAge, heartbeat: heartbeatEvery,
		writeTimeout: writeTimeout, sweepInterval: sweepEvery,
	}, nil
}

type sealedEventIDSource struct{ key [32]byte }

func newSealedEventIDSource() (*sealedEventIDSource, error) {
	source := &sealedEventIDSource{}
	if _, err := rand.Read(source.key[:]); err != nil {
		return nil, fmt.Errorf("initialize debug event identity: %w", err)
	}
	return source, nil
}

func (source *sealedEventIDSource) New() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:12]); err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, source.key[:])
	_, _ = mac.Write(raw[:12])
	copy(raw[12:], mac.Sum(nil)[:4])
	return "dbe_" + base64.RawURLEncoding.EncodeToString(raw[:]), nil
}

func (source *sealedEventIDSource) IsCurrent(value string) bool {
	if !validOID(value, "dbe_") {
		return false
	}
	raw, err := base64.RawURLEncoding.DecodeString(value[4:])
	if err != nil || len(raw) != 16 {
		return false
	}
	mac := hmac.New(sha256.New, source.key[:])
	_, _ = mac.Write(raw[:12])
	return hmac.Equal(raw[12:], mac.Sum(nil)[:4])
}

type eventRecord struct {
	event     EventEnvelope
	wireBytes int
	createdAt time.Time
	inRing    bool
	pinCount  int
}

type discardedEvent struct {
	reason GapReason
	at     time.Time
}

type knownEvent struct {
	userID     int64
	sessionID  string
	generation string
	reason     GapReason
}

type traceRecord struct {
	trace      DebugTrace
	wireBytes  int
	dispatched bool
	cancel     context.CancelCauseFunc
	ctx        context.Context
}

type session struct {
	userID          int64
	id              string
	identityBinding string
	generation      uint64
	revision        uint64
	mode            Mode
	createdAt       time.Time
	lastActivity    time.Time
	expiresAt       time.Time
	traces          map[string]*traceRecord
	traceOrder      []string
	traceBytes      int
	events          []*eventRecord
	eventIndex      map[string]*eventRecord
	eventBytes      int
	retainedEvents  map[string]*eventRecord
	retainedBytes   int
	discarded       map[string]discardedEvent
	discardedOrder  []string
	subscribers     map[*Subscription]struct{}
	inflight        int
	ended           bool
}

func (current *session) bytes() int {
	return current.traceBytes + current.eventBytes + current.retainedBytes
}

func (current *session) eventCount() int {
	return len(current.events) + len(current.retainedEvents)
}

type Hub struct {
	mu sync.Mutex

	activeByUser map[int64]*session
	sessions     map[*session]struct{}
	forgotten    map[int64]struct{}
	knownEvents  map[string]knownEvent
	knownOrder   []string
	generation   uint64
	closed       bool
	config       hubConfig
	stopSweep    chan struct{}
	sweepDone    chan struct{}
}

func NewHub(verifier IdentityVerifier) (*Hub, error) {
	config, err := defaultHubConfig(verifier)
	if err != nil {
		return nil, err
	}
	return newHub(config)
}

func newHub(config hubConfig) (*Hub, error) {
	if config.now == nil || config.newOpaqueID == nil || config.newEventID == nil ||
		config.eventIDIsCurrent == nil || config.ringAge <= 0 || config.heartbeat <= 0 ||
		config.writeTimeout <= 0 || (!config.disableSweeper && config.sweepInterval <= 0) {
		return nil, ErrInvalid
	}
	hub := &Hub{
		activeByUser: make(map[int64]*session), sessions: make(map[*session]struct{}),
		forgotten:   make(map[int64]struct{}),
		knownEvents: make(map[string]knownEvent),
		config:      config, stopSweep: make(chan struct{}), sweepDone: make(chan struct{}),
	}
	if config.disableSweeper {
		close(hub.sweepDone)
	} else {
		go hub.sweepLoop()
	}
	return hub, nil
}

func (hub *Hub) sweepLoop() {
	ticker := time.NewTicker(hub.config.sweepInterval)
	defer ticker.Stop()
	defer close(hub.sweepDone)
	for {
		select {
		case <-hub.stopSweep:
			return
		case <-ticker.C:
			hub.Sweep()
		}
	}
}

// Sweep applies time boundaries using a single sampled decision time. Tests
// can call it directly with a controlled clock; production also runs one
// bounded process-wide sweeper.
func (hub *Hub) Sweep() {
	if hub == nil {
		return
	}
	hub.mu.Lock()
	defer hub.mu.Unlock()
	if hub.closed {
		return
	}
	now := hub.config.now()
	users := make([]int64, 0, len(hub.activeByUser))
	for userID := range hub.activeByUser {
		users = append(users, userID)
	}
	sort.Slice(users, func(i, j int) bool { return users[i] < users[j] })
	for _, userID := range users {
		current := hub.activeByUser[userID]
		if current == nil || current.ended {
			continue
		}
		hub.cleanupRingLocked(current, now)
		switch {
		case !now.Before(current.expiresAt):
			hub.endSessionLocked(current, EndAbsoluteExpired)
		case current.inflight == 0 && !now.Before(current.lastActivity.Add(idleTTL)):
			hub.endSessionLocked(current, EndIdleExpired)
		}
	}
}

func (hub *Hub) createLocked(userID int64, binding string, now time.Time) (*session, error) {
	if userID <= 0 || binding == "" || !utf8.ValidString(binding) || len(binding) > 256 {
		return nil, ErrInvalid
	}
	if _, forgotten := hub.forgotten[userID]; forgotten {
		return nil, ErrNoActiveSession
	}
	if len(hub.activeByUser) >= MaxGlobalSessions {
		return nil, ErrCapacity
	}
	id, err := hub.config.newOpaqueID("dbs_")
	if err != nil {
		return nil, fmt.Errorf("generate debug session id: %w", err)
	}
	if !validOID(id, "dbs_") {
		return nil, errors.New("debug session id generator returned a non-canonical id")
	}
	hub.generation++
	if hub.generation == 0 {
		return nil, ErrCapacity
	}
	current := &session{
		userID: userID, id: id, identityBinding: binding, generation: hub.generation,
		revision: 1, mode: ModeDry, createdAt: now, lastActivity: now,
		expiresAt: now.Add(absoluteTTL), traces: make(map[string]*traceRecord),
		eventIndex: make(map[string]*eventRecord), retainedEvents: make(map[string]*eventRecord),
		discarded: make(map[string]discardedEvent), subscribers: make(map[*Subscription]struct{}),
	}
	hub.activeByUser[userID] = current
	hub.sessions[current] = struct{}{}
	return current, nil
}

// Start attaches to an existing active session without clearing it. Otherwise
// it creates the sole process-local session for the account in default Dry.
func (hub *Hub) Start(userID int64, identityBinding string) (DebugSessionMetadata, bool, error) {
	if hub == nil {
		return DebugSessionMetadata{}, false, ErrClosed
	}
	hub.mu.Lock()
	defer hub.mu.Unlock()
	if hub.closed {
		return DebugSessionMetadata{}, false, ErrClosed
	}
	if _, forgotten := hub.forgotten[userID]; forgotten {
		return DebugSessionMetadata{}, false, ErrNoActiveSession
	}
	now := hub.config.now()
	if existing := hub.activeByUser[userID]; existing != nil && !existing.ended {
		if hub.expireIfDueLocked(existing, now) {
			existing = nil
		}
	}
	if existing := hub.activeByUser[userID]; existing != nil && !existing.ended {
		if existing.identityBinding != identityBinding {
			return DebugSessionMetadata{}, false, ErrConflict
		}
		hub.touchLocked(existing, now)
		return hub.metadataLocked(existing), false, nil
	}
	current, err := hub.createLocked(userID, identityBinding, now)
	if err != nil {
		return DebugSessionMetadata{}, false, err
	}
	return hub.metadataLocked(current), true, nil
}

func (hub *Hub) expireIfDueLocked(current *session, now time.Time) bool {
	if current == nil || current.ended {
		return true
	}
	if !now.Before(current.expiresAt) {
		hub.endSessionLocked(current, EndAbsoluteExpired)
		return true
	}
	if current.inflight == 0 && !now.Before(current.lastActivity.Add(idleTTL)) {
		hub.endSessionLocked(current, EndIdleExpired)
		return true
	}
	return false
}

func (hub *Hub) Metadata(userID int64) (DebugSessionMetadata, error) {
	if hub == nil {
		return DebugSessionMetadata{}, ErrClosed
	}
	hub.mu.Lock()
	defer hub.mu.Unlock()
	if hub.closed {
		return DebugSessionMetadata{}, ErrClosed
	}
	current := hub.activeByUser[userID]
	if current == nil || current.ended {
		return DebugSessionMetadata{Active: false}, nil
	}
	now := hub.config.now()
	if hub.expireIfDueLocked(current, now) {
		return DebugSessionMetadata{Active: false}, nil
	}
	hub.touchLocked(current, now)
	return hub.metadataLocked(current), nil
}

func (hub *Hub) metadataLocked(current *session) DebugSessionMetadata {
	var last *string
	if len(current.events) > 0 {
		last = stringPointer(current.events[len(current.events)-1].event.EventID)
	}
	idleExpires := current.lastActivity.Add(idleTTL)
	if idleExpires.After(current.expiresAt) {
		idleExpires = current.expiresAt
	}
	return DebugSessionMetadata{
		Active: true, ID: current.id, Generation: strconv.FormatUint(current.generation, 10),
		Revision: strconv.FormatUint(current.revision, 10), Mode: current.mode,
		CreatedAt: current.createdAt.Unix(), ExpiresAt: current.expiresAt.Unix(),
		IdleExpiresAt: idleExpires.Unix(), InflightCount: current.inflight,
		ConnectedSubscribers: len(current.subscribers), LastEventID: last,
		Limits: fixedSessionLimits(),
	}
}

func (hub *Hub) touchLocked(current *session, now time.Time) {
	if now.After(current.lastActivity) {
		current.lastActivity = now
	}
}

func (hub *Hub) ChangeMode(userID int64, expectedRevision string, mode Mode, liveConfirmation bool) (DebugSessionMetadata, error) {
	if hub == nil {
		return DebugSessionMetadata{}, ErrClosed
	}
	revision, ok := parseRevision(expectedRevision)
	if !ok || !mode.valid() || (mode == ModeLive) != liveConfirmation {
		return DebugSessionMetadata{}, ErrInvalid
	}
	hub.mu.Lock()
	defer hub.mu.Unlock()
	if hub.closed {
		return DebugSessionMetadata{}, ErrClosed
	}
	current := hub.activeByUser[userID]
	if current == nil || current.ended {
		return DebugSessionMetadata{}, ErrNoActiveSession
	}
	if hub.expireIfDueLocked(current, hub.config.now()) {
		return DebugSessionMetadata{}, ErrNoActiveSession
	}
	if current.revision != revision {
		return DebugSessionMetadata{}, ErrConflict
	}
	if current.mode != mode {
		current.mode = mode
		current.revision++
	}
	hub.touchLocked(current, hub.config.now())
	return hub.metadataLocked(current), nil
}

func (hub *Hub) Stop(userID int64, expectedRevision string, confirmInflight bool) error {
	revision, ok := parseRevision(expectedRevision)
	if !ok {
		return ErrInvalid
	}
	if hub == nil {
		return ErrClosed
	}
	hub.mu.Lock()
	defer hub.mu.Unlock()
	if hub.closed {
		return ErrClosed
	}
	current := hub.activeByUser[userID]
	if current == nil || current.ended {
		return ErrNoActiveSession
	}
	if hub.expireIfDueLocked(current, hub.config.now()) {
		return ErrNoActiveSession
	}
	if current.revision != revision || (current.inflight > 0 && !confirmInflight) {
		return ErrConflict
	}
	hub.endSessionLocked(current, EndStopped)
	return nil
}

func (hub *Hub) Replace(userID int64, identityBinding, expectedRevision string, confirmInflight bool) (DebugSessionMetadata, error) {
	revision, ok := parseRevision(expectedRevision)
	if !ok || identityBinding == "" {
		return DebugSessionMetadata{}, ErrInvalid
	}
	if hub == nil {
		return DebugSessionMetadata{}, ErrClosed
	}
	hub.mu.Lock()
	defer hub.mu.Unlock()
	if hub.closed {
		return DebugSessionMetadata{}, ErrClosed
	}
	old := hub.activeByUser[userID]
	if old == nil || old.ended {
		return DebugSessionMetadata{}, ErrNoActiveSession
	}
	if hub.expireIfDueLocked(old, hub.config.now()) {
		return DebugSessionMetadata{}, ErrNoActiveSession
	}
	if old.revision != revision || old.identityBinding != identityBinding || (old.inflight > 0 && !confirmInflight) {
		return DebugSessionMetadata{}, ErrConflict
	}
	// Remove and replace while holding the same decision lock used by capture.
	// No request can observe an inactive ordinary-pass window.
	hub.endSessionLocked(old, EndReplaced)
	created, err := hub.createLocked(userID, identityBinding, hub.config.now())
	if err != nil {
		// Replacement reuses the old slot, so only an ID/clock invariant can
		// fail here. Keep fail-closed: the old session remains terminated.
		return DebugSessionMetadata{}, err
	}
	return hub.metadataLocked(created), nil
}

func parseRevision(value string) (uint64, bool) {
	if !canonicalPositive(value) {
		return 0, false
	}
	revision, err := strconv.ParseUint(value, 10, 64)
	return revision, err == nil && revision > 0
}

func (hub *Hub) TerminateUser(userID int64, reason EndReason) error {
	if !validIdentityEndReason(reason) || hub == nil {
		return ErrInvalid
	}
	hub.mu.Lock()
	defer hub.mu.Unlock()
	if hub.closed {
		return ErrClosed
	}
	current := hub.activeByUser[userID]
	if current == nil || current.ended {
		return nil
	}
	hub.endSessionLocked(current, reason)
	return nil
}

func validIdentityEndReason(reason EndReason) bool {
	return reason == EndAuthRevoked || reason == EndAccountBanned || reason == EndAccountDeleted
}

func (hub *Hub) endSessionLocked(current *session, reason EndReason) {
	if current == nil || current.ended {
		return
	}
	current.ended = true
	delete(hub.activeByUser, current.userID)
	cancelled := current.inflight
	for _, traceID := range current.traceOrder {
		if record := current.traces[traceID]; record != nil && record.cancel != nil {
			record.cancel(fmt.Errorf("%w: %s", ErrSessionEnded, reason))
		}
	}
	// Identity and hard lifecycle termination must immediately drop every
	// request body and trace. Drain queued trace snapshots before enqueueing the
	// one terminal event so no stale body can be delivered afterward.
	current.traces = make(map[string]*traceRecord)
	current.traceOrder = nil
	current.traceBytes = 0
	current.inflight = 0
	for subscriber := range current.subscribers {
		subscriber.drainLocked()
	}
	for len(current.events) > 0 {
		hub.discardOldestEventLocked(current, GapRingEvicted, hub.config.now())
	}
	if len(current.subscribers) == 0 {
		hub.maybeReleaseEndedSessionLocked(current)
		return
	}
	end, err := hub.newEventLocked(current, EventSessionEnd, SessionEndData{
		Reason: reason, CancelledInflightCount: cancelled,
	})
	if err == nil {
		err = hub.appendEventLocked(current, end)
	}
	for subscriber := range current.subscribers {
		if err == nil {
			if !subscriber.enqueueLocked(queuedEvent{eventID: end.event.EventID, closeAfter: true}) {
				subscriber.closeLocked()
			}
		} else {
			subscriber.closeLocked()
		}
	}
	hub.maybeReleaseEndedSessionLocked(current)
}

func (hub *Hub) maybeReleaseEndedSessionLocked(current *session) {
	if current == nil || !current.ended || len(current.subscribers) != 0 {
		return
	}
	for _, record := range current.eventIndex {
		if record != nil && record.pinCount != 0 {
			return
		}
	}
	for _, record := range current.events {
		if record != nil {
			record.inRing = false
		}
	}
	current.events = nil
	current.eventBytes = 0
	current.retainedEvents = make(map[string]*eventRecord)
	current.retainedBytes = 0
	current.eventIndex = make(map[string]*eventRecord)
	delete(hub.sessions, current)
}

func (hub *Hub) newEventLocked(current *session, kind EventKind, payload any) (*eventRecord, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	if err := validateEventData(kind, data); err != nil {
		return nil, err
	}
	id, err := hub.config.newEventID()
	if err != nil {
		return nil, fmt.Errorf("generate debug event id: %w", err)
	}
	if !validOID(id, "dbe_") {
		return nil, errors.New("debug event id generator returned a non-canonical id")
	}
	occurredAt := hub.config.now().Unix()
	if occurredAt < 0 || occurredAt > maxUnixSecond {
		return nil, errors.New("debug event clock is outside UTC range")
	}
	event := EventEnvelope{
		Version: 2, EventID: id, SessionID: current.id,
		Generation: strconv.FormatUint(current.generation, 10), Kind: kind,
		OccurredAt: occurredAt, Data: data,
	}
	wire, err := json.Marshal(event)
	if err != nil || len(wire) > MaxEventBytes {
		return nil, ErrCapacity
	}
	return &eventRecord{event: event, wireBytes: len(wire), createdAt: hub.config.now()}, nil
}

func (hub *Hub) appendEventLocked(current *session, record *eventRecord) error {
	if current == nil || record == nil || record.inRing ||
		current.eventIndex[record.event.EventID] != nil {
		return ErrInvalid
	}
	if !hub.reserveEventCapacityLocked(current, 1, record.wireBytes) {
		return ErrCapacity
	}
	hub.appendReservedEventLocked(current, record)
	return nil
}

func (hub *Hub) appendReservedEventLocked(current *session, record *eventRecord) {
	record.inRing = true
	current.events = append(current.events, record)
	current.eventIndex[record.event.EventID] = record
	current.eventBytes += record.wireBytes
	hub.rememberKnownLocked(record.event.EventID, knownEvent{
		userID: current.userID, sessionID: current.id,
		generation: strconv.FormatUint(current.generation, 10),
	})
}

func (hub *Hub) reserveEventCapacityLocked(current *session, addedCount, addedBytes int) bool {
	if current == nil || addedCount < 0 || addedBytes < 0 ||
		addedCount > MaxSessionEvents || addedBytes > MaxSessionBytes {
		return false
	}
	for current.eventCount()+addedCount > MaxSessionEvents ||
		current.bytes()+addedBytes > MaxSessionBytes {
		if !hub.discardOldestEventLocked(current, GapRingEvicted, hub.config.now()) {
			return false
		}
	}
	for hub.totalBytesLocked()+int64(addedBytes) > MaxGlobalBytes {
		if !hub.discardOldestGlobalEventLocked() {
			return false
		}
	}
	return current.eventCount()+addedCount <= MaxSessionEvents &&
		current.bytes()+addedBytes <= MaxSessionBytes &&
		hub.totalBytesLocked()+int64(addedBytes) <= MaxGlobalBytes
}

func (hub *Hub) rememberKnownLocked(id string, value knownEvent) {
	if _, exists := hub.knownEvents[id]; !exists {
		hub.knownOrder = append(hub.knownOrder, id)
	}
	hub.knownEvents[id] = value
	for len(hub.knownOrder) > knownEventLimit {
		oldest := hub.knownOrder[0]
		hub.knownOrder = hub.knownOrder[1:]
		delete(hub.knownEvents, oldest)
	}
}

func (hub *Hub) discardOldestEventLocked(current *session, reason GapReason, now time.Time) bool {
	if len(current.events) == 0 {
		return false
	}
	oldest := current.events[0]
	current.events[0] = nil
	current.events = current.events[1:]
	current.eventBytes -= oldest.wireBytes
	oldest.inRing = false
	if oldest.pinCount > 0 {
		current.retainedEvents[oldest.event.EventID] = oldest
		current.retainedBytes += oldest.wireBytes
	} else {
		delete(current.eventIndex, oldest.event.EventID)
	}
	current.discarded[oldest.event.EventID] = discardedEvent{reason: reason, at: now}
	current.discardedOrder = append(current.discardedOrder, oldest.event.EventID)
	known := hub.knownEvents[oldest.event.EventID]
	known.reason = reason
	hub.rememberKnownLocked(oldest.event.EventID, known)
	for len(current.discardedOrder) > discardedPerRing {
		id := current.discardedOrder[0]
		current.discardedOrder = current.discardedOrder[1:]
		delete(current.discarded, id)
	}
	return true
}

func (hub *Hub) releaseEventPinLocked(current *session, record *eventRecord) {
	if current == nil || record == nil || record.pinCount <= 0 {
		return
	}
	record.pinCount--
	if record.pinCount != 0 || record.inRing {
		return
	}
	if current.retainedEvents[record.event.EventID] != record {
		return
	}
	delete(current.retainedEvents, record.event.EventID)
	current.retainedBytes -= record.wireBytes
	delete(current.eventIndex, record.event.EventID)
}

func (hub *Hub) cleanupRingLocked(current *session, now time.Time) {
	for len(current.events) > 0 && now.Sub(current.events[0].createdAt) >= hub.config.ringAge {
		hub.discardOldestEventLocked(current, GapRingExpired, now)
	}
	for len(current.discardedOrder) > 0 {
		id := current.discardedOrder[0]
		discarded, exists := current.discarded[id]
		if exists && now.Sub(discarded.at) < hub.config.ringAge {
			break
		}
		current.discardedOrder = current.discardedOrder[1:]
		delete(current.discarded, id)
	}
}

func (hub *Hub) totalBytesLocked() int64 {
	var total int64
	for current := range hub.sessions {
		total += int64(current.bytes())
	}
	return total
}

func (hub *Hub) trimGlobalLocked() {
	for hub.totalBytesLocked() > MaxGlobalBytes {
		if !hub.discardOldestGlobalEventLocked() {
			return
		}
	}
}

func (hub *Hub) discardOldestGlobalEventLocked() bool {
	var target *session
	for current := range hub.sessions {
		if current.ended || len(current.events) == 0 {
			continue
		}
		if target == nil || current.events[0].createdAt.Before(target.events[0].createdAt) ||
			(current.events[0].createdAt.Equal(target.events[0].createdAt) && current.id < target.id) {
			target = current
		}
	}
	if target == nil {
		return false
	}
	hub.discardOldestEventLocked(target, GapRingEvicted, hub.config.now())
	return true
}

func (hub *Hub) snapshotDataLocked(current *session) SnapshotData {
	traces := make([]DebugTrace, 0, len(current.traceOrder))
	for _, traceID := range current.traceOrder {
		if record := current.traces[traceID]; record != nil {
			traces = append(traces, cloneTrace(record.trace))
		}
	}
	var first, last *string
	if len(current.events) > 0 {
		first = stringPointer(current.events[0].event.EventID)
		last = stringPointer(current.events[len(current.events)-1].event.EventID)
	}
	return SnapshotData{
		Session: hub.metadataLocked(current), Traces: traces,
		FirstEventID: first, LastEventID: last,
	}
}

func (hub *Hub) publishLocked(current *session, kind EventKind, payload any) (*eventRecord, error) {
	record, err := hub.newEventLocked(current, kind, payload)
	if err != nil {
		return nil, err
	}
	if err := hub.appendEventLocked(current, record); err != nil {
		return nil, err
	}
	for subscriber := range current.subscribers {
		if subscriber.closed {
			continue
		}
		if !subscriber.tryEnqueueLocked(queuedEvent{eventID: record.event.EventID}) {
			hub.recoverSlowLocked(current, subscriber)
		}
	}
	return record, nil
}

// appendRecoveryLocked reserves room for gap+snapshot before either event
// enters the ring. Without this preflight, adding the recovery pair at an
// exact count/byte boundary could evict the ID just advertised as
// first_available_event_id.
func (hub *Hub) appendRecoveryLocked(current *session, reason GapReason) (*eventRecord, *eventRecord, error) {
	build := func() (*eventRecord, *eventRecord, error) {
		first := firstEventID(current)
		gap, err := hub.newEventLocked(current, EventGap, GapData{
			Reason: reason, FirstAvailableEventID: first,
		})
		if err != nil {
			return nil, nil, err
		}

		// Project the snapshot exactly as it will look immediately after the gap
		// is appended, while keeping both records outside the ring until their
		// combined capacity has been reserved.
		snapshotData := hub.snapshotDataLocked(current)
		if snapshotData.FirstEventID == nil {
			snapshotData.FirstEventID = stringPointer(gap.event.EventID)
		}
		snapshotData.LastEventID = stringPointer(gap.event.EventID)
		snapshotData.Session.LastEventID = stringPointer(gap.event.EventID)
		snapshot, err := hub.newEventLocked(current, EventSnapshot, snapshotData)
		if err != nil {
			return nil, nil, err
		}
		return gap, snapshot, nil
	}

	for attempts := 0; attempts < MaxSessionEvents+2; attempts++ {
		firstBefore := firstEventID(current)
		gap, snapshot, err := build()
		if err != nil {
			return nil, nil, err
		}
		addedBytes := gap.wireBytes + snapshot.wireBytes
		if !hub.reserveEventCapacityLocked(current, 2, addedBytes) {
			return nil, nil, ErrCapacity
		}
		if !equalOptionalString(firstBefore, firstEventID(current)) {
			continue
		}
		hub.appendReservedEventLocked(current, gap)
		hub.appendReservedEventLocked(current, snapshot)
		return gap, snapshot, nil
	}
	return nil, nil, ErrCapacity
}

func equalOptionalString(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func (hub *Hub) recoverSlowLocked(current *session, subscriber *Subscription) bool {
	if subscriber == nil || subscriber.closed {
		return false
	}
	if current == nil || current.ended {
		subscriber.closeLocked()
		return false
	}
	subscriber.drainLocked()
	gap, snapshot, err := hub.appendRecoveryLocked(current, GapSlowConsumer)
	if err != nil || !subscriber.enqueueLocked(queuedEvent{eventID: gap.event.EventID}) ||
		!subscriber.enqueueLocked(queuedEvent{eventID: snapshot.event.EventID}) {
		subscriber.closeLocked()
		return false
	}
	return true
}

func firstEventID(current *session) *string {
	if len(current.events) == 0 {
		return nil
	}
	return stringPointer(current.events[0].event.EventID)
}

func (hub *Hub) Close() error {
	if hub == nil {
		return nil
	}
	hub.mu.Lock()
	if hub.closed {
		hub.mu.Unlock()
		return nil
	}
	hub.closed = true
	users := make([]int64, 0, len(hub.activeByUser))
	for userID := range hub.activeByUser {
		users = append(users, userID)
	}
	for _, userID := range users {
		hub.endSessionLocked(hub.activeByUser[userID], EndShutdown)
	}
	close(hub.stopSweep)
	hub.mu.Unlock()
	<-hub.sweepDone
	return nil
}
