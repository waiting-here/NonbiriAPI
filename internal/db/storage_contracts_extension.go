package db

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

const preStorageContractsManifestHash = "3f773b6dca01058f2296f437c3666afde92a74e8eeb861fa8637756dcd859481"
const storageContractsMarker = "\n-- Additive storage contracts"

var storageContractTableChanges = []struct {
	table, before, after string
	occurrences          int
}{
	{"idempotency_records", "'game_blackjack','activity_loan','donation'", "'game_blackjack','activity_loan','donation','lake_notes','personal_automation'", 1},
	{"risk_client_scans", "kind IN ('client_hits','users','shared_ips')", "kind IN ('client_hits','users','shared_ips','user_ips')", 1},
	{"fatfish_level_versions", "CHECK(engine_version IN (1,2))", "CHECK(engine_version IN (1,2,3))", 1},
	{"charity_model_bindings", "FOREIGN KEY(endpoint_key_id,upstream_model_id) REFERENCES model_pair_catalog(endpoint_key_id,normalized_model_id) ON DELETE CASCADE", "FOREIGN KEY(endpoint_key_id) REFERENCES endpoint_keys(id) ON DELETE CASCADE", 1},
	{"donation_reviews", "'failure_streak_reset','failure_policy_update'", "'failure_streak_reset','failure_policy_update','force_reject'", 1},
	{"policy_audits", "policy TEXT NOT NULL CHECK(policy IN ('force_store_false','flatten_tool_calls')),\n old_value INTEGER NOT NULL CHECK(old_value IN (0,1)),\n new_value INTEGER NOT NULL CHECK(new_value IN (0,1))", "policy TEXT NOT NULL CHECK(policy IN ('force_store_false','flatten_tool_calls','role_policy')),\n old_value INTEGER CHECK(old_value IN (0,1)),\n new_value INTEGER CHECK(new_value IN (0,1))", 1},
	{"request_source_facts", "request_log_id INTEGER PRIMARY KEY REFERENCES request_logs(id) ON DELETE CASCADE,\n user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,\n kind", "source_id INTEGER PRIMARY KEY AUTOINCREMENT,\n request_log_id INTEGER NOT NULL UNIQUE REFERENCES request_logs(id) ON DELETE CASCADE,\n user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,\n kind", 1},
	{"risk_scan_results", "user_id INTEGER REFERENCES users(id) ON DELETE CASCADE,\n request_log_id INTEGER REFERENCES request_source_facts(request_log_id)", "user_id INTEGER CHECK(user_id IS NULL OR user_id>0),\n request_log_id INTEGER REFERENCES request_source_facts(request_log_id)", 1},
	{"risk_scan_result_users", "user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,\n PRIMARY KEY(scan_id,row_no,user_id)", "user_id INTEGER NOT NULL CHECK(user_id>0),\n PRIMARY KEY(scan_id,row_no,user_id)", 1},
	{"risk_client_scans", "'candidate_limit','minute_limit','source_changed'", "'candidate_limit','minute_limit','source_changed','window_limit'", 1},
	{"credit_operations", "'fatfish_unlock','fatfish_ticket','fatfish_reward','fatfish_refund'", "'fatfish_unlock','fatfish_ticket','fatfish_reward','fatfish_refund','lake_entry','lake_exchange'", 2},
}

// Foreign-key enforcement is disabled on the isolated startup connection
// before its transaction. Explicit foreign-key and integrity checks run before
// commit, and the connection restores enforcement even after cancellation.
func applyStorageContractsExtension(ctx context.Context, tx *sql.Tx) error {
	manifest, err := readGenerationManifest(ctx, tx)
	if err != nil {
		return err
	}
	if generationManifestDigest(manifest) != preStorageContractsManifestHash {
		return errors.New("unrecognized storage contracts source manifest")
	}
	for _, change := range storageContractTableChanges {
		if err := rebuildStorageContractTable(ctx, tx, change.table, change.before, change.after, change.occurrences); err != nil {
			return err
		}
	}
	_, additive, ok := strings.Cut(preRecurrenceSchema(), storageContractsMarker)
	if !ok {
		return errors.New("canonical storage contracts are missing")
	}
	if _, err := tx.ExecContext(ctx, additive); err != nil {
		return err
	}
	if err := backfillDonationApprovalOrigins(ctx, tx); err != nil {
		return err
	}
	return seedLakeNotesStorage(ctx, tx)
}

