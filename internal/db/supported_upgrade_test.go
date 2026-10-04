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

func preStorageVersionSchema() string {
	return generationTwoSchema + preReleaseCurrentFixture
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
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return database
}

func TestSupportedReleasedSchemaIdentity(t *testing.T) {
	for _, source := range []struct{ schema, manifest string }{
		{preLedgerRetentionSchema(), preLedgerRetentionManifestHash},
		{preQueryIndexesSchema(), preQueryIndexesManifestHash},
		{preStorageVersionSchema(), preStorageVersionManifestHash},
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
			if version, err := readSchemaVersion(context.Background(), database); err != nil || version != 1 {
				t.Fatal("stable baseline not reached", version, err)
			}
		}
		_ = database.Close()
	}
}
