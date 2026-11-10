package db

import (
	"database/sql"
	"strings"
	"testing"
)

func restoreSchema51LayoutForTest(t *testing.T, database *sql.DB) {
	t.Helper()
	_, view, _ := strings.Cut(ServiceKeyScopeSQL, "DROP VIEW charity_model_catalog;")
	hostileMustExec(t, database, "DROP VIEW charity_model_catalog;"+view)
	assertRetainedManifest(t, database, Schema51ManifestHash)
}
