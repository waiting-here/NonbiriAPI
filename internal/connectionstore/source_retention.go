package connectionstore

import (
	"context"
	"database/sql"
	"errors"
)

// DeleteDiscoverySourceTx removes only this owner's source and its cached
// results. Active claims retain their separate exact version references.
func DeleteDiscoverySourceTx(ctx context.Context, tx *sql.Tx, ownerUserID, sourceID, now int64) error {
	if tx == nil || ownerUserID <= 0 || sourceID <= 0 || now < 0 || now > 253402300799 {
		return ErrInvariant
	}
	var owner int64
	err := tx.QueryRowContext(ctx, "SELECT user_id FROM discovery_sources WHERE id=?", sourceID).Scan(&owner)
	if errors.Is(err, sql.ErrNoRows) || err == nil && owner != ownerUserID {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `CREATE TEMP TABLE connection_retiring_source_versions(id INTEGER PRIMARY KEY);
 INSERT INTO temp.connection_retiring_source_versions(id) SELECT DISTINCT credential_version_id FROM discovery_source_revisions WHERE source_id=?`, sourceID); err != nil {
		return err
	}
	if err := interruptDiscoveryRunsTx(ctx, tx, "source_revision_id IN(SELECT id FROM discovery_source_revisions WHERE source_id=?)", sourceID, now); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM discovery_runs WHERE source_revision_id IN(SELECT id FROM discovery_source_revisions WHERE source_id=?)", sourceID); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, "DELETE FROM discovery_sources WHERE id=? AND user_id=?", sourceID, ownerUserID)
	if err != nil {
		return err
	}
	if err := requireOne(result); err != nil {
		return err
	}
	var after int64
	for {
		rows, err := tx.QueryContext(ctx, "SELECT id FROM temp.connection_retiring_source_versions WHERE id>? ORDER BY id LIMIT 100", after)
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
	_, err = tx.ExecContext(ctx, "DROP TABLE temp.connection_retiring_source_versions")
	return err
}
