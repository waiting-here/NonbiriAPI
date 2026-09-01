package db

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

const (
	hostileTimeMax  int64 = 253402300799
	hostileInt64Max int64 = 9223372036854775807
	hostileIDTail         = "Q"
)

func hostileOID(prefix string) string {
	return prefix + strings.Repeat("A", 21) + hostileIDTail
}

func hostileBadOID(prefix string) string {
	return prefix + strings.Repeat("A", 21) + "B"
}

func hostileOIDVariant(prefix string, marker byte, tail byte) string {
	body := []byte(strings.Repeat("A", 22))
	body[0] = marker
	body[len(body)-1] = tail
	return prefix + string(body)
}

func hostileBlob16(value byte) []byte {
	b := make([]byte, 16)
	b[15] = value
	return b
}

func hostileHigh128() []byte {
	b := make([]byte, 16)
	b[0] = 0x80
	return b
}

func hostileMaxU128() []byte {
	return []byte(strings.Repeat("\xff", 16))
}

func hostileMaxSM128() []byte {
	b := hostileMaxU128()
	b[0] = 0x7f
	return b
}

func hostileTwoTo63U128() []byte {
	b := make([]byte, 16)
	b[8] = 0x80
	return b
}

func hostileBlob32(value byte) []byte {
	b := make([]byte, 32)
	b[31] = value
	return b
}

func hostileMustExec(t *testing.T, db *sql.DB, query string, args ...any) sql.Result {
	t.Helper()
	result, err := db.Exec(query, args...)
	if err != nil {
		t.Fatalf("SQL failed: %v\n%s", err, query)
	}
	return result
}

func hostileMustFail(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err == nil {
		t.Fatalf("hostile SQL was accepted:\n%s", query)
	}
}

func hostileMustLastID(t *testing.T, result sql.Result) int64 {
	t.Helper()
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("last insert id: %v", err)
	}
	return id
}

// hostileNextPK64 reserves a deterministic positive row id for AUTOINCREMENT
// tables whose hostile INSERT guards intentionally reject implicit/zero ids.
// Each hostile database is private to one test, so MAX(id)+1 is sufficient and
// keeps the fixture independent of production ID generators.
func hostileNextPK64(t *testing.T, db *sql.DB, table string) int64 {
	t.Helper()
	var id int64
	if err := db.QueryRow(`SELECT COALESCE(MAX(id),0)+1 FROM ` + table).Scan(&id); err != nil {
		t.Fatalf("next %s id: %v", table, err)
	}
	if id <= 0 {
		t.Fatalf("next %s id is not positive: %d", table, id)
	}
	return id
}

func hostileQuoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func hostileCompactSQL(sqlText string) string {
	return strings.Join(strings.Fields(strings.ToLower(sqlText)), "")
}

func hostileInlineIntegerDefense(compact, column string) bool {
	column = strings.ToLower(column)
	if strings.Contains(compact, "typeof("+column+")") && strings.Contains(compact, "integer") {
		return true
	}
	for _, prefix := range []string{"(", ",", "check("} {
		if strings.Contains(compact, prefix+column+"in(") {
			return true
		}
	}
	return false
}

