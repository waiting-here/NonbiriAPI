package db

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

const preStorageContractsManifestHash = "3f773b6dca01058f2296f437c3666afde92a74e8eeb861fa8637756dcd859481"
const storageContractsMarker = "\n-- Additive storage contracts"

var storageContractTableChanges = []struct{ table, before, after string }{
	{"idempotency_records", "'game_blackjack','activity_loan','donation'", "'game_blackjack','activity_loan','donation','lake_notes','personal_automation'"},
	{"risk_client_scans", "kind IN ('client_hits','users','shared_ips')", "kind IN ('client_hits','users','shared_ips','user_ips')"},
	{"fatfish_level_versions", "CHECK(engine_version IN (1,2))", "CHECK(engine_version IN (1,2,3))"},
	{"donation_reviews", "'failure_streak_reset','failure_policy_update'", "'failure_streak_reset','failure_policy_update','force_reject'"},
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
		if err := rebuildStorageContractTable(ctx, tx, change.table, change.before, change.after); err != nil {
			return err
		}
	}
	_, additive, ok := strings.Cut(generationTwoSchema, storageContractsMarker)
	if !ok {
		return errors.New("canonical storage contracts are missing")
	}
	_, err = tx.ExecContext(ctx, additive)
	return err
}

// Rebuilding retains table SQL, every column, index, trigger, and the high-water
// autoincrement identity. It never edits SQLite's internal schema catalog.
func rebuildStorageContractTable(ctx context.Context, tx *sql.Tx, table, before, after string) error {
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
	if strings.Count(definition, before) != 1 {
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
	name := quoteSQLiteIdentifier(table)
	if _, err := tx.ExecContext(ctx, "CREATE TEMP TABLE storage_contract_saved_rows AS SELECT * FROM "+name); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DROP TABLE "+name); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, strings.Replace(definition, before, after, 1)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO "+name+" SELECT * FROM temp.storage_contract_saved_rows"); err != nil {
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
