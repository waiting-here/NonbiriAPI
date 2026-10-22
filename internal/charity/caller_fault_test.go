package charity

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/claim"
	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/smartbackoff"
	"github.com/waiting-here/NonbiriAPI/internal/upstreamerror"
)

func TestCallerParameterFaultPreservesStreakAndFailureAccounting(t *testing.T) {
	for _, tc := range []struct{ kind, code, param string }{
		{"invalid_request_error", "invalid_value", "temperature"},
		{"invalid_request_error", "invalid_value", "aspect_ratio"},
		{"BadRequest", "InvalidParameter", "reasoning_effort"},
	} {
		t.Run(tc.code+"/"+tc.param, func(t *testing.T) {
			testCallerParameterFaultAccounting(t, tc.kind, tc.code, tc.param)
		})
	}
}

func testCallerParameterFaultAccounting(t *testing.T, kind, code, param string) {
	t.Helper()
	e := newCharityTestEnv(t)
	rail := e.billingRail(t)
	ctx := t.Context()
	if _, err := e.store.DB().Exec("UPDATE donation_keys SET failure_disable_threshold=3 WHERE id=?", e.donationKey); err != nil {
		t.Fatal(err)
	}
	request := e.billingRequest(t, rail, e.requestModel, 2400, 1)
	handle := e.billingDispatch(t, rail, request.ID, 1)
	e.seedServiceStreak(t, handle, 2)
	errorContext := upstreamerror.Context{CharityCall: true, ContainsSecret: func([]byte) bool { return false }, UnchangedCallerParameter: func(name string) bool { return name == param }}
	payload := fmt.Sprintf(`{"error":{"type":%q,"code":%q,"param":%q,"message":"Invalid value."}}`, kind, code, param)
	detail := errorContext.ReadResponse(ctx, &http.Response{StatusCode: 400, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(payload))})
	outcome := claim.AttemptOutcome{Kind: claim.ResultResponse, UpstreamStatus: 400, UpstreamCode: detail.Code(), Diagnostic: "upstream returned HTTP 400", FailureOrigin: contract.OriginUpstreamResponse, StreakDisposition: contract.StreakNeutral, CallerFault: detail.CallerFault()}
	for _, tc := range []struct {
		name   string
		change func(*claim.AttemptOutcome)
	}{
		{"no evidence", func(o *claim.AttemptOutcome) { o.CallerFault = upstreamerror.CallerFault{} }},
		{"wrong code", func(o *claim.AttemptOutcome) { o.UpstreamCode = "insufficient_quota" }},
		{"wrong status", func(o *claim.AttemptOutcome) { o.UpstreamStatus = 401 }},
		{"accepted status", func(o *claim.AttemptOutcome) { o.UpstreamStatus = 200 }},
		{"wrong origin", func(o *claim.AttemptOutcome) { o.FailureOrigin = contract.OriginUpstreamProtocol }},
		{"response started", func(o *claim.AttemptOutcome) { o.ResponseStarted = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			invalid := outcome
			tc.change(&invalid)
			if _, err := rail.CompleteAttempt(ctx, handle, invalid); !errors.Is(err, claim.ErrInvalidInput) {
				t.Fatalf("invalid neutral accepted: %v", err)
			}
		})
	}
	for range 2 {
		attempt, err := rail.CompleteAttempt(ctx, handle, outcome)
		if err != nil || attempt.StreakDisposition != contract.StreakNeutral || attempt.UpstreamStatus != 400 || attempt.UpstreamCode != code || attempt.Diagnostic != outcome.Diagnostic {
			t.Fatal(attempt, err)
		}
	}
	var receipts, retryHints, classifiedBackoffs int
	if err := e.store.DB().QueryRow(`SELECT
(SELECT COUNT(*) FROM dispatch_caller_parameter_errors WHERE claim_id=? AND http_status=400 AND error_code=?),
(SELECT COUNT(*) FROM dispatch_retry_after_observations WHERE claim_id=?),
(SELECT COUNT(*) FROM dispatch_smart_backoff_facts WHERE claim_id=? AND period IS NOT NULL)`, handle.ClaimID(), code, handle.ClaimID(), handle.ClaimID()).Scan(&receipts, &retryHints, &classifiedBackoffs); err != nil || receipts != 1 || retryHints != 0 || classifiedBackoffs != 0 {
		t.Fatal("caller proof must not invent retry observations", receipts, retryHints, classifiedBackoffs, err)
	}
	var streak, calls []byte
	var disabled bool
	if err := e.store.DB().QueryRow("SELECT h.failure_streak,h.failure_disabled,dk.calls_used FROM donation_service_health h JOIN donation_uses u ON u.id=h.use_id JOIN donation_keys dk ON dk.id=u.donation_key_id WHERE h.id=?", e.serviceHealthID(t, handle)).Scan(&streak, &disabled, &calls); err != nil {
		t.Fatal(err)
	}
	assertU128(t, streak, 2, "caller fault preserves previous failures")
	assertU128(t, calls, 0, "caller fault consumes no accepted call")
	if disabled {
		t.Fatal("caller fault disabled key")
	}
	var success, price, reward int
	if err := e.store.DB().QueryRow("SELECT protocol_success,price_actual_milli,reward_actual_milli FROM donation_usage_reservations WHERE claim_id=?", handle.ClaimID()).Scan(&success, &price, &reward); err != nil || success != 0 || price != 0 || reward != 0 {
		t.Fatal(success, price, reward, err)
	}
	charge, err := e.service.CalculateRequestCharge(ctx, request.ID, claim.AccountingCommit)
	if err != nil || charge != 0 {
		t.Fatal("caller fault charged", charge, err)
	}
	e.billingBalance(t, e.donorID, 10000)
	// The next unclassified 400 still advances the same streak to its threshold.
	next := e.billingRequest(t, rail, e.requestModel, 2400, 1)
	nextHandle := e.billingDispatch(t, rail, next.ID, 1)
	unknown := claim.AttemptOutcome{Kind: claim.ResultResponse, UpstreamStatus: 400, FailureOrigin: contract.OriginUpstreamResponse, StreakDisposition: contract.StreakUpstreamFailure}
	if _, err := rail.CompleteAttempt(ctx, nextHandle, unknown); err != nil {
		t.Fatal(err)
	}
	if err := e.store.DB().QueryRow("SELECT failure_streak,failure_disabled FROM donation_service_health WHERE id=?", e.serviceHealthID(t, handle)).Scan(&streak, &disabled); err != nil {
		t.Fatal(err)
	}
	assertU128(t, streak, 3, "unknown failure remains counted")
	if !disabled {
		t.Fatal("unknown failure did not disable key")
	}
}

