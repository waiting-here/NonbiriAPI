package db

import (
	"database/sql"
	"strings"
	"testing"
)

func restoreSchema43LayoutForTest(t *testing.T, database *sql.DB) {
	t.Helper()
	old := strings.Replace(CharityLogCoveringIndexSQL, ",id,started_at)", ",id)", 1)
	hostileMustExec(t, database, "DROP INDEX idx_request_logs_charity_model;"+old)
	assertRetainedManifest(t, database, Schema43ManifestHash)
}

func TestCharityLogCoveringIndexPreservesSchema43Layout(t *testing.T) {
	database := openGenerationTwoDDLForTest(t)
	defer database.Close()
	var index string
	if err := database.QueryRow("SELECT sql FROM sqlite_schema WHERE name='idx_request_logs_charity_model'").Scan(&index); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(index)+";" != strings.TrimSpace(CharityLogCoveringIndexSQL) {
		t.Fatal("fresh schema and explicit upgrade index differ", index)
	}
	restoreSchema43LayoutForTest(t, database)
}
