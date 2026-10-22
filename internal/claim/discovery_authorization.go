package claim

import (
	"context"
	"database/sql"

	"github.com/waiting-here/NonbiriAPI/internal/connectionstore"
	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/db"
)

func discoveryWire(format string) connectorcontract.Type {
	switch format {
	case "openai":
		return connectorcontract.TypeOpenAICompatible
	case "anthropic":
		return connectorcontract.TypeAnthropicCompatible
	case "gemini":
		return connectorcontract.TypeGoogleGemini
	case "ai-sdk-gateway-v3":
		return connectorcontract.TypeAISDKGatewayV3
	case "ai-sdk-gateway-v4":
		return connectorcontract.TypeAISDKGatewayV4
	default:
		return ""
	}
}

func resolveDiscoveryTargetTx(ctx context.Context, tx *sql.Tx, input DiscoveryClaimInput) (authorizedTarget, error) {
	var target authorizedTarget
	if !db.ValidateOpaqueID(input.OperationID, "op_") || input.SourceRevisionID <= 0 || input.CredentialVersionID <= 0 {
		return target, ErrInvalidInput
	}
	if input.Source.Purpose == connectionstore.SourceSelf && input.Source.OwnerUserID != input.ActorUserID {
		return target, ErrNotFound
	}
	if input.Source.Purpose == connectionstore.SourceCharity && input.Authorize == nil {
		return target, ErrNotFound
	}
	source, err := connectionstore.LoadDiscoveryAuthorizationTx(ctx, tx, input.Source)
	if err != nil {
		return target, authorizationError(err)
	}
	if source.SourceRevisionID != input.SourceRevisionID || source.CredentialVersionID != input.CredentialVersionID ||
		source.UseApprovedRevisionID != input.UseApprovedRevisionID {
		return target, ErrNotFound
	}
	if err := requireCurrentDiscoveryRunTx(ctx, tx, source, input.OperationID); err != nil {
		return target, err
	}
	wire := discoveryWire(source.ListFormat)
	if !wire.Valid() || input.Candidate.ConnectorType != wire || input.Candidate.CanonicalBaseURL != source.CanonicalBaseURL {
		return target, ErrNotFound
	}
	if err := requireDiscoveryQuotaKeyTx(ctx, tx, source.OwnerUserID, input.Candidate.EndpointID, input.Candidate.EndpointKeyID); err != nil {
		return target, err
	}
	target.source = source
	target.grant = connectionstore.GrantAuthorization{
		OwnerUserID: source.OwnerUserID, EndpointID: input.Candidate.EndpointID, EndpointKeyID: input.Candidate.EndpointKeyID,
		CredentialID: source.CredentialID, CredentialVersionID: source.CredentialVersionID, CredentialVersion: source.CredentialVersion,
		SecretRefID: source.SecretRefID, ConfiguredType: wire, CanonicalBaseURL: source.CanonicalBaseURL,
	}
	return target, nil
}

func requireDiscoveryQuotaKeyTx(ctx context.Context, tx *sql.Tx, sourceOwnerID, candidateEndpointID, keyID int64) error {
	if keyID > 0 {
		var endpointID, ownerID int64
		var enabled, suspended bool
		err := tx.QueryRowContext(ctx, `SELECT e.id,e.user_id,k.enabled AND e.enabled,EXISTS(SELECT 1 FROM endpoint_key_suspensions s WHERE s.endpoint_key_id=k.id)
FROM endpoint_keys k JOIN endpoints e ON e.id=k.endpoint_id WHERE k.id=?`, keyID).Scan(&endpointID, &ownerID, &enabled, &suspended)
		if err != nil {
			return authorizationError(err)
		}
		if ownerID != sourceOwnerID || endpointID != candidateEndpointID || !enabled || suspended {
			return ErrNotFound
		}
	} else if candidateEndpointID != 0 || keyID != 0 {
		return ErrNotFound
	}
	return nil
}

