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
	// and request-size limit before sealing a credential.
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
	// ErrInvalidContext reports a malformed Generation 2 credential context.
	// It never includes the rejected context bytes.
	ErrInvalidContext = errors.New("secret: invalid credential context")
	// ErrInvalidSubkeyInfo reports a DeriveGenerationTwoSubkey call whose info label is
	// empty or oversized. The error never includes the info value.
	ErrInvalidSubkeyInfo = errors.New("secret: invalid subkey info")
)

const (
	// SubkeyBytes is the length of a DeriveGenerationTwoSubkey output. It matches the
	// SHA-256 output size so one HKDF block produces the whole subkey.
	SubkeyBytes           = sha256.Size
	maxSubkeyInfoBytes    = 256
	generationTwoHKDFSalt = "NonbiriAPI/generation/2"
)

// GenerationTwoContextCodec is the only recoverable-secret capability
// exposed by the Generation 2 persistence boundary. It accepts only the
// exact Generation 2 context and nbsec:v2 envelope.
type GenerationTwoContextCodec interface {
	OpenForGenerationTwoContext(ciphertext string, context GenerationTwoEndpointKeyContext) ([]byte, error)
	SealForGenerationTwoContext(plaintext []byte, context GenerationTwoEndpointKeyContext) (string, error)
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

var _ GenerationTwoContextCodec = (*Vault)(nil)

// New creates a Vault by copying exactly 32 bytes from masterKey. The caller
// retains ownership of masterKey and should clear that input immediately
// after New returns. Errors never include key material.
func New(masterKey []byte) (*Vault, error) {
	if len(masterKey) != MasterKeyBytes {
		return nil, ErrInvalidMasterKey
	}
	v := &Vault{}
	copy(v.key[:], masterKey)
	return v, nil
}

// SealForGenerationTwoContext seals a credential with the exact Generation 2
// context AAD and nbsec:v2 authenticated envelope format.
func (v *Vault) SealForGenerationTwoContext(plaintext []byte, context GenerationTwoEndpointKeyContext) (string, error) {
	aad, ok := context.associatedData()
	if !ok {
		return "", ErrInvalidContext
	}
	defer clear(aad)
	return v.seal(plaintext, aad)
}

// OpenForGenerationTwoContext opens only a Generation 2 context-bound
// envelope. Envelope, context, key, and authentication failures remain
// indistinguishable to callers.
func (v *Vault) OpenForGenerationTwoContext(encoded string, context GenerationTwoEndpointKeyContext) ([]byte, error) {
	aad, ok := context.associatedData()
	if !ok {
		return nil, ErrInvalidCiphertext
	}
	defer clear(aad)
	return v.open(encoded, aad)
}

func (v *Vault) seal(plaintext, aad []byte) (string, error) {
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
		clear(nonce)
		return "", ErrUnavailable
	}
	sealed := gcm.Seal(nil, nonce, plaintext, aad)
	encoded := encodeEnvelope(nonce, sealed)
	clear(nonce)
	clear(sealed)
	return encoded, nil
}

func (v *Vault) open(encoded string, aad []byte) ([]byte, error) {
	nonce, sealed, ok := parseEnvelope(encoded)
	if !ok {
		return nil, ErrInvalidCiphertext
	}
	defer clear(nonce)
	defer clear(sealed)
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

// DeriveGenerationTwoSubkey derives a purpose-bound subkey from the master key
// using the fixed Generation 2 HKDF-SHA256 salt. info is a non-empty purpose
// label; different labels yield independent subkeys, so one rail cannot
// accidentally reuse another rail's derived material. The caller owns the
// returned subkey and should clear it once it has derived the one-way
// fingerprint it needs.
func (v *Vault) DeriveGenerationTwoSubkey(info []byte) ([]byte, error) {
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

	// HKDF-Extract: PRK = HMAC-SHA256("NonbiriAPI/generation/2", IKM).
	prk := hmacSHA256([]byte(generationTwoHKDFSalt), v.key[:])
	// HKDF-Expand: a SubkeyBytes output is exactly one SHA-256 block, so T(1)
	// is the whole output: HMAC-SHA256(PRK, info || 0x01).
	mac := hmac.New(sha256.New, prk[:])
	_, _ = mac.Write(info)
	_, _ = mac.Write([]byte{0x01})
	sum := mac.Sum(nil)
	out := make([]byte, SubkeyBytes)
	copy(out, sum)
	clear(prk[:])
	clear(sum)
	return out, nil
}

func hmacSHA256(key, data []byte) [sha256.Size]byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(data)
	var out [sha256.Size]byte
	copy(out[:], mac.Sum(nil))
	return out
}

// Close clears the Vault's retained master-key bytes on a best-effort basis.
// It is idempotent and waits for in-flight operations to finish. Go's runtime
// may retain other copies or expanded cipher state; callers must not treat
// Close as a complete memory-erasure guarantee.
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
