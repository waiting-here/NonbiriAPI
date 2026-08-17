// Package httperr defines the uniform JSON error envelope returned by the
// platform and its stable code set. Error codes are stable machine identifiers
// (never changed for an existing meaning); HTTP status is derived from the
// code. Message and diag are length-bounded and control-char sanitized before
// emission: message to a short human-safe bound, diag to a longer debug bound
// (4096, the untrusted-upstream diagnostic limit). Raw upstream identifiers
// and error text live only in diag, never in message, and are themselves
// bounded/sanitized by this layer. All error responses carry
// Cache-Control: no-store so a cached error can never shadow a fresh result.
package httperr

import (
	"encoding/json"
	"net/http"
	"strings"
)

// Stable error codes. Add new codes; never redefine an existing one.
const (
	CodeInternal          = "internal"
	CodeInvalidRequest    = "invalid_request"
	CodeUnauthorized      = "unauthorized"
	CodeForbidden         = "forbidden"
	CodeNotFound          = "not_found"
	CodeConflict          = "conflict"
	CodeMethodNotAllowed  = "method_not_allowed"
	CodeRateLimited       = "rate_limited"
	CodePayloadTooLarge   = "payload_too_large"
	CodeUnboundModel      = "unbound_model"
	CodeUpstream          = "upstream"
	CodeServiceUnavailable = "service_unavailable"
)

const (
	msgBound  = 1000 // message: short, human-safe
	diagBound = 4096 // diag: bounded untrusted-upstream diagnostic (DEC-005s)
)

// Error is the inner "error" object of the envelope.
type Error struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Diag      string `json:"diag,omitempty"`
	RequestID string `json:"request_id,omitempty"`
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
	e.RequestID = id
	return e
}

// statusOf maps a stable code to its HTTP status. Unknown codes map to 500 to
// avoid leaking structure about unhandled cases.
func statusOf(code string) int {
	switch code {
	case CodeInvalidRequest:
		return http.StatusBadRequest
	case CodeUnauthorized:
		return http.StatusUnauthorized
	case CodeForbidden:
		return http.StatusForbidden
	case CodeNotFound:
		return http.StatusNotFound
	case CodeConflict:
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
	default:
		return http.StatusInternalServerError
	}
}

// WriteError writes the uniform error envelope with the code's HTTP status and
// Cache-Control: no-store.
func WriteError(w http.ResponseWriter, e Error) {
	status := statusOf(e.Code)
	if e.Code == "" {
		e.Code = CodeInternal
		status = http.StatusInternalServerError
	}
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

// sanitizeMessage bounds to msgBound runes and strips all control characters.
func sanitizeMessage(s string) string {
	return controlStrip(boundByRunes(s, msgBound))
}

// sanitizeDiag bounds to diagBound runes, keeps TAB and LF for readability,
// converts CR to space, and strips all other control characters.
func sanitizeDiag(s string) string {
	return controlNormalizeDiag(boundByRunes(s, diagBound))
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

// controlNormalizeDiag converts CR to a space, keeps TAB and LF, and strips the
// remaining control characters (C0 and DEL).
func controlNormalizeDiag(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\t', r == '\n':
			b.WriteRune(r)
		case r == '\r':
			b.WriteRune(' ')
		case r < 0x20 || r == 0x7F:
			continue
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}