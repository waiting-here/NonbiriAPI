package db

import (
	"database/sql"
	"testing"
)

func restoreSchema53LayoutForTest(t *testing.T, database *sql.DB) {
	t.Helper()
	hostileMustExec(t, database, "DROP INDEX idx_donation_keys_due_expiry")
	for _, column := range []string{"historical_costs_partial", "price_reserved_mag", "calls_reserved", "price_used_mag", "calls_used", "price_limit_mag", "call_limit_mag"} {
		hostileMustExec(t, database, "ALTER TABLE donation_model_quotas DROP COLUMN "+column)
	}
	hostileMustExec(t, database, StewardCharityPermissionsSQL)
	assertRetainedManifest(t, database, Schema53ManifestHash)
}
