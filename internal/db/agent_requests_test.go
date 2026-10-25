package db

import (
	"context"
	"database/sql"
	"testing"
)

func dropAgentFoundationForTest(t *testing.T, database *sql.DB) {
	t.Helper()
	for _, table := range []string{"agent_request_response_chunks", "agent_request_settings", "agent_requests", "agent_request_usage", "agent_request_usage_totals", "agent_request_usage_hours"} {
		hostileMustExec(t, database, "DROP TABLE "+table)
	}
}
func TestAgentStartupUpgradePreservesExactSchema36(t *testing.T) {
	path := bootstrapTestPath(t, "agent-upgrade.sqlite")
	vault := bootstrapTestVault(t)
	store, err := Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	user := hostileInsertUser(t, store.DB(), "preserved-agent-owner", 0, 0)
	hostileMustExec(t, store.DB(), "UPDATE site_config SET value='preserved instance' WHERE key='site_name'")
	var originalName string
	if err = store.DB().QueryRow("SELECT username FROM users WHERE id=?", user).Scan(&originalName); err != nil {
		t.Fatal(err)
	}
	dropAgentFoundationForTest(t, store.DB())
	hostileMustExec(t, store.DB(), "UPDATE schema_state SET version=36")
	manifest, err := SchemaManifestHash(context.Background(), store.DB())
	if err != nil || manifest != Schema36ManifestHash {
		t.Fatal("source36 fixture drift", manifest, err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	upgraded, err := Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	var username, site string
	if err = upgraded.DB().QueryRow("SELECT username,(SELECT value FROM site_config WHERE key='site_name') FROM users WHERE id=?", user).Scan(&username, &site); err != nil || username != originalName || site != "preserved instance" {
		t.Fatal("original facts changed", username, site, err)
	}
	for _, table := range []string{"agent_request_response_chunks", "agent_request_settings", "agent_requests", "agent_request_usage", "agent_request_usage_totals", "agent_request_usage_hours"} {
		var count int
		if err = upgraded.DB().QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil || count != 0 {
			t.Fatal("new table not empty", table, count, err)
		}
	}
	if err = validateGenerationTwoManifest(context.Background(), upgraded.DB()); err != nil {
		t.Fatal(err)
	}
}
