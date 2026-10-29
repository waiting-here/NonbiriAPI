package db

import (
	"context"
	"database/sql"
	"errors"

	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

// Full verification includes the separate administrator connection envelope.
// Ordinary startup retains the existing bounded identity checks.
func validateCodexEnvelope(ctx context.Context, q queryer, codec secret.GenerationTwoContextCodec) error {
	var encrypted string
	err := q.QueryRowContext(ctx, "SELECT encrypted_credentials FROM agent_codex_session WHERE id=1").Scan(&encrypted)
	if errors.Is(err, sql.ErrNoRows) || err == nil && encrypted == "" {
		return nil
	}
	if err != nil {
		return err
	}
	decoder, ok := codec.(secret.CodexCodec)
	if !ok {
		return errors.New("Codex credential codec is unavailable")
	}
	plain, err := decoder.OpenCodex(encrypted)
	clear(plain)
	if err != nil {
		return errors.New("Codex credential envelope was rejected")
	}
	return nil
}
