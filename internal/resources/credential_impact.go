package resources

import (
	"context"
	"database/sql"
	"strconv"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

const credentialImpactCTE = `WITH selected_credential AS (
 SELECT id FROM credentials WHERE id=? AND user_id=? AND deleted_at IS NULL
), current_uses AS (
 SELECT ur.id,g.credential_version_id,ur.discovery_source_revision_id,ur.review_state,
 u.id AS use_id,u.native_service_bit,dk.id AS donation_key_id,dk.donation_id,dk.endpoint_key_id,g.endpoint_id
 FROM selected_credential selected JOIN endpoint_key_grant_revisions g ON g.credential_id=selected.id
 JOIN donation_use_revisions ur ON ur.grant_revision_id=g.id
 JOIN donation_uses u ON u.id=ur.use_id AND (u.current_approved_revision_id=ur.id OR u.current_pending_revision_id=ur.id)
 JOIN donation_keys dk ON dk.id=u.donation_key_id JOIN donations d ON d.id=dk.donation_id
 JOIN donation_key_memberships dm ON dm.donation_key_id=dk.id AND dm.donation_id=d.id AND dm.endpoint_key_id=dk.endpoint_key_id
 WHERE dk.ended_at IS NULL AND d.terminal_at IS NULL AND d.status IN ('pending','approved')
), source_versions AS (
 SELECT sr.id,sr.source_id,sr.credential_version_id,3 AS priority FROM selected_credential selected
 JOIN discovery_source_revisions sr ON sr.credential_id=selected.id JOIN discovery_sources ds ON ds.current_revision_id=sr.id AND ds.id=sr.source_id
 UNION ALL SELECT sr.id,sr.source_id,sr.credential_version_id,CASE cu.review_state WHEN 'approved' THEN 2 ELSE 1 END
 FROM current_uses cu JOIN discovery_source_revisions sr ON sr.id=cu.discovery_source_revision_id
), affected_sources AS (
 SELECT id,source_id,credential_version_id,max(priority) AS priority FROM source_versions GROUP BY id
), current_keys AS (
 SELECT g.* FROM selected_credential selected JOIN endpoint_key_grant_revisions g ON g.credential_id=selected.id
 JOIN endpoint_keys k ON k.current_grant_revision_id=g.id AND k.id=g.endpoint_key_id
) `

func credentialImpactSummaryTx(ctx context.Context, tx *sql.Tx, userID, credentialID int64) (CredentialImpactSummary, error) {
	var keys, donations, sources int64
	err := tx.QueryRowContext(ctx, credentialImpactCTE+`SELECT (SELECT count(*) FROM current_keys),(SELECT count(DISTINCT donation_key_id) FROM current_uses),(SELECT count(DISTINCT source_id) FROM affected_sources)`, credentialID, userID).Scan(&keys, &donations, &sources)
	return CredentialImpactSummary{EndpointKeys: strconv.FormatInt(keys, 10), DonationKeys: strconv.FormatInt(donations, 10), DiscoverySources: strconv.FormatInt(sources, 10)}, err
}

type credentialImpactRow struct {
	CredentialImpactItem
	sortID string
}

func scanCredentialImpact(scanner pageScanner) (credentialImpactRow, error) {
	var item credentialImpactRow
	var id, version, mask int64
	var endpointID, keyID, donationID, donationKeyID, useID, sourceID sql.NullInt64
	err := scanner.Scan(&id, &endpointID, &keyID, &donationID, &donationKeyID, &useID, &sourceID, &version, &mask, &item.State, &item.EndpointNote, &item.EndpointBaseURL)
	if err != nil {
		return item, err
	}
	if id <= 0 || version <= 0 || !optionalChoice(item.State, "current", "approved", "pending") {
		return item, ErrUnavailable
	}
	for _, field := range []struct {
		target **string
		value  sql.NullInt64
	}{{&item.EndpointID, endpointID}, {&item.EndpointKeyID, keyID}, {&item.DonationID, donationID}, {&item.DonationKeyID, donationKeyID}, {&item.UseID, useID}, {&item.DiscoverySourceID, sourceID}} {
		*field.target, err = optionalDecimal(field.value)
		if err != nil {
			return item, err
		}
	}
	services, err := contract.NativeServicesFromMask(mask)
	if err != nil {
		return item, ErrUnavailable
	}
	item.NativeServices = services.Services()
	item.CredentialVersion = strconv.FormatInt(version, 10)
	item.sortID = strconv.FormatInt(id, 10)
	return item, nil
}

func (r *Repository) listCredentialImpact(ctx context.Context, userID, credentialID int64, kind string, window connectionListWindow) (CredentialImpact, error) {
	if credentialID <= 0 || kind == "" || !optionalChoice(kind, "personal", "charity", "discovery") {
		return CredentialImpact{}, ErrInvalidRequest
	}
	return userPageRead(ctx, r, userID, pagination.Default(), func(ctx context.Context, tx *sql.Tx) (CredentialImpact, error) {
		if _, err := getCredentialTx(ctx, tx, userID, credentialID); err != nil {
			return CredentialImpact{}, err
		}
		summary, err := credentialImpactSummaryTx(ctx, tx, userID, credentialID)
		if err != nil {
			return CredentialImpact{}, err
		}
		var selectSQL string
		switch kind {
		case "personal":
			selectSQL = `SELECT g.endpoint_key_id AS id,g.endpoint_id,g.endpoint_key_id,NULL,NULL,NULL,ks.source_id,v.version,g.native_service_mask,'current' FROM current_keys g JOIN credential_versions v ON v.id=g.credential_version_id LEFT JOIN endpoint_key_discovery_sources ks ON ks.endpoint_key_id=g.endpoint_key_id`
		case "charity":
			selectSQL = `SELECT cu.id,cu.endpoint_id,cu.endpoint_key_id,cu.donation_id,cu.donation_key_id,cu.use_id,sr.source_id,v.version,cu.native_service_bit,cu.review_state FROM current_uses cu JOIN credential_versions v ON v.id=cu.credential_version_id LEFT JOIN discovery_source_revisions sr ON sr.id=cu.discovery_source_revision_id`
		case "discovery":
			selectSQL = `SELECT sr.id,NULL AS endpoint_id,NULL,NULL,NULL,NULL,sr.source_id,v.version,0,CASE sr.priority WHEN 3 THEN 'current' WHEN 2 THEN 'approved' ELSE 'pending' END FROM affected_sources sr JOIN credential_versions v ON v.id=sr.credential_version_id`
		}
		page, err := readConnectionList(ctx, tx, r, window, "credential-impact-"+kind, credentialImpactCTE+`SELECT impact.*,COALESCE(e.note,''),COALESCE(e.base_url,'') FROM (`+selectSQL+`) impact LEFT JOIN endpoints e ON e.id=impact.endpoint_id WHERE 1=1`, "impact.id", []any{credentialID, userID}, scanCredentialImpact, func(item credentialImpactRow) string { return item.sortID })
		if err != nil {
			return CredentialImpact{}, err
		}
		result := CredentialImpact{Kind: kind, Summary: summary, Page: Page[CredentialImpactItem]{Data: make([]CredentialImpactItem, len(page.Data)), NextCursor: page.NextCursor, Pagination: page.Pagination}}
		for i, item := range page.Data {
			result.Data[i] = item.CredentialImpactItem
		}
		return result, nil
	})
}
