package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"io"
	"log/slog"
	"sync"
)

const (
	// MasterKeyBytes is the only accepted AES-256 master-key length.
	MasterKeyBytes = 32

	// MaxPlaintextBytes is a cryptographic allocation ceiling, not a request
	// body limit. Endpoint handlers must apply an equal or stricter semantic
	// and request-size limit before calling Seal.
	MaxPlaintextBytes = 64 << 10
)

var (
	// ErrInvalidMasterKey reports a key whose length is not exactly 32 bytes.
	ErrInvalidMasterKey = errors.New("secret: invalid master key")
	// ErrInvalidPlaintext reports an empty or over-limit plaintext.
	ErrInvalidPlaintext = errors.New("secret: invalid plaintext")
	// ErrInvalidCiphertext deliberately covers all envelope, decoding, key,
	// and authentication failures so callers cannot turn errors into an oracle.
	ErrInvalidCiphertext = errors.New("secret: invalid ciphertext")
	// ErrClosed reports use after the vault's master-key copy was cleared.
	ErrClosed = errors.New("secret: vault is closed")
	// ErrUnavailable reports failure of the operating system random source.
	ErrUnavailable = errors.New("secret: cryptographic operation unavailable")
	// ErrInvalidContext reports a malformed endpoint-credential context. It
	// never includes the rejected identifiers or origin.
	ErrInvalidContext = errors.New("secret: invalid credential context")
	// ErrInvalidSubkeyInfo reports a DeriveSubkey call whose info label is
	// empty or oversized. The error never includes the info value.
	ErrInvalidSubkeyInfo = errors.New("secret: invalid subkey info")
)

const (
	// SubkeyBytes is the length of a DeriveSubkey output. It matches the
	// SHA-256 output size so one HKDF block produces the whole subkey.
	SubkeyBytes        = sha256.Size
	maxSubkeyInfoBytes = 256
)

// ContextOpener is the only recoverable-secret capability exposed to outbound
// code. It cannot open a legacy envelope or seal new material.
type ContextOpener interface {
	OpenForContext(ciphertext string, context EndpointKeyContext) ([]byte, error)
}

// ContextCodec adds contextual sealing for the persistence boundary.
type ContextCodec interface {
	ContextOpener
	SealForContext(plaintext []byte, context EndpointKeyContext) (string, error)
}

// Codec extends the runtime contextual boundary with legacy operations needed
// by persistence migration and restore compatibility.
type Codec interface {
	ContextCodec
	Seal(plaintext []byte) (string, error)
	Open(ciphertext string) ([]byte, error)
}

// Vault holds the process master key and implements authenticated encryption.
// It is safe for concurrent use. A Vault must not be copied after first use.
type Vault struct {
	noCopy noCopy
	mu     sync.RWMutex
	key    [MasterKeyBytes]byte
	closed bool
}

// noCopy lets go vet reject accidental value copies of a live Vault.
type noCopy struct{}

func (*noCopy) Lock()   {}
func (*noCopy) Unlock() {}

var _ Codec = (*Vault)(nil)

// New creates a Vault by copying exactly 32 bytes from masterKey. The caller
// retains ownership of masterKey and should clear that input immediately after
// New returns. Errors never include key material.
func New(masterKey []byte) (*Vault, error) {
	if len(masterKey) != MasterKeyBytes {
		return nil, ErrInvalidMasterKey
	}
	v := &Vault{}
	copy(v.key[:], masterKey)
	return v, nil
}

// Seal encrypts a non-empty plaintext into the legacy envelope. It is retained
// for restore and migration tooling; new persistence paths must call
// SealForContext. A fresh random nonce is used for every call.
func (v *Vault) Seal(plaintext []byte) (string, error) {
	return v.seal(plaintext, []byte(legacyEnvelopeHeader), legacyEnvelopePrefix)
}

// Open authenticates and decrypts only a legacy envelope. It deliberately does
// not accept the contextual envelope. Runtime outbound paths must call
// OpenForContext instead.
func (v *Vault) Open(encoded string) ([]byte, error) {
	return v.open(encoded, legacyEnvelopePrefix, []byte(legacyEnvelopeHeader))
}

// SealForContext encrypts a non-empty endpoint credential into the contextual
// envelope. Purpose, version, user, endpoint, key, and canonical origin are
// authenticated through an unambiguous length-prefixed associated-data
// encoding. The associated data is not persisted in the envelope.
func (v *Vault) SealForContext(plaintext []byte, context EndpointKeyContext) (string, error) {
	aad, ok := context.associatedData()
	if !ok {
		return "", ErrInvalidContext
	}
	defer clear(aad)
	return v.seal(plaintext, aad, contextEnvelopePrefix)
}

