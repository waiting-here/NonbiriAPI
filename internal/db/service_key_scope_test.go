package db

import (
	"database/sql"
	"testing"
)

func restoreSchema39LayoutForTest(t *testing.T, database *sql.DB) {
	t.Helper()
	restoreSchema40LayoutForTest(t, database)
	for _, table := range []string{"gacha_shop_participants", "gacha_shop_card_statistics", "gacha_shop_statistics", "gacha_card_participants", "gacha_pool_participants", "gacha_card_statistics", "gacha_pool_statistics"} {
		hostileMustExec(t, database, "DROP TABLE "+table)
	}
	hostileMustExec(t, database, "DROP TABLE discovery_source_keys")
	hostileMustExec(t, database, "DROP TABLE discovery_run_changes")
	hostileMustExec(t, database, DonationDiscoveryAuthorizationSQL)
}

func restoreSchema40LayoutForTest(t *testing.T, database *sql.DB) {
	t.Helper()
	restoreSchema41LayoutForTest(t, database)
	for _, table := range []string{"management_operation_items", "management_operations", "management_operation_rollups", "management_operation_cleanup_jobs", "ai_agent_personal_memory_history", "ai_agent_personal_memories", "agent_request_drivers", "agent_driver_bindings", "agent_driver_profiles", "agent_chatgpt_session"} {
		hostileMustExec(t, database, "DROP TABLE "+table)
	}
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
