package db

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func deployedStorageFixture(t *testing.T) *sql.DB {
	t.Helper()
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	database.SetMaxOpenConns(1)
	t.Cleanup(func() { database.Close() })
	hostileMustExec(t, database, "PRAGMA foreign_keys=ON;"+deployedGenerationTwoSchema)
	assertRetainedManifest(t, database, preStorageContractsManifestHash)
	tx, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := seedGenerationTwo(context.Background(), tx, hostileOID("b1e_")); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return database
}

func seedStorageRebuildReferences(t *testing.T, database *sql.DB) {
	t.Helper()
	_, _, _ = seedBindingOrderFixture(t, database, 3)
	admin := hostileInsertUser(t, database, "storage-admin", 1, 0)
	donor := hostileInsertUser(t, database, "storage-donor", 0, 0)
	donation := hostileInsertDonation(t, database, donor)
	review := hostileInsertDonationReview(t, database, donation, admin)
	hostileMustExec(t, database, "UPDATE sqlite_sequence SET seq=9000 WHERE name='donation_reviews'")
	hostileMustExec(t, database, "DELETE FROM donation_reviews WHERE id=?", review)
	hostileInsertDonationReview(t, database, donation, admin)
	hostileMustExec(t, database, "UPDATE sqlite_sequence SET seq=12000 WHERE name='donation_reviews'")
	hostileMustExec(t, database, "INSERT INTO idempotency_records(scope,actor_scope_hash,key_hash,request_hash,state,http_status,response_body,created_at,expires_at) VALUES('control_mutation',zeroblob(32),zeroblob(32),zeroblob(32),'completed',200,x'7B7D',0,86400)")
	scan := hostileOID("scn_")
	hostileMustExec(t, database, "INSERT INTO risk_client_scans(id,user_id,admin,request_token,query_json,rules_json,state,from_at,to_at,call_kind,model,upper_log_id,after_at,candidates,created_at,updated_at,expires_at) VALUES(?,?,1,'abcdefghijklmnop','{}','[]','completed',0,100,'total','',0,0,0,0,0,86400)", scan, admin)
	hostileMustExec(t, database, "INSERT INTO risk_scan_results(scan_id,row_no,user_id,result_json) VALUES(?,1,?,'{}')", scan, donor)
	hostileMustExec(t, database, "INSERT INTO risk_scan_result_users VALUES(?,1,?)", scan, donor)
	hostileMustExec(t, database, "INSERT INTO fatfish_levels(id,title,description,draft_json,revision,created_at,updated_at) VALUES(?,'Level','','{}',1,0,0)", hostileOID("ffl_"))
	hostileMustExec(t, database, "INSERT INTO fatfish_level_versions VALUES(?,?,zeroblob(32),2,1,'{}',10,3,0)", hostileOID("ffv_"), hostileOID("ffl_"))
	hostileMustExec(t, database, "INSERT INTO fatfish_periods(id,title,description,state,starts_at,ends_at,revision,created_at,updated_at) VALUES(?,'Period','','draft',0,1000,1,0,0)", hostileOID("ffp_"))
	tx, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec("INSERT INTO fatfish_nodes VALUES(?,?,'Node','',0,0,0,1)", hostileOID("ffn_"), hostileOID("ffp_")); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec("INSERT INTO fatfish_node_revisions VALUES(?,1,?,'{}',0,zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),0)", hostileOID("ffn_"), hostileOID("ffv_")); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func assertForeignKeyEnforcement(t *testing.T, database *sql.DB) {
	t.Helper()
	var enabled int
	if err := database.QueryRow("PRAGMA foreign_keys").Scan(&enabled); err != nil || enabled != 1 {
		t.Fatalf("foreign keys=%d: %v", enabled, err)
	}
}

func TestStorageContractsUpgradeRetainsRowsReferencesAndSchema(t *testing.T) {
	database := deployedStorageFixture(t)
	seedStorageRebuildReferences(t, database)
	manifest, err := readGenerationManifest(context.Background(), database)
	if err != nil {
		t.Fatal(err)
	}
	before := interactionTableDigests(t, database, manifest)
	for range 2 {
		if err := extendKnownGenerationTwoSchema(context.Background(), database); err != nil {
			t.Fatal(err)
		}
		assertForeignKeyEnforcement(t, database)
		assertRetainedManifest(t, database, PinnedGenerationTwoManifestHash)
		if after := interactionTableDigests(t, database, manifest); !reflect.DeepEqual(before, after) {
			t.Fatal("deployed facts changed")
		}
		if err := foreignKeyCheck(context.Background(), database); err != nil {
			t.Fatal(err)
		}
		var integrity string
		if err := database.QueryRow("PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
			t.Fatal(integrity, err)
		}
	}
	var highWater int
	if err := database.QueryRow("SELECT seq FROM sqlite_sequence WHERE name='donation_reviews'").Scan(&highWater); err != nil || highWater != 12000 {
		t.Fatal("autoincrement high water changed", highWater, err)
	}
	fresh := openGenerationTwoDDLForTest(t)
	want, err := readGenerationManifest(context.Background(), fresh)
	if err != nil {
		t.Fatal(err)
	}
	got, err := readGenerationManifest(context.Background(), database)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatal("fresh/upgrade objects differ", err)
	}
	var policy string
	if err := database.QueryRow("SELECT role_policy FROM models LIMIT 1").Scan(&policy); err != nil || policy != "{\"default_action\":\"native\",\"rules\":{}}" {
		t.Fatal("old model default changed", policy, err)
	}
	hostileMustFail(t, database, "UPDATE fatfish_level_versions SET content_json='{\"changed\":true}'")
	hostileMustFail(t, database, "INSERT INTO risk_scan_results(scan_id,row_no,user_id,result_json) VALUES('missing',2,1,'{}')")
}

