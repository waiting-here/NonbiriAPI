package db

import (
	"database/sql"
	_ "embed"
	"testing"
)

//go:embed testdata/pre_rewards.sql
var preRewardsFixture string

func requestFactsStorageSchema() string {
	return generationTwoSchema + preHeartMomentFixture + preRewardsFixture
}
func TestRewardsUpgradePreservesExistingCheckin(t *testing.T) {
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	database.SetMaxOpenConns(1)
	hostileMustExec(t, database, "PRAGMA foreign_keys=ON;"+requestFactsStorageSchema())
	assertRetainedManifest(t, database, requestFactsManifestHash)
	user := hostileInsertUser(t, database, "existing-collector", 0, 0)
	hostileMustExec(t, database, `INSERT INTO gacha_profiles(user_id,permanent,limited,checked_day,month,entered_at,updated_at) VALUES(?,17,9,'2027-01-15','2027-01',123,124)`, user)
	for range 2 {
		if err := extendKnownGenerationTwoSchema(t.Context(), database); err != nil {
			t.Fatal(err)
		}
		assertRetainedManifest(t, database, PinnedGenerationTwoManifestHash)
		var permanent, limited, welcome int
		var checked, limitedDay string
		if err := database.QueryRow("SELECT permanent,limited,checked_day,limited_checked_day,welcome_granted FROM gacha_profiles WHERE user_id=?", user).Scan(&permanent, &limited, &checked, &limitedDay, &welcome); err != nil || permanent != 17 || limited != 9 || checked != "2027-01-15" || limitedDay != "" || welcome != 1 {
			t.Fatal(permanent, limited, checked, limitedDay, welcome, err)
		}
		assertForeignKeyEnforcement(t, database)
	}
}
