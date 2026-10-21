package logapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/observability"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

func domainLogFixture(t *testing.T) *logFixture {
	t.Helper()
	f := newLogFixture(t)
	f.mustExec(`ALTER TABLE users ADD COLUMN is_banned INTEGER NOT NULL DEFAULT 0`)
	f.mustExec(`ALTER TABLE users ADD COLUMN banned_until INTEGER`)
	f.mustExec(`INSERT INTO users(id) VALUES(?)`, logUserOne)
	f.mustExec(`UPDATE endpoints SET note='example endpoint' WHERE id=201`)
	f.mustExec(`UPDATE endpoint_keys SET note='EXAMPLE key' WHERE id=301`)
	f.mustExec(`UPDATE request_logs SET model='example/second',caller_error_code='example_error' WHERE id=1`)
	f.mustExec(`UPDATE request_attempts SET canonical_base_url='https://api.example.com/v1',upstream_model_id='example-gpt',upstream_code='example_error 192.0.2.7',diag='example second 192.0.2.7' WHERE request_log_id=1 AND attempt_seq=1`)
	f.mustExec(`UPDATE request_attempts SET canonical_base_url='https://second.test/v1',upstream_model_id='second-gpt',diag='example second 192.0.2.7' WHERE request_log_id=1 AND attempt_seq=2`)
	return f
}

