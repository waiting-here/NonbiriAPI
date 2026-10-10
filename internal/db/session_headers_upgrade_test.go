package db

import (
	"database/sql"
	"testing"
)

func TestSessionHeadersUpgradeDefaultAndReferences(t *testing.T) {
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	database.SetMaxOpenConns(1)
	defer database.Close()
	hostileMustExec(t, database, "PRAGMA foreign_keys=ON;"+contextPricingStorageSchema())
	_, model, _ := seedBindingOrderFixture(t, database, 1)
	for range 2 {
		if err = extendKnownGenerationTwoSchema(t.Context(), database); err != nil {
			t.Fatal(err)
		}
		var config string
		if err = database.QueryRow("SELECT session_headers FROM charity_models WHERE id=?", model).Scan(&config); err != nil || config != "[]" {
			t.Fatal(config, err)
		}
		assertRetainedManifest(t, database, PinnedGenerationTwoManifestHash)
	}
}
