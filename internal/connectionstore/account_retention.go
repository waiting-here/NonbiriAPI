package connectionstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// PrepareAccountDeletionTx runs after all domain deletion adapters and before
// deleting the user. Existing active claim references still protect versions.
func PrepareAccountDeletionTx(ctx context.Context, tx *sql.Tx, ownerUserID, now int64) error {
	if tx == nil || ownerUserID <= 0 || now < 0 || now > 253402300799 {
		return ErrInvariant
	}
	var remaining int
	if err := tx.QueryRowContext(ctx, `SELECT
 (SELECT count(*) FROM endpoints WHERE user_id=?)+
 (SELECT count(*) FROM donation_key_memberships m JOIN donations d ON d.id=m.donation_id WHERE d.user_id=?)`, ownerUserID, ownerUserID).Scan(&remaining); err != nil {
		return err
	}
	if remaining != 0 {
		return ErrInvariant
	}

	var sourceAfter int64
	for {
		rows, err := tx.QueryContext(ctx, "SELECT id FROM discovery_sources WHERE user_id=? AND id>? ORDER BY id LIMIT 100", ownerUserID, sourceAfter)
		if err != nil {
			return err
		}
		var ids []int64
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, id)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return err
		}
		if len(ids) == 0 {
			break
		}
		for _, id := range ids {
			if err := DeleteDiscoverySourceTx(ctx, tx, ownerUserID, id, now); err != nil {
				return err
			}
			sourceAfter = id
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE credentials SET current_version_id=NULL,connection_id=NULL,enabled=0,note='',deleted_at=max(?,created_at),updated_at=max(?,updated_at),revision=revision+1
 WHERE user_id=? AND deleted_at IS NULL`, now, now, ownerUserID); err != nil {
		return fmt.Errorf("connectionstore: end owner credentials: %w", err)
	}
	var after int64
	for {
		rows, err := tx.QueryContext(ctx, `SELECT v.id FROM credential_versions v JOIN credentials c ON c.id=v.credential_id
 WHERE c.user_id=? AND v.id>? AND v.secret_ref_id IS NOT NULL ORDER BY v.id LIMIT 100`, ownerUserID, after)
		if err != nil {
			return err
		}
		var ids []int64
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, id)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return err
		}
		if len(ids) == 0 {
			break
		}
		for _, id := range ids {
			if _, err := RetireUnreferencedVersionTx(ctx, tx, id, now); err != nil {
				return err
			}
			after = id
		}
	}
	if _, err := tx.ExecContext(ctx, "UPDATE credentials SET user_id=NULL WHERE user_id=?", ownerUserID); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, "DELETE FROM service_connections WHERE user_id=?", ownerUserID)
	return err
}
