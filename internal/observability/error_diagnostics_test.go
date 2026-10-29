package observability

import (
	"bytes"
	"encoding/base64"
	"errors"
	"io"
	"strconv"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/diagnostic"
	"github.com/waiting-here/NonbiriAPI/internal/upstreamerror"
)

func captureFailure(t *testing.T, f *diagnosticFixture, ref DiagnosticRef, raw []byte) string {
	t.Helper()
	ctx := diagnostic.WithRecorder(f.repository.ErrorScope(t.Context(), ref))
	diagnostic.RecordResponse(ctx, 502, []diagnostic.Correlation{{Source: "upstream", Name: "x-request-id", Value: "fixture 192.0.2.7"}})
	diagnostic.RecordRead(ctx, len(raw), nil)
	diagnostic.RecordKnownError(ctx, "read_response", "network", "unexpected_eof", "Response ended before all bytes were received", io.ErrUnexpectedEOF)
	upstreamerror.CaptureEvent(ctx, 502, "application/octet-stream", raw)
	var id int64
	if err := f.repository.db.QueryRow("SELECT max(id) FROM request_error_bodies").Scan(&id); err != nil {
		t.Fatal(err)
	}
	return strconv.FormatInt(id, 10)
}
func diagnosticPayload(t *testing.T, body ErrorBody) []byte {
	t.Helper()
	if body.Encoding == "base64" {
		data, err := base64.StdEncoding.DecodeString(body.Body)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	return []byte(body.Body)
}

func TestIndependentFailureDiagnosticsUseRecordedSourceAndRole(t *testing.T) {
	for _, kind := range []string{"image_task", "image_discovery"} {
		t.Run(kind, func(t *testing.T) {
			f := newDiagnosticFixture(t)
			task, operation := f.imageRoots(t)
			if _, err := f.repository.db.Exec("UPDATE image_activity_tasks SET upstream_revision=1 WHERE id=?", task); err != nil {
				t.Fatal(err)
			}
			ref := DiagnosticRef{TaskID: task, AttemptSeq: 1}
			if kind == "image_discovery" {
				ref = DiagnosticRef{OperationID: operation, AttemptSeq: 1}
			}
			raw := []byte("\xfffixture 192.0.2.7")
			id := captureFailure(t, f, ref, raw)
			for _, actor := range []authz.Actor{f.admin, f.steward} {
				shown, err := f.reader.Detail(diagContext(actor), diagActor(actor), id)
				if err != nil || !bytes.Equal(diagnosticPayload(t, shown.Body), raw) || shown.ErrorDiagnostics == nil || shown.ErrorDiagnostics.Message != "Response ended before all bytes were received" || shown.ErrorDiagnostics.ReceivedBytes != int64(len(raw)) {
					t.Fatal(shown, err)
				}
			}
			check := func(want []byte, wantText string) {
				t.Helper()
				shown, err := f.reader.Detail(diagContext(f.trainee), diagActor(f.trainee), id)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(diagnosticPayload(t, shown.Body), want) || shown.Body.BytesSaved != int64(len(raw)) || !shown.Body.IPv4Redacted || shown.ErrorDiagnostics.Message != "Response ended before all bytes were received" || shown.ErrorDiagnostics.Correlation[0].Value != wantText || shown.ErrorDiagnostics.ReceivedBytes != int64(len(raw)) || shown.ErrorDiagnostics.ProtocolComplete {
					t.Fatalf("projected=%+v payload=%q", shown, diagnosticPayload(t, shown.Body))
				}
				if kind == "image_discovery" && (shown.Item.Dispatch == nil || shown.Item.Dispatch.URL != "https://fixture.invalid/v1/models") {
					t.Fatal("authorized dispatch was redacted", shown.Item)
				}
			}
			check([]byte("\xff[redacted] [redacted]"), "[redacted] [redacted]")
			if _, err := f.repository.db.Exec("INSERT INTO image_upstream_revisions SELECT 2,control_id,'https://replacement.invalid',secret_context,secret_ciphertext,adapter_json,image_origins_json,per_user_limit,global_limit,queue_timeout_seconds,execution_timeout_seconds,memory_budget_mib,actor_user_id,created_at+1 FROM image_upstream_revisions WHERE revision=1"); err != nil {
				t.Fatal(err)
			}
			if kind == "image_task" {
				// Normal settlement releases the task's source revision. Retiring
				// its credentials must not erase the diagnostic's source identity.
				for _, statement := range []string{
					"UPDATE image_activity_tasks SET upstream_revision=NULL",
					"DELETE FROM image_model_refreshes",
					"DELETE FROM image_upstream_revisions WHERE revision=1",
				} {
					if _, err := f.repository.db.Exec(statement); err != nil {
						t.Fatal(err)
					}
				}
			}
			check([]byte("\xff[redacted] [redacted]"), "[redacted] [redacted]")
			channel := diagnosticID(t, "mch_")
			if _, err := f.repository.db.Exec("INSERT INTO mainstream_channels(id,name,category,enabled,state,revision,created_at,updated_at) VALUES(?,'Fixture','api_platform',1,'active',1,1,1)", channel); err != nil {
				t.Fatal(err)
			}
			if _, err := f.repository.db.Exec("INSERT INTO mainstream_channel_entries(channel_id,ord,connector_type,canonical_base_url,native_service_mask) VALUES(?,0,'openai-compatible','https://fixture.invalid',1)", channel); err != nil {
				t.Fatal(err)
			}
			check([]byte("\xfffixture [redacted]"), "fixture [redacted]")
			if _, err := f.repository.db.Exec("UPDATE mainstream_channels SET enabled=0 WHERE id=?", channel); err != nil {
				t.Fatal(err)
			}
			check([]byte("\xff[redacted] [redacted]"), "[redacted] [redacted]")
			if _, err := f.reader.Detail(diagContext(f.ordinary), diagActor(f.ordinary), id); !errors.Is(err, ErrDiagnosticForbidden) {
				t.Fatal(err)
			}
			var stored []byte
			if err := f.repository.db.QueryRow("SELECT body FROM request_error_bodies WHERE id=?", id).Scan(&stored); err != nil || !bytes.Equal(stored, raw) {
				t.Fatal("saved bytes changed", err)
			}
			if _, err := f.repository.db.Exec("DELETE FROM request_error_bodies WHERE id=?", id); err != nil {
				t.Fatal(err)
			}
			if n := scalar(t, f.repository.db, "SELECT count(*) FROM request_error_diagnostics"); n != 0 {
				t.Fatal("orphan diagnostic", n)
			}
		})
	}
}

func TestIndependentFailureDiagnosticsDoNotInventImageSource(t *testing.T) {
	f := newDiagnosticFixture(t)
	task, _ := f.imageRoots(t)
	id := captureFailure(t, f, DiagnosticRef{TaskID: task, AttemptSeq: 1}, []byte("fixture 192.0.2.7"))
	shown, err := f.reader.Detail(diagContext(f.trainee), diagActor(f.trainee), id)
	if err != nil || shown.Body.Body != "[redacted]" || shown.ErrorDiagnostics.Correlation[0].Value != "[redacted]" || shown.ErrorDiagnostics.Code != "unexpected_eof" {
		t.Fatal("unrelated configured source was used", shown, err)
	}
	var missing bool
	if err := f.repository.db.QueryRow("SELECT source_base_url IS NULL FROM request_error_diagnostics WHERE error_id=?", id).Scan(&missing); err != nil || !missing {
		t.Fatal("source was invented", err)
	}
}

func TestIndependentFailureDiagnosticsUnknownSourceAndLateCapture(t *testing.T) {
	f := newDiagnosticFixture(t)
	request := f.discovery(t, f.repository.now().Unix())
	id := captureFailure(t, f, DiagnosticRef{RequestID: request, AttemptSeq: 1}, []byte("fixture 192.0.2.7"))
	shown, err := f.reader.Detail(diagContext(f.trainee), diagActor(f.trainee), id)
	if err != nil || shown.Body.Body != "[redacted]" || shown.ErrorDiagnostics.Message != "Response ended before all bytes were received" || shown.ErrorDiagnostics.Correlation[0].Value != "[redacted]" || shown.ErrorDiagnostics.Code != "unexpected_eof" || len(shown.ErrorDiagnostics.Causes) == 0 || shown.ErrorDiagnostics.Causes[0].Type == "" || shown.ErrorDiagnostics.Causes[0].Message == "[redacted]" || shown.ErrorDiagnostics.ReceivedBytes != 17 {
		t.Fatal("unknown source disclosed", shown, err)
	}
	if _, err = f.repository.db.Exec("DELETE FROM request_logs WHERE logical_request_id=?", request); err != nil {
		t.Fatal(err)
	}
	ctx := diagnostic.WithRecorder(f.repository.ErrorScope(t.Context(), DiagnosticRef{RequestID: request, AttemptSeq: 1}))
	diagnostic.RecordError(ctx, "read_response", io.ErrUnexpectedEOF)
	upstreamerror.CaptureEvent(ctx, 502, "text/plain", []byte("late"))
	for _, table := range []string{"request_error_bodies", "request_error_diagnostics"} {
		if n := scalar(t, f.repository.db, "SELECT count(*) FROM "+table); n != 0 {
			t.Fatal("late capture resurrected "+table, n)
		}
	}
	if _, err = f.reader.Detail(diagContext(f.admin), diagActor(f.admin), id); !errors.Is(err, ErrDiagnosticNotFound) {
		t.Fatal(err)
	}
}

func seedDiagnosticAttempt(t *testing.T, f *diagnosticFixture, request string, sequence int, base string) {
	t.Helper()
	_, err := f.repository.db.Exec(`INSERT INTO request_attempts(
 claim_id,request_log_id,attempt_seq,connector_type,canonical_base_url,upstream_model_id,
 result_kind,upstream_status,started_at,completed_at)
 SELECT ?,id,?,'openai-compatible',?,'','response',502,started_at,started_at
 FROM request_logs WHERE logical_request_id=?`, diagnosticID(t, "clm_"), sequence, base, request)
	if err != nil {
		t.Fatal(err)
	}
}

func TestIndependentFailureDiagnosticsUseExactRequestAttempt(t *testing.T) {
	f := newDiagnosticFixture(t)
	request := f.discovery(t, f.repository.now().Unix())
	seedDiagnosticAttempt(t, f, request, 1, "https://fixture.invalid")
	seedDiagnosticAttempt(t, f, request, 2, "https://another.invalid")
	for _, tc := range []struct {
		seq       int
		raw, want string
	}{
		{1, "fixture another 192.0.2.7", "[redacted] another [redacted]"},
		{2, "fixture another 192.0.2.7", "fixture [redacted] [redacted]"},
		{3, "fixture another 192.0.2.7", "[redacted]"},
	} {
		id := captureFailure(t, f, DiagnosticRef{RequestID: request, AttemptSeq: tc.seq}, []byte(tc.raw))
		shown, err := f.reader.Detail(diagContext(f.trainee), diagActor(f.trainee), id)
		if err != nil || shown.Body.Body != tc.want {
			t.Fatal(tc.seq, shown, err)
		}
	}
}

func TestIndependentFailureDiagnosticsUseOnlyDispatchedClaimSource(t *testing.T) {
	f := newDiagnosticFixture(t)
	now := f.repository.now().Unix()
	request, claim := diagnosticID(t, "req_"), diagnosticID(t, "clm_")
	for _, stmt := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO logical_requests(id,user_id,route_kind,state,attempt_limit,accounting_state,settlement_destination,ledger_rows_remaining,created_at) VALUES(?,?,'model_discovery','accepted',1,'none','user',zeroblob(16),?)`, []any{request, f.owner, now}},
		{`INSERT INTO request_logs(logical_request_id,user_id,route_kind,started_at) VALUES(?,?,'model_discovery',?)`, []any{request, f.owner, now}},
		{`INSERT INTO endpoint_key_secrets(id,context_id,canonical_base_url,connector_type,encrypted_secret,created_at) VALUES(901,randomblob(16),'https://legacy.invalid','openai-compatible','fixture',?)`, []any{now}},
		{`INSERT INTO dispatch_claims(id,logical_request_id,attempt_seq,purpose,secret_ref_id,claim_now,state) VALUES(?,?,1,'discovery',901,?,'claimed')`, []any{claim, request, now}},
	} {
		if _, err := f.repository.db.Exec(stmt.query, stmt.args...); err != nil {
			t.Fatal(err)
		}
	}
	id := captureFailure(t, f, DiagnosticRef{RequestID: request, AttemptSeq: 1}, []byte("legacy fixture 192.0.2.7"))
	check := func(want string) {
		t.Helper()
		shown, err := f.reader.Detail(diagContext(f.trainee), diagActor(f.trainee), id)
		if err != nil || shown.Body.Body != want {
			t.Fatal(shown, err)
		}
	}
	check("[redacted]")
	if _, err := f.repository.db.Exec("UPDATE dispatch_claims SET state='dispatched',dispatched_at=? WHERE id=?", now, claim); err != nil {
		t.Fatal(err)
	}
	check("[redacted] fixture [redacted]")
	tx, err := f.repository.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for _, stmt := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO credentials(id,user_id,current_version_id,created_at,updated_at) VALUES(901,?,901,?,?)`, []any{f.owner, now, now}},
		{`INSERT INTO credential_versions(id,credential_id,version,secret_ref_id,created_at) VALUES(901,901,1,901,?)`, []any{now}},
		{`INSERT INTO dispatch_claim_targets(claim_id,authorization_kind,credential_id_snapshot,credential_version_id,credential_version,configured_type,canonical_base_url) VALUES(?,'legacy',901,901,1,'openai-compatible','https://fixture.invalid')`, []any{claim}},
	} {
		if _, err = tx.Exec(stmt.query, stmt.args...); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	check("legacy [redacted] [redacted]")
	otherID := captureFailure(t, f, DiagnosticRef{RequestID: request, AttemptSeq: 2}, []byte("legacy fixture 192.0.2.7"))
	shown, err := f.reader.Detail(diagContext(f.trainee), diagActor(f.trainee), otherID)
	if err != nil || shown.Body.Body != "[redacted]" {
		t.Fatal("another attempt borrowed an active source", shown, err)
	}
}
