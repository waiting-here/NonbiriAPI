package db

import (
	"database/sql"
	"testing"
)

func TestRequestFactsUpgradeLeavesHistoryUnknown(t *testing.T) {
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	database.SetMaxOpenConns(1)
	defer database.Close()
	hostileMustExec(t, database, personalAdaptationStorageSchema())
	for range 2 {
		if err := extendKnownGenerationTwoSchema(t.Context(), database); err != nil {
			t.Fatal(err)
		}
		assertRetainedManifest(t, database, PinnedGenerationTwoManifestHash)
	}
	var streamingDefault, actionsDefault sql.NullString
	if err := database.QueryRow("SELECT dflt_value FROM pragma_table_info('request_logs') WHERE name='requested_streaming'").Scan(&streamingDefault); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow("SELECT dflt_value FROM pragma_table_info('request_logs') WHERE name='automatic_actions'").Scan(&actionsDefault); err != nil {
		t.Fatal(err)
	}
	if streamingDefault.Valid || actionsDefault.Valid {
		t.Fatal("historical facts must remain unknown")
	}
}