func TestCallerParameterReceiptConstraints(t *testing.T) {
	e := newCharityTestEnv(t)
	rail := e.billingRail(t)
	request := e.billingRequest(t, rail, e.requestModel, 2400, 1)
	handle := e.billingDispatch(t, rail, request.ID, 1)
	tx, err := e.store.DB().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for _, tc := range []struct {
		status int
		code   string
	}{
		{200, "invalid_value"}, {401, "invalid_value"}, {429, "invalid_value"},
		{500, "invalid_value"}, {400, "invalid_request_error"}, {422, "insufficient_quota"},
		{422, "InvalidParameter"}, {400, "InvalidParameter.Unknown"},
	} {
		if _, err := tx.Exec(`INSERT INTO dispatch_caller_parameter_errors VALUES(?,?,?)`, handle.ClaimID(), tc.status, tc.code); err == nil {
			t.Fatal("invalid caller receipt accepted", tc)
		}
	}
	if _, err := tx.Exec(`INSERT INTO dispatch_caller_parameter_errors VALUES(?,400,'invalid_value')`, "missing"); err == nil {
		t.Fatal("receipt without dispatched charity claim accepted")
	}
	if _, err := tx.Exec(`INSERT INTO dispatch_caller_parameter_errors VALUES(?,400,'invalid_value')`, handle.ClaimID()); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`UPDATE dispatch_caller_parameter_errors SET http_status=422 WHERE claim_id=?`, handle.ClaimID()); err == nil || !strings.Contains(err.Error(), "caller parameter error is immutable") {
		t.Fatal("caller receipt mutation guard", err)
	}
	const terminalUsage = `UPDATE donation_usage_reservations SET state='committed',price_actual_milli=0,reward_actual_milli=0,calls_actual=0,tokens_actual=0,protocol_success=0,usage_unknown=1,finalized_at=created_at,streak_disposition='neutral',failure_origin=? WHERE claim_id=?`
	for _, origin := range []string{"upstream_protocol", "network", "timeout"} {
		if _, err := tx.Exec(terminalUsage, origin, handle.ClaimID()); err == nil || !strings.Contains(err.Error(), "invalid terminal outcome") {
			t.Fatal("caller receipt unrelated failure origin guard", origin, err)
		}
	}
	if _, err := tx.Exec(terminalUsage, "upstream_response", handle.ClaimID()); err != nil {
		t.Fatal("caller receipt did not admit usage terminalization", err)
	}
	// The claim update must also see the actual attempt's matching status/code.
	if _, err := tx.Exec(`UPDATE dispatch_claims SET state='committed',secret_ref_id=NULL,terminal_at=dispatched_at,donor_reward_state='zero',donor_reward_actual_milli=0,streak_disposition='neutral',failure_origin='upstream_response' WHERE id=?`, handle.ClaimID()); err == nil || !strings.Contains(err.Error(), "invalid terminal outcome") {
		t.Fatal("claim caller receipt projection guard", err)
	}
	if _, err := tx.Exec(`DELETE FROM dispatch_claims WHERE id=?`, handle.ClaimID()); err == nil || !strings.Contains(err.Error(), "legacy cursor has not passed service health origin") {
		t.Fatal("claim removed its service marker before folding", err)
	}
	var generationBlob []byte
	if err := tx.QueryRow(`SELECT streak_generation FROM donation_usage_reservations WHERE claim_id=?`, handle.ClaimID()).Scan(&generationBlob); err != nil {
		t.Fatal(err)
	}
	generation, err := decodeU128(generationBlob)
	if err != nil {
		t.Fatal(err)
	}
	if err := foldStreak(t.Context(), tx, e.donationKey, generation, charityTestNow); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`DELETE FROM dispatch_claims WHERE id=?`, handle.ClaimID()); err != nil {
		t.Fatal(err)
	}
	var receipts int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM dispatch_caller_parameter_errors WHERE claim_id=?`, handle.ClaimID()).Scan(&receipts); err != nil || receipts != 0 {
		t.Fatal("caller receipt did not follow claim deletion", receipts, err)
	}
}

func TestCallerParameterReceiptRollsBackWithCompletion(t *testing.T) {
	e := newCharityTestEnv(t)
	rail := e.billingRail(t)
	request := e.billingRequest(t, rail, e.requestModel, 2400, 1)
	handle := e.billingDispatch(t, rail, request.ID, 1)
	if _, err := e.store.DB().Exec(`CREATE TRIGGER reject_caller_terminal BEFORE UPDATE ON dispatch_claims WHEN NEW.state='committed' BEGIN SELECT RAISE(ABORT,'injected terminal failure'); END;`); err != nil {
		t.Fatal(err)
	}
	errorContext := upstreamerror.Context{CharityCall: true, ContainsSecret: func([]byte) bool { return false }, UnchangedCallerParameter: func(string) bool { return true }}
	detail := errorContext.ReadResponse(t.Context(), &http.Response{StatusCode: 400, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"error":{"type":"invalid_request_error","code":"invalid_value","param":"temperature","message":"Invalid value."}}`))})
	outcome := claim.AttemptOutcome{Kind: claim.ResultResponse, UpstreamStatus: 400, UpstreamCode: detail.Code(), StreakDisposition: contract.StreakNeutral, FailureOrigin: contract.OriginUpstreamResponse, CallerFault: detail.CallerFault()}
	if _, err := rail.CompleteAttempt(t.Context(), handle, outcome); err == nil || !strings.Contains(err.Error(), "injected terminal failure") {
		t.Fatal("completion did not reach injected failure", err)
	}
	var receipts, attempts int
	var usageState, claimState string
	if err := e.store.DB().QueryRow(`SELECT
(SELECT COUNT(*) FROM dispatch_caller_parameter_errors WHERE claim_id=?),
(SELECT COUNT(*) FROM request_attempts WHERE claim_id=?),
(SELECT state FROM donation_usage_reservations WHERE claim_id=?),
(SELECT state FROM dispatch_claims WHERE id=?)`, handle.ClaimID(), handle.ClaimID(), handle.ClaimID(), handle.ClaimID()).Scan(&receipts, &attempts, &usageState, &claimState); err != nil || receipts != 0 || attempts != 0 || usageState != "reserved" || claimState != "dispatched" {
		t.Fatal("completion partially persisted", receipts, attempts, usageState, claimState, err)
	}
	if _, err := e.store.DB().Exec(`DROP TRIGGER reject_caller_terminal`); err != nil {
		t.Fatal(err)
	}
	if _, err := rail.CompleteAttempt(t.Context(), handle, outcome); err != nil {
		t.Fatal("completion could not retry after rollback", err)
	}
}

