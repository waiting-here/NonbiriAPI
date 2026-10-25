package resources

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"sort"
	"strconv"

	"github.com/waiting-here/NonbiriAPI/internal/connectionstore"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

func authorizeEndpointProtocolTx(ctx context.Context, tx *sql.Tx, ownerID int64, endpoint Endpoint, selected *[]contract.NativeService, now int64) (Endpoint, error) {
	if selected == nil {
		return endpoint, nil
	}
	kind := contract.Type(endpoint.ConnectorType)
	full, err := contract.SupportedNativeServices(kind)
	value, selectionErr := contract.ParseNativeServices(*selected)
	if err != nil || selectionErr != nil || value.Mask() != full.Mask() {
		return endpoint, nil
	}
	format := endpoint.RerankFormat
	if kind.OpenAIServiceFamily() && format == "" {
		format = contract.RerankFormatGeneric
	}
	current, _ := contract.ParseNativeServices(endpoint.NativeServices)
	if current.Mask() != full.Mask() || endpoint.RerankFormat != format {
		id, _ := strconv.ParseInt(endpoint.ID, 10, 64)
		if _, err := tx.ExecContext(ctx, `UPDATE endpoints SET native_service_mask=?,rerank_format=?,capability_origin='user',revision=revision+1,updated_at=? WHERE id=? AND user_id=?`, full.Mask(), format, now, id, ownerID); err != nil {
			return Endpoint{}, err
		}
		endpoint.NativeServices, endpoint.RerankFormat = full.Services(), format
	}
	return endpoint, nil
}

