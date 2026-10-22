package resources

import (
	"context"
	"database/sql"
	"math"

	"github.com/waiting-here/NonbiriAPI/internal/connectionstore"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
)

func (r *Repository) rotateCredentialAuthorizationsTx(ctx context.Context, tx *sql.Tx, userID, credentialID, versionID, secretID, now int64, writer SharedSecretWriter) (map[int64]bool, error) {
	retired := map[int64]bool{}
	rows, err := tx.QueryContext(ctx, `SELECT ds.id,ds.revision,sr.source_revision,sr.credential_version_id,sr.canonical_base_url,sr.list_format FROM discovery_sources ds JOIN discovery_source_revisions sr ON sr.id=ds.current_revision_id WHERE ds.user_id=? AND sr.credential_id=? ORDER BY ds.id`, userID, credentialID)
	if err != nil {
		return nil, err
	}
	type source struct {
		id, businessRevision, revision, version int64
		base, format                            string
	}
	sources := []source{}
	for rows.Next() {
		var item source
		if err := rows.Scan(&item.id, &item.businessRevision, &item.revision, &item.version, &item.base, &item.format); err != nil {
			rows.Close()
			return nil, err
		}
		sources = append(sources, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for _, item := range sources {
		if item.businessRevision == math.MaxInt64 || item.revision == math.MaxInt64 {
			return nil, ErrConflict
		}
		id, err := nextConnectionIdentityTx(ctx, tx, "discovery_source_revisions")
		if err != nil {
			return nil, err
		}
		if err := insertSourceRevisionTx(ctx, tx, id, item.id, userID, credentialID, versionID, item.revision+1, item.base, item.format, now); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE discovery_sources SET current_revision_id=?,revision=revision+1,updated_at=? WHERE id=?`, id, now, item.id); err != nil {
			return nil, err
		}
		retired[item.version] = true
	}
	rows, err = tx.QueryContext(ctx, `SELECT k.id,k.endpoint_id,k.revision,g.grant_revision,g.credential_version_id,g.native_service_mask,g.rerank_format,e.connector_type,e.base_url FROM endpoint_keys k JOIN endpoint_key_grant_revisions g ON g.id=k.current_grant_revision_id JOIN endpoints e ON e.id=k.endpoint_id WHERE e.user_id=? AND g.credential_id=? ORDER BY k.id`, userID, credentialID)
	if err != nil {
		return nil, err
	}
	type key struct {
		id, endpointID, businessRevision, grantRevision, version, mask int64
		format, connector, base                                        string
	}
	keys := []key{}
	for rows.Next() {
		var item key
		if err := rows.Scan(&item.id, &item.endpointID, &item.businessRevision, &item.grantRevision, &item.version, &item.mask, &item.format, &item.connector, &item.base); err != nil {
			rows.Close()
			return nil, err
		}
		keys = append(keys, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	keyIDs := make([]int64, 0, len(keys))
	for _, item := range keys {
		if item.businessRevision == math.MaxInt64 || item.grantRevision == math.MaxInt64 {
			return nil, ErrConflict
		}
		services, err := contract.NativeServicesFromMask(item.mask)
		if err != nil {
			return nil, ErrUnavailable
		}
		fingerprint, err := writer.FingerprintEndpointSecret(ctx, tx, secretID, item.connector, item.base)
		if err != nil {
			return nil, err
		}
		grantID, err := nextConnectionIdentityTx(ctx, tx, "endpoint_key_grant_revisions")
		if err != nil {
			return nil, err
		}
		if err := insertKeyGrantTx(ctx, tx, grantID, item.id, item.endpointID, userID, credentialID, versionID, item.grantRevision+1, services, contract.RerankFormat(item.format), fingerprint, now); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE endpoint_keys SET current_grant_revision_id=?,revision=revision+1,updated_at=? WHERE id=?`, grantID, now, item.id); err != nil {
			return nil, err
		}
		if err := stageGrantPendingUsesTx(ctx, tx, userID, item.id, grantID, connectionstore.UseOriginRotation, now); err != nil {
			return nil, err
		}
		if err := r.keyCreation.ProtectNewEndpointKey(ctx, tx, userID, item.id, now); err != nil {
			return nil, err
		}
		retired[item.version] = true
		keyIDs = append(keyIDs, item.id)
	}
	for _, item := range sources {
		if err := r.reconcileSourceKeysTx(ctx, tx, userID, item.id, now); err != nil {
			return nil, err
		}
	}
	models, err := bindingRevisionTargetsForKeysTx(ctx, tx, userID, keyIDs)
	if err != nil {
		return nil, err
	}
	if err := r.reconcileRoutingTargetsTx(ctx, tx, userID, models); err != nil {
		return nil, err
	}
	return retired, nil
}
