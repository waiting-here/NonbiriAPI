package logapi

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/diagnostic"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

func TestManagementAttemptFocusAndWholeRequestFailures(t *testing.T) {
	f := newNumberedLogFixture(t)
	const root = 18
	req := logOpaqueID("req_", root)
	f.insertLog(root, req, logUserOne, "[公益]channel/model", string(RouteCharityChat), string(ResultSuccess), 200, nil, 100, 300, [4]int64{}, 0, 14)
	transition := &diagnostic.FailureDisablement{StreakBefore: "9", StreakAfter: "10", Threshold: "10", ConnectorType: "google-gemini", NativeService: "gemini_generate_content"}
	for i := range 14 {
		// Undispatched claims leave gaps. Sequence 33 is on page two, row two.
		seq := int64(2*i + 11)
		status, reason := 401, "google_access_token_type_unsupported"
		switch {
		case i == 13:
			status, reason = 200, ""
		case i >= 9:
			status, reason = 429, "google_daily_requests"
		}
		f.insertAttempt(root, seq, 202, 302, "https://generativelanguage.googleapis.com", "google-gemini", "model", string(ResultResponse), status, nil, nil, [4]int64{}, 0, 100, 101)
		if reason != "" {
			facts := &diagnostic.Failure{Category: "upstream", Phase: "upstream_response", Code: "upstream_error", ProviderReason: reason, UpstreamStatus: status}
			if seq == 33 {
				facts.FailureDisablement = transition
			}
			insertFailureFacts(t, f, root, seq, facts)
		}
	}
	filter, err := parseAttemptFilter("attempt_seq=33&attempt_page_size=10")
	if err != nil {
		t.Fatal(err)
	}
	admin, err := f.repo.GetAdmin(t.Context(), req, filter)
	if err != nil {
		t.Fatal(err)
	}
	want := []AttemptFailureGroup{{Reason: "google_access_token_type_unsupported", StatusCode: 401, Count: 9}, {Reason: "google_daily_requests", StatusCode: 429, Count: 4}}
	if !reflect.DeepEqual(admin.FailureSummary, want) || admin.AttemptPagination.Page != "2" || len(admin.Attempts.Data) != 4 {
		t.Fatalf("focused overview: %v %+v", admin.FailureSummary, admin.AttemptPagination)
	}
	row := admin.Attempts.Data[1]
	if row.AttemptSeq != "33" || !reflect.DeepEqual(row.FailureDisablement, transition) || row.ErrorDiagnostics.FailureDisablement != nil {
		t.Fatalf("target attempt: %+v", row)
	}
	steward, err := f.repo.GetSteward(t.Context(), 999, req, filter, allowLogStewardRead{})
	if err != nil {
		t.Fatal(err)
	}
	requireManagementLogEqual(t, admin, steward)
	first, err := f.repo.GetAdmin(t.Context(), req, AttemptFilter{FocusSeq: 32, Page: &pagination.Request{Page: 1, Size: 10}})
	if err != nil || first.AttemptPagination.Page != "1" || !reflect.DeepEqual(first.FailureSummary, want) {
		t.Fatalf("absent attempt fallback: %v %v", first, err)
	}
	if _, err := f.repo.GetUser(t.Context(), logUserOne, req, filter); !errors.Is(err, ErrInvalid) {
		t.Fatalf("ordinary user accepted management focus: %v", err)
	}
	f.mustExec(`INSERT INTO request_delivery_facts(request_log_id,details) VALUES(?,?)`, root,
		`{"failure":{"category":"upstream","provider_reason":"google_daily_requests","failure_disablement":{"threshold":"10"}}}`)
	user, err := f.repo.GetUser(t.Context(), logUserOne, req, AttemptFilter{})
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(user)
	if err != nil || strings.Contains(string(body), "google_daily_requests") || strings.Contains(string(body), "failure_disablement") || strings.Contains(string(body), "failure_summary") {
		t.Fatalf("ordinary charity projection: %s, %v", body, err)
	}
}

func TestAttemptFocusQueryRejectsConflictingWindows(t *testing.T) {
	for _, query := range []string{"attempt_seq=0", "attempt_seq=101", "attempt_seq=01", "attempt_seq=2&attempt_seq=3", "attempt_seq=2&attempt_cursor=x", "attempt_seq=2&attempt_page=1", "attempt_seq=2&attempt_limit=20"} {
		if _, err := parseAttemptFilter(query); !errors.Is(err, ErrInvalid) {
			t.Errorf("%q: %v", query, err)
		}
	}
}

func TestSyntheticFailureSummaryUsesOnlyObservedUpstreamStatus(t *testing.T) {
	f := newNumberedLogFixture(t)
	const root = 18
	id := logOpaqueID("req_", root)
	f.insertLog(root, id, logUserOne, "[公益]channel/model", string(RouteCharityChat), string(ResultFailed), 502, "upstream", 100, 300, [4]int64{}, 0, 2)
	for seq := int64(1); seq <= 2; seq++ {
		f.insertAttempt(root, seq, 202, 302, "https://example.invalid", "openai-compatible", "model", string(ResultSynthetic), 502, nil, nil, [4]int64{}, 0, 100, 101)
		insertFailureFacts(t, f, root, seq, &diagnostic.Failure{Category: "network", Phase: "read_response", Code: "connection_reset", HeadersReceived: seq == 2, UpstreamStatus: 200})
	}
	detail, err := f.repo.GetAdmin(t.Context(), id, AttemptFilter{})
	want := []AttemptFailureGroup{{Reason: "connection_reset", Count: 1}, {Reason: "connection_reset", StatusCode: 200, Count: 1}}
	if err != nil || !reflect.DeepEqual(detail.FailureSummary, want) {
		t.Fatal(detail.FailureSummary, err)
	}
	facts, effect := attemptDiagnostics(`{"failure_disablement":{"streak_before":"0","streak_after":"1","threshold":"1","connector_type":"google-gemini"}}`)
	if facts != nil || effect == nil || effect.Threshold != "1" {
		t.Fatal("an independently observed transition must not invent a diagnosis", facts, effect)
	}
}
