package claim

import (
	"context"
	"database/sql"
	"errors"

	"github.com/waiting-here/NonbiriAPI/internal/connectionstore"
	"github.com/waiting-here/NonbiriAPI/internal/connector"
	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/routing"
)

type authorizedTarget struct {
	grant           connectionstore.GrantAuthorization
	use             connectionstore.UseAuthorization
	source          connectionstore.SourceAuthorization
	forceStoreFalse bool
}

func authorizationError(err error) error {
	if errors.Is(err, connectionstore.ErrUnavailable) || errors.Is(err, connectionstore.ErrNotFound) || errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if errors.Is(err, connectionstore.ErrInvariant) {
		return ErrInvariant
	}
	return err
}

func sameGrant(a routing.TargetAuthorization, grant connectionstore.GrantAuthorization) bool {
	return a.CredentialID == grant.CredentialID && a.CredentialVersionID == grant.CredentialVersionID &&
		a.CredentialVersion == grant.CredentialVersion && a.GrantRevisionID == grant.GrantRevisionID &&
		a.GrantRevision == grant.GrantRevision && a.ConfiguredType == grant.ConfiguredType && a.RerankFormat == grant.RerankFormat
}

func resolveInferenceTargetTx(ctx context.Context, tx *sql.Tx, input ClaimInput, operation connectorcontract.Operation) (authorizedTarget, error) {
	var target authorizedTarget
	var err error
	a := input.Candidate.Authorization
	unboundDebug := input.Purpose == PurposeDebugLive && a.BindingID == 0 && a.TargetID == 0 && a.TargetRevision == 0
	if a.NativeService == "" || (!unboundDebug && (a.BindingID <= 0 || a.TargetID <= 0 || a.TargetRevision <= 0)) ||
		(unboundDebug && input.PersonalModelID != 0) {
		return target, ErrNotFound
	}
	if input.Purpose == PurposeCharity {
		target.use, err = connectionstore.LoadApprovedUseTx(ctx, tx, input.DonationKeyID, a.NativeService)
		if err != nil {
			return target, authorizationError(err)
		}
		if target.use.UseID != a.UseID || target.use.ApprovedRevisionID != a.UseRevisionID || target.use.ApprovedRevision != a.UseRevision {
			return target, ErrNotFound
		}
		target.grant = target.use.Grant
	} else {
		if a.UseID != 0 || a.UseRevisionID != 0 || a.UseRevision != 0 {
			return target, ErrNotFound
		}
		target.grant, err = connectionstore.LoadSelfGrantTx(ctx, tx, input.ActorUserID, input.Candidate.EndpointKeyID)
		if err != nil {
			return target, authorizationError(err)
		}
	}
	grant := target.grant
	if !sameGrant(a, grant) || grant.EndpointID != input.Candidate.EndpointID || grant.EndpointKeyID != input.Candidate.EndpointKeyID ||
		grant.CanonicalBaseURL != input.Candidate.CanonicalBaseURL || !grant.EffectiveServices.Has(a.NativeService) {
		return target, ErrNotFound
	}
	mask := grant.EffectiveServices.Mask()
	if input.Purpose == PurposeCharity {
		err = tx.QueryRowContext(ctx, `SELECT t.native_service_mask FROM charity_binding_targets t
JOIN charity_model_bindings b ON b.id=t.binding_id
JOIN charity_reservations r ON r.charity_model_id=b.charity_model_id
WHERE t.id=? AND t.revision=? AND b.id=? AND t.donation_key_id=? AND t.upstream_model_id=?
AND r.logical_request_id=? AND r.user_id=?`, a.TargetID, a.TargetRevision, a.BindingID, input.DonationKeyID,
			input.Candidate.UpstreamModelID, input.RequestID, input.ActorUserID).Scan(&mask)
	} else if !unboundDebug {
		err = tx.QueryRowContext(ctx, `SELECT t.native_service_mask FROM model_binding_targets t
JOIN model_bindings b ON b.id=t.binding_id JOIN models m ON m.id=b.model_id
JOIN logical_requests r ON r.user_id=m.user_id AND r.model_snapshot=m.full_name
WHERE t.id=? AND t.revision=? AND b.id=? AND t.endpoint_key_id=? AND t.upstream_model_id=?
AND m.id=? AND m.user_id=? AND r.id=?
AND NOT EXISTS(SELECT 1 FROM user_deletion_markers d WHERE d.user_id=m.user_id)`, a.TargetID, a.TargetRevision, a.BindingID,
			input.Candidate.EndpointKeyID, input.Candidate.UpstreamModelID, input.PersonalModelID, input.ActorUserID, input.RequestID).Scan(&mask)
	}
	if err != nil {
		return target, authorizationError(err)
	}
	services, err := connectorcontract.NativeServicesFromMask(mask)
	if err != nil || services.Empty() {
		return target, ErrInvariant
	}
	services = services.Intersect(grant.EffectiveServices)
	routes, err := connector.ResolveServiceRoutes(grant.ConfiguredType, services, grant.RerankFormat, operation)
	if errors.Is(err, connector.ErrNoServiceRoute) {
		return target, ErrNotFound
	}
	if err != nil {
		return target, ErrInvariant
	}
	matched := false
	for _, route := range routes {
		if route.NativeService == a.NativeService && route.WireType == input.Candidate.ConnectorType {
			matched = true
			break
		}
	}
	if !matched {
		return target, ErrNotFound
	}
	var suspended bool
	err = tx.QueryRowContext(ctx, `SELECT k.force_store_false,EXISTS(SELECT 1 FROM endpoint_key_suspensions x WHERE x.endpoint_key_id=k.id)
FROM endpoint_keys k WHERE k.id=?`, grant.EndpointKeyID).Scan(&target.forceStoreFalse, &suspended)
	if err != nil {
		return target, authorizationError(err)
	}
	if suspended {
		return target, ErrNotFound
	}
	return target, nil
}

