// Package httperr defines the uniform JSON error envelope returned by the
// platform and its stable code set. Error codes are stable machine identifiers
// (never changed for an existing meaning); HTTP status is derived from the
// code. The wire envelope contains only code, source, message, and optional
// upstream_code/diag fields. Message and diag are length-bounded and
// control-char sanitized before emission. Message is bounded to a short
// human-safe UTF-8 byte limit and stripped of all control characters; it never
// carries raw upstream identifiers or text. Diag is bounded to 4096 bytes by
// the shared internal/diagnostic boundary, which also repairs invalid UTF-8,
// collapses CR/LF/TAB to spaces so untrusted text cannot forge log lines,
// strips other C0 controls and DEL, and marks truncation. Raw upstream
// identifiers and error text live only in diag, and only after that shared
// bounding and sanitization. All error responses carry Cache-Control:
// no-store so a cached error can never shadow a fresh result.
package httperr

import (
	"encoding/json"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/diagnostic"
)

// Stable error codes. Add new codes; never redefine an existing one.
const (
	CodeInternal                = "internal"
	CodeInvalidRequest          = "invalid_request"
	CodeUnauthorized            = "unauthorized"
	CodeForbidden               = "forbidden"
	CodeNotFound                = "not_found"
	CodeConflict                = "conflict"
	CodeMethodNotAllowed        = "method_not_allowed"
	CodeRateLimited             = "rate_limited"
	CodePayloadTooLarge         = "payload_too_large"
	CodeElevationRequired       = "elevated_required"
	CodeUnboundModel            = "unbound_model"
	CodeUpstream                = "upstream"
	CodeMaintenance             = "maintenance"
	CodeServiceUnavailable      = "service_unavailable"
	CodeResourceLimitExceeded   = "resource_limit_exceeded"
	CodeResourceLocked          = "resource_locked"
	CodeDebugDryRunIntercepted  = "debug_dry_run_intercepted"
	CodeDebugLiveResultCaptured = "debug_live_result_captured"
	CodeDebugLiveCancelled      = "debug_live_cancelled"
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
	msgBound           = 1024 // message: bounded UTF-8 bytes, including platform prefix
	upstreamCodeBound  = 64   // connector code: bounded printable ASCII wire identifier
	platformPrefix     = "[NonbiriAPI] "
	sseErrorFrameBound = 4 * 1024
)

