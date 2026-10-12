package db

import (
	"database/sql"
	_ "embed"
	"testing"
)

//go:embed testdata/pre_heart_moment.sql
var preHeartMomentFixture string

func TestHeartMomentUpgradePreservesAppearance(t *testing.T) {
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	database.SetMaxOpenConns(1)
	hostileMustExec(t, database, "PRAGMA foreign_keys=ON;"+generationTwoSchema+preHeartMomentFixture)
	assertRetainedManifest(t, database, rewardsManifestHash)
	user := hostileInsertUser(t, database, "collector", 0, 0)
	hostileMustExec(t, database, "INSERT INTO user_appearance(user_id,selected,updated_at) VALUES(?,'wandering',123)", user)
	for range 2 {
		if err := extendKnownGenerationTwoSchema(t.Context(), database); err != nil {
			t.Fatal(err)
		}
		assertRetainedManifest(t, database, PinnedGenerationTwoManifestHash)
		var selected, moment string
		var updated int64
		if err := database.QueryRow("SELECT selected,moment,updated_at FROM user_appearance WHERE user_id=?", user).Scan(&selected, &moment, &updated); err != nil || selected != "wandering" || moment != "cover:permanent" || updated != 123 {
			t.Fatal(selected, moment, updated, err)
		}
		assertForeignKeyEnforcement(t, database)
	}
}
