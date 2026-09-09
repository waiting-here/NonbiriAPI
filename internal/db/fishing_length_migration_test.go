package db

import "testing"

func TestFishingLengthUpgradeUsesOnlyProvenCompleteSettlements(t *testing.T) {
	path, vault := bootstrapTestPath(t, "fishing-lengths.sqlite"), bootstrapTestVault(t)
	store, err := Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	makeRetainedSource(t, store.DB(), preStewardHoldReadManifestHash)
	database := store.DB()
	user := hostileInsertUser(t, database, "retained-length", 0, 0)
	sizes := []int{100, 110, 198, 130, 140, 150, 160, 198, 170, 180}
	for index, condition := range []string{"complete", "missing-rank", "unapplied", "expired-outcomes", "pending"} {
		batch := hostileOIDVariant("fb_", byte('A'+index), 'Q')
		hostileInsertFishingBatch(t, database, batch, user, hostileOIDVariant("op_", byte('A'+index), 'Q'))
		hostileMustExec(t, database, `UPDATE game_fishing_batches SET count=10,entry_total_milli=10 WHERE id=?`, batch)
		for ordinal, size := range sizes {
			if condition == "pending" && ordinal == 9 {
				continue
			}
			species := []string{"yellowcheek", "taimen", "koi"}[ordinal%3]
			hostileMustExec(t, database, `INSERT INTO game_fishing_outcomes(batch_id,ordinal,species_key,tier,size_cm,payout_milli) VALUES(?,?,?,'legend',?,0)`, batch, ordinal, species, size)
		}
		if condition == "pending" {
			continue
		}
		hostileMustExec(t, database, `UPDATE game_fishing_batches SET state='committed',payout_total_milli=0,ledger_rows_remaining=?,next_attempt_at=NULL,settled_at=100 WHERE id=?`, hostileBlob16(0), batch)
		if condition != "missing-rank" {
			applied := 1
			if condition == "unapplied" {
				applied = 0
			}
			hostileMustExec(t, database, `INSERT INTO game_fishing_rank_facts(batch_id_text,user_id,settled_at,expires_at,payout_total,aggregate_applied) VALUES(?,?,100,2592100,?,?)`, batch, user, hostileBlob16(0), applied)
		}
		if condition == "expired-outcomes" {
			hostileMustExec(t, database, `DELETE FROM game_fishing_batches WHERE id=?`, batch)
		}
	}
	before := retainedTableImages(t, database, nil)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		store, err = Open(path, vault)
		if err != nil {
			t.Fatal(err)
		}
		assertRetainedImages(t, store.DB(), before)
		if countRows(t, store, `SELECT COUNT(*) FROM game_fishing_length_facts`) != 1 ||
			countRows(t, store, `SELECT COUNT(*) FROM game_fishing_length_facts WHERE ordinal=2 AND species_key='koi' AND size_cm=198 AND blue_fat_fish_length_cm IS NULL`) != 1 {
			t.Fatal("upgrade did not preserve the first maximum of the only proven batch")
		}
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
