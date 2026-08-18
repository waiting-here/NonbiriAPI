package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
)

const (
	// MasterKeyBytes is the only accepted AES-256 master-key length.
	MasterKeyBytes = 32

	// MaxPlaintextBytes is a cryptographic allocation ceiling, not a request
	// body limit. Endpoint handlers must apply an equal or stricter semantic
	// and request-size limit before calling Seal.
	MaxPlaintextBytes = 64 << 10

	envelopeHeader = "nbsec:v1:aes-256-gcm"
	envelopePrefix = envelopeHeader + ":"
	gcmNonceBytes  = 12
	gcmTagBytes    = 16
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

// Codec is the narrow recoverable-secret boundary used by persistence code.
// Implementations must never return plaintext from formatting or logging
// methods. Decryption is available only through an explicit Open call.
type Codec interface {
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

// Seal encrypts a non-empty plaintext into the documented, persistence-safe
// envelope. A fresh random nonce is used for every call. Plaintext larger than
// MaxPlaintextBytes is rejected before cryptographic allocation.
func (v *Vault) Seal(plaintext []byte) (string, error) {
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
	sealed := gcm.Seal(nil, nonce, plaintext, []byte(envelopeHeader))

	nonceText := base64.RawURLEncoding.EncodeToString(nonce)
	sealedText := base64.RawURLEncoding.EncodeToString(sealed)
	var out strings.Builder
	out.Grow(len(envelopePrefix) + len(nonceText) + 1 + len(sealedText))
	out.WriteString(envelopePrefix)
	out.WriteString(nonceText)
	out.WriteByte(':')
	out.WriteString(sealedText)
	return out.String(), nil
}

// Open authenticates and decrypts a ciphertext produced by Seal. Every
// malformed envelope, wrong-key use, and authentication failure returns
// ErrInvalidCiphertext with no input material in the error. The caller owns the
// returned plaintext and should clear it promptly.
func (v *Vault) Open(encoded string) ([]byte, error) {
	nonce, sealed, ok := parseEnvelope(encoded)
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
	plaintext, err := gcm.Open(nil, nonce, sealed, []byte(envelopeHeader))
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

func parseEnvelope(encoded string) ([]byte, []byte, bool) {
	maxNonceText := base64.RawURLEncoding.EncodedLen(gcmNonceBytes)
	maxSealedText := base64.RawURLEncoding.EncodedLen(MaxPlaintextBytes + gcmTagBytes)
	maxEnvelopeBytes := len(envelopePrefix) + maxNonceText + 1 + maxSealedText
	if len(encoded) > maxEnvelopeBytes || !strings.HasPrefix(encoded, envelopePrefix) {
		return nil, nil, false
	}

	rest := encoded[len(envelopePrefix):]
	nonceText, sealedText, found := strings.Cut(rest, ":")
	if !found || strings.ContainsRune(sealedText, ':') {
		return nil, nil, false
	}
	if len(nonceText) != maxNonceText {
		return nil, nil, false
	}

	nonce, ok := decodeCanonicalRawURL(nonceText, gcmNonceBytes, gcmNonceBytes)
	if !ok {
		return nil, nil, false
	}
	sealed, ok := decodeCanonicalRawURL(sealedText, gcmTagBytes+1, MaxPlaintextBytes+gcmTagBytes)
	if !ok {
		return nil, nil, false
	}
	return nonce, sealed, true
}

func decodeCanonicalRawURL(encoded string, minBytes, maxBytes int) ([]byte, bool) {
	if encoded == "" || len(encoded) > base64.RawURLEncoding.EncodedLen(maxBytes) {
		return nil, false
	}
	for i := 0; i < len(encoded); i++ {
		c := encoded[i]
		if !((c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') ||
			(c >= '0' && c <= '9') || c == '-' || c == '_') {
			return nil, false
		}
	}
	decoded, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(decoded) < minBytes || len(decoded) > maxBytes {
		return nil, false
	}
	if base64.RawURLEncoding.EncodeToString(decoded) != encoded {
		return nil, false
	}
	return decoded, true
}