func hostileIntegerColumnHasFK(t *testing.T, db *sql.DB, table, column string) bool {
	t.Helper()
	rows, err := db.Query(`PRAGMA foreign_key_list(` + hostileQuoteIdent(table) + `)`)
	if err != nil {
		t.Fatalf("foreign keys for %s: %v", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, seq int
		var parent, child, parentColumn, onUpdate, onDelete, match string
		if err := rows.Scan(&id, &seq, &parent, &child, &parentColumn, &onUpdate, &onDelete, &match); err != nil {
			t.Fatalf("scan foreign key for %s: %v", table, err)
		}
		if child == column {
			return true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate foreign keys for %s: %v", table, err)
	}
	return false
}

func hostileIntegerColumnHasInsertUpdateTypeGuards(t *testing.T, db *sql.DB, table, column string) bool {
	t.Helper()
	rows, err := db.Query(`
SELECT sql FROM sqlite_schema
WHERE type='trigger' AND tbl_name=? AND sql IS NOT NULL`, table)
	if err != nil {
		t.Fatalf("integer guards for %s: %v", table, err)
	}
	defer rows.Close()
	needle := "typeof(new." + strings.ToLower(column) + ")"
	insertGuard, updateGuard := false, false
	for rows.Next() {
		var triggerSQL string
		if err := rows.Scan(&triggerSQL); err != nil {
			t.Fatalf("scan integer guard for %s.%s: %v", table, column, err)
		}
		compact := hostileCompactSQL(triggerSQL)
		if !strings.Contains(compact, needle) || !strings.Contains(compact, "integer") {
			continue
		}
		if strings.Contains(compact, "beforeinsert") {
			insertGuard = true
		}
		if strings.Contains(compact, "beforeupdate") {
			updateGuard = true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate integer guards for %s.%s: %v", table, column, err)
	}
	return insertGuard && updateGuard
}

func hostileHasIndexPrefix(t *testing.T, db *sql.DB, table string, want ...string) bool {
	t.Helper()
	rows, err := db.Query(`PRAGMA index_list(` + hostileQuoteIdent(table) + `)`)
	if err != nil {
		t.Fatalf("indexes for %s: %v", table, err)
	}
	var indexes []string
	for rows.Next() {
		var seq, unique, partial int
		var origin string
		var name string
		if err := rows.Scan(&seq, &name, &unique, &origin, &partial); err != nil {
			rows.Close()
			t.Fatalf("scan indexes for %s: %v", table, err)
		}
		indexes = append(indexes, name)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatalf("iterate indexes for %s: %v", table, err)
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("close indexes for %s: %v", table, err)
	}
	for _, index := range indexes {
		columns, err := db.Query(`PRAGMA index_info(` + hostileQuoteIdent(index) + `)`)
		if err != nil {
			t.Fatalf("index_info %s: %v", index, err)
		}
		var got []string
		for columns.Next() {
			var seq, cid int
			var name string
			if err := columns.Scan(&seq, &cid, &name); err != nil {
				columns.Close()
				t.Fatalf("scan index_info %s: %v", index, err)
			}
			got = append(got, name)
		}
		if err := columns.Err(); err != nil {
			columns.Close()
			t.Fatalf("iterate index_info %s: %v", index, err)
		}
		if err := columns.Close(); err != nil {
			t.Fatalf("close index_info %s: %v", index, err)
		}
		if len(got) < len(want) {
			continue
		}
		matches := true
		for i := range want {
			if got[i] != want[i] {
				matches = false
				break
			}
		}
		if matches {
			return true
		}
	}
	return false
}

func hostileInsertUser(t *testing.T, db *sql.DB, label string, isAdmin int, at int64) int64 {
	t.Helper()
	zero := hostileBlob16(0)
	result := hostileMustExec(t, db, `
INSERT INTO users (
 discord_id, username, is_admin, donation_credit_mag,
 total_requests, total_uncached_input_tokens, total_cache_write_input_tokens,
 total_cache_read_input_tokens, total_output_tokens,
 total_unknown_usage_requests, revision, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"hostile-"+label, "hostile", isAdmin, zero, zero, zero, zero, zero, zero, zero, zero, at, at)
	return hostileMustLastID(t, result)
}

func hostileInsertAccount(t *testing.T, db *sql.DB, kind string, userID any, code any, sign int, mag []byte, at int64) int64 {
	t.Helper()
	id := hostileNextPK64(t, db, "credit_accounts")
	result := hostileMustExec(t, db, `
INSERT INTO credit_accounts(id,kind,user_id,code,balance_sign,balance_mag,created_at,updated_at)
VALUES(?,?,?,?,?,?,?,?)`, id, kind, userID, code, sign, mag, at, at)
	return hostileMustLastID(t, result)
}

func hostileInsertEndpoint(t *testing.T, db *sql.DB, userID int64, base string) int64 {
	t.Helper()
	result := hostileMustExec(t, db, `
INSERT INTO endpoints(user_id,connector_type,base_url,note,enabled,revision,created_at,updated_at)
VALUES(?,'openai-compatible',?,'',1,1,0,0)`, userID, base)
	return hostileMustLastID(t, result)
}

func hostileInsertSecret(t *testing.T, db *sql.DB, base string, at int64) int64 {
	t.Helper()
	result := hostileMustExec(t, db, `
INSERT INTO endpoint_key_secrets(context_id,canonical_base_url,connector_type,encrypted_secret,created_at)
	VALUES(?,?,'openai-compatible',?,?)`,
		hostileBlob16(byte(at)), base, "nbsec:v2:hostile", at)
	return hostileMustLastID(t, result)
}

func hostileInsertEndpointKey(t *testing.T, db *sql.DB, endpointID, secretID int64) int64 {
	t.Helper()
	id := hostileNextPK64(t, db, "endpoint_keys")
	result := hostileMustExec(t, db, `
INSERT INTO endpoint_keys(
 id,endpoint_id,secret_ref_id,secret_fingerprint,display_head,display_tail,note,
 enabled,force_store_false,revision,created_at,updated_at
) VALUES(?,?,?,?,'head','tail','',1,0,1,0,0)`,
		id, endpointID, secretID, hostileBlob32(1))
	return hostileMustLastID(t, result)
}

func hostileInsertLogicalRequest(t *testing.T, db *sql.DB, id string, userID any, route string, limit int64) {
	t.Helper()
	remaining := hostileBlob16(1)
	if route == "charity_chat_completions" {
		remaining = hostileBlob16(byte(limit + 1))
	} else if route == "model_discovery" {
		remaining = hostileBlob16(0)
	}
	hostileMustExec(t, db, `
INSERT INTO logical_requests(
 id,user_id,route_kind,model_snapshot,state,attempt_limit,
 accounting_state,account_reserved_milli,settlement_destination,
 ledger_rows_remaining,created_at
) VALUES(?,?,?,'model','accepted',?,'none',0,'user',?,0)`,
		id, userID, route, limit, remaining)
}

func hostileInsertTerminalRequest(t *testing.T, db *sql.DB, id string, userID any, route, resultClass string, status any, errorCode any) {
	t.Helper()
	hostileMustExec(t, db, `
INSERT INTO logical_requests(
 id,user_id,route_kind,model_snapshot,state,attempt_limit,caller_result_class,
 caller_status,caller_error_code,accounting_state,settlement_destination,
 ledger_rows_remaining,created_at,terminal_at
) VALUES(?,?,?,'model','terminal',1,?,?,?,'none','user',?,0,1)`,
		id, userID, route, resultClass, status, errorCode, hostileBlob16(0))
}

func hostileInsertOperation(t *testing.T, db *sql.DB, id string, ledgerSeq int64, kind, sourceType, sourceID string) {
	t.Helper()
	zero := hostileBlob16(0)
	hostileMustExec(t, db, `
INSERT INTO credit_operations(
 id,ledger_seq,kind,source_type,source_id,source_seq,
 donation_credit_delta_sign,donation_credit_delta_mag,created_at
) VALUES(?,?,?,?,?,?,0,?,0)`, id, ledgerSeq, kind, sourceType, sourceID, zero, zero)
}

func hostileInsertReportCase(t *testing.T, db *sql.DB, id string, fingerprint []byte, status, progress string, at int64) {
	t.Helper()
	var terminalAt any
	if status == "approved" || status == "rejected" {
		terminalAt = at + 1
	}
	hostileMustExec(t, db, `
INSERT INTO report_cases(
 id,fingerprint,connector_type,canonical_base_url,status,progress_state,
 material_version,target_version,deadline,cursor_text,material_count,target_count,
 distinct_owner_count,processed_target_count,deleted_target_count,released_target_count,
 retry_attempt_count,created_at,terminal_at
) VALUES(?,?,'openai-compatible','https://upstream.example/v1',?,?,1,1,1000,NULL,0,0,0,0,0,0,0,?,?)`,
		id, fingerprint, status, progress, at, terminalAt)
}

func hostileInsertDonation(t *testing.T, db *sql.DB, userID any) int64 {
	t.Helper()
	id := hostileNextPK64(t, db, "donations")
	result := hostileMustExec(t, db, `
INSERT INTO donations(id,user_id,status,revision,description,review_note,created_at,updated_at)
VALUES(?,?,'pending',1,'','',0,0)`, id, userID)
	return hostileMustLastID(t, result)
}

func hostileInsertDonationKey(t *testing.T, db *sql.DB, donationID int64, endpointKeyID any) int64 {
	t.Helper()
	id := hostileNextPK64(t, db, "donation_keys")
	zero := hostileBlob16(0)
	one := hostileBlob16(1)
	result := hostileMustExec(t, db, `
INSERT INTO donation_keys(
 id,donation_id,endpoint_key_id,display_head,display_tail,canonical_base_url,connector_type,
 price_limit_mag,call_limit_mag,token_limit_mag,price_used_mag,price_reserved_mag,
 calls_used,calls_reserved,tokens_used,tokens_reserved,token_reserve,enabled,failure_disabled,
 failure_streak,streak_generation,next_claim_seq,next_fold_seq,safe_note,created_at,updated_at
) VALUES(?,?,?,'head','tail','https://upstream.example/v1','openai-compatible',NULL,NULL,NULL,?,?,?,?,?, ?,0,1,0,?,?,?,?,'',0,0)`,
		id, donationID, endpointKeyID, zero, zero, zero, zero, zero, zero, zero, one, one, zero)
	return hostileMustLastID(t, result)
}

func hostileInsertRequestLog(t *testing.T, db *sql.DB, reqID string, userID any, route string) int64 {
	t.Helper()
	result := hostileMustExec(t, db, `
INSERT INTO request_logs(
 logical_request_id,user_id,model,upstream_model_id,route_kind,endpoint_base_url,
 started_at
) VALUES(?,?, 'model','upstream',?,'https://upstream.example/v1',0)`, reqID, userID, route)
	return hostileMustLastID(t, result)
}

func hostileInsertAttempt(t *testing.T, db *sql.DB, claimID string, logID int64, seq int64) {
	t.Helper()
	hostileMustExec(t, db, `
INSERT INTO request_attempts(
 claim_id,request_log_id,attempt_seq,connector_type,canonical_base_url,upstream_model_id,
 result_kind,upstream_status,input_tokens,cache_write_input_tokens,
 cache_read_input_tokens,output_tokens,usage_unknown,started_at,completed_at
) VALUES(?,?,?,'openai-compatible','https://upstream.example/v1','upstream',
 'response',200,0,0,0,0,0,0,1)`, claimID, logID, seq)
}

func hostileInsertMaintenanceEvent(t *testing.T, db *sql.DB, id string, adminID int64) {
	t.Helper()
	hostileMustExec(t, db, `
INSERT INTO maintenance_events(
 id,actor_user_id,actor_discord_id,actor_role,action,reason,created_at
) VALUES(?,?,NULL,'admin','enable','hostile',0)`, id, adminID)
}

func hostileInsertAnnouncementAudit(t *testing.T, db *sql.DB, adminID int64, at int64) int64 {
	t.Helper()
	id := hostileNextPK64(t, db, "announcement_audits")
	result := hostileMustExec(t, db, `
INSERT INTO announcement_audits(
 id,announcement_id_text,actor_user_id,action,from_revision,to_revision,reason,
 created_at,actor_deidentify_at
) VALUES(?,?,?,'create',0,1,'hostile',?,?)`, id, hostileOID("ann_"), adminID, at, at+7776000)
	return hostileMustLastID(t, result)
}

func hostileInsertReportMaterial(t *testing.T, db *sql.DB, caseID string) int64 {
	t.Helper()
	id := hostileNextPK64(t, db, "report_materials")
	result := hostileMustExec(t, db, `
INSERT INTO report_materials(id,case_id,material_hash,note_text,source_ip_envelope,created_at)
VALUES(?,?,?, '', ?,0)`, id, caseID, hostileBlob32(2), make([]byte, 45))
	return hostileMustLastID(t, result)
}

func hostileInsertReportTarget(t *testing.T, db *sql.DB, caseID, id string) {
	t.Helper()
	hostileMustExec(t, db, `
INSERT INTO report_targets(
 id,case_id,target_seq,key_ref,connector_type,canonical_base_url,
 key_display_head,key_display_tail,state,discovered_version,created_at,updated_at
) VALUES(?,?,0,?,'openai-compatible','https://upstream.example/v1','head','tail','protected',1,0,0)`,
		id, caseID, hostileBlob32(3))
}

func hostileInsertReportDecision(t *testing.T, db *sql.DB, caseID string, adminID int64) int64 {
	t.Helper()
	id := hostileNextPK64(t, db, "report_decisions")
	result := hostileMustExec(t, db, `
INSERT INTO report_decisions(id,case_id,material_version,target_version,actor_user_id,action,reason,created_at)
VALUES(?,?,1,1,?,'reject','hostile',0)`, id, caseID, adminID)
	return hostileMustLastID(t, result)
}

func hostileInsertDonationReview(t *testing.T, db *sql.DB, donationID int64, adminID int64) int64 {
	t.Helper()
	id := hostileNextPK64(t, db, "donation_reviews")
	result := hostileMustExec(t, db, `
INSERT INTO donation_reviews(id,donation_id,submission_revision,reviewer_user_id,reviewer_role,action,note,created_at)
VALUES(?,?,1,?,'admin','note_update','hostile',0)`, id, donationID, adminID)
	return hostileMustLastID(t, result)
}

func hostileInsertRPSAccount(t *testing.T, db *sql.DB, sessionID string) int64 {
	t.Helper()
	return hostileInsertAccount(t, db, "platform", nil, "rps-session:"+sessionID, 0, hostileBlob16(0), 0)
}

func hostileInsertRPSSession(t *testing.T, db *sql.DB, id string, phase, state string, accountID int64) {
	t.Helper()
	hostileInsertRPSSessionWithReason(t, db, id, phase, state, accountID, "quick_resolved")
}

func hostileInsertRPSSessionWithReason(t *testing.T, db *sql.DB, id string, phase, state string, accountID int64, reason string) {
	t.Helper()
	zero := hostileBlob16(0)
	one := hostileBlob16(1)
	terminal := state == "terminal_processing"
	var poolBase, plan, dealerRaise, deadline, terminalOperationID, terminalReason any
	if terminal {
		terminalOperationID = hostileOID("op_")
		terminalReason = reason
	} else {
		deadline = int64(120)
		if phase == "paid_pool_gesture" || phase == "free_pool_gesture" {
			poolBase = one
		} else {
			plan = one
		}
		if phase == "followers" {
			dealerRaise = one
		}
	}
	hostileMustExec(t, db, `
INSERT INTO game_rps_sessions(
 id,account_id,mode,rules_version,state,phase,revision,phase_seq,identity_epoch,cut_seq,
 ledger_rows_remaining,dealer_seat,base_milli,platform_bp,welfare_bp,thursday_bp,
 gesture_seconds,dealer_seconds,follower_seconds,player_pool,permanent_multiplier,
 pool_base_multiplier,current_plan_multiplier,dealer_raise,base_round_count,paid_tie_count,
 free_tie_count,paid_pool_streak,free_pool_streak,platform_cut_total,welfare_cut_total,
 thursday_cut_total,welfare_carry_total,reminder_state,phase_deadline,health_epoch,
 recent_events_blob,recent_first_seq,recent_last_seq,recent_event_count,terminal_operation_id,
 terminal_retry_attempt_count,terminal_next_retry_at,terminal_last_error_class,started_at,terminal_reason
) VALUES(?,?,'quick',1,?,?,?,?,?, ?,?,0,5,0,0,0,20,15,15,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?, ?,0,X'',?,?,0,?, ?,NULL,NULL,100,?)`,
		id, accountID, state, phase, one, one, one, zero, one,
		zero, one, poolBase, plan, dealerRaise, zero, zero, zero, zero, zero, zero, zero, zero, zero,
		"none", deadline, zero, zero, terminalOperationID, zero, terminalReason)
}

const hostileRPSSeatInsertSQL = `
INSERT INTO game_rps_seats(
 session_id,seat_no,user_id,deletion_state,starting_balance,current_balance,current_round_input,
 current_all_in,current_gesture_envelope,current_gesture_phase_seq,follower_action,last_action_phase_seq,total_input,
 total_returned,terminal_return,wallet_net_sign,wallet_net_mag,rock_count,scissors_count,
 paper_count,timeout_count,stats_applied
) VALUES(?,?,?,?,?,?,?,0,?,?,?,?,?,?,?,?,?,?,?,?,?,0)`

func hostileInsertRPSSeat(t *testing.T, db *sql.DB, sessionID string, seatNo int, userID any, envelope, lastPhase any, terminalReturn any, netSign any, netMag any, deletionState string) {
	t.Helper()
	var gesturePhase any
	if envelope != nil {
		gesturePhase = lastPhase
	}
	hostileInsertRPSSeatWithActions(t, db, sessionID, seatNo, userID, envelope, gesturePhase, nil, lastPhase, terminalReturn, netSign, netMag, deletionState)
}

func hostileInsertRPSSeatWithActions(t *testing.T, db *sql.DB, sessionID string, seatNo int, userID any, envelope, gesturePhase, followerAction, lastPhase any, terminalReturn any, netSign any, netMag any, deletionState string) {
	t.Helper()
	zero := hostileBlob16(0)
	hostileMustExec(t, db, hostileRPSSeatInsertSQL,
		sessionID, seatNo, userID, deletionState, zero, zero, zero, envelope, gesturePhase, followerAction, lastPhase,
		hostileBlob32(0), hostileBlob32(0), terminalReturn, netSign, netMag, zero, zero, zero, zero)
}

func hostileMustFailRPSSeatWithActions(t *testing.T, db *sql.DB, sessionID string, seatNo int, userID any, envelope, gesturePhase, followerAction, lastPhase any, deletionState string) {
	t.Helper()
	zero := hostileBlob16(0)
	hostileMustFail(t, db, hostileRPSSeatInsertSQL,
		sessionID, seatNo, userID, deletionState, zero, zero, zero, envelope, gesturePhase, followerAction, lastPhase,
		hostileBlob32(0), hostileBlob32(0), nil, nil, nil, zero, zero, zero, zero)
}

func hostileInsertRPSQueue(t *testing.T, db *sql.DB, marker byte, mode string, reserved, remaining []byte) (string, int64) {
	t.Helper()
	queueID := hostileOIDVariant("rpsq_", marker, 'Q')
	accountID := hostileInsertAccount(t, db, "platform", nil, "rps-queue:"+queueID, 0, hostileBlob16(0), 0)
	hostileMustExec(t, db, `
INSERT INTO game_rps_queue(
 id,user_id,account_id,mode,revision,reservation_operation_id,reserved,
 ledger_rows_remaining,device_token_hash,source_ip_hash,deadline,created_at
) VALUES(?,?,? ,?, ?,? ,?, ?, ?, ?,0,0)`, queueID,
		hostileInsertUser(t, db, "queue-"+string(marker), 0, 0), accountID, mode,
		hostileBlob16(1), hostileOIDVariant("op_", marker, 'Q'), reserved, remaining,
		hostileBlob32(marker), hostileBlob32(marker+1))
	return queueID, accountID
}

func TestGenerationTwoRPSUserSlotCardinality(t *testing.T) {
	db := openGenerationTwoDDLForTest(t)
	defer db.Close()

	sessionID := hostileOIDVariant("rps_", 'S', 'Q')
	accountID := hostileInsertRPSAccount(t, db, sessionID)
	hostileInsertRPSSession(t, db, sessionID, "gesture", "started", accountID)
	for _, label := range []string{"rps-slot-one", "rps-slot-two", "rps-slot-three"} {
		userID := hostileInsertUser(t, db, label, 0, 0)
		hostileMustExec(t, db,
			`INSERT INTO game_rps_user_slots(user_id,queue_id,session_id,created_at) VALUES(?,NULL,?,0)`,
			userID, sessionID)
	}
	var sessionSlots int
	if err := db.QueryRow(`SELECT COUNT(*) FROM game_rps_user_slots WHERE session_id=?`, sessionID).Scan(&sessionSlots); err != nil {
		t.Fatalf("count RPS session slots: %v", err)
	}
	if sessionSlots != 3 {
		t.Fatalf("RPS session has %d user slots, want 3", sessionSlots)
	}

	queueID, _ := hostileInsertRPSQueue(t, db, 'U', "quick", hostileBlob16(1), hostileBlob16(1))
	var queuedUserID int64
	if err := db.QueryRow(`SELECT user_id FROM game_rps_queue WHERE id=?`, queueID).Scan(&queuedUserID); err != nil {
		t.Fatalf("read queued user: %v", err)
	}
	hostileMustExec(t, db,
		`INSERT INTO game_rps_user_slots(user_id,queue_id,session_id,created_at) VALUES(?,?,NULL,0)`,
		queuedUserID, queueID)
	secondQueuedUserID := hostileInsertUser(t, db, "rps-slot-duplicate-queue", 0, 0)
	hostileMustFail(t, db,
		`INSERT INTO game_rps_user_slots(user_id,queue_id,session_id,created_at) VALUES(?,?,NULL,0)`,
		secondQueuedUserID, queueID)
}

func hostileInsertFishingBatch(t *testing.T, db *sql.DB, batchID string, userID int64, operationID string) {
	t.Helper()
	hostileMustExec(t, db, `
INSERT INTO game_fishing_batches(
 id,user_id,bait,count,unit_price_milli,entry_total_milli,payout_total_milli,
 operation_id,request_hash,state,ledger_rows_remaining,attempt_count,next_attempt_at,
 retry_exhausted,created_at
) VALUES(?,?, 'worm',1,1,1,1,?,?, 'reserved',?,0,0,0,0)`,
		batchID, userID, operationID, hostileBlob32(1), hostileBlob16(1))
}

func hostileInsertSharedPool(t *testing.T, db *sql.DB, id, poolType, periodID string, accountID int64) {
	t.Helper()
	var period any
	if periodID != "" {
		period = periodID
	}
	hostileMustExec(t, db, `
INSERT INTO shared_pools(id,pool_type,period_id,account_id,state,revision,created_at)
VALUES(?,?,?,?,'open',1,0)`, id, poolType, period, accountID)
}

func hostileInsertThursdayPeriod(t *testing.T, db *sql.DB, id, currentPool, nextPool string, state string) {
	t.Helper()
	one := hostileBlob16(1)
	zero := hostileBlob16(0)
	var started, terminal, remaining any
	remaining = one
	if state == "settled" {
		started, terminal, remaining = int64(1), int64(2), zero
	}
	hostileMustExec(t, db, `
INSERT INTO thursday_periods(
 id,period_key,state,revision,opens_at,closes_at,literature,entry_milli,per_user_limit,
 platform_bp,welfare_bp,next_pool_bp,current_pool_id,next_pool_id,settlement_cursor,
 ledger_rows_remaining,frozen_pool_mag,frozen_contribution_count,eligible_contribution_count,
 platform_cut_mag,welfare_cut_mag,next_cut_mag,payout_total_mag,rollover_mag,created_at,
 started_settlement_at,terminal_at
) VALUES(?,?,?,1,0,86400,'',1,1,0,0,0,?,?,NULL,?,?,?,?,?,?,?, ?,?,0,?,?)`,
		id, "1970-01-01", state, currentPool, nextPool, remaining, zero, zero, zero, zero, zero, zero, zero, zero, started, terminal)
}

func hostileInsertLegalHold(t *testing.T, db *sql.DB, id, objectKind, objectRef string, adminID int64) {
	t.Helper()
	hostileMustExec(t, db, `
INSERT INTO legal_holds(
 id,object_kind,object_ref,state,revision,basis,created_by_user_id,created_at,expires_at
) VALUES(?,?,?,'active',1,'hostile',?,100,200)`, id, objectKind, objectRef, adminID)
}

func hostileInsertThursdayFixture(t *testing.T, db *sql.DB) (string, string, string) {
	t.Helper()
	periodID := hostileOIDVariant("thu_", 'T', 'Q')
	currentPoolID := hostileOIDVariant("pol_", 'C', 'Q')
	nextPoolID := hostileOIDVariant("pol_", 'N', 'Q')
	hostileMustExec(t, db, `PRAGMA defer_foreign_keys=ON`)
	hostileMustExec(t, db, `BEGIN`)
	currentAccount := hostileInsertAccount(t, db, "pool", nil, "pool:"+currentPoolID, 0, hostileBlob16(0), 0)
	nextAccount := hostileInsertAccount(t, db, "pool", nil, "pool:"+nextPoolID, 0, hostileBlob16(0), 0)
	hostileInsertSharedPool(t, db, currentPoolID, "thursday", periodID, currentAccount)
	// A Thursday period owns exactly one current pool; its next pool is the
	// single unbound Thursday pool that will be attached at rollover.
	hostileInsertSharedPool(t, db, nextPoolID, "thursday", "", nextAccount)
	hostileInsertThursdayPeriod(t, db, periodID, currentPoolID, nextPoolID, "open")
	hostileMustExec(t, db, `COMMIT`)
	return periodID, currentPoolID, nextPoolID
}

func hostileEnvelope(length int, version byte) []byte {
	b := make([]byte, length)
	b[0] = version
	return b
}

func TestGenerationTwoHostileScalarBoundaries(t *testing.T) {
	db := openGenerationTwoDDLForTest(t)
	uid := hostileInsertUser(t, db, "scalar", 0, 0)

	hostileMustExec(t, db, `UPDATE users SET created_at=?,updated_at=? WHERE id=?`, hostileTimeMax, hostileTimeMax, uid)
	hostileMustFail(t, db, `UPDATE users SET created_at=-1 WHERE id=?`, uid)
	hostileMustFail(t, db, `UPDATE users SET updated_at=? WHERE id=?`, hostileTimeMax+1, uid)

	hostileMustExec(t, db, `INSERT INTO worker_checkpoints(worker_key,cursor_text,generation,attempt_count,next_attempt_at,last_error_class,updated_at) VALUES('scalar','',0,0,0,'',0)`)
	hostileMustExec(t, db, `UPDATE worker_checkpoints SET generation=?,next_attempt_at=?,updated_at=? WHERE worker_key='scalar'`, hostileInt64Max, hostileTimeMax, hostileTimeMax)
	hostileMustFail(t, db, `UPDATE worker_checkpoints SET generation=? WHERE worker_key='scalar'`, "9223372036854775808")
	hostileMustFail(t, db, `UPDATE worker_checkpoints SET generation=-1 WHERE worker_key='scalar'`)
	hostileMustFail(t, db, `UPDATE worker_checkpoints SET next_attempt_at=? WHERE worker_key='scalar'`, hostileTimeMax+1)

	hostileMustExec(t, db, `INSERT INTO site_config(key,value,updated_at) VALUES('scalar','',0)`)
	hostileMustExec(t, db, `UPDATE site_config SET updated_at=? WHERE key='scalar'`, hostileTimeMax)
	hostileMustFail(t, db, `UPDATE site_config SET updated_at=? WHERE key='scalar'`, hostileTimeMax+1)

	hostileMustExec(t, db, `INSERT INTO config_revisions(domain,revision,updated_at) VALUES('site',1,0)`)
	hostileMustExec(t, db, `UPDATE config_revisions SET revision=? WHERE domain='site'`, hostileInt64Max)
	hostileMustFail(t, db, `UPDATE config_revisions SET revision=0 WHERE domain='site'`)
	hostileMustFail(t, db, `UPDATE config_revisions SET revision=? WHERE domain='site'`, "9223372036854775808")

	hostileMustExec(t, db, `INSERT INTO caller_keys(user_id,generation,updated_at) VALUES(?,0,0)`, uid)
	uidAtGenerationLimit := hostileInsertUser(t, db, "scalar-generation-limit", 0, 0)
	hostileMustExec(t, db, `INSERT INTO caller_keys(user_id,generation,updated_at) VALUES(?,?,0)`, uidAtGenerationLimit, hostileInt64Max)
	hostileMustFail(t, db, `UPDATE caller_keys SET generation=? WHERE user_id=?`, "9223372036854775808", uidAtGenerationLimit)
	hostileMustFail(t, db, `UPDATE caller_keys SET generation=-1 WHERE user_id=?`, uid)

	req := hostileOID("req_")
	hostileInsertLogicalRequest(t, db, req, uid, "openai_chat_completions", 100)
	for _, tc := range []struct {
		name string
		seq  int
	}{
		{"lower", 1},
		{"upper", 100},
	} {
		t.Run("claim-attempt-"+tc.name, func(t *testing.T) {
			claim := hostileOID("clm_")
			if tc.seq == 100 {
				claim = hostileOID("clm_")[:len(hostileOID("clm_"))-1] + "g"
			}
			hostileMustExec(t, db, `
INSERT INTO dispatch_claims(id,logical_request_id,attempt_seq,purpose,claim_now,state,terminal_at,donor_reward_state)
VALUES(?,?,?,'self',0,'released',0,'not_applicable')`, claim, req, tc.seq)
		})
	}
	hostileMustFail(t, db, `
INSERT INTO dispatch_claims(id,logical_request_id,attempt_seq,purpose,claim_now,state,terminal_at,donor_reward_state)
VALUES(?,?,101,'self',0,'released',0,'not_applicable')`, hostileBadOID("clm_"), req)

	hostileMustExec(t, db, `INSERT INTO worker_checkpoints(worker_key,cursor_text,generation,attempt_count,next_attempt_at,last_error_class,updated_at) VALUES('time-max','',0,2147483647,?, '',?)`, hostileTimeMax, hostileTimeMax)

	// Resource and secret-rail timestamps use the same inclusive UTC boundary;
	// their integer revision counters use the full signed SQLite range.
	endpointID := hostileInsertEndpoint(t, db, uid, "https://scalar.example/v1")
	hostileMustExec(t, db, `UPDATE endpoints SET revision=?,created_at=?,updated_at=? WHERE id=?`, hostileInt64Max, hostileTimeMax, hostileTimeMax, endpointID)
	hostileMustFail(t, db, `UPDATE endpoints SET revision=0 WHERE id=?`, endpointID)
	hostileMustFail(t, db, `UPDATE endpoints SET revision=? WHERE id=?`, "9223372036854775808", endpointID)
	hostileMustFail(t, db, `UPDATE endpoints SET updated_at=? WHERE id=?`, hostileTimeMax+1, endpointID)
	secretID := hostileInsertSecret(t, db, "https://scalar.example/v1", 0)
	keyID := hostileInsertEndpointKey(t, db, endpointID, secretID)
	hostileMustExec(t, db, `UPDATE endpoint_keys SET revision=?,created_at=?,updated_at=? WHERE id=?`, hostileInt64Max, hostileTimeMax, hostileTimeMax, keyID)
	hostileMustFail(t, db, `UPDATE endpoint_keys SET revision=0 WHERE id=?`, keyID)
	hostileMustFail(t, db, `UPDATE endpoint_keys SET updated_at=? WHERE id=?`, hostileTimeMax+1, keyID)
	hostileMustExec(t, db, `DELETE FROM endpoint_keys WHERE id=?`, keyID)
	hostileMustExec(t, db, `UPDATE endpoint_key_secrets SET orphaned_at=? WHERE id=?`, hostileTimeMax, secretID)
	// Once an orphan timestamp is recorded it is a terminal claim-first fact:
	// it may not be rewritten to a different non-NULL timestamp.
	hostileMustFail(t, db, `UPDATE endpoint_key_secrets SET orphaned_at=? WHERE id=?`, hostileTimeMax-1, secretID)
	hostileMustFail(t, db, `UPDATE endpoint_key_secrets SET orphaned_at=? WHERE id=?`, hostileTimeMax+1, secretID)
}

func TestGenerationTwoHostileWideIntegerCodecs(t *testing.T) {
	db := openGenerationTwoDDLForTest(t)
	zero := hostileBlob16(0)
	one := hostileBlob16(1)
	max := hostileMaxU128()
	high := hostileHigh128()
	twoTo63 := hostileTwoTo63U128()

	// U128 is a full unsigned 128-bit value.  In particular, the all-ones
	// encoding is valid for a pure U128 field; only SM128 magnitudes reserve
	// the high bit for their sign-safe canonical domain.
	hostileMustExec(t, db, `
INSERT INTO site_usage_totals(
 id,total_requests,total_uncached_input_tokens,total_cache_write_input_tokens,
 total_cache_read_input_tokens,total_output_tokens,total_unknown_usage_requests,
 revision,updated_at
 ) VALUES(1,?,?,?,?,?,?,?,0)`, max, max, max, max, max, max, max)
	hostileMustFail(t, db, `UPDATE site_usage_totals SET total_requests=? WHERE id=1`, []byte{1})
	hostileMustExec(t, db, `
INSERT INTO site_activity_daily(
 day,product_active,api_requests,uncached_input_tokens,cache_write_input_tokens,
 cache_read_input_tokens,output_tokens,checkins,console_writes,game_active,
 game_rounds,distinct_product_users,updated_at
) VALUES(0,0,?,?,?,?,?,?,?,0,?,?,?)`, max, max, max, max, max, max, max, max, max, hostileTimeMax)
	hostileMustFail(t, db, `UPDATE site_activity_daily SET api_requests=? WHERE day=0`, []byte{1})

	uid := hostileInsertUser(t, db, "wide", 0, 0)
	hostileMustExec(t, db, `
UPDATE users SET donation_credit_mag=?,total_requests=?,total_uncached_input_tokens=?,
 total_cache_write_input_tokens=?,total_cache_read_input_tokens=?,total_output_tokens=?,
 total_unknown_usage_requests=?,revision=? WHERE id=?`,
		max, max, max, max, max, max, max, max, uid)
	hostileMustFail(t, db, `UPDATE users SET revision=? WHERE id=?`, []byte{1}, uid)

	hostileMustExec(t, db, `INSERT INTO credit_capacity(id,last_ledger_seq,reserved_future_rows,revision) VALUES(1,0,?,?)`, max, max)
	hostileMustFail(t, db, `UPDATE credit_capacity SET reserved_future_rows=? WHERE id=1`, []byte{1})
	ledgerMaxID := hostileOIDVariant("op_", 'L', 'Q')
	hostileMustExec(t, db, `
INSERT INTO credit_operations(
 id,ledger_seq,kind,source_type,source_id,source_seq,
 donation_credit_delta_sign,donation_credit_delta_mag,created_at
) VALUES(?,?,'welfare_claim','operation',?,?,0,?,?)`, ledgerMaxID, hostileInt64Max, ledgerMaxID, zero, zero, hostileTimeMax)
	hostileMustFail(t, db, `
INSERT INTO credit_operations(
 id,ledger_seq,kind,source_type,source_id,source_seq,
 donation_credit_delta_sign,donation_credit_delta_mag,created_at
) VALUES(?,0,'welfare_claim','operation',?,?,0,?,0)`, hostileOIDVariant("op_", 'Z', 'Q'), hostileOIDVariant("op_", 'Z', 'Q'), zero, zero)
	roundMaxID := hostileOIDVariant("op_", 'R', 'Q')
	hostileMustExec(t, db, `
INSERT INTO credit_operations(
 id,ledger_seq,kind,source_type,source_id,source_seq,
 donation_credit_delta_sign,donation_credit_delta_mag,created_at
) VALUES(?,2,'rps_round_cut','rps_session',?,?,0,?,0)`, roundMaxID, hostileOID("rps_"), max, zero)
	hostileMustFail(t, db, `
INSERT INTO credit_operations(
 id,ledger_seq,kind,source_type,source_id,source_seq,
 donation_credit_delta_sign,donation_credit_delta_mag,created_at
) VALUES(?,3,'rps_round_cut','rps_session',?,?,0,?,0)`, hostileOIDVariant("op_", 'S', 'Q'), hostileOID("rps_"), []byte{1}, zero)

	claimID := hostileOID("clm_")
	hostileMustExec(t, db, `
INSERT INTO donation_usage_reservations(
 claim_id,donation_key_id,streak_generation,claim_seq,price_reserved_milli,
 calls_reserved,tokens_reserved,state,created_at
	 ) VALUES(?,NULL,?,?,0,0,0,'reserved',0)`, claimID, twoTo63, twoTo63)
	hostileMustExec(t, db, `UPDATE donation_usage_reservations SET streak_generation=?,claim_seq=? WHERE claim_id=?`, high, max, claimID)
	hostileMustExec(t, db, `UPDATE donation_usage_reservations SET streak_generation=?,claim_seq=? WHERE claim_id=?`, max, twoTo63, claimID)
	hostileMustFail(t, db, `UPDATE donation_usage_reservations SET claim_seq=? WHERE claim_id=?`, []byte{1}, claimID)

	// SM128 permits sign -1/0/+1, with zero iff the 16-byte magnitude is all
	// zero.  Its signed magnitude reserves the high bit: both signs accept the
	// maximum canonical magnitude 2^127-1, while high-bit values are hostile.
	userBalance := hostileInsertAccount(t, db, "user", uid, nil, 1, hostileMaxSM128(), 0)
	externalBalance := hostileInsertAccount(t, db, "external", nil, "external", -1, hostileMaxSM128(), 0)
	hostileMustFail(t, db, `INSERT INTO credit_accounts(kind,code,balance_sign,balance_mag,created_at,updated_at) VALUES('platform','sm128-positive-zero',1,?,0,0)`, zero)
	hostileMustFail(t, db, `INSERT INTO credit_accounts(kind,code,balance_sign,balance_mag,created_at,updated_at) VALUES('platform','sm128-negative-zero',-1,?,0,0)`, zero)
	hostileMustFail(t, db, `INSERT INTO credit_accounts(kind,code,balance_sign,balance_mag,created_at,updated_at) VALUES('platform','sm128-sign',2,?,0,0)`, one)
	hostileMustFail(t, db, `INSERT INTO credit_accounts(kind,code,balance_sign,balance_mag,created_at,updated_at) VALUES('platform','sm128-high',1,?,0,0)`, hostileHigh128())
	hostileMustFail(t, db, `INSERT INTO credit_accounts(kind,code,balance_sign,balance_mag,created_at,updated_at) VALUES('platform','sm128-short',1,?,0,0)`, []byte{1})
	hostileMustFail(t, db, `INSERT INTO credit_accounts(kind,code,balance_sign,balance_mag,created_at,updated_at) VALUES('platform','sm128-negative-platform',-1,?,0,0)`, one)

	poolID := hostileOID("pol_")
	hostileMustFail(t, db, `INSERT INTO credit_accounts(kind,code,balance_sign,balance_mag,created_at,updated_at) VALUES('pool',?, -1,?,0,0)`, "pool:"+poolID, one)

	opID := hostileOID("op_")
	hostileInsertOperation(t, db, opID, 1, "admin_user_adjustment", "operation", opID)
	hostileMustExec(t, db, `
INSERT INTO credit_entries(operation_id,line_no,account_id,account_kind_snapshot,delta_sign,delta_mag)
VALUES(?,0,?,'user',1,?)`, opID, userBalance, one)
	hostileMustExec(t, db, `
INSERT INTO credit_entries(operation_id,line_no,account_id,account_kind_snapshot,delta_sign,delta_mag)
VALUES(?,1,?,'user',1,?)`, opID, userBalance, hostileMaxSM128())
	hostileMustExec(t, db, `
INSERT INTO credit_entries(operation_id,line_no,account_id,account_kind_snapshot,delta_sign,delta_mag,balance_after_sign,balance_after_mag)
VALUES(?,2,?,'external',-1,?,-1,?)`, opID, externalBalance, hostileMaxSM128(), hostileMaxSM128())
	hostileMustFail(t, db, `
INSERT INTO credit_entries(operation_id,line_no,account_id,account_kind_snapshot,delta_sign,delta_mag)
VALUES(?,3,?,'user',1,?)`, opID, userBalance, []byte{1})
	hostileMustFail(t, db, `
INSERT INTO credit_entries(operation_id,line_no,account_id,account_kind_snapshot,delta_sign,delta_mag,balance_after_sign,balance_after_mag)
VALUES(?,4,?,'user',1,?,1,?)`, opID, userBalance, one, one)

	// RPS wallet_net is the other signed codec consumer.  Terminal seats may
	// carry either sign with the same sign-safe 127-bit magnitude.
	positiveRPSID := hostileOIDVariant("rps_", 'P', 'Q')
	positiveAccountID := hostileInsertRPSAccount(t, db, positiveRPSID)
	hostileInsertRPSSession(t, db, positiveRPSID, "terminal_processing", "terminal_processing", positiveAccountID)
	hostileInsertRPSSeat(t, db, positiveRPSID, 0, uid, nil, nil, hostileMaxU128(), 1, hostileMaxSM128(), "active")
	negativeRPSID := hostileOIDVariant("rps_", 'N', 'Q')
	negativeAccountID := hostileInsertRPSAccount(t, db, negativeRPSID)
	hostileInsertRPSSession(t, db, negativeRPSID, "terminal_processing", "terminal_processing", negativeAccountID)
	hostileInsertRPSSeat(t, db, negativeRPSID, 0, nil, nil, nil, nil, nil, nil, "deletion_pending")
	hostileMustExec(t, db, `
UPDATE game_rps_seats SET starting_balance=?,terminal_return=?,wallet_net_sign=?,wallet_net_mag=?
WHERE session_id=? AND seat_no=0`, hostileMaxU128(), zero, -1, hostileMaxSM128(), negativeRPSID)
	hostileMustFail(t, db, `
UPDATE game_rps_seats SET wallet_net_sign=0,wallet_net_mag=?
WHERE session_id=? AND seat_no=0`, hostileHigh128(), negativeRPSID)

	// U256 is also full-width and is used for RPS lifetime totals.  A max
	// value is valid, while a wrong-width BLOB is not.
	rpsID := hostileOID("rps_")
	accountID := hostileInsertRPSAccount(t, db, rpsID)
	hostileInsertRPSSession(t, db, rpsID, "gesture", "started", accountID)
	hostileInsertRPSSeat(t, db, rpsID, 0, hostileInsertUser(t, db, "wide-rps", 0, 0), nil, nil, nil, nil, nil, "active")
	hostileMustExec(t, db, `
UPDATE game_rps_sessions SET revision=?,phase_seq=?,identity_epoch=?,cut_seq=?,player_pool=?,
 permanent_multiplier=?,base_round_count=?,paid_tie_count=?,free_tie_count=?,paid_pool_streak=?,
 free_pool_streak=?,platform_cut_total=?,welfare_cut_total=?,thursday_cut_total=?,welfare_carry_total=?
 WHERE id=?`, twoTo63, twoTo63, high, max, max, max, max, max, max, max, max, max, max, max, max, rpsID)
	hostileMustExec(t, db, `UPDATE game_rps_seats SET current_gesture_envelope=?,current_gesture_phase_seq=?,last_action_phase_seq=? WHERE session_id=? AND seat_no=0`, hostileEnvelope(33, 0x01), twoTo63, twoTo63, rpsID)
	hostileMustFail(t, db, `UPDATE game_rps_sessions SET player_pool=? WHERE id=?`, []byte{1}, rpsID)
	hostileMustExec(t, db, `
UPDATE game_rps_seats SET starting_balance=?,current_balance=?,current_round_input=?,rock_count=?,
 scissors_count=?,paper_count=?,timeout_count=? WHERE session_id=? AND seat_no=0`, max, max, max, max, max, max, max, rpsID)
	hostileMustExec(t, db, `UPDATE game_rps_seats SET last_action_phase_seq=? WHERE session_id=? AND seat_no=0`, twoTo63, rpsID)
	hostileMustFail(t, db, `UPDATE game_rps_seats SET current_balance=? WHERE session_id=? AND seat_no=0`, []byte{1}, rpsID)
	hostileMustExec(t, db, `UPDATE game_rps_seats SET total_input=?,total_returned=? WHERE session_id=? AND seat_no=0`, hostileBlob32(0xff), hostileBlob32(0xff), rpsID)
	hostileMustFail(t, db, `UPDATE game_rps_seats SET total_input=? WHERE session_id=? AND seat_no=0`, []byte{1}, rpsID)
}

func TestGenerationTwoHostileAdminAlertKinds(t *testing.T) {
	db := openGenerationTwoDDLForTest(t)
	valid := []string{
		"fetch_failed",
		"forward_error",
		"registration_rejected",
		"maintenance_enabled",
		"donation_failure_disabled",
		"issue_projection_incomplete",
		"report_retry_exhausted",
		"fishing_retry_exhausted",
		"rps_terminal_retrying",
		"worker_checkpoint_failed",
		"invariant_violation",
	}
	for _, kind := range valid {
		hostileMustExec(t, db, `INSERT INTO admin_alerts(kind,created_at) VALUES(?,0)`, kind)
	}
	hostileMustFail(t, db, `INSERT INTO admin_alerts(kind,created_at) VALUES('unknown',0)`)
	hostileMustFail(t, db, `UPDATE admin_alerts SET kind='unknown' WHERE id=1`)
}

func TestGenerationTwoHostileMaintenanceActorMatrix(t *testing.T) {
	database := openGenerationTwoDDLForTest(t)
	adminID := hostileInsertUser(t, database, "maintenance-matrix-admin", 1, 0)
	stewardID := hostileInsertUser(t, database, "maintenance-matrix-steward", 0, 0)

	tests := []struct {
		name                        string
		actorUserID, actorDiscordID any
		actorRole                   any
		valid                       bool
	}{
		{name: "deidentified", valid: true},
		{name: "environment admin", actorUserID: adminID, actorRole: "admin", valid: true},
		{name: "steward", actorUserID: stewardID, actorDiscordID: "steward-discord", actorRole: "steward", valid: true},
		{name: "admin carries Discord", actorUserID: adminID, actorDiscordID: "admin-discord", actorRole: "admin"},
		{name: "steward missing Discord", actorUserID: stewardID, actorRole: "steward"},
		{name: "user only", actorUserID: stewardID},
		{name: "user and Discord without role", actorUserID: stewardID, actorDiscordID: "partial-discord"},
		{name: "Discord only", actorDiscordID: "orphan-discord"},
		{name: "role only", actorRole: "admin"},
		{name: "steward without user", actorDiscordID: "orphan-steward", actorRole: "steward"},
		{name: "admin without user but with Discord", actorDiscordID: "orphan-admin", actorRole: "admin"},
		{name: "all non-null unknown role", actorUserID: stewardID, actorDiscordID: "unknown-role", actorRole: "owner"},
		{name: "admin empty Discord", actorUserID: adminID, actorDiscordID: "", actorRole: "admin"},
		{name: "steward empty Discord", actorUserID: stewardID, actorDiscordID: "", actorRole: "steward"},
		{name: "steward role on admin-shaped null Discord", actorUserID: adminID, actorRole: "steward"},
		{name: "admin role on steward-shaped Discord", actorUserID: stewardID, actorDiscordID: "steward-discord", actorRole: "admin"},
	}
	markers := "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id := hostileOIDVariant("op_", markers[i], 'Q')
			_, err := database.Exec(`
INSERT INTO maintenance_events(
 id,actor_user_id,actor_discord_id,actor_role,action,reason,created_at,
 resolved_at,deidentify_at,retain_until
) VALUES(?,?,?,?,'enable','actor matrix',100,100,7776100,34560100)`,
				id, tt.actorUserID, tt.actorDiscordID, tt.actorRole)
			if tt.valid && err != nil {
				t.Fatalf("valid maintenance actor rejected: %v", err)
			}
			if !tt.valid && err == nil {
				t.Fatal("hostile maintenance actor accepted")
			}
		})
	}
}

func TestGenerationTwoHostileMaintenanceActorDeidentificationDeadline(t *testing.T) {
	database := openGenerationTwoDDLForTest(t)
	adminID := hostileInsertUser(t, database, "maintenance-deidentify-admin", 1, 0)
	stewardID := hostileInsertUser(t, database, "maintenance-deidentify-steward", 0, 0)

	var now int64
	if err := database.QueryRow(`SELECT unixepoch()`).Scan(&now); err != nil {
		t.Fatalf("read SQLite clock: %v", err)
	}
	dueResolvedAt := now - 7776001
	futureResolvedAt := now - 7776000 + 3600

	insertResolved := func(t *testing.T, id string, actorUserID int64, actorDiscordID any, actorRole string, resolvedAt int64) {
		t.Helper()
		hostileMustExec(t, database, `
INSERT INTO maintenance_events(
 id,actor_user_id,actor_discord_id,actor_role,action,reason,created_at,
 resolved_at,deidentify_at,retain_until
) VALUES(?,?,?,?,'disable','lifecycle',?,?,?,?)`,
			id, actorUserID, actorDiscordID, actorRole, resolvedAt,
			resolvedAt, resolvedAt+7776000, resolvedAt+34560000)
	}
	assertActorCleared := func(t *testing.T, id string) {
		t.Helper()
		var actorUserID, actorDiscordID, actorRole any
		if err := database.QueryRow(`
SELECT actor_user_id,actor_discord_id,actor_role
FROM maintenance_events WHERE id=?`, id).Scan(&actorUserID, &actorDiscordID, &actorRole); err != nil {
			t.Fatalf("read maintenance actor %s: %v", id, err)
		}
		if actorUserID != nil || actorDiscordID != nil || actorRole != nil {
			t.Fatalf("maintenance actor %s was not fully cleared: user=%v Discord=%v role=%v", id, actorUserID, actorDiscordID, actorRole)
		}
	}

	for _, tt := range []struct {
		name           string
		id             string
		actorUserID    int64
		actorDiscordID any
		actorRole      string
	}{
		{name: "administrator", id: hostileOIDVariant("op_", 'A', 'Q'), actorUserID: adminID, actorRole: "admin"},
		{name: "steward", id: hostileOIDVariant("op_", 'S', 'Q'), actorUserID: stewardID, actorDiscordID: "steward-discord", actorRole: "steward"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			insertResolved(t, tt.id, tt.actorUserID, tt.actorDiscordID, tt.actorRole, dueResolvedAt)
			hostileMustExec(t, database, `
UPDATE maintenance_events
SET actor_user_id=NULL,actor_discord_id=NULL,actor_role=NULL
WHERE id=?`, tt.id)
			assertActorCleared(t, tt.id)
		})
	}

	insertBoundaryEvent := func(t *testing.T, offset int64) string {
		t.Helper()
		id, err := GenerateOpaqueID("op_")
		if err != nil {
			t.Fatalf("generate boundary event ID: %v", err)
		}
		hostileMustExec(t, database, `
INSERT INTO maintenance_events(
 id,actor_user_id,actor_discord_id,actor_role,action,reason,created_at,
 resolved_at,deidentify_at,retain_until
) VALUES(?,?,NULL,'admin','disable','boundary',
 unixepoch()-7776000+?,unixepoch()-7776000+?,unixepoch()+?,unixepoch()+26784000+?)`,
			id, adminID, offset, offset, offset, offset)
		return id
	}

	// SQLite keeps no-argument date/time functions stable within one sqlite3_step.
	// The WHERE predicate therefore proves the trigger observes the exact same
	// equality boundary. A second rollover between INSERT and UPDATE only yields
	// zero affected rows, so retry with a fresh event instead of accepting a
	// timing-dependent result.
	const boundaryAttempts = 32
	matchedEquality := false
	for attempt := 0; attempt < boundaryAttempts; attempt++ {
		id := insertBoundaryEvent(t, 0)
		result, err := database.Exec(`
UPDATE maintenance_events
SET actor_user_id=NULL,actor_discord_id=NULL,actor_role=NULL
WHERE id=? AND unixepoch()=deidentify_at`, id)
		if err != nil {
			t.Fatalf("actor clear at exact deidentify deadline was rejected: %v", err)
		}
		rows, err := result.RowsAffected()
		if err != nil {
			t.Fatalf("observe exact-deadline actor clear: %v", err)
		}
		if rows == 1 {
			assertActorCleared(t, id)
			matchedEquality = true
			break
		}
		if rows != 0 {
			t.Fatalf("exact-deadline actor clear changed %d rows", rows)
		}
		hostileMustExec(t, database, `DELETE FROM maintenance_events WHERE id=?`, id)
	}
	if !matchedEquality {
		t.Fatal("could not exercise unixepoch()=deidentify_at boundary")
	}

	// Use the same statement-stable clock to prove the strict -1 second side of
	// the boundary. A rollover before this UPDATE is likewise a zero-row retry.
	matchedBeforeDeadline := false
	for attempt := 0; attempt < boundaryAttempts; attempt++ {
		id := insertBoundaryEvent(t, 1)
		result, err := database.Exec(`
UPDATE maintenance_events
SET actor_user_id=NULL,actor_discord_id=NULL,actor_role=NULL
WHERE id=? AND unixepoch()=deidentify_at-1`, id)
		if err != nil {
			if !strings.Contains(err.Error(), "maintenance event is append-only") {
				t.Fatalf("actor clear one second before deadline failed unexpectedly: %v", err)
			}
			var actorUserID int64
			var actorDiscordID sql.NullString
			var actorRole string
			if scanErr := database.QueryRow(`
SELECT actor_user_id,actor_discord_id,actor_role
FROM maintenance_events WHERE id=?`, id).Scan(&actorUserID, &actorDiscordID, &actorRole); scanErr != nil {
				t.Fatalf("read one-second-before actor after rejection: %v", scanErr)
			}
			if actorUserID != adminID || actorDiscordID.Valid || actorRole != "admin" {
				t.Fatalf("one-second-before rejection changed actor: user=%d Discord=%v role=%q", actorUserID, actorDiscordID, actorRole)
			}
			matchedBeforeDeadline = true
			break
		}
		rows, rowsErr := result.RowsAffected()
		if rowsErr != nil {
			t.Fatalf("observe one-second-before actor clear: %v", rowsErr)
		}
		if rows == 1 {
			t.Fatal("actor clear one second before deidentify deadline was accepted")
		}
		if rows != 0 {
			t.Fatalf("one-second-before actor clear changed %d rows", rows)
		}
		hostileMustExec(t, database, `DELETE FROM maintenance_events WHERE id=?`, id)
	}
	if !matchedBeforeDeadline {
		t.Fatal("could not exercise unixepoch()=deidentify_at-1 boundary")
	}

	futureID := hostileOIDVariant("op_", 'F', 'Q')
	insertResolved(t, futureID, stewardID, "future-steward", "steward", futureResolvedAt)
	hostileMustFail(t, database, `
UPDATE maintenance_events
SET actor_user_id=NULL,actor_discord_id=NULL,actor_role=NULL
WHERE id=?`, futureID)

	immutableID := hostileOIDVariant("op_", 'I', 'Q')
	insertResolved(t, immutableID, adminID, nil, "admin", dueResolvedAt)
	hostileMustFail(t, database, `
UPDATE maintenance_events
SET actor_user_id=NULL,actor_discord_id=NULL,actor_role=NULL,reason='rewritten'
WHERE id=?`, immutableID)

	heldID := hostileOIDVariant("op_", 'H', 'Q')
	insertResolved(t, heldID, stewardID, "held-steward", "steward", dueResolvedAt)
	holdID := hostileOIDVariant("lgh_", 'H', 'Q')
	hostileMustExec(t, database, `
INSERT INTO legal_holds(
 id,object_kind,object_ref,state,revision,basis,created_by_user_id,created_at,expires_at
) VALUES(?,'maintenance_event',?,'active',1,'lifecycle hold',?,?,?)`,
		holdID, heldID, adminID, now-10, now+3600)
	hostileMustExec(t, database, `UPDATE maintenance_events SET legal_hold_consumed=1 WHERE id=?`, heldID)
	hostileMustExec(t, database, `
UPDATE maintenance_events
SET actor_user_id=NULL,actor_discord_id=NULL,actor_role=NULL
WHERE id=?`, heldID)
	assertActorCleared(t, heldID)
	var marker int
	var holdState string
	if err := database.QueryRow(`SELECT legal_hold_consumed FROM maintenance_events WHERE id=?`, heldID).Scan(&marker); err != nil {
		t.Fatalf("read held maintenance marker: %v", err)
	}
	if err := database.QueryRow(`SELECT state FROM legal_holds WHERE id=?`, holdID).Scan(&holdState); err != nil {
		t.Fatalf("read active legal hold: %v", err)
	}
	if marker != 1 || holdState != "active" {
		t.Fatalf("actor clear changed legal hold facts: marker=%d state=%q", marker, holdState)
	}
}

func TestGenerationTwoHostileOIDPrefixesAndNotNull(t *testing.T) {
	db := openGenerationTwoDDLForTest(t)
	uid := hostileInsertUser(t, db, "oid", 0, 0)

	validOperation := hostileOID("op_")
	hostileMustFail(t, db, `
INSERT INTO accepted_operations(id,kind,payload_hash,created_at)
VALUES(NULL,'model_discovery',?,0)`, hostileBlob32(1))
	hostileMustFail(t, db, `
INSERT INTO accepted_operations(id,kind,payload_hash,created_at)
VALUES(?,'model_discovery',?,0)`, hostileBadOID("op_"), hostileBlob32(1))
	hostileMustExec(t, db, `
INSERT INTO accepted_operations(id,kind,payload_hash,state,created_at)
VALUES(?,'model_discovery',?,'accepted',0)`, validOperation, hostileBlob32(1))

	req := hostileOID("req_")
	hostileInsertLogicalRequest(t, db, req, uid, "openai_chat_completions", 1)
	hostileMustFail(t, db, `
INSERT INTO logical_requests(
 id,user_id,route_kind,state,attempt_limit,accounting_state,
 settlement_destination,ledger_rows_remaining,created_at
) VALUES(NULL,?,'openai_chat_completions','accepted',1,'none','user',?,0)`, uid, hostileBlob16(1))
	hostileMustFail(t, db, `
INSERT INTO logical_requests(
 id,user_id,route_kind,state,attempt_limit,accounting_state,
 settlement_destination,ledger_rows_remaining,created_at
) VALUES(? ,?,'openai_chat_completions','accepted',1,'none','user',?,0)`, hostileBadOID("op_"), uid, hostileBlob16(1))

	claim := hostileOID("clm_")
	hostileMustFail(t, db, `
INSERT INTO dispatch_claims(id,logical_request_id,attempt_seq,purpose,claim_now,state,terminal_at,donor_reward_state)
VALUES(NULL,?,1,'self',0,'released',0,'not_applicable')`, req)
	hostileMustFail(t, db, `
INSERT INTO dispatch_claims(id,logical_request_id,attempt_seq,purpose,claim_now,state,terminal_at,donor_reward_state)
VALUES(?, ?,1,'self',0,'released',0,'not_applicable')`, hostileBadOID("clm_"), req)
	hostileMustFail(t, db, `
INSERT INTO dispatch_claims(id,logical_request_id,attempt_seq,purpose,claim_now,state,terminal_at,donor_reward_state)
VALUES(?, ?,1,'self',0,'released',0,'not_applicable')`, claim, hostileBadOID("req_"))

	// Source identities are a closed prefix map, not a generic OID slot.
	otherOperation := hostileOID("op_")[:24] + "g"
	hostileMustFail(t, db, `
INSERT INTO credit_operations(
 id,ledger_seq,kind,source_type,source_id,source_seq,
 donation_credit_delta_sign,donation_credit_delta_mag,created_at
) VALUES(?,1,'welfare_claim','operation',?, ?,0,?,0)`, otherOperation, req, hostileBlob16(0), hostileBlob16(0))
	hostileMustFail(t, db, `
INSERT INTO credit_operations(
 id,ledger_seq,kind,source_type,source_id,source_seq,
 donation_credit_delta_sign,donation_credit_delta_mag,created_at
) VALUES(?,1,'welfare_claim','operation',NULL, ?,0,?,0)`, otherOperation, hostileBlob16(0), hostileBlob16(0))

	reportID := hostileOID("rpc_")
	hostileInsertReportCase(t, db, reportID, hostileBlob32(9), "pending_review", "complete", 0)
	hostileMustFail(t, db, `
INSERT INTO report_targets(
 id,case_id,target_seq,key_ref,connector_type,canonical_base_url,state,discovered_version,created_at,updated_at
) VALUES(?,?,0,?,'openai-compatible','https://upstream.example/v1','protected',1,0,0)`, hostileBadOID("rpt_"), reportID, hostileBlob32(1))
	hostileMustFail(t, db, `
INSERT INTO report_targets(
 id,case_id,target_seq,key_ref,connector_type,canonical_base_url,state,discovered_version,created_at,updated_at
) VALUES(?,?,0,?,'openai-compatible','https://upstream.example/v1','protected',1,0,0)`, hostileOID("rpt_"), hostileBadOID("rpc_"), hostileBlob32(1))

	hostileMustFail(t, db, `
INSERT INTO legal_holds(id,object_kind,object_ref,state,revision,basis,created_by_user_id,created_at,expires_at)
VALUES(NULL,'report_case',?, 'active',1,'hostile',?,0,100)`, reportID, uid)
	hostileMustFail(t, db, `
INSERT INTO legal_holds(id,object_kind,object_ref,state,revision,basis,created_by_user_id,created_at,expires_at)
VALUES(?, 'report_case',?, 'active',1,'hostile',?,0,100)`, hostileBadOID("lgh_"), reportID, uid)
}

func TestGenerationTwoHostileRequestClaimAttemptMatrices(t *testing.T) {
	db := openGenerationTwoDDLForTest(t)
	uid := hostileInsertUser(t, db, "request", 0, 0)

	hostileInsertTerminalRequest(t, db, hostileOIDVariant("req_", 'T', 'Q'), uid, "openai_chat_completions", "success", 200, nil)
	hostileInsertTerminalRequest(t, db, hostileOIDVariant("req_", 'F', 'g'), uid, "openai_chat_completions", "failed", 400, "invalid_request")
	hostileInsertTerminalRequest(t, db, hostileOIDVariant("req_", 'C', 'w'), uid, "openai_chat_completions", "cancelled", nil, nil)

	for _, tc := range []struct {
		name      string
		class     any
		status    any
		errorCode any
	}{
		{"success-status-too-low", "success", 400, nil},
		{"failed-status-too-low", "failed", 200, "invalid_request"},
		{"cancelled-status-present", "cancelled", 200, nil},
		{"failed-unknown-error", "failed", 400, "not-a-stable-code"},
	} {
		t.Run("terminal-"+tc.name, func(t *testing.T) {
			hostileMustFail(t, db, `
INSERT INTO logical_requests(
 id,user_id,route_kind,state,attempt_limit,caller_result_class,caller_status,
 caller_error_code,accounting_state,settlement_destination,ledger_rows_remaining,
 created_at,terminal_at
) VALUES(?,?,?,'terminal',1,?,?,?,'none','user',?,0,1)`,
				hostileOIDVariant("req_", byte('a'+len(tc.name)), 'A'), uid,
				"openai_chat_completions", tc.class, tc.status, tc.errorCode, hostileBlob16(0))
		})
	}
	runningID := hostileOIDVariant("req_", 'G', 'Q')
	hostileMustExec(t, db, `
INSERT INTO logical_requests(
 id,user_id,route_kind,state,attempt_limit,accounting_state,settlement_destination,
 ledger_rows_remaining,created_at
) VALUES(?,?,?,'running',1,'none','user',?,0)`, runningID, uid, "openai_chat_completions", hostileBlob16(1))
	reservedID := hostileOIDVariant("req_", 'V', 'Q')
	// A zero-price reservation is still a real reserved state; only a
	// non-reserved row carrying a non-zero account reservation is hostile.
	hostileMustExec(t, db, `
INSERT INTO logical_requests(
 id,user_id,route_kind,state,attempt_limit,accounting_state,account_reserved_milli,
 settlement_destination,ledger_rows_remaining,created_at
) VALUES(?,?,?,'accepted',1,'reserved',0,'user',?,0)`, reservedID, uid, "openai_chat_completions", hostileBlob16(1))

	nonterminal := hostileOIDVariant("req_", 'N', 'Q')
	hostileInsertLogicalRequest(t, db, nonterminal, uid, "openai_chat_completions", 1)
	hostileMustFail(t, db, `
UPDATE logical_requests SET caller_result_class='success',caller_status=200,terminal_at=1 WHERE id=?`, nonterminal)
	hostileMustFail(t, db, `
UPDATE logical_requests SET accounting_state='committed',account_reserved_milli=1 WHERE id=?`, nonterminal)
	hostileMustFail(t, db, `
INSERT INTO logical_requests(
 id,user_id,route_kind,state,attempt_limit,accounting_state,settlement_destination,
 ledger_rows_remaining,created_at
) VALUES(?,?,?,'accepted',2,'none','user',?,0)`, hostileOIDVariant("req_", 'D', 'Q'), uid, "model_discovery", hostileBlob16(0))
	hostileMustFail(t, db, `
INSERT INTO logical_requests(
 id,user_id,route_kind,state,attempt_limit,accounting_state,settlement_destination,
 account_reserved_milli,ledger_rows_remaining,created_at
) VALUES(?,?,?,'accepted',1,'none','user',1,?,0)`, hostileOIDVariant("req_", 'R', 'Q'), uid, "openai_chat_completions", hostileBlob16(0), hostileBlob16(0))

	endpointID := hostileInsertEndpoint(t, db, uid, "https://upstream.example/v1")
	secretID := hostileInsertSecret(t, db, "https://upstream.example/v1", 0)
	keyID := hostileInsertEndpointKey(t, db, endpointID, secretID)
	_ = keyID
	claimRequest := hostileOIDVariant("req_", 'L', 'Q')
	hostileInsertLogicalRequest(t, db, claimRequest, uid, "openai_chat_completions", 100)

	// Claim state transitions are represented by nullable secret/dispatch/
	// terminal fields.  Each valid matrix has a distinct canonical claim ID.
	hostileMustExec(t, db, `
INSERT INTO dispatch_claims(id,logical_request_id,attempt_seq,purpose,secret_ref_id,claim_now,state,donor_reward_state)
		VALUES(?,?,1,'self',?,0,'claimed','not_applicable')`, hostileOIDVariant("clm_", 'a', 'Q'), claimRequest, secretID)
	hostileMustExec(t, db, `
INSERT INTO dispatch_claims(id,logical_request_id,attempt_seq,purpose,secret_ref_id,claim_now,state,dispatched_at,donor_reward_state)
		VALUES(?,?,2,'self',?,0,'dispatched',1,'not_applicable')`, hostileOIDVariant("clm_", 'b', 'Q'), claimRequest, secretID)
	hostileMustExec(t, db, `
INSERT INTO dispatch_claims(id,logical_request_id,attempt_seq,purpose,claim_now,state,dispatched_at,terminal_at,donor_reward_state)
		VALUES(?,?,3,'self',0,'committed',1,2,'not_applicable')`, hostileOIDVariant("clm_", 'c', 'Q'), claimRequest)
	hostileMustExec(t, db, `
INSERT INTO dispatch_claims(id,logical_request_id,attempt_seq,purpose,claim_now,state,terminal_at,donor_reward_state)
		VALUES(?,?,4,'self',0,'released',2,'not_applicable')`, hostileOIDVariant("clm_", 'd', 'Q'), claimRequest)
	hostileMustFail(t, db, `
INSERT INTO dispatch_claims(id,logical_request_id,attempt_seq,purpose,claim_now,state,terminal_at,donor_reward_state)
		VALUES(?,?,5,'self',0,'claimed',NULL,'not_applicable')`, hostileOIDVariant("clm_", 'e', 'Q'), claimRequest)
	hostileMustFail(t, db, `
INSERT INTO dispatch_claims(id,logical_request_id,attempt_seq,purpose,secret_ref_id,claim_now,state,terminal_at,donor_reward_state)
		VALUES(?,?,6,'self',?,0,'committed',2,'not_applicable')`, hostileOIDVariant("clm_", 'f', 'Q'), claimRequest, secretID)
	hostileMustFail(t, db, `
INSERT INTO dispatch_claims(id,logical_request_id,attempt_seq,purpose,claim_now,state,terminal_at,donor_reward_state)
VALUES(?,?,101,'self',0,'released',2,'not_applicable')`, hostileOIDVariant("clm_", 'g', 'Q'), claimRequest)
	hostileMustFail(t, db, `
INSERT INTO logical_requests(
 id,user_id,route_kind,state,attempt_limit,accounting_state,settlement_destination,
 ledger_rows_remaining,created_at
) VALUES(?,?,?,'accepted',1,'none','external',?,0)`, hostileOIDVariant("req_", 'E', 'Q'), uid, "openai_chat_completions", hostileBlob16(0))

	// Acceptance reserves one worker row for self requests, one plus the
	// charity attempt limit for charity requests, and no row for discovery.
	// These core acceptance facts are immutable; only a running request may
	// consume its remaining reservation.
	acceptanceID := hostileOIDVariant("req_", 'A', 'Q')
	hostileInsertLogicalRequest(t, db, acceptanceID, uid, "openai_chat_completions", 1)
	var selfRemaining string
	if err := db.QueryRow(`SELECT hex(ledger_rows_remaining) FROM logical_requests WHERE id=?`, acceptanceID).Scan(&selfRemaining); err != nil {
		t.Fatal(err)
	}
	if selfRemaining != "00000000000000000000000000000001" {
		t.Fatalf("self acceptance reservation mismatch: %s", selfRemaining)
	}
	hostileMustFail(t, db, `UPDATE logical_requests SET attempt_limit=2 WHERE id=?`, acceptanceID)
	hostileMustFail(t, db, `UPDATE logical_requests SET model_snapshot='changed' WHERE id=?`, acceptanceID)
	hostileMustFail(t, db, `UPDATE logical_requests SET route_kind='model_discovery' WHERE id=?`, acceptanceID)
	hostileMustFail(t, db, `UPDATE logical_requests SET created_at=1 WHERE id=?`, acceptanceID)
	charityAcceptanceID := hostileOIDVariant("req_", 'B', 'Q')
	hostileInsertLogicalRequest(t, db, charityAcceptanceID, uid, "charity_chat_completions", 6)
	var charityRemaining string
	if err := db.QueryRow(`SELECT hex(ledger_rows_remaining) FROM logical_requests WHERE id=?`, charityAcceptanceID).Scan(&charityRemaining); err != nil {
		t.Fatal(err)
	}
	if charityRemaining != "00000000000000000000000000000007" {
		t.Fatalf("charity acceptance reservation mismatch: %s", charityRemaining)
	}
	discoveryAcceptanceID := hostileOIDVariant("req_", 'H', 'Q')
	hostileInsertLogicalRequest(t, db, discoveryAcceptanceID, uid, "model_discovery", 1)
	var discoveryRemaining string
	if err := db.QueryRow(`SELECT hex(ledger_rows_remaining) FROM logical_requests WHERE id=?`, discoveryAcceptanceID).Scan(&discoveryRemaining); err != nil {
		t.Fatal(err)
	}
	if discoveryRemaining != "00000000000000000000000000000000" {
		t.Fatalf("discovery acceptance reservation mismatch: %s", discoveryRemaining)
	}
	runningDecrementID := hostileOIDVariant("req_", 'I', 'Q')
	hostileInsertLogicalRequest(t, db, runningDecrementID, uid, "openai_chat_completions", 1)
	hostileMustExec(t, db, `UPDATE logical_requests SET state='running',ledger_rows_remaining=? WHERE id=?`, hostileBlob16(0), runningDecrementID)

	hostileMustFail(t, db, `UPDATE logical_requests SET settlement_destination='external' WHERE id=?`, nonterminal)
	hostileMustExec(t, db, `UPDATE logical_requests SET user_id=NULL,settlement_destination='external' WHERE id=?`, claimRequest)
	hostileMustFail(t, db, `UPDATE logical_requests SET user_id=?,settlement_destination='user' WHERE id=?`, uid, claimRequest)

	// Charity claims use an explicit reward-state matrix.  Receiver deletion,
	// zero reward and posted reward are distinct terminal facts.
	charityReq := hostileOIDVariant("req_", 'Y', 'Q')
	hostileInsertLogicalRequest(t, db, charityReq, uid, "charity_chat_completions", 6)
	hostileMustExec(t, db, `
INSERT INTO dispatch_claims(id,logical_request_id,attempt_seq,purpose,secret_ref_id,claim_now,state,donor_reward_state)
		VALUES(?,?,1,'charity',?,0,'claimed','pending')`, hostileOIDVariant("clm_", 'g', 'Q'), charityReq, secretID)
	hostileMustExec(t, db, `
INSERT INTO dispatch_claims(id,logical_request_id,attempt_seq,purpose,claim_now,state,dispatched_at,terminal_at,receiver_user_id,donor_reward_actual_milli,donor_reward_state)
		VALUES(?,?,2,'charity',0,'committed',1,2,?,?, 'posted')`, hostileOIDVariant("clm_", 'h', 'Q'), charityReq, uid, 1)
	hostileMustExec(t, db, `
INSERT INTO dispatch_claims(id,logical_request_id,attempt_seq,purpose,claim_now,state,dispatched_at,terminal_at,donor_reward_actual_milli,donor_reward_state)
		VALUES(?,?,3,'charity',0,'committed',1,2,0,'zero')`, hostileOIDVariant("clm_", 'i', 'Q'), charityReq)
	hostileMustExec(t, db, `
INSERT INTO dispatch_claims(id,logical_request_id,attempt_seq,purpose,claim_now,state,dispatched_at,terminal_at,donor_reward_actual_milli,donor_reward_state)
		VALUES(?,?,4,'charity',0,'committed',1,2,1,'receiver_deleted')`, hostileOIDVariant("clm_", 'j', 'Q'), charityReq)
	hostileMustExec(t, db, `
INSERT INTO dispatch_claims(id,logical_request_id,attempt_seq,purpose,claim_now,state,terminal_at,donor_reward_state)
		VALUES(?,?,5,'charity',0,'released',2,'not_due')`, hostileOIDVariant("clm_", 'k', 'Q'), charityReq)
	hostileMustFail(t, db, `
INSERT INTO dispatch_claims(id,logical_request_id,attempt_seq,purpose,secret_ref_id,claim_now,state,donor_reward_state)
		VALUES(?,?,6,'charity',?,0,'claimed','not_applicable')`, hostileOIDVariant("clm_", 'l', 'Q'), charityReq, secretID)

	logReq := hostileOIDVariant("req_", 'Z', 'Q')
	hostileInsertLogicalRequest(t, db, logReq, uid, "openai_chat_completions", 2)
	logID := hostileInsertRequestLog(t, db, logReq, uid, "openai_chat_completions")
	hostileInsertAttempt(t, db, hostileOID("clm_"), logID, 1)
	hostileInsertAttempt(t, db, hostileOIDVariant("clm_", 'o', 'Q'), logID, 100)
	hostileMustFail(t, db, `
INSERT INTO request_attempts(
 claim_id,request_log_id,attempt_seq,connector_type,canonical_base_url,upstream_model_id,
 result_kind,started_at,completed_at
) VALUES(?,?,0,'openai-compatible','https://upstream.example/v1','upstream','response',0,1)`, hostileOIDVariant("clm_", 'p', 'Q'), logID)
	hostileMustFail(t, db, `
INSERT INTO request_attempts(
 claim_id,request_log_id,attempt_seq,connector_type,canonical_base_url,upstream_model_id,
 result_kind,started_at,completed_at
) VALUES(?,?,101,'openai-compatible','https://upstream.example/v1','upstream','response',0,1)`, hostileOIDVariant("clm_", 'q', 'Q'), logID)
	hostileMustFail(t, db, `
INSERT INTO request_attempts(
 claim_id,request_log_id,attempt_seq,connector_type,canonical_base_url,upstream_model_id,
 result_kind,upstream_status,started_at,completed_at
	) VALUES(?,?,1,'openai-compatible','https://upstream.example/v1','upstream','bad',200,0,1)`, hostileOIDVariant("clm_", 'l', 'Q'), logID)
	hostileMustFail(t, db, `
INSERT INTO request_attempts(
 claim_id,request_log_id,attempt_seq,connector_type,canonical_base_url,upstream_model_id,
 result_kind,upstream_status,started_at,completed_at
	) VALUES(?,?,2,'openai-compatible','https://upstream.example/v1','upstream','response',99,0,1)`, hostileOIDVariant("clm_", 'm', 'Q'), logID)
	hostileMustFail(t, db, `
INSERT INTO request_attempts(
 claim_id,request_log_id,attempt_seq,connector_type,canonical_base_url,upstream_model_id,
 result_kind,started_at,completed_at
	) VALUES(?,?,3,'openai-compatible','https://upstream.example/v1','upstream','response',?,1)`, hostileOIDVariant("clm_", 'n', 'Q'), logID, hostileTimeMax+1)

	// request_logs must be a complete snapshot of its logical request, not an
	// independently writable caller result.
	hostileMustFail(t, db, `
INSERT INTO request_logs(logical_request_id,user_id,route_kind,started_at)
VALUES(?,NULL,'openai_chat_completions',0)`, logReq)
	hostileMustFail(t, db, `
INSERT INTO request_logs(logical_request_id,user_id,route_kind,started_at)
VALUES(?,?,'charity_chat_completions',0)`, logReq, uid)
}

func TestGenerationTwoHostileLogicalRequestTerminalSnapshot(t *testing.T) {
	db := openGenerationTwoDDLForTest(t)
	uid := hostileInsertUser(t, db, "terminal-snapshot", 0, 0)
	requestID := hostileOIDVariant("req_", 'T', 'Q')
	hostileInsertLogicalRequest(t, db, requestID, uid, "openai_chat_completions", 1)
	requestLogID := hostileInsertRequestLog(t, db, requestID, uid, "openai_chat_completions")

	// The terminal transition is a single DB transaction.  Updating the
	// logical request is the owner operation; the DDL atomically copies its
	// caller result into the one request-log snapshot before commit.
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`
UPDATE logical_requests
SET state='terminal',caller_result_class='success',caller_status=200,terminal_at=1
WHERE id=?`, requestID); err != nil {
		_ = tx.Rollback()
		t.Fatalf("terminal logical-request update: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("terminal snapshot commit: %v", err)
	}

	var logicalUser, logUser int64
	var logicalRoute, logRoute, logicalClass, logClass, logicalError, logError string
	var logSafeError string
	var logicalStatus, logStatus, logStatusCode, logicalTerminal, logCompleted int64
	if err := db.QueryRow(`
SELECT r.user_id,l.user_id,r.route_kind,l.route_kind,
       r.caller_result_class,l.caller_result_class,r.caller_status,l.caller_status,
       COALESCE(r.caller_error_code,''),COALESCE(l.caller_error_code,''),
       l.status_code,COALESCE(l.error_code,''),r.terminal_at,l.completed_at
FROM logical_requests r JOIN request_logs l ON l.logical_request_id=r.id
WHERE r.id=?`, requestID).Scan(
		&logicalUser, &logUser, &logicalRoute, &logRoute, &logicalClass, &logClass,
		&logicalStatus, &logStatus, &logicalError, &logError, &logStatusCode, &logSafeError,
		&logicalTerminal, &logCompleted); err != nil {
		t.Fatal(err)
	}
	if logicalUser != logUser || logicalRoute != logRoute || logicalClass != logClass ||
		logicalStatus != logStatus || logicalError != logError || logStatusCode != logicalStatus ||
		logSafeError != logicalError || logicalTerminal != logCompleted {
		t.Fatalf("terminal logical/log snapshot diverged: request=(%d,%s,%s,%d,%q,%d) log=(%d,%s,%s,%d,%q,status_code=%d,error_code=%q,completed=%d)", logicalUser, logicalRoute, logicalClass, logicalStatus, logicalError, logicalTerminal, logUser, logRoute, logClass, logStatus, logError, logStatusCode, logSafeError, logCompleted)
	}
	hostileMustFail(t, db, `UPDATE request_logs SET status_code=500 WHERE id=?`, requestLogID)

	logTamperRequestID := hostileOIDVariant("req_", 'L', 'Q')
	hostileInsertLogicalRequest(t, db, logTamperRequestID, uid, "openai_chat_completions", 1)
	logTamperID := hostileInsertRequestLog(t, db, logTamperRequestID, uid, "openai_chat_completions")
	hostileMustFail(t, db, `
UPDATE request_logs SET caller_result_class='success',caller_status=200,status_code=200,completed_at=1,error_code=''
WHERE id=?`, logTamperID)
	hostileMustFail(t, db, `UPDATE request_logs SET status_code=500 WHERE id=?`, logTamperID)

	logicalTamperRequestID := hostileOIDVariant("req_", 'M', 'Q')
	hostileInsertLogicalRequest(t, db, logicalTamperRequestID, uid, "openai_chat_completions", 1)
	logicalTamperLogID := hostileInsertRequestLog(t, db, logicalTamperRequestID, uid, "openai_chat_completions")
	hostileMustExec(t, db, `
UPDATE logical_requests
SET state='terminal',caller_result_class='failed',caller_status=500,caller_error_code='upstream',terminal_at=1
	WHERE id=?`, logicalTamperRequestID)
	hostileMustFail(t, db, `UPDATE request_logs SET error_code='internal' WHERE id=?`, logicalTamperLogID)
	hostileMustFail(t, db, `UPDATE request_logs SET caller_error_code='internal' WHERE id=?`, logicalTamperLogID)
	var failedStatusCode int
	var failedSafeError string
	if err := db.QueryRow(`SELECT status_code,error_code FROM request_logs WHERE id=?`, logicalTamperLogID).Scan(&failedStatusCode, &failedSafeError); err != nil {
		t.Fatal(err)
	}
	if failedStatusCode != 500 || failedSafeError != "upstream" {
		t.Fatalf("failed logical terminal did not sync inherited log status: status_code=%d error_code=%q", failedStatusCode, failedSafeError)
	}

	cancelledRequestID := hostileOIDVariant("req_", 'C', 'Q')
	hostileInsertLogicalRequest(t, db, cancelledRequestID, uid, "openai_chat_completions", 1)
	cancelledLogID := hostileInsertRequestLog(t, db, cancelledRequestID, uid, "openai_chat_completions")
	hostileMustExec(t, db, `
UPDATE logical_requests
SET state='terminal',caller_result_class='cancelled',caller_status=NULL,caller_error_code=NULL,terminal_at=1
	WHERE id=?`, cancelledRequestID)
	hostileMustFail(t, db, `UPDATE request_logs SET status_code=499,error_code='upstream' WHERE id=?`, cancelledLogID)
	var cancelledStatusCode int
	var cancelledSafeError string
	if err := db.QueryRow(`SELECT status_code,error_code FROM request_logs WHERE id=?`, cancelledLogID).Scan(&cancelledStatusCode, &cancelledSafeError); err != nil {
		t.Fatal(err)
	}
	if cancelledStatusCode != 0 || cancelledSafeError != "" {
		t.Fatalf("cancelled logical terminal did not clear inherited log status: status_code=%d error_code=%q", cancelledStatusCode, cancelledSafeError)
	}

	// Terminal facts have no revival path.  The same one-way rule applies to
	// dispatch claims and accepted worker operations after their terminal
	// outcome is recorded.
	hostileMustFail(t, db, `
UPDATE logical_requests
SET state='accepted',caller_result_class=NULL,caller_status=NULL,caller_error_code=NULL,terminal_at=NULL
WHERE id=?`, requestID)
	hostileMustFail(t, db, `
UPDATE logical_requests
SET state='running',caller_result_class=NULL,caller_status=NULL,caller_error_code=NULL,terminal_at=NULL
WHERE id=?`, requestID)
	claimStateRequestID := hostileOIDVariant("req_", 'S', 'Q')
	hostileInsertLogicalRequest(t, db, claimStateRequestID, uid, "openai_chat_completions", 2)
	hostileMustExec(t, db, `
INSERT INTO dispatch_claims(id,logical_request_id,attempt_seq,purpose,claim_now,state,dispatched_at,terminal_at,donor_reward_state)
VALUES(?,?,1,'self',0,'committed',1,2,'not_applicable')`, hostileOIDVariant("clm_", 'c', 'Q'), claimStateRequestID)
	hostileMustExec(t, db, `
INSERT INTO dispatch_claims(id,logical_request_id,attempt_seq,purpose,claim_now,state,terminal_at,donor_reward_state)
VALUES(?,?,2,'self',0,'released',1,'not_applicable')`, hostileOIDVariant("clm_", 'd', 'Q'), claimStateRequestID)
	hostileMustFail(t, db, `UPDATE dispatch_claims SET state='released' WHERE id=?`, hostileOIDVariant("clm_", 'c', 'Q'))
	hostileMustFail(t, db, `UPDATE dispatch_claims SET state='committed' WHERE id=?`, hostileOIDVariant("clm_", 'd', 'Q'))
	completedOperationID := hostileOIDVariant("op_", 'T', 'Q')
	hostileMustExec(t, db, `
INSERT INTO accepted_operations(id,kind,payload_hash,state,created_at,terminal_at)
VALUES(?,'model_discovery',?,'accepted',0,NULL)`, completedOperationID, hostileBlob32(41))
	hostileMustExec(t, db, `UPDATE accepted_operations SET state='completed',terminal_at=1 WHERE id=?`, completedOperationID)
	hostileMustFail(t, db, `UPDATE accepted_operations SET state='accepted',terminal_at=NULL WHERE id=?`, completedOperationID)
	hostileMustFail(t, db, `UPDATE accepted_operations SET state='running',terminal_at=NULL WHERE id=?`, completedOperationID)
	blockedOperationID := hostileOIDVariant("op_", 'B', 'Q')
	hostileMustExec(t, db, `
INSERT INTO accepted_operations(id,kind,payload_hash,state,last_error_class,created_at,terminal_at)
VALUES(?,'model_discovery',?,'failed_blocked','invariant_violation',0,1)`, blockedOperationID, hostileBlob32(42))
	hostileMustFail(t, db, `UPDATE accepted_operations SET state='running',last_error_class=NULL,terminal_at=NULL WHERE id=?`, blockedOperationID)

	// A held request log survives cleanup of its logical request as an
	// independent safe aggregate.  Its opaque logical-request identity is not a
	// foreign key and therefore remains stable while the held log and attempts
	// remain available for retention processing.
	heldRequestID := hostileOIDVariant("req_", 'H', 'Q')
	hostileInsertLogicalRequest(t, db, heldRequestID, uid, "openai_chat_completions", 1)
	heldLogID := hostileInsertRequestLog(t, db, heldRequestID, uid, "openai_chat_completions")
	hostileInsertAttempt(t, db, hostileOIDVariant("clm_", 'H', 'Q'), heldLogID, 1)
	heldHoldID := hostileOIDVariant("lgh_", 'H', 'Q')
	hostileInsertLegalHold(t, db, heldHoldID, "request_log", fmt.Sprintf("%d", heldLogID), hostileInsertUser(t, db, "terminal-snapshot-admin", 1, 0))
	hostileMustExec(t, db, `UPDATE request_logs SET legal_hold_consumed=1 WHERE id=?`, heldLogID)
	hostileMustExec(t, db, `DELETE FROM logical_requests WHERE id=?`, heldRequestID)
	var retainedRequestID sql.NullString
	var retainedMarker int
	if err := db.QueryRow(`SELECT logical_request_id,legal_hold_consumed FROM request_logs WHERE id=?`, heldLogID).Scan(&retainedRequestID, &retainedMarker); err != nil {
		t.Fatal(err)
	}
	if !retainedRequestID.Valid || retainedRequestID.String != heldRequestID || retainedMarker != 1 {
		t.Fatalf("held request log was not independently retained: logical_request_id=%v marker=%d", retainedRequestID, retainedMarker)
	}
}

func TestGenerationTwoHostileSecretReportDonationMembership(t *testing.T) {
	db := openGenerationTwoDDLForTest(t)
	twoTo63 := hostileTwoTo63U128()
	high := hostileHigh128()
	max := hostileMaxU128()
	uid := hostileInsertUser(t, db, "secret-report", 0, 0)
	endpointID := hostileInsertEndpoint(t, db, uid, "https://upstream.example/v1")
	secretID := hostileInsertSecret(t, db, "https://upstream.example/v1", 0)
	keyID := hostileInsertEndpointKey(t, db, endpointID, secretID)

	// Endpoint and secret identity are immutable at the DDL boundary.
	hostileMustFail(t, db, `UPDATE endpoints SET connector_type='anthropic-compatible' WHERE id=?`, endpointID)
	hostileMustFail(t, db, `UPDATE endpoints SET base_url='https://other.example' WHERE id=?`, endpointID)
	hostileMustFail(t, db, `UPDATE endpoint_key_secrets SET context_id=? WHERE id=?`, hostileBlob16(7), secretID)
	hostileMustFail(t, db, `UPDATE endpoint_key_secrets SET canonical_base_url='https://other.example' WHERE id=?`, secretID)
	hostileMustFail(t, db, `UPDATE endpoint_key_secrets SET encrypted_secret='tampered' WHERE id=?`, secretID)

	donationID := hostileInsertDonation(t, db, uid)
	donationKeyID := hostileInsertDonationKey(t, db, donationID, keyID)
	secondEndpointID := hostileInsertEndpoint(t, db, uid, "https://second.example/v1")
	secondSecretID := hostileInsertSecret(t, db, "https://second.example/v1", 1)
	secondEndpointKeyID := hostileInsertEndpointKey(t, db, secondEndpointID, secondSecretID)
	secondDonationKeyID := hostileInsertDonationKey(t, db, donationID, secondEndpointKeyID)
	hostileMustExec(t, db, `
INSERT INTO donation_key_memberships(endpoint_key_id,donation_key_id,donation_id,created_at)
VALUES(?,?,?,0)`, keyID, donationKeyID, donationID)
	// The membership row must agree with both the donation-key owner and the
	// physical endpoint-key identity; matching only the donation is not enough.
	hostileMustFail(t, db, `
INSERT INTO donation_key_memberships(endpoint_key_id,donation_key_id,donation_id,created_at)
VALUES(?,?,?,0)`, keyID, secondDonationKeyID, donationID)
	hostileMustExec(t, db, `
INSERT INTO donation_key_memberships(endpoint_key_id,donation_key_id,donation_id,created_at)
VALUES(?,?,?,0)`, secondEndpointKeyID, secondDonationKeyID, donationID)
	hostileMustFail(t, db, `
INSERT INTO donation_key_memberships(endpoint_key_id,donation_key_id,donation_id,created_at)
VALUES(?,?,?,0)`, keyID, donationKeyID, donationID+1)
	hostileMustFail(t, db, `
INSERT INTO donation_key_memberships(endpoint_key_id,donation_key_id,donation_id,created_at)
VALUES(NULL,?,?,0)`, donationKeyID, donationID)
	hostileMustFail(t, db, `
INSERT INTO donation_key_memberships(endpoint_key_id,donation_key_id,donation_id,created_at)
VALUES(?,?,?,0)`, keyID, donationKeyID, donationID)

	// A physical key cannot be orphan-marked or deleted while the live key
	// rail references it.  Once that rail is removed, NULL -> timestamp is the
	// only legal marker transition; it cannot be cleared or rewritten.
	hostileMustFail(t, db, `UPDATE endpoint_key_secrets SET orphaned_at=1 WHERE id=?`, secretID)
	hostileMustFail(t, db, `DELETE FROM endpoint_key_secrets WHERE id=?`, secretID)
	hostileMustExec(t, db, `DELETE FROM donation_key_memberships WHERE endpoint_key_id=?`, keyID)
	hostileMustExec(t, db, `DELETE FROM endpoint_keys WHERE id=?`, keyID)
	hostileMustExec(t, db, `UPDATE endpoint_key_secrets SET orphaned_at=1 WHERE id=?`, secretID)
	hostileMustFail(t, db, `UPDATE endpoint_key_secrets SET orphaned_at=NULL WHERE id=?`, secretID)
	hostileMustExec(t, db, `DELETE FROM endpoint_key_secrets WHERE id=?`, secretID)

	caseID := hostileOIDVariant("rpc_", 'R', 'Q')
	fingerprint := hostileBlob32(9)
	hostileInsertReportCase(t, db, caseID, fingerprint, "pending_review", "complete", 0)
	// The six case counters and retry_attempt_count are persisted INTEGER
	// values with no implementation-sized cap.  Keep the relational counters
	// consistent while exercising the full signed SQLite range.
	hostileMustExec(t, db, `
UPDATE report_cases SET material_count=?,target_count=?,distinct_owner_count=?,
 processed_target_count=?,deleted_target_count=?,released_target_count=?,retry_attempt_count=?
WHERE id=?`, hostileInt64Max, hostileInt64Max, hostileInt64Max, hostileInt64Max,
		hostileInt64Max, hostileInt64Max, hostileInt64Max, caseID)
	for _, column := range []string{
		"material_count", "target_count", "distinct_owner_count",
		"processed_target_count", "deleted_target_count", "released_target_count",
		"retry_attempt_count",
	} {
		column := column
		t.Run("report_"+column+"_negative", func(t *testing.T) {
			hostileMustFail(t, db, fmt.Sprintf("UPDATE report_cases SET %s=? WHERE id=?", column), int64(-1), caseID)
		})
		t.Run("report_"+column+"_real", func(t *testing.T) {
			hostileMustFail(t, db, fmt.Sprintf("UPDATE report_cases SET %s=? WHERE id=?", column), 1.5, caseID)
		})
		t.Run("report_"+column+"_overflow", func(t *testing.T) {
			hostileMustFail(t, db, fmt.Sprintf("UPDATE report_cases SET %s=? WHERE id=?", column), "9223372036854775808", caseID)
		})
	}
	hostileMustFail(t, db, `
INSERT INTO report_cases(
 id,fingerprint,connector_type,canonical_base_url,status,progress_state,
 material_version,target_version,deadline,material_count,target_count,distinct_owner_count,created_at
) VALUES(?,?,'openai-compatible','https://upstream.example/v1','pending_review','complete',1,1,1000,0,0,0,0)`, hostileOIDVariant("rpc_", 'S', 'Q'), fingerprint)
	hostileMustFail(t, db, `
INSERT INTO report_cases(
 id,fingerprint,connector_type,canonical_base_url,status,progress_state,
 material_version,target_version,deadline,material_count,target_count,distinct_owner_count,created_at
) VALUES(?,?,'openai-compatible','https://upstream.example/v1','pending_review','in_progress',1,1,1000,0,0,0,0)`, hostileOIDVariant("rpc_", 'T', 'Q'), hostileBlob32(8))

	hostileInsertReportMaterial(t, db, caseID)
	hostileMustFail(t, db, `
INSERT INTO report_materials(case_id,material_hash,note_text,source_ip_envelope,created_at)
VALUES(?,?,?, ?,0)`, caseID, hostileBlob32(4), strings.Repeat("x", 2049), make([]byte, 45))
	hostileMustFail(t, db, `
INSERT INTO report_materials(case_id,material_hash,note_text,source_ip_envelope,created_at)
VALUES(?,?, '', ?,0)`, caseID, hostileBlob32(5), make([]byte, 44))
	targetID := hostileOIDVariant("rpt_", 'R', 'Q')
	hostileInsertReportTarget(t, db, caseID, targetID)
	// target_seq is an exact non-negative INTEGER, not a page-size cap.  The
	// signed SQLite maximum is valid; negative, REAL, and overflow values must
	// be rejected by the hostile boundary guards.
	hostileMustExec(t, db, `UPDATE report_targets SET target_seq=? WHERE id=?`, hostileInt64Max, targetID)
	for _, tc := range []struct {
		name  string
		value any
	}{
		{name: "negative", value: int64(-1)},
		{name: "real", value: 1.5},
		{name: "overflow", value: "9223372036854775808"},
	} {
		t.Run("target_seq_"+tc.name, func(t *testing.T) {
			hostileMustFail(t, db, `UPDATE report_targets SET target_seq=? WHERE id=?`, tc.value, targetID)
		})
	}
	hostileMustFail(t, db, `
INSERT INTO report_targets(
 id,case_id,target_seq,key_ref,connector_type,canonical_base_url,state,discovered_version,created_at,updated_at,owner_display_name
) VALUES(?,?,1,?,'openai-compatible','https://upstream.example/v1','protected',1,0,0,?)`, hostileOIDVariant("rpt_", 'S', 'Q'), caseID, hostileBlob32(6), strings.Repeat("x", 129))

	// Donation usage keeps all three limit dimensions distinct.  Wrong-width
	// U128 values and an out-of-range token reserve are both rejected.
	secondDonation := hostileInsertDonation(t, db, uid)
	secondKey := hostileInsertDonationKey(t, db, secondDonation, nil)
	hostileMustExec(t, db, `
UPDATE donation_keys SET price_used_mag=?,price_reserved_mag=?,calls_used=?,calls_reserved=?,
 tokens_used=?,tokens_reserved=?,failure_streak=?,streak_generation=?,next_claim_seq=?,next_fold_seq=?,token_reserve=?
 WHERE id=?`, max, max, max, max, max, max, max, twoTo63, twoTo63, twoTo63, int64(2147483647), secondKey)
	hostileMustExec(t, db, `UPDATE donation_keys SET streak_generation=?,next_claim_seq=?,next_fold_seq=? WHERE id=?`, high, max, twoTo63, secondKey)
	hostileMustFail(t, db, `UPDATE donation_keys SET price_used_mag=? WHERE id=?`, []byte{1}, secondKey)
	hostileMustFail(t, db, `UPDATE donation_keys SET token_reserve=? WHERE id=?`, int64(2147483648), secondKey)
	hostileMustFail(t, db, `UPDATE donation_keys SET connector_type='unknown' WHERE id=?`, secondKey)
}

func TestGenerationTwoHostileReportDecisionTerminalMatrix(t *testing.T) {
	db := openGenerationTwoDDLForTest(t)

	insert := func(t *testing.T, id string, fingerprint []byte, status, progress string, decisionAt, terminalAt any) {
		t.Helper()
		hostileMustExec(t, db, `
INSERT INTO report_cases(
 id,fingerprint,connector_type,canonical_base_url,status,progress_state,
 material_version,target_version,deadline,cursor_text,material_count,target_count,
 distinct_owner_count,processed_target_count,deleted_target_count,released_target_count,
 decision_reason,decision_actor_user_id,decision_at,retry_attempt_count,next_retry_at,
 last_error_class,created_at,terminal_at
) VALUES(?,?,'openai-compatible','https://upstream.example/v1',?,?,1,1,1000,NULL,
 0,0,0,0,0,0,NULL,NULL,?,0,NULL,NULL,0,?)`,
			id, fingerprint, status, progress, decisionAt, terminalAt)
	}

	// The explicit status/progress and decision-time combinations form the
	// report state matrix.  Nonterminal statuses carry neither terminal nor
	// decision time; approved/rejected carry both times; expiry is terminal
	// without a human decision timestamp.
	valid := []struct {
		name                   string
		status, progress       string
		decisionAt, terminalAt any
	}{
		{name: "pending_indexing", status: "pending_indexing", progress: "in_progress"},
		{name: "pending_review", status: "pending_review", progress: "complete"},
		{name: "approved_processing", status: "approved_processing", progress: "in_progress"},
		{name: "approved", status: "approved", progress: "complete", decisionAt: int64(1), terminalAt: int64(2)},
		{name: "rejected", status: "rejected", progress: "complete", decisionAt: int64(1), terminalAt: int64(2)},
		{name: "expired", status: "expired", progress: "complete", terminalAt: int64(2)},
	}
	for i, tc := range valid {
		tc := tc
		t.Run("valid_"+tc.name, func(t *testing.T) {
			insert(t, hostileOIDVariant("rpc_", byte('A'+i), 'Q'), hostileBlob32(byte(i+130)), tc.status, tc.progress, tc.decisionAt, tc.terminalAt)
		})
	}

	invalid := []struct {
		name                   string
		status, progress       string
		decisionAt, terminalAt any
	}{
		{name: "pending_review_decision_without_terminal", status: "pending_review", progress: "complete", decisionAt: int64(1)},
		{name: "pending_review_terminal", status: "pending_review", progress: "complete", terminalAt: int64(2)},
		{name: "approved_without_terminal", status: "approved", progress: "complete", decisionAt: int64(1)},
		{name: "approved_processing_terminal", status: "approved_processing", progress: "in_progress", decisionAt: int64(1), terminalAt: int64(2)},
		{name: "expired_without_terminal", status: "expired", progress: "complete"},
	}
	for i, tc := range invalid {
		tc := tc
		t.Run("invalid_"+tc.name, func(t *testing.T) {
			t.Helper()
			query := `
INSERT INTO report_cases(
 id,fingerprint,connector_type,canonical_base_url,status,progress_state,
 material_version,target_version,deadline,cursor_text,material_count,target_count,
 distinct_owner_count,processed_target_count,deleted_target_count,released_target_count,
 decision_reason,decision_actor_user_id,decision_at,retry_attempt_count,next_retry_at,
 last_error_class,created_at,terminal_at
) VALUES(?,?,'openai-compatible','https://upstream.example/v1',?,?,1,1,1000,NULL,
 0,0,0,0,0,0,NULL,NULL,?,0,NULL,NULL,0,?)`
			hostileMustFail(t, db, query, hostileOIDVariant("rpc_", byte('a'+i), 'Q'), hostileBlob32(byte(i+140)), tc.status, tc.progress, tc.decisionAt, tc.terminalAt)
		})
	}
}

func TestGenerationTwoHostileThursdaySharedPoolAndWelfareInvariants(t *testing.T) {
	db := openGenerationTwoDDLForTest(t)
	uid := hostileInsertUser(t, db, "thursday", 0, 0)
	periodID, currentPoolID, nextPoolID := hostileInsertThursdayFixture(t, db)

	// Shared pool identity is coupled to its kind, OID and account code.
	wrongAccount := hostileInsertAccount(t, db, "platform", nil, "platform", 0, hostileBlob16(0), 0)
	hostileMustFail(t, db, `
INSERT INTO shared_pools(id,pool_type,period_id,account_id,state,revision,created_at)
VALUES(?, 'thursday', ?, ?, 'open',1,0)`, hostileOIDVariant("pol_", 'W', 'Q'), periodID, wrongAccount)
	hostileMustFail(t, db, `
INSERT INTO shared_pools(id,pool_type,period_id,account_id,state,revision,created_at)
VALUES(?, 'invalid', NULL, ?, 'open',1,0)`, hostileOIDVariant("pol_", 'I', 'Q'), wrongAccount)
	hostileMustFail(t, db, `
INSERT INTO shared_pools(id,pool_type,period_id,account_id,state,revision,created_at)
VALUES(?, 'thursday', ?, ?, 'open',1,0)`, hostileBadOID("pol_"), periodID, wrongAccount)

	// Current period scalar/matrix checks include Beijing-weekday identity,
	// exact 24-hour close, bp sum, capacity row and pool membership.
	hostileMustFail(t, db, `UPDATE thursday_periods SET platform_bp=10000 WHERE id=?`, periodID)
	hostileMustFail(t, db, `UPDATE thursday_periods SET platform_bp=9999,welfare_bp=1,next_pool_bp=0 WHERE id=?`, periodID)
	hostileMustFail(t, db, `UPDATE thursday_periods SET per_user_limit=0 WHERE id=?`, periodID)
	hostileMustFail(t, db, `UPDATE thursday_periods SET closes_at=0 WHERE id=?`, periodID)
	hostileMustFail(t, db, `UPDATE thursday_periods SET ledger_rows_remaining=? WHERE id=?`, hostileBlob16(0), periodID)
	hostileMustFail(t, db, `UPDATE thursday_periods SET current_pool_id=? WHERE id=?`, hostileOIDVariant("pol_", 'X', 'Q'), periodID)
	hostileMustExec(t, db, `UPDATE thursday_periods SET revision=?,created_at=? WHERE id=?`, hostileInt64Max, hostileTimeMax, periodID)
	hostileMustFail(t, db, `UPDATE thursday_periods SET revision=0 WHERE id=?`, periodID)
	hostileMustFail(t, db, `UPDATE thursday_periods SET revision=? WHERE id=?`, "9223372036854775808", periodID)
	hostileMustFail(t, db, `UPDATE thursday_periods SET created_at=? WHERE id=?`, hostileTimeMax+1, periodID)
	hostileMustExec(t, db, `UPDATE shared_pools SET revision=?,created_at=? WHERE id=?`, hostileInt64Max, hostileTimeMax, currentPoolID)
	hostileMustFail(t, db, `UPDATE shared_pools SET revision=0 WHERE id=?`, currentPoolID)
	hostileMustFail(t, db, `UPDATE shared_pools SET created_at=? WHERE id=?`, hostileTimeMax+1, currentPoolID)

	// Participant lifecycle matrix: an open row reserves one terminal ledger
	// operation and cannot claim a payout/unpaid reason before settlement.
	participantID := hostileOIDVariant("thp_", 'P', 'Q')
	hostileMustExec(t, db, `
INSERT INTO thursday_participants(
 period_id,participant_ref,user_id,contribution_count,contributed_mag,eligible_at_freeze,
 payout_mag,settled,ledger_rows_remaining,created_at,updated_at
) VALUES(?,?,?, ?,?,?, ?,0,?,0,0)`, periodID, participantID, uid, hostileBlob16(1), hostileBlob16(1), 1, hostileBlob16(0), hostileBlob16(1))
	hostileMustFail(t, db, `UPDATE thursday_participants SET ledger_rows_remaining=? WHERE period_id=? AND participant_ref=?`, hostileBlob16(0), periodID, participantID)
	hostileMustFail(t, db, `UPDATE thursday_participants SET unpaid_reason='account_banned' WHERE period_id=? AND participant_ref=?`, periodID, participantID)
	hostileMustFail(t, db, `UPDATE thursday_participants SET updated_at=-1 WHERE period_id=? AND participant_ref=?`, periodID, participantID)
	// A deleted account is a settled, deidentified participant: its user FK is
	// NULL, payout is zero, and the reserved terminal row is consumed.
	hostileMustExec(t, db, `
INSERT INTO thursday_participants(
 period_id,participant_ref,user_id,contribution_count,contributed_mag,eligible_at_freeze,
 payout_mag,unpaid_reason,settled,ledger_rows_remaining,created_at,updated_at
) VALUES(?,?,NULL,?,?,?,?, 'account_deleted',1,?,0,0)`, periodID, hostileOIDVariant("thp_", 'D', 'Q'), hostileMaxU128(), hostileBlob16(1), 0, hostileBlob16(0), hostileBlob16(0))
	// A banned account remains associated for the settled fact, but is marked
	// ineligible and also receives no payout.
	hostileMustExec(t, db, `
INSERT INTO thursday_participants(
 period_id,participant_ref,user_id,contribution_count,contributed_mag,eligible_at_freeze,
 payout_mag,unpaid_reason,settled,ledger_rows_remaining,created_at,updated_at
) VALUES(?,?,?, ?,?,?, ?, 'account_banned',1,?,0,0)`, periodID, hostileOIDVariant("thp_", 'B', 'Q'), uid, hostileBlob16(1), hostileBlob16(1), 0, hostileBlob16(0), hostileBlob16(0))
	hostileMustFail(t, db, `
INSERT INTO thursday_participants(
 period_id,participant_ref,user_id,contribution_count,contributed_mag,eligible_at_freeze,
 payout_mag,unpaid_reason,settled,ledger_rows_remaining,created_at,updated_at
) VALUES(?,?,?, ?,?,?, ?, 'account_deleted',1,?,0,0)`, periodID, hostileOIDVariant("thp_", 'E', 'Q'), uid, hostileBlob16(1), hostileBlob16(1), 0, hostileBlob16(0), hostileBlob16(0))
	// Deidentification after a banned settlement preserves the ineligible
	// classification instead of rewriting it as an account deletion.
	hostileMustExec(t, db, `
INSERT INTO thursday_participants(
 period_id,participant_ref,user_id,contribution_count,contributed_mag,eligible_at_freeze,
 payout_mag,unpaid_reason,settled,ledger_rows_remaining,created_at,updated_at
) VALUES(?,?,NULL,?,?,?,?, 'account_banned',1,?,0,0)`, periodID, hostileOIDVariant("thp_", 'C', 'Q'), hostileBlob16(1), hostileBlob16(1), 0, hostileBlob16(0), hostileBlob16(0))
	hostileMustFail(t, db, `
INSERT INTO thursday_participants(
 period_id,participant_ref,user_id,contribution_count,contributed_mag,eligible_at_freeze,
 payout_mag,unpaid_reason,settled,ledger_rows_remaining,created_at,updated_at
) VALUES(?,?,?, ?,?,?, ?, 'account_banned',0,?,0,0)`, periodID, hostileOIDVariant("thp_", 'U', 'Q'), uid, hostileBlob16(1), hostileBlob16(1), 0, hostileBlob16(0), hostileBlob16(1))
	hostileMustFail(t, db, `
INSERT INTO thursday_participants(
 period_id,participant_ref,user_id,contribution_count,contributed_mag,eligible_at_freeze,
 payout_mag,unpaid_reason,settled,ledger_rows_remaining,created_at,updated_at
) VALUES(?,?,?, ?,?,?, ?, 'account_banned',1,?,0,0)`, periodID, hostileOIDVariant("thp_", 'V', 'Q'), uid, hostileBlob16(1), hostileBlob16(1), 0, hostileBlob16(1), hostileBlob16(0))
	// A pure U128 participant count accepts 2^128-1; the SM128-like fields
	// remain independent and canonical.
	hostileMustExec(t, db, `UPDATE thursday_participants SET contribution_count=? WHERE period_id=? AND participant_ref=?`, hostileMaxU128(), periodID, participantID)

	// Welfare claims must be backed by the exact operation/source mapping and
	// obey the cap and pool-before bounds.
	operationID := hostileOIDVariant("op_", 'W', 'Q')
	hostileInsertOperation(t, db, operationID, 1, "welfare_claim", "operation", operationID)
	hostileMustExec(t, db, `
INSERT INTO welfare_claims(user_id,site_day,award_milli,operation_id,threshold_milli,cap_milli,pool_before_milli,created_at)
VALUES(?,'1970-01-01',1,?,0,1,1,0)`, uid, operationID)
	hostileMustExec(t, db, `UPDATE welfare_claims SET created_at=? WHERE operation_id=?`, hostileTimeMax, operationID)
	hostileMustFail(t, db, `UPDATE welfare_claims SET created_at=? WHERE operation_id=?`, hostileTimeMax+1, operationID)
	hostileMustFail(t, db, `
INSERT INTO welfare_claims(user_id,site_day,award_milli,operation_id,threshold_milli,cap_milli,pool_before_milli,created_at)
VALUES(?,'1970-01-01',1,?,0,1,1,0)`, uid, hostileOIDVariant("op_", 'N', 'Q'))
	hostileMustFail(t, db, `
INSERT INTO welfare_claims(user_id,site_day,award_milli,operation_id,threshold_milli,cap_milli,pool_before_milli,created_at)
VALUES(?,'1970-01-02',2,?,0,1,1,0)`, uid, operationID)
	hostileMustFail(t, db, `
INSERT INTO welfare_claims(user_id,site_day,award_milli,operation_id,threshold_milli,cap_milli,pool_before_milli,created_at)
VALUES(?,'1970-01-03',1,?,0,0,1,0)`, uid, hostileOIDVariant("op_", 'Z', 'Q'))

	// Keep both pool IDs live so an accidental delete/replace cannot silently
	// satisfy the period's current/next references.
	var poolCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM shared_pools WHERE id IN (?,?)`, currentPoolID, nextPoolID).Scan(&poolCount); err != nil {
		t.Fatal(err)
	}
	if poolCount != 2 {
		t.Fatalf("expected both Thursday pools, got %d", poolCount)
	}
}

func TestGenerationTwoHostileThursdayDeidentifiedSettlementMatrix(t *testing.T) {
	tests := []struct {
		name      string
		eligible  int
		payout    []byte
		reason    any
		settled   int
		remaining []byte
		allowed   bool
	}{
		{
			name:      "account_deleted_ineligible_zero_payout",
			eligible:  0,
			payout:    hostileBlob16(0),
			reason:    "account_deleted",
			settled:   1,
			remaining: hostileBlob16(0),
			allowed:   true,
		},
		{
			name:      "account_deleted_eligible_zero_payout",
			eligible:  1,
			payout:    hostileBlob16(0),
			reason:    "account_deleted",
			settled:   1,
			remaining: hostileBlob16(0),
			allowed:   true,
		},
		{
			name:      "account_banned_ineligible_zero_payout",
			eligible:  0,
			payout:    hostileBlob16(0),
			reason:    "account_banned",
			settled:   1,
			remaining: hostileBlob16(0),
			allowed:   true,
		},
		{
			name:      "eligible_zero_payout",
			eligible:  1,
			payout:    hostileBlob16(0),
			reason:    nil,
			settled:   1,
			remaining: hostileBlob16(0),
			allowed:   true,
		},
		{
			name:      "eligible_positive_payout",
			eligible:  1,
			payout:    hostileBlob16(1),
			reason:    nil,
			settled:   1,
			remaining: hostileBlob16(0),
			allowed:   true,
		},
		{
			name:      "eligible_maximum_payout",
			eligible:  1,
			payout:    hostileMaxU128(),
			reason:    nil,
			settled:   1,
			remaining: hostileBlob16(0),
			allowed:   true,
		},
		{
			name:      "account_banned_eligible",
			eligible:  1,
			payout:    hostileBlob16(0),
			reason:    "account_banned",
			settled:   1,
			remaining: hostileBlob16(0),
		},
		{
			name:      "null_reason_ineligible",
			eligible:  0,
			payout:    hostileBlob16(0),
			reason:    nil,
			settled:   1,
			remaining: hostileBlob16(0),
		},
		{
			name:      "account_deleted_ineligible_positive_payout",
			eligible:  0,
			payout:    hostileBlob16(1),
			reason:    "account_deleted",
			settled:   1,
			remaining: hostileBlob16(0),
		},
		{
			name:      "account_deleted_eligible_positive_payout",
			eligible:  1,
			payout:    hostileBlob16(1),
			reason:    "account_deleted",
			settled:   1,
			remaining: hostileBlob16(0),
		},
		{
			name:      "account_banned_ineligible_positive_payout",
			eligible:  0,
			payout:    hostileBlob16(1),
			reason:    "account_banned",
			settled:   1,
			remaining: hostileBlob16(0),
		},
		{
			name:      "account_banned_eligible_positive_payout",
			eligible:  1,
			payout:    hostileBlob16(1),
			reason:    "account_banned",
			settled:   1,
			remaining: hostileBlob16(0),
		},
		{
			name:      "unsettled_null_user",
			eligible:  1,
			payout:    hostileBlob16(0),
			reason:    nil,
			settled:   0,
			remaining: hostileBlob16(1),
		},
		{
			name:      "settled_remaining_nonzero",
			eligible:  1,
			payout:    hostileBlob16(0),
			reason:    nil,
			settled:   1,
			remaining: hostileBlob16(1),
		},
	}

	const insertParticipant = `
INSERT INTO thursday_participants(
 period_id,participant_ref,user_id,contribution_count,contributed_mag,eligible_at_freeze,
 payout_mag,unpaid_reason,settled,ledger_rows_remaining,created_at,updated_at
) VALUES(?,?,NULL,?,?,?,?,?,?,?,0,0)`
	const updateParticipant = `
UPDATE thursday_participants
SET user_id=NULL,eligible_at_freeze=?,payout_mag=?,unpaid_reason=?,settled=?,
    ledger_rows_remaining=?,updated_at=1
WHERE period_id=? AND participant_ref=?`

	for _, tt := range tests {
		tt := tt
		t.Run("insert/"+tt.name, func(t *testing.T) {
			db := openGenerationTwoDDLForTest(t)
			periodID, _, _ := hostileInsertThursdayFixture(t, db)
			participantID := hostileOIDVariant("thp_", 'M', 'Q')
			args := []any{
				periodID, participantID, hostileBlob16(1), hostileBlob16(1), tt.eligible,
				tt.payout, tt.reason, tt.settled, tt.remaining,
			}
			if tt.allowed {
				hostileMustExec(t, db, insertParticipant, args...)
				return
			}
			hostileMustFail(t, db, insertParticipant, args...)
		})

		t.Run("update/"+tt.name, func(t *testing.T) {
			db := openGenerationTwoDDLForTest(t)
			uid := hostileInsertUser(t, db, "thursday-matrix", 0, 0)
			periodID, _, _ := hostileInsertThursdayFixture(t, db)
			participantID := hostileOIDVariant("thp_", 'M', 'Q')
			hostileMustExec(t, db, `
INSERT INTO thursday_participants(
 period_id,participant_ref,user_id,contribution_count,contributed_mag,eligible_at_freeze,
 payout_mag,settled,ledger_rows_remaining,created_at,updated_at
) VALUES(?,?,?, ?,?,?, ?,0,?,0,0)`, periodID, participantID, uid, hostileBlob16(1), hostileBlob16(1), 1, hostileBlob16(0), hostileBlob16(1))
			args := []any{
				tt.eligible, tt.payout, tt.reason, tt.settled, tt.remaining,
				periodID, participantID,
			}
			if tt.allowed {
				hostileMustExec(t, db, updateParticipant, args...)
				return
			}
			hostileMustFail(t, db, updateParticipant, args...)
		})
	}
}

func TestGenerationTwoHostileRPSPhaseSeatEnvelope(t *testing.T) {
	db := openGenerationTwoDDLForTest(t)
	uid := hostileInsertUser(t, db, "rps", 0, 0)
	zero := hostileBlob16(0)
	one := hostileBlob16(1)

	validPhases := []string{
		"gesture",
		"dealer_raise",
		"followers",
		"paid_pool_gesture",
		"free_pool_gesture",
		"ultimate_gesture",
	}
	phaseIDs := make(map[string]string, len(validPhases))
	for i, phase := range validPhases {
		id := hostileOIDVariant("rps_", byte('a'+i), 'Q')
		phaseIDs[phase] = id
		accountID := hostileInsertRPSAccount(t, db, id)
		hostileInsertRPSSession(t, db, id, phase, "started", accountID)
	}
	terminalID := hostileOIDVariant("rps_", 't', 'Q')
	terminalAccount := hostileInsertRPSAccount(t, db, terminalID)
	hostileInsertRPSSession(t, db, terminalID, "terminal_processing", "terminal_processing", terminalAccount)

	// Terminal-processing and the retained summary share the same closed
	// reason set.  Every one of the six frozen reasons is a legal terminal
	// fact, while summary deletion is exactly terminal+30d and cannot drift.
	terminalReasons := []string{
		"quick_resolved",
		"standard_round_limit",
		"standard_insufficient_balance",
		"deathmatch_balance_exhausted",
		"ultimate_resolved",
		"free_tie_limit",
	}
	for i, reason := range terminalReasons {
		id := hostileOIDVariant("rps_", byte('u'+i), 'Q')
		accountID := hostileInsertRPSAccount(t, db, id)
		hostileInsertRPSSessionWithReason(t, db, id, "terminal_processing", "terminal_processing", accountID, reason)
		hostileMustExec(t, db, `
INSERT INTO game_rps_summaries(
 session_id,mode,rules_version,base_milli,platform_bp,welfare_bp,thursday_bp,
 started_at,terminal_at,terminal_reason,base_round_count,paid_tie_count,free_tie_count,
 total_timeout_count,total_rock_count,total_scissors_count,total_paper_count,
 platform_total,welfare_total,thursday_total,delete_at
) VALUES(?, 'quick',1,5,0,0,0,100,200,?,?,?,?,?,?,?,?,?,?,?,2592200)`,
			id, reason, zero, zero, zero, zero, zero, zero, zero, zero, zero, zero)
	}
	hostileMustFail(t, db, `UPDATE game_rps_sessions SET terminal_reason='not_closed' WHERE id=?`, terminalID)
	invalidSummaryID := hostileOIDVariant("rps_", '0', 'Q')
	hostileMustFail(t, db, `
INSERT INTO game_rps_summaries(
 session_id,mode,rules_version,base_milli,platform_bp,welfare_bp,thursday_bp,
 started_at,terminal_at,terminal_reason,base_round_count,paid_tie_count,free_tie_count,
 total_timeout_count,total_rock_count,total_scissors_count,total_paper_count,
 platform_total,welfare_total,thursday_total,delete_at
) VALUES(?, 'quick',1,5,0,0,0,100,200,'quick_resolved',?,?,?,?,?,?,?,?,?,?,?,2592199)`,
		invalidSummaryID, zero, zero, zero, zero, zero, zero, zero, zero, zero, zero)
	hostileMustFail(t, db, `UPDATE game_rps_summaries SET delete_at=2592199 WHERE session_id=?`, hostileOIDVariant("rps_", 'u', 'Q'))

	for _, phase := range validPhases {
		id := phaseIDs[phase]
		if phase == "free_pool_gesture" {
			hostileMustExec(t, db, `UPDATE game_rps_sessions SET free_pool_streak=?,reminder_state='active' WHERE id=?`, hostileBlob16(3), id)
			hostileMustFail(t, db, `UPDATE game_rps_sessions SET free_pool_streak=? WHERE id=?`, hostileBlob16(2), id)
			hostileMustFail(t, db, `UPDATE game_rps_sessions SET free_pool_streak=? WHERE id=?`, hostileBlob16(6), id)
		}
	}

	gestureID := phaseIDs["gesture"]
	hostileMustFail(t, db, `UPDATE game_rps_sessions SET rules_version=0 WHERE id=?`, gestureID)
	hostileMustFail(t, db, `UPDATE game_rps_sessions SET phase='reveal' WHERE id=?`, gestureID)
	hostileMustFail(t, db, `UPDATE game_rps_sessions SET current_plan_multiplier=NULL WHERE id=?`, gestureID)
	hostileMustFail(t, db, `UPDATE game_rps_sessions SET pool_base_multiplier=? WHERE id=?`, one, gestureID)
	hostileMustFail(t, db, `UPDATE game_rps_sessions SET reminder_state='active' WHERE id=?`, gestureID)

	// Each gesture phase accepts both a canonical pair on INSERT and the first
	// null-to-pair binding on UPDATE. The two phase sequences are the current
	// session sequence; a half pair or a different/zero sequence is rejected.
	gesturePhases := []string{"gesture", "paid_pool_gesture", "free_pool_gesture", "ultimate_gesture"}
	gestureEnvelopeLengths := []int{33, 34, 37, 33}
	for i, phase := range gesturePhases {
		id := phaseIDs[phase]
		directUser := uid
		if phase != "gesture" {
			directUser = hostileInsertUser(t, db, "rps-direct-gesture-"+phase, 0, 0)
		}
		hostileInsertRPSSeat(t, db, id, 0, directUser, hostileEnvelope(gestureEnvelopeLengths[i], 0x01), one, nil, nil, nil, "active")
		bindingUser := hostileInsertUser(t, db, "rps-bound-gesture-"+phase, 0, 0)
		hostileInsertRPSSeat(t, db, id, 1, bindingUser, nil, nil, nil, nil, nil, "active")
		hostileMustFail(t, db, `UPDATE game_rps_seats SET current_gesture_envelope=? WHERE session_id=? AND seat_no=1`, hostileEnvelope(33, 0x01), id)
		hostileMustFail(t, db, `UPDATE game_rps_seats SET current_gesture_envelope=?,current_gesture_phase_seq=?,last_action_phase_seq=? WHERE session_id=? AND seat_no=1`, hostileEnvelope(33, 0x01), "1", one, id)
		hostileMustFail(t, db, `UPDATE game_rps_seats SET current_gesture_envelope=?,current_gesture_phase_seq=?,last_action_phase_seq=? WHERE session_id=? AND seat_no=1`, hostileEnvelope(33, 0x01), hostileBlob16(0), hostileBlob16(0), id)
		hostileMustFail(t, db, `UPDATE game_rps_seats SET current_gesture_envelope=?,current_gesture_phase_seq=?,last_action_phase_seq=? WHERE session_id=? AND seat_no=1`, hostileEnvelope(33, 0x01), hostileBlob16(2), hostileBlob16(2), id)
		hostileMustExec(t, db, `UPDATE game_rps_seats SET current_gesture_envelope=?,current_gesture_phase_seq=?,last_action_phase_seq=? WHERE session_id=? AND seat_no=1`, hostileEnvelope(33, 0x01), one, one, id)
	}
	badPairUser := hostileInsertUser(t, db, "rps-half-pair", 0, 0)
	hostileMustFailRPSSeatWithActions(t, db, gestureID, 2, badPairUser, hostileEnvelope(33, 0x01), nil, nil, one, "active")
	hostileInsertRPSSeat(t, db, gestureID, 2, badPairUser, hostileEnvelope(37, 0x01), one, nil, nil, nil, "active")

	// A bound pair is immutable in place. It can only be consumed together,
	// after the session has advanced beyond the gesture's dedicated AAD phase.
	hostileMustExec(t, db, `UPDATE game_rps_seats SET current_gesture_envelope=? WHERE session_id=? AND seat_no=0`, hostileEnvelope(33, 0x01), gestureID)
	hostileMustFail(t, db, `UPDATE game_rps_seats SET current_gesture_envelope=? WHERE session_id=? AND seat_no=0`, hostileEnvelope(34, 0x01), gestureID)
	hostileMustFail(t, db, `UPDATE game_rps_seats SET current_gesture_envelope=? WHERE session_id=? AND seat_no=0`, hostileEnvelope(37, 0x01), gestureID)
	hostileMustFail(t, db, `UPDATE game_rps_seats SET current_gesture_envelope=? WHERE session_id=? AND seat_no=0`, hostileEnvelope(33, 0x00), gestureID)
	hostileMustFail(t, db, `UPDATE game_rps_seats SET current_gesture_envelope=? WHERE session_id=? AND seat_no=0`, make([]byte, 32), gestureID)
	hostileMustFail(t, db, `UPDATE game_rps_seats SET current_gesture_envelope=? WHERE session_id=? AND seat_no=0`, make([]byte, 38), gestureID)
	hostileMustFail(t, db, `UPDATE game_rps_seats SET current_gesture_envelope=NULL WHERE session_id=? AND seat_no=0`, gestureID)
	hostileMustFail(t, db, `UPDATE game_rps_seats SET current_gesture_envelope=NULL,current_gesture_phase_seq=NULL,last_action_phase_seq=NULL WHERE session_id=? AND seat_no=0`, gestureID)
	hostileMustFail(t, db, `UPDATE game_rps_seats SET last_action_phase_seq=NULL WHERE session_id=? AND seat_no=0`, gestureID)
	hostileMustFail(t, db, `UPDATE game_rps_seats SET current_gesture_phase_seq=? WHERE session_id=? AND seat_no=0`, hostileBlob16(2), gestureID)
	hostileMustFail(t, db, `UPDATE game_rps_seats SET last_action_phase_seq=? WHERE session_id=? AND seat_no=0`, hostileBlob16(0), gestureID)

	// dealer_raise and followers INSERTs carry the original non-zero gesture
	// phase, which must be strictly older than the current session phase.
	two := hostileBlob16(2)
	three := hostileBlob16(3)
	four := hostileBlob16(4)
	hostileMustExec(t, db, `UPDATE game_rps_sessions SET phase='dealer_raise',revision=?,phase_seq=? WHERE id=?`, two, two, gestureID)
	hostileMustExec(t, db, `UPDATE game_rps_seats SET last_action_phase_seq=NULL WHERE session_id=?`, gestureID)
	var crossedDealerCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM game_rps_seats WHERE session_id=? AND current_gesture_envelope IS NOT NULL AND current_gesture_phase_seq=? AND follower_action IS NULL AND last_action_phase_seq IS NULL`, gestureID, one).Scan(&crossedDealerCount); err != nil {
		t.Fatalf("count dealer-retained RPS pairs: %v", err)
	}
	if crossedDealerCount != 3 {
		t.Fatalf("dealer transition retained %d gesture pairs, want 3", crossedDealerCount)
	}
	hostileMustExec(t, db, `UPDATE game_rps_sessions SET phase='followers',revision=?,phase_seq=?,dealer_raise=? WHERE id=?`, three, three, one, gestureID)
	hostileMustExec(t, db, `UPDATE game_rps_seats SET follower_action='call',last_action_phase_seq=? WHERE session_id=? AND seat_no=1`, three, gestureID)
	hostileMustExec(t, db, `UPDATE game_rps_seats SET follower_action='surrender',last_action_phase_seq=? WHERE session_id=? AND seat_no=2`, three, gestureID)
	hostileMustExec(t, db, `UPDATE game_rps_sessions SET phase='gesture',revision=?,phase_seq=?,dealer_raise=NULL WHERE id=?`, four, four, gestureID)
	hostileMustExec(t, db, `UPDATE game_rps_seats SET current_gesture_envelope=NULL,current_gesture_phase_seq=NULL,follower_action=NULL,last_action_phase_seq=NULL WHERE session_id=?`, gestureID)

	dealerRaiseID := phaseIDs["dealer_raise"]
	hostileMustExec(t, db, `UPDATE game_rps_sessions SET revision=?,phase_seq=? WHERE id=?`, two, two, dealerRaiseID)
	dealerRaiseUser := hostileInsertUser(t, db, "rps-dealer-raise", 0, 0)
	hostileInsertRPSSeatWithActions(t, db, dealerRaiseID, 0, dealerRaiseUser, hostileEnvelope(33, 0x01), one, nil, nil, nil, nil, nil, "active")
	hostileMustFailRPSSeatWithActions(t, db, dealerRaiseID, 1, hostileInsertUser(t, db, "rps-dealer-current-phase", 0, 0), hostileEnvelope(33, 0x01), two, nil, nil, "active")
	hostileMustFailRPSSeatWithActions(t, db, dealerRaiseID, 1, hostileInsertUser(t, db, "rps-dealer-zero-phase", 0, 0), hostileEnvelope(33, 0x01), zero, nil, nil, "active")
	hostileMustFailRPSSeatWithActions(t, db, dealerRaiseID, 1, hostileInsertUser(t, db, "rps-dealer-last-action", 0, 0), hostileEnvelope(33, 0x01), one, nil, one, "active")
	hostileMustFail(t, db, `UPDATE game_rps_seats SET current_gesture_envelope=? WHERE session_id=? AND seat_no=0`, hostileEnvelope(34, 0x01), dealerRaiseID)

	followersID := phaseIDs["followers"]
	hostileMustExec(t, db, `UPDATE game_rps_sessions SET revision=?,phase_seq=? WHERE id=?`, three, three, followersID)
	followerDealer := hostileInsertUser(t, db, "rps-follower-dealer", 0, 0)
	followerOne := hostileInsertUser(t, db, "rps-follower-one", 0, 0)
	followerTwo := hostileInsertUser(t, db, "rps-follower-two", 0, 0)
	hostileMustFailRPSSeatWithActions(t, db, followersID, 0, followerDealer, hostileEnvelope(33, 0x01), one, "call", three, "active")
	hostileInsertRPSSeatWithActions(t, db, followersID, 0, followerDealer, hostileEnvelope(33, 0x01), one, nil, nil, nil, nil, nil, "active")
	hostileMustFailRPSSeatWithActions(t, db, followersID, 1, followerOne, hostileEnvelope(34, 0x01), three, nil, nil, "active")
	hostileMustFailRPSSeatWithActions(t, db, followersID, 1, followerOne, hostileEnvelope(34, 0x01), one, "call", nil, "active")
	hostileInsertRPSSeatWithActions(t, db, followersID, 1, followerOne, hostileEnvelope(34, 0x01), one, nil, nil, nil, nil, nil, "active")
	hostileInsertRPSSeatWithActions(t, db, followersID, 2, followerTwo, hostileEnvelope(37, 0x01), one, "surrender", three, nil, nil, nil, "active")
	hostileMustExec(t, db, `UPDATE game_rps_seats SET follower_action='call',last_action_phase_seq=? WHERE session_id=? AND seat_no=1`, three, followersID)
	hostileMustFail(t, db, `UPDATE game_rps_seats SET last_action_phase_seq=NULL WHERE session_id=? AND seat_no=1`, followersID)
	hostileMustFail(t, db, `UPDATE game_rps_seats SET follower_action='surrender',last_action_phase_seq=? WHERE session_id=? AND seat_no=1`, three, followersID)
	hostileMustFail(t, db, `UPDATE game_rps_seats SET current_gesture_envelope=NULL,current_gesture_phase_seq=NULL,follower_action=NULL,last_action_phase_seq=NULL WHERE session_id=? AND seat_no=2`, followersID)

	// De-identification retains both the gesture pair and an already submitted
	// follower action. It cannot use deletion as an early-consumption path.
	hostileMustExec(t, db, `UPDATE game_rps_seats SET user_id=NULL,deletion_state='deletion_pending' WHERE session_id=? AND seat_no=2`, followersID)
	var retainedEnvelope, retainedGesturePhase, retainedFollower, retainedLast any
	if err := db.QueryRow(`SELECT current_gesture_envelope,current_gesture_phase_seq,follower_action,last_action_phase_seq FROM game_rps_seats WHERE session_id=? AND seat_no=2`, followersID).Scan(&retainedEnvelope, &retainedGesturePhase, &retainedFollower, &retainedLast); err != nil {
		t.Fatalf("read retained deleted-seat actions: %v", err)
	}
	if retainedEnvelope == nil || retainedGesturePhase == nil || retainedFollower != "surrender" || retainedLast == nil {
		t.Fatal("de-identification did not retain submitted RPS recovery inputs")
	}

	// Once the session reaches the next stable gesture phase, the reducer may
	// clear the pair and all current actions together. Pair halves and follower
	// remnants remain invalid even after that phase transition.
	hostileMustExec(t, db, `UPDATE game_rps_sessions SET phase='gesture',revision=?,phase_seq=?,dealer_raise=NULL WHERE id=?`, four, four, followersID)
	hostileMustFail(t, db, `UPDATE game_rps_seats SET current_gesture_envelope=NULL WHERE session_id=? AND seat_no=2`, followersID)
	hostileMustFail(t, db, `UPDATE game_rps_seats SET current_gesture_envelope=NULL,current_gesture_phase_seq=NULL WHERE session_id=? AND seat_no=2`, followersID)
	hostileMustExec(t, db, `UPDATE game_rps_seats SET current_gesture_envelope=NULL,current_gesture_phase_seq=NULL,follower_action=NULL,last_action_phase_seq=NULL WHERE session_id=?`, followersID)
	var retainedCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM game_rps_seats WHERE session_id=? AND (current_gesture_envelope IS NOT NULL OR current_gesture_phase_seq IS NOT NULL OR follower_action IS NOT NULL OR last_action_phase_seq IS NOT NULL)`, followersID).Scan(&retainedCount); err != nil {
		t.Fatalf("count cleared RPS actions: %v", err)
	}
	if retainedCount != 0 {
		t.Fatalf("legal final clear left %d RPS action rows", retainedCount)
	}

	// terminal_processing likewise rejects retained recovery inputs on any
	// seat write. Clearing the complete pair after the session transition may
	// be combined with freezing the terminal return facts.
	terminalClearID := hostileOIDVariant("rps_", 'T', 'Q')
	terminalClearAccount := hostileInsertRPSAccount(t, db, terminalClearID)
	hostileInsertRPSSession(t, db, terminalClearID, "gesture", "started", terminalClearAccount)
	terminalClearUser := hostileInsertUser(t, db, "rps-terminal-clear", 0, 0)
	hostileInsertRPSSeat(t, db, terminalClearID, 0, terminalClearUser, hostileEnvelope(33, 0x01), one, nil, nil, nil, "active")
	hostileMustExec(t, db, `
UPDATE game_rps_sessions
SET state='terminal_processing',phase='terminal_processing',revision=?,phase_seq=?,
    pool_base_multiplier=NULL,current_plan_multiplier=NULL,dealer_raise=NULL,phase_deadline=NULL,
    terminal_operation_id=?,terminal_reason='quick_resolved'
WHERE id=?`, two, two, hostileOIDVariant("op_", 'T', 'Q'), terminalClearID)
	hostileMustFail(t, db, `UPDATE game_rps_seats SET terminal_return=?,wallet_net_sign=1,wallet_net_mag=? WHERE session_id=? AND seat_no=0`, one, one, terminalClearID)
	hostileMustExec(t, db, `
UPDATE game_rps_seats
SET current_gesture_envelope=NULL,current_gesture_phase_seq=NULL,follower_action=NULL,last_action_phase_seq=NULL,
    terminal_return=?,wallet_net_sign=1,wallet_net_mag=?
WHERE session_id=? AND seat_no=0`, one, one, terminalClearID)
	var terminalEnvelope, terminalGesturePhase any
	if err := db.QueryRow(`SELECT current_gesture_envelope,current_gesture_phase_seq FROM game_rps_seats WHERE session_id=? AND seat_no=0`, terminalClearID).Scan(&terminalEnvelope, &terminalGesturePhase); err != nil {
		t.Fatalf("read terminal-cleared gesture pair: %v", err)
	}
	if terminalEnvelope != nil || terminalGesturePhase != nil {
		t.Fatal("terminal transition retained an RPS gesture pair")
	}
	hostileMustFailRPSSeatWithActions(t, db, terminalID, 1, nil, hostileEnvelope(33, 0x01), one, nil, nil, "deletion_pending")

	// Terminal return and wallet net are an all-or-nothing pair and can only
	// be frozen while the session is in terminal_processing.
	hostileMustFail(t, db, `UPDATE game_rps_seats SET terminal_return=? WHERE session_id=? AND seat_no=0`, one, gestureID)
	hostileInsertRPSSeat(t, db, terminalID, 0, nil, nil, nil, one, 1, one, "deletion_pending")
	hostileMustFail(t, db, `UPDATE game_rps_seats SET wallet_net_sign=0 WHERE session_id=? AND seat_no=0`, terminalID)
	hostileMustFail(t, db, `UPDATE game_rps_seats SET wallet_net_mag=? WHERE session_id=? AND seat_no=0`, hostileHigh128(), terminalID)
	hostileMustFail(t, db, `UPDATE game_rps_seats SET follower_action='call',last_action_phase_seq=? WHERE session_id=? AND seat_no=0`, one, terminalID)
	hostileMustFail(t, db, `UPDATE game_rps_seats SET terminal_return=?,wallet_net_sign=1,wallet_net_mag=? WHERE session_id=? AND seat_no=0`, hostileBlob16(0), one, terminalID)

	// A started session cannot be silently changed into a terminal row without
	// terminal operation/reason metadata, and its phase deadline remains in the
	// UTC range.
	hostileMustFail(t, db, `UPDATE game_rps_sessions SET state='terminal_processing',phase='terminal_processing' WHERE id=?`, gestureID)
	hostileMustFail(t, db, `UPDATE game_rps_sessions SET phase_deadline=? WHERE id=?`, hostileTimeMax+1, gestureID)

	leaseID := hostileOID("gle_")
	hostileMustExec(t, db, `
INSERT INTO game_online_leases(session_id,user_id,lease_id,health_epoch,expires_at,last_renewed_at)
VALUES(?,?,?,0,?,0)`, gestureID, uid, leaseID, hostileTimeMax)
	hostileMustFail(t, db, `UPDATE game_online_leases SET expires_at=? WHERE session_id=? AND user_id=? AND lease_id=?`, hostileTimeMax+1, gestureID, uid, leaseID)
	hostileMustFail(t, db, `UPDATE game_online_leases SET expires_at=-1 WHERE session_id=? AND user_id=? AND lease_id=?`, gestureID, uid, leaseID)
}

func TestGenerationTwoHostileGameParentAndProjectionMatrices(t *testing.T) {
	db := openGenerationTwoDDLForTest(t)
	zero := hostileBlob16(0)
	one := hostileBlob16(1)

	// Fishing best keeps a denormalized safe snapshot.  Deleting the outcome
	// parent must clear both composite-FK columns together and leave that
	// snapshot intact; a writer cannot manually clear either half or rewrite
	// the linked snapshot while the outcome still exists.
	fishingUser := hostileInsertUser(t, db, "game-fishing-best", 0, 0)
	fishingBatchID := hostileOID("fb_")
	fishingOperationID := hostileOID("op_")
	hostileInsertFishingBatch(t, db, fishingBatchID, fishingUser, fishingOperationID)
	hostileMustExec(t, db, `
INSERT INTO game_fishing_outcomes(batch_id,ordinal,species_key,tier,size_cm,payout_milli)
VALUES(?,0,'whitebait','small',5,1)`, fishingBatchID)
	secondFishingUser := hostileInsertUser(t, db, "game-fishing-cross-batch", 0, 0)
	secondFishingBatchID := hostileOIDVariant("fb_", 'X', 'Q')
	hostileInsertFishingBatch(t, db, secondFishingBatchID, secondFishingUser, hostileOIDVariant("op_", 'X', 'Q'))
	// Outcome identity is bound to its original reserved batch.  A valid
	// ordinal in another batch must not turn an UPDATE into a cross-batch move.
	hostileMustFail(t, db, `UPDATE game_fishing_outcomes SET batch_id=? WHERE batch_id=? AND ordinal=0`, secondFishingBatchID, fishingBatchID)
	hostileMustExec(t, db, `
INSERT INTO game_fishing_best(user_id,batch_id,ordinal,species_key,tier,size_cm,caught_at,public_tie_key)
VALUES(?,?,0,'whitebait','small',5,0,?)`, fishingUser, fishingBatchID, hostileBlob32(1))
	hostileMustFail(t, db, `UPDATE game_fishing_best SET species_key='boot' WHERE user_id=?`, fishingUser)
	hostileMustFail(t, db, `UPDATE game_fishing_best SET batch_id=NULL WHERE user_id=?`, fishingUser)
	hostileMustFail(t, db, `UPDATE game_fishing_best SET ordinal=NULL WHERE user_id=?`, fishingUser)
	hostileMustFail(t, db, `UPDATE game_fishing_best SET batch_id=NULL,ordinal=NULL WHERE user_id=?`, fishingUser)
	hostileMustExec(t, db, `DELETE FROM game_fishing_outcomes WHERE batch_id=? AND ordinal=0`, fishingBatchID)
	var bestBatch, bestOrdinal, bestSpecies, bestTier, bestSize any
	if err := db.QueryRow(`SELECT batch_id,ordinal,species_key,tier,size_cm FROM game_fishing_best WHERE user_id=?`, fishingUser).Scan(&bestBatch, &bestOrdinal, &bestSpecies, &bestTier, &bestSize); err != nil {
		t.Fatal(err)
	}
	if bestBatch != nil || bestOrdinal != nil || bestSpecies != "whitebait" || bestTier != "small" || bestSize != int64(5) {
		t.Fatalf("fishing best parent delete did not preserve composite/null snapshot: batch=%v ordinal=%v species=%v tier=%v size=%v", bestBatch, bestOrdinal, bestSpecies, bestTier, bestSize)
	}
	// The composite link may be gone, but the denormalized snapshot remains a
	// closed, independently validated tier/species/size fact.  It must not be
	// possible to smuggle an illegal combination through the now-unlinked row.
	hostileMustFail(t, db, `UPDATE game_fishing_best SET species_key='boot' WHERE user_id=?`, fishingUser)
	hostileMustFail(t, db, `UPDATE game_fishing_best SET tier='junk' WHERE user_id=?`, fishingUser)
	hostileMustFail(t, db, `UPDATE game_fishing_best SET size_cm=0 WHERE user_id=?`, fishingUser)
	hostileMustExec(t, db, `
INSERT INTO game_fishing_outcomes(batch_id,ordinal,species_key,tier,size_cm,payout_milli)
VALUES(?,0,'whitebait','small',5,1)`, fishingBatchID)
	hostileMustExec(t, db, `UPDATE game_fishing_best SET batch_id=?,ordinal=0 WHERE user_id=?`, fishingBatchID, fishingUser)
	hostileMustExec(t, db, `UPDATE game_fishing_batches SET state='committed',ledger_rows_remaining=?,next_attempt_at=NULL,settled_at=1,revealed_at=1 WHERE id=?`, zero, fishingBatchID)
	hostileMustFail(t, db, `UPDATE game_fishing_best SET batch_id=NULL WHERE user_id=?`, fishingUser)
	hostileMustFail(t, db, `UPDATE game_fishing_best SET ordinal=NULL WHERE user_id=?`, fishingUser)

	// A fishing batch is born reserved.  Terminal rows are reachable only by
	// one atomic reserved -> committed/released transition after the outcome
	// facts have been materialized; direct terminal INSERTs and terminal fact
	// rewrites must not create or alter ledger obligations.
	committedUser := hostileInsertUser(t, db, "fishing-committed", 0, 0)
	committedBatchID := hostileOIDVariant("fb_", 'C', 'Q')
	committedOperationID := hostileOIDVariant("op_", 'C', 'Q')
	hostileMustFail(t, db, `
INSERT INTO game_fishing_batches(
 id,user_id,bait,count,unit_price_milli,entry_total_milli,payout_total_milli,
 operation_id,request_hash,state,ledger_rows_remaining,attempt_count,next_attempt_at,
 retry_exhausted,created_at,settled_at,revealed_at
) VALUES(?,?, 'worm',1,1,1,1,?,?, 'committed',?,0,NULL,0,0,1,1)`,
		committedBatchID, committedUser, committedOperationID, hostileBlob32(201), hostileBlob16(0))
	hostileInsertFishingBatch(t, db, committedBatchID, committedUser, committedOperationID)
	hostileMustExec(t, db, `
INSERT INTO game_fishing_outcomes(batch_id,ordinal,species_key,tier,size_cm,payout_milli)
VALUES(?,0,'whitebait','small',5,1)`, committedBatchID)
	hostileMustExec(t, db, `UPDATE game_fishing_batches SET state='committed',ledger_rows_remaining=?,next_attempt_at=NULL,settled_at=1,revealed_at=1 WHERE id=?`, hostileBlob16(0), committedBatchID)
	hostileMustFail(t, db, `UPDATE game_fishing_batches SET payout_total_milli=2 WHERE id=?`, committedBatchID)
	hostileMustFail(t, db, `UPDATE game_fishing_batches SET settled_at=2 WHERE id=?`, committedBatchID)
	hostileMustFail(t, db, `INSERT INTO game_fishing_outcomes(batch_id,ordinal,species_key,tier,size_cm,payout_milli) VALUES(?,0,'whitebait','small',5,1)`, committedBatchID)
	hostileMustFail(t, db, `UPDATE game_fishing_outcomes SET payout_milli=2 WHERE batch_id=? AND ordinal=0`, committedBatchID)
	hostileMustFail(t, db, `DELETE FROM game_fishing_outcomes WHERE batch_id=? AND ordinal=0`, committedBatchID)

	releasedUser := hostileInsertUser(t, db, "fishing-released", 0, 0)
	releasedBatchID := hostileOIDVariant("fb_", 'R', 'Q')
	releasedOperationID := hostileOIDVariant("op_", 'R', 'Q')
	hostileMustFail(t, db, `
INSERT INTO game_fishing_batches(
 id,user_id,bait,count,unit_price_milli,entry_total_milli,payout_total_milli,
 operation_id,request_hash,state,ledger_rows_remaining,attempt_count,next_attempt_at,
 retry_exhausted,created_at,settled_at
) VALUES(?,?, 'worm',1,1,1,0,?,?, 'released',?,0,NULL,0,0,1)`,
		releasedBatchID, releasedUser, releasedOperationID, hostileBlob32(202), hostileBlob16(0))
	hostileInsertFishingBatch(t, db, releasedBatchID, releasedUser, releasedOperationID)
	hostileMustExec(t, db, `UPDATE game_fishing_batches SET state='released',ledger_rows_remaining=?,next_attempt_at=NULL,settled_at=1 WHERE id=?`, zero, releasedBatchID)
	hostileMustFail(t, db, `UPDATE game_fishing_batches SET state='reserved' WHERE id=?`, releasedBatchID)
	hostileMustFail(t, db, `INSERT INTO game_fishing_outcomes(batch_id,ordinal,species_key,tier,size_cm,payout_milli) VALUES(?,0,'whitebait','small',5,1)`, releasedBatchID)

	// A live RPS queue reserves a positive amount and exactly one terminal
	// release/start row.  Both zero-headroom and zero-reserve rows are hostile.
	queueID, _ := hostileInsertRPSQueue(t, db, 'A', "quick", one, one)
	var queueReserved, queueRemaining []byte
	if err := db.QueryRow(`SELECT reserved,ledger_rows_remaining FROM game_rps_queue WHERE id=?`, queueID).Scan(&queueReserved, &queueRemaining); err != nil {
		t.Fatal(err)
	}
	if string(queueReserved) == string(zero) || string(queueRemaining) != string(one) {
		t.Fatalf("live queue headroom mismatch: reserved=%x remaining=%x", queueReserved, queueRemaining)
	}
	insertQueue := func(marker byte, mode string, reserved, remaining []byte) {
		t.Helper()
		id := hostileOIDVariant("rpsq_", marker, 'Q')
		accountID := hostileInsertAccount(t, db, "platform", nil, "rps-queue:"+id, 0, zero, 0)
		userID := hostileInsertUser(t, db, "queue-invalid-"+string(marker), 0, 0)
		hostileMustFail(t, db, `
INSERT INTO game_rps_queue(
 id,user_id,account_id,mode,revision,reservation_operation_id,reserved,
 ledger_rows_remaining,device_token_hash,source_ip_hash,deadline,created_at
) VALUES(?,?,? ,?, ?,? ,?, ?, ?, ?,0,0)`, id, userID, accountID, mode, one,
			hostileOIDVariant("op_", marker, 'Q'), reserved, remaining, hostileBlob32(marker), hostileBlob32(marker+1))
	}
	insertQueue('B', "quick", zero, one)
	insertQueue('C', "quick", one, zero)

	// Mode-specific base boundaries and the three-way cut sum are checked on
	// sessions, independently of the phase economy matrix.
	quickID := hostileOIDVariant("rps_", 'b', 'Q')
	quickAccount := hostileInsertRPSAccount(t, db, quickID)
	hostileInsertRPSSession(t, db, quickID, "gesture", "started", quickAccount)
	hostileMustExec(t, db, `UPDATE game_rps_sessions SET base_milli=? WHERE id=?`, int64(9000000000000000), quickID)
	hostileMustFail(t, db, `UPDATE game_rps_sessions SET base_milli=? WHERE id=?`, int64(9000000000000001), quickID)
	hostileMustFail(t, db, `UPDATE game_rps_sessions SET base_milli=0 WHERE id=?`, quickID)
	standardID := hostileOIDVariant("rps_", 'c', 'Q')
	standardAccount := hostileInsertRPSAccount(t, db, standardID)
	hostileInsertRPSSession(t, db, standardID, "gesture", "started", standardAccount)
	hostileMustExec(t, db, `UPDATE game_rps_sessions SET mode='standard' WHERE id=?`, standardID)
	hostileMustExec(t, db, `UPDATE game_rps_sessions SET base_milli=? WHERE id=?`, int64(1800000000000000), standardID)
	hostileMustFail(t, db, `UPDATE game_rps_sessions SET base_milli=? WHERE id=?`, int64(1800000000000001), standardID)
	deathmatchID := hostileOIDVariant("rps_", 'd', 'Q')
	deathmatchAccount := hostileInsertRPSAccount(t, db, deathmatchID)
	hostileInsertRPSSession(t, db, deathmatchID, "gesture", "started", deathmatchAccount)
	hostileMustExec(t, db, `UPDATE game_rps_sessions SET mode='deathmatch' WHERE id=?`, deathmatchID)
	hostileMustExec(t, db, `UPDATE game_rps_sessions SET base_milli=? WHERE id=?`, int64(9000000000000000), deathmatchID)
	hostileMustFail(t, db, `UPDATE game_rps_sessions SET base_milli=? WHERE id=?`, int64(9000000000000001), deathmatchID)
	hostileMustExec(t, db, `UPDATE game_rps_sessions SET platform_bp=9999,welfare_bp=0,thursday_bp=0 WHERE id=?`, quickID)
	hostileMustFail(t, db, `UPDATE game_rps_sessions SET platform_bp=9999,welfare_bp=1,thursday_bp=0 WHERE id=?`, quickID)
	hostileMustFail(t, db, `UPDATE game_rps_sessions SET platform_bp=5000,welfare_bp=4999,thursday_bp=1 WHERE id=?`, quickID)
	hostileMustFail(t, db, `UPDATE game_rps_sessions SET dealer_raise=? WHERE id=?`, one, quickID)

	// The phase/sequence action matrix is closed: gesture seats cannot carry a
	// follower action, followers cannot carry a gesture envelope, and an
	// action's sequence must equal the session phase sequence.
	gestureUser := hostileInsertUser(t, db, "game-gesture-seat", 0, 0)
	hostileInsertRPSSeat(t, db, quickID, 0, gestureUser, hostileEnvelope(33, 0x01), one, nil, nil, nil, "active")
	hostileMustFail(t, db, `UPDATE game_rps_seats SET follower_action='call',last_action_phase_seq=? WHERE session_id=? AND seat_no=0`, one, quickID)
	followerID := hostileOIDVariant("rps_", 'f', 'Q')
	followerAccount := hostileInsertRPSAccount(t, db, followerID)
	hostileInsertRPSSession(t, db, followerID, "followers", "started", followerAccount)
	two := hostileBlob16(2)
	hostileMustExec(t, db, `UPDATE game_rps_sessions SET revision=?,phase_seq=? WHERE id=?`, two, two, followerID)
	followerUser0 := hostileInsertUser(t, db, "game-follower-zero", 0, 0)
	followerUser1 := hostileInsertUser(t, db, "game-follower-one", 0, 0)
	hostileInsertRPSSeatWithActions(t, db, followerID, 0, followerUser0, hostileEnvelope(33, 0x01), one, nil, nil, nil, nil, nil, "active")
	hostileInsertRPSSeatWithActions(t, db, followerID, 1, followerUser1, hostileEnvelope(33, 0x01), one, nil, nil, nil, nil, nil, "active")
	hostileMustExec(t, db, `UPDATE game_rps_seats SET follower_action='surrender',last_action_phase_seq=? WHERE session_id=? AND seat_no=1`, two, followerID)
	hostileMustFail(t, db, `UPDATE game_rps_seats SET current_gesture_envelope=? WHERE session_id=? AND seat_no=1`, hostileEnvelope(34, 0x01), followerID)
	hostileMustFail(t, db, `UPDATE game_rps_seats SET last_action_phase_seq=? WHERE session_id=? AND seat_no=1`, hostileBlob16(3), followerID)

	identityID := hostileOIDVariant("rps_", 'i', 'Q')
	identityAccount := hostileInsertRPSAccount(t, db, identityID)
	hostileInsertRPSSession(t, db, identityID, "gesture", "started", identityAccount)
	hostileMustFail(t, db, `
INSERT INTO game_rps_seats(
 session_id,seat_no,user_id,deletion_state,starting_balance,current_balance,current_round_input,
 current_all_in,current_gesture_envelope,follower_action,last_action_phase_seq,total_input,
 total_returned,terminal_return,wallet_net_sign,wallet_net_mag,rock_count,scissors_count,
 paper_count,timeout_count,stats_applied
) VALUES(?,0,NULL,'active',?,?,?,0,NULL,NULL,NULL,?,?,?,?,?,?,?,?,?,0)`, identityID, zero, zero, zero, hostileBlob32(0), hostileBlob32(0), zero, zero, zero, zero, zero)

	// Pending-result own outcome is derived from the signed wallet net.  The
	// result projection cannot claim a win/loss/tie inconsistent with sign.
	insertPending := func(marker byte, userID int64, sign int, mag []byte, ownResult string, wantFail bool) {
		t.Helper()
		query := `
INSERT INTO game_rps_pending_results(
 user_id,session_id_text,mode,terminal_reason,own_seat_no,own_input,own_returned,
 own_wallet_net_sign,own_wallet_net_mag,seat0_result,seat1_result,seat2_result,created_at
) VALUES(?,?, 'quick','quick_resolved',0,?,?,?,? ,?,?,?,0)`
		if wantFail {
			hostileMustFail(t, db, query, userID, hostileOIDVariant("rps_", marker, 'Q'), hostileBlob32(marker), hostileBlob32(marker+1), sign, mag, ownResult, "loss", "tie")
			return
		}
		hostileMustExec(t, db, query, userID, hostileOIDVariant("rps_", marker, 'Q'), hostileBlob32(marker), hostileBlob32(marker+1), sign, mag, ownResult, "loss", "tie")
	}
	positivePendingUser := hostileInsertUser(t, db, "pending-positive", 0, 0)
	negativePendingUser := hostileInsertUser(t, db, "pending-negative", 0, 0)
	zeroPendingUser := hostileInsertUser(t, db, "pending-zero", 0, 0)
	insertPending('p', positivePendingUser, 1, one, "win", false)
	insertPending('n', negativePendingUser, -1, one, "loss", false)
	insertPending('z', zeroPendingUser, 0, zero, "tie", false)
	insertPending('P', hostileInsertUser(t, db, "pending-bad-positive", 0, 0), 1, one, "loss", true)
	insertPending('N', hostileInsertUser(t, db, "pending-bad-negative", 0, 0), -1, one, "win", true)
	insertPending('Z', hostileInsertUser(t, db, "pending-bad-zero", 0, 0), 0, zero, "win", true)

	// Rank facts use the same sign predicate, while aggregate rows encode the
	// exact rolling eligibility/count/rate relation.
	insertRankFact := func(marker byte, userID int64, sign int, mag []byte, profitable int, wantFail bool) {
		t.Helper()
		query := `
INSERT INTO game_rps_rank_facts(
 session_id_text,user_id,mode,terminal_at,expires_at,wallet_net_sign,wallet_net_mag,
 profitable,aggregate_applied
) VALUES(? ,?,'quick',0,2592000,?,?,?,0)`
		if wantFail {
			hostileMustFail(t, db, query, hostileOIDVariant("rps_", marker, 'Q'), userID, sign, mag, profitable)
			return
		}
		hostileMustExec(t, db, query, hostileOIDVariant("rps_", marker, 'Q'), userID, sign, mag, profitable)
	}
	insertRankFact('u', hostileInsertUser(t, db, "rank-positive", 0, 0), 1, one, 1, false)
	insertRankFact('v', hostileInsertUser(t, db, "rank-negative", 0, 0), -1, one, 0, false)
	insertRankFact('w', hostileInsertUser(t, db, "rank-zero", 0, 0), 0, zero, 0, false)
	insertRankFact('U', hostileInsertUser(t, db, "rank-bad-positive", 0, 0), 1, one, 0, true)
	insertRankFact('V', hostileInsertUser(t, db, "rank-bad-negative", 0, 0), -1, one, 1, true)
	insertRankFact('W', hostileInsertUser(t, db, "rank-bad-zero", 0, 0), 0, zero, 1, true)

	insertAggregate := func(marker byte, userID int64, sessionCount, profitableCount []byte, eligible, rate int, wantFail bool) {
		t.Helper()
		query := `
INSERT INTO game_rps_rank_aggregates(
 user_id,mode,session_count,profitable_count,net_profit_sign,net_profit_mag,eligible,
 profit_rate_bp,profit_rate_achieved_at,net_profit_achieved_at,profit_public_tie_key,
 net_public_tie_key,revision,updated_at
 ) VALUES(?,'quick',?,?,0,?,?,?,0,0,?,?,?,0)`
		args := []any{userID, sessionCount, profitableCount, zero, eligible, rate, hostileBlob32(marker), hostileBlob32(marker + 1), one}
		if wantFail {
			hostileMustFail(t, db, query, args...)
			return
		}
		hostileMustExec(t, db, query, args...)
	}
	insertAggregate('a', hostileInsertUser(t, db, "aggregate-valid", 0, 0), hostileBlob16(10), hostileBlob16(5), 1, 5000, false)
	insertAggregate('b', hostileInsertUser(t, db, "aggregate-ineligible", 0, 0), hostileBlob16(10), hostileBlob16(5), 0, 5000, true)
	insertAggregate('c', hostileInsertUser(t, db, "aggregate-under-ten", 0, 0), hostileBlob16(9), hostileBlob16(5), 1, 5555, true)
	// The rate formula is a service-owned projection invariant; the schema only
	// constrains its persisted scalar domain and the eligibility/count matrix.
	insertAggregate('d', hostileInsertUser(t, db, "aggregate-rate", 0, 0), hostileBlob16(10), hostileBlob16(5), 1, 4999, false)
	insertAggregate('e', hostileInsertUser(t, db, "aggregate-profitable-over", 0, 0), hostileBlob16(10), hostileBlob16(11), 1, 5000, true)
	insertAggregate('f', hostileInsertUser(t, db, "aggregate-zero", 0, 0), zero, zero, 0, 0, true)
}

func TestGenerationTwoHostileAutomaticCatalogIdentity(t *testing.T) {
	db := openGenerationTwoDDLForTest(t)
	uid := hostileInsertUser(t, db, "catalog", 0, 0)
	endpointID := hostileInsertEndpoint(t, db, uid, "https://upstream.example/v1")
	secretID := hostileInsertSecret(t, db, "https://upstream.example/v1", 0)
	keyID := hostileInsertEndpointKey(t, db, endpointID, secretID)

	insertEntry := func(id int64, sourceType, sourceIdentity, model string, sourceRevision int64) {
		t.Helper()
		hostileMustExec(t, db, `
INSERT INTO model_catalog_entries(
 id,endpoint_key_id,source_type,source_identity,normalized_model_id,provider,source_revision,created_at,updated_at
) VALUES(?,?,?,?,?,'provider',?,0,0)`, id, keyID, sourceType, sourceIdentity, model, sourceRevision)
	}
	insertEntry(1, "manual", "provider/model", "provider/model", 1)
	insertEntry(2, "automatic", "1:0", "provider/automatic", 1)
	insertEntry(3, "automatic", "1:9999", "provider/automatic-max", 1)
	insertEntry(4, "automatic", "9223372036854775807:9999", "provider/automatic-revision-max", hostileInt64Max)
	maxModel := strings.Repeat("界", 512)
	hostileMustExec(t, db, `
INSERT INTO model_catalog_entries(
 id,endpoint_key_id,source_type,source_identity,normalized_model_id,provider,source_revision,created_at,updated_at
) VALUES(5,?,'manual',?,?, '',1,0,0)`, keyID, maxModel, maxModel)
	hostileMustExec(t, db, `
INSERT INTO model_catalog_entries(
 id,endpoint_key_id,source_type,source_identity,normalized_model_id,provider,source_revision,created_at,updated_at
) VALUES(6,?,'manual','provider-limit','provider-limit',?,1,0,0)`, keyID, strings.Repeat("供", 128))
	hostileMustFail(t, db, `
INSERT INTO model_catalog_entries(
 id,endpoint_key_id,source_type,source_identity,normalized_model_id,provider,source_revision,created_at,updated_at
) VALUES(7,?,'manual',?,?, '',1,0,0)`, keyID, strings.Repeat("界", 513), strings.Repeat("界", 513))
	hostileMustFail(t, db, `
INSERT INTO model_catalog_entries(
 id,endpoint_key_id,source_type,source_identity,normalized_model_id,provider,source_revision,created_at,updated_at
) VALUES(8,?,'manual','provider-over','provider-over',?,1,0,0)`, keyID, strings.Repeat("供", 129))

	for i, tc := range []struct {
		name, sourceType, sourceIdentity, model string
		revision                                int64
	}{
		{"manual-identity-mismatch", "manual", "provider/other", "provider/model-mismatch", 1},
		{"automatic-leading-revision-zero", "automatic", "01:0", "provider/auto-leading", 1},
		{"automatic-leading-ordinal-zero", "automatic", "1:00", "provider/auto-leading-ordinal", 1},
		{"automatic-revision-mismatch", "automatic", "2:0", "provider/auto-mismatch", 1},
		{"automatic-ordinal-overflow", "automatic", "1:10000", "provider/auto-overflow", 1},
		{"automatic-extra-colon", "automatic", "1:0:1", "provider/auto-colon", 1},
		{"automatic-negative", "automatic", "-1:0", "provider/auto-negative", 1},
		{"automatic-zero-revision", "automatic", "0:0", "provider/auto-zero", 0},
	} {
		t.Run(fmt.Sprintf("%02d-%s", i, tc.name), func(t *testing.T) {
			hostileMustFail(t, db, `
INSERT INTO model_catalog_entries(
 id,endpoint_key_id,source_type,source_identity,normalized_model_id,provider,source_revision,created_at,updated_at
) VALUES(?,?,?,?,?,'provider',?,0,0)`, 10+i, keyID, tc.sourceType, tc.sourceIdentity, tc.model, tc.revision)
		})
	}

	// A pair row is the second half of the catalog identity.  Its support
	// counters are non-negative and its revision is a positive checked scalar.
	hostileMustExec(t, db, `
INSERT INTO model_pair_catalog(endpoint_key_id,normalized_model_id,automatic_supports,manual_supports,automatic_revision,pair_revision,updated_at)
VALUES(?, 'provider/model', 0, 1, 0, 1, 0)`, keyID)
	hostileMustExec(t, db, `UPDATE model_pair_catalog SET automatic_revision=?,pair_revision=? WHERE endpoint_key_id=? AND normalized_model_id='provider/model'`, hostileInt64Max, hostileInt64Max, keyID)
	hostileMustFail(t, db, `UPDATE model_pair_catalog SET automatic_supports=-1 WHERE endpoint_key_id=? AND normalized_model_id='provider/model'`, keyID)
	hostileMustFail(t, db, `UPDATE model_pair_catalog SET pair_revision=0 WHERE endpoint_key_id=? AND normalized_model_id='provider/model'`, keyID)

	modelResult := hostileMustExec(t, db, `
INSERT INTO models(user_id,provider,model,full_name,route_strategy,silent_retry,flatten_tool_calls,revision,binding_revision,created_at,updated_at)
VALUES(?,'provider','model','provider/model','ordered',0,0,1,0,0,0)`, uid)
	modelID := hostileMustLastID(t, modelResult)
	hostileMustExec(t, db, `UPDATE models SET revision=?,binding_revision=? WHERE id=?`, hostileInt64Max, hostileInt64Max, modelID)
	hostileMustFail(t, db, `UPDATE models SET revision=0 WHERE id=?`, modelID)
	hostileMustFail(t, db, `UPDATE models SET binding_revision=? WHERE id=?`, "9223372036854775808", modelID)
	bindingID := hostileNextPK64(t, db, "model_bindings")
	hostileMustExec(t, db, `
	INSERT INTO model_bindings(id,model_id,endpoint_key_id,upstream_model_id,ord,created_at,updated_at)
	VALUES(?,?,?, ?,0,0,0)`, bindingID, modelID, keyID, "provider/model")
	hostileMustFail(t, db, `
INSERT INTO model_bindings(model_id,endpoint_key_id,upstream_model_id,ord,created_at,updated_at)
VALUES(?,?,?,1,0,0)`, modelID, keyID, "provider/missing")
	hostileMustExec(t, db, `
INSERT INTO charity_models(provider,model,full_name,pricing_mode,revision,binding_revision,created_at,updated_at)
VALUES('provider','charity','[公益]provider/charity','per_request',?, ?,0,0)`, hostileInt64Max, hostileInt64Max)
}

func TestGenerationTwoHostilePK64PositiveIDs(t *testing.T) {
	db := openGenerationTwoDDLForTest(t)
	adminID := hostileInsertUser(t, db, "pk64-admin", 1, 0)
	userID := hostileInsertUser(t, db, "pk64-user", 0, 0)
	endpointID := hostileInsertEndpoint(t, db, userID, "https://pk64.example/v1")
	secretID := hostileInsertSecret(t, db, "https://pk64.example/v1", 0)
	endpointKeyID := hostileInsertEndpointKey(t, db, endpointID, secretID)
	pkEndpointID := hostileInsertEndpoint(t, db, userID, "https://pk64-key.example/v1")
	pkSecretID := hostileInsertSecret(t, db, "https://pk64-key.example/v1", 1)

	// Build one complete catalog/model/charity graph so that every negative
	// primary-key assertion reaches the PK64 contract rather than a missing-FK
	// or incomplete-fixture check.
	catalogEntryID := hostileNextPK64(t, db, "model_catalog_entries")
	hostileMustExec(t, db, `
	INSERT INTO model_catalog_entries(
	 id,endpoint_key_id,source_type,source_identity,normalized_model_id,provider,
	 source_revision,created_at,updated_at
	) VALUES(?,?,'manual','upstream','upstream','provider',1,0,0)`, catalogEntryID, endpointKeyID)
	hostileMustExec(t, db, `
INSERT INTO model_pair_catalog(
 endpoint_key_id,normalized_model_id,automatic_supports,manual_supports,
 automatic_revision,pair_revision,updated_at
) VALUES(?,'upstream',0,1,0,1,0)`, endpointKeyID)
	modelID := hostileMustLastID(t, hostileMustExec(t, db, `
INSERT INTO models(user_id,provider,model,full_name,revision,binding_revision,created_at,updated_at)
VALUES(?,'provider','model','provider/model',1,0,0,0)`, userID))
	charityModelID := hostileMustLastID(t, hostileMustExec(t, db, `
INSERT INTO charity_models(
 provider,model,full_name,enabled,pricing_mode,revision,binding_revision,created_at,updated_at
) VALUES('provider','model','[公益]provider/model',1,'per_request',1,0,0,0)`))
	donationID := hostileInsertDonation(t, db, userID)
	donationKeyID := hostileInsertDonationKey(t, db, donationID, endpointKeyID)
	pkDonationID := hostileInsertDonation(t, db, userID)
	reportID := hostileOIDVariant("rpc_", 'P', 'Q')
	hostileInsertReportCase(t, db, reportID, hostileBlob32(91), "pending_review", "complete", 0)
	charityRequestID := hostileOIDVariant("req_", 'C', 'Q')
	hostileInsertLogicalRequest(t, db, charityRequestID, userID, "charity_chat_completions", 1)

	// Each row is otherwise valid.  Use a subtest for every table so a schema
	// under repair reports all missing PK64 guards in one run; a failed hostile
	// assertion cannot poison the fixture for another table.
	pkCases := []struct {
		name  string
		query string
		args  []any
	}{
		{
			"endpoint-keys",
			`INSERT INTO endpoint_keys(
 id,endpoint_id,secret_ref_id,secret_fingerprint,display_head,display_tail,note,
 enabled,force_store_false,revision,created_at,updated_at
) VALUES(? ,? ,? ,? ,'head','tail','',1,0,1,0,0)`,
			[]any{pkEndpointID, pkSecretID, hostileBlob32(101)},
		},
		{
			"model-catalog-entries",
			`INSERT INTO model_catalog_entries(
 id,endpoint_key_id,source_type,source_identity,normalized_model_id,provider,
 source_revision,created_at,updated_at
) VALUES(? ,? ,'manual','pk64-upstream','pk64-upstream','provider',1,0,0)`,
			[]any{endpointKeyID},
		},
		{
			"model-bindings",
			`INSERT INTO model_bindings(
 id,model_id,endpoint_key_id,upstream_model_id,ord,created_at,updated_at
) VALUES(? ,? ,? ,'upstream',1,0,0)`,
			[]any{modelID, endpointKeyID},
		},
		{
			"charity-model-bindings",
			`INSERT INTO charity_model_bindings(
 id,charity_model_id,donation_key_id,endpoint_key_id,upstream_model_id,ord,created_at,updated_at
) VALUES(? ,? ,? ,? ,'upstream',1,0,0)`,
			[]any{charityModelID, donationKeyID, endpointKeyID},
		},
		{
			"credit-accounts",
			`INSERT INTO credit_accounts(
 id,kind,user_id,code,balance_sign,balance_mag,created_at,updated_at
) VALUES(? ,'external',NULL,'external',0,?,0,0)`,
			[]any{hostileBlob16(0)},
		},
		{
			"announcement-audits",
			`INSERT INTO announcement_audits(
 id,announcement_id_text,actor_user_id,action,from_revision,to_revision,reason,
 created_at,actor_deidentify_at
) VALUES(? ,? ,? ,'create',0,1,'hostile',0,7776000)`,
			[]any{hostileOIDVariant("ann_", 'P', 'Q'), adminID},
		},
		{
			"welfare-claims",
			`INSERT INTO welfare_claims(
 id,user_id,site_day,operation_id,threshold_milli,cap_milli,pool_before_milli,
 award_milli,created_at
) VALUES(? ,? ,'1970-01-01',? ,0,1,1,1,0)`,
			[]any{userID, hostileOIDVariant("op_", 'W', 'Q')},
		},
		{
			"donations",
			`INSERT INTO donations(
 id,user_id,status,revision,description,review_note,created_at,updated_at
) VALUES(? ,? ,'pending',1,'','',0,0)`,
			[]any{userID},
		},
		{
			"donation-keys",
			`INSERT INTO donation_keys(
 id,donation_id,endpoint_key_id,price_used_mag,price_reserved_mag,calls_used,
 calls_reserved,tokens_used,tokens_reserved,failure_streak,streak_generation,
 next_claim_seq,next_fold_seq,created_at,updated_at
) VALUES(? ,? ,? ,? ,? ,? ,? ,? ,? ,? ,? ,? ,? ,0,0)`,
			[]any{pkDonationID, endpointKeyID, hostileBlob16(0), hostileBlob16(0), hostileBlob16(0), hostileBlob16(0), hostileBlob16(0), hostileBlob16(0), hostileBlob16(0), hostileBlob16(1), hostileBlob16(1), hostileBlob16(0)},
		},
		{
			"donation-reviews",
			`INSERT INTO donation_reviews(
 id,donation_id,submission_revision,reviewer_user_id,reviewer_role,action,note,created_at
) VALUES(? ,? ,1,? ,'admin','note_update','',0)`,
			[]any{donationID, adminID},
		},
		{
			"charity-reservations",
			`INSERT INTO charity_reservations(
 id,logical_request_id,user_id,charity_model_id,model_snapshot,state,pricing_mode,
 discount_percent,request_user_price_milli,request_donor_reward_milli,
 uncached_user_price_milli,cache_write_user_price_milli,cache_read_user_price_milli,
 output_user_price_milli,uncached_donor_reward_milli,cache_write_donor_reward_milli,
 cache_read_donor_reward_milli,output_donor_reward_milli,token_reserve_milli,
 user_reserved_milli,original_charge_milli,user_charge_milli,donor_reward_total_mag,
 usage_uncached_input_tokens,cache_write_input_tokens,cache_read_input_tokens,
 usage_output_tokens,usage_unknown,created_at,updated_at
) VALUES(? ,? ,? ,? ,'provider/model','reserved','per_request',100,0,0,0,0,0,0,0,0,0,0,0,0,0,0,? ,0,0,0,0,0,0,0)`,
			[]any{charityRequestID, userID, charityModelID, hostileBlob16(0)},
		},
		{
			"report-materials",
			`INSERT INTO report_materials(
 id,case_id,material_hash,note_text,source_ip_envelope,created_at
) VALUES(? ,? ,? ,'',? ,0)`,
			[]any{reportID, hostileBlob32(102), make([]byte, 45)},
		},
		{
			"report-decisions",
			`INSERT INTO report_decisions(
 id,case_id,material_version,target_version,actor_user_id,action,reason,created_at
) VALUES(? ,? ,1,1,? ,'reject','hostile',0)`,
			[]any{reportID, adminID},
		},
		{
			"legal-hold-audits",
			`INSERT INTO legal_hold_audits(
 id,hold_id_text,actor_user_id,action,reason,created_at,retain_until
) VALUES(? ,? ,? ,'create',NULL,100,NULL)`,
			[]any{hostileOIDVariant("lgh_", 'P', 'Q'), adminID},
		},
	}

	// The welfare operation and legal hold are the two rows whose FK matrix is
	// not expressible by a DEFAULT.  Seed them before the table loops.
	hostileInsertOperation(t, db, hostileOIDVariant("op_", 'W', 'Q'), 91, "welfare_claim", "operation", hostileOIDVariant("op_", 'W', 'Q'))
	hostileInsertLegalHold(t, db, hostileOIDVariant("lgh_", 'P', 'Q'), "report_case", reportID, adminID)

	for _, tc := range pkCases {
		t.Run(tc.name, func(t *testing.T) {
			for _, id := range []int64{0, -1} {
				args := make([]any, 0, len(tc.args)+1)
				args = append(args, id)
				args = append(args, tc.args...)
				hostileMustFail(t, db, tc.query, args...)
			}
		})
	}
}

func TestGenerationTwoHostileAnnouncementAuditRevisionMatrix(t *testing.T) {
	db := openGenerationTwoDDLForTest(t)
	adminID := hostileInsertUser(t, db, "announcement-revisions", 1, 0)
	hostileInsertAnnouncementAudit(t, db, adminID, 0)

	insertAudit := func(id, action string, from, to int64) {
		t.Helper()
		auditID := hostileNextPK64(t, db, "announcement_audits")
		hostileMustExec(t, db, `
INSERT INTO announcement_audits(
 id,announcement_id_text,actor_user_id,action,from_revision,to_revision,reason,
 created_at,actor_deidentify_at
) VALUES(?,?,?,?,?,?,'hostile',0,7776000)`, auditID, id, adminID, action, from, to)
	}
	insertAudit(hostileOIDVariant("ann_", 'E', 'Q'), "edit", 1, 2)
	insertAudit(hostileOIDVariant("ann_", 'P', 'Q'), "publish", 2, 3)
	hostileMustFail(t, db, `
INSERT INTO announcement_audits(
 announcement_id_text,actor_user_id,action,from_revision,to_revision,reason,
 created_at,actor_deidentify_at
) VALUES(? ,? ,'edit',0,1,'hostile',0,7776000)`, hostileOIDVariant("ann_", 'Z', 'Q'), adminID)
	hostileMustFail(t, db, `
INSERT INTO announcement_audits(
 announcement_id_text,actor_user_id,action,from_revision,to_revision,reason,
 created_at,actor_deidentify_at
) VALUES(? ,? ,'publish',2,2,'hostile',0,7776000)`, hostileOIDVariant("ann_", 'Y', 'Q'), adminID)
	hostileMustFail(t, db, `
INSERT INTO announcement_audits(
 announcement_id_text,actor_user_id,action,from_revision,to_revision,reason,
 created_at,actor_deidentify_at
) VALUES(? ,? ,'withdraw',3,2,'hostile',0,7776000)`, hostileOIDVariant("ann_", 'X', 'Q'), adminID)
}

func TestGenerationTwoHostileUnicodeScalarTextBounds(t *testing.T) {
	db := openGenerationTwoDDLForTest(t)
	uid := hostileInsertUser(t, db, "unicode-text", 0, 0)
	adminID := hostileInsertUser(t, db, "unicode-text-admin", 1, 0)
	text1024 := strings.Repeat("界", 1024)
	text1025 := strings.Repeat("界", 1025)
	text2048 := strings.Repeat("界", 2048)
	text2049 := strings.Repeat("界", 2049)

	// Donor description and review note are Unicode-scalar limits, not byte
	// limits: each test string is three UTF-8 bytes per scalar.
	donationIDValue := hostileNextPK64(t, db, "donations")
	donationID := hostileMustLastID(t, hostileMustExec(t, db, `
	INSERT INTO donations(id,user_id,status,revision,description,review_note,created_at,updated_at)
	VALUES(?,?,'pending',1,?,?,0,0)`, donationIDValue, uid, text1024, text1024))
	hostileMustFail(t, db, `
INSERT INTO donations(user_id,status,revision,description,review_note,created_at,updated_at)
VALUES(?,'pending',1,?,?,0,0)`, uid, text1025, "")
	hostileMustFail(t, db, `
INSERT INTO donations(user_id,status,revision,description,review_note,created_at,updated_at)
VALUES(?,'pending',1,?,?,0,0)`, uid, "", text1025)

	reviewID := hostileNextPK64(t, db, "donation_reviews")
	hostileMustExec(t, db, `
	INSERT INTO donation_reviews(id,donation_id,submission_revision,reviewer_user_id,reviewer_role,action,note,created_at)
	VALUES(?,?,1,?,'admin','note_update',?,0)`, reviewID, donationID, adminID, text1024)
	hostileMustFail(t, db, `
INSERT INTO donation_reviews(donation_id,submission_revision,reviewer_user_id,reviewer_role,action,note,created_at)
VALUES(?,1,?,'admin','note_update',?,0)`, donationID, adminID, text1025)

	reportID := hostileOIDVariant("rpc_", 'U', 'Q')
	hostileInsertReportCase(t, db, reportID, hostileBlob32(111), "pending_review", "complete", 0)
	materialID := hostileNextPK64(t, db, "report_materials")
	hostileMustExec(t, db, `
	INSERT INTO report_materials(id,case_id,material_hash,note_text,source_ip_envelope,created_at)
	VALUES(?,?,?, ?, ?,0)`, materialID, reportID, hostileBlob32(112), text1024, make([]byte, 45))
	hostileMustExec(t, db, `
INSERT INTO report_materials(case_id,material_hash,note_text,source_ip_envelope,created_at)
VALUES(?,?,?, ?,0)`, reportID, hostileBlob32(113), text2048, make([]byte, 45))
	hostileMustFail(t, db, `
INSERT INTO report_materials(case_id,material_hash,note_text,source_ip_envelope,created_at)
VALUES(?,?,?, ?,0)`, reportID, hostileBlob32(114), text2049, make([]byte, 45))

	decisionID := hostileNextPK64(t, db, "report_decisions")
	hostileMustExec(t, db, `
	INSERT INTO report_decisions(id,case_id,material_version,target_version,actor_user_id,action,reason,created_at)
	VALUES(?,?,1,1,?,'reject',?,0)`, decisionID, reportID, adminID, text2048)
	hostileMustFail(t, db, `
INSERT INTO report_decisions(case_id,material_version,target_version,actor_user_id,action,reason,created_at)
VALUES(?,1,1,?,'reject',?,0)`, reportID, adminID, text2049)
}

func TestGenerationTwoHostileSQLiteIntegerAffinity(t *testing.T) {
	db := openGenerationTwoDDLForTest(t)
	uid := hostileInsertUser(t, db, "integer-affinity", 0, 0)
	adminID := hostileInsertUser(t, db, "integer-affinity-admin", 1, 0)

	donationID := hostileInsertDonation(t, db, uid)
	hostileMustFail(t, db, `UPDATE donations SET revision=1.5 WHERE id=?`, donationID)
	hostileMustFail(t, db, `UPDATE donations SET created_at=1.5 WHERE id=?`, donationID)
	endpointID := hostileInsertEndpoint(t, db, uid, "https://integer-affinity.example/v1")
	hostileMustFail(t, db, `UPDATE endpoints SET enabled=1.5 WHERE id=?`, endpointID)

	welfareOperationID := hostileOIDVariant("op_", 'I', 'Q')
	hostileInsertOperation(t, db, welfareOperationID, 101, "welfare_claim", "operation", welfareOperationID)
	welfareUser := hostileInsertUser(t, db, "integer-affinity-welfare", 0, 0)
	welfareIDValue := hostileNextPK64(t, db, "welfare_claims")
	welfareID := hostileMustLastID(t, hostileMustExec(t, db, `
	INSERT INTO welfare_claims(id,user_id,site_day,operation_id,threshold_milli,cap_milli,pool_before_milli,award_milli,created_at)
	VALUES(?,?,'1970-01-01',?,0,2,2,1,0)`, welfareIDValue, welfareUser, welfareOperationID))
	hostileMustFail(t, db, `UPDATE welfare_claims SET award_milli=1.5 WHERE id=?`, welfareID)

	fishingUser := hostileInsertUser(t, db, "integer-affinity-fishing", 0, 0)
	fishingBatchID := hostileOIDVariant("fb_", 'I', 'Q')
	fishingOperationID := hostileOIDVariant("op_", 'F', 'Q')
	hostileInsertFishingBatch(t, db, fishingBatchID, fishingUser, fishingOperationID)
	hostileMustFail(t, db, `UPDATE game_fishing_batches SET count=1.5 WHERE id=?`, fishingBatchID)
	hostileMustExec(t, db, `
INSERT INTO game_fishing_outcomes(batch_id,ordinal,species_key,tier,size_cm,payout_milli)
VALUES(?,0,'whitebait','small',5,1)`, fishingBatchID)
	hostileMustFail(t, db, `UPDATE game_fishing_outcomes SET ordinal=0.5 WHERE batch_id=? AND ordinal=0`, fishingBatchID)

	requestID := hostileOIDVariant("req_", 'I', 'Q')
	hostileInsertLogicalRequest(t, db, requestID, uid, "openai_chat_completions", 1)
	requestLogID := hostileInsertRequestLog(t, db, requestID, uid, "openai_chat_completions")
	claimID := hostileOIDVariant("clm_", 'I', 'Q')
	hostileInsertAttempt(t, db, claimID, requestLogID, 1)
	hostileMustFail(t, db, `UPDATE request_attempts SET attempt_seq=1.5 WHERE claim_id=?`, claimID)
	hostileMustFail(t, db, `UPDATE request_attempts SET input_tokens=1.5 WHERE claim_id=?`, claimID)

	// A REAL in a boolean column must not be persisted merely because a loose
	// SQLite comparison happens to accept it; this second bool is independent
	// of the endpoint enabled flag above.
	announcementID := hostileInsertAnnouncementAudit(t, db, adminID, 0)
	hostileMustFail(t, db, `UPDATE announcement_audits SET legal_hold_consumed=1.5 WHERE id=?`, announcementID)
}

func TestGenerationTwoHostileIntegerColumnStructuralGuards(t *testing.T) {
	db := openGenerationTwoDDLForTest(t)
	rows, err := db.Query(`
SELECT name,sql FROM sqlite_schema
WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	var tables []struct {
		name string
		sql  sql.NullString
	}
	for rows.Next() {
		var tableName string
		var tableSQL sql.NullString
		if err := rows.Scan(&tableName, &tableSQL); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		tables = append(tables, struct {
			name string
			sql  sql.NullString
		}{name: tableName, sql: tableSQL})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	var missing []string
	for _, table := range tables {
		tableName := table.name
		compactTableSQL := hostileCompactSQL(table.sql.String)
		columns, err := db.Query(`PRAGMA table_info(` + hostileQuoteIdent(tableName) + `)`)
		if err != nil {
			t.Fatalf("table_info %s: %v", tableName, err)
		}
		var integerColumns []struct {
			name string
			pk   int
		}
		for columns.Next() {
			var cid, notNull, pk int
			var name, declaredType string
			var defaultValue any
			if err := columns.Scan(&cid, &name, &declaredType, &notNull, &defaultValue, &pk); err != nil {
				columns.Close()
				t.Fatalf("table_info scan %s: %v", tableName, err)
			}
			if !strings.EqualFold(strings.TrimSpace(declaredType), "INTEGER") {
				continue
			}
			integerColumns = append(integerColumns, struct {
				name string
				pk   int
			}{name: name, pk: pk})
		}
		if err := columns.Err(); err != nil {
			columns.Close()
			t.Fatalf("table_info rows %s: %v", tableName, err)
		}
		if err := columns.Close(); err != nil {
			t.Fatalf("close table_info %s: %v", tableName, err)
		}
		for _, column := range integerColumns {
			protected := strings.HasSuffix(compactTableSQL, "strict") || column.pk > 0 ||
				hostileInlineIntegerDefense(compactTableSQL, column.name) ||
				hostileIntegerColumnHasFK(t, db, tableName, column.name) ||
				hostileIntegerColumnHasInsertUpdateTypeGuards(t, db, tableName, column.name)
			if !protected {
				missing = append(missing, tableName+"."+column.name)
			}
		}
	}
	if len(missing) != 0 {
		t.Fatalf("INTEGER columns lack an approved type defense (BETWEEN/range checks alone do not count): %s", strings.Join(missing, ", "))
	}
}

func TestGenerationTwoHostileSQLiteIntegerAffinityRepresentative(t *testing.T) {
	db := openGenerationTwoDDLForTest(t)
	zero := hostileBlob16(0)
	uid := hostileInsertUser(t, db, "integer-representative", 0, 0)
	adminID := hostileInsertUser(t, db, "integer-representative-admin", 1, 0)

	// Ledger: a REAL must not pass the numeric range merely because SQLite's
	// affinity comparison says 1.5 is between the endpoints.
	ledgerID := hostileOIDVariant("op_", 'L', 'Q')
	hostileMustFail(t, db, `
INSERT INTO credit_operations(
 id,ledger_seq,kind,source_type,source_id,source_seq,
 donation_credit_delta_sign,donation_credit_delta_mag,created_at
) VALUES(?,1.5,'admin_user_adjustment','operation',?,?,0,?,0)`, ledgerID, ledgerID, zero, zero)
	capacityID := int64(1)
	hostileMustExec(t, db, `INSERT INTO credit_capacity(id,last_ledger_seq,reserved_future_rows,revision) VALUES(?,?,?,?)`, capacityID, 0, zero, zero)
	hostileMustFail(t, db, `UPDATE credit_capacity SET last_ledger_seq=1.5 WHERE id=?`, capacityID)

	// Logical request and dispatch claim each need both INSERT and UPDATE
	// defenses.  The claim's INTEGER attempt sequence is checked against its
	// request limit, which is another place weak typing otherwise slips through.
	requestID := hostileOIDVariant("req_", 'L', 'Q')
	hostileMustFail(t, db, `
INSERT INTO logical_requests(
 id,user_id,route_kind,state,attempt_limit,accounting_state,settlement_destination,
 ledger_rows_remaining,created_at
) VALUES(?,?, 'openai_chat_completions','accepted',1.5,'none','user',?,0)`, requestID, uid, hostileBlob16(1))
	validRequestID := hostileOIDVariant("req_", 'M', 'Q')
	hostileInsertLogicalRequest(t, db, validRequestID, uid, "openai_chat_completions", 1)
	hostileMustFail(t, db, `UPDATE logical_requests SET attempt_limit=1.5 WHERE id=?`, validRequestID)
	hostileMustFail(t, db, `UPDATE logical_requests SET created_at=1.5 WHERE id=?`, validRequestID)
	endpointID := hostileInsertEndpoint(t, db, uid, "https://integer-representative.example/v1")
	secretID := hostileInsertSecret(t, db, "https://integer-representative.example/v1", 0)
	endpointKeyID := hostileInsertEndpointKey(t, db, endpointID, secretID)
	hostileMustFail(t, db, `
INSERT INTO dispatch_claims(
 id,logical_request_id,attempt_seq,purpose,endpoint_key_id,secret_ref_id,claim_now,state,donor_reward_state
) VALUES(?,?,1.5,'self',?,?,0,'claimed','not_applicable')`, hostileOIDVariant("clm_", 'L', 'Q'), validRequestID, endpointKeyID, secretID)
	claimID := hostileOIDVariant("clm_", 'M', 'Q')
	hostileMustExec(t, db, `
INSERT INTO dispatch_claims(
 id,logical_request_id,attempt_seq,purpose,endpoint_key_id,secret_ref_id,claim_now,state,donor_reward_state
) VALUES(?,?,1,'self',?,?,0,'claimed','not_applicable')`, claimID, validRequestID, endpointKeyID, secretID)
	hostileMustFail(t, db, `UPDATE dispatch_claims SET attempt_seq=1.5 WHERE id=?`, claimID)

	// Donation and charity reservations use positive INTEGER PK rows and
	// monetary/count columns; explicit IDs keep this fixture independent of
	// any hostile AUTOINCREMENT guard.
	donationID := hostileNextPK64(t, db, "donations")
	hostileMustFail(t, db, `
INSERT INTO donations(id,user_id,status,revision,description,review_note,created_at,updated_at)
VALUES(?,?, 'pending',1.5,'','',0,0)`, donationID, uid)
	validDonationID := hostileInsertDonation(t, db, uid)
	hostileMustFail(t, db, `UPDATE donations SET revision=1.5 WHERE id=?`, validDonationID)
	charityModelID := hostileMustLastID(t, hostileMustExec(t, db, `
INSERT INTO charity_models(
 provider,model,full_name,enabled,pricing_mode,revision,binding_revision,created_at,updated_at
) VALUES('integer','representative','[公益]integer/representative',1,'per_request',1,0,0,0)`))
	charityRequestID := hostileOIDVariant("req_", 'C', 'Q')
	hostileInsertLogicalRequest(t, db, charityRequestID, uid, "charity_chat_completions", 1)
	charityReservationID := hostileNextPK64(t, db, "charity_reservations")
	hostileMustFail(t, db, `
INSERT INTO charity_reservations(
 id,logical_request_id,user_id,charity_model_id,model_snapshot,state,pricing_mode,
 discount_percent,request_user_price_milli,request_donor_reward_milli,
 uncached_user_price_milli,cache_write_user_price_milli,cache_read_user_price_milli,
 output_user_price_milli,uncached_donor_reward_milli,cache_write_donor_reward_milli,
 cache_read_donor_reward_milli,output_donor_reward_milli,token_reserve_milli,
 user_reserved_milli,original_charge_milli,user_charge_milli,donor_reward_total_mag,
 usage_uncached_input_tokens,cache_write_input_tokens,cache_read_input_tokens,
 usage_output_tokens,usage_unknown,created_at,updated_at
	) VALUES(
	 ?,?,?,?,
	 'integer/representative','reserved','per_request',
	 1.5,
	 0,0,0,0,0,0,0,0,0,0,0,0,0,0,
	 ?,0,0,0,0,0,0,0
	)`,
		charityReservationID, charityRequestID, uid, charityModelID, zero)
	validCharityRequestID := hostileOIDVariant("req_", 'D', 'Q')
	hostileInsertLogicalRequest(t, db, validCharityRequestID, uid, "charity_chat_completions", 1)
	validCharityReservationID := hostileNextPK64(t, db, "charity_reservations")
	hostileMustExec(t, db, `
INSERT INTO charity_reservations(
 id,logical_request_id,user_id,charity_model_id,model_snapshot,state,pricing_mode,
 discount_percent,request_user_price_milli,request_donor_reward_milli,
 uncached_user_price_milli,cache_write_user_price_milli,cache_read_user_price_milli,
 output_user_price_milli,uncached_donor_reward_milli,cache_write_donor_reward_milli,
 cache_read_donor_reward_milli,output_donor_reward_milli,token_reserve_milli,
 user_reserved_milli,original_charge_milli,user_charge_milli,donor_reward_total_mag,
 usage_uncached_input_tokens,cache_write_input_tokens,cache_read_input_tokens,
 usage_output_tokens,usage_unknown,created_at,updated_at
	) VALUES(
	 ?,?,?,?,
	 'integer/representative','reserved','per_request',
	 100,
	 0,0,0,0,0,0,0,0,0,0,0,0,0,0,
	 ?,0,0,0,0,0,0,0
	)`,
		validCharityReservationID, validCharityRequestID, uid, charityModelID, zero)
	hostileMustFail(t, db, `UPDATE charity_reservations SET discount_percent=1.5 WHERE id=?`, validCharityReservationID)

	// Thursday, Fishing, LinkLink and RPS each have an INTEGER count/time
	// field with a valid parent row behind it.
	periodID, currentPoolID, nextPoolID := hostileInsertThursdayFixture(t, db)
	participantID := hostileOIDVariant("thp_", 'I', 'Q')
	hostileMustFail(t, db, `
INSERT INTO thursday_participants(
 period_id,participant_ref,user_id,contribution_count,contributed_mag,eligible_at_freeze,
 payout_mag,settled,ledger_rows_remaining,created_at,updated_at
) VALUES(?,?,?, ?,?,?, ?,1.5,?,0,0)`, periodID, participantID, uid, zero, zero, 1, zero, zero)
	hostileMustFail(t, db, `UPDATE thursday_periods SET per_user_limit=1.5 WHERE id=?`, periodID)
	_ = currentPoolID
	_ = nextPoolID
	fishingUser := hostileInsertUser(t, db, "integer-representative-fishing", 0, 0)
	fishingBatchID := hostileOIDVariant("fb_", 'I', 'Q')
	fishingOperationID := hostileOIDVariant("op_", 'F', 'Q')
	hostileMustFail(t, db, `
INSERT INTO game_fishing_batches(
 id,user_id,bait,count,unit_price_milli,entry_total_milli,payout_total_milli,
 operation_id,request_hash,state,ledger_rows_remaining,attempt_count,next_attempt_at,
 retry_exhausted,created_at
) VALUES(?,?, 'worm',1.5,1,1,1,?,?, 'reserved',?,0,0,0,0)`, fishingBatchID, fishingUser, fishingOperationID, hostileBlob32(210), hostileBlob16(1))
	hostileInsertFishingBatch(t, db, fishingBatchID, fishingUser, fishingOperationID)
	hostileMustFail(t, db, `UPDATE game_fishing_batches SET count=1.5 WHERE id=?`, fishingBatchID)
	linklinkID := hostileOIDVariant("ll_", 'I', 'Q')
	hostileMustFail(t, db, `
INSERT INTO game_linklink_sessions(
 id,user_id,spec,state,revision,price_milli,board_blob,removed_bits,pairs_removed,
 deadline,operation_id,request_hash,created_at,updated_at
) VALUES(?,?, '6x8','active',?,1,?, ?,1.5,0,?, ?,0,0)`,
		linklinkID, uid, hostileBlob16(1), []byte{}, make([]byte, 6), hostileOIDVariant("op_", 'K', 'Q'), hostileBlob32(211))
	validLinklinkID := hostileOIDVariant("ll_", 'J', 'Q')
	hostileMustExec(t, db, `
INSERT INTO game_linklink_sessions(
 id,user_id,spec,state,revision,price_milli,board_blob,removed_bits,pairs_removed,
 deadline,operation_id,request_hash,created_at,updated_at
) VALUES(?,?, '6x8','active',?,1,?, ?,0,0,?, ?,0,0)`,
		validLinklinkID, hostileInsertUser(t, db, "integer-representative-linklink", 0, 0), hostileBlob16(1), []byte{}, make([]byte, 6), hostileOIDVariant("op_", 'J', 'Q'), hostileBlob32(212))
	hostileMustFail(t, db, `UPDATE game_linklink_sessions SET pairs_removed=1.5 WHERE id=?`, validLinklinkID)
	rpsQueueID := hostileOIDVariant("rpsq_", 'I', 'Q')
	rpsQueueAccount := hostileInsertAccount(t, db, "platform", nil, "rps-queue:"+rpsQueueID, 0, zero, 0)
	rpsQueueUser := hostileInsertUser(t, db, "integer-representative-rps-queue", 0, 0)
	hostileMustFail(t, db, `
INSERT INTO game_rps_queue(
 id,user_id,account_id,mode,revision,reservation_operation_id,reserved,
 ledger_rows_remaining,device_token_hash,source_ip_hash,deadline,created_at
) VALUES(?,?,?,'quick',?,?,?, ?,?,?,1.5,0)`, rpsQueueID, rpsQueueUser, rpsQueueAccount,
		hostileBlob16(1), hostileOIDVariant("op_", 'Q', 'Q'), hostileBlob16(1), hostileBlob16(1), hostileBlob32(213), hostileBlob32(214))
	rpsID := hostileOIDVariant("rps_", 'I', 'Q')
	rpsAccount := hostileInsertRPSAccount(t, db, rpsID)
	hostileInsertRPSSession(t, db, rpsID, "gesture", "started", rpsAccount)
	hostileMustFail(t, db, `UPDATE game_rps_sessions SET base_milli=1.5 WHERE id=?`, rpsID)

	// Report and legal-hold INTEGER fields are covered on valid parent rows as
	// well; the tests intentionally use both INSERT and UPDATE paths.
	reportID := hostileOIDVariant("rpc_", 'I', 'Q')
	hostileMustFail(t, db, `
INSERT INTO report_cases(
 id,fingerprint,connector_type,canonical_base_url,status,progress_state,
 material_version,target_version,deadline,material_count,target_count,distinct_owner_count,created_at
) VALUES(?,?,'openai-compatible','https://upstream.example/v1','pending_review','complete',1.5,1,1000,0,0,0,0)`, reportID, hostileBlob32(215))
	validReportID := hostileOIDVariant("rpc_", 'J', 'Q')
	hostileInsertReportCase(t, db, validReportID, hostileBlob32(216), "pending_review", "complete", 0)
	hostileMustFail(t, db, `UPDATE report_cases SET material_version=1.5 WHERE id=?`, validReportID)
	maintenanceID := hostileOIDVariant("op_", 'H', 'Q')
	hostileInsertMaintenanceEvent(t, db, maintenanceID, adminID)
	holdID := hostileOIDVariant("lgh_", 'I', 'Q')
	hostileMustFail(t, db, `
INSERT INTO legal_holds(id,object_kind,object_ref,state,revision,basis,created_by_user_id,created_at,expires_at)
VALUES(?,?, 'active',1.5,'hostile',?,0,100)`, holdID, "maintenance_event", maintenanceID, adminID)
	validHoldID := hostileOIDVariant("lgh_", 'J', 'Q')
	hostileInsertLegalHold(t, db, validHoldID, "maintenance_event", maintenanceID, adminID)
	hostileMustFail(t, db, `UPDATE legal_holds SET revision=1.5 WHERE id=?`, validHoldID)
}

func TestGenerationTwoHostileIdempotencyExpiryAcrossScopes(t *testing.T) {
	db := openGenerationTwoDDLForTest(t)
	scopes := []string{
		"credential_report",
		"control_mutation",
		"openai_chat_completions",
		"charity_chat_completions",
		"model_discovery",
		"maintenance",
		"announcement",
		"activity",
		"game_fishing",
		"game_linklink",
		"game_rps",
		"donation",
	}
	for i, scope := range scopes {
		t.Run(scope, func(t *testing.T) {
			actorHash := hostileBlob32(byte(i + 1))
			keyHash := hostileBlob32(byte(i + 33))
			requestHash := hostileBlob32(byte(i + 65))
			lookup := hostileBlob32(byte(i + 97))
			if scope == "credential_report" {
				hostileMustExec(t, db, `
INSERT INTO idempotency_records(
 scope,actor_scope_hash,key_hash,request_hash,lookup_fingerprint,state,http_status,
 response_body,created_at,expires_at
) VALUES(?,?,?,?,?,'accepted',0,?,0,86400)`, scope, actorHash, keyHash, requestHash, lookup, []byte{})
				hostileMustFail(t, db, `
INSERT INTO idempotency_records(
 scope,actor_scope_hash,key_hash,request_hash,lookup_fingerprint,state,http_status,
 response_body,created_at,expires_at
) VALUES(?,?,?,?,?,'accepted',0,?,0,86399)`, scope, hostileBlob32(byte(i+2)), hostileBlob32(byte(i+34)), hostileBlob32(byte(i+66)), lookup, []byte{})
				hostileMustFail(t, db, `
INSERT INTO idempotency_records(
 scope,actor_scope_hash,key_hash,request_hash,lookup_fingerprint,state,http_status,
 response_body,created_at,expires_at
) VALUES(?,?,?,?,?,'accepted',0,?,0,86401)`, scope, hostileBlob32(byte(i+3)), hostileBlob32(byte(i+35)), hostileBlob32(byte(i+67)), lookup, []byte{})
				return
			}
			hostileMustExec(t, db, `
INSERT INTO idempotency_records(
 scope,actor_scope_hash,key_hash,request_hash,state,http_status,response_body,created_at,expires_at
) VALUES(?,?,?,?,'accepted',0,?,0,86400)`, scope, actorHash, keyHash, requestHash, []byte{})
			hostileMustFail(t, db, `
INSERT INTO idempotency_records(
 scope,actor_scope_hash,key_hash,request_hash,state,http_status,response_body,created_at,expires_at
) VALUES(?,?,?,?,'accepted',0,?,0,86399)`, scope, hostileBlob32(byte(i+2)), hostileBlob32(byte(i+34)), hostileBlob32(byte(i+66)), []byte{})
			hostileMustFail(t, db, `
INSERT INTO idempotency_records(
 scope,actor_scope_hash,key_hash,request_hash,state,http_status,response_body,created_at,expires_at
) VALUES(?,?,?,?,'accepted',0,?,0,86401)`, scope, hostileBlob32(byte(i+3)), hostileBlob32(byte(i+35)), hostileBlob32(byte(i+67)), []byte{})
		})
	}
	hostileMustFail(t, db, `
INSERT INTO idempotency_records(
 scope,actor_scope_hash,key_hash,request_hash,state,http_status,response_body,created_at,expires_at
) VALUES('unknown',?,?,?,'accepted',0,?,0,86400)`, hostileBlob32(240), hostileBlob32(241), hostileBlob32(242), []byte{})
}

func TestGenerationTwoHostileIdempotencyIdentityAndTransitions(t *testing.T) {
	db := openGenerationTwoDDLForTest(t)
	actorHash := hostileBlob32(1)
	keyHash := hostileBlob32(2)
	requestHash := hostileBlob32(3)
	hostileMustExec(t, db, `
INSERT INTO idempotency_records(
 scope,actor_scope_hash,key_hash,request_hash,state,http_status,response_body,created_at,expires_at
) VALUES('openai_chat_completions',?,?,?,'accepted',0,?,0,86400)`, actorHash, keyHash, requestHash, []byte{})

	// The idempotency identity and expiry are acceptance facts, not mutable
	// bookkeeping.  Each update keeps the value otherwise representable so a
	// missing immutable guard cannot hide behind a type/check failure.
	hostileMustFail(t, db, `UPDATE idempotency_records SET scope='activity' WHERE scope='openai_chat_completions' AND actor_scope_hash=? AND key_hash=?`, actorHash, keyHash)
	hostileMustFail(t, db, `UPDATE idempotency_records SET actor_scope_hash=? WHERE scope='openai_chat_completions' AND key_hash=?`, hostileBlob32(4), keyHash)
	hostileMustFail(t, db, `UPDATE idempotency_records SET key_hash=? WHERE scope='openai_chat_completions' AND actor_scope_hash=?`, hostileBlob32(5), actorHash)
	hostileMustFail(t, db, `UPDATE idempotency_records SET request_hash=? WHERE scope='openai_chat_completions' AND actor_scope_hash=? AND key_hash=?`, hostileBlob32(6), actorHash, keyHash)
	hostileMustFail(t, db, `UPDATE idempotency_records SET lookup_fingerprint=? WHERE scope='openai_chat_completions' AND actor_scope_hash=? AND key_hash=?`, hostileBlob32(7), actorHash, keyHash)
	hostileMustFail(t, db, `UPDATE idempotency_records SET created_at=1,expires_at=86401 WHERE scope='openai_chat_completions' AND actor_scope_hash=? AND key_hash=?`, actorHash, keyHash)
	hostileMustFail(t, db, `UPDATE idempotency_records SET expires_at=86401 WHERE scope='openai_chat_completions' AND actor_scope_hash=? AND key_hash=?`, actorHash, keyHash)

	// Exactly one accepted -> completed transition is legal; completion data
	// and the state are then immutable, with no completed -> accepted revival.
	hostileMustExec(t, db, `UPDATE idempotency_records SET state='completed',http_status=200,response_body=? WHERE scope='openai_chat_completions' AND actor_scope_hash=? AND key_hash=?`, []byte("ok"), actorHash, keyHash)
	hostileMustFail(t, db, `UPDATE idempotency_records SET state='accepted',http_status=0,response_body=? WHERE scope='openai_chat_completions' AND actor_scope_hash=? AND key_hash=?`, []byte{}, actorHash, keyHash)
	hostileMustFail(t, db, `UPDATE idempotency_records SET http_status=201 WHERE scope='openai_chat_completions' AND actor_scope_hash=? AND key_hash=?`, actorHash, keyHash)
	hostileMustFail(t, db, `UPDATE idempotency_records SET response_body=? WHERE scope='openai_chat_completions' AND actor_scope_hash=? AND key_hash=?`, []byte("tampered"), actorHash, keyHash)

	credentialActor := hostileBlob32(11)
	credentialKey := hostileBlob32(12)
	credentialRequest := hostileBlob32(13)
	credentialLookup := hostileBlob32(14)
	hostileMustExec(t, db, `
INSERT INTO idempotency_records(
 scope,actor_scope_hash,key_hash,request_hash,lookup_fingerprint,state,http_status,
 response_body,created_at,expires_at
) VALUES('credential_report',?,?,?,?, 'accepted',0,?,0,86400)`, credentialActor, credentialKey, credentialRequest, credentialLookup, []byte{})
	hostileMustFail(t, db, `UPDATE idempotency_records SET lookup_fingerprint=? WHERE scope='credential_report' AND actor_scope_hash=? AND key_hash=?`, hostileBlob32(15), credentialActor, credentialKey)
}

func TestGenerationTwoHostileStorageAndAppendOnlyGuards(t *testing.T) {
	db := openGenerationTwoDDLForTest(t)
	uid := hostileInsertUser(t, db, "storage", 0, 0)
	accountID := hostileInsertAccount(t, db, "user", uid, nil, 0, hostileBlob16(0), 0)
	opID := hostileOID("op_")
	hostileInsertOperation(t, db, opID, 1, "admin_user_adjustment", "operation", opID)
	hostileMustExec(t, db, `
INSERT INTO credit_entries(operation_id,line_no,account_id,account_kind_snapshot,delta_sign,delta_mag)
VALUES(?,0,?,'user',1,?)`, opID, accountID, hostileBlob16(1))

	for _, table := range []string{"credit_operations", "credit_entries"} {
		var withoutRowID int
		if err := db.QueryRow(`SELECT wr FROM pragma_table_list WHERE name=?`, table).Scan(&withoutRowID); err != nil {
			t.Fatalf("table-list %s: %v", table, err)
		}
		if withoutRowID != 1 {
			t.Fatalf("%s must be WITHOUT ROWID", table)
		}
	}
	hostileMustFail(t, db, `UPDATE credit_operations SET created_at=1 WHERE id=?`, opID)
	hostileMustFail(t, db, `DELETE FROM credit_operations WHERE id=?`, opID)
	hostileMustFail(t, db, `UPDATE credit_entries SET line_no=1 WHERE operation_id=? AND line_no=0`, opID)
	hostileMustFail(t, db, `DELETE FROM credit_entries WHERE operation_id=? AND line_no=0`, opID)
	var retentionColumns []string
	rows, err := db.Query(`PRAGMA index_info('idx_request_logs_retention')`)
	if err != nil {
		t.Fatalf("request-log retention index lookup: %v", err)
	}
	for rows.Next() {
		var seq, cid int
		var name string
		if err := rows.Scan(&seq, &cid, &name); err != nil {
			rows.Close()
			t.Fatalf("request-log retention index scan: %v", err)
		}
		retentionColumns = append(retentionColumns, name)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatalf("request-log retention index rows: %v", err)
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("request-log retention index close: %v", err)
	}
	if len(retentionColumns) != 2 || retentionColumns[0] != "completed_at" || retentionColumns[1] != "id" {
		t.Fatalf("request-log retention index must be (completed_at,id), got %v", retentionColumns)
	}
	for _, retention := range []struct {
		table   string
		columns []string
	}{
		{table: "game_fishing_batches", columns: []string{"settled_at"}},
		{table: "request_attempts", columns: []string{"completed_at"}},
		{table: "welfare_claims", columns: []string{"created_at"}},
	} {
		if !hostileHasIndexPrefix(t, db, retention.table, retention.columns...) {
			t.Fatalf("%s must have a dedicated retention index beginning with %v", retention.table, retention.columns)
		}
	}

	// Required owner/worker/partial indexes and immutable guards are part of
	// the schema contract, not optional query optimizations.
	for _, indexName := range []string{
		"idx_credit_operations_source",
		"idx_credit_operations_created",
		"idx_credit_entries_account",
		"idx_dispatch_claims_request",
		"idx_dispatch_claims_secret",
		"idx_dispatch_claims_due",
		"idx_report_cases_active_fingerprint",
		"idx_thursday_active_participant",
		"idx_rps_rank_profit",
		"idx_rps_rank_net",
	} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_schema WHERE type='index' AND name=?`, indexName).Scan(&count); err != nil {
			t.Fatalf("required index %s lookup: %v", indexName, err)
		}
		if count != 1 {
			t.Fatalf("required index %s missing", indexName)
		}
	}

	for _, triggerName := range []string{
		"endpoints_identity_immutable",
		"endpoint_key_secrets_identity_immutable",
		"caller_keys_generation_guard",
		"donation_key_membership_consistency_guard",
		"rps_gesture_envelope_guard",
		"legal_hold_consumed_guard",
	} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_schema WHERE type='trigger' AND name=?`, triggerName).Scan(&count); err != nil {
			t.Fatalf("required trigger %s lookup: %v", triggerName, err)
		}
		if count != 1 {
			t.Fatalf("required trigger %s missing", triggerName)
		}
	}
}

func TestGenerationTwoHostileLegalHoldMarkersDeletesAndAudits(t *testing.T) {
	db := openGenerationTwoDDLForTest(t)
	adminID := hostileInsertUser(t, db, "legal-admin", 1, 0)
	userID := hostileInsertUser(t, db, "legal-user", 0, 0)
	// announcement_audits has an INTEGER identity independent of the other
	// aggregate tables; this first row is the legal-hold root exercised below.
	hostileInsertAnnouncementAudit(t, db, adminID, 0)

	maintenanceID := hostileOIDVariant("op_", 'M', 'Q')
	hostileInsertMaintenanceEvent(t, db, maintenanceID, adminID)
	// A disable event closes the open enable in the same transaction and gives
	// both rows the one immutable de-identification/retention schedule.
	disableMaintenanceID := hostileOIDVariant("op_", 'C', 'Q')
	hostileMustExec(t, db, `BEGIN`)
	hostileMustExec(t, db, `
INSERT INTO maintenance_events(
 id,actor_user_id,actor_discord_id,actor_role,action,reason,created_at,
 resolved_at,deidentify_at,retain_until
) VALUES(?,?,NULL,'admin','disable','hostile',100,100,7776100,34560100)`, disableMaintenanceID, adminID)
	hostileMustExec(t, db, `
UPDATE maintenance_events
SET resolved_at=100,deidentify_at=7776100,retain_until=34560100
WHERE id=? AND action='enable'`, maintenanceID)
	hostileMustExec(t, db, `COMMIT`)
	var enableResolved, disableResolved, enableDeidentify, disableDeidentify, enableRetain, disableRetain int64
	if err := db.QueryRow(`SELECT resolved_at,deidentify_at,retain_until FROM maintenance_events WHERE id=?`, maintenanceID).Scan(&enableResolved, &enableDeidentify, &enableRetain); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT resolved_at,deidentify_at,retain_until FROM maintenance_events WHERE id=?`, disableMaintenanceID).Scan(&disableResolved, &disableDeidentify, &disableRetain); err != nil {
		t.Fatal(err)
	}
	if enableResolved != 100 || disableResolved != 100 || enableDeidentify != 7776100 || disableDeidentify != 7776100 || enableRetain != 34560100 || disableRetain != 34560100 {
		t.Fatalf("maintenance close did not atomically close both events: enable=(%d,%d,%d) disable=(%d,%d,%d)", enableResolved, enableDeidentify, enableRetain, disableResolved, disableDeidentify, disableRetain)
	}

	// Account deletion must clear all three maintenance actor identity fields
	// together; FK SET NULL on only actor_user_id would violate the row check.
	maintenanceActor := hostileInsertUser(t, db, "maintenance-actor-delete", 0, 0)
	maintenanceActorEvent := hostileOIDVariant("op_", 'D', 'Q')
	hostileMustExec(t, db, `
INSERT INTO maintenance_events(
 id,actor_user_id,actor_discord_id,actor_role,action,reason,created_at
) VALUES(?,?,?,'steward','enable','hostile',0)`, maintenanceActorEvent, maintenanceActor, "hostile-steward")
	hostileMustExec(t, db, `DELETE FROM users WHERE id=?`, maintenanceActor)
	var clearedActor, clearedDiscord, clearedRole any
	if err := db.QueryRow(`SELECT actor_user_id,actor_discord_id,actor_role FROM maintenance_events WHERE id=?`, maintenanceActorEvent).Scan(&clearedActor, &clearedDiscord, &clearedRole); err != nil {
		t.Fatal(err)
	}
	if clearedActor != nil || clearedDiscord != nil || clearedRole != nil {
		t.Fatalf("maintenance actor identity was not fully deidentified: user=%v discord=%v role=%v", clearedActor, clearedDiscord, clearedRole)
	}

	reportID := hostileOIDVariant("rpc_", 'R', 'Q')
	hostileInsertReportCase(t, db, reportID, hostileBlob32(1), "pending_review", "complete", 0)
	materialID := hostileInsertReportMaterial(t, db, reportID)
	targetID := hostileOIDVariant("rpt_", 'R', 'Q')
	hostileInsertReportTarget(t, db, reportID, targetID)
	decisionID := hostileInsertReportDecision(t, db, reportID, adminID)

	donationID := hostileInsertDonation(t, db, userID)
	donationKeyID := hostileInsertDonationKey(t, db, donationID, nil)
	reviewID := hostileInsertDonationReview(t, db, donationID, adminID)

	// Append-only review/decision rows permit only the FK-driven actor NULL
	// mutation.  Any other direct field rewrite remains forbidden.
	reviewActor := hostileInsertUser(t, db, "review-actor-null", 0, 0)
	reviewActorReviewID := hostileInsertDonationReview(t, db, donationID, reviewActor)
	hostileMustExec(t, db, `UPDATE donation_reviews SET reviewer_user_id=NULL WHERE id=?`, reviewActorReviewID)
	hostileMustFail(t, db, `UPDATE donation_reviews SET note='rewritten' WHERE id=?`, reviewActorReviewID)
	reviewDeleteActor := hostileInsertUser(t, db, "review-actor-delete", 0, 0)
	reviewDeleteID := hostileInsertDonationReview(t, db, donationID, reviewDeleteActor)
	hostileMustExec(t, db, `DELETE FROM users WHERE id=?`, reviewDeleteActor)
	var reviewerAfterDelete any
	if err := db.QueryRow(`SELECT reviewer_user_id FROM donation_reviews WHERE id=?`, reviewDeleteID).Scan(&reviewerAfterDelete); err != nil {
		t.Fatal(err)
	}
	if reviewerAfterDelete != nil {
		t.Fatalf("donation review actor FK did not deidentify: %v", reviewerAfterDelete)
	}

	decisionActor := hostileInsertUser(t, db, "decision-actor-null", 0, 0)
	decisionActorDecisionID := hostileInsertReportDecision(t, db, reportID, decisionActor)
	hostileMustExec(t, db, `UPDATE report_decisions SET actor_user_id=NULL WHERE id=?`, decisionActorDecisionID)
	hostileMustFail(t, db, `UPDATE report_decisions SET reason='rewritten' WHERE id=?`, decisionActorDecisionID)
	decisionDeleteActor := hostileInsertUser(t, db, "decision-actor-delete", 0, 0)
	decisionDeleteID := hostileInsertReportDecision(t, db, reportID, decisionDeleteActor)
	hostileMustExec(t, db, `DELETE FROM users WHERE id=?`, decisionDeleteActor)
	var decisionActorAfterDelete any
	if err := db.QueryRow(`SELECT actor_user_id FROM report_decisions WHERE id=?`, decisionDeleteID).Scan(&decisionActorAfterDelete); err != nil {
		t.Fatal(err)
	}
	if decisionActorAfterDelete != nil {
		t.Fatalf("report decision actor FK did not deidentify: %v", decisionActorAfterDelete)
	}

	requestID := hostileOIDVariant("req_", 'L', 'Q')
	hostileInsertLogicalRequest(t, db, requestID, userID, "openai_chat_completions", 1)
	requestLogID := hostileInsertRequestLog(t, db, requestID, userID, "openai_chat_completions")
	attemptID := hostileOIDVariant("clm_", 'A', 'Q')
	hostileInsertAttempt(t, db, attemptID, requestLogID, 1)

	// Announcement actor deletion is allowed through the FK path, while an
	// early manual NULL update is not.  A due 90-day de-identification update
	// is allowed and uses the same narrow mutation.
	earlyAnnouncementAt := hostileTimeMax - 7776000 - 100
	earlyAnnouncementID := hostileInsertAnnouncementAudit(t, db, adminID, earlyAnnouncementAt)
	hostileMustFail(t, db, `UPDATE announcement_audits SET actor_user_id=NULL WHERE id=?`, earlyAnnouncementID)
	futureAnnouncementActor := hostileInsertUser(t, db, "announcement-actor-delete", 0, 0)
	futureAnnouncementID := hostileInsertAnnouncementAudit(t, db, futureAnnouncementActor, earlyAnnouncementAt)
	hostileMustExec(t, db, `DELETE FROM users WHERE id=?`, futureAnnouncementActor)
	var announcementActorAfterDelete any
	if err := db.QueryRow(`SELECT actor_user_id FROM announcement_audits WHERE id=?`, futureAnnouncementID).Scan(&announcementActorAfterDelete); err != nil {
		t.Fatal(err)
	}
	if announcementActorAfterDelete != nil {
		t.Fatalf("announcement audit actor FK did not deidentify: %v", announcementActorAfterDelete)
	}
	dueAnnouncementID := hostileInsertAnnouncementAudit(t, db, adminID, 0)
	hostileMustExec(t, db, `UPDATE announcement_audits SET actor_user_id=NULL WHERE id=?`, dueAnnouncementID)
	var dueAnnouncementActor any
	if err := db.QueryRow(`SELECT actor_user_id FROM announcement_audits WHERE id=?`, dueAnnouncementID).Scan(&dueAnnouncementActor); err != nil {
		t.Fatal(err)
	}
	if dueAnnouncementActor != nil {
		t.Fatalf("due announcement audit actor was not deidentified: %v", dueAnnouncementActor)
	}

	// The five legal-hold roots cannot be inserted already marked.  A marker
	// is an irreversible fact produced only by the matching active-hold CAS.
	hostileMustFail(t, db, `
INSERT INTO maintenance_events(
 id,actor_user_id,actor_discord_id,actor_role,action,reason,created_at,legal_hold_consumed
) VALUES(?,?,NULL,'admin','enable','hostile',0,1)`, hostileOIDVariant("op_", 'I', 'Q'), adminID)
	hostileMustFail(t, db, `
INSERT INTO report_cases(
 id,fingerprint,connector_type,canonical_base_url,status,progress_state,
 material_version,target_version,deadline,cursor_text,material_count,target_count,
 distinct_owner_count,processed_target_count,deleted_target_count,released_target_count,
 retry_attempt_count,created_at,terminal_at,legal_hold_consumed
) VALUES(?,?,'openai-compatible','https://upstream.example/v1','pending_review','complete',
 1,1,1000,NULL,0,0,0,0,0,0,0,0,NULL,1)`, hostileOIDVariant("rpc_", 'I', 'Q'), hostileBlob32(11))
	hostileMustFail(t, db, `
INSERT INTO announcement_audits(
 announcement_id_text,actor_user_id,action,from_revision,to_revision,reason,
 created_at,actor_deidentify_at,legal_hold_consumed
) VALUES(?,?,'create',0,1,'hostile',0,7776000,1)`, hostileOIDVariant("ann_", 'I', 'Q'), adminID)
	hostileMustFail(t, db, `
INSERT INTO donations(
 user_id,status,revision,description,review_note,created_at,updated_at,legal_hold_consumed
) VALUES(?,'pending',1,'','',0,0,1)`, userID)
	markerRequestID := hostileOIDVariant("req_", 'I', 'Q')
	hostileInsertLogicalRequest(t, db, markerRequestID, userID, "openai_chat_completions", 1)
	hostileMustFail(t, db, `
INSERT INTO request_logs(
 logical_request_id,user_id,model,upstream_model_id,route_kind,endpoint_base_url,
 started_at,legal_hold_consumed
) VALUES(?,?,'model','upstream','openai_chat_completions','https://upstream.example/v1',0,1)`, markerRequestID, userID)

	hostileMustFail(t, db, `
INSERT INTO legal_holds(id,object_kind,object_ref,state,revision,basis,created_by_user_id,created_at,expires_at)
VALUES(?, 'report_case','not-a-report-id','active',1,'hostile',?,100,200)`, hostileOIDVariant("lgh_", 'X', 'Q'), adminID)
	hostileMustFail(t, db, `
INSERT INTO legal_holds(id,object_kind,object_ref,state,revision,basis,created_by_user_id,created_at,expires_at)
VALUES(?, 'maintenance_event',?,'active',1,'hostile',?,100,200)`, hostileOIDVariant("lgh_", 'Y', 'Q'), maintenanceID, userID)
	hostileMustFail(t, db, `
INSERT INTO legal_holds(id,object_kind,object_ref,state,revision,basis,created_by_user_id,created_at,expires_at)
VALUES(?, 'maintenance_event',?,'active',1,'hostile',?,100,31536101)`, hostileOIDVariant("lgh_", 'Z', 'Q'), maintenanceID, adminID)
	hostileMustFail(t, db, `
INSERT INTO legal_holds(id,object_kind,object_ref,state,revision,basis,created_by_user_id,created_at,expires_at)
VALUES(?, 'maintenance_event',?,'released',1,'hostile',?,100,200)`, hostileOIDVariant("lgh_", 'V', 'Q'), maintenanceID, adminID)

	rootCases := []struct {
		name, kind, ref, holdID, markerUpdate, deleteRoot string
		children                                          []string
	}{
		{
			"maintenance-event", "maintenance_event", maintenanceID, hostileOIDVariant("lgh_", 'M', 'Q'),
			`UPDATE maintenance_events SET legal_hold_consumed=1 WHERE id=?`,
			`DELETE FROM maintenance_events WHERE id=?`, nil,
		},
		{
			"report-case", "report_case", reportID, hostileOIDVariant("lgh_", 'R', 'Q'),
			`UPDATE report_cases SET legal_hold_consumed=1 WHERE id=?`,
			`DELETE FROM report_cases WHERE id=?`, []string{
				`DELETE FROM report_materials WHERE id=?`,
				`DELETE FROM report_targets WHERE id=?`,
				`DELETE FROM report_decisions WHERE id=?`,
			},
		},
		{
			"announcement-audit", "announcement_audit", "1", hostileOIDVariant("lgh_", 'A', 'Q'),
			`UPDATE announcement_audits SET legal_hold_consumed=1 WHERE id=1`,
			`DELETE FROM announcement_audits WHERE id=1`, nil,
		},
		{
			"donation", "donation", fmt.Sprintf("%d", donationID), hostileOIDVariant("lgh_", 'D', 'Q'),
			`UPDATE donations SET legal_hold_consumed=1 WHERE id=?`,
			`DELETE FROM donations WHERE id=?`, []string{
				`DELETE FROM donation_keys WHERE id=?`,
				`DELETE FROM donation_reviews WHERE id=?`,
			},
		},
		{
			"request-log", "request_log", fmt.Sprintf("%d", requestLogID), hostileOIDVariant("lgh_", 'Q', 'Q'),
			`UPDATE request_logs SET legal_hold_consumed=1 WHERE id=?`,
			`DELETE FROM request_logs WHERE id=?`, []string{
				`DELETE FROM request_attempts WHERE claim_id=?`,
			},
		},
	}

	for _, tc := range rootCases {
		t.Run(tc.name, func(t *testing.T) {
			hostileInsertLegalHold(t, db, tc.holdID, tc.kind, tc.ref, adminID)
			switch tc.kind {
			case "maintenance_event":
				hostileMustExec(t, db, tc.markerUpdate, tc.ref)
			case "report_case":
				hostileMustExec(t, db, tc.markerUpdate, tc.ref)
			case "donation":
				hostileMustExec(t, db, tc.markerUpdate, donationID)
			case "request_log":
				hostileMustExec(t, db, tc.markerUpdate, requestLogID)
			case "announcement_audit":
				hostileMustExec(t, db, tc.markerUpdate)
			}
			// A legal-hold marker is monotonic and can only be set in the same
			// transaction as an active matching hold.
			switch tc.kind {
			case "maintenance_event":
				hostileMustFail(t, db, `UPDATE maintenance_events SET legal_hold_consumed=0 WHERE id=?`, tc.ref)
			case "report_case":
				hostileMustFail(t, db, `UPDATE report_cases SET legal_hold_consumed=0 WHERE id=?`, tc.ref)
			case "donation":
				hostileMustFail(t, db, `UPDATE donations SET legal_hold_consumed=0 WHERE id=?`, donationID)
			case "request_log":
				hostileMustFail(t, db, `UPDATE request_logs SET legal_hold_consumed=0 WHERE id=?`, requestLogID)
			case "announcement_audit":
				hostileMustFail(t, db, `UPDATE announcement_audits SET legal_hold_consumed=0 WHERE id=1`)
			}
			for _, childDelete := range tc.children {
				switch childDelete {
				case `DELETE FROM report_materials WHERE id=?`:
					hostileMustFail(t, db, childDelete, materialID)
				case `DELETE FROM report_targets WHERE id=?`:
					hostileMustFail(t, db, childDelete, targetID)
				case `DELETE FROM report_decisions WHERE id=?`:
					hostileMustFail(t, db, childDelete, decisionID)
				case `DELETE FROM donation_keys WHERE id=?`:
					hostileMustFail(t, db, childDelete, donationKeyID)
				case `DELETE FROM donation_reviews WHERE id=?`:
					hostileMustFail(t, db, childDelete, reviewID)
				case `DELETE FROM request_attempts WHERE claim_id=?`:
					hostileMustFail(t, db, childDelete, attemptID)
				}
			}
			switch tc.kind {
			case "maintenance_event", "report_case":
				hostileMustFail(t, db, tc.deleteRoot, tc.ref)
			case "donation":
				hostileMustFail(t, db, tc.deleteRoot, donationID)
			case "request_log":
				hostileMustFail(t, db, tc.deleteRoot, requestLogID)
			case "announcement_audit":
				hostileMustFail(t, db, tc.deleteRoot)
			}
		})
	}

	// No root may carry a marker without an active matching hold.
	unheldMaintenanceID := hostileOIDVariant("op_", 'U', 'Q')
	hostileInsertMaintenanceEvent(t, db, unheldMaintenanceID, adminID)
	hostileMustFail(t, db, `UPDATE maintenance_events SET legal_hold_consumed=1 WHERE id=?`, unheldMaintenanceID)
	hostileMustFail(t, db, `DELETE FROM legal_holds WHERE id=?`, rootCases[0].holdID)

	// Audit rows are append-only except for one narrow retention update after
	// the hold ends.  Arbitrary edits and second retention changes are hostile.
	auditHoldID := rootCases[1].holdID
	hostileMustFail(t, db, `UPDATE legal_holds SET object_kind='donation' WHERE id=?`, auditHoldID)
	hostileMustFail(t, db, `UPDATE legal_holds SET object_ref=? WHERE id=?`, hostileOIDVariant("rpc_", 'Z', 'Q'), auditHoldID)
	hostileMustFail(t, db, `UPDATE legal_holds SET expires_at=201 WHERE id=?`, auditHoldID)
	hostileMustFail(t, db, `UPDATE legal_holds SET basis='rewritten' WHERE id=?`, auditHoldID)
	hostileMustFail(t, db, `UPDATE legal_holds SET created_by_user_id=? WHERE id=?`, userID, auditHoldID)
	hostileMustFail(t, db, `UPDATE legal_holds SET revision=2 WHERE id=?`, auditHoldID)
	retainUntil := int64(150 + 34560000)
	hostileMustFail(t, db, `
UPDATE legal_holds
SET state='released',revision=1,ended_by_user_id=?,ended_at=150,end_reason='released',retain_until=?
WHERE id=?`, adminID, retainUntil, auditHoldID)
	hostileMustFail(t, db, `
 UPDATE legal_holds
 SET state='released',revision=3,ended_by_user_id=?,ended_at=150,end_reason='released',retain_until=?
 WHERE id=?`, adminID, retainUntil, auditHoldID)
	createAuditID := hostileNextPK64(t, db, "legal_hold_audits")
	hostileMustExec(t, db, `
	INSERT INTO legal_hold_audits(id,hold_id_text,actor_user_id,action,created_at)
	VALUES(?,?,?,'create',100)`, createAuditID, auditHoldID, adminID)
	hostileMustFail(t, db, `UPDATE legal_hold_audits SET reason='changed' WHERE hold_id_text=? AND action='create'`, auditHoldID)
	hostileMustExec(t, db, `
UPDATE legal_holds SET state='released',revision=2,ended_by_user_id=?,ended_at=150,end_reason='released',retain_until=? WHERE id=?`, adminID, retainUntil, auditHoldID)
	hostileMustFail(t, db, `
UPDATE legal_holds
SET state='active',revision=3,ended_by_user_id=NULL,ended_at=NULL,end_reason=NULL,retain_until=NULL
WHERE id=?`, auditHoldID)
	hostileMustFail(t, db, `
UPDATE legal_holds
SET state='expired',revision=3,ended_by_user_id=NULL,ended_at=200,end_reason='expired',retain_until=34560200
WHERE id=?`, auditHoldID)
	hostileMustExec(t, db, `UPDATE legal_hold_audits SET retain_until=? WHERE hold_id_text=? AND action='create'`, retainUntil, auditHoldID)
	hostileMustFail(t, db, `UPDATE legal_hold_audits SET retain_until=? WHERE hold_id_text=? AND action='create'`, retainUntil+1, auditHoldID)
	releaseAuditID := hostileNextPK64(t, db, "legal_hold_audits")
	hostileMustExec(t, db, `
	INSERT INTO legal_hold_audits(id,hold_id_text,actor_user_id,action,reason,created_at,retain_until)
	VALUES(?,?,?,'release','released',150,?)`, releaseAuditID, auditHoldID, adminID, retainUntil)
	hostileMustFail(t, db, `
INSERT INTO legal_hold_audits(hold_id_text,actor_user_id,action,reason,created_at,retain_until)
VALUES(?,?,'release','released',150,?)`, auditHoldID, adminID, retainUntil)

	readHoldID := rootCases[0].holdID
	hostileMustExec(t, db, `
INSERT INTO legal_hold_read_audits(hold_id_text,admin_user_id,read_kind,first_read_at,last_read_at,read_count)
VALUES(?,?, 'metadata',100,100,1)`, readHoldID, adminID)
	hostileMustExec(t, db, `UPDATE legal_hold_read_audits SET last_read_at=101,read_count=2 WHERE hold_id_text=? AND admin_user_id=? AND read_kind='metadata'`, readHoldID, adminID)
	hostileMustFail(t, db, `UPDATE legal_hold_read_audits SET last_read_at=102,read_count=2 WHERE hold_id_text=? AND admin_user_id=? AND read_kind='metadata'`, readHoldID, adminID)
	hostileMustFail(t, db, `UPDATE legal_hold_read_audits SET last_read_at=103,read_count=4 WHERE hold_id_text=? AND admin_user_id=? AND read_kind='metadata'`, readHoldID, adminID)
	hostileMustFail(t, db, `UPDATE legal_hold_read_audits SET last_read_at=102,read_count=1 WHERE hold_id_text=? AND admin_user_id=? AND read_kind='metadata'`, readHoldID, adminID)
	hostileMustFail(t, db, `UPDATE legal_hold_read_audits SET first_read_at=101,last_read_at=102,read_count=3 WHERE hold_id_text=? AND admin_user_id=? AND read_kind='metadata'`, readHoldID, adminID)
	hostileMustFail(t, db, `UPDATE legal_hold_read_audits SET last_read_at=100,read_count=3 WHERE hold_id_text=? AND admin_user_id=? AND read_kind='metadata'`, readHoldID, adminID)
	hostileMustFail(t, db, `UPDATE legal_hold_read_audits SET last_read_at=99 WHERE hold_id_text=? AND admin_user_id=? AND read_kind='metadata'`, readHoldID, adminID)
	hostileMustFail(t, db, `UPDATE legal_hold_read_audits SET retain_until=1 WHERE hold_id_text=? AND admin_user_id=? AND read_kind='metadata'`, readHoldID, adminID)
	readRetainUntil := int64(150 + 34560000)
	hostileMustExec(t, db, `
UPDATE legal_holds SET state='released',revision=2,ended_by_user_id=?,ended_at=150,end_reason='released',retain_until=?
WHERE id=?`, adminID, readRetainUntil, readHoldID)
	hostileMustExec(t, db, `UPDATE legal_hold_read_audits SET retain_until=? WHERE hold_id_text=? AND admin_user_id=? AND read_kind='metadata'`, readRetainUntil, readHoldID, adminID)
	hostileMustFail(t, db, `UPDATE legal_hold_read_audits SET retain_until=? WHERE hold_id_text=? AND admin_user_id=? AND read_kind='metadata'`, readRetainUntil+1, readHoldID, adminID)
	hostileMustFail(t, db, `UPDATE legal_hold_read_audits SET retain_until=?,read_count=3 WHERE hold_id_text=? AND admin_user_id=? AND read_kind='metadata'`, readRetainUntil, readHoldID, adminID)
	var firstRead, lastRead, readCount int64
	var readRetain sql.NullInt64
	if err := db.QueryRow(`SELECT first_read_at,last_read_at,read_count,retain_until FROM legal_hold_read_audits WHERE hold_id_text=? AND admin_user_id=? AND read_kind='metadata'`, readHoldID, adminID).Scan(&firstRead, &lastRead, &readCount, &readRetain); err != nil {
		t.Fatal(err)
	}
	if firstRead != 100 || lastRead != 101 || readCount != 2 || !readRetain.Valid || readRetain.Int64 != readRetainUntil {
		t.Fatalf("read audit fields changed during retention update: first=%d last=%d count=%d retain=%v", firstRead, lastRead, readCount, readRetain)
	}

	// Cleanup may remove ended hold metadata, but the root's irreversible
	// marker survives and blocks a second hold for the same object.
	hostileMustExec(t, db, `DELETE FROM legal_hold_audits WHERE hold_id_text=?`, auditHoldID)
	hostileMustExec(t, db, `DELETE FROM legal_hold_read_audits WHERE hold_id_text=?`, auditHoldID)
	hostileMustExec(t, db, `DELETE FROM legal_holds WHERE id=?`, auditHoldID)
	var reportMarker int
	if err := db.QueryRow(`SELECT legal_hold_consumed FROM report_cases WHERE id=?`, reportID).Scan(&reportMarker); err != nil {
		t.Fatal(err)
	}
	if reportMarker != 1 {
		t.Fatalf("ended hold cleanup cleared report root marker: %d", reportMarker)
	}
	hostileMustFail(t, db, `
INSERT INTO legal_holds(id,object_kind,object_ref,state,revision,basis,created_by_user_id,created_at,expires_at)
VALUES(?,'report_case',?,'active',1,'reopened',?,100,200)`, hostileOIDVariant("lgh_", 'S', 'Q'), reportID, adminID)
}

