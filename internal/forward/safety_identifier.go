package forward

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base32"
	"encoding/binary"
	"errors"
	"log/slog"
	"sync"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/egress"
)

const (
	// SafetyIdentifierSubkeyInfo is the dedicated v3 purpose/version label for
	// the Vault-derived safety identifier subkey. The HMAC message includes
	// these exact bytes, a four-byte canonical-origin length plus its bytes,
	// then one eight-byte big-endian positive user id.
	SafetyIdentifierSubkeyInfo = safetyIdentifierPurpose + ":" + safetyIdentifierVersion
	safetyIdentifierPurpose    = "nonbiriapi:safety-identifier"
	safetyIdentifierVersion    = "v3"

	safetyIdentifierPrefix       = "nbu_v3_"
	safetyIdentifierKeyBytes     = sha256.Size
	safetyIdentifierUserIDBytes  = 8
	safetyIdentifierLengthPrefix = 4
	safetyIdentifierDigestText   = 52
	safetyIdentifierLength       = len(safetyIdentifierPrefix) + safetyIdentifierDigestText
)

var (
	errInvalidSafetyIdentifierKey    = errors.New("forward: invalid safety identifier key")
	errSafetyIdentifierClosed        = errors.New("forward: safety identifier generator is closed")
	errInvalidSafetyIdentifierID     = errors.New("forward: invalid safety identifier user")
	errInvalidSafetyIdentifierOrigin = errors.New("forward: invalid safety identifier origin")
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

func (g *safetyIdentifierGenerator) generate(userID int64, canonicalOrigin string) (string, error) {
	if g == nil {
		return "", errSafetyIdentifierClosed
	}
	if userID <= 0 {
		return "", errInvalidSafetyIdentifierID
	}
	if !validSafetyIdentifierOrigin(canonicalOrigin) {
		return "", errInvalidSafetyIdentifierOrigin
	}

	g.mu.RLock()
	defer g.mu.RUnlock()
	if g.closed {
		return "", errSafetyIdentifierClosed
	}

	message := safetyIdentifierMessage(userID, canonicalOrigin)
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

func safetyIdentifierMessage(userID int64, canonicalOrigin string) []byte {
	total := len(SafetyIdentifierSubkeyInfo) + safetyIdentifierLengthPrefix + len(canonicalOrigin) + safetyIdentifierUserIDBytes
	message := make([]byte, 0, total)
	message = append(message, SafetyIdentifierSubkeyInfo...)
	var length [safetyIdentifierLengthPrefix]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(canonicalOrigin)))
	message = append(message, length[:]...)
	message = append(message, canonicalOrigin...)
	var user [safetyIdentifierUserIDBytes]byte
	binary.BigEndian.PutUint64(user[:], uint64(userID))
	message = append(message, user[:]...)
	clear(user[:])
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

// SafetyIdentifierFactory owns the one shared generator injected into
// SecureRunner. Both personal and charity attempts therefore derive from the
// same deployment subkey after their final target has been revalidated.
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

// Generate mints one stable per-user, canonical-origin-scoped identifier.
func (f *SafetyIdentifierFactory) Generate(userID int64, canonicalOrigin string) (string, error) {
	if f == nil || f.inner == nil {
		return "", errSafetyIdentifierClosed
	}
	return f.inner.generate(userID, canonicalOrigin)
}

// Close clears the retained subkey. Idempotent.
func (f *SafetyIdentifierFactory) Close() error {
	if f == nil || f.inner == nil {
		return nil
	}
	return f.inner.close()
}

func validSafetyIdentifierOrigin(origin string) bool {
	if origin == "" || !utf8.ValidString(origin) {
		return false
	}
	target, canonical, err := egress.CanonicalEndpointTarget(origin)
	return err == nil && target == origin && canonical == origin
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
