package db

import (
	"database/sql"
	"fmt"
	"testing"
)

func TestGenerationTwoHostileRequestClaimAttemptMatrices(t *testing.T) {
	db := openGenerationTwoConstraintFixture(t)
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
INSERT INTO dispatch_claims(id,logical_request_id,attempt_seq,purpose,claim_now,state,dispatched_at,terminal_at,donor_reward_state,streak_disposition,failure_origin)
VALUES(?,?,3,'self',0,'committed',1,2,'not_applicable','neutral','platform')`, hostileOIDVariant("clm_", 'c', 'Q'), claimRequest)
	hostileMustExec(t, db, `
INSERT INTO dispatch_claims(id,logical_request_id,attempt_seq,purpose,claim_now,state,terminal_at,donor_reward_state,streak_disposition,failure_origin)
VALUES(?,?,4,'self',0,'released',2,'not_applicable','neutral','platform')`, hostileOIDVariant("clm_", 'd', 'Q'), claimRequest)
	hostileMustFail(t, db, `
INSERT INTO dispatch_claims(id,logical_request_id,attempt_seq,purpose,claim_now,state,terminal_at,donor_reward_state)
		VALUES(?,?,5,'self',0,'claimed',NULL,'not_applicable')`, hostileOIDVariant("clm_", 'e', 'Q'), claimRequest)
	hostileMustFail(t, db, `
INSERT INTO dispatch_claims(id,logical_request_id,attempt_seq,purpose,secret_ref_id,claim_now,state,terminal_at,donor_reward_state,streak_disposition,failure_origin)
VALUES(?,?,6,'self',?,0,'committed',2,'not_applicable','neutral','platform')`, hostileOIDVariant("clm_", 'f', 'Q'), claimRequest, secretID)
	hostileMustFail(t, db, `
INSERT INTO dispatch_claims(id,logical_request_id,attempt_seq,purpose,claim_now,state,terminal_at,donor_reward_state,streak_disposition,failure_origin)
VALUES(?,?,101,'self',0,'released',2,'not_applicable','neutral','platform')`, hostileOIDVariant("clm_", 'g', 'Q'), claimRequest)
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
INSERT INTO dispatch_claims(id,logical_request_id,attempt_seq,purpose,claim_now,state,dispatched_at,terminal_at,receiver_user_id,donor_reward_actual_milli,donor_reward_state,streak_disposition,failure_origin)
VALUES(?,?,2,'charity',0,'committed',1,2,?,?, 'posted','neutral','platform')`, hostileOIDVariant("clm_", 'h', 'Q'), charityReq, uid, 1)
	hostileMustExec(t, db, `
INSERT INTO dispatch_claims(id,logical_request_id,attempt_seq,purpose,claim_now,state,dispatched_at,terminal_at,donor_reward_actual_milli,donor_reward_state,streak_disposition,failure_origin)
VALUES(?,?,3,'charity',0,'committed',1,2,0,'zero','neutral','platform')`, hostileOIDVariant("clm_", 'i', 'Q'), charityReq)
	hostileMustExec(t, db, `
INSERT INTO dispatch_claims(id,logical_request_id,attempt_seq,purpose,claim_now,state,dispatched_at,terminal_at,donor_reward_actual_milli,donor_reward_state,streak_disposition,failure_origin)
VALUES(?,?,4,'charity',0,'committed',1,2,1,'receiver_deleted','neutral','platform')`, hostileOIDVariant("clm_", 'j', 'Q'), charityReq)
	hostileMustExec(t, db, `
INSERT INTO dispatch_claims(id,logical_request_id,attempt_seq,purpose,claim_now,state,terminal_at,donor_reward_state,streak_disposition,failure_origin)
VALUES(?,?,5,'charity',0,'released',2,'not_due','neutral','platform')`, hostileOIDVariant("clm_", 'k', 'Q'), charityReq)
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
	db := openGenerationTwoConstraintFixture(t)
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
INSERT INTO dispatch_claims(id,logical_request_id,attempt_seq,purpose,claim_now,state,dispatched_at,terminal_at,donor_reward_state,streak_disposition,failure_origin)
VALUES(?,?,1,'self',0,'committed',1,2,'not_applicable','neutral','platform')`, hostileOIDVariant("clm_", 'c', 'Q'), claimStateRequestID)
	hostileMustExec(t, db, `
INSERT INTO dispatch_claims(id,logical_request_id,attempt_seq,purpose,claim_now,state,terminal_at,donor_reward_state,streak_disposition,failure_origin)
VALUES(?,?,2,'self',0,'released',1,'not_applicable','neutral','platform')`, hostileOIDVariant("clm_", 'd', 'Q'), claimStateRequestID)
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
