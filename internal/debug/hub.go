package debug

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"sync"
	"sync/atomic"
	"time"
)

type hubSession struct {
	userID      int64
	binding     string
	id          string
	generation  uint64
	mode        Mode
	created     time.Time
	expires     time.Time
	idleExpires time.Time
	firstAttach bool
	closed      bool
	// observerActive bounds request-side SafeObserver sidecars by the same
	// logical-trace cap as the session.  A session can never allocate an
	// unbounded worker/queue set merely by sending more concurrent requests.
	observerActive             int
	confirmationDigest         [32]byte
	hasConfirmation            bool
	nextSeq                    uint64
	dropped                    uint64
	bytes                      int64
	requestCopyBytes           int64
	events                     []*storedEvent
	traces                     map[string]*traceRecord
	traceOrder                 []string
	subscribers                map[*subscriber]struct{}
	firstTimer                 *time.Timer
	idleTimer                  *time.Timer
	absTimer                   *time.Timer
	reconnectTimer             *time.Timer
	reconnectExpires           time.Time
	suppressSubscriberDelivery bool
	snapshotConnectedOverride  bool
}

type traceRecord struct {
	id       string
	revision uint64
	wire     []byte
	terminal bool
}

type storedEvent struct {
	envelope EventEnvelope
	wire     []byte
	seq      uint64
	size     int64
	refs     int
	closed   bool
	session  *hubSession
}

// eventDelivery is one subscriber-queue reference to a stored event. The
// event wire and its detached payload are retained/countable exactly once,
// while ring eviction merely removes the ring reference until queued writers
// have consumed or dropped their delivery.
type eventDelivery struct {
	hub         *Hub
	event       *storedEvent
	session     *hubSession
	subscriber  *subscriber
	payload     map[string]any
	payloadSize int64
	released    atomic.Bool
}

// fragmentData keeps the decoded fragment bytes in an erasable, budgeted
// backing array while MarshalJSON exposes the documented base64url string on
// the wire. Storing a plain string here would retain an uncounted immutable
// copy that could not be zeroed when an event or subscriber is evicted.
type fragmentData []byte

func (data fragmentData) MarshalJSON() ([]byte, error) {
	encoded := make([]byte, base64.RawURLEncoding.EncodedLen(len(data))+2)
	encoded[0] = '"'
	base64.RawURLEncoding.Encode(encoded[1:len(encoded)-1], data)
	encoded[len(encoded)-1] = '"'
	return encoded, nil
}

func (d *eventDelivery) release() {
	if d == nil || d.hub == nil || d.released.Swap(true) {
		return
	}
	d.hub.mu.Lock()
	d.hub.releaseDeliveryLocked(d)
	d.hub.mu.Unlock()
}

type subscriber struct {
	ch          chan EventEnvelope
	session     *hubSession
	closed      bool
	serial      uint64
	queuedBytes int64
}

// Subscription is an in-memory replay cursor.  The channel is bounded and is
// closed by Hub when the session ends; Close is idempotent and safe from the
// HTTP request's cancellation path.
type Subscription struct {
	hub  *Hub
	sub  *subscriber
	once sync.Once
}

func (s *Subscription) Events() <-chan EventEnvelope {
	if s == nil || s.sub == nil {
		return nil
	}
	return s.sub.ch
}

func (s *Subscription) SessionID() string {
	if s == nil || s.sub == nil || s.sub.session == nil {
		return ""
	}
	return s.sub.session.id
}

func (s *Subscription) Close() {
	if s == nil {
		return
	}
	s.once.Do(func() {
		if s.hub != nil {
			s.hub.detach(s.sub)
		}
	})
}

// Hub owns all active sessions and retained safe projections.  It intentionally
// has no persistence callback and no logger callback.
type Hub struct {
	mu                       sync.Mutex
	now                      func() time.Time
	maxSessions              int
	maxHubBytes              int64
	maxSessionBytes          int64
	maxRequestCopyBytes      int64
	maxRequestCopyPerSession int64
	limits                   Limits
	sessions                 map[int64]*hubSession
	nextGeneration           uint64
	confirmations            map[[32]byte]confirmation
	totalBytes               int64
	requestCopyBytes         int64
	observerCount            int
	maxObservers             int
	serial                   atomic.Uint64
	closed                   bool
	recoveryDepth            int
	snapshotCompactionDepth  int
	validator                DryRunValidator
	charityValidator         DryRunValidator
	mapDryError              ErrorMapper
	bindingValidator         SessionBindingValidator
}

type confirmation struct {
	userID     int64
	binding    string
	sessionID  string
	generation uint64
	expires    time.Time
}

// NewHub creates one process-local Hub.  Config limits may only lower the
// frozen process/session budgets; callers cannot raise them accidentally.
func NewHub(config Config) (*Hub, error) {
	now := config.Now
	if now == nil {
		now = time.Now
	}
	maxSessions := config.MaxSessions
	if maxSessions == 0 {
		maxSessions = MaxSessions
	}
	maxHubBytes := config.MaxHubBytes
	if maxHubBytes == 0 {
		maxHubBytes = MaxHubBytes
	}
	maxSessionBytes := config.MaxSessionBytes
	if maxSessionBytes == 0 {
		maxSessionBytes = MaxSessionBytes
	}
	if maxSessions < 1 || maxSessions > MaxSessions || maxHubBytes < 1 || maxHubBytes > MaxHubBytes ||
		maxSessionBytes < 1 || maxSessionBytes > MaxSessionBytes {
		return nil, ErrInvalid
	}
	maxRequestCopyBytes := int64(maxSessions) * int64(MaxRequestCopyBytes)
	if maxRequestCopyBytes > maxHubBytes {
		maxRequestCopyBytes = maxHubBytes
	}
	return &Hub{
		now:                      now,
		maxSessions:              maxSessions,
		maxHubBytes:              maxHubBytes,
		maxSessionBytes:          maxSessionBytes,
		maxRequestCopyBytes:      maxRequestCopyBytes,
		maxRequestCopyPerSession: int64(MaxRequestCopyBytes),
		maxObservers:             maxSessions * MaxTraces,
		limits:                   defaultLimits(maxSessions, maxHubBytes, maxSessionBytes),
		sessions:                 make(map[int64]*hubSession),
		confirmations:            make(map[[32]byte]confirmation),
		validator:                config.DryRunValidator,
		charityValidator:         config.CharityDryRunValidator,
		mapDryError:              config.MapDryRunError,
		bindingValidator:         config.SessionBindingValidator,
	}, nil
}

func (h *Hub) String() string { return "[debug hub]" }

// requestCopyLease accounts temporary request/response/observer buffers that
// intentionally are not retained trace bytes.  The lease owns a session
// pointer so a session replacement/close cannot strand the global reservation;
// Release is idempotent and may run after the session has left h.sessions.
type requestCopyLease struct {
	hub     *Hub
	session *hubSession
	bytes   int64
	once    atomic.Bool
}

func (lease *requestCopyLease) Release() {
	if lease == nil || lease.hub == nil || lease.session == nil || lease.once.Swap(true) {
		return
	}
	lease.hub.mu.Lock()
	defer lease.hub.mu.Unlock()
	if lease.session.requestCopyBytes >= lease.bytes {
		lease.session.requestCopyBytes -= lease.bytes
	} else {
		lease.session.requestCopyBytes = 0
	}
	if lease.hub.requestCopyBytes >= lease.bytes {
		lease.hub.requestCopyBytes -= lease.bytes
	} else {
		lease.hub.requestCopyBytes = 0
	}
}

func (h *Hub) acquireRequestCopyLease(userID int64, sessionID string, generation uint64) (*requestCopyLease, bool) {
	if h == nil || userID <= 0 || sessionID == "" || generation == 0 {
		return nil, false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	session := h.sessions[userID]
	if h.closed || session == nil || session.closed || session.id != sessionID || session.generation != generation || !h.validDeadlineLocked(session) {
		return nil, false
	}
	bytes := int64(MaxRequestCopyBytes)
	if bytes <= 0 || session.requestCopyBytes+bytes > h.maxRequestCopyPerSession || h.requestCopyBytes+bytes > h.maxRequestCopyBytes || h.totalBytes+h.requestCopyBytes+bytes > h.maxHubBytes {
		return nil, false
	}
	session.requestCopyBytes += bytes
	h.requestCopyBytes += bytes
	return &requestCopyLease{hub: h, session: session, bytes: bytes}, true
}

// Close closes every session, emits a shutdown terminal event to attached
// subscribers, zeroes retained projections, and rejects all future actions.
func (h *Hub) Close() error {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil
	}
	h.closed = true
	for userID, session := range h.sessions {
		h.closeSessionLocked(userID, session, EndShutdown)
	}
	for key := range h.confirmations {
		delete(h.confirmations, key)
	}
	// closeSessionLocked preserves one small session_end delivery for an
	// attached consumer. Process shutdown has no future accounting owner, so
	// discard that terminal-only queue budget as well; its payload contains
	// only the fixed lifecycle reason and no captured body.
	h.totalBytes = 0
	return nil
}

// Start creates a new dry session and replaces any existing session for the
// same user.  The opaque browser binding is held only as a hash supplied by
// the existing session middleware.
func (h *Hub) Start(userID int64, binding string) (SessionMetadata, error) {
	if h == nil || userID <= 0 || binding == "" {
		return SessionMetadata{}, ErrInvalid
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return SessionMetadata{}, ErrClosed
	}
	old := h.sessions[userID]
	// Do not tear down a usable session before proving that its replacement can
	// be identified and retain its initial snapshot. A failed start is a 503
	// capacity/entropy result, not an invitation to leave the caller with no
	// recoverable session.
	if old == nil && len(h.sessions) >= h.maxSessions {
		return SessionMetadata{}, ErrCapacity
	}
	now := h.now().UTC()
	h.nextGeneration++
	generation := h.nextGeneration
	if generation == 0 {
		// A uint64 wrap is not a safe way to reuse a generation-bound
		// confirmation. Fail closed rather than creating an alias.
		return SessionMetadata{}, ErrCapacity
	}
	sessionID, err := opaqueID("dbg_")
	if err != nil {
		return SessionMetadata{}, ErrClosed
	}
	session := &hubSession{
		userID:      userID,
		binding:     binding,
		id:          sessionID,
		generation:  generation,
		mode:        ModeDry,
		created:     now,
		expires:     now.Add(AbsoluteLifetime),
		idleExpires: now.Add(IdleTimeout),
		traces:      make(map[string]*traceRecord),
		subscribers: make(map[*subscriber]struct{}),
	}
	// Preflight the initial snapshot while the old session is still present.
	// This deliberately accounts against the existing process/session budgets;
	// if the replacement cannot fit, the old session remains authoritative.
	if !h.emitSnapshotLocked(session) {
		h.discardSessionAllocationLocked(session)
		return SessionMetadata{}, ErrCapacity
	}
	if old != nil {
		h.closeSessionLocked(userID, old, EndReplaced)
	}
	h.sessions[userID] = session
	session.firstTimer = time.AfterFunc(FirstAttachTimeout, func() {
		h.expire(userID, session, EndSessionInvalid)
	})
	session.idleTimer = time.AfterFunc(IdleTimeout, func() {
		h.expire(userID, session, EndIdleTimeout)
	})
	session.absTimer = time.AfterFunc(AbsoluteLifetime, func() {
		h.expire(userID, session, EndMaxAge)
	})
	return h.metadataLocked(session), nil
}

func (h *Hub) expire(userID int64, expected *hubSession, reason SessionEndReason) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	if current := h.sessions[userID]; current == expected {
		h.closeSessionLocked(userID, current, reason)
	}
}

