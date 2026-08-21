package db

import (
	"context"
	"database/sql"
	"errors"

	"github.com/waiting-here/NonbiriAPI/internal/egress"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

// ErrEndpointCredentialMigration is the opaque startup-migration failure. It
// intentionally carries no row identifier, URL, authenticated context,
// ciphertext, or cryptographic detail.
var ErrEndpointCredentialMigration = errors.New("db: endpoint credential migration failed")

// MigrateEndpointKeyEnvelopes transactionally upgrades every legacy endpoint
// credential and validates every already-contextual credential. It is
// idempotent: a valid contextual row is authenticated but never rewritten.
// Any malformed, unknown, damaged, orphaned, or wrong-context row aborts and
// rolls back the complete batch.
func (s *Store) MigrateEndpointKeyEnvelopes(ctx context.Context) error {
	if s == nil || s.db == nil || nilSecretCodec(s.secrets) || ctx == nil {
		return ErrEndpointCredentialMigration
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ErrEndpointCredentialMigration
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	var afterID int64
	for {
		var keyID int64
		var endpointID, userID sql.NullInt64
		var baseURL, ciphertext sql.NullString
		err := tx.QueryRowContext(ctx, `
SELECT ek.id, e.id, u.id,
       CASE WHEN length(CAST(e.base_url AS BLOB)) BETWEEN 1 AND ? THEN e.base_url END,
       CASE WHEN length(CAST(ek.encrypted_secret AS BLOB)) BETWEEN 1 AND ? THEN ek.encrypted_secret END
FROM endpoint_keys ek
LEFT JOIN endpoints e ON e.id=ek.endpoint_id
LEFT JOIN users u ON u.id=e.user_id
WHERE ek.id>?
ORDER BY ek.id
LIMIT 1`, maxStoredEndpointBaseURLBytes, maxEndpointCredentialEnvelopeBytes, afterID).
			Scan(&keyID, &endpointID, &userID, &baseURL, &ciphertext)
		if errors.Is(err, sql.ErrNoRows) {
			break
		}
		if err != nil || keyID <= afterID || !endpointID.Valid || endpointID.Int64 <= 0 ||
			!userID.Valid || userID.Int64 <= 0 || !baseURL.Valid || !ciphertext.Valid {
			return ErrEndpointCredentialMigration
		}
		afterID = keyID

		_, canonicalOrigin, err := egress.CanonicalEndpointTarget(baseURL.String)
		baseURL.String = ""
		if err != nil {
			return ErrEndpointCredentialMigration
		}
		credentialContext, err := secret.NewEndpointKeyContext(userID.Int64, endpointID.Int64, keyID, canonicalOrigin)
		canonicalOrigin = ""
		if err != nil {
			return ErrEndpointCredentialMigration
		}
		version, err := secret.ParseEnvelopeVersion(ciphertext.String)
		if err != nil {
			ciphertext.String = ""
			return ErrEndpointCredentialMigration
		}

		switch version {
		case secret.EnvelopeVersionV1:
			plaintext, err := s.secrets.Open(ciphertext.String)
			if err != nil {
				clear(plaintext)
				ciphertext.String = ""
				return ErrEndpointCredentialMigration
			}
			replacement, err := s.secrets.SealForContext(plaintext, credentialContext)
			clear(plaintext)
			if err != nil {
				ciphertext.String = ""
				return ErrEndpointCredentialMigration
			}
			result, err := tx.ExecContext(ctx, `
UPDATE endpoint_keys
SET encrypted_secret=?
WHERE id=? AND endpoint_id=? AND encrypted_secret=?
  AND endpoint_id IN (
    SELECT e.id FROM endpoints e
    JOIN users u ON u.id=e.user_id
    WHERE e.id=? AND u.id=?)`,
				replacement, keyID, endpointID.Int64, ciphertext.String, endpointID.Int64, userID.Int64)
			replacement = ""
			ciphertext.String = ""
			if err != nil {
				return ErrEndpointCredentialMigration
			}
			affected, err := result.RowsAffected()
			if err != nil || affected != 1 {
				return ErrEndpointCredentialMigration
			}
		case secret.EnvelopeVersionV2:
			plaintext, err := s.secrets.OpenForContext(ciphertext.String, credentialContext)
			ciphertext.String = ""
			if err != nil {
				clear(plaintext)
				return ErrEndpointCredentialMigration
			}
			clear(plaintext)
		default:
			ciphertext.String = ""
			return ErrEndpointCredentialMigration
		}
	}

	if err := tx.Commit(); err != nil {
		return ErrEndpointCredentialMigration
	}
	committed = true
	return nil
}
