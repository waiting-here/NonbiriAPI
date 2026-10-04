package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"testing"
)

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
	const releasedSchemaHash = "41d6ba87261075647661ec37279c2cd83a6c3b32c97206d2b4b3cb68c05e69e5"
	if got := fmt.Sprintf("%x", sha256.Sum256([]byte(preLedgerRetentionSchema()))); got != releasedSchemaHash {
		t.Fatalf("released schema identity changed: %s", got)
	}
	for _, source := range []struct{ schema, manifest string }{
		{preLedgerRetentionSchema(), preLedgerRetentionManifestHash},
		{preQueryIndexesSchema(), preQueryIndexesManifestHash},
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
		}
		_ = database.Close()
	}
}