// Sweep applies lifecycle deadlines using the supplied clock.  Production
// timers provide prompt cleanup; maintenance/tests can call Sweep to make the
// same state transitions deterministic without sleeping.
func (h *Hub) Sweep(now time.Time) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for userID, session := range h.sessions {
		if !session.firstAttach && now.Before(session.created.Add(FirstAttachTimeout)) {
			// No-op; this branch documents that first attach is measured from
			// creation rather than from an idle refresh.
		}
		if !now.Before(session.expires) {
			h.closeSessionLocked(userID, session, EndMaxAge)
			continue
		}
		if !now.Before(session.idleExpires) {
			h.closeSessionLocked(userID, session, EndIdleTimeout)
			continue
		}
		if !session.firstAttach && !now.Before(session.created.Add(FirstAttachTimeout)) {
			h.closeSessionLocked(userID, session, EndSessionInvalid)
			continue
		}
		if !session.reconnectExpires.IsZero() && !now.Before(session.reconnectExpires) {
			// Reconnect grace is an authoritative deadline, not merely a timer
			// hint.  Sweep must close a detached session even when the runtime
			// timer has not fired yet, matching validDeadlineLocked used by every
			// control/data request.
			h.closeSessionLocked(userID, session, EndSessionInvalid)
		}
	}
	for key, item := range h.confirmations {
		if !now.Before(item.expires) {
			delete(h.confirmations, key)
		}
	}
}

// ForgetUser immediately revokes and zeroes the current session.  It is the
// lifecycle hook used by logout/ban/delete paths.
func (h *Hub) ForgetUser(userID int64) { h.ForgetUserReason(userID, EndDeleted) }

