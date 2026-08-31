package reports

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"io"
	"strconv"
	"sync"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

const (
	fingerprintInfo = "credential-report-fingerprint/v1"
	idempotencyInfo = "credential-report-idempotency/v1"
	targetInfo      = "credential-report-target-ref/v1"
	sourceIPInfo    = "credential-report-source-ip/v1"
	rateInfo        = "credential-report-rate-scope/v1"
	cursorInfo      = "pagination-cursor/v1"
	envelopeVersion = byte(1)
	envelopeBytes   = 1 + 12 + 16 + 16
)

var errInvalidEnvelope = errors.New("reports: invalid source IP envelope")

type reportKeys struct {
	mu       sync.RWMutex
	randomMu sync.Mutex
	random   io.Reader
	closed   bool

	fingerprint [32]byte
	idempotency [32]byte
	target      [32]byte
	sourceIP    [32]byte
	rate        [32]byte
}

func newReportKeys(deriver GenerationTwoSubkeyDeriver, random io.Reader) (*reportKeys, error) {
	if deriver == nil || random == nil {
		return nil, ErrUnavailable
	}
	keys := &reportKeys{random: random}
	for _, item := range []struct {
		info string
		dst  *[32]byte
	}{
		{fingerprintInfo, &keys.fingerprint},
		{idempotencyInfo, &keys.idempotency},
		{targetInfo, &keys.target},
		{sourceIPInfo, &keys.sourceIP},
		{rateInfo, &keys.rate},
	} {
		derived, err := deriver.DeriveGenerationTwoSubkey([]byte(item.info))
		if err != nil || len(derived) != sha256.Size {
			clear(derived)
			_ = keys.Close()
			return nil, ErrUnavailable
		}
		copy(item.dst[:], derived)
		clear(derived)
	}
	all := [][32]byte{keys.fingerprint, keys.idempotency, keys.target, keys.sourceIP, keys.rate}
	for index := range all {
		for other := index + 1; other < len(all); other++ {
			if all[index] == all[other] {
				_ = keys.Close()
				return nil, ErrUnavailable
			}
		}
	}
	return keys, nil
}

func (keys *reportKeys) Close() error {
	if keys == nil {
		return nil
	}
	keys.mu.Lock()
	defer keys.mu.Unlock()
	if keys.closed {
		return nil
	}
	clear(keys.fingerprint[:])
	clear(keys.idempotency[:])
	clear(keys.target[:])
	clear(keys.sourceIP[:])
	clear(keys.rate[:])
	keys.closed = true
	return nil
}

func (*reportKeys) String() string   { return "[redacted report keys]" }
func (*reportKeys) GoString() string { return "[redacted report keys]" }

func framed(value []byte) []byte {
	var size [4]byte
	binary.BigEndian.PutUint32(size[:], uint32(len(value)))
	out := make([]byte, 0, 4+len(value))
	out = append(out, size[:]...)
	return append(out, value...)
}

func keyedDigest(key *[32]byte, framedFields bool, fields ...[]byte) [32]byte {
	mac := hmac.New(sha256.New, key[:])
	for _, field := range fields {
		if framedFields {
			encoded := framed(field)
			_, _ = mac.Write(encoded)
			clear(encoded)
		} else {
			_, _ = mac.Write(field)
		}
	}
	var out [32]byte
	sum := mac.Sum(nil)
	copy(out[:], sum)
	clear(sum)
	return out
}

func (keys *reportKeys) fingerprintDigest(connector, canonicalBaseURL string, secret []byte) ([32]byte, error) {
	if keys == nil || !utf8.ValidString(connector) || !utf8.ValidString(canonicalBaseURL) || !utf8.Valid(secret) {
		return [32]byte{}, ErrInvalidRequest
	}
	keys.mu.RLock()
	defer keys.mu.RUnlock()
	if keys.closed {
		return [32]byte{}, ErrClosed
	}
	return keyedDigest(&keys.fingerprint, true, []byte(connector), []byte(canonicalBaseURL), secret), nil
}

func (keys *reportKeys) requestDigest(connector, canonicalBaseURL string, secret []byte, note string) ([32]byte, error) {
	if keys == nil || !utf8.ValidString(connector) || !utf8.ValidString(canonicalBaseURL) || !utf8.Valid(secret) || !utf8.ValidString(note) {
		return [32]byte{}, ErrInvalidRequest
	}
	keys.mu.RLock()
	defer keys.mu.RUnlock()
	if keys.closed {
		return [32]byte{}, ErrClosed
	}
	return keyedDigest(&keys.idempotency, true, []byte(connector), []byte(canonicalBaseURL), secret, []byte(note)), nil
}

func (keys *reportKeys) materialDigest(caseID string, requestHash [32]byte) ([32]byte, error) {
	if keys == nil || !db.ValidateOpaqueID(caseID, "rpc_") {
		return [32]byte{}, ErrInvalidRequest
	}
	keys.mu.RLock()
	defer keys.mu.RUnlock()
	if keys.closed {
		return [32]byte{}, ErrClosed
	}
	caseField := framed([]byte(caseID))
	defer clear(caseField)
	return keyedDigest(&keys.idempotency, false, caseField, requestHash[:]), nil
}

