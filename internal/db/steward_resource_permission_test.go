package db

import (
	"database/sql"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/dbtest"
)

func restoreSchema53LayoutForTest(t *testing.T, database *sql.DB) {
	t.Helper()
	dbtest.RestoreSchema54Fixture(t, database)
	hostileMustExec(t, database, StewardCharityPermissionsSQL)
	assertRetainedManifest(t, database, Schema53ManifestHash)
}
