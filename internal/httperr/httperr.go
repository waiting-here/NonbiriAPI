// Package httperr defines the uniform JSON error envelope returned by the
// platform and its stable code set. Error codes are stable machine identifiers
// (never changed for an existing meaning); HTTP status is derived from the
// code. Message and diag are length-bounded and control-char sanitized before
// emission. Message is bounded to a short human-safe rune limit and stripped
// of all control characters; it never carries raw upstream identifiers or
// text. Diag is bounded to 4096 bytes by the shared internal/diagnostic
// boundary, which also repairs invalid UTF-8, collapses CR/LF/TAB to spaces so
// untrusted text cannot forge log lines, strips other C0 controls and DEL, and
// marks truncation. Raw upstream identifiers and error text live only in diag,
// and only after that shared bounding and sanitization. All error responses
// carry Cache-Control: no-store so a cached error can never shadow a fresh
// result.
package httperr

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/diagnostic"
)

// Stable error codes. Add new codes; never redefine an existing one.
const (
	CodeInternal              = "internal"
	CodeInvalidRequest        = "invalid_request"
	CodeUnauthorized          = "unauthorized"
	CodeForbidden             = "forbidden"
	CodeNotFound              = "not_found"
	CodeConflict              = "conflict"
	CodeMethodNotAllowed      = "method_not_allowed"
	CodeRateLimited           = "rate_limited"
	CodePayloadTooLarge       = "payload_too_large"
	CodeElevationRequired     = "elevated_required"
	CodeUnboundModel          = "unbound_model"
	CodeUpstream              = "upstream"
	CodeServiceUnavailable    = "service_unavailable"
	CodeResourceLimitExceeded = "resource_limit_exceeded"
	// Economy / feature-gate codes are reserved now (403) so later phases can
	// emit a stable machine identifier without renumbering the wire contract.
	// No business trigger is wired in this task.
	CodeInsufficientCredits = "insufficient_credits"
	CodeFeatureDisabled     = "feature_disabled"
	CodeCharitySuspended    = "charity_suspended"
	CodeContentTooShort     = "content_too_short"
	// Check-in codes (additive alpha.2 wire contract): a second check-in on
	// the same site-local day is a conflict, and the check-in credits-cap
	// threshold is a platform admission refusal.
	CodeAlreadyCheckedIn  = "already_checked_in"
	CodeCheckinCapReached = "checkin_cap_reached"
)

// Source is the stable origin attribution every error carries. It tells the
// caller whether a failure is the platform's own decision (admission,
// authorization, maintenance, accounting, feature gate) or a propagated
// upstream failure, so a client never has to infer it from a message prefix.
const (
	SourcePlatform = "platform"
	SourceUpstream = "upstream"
)

const (
	msgBound       = 1000 // message: short, human-safe, rune limit
	requestIDBound = 128  // correlation id: finite and control-safe at the wire sink
	resourceBound  = 64   // resource name: short stable identifier, control-safe at the wire sink
)

// Error is the inner "error" object of the envelope.
type Error struct {
	Code      string `json:"code"`
	Source    string `json:"source"`
	Message   string `json:"message"`
	Diag      string `json:"diag,omitempty"`
	RequestID string `json:"request_id,omitempty"`
	Limit     int    `json:"limit,omitempty"`
	Resource  string `json:"resource,omitempty"`
}

// Envelope is the top-level error response shape.
type Envelope struct {
	Error Error `json:"error"`
}

// New builds an Error with a sanitized, bounded message and no diag.
func New(code, message string) Error {
	return Error{Code: code, Message: sanitizeMessage(message)}
}

// WithDiag adds a bounded, sanitized diagnostic. Diag is omitted from JSON when
// empty; callers should only attach diag where it carries useful, already
// privacy-safe upstream context.
func (e Error) WithDiag(diag string) Error {
	e.Diag = sanitizeDiag(diag)
	return e
}

// WithRequestID attaches a request correlation id.
func (e Error) WithRequestID(id string) Error {
	e.RequestID = sanitizeRequestID(id)
	return e
}

// WithResourceLimit attaches the effective cap and resource name to a
// resource_limit_exceeded error so the caller can report which bounded
// resource was refused and at what limit. resource is re-sanitized at the
// wire sink; it is a short stable identifier (never request or secret
// material).
func (e Error) WithResourceLimit(resource string, limit int) Error {
	e.Resource = resource
	e.Limit = limit
	return e
}

// statusOf maps a stable code to its HTTP status. Unknown codes map to 500 to
// avoid leaking structure about unhandled cases.
func statusOf(code string) int {
	switch code {
	case CodeInvalidRequest, CodeContentTooShort:
		return http.StatusBadRequest
	case CodeUnauthorized:
		return http.StatusUnauthorized
	case CodeForbidden, CodeElevationRequired, CodeInsufficientCredits, CodeFeatureDisabled,
		CodeCharitySuspended, CodeCheckinCapReached:
		return http.StatusForbidden
	case CodeNotFound:
		return http.StatusNotFound
	case CodeConflict, CodeAlreadyCheckedIn:
		return http.StatusConflict
	case CodeMethodNotAllowed:
		return http.StatusMethodNotAllowed
	case CodeRateLimited:
		return http.StatusTooManyRequests
	case CodePayloadTooLarge:
		return http.StatusRequestEntityTooLarge
	case CodeUpstream:
		return http.StatusBadGateway
	case CodeUnboundModel, CodeServiceUnavailable:
		return http.StatusServiceUnavailable
	case CodeResourceLimitExceeded:
		return http.StatusUnprocessableEntity
	default:
		return http.StatusInternalServerError
	}
}

