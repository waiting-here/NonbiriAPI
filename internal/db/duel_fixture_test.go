package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
)

// Restore an empty extension to its exact predecessor for upgrade fixtures.
func makePreDuelFixture(t *testing.T, database *sql.DB) {
	t.Helper()
	makePreBlackjackFixture(t, database)
	present, err := DuelStoragePresent(context.Background(), database)
	if err != nil {
		t.Fatal(err)
	}
	if !present {
		return
	}
	for _, table := range []string{"anonymous_rounds", "anonymous", "user_slots", "rounds", "seats", "sessions", "queue", "catalogs"} {
		var n int
		if err := database.QueryRow(`SELECT COUNT(*) FROM game_duel_` + table).Scan(&n); err != nil || n != 0 {
			t.Fatal("duel fixture contains data", table, n, err)
		}
	}
	prior, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer prior.Close()
	if _, err := prior.Exec(generationTwoWithoutDuelsSchema); err != nil {
		t.Fatal(err)
	}
	type change struct{ table, previous, current string }
	var changes []change
	for _, table := range []string{"credit_accounts", "credit_operations", "idempotency_records"} {
		c := change{table: table}
		if err := prior.QueryRow(`SELECT sql FROM sqlite_schema WHERE type='table' AND name=?`, table).Scan(&c.previous); err != nil {
			t.Fatal(err)
		}
		if c.current, err = extendDuelTableSQL(table, c.previous); err != nil {
			t.Fatal(err)
		}
		changes = append(changes, c)
	}
	tx, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for _, line := range strings.Split(duelTablesSchema, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 3 && fields[0] == "CREATE" && fields[1] == "TRIGGER" {
			if _, err := tx.Exec(`DROP TRIGGER ` + hostileQuoteIdent(fields[2])); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := tx.Exec(`DROP INDEX idx_credit_duel_terminal`); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"anonymous_rounds", "anonymous", "user_slots", "rounds", "seats", "sessions", "queue", "catalogs"} {
		if _, err := tx.Exec(`DROP TABLE game_duel_` + table); err != nil {
			t.Fatal(err)
		}
	}
	for key := range duelConfigDefaults() {
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
	for _, c := range changes {
		result, err := tx.Exec(`UPDATE sqlite_schema SET sql=? WHERE type='table' AND name=? AND sql=?`, c.previous, c.table, c.current)
		if err != nil {
			t.Fatal(err)
		}
		if n, err := result.RowsAffected(); err != nil || n != 1 {
			t.Fatal("unexpected duel table fixture", c.table, n, err)
		}
	}
	if _, err := tx.Exec(fmt.Sprintf(`PRAGMA schema_version=%d;PRAGMA writable_schema=RESET`, version+1)); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}
