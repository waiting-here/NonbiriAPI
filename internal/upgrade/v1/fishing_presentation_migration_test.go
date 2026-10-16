package v1

import (
	"context"
	"strconv"
	"strings"
	"testing"
)

func TestFishingPresentationMigrationFromExactSchema17(t *testing.T) {
	for _, tc := range []struct{ blue, explicit, want string }{{"0", "", "1000"}, {"9500", "", "500"}, {"10000", "", "0"}, {"2000", "625", "625"}} {
		t.Run(tc.blue+"/"+tc.explicit, func(t *testing.T) {
			database := openSchema17DDLForTest(t)
			user := hostileInsertUser(t, database, "migration", 0, 0)
			batch := hostileOID("fb_")
			hostileInsertFishingBatch(t, database, batch, user, hostileOID("op_"))
			length := strings.Repeat("9", 128)
			hostileMustExec(t, database, `INSERT INTO game_fishing_outcomes(batch_id,ordinal,species_key,tier,size_cm,payout_milli) VALUES(?,0,'koi','legend',137,1)`, batch)
			hostileMustExec(t, database, `INSERT INTO game_fishing_outcome_lengths VALUES(?,0,?)`, batch, length)
			hostileMustExec(t, database, `INSERT INTO game_fishing_best(user_id,batch_id,ordinal,species_key,tier,size_cm,caught_at,public_tie_key) VALUES(?,?,0,'koi','legend',137,0,?)`, user, batch, hostileBlob32(1))
			hostileMustExec(t, database, `INSERT INTO game_fishing_best_lengths VALUES(?,?)`, user, length)
			hostileMustExec(t, database, `INSERT INTO game_fishing_rank_facts VALUES(?,?,0,2592000,?,1)`, batch, user, hostileBlob16(1))
			hostileMustExec(t, database, `INSERT INTO game_fishing_length_facts VALUES(?,0,'koi','legend',137,0,?)`, batch, length)
			hostileMustExec(t, database, `INSERT INTO site_config VALUES('game_fishing_blue_fish_chance_bps',?,0)`, tc.blue)
			if tc.explicit != "" {
				hostileMustExec(t, database, `INSERT INTO site_config VALUES('game_fishing_milk_crab_chance_bps',?,0)`, tc.explicit)
			}
			ctx := context.Background()
			hostileMustExec(t, database, `PRAGMA foreign_keys=OFF`)
			tx, err := database.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			if err = extendContentAndProtocols(ctx, tx); err != nil {
				_ = tx.Rollback()
				t.Fatal(err)
			}
			if err = tx.Commit(); err != nil {
				t.Fatal(err)
			}
			var blue, milk string
			if err = database.QueryRow(`SELECT value FROM site_config WHERE key='game_fishing_blue_fish_chance_bps'`).Scan(&blue); err != nil || blue != tc.blue {
				t.Fatal(blue, err)
			}
			if err = database.QueryRow(`SELECT value FROM site_config WHERE key='game_fishing_milk_crab_chance_bps'`).Scan(&milk); err != nil || milk != tc.want {
				t.Fatal(milk, err)
			}
			var oldBlue, oldMilk int
			if err = database.QueryRow(`SELECT blue_fish_chance_bps,milk_crab_chance_bps FROM game_fishing_batches`).Scan(&oldBlue, &oldMilk); err != nil || oldBlue != 1000 || oldMilk != 0 {
				t.Fatal(oldBlue, oldMilk, err)
			}
			for _, table := range []string{"game_fishing_outcome_lengths", "game_fishing_best_lengths"} {
				var got, kind string
				if err = database.QueryRow(`SELECT length_cm,kind FROM `+table).Scan(&got, &kind); err != nil || got != length || kind != "blue" {
					t.Fatal(table, got, kind, err)
				}
			}
			var got string
			var absent *string
			if err = database.QueryRow(`SELECT blue_fat_fish_length_cm,milk_crab_length_cm FROM game_fishing_length_facts`).Scan(&got, &absent); err != nil || got != length || absent != nil {
				t.Fatal(got, absent, err)
			}
			gotManifest, err := readGenerationManifest(ctx, database)
			if err != nil || generationManifestDigest(gotManifest) != stableSchema18Manifest {
				t.Fatal("upgraded and fresh definitions differ", err)
			}
			rows, err := database.Query(`PRAGMA foreign_key_check`)
			if err != nil {
				t.Fatal(err)
			}
			if rows.Next() {
				t.Fatal("broken migrated reference")
			}
			rows.Close()
		})
	}
}

func TestFishingPresentationMigrationRejectsOtherVersion(t *testing.T) {
	for _, version := range []int{16, 18} {
		database := openSchema17DDLForTest(t)
		hostileMustExec(t, database, `UPDATE schema_state SET version=?`, version)
		tx, err := database.BeginTx(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		if err = migrateFishingPresentations(context.Background(), tx); err == nil {
			t.Fatal("accepted source " + strconv.Itoa(version))
		}
		_ = tx.Rollback()
	}
}
