package secret

import (
	"encoding/binary"
	"log/slog"
)

const contextLengthPrefixBytes = 4

// GenerationTwoEndpointKeyContext is the opaque cryptographic identity of a
// Generation 2 endpoint-key secret. The context contains only the random
// per-secret context_id; database ownership and routing identifiers are not
// authenticated into the credential envelope.
type GenerationTwoEndpointKeyContext struct {
	contextID   [16]byte
	initialized bool
}

// NewGenerationTwoEndpointKeyContext accepts exactly one BLOB16 context_id.
// The input is copied, so subsequent caller mutation cannot change the AAD.
func NewGenerationTwoEndpointKeyContext(contextID []byte) (GenerationTwoEndpointKeyContext, error) {
	if len(contextID) != len(GenerationTwoEndpointKeyContext{}.contextID) {
		return GenerationTwoEndpointKeyContext{}, ErrInvalidContext
	}
	var context GenerationTwoEndpointKeyContext
	copy(context.contextID[:], contextID)
	context.initialized = true
	return context, nil
}

func (c GenerationTwoEndpointKeyContext) valid() bool {
	return c.initialized
}

// associatedData implements the frozen
// F("generation_two") || F("endpoint-key-secret/v2") || FB(context_id)
// encoding. Every field is prefixed by its four-byte big-endian byte length.
func (c GenerationTwoEndpointKeyContext) associatedData() ([]byte, bool) {
	if !c.valid() {
		return nil, false
	}
	fields := [...][]byte{
		[]byte("generation_two"),
		[]byte("endpoint-key-secret/v2"),
		c.contextID[:],
	}
	total := len(fields) * contextLengthPrefixBytes
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

// ContextID returns a defensive copy for the database row binding. It must
// never be used as an owner identifier, ordinary lookup key, log value, or
// export field.
func (c GenerationTwoEndpointKeyContext) ContextID() []byte {
	if !c.valid() {
		return nil
	}
	out := make([]byte, len(c.contextID))
	copy(out, c.contextID[:])
	return out
}

// String prevents routine formatting from exposing the cryptographic context.
func (GenerationTwoEndpointKeyContext) String() string { return "[redacted credential context]" }

// GoString prevents %#v formatting from exposing the cryptographic context.
func (GenerationTwoEndpointKeyContext) GoString() string { return "[redacted credential context]" }

// LogValue prevents structured logging from exposing the cryptographic
// context, including its random context_id.
func (GenerationTwoEndpointKeyContext) LogValue() slog.Value {
	return slog.StringValue("[redacted credential context]")
}
