// Package elevation issues and consumes short-lived, single-use, identity-bound
// elevated-action capabilities. A capability is a second factor for destructive
// self-service and administrator actions (account export, account deletion): it
// is obtained through a two-step endpoint (a fresh Discord re-authorization for
// normal users, or a password re-input for the administrator) and then presented
// as the `X-Elevated-Token` request header by the destructive call.
//
// Design (mirrors internal/auth.StateManager, deliberately stateless payload +
// server-side one-use nonce set):
//
//   - A token is `<base64url(payload)>.<base64url(hmac)>`. The payload carries
//     version, nonce, bound user id, bound kind, issued-at and expiry. The HMAC
//     is keyed by an in-process random key that is never persisted or exposed,
//     so a token cannot be forged or modified outside this process.
//   - Single-use is enforced by a server-side nonce-hash replay set: Consume
//     removes the nonce under the manager mutex, so the same token can drive at
//     most one destructive action. Process restart drops pending capabilities,
//     which is safe (the user re-elevates); it never resurrects a consumed one.
//   - Binding: Consume verifies the payload's user id and kind match the
//     caller-supplied values, so a token issued for one user cannot be replayed
//     against another user's session or across the user/admin station boundary.
//
// This package is intentionally narrow and shared: the export (track S) and
// account-deletion (track T) handlers both consume the same capability type, so
// a single elevation grants one destructive action regardless of which endpoint
// spends it first.
package elevation

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Kind separates the user-station and administrator-station elevation domains.
// A token issued for one kind is refused by Consume for the other, so a user
// elevation can never authorize an administrator destructive action or vice
// versa.
type Kind uint8

const (
	// KindUser is the normal-user station elevation domain.
	KindUser Kind = iota + 1
	// KindAdmin is the administrator station elevation domain.
	KindAdmin
)

func (k Kind) valid() bool { return k == KindUser || k == KindAdmin }

// DefaultTTL is the bounded lifetime of an elevated-action capability. It is
// short on purpose: the capability is single-use and grants a destructive
// action, so a long window would only widen the replay/force-window risk.
const DefaultTTL = 10 * time.Minute

const (
	maxPending      = 4096
	maxClockSkew    = 30 * time.Second
	maxPayloadBytes = 1024
	maxTokenBytes   = 4096
	nonceBytes      = 32
	encodedNonceLen = 43 // base64.RawURLEncoding.EncodedLen(nonceBytes)
)

// Sentinel errors. None of them carries token material; Consume callers map any
// non-nil error to a stable forbidden response.
var (
	ErrInvalid   = errors.New("elevation token is invalid")
	ErrExpired   = errors.New("elevation token is expired")
	ErrReplay    = errors.New("elevation token already used")
	ErrMismatch  = errors.New("elevation token does not match the identity")
	ErrCapacity  = errors.New("elevation capacity exhausted")
	ErrClosed    = errors.New("elevation manager is closed")
	ErrMalformed = errors.New("elevation request is malformed")
)

type clock func() time.Time

type payload struct {
	Version int    `json:"v"`
	Nonce   string `json:"n"`
	UserID  int64  `json:"u"`
	Kind    Kind   `json:"k"`
	Binding string `json:"b,omitempty"`
	Issued  int64  `json:"iat"`
	Expires int64  `json:"exp"`
}

type pendingRecord struct {
	binding string
	expires int64
}

// Manager issues and consumes elevated-action capabilities. It is safe for
// concurrent use. The HMAC key and the nonce replay set live only in process
// memory and are wiped on Close.
type Manager struct {
	mu      sync.Mutex
	key     []byte
	ttl     time.Duration
	max     int
	now     clock
	pending map[[32]byte]pendingRecord
	closed  bool
}

// NewManager creates an elevation manager with the default TTL and a random
// in-process HMAC key.
func NewManager() (*Manager, error) {
	return NewManagerWithTTL(DefaultTTL)
}

// NewManagerWithTTL creates a manager with a caller-supplied TTL. The TTL must
// be positive and no longer than one hour; a longer window is rejected because
// an elevated-action capability is single-use and should not linger.
func NewManagerWithTTL(ttl time.Duration) (*Manager, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		clear(key)
		return nil, fmt.Errorf("elevation signer unavailable")
	}
	return newManager(key, ttl, maxPending, time.Now)
}

// NewManagerWithKey is intended for deterministic tests. The key is copied and
// never returned; the caller retains ownership of the input slice.
func NewManagerWithKey(key []byte, ttl time.Duration) (*Manager, error) {
	if len(key) < 32 {
		return nil, ErrMalformed
	}
	copyKey := append([]byte(nil), key...)
	m, err := newManager(copyKey, ttl, maxPending, time.Now)
	if err != nil {
		clear(copyKey)
		return nil, err
	}
	return m, nil
}

func newManager(key []byte, ttl time.Duration, max int, now clock) (*Manager, error) {
	if len(key) < 32 || ttl <= 0 || ttl > time.Hour || max <= 0 || now == nil {
		return nil, ErrMalformed
	}
	return &Manager{
		key:     key,
		ttl:     ttl,
		max:     max,
		now:     now,
		pending: make(map[[32]byte]pendingRecord),
	}, nil
}

// SetClock is a test/maintenance hook. The supplied function must be safe for
// concurrent calls; production code leaves the real clock in place.
func (m *Manager) SetClock(now func() time.Time) error {
	if m == nil || now == nil {
		return ErrInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrClosed
	}
	m.now = now
	return nil
}

// TTL reports the configured capability lifetime.
func (m *Manager) TTL() time.Duration {
	if m == nil {
		return 0
	}
	return m.ttl
}

