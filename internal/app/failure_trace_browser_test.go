package app

import (
	"strconv"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/adminalerts"
	"github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/diagnostic"
)

// A retained request with gaps left by candidates skipped before dispatch.
// The alert and its link back from the attempt use the regular event writer.
func seedBrowserFailureTrace(t *testing.T, f *imageBrowserFixture, at int64) map[string]string {
	t.Helper()
	id := func(prefix string) string {
		t.Helper()
		value, err := db.GenerateOpaqueID(prefix)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	requestID := id("req_")
	tx, err := f.store.DB().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := tx.Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	const model = "[公益]browser/quota-diagnostics"
	exec(`INSERT INTO logical_requests(id,user_id,route_kind,model_snapshot,state,attempt_limit,caller_result_class,caller_status,caller_error_code,accounting_state,settlement_destination,ledger_rows_remaining,created_at,terminal_at)
VALUES(?,?,'charity_chat_completions',?,'terminal',27,'failed',400,'upstream','released','user',zeroblob(16),?,?)`, requestID, f.users[0].ID, model, at, at+14)
	exec(`INSERT INTO request_logs(logical_request_id,user_id,route_kind,model,started_at,completed_at,caller_result_class,caller_status,caller_error_code,status_code,attempt_count,error_source,error_code,duration_ms)
VALUES(?,?,'charity_chat_completions',?,?,?,'failed',400,'upstream',400,14,'upstream','upstream',14000)`, requestID, f.users[0].ID, model, at, at+14)
	var root int64
	if err := tx.QueryRow("SELECT id FROM request_logs WHERE logical_request_id=?", requestID).Scan(&root); err != nil {
		t.Fatal(err)
	}
	service, err := contract.ParseNativeServices([]contract.NativeService{contract.NativeServiceGeminiGenerateContent})
	if err != nil {
		t.Fatal(err)
	}
	var crossing string
	for i := range 14 {
		seq, key, claimID := 2*i+1, 10000+i, id("clm_")
		status, cause := 401, "google_access_token_type_unsupported"
		switch i {
		case 2, 4, 6, 8, 10:
			status, cause = 429, "google_daily_requests"
		case 13:
			status, cause = 400, "google_api_key_invalid"
		}
		if seq == 23 {
			crossing = claimID
		}
		exec(`INSERT INTO request_attempts(claim_id,request_log_id,attempt_seq,endpoint_key_id_snapshot,connector_type,canonical_base_url,upstream_model_id,result_kind,upstream_status,started_at,completed_at)
VALUES(?,?,?,?,'google-gemini','https://generativelanguage.googleapis.com','fixture-model','response',?,?,?)`, claimID, root, seq, key, status, at+int64(i), at+int64(i)+1)
		exec(`INSERT INTO request_attempt_service_facts(claim_id,credential_id_snapshot,credential_version,native_service_bit,configured_type,wire_type)
VALUES(?,?,1,?,'google-gemini','google-gemini')`, claimID, key, service.Mask())
		facts := diagnostic.Failure{Category: "upstream", Phase: "upstream_response", Code: "upstream_error", ProviderReason: cause, UpstreamStatus: status, HeadersReceived: true}
		exec(`INSERT INTO request_attempt_diagnostics(claim_id,request_log_id,attempt_seq,category,phase,details,completed_at)
VALUES(?,?,?,?,?,?,?)`, claimID, root, seq, facts.Category, facts.Phase, diagnostic.Encode(&facts), at+int64(i)+1)
	}
	const ref = "donation-use:10011:health:10011:generation:1:fold:10"
	event := adminalerts.DonationFailure{
		DonationID: 10011, KeyID: 10011, EndpointKeyID: 10011, UseID: 10011, Ref: ref, Generation: db.U128{15: 1}, OccurredAt: at + 12,
		Crossing: adminalerts.FailureCrossing{ClaimID: crossing, Fold: db.U128{15: 10}, Before: db.U128{15: 9}, After: db.U128{15: 10}, Threshold: db.U128{15: 10}},
	}
	if err := adminalerts.RecordDonationFailureTx(t.Context(), tx, event); err != nil {
		t.Fatal(err)
	}
	var alertID int64
	if err := tx.QueryRow("SELECT id FROM admin_alerts WHERE kind='donation_failure_disabled' AND ref=?", ref).Scan(&alertID); err != nil {
		t.Fatal(err)
	}
	// Independent retries use different donations. Their streaks and waits
	// remain visible even though the caller eventually received a success.
	healthID, start := id("req_"), at-150
	exec(`INSERT INTO logical_requests(id,user_id,route_kind,model_snapshot,state,attempt_limit,caller_result_class,caller_status,accounting_state,settlement_destination,ledger_rows_remaining,created_at,terminal_at)
VALUES(?,?,'charity_chat_completions',?,'terminal',3,'success',200,'committed','user',zeroblob(16),?,?)`, healthID, f.users[0].ID, model, start, start+3)
	exec(`INSERT INTO request_logs(logical_request_id,user_id,route_kind,model,started_at,completed_at,caller_result_class,caller_status,status_code,attempt_count,duration_ms)
VALUES(?,?,'charity_chat_completions',?,?,?,'success',200,200,3,3000)`, healthID, f.users[0].ID, model, start, start+3)
	var healthLog int64
	if err := tx.QueryRow("SELECT id FROM request_logs WHERE logical_request_id=?", healthID).Scan(&healthLog); err != nil {
		t.Fatal(err)
	}
	overloadUntil := start + 1 + 60
	for i, sample := range []struct {
		status                               int
		effect, before, after, reason, scope string
		until                                int64
	}{
		{503, "unchanged", "5", "5", "google_model_overloaded", "service", overloadUntil},
		{429, "unchanged", "2", "2", "google_daily_requests", "model", start + 86400},
		{200, "reset", "4", "0", "", "", 0},
	} {
		claimID := id("clm_")
		key := 15000 + i
		exec(`INSERT INTO request_attempts(claim_id,request_log_id,attempt_seq,endpoint_key_id_snapshot,connector_type,canonical_base_url,upstream_model_id,result_kind,upstream_status,started_at,completed_at,streak_effect,streak_before,streak_after)
VALUES(?,?,?,?,'google-gemini','https://generativelanguage.googleapis.com','fixture-flash','response',?,?,?,?,?,?)`,
			claimID, healthLog, i+1, key, sample.status, start+int64(i), start+int64(i)+1, sample.effect, sample.before, sample.after)
		exec(`INSERT INTO request_attempt_service_facts(claim_id,credential_id_snapshot,credential_version,native_service_bit,configured_type,wire_type)
VALUES(?,?,1,?,'google-gemini','google-gemini')`, claimID, key, service.Mask())
		if sample.reason != "" {
			exec(`INSERT INTO request_attempt_backoffs(claim_id,request_log_id,reason,scope,retry_at) VALUES(?,?,?,?,?)`, claimID, healthLog, sample.reason, sample.scope, sample.until)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return map[string]string{"request_id": requestID, "alert_id": strconv.FormatInt(alertID, 10), "attempt_seq": "23",
		"health_request_id": healthID, "overload_retry_at": strconv.FormatInt(overloadUntil, 10)}
}