func TestStorageContractsRebuildFailureRollsBackAndRestoresForeignKeys(t *testing.T) {
	database := deployedStorageFixture(t)
	seedStorageRebuildReferences(t, database)
	manifest, err := readGenerationManifest(context.Background(), database)
	if err != nil {
		t.Fatal(err)
	}
	before := interactionTableDigests(t, database, manifest)
	// Force failure after the first parent has already been rebuilt.
	original := storageContractTableChanges[1].before
	storageContractTableChanges[1].before = "constraint_that_does_not_exist"
	defer func() { storageContractTableChanges[1].before = original }()
	if err := extendKnownGenerationTwoSchema(context.Background(), database); err == nil {
		t.Fatal("mid-migration failure accepted")
	}
	assertForeignKeyEnforcement(t, database)
	assertRetainedManifest(t, database, preStorageContractsManifestHash)
	if after := interactionTableDigests(t, database, manifest); !reflect.DeepEqual(before, after) {
		t.Fatal("failed migration changed rows")
	}
	var temporary int
	if err := database.QueryRow("SELECT count(*) FROM sqlite_temp_schema WHERE name='storage_contract_saved_rows'").Scan(&temporary); err != nil || temporary != 0 {
		t.Fatal("temporary migration state leaked", temporary, err)
	}
}

func TestStorageContractsRejectsCancelledAndUnknownSource(t *testing.T) {
	database := deployedStorageFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := extendKnownGenerationTwoSchema(ctx, database); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled migration", err)
	}
	assertForeignKeyEnforcement(t, database)
	assertRetainedManifest(t, database, preStorageContractsManifestHash)
	hostileMustExec(t, database, "CREATE INDEX unexpected_storage_index ON donations(status)")
	if err := extendKnownGenerationTwoSchema(context.Background(), database); err == nil {
		t.Fatal("unknown source accepted")
	}
	assertForeignKeyEnforcement(t, database)
	var columns int
	if err := database.QueryRow("SELECT count(*) FROM pragma_table_info('models') WHERE name='role_policy'").Scan(&columns); err != nil || columns != 0 {
		t.Fatal("unknown source mutated", columns, err)
	}
}

func TestStorageContractsCancelledTransactionRestoresForeignKeys(t *testing.T) {
	database := deployedStorageFixture(t)
	seedStorageRebuildReferences(t, database)
	manifest, err := readGenerationManifest(context.Background(), database)
	if err != nil {
		t.Fatal(err)
	}
	before := interactionTableDigests(t, database, manifest)
	for _, stage := range []string{"statement", "commit"} {
		t.Run(stage, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			entered := false
			err := runGenerationTwoExtension(ctx, database, func(ctx context.Context, tx *sql.Tx) error {
				// Reach a real schema mutation in the active transaction before
				// cancellation. No wall-clock deadline chooses the test phase.
				change := storageContractTableChanges[0]
				if err := rebuildStorageContractTable(ctx, tx, change.table, change.before, change.after); err != nil {
					return err
				}
				var definition string
				if err := tx.QueryRowContext(ctx, "SELECT sql FROM sqlite_schema WHERE name='idempotency_records'").Scan(&definition); err != nil {
					return err
				}
				if !strings.Contains(definition, "'lake_notes','personal_automation'") {
					t.Fatal("cancellation did not reach the rebuilt table")
				}
				entered = true
				cancel()
				if stage == "statement" {
					return extendGenerationTwoTransaction(ctx, tx)
				}
				return nil // The transaction owner must check cancellation before commit.
			})
			if !entered || !errors.Is(err, context.Canceled) {
				t.Fatalf("entered=%v cancellation=%v", entered, err)
			}
			assertForeignKeyEnforcement(t, database)
			assertRetainedManifest(t, database, preStorageContractsManifestHash)
			if after := interactionTableDigests(t, database, manifest); !reflect.DeepEqual(before, after) {
				t.Fatal("cancelled migration changed rows")
			}
		})
	}
	if err := extendKnownGenerationTwoSchema(context.Background(), database); err != nil {
		t.Fatal("retry after completed rollback", err)
	}
	assertForeignKeyEnforcement(t, database)
}
