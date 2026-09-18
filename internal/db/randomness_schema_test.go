package db

import (
	"context"
	"database/sql"
	"testing"
)

func makePreRandomnessFixture(t *testing.T, database *sql.DB) {
	t.Helper()
	makePreGatewayPolicyFixture(t, database)
	var n int
	if err := database.QueryRow(`SELECT count(*) FROM sqlite_schema WHERE type='table' AND name='game_random_proofs'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		return
	}
	if err := database.QueryRow(`SELECT count(*) FROM game_random_proofs`).Scan(&n); err != nil || n != 0 {
		t.Fatal("fixture contains game random proofs", n, err)
	}
	if _, err := database.Exec(`DROP TABLE game_random_proofs`); err != nil {
		t.Fatal(err)
	}
}

func TestGameRandomnessSchemaExtension(t *testing.T) {
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec(generationTwoWithoutRandomnessSchema); err != nil {
		t.Fatal(err)
	}
	assertRetainedManifest(t, database, preRandomnessManifestHash)
	tx, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := applyRandomnessExtension(context.Background(), tx); err != nil {
		t.Fatal(err)
	}
	if err := applyGatewayPolicyExtension(context.Background(), tx); err != nil {
		t.Fatal(err)
	}
	if err := applyBlackjackNineSeatExtension(context.Background(), tx); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	assertRetainedManifest(t, database, PinnedGenerationTwoManifestHash)
}
