package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	DefaultOAuthStateTTL = 10 * time.Minute
	maxOAuthStates       = 4096
	maxStateClockSkew    = 30 * time.Second
	maxStateBindingBytes = 256
)

type stateClock func() time.Time

type oauthStatePayload struct {
	Version int    `json:"v"`
	Nonce   string `json:"n"`
	Station string `json:"s"`
	Intent  string `json:"i"`
	Bind    string `json:"b,omitempty"` // opaque session binding (e.g. session token hash); empty for login states
	Issued  int64  `json:"iat"`
	Expires int64  `json:"exp"`
}

type stateRecord struct {
	expires int64
}

// StateManager creates signed, short-lived, one-use OAuth states. The HMAC
// key is generated in process memory and is never persisted or exposed.
type StateManager struct {
	mu      sync.Mutex
	key     []byte
	ttl     time.Duration
	max     int
	now     stateClock
	pending map[[32]byte]stateRecord
	closed  bool
}

// NewStateManager creates an ephemeral state signer with a bounded TTL.
func NewStateManager(ttl time.Duration) (*StateManager, error) {
	if ttl <= 0 || ttl > time.Hour {
		return nil, fmt.Errorf("oauth state TTL is invalid")
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		clear(key)
		return nil, fmt.Errorf("oauth state signer unavailable")
	}
	return &StateManager{key: key, ttl: ttl, max: maxOAuthStates, now: time.Now, pending: make(map[[32]byte]stateRecord)}, nil
}

// NewStateManagerWithKey is intended for deterministic tests or a process
// bootstrapper that owns an ephemeral key. The key is copied and never
// returned.
func NewStateManagerWithKey(key []byte, ttl time.Duration) (*StateManager, error) {
	if len(key) < 32 || ttl <= 0 || ttl > time.Hour {
		return nil, fmt.Errorf("oauth state signer configuration is invalid")
	}
	copyKey := append([]byte(nil), key...)
	return &StateManager{key: copyKey, ttl: ttl, max: maxOAuthStates, now: time.Now, pending: make(map[[32]byte]stateRecord)}, nil
}

// SetClock is a test/maintenance hook. The supplied function must be safe for
// concurrent calls; production code should leave the real clock in place.
func (m *StateManager) SetClock(now func() time.Time) error {
	if m == nil || now == nil {
		return ErrStateInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrStateInvalid
	}
	m.now = now
	return nil
}

func (m *StateManager) purgeLocked(now time.Time) {
	nanos := now.UnixNano()
	for nonce, record := range m.pending {
		if record.expires <= nanos {
			delete(m.pending, nonce)
		}
	}
}

// Issue creates a state bound to station and intent. Only the signed opaque
// value is returned; its nonce is retained as a hash for replay detection.
// The state carries no session binding (see IssueBound for elevation flows).
func (m *StateManager) Issue(station, intent string) (string, error) {
	return m.issueInternal(station, intent, "")
}

// IssueBound issues a state additionally bound to an opaque session binding
// (the hash of the current user session token). A state issued here can only
// be consumed by ConsumeBound/ConsumeAny against the exact same binding, so
// a login state can never be replayed into an elevation flow and vice versa.
func (m *StateManager) IssueBound(station, intent, binding string) (string, error) {
	if !validateStateBinding(binding) {
		return "", ErrStateInvalid
	}
	return m.issueInternal(station, intent, binding)
}

func (m *StateManager) issueInternal(station, intent, binding string) (string, error) {
	if m == nil || !validStateStation(station) || !validateIntent(intent) {
		return "", ErrStateInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return "", ErrStateInvalid
	}
	now := m.now()
	m.purgeLocked(now)
	if len(m.pending) >= m.max {
		return "", ErrStateCapacity
	}
	nonceBytes := make([]byte, 32)
	if _, err := rand.Read(nonceBytes); err != nil {
		clear(nonceBytes)
		return "", ErrStateInvalid
	}
	nonce := base64.RawURLEncoding.EncodeToString(nonceBytes)
	nonceHash := sha256.Sum256(nonceBytes)
	clear(nonceBytes)
	payload := oauthStatePayload{
		Version: 1,
		Nonce:   nonce,
		Station: station,
		Intent:  intent,
		Bind:    binding,
		Issued:  now.UnixNano(),
		Expires: now.Add(m.ttl).UnixNano(),
	}
	encodedPayload, err := encodeStatePayload(payload)
	if err != nil {
		return "", ErrStateInvalid
	}
	signed := encodedPayload + "." + m.sign(encodedPayload)
	m.pending[nonceHash] = stateRecord{expires: payload.Expires}
	return signed, nil
}

// Consume verifies the signed state, cookie equality, station/intent binding,
// expiry and one-use nonce atomically. It only accepts states issued without a
// session binding (login flows); an elevation-bound state fails here.
// The raw state is never included in an error value.
func (m *StateManager) Consume(state, cookie, station, intent string) error {
	return m.consumeInternal(state, cookie, station, intent, "")
}

// ConsumeBound consumes a state issued by IssueBound against the exact
// session binding that requested it. A missing, expired, replayed, or
// mismatched-binding state all fail atomically (nothing is replayed later).
func (m *StateManager) ConsumeBound(state, cookie, station, intent, binding string) error {
	if !validateStateBinding(binding) {
		return ErrStateInvalid
	}
	return m.consumeInternal(state, cookie, station, intent, binding)
}

