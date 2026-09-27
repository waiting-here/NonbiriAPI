package adminalerts

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
)

func TestAlertDetailClosedKindsAndLegacyReferences(t *testing.T) {
	environment := newAlertTestEnvironment(t)
	cases := []struct {
		kind     Kind
		ref      string
		wantKind string
	}{
		{KindFetchFailed, "https://host.invalid/private", ""},
		{KindForwardError, "req_" + strings.Repeat("A", 22), "request_log"},
		{KindRegistrationRejected, "user:42", ""},
		{KindMaintenanceEnabled, "op_" + strings.Repeat("A", 22), "maintenance_event"},
		{KindDonationFailureDisabled, "donation-key:41:generation:2:fold:3", "donation_key"},
		{KindIssueProjectionIncomplete, "", ""},
		{KindReportRetryExhausted, "rpc_" + strings.Repeat("A", 22), "report_case"},
		{KindFishingRetryExhausted, "fb_" + strings.Repeat("A", 22), "fishing_batch"},
		{KindRPSTerminalRetrying, "rps_" + strings.Repeat("A", 22), "rps_session"},
		{KindWorkerCheckpointFailed, "lifecycle_recovery_v1", "worker_checkpoint"},
		{KindInvariantViolation, "javascript:alert(1)", ""},
		{KindAccountDeleted, "", "deleted_account"},
	}
	for _, tc := range cases {
		t.Run(string(tc.kind), func(t *testing.T) {
			id := environment.seedAlert(t, string(tc.kind), "Safe event", tc.ref, nil, alertTestNow-1, false)
			if tc.kind == KindAccountDeleted {
				_, err := environment.store.DB().Exec(`INSERT INTO admin_account_deletions(alert_id,snapshot_json) VALUES(?,?)`, id,
					`{"user_id":"42","discord_id":"","general_balance":"0","game_balance":"0","donation_credit":"0","sketch_paper":"0","sketch_brush":"0"}`)
				if err != nil {
					t.Fatal(err)
				}
			}
			detail, err := environment.repository.GetDetail(context.Background(), environment.adminID, id)
			if err != nil {
				t.Fatal(err)
			}
			if detail.Alert.Kind != tc.kind || detail.ContextVersion != 0 || detail.OccurredFacts == nil || detail.Targets == nil || detail.CurrentState == nil {
				t.Fatalf("detail=%+v", detail)
			}
			if tc.wantKind == "" && len(detail.Targets) != 0 {
				t.Fatalf("fabricated target: %+v", detail.Targets)
			}
			if tc.wantKind != "" && (len(detail.Targets) == 0 || detail.Targets[0].Kind != tc.wantKind) {
				t.Fatalf("missing exact target: %+v", detail.Targets)
			}
		})
	}
}

