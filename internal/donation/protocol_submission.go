package donation

import (
	"context"
	"database/sql"

	"github.com/waiting-here/NonbiriAPI/internal/connectionstore"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
)

func (s *Service) resubmitDonationProtocolTx(ctx context.Context, tx *sql.Tx, ownerID, donationID, now int64) (bool, error) {
	rows, err := tx.QueryContext(ctx, `SELECT dk.id,dk.endpoint_key_id FROM donation_keys dk
JOIN donations d ON d.id=dk.donation_id JOIN donation_key_memberships m ON m.donation_key_id=dk.id AND m.endpoint_key_id=dk.endpoint_key_id
WHERE d.id=? AND d.user_id=? AND dk.ended_at IS NULL AND (dk.expires_at IS NULL OR dk.expires_at>?) ORDER BY dk.id`, donationID, ownerID, now)
	if err != nil {
		return false, err
	}
	type key struct{ id, endpointKey int64 }
	keys := []key{}
	for rows.Next() {
		var value key
		if err := rows.Scan(&value.id, &value.endpointKey); err != nil {
			rows.Close()
			return false, err
		}
		keys = append(keys, value)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return false, err
	}
	rows.Close()
	var submitted []int64
	for _, value := range keys {
		grantID, err := resources.AuthorizeProtocolServicesTx(ctx, tx, ownerID, value.endpointKey, now, false)
		if err != nil {
			return false, protocolAuthorizationError(err)
		}
		var kind contract.Type
		var source sql.NullInt64
		if err := tx.QueryRowContext(ctx, `SELECT e.connector_type,ks.source_revision_id FROM endpoint_keys k JOIN endpoints e ON e.id=k.endpoint_id LEFT JOIN endpoint_key_discovery_sources ks ON ks.endpoint_key_id=k.id WHERE k.id=? AND e.user_id=?`, value.endpointKey, ownerID).Scan(&kind, &source); err != nil {
			return false, err
		}
		services, err := contract.SupportedNativeServices(kind)
		if err != nil {
			return false, ErrInvariant
		}
		var sourceID *int64
		if source.Valid {
			sourceID = &source.Int64
		}
		for _, service := range services.Services() {
			useID, err := resubmitProtocolUseTx(ctx, tx, value.id, service, grantID, sourceID, now)
			if err != nil {
				return false, err
			}
			submitted = append(submitted, useID)
		}
	}
	approved, err := s.autoApproveSubmittedUsesTx(ctx, tx, donationID, submitted, now)
	return len(keys) > 0 && !approved, err
}

func resubmitProtocolUseTx(ctx context.Context, tx *sql.Tx, keyID int64, service contract.NativeService, grantID int64, sourceID *int64, now int64) (int64, error) {
	set, _ := contract.ParseNativeServices([]contract.NativeService{service})
	var id, revision int64
	err := tx.QueryRowContext(ctx, `SELECT id,revision FROM donation_uses WHERE donation_key_id=? AND native_service_bit=?`, keyID, set.Mask()).Scan(&id, &revision)
	if err == sql.ErrNoRows {
		result, err := tx.ExecContext(ctx, `INSERT INTO donation_uses(donation_key_id,native_service_bit,enabled,discovery_enabled,revision,created_at,updated_at)
VALUES(?,?,COALESCE((SELECT MAX(enabled) FROM donation_uses WHERE donation_key_id=?),1),CASE WHEN ? THEN COALESCE((SELECT MAX(discovery_enabled) FROM donation_uses WHERE donation_key_id=?),1) ELSE 0 END,1,?,?)`, keyID, set.Mask(), keyID, sourceID != nil, keyID, now, now)
		if err != nil {
			return 0, err
		}
		id, err = result.LastInsertId()
		if err != nil {
			return 0, err
		}
		revision = 1
	} else if err != nil {
		return 0, err
	}
	_, err = connectionstore.StagePendingUseRevisionTx(ctx, tx, connectionstore.StagePendingUseRevisionInput{
		UseID: id, ExpectedRevision: revision, GrantRevisionID: grantID, DiscoverySourceRevisionID: sourceID,
		Origin: connectionstore.UseOriginServiceExpansion, At: now,
	})
	return id, connectionError(err)
}