func TestCallerParameterEvidenceCannotExemptPersonalAttempt(t *testing.T) {
	e := newCharityTestEnv(t)
	rail := e.billingRail(t)
	ctx := t.Context()
	request, err := rail.Accept(ctx, claim.AcceptInput{UserID: e.donorID, Route: claim.RouteOpenAIChat, ModelSnapshot: "private/model", AttemptLimit: 1})
	if err != nil {
		t.Fatal(err)
	}
	input := e.selfClaimInput(t, request.ID)
	handle, err := rail.Claim(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	grant, err := rail.TakeForDispatch(ctx, handle)
	if err != nil {
		t.Fatal(err)
	}
	grant.Clear()
	if _, err := e.store.DB().Exec(`INSERT INTO dispatch_caller_parameter_errors VALUES(?,400,'invalid_value')`, handle.ClaimID()); err == nil {
		t.Fatal("personal claim accepted caller receipt")
	}
	errorContext := upstreamerror.Context{CharityCall: true, ContainsSecret: func([]byte) bool { return false }, UnchangedCallerParameter: func(string) bool { return true }}
	detail := errorContext.ReadResponse(ctx, &http.Response{StatusCode: 400, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"error":{"type":"invalid_request_error","code":"invalid_value","param":"temperature","message":"Invalid value."}}`))})
	outcome := claim.AttemptOutcome{Kind: claim.ResultResponse, UpstreamStatus: 400, UpstreamCode: detail.Code(), StreakDisposition: contract.StreakNeutral, FailureOrigin: contract.OriginUpstreamResponse, CallerFault: detail.CallerFault()}
	if _, err := rail.CompleteAttempt(ctx, handle, outcome); !errors.Is(err, claim.ErrInvalidInput) {
		t.Fatal("personal neutral accepted", err)
	}
	outcome.StreakDisposition = contract.StreakUpstreamFailure
	if _, err := rail.CompleteAttempt(ctx, handle, outcome); err != nil {
		t.Fatal(err)
	}
}

func TestCallerParameterEvidenceCannotOverrideAcceptedResponse(t *testing.T) {
	e := newCharityTestEnv(t)
	rail := e.billingRail(t)
	request := e.billingRequest(t, rail, e.requestModel, 2400, 1)
	handle := e.billingDispatch(t, rail, request.ID, 1)
	if err := rail.MarkResponseStarted(t.Context(), handle, 200); err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.DB().Exec(`INSERT INTO dispatch_caller_parameter_errors VALUES(?,400,'invalid_value')`, handle.ClaimID()); err == nil {
		t.Fatal("accepted response received caller-error evidence")
	}
	errorContext := upstreamerror.Context{CharityCall: true, ContainsSecret: func([]byte) bool { return false }, UnchangedCallerParameter: func(string) bool { return true }}
	detail := errorContext.ReadResponse(t.Context(), &http.Response{StatusCode: 400, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"error":{"type":"invalid_request_error","code":"invalid_value","param":"temperature","message":"Invalid value."}}`))})
	outcome := claim.AttemptOutcome{Kind: claim.ResultResponse, UpstreamStatus: 400, UpstreamCode: detail.Code(), StreakDisposition: contract.StreakNeutral, FailureOrigin: contract.OriginUpstreamResponse, CallerFault: detail.CallerFault()}
	if _, err := rail.CompleteAttempt(t.Context(), handle, outcome); !errors.Is(err, claim.ErrInvalidInput) {
		t.Fatal("caller evidence overrode response checkpoint", err)
	}
}

func TestCallerParameterFaultPreservesConfiguredBackoff(t *testing.T) {
	e := newBackoffEnv(t)
	e.endpointPolicy(t, []smartbackoff.Rule{{Kind: "code", Value: "invalid_value", Period: "auto"}})
	h, grant := e.dispatch(t)
	errorContext := upstreamerror.Context{CharityCall: true, Rules: grant.BackoffRules(), ContainsSecret: func([]byte) bool { return false }, UnchangedCallerParameter: func(string) bool { return true }}
	detail := errorContext.ReadResponse(t.Context(), &http.Response{StatusCode: 400, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"error":{"type":"invalid_request_error","code":"invalid_value","param":"temperature","message":"Invalid value."}}`))})
	e.complete(t, h, claim.AttemptOutcome{Kind: claim.ResultResponse, UpstreamStatus: 400, UpstreamCode: detail.Code(), StreakDisposition: contract.StreakNeutral, FailureOrigin: contract.OriginUpstreamResponse, CallerFault: detail.CallerFault(), Backoff: detail.Backoff()})
	e.wantWaiting(t, 900)
	if e.status(t).Source != "custom_rule" {
		t.Fatal("caller classification displaced configured backoff")
	}
}