func TestLogDNSUserDetailListExportAndAttemptIsolation(t *testing.T) {
	f := domainLogFixture(t)
	ctx := t.Context()
	detail, err := f.repo.GetUser(ctx, logUserOne, f.selfID, AttemptFilter{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	self := detail.(UserSelfLogDetail)
	if self.Request.Model != "[redacted]/[redacted]" || *self.Request.CallerErrorCode != "[redacted]_error" || self.Request.RequestPath != nil || self.Attempts.NextCursor == nil {
		t.Fatal(self)
	}
	first := self.Attempts.Data[0]
	if first.EndpointBaseURL != "https://[redacted]/v1" || first.EndpointNote != "[redacted] endpoint" || first.KeyNote != "[redacted] key" || first.UpstreamModelID != "[redacted]-gpt" || *first.Diag != "[redacted] second 192.0.2.7" {
		t.Fatal(first)
	}
	next, err := f.repo.GetUser(ctx, logUserOne, f.selfID, AttemptFilter{Limit: 1, Cursor: *self.Attempts.NextCursor})
	if err != nil {
		t.Fatal(err)
	}
	if got := *next.(UserSelfLogDetail).Attempts.Data[0].Diag; got != "example [redacted] 192.0.2.7" {
		t.Fatal("attempt used another endpoint's terms", got)
	}
	model := "example/second"
	for _, filter := range []ListFilter{{Model: &model}, {Model: &model, Page: &pagination.Request{Page: 1, Size: 10}}} {
		page, err := f.repo.ListUser(ctx, logUserOne, filter)
		if err != nil || len(page.Data) != 1 || page.Data[0].(UserSelfLogRow).Model != "[redacted]/[redacted]" {
			t.Fatal(page, err)
		}
	}
	exported, err := f.repo.ExportUser(ctx, logUserOne, ListFilter{Model: &model})
	if err != nil || len(exported) != 1 {
		t.Fatal(exported, err)
	}
	for _, encode := range []func([]UserLogRow) ([]byte, error){MarshalUserJSON, MarshalUserCSV} {
		body, err := encode(exported)
		if err != nil || bytes.Contains(body, []byte("example")) || bytes.Contains(body, []byte("second")) || !bytes.Contains(body, []byte("[redacted]")) {
			t.Fatal(string(body), err)
		}
	}
	registrar := &logUserTestRegistrar{}
	if err := RegisterUserRoutes(registrar, f.repo); err != nil {
		t.Fatal(err)
	}
	response := callLogUserHandler(t, registrar.handlers["GET /api/logs/{id}"], logUserOne, "/api/logs/"+f.selfID, "", map[string]string{"id": f.selfID})
	if response.Code != 200 || strings.Contains(response.Body.String(), "192.0.2.7") || strings.Contains(response.Body.String(), "api.example.com") || !strings.Contains(response.Body.String(), "[redacted]") {
		t.Fatal(response.Code, response.Body.String())
	}
	var original string
	if err := f.db.QueryRow(`SELECT model FROM request_logs WHERE id=1`).Scan(&original); err != nil || original != model {
		t.Fatal("saved model changed", original, err)
	}
	if err := f.db.QueryRow(`SELECT diag FROM request_attempts WHERE request_log_id=1 AND attempt_seq=1`).Scan(&original); err != nil || original != "example second 192.0.2.7" {
		t.Fatal("saved diagnostic changed", original, err)
	}
}

func TestLogDNSRoleAndCurrentMainstreamAddress(t *testing.T) {
	f := domainLogFixture(t)
	check := func(wantDNS bool) {
		t.Helper()
		l5, err := f.repo.GetSteward(t.Context(), logUserTwo, f.selfID, AttemptFilter{}, traineeLogRead{})
		if err != nil {
			t.Fatal(err)
		}
		attempt := l5.Attempts.Data[0]
		want := "example second [redacted]"
		if wantDNS {
			want = "[redacted] second [redacted]"
		}
		if *attempt.Diag != want || attempt.EndpointBaseURL != "https://api.example.com/v1" || attempt.UpstreamModelID != "example-gpt" || *attempt.UpstreamCode != "example_error [redacted]" {
			t.Fatal(attempt)
		}
		user, err := f.repo.GetUser(t.Context(), logUserOne, f.selfID, AttemptFilter{})
		if err != nil {
			t.Fatal(err)
		}
		shown := user.(UserSelfLogDetail).Attempts.Data[0]
		wantModel := "example/[redacted]"
		if wantDNS {
			wantModel = "[redacted]/[redacted]"
		}
		if strings.Contains(shown.EndpointBaseURL, "[redacted]") != wantDNS || user.(UserSelfLogDetail).Request.Model != wantModel {
			t.Fatal(shown)
		}
		admin, err := f.repo.GetAdmin(t.Context(), f.selfID, AttemptFilter{})
		if err != nil {
			t.Fatal(err)
		}
		l6, err := f.repo.GetSteward(t.Context(), logUserTwo, f.selfID, AttemptFilter{}, allowLogStewardRead{})
		if err != nil || *admin.Attempts.Data[0].Diag != "example second 192.0.2.7" {
			t.Fatal(admin, err)
		}
		requireManagementLogEqual(t, admin, l6)
	}
	check(true)
	// Later registration applies to older custom requests at the exact address.
	f.mustExec(`INSERT INTO mainstream_channels VALUES('registered','openai-compatible','https://api.example.com/v1',1,'active')`)
	check(false)
	f.mustExec(`UPDATE mainstream_channels SET canonical_base_url='https://api.example.com/v2' WHERE id='registered'`)
	check(true)
	f.mustExec(`UPDATE mainstream_channels SET canonical_base_url='https://api.example.com/v1',connector_type='anthropic-compatible' WHERE id='registered'`)
	check(true)
	f.mustExec(`UPDATE mainstream_channels SET connector_type='openai-compatible',enabled=0 WHERE id='registered'`)
	check(true)
	f.mustExec(`UPDATE mainstream_channels SET enabled=1 WHERE id='registered'`)
	check(false)
	f.mustExec(`DELETE FROM endpoints WHERE id=201`)
	check(false)
	// Charity logs use the same current address match without historical source.
	f.mustExec(`UPDATE request_attempts SET canonical_base_url='https://api.example.com/v1',diag='example 192.0.2.7',endpoint_id_snapshot=NULL WHERE request_log_id=2 AND attempt_seq=1`)
	f.mustExec(`UPDATE request_logs SET model='example' WHERE id=2`)
	l5, err := f.repo.GetSteward(t.Context(), logUserTwo, f.charityID, AttemptFilter{}, traineeLogRead{})
	if err != nil || *l5.Attempts.Data[0].Diag != "example [redacted]" {
		t.Fatal(l5, err)
	}
	user, err := f.repo.GetUser(t.Context(), logUserOne, f.charityID, AttemptFilter{})
	if err != nil || user.(UserCharityLogDetail).Request.Model != "example" {
		t.Fatal(user, err)
	}
	f.mustExec(`UPDATE mainstream_channels SET state='retired',enabled=0 WHERE id='registered'`)
	check(true)
	l5, err = f.repo.GetSteward(t.Context(), logUserTwo, f.charityID, AttemptFilter{}, traineeLogRead{})
	if err != nil || *l5.Attempts.Data[0].Diag != "[redacted] [redacted]" {
		t.Fatal(l5, err)
	}
	user, err = f.repo.GetUser(t.Context(), logUserOne, f.charityID, AttemptFilter{})
	if err != nil || user.(UserCharityLogDetail).Request.Model != "[redacted]" {
		t.Fatal(user, err)
	}
}

func TestLogDNSRawErrorCompletedActiveAndUnknownSources(t *testing.T) {
	f := domainLogFixture(t)
	f.mustExec(`CREATE TABLE request_error_bodies(request_log_id INTEGER,attempt_seq INTEGER,event_seq INTEGER,http_status INTEGER,content_type TEXT,bytes_saved INTEGER,truncated INTEGER,save_state TEXT,failure_reason TEXT,created_at INTEGER,expires_at INTEGER,body BLOB)`)
	f.mustExec(`INSERT INTO endpoint_key_secrets(id,connector_type,canonical_base_url) VALUES(901,'openai-compatible','https://api.example.com/v1')`)
	f.mustExec(`INSERT INTO dispatch_claims(id,logical_request_id,attempt_seq,purpose,state,secret_ref_id) VALUES(?, ?,1,'self','dispatched',901)`, logOpaqueID("clm_", 901), f.pendingID)
	// Another attempt's active endpoint cannot identify this body's source.
	f.mustExec(`INSERT INTO dispatch_claims(id,logical_request_id,attempt_seq,purpose,state,secret_ref_id) VALUES(?, ?,2,'self','dispatched',901)`, logOpaqueID("clm_", 902), f.discoveryID)
	raw := []byte("example 192.0.2.7\xffEXAMPLE_error")
	for _, id := range []int64{1, 3, 6} {
		f.mustExec(`INSERT INTO request_error_bodies VALUES(?,1,1,502,'application/octet-stream',?,0,'saved','',900000,2000000,?)`, id, len(raw), raw)
	}
	for _, tc := range []struct {
		id   string
		want string
	}{{f.selfID, "[redacted] [redacted]\xff[redacted]_error"}, {f.pendingID, "[redacted] [redacted]\xff[redacted]_error"}, {f.discoveryID, "[redacted]"}} {
		for _, trainee := range []bool{true, false} {
			var auth StewardAuthorizer = allowLogStewardRead{}
			if trainee {
				auth = traineeLogRead{}
			}
			registrar := &logUserTestRegistrar{}
			if err := RegisterStewardDiagnosticRoutes(registrar, f.repo, auth); err != nil {
				t.Fatal(err)
			}
			response := callLogUserHandler(t, registrar.handlers["GET /api/steward/logs/{id}/attempts/{seq}/errors/{event}"], logUserTwo, "/api/steward/logs/"+tc.id+"/attempts/1/errors/1", "", map[string]string{"id": tc.id, "seq": "1", "event": "1"})
			var body observability.ErrorBody
			if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &body) != nil {
				t.Fatal(response.Code, response.Body.String())
			}
			decoded, err := base64.StdEncoding.DecodeString(body.Body)
			want := string(raw)
			if trainee {
				want = tc.want
			}
			if err != nil || string(decoded) != want || body.Encoding != "base64" || body.BytesSaved != int64(len(raw)) || body.Truncated || body.SaveState != "saved" || body.ContentType != "application/octet-stream" || body.IPv4Redacted != trainee {
				t.Fatal(tc.id, trainee, body, string(decoded), err)
			}
		}
	}
	// Mainstream exemption preserves domain text and the old IPv4 rule.
	f.mustExec(`INSERT INTO mainstream_channels VALUES('registered','openai-compatible','https://api.example.com/v1',1,'active')`)
	f.mustExec(`UPDATE request_error_bodies SET body=?,bytes_saved=? WHERE request_log_id=1`, []byte("example 192.0.2.7"), len("example 192.0.2.7"))
	api := &diagnosticAPI{repository: f.repo, steward: traineeLogRead{}}
	for _, tc := range []struct{ id, encoding, want string }{
		{f.selfID, "utf-8", "example [redacted]"},
		{f.pendingID, "base64", base64.StdEncoding.EncodeToString([]byte("example [redacted]\xffEXAMPLE_error"))},
	} {
		r := httptest.NewRequest(http.MethodGet, "/errors", nil)
		r.SetPathValue("id", tc.id)
		r.SetPathValue("seq", "1")
		r.SetPathValue("event", "1")
		w := httptest.NewRecorder()
		api.diagnostic(w, r, logUserTwo)
		var body observability.ErrorBody
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &body) != nil || body.Encoding != tc.encoding || body.Body != tc.want {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	var saved []byte
	if err := f.db.QueryRow(`SELECT body FROM request_error_bodies WHERE request_log_id=6`).Scan(&saved); err != nil || !bytes.Equal(saved, raw) {
		t.Fatal("raw capture changed", saved, err)
	}
}

type privacyCountingReader struct {
	*sql.DB
	queries int
}

func (r *privacyCountingReader) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	r.queries++
	return r.DB.QueryContext(ctx, query, args...)
}

func TestLogDNSProjectionBatchesOnlySelectedRoots(t *testing.T) {
	f := domainLogFixture(t)
	reader := &privacyCountingReader{DB: f.db}
	ids := make([]int64, 101)
	rows := make([]UserLogRow, len(ids))
	for i := range ids {
		ids[i] = int64(i + 1)
		rows[i] = UserSelfLogRow{ID: strconv.Itoa(i + 1), Model: "example second other"}
	}
	if err := redactUserLogRows(t.Context(), reader, ids, rows); err != nil || reader.queries != 2 {
		t.Fatal(reader.queries, err)
	}
	if rows[0].(UserSelfLogRow).Model != "[redacted] [redacted] other" || rows[2].(UserSelfLogRow).Model != "example second other" {
		t.Fatal(rows[:3])
	}
}
