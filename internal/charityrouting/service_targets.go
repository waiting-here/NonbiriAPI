package charityrouting

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/waiting-here/NonbiriAPI/internal/connectionstore"
	"github.com/waiting-here/NonbiriAPI/internal/connector"
	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
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
	available, err := availableServiceUsesTx(ctx, tx, keys, now)
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
	for _, item := range candidates {
		for _, use := range uses[item.candidate.DonationKeyID] {
			candidate, grant := item.candidate, use.Grant
			if !available[use.UseID] || grant.EndpointID != candidate.EndpointID || grant.EndpointKeyID != candidate.EndpointKeyID ||
				!candidate.NativeServices.Has(use.NativeService) || !grant.EffectiveServices.Has(use.NativeService) {
				continue
			}
			services, err := connectorcontract.ParseNativeServices([]connectorcontract.NativeService{use.NativeService})
			if err != nil {
				return nil, ErrInvariant
			}
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
					sawAvailableOperation = true
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
				continue
			}
			candidate.NativeServices = services
			candidate.ConnectorType, candidate.CanonicalBaseURL = grant.ConfiguredType, grant.CanonicalBaseURL
			candidate.Authorization = routing.TargetAuthorization{
				BindingID: candidate.BindingID, TargetID: candidate.Authorization.TargetID, TargetRevision: candidate.Authorization.TargetRevision,
				CredentialID: grant.CredentialID, CredentialVersionID: grant.CredentialVersionID, CredentialVersion: grant.CredentialVersion,
				GrantRevisionID: grant.GrantRevisionID, GrantRevision: grant.GrantRevision,
				ConfiguredType: grant.ConfiguredType, RerankFormat: grant.RerankFormat, NativeService: use.NativeService,
				UseID: use.UseID, UseRevisionID: use.ApprovedRevisionID, UseRevision: use.ApprovedRevision,
			}
			result = append(result, candidate)
			groups[candidate.BindingID] = true
		}
	}
	if len(groups) > MaxRuntimeCandidates {
		return nil, ErrResourceLimit
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

func availableServiceUsesTx(ctx context.Context, tx *sql.Tx, keys []int64, now int64) (map[int64]bool, error) {
	if len(keys) == 0 || len(keys) > maxBindingBatch {
		return nil, ErrInvalidRequest
	}
	args := make([]any, 0, len(keys)+2)
	for _, key := range keys {
		args = append(args, key)
	}
	args = append(args, now)
	rows, err := tx.QueryContext(ctx, `SELECT a.use_id FROM charity_use_availability a
JOIN donation_keys dk ON dk.id=a.donation_key_id
LEFT JOIN donation_service_health h ON h.use_id=a.use_id AND h.endpoint_key_id=dk.endpoint_key_id
AND h.native_service_bit=a.native_service_bit AND h.credential_version_id=a.credential_version_id
WHERE a.donation_key_id IN (`+strings.TrimSuffix(strings.Repeat("?,", len(keys)), ",")+`)
AND COALESCE(h.failure_disabled,0)=0 AND `+smartbackoff.ServiceUntilSQL("h")+`<=?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make(map[int64]bool)
	for rows.Next() {
		var useID int64
		if err := rows.Scan(&useID); err != nil {
			return nil, err
		}
		result[useID] = true
	}
	return result, rows.Err()
}