func requireCurrentDiscoveryRunTx(ctx context.Context, tx *sql.Tx, source connectionstore.SourceAuthorization, operationID string) error {
	var available bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM discovery_runs r
JOIN discovery_source_states s ON s.source_revision_id=r.source_revision_id AND s.current_run_id=r.id
JOIN accepted_operations op ON op.id=r.operation_id
WHERE r.operation_id=? AND r.source_revision_id=? AND r.credential_version_id=? AND r.state='checking'
AND r.authorization_kind=? AND COALESCE(r.authorization_use_id,0)=? AND COALESCE(r.authorization_use_revision_id,0)=?
AND op.state IN ('accepted','running'))`, operationID, source.SourceRevisionID, source.CredentialVersionID,
		source.Purpose, source.DonationUseID, source.UseApprovedRevisionID).Scan(&available)
	if err != nil {
		return err
	}
	if !available {
		return ErrNotFound
	}
	return nil
}

func persistDiscoveryTargetTx(ctx context.Context, tx *sql.Tx, claimID string, input DiscoveryClaimInput, target authorizedTarget) error {
	source := target.source
	var endpointID, keyID, useID, useRevisionID any
	if input.Candidate.EndpointKeyID > 0 {
		endpointID, keyID = input.Candidate.EndpointID, input.Candidate.EndpointKeyID
	}
	if source.DonationUseID > 0 {
		useID, useRevisionID = source.DonationUseID, source.UseApprovedRevisionID
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO dispatch_claim_targets
(claim_id,authorization_kind,owner_user_id,endpoint_key_id_snapshot,endpoint_id_snapshot,credential_id_snapshot,
credential_version_id,credential_version,configured_type,wire_type,canonical_base_url,upstream_model_id,rerank_format,
use_id_snapshot,use_revision_id_snapshot,source_revision_id_snapshot,operation_id)
VALUES(?,'discovery',?,?,?,?,?,?,?,?,?,'','',?,?,?,?)`, claimID, source.OwnerUserID, keyID, endpointID,
		source.CredentialID, source.CredentialVersionID, source.CredentialVersion, target.grant.ConfiguredType, target.grant.ConfiguredType,
		source.CanonicalBaseURL, useID, useRevisionID, source.SourceRevisionID, input.OperationID)
	return err
}

func revalidateDiscoveryTargetTx(ctx context.Context, tx *sql.Tx, claimID string, at int64) error {
	var ref connectionstore.SourceAuthorizationRef
	var sourceRevisionID, versionID, approvedID, secretRefID, actorID, endpointID, keyID int64
	var operationID, baseURL string
	var wire connectorcontract.Type
	err := tx.QueryRowContext(ctx, `SELECT t.owner_user_id,s.source_id,t.source_revision_id_snapshot,t.credential_version_id,
COALESCE(t.use_id_snapshot,0),COALESCE(t.use_revision_id_snapshot,0),t.operation_id,t.wire_type,t.canonical_base_url,c.secret_ref_id,r.user_id,
COALESCE(t.endpoint_id_snapshot,0),COALESCE(t.endpoint_key_id_snapshot,0)
FROM dispatch_claim_targets t JOIN dispatch_claims c ON c.id=t.claim_id JOIN logical_requests r ON r.id=c.logical_request_id
JOIN discovery_source_revisions s ON s.id=t.source_revision_id_snapshot WHERE t.claim_id=? AND c.state='claimed'`, claimID).Scan(
		&ref.OwnerUserID, &ref.SourceID, &sourceRevisionID, &versionID, &ref.DonationUseID, &approvedID, &operationID, &wire, &baseURL, &secretRefID, &actorID, &endpointID, &keyID)
	if err != nil {
		return authorizationError(err)
	}
	ref.Purpose = connectionstore.SourceSelf
	if ref.DonationUseID > 0 {
		ref.Purpose = connectionstore.SourceCharity
	}
	source, err := connectionstore.LoadDiscoveryAuthorizationTx(ctx, tx, ref)
	if err != nil {
		return authorizationError(err)
	}
	if source.SourceRevisionID != sourceRevisionID || source.CredentialVersionID != versionID || source.UseApprovedRevisionID != approvedID ||
		source.SecretRefID != secretRefID || source.CanonicalBaseURL != baseURL || discoveryWire(source.ListFormat) != wire {
		return ErrNotFound
	}
	if err := requireCurrentDiscoveryRunTx(ctx, tx, source, operationID); err != nil {
		return err
	}
	if err := requireDiscoveryQuotaKeyTx(ctx, tx, source.OwnerUserID, endpointID, keyID); err != nil {
		return err
	}
	if ref.Purpose == connectionstore.SourceSelf && actorID != source.OwnerUserID {
		return ErrNotFound
	}
	return requireActiveUser(ctx, tx, actorID, at, false)
}
