package resources

import (
	"context"
	"database/sql"
	"errors"

	"github.com/waiting-here/NonbiriAPI/internal/connectionstore"
)

func mapConnectionStoreError(err error) error {
	switch {
	case errors.Is(err, connectionstore.ErrNotFound):
		return ErrNotFound
	case errors.Is(err, connectionstore.ErrConflict):
		return ErrConflict
	case errors.Is(err, connectionstore.ErrUnavailable), errors.Is(err, connectionstore.ErrInvariant):
		return ErrUnavailable
	default:
		return err
	}
}

func (r *Repository) rebuildKeyCatalogProjectionTx(ctx context.Context, tx *sql.Tx, userID, keyID, now int64) error {
	var runID sql.NullInt64
	var generation int64
	err := tx.QueryRowContext(ctx, `SELECT st.last_successful_run_id,COALESCE(r.result_generation,1)
 FROM endpoint_keys k JOIN endpoints e ON e.id=k.endpoint_id
 LEFT JOIN endpoint_key_discovery_sources ks ON ks.endpoint_key_id=k.id
 LEFT JOIN discovery_sources ds ON ds.id=ks.source_id
 LEFT JOIN discovery_source_states st ON st.source_revision_id=ds.current_revision_id
 LEFT JOIN discovery_runs r ON r.id=st.last_successful_run_id
 WHERE k.id=? AND e.user_id=?`, keyID, userID).Scan(&runID, &generation)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE model_pair_catalog SET automatic_supports=0,automatic_revision=?,updated_at=? WHERE endpoint_key_id=?`, generation, now, keyID); err != nil {
		return err
	}
	if runID.Valid {
		if _, err := tx.ExecContext(ctx, `INSERT INTO model_pair_catalog(endpoint_key_id,normalized_model_id,automatic_supports,manual_supports,automatic_revision,pair_revision,updated_at)
 SELECT ?,normalized_model_id,count(*),0,?,1,? FROM model_catalog_entries WHERE discovery_run_id=? AND source_type='automatic' GROUP BY normalized_model_id
 ON CONFLICT(endpoint_key_id,normalized_model_id) DO UPDATE SET automatic_supports=excluded.automatic_supports,automatic_revision=excluded.automatic_revision,updated_at=excluded.updated_at`, keyID, generation, now, runID.Int64); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM model_pair_catalog WHERE endpoint_key_id=? AND automatic_supports=0 AND manual_supports=0
 AND NOT EXISTS(SELECT 1 FROM model_binding_targets t WHERE t.endpoint_key_id=model_pair_catalog.endpoint_key_id AND t.upstream_model_id=model_pair_catalog.normalized_model_id)
 AND NOT EXISTS(SELECT 1 FROM charity_binding_targets t JOIN donation_keys dk ON dk.id=t.donation_key_id WHERE dk.endpoint_key_id=model_pair_catalog.endpoint_key_id AND t.upstream_model_id=model_pair_catalog.normalized_model_id)`, keyID)
	return err
}
