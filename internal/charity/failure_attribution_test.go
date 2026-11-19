package charity

import (
	"database/sql"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/adminalerts"
	"github.com/waiting-here/NonbiriAPI/internal/claim"
	"github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/diagnostic"
)

func assertAttemptStreak(t *testing.T, database *sql.DB, claimID, effect, before, after string) {
	t.Helper()
	var gotEffect, gotBefore, gotAfter string
	if err := database.QueryRow(`SELECT COALESCE(streak_effect,''),COALESCE(streak_before,''),COALESCE(streak_after,'')
FROM request_attempts WHERE claim_id=?`, claimID).Scan(&gotEffect, &gotBefore, &gotAfter); err != nil {
		t.Fatal(err)
	}
	if effect != gotEffect || before != gotBefore || after != gotAfter {
		t.Fatalf("attempt streak = %q %q → %q, want %q %q → %q", gotEffect, gotBefore, gotAfter, effect, before, after)
	}
}

func TestFailureDisablementOnRetainedHTTPResultWithoutDiagnosis(t *testing.T) {
	e := newCharityTestEnv(t)
	rail := e.billingRail(t)
	if _, err := e.store.DB().Exec("UPDATE donation_keys SET failure_disable_threshold='2' WHERE id=?", e.donationKey); err != nil {
		t.Fatal(err)
	}
	first := e.billingRequest(t, rail, e.tokenModel, 5, 1)
	oldest := e.billingDispatch(t, rail, first.ID, 1)
	request := e.billingRequest(t, rail, e.tokenModel, 5, 1)
	h := e.billingDispatch(t, rail, request.ID, 1)
	outcome := claim.AttemptOutcome{Kind: claim.ResultResponse, UpstreamStatus: 401, StreakDisposition: contract.StreakUpstreamFailure, FailureOrigin: contract.OriginUpstreamResponse}
	if _, err := rail.CompleteAttempt(t.Context(), h, outcome); err != nil {
		t.Fatal(err)
	}
	// Older attempts can retain only their HTTP result when the unfinished
	// preceding claim finally settles.
	if _, err := e.store.DB().Exec("DELETE FROM request_attempt_diagnostics WHERE claim_id=?", h.ClaimID()); err != nil {
		t.Fatal(err)
	}
	if _, err := rail.CompleteAttempt(t.Context(), oldest, outcome); err != nil {
		t.Fatal(err)
	}
	var before, after, threshold string
	var fields int
	if err := e.store.DB().QueryRow(`SELECT json_extract(details,'$.failure_disablement.streak_before'),
json_extract(details,'$.failure_disablement.streak_after'),json_extract(details,'$.failure_disablement.threshold'),
(SELECT count(*) FROM json_each(details)) FROM request_attempt_diagnostics WHERE claim_id=?`, h.ClaimID()).Scan(&before, &after, &threshold, &fields); err != nil || before != "1" || after != "2" || threshold != "2" || fields != 1 {
		t.Fatalf("receipt for an HTTP-only attempt: %s → %s / %s, %d fields, %v", before, after, threshold, fields, err)
	}
}

