package resources

import (
	"context"
	"database/sql"

	"github.com/waiting-here/NonbiriAPI/internal/connectionstore"
)

func (r *Repository) stageKeyPendingUsesTx(ctx context.Context, tx *sql.Tx, userID, keyID, grantID int64, reattached bool, now int64) error {
	origin := connectionstore.UseOriginServiceExpansion
	if reattached {
		origin = connectionstore.UseOriginReattachment
	}
	return stageGrantPendingUsesTx(ctx, tx, userID, keyID, grantID, origin, now)
}

func stageGrantPendingUsesTx(ctx context.Context, tx *sql.Tx, userID, keyID, grantID int64, origin connectionstore.UseRevisionOrigin, now int64) error {
	rows, err := tx.QueryContext(ctx, `SELECT u.id,u.revision,
 (SELECT ds.current_revision_id FROM discovery_source_revisions old_source
  JOIN discovery_sources ds ON ds.id=old_source.source_id
  JOIN discovery_source_revisions sr ON sr.id=ds.current_revision_id
  WHERE old_source.id=ur.discovery_source_revision_id AND sr.owner_user_id=? AND sr.credential_version_id=g.credential_version_id)
 FROM donation_uses u JOIN donation_keys dk ON dk.id=u.donation_key_id
 JOIN donations d ON d.id=dk.donation_id
 JOIN donation_key_memberships dm ON dm.donation_key_id=dk.id AND dm.endpoint_key_id=dk.endpoint_key_id AND dm.donation_id=d.id
 JOIN endpoint_key_grant_revisions g ON g.id=? AND g.endpoint_key_id=dk.endpoint_key_id
 LEFT JOIN donation_use_revisions ur ON ur.id=COALESCE(u.current_pending_revision_id,u.current_approved_revision_id) AND ur.use_id=u.id
 WHERE dk.endpoint_key_id=? AND d.user_id=? AND d.terminal_at IS NULL AND d.status IN ('pending','approved') AND dk.ended_at IS NULL
 AND (g.native_service_mask&u.native_service_bit)=u.native_service_bit
 AND (u.current_pending_revision_id IS NOT NULL OR u.current_approved_revision_id IS NOT NULL)
 ORDER BY u.id`, userID, grantID, keyID, userID)
	if err != nil {
		return err
	}
	type pending struct {
		id, revision int64
		source       sql.NullInt64
	}
	items := []pending{}
	for rows.Next() {
		var item pending
		if err := rows.Scan(&item.id, &item.revision, &item.source); err != nil {
			rows.Close()
			return err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, item := range items {
		var source *int64
		if item.source.Valid {
			id := item.source.Int64
			source = &id
		}
		if _, err := connectionstore.StagePendingUseRevisionTx(ctx, tx, connectionstore.StagePendingUseRevisionInput{UseID: item.id, ExpectedRevision: item.revision, GrantRevisionID: grantID, DiscoverySourceRevisionID: source, Origin: origin, At: now}); err != nil {
			return mapConnectionStoreError(err)
		}
	}
	return nil
}
