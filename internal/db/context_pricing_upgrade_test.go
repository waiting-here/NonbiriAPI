package db

import (
	"database/sql"
	"testing"
)

func TestContextPricingUpgradeDefaultsOffAndRetainsPrices(t *testing.T) {
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	database.SetMaxOpenConns(1)
	defer database.Close()
	hostileMustExec(t, database, "PRAGMA foreign_keys=ON;"+userAppearanceStorageSchema())
	_, model, _ := seedBindingOrderFixture(t, database, 1)
	hostileMustExec(t, database, "UPDATE charity_models SET request_user_price=1250,output_user_price=7500 WHERE id=?", model)
	for range 2 {
		if err := extendKnownGenerationTwoSchema(t.Context(), database); err != nil {
			t.Fatal(err)
		}
		var policy string
		var request, output int64
		if err := database.QueryRow("SELECT context_pricing,request_user_price,output_user_price FROM charity_models WHERE id=?", model).Scan(&policy, &request, &output); err != nil {
			t.Fatal(err)
		}
		if policy != "{}" || request != 1250 || output != 7500 {
			t.Fatal(policy, request, output)
		}
		assertRetainedManifest(t, database, PinnedGenerationTwoManifestHash)
	}
}
