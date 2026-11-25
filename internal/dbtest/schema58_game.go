package dbtest

import (
	"database/sql"
	_ "embed"
	"testing"
)

//go:embed testdata/schema58_game_cancellation.sql.txt
var originalGameTerminalSchema string

// RestoreSchema58GameCancellation reconstructs the original game constraints on
// an empty synthetic fixture before test cases seed its historical game data.
func RestoreSchema58GameCancellation(t *testing.T, source *sql.DB) {
	t.Helper()
	tx, err := source.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var populated bool
	if err = tx.QueryRow("SELECT EXISTS(SELECT 1 FROM game_rps_summaries) OR EXISTS(SELECT 1 FROM lake_notes_casts) OR EXISTS(SELECT 1 FROM game_linklink_summaries)").Scan(&populated); err != nil || populated {
		t.Fatalf("expected empty game fixture: populated=%v err=%v", populated, err)
	}
	if _, err = tx.Exec(originalGameTerminalSchema); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
}
