// Package debug implements the user-scoped, process-memory Debug Hub.
//
// The package deliberately has no database, file, logger, or upstream
// dependency.  Control requests are authenticated by the caller's existing
// user-session middleware; model requests are attached by the caller-key
// middleware through WrapCaller.  A Hub is safe for concurrent use and can be
// discarded on process shutdown without leaving durable debug state behind.
package debug

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
)

const (
	// Frozen control/event and memory ceilings.
	MaxControlBodyBytes      = 1 << 10
	MaxEventBytes            = 512 << 10
	MaxSessions              = 64
	MaxHubBytes              = 128 << 20
	MaxSessionBytes          = 4 << 20
	MaxTraces                = 32
	MaxRetainedEvents        = 128
	MaxSubscriberQueue       = 64
	MaxSubscribers           = 2
	MaxRawRequestBytes       = 64 << 10
	MaxMessagesToolsBytes    = 128 << 10
	MaxParametersBytes       = 64 << 10
	MaxEffectiveSummaryBytes = 64 << 10
	MaxResponseBytes         = 256 << 10
	// A request-copy lease covers the complete bounded ingress read plus the
	// replay, DecodeChatRequest/ordered-redaction ASTs, response tee, and
	// observer-side temporary copies that coexist during one live call. The
	// one-MiB body and 256-KiB response ceilings are therefore not treated as
	// the whole temporary-memory cost; this conservative 8-MiB lease is kept
	// outside retained trace bytes and still participates in the global cap.
	MaxRequestCopyBytes = 8 << 20
	MaxTraceBytes       = 768 << 10
	// Base64url adds 4/3 overhead; this raw chunk size leaves enough room for
	// the fixed envelope and fragment metadata while keeping every assembled
	// SSE event below MaxEventBytes.
	MaxFragmentBytes          = 320 << 10
	FirstAttachTimeout        = 30 * time.Second
	ReconnectGrace            = 30 * time.Second
	IdleTimeout               = 10 * time.Minute
	AbsoluteLifetime          = time.Hour
	HeartbeatInterval         = 15 * time.Second
	WriteDeadline             = 15 * time.Second
	ConfirmationLifetime      = time.Minute
	defaultTraceRevision      = 1
	maxTraceIdentifierBytes   = 128
	maxRequestIdentifierBytes = 128
)

var (
	ErrNotFound     = errors.New("debug: session not found")
	ErrCapacity     = errors.New("debug: capacity exhausted")
	ErrInvalid      = errors.New("debug: invalid request")
	ErrConflict     = errors.New("debug: conflict")
	ErrUnauthorized = errors.New("debug: unauthorized")
	ErrConfirmation = errors.New("debug: confirmation invalid")
	ErrClosed       = errors.New("debug: hub closed")
	ErrTooLarge     = errors.New("debug: resource limit exceeded")
	ErrNoStream     = errors.New("debug: streaming unsupported")
)

// Mode is the server-authoritative mode of an active session.  New sessions
// and all uncertain/reconnected sessions are ModeDry.
type Mode string

const (
	ModeDry  Mode = "dry"
	ModeLive Mode = "live"
)

// EventType is intentionally closed-world.  Unknown values are never emitted
// by Hub and are rejected by the SSE consumer contract.
type EventType string

const (
	EventSessionSnapshot EventType = "session_snapshot"
	EventTraceUpsert     EventType = "trace_upsert"
	EventGap             EventType = "gap"
	EventSessionEnd      EventType = "session_end"
)

// SessionEndReason is the safe, non-sensitive lifecycle reason vocabulary.
type SessionEndReason string

const (
	EndStopped        SessionEndReason = "stopped"
	EndReplaced       SessionEndReason = "replaced"
	EndIdleTimeout    SessionEndReason = "idle_timeout"
	EndMaxAge         SessionEndReason = "max_age"
	EndSessionInvalid SessionEndReason = "session_invalid"
	EndLogout         SessionEndReason = "logout"
	EndBanned         SessionEndReason = "banned"
	EndDeleted        SessionEndReason = "deleted"
	EndShutdown       SessionEndReason = "shutdown"
	EndCapacity       SessionEndReason = "capacity"
)

