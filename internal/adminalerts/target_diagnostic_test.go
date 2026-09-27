package adminalerts

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

func diagnosticRequest(t *testing.T, environment *alertTestEnvironment, kind, id, suffix string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/admin/api/alerts/targets/"+kind+"/"+id+suffix, nil)
	request.SetPathValue("kind", kind)
	request.SetPathValue("target_id", id)
	recorder := httptest.NewRecorder()
	environment.handler(t, http.MethodGet, routeTargetDiagnostic)(recorder, request, AdminPrincipal{UserID: environment.adminID})
	return recorder
}

func diagnosticFacts(t *testing.T, response *httptest.ResponseRecorder) TargetDiagnostic {
	t.Helper()
	if response.Code != http.StatusOK {
		t.Fatalf("diagnostic status=%d body=%s", response.Code, response.Body.String())
	}
	var result TargetDiagnostic
	if err := jsonDecode(response, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Facts) == 0 || len(result.Facts) > 12 {
		t.Fatalf("unbounded or empty diagnostic: %+v", result)
	}
	return result
}

func TestTargetDiagnosticExactReadOnlyFactsAndAdministratorGate(t *testing.T) {
	env := newAlertTestEnvironment(t)
	database := env.store.DB()
	issueA := "iss_" + strings.Repeat("A", 22)
	issueB := "iss_" + strings.Repeat("B", 21) + "A"
	for _, issue := range []struct{ id, state string }{{issueA, "current"}, {issueB, "closed"}} {
		var closed, retain any
		if issue.state == "closed" {
			closed, retain = alertTestNow, alertTestNow+86400
		}
		if _, err := database.Exec(`INSERT INTO user_issues(id,user_id,source,resource_kind,resource_ref,root_cause,generation,state,summary_code,safe_detail,first_seen_at,last_seen_at,count,closed_at,retain_until)
VALUES(?,?,'model_discovery','endpoint_key','17','discovery_failed',1,?,'discovery_failed','discovery failed raw',?,?,2,?,?)`, issue.id, env.adminID, issue.state, alertTestNow-60, alertTestNow, closed, retain); err != nil {
			t.Fatal(err)
		}
	}
	first := diagnosticFacts(t, diagnosticRequest(t, env, "issue", issueA, ""))
	second := diagnosticFacts(t, diagnosticRequest(t, env, "issue", issueB, ""))
	if first.ID != issueA || second.ID != issueB || first.Kind != "issue" || second.Kind != "issue" {
		t.Fatalf("cross-target identity: %+v %+v", first, second)
	}
	if strings.Contains(strings.Join(factValues(second.Facts), "|"), "discovery failed raw") {
		t.Fatal("unexpected free-form issue detail")
	}
	if got := factValue(first.Facts, "state"); got != "current" {
		t.Fatalf("first issue state=%s", got)
	}
	if got := factValue(second.Facts, "state"); got != "closed" {
		t.Fatalf("second issue state=%s", got)
	}

	fishingID := "fb_" + strings.Repeat("A", 22)
	zero := make([]byte, 16)
	one := make([]byte, 16)
	one[15] = 1
	hash := make([]byte, 32)
	if _, err := database.Exec(`INSERT INTO game_fishing_batches(id,user_id,bait,count,unit_price_milli,entry_total_milli,payout_total_milli,operation_id,request_hash,state,ledger_rows_remaining,attempt_count,next_attempt_at,last_error_class,created_at)
VALUES(?,?,'worm',1,1000,1000,0,?,?, 'reserved',?,2,?,'db_busy',?)`, fishingID, env.adminID, "op_"+strings.Repeat("A", 22), hash, one, alertTestNow+30, alertTestNow); err != nil {
		t.Fatal(err)
	}
	fishing := diagnosticFacts(t, diagnosticRequest(t, env, "fishing_batch", fishingID, ""))
	if fishing.ID != fishingID || factValue(fishing.Facts, "state") != "reserved" || factValue(fishing.Facts, "last_error_class") != "db_busy" {
		t.Fatalf("fishing diagnostic=%+v", fishing)
	}
	for _, forbidden := range []string{"request_hash", "ledger_rows_remaining", "bait"} {
		if strings.Contains(diagnosticRequest(t, env, "fishing_batch", fishingID, "").Body.String(), forbidden) {
			t.Fatalf("private fishing field exposed: %s", forbidden)
		}
	}

	rpsID := "rps_" + strings.Repeat("A", 22)
	tx, err := database.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	account, err := ledger.CreateRPSSessionAccount(context.Background(), tx, rpsID, alertTestNow)
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if _, err := tx.Exec(`INSERT INTO game_rps_sessions(
id,account_id,mode,rules_version,state,phase,revision,phase_seq,identity_epoch,cut_seq,
ledger_rows_remaining,base_milli,platform_bp,welfare_bp,thursday_bp,gesture_seconds,
dealer_seconds,follower_seconds,player_pool,permanent_multiplier,current_plan_multiplier,
base_round_count,paid_tie_count,free_tie_count,paid_pool_streak,free_pool_streak,
platform_cut_total,welfare_cut_total,thursday_cut_total,welfare_carry_total,
phase_deadline,recent_first_seq,recent_last_seq,terminal_retry_attempt_count,started_at)
VALUES(?,?,'quick',2,'started','gesture',?,?,?,?,?,5,0,0,0,20,15,15,?,?,?,
 ?,?,?,?,?,?,?,?,?,?,?,?, ?,?)`,
		rpsID, account.ID, one, one, one, zero, zero, one, one, one,
		zero, zero, zero, zero, zero, zero, zero, zero, zero,
		alertTestNow+20, zero, zero, zero, alertTestNow); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	rps := diagnosticFacts(t, diagnosticRequest(t, env, "rps_session", rpsID, ""))
	if rps.ID != rpsID || factValue(rps.Facts, "phase") != "gesture" || factValue(rps.Facts, "retry_attempts") != "0" {
		t.Fatalf("live RPS diagnostic=%+v", rps)
	}
	for _, forbidden := range []string{"recent_events_blob", "current_gesture", "player_pool", "identity_epoch"} {
		if strings.Contains(diagnosticRequest(t, env, "rps_session", rpsID, "").Body.String(), forbidden) {
			t.Fatalf("private RPS field exposed: %s", forbidden)
		}
	}

	for _, tc := range []struct {
		kind, id string
		status   int
		code     string
	}{
		{"issue", "iss_" + strings.Repeat("C", 21) + "A", http.StatusNotFound, httperr.CodeNotFound},
		{"issue", "https://evil.invalid", http.StatusBadRequest, httperr.CodeInvalidRequest},
		{"endpoint_key", "1", http.StatusBadRequest, httperr.CodeInvalidRequest},
		{"rps_session", "rps_" + strings.Repeat("C", 21) + "A", http.StatusNotFound, httperr.CodeNotFound},
	} {
		requireErrorCode(t, diagnosticRequest(t, env, tc.kind, tc.id, ""), tc.status, tc.code)
	}
	requireErrorCode(t, diagnosticRequest(t, env, "issue", issueA, "?unknown=1"), http.StatusBadRequest, httperr.CodeInvalidRequest)
	env.authorizer.forced = authz.ErrForbidden
	requireErrorCode(t, diagnosticRequest(t, env, "issue", issueA, ""), http.StatusForbidden, httperr.CodeForbidden)
}

