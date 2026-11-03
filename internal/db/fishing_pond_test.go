package db

import (
	"database/sql"
	"testing"
)

func restoreSchema44LayoutForTest(t *testing.T, database *sql.DB) {
	t.Helper()
	hostileMustExec(t, database, "DROP TABLE attendance_awards; DROP TABLE attendance_sign_ins; DROP TABLE attendance_progress; DROP TABLE attendance_tiers; DROP TABLE attendance_periods;")
	hostileMustExec(t, database, "DROP INDEX idx_request_logs_operation_started")
	hostileMustExec(t, database, `DROP TRIGGER fishing_pond_queue_head_insert;
DROP TRIGGER fishing_pond_queue_head_delete;
DROP TABLE game_fishing_pond_queue_heads;`)
	hostileMustExec(t, database, `DROP TABLE browser_associations`)
	hostileMustExec(t, database, `DROP INDEX idx_lake_notes_live_lease;
DROP TRIGGER fishing_length_fact_recent_order;
DROP TABLE game_fishing_recent_order;
DROP TABLE game_fishing_pond_outcomes;
DROP TABLE game_fishing_pond_queue;
DROP TABLE game_fishing_pond_queue_state;
DROP TABLE game_fishing_pond_counts;
DROP TABLE game_fishing_pond_daily;
DROP TABLE game_fishing_pond_totals;`)
	assertRetainedManifest(t, database, Schema44ManifestHash)
}

func seedPondCatch(t *testing.T, database *sql.DB, owner int64, marker byte, species string) string {
	t.Helper()
	batch := hostileOIDVariant("fb_", marker, 'Q')
	hostileInsertFishingBatch(t, database, batch, owner, hostileOIDVariant("op_", marker, 'Q'))
	hostileMustExec(t, database, `INSERT INTO game_fishing_outcomes(batch_id,ordinal,species_key,tier,size_cm,payout_milli) VALUES(?,0,?,'junk',0,0)`, batch, species)
	return batch
}

func commitPondCatch(t *testing.T, database *sql.DB, batch string) {
	t.Helper()
	hostileMustExec(t, database, `UPDATE game_fishing_batches SET state='committed',payout_total_milli=0,settled_at=100,ledger_rows_remaining=zeroblob(16),next_attempt_at=NULL WHERE id=?`, batch)
}

func TestFishingPondSchemaEligibilityAndGiftIdentity(t *testing.T) {
	database := openGenerationTwoConstraintFixture(t)
	owner := hostileInsertUser(t, database, "pond-owner", 0, 0)
	donor := hostileInsertUser(t, database, "pond-donor", 0, 0)
	batch := seedPondCatch(t, database, owner, 'a', "fry")
	hostileMustExec(t, database, `INSERT INTO game_fishing_pond_outcomes(batch_id,ordinal) VALUES(?,0)`, batch)
	hostileMustFail(t, database, `INSERT INTO game_fishing_pond_outcomes(batch_id,ordinal) VALUES(?,0)`, batch)
	hostileMustFail(t, database, `UPDATE game_fishing_pond_outcomes SET gift_species_key='fry' WHERE batch_id=?`, batch)
	hostileMustFail(t, database, `UPDATE game_fishing_pond_outcomes SET gift_species_key='boot',donor_user_id=? WHERE batch_id=?`, donor, batch)
	hostileMustExec(t, database, `UPDATE game_fishing_pond_outcomes SET gift_species_key='boot',donor_user_id=?,donor_display_name='Original',donor_avatar_url='https://example.test/a.png' WHERE batch_id=?`, donor, batch)
	commitPondCatch(t, database, batch)
	hostileMustFail(t, database, `UPDATE game_fishing_pond_outcomes SET returned_at=101 WHERE batch_id=?`, batch)
	hostileMustFail(t, database, `UPDATE game_fishing_pond_outcomes SET gift_species_key='branch' WHERE batch_id=?`, batch)
	hostileMustFail(t, database, `UPDATE game_fishing_pond_outcomes SET donor_display_name='Changed' WHERE batch_id=?`, batch)
	hostileMustFail(t, database, `UPDATE game_fishing_pond_outcomes SET donor_user_id=NULL WHERE batch_id=?`, batch)
	hostileMustExec(t, database, `UPDATE users SET game_profile_public=0 WHERE id=?`, donor)
	var name string
	if err := database.QueryRow(`SELECT donor_display_name FROM game_fishing_pond_outcomes WHERE batch_id=?`, batch).Scan(&name); err != nil || name != "Original" {
		t.Fatalf("anonymous preference changed gift identity: %q %v", name, err)
	}
	hostileMustExec(t, database, `UPDATE game_fishing_pond_outcomes SET donor_user_id=NULL,donor_display_name=NULL,donor_avatar_url=NULL WHERE donor_user_id=?`, donor)
	hostileMustExec(t, database, `DELETE FROM users WHERE id=?`, donor)
	var gifts int
	if err := database.QueryRow(`SELECT COUNT(*) FROM game_fishing_pond_outcomes WHERE gift_species_key='boot' AND donor_user_id IS NULL AND donor_display_name IS NULL AND donor_avatar_url IS NULL`).Scan(&gifts); err != nil || gifts != 1 {
		t.Fatal("deleted donor was not anonymized", gifts, err)
	}

	old := seedPondCatch(t, database, owner, 'b', "boot")
	commitPondCatch(t, database, old)
	hostileMustFail(t, database, `INSERT INTO game_fishing_pond_outcomes(batch_id,ordinal) VALUES(?,0)`, old)
	own := seedPondCatch(t, database, owner, 'c', "boot")
	hostileMustExec(t, database, `INSERT INTO game_fishing_pond_outcomes(batch_id,ordinal) VALUES(?,0)`, own)
	commitPondCatch(t, database, own)
	hostileMustFail(t, database, `UPDATE game_fishing_pond_outcomes SET gift_species_key='boot' WHERE batch_id=?`, own)
	hostileMustExec(t, database, `UPDATE game_fishing_pond_outcomes SET returned_at=101 WHERE batch_id=?`, own)
	hostileMustFail(t, database, `UPDATE game_fishing_pond_outcomes SET returned_at=NULL WHERE batch_id=?`, own)
	hostileMustFail(t, database, `UPDATE game_fishing_pond_outcomes SET returned_at=102 WHERE batch_id=?`, own)
}

