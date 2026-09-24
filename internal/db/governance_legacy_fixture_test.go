package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
)

// Older structural fixtures remove the latest extension before testing a
// historical upgrade. Callers must start with untouched governance defaults,
// never actual governance records or configured new fields. The guards below
// detect common misuse, not arbitrary new facts. The published binary supplies
// the independent populated upgrade source.
func makePreGovernanceFixture(t *testing.T, database *sql.DB) {
	t.Helper()
	ctx := context.Background()
	present, err := GovernanceStoragePresent(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	if !present {
		return
	}
	prior, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer prior.Close()
	if _, err := prior.Exec(generationTwoWithoutGovernanceSchema); err != nil {
		t.Fatal(err)
	}
	want, err := readGenerationManifest(ctx, prior)
	if err != nil {
		t.Fatal(err)
	}
	got, err := readGenerationManifest(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	known := map[string]schemaObject{}
	for _, object := range want.Objects {
		known[object.Name] = object
	}
	currentTables := map[string]string{}
	for _, object := range got.Objects {
		if object.Type == "table" {
			currentTables[object.Name] = object.SQL
		}
	}
	// These are the only new tables seeded without user actions.
	seedRows := map[string]int{
		"observability_state": 1, "risk_audit_config": 1, "economy_audit_checkpoint": 1,
		"limited_activity_configs": 1, "limited_activity_revisions": 1, "activity_exchange_state": 2,
		"inactivity_policy": 1, "game_rank_net_rebuild": 1, "image_activity_state": 1,
	}
	for _, object := range got.Objects {
		if _, ok := known[object.Name]; ok || object.Type != "table" {
			continue
		}
		var count int
		if err := database.QueryRow("SELECT count(*) FROM " + quoteSQLiteIdentifier(object.Name)).Scan(&count); err != nil || (count != 0 && count != seedRows[object.Name]) {
			t.Fatalf("fixture contains governance facts in %s: %d: %v", object.Name, count, err)
		}
	}
	var assetFacts int
	if err := database.QueryRow(`SELECT count(*) FROM credit_accounts WHERE asset_type IN ('sketch_paper','sketch_brush') AND (balance_sign<>0 OR kind NOT IN ('external','platform'))`).Scan(&assetFacts); err != nil || assetFacts != 0 {
		t.Fatal("fixture contains activity asset facts", assetFacts, err)
	}
	database.SetMaxOpenConns(1)
	hostileMustExec(t, database, `PRAGMA foreign_keys=OFF`)
	defer hostileMustExec(t, database, `PRAGMA foreign_keys=ON`)
	tx, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	sequences := map[string]int64{}
	rows, err := tx.Query(`SELECT name,seq FROM sqlite_sequence`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var name string
		var value int64
		if err := rows.Scan(&name, &value); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		if object, ok := known[name]; ok && object.Type == "table" {
			sequences[name] = value
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatal(err)
	}
	rows.Close()
	for _, object := range got.Objects {
		if object.Type == "trigger" || object.Type == "index" {
			if _, err := tx.Exec("DROP " + object.Type + " " + quoteSQLiteIdentifier(object.Name)); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, object := range got.Objects {
		if _, ok := known[object.Name]; !ok && object.Type == "table" {
			if _, err := tx.Exec("DROP TABLE " + quoteSQLiteIdentifier(object.Name)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := tx.Exec(`DELETE FROM credit_accounts WHERE asset_type IN ('sketch_paper','sketch_brush');
UPDATE users SET level=5 WHERE level=6;
UPDATE charity_model_access SET allowed_level_mask=(allowed_level_mask & 15) | ((allowed_level_mask & 32)>>1);
UPDATE site_config SET value=(SELECT value FROM site_config WHERE key='level_display_name_6') WHERE key='level_display_name_5';`); err != nil {
		t.Fatal(err)
	}
	for key := range governanceConfigDefaults() {
		if _, err := tx.Exec(`DELETE FROM site_config WHERE key=?`, key); err != nil {
			t.Fatal(err)
		}
	}
	// Rebuild using the old column set: dropping a single new column cannot
	// remove a CHECK that relates several new signed-value columns.
	for _, table := range want.Tables {
		definition := known[table.Name].SQL
		if currentTables[table.Name] == definition {
			continue
		}
		name := quoteSQLiteIdentifier(table.Name)
		temporary := quoteSQLiteIdentifier("legacy_fixture_" + table.Name)
		start := strings.IndexByte(definition, '(')
		if start < 0 {
			t.Fatal("missing table definition", table.Name)
		}
		var names []string
		for _, column := range table.Columns {
			names = append(names, quoteSQLiteIdentifier(column.Name))
		}
		list := strings.Join(names, ",")
		for _, statement := range []string{
			"CREATE TABLE " + temporary + " " + definition[start:],
			"INSERT INTO " + temporary + " (" + list + ") SELECT " + list + " FROM " + name,
			"DROP TABLE " + name,
			"ALTER TABLE " + temporary + " RENAME TO " + name,
		} {
			if _, err := tx.Exec(statement); err != nil {
				t.Fatal(table.Name, err)
			}
		}
	}
	var version int
	if err := tx.QueryRow(`PRAGMA schema_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`PRAGMA writable_schema=ON`); err != nil {
		t.Fatal(err)
	}
	for _, object := range want.Objects {
		if object.Type == "table" {
			if _, err := tx.Exec(`UPDATE sqlite_schema SET sql=? WHERE type='table' AND name=?`, object.SQL, object.Name); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := tx.Exec(fmt.Sprintf(`PRAGMA schema_version=%d; PRAGMA writable_schema=RESET`, version+1)); err != nil {
		t.Fatal(err)
	}
	for _, object := range want.Objects {
		if object.Type == "trigger" || object.Type == "index" {
			if _, err := tx.Exec(object.SQL); err != nil {
				t.Fatal(err)
			}
		}
	}
	// Copying rows alone would reuse deleted high IDs on the next insert.
	for name, sequence := range sequences {
		if _, err := tx.Exec(`DELETE FROM sqlite_sequence WHERE name=?`, name); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(`INSERT INTO sqlite_sequence(name,seq) VALUES(?,?)`, name, sequence); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	assertRetainedManifest(t, database, preGovernanceManifestHash)
}

func TestLegacyStructuralFixturePreservesAutoincrementHistory(t *testing.T) {
	database := openGenerationTwoDDLForTest(t)
	hostileMustExec(t, database, `INSERT INTO sqlite_sequence(name,seq) VALUES('request_logs',1000)`)
	makePreGovernanceFixture(t, database)
	var next int64
	if err := database.QueryRow(`SELECT seq FROM sqlite_sequence WHERE name='request_logs'`).Scan(&next); err != nil || next != 1000 {
		t.Fatal("lost the deleted-ID high-water mark", next, err)
	}
}