// Error is the inner "error" object of the envelope.
type Error struct {
	Code         string `json:"code"`
	Source       string `json:"source"`
	Message      string `json:"message"`
	UpstreamCode string `json:"upstream_code,omitempty"`
	Diag         string `json:"diag,omitempty"`
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

// WithUpstreamCode attaches a bounded connector-owned machine identifier. It
// is kept separate from Code: the latter is the platform's closed stable set,
// while this field may preserve a safe upstream classification for an
// explicitly whitelisted route.
func (e Error) WithUpstreamCode(code string) Error {
	e.UpstreamCode = sanitizeUpstreamCode(code)
	return e
}

// IsStableCode reports whether code belongs to the frozen Generation 2 error
// closed set. Wire sinks use the same predicate to fail closed to internal.
func IsStableCode(code string) bool {
	switch code {
	case CodeInternal, CodeInvalidRequest, CodeUnauthorized, CodeForbidden,
		CodeNotFound, CodeConflict, CodeMethodNotAllowed, CodeRateLimited,
		CodePayloadTooLarge, CodeElevationRequired, CodeUnboundModel,
		CodeUpstream, CodeMaintenance, CodeServiceUnavailable,
		CodeResourceLimitExceeded, CodeResourceLocked, CodeInsufficientCredits,
		CodeFeatureDisabled, CodeCharitySuspended, CodeContentTooShort,
		CodeAlreadyCheckedIn, CodeCheckinCapReached,
		CodeDebugDryRunIntercepted, CodeDebugLiveResultCaptured,
		CodeDebugLiveCancelled:
		return true
	default:
		return false
	}
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
	case CodeUnboundModel, CodeMaintenance, CodeServiceUnavailable:
		return http.StatusServiceUnavailable
	case CodeResourceLimitExceeded, CodeDebugDryRunIntercepted, CodeDebugLiveResultCaptured:
		return http.StatusUnprocessableEntity
	case CodeResourceLocked:
		return http.StatusLocked
	case CodeDebugLiveCancelled:
		return http.StatusConflict
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

func canonicalCode(code string) string {
	if IsStableCode(code) {
		return code
	}
	return CodeInternal
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
	writeError(w, e, nil, false)
}

// WriteUpstreamError writes a CodeUpstream envelope while allowing the
// caller-facing status to preserve a self-route upstream 4xx or timeout. Only
// statuses 400..499, 502, and 504 are valid overrides, and only an exact
// CodeUpstream error may use them. Any other combination falls back to the
// ordinary code-derived status, so an invalid caller-supplied status can never
// reach the wire.
func WriteUpstreamError(w http.ResponseWriter, e Error, status int) {
	if e.Code != CodeUpstream || !validUpstreamStatusOverride(status) {
		writeError(w, e, nil, false)
		return
	}
	writeError(w, e, &status, true)
}

func validUpstreamStatusOverride(status int) bool {
	return status >= http.StatusBadRequest && status <= 499 ||
		status == http.StatusBadGateway || status == http.StatusGatewayTimeout
}

func writeError(w http.ResponseWriter, e Error, statusOverride *int, allowUpstreamContext bool) {
	e = sanitizeError(e, allowUpstreamContext)
	status := statusOf(e.Code)
	if e.Code == CodeUpstream && statusOverride != nil && validUpstreamStatusOverride(*statusOverride) {
		status = *statusOverride
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(Envelope{Error: e})
}

func sanitizeError(e Error, allowUpstreamContext bool) Error {
	// Error is exported for inspection and tests, so callers can construct one
	// without New/WithDiag. Re-apply every wire-boundary sanitizer here; a
	// final response sink must not depend on every caller remembering a helper.
	e.Code = canonicalCode(e.Code)
	e.Source = deriveSource(e.Code, e.Source)
	e.Message = sanitizeWireMessage(e.Source, e.Message)
	e.UpstreamCode = sanitizeUpstreamCode(e.UpstreamCode)
	e.Diag = sanitizeDiag(e.Diag)
	// Connector-owned context is opt-in at the sink. Ordinary WriteError and
	// the default SSE path are fail-closed, even for CodeUpstream; only an
	// explicit self/owner upstream API may opt in. Platform, public, and debug
	// errors can never expose upstream_code/diag.
	if !allowUpstreamContext || e.Code != CodeUpstream || e.Source != SourceUpstream {
		e.UpstreamCode = ""
		e.Diag = ""
	}
	return e
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
// It deliberately omits diag and the other non-contract fields: an in-stream
// frame has no HTTP headers to carry them and the contract pins it to this safe
// projection (plus an optional upstream_code).
type sseErrorPayload struct {
	Error sseErrorBody `json:"error"`
}

type sseErrorBody struct {
	Code         string `json:"code"`
	Source       string `json:"source"`
	Message      string `json:"message"`
	UpstreamCode string `json:"upstream_code,omitempty"`
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
	return sseErrorFrame(e, false)
}

// SSEUpstreamErrorFrame returns an in-stream upstream error frame for an
// explicitly self/owner-owned upstream route. The default SSEErrorFrame is
// fail-closed and strips upstream_code even for CodeUpstream.
func SSEUpstreamErrorFrame(e Error) []byte {
	return sseErrorFrame(e, true)
}

func sseErrorFrame(e Error, allowUpstreamContext bool) []byte {
	e = sanitizeError(e, allowUpstreamContext)
	build := func(message string) []byte {
		payload := sseErrorPayload{Error: sseErrorBody{
			Code:         e.Code,
			Source:       e.Source,
			Message:      message,
			UpstreamCode: e.UpstreamCode,
		}}
		encoded, _ := json.Marshal(payload)
		frame := make([]byte, 0, len(encoded)+8)
		frame = append(frame, "data: "...)
		frame = append(frame, encoded...)
		frame = append(frame, '\n', '\n')
		return frame
	}
	frame := build(e.Message)
	if len(frame) <= sseErrorFrameBound {
		return frame
	}

	// JSON HTML escaping can expand a legal 1,024-byte message beyond the
	// separate 4 KiB SSE frame budget. Retain the largest UTF-8-safe prefix that
	// fits, preserving the platform prefix when present.
	prefix := ""
	content := e.Message
	if e.Source == SourcePlatform && strings.HasPrefix(content, platformPrefix) {
		prefix = platformPrefix
		content = strings.TrimPrefix(content, platformPrefix)
	}
	best := build(prefix)
	low, high := 0, len(content)
	for low <= high {
		middle := low + (high-low)/2
		candidate := build(prefix + truncateUTF8Bytes(content, middle))
		if len(candidate) <= sseErrorFrameBound {
			best = candidate
			low = middle + 1
		} else {
			high = middle - 1
		}
	}
	return best
}

// sanitizeMessage bounds to msgBound UTF-8 bytes and strips all control
// characters. The platform prefix is applied separately at the wire sink so
// its bytes are included in the same bound.
func sanitizeMessage(s string) string {
	return truncateUTF8Bytes(controlStrip(s), msgBound)
}

func sanitizeWireMessage(source, message string) string {
	// Prefix handling is deliberately centralized at both wire sinks. Remove
	// every exact marker before applying the source-specific prefix, so an
	// internal marker cannot smuggle platform attribution through either sink.
	message = strings.ReplaceAll(controlStrip(message), platformPrefix, "")
	if source == SourcePlatform {
		contentBound := msgBound - len(platformPrefix)
		if contentBound < 0 {
			contentBound = 0
		}
		message = platformPrefix + truncateUTF8Bytes(message, contentBound)
		return message
	}
	return truncateUTF8Bytes(message, msgBound)
}

// sanitizeDiag bounds diag to the shared diagnostic byte limit and applies its
// UTF-8 repair and control-character normalization. Empty input returns the
// empty string, which the omitempty JSON tag drops from the envelope.
func sanitizeDiag(s string) string {
	return diagnostic.Bound(stripC1Controls(s))
}

func sanitizeUpstreamCode(s string) string {
	if s == "" {
		return ""
	}
	if len(s) > upstreamCodeBound {
		return ""
	}
	for i := 0; i < len(s); i++ {
		if s[i] < 0x21 || s[i] > 0x7e {
			return ""
		}
	}
	return s
}

// truncateUTF8Bytes returns at most n bytes while preserving valid UTF-8. Its
// callers have already repaired invalid input, but keeping this helper safe on
// its own prevents a byte-boundary change from ever emitting a partial rune.
func truncateUTF8Bytes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if len(s) <= n {
		return s
	}
	end := n
	for end > 0 && !utf8.ValidString(s[:end]) {
		end--
	}
	return s[:end]
}

// controlStrip removes C0/C1 control characters (0x00-0x1F, 0x80-0x9F) and
// DEL (0x7F), keeping wire text from carrying terminal or line-control data.
func controlStrip(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r < 0x20 || (r >= 0x80 && r <= 0x9F) || r == 0x7F {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// stripC1Controls removes the Unicode C1 range while preserving the
// diagnostic package's deliberate CR/LF/TAB-to-space normalization for C0
// line separators.
func stripC1Controls(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r >= 0x80 && r <= 0x9F {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