func TestAlertDetailStrictReferenceAndCurrentWorkerState(t *testing.T) {
	environment := newAlertTestEnvironment(t)
	if _, err := environment.store.DB().Exec(`INSERT INTO worker_checkpoints(worker_key,cursor_text,generation,attempt_count,next_attempt_at,last_error_class,updated_at,last_success_at) VALUES('lifecycle_retention_v1','',1,2,?,?,?,?)`, alertTestNow+30, "db_busy", alertTestNow, alertTestNow-60); err != nil {
		t.Fatal(err)
	}
	id := environment.seedAlert(t, string(KindWorkerCheckpointFailed), "Account lifecycle worker failed: db_busy", "lifecycle_retention_v1", nil, alertTestNow-10, false)
	body := `{"targets":[{"kind":"worker_checkpoint","id":"lifecycle_retention_v1"}],"occurred_facts":[{"key":"worker_domain","value":"request_logs"},{"key":"worker_stage","value":"retention"}]}`
	if _, err := environment.store.DB().Exec(`UPDATE admin_alerts SET context_version=1,context_json=? WHERE id=?`, body, id); err != nil {
		t.Fatal(err)
	}
	detail, err := environment.repository.GetDetail(context.Background(), environment.adminID, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Targets) != 1 || !detail.Targets[0].Available || detail.Targets[0].Status != "retrying" || len(detail.CurrentState) != 5 || detail.CurrentState[4].Value != "1699999940" {
		t.Fatalf("worker detail=%+v", detail)
	}
	handler := environment.handler(t, http.MethodGet, routeAlertDetail)
	response := invokeAlertHandler(t, handler, http.MethodGet, routeAlertDetail, nil, alertIDString(id), environment.adminID, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var wire AlertDetail
	if err := jsonDecode(response, &wire); err != nil || wire.Alert.ID != alertIDString(id) {
		t.Fatalf("detail decode=%+v err=%v", wire, err)
	}
	for _, target := range []string{routeAlertDetail + "?return_to=https://evil.invalid", routeAlertDetail + "?a=1"} {
		requireErrorCode(t, invokeAlertHandler(t, handler, http.MethodGet, target, nil, alertIDString(id), environment.adminID, nil), http.StatusBadRequest, httperr.CodeInvalidRequest)
	}
	for _, invalid := range []string{"0", "01", "-1", "9223372036854775808"} {
		requireErrorCode(t, invokeAlertHandler(t, handler, http.MethodGet, routeAlertDetail, nil, invalid, environment.adminID, nil), http.StatusBadRequest, httperr.CodeInvalidRequest)
	}
	environment.authorizer.forced = authz.ErrForbidden
	requireErrorCode(t, invokeAlertHandler(t, handler, http.MethodGet, routeAlertDetail, nil, alertIDString(id), environment.adminID, nil), http.StatusForbidden, httperr.CodeForbidden)
}

func TestAlertDetailRejectsMalformedStructuredContextWithoutRenderingIt(t *testing.T) {
	environment := newAlertTestEnvironment(t)
	id := environment.seedAlert(t, string(KindForwardError), "safe", "https://untrusted.invalid", nil, alertTestNow, false)
	for _, contextBody := range []string{
		`{"targets":[{"kind":"request_log","id":"https://untrusted.invalid"}],"occurred_facts":[]}`,
		`{"targets":[{"kind":"user","id":"1"},{"kind":"user","id":"1"}],"occurred_facts":[]}`,
		`{"targets":[],"occurred_facts":[{"key":"stage","value":"bad\u0000value"}]}`,
	} {
		if _, err := environment.store.DB().Exec(`UPDATE admin_alerts SET context_version=1,context_json=? WHERE id=?`, contextBody, id); err != nil {
			t.Fatal(err)
		}
		response := invokeAlertHandler(t, environment.handler(t, http.MethodGet, routeAlertDetail), http.MethodGet, routeAlertDetail, nil, alertIDString(id), environment.adminID, nil)
		requireErrorCode(t, response, http.StatusInternalServerError, httperr.CodeInternal)
		if strings.Contains(response.Body.String(), "untrusted.invalid") {
			t.Fatalf("context escaped: %s", response.Body.String())
		}
	}
}

func TestAlertDetailDonationUsesVerifiedDistinctEndpointKey(t *testing.T) {
	environment := newAlertTestEnvironment(t)
	database := environment.store.DB()
	must := func(query string, args ...any) int64 {
		t.Helper()
		result, err := database.Exec(query, args...)
		if err != nil {
			t.Fatal(err)
		}
		id, err := result.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	endpointID := must(`INSERT INTO endpoints(user_id,connector_type,base_url,note,enabled,revision,created_at,updated_at) VALUES(?,'openai-compatible','https://example.invalid/v1','',1,1,?,?)`, environment.adminID, alertTestNow, alertTestNow)
	var endpointKeys [2]int64
	for index := range endpointKeys {
		contextID := make([]byte, 16)
		contextID[15] = byte(index + 1)
		secretID := must(`INSERT INTO endpoint_key_secrets(context_id,canonical_base_url,connector_type,encrypted_secret,created_at) VALUES(?,'https://example.invalid/v1','openai-compatible','sealed-fixture',?)`, contextID, alertTestNow)
		fingerprint := make([]byte, 32)
		fingerprint[31] = byte(index + 1)
		endpointKeys[index] = must(`INSERT INTO endpoint_keys(endpoint_id,secret_ref_id,secret_fingerprint,revision,created_at,updated_at) VALUES(?,?,?,1,?,?)`, endpointID, secretID, fingerprint, alertTestNow, alertTestNow)
	}
	donationID := must(`INSERT INTO donations(user_id,status,revision,description,created_at,updated_at) VALUES(?,'approved',1,'',?,?)`, environment.adminID, alertTestNow, alertTestNow)
	zero := db.EncodeU128(db.U128{})
	one := make([]byte, 16)
	one[15] = 1
	keyID := must(`INSERT INTO donation_keys(donation_id,endpoint_key_id,source_endpoint_key_id,price_used_mag,price_reserved_mag,calls_used,calls_reserved,tokens_used,tokens_reserved,failure_streak,streak_generation,next_claim_seq,next_fold_seq,report_fingerprint,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, donationID, endpointKeys[1], endpointKeys[1], zero, zero, zero, zero, zero, zero, zero, one, one, one, make([]byte, 32), alertTestNow, alertTestNow)
	if keyID == endpointKeys[1] {
		t.Fatal("fixture must distinguish donation key and endpoint key IDs")
	}
	alertID := environment.seedAlert(t, string(KindDonationFailureDisabled), "Donation source disabled", "donation-key:"+strconv.FormatInt(keyID, 10)+":generation:1:fold:1", nil, alertTestNow, false)
	// Even a stored, syntactically valid target may no longer describe the
	// live donation key. The unrelated endpoint key cannot become a link.
	contextBody := `{"targets":[{"kind":"donation_key","id":"` + strconv.FormatInt(keyID, 10) + `"},{"kind":"endpoint_key","id":"` + strconv.FormatInt(endpointKeys[0], 10) + `"}],"occurred_facts":[]}`
	if _, err := database.Exec(`UPDATE admin_alerts SET context_version=1,context_json=? WHERE id=?`, contextBody, alertID); err != nil {
		t.Fatal(err)
	}
	detail, err := environment.repository.GetDetail(context.Background(), environment.adminID, alertID)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]AlertTarget{}
	for _, target := range detail.Targets {
		seen[target.Kind+":"+target.ID] = target
	}
	wrong := seen["endpoint_key:"+strconv.FormatInt(endpointKeys[0], 10)]
	correct := seen["endpoint_key:"+strconv.FormatInt(endpointKeys[1], 10)]
	if wrong.Available || wrong.UnavailableReason != "association_changed" || !correct.Available || !seen["donation:"+strconv.FormatInt(donationID, 10)].Available {
		t.Fatalf("cross-key navigation: %+v", detail.Targets)
	}
	if detail.RelatedLogs == nil || detail.RelatedLogs.Available {
		t.Fatalf("missing logs were reported available: %+v", detail.RelatedLogs)
	}
	requestID, err := db.GenerateOpaqueID("req_")
	if err != nil {
		t.Fatal(err)
	}
	must(`INSERT INTO logical_requests(id,user_id,route_kind,model_snapshot,state,attempt_limit,caller_result_class,caller_status,accounting_state,settlement_destination,ledger_rows_remaining,created_at,terminal_at) VALUES(?,?,'openai_chat_completions','model','terminal',2,'success',200,'none','user',zeroblob(16),?,?)`, requestID, environment.adminID, alertTestNow, alertTestNow)
	logID := must(`INSERT INTO request_logs(logical_request_id,user_id,endpoint_key_id,started_at,completed_at,caller_result_class,caller_status,status_code,attempt_count) VALUES(?,?,?,?,?,'success',200,200,2)`, requestID, environment.adminID, endpointKeys[0], alertTestNow, alertTestNow)
	claimID, err := db.GenerateOpaqueID("clm_")
	if err != nil {
		t.Fatal(err)
	}
	must(`INSERT INTO request_attempts(claim_id,request_log_id,attempt_seq,endpoint_key_id_snapshot,connector_type,canonical_base_url,upstream_model_id,result_kind,upstream_status,started_at,completed_at) VALUES(?,?,1,?,'openai-compatible','https://example.invalid/v1','model','response',500,?,?)`, claimID, logID, endpointKeys[1], alertTestNow, alertTestNow)
	secondClaim, err := db.GenerateOpaqueID("clm_")
	if err != nil {
		t.Fatal(err)
	}
	must(`INSERT INTO request_attempts(claim_id,request_log_id,attempt_seq,endpoint_key_id_snapshot,connector_type,canonical_base_url,upstream_model_id,result_kind,upstream_status,started_at,completed_at) VALUES(?,?,2,?,'openai-compatible','https://example.invalid/v1','model','response',200,?,?)`, secondClaim, logID, endpointKeys[0], alertTestNow, alertTestNow)
	detail, err = environment.repository.GetDetail(context.Background(), environment.adminID, alertID)
	if err != nil || detail.RelatedLogs == nil || !detail.RelatedLogs.Available || detail.RelatedLogs.EndpointKeyID != strconv.FormatInt(endpointKeys[1], 10) {
		t.Fatalf("failed attempt was hidden by the final endpoint key: %+v, %v", detail.RelatedLogs, err)
	}
	must(`DELETE FROM request_attempts WHERE claim_id=?`, claimID)
	detail, err = environment.repository.GetDetail(context.Background(), environment.adminID, alertID)
	if err != nil || detail.RelatedLogs == nil || detail.RelatedLogs.Available {
		t.Fatalf("retired attempt remained available: %+v, %v", detail.RelatedLogs, err)
	}
	// The source ID remains as history after key revocation; no live endpoint
	// target may be inferred from it or from another key with the same number.
	if _, err := database.Exec(`UPDATE donation_keys SET endpoint_key_id=NULL,enabled=0,ended_at=?,ended_reason='withdrawn',report_match_until=?,updated_at=? WHERE id=?`, alertTestNow, alertTestNow+7776000, alertTestNow, keyID); err != nil {
		t.Fatal(err)
	}
	detail, err = environment.repository.GetDetail(context.Background(), environment.adminID, alertID)
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range detail.Targets {
		if target.Kind == "endpoint_key" && target.Available {
			t.Fatalf("revoked key linked live endpoint: %+v", detail.Targets)
		}
	}
	if detail.RelatedLogs != nil {
		t.Fatalf("revoked key retained log navigation: %+v", detail.RelatedLogs)
	}
}
