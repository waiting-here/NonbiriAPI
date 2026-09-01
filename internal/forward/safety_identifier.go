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
	SafetyIdentifierSubkeyInfo = "nonbiriapi:safety-identifier:v3"
	safetyIdentifierPrefix     = "nbu_v3_"
	safetyIdentifierLength     = len(safetyIdentifierPrefix) + 52
)

var (
	errSafetyIdentifierClosed = errors.New("forward: safety identifier factory is closed")
	errSafetyIdentifierInput  = errors.New("forward: invalid safety identifier input")
)

type GenerationTwoSubkeyDeriver interface {
	DeriveGenerationTwoSubkey([]byte) ([]byte, error)
}

// SafetyIdentifierFactory owns one purpose-derived key. Construction derives
// the key exactly once and clears the returned temporary slice; Close waits
// for in-flight generations before clearing the retained copy.
type SafetyIdentifierFactory struct {
	mu     sync.RWMutex
	key    [sha256.Size]byte
	closed bool
}

func NewSafetyIdentifierFactory(deriver GenerationTwoSubkeyDeriver) (*SafetyIdentifierFactory, error) {
	if deriver == nil {
		return nil, ErrInvalidConfiguration
	}
	derived, err := deriver.DeriveGenerationTwoSubkey([]byte(SafetyIdentifierSubkeyInfo))
	if err != nil {
		clear(derived)
		return nil, err
	}
	defer clear(derived)
	if len(derived) != sha256.Size {
		return nil, errSafetyIdentifierInput
	}
	factory := &SafetyIdentifierFactory{}
	copy(factory.key[:], derived)
	return factory, nil
}

// Generate returns the frozen nbu_v3_ pseudonym for one positive consumer
// and exact canonical origin. Raw user ids and origins never leave this call.
func (factory *SafetyIdentifierFactory) Generate(userID int64, canonicalOrigin string) (string, error) {
	if factory == nil || userID <= 0 || !validSafetyOrigin(canonicalOrigin) {
		return "", errSafetyIdentifierInput
	}
	factory.mu.RLock()
	defer factory.mu.RUnlock()
	if factory.closed {
		return "", errSafetyIdentifierClosed
	}
	message := makeSafetyMessage(userID, canonicalOrigin)
	mac := hmac.New(sha256.New, factory.key[:])
	_, _ = mac.Write(message)
	digest := mac.Sum(nil)
	identifier := safetyIdentifierPrefix + base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(digest)
	clear(message)
	clear(digest)
	if len(identifier) != safetyIdentifierLength {
		return "", errSafetyIdentifierClosed
	}
	return identifier, nil
}

func makeSafetyMessage(userID int64, canonicalOrigin string) []byte {
	message := make([]byte, 0, len(SafetyIdentifierSubkeyInfo)+4+len(canonicalOrigin)+8)
	message = append(message, SafetyIdentifierSubkeyInfo...)
	var originLength [4]byte
	binary.BigEndian.PutUint32(originLength[:], uint32(len(canonicalOrigin)))
	message = append(message, originLength[:]...)
	message = append(message, canonicalOrigin...)
	var actor [8]byte
	binary.BigEndian.PutUint64(actor[:], uint64(userID))
	message = append(message, actor[:]...)
	clear(actor[:])
	return message
}

func validSafetyOrigin(origin string) bool {
	if origin == "" || !utf8.ValidString(origin) {
		return false
	}
	target, canonical, err := egress.CanonicalEndpointTarget(origin)
	return err == nil && target == origin && canonical == origin
}

func canonicalOrigin(baseURL string) (string, error) {
	_, origin, err := egress.CanonicalEndpointTarget(baseURL)
	if err != nil || !validSafetyOrigin(origin) {
		return "", errSafetyIdentifierInput
	}
	return origin, nil
}

func (factory *SafetyIdentifierFactory) Close() error {
	if factory == nil {
		return nil
	}
	factory.mu.Lock()
	if !factory.closed {
		clear(factory.key[:])
		factory.closed = true
	}
	factory.mu.Unlock()
	return nil
}

func (*SafetyIdentifierFactory) String() string   { return "[redacted safety identifier factory]" }
func (*SafetyIdentifierFactory) GoString() string { return "[redacted safety identifier factory]" }
func (*SafetyIdentifierFactory) LogValue() slog.Value {
	return slog.StringValue("[redacted safety identifier factory]")
}
