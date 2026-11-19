package v1

import (
	"database/sql"
	"errors"
	"net/http"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/headerfacts"
)

func TestAttemptBackoffRecoveryFromSchema54(t *testing.T) {
	store, err := db.Open(bootstrapTestPath(t, "attempts.sqlite"), bootstrapTestVault(t))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	database := store.DB()
	restoreSourceLayout(t, database)
	const at int64 = 1_700_000_000
	type sample struct {
		name, capture, reason string
		offset                int64
	}
	cases := []sample{
		{"google overload", "empty", "google_model_overloaded", 60},
		{"native Google", "invalid", "google_model_overloaded", 60},
		{"exact header", "60", "retry_header", 60},
		{"rounded header", "59", "retry_header", 60},
		{"missing capture", "", "recorded_wait", 60},
		{"wrong capture", "wrong status", "recorded_wait", 60},
		{"mismatched hint", "300", "recorded_wait", 60},
		{"other provider", "", "retry_header", 60},
		{"other delay", "", "retry_header", 45},
		{"legacy key", "", "retry_header", 9},
		{"already recorded", "", "smart_quota", 77},
		{"zero", "", "", 0},
		{"expired", "", "", -1},
		{"personal", "", "", 60},
		{"personal log", "", "", 60},
		{"different sequence", "", "", 60},
		{"classification alone", "", "", 60},
	}
	var claims []string
	for i, s := range cases {
		request, err := db.GenerateOpaqueID("req_")
		if err != nil {
			t.Fatal(err)
		}
		id, err := db.GenerateOpaqueID("clm_")
		if err != nil {
			t.Fatal(err)
		}
		claims = append(claims, id)
		logID := i + 1
		route, purpose, reward := "charity_chat_completions", "charity", "zero"
		var rewardAmount any = 0
		if s.name == "personal" {
			route, purpose, reward, rewardAmount = "openai_chat_completions", "self", "not_applicable", nil
		}
		if s.name == "personal log" {
			route = "openai_chat_completions"
		}
		mustExec(t, database, `INSERT INTO logical_requests(id,route_kind,state,attempt_limit,accounting_state,settlement_destination,ledger_rows_remaining,created_at)
VALUES(?,?,'running',2,'none','user',zeroblob(16),?)`, request, route, at)
		mustExec(t, database, `INSERT INTO request_logs(id,logical_request_id,route_kind,started_at) VALUES(?,?,?,?)`, logID, request, route, at)
		mustExec(t, database, `INSERT INTO dispatch_claims(id,logical_request_id,attempt_seq,purpose,claim_now,state,donor_reward_state,donor_reward_actual_milli,dispatched_at,terminal_at,streak_disposition,failure_origin)
VALUES(?,?,1,?,?,'committed',?,?,?,?,'neutral','legacy_unknown')`, id, request, purpose, at, reward, rewardAmount, at, at)
		connector, baseURL := "openai-compatible", "https://generativelanguage.googleapis.com/v1beta/openai"
		if s.name == "native Google" || s.name == "other provider" {
			baseURL = "https://synthetic.example"
		}
		if s.name == "native Google" {
			connector = "google-gemini"
		}
		attempt := 1
		if s.name == "different sequence" {
			attempt = 2
		}
		mustExec(t, database, `INSERT INTO request_attempts(claim_id,request_log_id,attempt_seq,connector_type,canonical_base_url,upstream_model_id,result_kind,upstream_status,started_at,completed_at)
VALUES(?,?,?,?,?,'model','response',503,?,?)`, id, logID, attempt, connector, baseURL, at, at)
		if s.name != "classification alone" {
			mustExec(t, database, `INSERT INTO dispatch_retry_after_observations VALUES(?,503,?,?)`, id, at, at+s.offset)
		}
		if s.name == "legacy key" || s.name == "classification alone" {
			mustExec(t, database, `INSERT INTO dispatch_smart_backoff_facts(claim_id,generation,episode,is_trial,period,source,reset_at) VALUES(?,1,0,0,'day','builtin',0)`, id)
		}
		if s.capture != "" {
			status := 503
			h := make(http.Header)
			if s.capture == "wrong status" {
				status = 200
			} else if s.capture != "empty" {
				h.Set("X-Retry-After", s.capture)
			}
			ctx := headerfacts.WithResponse(t.Context(), request, 1)
			headerfacts.CaptureResponse(ctx, &http.Response{StatusCode: status, Header: h})
			fact := headerfacts.Response(headerfacts.PrepareResponse(ctx, request, 1), request, 1)
			mustExec(t, database, `INSERT INTO request_header_facts(request_log_id,attempt_seq,captured_at,save_state,encoded,bytes_saved)
VALUES(?,1,?,'saved',?,?)`, logID, at, fact, fact.BytesSaved())
		}
	}
	mustExec(t, database, db.AttemptHealthSQL)
	for i, s := range cases {
		if s.name == "already recorded" {
			mustExec(t, database, `INSERT INTO request_attempt_backoffs(claim_id,request_log_id,scope,reason,period,retry_at) VALUES(?,?,'service','smart_quota','day',?)`, claims[i], i+1, at+900)
		}
	}
	tx, err := database.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := backfillAttemptBackoffs(t.Context(), tx); err != nil {
		t.Fatal(err)
	}
	for i, s := range cases {
		var reason, scope string
		var deadline int64
		var observed bool
		err := tx.QueryRow(`SELECT reason,scope,retry_at,observed_only FROM request_attempt_backoffs WHERE claim_id=? AND request_log_id=?`, claims[i], i+1).
			Scan(&reason, &scope, &deadline, &observed)
		if s.reason == "" && errors.Is(err, sql.ErrNoRows) {
			continue
		}
		wantScope, wantAt, wantObserved := "unknown", at+s.offset, true
		switch s.name {
		case "legacy key":
			wantScope = "key"
		case "already recorded":
			wantScope, wantAt, wantObserved = "service", at+900, false
		}
		if err != nil || reason != s.reason || scope != wantScope || deadline != wantAt || observed != wantObserved {
			t.Fatalf("%s: got (%q,%q,%d,%t), want (%q,%q,%d,%t), err=%v", s.name, reason, scope, deadline, observed, s.reason, wantScope, wantAt, wantObserved, err)
		}
	}
}
