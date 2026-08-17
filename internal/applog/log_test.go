package applog

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

// captureLogger builds a logger writing to a buffer at the given level.
func captureLogger(t *testing.T, level slog.Level) (*slog.Logger, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	return New(&buf, level), &buf
}

func TestRedactionByFieldName(t *testing.T) {
	logger, buf := captureLogger(t, slog.LevelDebug)
	logger.Info("login attempt",
		"api_key", "sk-plaintext-visible-123",
		"token", "tok-plaintext-abc",
		"password", "supersecret-pw",
		"Authorization", "Bearer leak",
		"client_secret", "cs-leak",
		"master_key", "mk-bytes-leak",
		"normal_field", "this must stay visible",
	)
	out := buf.String()

	for _, secret := range []string{"sk-plaintext-visible-123", "tok-plaintext-abc", "supersecret-pw", "Bearer leak", "cs-leak", "mk-bytes-leak"} {
		if strings.Contains(out, secret) {
			t.Fatalf("log leaked a sensitive value %q:\n%s", secret, out)
		}
	}
	if !strings.Contains(out, Redacted) {
		t.Fatalf("log does not contain the redaction marker:\n%s", out)
	}
	if !strings.Contains(out, "this must stay visible") {
		t.Fatalf("log redacted a non-sensitive value:\n%s", out)
	}
}

func TestRedactionWithinGroup(t *testing.T) {
	// slog.Group routes its child attributes through ReplaceAttr (with the
	// group name on the path), so sensitive children are redacted. This is the
	// idiomatic nested form; arbitrary structs passed via slog.Any do not go
	// through ReplaceAttr field-by-field, so callers must use named attrs or
	// slog.Group for nested sensitive values.
	logger, buf := captureLogger(t, slog.LevelDebug)
	logger.Info("cfg", slog.Group("creds",
		"username", "alice",
		"password", "pw-in-group",
		"api_key", "key-in-group",
	))
	out := buf.String()
	for _, secret := range []string{"pw-in-group", "key-in-group"} {
		if strings.Contains(out, secret) {
			t.Fatalf("log leaked a value inside a nested group: %q\n%s", secret, out)
		}
	}
	if !strings.Contains(out, "alice") {
		t.Fatalf("log redacted the non-sensitive child username:\n%s", out)
	}
	if !strings.Contains(out, Redacted) {
		t.Fatalf("log did not redact sensitive group children:\n%s", out)
	}
}

func TestLevelFiltering(t *testing.T) {
	logger, buf := captureLogger(t, slog.LevelWarn)
	logger.Info("should be filtered out")
	logger.Warn("should remain")
	out := buf.String()
	if strings.Contains(out, "should be filtered out") {
		t.Fatalf("info record passed a warn-level logger:\n%s", out)
	}
	if !strings.Contains(out, "should remain") {
		t.Fatalf("warn record was filtered at a warn-level logger:\n%s", out)
	}
}