func TestTargetDiagnosticRetainedRPSSummary(t *testing.T) {
	env := newAlertTestEnvironment(t)
	id := "rps_" + strings.Repeat("D", 21) + "A"
	zero := make([]byte, 16)
	args := []any{id, "quick", 2, 5, 0, 0, 0, alertTestNow - 90, alertTestNow, "quick_resolved"}
	for range 10 {
		args = append(args, zero)
	}
	args = append(args, alertTestNow+2592000)
	_, err := env.store.DB().Exec(`INSERT INTO game_rps_summaries(
session_id,mode,rules_version,base_milli,platform_bp,welfare_bp,thursday_bp,started_at,terminal_at,terminal_reason,
base_round_count,paid_tie_count,free_tie_count,total_timeout_count,total_rock_count,total_scissors_count,total_paper_count,
platform_total,welfare_total,thursday_total,delete_at)
VALUES(`+strings.TrimSuffix(strings.Repeat("?,", len(args)), ",")+`)`, args...)
	if err != nil {
		t.Fatal(err)
	}
	result := diagnosticFacts(t, diagnosticRequest(t, env, "rps_session", id, ""))
	if result.ID != id || factValue(result.Facts, "state") != "completed" || factValue(result.Facts, "terminal_reason") != "quick_resolved" || factValue(result.Facts, "phase") != "" {
		t.Fatalf("retained summary diagnostic=%+v", result)
	}
}