func TestGenerationTwoHostilePublicSiteConfigWriteBoundary(t *testing.T) {
	store := openTestStore(t, filepath.Join(privateDBDir(t), "config-boundary.sqlite"))
	db := store.DB()

	// The public Store entry point is intentionally narrower than the typed
	// catalog validator.  Maintenance, announcement identity, and every
	// activity/game key are owned by their respective state/configuration
	// mutation paths; accepting them here would let a generic caller bypass
	// those gates.  A rejected write must not bump the site revision or touch
	// the row's timestamp/value.
	var beforeRevision int64
	if err := db.QueryRow(`SELECT revision FROM config_revisions WHERE domain='site'`).Scan(&beforeRevision); err != nil {
		t.Fatal(err)
	}
	var beforeEpoch string
	if err := db.QueryRow(`SELECT value FROM site_config WHERE key='announcement_epoch'`).Scan(&beforeEpoch); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"maintenance_mode", "announcement_epoch"} {
		var beforeValue string
		var beforeUpdatedAt int64
		if err := db.QueryRow(`SELECT value,updated_at FROM site_config WHERE key=?`, key).Scan(&beforeValue, &beforeUpdatedAt); err != nil {
			t.Fatalf("read protected config %s: %v", key, err)
		}
		if err := store.SetSiteConfigValue(key, beforeValue); err == nil {
			t.Fatalf("public config setter accepted protected key %q", key)
		}
		var afterValue string
		var afterUpdatedAt, afterRevision int64
		if err := db.QueryRow(`SELECT value,updated_at FROM site_config WHERE key=?`, key).Scan(&afterValue, &afterUpdatedAt); err != nil {
			t.Fatalf("read protected config %s after rejection: %v", key, err)
		}
		if err := db.QueryRow(`SELECT revision FROM config_revisions WHERE domain='site'`).Scan(&afterRevision); err != nil {
			t.Fatal(err)
		}
		if afterValue != beforeValue || afterUpdatedAt != beforeUpdatedAt || afterRevision != beforeRevision {
			t.Fatalf("protected config %s changed after rejected write: value=%q/%q updated_at=%d/%d revision=%d/%d", key, beforeValue, afterValue, beforeUpdatedAt, afterUpdatedAt, beforeRevision, afterRevision)
		}
	}
	if beforeEpoch == "" {
		t.Fatal("fresh announcement epoch is unexpectedly empty")
	}

	var protectedKeys []string
	rows, err := db.Query(`SELECT key FROM site_config WHERE key LIKE 'activity_%' OR key LIKE 'activities_%' OR key LIKE 'game_%' OR key='games_enabled' ORDER BY key`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		protectedKeys = append(protectedKeys, key)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if len(protectedKeys) == 0 {
		t.Fatal("fresh seed has no activity/game config keys to protect")
	}
	for _, key := range protectedKeys {
		var beforeValue string
		var beforeUpdatedAt int64
		if err := db.QueryRow(`SELECT value,updated_at FROM site_config WHERE key=?`, key).Scan(&beforeValue, &beforeUpdatedAt); err != nil {
			t.Fatalf("read protected config %s: %v", key, err)
		}
		if err := store.SetSiteConfigValue(key, beforeValue); err == nil {
			t.Fatalf("public config setter accepted protected key %q", key)
		}
		var afterValue string
		var afterUpdatedAt, afterRevision int64
		if err := db.QueryRow(`SELECT value,updated_at FROM site_config WHERE key=?`, key).Scan(&afterValue, &afterUpdatedAt); err != nil {
			t.Fatalf("read protected config %s after rejection: %v", key, err)
		}
		if err := db.QueryRow(`SELECT revision FROM config_revisions WHERE domain='site'`).Scan(&afterRevision); err != nil {
			t.Fatal(err)
		}
		if afterValue != beforeValue || afterUpdatedAt != beforeUpdatedAt || afterRevision != beforeRevision {
			t.Fatalf("protected config %s changed after rejected write: value=%q/%q updated_at=%d/%d revision=%d/%d", key, beforeValue, afterValue, beforeUpdatedAt, afterUpdatedAt, beforeRevision, afterRevision)
		}
	}

	// A regular catalog key remains writable through this entry point and
	// advances exactly the site revision once.
	var ordinaryBeforeRevision int64
	if err := db.QueryRow(`SELECT revision FROM config_revisions WHERE domain='site'`).Scan(&ordinaryBeforeRevision); err != nil {
		t.Fatal(err)
	}
	if err := store.SetSiteConfigValue("site_name", "hostile-config-write"); err != nil {
		t.Fatalf("ordinary site config write rejected: %v", err)
	}
	var gotName string
	var ordinaryAfterRevision int64
	if err := db.QueryRow(`SELECT value FROM site_config WHERE key='site_name'`).Scan(&gotName); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT revision FROM config_revisions WHERE domain='site'`).Scan(&ordinaryAfterRevision); err != nil {
		t.Fatal(err)
	}
	if gotName != "hostile-config-write" || ordinaryAfterRevision != ordinaryBeforeRevision+1 {
		t.Fatalf("ordinary site config write mismatch: value=%q revision=%d want %q/%d", gotName, ordinaryAfterRevision, "hostile-config-write", ordinaryBeforeRevision+1)
	}
}

func TestGenerationTwoHostileManifestDynamicRPSAccountIdentity(t *testing.T) {
	// Fresh bootstrap has two pool accounts, both canonical zero SM128 values.
	// The stricter fresh validator also has to tolerate a valid historical RPS
	// platform account after its queue/session root has been cleaned up.
	{
		store := openTestStore(t, filepath.Join(privateDBDir(t), "manifest-rps-fresh.sqlite"))
		db := store.DB()
		if err := validateGenerationTwoFreshSeedManifest(context.Background(), db); err != nil {
			t.Fatalf("fresh seed manifest rejected canonical pools: %v", err)
		}
		rows, err := db.Query(`
SELECT p.pool_type,a.kind,a.code,a.balance_sign,hex(a.balance_mag)
FROM shared_pools p JOIN credit_accounts a ON a.id=p.account_id
ORDER BY p.pool_type`)
		if err != nil {
			t.Fatal(err)
		}
		poolCount := 0
		for rows.Next() {
			var poolType, kind, code, balanceHex string
			var sign int
			if err := rows.Scan(&poolType, &kind, &code, &sign, &balanceHex); err != nil {
				t.Fatal(err)
			}
			poolCount++
			if kind != "pool" || sign != 0 || balanceHex != strings.Repeat("0", 32) ||
				code == "" || !strings.HasPrefix(code, "pool:") {
				t.Fatalf("fresh pool account is not canonical zero: type=%q kind=%q code=%q sign=%d mag=%q", poolType, kind, code, sign, balanceHex)
			}
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		if poolCount != 2 {
			t.Fatalf("fresh seed expected two pool accounts, got %d", poolCount)
		}
		validQueueID := hostileOIDVariant("rpsq_", 'H', 'Q')
		validQueueCode := "rps-queue:" + validQueueID
		hostileMustExec(t, db, `
INSERT INTO credit_accounts(id,kind,user_id,code,balance_sign,balance_mag,created_at,updated_at)
VALUES(?,?,?,?,0,?,0,0)`, hostileNextPK64(t, db, "credit_accounts"), "platform", nil, validQueueCode, hostileBlob16(0))
		if err := validateGenerationTwoSeedManifest(context.Background(), db); err != nil {
			t.Fatalf("valid historical dynamic RPS account without live root rejected: %v", err)
		}
	}
	for _, tc := range []struct {
		name  string
		query string
		args  []any
	}{
		{
			name:  "welfare-sign",
			query: `UPDATE credit_accounts SET balance_sign=1 WHERE id=(SELECT account_id FROM shared_pools WHERE pool_type='welfare')`,
		},
		{
			name:  "thursday-magnitude",
			query: `UPDATE credit_accounts SET balance_mag=? WHERE id=(SELECT account_id FROM shared_pools WHERE pool_type='thursday')`,
			args:  []any{hostileBlob16(1)},
		},
	} {
		tc := tc
		t.Run("fresh-corruption-"+tc.name, func(t *testing.T) {
			store := openTestStore(t, filepath.Join(privateDBDir(t), "manifest-rps-corrupt-"+tc.name+".sqlite"))
			db := store.DB()
			hostileMustExec(t, db, `PRAGMA ignore_check_constraints=ON`)
			hostileMustExec(t, db, tc.query, tc.args...)
			if err := validateGenerationTwoFreshSeedManifest(context.Background(), db); err == nil {
				t.Fatalf("fresh manifest accepted corrupted %s pool account", tc.name)
			}
		})
	}

	// Each malformed identity gets a fresh database.  A malformed row must be
	// the only corruption under test; retaining an earlier bad row would make
	// later iterations unable to prove their own rejection.
	malformed := []string{
		"rps-queue:rpsq_A", // short body
		"rps-queue:rpsq_" + strings.Repeat("A", 21) + "B", // bad tail
		"rps-queue:rpsq_" + strings.Repeat("A", 21) + "!", // bad charset
		"rps-queue:rpsq_" + strings.Repeat("A", 22) + "Q", // overlong body
		"rps-session:rps_A", // short body
		"rps-session:rps_" + strings.Repeat("A", 21) + "B", // bad tail
		"rps-session:rps_" + strings.Repeat("A", 21) + "!", // bad charset
		"rps-session:rps_" + strings.Repeat("A", 22) + "Q", // overlong body
		"rps-queue:rpsx_" + strings.Repeat("A", 21) + "Q",  // wrong prefix
	}
	for i, code := range malformed {
		t.Run(fmt.Sprintf("malformed-%02d", i), func(t *testing.T) {
			store := openTestStore(t, filepath.Join(privateDBDir(t), fmt.Sprintf("manifest-rps-malformed-%02d.sqlite", i)))
			db := store.DB()
			hostileMustExec(t, db, `PRAGMA ignore_check_constraints=ON`)
			id := hostileNextPK64(t, db, "credit_accounts")
			hostileMustExec(t, db, `
		INSERT INTO credit_accounts(id,kind,user_id,code,balance_sign,balance_mag,created_at,updated_at)
		VALUES(?, 'platform',NULL,?,0,?,0,0)`, id, code, hostileBlob16(0))
			if err := validateGenerationTwoSeedManifest(context.Background(), db); err == nil {
				t.Fatalf("manifest accepted malformed dynamic RPS code %q", code)
			}
		})
	}
}
