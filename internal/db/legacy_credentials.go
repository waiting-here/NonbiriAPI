package db

import (
	"context"
	"database/sql"
	"errors"
	"unicode/utf8"

	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/egress"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

// ValidateLegacyStoredEndpointCredentials is used only after the offline
// upgrader has matched an exact supported source manifest.
func ValidateLegacyStoredEndpointCredentials(ctx context.Context, q queryer, codec secret.GenerationTwoContextCodec) error {
	var version int
	if err := q.QueryRowContext(ctx, "SELECT version FROM schema_state WHERE id=1").Scan(&version); err != nil {
		return err
	}
	if version < 16 || version > 26 {
		return errors.New("unsupported legacy credential schema")
	}
	return validateStoredEndpointCredentials(ctx, q, codec, false)
}

func validateLegacyCredentialLinks(ctx context.Context, q queryer) error {
	rows, err := q.QueryContext(ctx, `
SELECT ek.id, ek.secret_ref_id,
       typeof(e.base_url), length(CAST(e.base_url AS BLOB)),
       CASE WHEN typeof(e.base_url)='text'
                  AND length(CAST(e.base_url AS BLOB)) BETWEEN 1 AND ?
            THEN e.base_url END,
       e.connector_type, eks.canonical_base_url, eks.connector_type, eks.orphaned_at
FROM endpoint_keys ek
JOIN endpoints e ON e.id=ek.endpoint_id
JOIN endpoint_key_secrets eks ON eks.id=ek.secret_ref_id
ORDER BY ek.id`, maxStoredEndpointBaseURLBytes)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var keyID, secretRefID int64
		var baseType, endpointConnector, secretBaseURL, secretConnector string
		var baseBytes int64
		var baseURL sql.NullString
		var orphanedAt sql.NullInt64
		if err := rows.Scan(&keyID, &secretRefID, &baseType, &baseBytes, &baseURL, &endpointConnector, &secretBaseURL, &secretConnector, &orphanedAt); err != nil {
			return err
		}
		if keyID <= 0 || secretRefID <= 0 || baseType != "text" || baseBytes < 1 || baseBytes > maxStoredEndpointBaseURLBytes ||
			!baseURL.Valid || int64(len([]byte(baseURL.String))) != baseBytes || !utf8.ValidString(baseURL.String) ||
			!connectorcontract.Type(endpointConnector).Valid() ||
			secretConnector != endpointConnector || orphanedAt.Valid {
			baseURL.String = ""
			return errors.New("invalid endpoint credential link")
		}
		target, _, targetErr := egress.CanonicalEndpointTarget(baseURL.String)
		secretTarget, _, secretTargetErr := egress.CanonicalEndpointTarget(secretBaseURL)
		baseURL.String = ""
		if targetErr != nil || secretTargetErr != nil || target != secretTarget {
			return errors.New("invalid endpoint credential link origin")
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}

	return nil
}
