// Package diagnostic implements the shared boundary for untrusted operational
// diagnostics: any text derived from upstream errors, model identifiers, or
// network failures that may be surfaced in an error envelope, persisted, or
// written to the process log. The same byte limit, UTF-8 repair, and control
// normalization apply everywhere, so no caller can accidentally rely on a
// second, weaker sink-specific policy.
//
// Contract scope. This package owns only the resource boundary that every
// diagnostic sink shares: a byte limit, valid UTF-8, and control-character
// normalization that prevents line forgery. It is deliberately NOT a
// CSV-formula-injection, HTML, or HTTP-header escaper. Those concerns belong
// to their own sinks (a CSV writer escapes leading =/+/-/@, a frontend renders
// as text, a header writer rejects CRLF); the boundary here only guarantees
// that no CR/LF/TAB survives to let untrusted text fabricate a log line, and
// that the result is valid UTF-8 within the byte budget.
package diagnostic

import "unicode/utf8"

// MaxBytes is the maximum size, in bytes, of one bounded diagnostic. It is a
// resource bound, not a rune count: a 4096-rune multibyte payload would far
// exceed it. Callers must never substitute a rune count for this limit.
const MaxBytes = 4096

// TruncationMarker makes a bounded value distinguishable from the complete
// diagnostic. It is itself valid UTF-8 and contains no line or control
// separator, so appending it can never reintroduce a forgery vector.
const TruncationMarker = "…[truncated]"

// Bound returns a valid UTF-8, single-line diagnostic no longer than MaxBytes.
func Bound(value string) string {
	return BoundTo(value, MaxBytes)
}

// BoundTo applies the same normalization as Bound with a caller-supplied
// smaller positive limit. A non-positive limit, or a limit larger than
// MaxBytes, is clamped to MaxBytes: callers may tighten the boundary but never
// widen it past the shared default. Input is decoded in place and the output
// buffer is capped at the limit, so an oversized upstream body is never copied
// whole before the boundary is applied.
//
// Normalization rules:
//   - Invalid UTF-8 is repaired: a run of invalid bytes collapses into one
//     U+FFFD replacement rune (matching strings.ToValidUTF8).
//   - CR, LF, and TAB become spaces so untrusted text cannot forge log lines.
//   - Other C0 control characters (0x00-0x1F) and DEL (0x7F) are stripped; they
//     carry no diagnostic value and are injection hazards in persisted text.
//   - Truncation ends on a rune boundary and appends TruncationMarker; the
//     final result never exceeds maxBytes. When maxBytes is too small to hold
//     the marker together with any content, a rune-boundary prefix of the
//     marker (possibly empty) is emitted instead.
func BoundTo(value string, maxBytes int) string {
	if maxBytes <= 0 || maxBytes > MaxBytes {
		maxBytes = MaxBytes
	}
	if value == "" {
		return value
	}
	// Fast path: already valid UTF-8, free of every control character this
	// package would transform or strip, and within the byte budget. No work
	// is needed and no allocation beyond the returned string header occurs.
	if len(value) <= maxBytes && utf8.ValidString(value) && !containsControl(value) {
		return value
	}

	output := make([]byte, 0, min(len(value), maxBytes))
	for len(value) > 0 {
		r, size := utf8.DecodeRuneInString(value)
		if r == utf8.RuneError && size == 1 {
			// Collapse a whole run of invalid bytes into one replacement rune,
			// the same behavior as strings.ToValidUTF8.
			for size < len(value) {
				next, nextSize := utf8.DecodeRuneInString(value[size:])
				if next != utf8.RuneError || nextSize != 1 {
					break
				}
				size++
			}
			r = utf8.RuneError
		}
		switch {
		case r == '\r', r == '\n', r == '\t':
			r = ' '
		case r < 0x20 || r == 0x7f:
			value = value[size:]
			continue
		}
		runeBytes := utf8.RuneLen(r)
		if len(output)+runeBytes > maxBytes {
			return appendMarker(output, maxBytes)
		}
		output = utf8.AppendRune(output, r)
		value = value[size:]
	}
	return string(output)
}

// appendMarker truncates output to make room for TruncationMarker within
// maxBytes, ending on a rune boundary. When maxBytes cannot hold the marker
// alongside any content, a rune-boundary prefix of the marker is emitted
// instead (possibly empty for limits below the first marker rune).
func appendMarker(output []byte, maxBytes int) string {
	if len(TruncationMarker) >= maxBytes {
		return markerPrefix(maxBytes)
	}
	end := maxBytes - len(TruncationMarker)
	if end > len(output) {
		end = len(output)
	}
	for end > 0 && end < len(output) && !utf8.RuneStart(output[end]) {
		end--
	}
	return string(append(output[:end], TruncationMarker...))
}

// markerPrefix returns a rune-boundary-safe prefix of TruncationMarker capped
// at maxBytes, used when the limit is too small to hold the full marker.
func markerPrefix(maxBytes int) string {
	end := maxBytes
	if end > len(TruncationMarker) {
		end = len(TruncationMarker)
	}
	for end > 0 && end < len(TruncationMarker) && !utf8.RuneStart(TruncationMarker[end]) {
		end--
	}
	return TruncationMarker[:end]
}

// containsControl reports whether s contains any C0 control character
// (0x00-0x1F) or DEL (0x7F). It is only meaningful for already-valid UTF-8:
// in valid UTF-8 the only bytes below 0x20 or equal to 0x7F are real ASCII
// controls, since continuation bytes are 0x80-0xBF and lead bytes are >= 0xC2.
func containsControl(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] == 0x7f {
			return true
		}
	}
	return false
}