// OpenForContext authenticates and decrypts only a contextual envelope under
// the exact supplied identity and canonical origin. Malformed envelopes,
// invalid contexts, wrong keys, and every context mismatch collapse to
// ErrInvalidCiphertext.
func (v *Vault) OpenForContext(encoded string, context EndpointKeyContext) ([]byte, error) {
	aad, ok := context.associatedData()
	if !ok {
		return nil, ErrInvalidCiphertext
	}
	defer clear(aad)
	return v.open(encoded, contextEnvelopePrefix, aad)
}

func (v *Vault) seal(plaintext, aad []byte, prefix string) (string, error) {
	if len(plaintext) == 0 || len(plaintext) > MaxPlaintextBytes {
		return "", ErrInvalidPlaintext
	}
	if v == nil {
		return "", ErrClosed
	}

	v.mu.RLock()
	defer v.mu.RUnlock()
	if v.closed {
		return "", ErrClosed
	}
	gcm, err := v.gcmLocked()
	if err != nil {
		return "", ErrUnavailable
	}
	nonce := make([]byte, gcmNonceBytes)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", ErrUnavailable
	}
	sealed := gcm.Seal(nil, nonce, plaintext, aad)
	return encodeEnvelope(prefix, nonce, sealed), nil
}

func (v *Vault) open(encoded, prefix string, aad []byte) ([]byte, error) {
	nonce, sealed, ok := parseEnvelope(encoded, prefix)
	if !ok {
		return nil, ErrInvalidCiphertext
	}
	if v == nil {
		return nil, ErrClosed
	}

	v.mu.RLock()
	defer v.mu.RUnlock()
	if v.closed {
		return nil, ErrClosed
	}
	gcm, err := v.gcmLocked()
	if err != nil {
		return nil, ErrUnavailable
	}
	plaintext, err := gcm.Open(nil, nonce, sealed, aad)
	if err != nil {
		clear(plaintext)
		return nil, ErrInvalidCiphertext
	}
	if len(plaintext) == 0 || len(plaintext) > MaxPlaintextBytes {
		clear(plaintext)
		return nil, ErrInvalidCiphertext
	}
	return plaintext, nil
}

// DeriveSubkey derives a purpose-bound subkey from the master key using
// HKDF-SHA256 (RFC 5869). info is a non-empty purpose label; different labels
// yield independent subkeys, so one rail cannot accidentally reuse another
// rail's derived material. The master key is read under the read lock; the
// caller owns the returned subkey and should clear it once it has derived the
// one-way fingerprint it needs. The output never reveals the master key in a
// reversible way.
func (v *Vault) DeriveSubkey(info []byte) ([]byte, error) {
	if v == nil {
		return nil, ErrClosed
	}
	if len(info) == 0 || len(info) > maxSubkeyInfoBytes {
		return nil, ErrInvalidSubkeyInfo
	}
	v.mu.RLock()
	defer v.mu.RUnlock()
	if v.closed {
		return nil, ErrClosed
	}
	// HKDF-Extract: PRK = HMAC-SHA256(salt, IKM). RFC 5869 uses a string of
	// HashLen zero bytes when salt is empty.
	salt := make([]byte, sha256.Size)
	prk := hmacSHA256(salt, v.key[:])
	clear(salt)
	// HKDF-Expand: a SubkeyBytes output is exactly one SHA-256 block, so T(1)
	// is the whole output: HMAC-SHA256(PRK, info || 0x01).
	mac := hmac.New(sha256.New, prk[:])
	mac.Write(info)
	mac.Write([]byte{0x01})
	sum := mac.Sum(nil)
	out := make([]byte, SubkeyBytes)
	copy(out, sum)
	clear(prk[:])
	clear(sum)
	return out, nil
}

func hmacSHA256(key, data []byte) [sha256.Size]byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(data)
	var out [sha256.Size]byte
	copy(out[:], mac.Sum(nil))
	return out
}

// Close clears the Vault's retained master-key bytes on a best-effort basis.
// It is idempotent and waits for in-flight Seal/Open calls to finish. Go's
// runtime may retain other copies or expanded cipher state; callers must not
// treat Close as a complete memory-erasure guarantee.
func (v *Vault) Close() error {
	if v == nil {
		return nil
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if !v.closed {
		clear(v.key[:])
		v.closed = true
	}
	return nil
}

// String prevents routine formatting from exposing internal key state.
func (*Vault) String() string { return "[redacted secret vault]" }

// GoString prevents %#v formatting from exposing internal key state.
func (*Vault) GoString() string { return "[redacted secret vault]" }

// LogValue prevents structured logging from reflecting internal key state.
func (*Vault) LogValue() slog.Value { return slog.StringValue("[redacted secret vault]") }

func (v *Vault) gcmLocked() (cipher.AEAD, error) {
	block, err := aes.NewCipher(v.key[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCMWithNonceSize(block, gcmNonceBytes)
}
