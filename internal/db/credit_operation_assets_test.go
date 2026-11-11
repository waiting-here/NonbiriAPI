package db

import (
	"database/sql"
	"testing"
)

func restoreSchema52LayoutForTest(t *testing.T, database *sql.DB) {
	t.Helper()
	restoreSchema53LayoutForTest(t, database)
	hostileMustExec(t, database, `DROP TRIGGER credit_operation_assets_insert;
DROP TRIGGER credit_operation_assets_delete;
DROP TABLE credit_operation_assets;
DROP INDEX idx_request_logs_user_started;
CREATE INDEX idx_request_logs_user_started ON request_logs(user_id,started_at,id);
DROP INDEX idx_image_activity_tasks_user;
CREATE INDEX idx_image_activity_tasks_user ON image_activity_tasks(user_id,created_at,id);
DROP INDEX idx_image_activity_tasks_unfinished;
CREATE INDEX idx_image_activity_tasks_unfinished ON image_activity_tasks(user_id,finance_state,accepted_seq);`)
	assertRetainedManifest(t, database, Schema52ManifestHash)
}

func TestCreditOperationAssetsFollowRetainedEntries(t *testing.T) {
	database := openGenerationTwoDDLForTest(t)
	owner := hostileInsertUser(t, database, "asset-index", 0, 0)
	account := hostileInsertAccount(t, database, "user", owner, nil, 0, hostileBlob16(0), 0)
	id := hostileOID("op_")
	hostileInsertOperation(t, database, id, 1, "admin_user_adjustment", "operation", id)
	count := func(want int) {
		t.Helper()
		var got int
		if err := database.QueryRow(`SELECT count(*) FROM credit_operation_assets WHERE operation_id=?`, id).Scan(&got); err != nil || got != want {
			t.Fatalf("assets=%d, want=%d, err=%v", got, want, err)
		}
	}
	count(0)
	for line := range 2 {
		hostileMustExec(t, database, `INSERT INTO credit_entries(operation_id,line_no,account_id,account_kind_snapshot,delta_sign,delta_mag)
VALUES(?, ?, ?, 'user',1,?)`, id, line, account, hostileBlob16(1))
	}
	count(1)
	hostileMustExec(t, database, `INSERT INTO credit_entries(operation_id,line_no,account_kind_snapshot,asset_type,delta_sign,delta_mag)
VALUES(?,2,'user','game',1,?)`, id, hostileBlob16(1))
	count(2)
	hostileMustExec(t, database, `DELETE FROM credit_accounts WHERE id=?`, account)
	count(2)
	var seq, at int64
	var kind string
	if err := database.QueryRow(`SELECT ledger_seq,created_at,kind FROM credit_operation_assets WHERE operation_id=? AND asset_type='game'`, id).Scan(&seq, &at, &kind); err != nil || seq != 1 || at != 0 || kind != "admin_user_adjustment" {
		t.Fatal(seq, at, kind, err)
	}
	// Retention may retain an operation after compacting all its entries.
	hostileMustExec(t, database, `UPDATE credit_compaction SET through_seq=1 WHERE id=1`)
	hostileMustExec(t, database, `UPDATE credit_operations SET compacted=1 WHERE id=?`, id)
	hostileMustExec(t, database, `DELETE FROM credit_entries WHERE operation_id=? AND line_no=0`, id)
	count(2)
	hostileMustExec(t, database, `DELETE FROM credit_entries WHERE operation_id=? AND line_no=1`, id)
	count(1)
	hostileMustExec(t, database, `DELETE FROM credit_entries WHERE operation_id=? AND line_no=2`, id)
	count(0)
	hostileMustExec(t, database, `DELETE FROM credit_operations WHERE id=?`, id)
}
