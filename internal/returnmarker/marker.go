// Package returnmarker authenticates short-lived, opaque outbound provenance.
// A returned marker is a signal for review, never an authorization decision.
package returnmarker

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

const (
	Header         = "Nonbiri-Request-Origin"
	Lifetime       = 30 * time.Minute
	maxHeaderBytes = 768
	keyPurpose     = "outbound-return-marker/v1"
)

type KeyDeriver interface {
	DeriveGenerationTwoSubkey([]byte) ([]byte, error)
}

type Source struct {
	RequestID     string `json:"r,omitempty"`
	EndpointID    int64  `json:"e,omitempty"`
	EndpointKeyID int64  `json:"k,omitempty"`
	DonationKeyID int64  `json:"d,omitempty"`
}

type Evidence struct {
	Source
	IssuedAt int64    `json:"t"`
	Origin   [32]byte `json:"o"`
}

type contextKey struct{}

func WithSource(ctx context.Context, source Source) context.Context {
	return context.WithValue(ctx, contextKey{}, source)
}

type Marker struct {
	aead cipher.AEAD
	now  func() time.Time
}

func New(keys KeyDeriver) (*Marker, error) {
	if keys == nil {
		return nil, errors.New("return marker: key deriver is required")
	}
	key, err := keys.DeriveGenerationTwoSubkey([]byte(keyPurpose))
	if err != nil {
		return nil, err
	}
	defer clear(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCMWithRandomNonce(block)
	if err != nil {
		return nil, err
	}
	return &Marker{aead: aead, now: time.Now}, nil
}

// Stamp runs after request adaptation. A caller cannot preserve, remove, or
// replace the server marker through an upstream-header override.
func (m *Marker) Stamp(req *http.Request, baseURL string) {
	Strip(req.Header)
	if m == nil {
		return
	}
	source, _ := req.Context().Value(contextKey{}).(Source)
	if !validSource(source) {
		source = Source{}
	}
	evidence := Evidence{Source: source, IssuedAt: m.now().Unix(), Origin: sha256.Sum256([]byte(baseURL))}
	body, err := json.Marshal(evidence)
	if err != nil {
		return
	}
	sealed := m.aead.Seal(nil, nil, body, []byte(keyPurpose))
	if req.Header == nil {
		req.Header = make(http.Header)
	}
	req.Header.Set(Header, "v1."+base64.RawURLEncoding.EncodeToString(sealed))
}

// Verify ignores duplicates (including differently cased fields), combined
// values, malformed input, future timestamps and expired markers.
func (m *Marker) Verify(headers http.Header) (Evidence, bool) {
	if m == nil {
		return Evidence{}, false
	}
	value, count, fields := "", 0, 0
	for name, values := range headers {
		if strings.EqualFold(name, Header) {
			fields++
			count += len(values)
			if len(values) == 1 {
				value = values[0]
			}
		}
	}
	if fields != 1 || count != 1 || len(value) > maxHeaderBytes || !strings.HasPrefix(value, "v1.") || strings.ContainsAny(value, "\x00\r\n\t ,") {
		return Evidence{}, false
	}
	sealed, err := base64.RawURLEncoding.Strict().DecodeString(value[3:])
	if err != nil {
		return Evidence{}, false
	}
	body, err := m.aead.Open(nil, nil, sealed, []byte(keyPurpose))
	if err != nil {
		return Evidence{}, false
	}
	var evidence Evidence
	if json.Unmarshal(body, &evidence) != nil || !validSource(evidence.Source) {
		return Evidence{}, false
	}
	now := m.now().Unix()
	if evidence.IssuedAt < 0 || evidence.IssuedAt > now || now-evidence.IssuedAt >= int64(Lifetime/time.Second) {
		return Evidence{}, false
	}
	return evidence, true
}

func validSource(source Source) bool {
	return (source.RequestID == "" || validRequestID(source.RequestID)) &&
		source.EndpointID >= 0 && source.EndpointKeyID >= 0 && source.DonationKeyID >= 0
}

func validRequestID(id string) bool {
	if len(id) != 26 || !strings.HasPrefix(id, "req_") {
		return false
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(id[4:])
	return err == nil && len(raw) == 16
}

func Strip(headers http.Header) {
	for name := range headers {
		if strings.EqualFold(name, Header) {
			delete(headers, name)
		}
	}
}
