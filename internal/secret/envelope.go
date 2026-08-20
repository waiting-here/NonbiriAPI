package secret

import (
	"encoding/base64"
	"strings"
)

const (
	legacyEnvelopeHeader  = "nbsec:v1:aes-256-gcm"
	legacyEnvelopePrefix  = legacyEnvelopeHeader + ":"
	contextEnvelopeHeader = "nbsec:v2:aes-256-gcm"
	contextEnvelopePrefix = contextEnvelopeHeader + ":"

	gcmNonceBytes = 12
	gcmTagBytes   = 16
)

// These aliases keep the legacy envelope implementation explicit while
// persisted endpoint credentials use the contextual form.
const (
	envelopeHeader = legacyEnvelopeHeader
	envelopePrefix = legacyEnvelopePrefix
)

// EnvelopeVersion identifies a fully canonical envelope encoding. It does not
// authenticate the payload; Open or OpenForContext remains the authority for
// that operation.
type EnvelopeVersion uint8

const (
	EnvelopeVersionV1 EnvelopeVersion = 1
	EnvelopeVersionV2 EnvelopeVersion = 2
)

// ParseEnvelopeVersion accepts only a complete, canonical v1 or v2 envelope.
// Unknown future versions and malformed encodings collapse to
// ErrInvalidCiphertext without echoing any input material.
func ParseEnvelopeVersion(encoded string) (EnvelopeVersion, error) {
	switch {
	case strings.HasPrefix(encoded, legacyEnvelopePrefix):
		if _, _, ok := parseEnvelope(encoded, legacyEnvelopePrefix); ok {
			return EnvelopeVersionV1, nil
		}
	case strings.HasPrefix(encoded, contextEnvelopePrefix):
		if _, _, ok := parseEnvelope(encoded, contextEnvelopePrefix); ok {
			return EnvelopeVersionV2, nil
		}
	}
	return 0, ErrInvalidCiphertext
}

func encodeEnvelope(prefix string, nonce, sealed []byte) string {
	nonceText := base64.RawURLEncoding.EncodeToString(nonce)
	sealedText := base64.RawURLEncoding.EncodeToString(sealed)
	var out strings.Builder
	out.Grow(len(prefix) + len(nonceText) + 1 + len(sealedText))
	out.WriteString(prefix)
	out.WriteString(nonceText)
	out.WriteByte(':')
	out.WriteString(sealedText)
	return out.String()
}

func parseEnvelope(encoded, prefix string) ([]byte, []byte, bool) {
	maxNonceText := base64.RawURLEncoding.EncodedLen(gcmNonceBytes)
	maxSealedText := base64.RawURLEncoding.EncodedLen(MaxPlaintextBytes + gcmTagBytes)
	maxEnvelopeBytes := len(prefix) + maxNonceText + 1 + maxSealedText
	if len(encoded) > maxEnvelopeBytes || !strings.HasPrefix(encoded, prefix) {
		return nil, nil, false
	}

	rest := encoded[len(prefix):]
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