func (keys *reportKeys) targetDigest(caseID string, endpointKeyID int64) ([32]byte, error) {
	if keys == nil || !db.ValidateOpaqueID(caseID, "rpc_") || endpointKeyID <= 0 {
		return [32]byte{}, ErrInvalidRequest
	}
	keys.mu.RLock()
	defer keys.mu.RUnlock()
	if keys.closed {
		return [32]byte{}, ErrClosed
	}
	return keyedDigest(&keys.target, true, []byte(caseID), []byte(strconv.FormatInt(endpointKeyID, 10))), nil
}

func validRateValue(scope string, value []byte) bool {
	switch scope {
	case "ip":
		return len(value) == 16
	case "account":
		if len(value) == 0 || value[0] == '0' {
			return false
		}
		for _, b := range value {
			if b < '0' || b > '9' {
				return false
			}
		}
		return true
	case "fingerprint":
		return len(value) == sha256.Size
	case "global":
		return len(value) == 0
	default:
		return false
	}
}

func (keys *reportKeys) rateDigest(scope string, value []byte) ([32]byte, error) {
	if keys == nil || !validRateValue(scope, value) {
		return [32]byte{}, ErrInvalidRequest
	}
	keys.mu.RLock()
	defer keys.mu.RUnlock()
	if keys.closed {
		return [32]byte{}, ErrClosed
	}
	return keyedDigest(&keys.rate, false, framed([]byte(scope)), framed(value)), nil
}

func (keys *reportKeys) acceptanceActorDigest() ([32]byte, error) {
	if keys == nil {
		return [32]byte{}, ErrUnavailable
	}
	keys.mu.RLock()
	defer keys.mu.RUnlock()
	if keys.closed {
		return [32]byte{}, ErrClosed
	}
	return keyedDigest(&keys.idempotency, true, []byte("credential-report-acceptance-rail/v1")), nil
}

func (keys *reportKeys) fillRandom(destination []byte) error {
	if keys == nil || len(destination) == 0 {
		return ErrUnavailable
	}
	keys.mu.RLock()
	defer keys.mu.RUnlock()
	if keys.closed {
		return ErrClosed
	}
	keys.randomMu.Lock()
	_, err := io.ReadFull(keys.random, destination)
	keys.randomMu.Unlock()
	if err != nil {
		clear(destination)
		return ErrUnavailable
	}
	return nil
}

func sourceIPAAD(caseID string, materialHash [32]byte) []byte {
	aad := framed([]byte(caseID))
	material := framed(materialHash[:])
	aad = append(aad, material...)
	clear(material)
	return aad
}

func (keys *reportKeys) sealSourceIP(caseID string, materialHash [32]byte, canonicalIP [16]byte) ([]byte, error) {
	if keys == nil || !db.ValidateOpaqueID(caseID, "rpc_") {
		return nil, errInvalidEnvelope
	}
	keys.mu.RLock()
	defer keys.mu.RUnlock()
	if keys.closed {
		return nil, ErrClosed
	}
	block, err := aes.NewCipher(keys.sourceIP[:])
	if err != nil {
		return nil, errInvalidEnvelope
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil || gcm.NonceSize() != 12 || gcm.Overhead() != 16 {
		return nil, errInvalidEnvelope
	}
	nonce := make([]byte, 12)
	keys.randomMu.Lock()
	_, randomErr := io.ReadFull(keys.random, nonce)
	keys.randomMu.Unlock()
	if randomErr != nil {
		clear(nonce)
		return nil, errInvalidEnvelope
	}
	aad := sourceIPAAD(caseID, materialHash)
	sealed := gcm.Seal(nil, nonce, canonicalIP[:], aad)
	clear(aad)
	if len(sealed) != 32 {
		clear(nonce)
		clear(sealed)
		return nil, errInvalidEnvelope
	}
	envelope := make([]byte, envelopeBytes)
	envelope[0] = envelopeVersion
	copy(envelope[1:13], nonce)
	copy(envelope[13:], sealed)
	clear(nonce)
	clear(sealed)
	return envelope, nil
}

func (keys *reportKeys) openSourceIP(caseID string, materialHash [32]byte, envelope []byte) ([16]byte, error) {
	var out [16]byte
	if keys == nil || !db.ValidateOpaqueID(caseID, "rpc_") || len(envelope) != envelopeBytes || envelope[0] != envelopeVersion {
		return out, errInvalidEnvelope
	}
	keys.mu.RLock()
	defer keys.mu.RUnlock()
	if keys.closed {
		return out, ErrClosed
	}
	block, err := aes.NewCipher(keys.sourceIP[:])
	if err != nil {
		return out, errInvalidEnvelope
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil || gcm.NonceSize() != 12 || gcm.Overhead() != 16 {
		return out, errInvalidEnvelope
	}
	aad := sourceIPAAD(caseID, materialHash)
	plaintext, err := gcm.Open(nil, envelope[1:13], envelope[13:], aad)
	clear(aad)
	if err != nil || len(plaintext) != len(out) {
		clear(plaintext)
		return out, errInvalidEnvelope
	}
	copy(out[:], plaintext)
	clear(plaintext)
	return out, nil
}
