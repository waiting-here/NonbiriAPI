package db

import (
	"context"
	"testing"
)

func TestLakePermanentUpgradePreservesHistory(t *testing.T) {
	d := supportedSourceFixture(t)
	uid := hostileInsertUser(t, d, "lake-history", 0, 0)
	period, cast, receipt, operation := hostileOID("lnp_"), hostileOID("lnc_"), hostileOID("lne_"), hostileOID("op_")
	hostileMustExec(t, d, "INSERT INTO lake_notes_periods(id,name,revision,status,starts_at,ends_at,entry_fee_milli,created_at,updated_at) VALUES(?,'Past lake',2,'published',1,2,125,0,0)", period)
	hostileMustExec(t, d, "INSERT INTO lake_notes_profiles(user_id,revision,rules_id,coin_mag,profile,updated_at) VALUES(?,7,'saved-rules',?,X'7b7d',1)", uid, hostileBlob16(123))
	hostileMustExec(t, d, "INSERT INTO lake_notes_casts(id,user_id,source_period_id,rules_id,phase,paused,generation,revision,last_tick,snapshot,reward_plan,held,active_elapsed_ns,created_at,updated_at) VALUES(?,?,?,'saved-rules','playing',1,3,4,180,X'7b7d',X'7b7d',0,3000000000,1,1)", cast, uid, period)
	hostileInsertOperation(t, d, operation, 1, "admin_user_adjustment", "operation", operation)
	hostileMustExec(t, d, "INSERT INTO lake_notes_entitlements(user_id,period_id,fee_milli,period_revision,operation_key_hash,ledger_operation_id,created_at) VALUES(?,?,125,2,zeroblob(32),?,1)", uid, period, operation)
	hostileMustExec(t, d, "INSERT INTO lake_notes_exchange_receipts(id,user_id,period_id,period_revision,direction,quantity_mag,source_lot,target_lot,source_amount_mag,target_amount_mag,operation_key_hash,ledger_operation_id,created_at) VALUES(?,?,?,2,'general_to_coin',?,?,?,?,?,zeroblob(32),?,1)", receipt, uid, period, hostileBlob16(2), hostileBlob16(125), hostileBlob16(3), hostileBlob16(250), hostileBlob16(6), operation)
	hostileMustExec(t, d, "UPDATE limited_activity_configs SET visible=1,paused=0 WHERE activity_key='lake-notes'")
	if err := extendKnownGenerationTwoSchema(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	var enabled, exchanges string
	if err := d.QueryRow("SELECT value FROM site_config WHERE key='game_lakenotes_enabled'").Scan(&enabled); err != nil || enabled != "1" {
		t.Fatal(enabled, err)
	}
	if err := d.QueryRow("SELECT value FROM site_config WHERE key='game_lakenotes_exchanges'").Scan(&exchanges); err != nil {
		t.Fatal(err)
	}
	for _, direction := range []string{"coins_to_general", "general_to_coins", "coins_to_game", "game_to_coins"} {
		var active int
		if err := d.QueryRow("SELECT json_extract(?,?)", exchanges, "$."+direction+".enabled").Scan(&active); err != nil || active != 0 {
			t.Fatal(direction, active, err)
		}
	}
	var revision, tick, storage int
	var source, coins, profile, snapshot string
	if err := d.QueryRow("SELECT revision,hex(coin_mag),hex(profile) FROM lake_notes_profiles WHERE user_id=?", uid).Scan(&revision, &coins, &profile); err != nil || revision != 7 || coins != "0000000000000000000000000000007B" || profile != "7B7D" {
		t.Fatal(revision, coins, profile, err)
	}
	if err := d.QueryRow("SELECT source_period_id,last_tick,storage_version,hex(snapshot) FROM lake_notes_casts WHERE id=?", cast).Scan(&source, &tick, &storage, &snapshot); err != nil || source != period || tick != 180 || storage != 1 || snapshot != "7B7D" {
		t.Fatal(source, tick, storage, snapshot, err)
	}
	var count int
	if err := d.QueryRow("SELECT count(*) FROM lake_notes_entitlements WHERE user_id=? AND fee_milli=125 AND ledger_operation_id=?", uid, operation).Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	if err := d.QueryRow("SELECT count(*) FROM lake_notes_exchange_receipts WHERE id=? AND period_id=? AND period_revision=2 AND config_revision IS NULL AND ledger_operation_id=?", receipt, period, operation).Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	assertForeignKeyEnforcement(t, d)
}