// Limits is the public, non-sensitive resource budget advertised in session
// metadata.  Durations are represented as seconds on the JSON wire.
type Limits struct {
	MaxSessions           int   `json:"max_sessions"`
	HubBytes              int64 `json:"hub_bytes"`
	SessionBytes          int64 `json:"session_bytes"`
	MaxTraces             int   `json:"max_traces"`
	MaxEvents             int   `json:"max_events"`
	EventBytes            int64 `json:"event_bytes"`
	SubscriberQueue       int   `json:"subscriber_queue"`
	MaxSubscribers        int   `json:"max_subscribers"`
	RawRequestBytes       int64 `json:"raw_request_bytes"`
	MessagesToolsBytes    int64 `json:"messages_tools_bytes"`
	ParametersBytes       int64 `json:"parameters_bytes"`
	EffectiveSummaryBytes int64 `json:"effective_summary_bytes"`
	ResponseBytes         int64 `json:"response_bytes"`
	TraceBytes            int64 `json:"trace_bytes"`
	FirstAttachSeconds    int64 `json:"first_attach_seconds"`
	ReconnectSeconds      int64 `json:"reconnect_seconds"`
	IdleSeconds           int64 `json:"idle_seconds"`
	AbsoluteSeconds       int64 `json:"absolute_seconds"`
	HeartbeatSeconds      int64 `json:"heartbeat_seconds"`
	WriteDeadlineSeconds  int64 `json:"write_deadline_seconds"`
	ConfirmationSeconds   int64 `json:"confirmation_seconds"`
}

func defaultLimits(maxSessions int, maxHubBytes, maxSessionBytes int64) Limits {
	return Limits{
		MaxSessions:           maxSessions,
		HubBytes:              maxHubBytes,
		SessionBytes:          maxSessionBytes,
		MaxTraces:             MaxTraces,
		MaxEvents:             MaxRetainedEvents,
		EventBytes:            MaxEventBytes,
		SubscriberQueue:       MaxSubscriberQueue,
		MaxSubscribers:        MaxSubscribers,
		RawRequestBytes:       MaxRawRequestBytes,
		MessagesToolsBytes:    MaxMessagesToolsBytes,
		ParametersBytes:       MaxParametersBytes,
		EffectiveSummaryBytes: MaxEffectiveSummaryBytes,
		ResponseBytes:         MaxResponseBytes,
		TraceBytes:            MaxTraceBytes,
		FirstAttachSeconds:    int64(FirstAttachTimeout / time.Second),
		ReconnectSeconds:      int64(ReconnectGrace / time.Second),
		IdleSeconds:           int64(IdleTimeout / time.Second),
		AbsoluteSeconds:       int64(AbsoluteLifetime / time.Second),
		HeartbeatSeconds:      int64(HeartbeatInterval / time.Second),
		WriteDeadlineSeconds:  int64(WriteDeadline / time.Second),
		ConfirmationSeconds:   int64(ConfirmationLifetime / time.Second),
	}
}

// SessionMetadata is the fixed safe control-plane projection.  It contains no
// login binding, confirmation, request body, trace body, or credential.
type SessionMetadata struct {
	ID            string `json:"id"`
	Generation    uint64 `json:"generation"`
	Mode          Mode   `json:"mode"`
	CreatedAt     int64  `json:"created_at"`
	ExpiresAt     int64  `json:"expires_at"`
	IdleExpiresAt int64  `json:"idle_expires_at"`
	Connected     bool   `json:"connected"`
	LastEventID   uint64 `json:"last_event_id"`
	Limits        Limits `json:"limits"`
}

// EventEnvelope is the exact SSE data envelope.  Payload is always a JSON
// object created by Hub; callers cannot use it to inject a second envelope.
type EventEnvelope struct {
	Version   int            `json:"version"`
	Seq       uint64         `json:"seq"`
	Type      EventType      `json:"type"`
	SessionID string         `json:"session_id"`
	TraceID   string         `json:"trace_id"`
	Revision  uint64         `json:"revision"`
	At        int64          `json:"at"`
	Payload   map[string]any `json:"payload"`
	// retained is an internal reference to the already-accounted event wire.
	// It lets the HTTP consumer release a queued delivery after writing while
	// keeping EventEnvelope's public JSON shape unchanged. Callers that consume
	// Subscription.Events directly should call Release after processing.
	retained *eventDelivery
}

// Release drops this envelope's subscriber-queue reference. It is safe to
// call more than once and is a no-op for retained ring snapshots. The method
// is intentionally separate from JSON serialization; no lifecycle metadata
// crosses the fixed SSE wire.
func (e EventEnvelope) Release() {
	if e.retained != nil {
		e.retained.release()
	}
}