func persistInferenceTargetTx(ctx context.Context, tx *sql.Tx, claimID string, input ClaimInput, target authorizedTarget) error {
	a, grant := input.Candidate.Authorization, target.grant
	service, err := connectorcontract.ParseNativeServices([]connectorcontract.NativeService{a.NativeService})
	if err != nil {
		return ErrInvariant
	}
	kind := "self"
	var useID, useRevisionID, targetID, targetRevision any
	if a.TargetID > 0 {
		targetID, targetRevision = a.TargetID, a.TargetRevision
	}
	if input.Purpose == PurposeCharity {
		kind, useID, useRevisionID = "charity", a.UseID, a.UseRevisionID
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO dispatch_claim_targets
(claim_id,authorization_kind,owner_user_id,endpoint_key_id_snapshot,endpoint_id_snapshot,
credential_id_snapshot,credential_version_id,credential_version,grant_revision_id_snapshot,grant_revision,
target_id_snapshot,target_revision,native_service_bit,configured_type,wire_type,canonical_base_url,
upstream_model_id,rerank_format,use_id_snapshot,use_revision_id_snapshot)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, claimID, kind, grant.OwnerUserID, grant.EndpointKeyID, grant.EndpointID,
		grant.CredentialID, grant.CredentialVersionID, grant.CredentialVersion, grant.GrantRevisionID, grant.GrantRevision,
		targetID, targetRevision, service.Mask(), grant.ConfiguredType, input.Candidate.ConnectorType,
		grant.CanonicalBaseURL, input.Candidate.UpstreamModelID, grant.RerankFormat, useID, useRevisionID)
	return err
}

