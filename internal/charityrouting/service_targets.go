package charityrouting

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/connectionstore"
	"github.com/waiting-here/NonbiriAPI/internal/connector"
	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/donationquota"
	"github.com/waiting-here/NonbiriAPI/internal/requestattempt"
	"github.com/waiting-here/NonbiriAPI/internal/routing"
	"github.com/waiting-here/NonbiriAPI/internal/smartbackoff"
)

func authorizedServiceTargetsTx(ctx context.Context, tx *sql.Tx, candidates []weightedRuntimeCandidate, connectorSet map[connectorcontract.Type]struct{}, operation connectorcontract.Operation, now int64) ([]RuntimeCandidate, error) {
	keys := make([]int64, 0, len(candidates))
	seenKeys := make(map[int64]bool)
	for _, item := range candidates {
		key := item.candidate.DonationKeyID
		if !seenKeys[key] {
			keys = append(keys, key)
			seenKeys[key] = true
		}
	}
	if len(keys) == 0 {
		return nil, nil
	}
	uses, err := connectionstore.LoadApprovedUsesTx(ctx, tx, keys)
	if err != nil {
		return nil, err
	}
	targetIDs := make([]int64, 0, len(candidates))
	for _, item := range candidates {
		targetIDs = append(targetIDs, item.candidate.Authorization.TargetID)
	}
	available, err := availableServiceTargetsTx(ctx, tx, targetIDs, now)
	if err != nil {
		return nil, err
	}
	operations := connectorcontract.AllOperations()
	if operation != "" {
		valid := false
		for _, known := range operations {
			if known == operation {
				valid = true
				break
			}
		}
		if !valid {
			return nil, ErrInvalidRequest
		}
		operations = []connectorcontract.Operation{operation}
	}
	result := make([]RuntimeCandidate, 0, len(candidates))
	groups := make(map[int64]bool)
	sawAvailableOperation := false
	sawModelQuota := false
	var skipped requestattempt.AdmissionCause
	for _, item := range candidates {
		var approvedMask int64
		var unsupported connectorcontract.NativeService
		start := len(result)
		for _, use := range uses[item.candidate.DonationKeyID] {
			candidate, grant := item.candidate, use.Grant
			if grant.EndpointID != candidate.EndpointID || grant.EndpointKeyID != candidate.EndpointKeyID ||
				!candidate.NativeServices.Has(use.NativeService) || !grant.EffectiveServices.Has(use.NativeService) {
				continue
			}
			services, err := connectorcontract.ParseNativeServices([]connectorcontract.NativeService{use.NativeService})
			if err != nil {
				return nil, ErrInvariant
			}
			approvedMask |= services.Mask()
			state, present := available[serviceTarget{use.UseID, candidate.Authorization.TargetID}]
			modelLimited := state.modelRetryAt > now && state.reason == state.modelReason
			if !present || (state.reason != "" && !modelLimited) {
				skipped = skipped.Combine(requestattempt.AdmissionUnavailable)
				if state.reason == "" {
					state.reason = "unavailable"
				}
				requestattempt.ObserveRoute(ctx, candidate.BindingID, use.NativeService, state.reason, "", "")
				continue
			}
			matchedOperation := false
			compatible := false
			for _, requested := range operations {
				routes, err := connector.ResolveServiceRoutes(grant.ConfiguredType, services, grant.RerankFormat, requested)
				if errors.Is(err, connector.ErrNoServiceRoute) {
					continue
				}
				if err != nil {
					return nil, ErrInvariant
				}
				if len(routes) > 0 {
					sawAvailableOperation = sawAvailableOperation || !modelLimited
					matchedOperation = true
				}
				for _, route := range routes {
					_, allowed := connectorSet[route.WireType]
					if connectorSet == nil || allowed {
						compatible = true
						break
					}
				}
				if compatible {
					break
				}
			}
			if !compatible {
				skipped = skipped.Combine(requestattempt.AdmissionUnavailable)
				if matchedOperation {
					requestattempt.ObserveRoute(ctx, candidate.BindingID, use.NativeService, "features_unsupported", "", "")
				} else if unsupported == "" {
					unsupported = use.NativeService
				}
				continue
			}
			if modelLimited {
				requestattempt.ObserveRoute(ctx, candidate.BindingID, use.NativeService, state.reason, "", "")
				skipped = skipped.Combine(requestattempt.AdmissionCooldown)
				sawModelQuota = true
				continue
			}
			candidate.NativeServices = services
			candidate.ConnectorType, candidate.CanonicalBaseURL = grant.ConfiguredType, grant.CanonicalBaseURL
			candidate.Authorization = routing.TargetAuthorization{
				BindingID: candidate.BindingID, TargetID: candidate.Authorization.TargetID, TargetRevision: candidate.Authorization.TargetRevision,
				CredentialID: grant.CredentialID, CredentialVersionID: grant.CredentialVersionID, CredentialVersion: grant.CredentialVersion,
				GrantRevisionID: grant.GrantRevisionID, GrantRevision: grant.GrantRevision,
				ConfiguredType: grant.ConfiguredType, RerankFormat: grant.RerankFormat, RerankPath: grant.RerankPath, NativeService: use.NativeService,
				UseID: use.UseID, UseRevisionID: use.ApprovedRevisionID, UseRevision: use.ApprovedRevision,
			}
			result = append(result, candidate)
			groups[candidate.BindingID] = true
		}
		if len(result) == start && unsupported != "" {
			requestattempt.ObserveRoute(ctx, item.candidate.BindingID, unsupported, "operation_unsupported", "", "")
		}
		missing, _ := connectorcontract.NativeServicesFromMask(item.candidate.NativeServices.Mask() &^ approvedMask)
		observeBinding(ctx, item.candidate.BindingID, missing, "unavailable")
	}
	if len(groups) > MaxRuntimeCandidates {
		return nil, ErrResourceLimit
	}
	if len(result) == 0 && sawModelQuota {
		return nil, skipped.Wrap(donationquota.ErrLimited)
	}
	if len(result) == 0 && sawAvailableOperation {
		return nil, errors.Join(ErrInvalidRequest, &connectorcontract.RequestRejection{
			Field: "request", Reason: "no connector supports the required request features",
		})
	}
	// Health in another service does not establish capacity for this request.
	// The caller maps an empty selection to unavailable model resources.
	return result, nil
}

