package secret

import (
	"encoding/binary"
	"log/slog"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	endpointKeyPurpose       = "endpoint-key"
	contextEnvelopeVersion   = "v2"
	maxCanonicalOriginBytes  = 4096
	contextAssociatedFields  = 6
	contextLengthPrefixBytes = 4
)

// EndpointKeyContext is the authenticated identity of one persisted endpoint
// credential. Its fields are deliberately opaque so callers cannot mutate a
// context after construction. canonicalOrigin must come from the egress
// package's canonical endpoint helper; paths are intentionally excluded.
type EndpointKeyContext struct {
	userID          int64
	endpointID      int64
	keyID           int64
	canonicalOrigin string
}

// NewEndpointKeyContext validates the identifier and canonical-origin shape
// used by the contextual envelope. It never includes supplied values in an
// error. Production callers must pass the origin returned by the egress
// package's canonical endpoint helper rather than deriving it independently.
func NewEndpointKeyContext(userID, endpointID, keyID int64, canonicalOrigin string) (EndpointKeyContext, error) {
	ctx := EndpointKeyContext{
		userID:          userID,
		endpointID:      endpointID,
		keyID:           keyID,
		canonicalOrigin: canonicalOrigin,
	}
	if !ctx.valid() {
		return EndpointKeyContext{}, ErrInvalidContext
	}
	return ctx, nil
}

func (c EndpointKeyContext) valid() bool {
	return c.userID > 0 && c.endpointID > 0 && c.keyID > 0 && validCanonicalOrigin(c.canonicalOrigin)
}

// associatedData encodes each field with a four-byte big-endian byte length.
// Numeric identifiers use their unique base-10 form. Boundaries therefore
// cannot collide even when adjacent values have ambiguous textual forms.
func (c EndpointKeyContext) associatedData() ([]byte, bool) {
	if !c.valid() {
		return nil, false
	}
	fields := [...]string{
		endpointKeyPurpose,
		contextEnvelopeVersion,
		strconv.FormatInt(c.userID, 10),
		strconv.FormatInt(c.endpointID, 10),
		strconv.FormatInt(c.keyID, 10),
		c.canonicalOrigin,
	}
	total := contextAssociatedFields * contextLengthPrefixBytes
	for _, field := range fields {
		total += len(field)
	}
	aad := make([]byte, 0, total)
	var size [contextLengthPrefixBytes]byte
	for _, field := range fields {
		binary.BigEndian.PutUint32(size[:], uint32(len(field)))
		aad = append(aad, size[:]...)
		aad = append(aad, field...)
	}
	return aad, true
}

func validCanonicalOrigin(origin string) bool {
	if origin == "" || len(origin) > maxCanonicalOriginBytes || !utf8.ValidString(origin) {
		return false
	}
	for _, r := range origin {
		if r < 0x21 || r == 0x7f {
			return false
		}
	}
	u, err := url.Parse(origin)
	if err != nil || u.Opaque != "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Path != "" || u.RawPath != "" {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	host := u.Hostname()
	port := u.Port()
	if host == "" || port == "" || strings.HasSuffix(u.Host, ":") || strings.ContainsAny(host, "\\@%") {
		return false
	}
	portNumber, err := strconv.ParseUint(port, 10, 16)
	if err != nil || portNumber == 0 || strconv.FormatUint(portNumber, 10) != port {
		return false
	}
	if addr, err := netip.ParseAddr(host); err == nil {
		if addr.Zone() != "" || addr.Unmap().String() != host {
			return false
		}
	} else {
		if len(host) > 253 || host != strings.ToLower(host) || strings.HasSuffix(host, ".") {
			return false
		}
		for _, label := range strings.Split(host, ".") {
			if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
				return false
			}
			for _, r := range label {
				if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
					continue
				}
				return false
			}
		}
	}
	return origin == u.Scheme+"://"+net.JoinHostPort(host, port)
}

// String prevents routine formatting from exposing authenticated context.
func (EndpointKeyContext) String() string { return "[redacted credential context]" }

// GoString prevents detailed formatting from exposing authenticated context.
func (EndpointKeyContext) GoString() string { return "[redacted credential context]" }

// LogValue prevents structured logging from exposing authenticated context.
func (EndpointKeyContext) LogValue() slog.Value {
	return slog.StringValue("[redacted credential context]")
}
