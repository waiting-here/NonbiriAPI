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
	maxOAuthStates       = 4096
	maxStateClockSkew    = 30 * time.Second
	maxStateBindingBytes = 256
	maxRouteIDBytes      = 64
)

type oauthStatePayload struct {
	Version    int    `json:"v"`
	Nonce      string `json:"n"`
	Station    string `json:"s"`
	Intent     string `json:"i"`
	Bind       string `json:"b,omitempty"`
	RouteID    string `json:"r"`
	ResourceID string `json:"x,omitempty"`
	Issued     int64  `json:"iat"`
	Expires    int64  `json:"exp"`
}

type stateRecord struct{ expires int64 }

type StateClaims struct {
	Intent     string
	Binding    string
	RouteID    string
	ResourceID string
}

// StateManager creates process-local, signed, bounded and one-use OAuth state.
type StateManager struct {
	mu      sync.Mutex
	key     []byte
	ttl     time.Duration
	max     int
	now     func() time.Time
	pending map[[32]byte]stateRecord
	closed  bool
}

func NewStateManager(ttl time.Duration) (*StateManager, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		clear(key)
		return nil, fmt.Errorf("oauth state signer unavailable")
	}
	m, err := newStateManager(key, ttl, maxOAuthStates, time.Now)
	if err != nil {
		clear(key)
	}
	return m, err
}

func NewStateManagerWithKey(key []byte, ttl time.Duration) (*StateManager, error) {
	copyKey := append([]byte(nil), key...)
	m, err := newStateManager(copyKey, ttl, maxOAuthStates, time.Now)
	if err != nil {
		clear(copyKey)
	}
	return m, err
}

func newStateManager(key []byte, ttl time.Duration, max int, now func() time.Time) (*StateManager, error) {
	if len(key) < 32 || ttl <= 0 || ttl > time.Hour || max <= 0 || now == nil {
		return nil, fmt.Errorf("oauth state signer configuration is invalid")
	}
	return &StateManager{key: key, ttl: ttl, max: max, now: now, pending: make(map[[32]byte]stateRecord)}, nil
}

func (m *StateManager) TTL() time.Duration {
	if m == nil {
		return 0
	}
	return m.ttl
}

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

func (m *StateManager) Issue(station, intent string) (string, error) {
	return m.issue(station, intent, "", "home", "")
}
func (m *StateManager) IssueBound(station, intent, binding string) (string, error) {
	return m.issue(station, intent, binding, "home", "")
}
func (m *StateManager) IssueForRoute(station, intent, routeID, resourceID string) (string, error) {
	return m.issue(station, intent, "", routeID, resourceID)
}
func (m *StateManager) IssueBoundForRoute(station, intent, binding, routeID, resourceID string) (string, error) {
	return m.issue(station, intent, binding, routeID, resourceID)
}