// Trace is a caller-provided safe projection.  Hub redacts and bounds the
// JSON representation once more before retaining it, so callers may pass
// structured maps or json.RawMessage without creating a sensitive sink.
type Trace struct {
	ID       string
	Revision uint64
	Payload  any
	// Merge applies a top-level safe projection update to the existing trace
	// instead of replacing it. Connector observer updates use this so an
	// asynchronous attempt record cannot erase the caller request/response.
	Merge bool
	// Terminal marks a complete logical request projection. The Hub may
	// evict only the oldest terminal trace when the per-session trace cap is
	// reached; active traces are retained and a new copy is dropped instead.
	Terminal bool
}

// TraceInput is the convenient projection used by WrapCaller.  RawRequest,
// Messages, Tools, Parameters, Effective, and Response are all redacted and
// bounded before they enter the Hub.
type TraceInput struct {
	ID        string
	RequestID string
	// ReceivedAt is captured once when the logical request is admitted.  It
	// remains stable across wrapper/observer revisions; event.at is the
	// publication time and therefore cannot serve as the trace receive time.
	ReceivedAt      int64
	Mode            Mode
	Model           string
	Personal        bool
	Charity         bool
	RawRequest      []byte
	Messages        []byte
	Tools           []byte
	Parameters      []byte
	Effective       []byte
	Status          int
	ContentType     string
	DebugModeHeader string
	Response        []byte
	// ResponseOriginalBytes is the number of caller-visible response bytes
	// written by the underlying handler.  Response may contain only the
	// bounded capture; the distinction is required for a truthful truncated
	// response section in the trace wire.
	ResponseOriginalBytes int
	ResponseCapturedBytes int
	ResponseTruncated     bool
	Usage                 any
	Terminal              string
	ErrorCategory         string
	Truncated             bool
	Incomplete            bool
	Dropped               uint64
}

// DryRunResult is the safe logical validation result supplied by the existing
// forwarding/charity pipeline.  It contains no physical candidate or secret.
type DryRunResult struct {
	Model          string
	Personal       bool
	Charity        bool
	FlattenApplied bool
	Effective      map[string]any
}

// DryRunValidator is called only after CallerKey authentication, flow-control
// admission, and bounded OpenAI parsing.  It must inspect logical routing only
// and must not choose candidates, decrypt keys, resolve DNS, reserve charity
// credits, or perform network I/O.
type DryRunValidator func(context.Context, int64, *openai.ChatRequest) (DryRunResult, error)

// CombineDryRunValidators selects the namespace-specific logical seam before
// any model lookup. In particular, a [公益] request never falls through to a
// caller-owned personal model repository, and a personal request never enters
// the charity model projection. Both validators are expected to be logical
// only; this helper performs no routing or accounting work itself.
func CombineDryRunValidators(personal, charity DryRunValidator) DryRunValidator {
	return func(ctx context.Context, userID int64, request *openai.ChatRequest) (DryRunResult, error) {
		if ctx == nil || userID <= 0 || request == nil {
			return DryRunResult{}, ErrInvalid
		}
		validator := personal
		if strings.HasPrefix(request.Model, "[公益]") {
			validator = charity
		}
		if validator == nil {
			return DryRunResult{}, ErrInvalid
		}
		return validator(ctx, userID, request)
	}
}

// SessionBindingValidator is the read-only authority for the browser login
// session that created a Debug session.  ok=false means the binding was
// revoked; a non-nil error is uncertainty and therefore only permits a dry
// fallback while the in-memory Debug session remains identifiable.
type SessionBindingValidator func(context.Context, int64, string) (ok bool, err error)

// ErrorMapper maps a validator failure to a stable platform response.  The
// default mapper intentionally exposes no underlying error text.
type ErrorMapper func(error) (code, message string)

// Config controls ephemeral Hub behavior.  The production defaults are frozen
// constants above; durations/clock are injectable only for deterministic tests.
type Config struct {
	Now             func() time.Time
	MaxSessions     int
	MaxHubBytes     int64
	MaxSessionBytes int64
	DryRunValidator DryRunValidator
	// CharityDryRunValidator is the logical-only counterpart for the
	// [公益] namespace. It must inspect only the charity model definition
	// and model policy; candidate, donation-key, reservation, secret, DNS,
	// and egress rails are deliberately outside this seam.
	CharityDryRunValidator  DryRunValidator
	MapDryRunError          ErrorMapper
	SessionBindingValidator SessionBindingValidator
}

// HandlerConfig is a control/data HTTP integration surface.  Identity is
// optional: when nil, Handler uses auth.UserFromContext and the one-way hash of
// the current user-session cookie.  A custom function is useful for tests and
// alternate session middleware; it must return a stable, non-secret binding.
type HandlerConfig struct {
	Identity func(*http.Request) (userID int64, sessionBinding string, err error)
}
