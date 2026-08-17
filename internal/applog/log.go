// Package applog provides a structured logger (log/slog, JSON to stdout) with
// field-name-based redaction of sensitive values. Redaction is by attribute
// key name: any attribute whose key matches a sensitive pattern is replaced
// with a fixed marker before the record is written. Built-in keys (time,
// level, msg, source) and non-matching keys pass through unchanged.
//
// Redaction is field-name based by design (per the privacy contract). It does
// not attempt to scrub secrets embedded inside arbitrary message bodies or
// free-text attribute values; callers must keep human-authored messages free
// of secrets. The runtime master key, upstream secrets, caller keys, tokens,
// and OAuth credentials are the canonical sensitive fields this guards.
package applog

import (
	"io"
	"log/slog"
	"strings"
)

// Redacted is the fixed replacement written in place of a sensitive value.
const Redacted = "[redacted]"

// sensitiveSubstrings are matched case-insensitively against attribute keys.
var sensitiveSubstrings = []string{
	"password",
	"secret",
	"token",
	"cookie",
	"authorization",
	"bearer",
	"master_key",
	"encrypted_secret",
	"api_key",
	"apikey",
	"caller_key",
	"client_secret",
	"access_token",
	"refresh_token",
}

// redactReplace is the slog ReplaceAttr hook that redacts sensitive values.
func redactReplace(groups []string, a slog.Attr) slog.Attr {
	// Built-in record keys pass through untouched.
	switch a.Key {
	case slog.TimeKey, slog.LevelKey, slog.MessageKey, slog.SourceKey:
		return a
	}
	if isSensitiveKey(a.Key) {
		return slog.String(a.Key, Redacted)
	}
	return a
}

// isSensitiveKey reports whether an attribute key names a sensitive field.
// Matching is case-insensitive on the lowercased key.
func isSensitiveKey(key string) bool {
	k := strings.ToLower(key)
	for _, sub := range sensitiveSubstrings {
		if strings.Contains(k, sub) {
			return true
		}
	}
	// Catch "_key"-suffixed or "key_"-prefixed names (api_key, caller_key,
	// key_hash, endpoint_key_id, ...) without a bare "key" false-positive.
	if k == "key" || strings.HasSuffix(k, "_key") || strings.HasPrefix(k, "key_") {
		return true
	}
	return false
}

// New returns a slog.Logger writing JSON to w at the given level, redacting
// sensitive attributes. The level controls filtering; debug is for dev only.
func New(w io.Writer, level slog.Level) *slog.Logger {
	h := slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level:       level,
		ReplaceAttr: redactReplace,
	})
	return slog.New(h)
}

// ParseLevel maps a lowercase level name to a slog.Level. Unknown values fall
// back to LevelInfo, the production default.
func ParseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}