package fatfish

import (
	"context"
	"database/sql"
	"encoding/json"
)

func cleanupIDs(ids []string) string { encoded, _ := json.Marshal(ids); return string(encoded) }
func legacyCleanupRoots(ctx context.Context, tx *sql.Tx, kind string) ([]string, error) {
	query := `SELECT id,json_extract(draft_json,'$.engine_version') FROM fatfish_levels ORDER BY id`
	if kind == "periods" {
		query = `SELECT p.id,1 FROM fatfish_periods p WHERE NOT EXISTS(
        SELECT 1 FROM fatfish_nodes n JOIN fatfish_node_revisions r ON r.node_id=n.id
        JOIN fatfish_level_versions v ON v.id=r.version_id WHERE n.period_id=p.id AND v.engine_version=3) ORDER BY p.id`
	}
	rows, err := tx.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		var version int
		if err = rows.Scan(&id, &version); err != nil {
			return nil, err
		}
		if version == 1 || version == 2 {
			ids = append(ids, id)
		} else if version != 3 {
			return nil, ErrInvariant
		}
	}
	return ids, rows.Err()
}
func cleanupTableExists(ctx context.Context, tx *sql.Tx, table string) (bool, error) {
	var count int
	err := tx.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_schema WHERE type='table' AND name=?`, table).Scan(&count)
	return count == 1, err
}
func readCleanupRows(ctx context.Context, tx *sql.Tx, levels, periods []string) ([]CleanupRow, error) {
	levelJSON, periodJSON := cleanupIDs(levels), cleanupIDs(periods)
	versionSelection := `SELECT id FROM fatfish_level_versions WHERE level_id IN (SELECT value FROM json_each(?))`
	nodeSelection := `SELECT id FROM fatfish_nodes WHERE period_id IN (SELECT value FROM json_each(?))`
	rows := []CleanupRow{}
	appendQuery := func(query string, args ...any) error {
		result, err := tx.QueryContext(ctx, query, args...)
		if err != nil {
			return err
		}
		defer result.Close()
		for result.Next() {
			var row CleanupRow
			if err = result.Scan(&row.Kind, &row.ID, &row.ParentID, &row.UserID, &row.Revision, &row.EngineVersion, &row.ContentHash, &row.State); err != nil {
				return err
			}
			rows = append(rows, row)
			if len(rows) > 1_100_000 {
				return ErrCapacity
			}
		}
		return result.Err()
	}
	queries := []struct {
		query string
		args  []any
	}{
		{`SELECT 'levels',id,'',0,revision,0,'','' FROM fatfish_levels WHERE id IN (SELECT value FROM json_each(?))`, []any{levelJSON}},
		{`SELECT 'versions',id,level_id,0,0,engine_version,lower(hex(content_hash)),'' FROM fatfish_level_versions WHERE level_id IN (SELECT value FROM json_each(?))`, []any{levelJSON}},
		{`SELECT 'periods',id,'',0,revision,0,'',state FROM fatfish_periods WHERE id IN (SELECT value FROM json_each(?))`, []any{periodJSON}},
		{`SELECT 'nodes',id,period_id,0,current_revision,0,'','' FROM fatfish_nodes WHERE period_id IN (SELECT value FROM json_each(?))`, []any{periodJSON}},
		{`SELECT 'node_revisions',node_id||':'||revision,version_id,0,revision,0,'','' FROM fatfish_node_revisions WHERE node_id IN (` + nodeSelection + `)`, []any{periodJSON}},
		{`SELECT 'challenges',id,version_id,user_id,revision,0,'',state FROM fatfish_challenges WHERE version_id IN (` + versionSelection + `) OR period_id IN (SELECT value FROM json_each(?))`, []any{levelJSON, periodJSON}},
		{`SELECT 'playtests',id,version_id,0,0,0,'','' FROM fatfish_playtests WHERE version_id IN (` + versionSelection + `)`, []any{levelJSON}},
		{`SELECT 'progress',user_id||':'||period_id||':'||node_id,COALESCE(best_version_id,''),user_id,best_score_units,0,'','' FROM fatfish_progress WHERE period_id IN (SELECT value FROM json_each(?)) OR best_version_id IN (` + versionSelection + `)`, []any{periodJSON, levelJSON}},
		{`SELECT 'period_progress',user_id||':'||period_id,period_id,user_id,total_score_units,0,'','' FROM fatfish_period_progress WHERE period_id IN (SELECT value FROM json_each(?))`, []any{periodJSON}},
	}
	for _, query := range queries {
		if err := appendQuery(query.query, query.args...); err != nil {
			return nil, err
		}
	}
	for _, optional := range []struct {
		table, kind, column, ids string
		revision                 string
	}{
		{"fatfish_deleted_levels", "deleted_levels", "level_id", levelJSON, "0"},
		{"fatfish_graph_layouts", "graph_layouts", "period_id", periodJSON, "revision"},
	} {
		exists, err := cleanupTableExists(ctx, tx, optional.table)
		if err != nil {
			return nil, err
		}
		if exists {
			query := "SELECT '" + optional.kind + "'," + optional.column + ",'',0," + optional.revision + ",0,'','' FROM " + optional.table + " WHERE " + optional.column + " IN (SELECT value FROM json_each(?))"
			if err = appendQuery(query, optional.ids); err != nil {
				return nil, err
			}
		}
	}
	versionIDs := map[string]bool{}
	entityIDs := []string{}
	for _, row := range rows {
		switch row.Kind {
		case "versions":
			if row.EngineVersion != 1 && row.EngineVersion != 2 {
				return nil, ErrConflict
			}
			versionIDs[row.ID] = true
			entityIDs = append(entityIDs, row.ID)
		case "periods":
			entityIDs = append(entityIDs, row.ID)
		case "nodes":
			entityIDs = append(entityIDs, row.ID)
		case "levels", "challenges", "playtests":
			entityIDs = append(entityIDs, row.ID)
		}
	}
	for _, row := range rows {
		if (row.Kind == "node_revisions" || row.Kind == "challenges" || row.Kind == "playtests") && !versionIDs[row.ParentID] {
			return nil, ErrConflict
		}
	}
	// These completed responses have an exact Fat Fish root object ID.
	// Shared control receipts without an object reference keep their normal expiry.
	if err := appendQuery(`SELECT 'mutation_receipts',lower(hex(actor_scope_hash))||':'||lower(hex(key_hash)),'',0,created_at,0,lower(hex(request_hash)),''
        FROM idempotency_records WHERE scope='control_mutation' AND state='completed'
        AND CASE WHEN json_valid(CAST(response_body AS TEXT)) THEN json_extract(CAST(response_body AS TEXT),'$.id') END
        IN (SELECT value FROM json_each(?))`, cleanupIDs(entityIDs)); err != nil {
		return nil, err
	}
	sortCleanupRows(rows)
	return rows, nil
}
func cleanupComposite(id string, parts int) ([]string, error) {
	// The schema's opaque IDs never contain a colon.
	result := []string{}
	start := 0
	for i := range id {
		if id[i] == ':' {
			result = append(result, id[start:i])
			start = i + 1
		}
	}
	result = append(result, id[start:])
	if len(result) != parts {
		return nil, ErrInvalid
	}
	return result, nil
}
