package v1

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
)

type legacyDiscoveryRow struct {
	keyID, revision, count       int64
	state, safeClass, diagnostic string
	operationHash                []byte
	started, completed           sql.NullInt64
}

// Published catalogs retain their successful generation even when the latest
// refresh failed. No second copy is made for another key or donation use.
func populateConnectionDiscovery(ctx context.Context, tx *sql.Tx, now int64) error {
	if err := mapLegacyDiscoveryOperations(ctx, tx); err != nil {
		return err
	}
	statements := []string{
		`INSERT INTO discovery_runs(source_revision_id,source_revision_snapshot,credential_version_id,result_generation,authorization_kind,state,safe_class,started_at,completed_at,model_count)
 SELECT c.endpoint_key_id,1,c.endpoint_key_id,c.source_revision,'legacy','succeeded','none',min(c.created_at),max(c.updated_at),count(*)
 FROM model_catalog_entries c WHERE c.source_type='automatic'
 GROUP BY c.endpoint_key_id,c.source_revision ORDER BY c.endpoint_key_id,c.source_revision`,
		`INSERT INTO discovery_source_states(source_revision_id,result_generation,current_run_id,last_successful_run_id,updated_at)
 SELECT k.id,max(COALESCE(d.revision,1),COALESCE((SELECT max(r.result_generation) FROM discovery_runs r WHERE r.source_revision_id=k.id),0)),NULL,
 (SELECT r.id FROM discovery_runs r WHERE r.source_revision_id=k.id AND r.state='succeeded' ORDER BY r.result_generation DESC LIMIT 1),k.updated_at
 FROM temp.connection_old_keys k LEFT JOIN temp.connection_old_discovery d ON d.endpoint_key_id=k.id ORDER BY k.id`,
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("connection discovery baseline: %w", err)
		}
	}
	var after int64
	for {
		rows, err := tx.QueryContext(ctx, `SELECT endpoint_key_id,state,revision,operation_hash,safe_class,safe_diag,started_at,completed_at,fetched_count
 FROM temp.connection_old_discovery WHERE endpoint_key_id>? ORDER BY endpoint_key_id LIMIT 100`, after)
		if err != nil {
			return err
		}
		var batch []legacyDiscoveryRow
		for rows.Next() {
			var row legacyDiscoveryRow
			if err := rows.Scan(&row.keyID, &row.state, &row.revision, &row.operationHash, &row.safeClass, &row.diagnostic, &row.started, &row.completed, &row.count); err != nil {
				rows.Close()
				return err
			}
			batch = append(batch, row)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return err
		}
		if len(batch) == 0 {
			break
		}
		for _, row := range batch {
			if err := mapLegacyDiscoveryRow(ctx, tx, row, now); err != nil {
				return err
			}
			after = row.keyID
		}
	}
	if _, err := tx.ExecContext(ctx, `CREATE TEMP TABLE connection_run_map AS
 SELECT source_revision_id AS endpoint_key_id,result_generation,id AS run_id FROM discovery_runs;
 CREATE UNIQUE INDEX temp.idx_connection_run_map ON connection_run_map(endpoint_key_id,result_generation);
 DROP TABLE temp.connection_legacy_operations`); err != nil {
		return err
	}
	return nil
}

func mapLegacyDiscoveryOperations(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, `CREATE TEMP TABLE connection_legacy_operations(id TEXT PRIMARY KEY,checkpoint TEXT NOT NULL,operation_hash BLOB NOT NULL);
 CREATE INDEX temp.idx_connection_legacy_operations ON connection_legacy_operations(checkpoint,operation_hash)`); err != nil {
		return err
	}
	after := ""
	for {
		rows, err := tx.QueryContext(ctx, "SELECT id,checkpoint FROM accepted_operations WHERE kind='model_discovery' AND state IN ('accepted','running') AND id>? ORDER BY id LIMIT 100", after)
		if err != nil {
			return err
		}
		type operation struct{ id, checkpoint string }
		var batch []operation
		for rows.Next() {
			var row operation
			if err := rows.Scan(&row.id, &row.checkpoint); err != nil {
				rows.Close()
				return err
			}
			batch = append(batch, row)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return err
		}
		if len(batch) == 0 {
			return nil
		}
		for _, row := range batch {
			digest := sha256.Sum256([]byte(row.id))
			if _, err := tx.ExecContext(ctx, "INSERT INTO temp.connection_legacy_operations(id,checkpoint,operation_hash) VALUES(?,?,?)", row.id, row.checkpoint, digest[:]); err != nil {
				return err
			}
			after = row.id
		}
	}
}

func mapLegacyDiscoveryRow(ctx context.Context, tx *sql.Tx, row legacyDiscoveryRow, now int64) error {
	if row.state == "unknown" {
		return nil
	}
	var highest int64
	if err := tx.QueryRowContext(ctx, "SELECT COALESCE(max(result_generation),0) FROM discovery_runs WHERE source_revision_id=?", row.keyID).Scan(&highest); err != nil {
		return err
	}
	if highest > row.revision {
		return errors.New("legacy discovery generation precedes its retained catalog")
	}
	started := row.completed.Int64
	if row.started.Valid {
		started = row.started.Int64
	}
	var completed any
	if row.completed.Valid {
		completed = row.completed.Int64
	}
	var operationID, operationHash any
	if row.state == "checking" {
		var id string
		checkpoint := strconv.FormatInt(row.keyID, 10) + ":" + strconv.FormatInt(row.revision, 10)
		err := tx.QueryRowContext(ctx, "SELECT id FROM temp.connection_legacy_operations WHERE checkpoint=? AND operation_hash=?", checkpoint, row.operationHash).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			// A checking row without its accepted operation cannot authorize a new
			// outbound call. Preserve the cache and expose interruption for recovery.
			row.state = "failed"
			row.safeClass = "interrupted"
			row.diagnostic = ""
			row.count = 0
			completed = max(now, started)
		} else if err != nil {
			return err
		} else {
			operationID = id
			operationHash = row.operationHash
		}
	}
	var runID int64
	err := tx.QueryRowContext(ctx, "SELECT id FROM discovery_runs WHERE source_revision_id=? AND result_generation=?", row.keyID, row.revision).Scan(&runID)
	switch {
	case err == nil:
		if row.state != "succeeded" {
			return errors.New("legacy refresh conflicts with a successful catalog generation")
		}
		if _, err := tx.ExecContext(ctx, "UPDATE discovery_runs SET started_at=?,completed_at=?,model_count=?,safe_diagnostic=? WHERE id=?", started, completed, row.count, row.diagnostic, runID); err != nil {
			return err
		}
	case errors.Is(err, sql.ErrNoRows):
		result, err := tx.ExecContext(ctx, `INSERT INTO discovery_runs(source_revision_id,source_revision_snapshot,credential_version_id,operation_id,operation_hash,result_generation,authorization_kind,state,safe_class,safe_diagnostic,started_at,completed_at,model_count)
 VALUES(?,1,?,?,?,?,'legacy',?,?,?,?,?,?)`, row.keyID, row.keyID, operationID, operationHash, row.revision, row.state, row.safeClass, row.diagnostic, started, completed, row.count)
		if err != nil {
			return err
		}
		runID, err = result.LastInsertId()
		if err != nil {
			return err
		}
	default:
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE discovery_source_states SET result_generation=?,current_run_id=?,
 last_successful_run_id=CASE WHEN ?='succeeded' THEN ? ELSE last_successful_run_id END,
 updated_at=max(updated_at,?)
 WHERE source_revision_id=?`, row.revision, runID, row.state, runID, max(started, row.completed.Int64), row.keyID)
	return err
}