// deriveSource returns the stable source attribution for an error, locked to
// the code. CodeUpstream is the only upstream-origin code, so it is always
// upstream; every other code — including the unknown-code fallback — is always
// platform. An explicit caller-supplied source is honored only when it matches
// that code-derived value, so an upstream code can never be relabeled platform
// and a platform code can never be relabeled upstream. Any other explicit
// value (including an empty string from a hand-constructed Error, an invalid
// string, or wrong casing) is dropped in favor of the code-derived default.
// This runs at the wire sink so a caller that builds an Error directly can
// neither omit source nor forge a different attribution than its code.
func deriveSource(code, source string) string {
	derived := SourcePlatform
	if code == CodeUpstream {
		derived = SourceUpstream
	}
	if source == derived {
		return source
	}
	return derived
}

// WithSource sets an explicit source attribution. It is honored at the wire
// sink only when it matches the value derived from the code (upstream for
// CodeUpstream, platform for every other code), so it can confirm but never
// override the code-derived attribution: an explicit value that disagrees
// with the code is dropped in favor of the derived default. Most callers omit
// it; the sink derives source from the code for every error.
func (e Error) WithSource(source string) Error {
	e.Source = source
	return e
}

// WriteError writes the uniform error envelope with the code's HTTP status,
// a stable source attribution, and Cache-Control: no-store. Source is
// derived/validated here — the single wire sink — so a hand-constructed Error
// can neither omit source nor forge a different attribution than its code.
func WriteError(w http.ResponseWriter, e Error) {
	// Error is exported for inspection and tests, so callers can construct one
	// without New/WithDiag. Re-apply every wire-boundary sanitizer here; a
	// final response sink must not depend on every caller remembering a helper.
	e.Message = sanitizeMessage(e.Message)
	e.Diag = sanitizeDiag(e.Diag)
	e.RequestID = sanitizeRequestID(e.RequestID)
	e.Resource = sanitizeResource(e.Resource)

	status := statusOf(e.Code)
	if e.Code == "" {
		e.Code = CodeInternal
		status = http.StatusInternalServerError
	}
	e.Source = deriveSource(e.Code, e.Source)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(Envelope{Error: e})
}

// WriteJSON writes a JSON success body with Cache-Control: no-store, the
// appropriate default for user-scoped / dynamic API responses. Use
// WriteJSONCacheable for explicitly cacheable content.
func WriteJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// sseErrorPayload is the compact {error:{code,source,message}} shape shared by
// the JSON envelope and an SSE error frame emitted after the commit boundary.
// It deliberately omits diag/request_id/limit/resource: an in-stream frame has
// no HTTP headers to carry them and the contract pins it to those three fields.
type sseErrorPayload struct {
	Error sseErrorBody `json:"error"`
}

type sseErrorBody struct {
	Code    string `json:"code"`
	Source  string `json:"source"`
	Message string `json:"message"`
}

// SSEErrorFrame returns a complete "data: <json>\n\n" SSE frame carrying the
// same stable {error:{code,source,message}} shape as the JSON envelope, for an
// error emitted after the response commit boundary where a fresh HTTP envelope
// can no longer be written. Source is derived/validated exactly as in
// WriteError, so an already-started stream cannot forge a different source
// attribution or rely on a message prefix. The message is sanitized at this
// sink; code is coerced to internal when empty. The returned frame is safe to
// write verbatim onto a text/event-stream response.
func SSEErrorFrame(e Error) []byte {
	if e.Code == "" {
		e.Code = CodeInternal
	}
	payload := sseErrorPayload{Error: sseErrorBody{
		Code:    e.Code,
		Source:  deriveSource(e.Code, e.Source),
		Message: sanitizeMessage(e.Message),
	}}
	encoded, _ := json.Marshal(payload)
	frame := make([]byte, 0, len(encoded)+8)
	frame = append(frame, "data: "...)
	frame = append(frame, encoded...)
	frame = append(frame, '\n', '\n')
	return frame
}

// sanitizeMessage bounds to msgBound runes and strips all control characters.
func sanitizeMessage(s string) string {
	return controlStrip(boundByRunes(s, msgBound))
}

// sanitizeDiag bounds diag to the shared diagnostic byte limit and applies its
// UTF-8 repair and control-character normalization. Empty input returns the
// empty string, which the omitempty JSON tag drops from the envelope.
func sanitizeDiag(s string) string {
	return diagnostic.Bound(s)
}

func sanitizeRequestID(s string) string {
	return controlStrip(boundByRunes(s, requestIDBound))
}

// sanitizeResource bounds a resource name to resourceBound runes and strips
// all control characters. It is defense-in-depth at the wire sink: callers
// supply short stable identifiers.
func sanitizeResource(s string) string {
	return controlStrip(boundByRunes(s, resourceBound))
}

// boundByRunes returns s truncated to at most n runes, UTF-8 safe.
func boundByRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// controlStrip removes C0 control characters (0x00-0x1F) and DEL (0x7F).
func controlStrip(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r < 0x20 || r == 0x7F {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
