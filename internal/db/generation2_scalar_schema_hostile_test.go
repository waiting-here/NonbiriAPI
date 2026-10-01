package db

import (
	"database/sql"
	"strings"
	"testing"
)

func TestGenerationTwoHostileScalarBoundaries(t *testing.T) {
	db := openGenerationTwoConstraintFixture(t)
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
INSERT INTO dispatch_claims(id,logical_request_id,attempt_seq,purpose,claim_now,state,terminal_at,donor_reward_state,streak_disposition,failure_origin)
VALUES(?,?,?,'self',0,'released',0,'not_applicable','neutral','platform')`, claim, req, tc.seq)
		})
	}
	hostileMustFail(t, db, `
INSERT INTO dispatch_claims(id,logical_request_id,attempt_seq,purpose,claim_now,state,terminal_at,donor_reward_state,streak_disposition,failure_origin)
VALUES(?,?,101,'self',0,'released',0,'not_applicable','neutral','platform')`, hostileBadOID("clm_"), req)

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
	db := openGenerationTwoConstraintFixture(t)
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

func TestGenerationTwoHostileOIDPrefixesAndNotNull(t *testing.T) {
	db := openGenerationTwoConstraintFixture(t)
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
INSERT INTO dispatch_claims(id,logical_request_id,attempt_seq,purpose,claim_now,state,terminal_at,donor_reward_state,streak_disposition,failure_origin)
VALUES(NULL,?,1,'self',0,'released',0,'not_applicable','neutral','platform')`, req)
	hostileMustFail(t, db, `
INSERT INTO dispatch_claims(id,logical_request_id,attempt_seq,purpose,claim_now,state,terminal_at,donor_reward_state,streak_disposition,failure_origin)
VALUES(?, ?,1,'self',0,'released',0,'not_applicable','neutral','platform')`, hostileBadOID("clm_"), req)
	hostileMustFail(t, db, `
INSERT INTO dispatch_claims(id,logical_request_id,attempt_seq,purpose,claim_now,state,terminal_at,donor_reward_state,streak_disposition,failure_origin)
VALUES(?, ?,1,'self',0,'released',0,'not_applicable','neutral','platform')`, claim, hostileBadOID("req_"))

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

func TestGenerationTwoHostilePK64PositiveIDs(t *testing.T) {
	db := openGenerationTwoConstraintFixture(t)
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

func TestGenerationTwoHostileUnicodeScalarTextBounds(t *testing.T) {
	db := openGenerationTwoConstraintFixture(t)
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
	db := openGenerationTwoConstraintFixture(t)
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
	hostileGameAwardEntry(t, db, welfareUser, welfareOperationID)
	welfareIDValue := hostileNextPK64(t, db, "welfare_claims")
	welfareID := hostileMustLastID(t, hostileMustExec(t, db, `
	INSERT INTO welfare_claims(id,user_id,site_day,operation_id,threshold_milli,cap_milli,pool_before_milli,award_milli,created_at,asset_type)
	VALUES(?,?,'1970-01-01',?,0,2,2,1,0,'game')`, welfareIDValue, welfareUser, welfareOperationID))
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
			protected := strings.HasSuffix(compactTableSQL, "strict") || strings.HasSuffix(compactTableSQL, "strict,withoutrowid") || column.pk > 0 ||
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
	db := openGenerationTwoConstraintFixture(t)
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
