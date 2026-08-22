package forward

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base32"
	"encoding/binary"
	"errors"
	"log/slog"
	"sync"
)

const (
	// SafetyIdentifierSubkeyInfo is the purpose label used both by the Vault
	// HKDF boundary and as the fixed prefix of the HMAC message. The message is
	// these exact bytes followed immediately by one eight-byte big-endian,
	// positive int64 user identifier.
	SafetyIdentifierSubkeyInfo = "nonbiriapi:safety-identifier:v2"

	safetyIdentifierPrefix       = "nbu_v2_"
	safetyIdentifierKeyBytes     = sha256.Size
	safetyIdentifierUserIDBytes  = 8
	safetyIdentifierMessageBytes = len(SafetyIdentifierSubkeyInfo) + safetyIdentifierUserIDBytes
	safetyIdentifierDigestText   = 52
	safetyIdentifierLength       = len(safetyIdentifierPrefix) + safetyIdentifierDigestText
)

var (
	errInvalidSafetyIdentifierKey = errors.New("forward: invalid safety identifier key")
	errSafetyIdentifierClosed     = errors.New("forward: safety identifier generator is closed")
	errInvalidSafetyIdentifierID  = errors.New("forward: invalid safety identifier user")
)

// safetyIdentifierGenerator owns one purpose-derived key copied at
// construction. It is safe for concurrent use and closes idempotently; the
// first close waits for in-flight generations and clears the retained key on
// a best-effort basis.
type safetyIdentifierGenerator struct {
	mu     sync.RWMutex
	key    [safetyIdentifierKeyBytes]byte
	closed bool
}

func newSafetyIdentifierGenerator(key []byte) (*safetyIdentifierGenerator, error) {
	if len(key) != safetyIdentifierKeyBytes {
		return nil, errInvalidSafetyIdentifierKey
	}
	generator := &safetyIdentifierGenerator{}
	copy(generator.key[:], key)
	return generator, nil
}

func (g *safetyIdentifierGenerator) generate(userID int64) (string, error) {
	if g == nil {
		return "", errSafetyIdentifierClosed
	}
	if userID <= 0 {
		return "", errInvalidSafetyIdentifierID
	}

	g.mu.RLock()
	defer g.mu.RUnlock()
	if g.closed {
		return "", errSafetyIdentifierClosed
	}

	message := safetyIdentifierMessage(userID)
	mac := hmac.New(sha256.New, g.key[:])
	_, _ = mac.Write(message[:])
	digest := mac.Sum(nil)
	identifier := safetyIdentifierPrefix + base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(digest)
	clear(digest)
	clear(message[:])
	if len(identifier) != safetyIdentifierLength {
		return "", errSafetyIdentifierClosed
	}
	return identifier, nil
}

func safetyIdentifierMessage(userID int64) [safetyIdentifierMessageBytes]byte {
	var message [safetyIdentifierMessageBytes]byte
	copy(message[:], SafetyIdentifierSubkeyInfo)
	binary.BigEndian.PutUint64(message[len(SafetyIdentifierSubkeyInfo):], uint64(userID))
	return message
}

func (g *safetyIdentifierGenerator) close() error {
	if g == nil {
		return nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.closed {
		clear(g.key[:])
		g.closed = true
	}
	return nil
}

// SafetyIdentifierFactory is the exported handle for rails outside this
// package (the charity routing exit) that must mint the same per-user safety
// identifier from the same derived subkey. It wraps the same generator the
// personal forwarding service uses, so one deployment never emits two
// different identifiers for the same user.
type SafetyIdentifierFactory struct {
	inner *safetyIdentifierGenerator
}

// NewSafetyIdentifierFactory builds a factory over one 32-byte purpose-derived
// subkey. The input is copied; clear it after construction.
func NewSafetyIdentifierFactory(key []byte) (*SafetyIdentifierFactory, error) {
	inner, err := newSafetyIdentifierGenerator(key)
	if err != nil {
		return nil, err
	}
	return &SafetyIdentifierFactory{inner: inner}, nil
}

// Generate mints one stable per-user identifier.
func (f *SafetyIdentifierFactory) Generate(userID int64) (string, error) {
	if f == nil || f.inner == nil {
		return "", errSafetyIdentifierClosed
	}
	return f.inner.generate(userID)
}

// Close clears the retained subkey. Idempotent.
func (f *SafetyIdentifierFactory) Close() error {
	if f == nil || f.inner == nil {
		return nil
	}
	return f.inner.close()
}

// String prevents routine formatting from exposing the retained derived key.
func (*safetyIdentifierGenerator) String() string {
	return "[redacted safety identifier generator]"
}

// GoString prevents detailed formatting from exposing the retained derived key.
func (*safetyIdentifierGenerator) GoString() string {
	return "[redacted safety identifier generator]"
}

// LogValue prevents structured logging from reflecting the retained derived key.
func (*safetyIdentifierGenerator) LogValue() slog.Value {
	return slog.StringValue("[redacted safety identifier generator]")
}