func (h *Hub) ForgetUserReason(userID int64, reason SessionEndReason) {
	if h == nil || userID <= 0 {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if session := h.sessions[userID]; session != nil {
		h.closeSessionLocked(userID, session, reason)
	}
	for key, item := range h.confirmations {
		if item.userID == userID {
			delete(h.confirmations, key)
		}
	}
}

// ForgetBindingReason closes only the Debug session created by one exact
// browser login. It is used by logout so a stale tab cannot terminate a newer
// Debug session that the same user opened from another browser session.
func (h *Hub) ForgetBindingReason(userID int64, binding string, reason SessionEndReason) bool {
	if h == nil || userID <= 0 || binding == "" {
		return false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return false
	}
	changed := false
	if session := h.sessions[userID]; session != nil && !session.closed && session.binding == binding {
		h.closeSessionLocked(userID, session, reason)
		changed = true
	}
	for key, item := range h.confirmations {
		if item.userID == userID && item.binding == binding {
			delete(h.confirmations, key)
			changed = true
		}
	}
	return changed
}

func (h *Hub) metadataLocked(session *hubSession) SessionMetadata {
	if session == nil {
		return SessionMetadata{}
	}
	return SessionMetadata{
		ID:            session.id,
		Generation:    session.generation,
		Mode:          session.mode,
		CreatedAt:     session.created.Unix(),
		ExpiresAt:     session.expires.Unix(),
		IdleExpiresAt: session.idleExpires.Unix(),
		Connected:     len(session.subscribers) != 0,
		LastEventID:   session.nextSeq,
		Limits:        h.limits,
	}
}

// Metadata returns the current metadata only when the caller's user and
// binding still match.  No trace body is returned.
func (h *Hub) Metadata(userID int64, binding string) (SessionMetadata, bool) {
	if h == nil || userID <= 0 || binding == "" {
		return SessionMetadata{}, false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	session := h.sessions[userID]
	if session == nil || session.closed || session.binding != binding {
		return SessionMetadata{}, false
	}
	if !h.validDeadlineLocked(session) {
		h.closeSessionLocked(userID, session, EndSessionInvalid)
		return SessionMetadata{}, false
	}
	// GET /api/debug/session is an authenticated control action.  It is not
	// heartbeat traffic (SSE deliberately does not call Metadata), so a caller
	// actively inspecting the control state keeps the session's idle deadline
	// alive just like the other control actions.
	h.touchLocked(session)
	return h.metadataLocked(session), true
}

// Stop atomically verifies the browser binding and closes exactly the session
// that was observed by the caller.  Keeping the compare and close under one
// Hub lock prevents DELETE from doing a Metadata-then-Forget TOCTOU in which
// an old browser could stop a replacement session created for the same user.
func (h *Hub) Stop(userID int64, binding string) bool {
	if h == nil || userID <= 0 || binding == "" {
		return false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return false
	}
	session := h.sessions[userID]
	if session == nil || session.closed || session.binding != binding {
		return false
	}
	if !h.validDeadlineLocked(session) {
		h.closeSessionLocked(userID, session, EndSessionInvalid)
		return false
	}
	h.closeSessionLocked(userID, session, EndStopped)
	return true
}

// ModeForCaller freezes the current mode for one CallerKey request.  The
// returned session id/generation let a caller attach one stable trace even if
// the control plane changes mode mid-request.  Callers with an HTTP context
// should use ModeForCallerContext so the bound browser-session validator can
// be applied before a live grant is honored.
func (h *Hub) ModeForCaller(userID int64) (mode Mode, sessionID string, generation uint64, ok bool) {
	return h.ModeForCallerContext(context.Background(), userID)
}

// ModeForCallerContext validates the bound browser login session, then
// freezes the mode for one request.  A revoked binding closes the Debug
// session and returns inactive so the caller can resume ordinary forwarding.
// Validator uncertainty fails safe to an active dry snapshot; it never
// preserves a live grant.  The validator runs outside the Hub mutex so a
// session-store read cannot block or deadlock lifecycle operations.
func (h *Hub) ModeForCallerContext(ctx context.Context, userID int64) (mode Mode, sessionID string, generation uint64, ok bool) {
	if h == nil || userID <= 0 {
		return ModeDry, "", 0, false
	}
	if ctx == nil {
		ctx = context.Background()
	}
	h.mu.Lock()
	session := h.sessions[userID]
	if session == nil || session.closed {
		h.mu.Unlock()
		return ModeDry, "", 0, false
	}
	if !h.validDeadlineLocked(session) {
		// The caller identity is known, so an expired active session is
		// closed before the data path falls back to ordinary forwarding.  This
		// avoids leaving an ambiguous expired session in a state that could
		// accidentally inherit a live grant.
		h.closeSessionLocked(userID, session, EndSessionInvalid)
		h.mu.Unlock()
		return ModeDry, "", 0, false
	}
	binding := session.binding
	snapshotID, snapshotGeneration := session.id, session.generation
	h.touchLocked(session)
	validator := h.bindingValidator
	h.mu.Unlock()

	if validator == nil {
		// A production wiring that forgot to provide the authoritative browser
		// session validator must never retain a live grant. Keep an identifiable
		// session only in the safe dry mode so the request cannot silently fall
		// through to a real upstream call.
		h.mu.Lock()
		defer h.mu.Unlock()
		current := h.sessions[userID]
		if current == nil || current.closed || current.id != snapshotID || current.generation != snapshotGeneration {
			return ModeDry, "", 0, false
		}
		if current.mode == ModeLive {
			current.mode = ModeDry
			h.emitSnapshotLocked(current)
		}
		return current.mode, current.id, current.generation, true
	}
	valid, validationErr := validator(ctx, userID, binding)
	h.mu.Lock()
	defer h.mu.Unlock()
	current := h.sessions[userID]
	if current == nil || current.closed {
		return ModeDry, "", 0, false
	}
	if current.id != snapshotID || current.generation != snapshotGeneration || current.binding != binding {
		// A replacement raced with validation.  Do not carry the old grant
		// into the new session; make the current session dry and let this
		// request attach only to that safe snapshot.
		if current.mode == ModeLive {
			current.mode = ModeDry
			h.emitSnapshotLocked(current)
		}
		return ModeDry, current.id, current.generation, true
	}
	if validationErr != nil {
		if current.mode == ModeLive {
			current.mode = ModeDry
			h.emitSnapshotLocked(current)
		}
		return ModeDry, current.id, current.generation, true
	}
	if !valid {
		h.closeSessionLocked(userID, current, EndSessionInvalid)
		return ModeDry, "", 0, false
	}
	return current.mode, current.id, current.generation, true
}

// RevalidateBinding is the heartbeat/lifecycle form of the binding check. It
// deliberately does not touch idle expiry: SSE heartbeats are liveness
// traffic, not observed CallerKey activity.  active=false means the session
// was revoked/replaced/expired; uncertain=true keeps an identifiable session
// alive only after forcing it to dry.
func (h *Hub) RevalidateBinding(ctx context.Context, userID int64, binding string) (active bool, uncertain bool) {
	if h == nil || userID <= 0 || binding == "" {
		return false, false
	}
	if ctx == nil {
		ctx = context.Background()
	}
	h.mu.Lock()
	session := h.sessions[userID]
	if session == nil || session.closed || session.binding != binding {
		h.mu.Unlock()
		return false, false
	}
	if !h.validDeadlineLocked(session) {
		h.closeSessionLocked(userID, session, EndSessionInvalid)
		h.mu.Unlock()
		return false, false
	}
	validator := h.bindingValidator
	sessionID, generation := session.id, session.generation
	h.mu.Unlock()
	if validator == nil {
		// Missing validation authority is uncertainty, not proof that a browser
		// login is still valid. Revoke live mode immediately while retaining the
		// session for a dry snapshot.
		h.mu.Lock()
		defer h.mu.Unlock()
		current := h.sessions[userID]
		if current == nil || current.closed || current.id != sessionID || current.generation != generation {
			return false, false
		}
		if current.mode == ModeLive {
			current.mode = ModeDry
			h.emitSnapshotLocked(current)
		}
		return true, true
	}
	valid, validationErr := validator(ctx, userID, binding)
	h.mu.Lock()
	defer h.mu.Unlock()
	current := h.sessions[userID]
	if current == nil || current.closed || current.id != sessionID || current.generation != generation || current.binding != binding {
		return false, false
	}
	if validationErr != nil {
		if current.mode == ModeLive {
			current.mode = ModeDry
			h.emitSnapshotLocked(current)
		}
		return true, true
	}
	if !valid {
		h.closeSessionLocked(userID, current, EndSessionInvalid)
		return false, false
	}
	return true, false
}

func (h *Hub) validDeadlineLocked(session *hubSession) bool {
	now := h.now()
	if !now.Before(session.expires) || !now.Before(session.idleExpires) {
		return false
	}
	if !session.firstAttach && !now.Before(session.created.Add(FirstAttachTimeout)) {
		return false
	}
	if !session.reconnectExpires.IsZero() && !now.Before(session.reconnectExpires) {
		return false
	}
	return true
}

func (h *Hub) touchLocked(session *hubSession) {
	if session == nil || session.closed {
		return
	}
	now := h.now().UTC()
	idle := now.Add(IdleTimeout)
	if idle.After(session.expires) {
		idle = session.expires
	}
	session.idleExpires = idle
	if session.idleTimer != nil {
		session.idleTimer.Stop()
		delay := idle.Sub(now)
		if delay <= 0 {
			delay = time.Millisecond
		}
		session.idleTimer = time.AfterFunc(delay, func() {
			h.expire(session.userID, session, EndIdleTimeout)
		})
	}
}

// IssueChallenge creates a single-use confirmation bound to the active
// session, login binding and generation.  Only its opaque value leaves the
// Hub; the stored value is a SHA-256 digest.
func (h *Hub) IssueChallenge(userID int64, binding string) (string, error) {
	if h == nil || userID <= 0 || binding == "" {
		return "", ErrInvalid
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return "", ErrClosed
	}
	session := h.sessions[userID]
	if session == nil || session.closed || session.binding != binding {
		return "", ErrNotFound
	}
	if !h.validDeadlineLocked(session) {
		h.closeSessionLocked(userID, session, EndSessionInvalid)
		return "", ErrNotFound
	}
	h.touchLocked(session)
	raw, err := opaqueID("confirm_")
	if err != nil {
		return "", ErrClosed
	}
	digest := sha256.Sum256([]byte(raw))
	if session.hasConfirmation {
		delete(h.confirmations, session.confirmationDigest)
		session.hasConfirmation = false
		clear(session.confirmationDigest[:])
	}
	h.confirmations[digest] = confirmation{
		userID: userID, binding: binding, sessionID: session.id,
		generation: session.generation, expires: h.now().Add(ConfirmationLifetime),
	}
	session.confirmationDigest = digest
	session.hasConfirmation = true
	return raw, nil
}

// SetMode consumes a confirmation atomically when switching to live.  Dry is
// always immediately available and clears every pending confirmation.
func (h *Hub) SetMode(userID int64, binding string, mode Mode, confirmationID string) (SessionMetadata, error) {
	if h == nil || userID <= 0 || binding == "" || (mode != ModeDry && mode != ModeLive) {
		return SessionMetadata{}, ErrInvalid
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return SessionMetadata{}, ErrClosed
	}
	session := h.sessions[userID]
	if session == nil || session.closed || session.binding != binding {
		return SessionMetadata{}, ErrNotFound
	}
	if !h.validDeadlineLocked(session) {
		h.closeSessionLocked(userID, session, EndSessionInvalid)
		return SessionMetadata{}, ErrNotFound
	}
	h.touchLocked(session)
	if mode == ModeLive {
		// Actual sending is only meaningful while this browser still has an
		// attached SSE consumer.  Keep this check under the same mutex as
		// detach: whichever operation wins the lock decides whether the live
		// transition is accepted, and detaching the last subscriber will
		// immediately force dry mode. Consume any outstanding confirmation on
		// this failed attempt so
		// a challenge cannot be replayed after the browser has disconnected.
		if len(session.subscribers) == 0 {
			for key, item := range h.confirmations {
				if item.userID == userID && item.sessionID == session.id {
					delete(h.confirmations, key)
				}
			}
			session.hasConfirmation = false
			clear(session.confirmationDigest[:])
			return SessionMetadata{}, ErrConfirmation
		}
		if h.bindingValidator == nil {
			// Live mode is fail-closed when the integration has not supplied the
			// browser-session authority required by the frozen contract.
			return SessionMetadata{}, ErrConfirmation
		}
		if confirmationID == "" {
			return SessionMetadata{}, ErrConfirmation
		}
		digest := sha256.Sum256([]byte(confirmationID))
		item, ok := h.confirmations[digest]
		if !ok || item.userID != userID || item.binding != binding || item.sessionID != session.id ||
			item.generation != session.generation || !h.now().Before(item.expires) {
			delete(h.confirmations, digest)
			if session.hasConfirmation && session.confirmationDigest == digest {
				session.hasConfirmation = false
			}
			return SessionMetadata{}, ErrConfirmation
		}
		delete(h.confirmations, digest)
		session.hasConfirmation = false
	}
	for key, item := range h.confirmations {
		if item.userID == userID {
			delete(h.confirmations, key)
		}
	}
	session.hasConfirmation = false
	if session.mode != mode {
		session.mode = mode
		h.emitSnapshotLocked(session)
	}
	return h.metadataLocked(session), nil
}

// Subscribe attaches one bounded SSE consumer and prepares a replay.  When a
// resume id has fallen out of the ring, a gap followed by a fresh snapshot is
// emitted before newer events.
func (h *Hub) Subscribe(userID int64, binding string, lastEventID uint64, hasLastID bool) (*Subscription, error) {
	if h == nil || userID <= 0 || binding == "" {
		return nil, ErrInvalid
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil, ErrClosed
	}
	session := h.sessions[userID]
	if session == nil || session.closed || session.binding != binding {
		return nil, ErrNotFound
	}
	if !h.validDeadlineLocked(session) {
		h.closeSessionLocked(userID, session, EndSessionInvalid)
		return nil, ErrNotFound
	}
	if len(session.subscribers) >= MaxSubscribers {
		return nil, ErrCapacity
	}
	originalFirstAttach := session.firstAttach
	originalMode := session.mode
	originalIdleExpires := session.idleExpires
	firstTimerWasSet := session.firstTimer != nil
	reconnectTimerWasSet := session.reconnectTimer != nil
	idleTimerWasSet := session.idleTimer != nil
	originalReconnectExpires := session.reconnectExpires
	originalEventLen := len(session.events)
	originalNextSeq := session.nextSeq
	originalDropped := session.dropped
	forceDryReconnect := session.firstAttach && len(session.subscribers) == 0
	reconnectCursorStale := forceDryReconnect && hasLastID && h.replayCursorStaleLocked(session, lastEventID)
	cursorStale := hasLastID && h.replayCursorStaleLocked(session, lastEventID)
	replayCount := len(session.events)
	if hasLastID {
		replayCount = len(h.replayAfterLocked(session, lastEventID))
	}
	if forceDryReconnect {
		// Freeze the reconnect snapshot's mode before both planning and
		// emission. No event/timer mutation has happened yet, so a capacity
		// failure can restore this scalar state transactionally.
		session.mode = ModeDry
	}
	// Plan the replay before touching first-attach/reconnect state.  Delivery
	// clones are separately budgeted from the retained ring; without this
	// preflight a nearly-full session could be reported as successfully
	// connected with an empty queue after deliveryEnvelopeLocked failed.
	needsRecovery := forceDryReconnect || h.replayNeedsRecoveryLocked(session, lastEventID, hasLastID)
	if !h.subscribeReplayFitsLocked(session, lastEventID, hasLastID, needsRecovery) {
		session.mode = originalMode
		return nil, ErrCapacity
	}
	if session.reconnectTimer != nil {
		session.reconnectTimer.Stop()
		session.reconnectTimer = nil
		session.reconnectExpires = time.Time{}
	}
	if !session.firstAttach {
		session.firstAttach = true
		if session.firstTimer != nil {
			session.firstTimer.Stop()
			session.firstTimer = nil
		}
	} else if len(session.subscribers) == 0 && !forceDryReconnect {
		// A reconnect is deliberately dry even within grace; live requires a
		// new challenge and confirmation.
		if session.mode != ModeDry {
			session.mode = ModeDry
			h.emitSnapshotLocked(session)
		}
	}
	h.touchLocked(session)
	sub := &subscriber{ch: make(chan EventEnvelope, MaxSubscriberQueue), session: session, serial: h.serial.Add(1)}
	var replay []*storedEvent
	if forceDryReconnect {
		before := session.nextSeq
		if reconnectCursorStale {
			h.emitGapLocked(session, lastEventID, "resume_gap")
		}
		session.snapshotConnectedOverride = true
		snapshotOK := h.emitSnapshotLocked(session)
		session.snapshotConnectedOverride = false
		if !snapshotOK {
			h.rollbackSubscribeStateLocked(session, originalFirstAttach, originalMode, originalIdleExpires, firstTimerWasSet, reconnectTimerWasSet, idleTimerWasSet, originalReconnectExpires, originalEventLen, originalNextSeq, originalDropped)
			return nil, ErrCapacity
		}
		replay = h.replayAfterLocked(session, before)
	} else {
		session.snapshotConnectedOverride = needsRecovery
		replay = h.replayLocked(session, lastEventID, hasLastID)
		session.snapshotConnectedOverride = false
		if len(replay) == 0 && needsRecovery {
			before := session.nextSeq
			session.snapshotConnectedOverride = true
			snapshotOK := h.emitSnapshotLocked(session)
			session.snapshotConnectedOverride = false
			if !snapshotOK {
				h.rollbackSubscribeStateLocked(session, originalFirstAttach, originalMode, originalIdleExpires, firstTimerWasSet, reconnectTimerWasSet, idleTimerWasSet, originalReconnectExpires, originalEventLen, originalNextSeq, originalDropped)
				return nil, ErrCapacity
			}
			replay = h.replayAfterLocked(session, before)
		}
	}
	if len(replay) > MaxSubscriberQueue {
		// A bounded subscriber cannot consume an arbitrarily large retained
		// ring.  Replace its backlog with a gap and current snapshot.
		before := session.nextSeq
		h.emitGapLocked(session, lastEventID, "subscriber_queue")
		session.snapshotConnectedOverride = true
		h.emitSnapshotLocked(session)
		session.snapshotConnectedOverride = false
		replay = h.replayAfterLocked(session, before)
	}
	requireRecoveryGap := reconnectCursorStale || (!forceDryReconnect && (cursorStale || (!hasLastID && replayCount > MaxSubscriberQueue) || (hasLastID && replayCount > MaxSubscriberQueue)))
	if needsRecovery && !hasRecoverableReplay(replay, requireRecoveryGap) {
		// The preflight should make this unreachable, but a defensive rollback
		// keeps a failed recovery from leaving firstAttach/mode/timers changed
		// if a future event-budget implementation rejects a planned event.
		h.rollbackSubscribeStateLocked(session, originalFirstAttach, originalMode, originalIdleExpires, firstTimerWasSet, reconnectTimerWasSet, idleTimerWasSet, originalReconnectExpires, originalEventLen, originalNextSeq, originalDropped)
		return nil, ErrCapacity
	}
	prepared := make([]EventEnvelope, 0, len(replay))
	for _, event := range replay {
		envelope := h.deliveryEnvelopeLocked(event)
		if envelope.retained == nil {
			for _, ready := range prepared {
				h.releaseEnvelopeLocked(ready)
			}
			h.rollbackSubscribeStateLocked(session, originalFirstAttach, originalMode, originalIdleExpires, firstTimerWasSet, reconnectTimerWasSet, idleTimerWasSet, originalReconnectExpires, originalEventLen, originalNextSeq, originalDropped)
			return nil, ErrCapacity
		}
		envelope.retained.subscriber = sub
		prepared = append(prepared, h.replayEnvelopeLocked(session, envelope))
	}
	session.subscribers[sub] = struct{}{}
	for _, envelope := range prepared {
		if envelope.retained != nil {
			sub.queuedBytes += envelope.retained.payloadSize
		}
		sub.ch <- envelope
	}
	return &Subscription{hub: h, sub: sub}, nil
}

func (h *Hub) rollbackSubscribeStateLocked(session *hubSession, firstAttach bool, mode Mode, idleExpires time.Time, firstTimerWasSet, reconnectTimerWasSet, idleTimerWasSet bool, reconnectExpires time.Time, eventLen int, nextSeq, dropped uint64) {
	if session == nil {
		return
	}
	// This path is defensive; normal operation preflights all delivery bytes.
	// Remove only events appended by recovery.  The preflight guarantees that
	// no older event is evicted before a failure can reach here.
	for len(session.events) > eventLen {
		event := session.events[len(session.events)-1]
		session.events = session.events[:len(session.events)-1]
		h.forceCloseEventLocked(event)
	}
	session.nextSeq = nextSeq
	session.dropped = dropped
	session.firstAttach = firstAttach
	session.mode = mode
	session.idleExpires = idleExpires
	if session.firstTimer != nil {
		session.firstTimer.Stop()
		session.firstTimer = nil
	}
	if firstTimerWasSet {
		delay := session.created.Add(FirstAttachTimeout).Sub(h.now())
		if delay <= 0 {
			delay = time.Millisecond
		}
		session.firstTimer = time.AfterFunc(delay, func() { h.expire(session.userID, session, EndSessionInvalid) })
	}
	if session.reconnectTimer != nil {
		session.reconnectTimer.Stop()
		session.reconnectTimer = nil
	}
	session.reconnectExpires = time.Time{}
	if reconnectTimerWasSet {
		session.reconnectExpires = reconnectExpires
		delay := reconnectExpires.Sub(h.now())
		if delay <= 0 {
			delay = time.Millisecond
		}
		session.reconnectTimer = time.AfterFunc(delay, func() { h.expire(session.userID, session, EndSessionInvalid) })
	}
	if session.idleTimer != nil {
		session.idleTimer.Stop()
		session.idleTimer = nil
	}
	if idleTimerWasSet {
		delay := idleExpires.Sub(h.now())
		if delay <= 0 {
			delay = time.Millisecond
		}
		session.idleTimer = time.AfterFunc(delay, func() { h.expire(session.userID, session, EndIdleTimeout) })
	}
}

func (h *Hub) replayNeedsRecoveryLocked(session *hubSession, lastEventID uint64, hasLastID bool) bool {
	if session == nil || len(session.events) == 0 {
		return true
	}
	if !hasLastID {
		return len(session.events) > MaxSubscriberQueue
	}
	oldest := session.events[0].seq
	replay := h.replayAfterLocked(session, lastEventID)
	return (oldest > 0 && lastEventID < oldest-1) || lastEventID > session.nextSeq || len(replay) == 0 || len(replay) > MaxSubscriberQueue
}

func (h *Hub) replayCursorStaleLocked(session *hubSession, lastEventID uint64) bool {
	if session == nil || len(session.events) == 0 {
		return true
	}
	oldest := session.events[0].seq
	return (oldest > 0 && lastEventID < oldest-1) || lastEventID > session.nextSeq
}

// subscribeReplayFitsLocked reserves enough room for the replay delivery and,
// for a stale/oversized cursor, the newly generated gap+snapshot recovery.
// It intentionally errs toward ErrCapacity: a control-plane GET must never
// return a connected subscription whose required snapshot could not be
// delivered atomically.
func (h *Hub) subscribeReplayFitsLocked(session *hubSession, lastEventID uint64, hasLastID, needsRecovery bool) bool {
	if session == nil {
		return false
	}
	var sessionExtra, hubExtra int64
	add := func(event *storedEvent) bool {
		if event == nil || event.closed {
			return false
		}
		cloned, payloadBytes := cloneEventPayload(event.envelope.Payload)
		clearEventPayload(cloned)
		delta := payloadBytes
		if session.bytes+sessionExtra+delta > h.maxSessionBytes || h.totalBytes+hubExtra+delta > h.maxHubBytes {
			return false
		}
		sessionExtra += delta
		hubExtra += delta
		return true
	}
	if !needsRecovery {
		var replay []*storedEvent
		if hasLastID {
			replay = h.replayAfterLocked(session, lastEventID)
		} else {
			replay = session.events
		}
		for _, event := range replay {
			if !add(event) {
				return false
			}
		}
		return true
	}
	// A recovery snapshot may replace a large retained upsert history.  Plan
	// against the compacted state (trace authority plus the final snapshot and
	// one new subscriber delivery), rather than adding another 768KiB trace
	// projection on top of its fragment group.
	if h.compactedSnapshotFitsLocked(session, lastEventID, hasLastID) {
		return true
	}
	// The recovery events are emitted after this check. Estimate their exact
	// bounded wire/payload cost using the same projection shape, without
	// mutating the ring or session sequence.
	gapPayload := map[string]any{"after": lastEventID, "reason": "resume_gap"}
	if lastEventID > session.nextSeq {
		gapPayload["reason"] = "future_cursor"
	}
	if !h.addProjectedEventCostLocked(session, EventGap, "", 0, gapPayload, &sessionExtra, &hubExtra) {
		return false
	}
	snapshotPayload := h.snapshotPayloadLocked(session)
	raw := marshalJSON(snapshotPayload)
	if len(raw) == 0 {
		return false
	}
	count := (len(raw) + MaxFragmentBytes - 1) / MaxFragmentBytes
	if count < 1 || count > MaxRetainedEvents {
		return false
	}
	if h.eventFitsLocked(session, EventSessionSnapshot, "", 0, snapshotPayload) {
		return h.addProjectedEventCostLocked(session, EventSessionSnapshot, "", 0, snapshotPayload, &sessionExtra, &hubExtra)
	}
	for index := 0; index < count; index++ {
		start := index * MaxFragmentBytes
		end := start + MaxFragmentBytes
		if end > len(raw) {
			end = len(raw)
		}
		digest := sha256.Sum256(raw)
		fragment := map[string]any{"kind": "snapshot", "encoding": "base64url-json", "index": index, "count": count, "total_bytes": len(raw), "sha256": hex.EncodeToString(digest[:]), "data": fragmentData(raw[start:end])}
		if !h.addProjectedEventCostLocked(session, EventSessionSnapshot, "", 0, map[string]any{"fragment": fragment}, &sessionExtra, &hubExtra) {
			return false
		}
	}
	return true
}

func (h *Hub) compactedSnapshotFitsLocked(session *hubSession, lastEventID uint64, hasLastID bool) bool {
	if h == nil || session == nil {
		return false
	}
	needsGap := len(session.events) == 0
	if hasLastID {
		replay := h.replayAfterLocked(session, lastEventID)
		needsGap = h.replayCursorStaleLocked(session, lastEventID) || len(replay) > MaxSubscriberQueue
	} else if len(session.events) > MaxSubscriberQueue {
		needsGap = true
	}
	removed := int64(0)
	for _, event := range session.events {
		if event != nil && event.envelope.Type != EventGap {
			removed += event.size
		}
	}
	for sub := range session.subscribers {
		removed += sub.queuedBytes
	}
	baseSession := session.bytes - removed
	baseHub := h.totalBytes - removed
	if baseSession < 0 || baseHub < 0 {
		return false
	}
	var projectedSession, projectedHub int64
	addCost := func(typ EventType, traceID string, revision uint64, payload map[string]any, delivery bool) bool {
		retained, payloadBytes, ok := projectedEventCost(session, typ, traceID, revision, payload)
		if !ok {
			return false
		}
		projectedSession += retained
		projectedHub += retained
		if delivery {
			projectedSession += payloadBytes
			projectedHub += payloadBytes
		}
		return true
	}
	if needsGap {
		reason := "resume_gap"
		if hasLastID && lastEventID > session.nextSeq {
			reason = "future_cursor"
		}
		if !addCost(EventGap, "", 0, map[string]any{"after": lastEventID, "reason": reason}, true) {
			return false
		}
	}
	payload := h.snapshotPayloadLocked(session)
	raw := marshalJSON(payload)
	if len(raw) == 0 {
		return false
	}
	count := (len(raw) + MaxFragmentBytes - 1) / MaxFragmentBytes
	if count < 1 || count > MaxRetainedEvents {
		return false
	}
	digest := sha256.Sum256(raw)
	sha := hex.EncodeToString(digest[:])
	for index := 0; index < count; index++ {
		start := index * MaxFragmentBytes
		end := start + MaxFragmentBytes
		if end > len(raw) {
			end = len(raw)
		}
		fragment := map[string]any{"kind": "snapshot", "encoding": "base64url-json", "index": index, "count": count, "total_bytes": len(raw), "sha256": sha, "data": fragmentData(raw[start:end])}
		if !addCost(EventSessionSnapshot, "", 0, map[string]any{"fragment": fragment}, true) {
			return false
		}
	}
	return baseSession+projectedSession <= h.maxSessionBytes && baseHub+projectedHub+h.requestCopyBytes <= h.maxHubBytes
}

func projectedEventCost(session *hubSession, typ EventType, traceID string, revision uint64, payload map[string]any) (retained, payloadBytes int64, ok bool) {
	if session == nil {
		return 0, 0, false
	}
	cloned, payloadBytes := cloneEventPayload(payload)
	envelope := EventEnvelope{Version: 1, Seq: session.nextSeq + 1, Type: typ, SessionID: session.id, TraceID: safeIdentifier(traceID, maxTraceIdentifierBytes), Revision: revision, At: time.Now().Unix(), Payload: cloned}
	wire, err := json.Marshal(envelope)
	clearEventPayload(cloned)
	if err != nil || len(wire) > MaxEventBytes {
		return 0, 0, false
	}
	return int64(len(wire)) + payloadBytes, payloadBytes, true
}

func (h *Hub) addProjectedEventCostLocked(session *hubSession, typ EventType, traceID string, revision uint64, payload map[string]any, sessionExtra, hubExtra *int64) bool {
	if session == nil || sessionExtra == nil || hubExtra == nil {
		return false
	}
	cloned, payloadBytes := cloneEventPayload(payload)
	envelope := EventEnvelope{Version: 1, Seq: session.nextSeq + 1, Type: typ, SessionID: session.id, TraceID: safeIdentifier(traceID, maxTraceIdentifierBytes), Revision: revision, At: h.now().Unix(), Payload: cloned}
	wire, err := json.Marshal(envelope)
	clearEventPayload(cloned)
	if err != nil || len(wire) > MaxEventBytes {
		return false
	}
	// Retention owns wire+payload; this new subscriber additionally owns one
	// detached payload clone.
	delta := int64(len(wire)) + payloadBytes + payloadBytes
	if session.bytes+*sessionExtra+delta > h.maxSessionBytes || h.totalBytes+*hubExtra+delta > h.maxHubBytes {
		return false
	}
	*sessionExtra += delta
	*hubExtra += delta
	return true
}

func hasRecoverableReplay(replay []*storedEvent, requireGap bool) bool {
	if len(replay) == 0 {
		return false
	}
	gapSeen := false
	lastGapSeq := uint64(0)
	latestSnapshotSeq := uint64(0)
	type fragmentGroup struct {
		count int
		total int
		sha   string
		seen  map[int]int
		data  map[int]fragmentData
		bytes int
	}
	groups := make(map[string]*fragmentGroup)
	completePlain := false
	for _, event := range replay {
		if event == nil || event.closed {
			continue
		}
		switch event.envelope.Type {
		case EventGap:
			gapSeen = true
			lastGapSeq = event.envelope.Seq
		case EventSessionSnapshot:
			latestSnapshotSeq = event.envelope.Seq
			fragment, ok := event.envelope.Payload["fragment"].(map[string]any)
			if !ok {
				completePlain = true
				continue
			}
			sha, _ := fragment["sha256"].(string)
			count, okCount := fragment["count"].(int)
			index, okIndex := fragment["index"].(int)
			total, okTotal := fragment["total_bytes"].(int)
			data, okData := fragment["data"].(fragmentData)
			if !okCount || !okIndex || !okTotal || !okData || sha == "" || count < 1 || index < 0 || index >= count || total < 1 {
				continue
			}
			group := groups[sha]
			if group == nil {
				group = &fragmentGroup{count: count, total: total, sha: sha, seen: make(map[int]int), data: make(map[int]fragmentData)}
				groups[sha] = group
			}
			if group.count != count || group.total != total {
				continue
			}
			group.seen[index]++
			group.data[index] = append(fragmentData(nil), data...)
			group.bytes += len(data)
		}
	}
	if requireGap && (!gapSeen || latestSnapshotSeq == 0 || lastGapSeq >= latestSnapshotSeq) {
		return false
	}
	if completePlain {
		return true
	}
	for _, group := range groups {
		if len(group.seen) != group.count || group.bytes != group.total {
			continue
		}
		complete := true
		for index := 0; index < group.count; index++ {
			if group.seen[index] != 1 {
				complete = false
				break
			}
		}
		if complete {
			assembled := make([]byte, 0, group.total)
			for index := 0; index < group.count; index++ {
				assembled = append(assembled, group.data[index]...)
			}
			digest := sha256.Sum256(assembled)
			clear(assembled)
			for index, data := range group.data {
				clear(data)
				delete(group.data, index)
			}
			if hex.EncodeToString(digest[:]) == group.sha {
				return true
			}
		}
	}
	return false
}

func (h *Hub) deliveryEnvelopeLocked(event *storedEvent) EventEnvelope {
	if event == nil || event.closed {
		return EventEnvelope{}
	}
	event.refs++
	envelope := event.envelope
	clonedPayload, payloadBytes := cloneEventPayload(event.envelope.Payload)
	if event.session == nil || event.session.bytes+payloadBytes > h.maxSessionBytes || h.totalBytes+h.requestCopyBytes+payloadBytes > h.maxHubBytes {
		clearEventPayload(clonedPayload)
		h.releaseEventRefLocked(event)
		return EventEnvelope{}
	}
	envelope.Payload = clonedPayload
	delivery := &eventDelivery{hub: h, event: event, session: event.session, payload: clonedPayload, payloadSize: payloadBytes}
	envelope.retained = delivery
	event.session.bytes += payloadBytes
	h.totalBytes += payloadBytes
	return envelope
}

func (h *Hub) releaseEnvelopeLocked(envelope EventEnvelope) {
	if envelope.retained == nil || envelope.retained.released.Swap(true) {
		return
	}
	h.releaseDeliveryLocked(envelope.retained)
}

func (h *Hub) releaseDeliveryLocked(delivery *eventDelivery) {
	if delivery == nil {
		return
	}
	if delivery.payload != nil {
		clearEventPayload(delivery.payload)
		delivery.payload = nil
	}
	if delivery.payloadSize != 0 {
		if delivery.subscriber != nil {
			delivery.subscriber.queuedBytes -= delivery.payloadSize
			if delivery.subscriber.queuedBytes < 0 {
				delivery.subscriber.queuedBytes = 0
			}
		}
		owner := delivery.session
		if owner == nil && delivery.event != nil {
			owner = delivery.event.session
		}
		if owner != nil && !owner.closed {
			owner.bytes -= delivery.payloadSize
			if owner.bytes < 0 {
				owner.bytes = 0
			}
		}
		h.totalBytes -= delivery.payloadSize
		if h.totalBytes < 0 {
			h.totalBytes = 0
		}
		delivery.payloadSize = 0
	}
	if delivery.event != nil && !delivery.event.closed {
		h.releaseEventRefLocked(delivery.event)
	}
}

func (h *Hub) releaseEventRefLocked(event *storedEvent) {
	if event == nil || event.closed || event.refs <= 0 {
		return
	}
	event.refs--
	if event.refs != 0 {
		return
	}
	if event.envelope.Payload != nil {
		clearEventPayload(event.envelope.Payload)
	}
	clear(event.wire)
	if event.session != nil {
		event.session.bytes -= event.size
		if event.session.bytes < 0 {
			event.session.bytes = 0
		}
	}
	h.totalBytes -= event.size
	if h.totalBytes < 0 {
		h.totalBytes = 0
	}
	event.wire = nil
	event.size = 0
	event.closed = true
}

func cloneEventPayload(payload map[string]any) (map[string]any, int64) {
	if payload == nil {
		return nil, 0
	}
	value, bytes := cloneEventValue(payload)
	cloned, ok := value.(map[string]any)
	if !ok {
		return map[string]any{}, bytes
	}
	return cloned, bytes
}

func cloneEventValue(value any) (any, int64) {
	switch typed := value.(type) {
	case fragmentData:
		copyValue := append(fragmentData(nil), typed...)
		return copyValue, int64(len(copyValue))
	case json.RawMessage:
		copyValue := append([]byte(nil), typed...)
		return json.RawMessage(copyValue), int64(len(copyValue))
	case []byte:
		copyValue := append([]byte(nil), typed...)
		return copyValue, int64(len(copyValue))
	case map[string]any:
		result := make(map[string]any, len(typed))
		var total int64
		for key, child := range typed {
			copyChild, childBytes := cloneEventValue(child)
			result[key] = copyChild
			total += childBytes
		}
		return result, total
	case map[string]json.RawMessage:
		result := make(map[string]json.RawMessage, len(typed))
		var total int64
		for key, child := range typed {
			copyChild := append([]byte(nil), child...)
			result[key] = json.RawMessage(copyChild)
			total += int64(len(copyChild))
		}
		return result, total
	case []any:
		result := make([]any, len(typed))
		var total int64
		for index, child := range typed {
			copyChild, childBytes := cloneEventValue(child)
			result[index] = copyChild
			total += childBytes
		}
		return result, total
	case []json.RawMessage:
		result := make([]json.RawMessage, len(typed))
		var total int64
		for index, child := range typed {
			copyChild := append([]byte(nil), child...)
			result[index] = json.RawMessage(copyChild)
			total += int64(len(copyChild))
		}
		return result, total
	case string:
		// Fixed metadata strings are immutable and cannot be zeroed, but they
		// still consume heap budget. Large/body-bearing values use byte-backed
		// RawMessage or fragmentData so their storage is erasable.
		return typed, int64(len(typed))
	default:
		return value, 0
	}
}

func clearEventPayload(payload map[string]any) {
	for key, value := range payload {
		clearEventValue(value)
		payload[key] = nil
	}
}

func clearEventValue(value any) {
	switch typed := value.(type) {
	case fragmentData:
		clear(typed)
	case json.RawMessage:
		clear(typed)
	case []byte:
		clear(typed)
	case map[string]any:
		clearEventPayload(typed)
	case map[string]json.RawMessage:
		for key, child := range typed {
			clear(child)
			delete(typed, key)
		}
	case []any:
		for index, child := range typed {
			clearEventValue(child)
			typed[index] = nil
		}
	case []json.RawMessage:
		for index, child := range typed {
			clear(child)
			typed[index] = nil
		}
	}
}

func (h *Hub) replayLocked(session *hubSession, lastID uint64, hasLastID bool) []*storedEvent {
	if session == nil {
		return nil
	}
	if !hasLastID {
		return append([]*storedEvent(nil), session.events...)
	}
	if len(session.events) == 0 {
		before := session.nextSeq
		h.emitGapLocked(session, lastID, "resume_gap")
		h.emitSnapshotLocked(session)
		return h.replayAfterLocked(session, before)
	}
	oldest := session.events[0].seq
	if (oldest > 0 && lastID < oldest-1) || lastID > session.nextSeq {
		before := session.nextSeq
		reason := "resume_gap"
		if lastID > session.nextSeq {
			reason = "future_cursor"
		}
		h.emitGapLocked(session, lastID, reason)
		h.emitSnapshotLocked(session)
		// The cursor is outside the retained history. Return only the newly
		// generated gap and current snapshot; replaying from lastID here would
		// accidentally append the entire old ring after the recovery snapshot.
		return h.replayAfterLocked(session, before)
	}
	return h.replayAfterLocked(session, lastID)
}

// replayEnvelopeLocked gives the newly attached consumer a truthful current
// connection bit without mutating the retained event (which was created
// before this subscriber existed). All other envelope fields and payload
// members remain byte-for-byte equivalent to the retained projection.
func (h *Hub) replayEnvelopeLocked(session *hubSession, envelope EventEnvelope) EventEnvelope {
	if session == nil || envelope.Type != EventSessionSnapshot {
		return envelope
	}
	payload := make(map[string]any, len(envelope.Payload)+1)
	for key, value := range envelope.Payload {
		payload[key] = value
	}
	// Preserve the snapshot's own cursor/mode/expiry. Replacing the whole
	// metadata struct with a live read would advertise a future last_event_id
	// that is not covered by this replay envelope. Only the attach-local bit is
	// changed; reconnect emits a fresh snapshot first when mode must become dry.
	switch metadata := payload["metadata"].(type) {
	case SessionMetadata:
		metadata.Connected = true
		payload["metadata"] = metadata
	case map[string]any:
		metadata["connected"] = true
	}
	envelope.Payload = payload
	return envelope
}

func (h *Hub) replayAfterLocked(session *hubSession, lastID uint64) []*storedEvent {
	items := make([]*storedEvent, 0)
	for _, event := range session.events {
		if event.seq > lastID {
			items = append(items, event)
		}
	}
	return items
}

func (h *Hub) detach(sub *subscriber) {
	if h == nil || sub == nil || sub.session == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if sub.closed {
		// closeSessionLocked may have left one terminal delivery in the closed
		// channel for a connected consumer. If the HTTP context disconnected
		// before reading it, Subscription.Close must still release that clone.
		for {
			select {
			case envelope, open := <-sub.ch:
				if !open {
					return
				}
				h.releaseEnvelopeLocked(envelope)
			default:
				return
			}
		}
	}
	session := sub.session
	if _, ok := session.subscribers[sub]; !ok {
		sub.closed = true
		return
	}
	delete(session.subscribers, sub)
	sub.closed = true
	if len(session.subscribers) == 0 && !session.closed {
		if session.mode != ModeDry {
			session.mode = ModeDry
			h.emitSnapshotLocked(session)
		}
		if session.reconnectTimer != nil {
			session.reconnectTimer.Stop()
		}
		session.reconnectExpires = h.now().Add(ReconnectGrace)
		session.reconnectTimer = time.AfterFunc(ReconnectGrace, func() {
			h.expire(session.userID, session, EndSessionInvalid)
		})
	}
	for {
		select {
		case envelope, open := <-sub.ch:
			if !open {
				return
			}
			h.releaseEnvelopeLocked(envelope)
		default:
			close(sub.ch)
			return
		}
	}
}

// PublishTrace stores one complete safe trace projection and emits a
// trace_upsert event.  The function is non-blocking with respect to any SSE
// consumer; false means there was no active matching session or the bounded
// memory budget dropped the debug copy.
func (h *Hub) PublishTrace(userID int64, binding string, trace Trace) bool {
	if h == nil || userID <= 0 || binding == "" {
		return false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	session := h.sessions[userID]
	if h.closed || session == nil || session.closed || session.binding != binding || !h.validDeadlineLocked(session) {
		return false
	}
	h.touchLocked(session)
	return h.publishTraceLocked(session, trace)
}

// publishTraceLocked is the single request/observer projection path. Both the
// public in-memory publisher and the caller wrapper use it so fragment
// revisions, terminal merge rules, memory rollback, and gap recovery cannot
// drift between debug-only and actual-call traces.
func (h *Hub) publishTraceLocked(session *hubSession, trace Trace) bool {
	if h == nil || session == nil || session.closed {
		return false
	}
	id := safeIdentifier(trace.ID, maxTraceIdentifierBytes)
	if id == "" {
		if generated, err := opaqueID("trace_"); err == nil {
			id = generated
		} else {
			return false
		}
	}
	encoded := marshalJSON(trace.Payload)
	projection := RedactJSON(encoded, MaxTraceBytes)
	traceWire := projection.Data
	if len(traceWire) > MaxTraceBytes {
		h.dropTraceLocked(session, "trace_size")
		return false
	}
	old := session.traces[id]
	if trace.Merge && old != nil {
		traceWire = mergeTraceProjection(old.wire, traceWire)
		if !trace.Terminal {
			traceWire = preserveTerminalProjection(old.wire, traceWire)
		}
		if len(traceWire) > MaxTraceBytes {
			h.dropTraceLocked(session, "trace_merge_size")
			return false
		}
	}
	revision := trace.Revision
	if revision == 0 {
		revision = defaultTraceRevision
		if old != nil {
			if old.revision == ^uint64(0) {
				return false
			}
			revision = old.revision + 1
		}
	}
	if old != nil && revision <= old.revision {
		// Observer/final wrapper updates may omit a caller revision. Once a
		// projection was split into fragments, the final fragment consumes more
		// than one wire revision; allocate the next monotonic revision instead of
		// rejecting a valid request-scoped merge.
		if old.revision == ^uint64(0) {
			return false
		}
		revision = old.revision + 1
	}
	if old == nil && len(session.traces) >= MaxTraces {
		if !h.evictOldestTraceLocked(session) {
			h.dropTraceLocked(session, "trace_active_cap")
			return false
		}
		// A terminal trace eviction is a cursor discontinuity. Tell attached
		// consumers to discard their local copy and apply the current snapshot.
		h.emitGapLocked(session, session.nextSeq, "trace_evicted")
		h.emitSnapshotLocked(session)
	}
	delta := int64(len(traceWire))
	if old != nil {
		delta -= int64(len(old.wire))
	}
	if !h.reserveBytesLocked(session, delta, id) {
		h.dropTraceLocked(session, "trace_memory")
		return false
	}
	if old == nil {
		session.traceOrder = append(session.traceOrder, id)
	}
	terminal := trace.Terminal
	if old != nil {
		terminal = old.terminal || terminal
	}
	record := &traceRecord{id: id, revision: revision, wire: append([]byte(nil), traceWire...), terminal: terminal}
	session.traces[id] = record
	if !h.emitTraceLocked(session, id, revision) {
		if old == nil {
			delete(session.traces, id)
			if len(session.traceOrder) != 0 && session.traceOrder[len(session.traceOrder)-1] == id {
				session.traceOrder = session.traceOrder[:len(session.traceOrder)-1]
			}
			session.bytes -= int64(len(traceWire))
			h.totalBytes -= int64(len(traceWire))
		} else {
			session.traces[id] = old
			session.bytes -= int64(len(traceWire))
			session.bytes += int64(len(old.wire))
			h.totalBytes -= int64(len(traceWire))
			h.totalBytes += int64(len(old.wire))
		}
		clear(record.wire)
		if old != nil {
			// The caller has already reached a logical return point. If the
			// terminal response projection could not be retained (most commonly
			// because a slow subscriber consumed the remaining copy budget), do
			// not leave the received trace permanently active. Replace it with a
			// small terminal/incomplete marker; this keeps the trace evictable and
			// avoids retaining a large response body solely because publication
			// failed.
			h.markIncompleteTraceLocked(session, old)
		}
		if h.recoveryDepth == 0 {
			h.recoveryDepth++
			h.emitGapLocked(session, session.nextSeq, "fragment_drop")
			h.emitSnapshotLocked(session)
			h.recoveryDepth--
		}
		return false
	}
	if old != nil {
		// The retained event ring owns detached copies. Once the new complete
		// projection is published, clear the superseded trace backing array so
		// an old revision cannot remain reachable through heap history.
		clear(old.wire)
	}
	return true
}

func (h *Hub) markIncompleteTraceLocked(session *hubSession, trace *traceRecord) {
	if h == nil || session == nil || trace == nil || trace.terminal {
		if trace != nil {
			trace.terminal = true
		}
		return
	}
	fallback := marshalJSON(map[string]any{"id": trace.id, "terminal": "incomplete", "incomplete": true, "debug_copy_dropped": true})
	if len(fallback) == 0 {
		fallback = []byte(`{"terminal":"incomplete"}`)
	}
	if len(fallback) > len(trace.wire) {
		// Even a tiny received projection must not remain visibly nonterminal.
		// The fixed marker is intentionally smaller than the full diagnostic.
		fallback = []byte(`{"terminal":"incomplete"}`)
	}
	oldLen := len(trace.wire)
	oldWire := trace.wire
	delta := int64(len(fallback) - oldLen)
	if delta > 0 {
		if !h.reserveBytesLocked(session, delta, trace.id) {
			// A pathological custom cap can leave no room even for the fixed
			// incomplete marker.  Never publish an unaccounted marker or retain
			// the misleading received projection; drop this copy and let the
			// caller's gap/snapshot recovery make the loss explicit.
			if current := session.traces[trace.id]; current == trace {
				delete(session.traces, trace.id)
				for index, id := range session.traceOrder {
					if id == trace.id {
						session.traceOrder = append(session.traceOrder[:index], session.traceOrder[index+1:]...)
						break
					}
				}
				h.removeTraceEventsLocked(session, trace.id)
				session.bytes -= int64(oldLen)
				if session.bytes < 0 {
					session.bytes = 0
				}
				h.totalBytes -= int64(oldLen)
				if h.totalBytes < 0 {
					h.totalBytes = 0
				}
				clear(oldWire)
				trace.wire = nil
			}
			session.dropped++
			return
		}
	} else {
		session.bytes += delta
		h.totalBytes += delta
	}
	trace.wire = append([]byte(nil), fallback...)
	clear(oldWire)
	clear(fallback)
	trace.terminal = true
	if trace.revision == ^uint64(0) {
		return
	}
	trace.revision++
	if h.recoveryDepth == 0 {
		h.recoveryDepth++
		_ = h.emitTraceLocked(session, trace.id, trace.revision)
		h.recoveryDepth--
	}
}

// Publish is an error-returning alias useful to integration code that wants
// to distinguish a missing session from a non-blocking queue drop.
func (h *Hub) Publish(userID int64, binding string, trace Trace) error {
	if h == nil || userID <= 0 || binding == "" {
		return ErrInvalid
	}
	if !h.PublishTrace(userID, binding, trace) {
		if _, ok := h.Metadata(userID, binding); !ok {
			return ErrNotFound
		}
		return ErrTooLarge
	}
	return nil
}

func (h *Hub) reserveBytesLocked(session *hubSession, delta int64, protectedTraceID string) bool {
	if delta <= 0 {
		session.bytes += delta
		h.totalBytes += delta
		return session.bytes <= h.maxSessionBytes && h.totalBytes+h.requestCopyBytes <= h.maxHubBytes
	}
	for session.bytes+delta > h.maxSessionBytes || h.totalBytes+h.requestCopyBytes+delta > h.maxHubBytes {
		if h.evictOldestTraceLockedExcept(session, protectedTraceID) {
			h.emitGapLocked(session, session.nextSeq, "trace_evicted")
			h.emitSnapshotLocked(session)
			continue
		}
		if len(session.events) == 0 {
			break
		}
		h.evictOldestEventLocked(session)
	}
	if session.bytes+delta > h.maxSessionBytes || h.totalBytes+h.requestCopyBytes+delta > h.maxHubBytes {
		return false
	}
	session.bytes += delta
	h.totalBytes += delta
	return true
}

func (h *Hub) evictOldestEventLocked(session *hubSession) {
	if len(session.events) == 0 {
		return
	}
	event := session.events[0]
	// Clear the stale slot in the retained backing array before shortening the
	// slice so an evicted body cannot remain reachable until a future append.
	session.events[0] = nil
	session.events = session.events[1:]
	h.releaseEventRefLocked(event)
	session.dropped++
}

func (h *Hub) evictOldestTraceLocked(session *hubSession) bool {
	return h.evictOldestTraceLockedExcept(session, "")
}

func (h *Hub) evictOldestTraceLockedExcept(session *hubSession, protectedTraceID string) bool {
	for scan := len(session.traceOrder); scan > 0; scan-- {
		id := session.traceOrder[0]
		session.traceOrder = session.traceOrder[1:]
		trace := session.traces[id]
		if trace == nil {
			continue
		}
		if id == protectedTraceID || !trace.terminal {
			// Keep active work in the order list; a later terminal trace may
			// still be the oldest evictable copy.
			session.traceOrder = append(session.traceOrder, id)
			continue
		}
		delete(session.traces, id)
		session.bytes -= int64(len(trace.wire))
		h.totalBytes -= int64(len(trace.wire))
		h.removeTraceEventsLocked(session, id)
		clear(trace.wire)
		return true
	}
	return false
}

// removeTraceEventsLocked prevents a retained pre-eviction upsert from
// resurrecting a debug copy after a gap/snapshot recovery. Older snapshots
// are deliberately left as historical envelopes; the gap tells consumers to
// discard them before applying the fresh snapshot.
func (h *Hub) removeTraceEventsLocked(session *hubSession, traceID string) {
	if session == nil || traceID == "" || len(session.events) == 0 {
		return
	}
	originalLen := len(session.events)
	kept := session.events[:0]
	for index, event := range session.events {
		if event != nil && event.envelope.Type == EventTraceUpsert && event.envelope.TraceID == traceID {
			session.events[index] = nil
			h.releaseEventRefLocked(event)
			continue
		}
		kept = append(kept, event)
	}
	for index := len(kept); index < originalLen; index++ {
		session.events[index] = nil
	}
	session.events = kept
}

func (h *Hub) dropTraceLocked(session *hubSession, reason string) {
	if session == nil {
		return
	}
	session.dropped++
	before := session.nextSeq
	h.emitGapLocked(session, before, reason)
	h.emitSnapshotLocked(session)
}

func (h *Hub) publishObserverDrop(userID int64, sessionID string, generation uint64, dropped uint64) {
	if h == nil || userID <= 0 || sessionID == "" || generation == 0 || dropped == 0 {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	session := h.sessions[userID]
	if h.closed || session == nil || session.closed || session.id != sessionID || session.generation != generation {
		return
	}
	session.dropped += dropped
	h.emitGapLocked(session, session.nextSeq, "observer_queue")
	h.emitSnapshotLocked(session)
}

func (h *Hub) emitSnapshotLocked(session *hubSession) bool {
	if session == nil || session.closed {
		return false
	}
	payload := h.snapshotPayloadLocked(session)
	if session.dropped != 0 {
		payload["dropped"] = session.dropped
	}
	if h.eventFitsLocked(session, EventSessionSnapshot, "", 0, payload) {
		if h.emitLocked(session, EventSessionSnapshot, "", 0, payload) {
			return true
		}
	}
	raw := marshalJSON(payload)
	if !h.emitFragmentsLocked(session, EventSessionSnapshot, "", 0, raw) {
		if h.snapshotCompactionDepth == 0 && h.compactForSnapshotLocked(session) {
			h.snapshotCompactionDepth++
			ok := h.emitSnapshotLocked(session)
			h.snapshotCompactionDepth--
			session.suppressSubscriberDelivery = false
			return ok
		}
		// A snapshot that cannot be retained within the session budget is an
		// explicit copy drop. The next successful bounded event still carries
		// dropped metadata; no incomplete marker is presented as a snapshot.
		session.dropped++
		return false
	}
	return true
}

// compactForSnapshotLocked drops superseded retained event history before a
// large current snapshot is emitted. The trace map remains authoritative;
// only old upserts/snapshots and queued subscriber copies are discarded. A
// previously emitted gap is retained as the recovery boundary. Existing
// subscribers receive that gap, while the snapshot itself is retained for a
// later replay rather than multiplying a near-4MiB projection into every
// queue. This keeps the session cap provable even for a 768KiB trace.
func (h *Hub) compactForSnapshotLocked(session *hubSession) bool {
	if h == nil || session == nil || session.closed {
		return false
	}
	for sub := range session.subscribers {
		for {
			select {
			case envelope, open := <-sub.ch:
				if !open {
					goto subscriberQueueDrained
				}
				h.releaseEnvelopeLocked(envelope)
				session.dropped++
			default:
				goto subscriberQueueDrained
			}
		}
	subscriberQueueDrained:
	}
	kept := make([]*storedEvent, 0, 1)
	for _, event := range session.events {
		if event == nil {
			continue
		}
		if event.envelope.Type == EventGap {
			kept = append(kept, event)
			continue
		}
		h.releaseEventRefLocked(event)
	}
	for index := range session.events {
		session.events[index] = nil
	}
	session.events = kept
	// The compaction itself is a cursor discontinuity. Existing subscribers
	// must observe a small recovery marker before the replacement snapshot;
	// their old queued body-bearing copies were already released above.
	if len(session.subscribers) != 0 {
		h.emitGapLocked(session, session.nextSeq, "resume_gap")
		// A 768KiB snapshot cannot be cloned into one or two existing queues
		// under the 4MiB session budget. Close each stream immediately after its
		// gap; the ring retains the complete snapshot group and the client has a
		// deterministic Last-Event-ID reconnect path instead of seeing a gap
		// followed by unrelated future upserts.
		for sub := range session.subscribers {
			delete(session.subscribers, sub)
			sub.closed = true
			close(sub.ch)
		}
		if session.mode != ModeDry {
			session.mode = ModeDry
		}
		if session.firstAttach {
			if session.reconnectTimer != nil {
				session.reconnectTimer.Stop()
			}
			session.reconnectExpires = h.now().Add(ReconnectGrace)
			session.reconnectTimer = time.AfterFunc(ReconnectGrace, func() {
				h.expire(session.userID, session, EndSessionInvalid)
			})
		}
	}
	session.suppressSubscriberDelivery = true
	return true
}

func (h *Hub) snapshotPayloadLocked(session *hubSession) map[string]any {
	traces := make([]json.RawMessage, 0, len(session.traceOrder))
	for _, id := range session.traceOrder {
		if trace := session.traces[id]; trace != nil {
			traces = append(traces, json.RawMessage(trace.wire))
		}
	}
	metadata := h.metadataLocked(session)
	metadata.Connected = metadata.Connected || session.snapshotConnectedOverride
	// The metadata cursor is the last sequence covered by this complete
	// snapshot, not the sequence immediately before its first fragment. A
	// short fixed-point pass accounts for the decimal width of the cursor and
	// the resulting fragment count; every fragment then carries the same
	// recovery baseline so consumers can resume strictly after the group.
	metadata.LastEventID = session.nextSeq + 1
	payload := map[string]any{"metadata": metadata, "traces": traces}
	if session.dropped != 0 {
		payload["dropped"] = session.dropped
	}
	for pass := 0; pass < 4; pass++ {
		raw := marshalJSON(payload)
		count := (len(raw) + MaxFragmentBytes - 1) / MaxFragmentBytes
		if count < 1 {
			count = 1
		}
		last := session.nextSeq + uint64(count)
		if metadata.LastEventID == last {
			break
		}
		metadata.LastEventID = last
		payload["metadata"] = metadata
	}
	return payload
}

func (h *Hub) emitTraceLocked(session *hubSession, id string, revision uint64) bool {
	trace := session.traces[id]
	if trace == nil {
		return false
	}
	payload := map[string]any{"trace": json.RawMessage(trace.wire)}
	if session.dropped != 0 {
		payload["dropped"] = session.dropped
	}
	if h.eventFitsLocked(session, EventTraceUpsert, id, revision, payload) {
		return h.emitLocked(session, EventTraceUpsert, id, revision, payload)
	}
	if !h.emitFragmentsLocked(session, EventTraceUpsert, id, revision, trace.wire) {
		session.dropped++
		return false
	}
	count := (len(trace.wire) + MaxFragmentBytes - 1) / MaxFragmentBytes
	if count > 1 {
		trace.revision = revision + uint64(count-1)
	}
	return true
}

// eventFitsLocked checks the serialized wire limit without retaining a second
// projection. It is used before selecting the documented fragment form for a
// large trace/snapshot.
func (h *Hub) eventFitsLocked(session *hubSession, typ EventType, traceID string, revision uint64, payload map[string]any) bool {
	if session == nil {
		return false
	}
	cloned, _ := cloneEventPayload(payload)
	envelope := EventEnvelope{
		Version: 1, Seq: session.nextSeq + 1, Type: typ, SessionID: session.id,
		TraceID: safeIdentifier(traceID, maxTraceIdentifierBytes), Revision: revision,
		At: h.now().Unix(), Payload: cloned,
	}
	wire, err := json.Marshal(envelope)
	clearEventPayload(cloned)
	return err == nil && len(wire) <= MaxEventBytes
}

// emitFragmentsLocked uses the existing trace_upsert/session_snapshot event
// types. The payload's explicit fragment object is self-describing and can be
// consumed without guessing a patch: clients group by kind+trace_id+sha256,
// requires contiguous index/count/total_bytes, decodes base64url JSON bytes,
// verifies the digest, and applies the assembled complete projection only
// after all fragments arrive. Trace fragment envelope revisions are strictly
// increasing from baseRevision; event seq remains the global ordering guard.
func (h *Hub) emitFragmentsLocked(session *hubSession, typ EventType, traceID string, baseRevision uint64, raw []byte) bool {
	if session == nil || len(raw) == 0 {
		return false
	}
	if typ == EventSessionSnapshot {
		// Snapshot assembly is a temporary projection built from the
		// authoritative trace wires. Fragment payloads own their copies; the
		// temporary assembled buffer must not remain readable after emission.
		defer clear(raw)
	}
	digest := sha256.Sum256(raw)
	digestText := hex.EncodeToString(digest[:])
	count := (len(raw) + MaxFragmentBytes - 1) / MaxFragmentBytes
	if count < 1 || count > MaxRetainedEvents {
		return false
	}
	kind := "snapshot"
	if typ == EventTraceUpsert {
		kind = "trace"
	}
	// Build every retained event and its complete wire before changing the
	// session sequence, ring, or subscriber queues. The whole group is then
	// committed in one lock-held transaction; any marshal/budget failure clears
	// all temporary payloads and leaves no partial fragment group behind.
	events := make([]*storedEvent, 0, count)
	var projected int64
	startSeq := session.nextSeq
	for index := 0; index < count; index++ {
		start := index * MaxFragmentBytes
		end := start + MaxFragmentBytes
		if end > len(raw) {
			end = len(raw)
		}
		fragment := map[string]any{
			"kind": "snapshot", "encoding": "base64url-json", "index": index,
			"count": count, "total_bytes": len(raw), "sha256": digestText,
			"data": fragmentData(raw[start:end]),
		}
		if kind == "trace" {
			fragment["kind"] = "trace"
		}
		revision := uint64(0)
		if typ == EventTraceUpsert {
			if baseRevision > ^uint64(0)-uint64(index) {
				return false
			}
			revision = baseRevision + uint64(index)
		}
		payload := map[string]any{"fragment": fragment}
		cloned, payloadBytes := cloneEventPayload(payload)
		envelope := EventEnvelope{Version: 1, Seq: startSeq + uint64(index) + 1, Type: typ, SessionID: session.id, TraceID: safeIdentifier(traceID, maxTraceIdentifierBytes), Revision: revision, At: h.now().Unix(), Payload: cloned}
		wire, err := json.Marshal(envelope)
		if err != nil || len(wire) > MaxEventBytes {
			clearEventPayload(cloned)
			for _, built := range events {
				clearEventPayload(built.envelope.Payload)
				clear(built.wire)
			}
			return false
		}
		storedWire := append([]byte(nil), wire...)
		events = append(events, &storedEvent{envelope: envelope, wire: storedWire, seq: envelope.Seq, size: int64(len(wire)) + payloadBytes, refs: 1, session: session})
		projected += int64(len(wire)) + payloadBytes
	}
	if session.bytes+projected > h.maxSessionBytes || h.totalBytes+h.requestCopyBytes+projected > h.maxHubBytes {
		for _, built := range events {
			clearEventPayload(built.envelope.Payload)
			clear(built.wire)
		}
		return false
	}
	// A group must either be queued in full for a subscriber or not queued at
	// all. Ring retention remains complete even when a slow queue is skipped;
	// its dropped count forces a later gap/snapshot recovery.
	type subscriberPlan struct {
		sub  *subscriber
		drop bool
	}
	plans := make([]subscriberPlan, 0, len(session.subscribers))
	var plannedDelivery int64
	for sub := range session.subscribers {
		if sub.closed || session.suppressSubscriberDelivery {
			continue
		}
		var deliveryBytes int64
		for _, event := range events {
			cloned, payloadBytes := cloneEventPayload(event.envelope.Payload)
			clearEventPayload(cloned)
			deliveryBytes += payloadBytes
		}
		drop := session.suppressSubscriberDelivery || len(sub.ch)+count > MaxSubscriberQueue || session.bytes+projected+plannedDelivery+deliveryBytes > h.maxSessionBytes || h.totalBytes+h.requestCopyBytes+projected+plannedDelivery+deliveryBytes > h.maxHubBytes
		if !drop {
			plannedDelivery += deliveryBytes
		}
		plans = append(plans, subscriberPlan{sub: sub, drop: drop})
	}
	for len(session.events)+count > MaxRetainedEvents {
		h.evictOldestEventLocked(session)
	}
	for _, event := range events {
		session.events = append(session.events, event)
		session.bytes += event.size
		h.totalBytes += event.size
	}
	session.nextSeq = startSeq + uint64(count)
	for _, plan := range plans {
		if plan.drop {
			session.dropped += uint64(count)
			h.dropSubscriberLocked(session, plan.sub, events[len(events)-1].seq, "debug_copy_drop")
			continue
		}
		prepared := make([]EventEnvelope, 0, len(events))
		for _, event := range events {
			clonedPayload, payloadBytes := cloneEventPayload(event.envelope.Payload)
			event.refs++
			delivery := &eventDelivery{hub: h, event: event, session: event.session, subscriber: plan.sub, payload: clonedPayload, payloadSize: payloadBytes}
			envelope := event.envelope
			envelope.Payload = clonedPayload
			envelope.retained = delivery
			prepared = append(prepared, envelope)
			session.bytes += payloadBytes
			h.totalBytes += payloadBytes
			plan.sub.queuedBytes += payloadBytes
		}
		for _, envelope := range prepared {
			plan.sub.ch <- envelope
		}
	}
	return true
}

func (h *Hub) emitGapLocked(session *hubSession, lastID uint64, reason string) {
	payload := map[string]any{"after": lastID, "reason": reason}
	h.emitLocked(session, EventGap, "", 0, payload)
}

// dropSubscriberLocked makes a per-subscriber debug-copy loss explicit. The
// slow consumer receives one bounded gap envelope and then EOF; other
// subscribers continue to receive the complete retained group. The gap is
// retained when budget permits so a reconnect can obtain the same recovery
// signal. A tiny detached delivery is the last-resort notification when the
// ring is completely full of active traces.
func (h *Hub) dropSubscriberLocked(session *hubSession, sub *subscriber, after uint64, reason string) {
	if h == nil || session == nil || sub == nil || sub.closed {
		return
	}
	for {
		select {
		case envelope, open := <-sub.ch:
			if !open {
				goto drained
			}
			h.releaseEnvelopeLocked(envelope)
		default:
			goto drained
		}
	}
drained:
	var envelope EventEnvelope
	before := session.nextSeq
	previousSuppress := session.suppressSubscriberDelivery
	session.suppressSubscriberDelivery = true
	h.emitGapLocked(session, after, reason)
	session.suppressSubscriberDelivery = previousSuppress
	if session.nextSeq > before && len(session.events) != 0 {
		event := session.events[len(session.events)-1]
		if event != nil && event.envelope.Type == EventGap {
			envelope = h.deliveryEnvelopeLocked(event)
		}
	}
	if envelope.retained == nil {
		// There may be no retained budget left when active traces occupy the
		// whole session. A detached fixed gap is allowed only when its actual
		// payload clone fits the same session/global byte accounting; otherwise
		// close without inventing an unaccounted body and let reconnect recovery
		// use the retained ring (or return capacity).
		payload, payloadBytes := cloneEventPayload(map[string]any{"after": after, "reason": reason})
		if session.bytes+payloadBytes <= h.maxSessionBytes && h.totalBytes+h.requestCopyBytes+payloadBytes <= h.maxHubBytes {
			if session.nextSeq <= before {
				session.nextSeq++
			}
			session.bytes += payloadBytes
			h.totalBytes += payloadBytes
			delivery := &eventDelivery{hub: h, session: session, subscriber: sub, payload: payload, payloadSize: payloadBytes}
			envelope = EventEnvelope{Version: 1, Seq: session.nextSeq, Type: EventGap, SessionID: session.id, At: h.now().Unix(), Payload: payload, retained: delivery}
			sub.queuedBytes += payloadBytes
		} else {
			clearEventPayload(payload)
			// A session whose active trace already consumes every byte cannot
			// manufacture another retained body. Still notify the dropped stream
			// with the fixed, payload-empty gap envelope; it carries no captured
			// bytes and therefore needs no budget/ref release. Reconnect then
			// obtains the authoritative snapshot (or a capacity error).
			envelope = EventEnvelope{
				Version: 1, Seq: session.nextSeq, Type: EventGap,
				SessionID: session.id, At: h.now().Unix(), Payload: map[string]any{},
			}
		}
	}
	delete(session.subscribers, sub)
	sub.closed = true
	if envelope.Type == EventGap {
		sub.ch <- envelope
	}
	close(sub.ch)
	if len(session.subscribers) == 0 && !session.closed {
		if session.mode != ModeDry {
			session.mode = ModeDry
			h.emitSnapshotLocked(session)
		}
		if session.reconnectTimer != nil {
			session.reconnectTimer.Stop()
		}
		session.reconnectExpires = h.now().Add(ReconnectGrace)
		session.reconnectTimer = time.AfterFunc(ReconnectGrace, func() {
			h.expire(session.userID, session, EndSessionInvalid)
		})
	}
}

func (h *Hub) emitLocked(session *hubSession, typ EventType, traceID string, revision uint64, payload map[string]any) bool {
	if session == nil || session.closed {
		return false
	}
	if typ != EventSessionSnapshot && typ != EventTraceUpsert && typ != EventGap && typ != EventSessionEnd {
		return false
	}
	session.nextSeq++
	clonedPayload, payloadBytes := cloneEventPayload(payload)
	envelope := EventEnvelope{
		Version: 1, Seq: session.nextSeq, Type: typ, SessionID: session.id,
		TraceID: safeIdentifier(traceID, maxTraceIdentifierBytes), Revision: revision,
		At: h.now().Unix(), Payload: clonedPayload,
	}
	wire, err := json.Marshal(envelope)
	if err != nil {
		clearEventPayload(clonedPayload)
		return false
	}
	if len(wire) > MaxEventBytes {
		// A projection is never replaced by a misleading truncation marker. The
		// caller uses emitFragmentsLocked for the documented large-projection
		// form; this branch is only a defensive failure for a non-fragment event.
		clearEventPayload(clonedPayload)
		session.dropped++
		if typ != EventGap {
			h.emitGapLocked(session, envelope.Seq-1, "event_size")
		}
		return false
	}
	size := int64(len(wire)) + payloadBytes
	traceEvicted := false
	for session.bytes+size > h.maxSessionBytes {
		if h.recoveryDepth == 0 && h.evictOldestTraceLockedExcept(session, traceID) {
			traceEvicted = true
			continue
		}
		if len(session.events) == 0 {
			break
		}
		h.evictOldestEventLocked(session)
	}
	for h.totalBytes+h.requestCopyBytes+size > h.maxHubBytes {
		if h.recoveryDepth == 0 && h.evictOldestTraceLockedExcept(session, traceID) {
			traceEvicted = true
			continue
		}
		if len(session.events) == 0 {
			break
		}
		h.evictOldestEventLocked(session)
	}
	if traceEvicted && typ != EventGap && typ != EventSessionEnd {
		h.recoveryDepth++
		h.emitGapLocked(session, session.nextSeq, "trace_evicted")
		h.emitSnapshotLocked(session)
		h.recoveryDepth--
	}
	if session.bytes+size > h.maxSessionBytes || h.totalBytes+h.requestCopyBytes+size > h.maxHubBytes {
		session.dropped++
		clearEventPayload(clonedPayload)
		return false
	}
	event := &storedEvent{envelope: envelope, wire: append([]byte(nil), wire...), seq: envelope.Seq, size: size, refs: 1, session: session}
	session.events = append(session.events, event)
	session.bytes += size
	h.totalBytes += size
	if len(session.events) > MaxRetainedEvents {
		h.evictOldestEventLocked(session)
	}
	for sub := range session.subscribers {
		if sub.closed || session.suppressSubscriberDelivery {
			continue
		}
		delivery := h.deliveryEnvelopeLocked(event)
		if delivery.retained == nil {
			session.dropped++
			h.dropSubscriberLocked(session, sub, event.seq, "debug_copy_drop")
			continue
		}
		delivery.retained.subscriber = sub
		select {
		case sub.ch <- delivery:
			sub.queuedBytes += delivery.retained.payloadSize
		default:
			if typ == EventSessionEnd {
				// Terminal state is more important than an old debug copy.  Make
				// room for it without ever blocking the request/session path.
				select {
				case old := <-sub.ch:
					h.releaseEnvelopeLocked(old)
				default:
				}
				select {
				case sub.ch <- delivery:
					sub.queuedBytes += delivery.retained.payloadSize
				default:
					h.releaseEnvelopeLocked(delivery)
				}
			} else {
				session.dropped++
				h.releaseEnvelopeLocked(delivery)
				h.dropSubscriberLocked(session, sub, event.seq, "debug_copy_drop")
			}
		}
	}
	return true
}

func (h *Hub) closeSessionLocked(userID int64, session *hubSession, reason SessionEndReason) {
	if session == nil || session.closed {
		return
	}
	// Session-end reasons are part of the closed event vocabulary, not an
	// arbitrary lifecycle log field.  Hooks may be wired from multiple
	// packages, so unknown values fail closed to the stable invalid-session
	// reason instead of reaching an event consumer.
	reason = normalizedSessionEndReason(reason)
	session.mode = ModeDry
	for key, item := range h.confirmations {
		if item.userID == userID && item.sessionID == session.id {
			delete(h.confirmations, key)
		}
	}
	session.hasConfirmation = false
	clear(session.confirmationDigest[:])
	if session.firstTimer != nil {
		session.firstTimer.Stop()
	}
	if session.idleTimer != nil {
		session.idleTimer.Stop()
	}
	if session.absTimer != nil {
		session.absTimer.Stop()
	}
	if session.reconnectTimer != nil {
		session.reconnectTimer.Stop()
	}
	session.reconnectExpires = time.Time{}
	// Free retained history and every buffered subscriber copy before reserving
	// session_end. This keeps the terminal lifecycle event deliverable even
	// when active traces filled the session budget.
	for sub := range session.subscribers {
		for {
			select {
			case envelope, open := <-sub.ch:
				if !open {
					goto subscriberDrained
				}
				h.releaseEnvelopeLocked(envelope)
			default:
				goto subscriberDrained
			}
		}
	subscriberDrained:
	}
	for _, event := range session.events {
		h.forceCloseEventLocked(event)
	}
	session.events = nil
	for _, trace := range session.traces {
		if trace != nil {
			clear(trace.wire)
			session.bytes -= int64(len(trace.wire))
			h.totalBytes -= int64(len(trace.wire))
		}
	}
	session.traces = nil
	session.traceOrder = nil
	// Delivery clones that were already taken from a subscription remain
	// accounted until EventEnvelope.Release. They cannot receive further body
	// events after this point; reset the closed session's local append budget.
	session.bytes = 0
	payload := map[string]any{"reason": string(reason)}
	h.emitLocked(session, EventSessionEnd, "", 0, payload)
	session.closed = true
	for sub := range session.subscribers {
		sub.closed = true
		var terminal EventEnvelope
		hasTerminal := false
		for {
			select {
			case envelope, open := <-sub.ch:
				if !open {
					goto terminalReady
				}
				if envelope.Type == EventSessionEnd {
					if hasTerminal {
						h.releaseEnvelopeLocked(terminal)
					}
					terminal = envelope
					hasTerminal = true
				} else {
					h.releaseEnvelopeLocked(envelope)
				}
			default:
				goto terminalReady
			}
		}
	terminalReady:
		if hasTerminal {
			sub.ch <- terminal
		}
		close(sub.ch)
	}
	session.subscribers = nil
	// SafeObserver queues may still be draining after this session leaves the
	// authoritative map. Keep the process-wide lease until each sidecar's
	// close hook runs; only the session-local admission counter is detached.
	session.observerActive = 0
	for _, event := range session.events {
		h.forceCloseEventLocked(event)
	}
	session.events = nil
	if h.totalBytes < 0 {
		h.totalBytes = 0
	}
	delete(h.sessions, userID)
}

// discardSessionAllocationLocked releases a not-yet-published replacement
// session after Start's snapshot preflight fails. It is intentionally separate
// from closeSessionLocked: no subscriber or session_end is exposed for a
// session that never became authoritative.
func (h *Hub) discardSessionAllocationLocked(session *hubSession) {
	if h == nil || session == nil {
		return
	}
	for _, event := range session.events {
		h.forceCloseEventLocked(event)
	}
	for _, trace := range session.traces {
		if trace == nil {
			continue
		}
		clear(trace.wire)
	}
	session.events = nil
	session.traces = nil
	session.traceOrder = nil
	session.bytes = 0
}

func (h *Hub) forceCloseEventLocked(event *storedEvent) {
	if event == nil || event.closed {
		return
	}
	event.closed = true
	if event.envelope.Payload != nil {
		clearEventPayload(event.envelope.Payload)
	}
	clear(event.wire)
	if event.session != nil {
		event.session.bytes -= event.size
		if event.session.bytes < 0 {
			event.session.bytes = 0
		}
	}
	h.totalBytes -= event.size
	if h.totalBytes < 0 {
		h.totalBytes = 0
	}
	event.wire = nil
	event.size = 0
	event.refs = 0
}

func normalizedSessionEndReason(reason SessionEndReason) SessionEndReason {
	switch reason {
	case EndStopped, EndReplaced, EndIdleTimeout, EndMaxAge, EndSessionInvalid,
		EndLogout, EndBanned, EndDeleted, EndShutdown, EndCapacity:
		return reason
	default:
		return EndSessionInvalid
	}
}

func opaqueID(prefix string) (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString(raw[:])
	clear(raw[:])
	return prefix + encoded, nil
}

// Validator returns the configured dry-run validator for integration wiring.
func (h *Hub) Validator() DryRunValidator {
	if h == nil {
		return nil
	}
	return h.validator
}

// CharityValidator returns the optional logical-only validator for the
// [公益] namespace. Keeping it separate from the owner-scoped personal seam
// prevents a charity request from accidentally falling through to a personal
// model query (or, worse, the physical charity route resolver).
func (h *Hub) CharityValidator() DryRunValidator {
	if h == nil {
		return nil
	}
	return h.charityValidator
}

// MapDryRunError returns the configured stable error mapper.
func (h *Hub) MapDryRunError(err error) (string, string) {
	if h != nil && h.mapDryError != nil {
		return h.mapDryError(err)
	}
	return "internal", "request could not be validated"
}

// Dropped returns the current process-local dropped debug-copy count for a
// user's active session.  It intentionally contains no body or identity.
func (h *Hub) Dropped(userID int64) uint64 {
	if h == nil || userID <= 0 {
		return 0
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if session := h.sessions[userID]; session != nil {
		return session.dropped
	}
	return 0
}
