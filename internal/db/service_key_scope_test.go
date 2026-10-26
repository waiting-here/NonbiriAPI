package db

import (
	"database/sql"
	"testing"
)

func restoreSchema39LayoutForTest(t *testing.T, database *sql.DB) {
	t.Helper()
	for _, table := range []string{"gacha_card_participants", "gacha_pool_participants", "gacha_card_statistics", "gacha_pool_statistics"} {
		hostileMustExec(t, database, "DROP TABLE "+table)
	}
	hostileMustExec(t, database, "DROP TABLE discovery_source_keys")
	hostileMustExec(t, database, "DROP TABLE discovery_run_changes")
	hostileMustExec(t, database, DonationDiscoveryAuthorizationSQL)
}

func TestServiceKeyScopeSchema(t *testing.T) {
	database := openGenerationTwoDDLForTest(t)
	manifest, err := SchemaManifestHash(t.Context(), database)
	if err != nil {
		t.Fatal(err)
	}
	if manifest != PinnedGenerationTwoManifestHash || GenerationTwoSchemaHash() != PinnedGenerationTwoSchemaHash {
		t.Fatalf("schema=%s manifest=%s", GenerationTwoSchemaHash(), manifest)
	}
	restoreSchema39LayoutForTest(t, database)
	assertRetainedManifest(t, database, Schema39ManifestHash)
}
