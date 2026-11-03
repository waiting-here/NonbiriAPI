package db

import (
	"fmt"
	"testing"
)

func TestFishingPondQueueHeadsFollowInsertDeleteAndCascade(t *testing.T) {
	database := openGenerationTwoConstraintFixture(t)
	owner := hostileInsertUser(t, database, "head-owner", 0, 0)
	other := hostileInsertUser(t, database, "head-other", 0, 0)
	insert := `INSERT INTO game_fishing_pond_queue(id,donor_user_id,source_batch_id,source_ordinal,species_key,donor_display_name,returned_at) VALUES(?,?,?,0,'boot','Donor',100)`
	// Explicit IDs exercise out-of-order inserts as well as normal appends.
	for _, entry := range []struct{ id, donor int64 }{{20, owner}, {30, owner}, {10, owner}, {40, other}, {50, owner}, {60, other}} {
		hostileMustExec(t, database, insert, entry.id, entry.donor, fmt.Sprintf("fb_%021dA", entry.id))
	}
	assertHeads := func() {
		t.Helper()
		for _, query := range []string{
			`SELECT donor_user_id,MIN(id) FROM game_fishing_pond_queue GROUP BY donor_user_id EXCEPT SELECT donor_user_id,queue_id FROM game_fishing_pond_queue_heads`,
			`SELECT donor_user_id,queue_id FROM game_fishing_pond_queue_heads EXCEPT SELECT donor_user_id,MIN(id) FROM game_fishing_pond_queue GROUP BY donor_user_id`,
		} {
			var mismatch bool
			if err := database.QueryRow("SELECT EXISTS(" + query + ")").Scan(&mismatch); err != nil || mismatch {
				t.Fatal("queue heads drifted", mismatch, err)
			}
		}
	}
	assertHeads()
	// Removing a non-head leaves the head in place; a multi-row delete must
	// advance through every removed head and settle on the first survivor.
	hostileMustExec(t, database, `DELETE FROM game_fishing_pond_queue WHERE id=30`)
	assertHeads()
	tx, err := database.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(`DELETE FROM game_fishing_pond_queue WHERE id IN (10,20,40)`); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	var head int64
	if err = tx.QueryRow(`SELECT queue_id FROM game_fishing_pond_queue_heads WHERE donor_user_id=?`, owner).Scan(&head); err != nil || head != 50 {
		tx.Rollback()
		t.Fatalf("transaction head=%d err=%v", head, err)
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	assertHeads()
	assertPondQueueCount(t, database, 5)
	hostileMustExec(t, database, `DELETE FROM game_fishing_pond_queue WHERE id IN (10,20,40)`)
	assertHeads()
	assertPondQueueCount(t, database, 2)
	hostileMustExec(t, database, `DELETE FROM game_fishing_pond_queue WHERE donor_user_id=?`, owner)
	assertHeads()
	hostileMustExec(t, database, insert, 70, other, fmt.Sprintf("fb_%021dA", 70))
	// A parent cascade may retire the head row before the queue rows. The
	// queue trigger must not recreate a head for the deleted user.
	hostileMustExec(t, database, `DELETE FROM users WHERE id=?`, other)
	assertHeads()
	assertPondQueueCount(t, database, 0)
}