// ConsumeAny verifies and consumes a state whose intent is not yet known to
// the caller (the OAuth callback), returning the signed intent and session
// binding. Verification, expiry, and one-use nonce deletion happen atomically.
func (m *StateManager) ConsumeAny(state, cookie, station string) (intent, binding string, err error) {
	if m == nil || !validateOAuthStateText(state) || !validateOAuthStateText(cookie) || !validStateStation(station) {
		return "", "", ErrStateInvalid
	}
	stateDigest := sha256.Sum256([]byte(state))
	cookieDigest := sha256.Sum256([]byte(cookie))
	if !hmac.Equal(stateDigest[:], cookieDigest[:]) {
		return "", "", ErrStateInvalid
	}
	parts := strings.Split(state, ".")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", ErrStateInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return "", "", ErrStateInvalid
	}
	payload, err := decodeStatePayload(parts[0])
	if err != nil || !hmac.Equal([]byte(parts[1]), []byte(m.sign(parts[0]))) {
		return "", "", ErrStateInvalid
	}
	now := m.now()
	m.purgeLocked(now)
	if payload.Version != 1 || payload.Station != station || payload.Nonce == "" || !validateIntent(payload.Intent) {
		return "", "", ErrStateInvalid
	}
	if payload.Expires <= now.UnixNano() {
		return "", "", ErrStateExpired
	}
	if payload.Issued > now.Add(maxStateClockSkew).UnixNano() || payload.Expires <= payload.Issued {
		return "", "", ErrStateInvalid
	}
	nonceBytes, err := base64.RawURLEncoding.DecodeString(payload.Nonce)
	if err != nil || len(nonceBytes) != 32 {
		clear(nonceBytes)
		return "", "", ErrStateInvalid
	}
	nonceHash := sha256.Sum256(nonceBytes)
	clear(nonceBytes)
	if _, ok := m.pending[nonceHash]; !ok {
		return "", "", ErrStateReplay
	}
	delete(m.pending, nonceHash)
	return payload.Intent, payload.Bind, nil
}

func (m *StateManager) consumeInternal(state, cookie, station, intent, binding string) error {
	if m == nil || !validateOAuthStateText(state) || !validateOAuthStateText(cookie) || !validStateStation(station) || !validateIntent(intent) {
		return ErrStateInvalid
	}
	stateDigest := sha256.Sum256([]byte(state))
	cookieDigest := sha256.Sum256([]byte(cookie))
	if !hmac.Equal(stateDigest[:], cookieDigest[:]) {
		return ErrStateInvalid
	}
	parts := strings.Split(state, ".")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return ErrStateInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrStateInvalid
	}
	payload, err := decodeStatePayload(parts[0])
	if err != nil || !hmac.Equal([]byte(parts[1]), []byte(m.sign(parts[0]))) {
		return ErrStateInvalid
	}
	now := m.now()
	m.purgeLocked(now)
	if payload.Version != 1 || payload.Station != station || payload.Intent != intent || payload.Nonce == "" {
		return ErrStateInvalid
	}
	if binding == "" {
		// Login flows: an elevation-bound state must never be consumable as a
		// plain login state.
		if payload.Bind != "" {
			return ErrStateInvalid
		}
	} else {
		if !hmac.Equal([]byte(payload.Bind), []byte(binding)) {
			return ErrStateInvalid
		}
	}
	if payload.Expires <= now.UnixNano() {
		return ErrStateExpired
	}
	if payload.Issued > now.Add(maxStateClockSkew).UnixNano() || payload.Expires <= payload.Issued {
		return ErrStateInvalid
	}
	nonceBytes, err := base64.RawURLEncoding.DecodeString(payload.Nonce)
	if err != nil || len(nonceBytes) != 32 {
		clear(nonceBytes)
		return ErrStateInvalid
	}
	nonceHash := sha256.Sum256(nonceBytes)
	clear(nonceBytes)
	if _, ok := m.pending[nonceHash]; !ok {
		return ErrStateReplay
	}
	delete(m.pending, nonceHash)
	return nil
}

// Close wipes the in-memory MAC key and rejects future operations.
func (m *StateManager) Close() error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil
	}
	m.closed = true
	clear(m.key)
	m.key = nil
	for nonce := range m.pending {
		delete(m.pending, nonce)
	}
	return nil
}

func (m *StateManager) sign(payload string) string {
	mac := hmac.New(sha256.New, m.key)
	_, _ = mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func encodeStatePayload(payload oauthStatePayload) (string, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(encoded), nil
}

func decodeStatePayload(encoded string) (oauthStatePayload, error) {
	var payload oauthStatePayload
	if len(encoded) > maxOAuthStateBytes {
		return payload, ErrStateInvalid
	}
	decoded, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return payload, ErrStateInvalid
	}
	defer clear(decoded)
	if err := json.Unmarshal(decoded, &payload); err != nil {
		return payload, ErrStateInvalid
	}
	return payload, nil
}

func validStateStation(station string) bool {
	return station == StationUser || station == StationAdmin
}

// validateStateBinding accepts an opaque session binding value (a hex session
// token hash from the identity rail). It is bounded and control-free so a
// malformed binding can never be embedded in a signed state payload.
func validateStateBinding(binding string) bool {
	return binding != "" && len(binding) <= maxStateBindingBytes && utf8.ValidString(binding) && !strings.ContainsAny(binding, "\x00\r\n")
}
