package db

import (
	"context"
	"database/sql"
	"testing"
)

func TestStorageContractsRetainsRequestIdentityAndSourceSequence(t *testing.T) {
	database := supportedSourceFixture(t)
	user := hostileInsertUser(t, database, "retained-root", 0, 0)
	hostileMustExec(t, database, "UPDATE users SET discord_id='123456789012345678' WHERE id=?", user)
	admin := hostileInsertUser(t, database, "scan-actor", 1, 0)
	requests := []string{"req_AAAAAAAAAAAAAAAAAAAAAA", "req_BBBBBBBBBBBBBBBBBBBBBA", "req_CCCCCCCCCCCCCCCCCCCCCA"}
	var logs []int64
	for _, request := range requests {
		hostileInsertTerminalRequest(t, database, request, user, "openai_chat_completions", "success", 200, nil)
		result := hostileMustExec(t, database, `INSERT INTO request_logs(logical_request_id,user_id,model,route_kind,caller_result_class,caller_status,status_code,started_at,completed_at) VALUES(?,?,'model','openai_chat_completions','success',200,200,0,1)`, request, user)
		logs = append(logs, hostileMustLastID(t, result))
	}
	insertSource := "INSERT INTO request_source_facts(request_log_id,user_id,kind,effective_ip,ip_quality,source_json,occurred_at) VALUES(?,?,'self','192.0.2.1','direct_peer','{}',1)"
	hostileMustExec(t, database, insertSource, logs[1], user)
	hostileMustExec(t, database, insertSource, logs[0], user)
	scan := hostileOID("scn_")
	hostileMustExec(t, database, `INSERT INTO risk_client_scans(id,user_id,admin,request_token,query_json,rules_json,state,from_at,to_at,call_kind,model,upper_log_id,after_at,candidates,created_at,updated_at,expires_at,kind) VALUES(?,?,1,'abcdefghijklmnop','{}','[]','running',0,100,'total','',?,0,2,0,0,86400,'shared_ips')`, scan, admin, logs[2])
	hostileMustExec(t, database, "INSERT INTO risk_scan_results(scan_id,row_no,user_id,published,result_json) VALUES(?,1,?,1,'{}')", scan, user)
	hostileMustExec(t, database, "INSERT INTO risk_scan_result_users VALUES(?,1,?)", scan, user)
	hostileMustExec(t, database, "INSERT INTO risk_scan_result_sources VALUES(?,1,?)", scan, logs[0])
	if err := extendKnownGenerationTwoSchema(context.Background(), database); err != nil {
		t.Fatal(err)
	}
	var state, reason string
	if err := database.QueryRow("SELECT state,reason FROM risk_client_scans WHERE id=?", scan).Scan(&state, &reason); err != nil || state != "running" || reason != "" {
		t.Fatal(state, reason, err)
	}
	var origin int64
	var discord string
	if err := database.QueryRow("SELECT origin_user_id,origin_discord_id FROM request_logs WHERE id=?", logs[0]).Scan(&origin, &discord); err != nil || origin != user || discord != "123456789012345678" {
		t.Fatal(origin, discord, err)
	}
	hostileMustFail(t, database, "UPDATE request_logs SET origin_user_id=NULL WHERE id=?", logs[0])
	hostileMustFail(t, database, "UPDATE request_logs SET origin_discord_id='999' WHERE id=?", logs[0])
	var watermark int64
	if err := database.QueryRow("SELECT max(source_id) FROM request_source_facts").Scan(&watermark); err != nil {
		t.Fatal(err)
	}
	hostileMustExec(t, database, "DELETE FROM request_source_facts WHERE request_log_id=?", logs[1])
	hostileMustExec(t, database, "UPDATE logical_requests SET user_id=NULL WHERE user_id=?", user)
	hostileMustExec(t, database, "DELETE FROM users WHERE id=?", user)
	var active sql.NullInt64
	if err := database.QueryRow("SELECT user_id,origin_user_id,origin_discord_id FROM request_logs WHERE id=?", logs[0]).Scan(&active, &origin, &discord); err != nil || active.Valid || origin != user || discord != "123456789012345678" {
		t.Fatal(active, origin, discord, err)
	}
	var rows int
	if err := database.QueryRow("SELECT count(*) FROM risk_scan_results WHERE scan_id=?", scan).Scan(&rows); err != nil || rows != 1 {
		t.Fatal(rows, err)
	}
	if err := database.QueryRow("SELECT user_id FROM risk_scan_result_users WHERE scan_id=?", scan).Scan(&origin); err != nil || origin != user {
		t.Fatal(origin, err)
	}
	hostileMustExec(t, database, insertSource, logs[2], nil)
	var late int64
	if err := database.QueryRow("SELECT source_id FROM request_source_facts WHERE request_log_id=?", logs[2]).Scan(&late); err != nil || late <= watermark {
		t.Fatal("late source reused watermark", late, watermark, err)
	}
	hostileMustExec(t, database, "DELETE FROM request_logs WHERE id=?", logs[0])
	if err := database.QueryRow("SELECT count(*) FROM risk_scan_results WHERE scan_id=?", scan).Scan(&rows); err != nil || rows != 0 {
		t.Fatal(rows, err)
	}
	if err := foreignKeyCheck(context.Background(), database); err != nil {
		t.Fatal(err)
	}
	if err := extendKnownGenerationTwoSchema(context.Background(), database); err != nil {
		t.Fatal(err)
	}
}

