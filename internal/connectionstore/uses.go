package connectionstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

// LoadApprovedUseTx resolves only the current approved use revision.
func LoadApprovedUseTx(ctx context.Context, tx *sql.Tx, donationKeyID int64, service connectorcontract.NativeService) (UseAuthorization, error) {
	uses, err := loadApprovedUsesTx(ctx, tx, []int64{donationKeyID}, &service, true)
	if err != nil {
		return UseAuthorization{}, err
	}
	items := uses[donationKeyID]
	if len(items) == 0 {
		return UseAuthorization{}, ErrNotFound
	}
	if len(items) != 1 {
		return UseAuthorization{}, ErrInvariant
	}
	return items[0], nil
}

// LoadApprovedUsesTx returns each key's available uses in native-service order.
func LoadApprovedUsesTx(ctx context.Context, tx *sql.Tx, donationKeyIDs []int64) (map[int64][]UseAuthorization, error) {
	return loadApprovedUsesTx(ctx, tx, donationKeyIDs, nil, false)
}

func loadApprovedUsesTx(ctx context.Context, tx *sql.Tx, ids []int64, service *connectorcontract.NativeService, strict bool) (map[int64][]UseAuthorization, error) {
	if tx == nil {
		return nil, ErrInvariant
	}
	placeholders, args, err := boundedIDs(ids)
	if err != nil {
		return nil, err
	}
	result := make(map[int64][]UseAuthorization)
	if len(args) == 0 {
		return result, nil
	}
	filter := ""
	if service != nil {
		services, err := connectorcontract.ParseNativeServices([]connectorcontract.NativeService{*service})
		if err != nil {
			return nil, ErrUnavailable
		}
		filter = " AND u.native_service_bit=?"
		args = append(args, services.Mask())
	}
	query := "SELECT dk.id,u.id,u.revision,COALESCE(u.current_approved_revision_id,0),COALESCE(ur.id,0),COALESCE(ur.use_revision,0),u.native_service_bit,u.enabled,u.discovery_enabled,COALESCE(ur.discovery_source_revision_id,0),COALESCE(ur.review_state,''),COALESCE(d.user_id,0),COALESCE(dk.endpoint_key_id,0),EXISTS(SELECT 1 FROM donation_key_memberships m WHERE m.donation_key_id=dk.id AND m.donation_id=d.id AND m.endpoint_key_id=dk.endpoint_key_id),(dk.ended_at IS NULL AND d.status IN ('pending','approved') AND d.terminal_at IS NULL),EXISTS(SELECT 1 FROM donation_key_review_requirements rr WHERE rr.hmac=ur.key_body_review_hmac AND rr.required=1)," +
		grantReadColumns + " FROM donation_uses u JOIN donation_keys dk ON dk.id=u.donation_key_id JOIN donations d ON d.id=dk.donation_id LEFT JOIN donation_use_revisions ur ON ur.id=u.current_approved_revision_id AND ur.use_id=u.id LEFT JOIN endpoint_grant_authorizations a ON a.grant_revision_id=ur.grant_revision_id WHERE dk.id IN (" + placeholders + ")" + filter + " ORDER BY dk.id,u.native_service_bit"
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("connectionstore: read approved uses: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var use UseAuthorization
		var row grantRow
		var approvedRowID, serviceBit, ownerID, keyID int64
		var enabled, discoveryEnabled, membership, lifecycleLive, reviewRequired int
		var state string
		destinations := []any{&use.DonationKeyID, &use.UseID, &use.UseRevision, &use.ApprovedRevisionID, &approvedRowID, &use.ApprovedRevision, &serviceBit, &enabled, &discoveryEnabled, &use.DiscoverySourceRevisionID, &state, &ownerID, &keyID, &membership, &lifecycleLive, &reviewRequired}
		if err := rows.Scan(append(destinations, row.destinations()...)...); err != nil {
			return nil, err
		}
		services, err := connectorcontract.NativeServicesFromMask(serviceBit)
		if err != nil || len(services.Services()) != 1 || !validBool(enabled) || !validBool(discoveryEnabled) || !validBool(membership) || !validBool(lifecycleLive) || !validBool(reviewRequired) || use.UseID <= 0 || use.UseRevision <= 0 {
			return nil, ErrInvariant
		}
		use.NativeService = services.Services()[0]
		use.DiscoveryEnabled = discoveryEnabled == 1
		if use.ApprovedRevisionID == 0 {
			if strict {
				return nil, ErrUnavailable
			}
			continue
		}
		if use.ApprovedRevisionID != approvedRowID || use.ApprovedRevision <= 0 || state != "approved" || use.DiscoverySourceRevisionID < 0 {
			return nil, ErrInvariant
		}
		if enabled == 0 || membership == 0 || lifecycleLive == 0 || reviewRequired == 1 {
			if strict {
				return nil, ErrUnavailable
			}
			continue
		}
		grant, err := row.authorization()
		if errors.Is(err, ErrUnavailable) {
			if strict {
				return nil, err
			}
			continue
		}
		if err != nil {
			return nil, err
		}
		if ownerID <= 0 || keyID <= 0 || grant.OwnerUserID != ownerID || grant.EndpointKeyID != keyID {
			return nil, ErrInvariant
		}
		if !grant.EffectiveServices.Has(use.NativeService) {
			if strict {
				return nil, ErrUnavailable
			}
			continue
		}
		use.Grant = grant
		result[use.DonationKeyID] = append(result[use.DonationKeyID], use)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}