func (m *StateManager) issue(station, intent, binding, routeID, resourceID string) (string, error) {
	if m == nil || !validStateStation(station) || !validateIntent(intent) || !validStateBindingOptional(binding) || !validateBoundedText(routeID, maxRouteIDBytes, false) || !validResourceIDOptional(resourceID) {
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
	nonceRaw := make([]byte, 32)
	if _, err := rand.Read(nonceRaw); err != nil {
		clear(nonceRaw)
		return "", ErrStateInvalid
	}
	nonce := base64.RawURLEncoding.EncodeToString(nonceRaw)
	nonceHash := sha256.Sum256(nonceRaw)
	clear(nonceRaw)
	p := oauthStatePayload{Version: 2, Nonce: nonce, Station: station, Intent: intent, Bind: binding, RouteID: routeID, ResourceID: resourceID, Issued: now.UnixNano(), Expires: now.Add(m.ttl).UnixNano()}
	encoded, err := encodeStatePayload(p)
	if err != nil {
		return "", ErrStateInvalid
	}
	signed := encoded + "." + m.sign(encoded)
	m.pending[nonceHash] = stateRecord{expires: p.Expires}
	return signed, nil
}

func (m *StateManager) Consume(state, cookie, station, intent string) error {
	claims, err := m.ConsumeClaims(state, cookie, station)
	if err != nil {
		return err
	}
	if claims.Intent != intent || claims.Binding != "" {
		return ErrStateInvalid
	}
	return nil
}
func (m *StateManager) ConsumeBound(state, cookie, station, intent, binding string) error {
	claims, err := m.ConsumeClaims(state, cookie, station)
	if err != nil {
		return err
	}
	if claims.Intent != intent || !hmac.Equal([]byte(claims.Binding), []byte(binding)) || !validateStateBinding(binding) {
		return ErrStateInvalid
	}
	return nil
}
func (m *StateManager) ConsumeAny(state, cookie, station string) (string, string, error) {
	claims, err := m.ConsumeClaims(state, cookie, station)
	return claims.Intent, claims.Binding, err
}

// ConsumeClaims verifies and burns a state before returning its server-issued
// intent, session binding and route identity.
func (m *StateManager) ConsumeClaims(state, cookie, station string) (StateClaims, error) {
	if m == nil || !validateOAuthStateText(state) || !validateOAuthStateText(cookie) || !validStateStation(station) {
		return StateClaims{}, ErrStateInvalid
	}
	a := sha256.Sum256([]byte(state))
	b := sha256.Sum256([]byte(cookie))
	if !hmac.Equal(a[:], b[:]) {
		return StateClaims{}, ErrStateInvalid
	}
	parts := strings.Split(state, ".")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return StateClaims{}, ErrStateInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return StateClaims{}, ErrStateInvalid
	}
	p, err := decodeStatePayload(parts[0])
	if err != nil || !hmac.Equal([]byte(parts[1]), []byte(m.sign(parts[0]))) {
		return StateClaims{}, ErrStateInvalid
	}
	now := m.now()
	m.purgeLocked(now)
	if p.Version != 2 || p.Station != station || p.Nonce == "" || !validateIntent(p.Intent) || !validStateBindingOptional(p.Bind) || !validateBoundedText(p.RouteID, maxRouteIDBytes, false) || !validResourceIDOptional(p.ResourceID) {
		return StateClaims{}, ErrStateInvalid
	}
	if p.Expires <= now.UnixNano() {
		return StateClaims{}, ErrStateExpired
	}
	if p.Issued > now.Add(maxStateClockSkew).UnixNano() || p.Expires <= p.Issued {
		return StateClaims{}, ErrStateInvalid
	}
	raw, err := base64.RawURLEncoding.DecodeString(p.Nonce)
	if err != nil || len(raw) != 32 {
		clear(raw)
		return StateClaims{}, ErrStateInvalid
	}
	h := sha256.Sum256(raw)
	clear(raw)
	if _, ok := m.pending[h]; !ok {
		return StateClaims{}, ErrStateReplay
	}
	delete(m.pending, h)
	return StateClaims{Intent: p.Intent, Binding: p.Bind, RouteID: p.RouteID, ResourceID: p.ResourceID}, nil
}

func (m *StateManager) purgeLocked(now time.Time) {
	for n, r := range m.pending {
		if r.expires <= now.UnixNano() {
			delete(m.pending, n)
		}
	}
}

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
	clear(m.pending)
	return nil
}

func (m *StateManager) sign(payload string) string {
	mac := hmac.New(sha256.New, m.key)
	_, _ = mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
func encodeStatePayload(p oauthStatePayload) (string, error) {
	b, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
func decodeStatePayload(encoded string) (oauthStatePayload, error) {
	var p oauthStatePayload
	if len(encoded) > maxOAuthStateBytes {
		return p, ErrStateInvalid
	}
	b, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return p, ErrStateInvalid
	}
	defer clear(b)
	if json.Unmarshal(b, &p) != nil {
		return p, ErrStateInvalid
	}
	return p, nil
}
func validStateStation(v string) bool { return v == StationUser || v == StationAdmin }
func validateStateBinding(v string) bool {
	return v != "" && len(v) <= maxStateBindingBytes && utf8.ValidString(v) && !strings.ContainsAny(v, "\x00\r\n")
}
func validStateBindingOptional(v string) bool { return v == "" || validateStateBinding(v) }
func validResourceIDOptional(v string) bool   { return v == "" || validateResourceID(v) }
func validateResourceID(v string) bool {
	return validateBoundedText(v, maxResourceIDBytes, false) && v != "." && v != ".." && !strings.ContainsAny(v, "/\\?#")
}