// RevalidateDispatchTargetTx runs inside the transaction that marks dispatch.
// The stored immutable target is checked again against live grants and targets.
func RevalidateDispatchTargetTx(ctx context.Context, tx *sql.Tx, claimID string, at int64) error {
	var input ClaimInput
	var kind string
	var operationRoute RouteKind
	var serviceMask int64
	var secretRefID int64
	a := &input.Candidate.Authorization
	if err := tx.QueryRowContext(ctx, `SELECT authorization_kind FROM dispatch_claim_targets WHERE claim_id=?`, claimID).Scan(&kind); err != nil {
		return authorizationError(err)
	}
	if kind == "discovery" {
		return revalidateDiscoveryTargetTx(ctx, tx, claimID, at)
	}
	err := tx.QueryRowContext(ctx, `SELECT c.logical_request_id,c.purpose,COALESCE(c.donation_key_id,0),c.secret_ref_id,r.user_id,r.route_kind,
t.authorization_kind,t.endpoint_id_snapshot,t.endpoint_key_id_snapshot,t.credential_id_snapshot,t.credential_version_id,t.credential_version,
t.grant_revision_id_snapshot,t.grant_revision,COALESCE(t.target_id_snapshot,0),COALESCE(t.target_revision,0),t.native_service_bit,t.configured_type,t.wire_type,
t.canonical_base_url,t.upstream_model_id,t.rerank_format,COALESCE(t.use_id_snapshot,0),COALESCE(t.use_revision_id_snapshot,0)
FROM dispatch_claims c JOIN logical_requests r ON r.id=c.logical_request_id JOIN dispatch_claim_targets t ON t.claim_id=c.id
WHERE c.id=? AND c.state='claimed'`, claimID).Scan(&input.RequestID, &input.Purpose, &input.DonationKeyID, &secretRefID, &input.ActorUserID, &operationRoute,
		&kind, &input.Candidate.EndpointID, &input.Candidate.EndpointKeyID, &a.CredentialID, &a.CredentialVersionID, &a.CredentialVersion,
		&a.GrantRevisionID, &a.GrantRevision, &a.TargetID, &a.TargetRevision, &serviceMask, &a.ConfiguredType, &input.Candidate.ConnectorType,
		&input.Candidate.CanonicalBaseURL, &input.Candidate.UpstreamModelID, &a.RerankFormat, &a.UseID, &a.UseRevisionID)
	if err != nil {
		return authorizationError(err)
	}
	if kind != "self" && kind != "charity" {
		return ErrNotFound
	}
	services, err := connectorcontract.NativeServicesFromMask(serviceMask)
	if err != nil || len(services.Services()) != 1 {
		return ErrInvariant
	}
	a.NativeService = services.Services()[0]
	if kind == "charity" {
		err = tx.QueryRowContext(ctx, `SELECT binding_id FROM charity_binding_targets WHERE id=?`, a.TargetID).Scan(&a.BindingID)
		if err == nil {
			err = tx.QueryRowContext(ctx, `SELECT use_revision FROM donation_use_revisions WHERE id=? AND use_id=?`, a.UseRevisionID, a.UseID).Scan(&a.UseRevision)
		}
	} else if a.TargetID > 0 {
		err = tx.QueryRowContext(ctx, `SELECT b.id,b.model_id FROM model_binding_targets t JOIN model_bindings b ON b.id=t.binding_id WHERE t.id=?`, a.TargetID).Scan(&a.BindingID, &input.PersonalModelID)
	}
	if err != nil {
		return authorizationError(err)
	}
	target, err := resolveInferenceTargetTx(ctx, tx, input, operationRoute.Operation())
	if err != nil {
		return err
	}
	if target.grant.SecretRefID != secretRefID {
		return ErrInvariant
	}
	return requireActiveUser(ctx, tx, input.ActorUserID, at, kind == "charity")
}

func persistAttemptServiceFactsTx(ctx context.Context, tx *sql.Tx, claimID string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO request_attempt_service_facts
(claim_id,credential_id_snapshot,credential_version,grant_revision,target_revision,native_service_bit,configured_type,wire_type,rerank_format,use_id_snapshot,use_revision)
SELECT t.claim_id,t.credential_id_snapshot,t.credential_version,t.grant_revision,t.target_revision,t.native_service_bit,t.configured_type,t.wire_type,t.rerank_format,t.use_id_snapshot,
(SELECT u.use_revision FROM donation_use_revisions u WHERE u.id=t.use_revision_id_snapshot)
FROM dispatch_claim_targets t WHERE t.claim_id=?`, claimID)
	return err
}
