package db

import (
	"database/sql"
	"testing"
)

func restoreSchema42LayoutForTest(t *testing.T, database *sql.DB) {
	t.Helper()
	hostileMustExec(t, database, `DROP TABLE agent_operation_diagnostics;
DROP TRIGGER management_capture_donation_service_health_insert;
DROP TRIGGER management_capture_donation_service_health_update;
DROP TRIGGER management_capture_donation_service_health_delete;
DROP INDEX idx_charity_outcomes_success;
DROP INDEX idx_fishing_rank_active_user;`)
	assertRetainedManifest(t, database, Schema42ManifestHash)
}

func TestOperatorFollowupPreservesSchema42Layout(t *testing.T) {
	database := openGenerationTwoDDLForTest(t)
	defer database.Close()
	restoreSchema42LayoutForTest(t, database)
}