func (m *Manager) purgeLocked(now time.Time) {
	nanos := now.UnixNano()
	for nonce, record := range m.pending {
		if record.expires <= nanos {
			delete(m.pending, nonce)
		}
	}
}

// Issue creates a single-use elevated-action capability bound to userID and
// kind. Only the signed opaque token is returned; the nonce is retained as a
// hash for replay detection.
func (m *Manager) Issue(userID int64, kind Kind) (string, time.Time, error) {
	return m.issue(userID, kind, "")
}

// IssueBound creates a capability bound to an active session binding. The
// unbound Issue form remains only for compatibility and tests; browser
// destructive actions must use this method.
func (m *Manager) IssueBound(userID int64, kind Kind, binding string) (string, time.Time, error) {
	if !validBinding(binding) {
		return "", time.Time{}, ErrMalformed
	}
	return m.issue(userID, kind, binding)
}

func (m *Manager) issue(userID int64, kind Kind, binding string) (string, time.Time, error) {
	if m == nil {
		return "", time.Time{}, ErrInvalid
	}
	if !kind.valid() || userID <= 0 {
		return "", time.Time{}, ErrMalformed
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return "", time.Time{}, ErrClosed
	}
	now := m.now()
	m.purgeLocked(now)
	if len(m.pending) >= m.max {
		return "", time.Time{}, ErrCapacity
	}
	nonceRaw := make([]byte, nonceBytes)
	if _, err := rand.Read(nonceRaw); err != nil {
		clear(nonceRaw)
		return "", time.Time{}, ErrInvalid
	}
	nonce := base64.RawURLEncoding.EncodeToString(nonceRaw)
	nonceHash := sha256.Sum256(nonceRaw)
	clear(nonceRaw)
	expires := now.Add(m.ttl)
	p := payload{
		Version: 1,
		Nonce:   nonce,
		UserID:  userID,
		Kind:    kind,
		Binding: binding,
		Issued:  now.UnixNano(),
		Expires: expires.UnixNano(),
	}
	encoded, err := encodePayload(p)
	if err != nil {
		return "", time.Time{}, ErrInvalid
	}
	signed := encoded + "." + m.sign(encoded)
	m.pending[nonceHash] = pendingRecord{binding: binding, expires: p.Expires}
	return signed, expires, nil
}

// Consume verifies the signed token, its expiry, its identity binding, and its
// one-use nonce, then atomically removes the nonce under the manager mutex. It
// returns nil only when the capability was valid and removed; any failure
// returns a sentinel error and leaves the pending set unchanged (a mismatched
// identity does not burn another user's token).
func (m *Manager) Consume(token string, userID int64, kind Kind) error {
	return m.consume(token, userID, kind, "")
}

// ConsumeBound validates and consumes a capability bound to the exact active
// session binding supplied by the caller.
func (m *Manager) ConsumeBound(token string, userID int64, kind Kind, binding string) error {
	if !validBinding(binding) {
		return ErrInvalid
	}
	return m.consume(token, userID, kind, binding)
}

func (m *Manager) consume(token string, userID int64, kind Kind, binding string) error {
	if m == nil || !kind.valid() || userID <= 0 || !validTokenText(token) {
		return ErrInvalid
	}
	parts := strings.Split(token, ".")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return ErrInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrClosed
	}
	now := m.now()
	m.purgeLocked(now)
	if !hmac.Equal([]byte(parts[1]), []byte(m.sign(parts[0]))) {
		return ErrInvalid
	}
	p, err := decodePayload(parts[0])
	if err != nil {
		return ErrInvalid
	}
	if p.Version != 1 || !p.Kind.valid() || p.Nonce == "" {
		return ErrInvalid
	}
	if p.Expires <= now.UnixNano() {
		return ErrExpired
	}
	if p.Issued > now.Add(maxClockSkew).UnixNano() || p.Expires <= p.Issued {
		return ErrInvalid
	}
	if p.UserID != userID || p.Kind != kind || !hmac.Equal([]byte(p.Binding), []byte(binding)) {
		return ErrMismatch
	}
	nonceBytesRaw, err := base64.RawURLEncoding.DecodeString(p.Nonce)
	if err != nil || len(nonceBytesRaw) != nonceBytes {
		clear(nonceBytesRaw)
		return ErrInvalid
	}
	nonceHash := sha256.Sum256(nonceBytesRaw)
	clear(nonceBytesRaw)
	record, ok := m.pending[nonceHash]
	if !ok {
		// Already consumed, or expired and purged between Issue and Consume.
		return ErrReplay
	}
	if !hmac.Equal([]byte(record.binding), []byte(binding)) {
		return ErrMismatch
	}
	delete(m.pending, nonceHash)
	return nil
}

// Close wipes the HMAC key and the nonce replay set and rejects future
// operations. It is idempotent.
func (m *Manager) Close() error {
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

func (m *Manager) sign(payload string) string {
	mac := hmac.New(sha256.New, m.key)
	_, _ = mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func encodePayload(p payload) (string, error) {
	encoded, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(encoded), nil
}

func decodePayload(encoded string) (payload, error) {
	var p payload
	if len(encoded) > maxPayloadBytes {
		return p, ErrInvalid
	}
	decoded, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return p, err
	}
	defer clear(decoded)
	if err := json.Unmarshal(decoded, &p); err != nil {
		return p, err
	}
	return p, nil
}

func validBinding(binding string) bool {
	if binding == "" || len(binding) > 256 {
		return false
	}
	for _, r := range binding {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func validTokenText(token string) bool {
	if token == "" || len(token) > maxTokenBytes {
		return false
	}
	for _, r := range token {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// EncodedNonceLength is exported for tests that inspect token shape without
// touching the signer.
const EncodedNonceLength = encodedNonceLen
