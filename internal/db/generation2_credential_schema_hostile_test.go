package db

import (
	"fmt"
	"strings"
	"testing"
)

func TestGenerationTwoHostileSecretReportDonationMembership(t *testing.T) {
	db := openGenerationTwoConstraintFixture(t)
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
	// rail references it. Once membership is removed, the donation key must be
	// atomically terminalized while detaching the physical key; only then can
	// NULL -> timestamp be applied to the orphan marker.
	hostileMustFail(t, db, `UPDATE endpoint_key_secrets SET orphaned_at=1 WHERE id=?`, secretID)
	hostileMustFail(t, db, `DELETE FROM endpoint_key_secrets WHERE id=?`, secretID)
	hostileMustExec(t, db, `DELETE FROM donation_key_memberships WHERE endpoint_key_id=?`, keyID)
	hostileMustExec(t, db, `
UPDATE donation_keys
SET endpoint_key_id=NULL,enabled=0,ended_reason='terminated',ended_at=1,
 report_match_until=7776001,updated_at=1
WHERE id=?`, donationKeyID)
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
	usageEndpointID := hostileInsertEndpoint(t, db, uid, "https://usage-boundary.example/v1")
	usageSecretID := hostileInsertSecret(t, db, "https://usage-boundary.example/v1", 2)
	usageEndpointKeyID := hostileInsertEndpointKey(t, db, usageEndpointID, usageSecretID)
	secondDonation := hostileInsertDonation(t, db, uid)
	secondKey := hostileInsertDonationKey(t, db, secondDonation, usageEndpointKeyID)
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
	db := openGenerationTwoConstraintFixture(t)

	insert := func(t *testing.T, id string, fingerprint []byte, status, progress string, decisionAt, terminalAt any) {
		t.Helper()
		hostileMustExec(t, db, `
INSERT INTO report_cases(
 id,fingerprint,connector_type,canonical_base_url,status,progress_state,
 material_version,target_version,deadline,cursor_source,cursor_id,material_count,target_count,
 distinct_owner_count,processed_target_count,deleted_target_count,released_target_count,
 decision_reason,decision_actor_user_id,decision_at,retry_attempt_count,next_retry_at,
 last_error_class,created_at,terminal_at
) VALUES(?,?,'openai-compatible','https://upstream.example/v1',?,?,1,1,1000,NULL,NULL,
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
 material_version,target_version,deadline,cursor_source,cursor_id,material_count,target_count,
 distinct_owner_count,processed_target_count,deleted_target_count,released_target_count,
 decision_reason,decision_actor_user_id,decision_at,retry_attempt_count,next_retry_at,
 last_error_class,created_at,terminal_at
) VALUES(?,?,'openai-compatible','https://upstream.example/v1',?,?,1,1,1000,NULL,NULL,
 0,0,0,0,0,0,NULL,NULL,?,0,NULL,NULL,0,?)`
			hostileMustFail(t, db, query, hostileOIDVariant("rpc_", byte('a'+i), 'Q'), hostileBlob32(byte(i+140)), tc.status, tc.progress, tc.decisionAt, tc.terminalAt)
		})
	}
}
