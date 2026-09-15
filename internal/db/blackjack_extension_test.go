package db

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func makePreBlackjackFixture(t *testing.T, database *sql.DB) {
	t.Helper()
	makePreRandomnessFixture(t, database)
	present, err := BlackjackStoragePresent(context.Background(), database)
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
	if _, err := prior.Exec(generationTwoWithoutBlackjackSchema); err != nil {
		t.Fatal(err)
	}
	tx, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for _, table := range []string{"anonymous", "events", "payments", "entries", "sessions"} {
		var count int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM game_blackjack_` + table).Scan(&count); err != nil || count != 0 {
			t.Fatal("fixture has blackjack facts", count, err)
		}
	}
	for _, line := range strings.Split(blackjackTablesSchema, "\n") {
		fields := strings.Fields(line)
		if len(fields) > 2 && fields[0] == "CREATE" && fields[1] == "TRIGGER" {
			if _, err := tx.Exec(`DROP TRIGGER ` + hostileQuoteIdent(fields[2])); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := tx.Exec(`DROP INDEX idx_credit_blackjack_history`); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"anonymous", "events", "payments", "entries", "sessions", "clock"} {
		if _, err := tx.Exec(`DROP TABLE game_blackjack_` + table); err != nil {
			t.Fatal(err)
		}
	}
	for key := range blackjackConfigDefaults() {
		if _, err := tx.Exec(`DELETE FROM site_config WHERE key=?`, key); err != nil {
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
	for _, table := range []string{"credit_accounts", "credit_operations", "idempotency_records"} {
		var old string
		if err := prior.QueryRow(`SELECT sql FROM sqlite_schema WHERE type='table' AND name=?`, table).Scan(&old); err != nil {
			t.Fatal(err)
		}
		current, err := extendBlackjackTableSQL(table, old)
		if err != nil {
			t.Fatal(err)
		}
		r, err := tx.Exec(`UPDATE sqlite_schema SET sql=? WHERE type='table' AND name=? AND sql=?`, old, table, current)
		if err != nil {
			t.Fatal(err)
		}
		if n, err := r.RowsAffected(); err != nil || n != 1 {
			t.Fatal("wrong fixture constraint", table, err)
		}
	}
	if _, err := tx.Exec(fmt.Sprintf(`PRAGMA schema_version=%d; PRAGMA writable_schema=RESET`, version+1)); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestBlackjackPreviousCandidateUpgradeAndReopen(t *testing.T) {
	path, vault := bootstrapTestPath(t, "source.sqlite"), bootstrapTestVault(t)
	store, err := Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	makePreBlackjackFixture(t, store.DB())
	hostileMustExec(t, store.DB(), `UPDATE site_config SET value='Preserved site' WHERE key='site_name'`)
	assertRetainedManifest(t, store.DB(), preBlackjackManifestHash)
	before := retainedTableImages(t, store.DB(), nil)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		store, err = Open(path, vault)
		if err != nil {
			t.Fatal(err)
		}
		assertRetainedManifest(t, store.DB(), PinnedGenerationTwoManifestHash)
		assertRetainedImages(t, store.DB(), before)
		for key, want := range blackjackConfigDefaults() {
			var value string
			if err := store.DB().QueryRow(`SELECT value FROM site_config WHERE key=?`, key).Scan(&value); err != nil || value != want {
				t.Fatal(key, value, err)
			}
		}
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestBlackjackPartialSourceRejectedWithoutWriting(t *testing.T) {
	path, vault := bootstrapTestPath(t, "partial.sqlite"), bootstrapTestVault(t)
	store, err := Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	makePreBlackjackFixture(t, store.DB())
	hostileMustExec(t, store.DB(), `CREATE TABLE game_blackjack_entries(incomplete TEXT)`)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	before := snapshotBootstrapSources(t, path)
	store, err = Open(path, vault)
	if store != nil {
		store.Close()
		t.Fatal("partial schema accepted")
	}
	if err == nil || !reflect.DeepEqual(before, snapshotBootstrapSources(t, path)) {
		t.Fatal("rejection modified source", err)
	}
}
