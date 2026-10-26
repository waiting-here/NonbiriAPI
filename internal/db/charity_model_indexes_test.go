package db

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"
)

var charityModelDeletionIndexes = []string{
	"idx_charity_reservations_model", "idx_charity_dispatch_receipts_model",
	"idx_charity_affinities_model", "idx_charity_header_sessions_model", "idx_charity_header_uses_model",
}

func dropCharityModelIndexesForTest(t *testing.T, database *sql.DB) {
	t.Helper()
	for _, index := range charityModelDeletionIndexes {
		hostileMustExec(t, database, "DROP INDEX "+index)
	}
}

func TestCharityModelDeletionUsesScopedIndexes(t *testing.T) {
	database := openGenerationTwoConstraintFixture(t)
	plan := endpointKeyDeletePlan(t, database, "DELETE FROM charity_models WHERE id=?")
	for _, index := range charityModelDeletionIndexes {
		if !strings.Contains(plan, index) {
			t.Fatalf("model deletion does not use %s: %s", index, plan)
		}
	}
	if strings.Contains(plan, "SCAN ") {
		t.Fatal("model deletion scans unrelated rows", plan)
	}
	for _, query := range []string{"DELETE FROM charity_model_bindings WHERE charity_model_id=?", "DELETE FROM charity_binding_targets WHERE binding_id=?"} {
		if plan := endpointKeyDeletePlan(t, database, query); strings.Contains(plan, "SCAN ") {
			t.Fatal("model cascade scans unrelated rows", query, plan)
		}
	}
}

func TestCharityModelDeletionRetainsHistoryAndUnrelatedRows(t *testing.T) {
	database := openGenerationTwoConstraintFixture(t)
	key := seedEndpointKeyHistory(t, database, bootstrapTestVault(t), 'A')
	user := hostileInsertUser(t, database, "model-deletion", 0, 0)
	hostileMustExec(t, database, `INSERT INTO charity_routing_capacity(id,affinities,buckets,revision) VALUES(1,0,0,1)`)
	models := make([]int64, 2)
	requests := make([]string, 2)
	attempts := make([]string, 2)
	for i := range models {
		name := fmt.Sprintf("model%d", i)
		models[i] = hostileMustLastID(t, hostileMustExec(t, database, `INSERT INTO charity_models(provider,model,full_name,pricing_mode,created_at,updated_at) VALUES('delete',?,?,'per_request',0,0)`, name, "[公益]delete/"+name))
		requests[i] = hostileOIDVariant("req_", byte('B'+i), 'Q')
		attempts[i] = hostileOIDVariant("clm_", byte('B'+i), 'Q')
		hostileInsertLogicalRequest(t, database, requests[i], user, "charity_chat_completions", 1)
		hostileMustExec(t, database, `INSERT INTO charity_reservations(logical_request_id,user_id,charity_model_id,model_snapshot,state,pricing_mode,discount_percent,request_user_price_milli,request_donor_reward_milli,uncached_user_price_milli,cache_write_user_price_milli,cache_read_user_price_milli,output_user_price_milli,uncached_donor_reward_milli,cache_write_donor_reward_milli,cache_read_donor_reward_milli,output_donor_reward_milli,token_reserve_milli,user_reserved_milli,original_charge_milli,user_charge_milli,donor_reward_total_mag,created_at,updated_at) VALUES(?,?,?,'saved-model','reserved','per_request',100,7,0,0,0,0,0,0,0,0,0,0,0,0,0,zeroblob(16),0,0)`, requests[i], user, models[i])
		hostileMustExec(t, database, `INSERT INTO charity_dispatch_receipts(attempt_id,physical_id,endpoint_key_id,user_id,model_id,routing_revision,state,reserved_at,expires_at) VALUES(?,zeroblob(32),?,?,?,1,'reserved',0,300)`, attempts[i], key, user, models[i])
		hostileMustExec(t, database, `INSERT INTO charity_key_affinities(user_id,model_id,endpoint_key_id,physical_id,attempt_id,routing_revision,dispatched_at,expires_at) VALUES(?,?,?,zeroblob(32),?,1,0,300)`, user, models[i], key, attempts[i])
		hostileMustExec(t, database, `INSERT INTO charity_header_sessions(user_id,model_id,name,value,format,idle_seconds,expires_at) VALUES(?,?,'header','12345678901234567890123456789012','hex_32',300,300)`, user, models[i])
		hostileMustExec(t, database, `INSERT INTO charity_header_uses(logical_request_id,user_id,model_id,name,value,idle_seconds,sent) VALUES(?,?,?,'header','12345678901234567890123456789012',300,1)`, requests[i], user, models[i])
	}
	hostileMustExec(t, database, "DELETE FROM charity_models WHERE id=?", models[0])
	for i := range models {
		var model sql.NullInt64
		var snapshot, state string
		var price int64
		if err := database.QueryRow(`SELECT charity_model_id,model_snapshot,state,request_user_price_milli FROM charity_reservations WHERE logical_request_id=?`, requests[i]).Scan(&model, &snapshot, &state, &price); err != nil {
			t.Fatal(err)
		}
		if model.Valid != (i == 1) || i == 1 && model.Int64 != models[i] || snapshot != "saved-model" || state != "reserved" || price != 7 {
			t.Fatal("reservation history changed", i, model, snapshot, state, price)
		}
		if err := database.QueryRow(`SELECT model_id,state FROM charity_dispatch_receipts WHERE attempt_id=?`, attempts[i]).Scan(&model, &state); err != nil {
			t.Fatal(err)
		}
		if model.Valid != (i == 1) || i == 1 && model.Int64 != models[i] || state != "reserved" {
			t.Fatal("dispatch history changed", i, model, state)
		}
		for _, table := range []string{"charity_key_affinities", "charity_header_sessions", "charity_header_uses"} {
			var count int
			if err := database.QueryRow("SELECT count(*) FROM "+table+" WHERE model_id=?", models[i]).Scan(&count); err != nil || count != i {
				t.Fatal("cascade crossed model boundary", table, i, count, err)
			}
		}
	}
	var affinities int
	if err := database.QueryRow("SELECT affinities FROM charity_routing_capacity WHERE id=1").Scan(&affinities); err != nil || affinities != 1 {
		t.Fatal("capacity changed incorrectly", affinities, err)
	}
	rows, err := database.Query("PRAGMA foreign_key_check")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() || rows.Err() != nil {
		t.Fatal("invalid retained references", rows.Err())
	}
}