func TestScanWindowRetiresWithSourceAndCompletion(t *testing.T) {
	for _, sourceRemoved := range []bool{false, true} {
		database := openGenerationTwoConstraintFixture(t)
		actor := hostileInsertUser(t, database, "window-owner", 1, 0)
		req := hostileOID("req_")
		hostileInsertLogicalRequest(t, database, req, actor, "openai_chat_completions", 1)
		log := hostileInsertRequestLog(t, database, req, actor, "openai_chat_completions")
		hostileMustExec(t, database, "INSERT INTO request_source_facts(request_log_id,user_id,kind,effective_ip,ip_quality,source_json,occurred_at) VALUES(?,?,'self','192.0.2.1','direct_peer','{}',1)", log, actor)
		scan := hostileOID("scn_")
		hostileMustExec(t, database, `INSERT INTO risk_client_scans(id,user_id,admin,request_token,query_json,rules_json,state,from_at,to_at,call_kind,model,upper_log_id,after_at,candidates,created_at,updated_at,expires_at,kind,checkpoint_json) VALUES(?,?,1,'abcdefghijklmnop','{}','[]','running',0,100,'total','',?,0,1,0,0,86400,'user_ips','{"pending_discord":"123","source_id":1}')`, scan, actor, log)
		hostileMustExec(t, database, "INSERT INTO risk_scan_window_sources VALUES(?,?)", scan, log)
		want := "completed"
		if sourceRemoved {
			hostileMustExec(t, database, "DELETE FROM request_source_facts WHERE request_log_id=?", log)
			want = "failed"
		} else {
			hostileMustExec(t, database, "UPDATE risk_client_scans SET state='completed' WHERE id=?", scan)
		}
		var rows int
		if err := database.QueryRow("SELECT count(*) FROM risk_scan_window_sources").Scan(&rows); err != nil || rows != 0 {
			t.Fatal(rows, err)
		}
		var state, checkpoint string
		if err := database.QueryRow("SELECT state,checkpoint_json FROM risk_client_scans WHERE id=?", scan).Scan(&state, &checkpoint); err != nil || state != want || checkpoint != "{}" {
			t.Fatal(state, checkpoint, err)
		}
	}
}

func TestMinuteScanFactsRetireWithAccount(t *testing.T) {
	database := openGenerationTwoConstraintFixture(t)
	actor := hostileInsertUser(t, database, "minute-owner", 1, 0)
	subject := hostileInsertUser(t, database, "minute-subject", 0, 0)
	scan := hostileOID("scn_")
	hostileMustExec(t, database, `INSERT INTO risk_client_scans(id,user_id,admin,request_token,query_json,rules_json,state,from_at,to_at,call_kind,model,upper_log_id,after_at,candidates,created_at,updated_at,expires_at,kind) VALUES(?,?,1,'abcdefghijklmnop','{}','[]','running',0,100,'total','',0,0,1,0,0,86400,'users')`, scan, actor)
	hostileMustExec(t, database, "INSERT INTO risk_scan_results(scan_id,row_no,user_id,published,result_json) VALUES(?,1,?,1,'{}')", scan, subject)
	hostileMustExec(t, database, "INSERT INTO risk_scan_result_users VALUES(?,1,?)", scan, subject)
	hostileMustExec(t, database, "DELETE FROM users WHERE id=?", subject)
	var state, reason string
	if err := database.QueryRow("SELECT state,reason FROM risk_client_scans WHERE id=?", scan).Scan(&state, &reason); err != nil || state != "failed" || reason != "source_changed" {
		t.Fatal(state, reason, err)
	}
	var count int
	if err := database.QueryRow("SELECT count(*) FROM risk_scan_results WHERE scan_id=?", scan).Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
}
