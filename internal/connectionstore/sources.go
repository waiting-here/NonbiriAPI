package connectionstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// LoadDiscoveryAuthorizationTx resolves the specified purpose without borrowing
// the current self authorization for an approved charity use.
func LoadDiscoveryAuthorizationTx(ctx context.Context, tx *sql.Tx, ref SourceAuthorizationRef) (SourceAuthorization, error) {
	if tx == nil || ref.OwnerUserID <= 0 || ref.SourceID <= 0 || (ref.Purpose != SourceSelf && ref.Purpose != SourceCharity) ||
		(ref.UseRevisionID < 0) || (ref.Purpose == SourceSelf && (ref.DonationUseID != 0 || ref.UseRevisionID != 0)) || (ref.Purpose == SourceCharity && ref.DonationUseID <= 0) {
		return SourceAuthorization{}, ErrInvariant
	}
	var result SourceAuthorization
	var sourceRevisionID int64
	err := tx.QueryRowContext(ctx, "SELECT revision,current_revision_id FROM discovery_sources WHERE id=? AND user_id=?", ref.SourceID, ref.OwnerUserID).Scan(&result.ResourceRevision, &sourceRevisionID)
	if errors.Is(err, sql.ErrNoRows) {
		return SourceAuthorization{}, ErrNotFound
	}
	if err != nil {
		return SourceAuthorization{}, fmt.Errorf("connectionstore: read source: %w", err)
	}
	var use DiscoveryUseAuthorization
	if ref.Purpose == SourceCharity {
		use, err = LoadDiscoveryUseTx(ctx, tx, ref.DonationUseID, ref.UseRevisionID)
		if err != nil {
			return SourceAuthorization{}, err
		}
		if use.Grant.OwnerUserID != ref.OwnerUserID {
			return SourceAuthorization{}, ErrNotFound
		}
		if use.DiscoverySourceRevisionID <= 0 {
			return SourceAuthorization{}, ErrUnavailable
		}
		sourceRevisionID = use.DiscoverySourceRevisionID
	}
	var relationshipValid, available int
	err = tx.QueryRowContext(ctx, "SELECT owner_user_id,source_id,source_revision_id,source_revision,credential_id,credential_version_id,credential_version,COALESCE(secret_ref_id,0),canonical_base_url,list_format,relationship_valid,available FROM discovery_source_authorizations WHERE source_revision_id=?", sourceRevisionID).
		Scan(&result.OwnerUserID, &result.SourceID, &result.SourceRevisionID, &result.SourceRevision, &result.CredentialID, &result.CredentialVersionID, &result.CredentialVersion, &result.SecretRefID, &result.CanonicalBaseURL, &result.ListFormat, &relationshipValid, &available)
	if errors.Is(err, sql.ErrNoRows) {
		return SourceAuthorization{}, ErrInvariant
	}
	if err != nil {
		return SourceAuthorization{}, fmt.Errorf("connectionstore: read source authorization: %w", err)
	}
	if relationshipValid != 1 || !validBool(available) || result.OwnerUserID <= 0 || result.SourceID <= 0 || result.SourceRevisionID <= 0 || result.SourceRevision <= 0 || result.CredentialID <= 0 || result.CredentialVersionID <= 0 || result.CredentialVersion <= 0 || result.ResourceRevision <= 0 || result.CanonicalBaseURL == "" || !validListFormat(result.ListFormat) {
		return SourceAuthorization{}, ErrInvariant
	}
	if result.OwnerUserID != ref.OwnerUserID || result.SourceID != ref.SourceID {
		return SourceAuthorization{}, ErrUnavailable
	}
	if available == 0 {
		return SourceAuthorization{}, ErrUnavailable
	}
	if result.SecretRefID <= 0 {
		return SourceAuthorization{}, ErrInvariant
	}
	result.Purpose = ref.Purpose
	if ref.Purpose == SourceCharity {
		if result.CredentialID != use.Grant.CredentialID || result.CredentialVersionID != use.Grant.CredentialVersionID || result.CredentialVersion != use.Grant.CredentialVersion {
			return SourceAuthorization{}, ErrInvariant
		}
		result.DonationUseID = use.UseID
		result.UseApprovedRevisionID = use.RevisionID
	}
	return result, nil
}

func validListFormat(format string) bool {
	switch format {
	case "openai", "anthropic", "gemini", "ai-sdk-gateway-v3", "ai-sdk-gateway-v4":
		return true
	default:
		return false
	}
}
