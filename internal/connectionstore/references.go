package connectionstore

import (
	"context"
	"database/sql"
	"fmt"
)

// FindKeyCredentialEvidenceTx includes retained approved and pending versions,
// even when the key is disabled. Reports owns the caller's permissions.
func FindKeyCredentialEvidenceTx(ctx context.Context, tx *sql.Tx, fingerprint [32]byte, afterEndpointKeyID int64, limit int) ([]KeyCredentialEvidence, error) {
	if tx == nil || afterEndpointKeyID < 0 || limit <= 0 || limit > 1000 {
		return nil, ErrInvariant
	}
	query := "WITH keys AS (SELECT DISTINCT endpoint_key_id FROM endpoint_key_credential_evidence WHERE report_fingerprint=? AND endpoint_key_id>? ORDER BY endpoint_key_id LIMIT ?) " +
		"SELECT e.owner_user_id,e.endpoint_id,e.endpoint_key_id,e.grant_revision_id,e.credential_version_id,e.report_fingerprint,e.key_body_review_hmac,e.display_head,e.display_tail " +
		"FROM keys k JOIN endpoint_key_credential_evidence e ON e.endpoint_key_id=k.endpoint_key_id WHERE e.report_fingerprint=? AND e.grant_revision_id=(SELECT MIN(m.grant_revision_id) FROM endpoint_key_credential_evidence m WHERE m.endpoint_key_id=e.endpoint_key_id AND m.report_fingerprint=?) ORDER BY e.endpoint_key_id"
	rows, err := tx.QueryContext(ctx, query, fingerprint[:], afterEndpointKeyID, limit, fingerprint[:], fingerprint[:])
	if err != nil {
		return nil, fmt.Errorf("connectionstore: read key evidence: %w", err)
	}
	defer rows.Close()
	result := make([]KeyCredentialEvidence, 0)
	var previous int64
	for rows.Next() {
		var item KeyCredentialEvidence
		var storedFingerprint, bodyHMAC []byte
		if err := rows.Scan(&item.OwnerUserID, &item.EndpointID, &item.EndpointKeyID, &item.GrantRevisionID, &item.CredentialVersionID, &storedFingerprint, &bodyHMAC, &item.DisplayHead, &item.DisplayTail); err != nil {
			return nil, err
		}
		if item.OwnerUserID <= 0 || item.EndpointID <= 0 || item.EndpointKeyID <= afterEndpointKeyID || item.EndpointKeyID <= previous || item.GrantRevisionID <= 0 || item.CredentialVersionID <= 0 || len(storedFingerprint) != 32 || (bodyHMAC != nil && len(bodyHMAC) != 32) {
			return nil, ErrInvariant
		}
		copy(item.Fingerprint[:], storedFingerprint)
		if item.Fingerprint != fingerprint {
			return nil, ErrInvariant
		}
		if bodyHMAC != nil {
			var value [32]byte
			copy(value[:], bodyHMAC)
			item.BodyReviewHMAC = &value
		}
		result = append(result, item)
		previous = item.EndpointKeyID
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

// HasLiveVersionReferenceTx does not count inactive history as authorization.
func HasLiveVersionReferenceTx(ctx context.Context, tx *sql.Tx, credentialVersionID int64) (bool, error) {
	if tx == nil || credentialVersionID <= 0 {
		return false, ErrInvariant
	}
	var live bool
	err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM credential_version_live_references WHERE credential_version_id=?)", credentialVersionID).Scan(&live)
	if err != nil {
		return false, fmt.Errorf("connectionstore: read version references: %w", err)
	}
	return live, nil
}

// HasLiveSecretReferenceTx also covers pre-upgrade active claim references.
func HasLiveSecretReferenceTx(ctx context.Context, tx *sql.Tx, secretRefID int64) (bool, error) {
	if tx == nil || secretRefID <= 0 {
		return false, ErrInvariant
	}
	var live bool
	err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM secret_live_references WHERE secret_ref_id=?)", secretRefID).Scan(&live)
	if err != nil {
		return false, fmt.Errorf("connectionstore: read secret references: %w", err)
	}
	return live, nil
}
