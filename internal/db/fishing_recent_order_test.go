package db

import (
	"strings"
	"testing"
)

func TestFishingRecentOrderCopiesExactLengthAndCascades(t *testing.T) {
	database := openGenerationTwoConstraintFixture(t)
	owner := hostileInsertUser(t, database, "recent-owner", 0, 0)
	for index, fixture := range []struct {
		species, tier string
		size          int
		blue, milk    any
		want          string
	}{
		{"boot", "junk", 0, nil, nil, "0"},
		{"koi", "legend", 137, nil, nil, "137"},
		{"koi", "legend", 137, "201", nil, "201"},
		{"koi", "legend", 137, nil, strings.Repeat("9", 128), strings.Repeat("9", 128)},
	} {
		batch := hostileOIDVariant("fb_", byte('m'+index), 'Q')
		hostileInsertFishingBatch(t, database, batch, owner, hostileOIDVariant("op_", byte('m'+index), 'Q'))
		hostileMustExec(t, database, `INSERT INTO game_fishing_outcomes(batch_id,ordinal,species_key,tier,size_cm,payout_milli) VALUES(?,0,?,?,?,0)`, batch, fixture.species, fixture.tier, fixture.size)
		if fixture.blue != nil || fixture.milk != nil {
			kind, length := "blue", fixture.blue
			if fixture.milk != nil {
				kind, length = "milk", fixture.milk
			}
			hostileMustExec(t, database, `INSERT INTO game_fishing_outcome_lengths(batch_id,ordinal,length_cm,kind) VALUES(?,0,?,?)`, batch, length, kind)
		}
		hostileMustExec(t, database, `INSERT INTO game_fishing_rank_facts VALUES(?,?,0,2592000,zeroblob(16),1)`, batch, owner)
		hostileMustExec(t, database, `INSERT INTO game_fishing_length_facts VALUES(?,0,?,?,?,0,?,?)`, batch, fixture.species, fixture.tier, fixture.size, fixture.blue, fixture.milk)
		var length string
		if err := database.QueryRow(`SELECT length_cm FROM game_fishing_recent_order WHERE batch_id_text=? AND user_id=? AND expires_at=2592000 AND caught_at=0 AND ordinal=0`, batch, owner).Scan(&length); err != nil || length != fixture.want {
			t.Fatal("projection changed exact length", length, err)
		}
		hostileMustFail(t, database, `UPDATE game_fishing_recent_order SET length_cm='999' WHERE batch_id_text=?`, batch)
		hostileMustExec(t, database, `DELETE FROM game_fishing_recent_order WHERE batch_id_text=?`, batch)
		hostileMustFail(t, database, `INSERT INTO game_fishing_recent_order VALUES(?,?,2592001,?,0,0)`, batch, owner, fixture.want)
		hostileMustFail(t, database, `INSERT INTO game_fishing_recent_order VALUES(?,?,2592000,'01',0,0)`, batch, owner)
		hostileMustExec(t, database, `INSERT INTO game_fishing_recent_order VALUES(?,?,2592000,?,0,0)`, batch, owner, fixture.want)
		hostileMustExec(t, database, `DELETE FROM game_fishing_rank_facts WHERE batch_id_text=?`, batch)
		var remaining int
		if err := database.QueryRow(`SELECT COUNT(*) FROM game_fishing_recent_order WHERE batch_id_text=?`, batch).Scan(&remaining); err != nil || remaining != 0 {
			t.Fatal("expired rank projection survived", remaining, err)
		}
		hostileMustExec(t, database, `DELETE FROM game_fishing_batches WHERE id=?`, batch)
	}
}
