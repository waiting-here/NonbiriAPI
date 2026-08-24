// Package contract defines the connector-neutral execution values shared by
// forwarding, accounting, and protocol implementations. It deliberately
// contains no vendor JSON representation and no routing or persistence logic.
package contract

import (
	"context"
	"log/slog"
	"sync"

	"github.com/waiting-here/NonbiriAPI/internal/backend"
)

// Type is one closed-world connector identifier admitted by the process
// registry. Persisted strings are accepted only after registry validation.
type Type string

const (
	TypeOpenAICompatible    Type = "openai-compatible"
	TypeAnthropicCompatible Type = "anthropic-compatible"
)

// Capability is one fidelity guarantee made by a connector descriptor.
type Capability uint64

const (
	CapabilityText Capability = 1 << iota
	CapabilitySystem
	CapabilityDeveloper
	CapabilityImages
	CapabilityTools
	CapabilityToolChoice
	CapabilityParallelTools
	CapabilityStream
	CapabilitySampling
	CapabilityUnknownOpenAIFields
	CapabilityModelDiscovery
)

const KnownCapabilities = CapabilityText |
	CapabilitySystem |
	CapabilityDeveloper |
	CapabilityImages |
	CapabilityTools |
	CapabilityToolChoice |
	CapabilityParallelTools |
	CapabilityStream |
	CapabilitySampling |
	CapabilityUnknownOpenAIFields |
	CapabilityModelDiscovery

// CapabilitySet is an immutable bit set in a registry descriptor.
type CapabilitySet uint64

func (s CapabilitySet) Has(capability Capability) bool {
	return capability != 0 && uint64(s)&uint64(capability) == uint64(capability)
}

func (s CapabilitySet) HasAll(required CapabilitySet) bool {
	return uint64(s)&uint64(required) == uint64(required)
}

// FailureKind is a stable, connector-neutral failure classification. Only an
// uncommitted FailureUpstream is eligible for silent retry.
type FailureKind uint8

const (
	FailureNone FailureKind = iota
	FailureUpstream
	FailureInternal
	FailureCanceled
	FailureSink
)

// Usage is the normalized four-bucket token report. Present distinguishes an
// absent/malformed report from a valid report containing four zero values.
type Usage struct {
	UncachedInputTokens   int64
	CacheWriteInputTokens int64
	CacheReadInputTokens  int64
	OutputTokens          int64
	Present               bool
}

// AttemptResult contains only bounded metadata. Diagnostic is a locally
// generated safe category; it must never contain an upstream body, URL,
// request value, credential, or raw transport error.
type AttemptResult struct {
	Success         bool
	Committed       bool
	SinkFailed      bool
	Failure         FailureKind
	Diagnostic      string
	UpstreamStatus  int
	ClientStatus    int
	EndpointBaseURL string
	Usage           Usage
}

// Target is the final revalidated physical target of one attempt. Fields are
// private so generic encoding and routine formatting cannot disclose the
// owner-visible base URL or upstream model through an observer or log hook.
type Target struct {
	connectorType Type
	baseURL       string
	upstreamModel string
}

func NewTarget(connectorType Type, baseURL, upstreamModel string) Target {
	return Target{connectorType: connectorType, baseURL: baseURL, upstreamModel: upstreamModel}
}

func (t Target) Type() Type            { return t.connectorType }
func (t Target) BaseURL() string       { return t.baseURL }
func (t Target) UpstreamModel() string { return t.upstreamModel }
func (Target) String() string          { return "[redacted upstream target]" }
func (Target) GoString() string        { return "[redacted upstream target]" }
func (Target) LogValue() slog.Value {
	return slog.StringValue("[redacted upstream target]")
}

// AttemptPolicy is the immutable policy projection for exactly one attempt.
// Additional fields belong here only when their policy implementation needs
// them; the initial projection carries the existing origin-scoped identifier.
type AttemptPolicy struct {
	SafetyIdentifier string
}

func (AttemptPolicy) String() string   { return "[redacted attempt policy]" }
func (AttemptPolicy) GoString() string { return "[redacted attempt policy]" }
func (AttemptPolicy) LogValue() slog.Value {
	return slog.StringValue("[redacted attempt policy]")
}

// ShortLivedSecret owns the plaintext and ciphertext guard aliases handed to
// one attempt. Take transfers them exactly once; Clear is an idempotent
// backstop for every pre-dispatch/error path.
type ShortLivedSecret struct {
	mu         sync.Mutex
	plaintext  []byte
	ciphertext []byte
	taken      bool
}

func NewShortLivedSecret(plaintext, ciphertext []byte) *ShortLivedSecret {
	return &ShortLivedSecret{plaintext: plaintext, ciphertext: ciphertext}
}

func (s *ShortLivedSecret) Take() (plaintext, ciphertext []byte, ok bool) {
	if s == nil {
		return nil, nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.taken || len(s.plaintext) == 0 {
		return nil, nil, false
	}
	s.taken = true
	plaintext, ciphertext = s.plaintext, s.ciphertext
	s.plaintext, s.ciphertext = nil, nil
	return plaintext, ciphertext, true
}

func (s *ShortLivedSecret) Clear() {
	if s == nil {
		return
	}
	s.mu.Lock()
	clear(s.plaintext)
	clear(s.ciphertext)
	s.plaintext, s.ciphertext = nil, nil
	s.taken = true
	s.mu.Unlock()
}

func (*ShortLivedSecret) String() string   { return "[redacted upstream credential]" }
func (*ShortLivedSecret) GoString() string { return "[redacted upstream credential]" }
func (*ShortLivedSecret) LogValue() slog.Value {
	return slog.StringValue("[redacted upstream credential]")
}

// StreamEventKind describes connector-neutral stream progress. It carries no
// vendor frame or body bytes and is safe to use as bounded observer metadata.
type StreamEventKind uint8

const (
	StreamEventStarted StreamEventKind = iota + 1
	StreamEventChunk
	StreamEventUsage
	StreamEventTerminal
	StreamEventFailed
)

type StreamEvent struct {
	Kind      StreamEventKind
	Bytes     int64
	Usage     Usage
	Committed bool
}

// DiscoveredModel is the connector-neutral cache input returned by an
// optional ModelDiscoverer. Sorting and persistence remain fetch-owned.
type DiscoveredModel struct {
	ID       string
	Provider string
}

// DiscoveryInput transfers one fetch-owned, already revalidated target and
// short-lived credential to a protocol discoverer. The discoverer performs
// network I/O only through Backend and owns no cache or database writes.
type DiscoveryInput struct {
	Backend    backend.Backend
	Target     Target
	Credential *ShortLivedSecret
}

type DiscoveryResult struct {
	Models     []DiscoveredModel
	Diagnostic string
}

// AnthropicDefaultMaxTokensProvider exposes the nullable raw site override to
// the Anthropic connector without coupling protocol code to site-config
// persistence. A nil value means "not configured" and resolves to the
// built-in 65536 fallback. Implementations must never return request data.
type AnthropicDefaultMaxTokensProvider interface {
	RawAnthropicDefaultMaxTokens(context.Context) (*int64, error)
}