// Rebuilding retains table SQL, every column, index, trigger, and the high-water
// autoincrement identity. It never edits SQLite's internal schema catalog.
func rebuildStorageContractTable(ctx context.Context, tx *sql.Tx, table, before, after string, occurrences int) error {
	var enforcement int
	if err := tx.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&enforcement); err != nil {
		return err
	}
	if enforcement != 0 {
		return errors.New("table rebuild requires isolated foreign-key handling")
	}
	var definition string
	if err := tx.QueryRowContext(ctx, "SELECT sql FROM sqlite_schema WHERE type='table' AND name=?", table).Scan(&definition); err != nil {
		return err
	}
	if strings.Count(definition, before) != occurrences {
		return errors.New("storage contracts source constraint mismatch")
	}
	rows, err := tx.QueryContext(ctx, "SELECT sql FROM sqlite_schema WHERE tbl_name=? AND type IN ('index','trigger') AND sql IS NOT NULL ORDER BY type,name", table)
	if err != nil {
		return err
	}
	var objects []string
	for rows.Next() {
		var object string
		if err := rows.Scan(&object); err != nil {
			rows.Close()
			return err
		}
		objects = append(objects, object)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	var sequence sql.NullInt64
	err = tx.QueryRowContext(ctx, "SELECT seq FROM sqlite_sequence WHERE name=?", table).Scan(&sequence)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	columns, err := tx.QueryContext(ctx, "SELECT name FROM pragma_table_info(?) ORDER BY cid", table)
	if err != nil {
		return err
	}
	var names []string
	for columns.Next() {
		var name string
		if err := columns.Scan(&name); err != nil {
			columns.Close()
			return err
		}
		names = append(names, quoteSQLiteIdentifier(name))
	}
	if err := columns.Err(); err != nil {
		columns.Close()
		return err
	}
	if err := columns.Close(); err != nil {
		return err
	}
	name := quoteSQLiteIdentifier(table)
	if _, err := tx.ExecContext(ctx, "CREATE TEMP TABLE storage_contract_saved_rows AS SELECT * FROM "+name); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DROP TABLE "+name); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, strings.Replace(definition, before, after, occurrences)); err != nil {
		return err
	}
	projection := strings.Join(names, ",")
	order := ""
	if table == "request_source_facts" {
		order = " ORDER BY request_log_id"
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO "+name+"("+projection+") SELECT "+projection+" FROM temp.storage_contract_saved_rows"+order); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DROP TABLE temp.storage_contract_saved_rows"); err != nil {
		return err
	}
	for _, object := range objects {
		if _, err := tx.ExecContext(ctx, object); err != nil {
			return err
		}
	}
	if !strings.Contains(definition, "AUTOINCREMENT") && strings.Contains(after, "AUTOINCREMENT") {
		return nil
	}
	// An empty INSERT SELECT also creates an allocator row. Preserve its
	// original absence as well as a previously allocated high-water value.
	if _, err := tx.ExecContext(ctx, "DELETE FROM sqlite_sequence WHERE name=?", table); err != nil {
		return err
	}
	if sequence.Valid {
		if _, err := tx.ExecContext(ctx, "INSERT INTO sqlite_sequence(name,seq) VALUES(?,?)", table, sequence.Int64); err != nil {
			return err
		}
	}
	return nil
}

func seedLakeNotesStorage(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `
INSERT INTO limited_activity_configs(activity_key,visible,starts_at,ends_at,paused,module_config,revision,updated_at)
 VALUES('lake-notes',0,NULL,NULL,0,'{}',1,0);
INSERT INTO limited_activity_revisions(activity_key,revision,visible,starts_at,ends_at,paused,module_config,actor_user_id,created_at)
 VALUES('lake-notes',1,0,NULL,NULL,0,'{}',NULL,0);`)
	return err
}
