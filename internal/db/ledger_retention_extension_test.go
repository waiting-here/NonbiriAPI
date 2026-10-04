package db

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

func TestLedgerRetentionUpgradePreservesCurrentLedgerAndRollsBack(t *testing.T) {
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	database.SetMaxOpenConns(1)
	hostileMustExec(t, database, "PRAGMA foreign_keys=ON;"+preLedgerRetentionSchema())
	assertRetainedManifest(t, database, preLedgerRetentionManifestHash)
	ctx := context.Background()
	tx, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err = seedGenerationTwo(ctx, tx, hostileOID("b1e_")); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	user := hostileInsertUser(t, database, "retention", 0, 0)
	wallet := hostileInsertAccount(t, database, "user", user, nil, 1, hostileBlob16(100), 0)
	var external int64
	if err = database.QueryRow(`SELECT id FROM credit_accounts WHERE code='external' AND asset_type='general'`).Scan(&external); err != nil {
		t.Fatal(err)
	}
	hostileMustExec(t, database, `UPDATE credit_accounts SET balance_sign=-1,balance_mag=? WHERE id=?`, hostileBlob16(100), external)
	id := hostileOID("op_")
	hostileInsertOperation(t, database, id, 1, "admin_user_adjustment", "operation", id)
	hostileMustExec(t, database, `INSERT INTO credit_entries(operation_id,line_no,account_id,account_kind_snapshot,delta_sign,delta_mag,balance_after_sign,balance_after_mag) VALUES(?,0,?,'external',-1,?,-1,?),(?,1,?,'user',1,?,NULL,NULL)`, id, external, hostileBlob16(100), hostileBlob16(100), id, wallet, hostileBlob16(100))
	hostileMustExec(t, database, `UPDATE credit_capacity SET last_ledger_seq=1 WHERE id=1`)
	cancelled, cancel := context.WithCancel(ctx)
	err = runGenerationTwoExtension(cancelled, database, func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, ledgerRetentionSQL); err != nil {
			return err
		}
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	assertForeignKeyEnforcement(t, database)
	assertRetainedManifest(t, database, preLedgerRetentionManifestHash)
	for range 2 {
		if err = extendKnownGenerationTwoSchema(ctx, database); err != nil {
			t.Fatal(err)
		}
		assertForeignKeyEnforcement(t, database)
		assertRetainedManifest(t, database, PinnedGenerationTwoManifestHash)
		var seq, operations, entries, through, opening, compacted int
		if err = database.QueryRow(`SELECT (SELECT last_ledger_seq FROM credit_capacity),(SELECT count(*) FROM credit_operations),(SELECT count(*) FROM credit_entries),(SELECT through_seq FROM credit_compaction),(SELECT count(*) FROM credit_opening_balances),(SELECT compacted FROM credit_operations WHERE id=?)`, id).Scan(&seq, &operations, &entries, &through, &opening, &compacted); err != nil {
			t.Fatal(err)
		}
		if seq != 1 || operations != 1 || entries != 2 || through != 0 || opening != 0 || compacted != 0 {
			t.Fatal(seq, operations, entries, through, opening, compacted)
		}
	}
}
