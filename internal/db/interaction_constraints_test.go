package db

import (
	"context"
	"database/sql"
	"strings"
	"testing"
)

func interactionFishFixture(t *testing.T) (*sql.DB, int64) {
	t.Helper()
	database := openGenerationTwoConstraintFixture(t)
	t.Cleanup(func() { _ = database.Close() })
	user := hostileInsertUser(t, database, "fish", 0, 1)
	hostileMustExec(t, database, `INSERT INTO fatfish_capacity VALUES(1,0,0)`)
	hostileMustExec(t, database, `INSERT INTO fatfish_levels(id,title,description,draft_json,revision,created_at,updated_at) VALUES(?,'Level','','{}',1,0,0)`, hostileOID("ffl_"))
	hostileMustExec(t, database, `INSERT INTO fatfish_level_versions(id,level_id,content_hash,engine_version,scoring_version,content_json,duration_seconds,maximum_stars,created_at) VALUES(?,?,zeroblob(32),1,1,'{}',10,3,0)`, hostileOID("ffv_"), hostileOID("ffl_"))
	hostileMustExec(t, database, `INSERT INTO fatfish_periods(id,title,description,state,starts_at,ends_at,revision,created_at,updated_at) VALUES(?,'Period','','draft',0,1000,1,0,0)`, hostileOID("ffp_"))
	tx, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO fatfish_nodes VALUES(?,?,'Node','',0,0,0,1)`, hostileOID("ffn_"), hostileOID("ffp_")); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`INSERT INTO fatfish_node_revisions VALUES(?,1,?,'{}',0,zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),0)`, hostileOID("ffn_"), hostileOID("ffv_")); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return database, user
}

func TestInteractionFishFinancialAndScoreInvariants(t *testing.T) {
	database, user := interactionFishFixture(t)
	challenge := hostileOID("ffc_")
	hostileMustExec(t, database, `INSERT INTO fatfish_challenges(id,user_id,playtest,period_id,node_id,node_revision,version_id,tab_capability_hash,seed,seed_commit,state,prepared_at_ms,prepare_until_ms,ticket_price_mag,revision)
 VALUES(?,?,0,?,?,1,?,zeroblob(32),zeroblob(32),zeroblob(32),'prepared',0,60000,?,1)`, challenge, user, hostileOID("ffp_"), hostileOID("ffn_"), hostileOID("ffv_"), hostileBlob16(1))
	hostileMustFail(t, database, `UPDATE fatfish_challenges SET state='active',start_at_ms=3000,end_at_ms=13000,submit_until_ms=1813000,revision=2 WHERE id=?`, challenge)
	op := hostileOID("op_")
	hostileInsertOperation(t, database, op, 1, "fatfish_ticket", "operation", op)
	hostileMustExec(t, database, `UPDATE fatfish_challenges SET state='active',start_at_ms=3000,end_at_ms=13000,submit_until_ms=1813000,ticket_operation_id=?,revision=2 WHERE id=?`, op, challenge)
	hostileMustFail(t, database, `UPDATE fatfish_challenges SET state='cancelled_refunded',terminal_at_ms=5000,revision=3 WHERE id=?`, challenge)
	hostileMustExec(t, database, `UPDATE fatfish_challenges SET state='abandoned',terminal_at_ms=5000,revision=3 WHERE id=?`, challenge)
	hostileMustFail(t, database, `UPDATE fatfish_challenges SET state='active',terminal_at_ms=NULL,revision=4 WHERE id=?`, challenge)
	hostileMustExec(t, database, `INSERT INTO fatfish_progress(user_id,period_id,node_id,unlocked_at) VALUES(?,?,?,1)`, user, hostileOID("ffp_"), hostileOID("ffn_"))
	hostileMustFail(t, database, `UPDATE fatfish_progress SET passed=1 WHERE user_id=?`, user)
	hostileMustFail(t, database, `UPDATE fatfish_progress SET best_score_units=1 WHERE user_id=?`, user)
	hostileMustExec(t, database, `UPDATE fatfish_progress SET passed=1,best_stars=1,best_score_units=100000000,best_version_id=?,best_at_ms=3000,best_challenge_id=?,best_confirmed_at_ms=4000 WHERE user_id=?`, hostileOID("ffv_"), challenge, user)
	hostileMustFail(t, database, `UPDATE fatfish_progress SET best_score_units=100000001 WHERE user_id=?`, user)
	hostileMustFail(t, database, `UPDATE fatfish_progress SET best_at_ms=4000,best_confirmed_at_ms=5000 WHERE user_id=?`, user)
	hostileMustFail(t, database, `UPDATE fatfish_progress SET best_score_units=99999999 WHERE user_id=?`, user)
	hostileMustExec(t, database, `INSERT INTO fatfish_period_progress VALUES(?,?,100000000,4000,zeroblob(16))`, user, hostileOID("ffp_"))
	hostileMustFail(t, database, `UPDATE fatfish_period_progress SET achieved_at_ms=5000 WHERE user_id=?`, user)
	hostileMustExec(t, database, `DELETE FROM fatfish_challenges WHERE id=?`, challenge)
	var best string
	if err := database.QueryRow(`SELECT best_challenge_id FROM fatfish_progress WHERE user_id=?`, user).Scan(&best); err != nil || best != challenge {
		t.Fatal(best, err)
	}
	for _, values := range []string{"1,1,0", "0,0,1", "1,1,100000001"} {
		hostileMustFail(t, database, `INSERT INTO fatfish_playtests(id,version_id,passed,stars,score_units,duration_ms,result_json,created_at) VALUES(?,?,`+values+`,1,'{}',1)`, hostileOID("fpt_"), hostileOID("ffv_"))
	}
	if err := foreignKeyCheck(context.Background(), database); err != nil {
		t.Fatal(err)
	}
}

func TestInteractionContinuityAndPermanentProjection(t *testing.T) {
	database := openGenerationTwoConstraintFixture(t)
	defer database.Close()
	hostileMustFail(t, database, `INSERT INTO identity_continuity_facts VALUES(zeroblob(32),'game_onboarding','game','once','{}',1,2)`)
	hostileMustExec(t, database, `INSERT INTO identity_continuity_facts VALUES(zeroblob(32),'game_onboarding','game','once','{}',1,NULL)`)
	hostileMustFail(t, database, `INSERT INTO identity_continuity_facts VALUES(zeroblob(32),'welfare','pool','day','{}',1,NULL)`)
	hostileMustExec(t, database, `INSERT INTO admin_alerts(kind,message,created_at,resolved,resolved_at) VALUES('account_deleted','Deleted',1,1,2)`)
	hostileMustExec(t, database, `INSERT INTO admin_account_deletions(alert_id,snapshot_json) VALUES(1,'{}')`)
	hostileMustFail(t, database, `DELETE FROM admin_alerts WHERE id=1`)
	hostileMustFail(t, database, `DELETE FROM admin_account_deletions WHERE alert_id=1`)
}

func TestInteractionAggregateScanSourceRemoval(t *testing.T) {
	for _, remove := range []string{`DELETE FROM request_source_facts WHERE request_log_id=?`, `UPDATE request_source_facts SET user_id=NULL WHERE request_log_id=?`} {
		t.Run(remove, func(t *testing.T) {
			database := openGenerationTwoConstraintFixture(t)
			defer database.Close()
			user := hostileInsertUser(t, database, "scan", 1, 1)
			req := hostileOID("req_")
			hostileInsertLogicalRequest(t, database, req, user, "openai_chat_completions", 1)
			log := hostileInsertRequestLog(t, database, req, user, "openai_chat_completions")
			hostileMustExec(t, database, `INSERT INTO request_source_facts(request_log_id,user_id,kind,effective_ip,ip_quality,source_json,occurred_at) VALUES(?,?,'self','192.0.2.1','direct_peer','{}',1)`, log, user)
			scan := hostileOID("scn_")
			hostileMustExec(t, database, `INSERT INTO risk_client_scans(id,user_id,admin,request_token,query_json,rules_json,state,from_at,to_at,call_kind,model,upper_log_id,after_at,candidates,created_at,updated_at,expires_at,kind) VALUES(?,?,1,'abcdefghijklmnop','{}','[]','completed',0,100,'total','',?,0,1,0,0,86400,'shared_ips')`, scan, user, log)
			hostileMustExec(t, database, `INSERT INTO risk_scan_results(scan_id,row_no,user_id,request_log_id,published,result_json) VALUES(?,1,?,NULL,1,'{"ip":"192.0.2.1"}')`, scan, user)
			hostileMustExec(t, database, `INSERT INTO risk_scan_result_sources VALUES(?,1,?)`, scan, log)
			hostileMustExec(t, database, remove, log)
			var count, changed int
			wantCount, wantChanged := 0, 1
			if strings.HasPrefix(remove, "UPDATE") {
				wantCount, wantChanged = 1, 0
			}
			if err := database.QueryRow(`SELECT count(*) FROM risk_scan_results`).Scan(&count); err != nil || count != wantCount {
				t.Fatal(count, err)
			}
			if err := database.QueryRow(`SELECT changed FROM risk_client_scans WHERE id=?`, scan).Scan(&changed); err != nil || changed != wantChanged {
				t.Fatal(changed, err)
			}
		})
	}
}