func TestFishingPondSchemaQueueRetentionAndCountBounds(t *testing.T) {
	database := openGenerationTwoConstraintFixture(t)
	owner := hostileInsertUser(t, database, "pond-owner", 0, 0)
	batch := seedPondCatch(t, database, owner, 'd', "boot")
	hostileMustExec(t, database, `INSERT INTO game_fishing_pond_outcomes(batch_id,ordinal) VALUES(?,0)`, batch)
	commitPondCatch(t, database, batch)
	queueSQL := `INSERT INTO game_fishing_pond_queue(donor_user_id,source_batch_id,source_ordinal,species_key,donor_display_name,returned_at) VALUES(?,?,?,?, 'Donor',101)`
	hostileMustFail(t, database, queueSQL, owner, batch, 0, "fry")
	hostileMustExec(t, database, queueSQL, owner, batch, 0, "boot")
	assertPondQueueCount(t, database, 1)
	hostileMustFail(t, database, queueSQL, owner, batch, 0, "boot")
	hostileMustFail(t, database, queueSQL, owner, batch, 10, "boot")
	hostileMustFail(t, database, `UPDATE game_fishing_pond_queue SET donor_display_name='Changed'`)
	tx, err := database.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(t.Context(), queueSQL, owner, batch, 1, "branch"); err != nil {
		t.Fatal(err)
	}
	var pendingCount int
	if err = tx.QueryRowContext(t.Context(), "SELECT item_count FROM game_fishing_pond_queue_state WHERE id=1").Scan(&pendingCount); err != nil || pendingCount != 2 {
		t.Fatal("queue insert was not counted in transaction", pendingCount, err)
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	assertPondQueueCount(t, database, 1)
	for _, board := range []string{"cleanup", "gifts"} {
		hostileMustExec(t, database, `INSERT INTO game_fishing_pond_counts VALUES(?,?,100,1)`, owner, board)
		hostileMustExec(t, database, `INSERT INTO game_fishing_pond_daily VALUES(?,?,0,1,100)`, owner, board)
		hostileMustExec(t, database, `INSERT INTO game_fishing_pond_totals VALUES(?,?,1,100,zeroblob(32))`, owner, board)
	}
	hostileMustFail(t, database, `UPDATE game_fishing_pond_counts SET item_count=0`)
	hostileMustFail(t, database, `UPDATE game_fishing_pond_counts SET item_count=1.5`)
	hostileMustFail(t, database, `UPDATE game_fishing_pond_totals SET item_count=9223372036854775807+1`)
	hostileMustFail(t, database, `UPDATE game_fishing_pond_totals SET public_tie_key=zeroblob(31)`)
	hostileMustFail(t, database, `UPDATE game_fishing_pond_counts SET board='unknown'`)
	hostileMustFail(t, database, `UPDATE game_fishing_pond_counts SET occurred_at=-1`)
	hostileMustFail(t, database, `UPDATE game_fishing_pond_daily SET day_start=1`)
	hostileMustFail(t, database, `UPDATE game_fishing_pond_daily SET last_occurred_at=86400`)
	hostileMustFail(t, database, `UPDATE game_fishing_pond_daily SET item_count=1.5`)
	hostileMustFail(t, database, `UPDATE game_fishing_pond_daily SET item_count=0`)
	hostileMustExec(t, database, `DELETE FROM game_fishing_batches WHERE id=?`, batch)
	hostileMustExec(t, database, `DELETE FROM game_fishing_pond_counts WHERE occurred_at<2592101`)
	for table, want := range map[string]int{"game_fishing_pond_outcomes": 0, "game_fishing_pond_counts": 0, "game_fishing_pond_queue": 1, "game_fishing_pond_totals": 2} {
		var got int
		if err := database.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&got); err != nil || got != want {
			t.Fatalf("%s after retention=%d want%d err%v", table, got, want, err)
		}
	}
	hostileMustExec(t, database, `DELETE FROM users WHERE id=?`, owner)
	assertPondQueueCount(t, database, 0)
	for _, table := range []string{"game_fishing_pond_queue", "game_fishing_pond_daily", "game_fishing_pond_totals"} {
		var got int
		if err := database.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&got); err != nil || got != 0 {
			t.Fatalf("%s after deletion=%d err%v", table, got, err)
		}
	}
}

func assertPondQueueCount(t *testing.T, database *sql.DB, want int) {
	t.Helper()
	var stored, actual int
	if err := database.QueryRow(`SELECT item_count,(SELECT COUNT(*) FROM game_fishing_pond_queue) FROM game_fishing_pond_queue_state WHERE id=1`).Scan(&stored, &actual); err != nil || stored != want || actual != want {
		t.Fatalf("queue count stored=%d actual=%d want=%d err=%v", stored, actual, want, err)
	}
}
