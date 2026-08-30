package secret

import (
	"encoding/base64"
	"strings"
)

const (
	contextEnvelopeHeader = "nbsec:v2:aes-256-gcm"
	contextEnvelopePrefix = contextEnvelopeHeader + ":"

	gcmNonceBytes = 12
	gcmTagBytes   = 16

	// MaxEnvelopeBytes bounds the complete textual credential envelope before
	// parsing. The plaintext ceiling is lower; this independent limit prevents
	// an untrusted ciphertext string from causing a large parser allocation.
	MaxEnvelopeBytes = 128 << 10
)

// EnvelopeVersion identifies the sole accepted Generation 2 envelope format.
// The version is only a syntactic classification; OpenForGenerationTwoContext
// remains the authority for authentication and decryption.
type EnvelopeVersion uint8

const EnvelopeVersionV2 EnvelopeVersion = 2

// ParseEnvelopeVersion accepts only a complete, canonical Generation 2
// envelope. Unknown and retired versions, malformed encodings, and lengths
// outside the contract collapse to ErrInvalidCiphertext.
func ParseEnvelopeVersion(encoded string) (EnvelopeVersion, error) {
	if _, _, ok := parseEnvelope(encoded); ok {
		return EnvelopeVersionV2, nil
	}
	return 0, ErrInvalidCiphertext
}

func encodeEnvelope(nonce, sealed []byte) string {
	nonceText := base64.RawURLEncoding.EncodeToString(nonce)
	sealedText := base64.RawURLEncoding.EncodeToString(sealed)
	var out strings.Builder
	out.Grow(len(contextEnvelopePrefix) + len(nonceText) + 1 + len(sealedText))
	out.WriteString(contextEnvelopePrefix)
	out.WriteString(nonceText)
	out.WriteByte(':')
	out.WriteString(sealedText)
	return out.String()
}

func parseEnvelope(encoded string) ([]byte, []byte, bool) {
	maxNonceText := base64.RawURLEncoding.EncodedLen(gcmNonceBytes)
	maxSealedText := base64.RawURLEncoding.EncodedLen(MaxPlaintextBytes + gcmTagBytes)
	if len(encoded) > MaxEnvelopeBytes ||
		len(encoded) > len(contextEnvelopePrefix)+maxNonceText+1+maxSealedText ||
		!strings.HasPrefix(encoded, contextEnvelopePrefix) {
		return nil, nil, false
	}

	rest := encoded[len(contextEnvelopePrefix):]
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
		clear(nonce)
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
		clear(decoded)
		return nil, false
	}
	if base64.RawURLEncoding.EncodeToString(decoded) != encoded {
		clear(decoded)
		return nil, false
	}
	return decoded, true
}
