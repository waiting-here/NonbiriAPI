package db

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
)

// Historical structural tests peel off newer extensions before exercising an
// older migration. This helper is limited to fixtures with no new domain facts;
// the published-binary upgrade gate supplies the independent populated source.
func makePreProgressionFixture(t *testing.T, database *sql.DB) {
	t.Helper()
	ctx := context.Background()
	present, err := ProgressionStoragePresent(ctx, database)
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
	if _, err := prior.Exec(generationTwoWithoutProgressionSchema); err != nil {
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
	tx, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for _, object := range got.Objects {
		if _, ok := known[object.Name]; !ok && (object.Type == "trigger" || object.Type == "index") {
			if _, err := tx.Exec("DROP " + object.Type + " " + quoteSQLiteIdentifier(object.Name)); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, table := range []string{"abuse_evidence", "abuse_actions", "abuse_cases", "abuse_window_events", "abuse_windows", "activity_loans", "game_rank_expiry_work", "game_rank_totals", "game_rank_events", "game_rank_counters", "game_statistics_epoch"} {
		var count int
		if err := tx.QueryRow("SELECT count(*) FROM " + quoteSQLiteIdentifier(table)).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 && table != "game_rank_counters" && table != "game_statistics_epoch" {
			t.Fatal("fixture has progression facts", table)
		}
		if _, err := tx.Exec("DROP TABLE " + quoteSQLiteIdentifier(table)); err != nil {
			t.Fatal(err)
		}
	}
	var version int
	if err := tx.QueryRow(`PRAGMA schema_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`PRAGMA writable_schema=ON`); err != nil {
		t.Fatal(err)
	}
	for _, object := range got.Objects {
		previous, ok := known[object.Name]
		if !ok || previous.SQL == object.SQL {
			continue
		}
		if _, err := tx.Exec(`UPDATE sqlite_schema SET sql=? WHERE type=? AND name=?`, previous.SQL, previous.Type, previous.Name); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tx.Exec(fmt.Sprintf(`PRAGMA schema_version=%d; PRAGMA writable_schema=RESET`, version+1)); err != nil {
		t.Fatal(err)
	}
	for key := range progressionConfigDefaults() {
		if _, err := tx.Exec(`DELETE FROM site_config WHERE key=?`, key); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	assertRetainedManifest(t, database, preProgressionManifestHash)
}
