package db

import (
	"context"
	"database/sql"
	_ "embed"
	"testing"
)

// These small reverse DDL fixtures reconstruct empty predecessor schemas.
// Production upgrades only use the forward SQL in migrations/.
//
//go:embed testdata/pre_release_current.sql
var preReleaseCurrentFixture string

//go:embed testdata/pre_release_indexes.sql
var preReleaseIndexesFixture string

//go:embed testdata/pre_release_ledger.sql
var preReleaseLedgerFixture string

//go:embed testdata/pre_ai_players.sql
var preAIPlayersFixture string

//go:embed testdata/pre_management_and_games.sql
var preManagementAndGamesFixture string

func aiPlayersStorageSchema() string { return generationTwoSchema + preManagementAndGamesFixture }

func baselineStorageSchema() string {
	return aiPlayersStorageSchema() + preAIPlayersFixture + `
DROP INDEX idx_charity_reservations_retention;
DROP INDEX idx_donation_usage_retention;
UPDATE schema_state SET version=1 WHERE id=1;
`
}

func preStorageVersionSchema() string {
	return baselineStorageSchema() + preReleaseCurrentFixture
}

func preQueryIndexesSchema() string {
	return preStorageVersionSchema() + preReleaseIndexesFixture
}

func preLedgerRetentionSchema() string {
	return preQueryIndexesSchema() + preReleaseLedgerFixture
}

func supportedSourceFixture(t *testing.T) *sql.DB {
	t.Helper()
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	database.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = database.Close() })
	hostileMustExec(t, database, "PRAGMA foreign_keys=ON;"+preLedgerRetentionSchema())
	assertRetainedManifest(t, database, preLedgerRetentionManifestHash)
	tx, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := seedGenerationTwo(context.Background(), tx, hostileOID("b1e_")); err != nil {
		t.Fatal(err)
	}
	// The historical source predates these configuration rows.
	if _, err := tx.Exec(`DELETE FROM site_config WHERE key='global_rpm_per_user' OR key LIKE 'game_gwent_%' OR key LIKE 'game_steadycatch_%' OR key LIKE 'game_lakenotes_%'`); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return database
}

func TestSupportedReleasedSchemaIdentity(t *testing.T) {
	for _, source := range []struct{ schema, manifest string }{
		{aiPlayersStorageSchema(), aiPlayersManifestHash},
		{preLedgerRetentionSchema(), preLedgerRetentionManifestHash},
		{preQueryIndexesSchema(), preQueryIndexesManifestHash},
		{preStorageVersionSchema(), preStorageVersionManifestHash},
		{baselineStorageSchema(), baselineManifestHash},
		{aiPlayersStorageSchema() + preAIPlayersFixture, terminalReservationIndexesManifestHash},
	} {
		database, err := sql.Open("sqlite", ":memory:")
		if err != nil {
			t.Fatal(err)
		}
		database.SetMaxOpenConns(1)
		hostileMustExec(t, database, "PRAGMA foreign_keys=ON;"+source.schema)
		assertRetainedManifest(t, database, source.manifest)
		for range 2 {
			if err := extendKnownGenerationTwoSchema(context.Background(), database); err != nil {
				t.Fatal(err)
			}
			assertRetainedManifest(t, database, PinnedGenerationTwoManifestHash)
			assertForeignKeyEnforcement(t, database)
			if version, err := readSchemaVersion(context.Background(), database); err != nil || version != len(storageSchema.versions) {
				t.Fatal("current schema not reached", version, err)
			}
		}
		_ = database.Close()
	}
}