func TestFailureDisablementIdentifiesCrossingAcrossRequests(t *testing.T) {
	e := newCharityTestEnv(t)
	rail := e.billingRail(t)
	if _, err := e.store.DB().Exec("UPDATE donation_keys SET failure_disable_threshold='3' WHERE id=?", e.donationKey); err != nil {
		t.Fatal(err)
	}
	first := e.billingRequest(t, rail, e.tokenModel, 5, 1)
	oldest := e.billingDispatch(t, rail, first.ID, 1)
	retry := e.billingRequest(t, rail, e.tokenModel, 5, 2)
	prior := e.billingDispatch(t, rail, retry.ID, 1)
	httpFailure := claim.AttemptOutcome{Kind: claim.ResultResponse, UpstreamStatus: 401,
		StreakDisposition: contract.StreakUpstreamFailure, FailureOrigin: contract.OriginUpstreamResponse}
	complete := func(h claim.Handle, outcome claim.AttemptOutcome) {
		t.Helper()
		if _, err := rail.CompleteAttempt(t.Context(), h, outcome); err != nil {
			t.Fatal(err)
		}
	}
	complete(prior, httpFailure)
	trigger := e.billingDispatch(t, rail, retry.ID, 2)
	last := e.billingRequest(t, rail, e.tokenModel, 5, 1)
	later := e.billingDispatch(t, rail, last.ID, 1)
	// This full but valid document exercises the size limit on appending the
	// receipt. The synthetic status belongs to the proxy, not the upstream.
	prefix := `{"category":"network","phase":"transport","code":"connection_reset","message":"`
	diagnosis := prefix + strings.Repeat("x", (16<<10)-len(prefix)-2) + `"}`
	transport := claim.AttemptOutcome{Kind: claim.ResultSynthetic, UpstreamStatus: 502,
		StreakDisposition: contract.StreakUpstreamFailure, FailureOrigin: contract.OriginNetwork, ErrorDiagnostics: diagnosis}
	var wg sync.WaitGroup
	for h, outcome := range map[*claim.Handle]claim.AttemptOutcome{&trigger: transport, &later: httpFailure} {
		wg.Go(func() {
			if _, err := rail.CompleteAttempt(t.Context(), *h, outcome); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if t.Failed() {
		t.FailNow()
	}
	for _, handle := range []claim.Handle{prior, trigger, later} {
		assertAttemptStreak(t, e.store.DB(), handle.ClaimID(), "pending", "", "")
	}
	var alerts int
	if err := e.store.DB().QueryRow("SELECT count(*) FROM admin_alerts WHERE kind='donation_failure_disabled'").Scan(&alerts); err != nil || alerts != 0 {
		t.Fatalf("blocked fold produced %d alerts, %v", alerts, err)
	}
	complete(oldest, httpFailure)
	complete(trigger, transport)
	complete(oldest, httpFailure)
	assertAttemptStreak(t, e.store.DB(), oldest.ClaimID(), "incremented", "0", "1")
	assertAttemptStreak(t, e.store.DB(), prior.ClaimID(), "incremented", "1", "2")
	assertAttemptStreak(t, e.store.DB(), trigger.ClaimID(), "incremented", "2", "3")
	assertAttemptStreak(t, e.store.DB(), later.ClaimID(), "incremented", "3", "4")
	var body string
	if err := e.store.DB().QueryRow("SELECT count(*),coalesce(max(context_json),'') FROM admin_alerts WHERE kind='donation_failure_disabled'").Scan(&alerts, &body); err != nil || alerts != 1 {
		t.Fatalf("failure alerts=%d, %v", alerts, err)
	}
	var detail struct {
		Trigger adminalerts.TriggerAttempt `json:"trigger_attempt"`
	}
	if err := json.Unmarshal([]byte(body), &detail); err != nil {
		t.Fatal(err)
	}
	a := detail.Trigger
	if a.RequestID != retry.ID || a.AttemptSeq != "2" || a.StreakBefore != "2" || a.StreakAfter != "3" || a.Threshold != "3" ||
		a.Reason != "connection_reset" || a.StatusCode != 0 || a.NativeService != "chat_completions" || !contract.Type(a.ConnectorType).Valid() {
		t.Fatalf("wrong folded attempt: %+v", a)
	}
	var effectCount int
	if err := e.store.DB().QueryRow(`SELECT count(*) FROM request_attempt_diagnostics WHERE json_type(details,'$.failure_disablement') IS NOT NULL`).Scan(&effectCount); err != nil || effectCount != 1 {
		t.Fatalf("attempt receipts=%d, %v", effectCount, err)
	}
	if err := e.store.DB().QueryRow("SELECT details FROM request_attempt_diagnostics WHERE claim_id=?", trigger.ClaimID()).Scan(&body); err != nil {
		t.Fatal(err)
	}
	f := diagnostic.Decode(body)
	if f == nil || f.FailureDisablement == nil || *f.FailureDisablement != a.FailureDisablement ||
		f.Code != "connection_reset" || f.Category != "network" || f.Phase != "transport" {
		t.Fatalf("wrong attempt receipt: %+v", f)
	}
}

func TestUndispatchedReleaseSettlesLegacyKeyReceipt(t *testing.T) {
	e := newCharityTestEnv(t)
	rail := e.billingRail(t)
	first := e.billingRequest(t, rail, e.tokenModel, 5, 1)
	firstClaim := e.claim(t, first.ID, 1, false)
	second := e.billingRequest(t, rail, e.tokenModel, 5, 1)
	secondClaim := e.claim(t, second.ID, 1, true)
	// Older unmarked claims have only the physical key cursor. Model the
	// retained attempt awaiting its predecessor across an upgrade.
	if _, err := e.store.DB().Exec(`INSERT INTO request_attempts(claim_id,request_log_id,attempt_seq,
connector_type,canonical_base_url,upstream_model_id,result_kind,upstream_status,started_at,completed_at,streak_effect)
SELECT ?,id,1,'openai-compatible','https://charity.example.test/v1','upstream-model','response',401,?,?,'pending'
FROM request_logs WHERE logical_request_id=?`, secondClaim.id, charityTestNow+1, charityTestNow+2, second.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.DB().Exec("UPDATE donation_keys SET failure_disable_threshold='0',failure_streak=X'00000000000000010000000000000000' WHERE id=?", e.donationKey); err != nil {
		t.Fatal(err)
	}
	e.completeAttempt(t, second.ID, secondClaim, claim.CharityAttemptInput{CompletedAt: charityTestNow + 2, UsageUnknown: true,
		StreakDisposition: contract.StreakUpstreamFailure, FailureOrigin: contract.OriginUpstreamResponse}, claim.RewardZero, claim.CharityActual{})
	reopenAfterOfflineUpgrade(t, e)
	assertAttemptStreak(t, e.store.DB(), secondClaim.id, "pending", "", "")
	tx := beginTestTx(t, e.store.DB())
	if err := e.service.ReleaseUndispatched(t.Context(), tx, claim.CharityRelease{RequestID: first.ID, ClaimID: firstClaim.id,
		DonationKeyID: &e.donationKey, ReleasedAt: charityTestNow + 3}); err != nil {
		t.Fatal(err)
	}
	commitTestTx(t, tx)
	assertAttemptStreak(t, e.store.DB(), secondClaim.id, "incremented", "18446744073709551616", "18446744073709551617")
	var attempts int
	if err := e.store.DB().QueryRow("SELECT count(*) FROM request_attempts WHERE claim_id=?", firstClaim.id).Scan(&attempts); err != nil || attempts != 0 {
		t.Fatalf("undispatched attempt rows=%d err=%v", attempts, err)
	}
}