type serviceTarget struct{ useID, targetID int64 }
type serviceTargetState struct {
	reason       string
	modelReason  string
	modelRetryAt int64
}

func availableServiceTargetsTx(ctx context.Context, tx *sql.Tx, targetIDs []int64, now int64) (map[serviceTarget]serviceTargetState, error) {
	if len(targetIDs) == 0 || len(targetIDs) > maxBindingBatch {
		return nil, ErrInvalidRequest
	}
	args := []any{now, now}
	for _, target := range targetIDs {
		args = append(args, target)
	}
	rows, err := tx.QueryContext(ctx, `SELECT a.use_id,t.id,CASE WHEN h.failure_disabled=1 THEN 'failure_disabled'
WHEN `+smartbackoff.ServiceUntilSQL("h")+`>? THEN 'cooldown' WHEN mc.retry_at>? THEN mc.reason ELSE '' END,
COALESCE(mc.reason,''),COALESCE(mc.retry_at,0)
FROM charity_binding_targets t JOIN charity_use_availability a ON a.donation_key_id=t.donation_key_id AND (a.native_service_bit & t.native_service_mask)<>0
JOIN donation_keys dk ON dk.id=a.donation_key_id
LEFT JOIN donation_service_health h ON h.use_id=a.use_id AND h.endpoint_key_id=dk.endpoint_key_id
AND h.native_service_bit=a.native_service_bit AND h.credential_version_id=a.credential_version_id
LEFT JOIN donation_service_model_cooldowns mc ON mc.health_id=h.id AND mc.upstream_model_id=t.upstream_model_id
WHERE t.id IN (`+strings.TrimSuffix(strings.Repeat("?,", len(targetIDs)), ",")+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make(map[serviceTarget]serviceTargetState)
	for rows.Next() {
		var key serviceTarget
		var state serviceTargetState
		if err := rows.Scan(&key.useID, &key.targetID, &state.reason, &state.modelReason, &state.modelRetryAt); err != nil {
			return nil, err
		}
		result[key] = state
	}
	return result, rows.Err()
}
