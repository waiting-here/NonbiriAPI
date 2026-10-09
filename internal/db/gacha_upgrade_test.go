package db

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

func TestGachaUpgradePreservesLedgerAndAuditBuckets(t *testing.T) {
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	database.SetMaxOpenConns(1)
	defer database.Close()
	hostileMustExec(t, database, "PRAGMA foreign_keys=ON;"+personalBalancedRoutingStorageSchema())
	assertRetainedManifest(t, database, personalBalancedRoutingManifestHash)
	user := hostileInsertUser(t, database, "upgrade-collector", 0, 0)
	wallet := hostileInsertAccount(t, database, "user", user, nil, 1, hostileBlob16(100), 0)
	id := hostileOID("op_")
	hostileInsertOperation(t, database, id, 1, "admin_user_adjustment", "operation", id)
	hostileMustExec(t, database, `INSERT INTO credit_entries(operation_id,line_no,account_id,account_kind_snapshot,delta_sign,delta_mag,balance_after_sign,balance_after_mag)
 VALUES(?,0,?,'user',1,?,NULL,NULL)`, id, wallet, hostileBlob16(100))
	hostileMustExec(t, database, `INSERT INTO economy_audit_buckets VALUES('general','admin_user_adjustment','operation','admin','day',0,0,'100','0','100','0','0',1,1,1)`)
	cancelled, cancel := context.WithCancel(t.Context())
	err = runGenerationTwoExtension(cancelled, database, func(ctx context.Context, tx *sql.Tx) error {
		if _, e := tx.ExecContext(ctx, gachaSQL); e != nil {
			return e
		}
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	assertForeignKeyEnforcement(t, database)
	assertRetainedManifest(t, database, personalBalancedRoutingManifestHash)
	for range 2 {
		if err = extendKnownGenerationTwoSchema(t.Context(), database); err != nil {
			t.Fatal(err)
		}
		assertForeignKeyEnforcement(t, database)
		assertRetainedManifest(t, database, PinnedGenerationTwoManifestHash)
		var kind, issued, income string
		var entries, buckets, version, profiles int
		if err = database.QueryRow(`SELECT credit_operations.kind,(SELECT count(*) FROM credit_entries WHERE operation_id=?),(SELECT count(*) FROM economy_audit_buckets),issued,user_income,(SELECT version FROM schema_state),(SELECT count(*) FROM gacha_profiles)
 FROM credit_operations,economy_audit_buckets WHERE id=?`, id, id).Scan(&kind, &entries, &buckets, &issued, &income, &version, &profiles); err != nil {
			t.Fatal(err)
		}
		if kind != "admin_user_adjustment" || entries != 1 || buckets != 1 || issued != "100" || income != "100" || version != 9 || profiles != 0 {
			t.Fatal(kind, entries, buckets, issued, income, version, profiles)
		}
		var foreignKeys int
		if err = database.QueryRow("SELECT count(*) FROM pragma_foreign_key_check").Scan(&foreignKeys); err != nil || foreignKeys != 0 {
			t.Fatal(foreignKeys, err)
		}
	}
	hostileMustExec(t, database, `INSERT INTO economy_audit_buckets VALUES('game','gacha_ticket_purchase','operation','gacha','day',0,0,'0','40','0','40','0',1,2,2)`)
	hostileMustFail(t, database, "DELETE FROM credit_operations WHERE id=?", id)
}