func TestTargetDiagnosticIssueUserProjectionAndRetainedReferences(t *testing.T) {
	env := newAlertTestEnvironment(t)
	id := strconv.FormatInt(env.adminID, 10)
	database := env.store.DB()
	requireErrorCode(t, diagnosticRequest(t, env, "issue_user", id, ""), http.StatusNotFound, httperr.CodeNotFound)
	env.seedAlert(t, string(KindIssueProjectionIncomplete), "projection incomplete", "", &env.adminID, alertTestNow, false)
	empty := diagnosticFacts(t, diagnosticRequest(t, env, "issue_user", id, ""))
	if factValue(empty.Facts, "projection_phase") != "not_recorded" || factValue(empty.Facts, "retained_issue_count") != "0" || len(empty.RelatedIssueIDs) != 0 {
		t.Fatalf("missing projection state should remain explicit: %+v", empty)
	}
	if _, err := database.Exec(`INSERT INTO user_issue_projection_state(user_id,projection_incomplete,rebuild_generation,rebuild_cursor,updated_at) VALUES(?,1,3,NULL,?)`, env.adminID, alertTestNow); err != nil {
		t.Fatal(err)
	}
	incomplete := diagnosticFacts(t, diagnosticRequest(t, env, "issue_user", id, ""))
	if factValue(incomplete.Facts, "projection_phase") != "incomplete" {
		t.Fatalf("incomplete projection=%+v", incomplete)
	}
	if _, err := database.Exec(`UPDATE user_issue_projection_state SET rebuild_cursor='opaque-checkpoint' WHERE user_id=?`, env.adminID); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 23; index++ {
		issueID := "iss_" + fmt.Sprintf("%021dA", index)
		state := "current"
		var closed, retain any
		lastSeen := alertTestNow + int64(index)
		if index == 22 {
			state, closed, retain = "closed", alertTestNow-2, alertTestNow-1
			lastSeen = alertTestNow - 3
		}
		if _, err := database.Exec(`INSERT INTO user_issues(id,user_id,source,resource_kind,resource_ref,root_cause,generation,state,summary_code,safe_detail,first_seen_at,last_seen_at,count,closed_at,retain_until)
VALUES(?,?,'model_discovery','endpoint_key',?,'discovery_failed',1,?,'discovery_failed','hidden raw detail',?,?,1,?,?)`,
			issueID, env.adminID, strconv.Itoa(index+1), state, alertTestNow-100, lastSeen, closed, retain); err != nil {
			t.Fatal(err)
		}
	}
	result := diagnosticFacts(t, diagnosticRequest(t, env, "issue_user", id, ""))
	if result.Kind != "issue_user" || result.ID != id || factValue(result.Facts, "projection_phase") != "checkpointed" ||
		factValue(result.Facts, "rebuild_generation") != "3" || factValue(result.Facts, "retained_issue_count") != "22" ||
		factValue(result.Facts, "current_issue_count") != "22" || len(result.RelatedIssueIDs) != 20 ||
		result.RelatedIssueIDs[0] != "iss_"+fmt.Sprintf("%021dA", 21) {
		t.Fatalf("projection and bounded real IDs=%+v", result)
	}
	if strings.Contains(diagnosticRequest(t, env, "issue_user", id, "").Body.String(), "hidden raw detail") {
		t.Fatal("free-form issue detail escaped the safe projection")
	}
	for _, tc := range []struct {
		id     string
		status int
		code   string
	}{
		{"999999", http.StatusNotFound, httperr.CodeNotFound},
		{"01", http.StatusBadRequest, httperr.CodeInvalidRequest},
		{"9223372036854775808", http.StatusBadRequest, httperr.CodeInvalidRequest},
	} {
		requireErrorCode(t, diagnosticRequest(t, env, "issue_user", tc.id, ""), tc.status, tc.code)
	}
	requireErrorCode(t, diagnosticRequest(t, env, "issue_user", id, "?scope=all"), http.StatusBadRequest, httperr.CodeInvalidRequest)
	env.authorizer.forced = authz.ErrForbidden
	requireErrorCode(t, diagnosticRequest(t, env, "issue_user", id, ""), http.StatusForbidden, httperr.CodeForbidden)
}

func factValue(facts []AlertFact, key string) string {
	for _, fact := range facts {
		if fact.Key == key {
			return fact.Value
		}
	}
	return ""
}

func factValues(facts []AlertFact) []string {
	values := make([]string, len(facts))
	for index, fact := range facts {
		values[index] = fact.Value
	}
	return values
}