// AuthorizeProtocolServicesTx creates a new current grant in an authorized owner
// transaction. Existing grants and approved donation snapshots remain immutable.
func AuthorizeProtocolServicesTx(ctx context.Context, tx *sql.Tx, ownerID, keyID, now int64, stageExistingUses bool) (int64, error) {
	var endpointID, endpointMask, grantID, credentialID, versionID, grantRevision, grantedMask, keyRevision int64
	var kind contract.Type
	var endpointFormat, grantFormat contract.RerankFormat
	var fingerprint []byte
	err := tx.QueryRowContext(ctx, `SELECT e.id,e.connector_type,e.native_service_mask,e.rerank_format,g.id,g.credential_id,g.credential_version_id,g.grant_revision,g.native_service_mask,g.rerank_format,g.report_fingerprint,k.revision
FROM endpoint_keys k JOIN endpoints e ON e.id=k.endpoint_id
JOIN endpoint_key_grant_revisions g ON g.id=k.current_grant_revision_id AND g.endpoint_key_id=k.id
WHERE k.id=? AND e.user_id=? AND g.owner_user_id=?`, keyID, ownerID, ownerID).
		Scan(&endpointID, &kind, &endpointMask, &endpointFormat, &grantID, &credentialID, &versionID, &grantRevision, &grantedMask, &grantFormat, &fingerprint, &keyRevision)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, err
	}
	defer clear(fingerprint)
	services, err := contract.SupportedNativeServices(kind)
	if err != nil || len(fingerprint) != 32 {
		return 0, ErrUnavailable
	}
	if kind.OpenAIServiceFamily() && endpointFormat == "" {
		endpointFormat = contract.RerankFormatGeneric
	}
	if contract.ValidateNativeServices(kind, services, endpointFormat) != nil {
		return 0, ErrInvalidRequest
	}
	if endpointMask != services.Mask() {
		if _, err := tx.ExecContext(ctx, `UPDATE endpoints SET native_service_mask=?,rerank_format=?,capability_origin='user',revision=revision+1,updated_at=? WHERE id=? AND user_id=?`, services.Mask(), endpointFormat, now, endpointID, ownerID); err != nil {
			return 0, err
		}
	}
	if grantedMask == services.Mask() && grantFormat == endpointFormat {
		return grantID, nil
	}
	if grantRevision == math.MaxInt64 || keyRevision == math.MaxInt64 {
		return 0, ErrConflict
	}
	newID, err := nextConnectionIdentityTx(ctx, tx, "endpoint_key_grant_revisions")
	if err != nil {
		return 0, err
	}
	var digest [32]byte
	copy(digest[:], fingerprint)
	defer clear(digest[:])
	if err := insertKeyGrantTx(ctx, tx, newID, keyID, endpointID, ownerID, credentialID, versionID, grantRevision+1, services, endpointFormat, digest, now); err != nil {
		return 0, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE endpoint_keys SET current_grant_revision_id=?,revision=revision+1,updated_at=? WHERE id=? AND endpoint_id=? AND current_grant_revision_id=? AND revision=?`, newID, now, keyID, endpointID, grantID, keyRevision)
	if err != nil {
		return 0, err
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		return 0, ErrConflict
	}
	if _, err := tx.ExecContext(ctx, `UPDATE endpoint_key_discovery_sources SET grant_revision_id=? WHERE endpoint_key_id=? AND grant_revision_id=?`, newID, keyID, grantID); err != nil {
		return 0, err
	}
	if stageExistingUses {
		if err := stageGrantPendingUsesTx(ctx, tx, ownerID, keyID, newID, connectionstore.UseOriginServiceExpansion, now); err != nil {
			return 0, err
		}
		if err := stageMissingProtocolUsesTx(ctx, tx, ownerID, keyID, newID, now); err != nil {
			return 0, err
		}
	}
	return newID, nil
}

func stageMissingProtocolUsesTx(ctx context.Context, tx *sql.Tx, ownerID, keyID, grantID, now int64) error {
	rows, err := tx.QueryContext(ctx, `SELECT dk.id,g.native_service_mask,ks.source_revision_id FROM donation_keys dk
JOIN donations d ON d.id=dk.donation_id
JOIN donation_key_memberships dm ON dm.donation_key_id=dk.id AND dm.endpoint_key_id=dk.endpoint_key_id AND dm.donation_id=d.id
JOIN endpoint_key_grant_revisions g ON g.id=? AND g.endpoint_key_id=dk.endpoint_key_id AND g.owner_user_id=d.user_id
LEFT JOIN endpoint_key_discovery_sources ks ON ks.endpoint_key_id=dk.endpoint_key_id AND ks.grant_revision_id=g.id
WHERE dk.endpoint_key_id=? AND d.user_id=? AND d.terminal_at IS NULL AND d.status IN ('pending','approved')
AND dk.ended_at IS NULL AND (dk.expires_at IS NULL OR dk.expires_at>?) ORDER BY dk.id`, grantID, keyID, ownerID, now)
	if err != nil {
		return err
	}
	type member struct {
		id       int64
		services contract.NativeServiceSet
		source   sql.NullInt64
	}
	var members []member
	for rows.Next() {
		var item member
		var mask int64
		if err := rows.Scan(&item.id, &mask, &item.source); err != nil {
			rows.Close()
			return err
		}
		item.services, err = contract.NativeServicesFromMask(mask)
		if err != nil {
			rows.Close()
			return err
		}
		members = append(members, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, item := range members {
		for _, service := range item.services.Services() {
			set, _ := contract.ParseNativeServices([]contract.NativeService{service})
			result, err := tx.ExecContext(ctx, `INSERT INTO donation_uses(donation_key_id,native_service_bit,enabled,discovery_enabled,revision,created_at,updated_at)
SELECT ?,?,COALESCE((SELECT MAX(enabled) FROM donation_uses WHERE donation_key_id=?),1),CASE WHEN ? THEN COALESCE((SELECT MAX(discovery_enabled) FROM donation_uses WHERE donation_key_id=?),1) ELSE 0 END,1,?,?
WHERE NOT EXISTS(SELECT 1 FROM donation_uses WHERE donation_key_id=? AND native_service_bit=?)`, item.id, set.Mask(), item.id, item.source.Valid, item.id, now, now, item.id, set.Mask())
			if err != nil {
				return err
			}
			count, err := result.RowsAffected()
			if err != nil {
				return err
			}
			if count == 0 {
				continue
			}
			id, err := result.LastInsertId()
			if err != nil {
				return err
			}
			var source *int64
			if item.source.Valid {
				source = &item.source.Int64
			}
			if _, err := connectionstore.StagePendingUseRevisionTx(ctx, tx, connectionstore.StagePendingUseRevisionInput{UseID: id, ExpectedRevision: 1, GrantRevisionID: grantID, DiscoverySourceRevisionID: source, Origin: connectionstore.UseOriginServiceExpansion, At: now}); err != nil {
				return mapConnectionStoreError(err)
			}
		}
	}
	return nil
}

func (r *Repository) notifyPendingSubmissionsTx(ctx context.Context, tx *sql.Tx, ownerID int64, grantIDs []int64) error {
	if r.pendingSubmission == nil {
		return nil
	}
	ids := map[int64]bool{}
	for _, grantID := range grantIDs {
		rows, err := tx.QueryContext(ctx, `SELECT DISTINCT d.id FROM donation_use_revisions ur
JOIN donation_uses u ON u.current_pending_revision_id=ur.id AND u.id=ur.use_id
JOIN donation_keys dk ON dk.id=u.donation_key_id JOIN donations d ON d.id=dk.donation_id
WHERE ur.grant_revision_id=? AND d.user_id=? AND ur.review_state='pending' ORDER BY d.id`, grantID, ownerID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			ids[id] = true
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		if err := rows.Close(); err != nil {
			return err
		}
	}
	ordered := make([]int64, 0, len(ids))
	for id := range ids {
		ordered = append(ordered, id)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	for _, id := range ordered {
		if err := r.pendingSubmission(ctx, tx, id); err != nil {
			return err
		}
	}
	return nil
}
