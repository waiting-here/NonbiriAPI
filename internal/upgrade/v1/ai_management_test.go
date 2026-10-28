package v1

import (
	"database/sql"
	"slices"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

var aiManagementTables = []string{"management_operation_items", "management_operations", "management_operation_rollups", "management_operation_cleanup_jobs", "ai_agent_personal_memory_history", "ai_agent_personal_memories", "agent_request_drivers", "agent_driver_bindings", "agent_driver_profiles", "agent_chatgpt_session"}

func isAIManagementTableForTest(table string) bool { return slices.Contains(aiManagementTables, table) }

func restoreSchema40LayoutForTest(t *testing.T, database *sql.DB) {
	t.Helper()
	for _, table := range aiManagementTables {
		hostileMustExec(t, database, "DROP TABLE "+table)
	}
}

func TestAIManagementUpgradePreservesExistingConfiguration(t *testing.T) {
	path, vault := bootstrapTestPath(t, "ai-management.sqlite"), bootstrapTestVault(t)
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	restoreSchema40LayoutForTest(t, store.DB())
	hostileMustExec(t, store.DB(), `UPDATE schema_state SET version=40`)
	hostileMustExec(t, store.DB(), `INSERT INTO ai_agent_memories(kind,content,revision,updated_at,updated_by) VALUES('chat','Preserved operational knowledge',2,1,'Administrator')`)
	assertRetainedManifest(t, store.DB(), db.Schema40ManifestHash)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if prior, err := db.Open(path, vault); err == nil {
		prior.Close()
		t.Fatal("previous schema started without explicit upgrade")
	}
	if _, err := Run(t.Context(), path, vault, Options{Apply: true}); err != nil {
		t.Fatal(err)
	}
	store, err = db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var memory string
	if err := store.DB().QueryRow(`SELECT content FROM ai_agent_memories WHERE kind='chat'`).Scan(&memory); err != nil || memory != "Preserved operational knowledge" {
		t.Fatalf("existing memory changed: %q, %v", memory, err)
	}
	for _, table := range aiManagementTables {
		var count int
		if err := store.DB().QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("unexpected backfill in %s: count=%d error=%v", table, count, err)
		}
	}
}
