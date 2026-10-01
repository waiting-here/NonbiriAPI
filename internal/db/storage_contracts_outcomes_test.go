package db

import (
	"context"
	"database/sql"
	"testing"
)

func TestStorageContractsRolePolicyAuditValues(t *testing.T) {
	database := openGenerationTwoConstraintFixture(t)
	actor := hostileInsertUser(t, database, "policy-actor", 0, 0)
	insert := `INSERT INTO policy_audits(actor_user_id,actor_role,resource_type,resource_id,policy,old_value,new_value,created_at,from_revision,to_revision) VALUES(?,'owner','model',1,?,?,?,0,?,?)`
	hostileMustExec(t, database, insert, actor, "flatten_tool_calls", 0, 1, nil, nil)
	hostileMustExec(t, database, insert, actor, "role_policy", nil, nil, 0, 1)
	for _, values := range [][]any{
		{"flatten_tool_calls", nil, 1, nil, nil},
		{"flatten_tool_calls", 0, 1, 0, 1},
		{"role_policy", 0, 1, 0, 1},
		{"role_policy", nil, nil, nil, nil},
		{"role_policy", nil, nil, 3, 3},
		{"role_policy", nil, nil, 3, 5},
	} {
		hostileMustFail(t, database, insert, append([]any{actor}, values...)...)
	}
	hostileMustFail(t, database, "UPDATE policy_audits SET to_revision=2 WHERE policy='role_policy'")
	hostileMustFail(t, database, "UPDATE policy_audits SET old_value=0 WHERE policy='role_policy'")
	hostileMustFail(t, database, "UPDATE policy_audits SET actor_user_id=NULL WHERE actor_user_id=?", actor)
	hostileMustExec(t, database, "DELETE FROM users WHERE id=?", actor)
	var count int
	if err := database.QueryRow("SELECT count(*) FROM policy_audits WHERE actor_user_id IS NULL").Scan(&count); err != nil || count != 2 {
		t.Fatal("audit anonymization", count, err)
	}
	hostileMustFail(t, database, "UPDATE policy_audits SET id=id+10 WHERE actor_user_id IS NULL")
}

func insertLegacyOutcomeFixture(t *testing.T, database *sql.DB) (string, string) {
	t.Helper()
	user := hostileInsertUser(t, database, "outcome-user", 0, 0)
	request := hostileOID("req_")
	hostileInsertLogicalRequest(t, database, request, user, "openai_chat_completions", 3)
	hostileMustExec(t, database, "UPDATE logical_requests SET ledger_rows_remaining=zeroblob(16) WHERE id=?", request)
	base := hostileOID("clm_")
	success := base[:len(base)-1] + "g"
	for seq, id := range []string{base, success} {
		hostileMustExec(t, database, `INSERT INTO dispatch_claims(id,logical_request_id,attempt_seq,purpose,claim_now,state,dispatched_at,terminal_at,donor_reward_state) VALUES(?,?,?,'self',0,'committed',0,1,'not_applicable')`, id, request, seq+1)
		hostileMustExec(t, database, `INSERT INTO donation_usage_reservations(claim_id,streak_generation,claim_seq,price_reserved_milli,price_actual_milli,reward_actual_milli,calls_reserved,calls_actual,tokens_reserved,tokens_actual,protocol_success,usage_unknown,state,created_at,finalized_at) VALUES(?,zeroblob(16),zeroblob(16),0,0,0,1,1,0,0,?,0,'committed',0,1)`, id, seq)
	}
	return base, success
}

func TestStorageContractsLegacyTerminalClassification(t *testing.T) {
	database := deployedStorageFixture(t)
	failed, success := insertLegacyOutcomeFixture(t, database)
	if err := extendKnownGenerationTwoSchema(context.Background(), database); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"dispatch_claims", "donation_usage_reservations"} {
		idColumn := "id"
		if table == "donation_usage_reservations" {
			idColumn = "claim_id"
		}
		for id, want := range map[string]string{failed: "neutral/legacy_unknown", success: "success/none"} {
			var got string
			if err := database.QueryRow("SELECT streak_disposition||'/'||failure_origin FROM "+table+" WHERE "+idColumn+"=?", id).Scan(&got); err != nil || got != want {
				t.Fatal(table, id, got, err)
			}
		}
		hostileMustFail(t, database, "UPDATE "+table+" SET streak_disposition=NULL WHERE "+idColumn+"=?", failed)
		hostileMustFail(t, database, "UPDATE "+table+" SET streak_disposition='upstream_failure',failure_origin='client_cancel' WHERE "+idColumn+"=?", failed)
		hostileMustExec(t, database, "UPDATE "+table+" SET streak_disposition='upstream_failure',failure_origin='network' WHERE "+idColumn+"=?", failed)
	}
	hostileMustFail(t, database, "UPDATE donation_usage_reservations SET streak_disposition='success',failure_origin='none' WHERE claim_id=?", failed)
	hostileMustFail(t, database, "UPDATE donation_usage_reservations SET streak_disposition='neutral',failure_origin='client_cancel' WHERE claim_id=?", success)
	if err := extendKnownGenerationTwoSchema(context.Background(), database); err != nil {
		t.Fatal(err)
	}
	var retained string
	if err := database.QueryRow("SELECT failure_origin FROM donation_usage_reservations WHERE claim_id=?", failed).Scan(&retained); err != nil || retained != "network" {
		t.Fatal("restart reclassified an existing outcome", retained, err)
	}
}

func TestStorageContractsPersistedLevelVersionNumbers(t *testing.T) {
	database := deployedStorageFixture(t)
	seedStorageRebuildReferences(t, database)
	first := hostileOID("ffv_")
	second := first[:len(first)-1] + "g"
	level := hostileOID("ffl_")
	hostileMustExec(t, database, "INSERT INTO fatfish_level_versions VALUES(?,?,?,2,1,'{}',10,3,0)", second, level, hostileBlob32(1))
	if err := extendKnownGenerationTwoSchema(context.Background(), database); err != nil {
		t.Fatal(err)
	}
	rows, err := database.Query("SELECT id,version_number FROM fatfish_level_versions ORDER BY version_number")
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for rows.Next() {
		var id string
		var number int
		if err := rows.Scan(&id, &number); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
		if number != len(ids) {
			t.Fatal("version sequence", number)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	if len(ids) != 2 || ids[0] != first || ids[1] != second {
		t.Fatal("stable tie ordering", ids)
	}
	hostileMustFail(t, database, "UPDATE fatfish_level_versions SET version_number=20 WHERE id=?", first)
	third := first[:len(first)-1] + "w"
	insert := `INSERT INTO fatfish_level_versions(id,level_id,content_hash,engine_version,scoring_version,content_json,duration_seconds,maximum_stars,created_at,version_number) VALUES(?,?,?,3,1,'{}',10,3,1,?)`
	hostileMustFail(t, database, insert, third, level, hostileBlob32(2), 1)
	hostileMustExec(t, database, insert, third, level, hostileBlob32(2), 3)
	if err := extendKnownGenerationTwoSchema(context.Background(), database); err != nil {
		t.Fatal(err)
	}
	var number int
	if err := database.QueryRow("SELECT version_number FROM fatfish_level_versions WHERE id=?", third).Scan(&number); err != nil || number != 3 {
		t.Fatal(number, err)
	}
}
