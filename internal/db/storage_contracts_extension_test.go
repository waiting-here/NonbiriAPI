package db

import (
	"database/sql"
	"testing"
)

func assertForeignKeyEnforcement(t *testing.T, database *sql.DB) {
	t.Helper()
	var enabled int
	if err := database.QueryRow("PRAGMA foreign_keys").Scan(&enabled); err != nil || enabled != 1 {
		t.Fatalf("foreign keys=%d: %v", enabled, err)
	}
}

func assertLakeNotesDefault(t *testing.T, database *sql.DB) {
	t.Helper()
	var defaults int
	err := database.QueryRow(`SELECT count(*) FROM limited_activity_configs c
JOIN limited_activity_revisions r ON r.activity_key=c.activity_key AND r.revision=c.revision
WHERE c.activity_key='lake-notes' AND c.visible=0 AND c.paused=0
 AND c.starts_at IS NULL AND c.ends_at IS NULL AND c.module_config='{}' AND c.revision=1
 AND r.visible=0 AND r.paused=0 AND r.starts_at IS NULL AND r.ends_at IS NULL
 AND r.module_config='{}' AND r.actor_user_id IS NULL`).Scan(&defaults)
	if err != nil || defaults != 1 {
		t.Fatalf("hidden lake defaults=%d: %v", defaults, err)
	}
}
