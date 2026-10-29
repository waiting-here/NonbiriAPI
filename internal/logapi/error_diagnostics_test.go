package logapi

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/diagnostic"
	"github.com/waiting-here/NonbiriAPI/internal/observability"
)

func insertFailureFacts(t *testing.T, f *logFixture, root, sequence int64, facts *diagnostic.Failure) {
	t.Helper()
	f.mustExec("INSERT INTO request_attempt_diagnostics(claim_id,request_log_id,attempt_seq,category,phase,details,completed_at) VALUES(?,?,?,?,?,?,?)",
		logOpaqueID("clm_", uint64(root*10+sequence)), root, sequence, facts.Category, facts.Phase, diagnostic.Encode(facts), f.clock.Now().Unix())
}
func privacyFailure() *diagnostic.Failure {
	text := "example 192.0.2.7"
	return &diagnostic.Failure{Category: "network", Phase: "read_response", Code: "connection_reset", Message: text, Causes: []diagnostic.Cause{{Type: "*net.OpError", Code: "connection_reset", Message: text}}, Secondary: []diagnostic.Cause{{Type: "*errors.errorString", Code: "client_write_failed", Message: text}}, Correlation: []diagnostic.Correlation{{Source: "upstream", Name: "x-request-id", Value: text}}, UpstreamStatus: 200, HeadersReceived: true, ReceivedBytes: 341, ReceivedEvents: 2, ForwardedBytes: 90, BodyEOF: true}
}
func TestFailureDetailsRoleProjectionAndHistoricalAbsence(t *testing.T) {
	f := domainLogFixture(t)
	facts := privacyFailure()
	insertFailureFacts(t, f, 1, 1, facts)
	insertFailureFacts(t, f, 2, 1, facts)
	f.mustExec("UPDATE request_attempts SET canonical_base_url='https://api.example.com/v1' WHERE request_log_id=2")
	recorder, err := observability.NewRepository(f.db)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{f.selfID, f.charityID} {
		if err = recorder.RecordDelivery(t.Context(), id, diagnostic.Delivery{HTTPStatus: 200, Bytes: 90, Failed: true, Failure: facts}); err != nil {
			t.Fatal(err)
		}
	}
	admin, err := f.repo.GetAdmin(t.Context(), f.selfID, AttemptFilter{})
	if err != nil || !reflect.DeepEqual(admin.Attempts.Data[0].ErrorDiagnostics, facts) || !reflect.DeepEqual(admin.Delivery.Failure, facts) {
		t.Fatal(admin, err)
	}
	l6, err := f.repo.GetSteward(t.Context(), logUserTwo, f.selfID, AttemptFilter{}, allowLogStewardRead{})
	if err != nil {
		t.Fatal(err)
	}
	requireManagementLogEqual(t, admin, l6)
	l5, err := f.repo.GetSteward(t.Context(), logUserTwo, f.selfID, AttemptFilter{}, traineeLogRead{})
	if err != nil {
		t.Fatal(err)
	}
	for _, shown := range []*diagnostic.Failure{l5.Attempts.Data[0].ErrorDiagnostics, l5.Delivery.Failure} {
		body, _ := json.Marshal(shown)
		if strings.Contains(string(body), "example") || strings.Contains(string(body), "192.0.2.7") || shown.Message != "[redacted] [redacted]" || shown.Causes[0].Message != shown.Message || shown.Secondary[0].Message != shown.Message || shown.Correlation[0].Value != shown.Message || shown.ReceivedBytes != 341 || shown.ProtocolComplete {
			t.Fatalf("L5 projection: %s", body)
		}
	}
	if l5.Attempts.Data[0].EndpointBaseURL != "https://api.example.com/v1" {
		t.Fatal("authorized endpoint was hidden")
	}
	owner, err := f.repo.GetUser(t.Context(), logUserOne, f.selfID, AttemptFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if got := owner.(UserSelfLogDetail).Attempts.Data[0].ErrorDiagnostics.Message; got != "[redacted] 192.0.2.7" {
		t.Fatalf("owner DNS projection: %q", got)
	}
	for _, id := range []string{f.selfID, f.charityID} {
		detail, err := f.repo.GetUser(t.Context(), logUserOne, id, AttemptFilter{})
		if err != nil {
			t.Fatal(err)
		}
		out := httptest.NewRecorder()
		writeLogJSON(out, 200, detail)
		if strings.Contains(out.Body.String(), "192.0.2.7") || strings.Contains(out.Body.String(), "api.example.com") {
			t.Fatal(out.Body.String())
		}
		if id == f.charityID {
			requireNoJSONKeys(t, detail, "attempts", "attempt_count", "endpoint_key_id", "upstream_model_id", "error_diagnostics")
		}
		if _, err = f.repo.GetUser(t.Context(), logUserTwo, id, AttemptFilter{}); !errors.Is(err, ErrNotFound) {
			t.Fatalf("other owner accessed facts: %v", err)
		}
	}
	old, err := f.repo.GetAdmin(t.Context(), f.otherID, AttemptFilter{})
	if err != nil || old.Delivery != nil || old.Attempts.Data[0].ErrorDiagnostics != nil {
		t.Fatal("invented legacy observations", old, err)
	}
	f.mustExec("INSERT INTO mainstream_channels VALUES('diagnostic-mainstream',1,'active')")
	f.mustExec("INSERT INTO mainstream_channel_entries VALUES(9,'diagnostic-mainstream','openai-compatible','https://api.example.com/v1',1)")
	l5, err = f.repo.GetSteward(t.Context(), logUserTwo, f.selfID, AttemptFilter{}, traineeLogRead{})
	if err != nil || l5.Attempts.Data[0].ErrorDiagnostics.Message != "example [redacted]" {
		t.Fatal("mainstream projection", l5, err)
	}
	again, err := f.repo.GetAdmin(t.Context(), f.selfID, AttemptFilter{})
	if err != nil || !reflect.DeepEqual(again.Attempts.Data[0].ErrorDiagnostics, facts) {
		t.Fatal("read mutated stored facts", again, err)
	}
}

func TestFailureFiltersMatchSameAttemptAcrossListsAndExports(t *testing.T) {
	f := newLogFixture(t)
	insertFailureFacts(t, f, 1, 1, &diagnostic.Failure{Category: "network", Phase: "read_response", Code: "network", Message: "DETAIL-ONLY-SENTINEL"})
	insertFailureFacts(t, f, 1, 2, &diagnostic.Failure{Category: "protocol", Phase: "decode_response", Code: "protocol"})
	insertFailureFacts(t, f, 2, 1, &diagnostic.Failure{Category: "network", Phase: "decode_response", Code: "network"})
	for _, tc := range []struct {
		filter ListFilter
		want   []string
	}{
		{ListFilter{ErrorCategory: "network"}, []string{f.selfID, f.charityID}},
		{ListFilter{ErrorPhase: "read_response"}, []string{f.selfID}},
		{ListFilter{ErrorCategory: "network", ErrorPhase: "decode_response"}, []string{f.charityID}},
	} {
		page, err := f.repo.ListAdmin(t.Context(), tc.filter)
		if err != nil {
			t.Fatal(err)
		}
		ids := map[string]bool{}
		for _, row := range page.Data {
			ids[row.ID] = true
		}
		if len(ids) != len(tc.want) {
			t.Fatal(page)
		}
		for _, id := range tc.want {
			if !ids[id] {
				t.Fatal("wrong filter result", page)
			}
		}
		for _, auth := range []StewardAuthorizer{allowLogStewardRead{}, traineeLogRead{}} {
			steward, err := f.repo.ListSteward(t.Context(), logUserTwo, tc.filter, auth)
			if err != nil || len(steward.Data) != len(tc.want) {
				t.Fatal(steward, err)
			}
			rows, err := f.repo.ExportSteward(t.Context(), logUserTwo, tc.filter, auth)
			if err != nil || len(rows) != len(tc.want) {
				t.Fatal(rows, err)
			}
			for _, row := range rows {
				if !ids[row.ID] {
					t.Fatal("steward export filter drift", rows)
				}
			}
		}
		rows, err := f.repo.ExportAdmin(t.Context(), tc.filter)
		if err != nil || len(rows) != len(tc.want) {
			t.Fatal(rows, err)
		}
		for _, row := range rows {
			if !ids[row.ID] {
				t.Fatal("admin export filter drift", rows)
			}
		}
		for _, encode := range []func([]AdminLogRow) ([]byte, error){MarshalAdminJSON, MarshalAdminCSV} {
			body, err := encode(rows)
			if err != nil || strings.Contains(string(body), "DETAIL-ONLY-SENTINEL") || strings.Contains(string(body), "error_diagnostics") {
				t.Fatal("details leaked to export", string(body), err)
			}
		}
	}
	for _, role := range []string{"admin", "steward", "user"} {
		_, err := parseListFilter("error_category=network&error_phase=decode_response", role, true)
		if role == "user" && !errors.Is(err, ErrInvalid) || role != "user" && err != nil {
			t.Fatalf("filter privilege %s: %v", role, err)
		}
	}
}
