package logapi

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/requestattempt"
)

func TestPrehandlerPhaseFiltersAndManagementExport(t *testing.T) {
	f := newLogFixture(t)
	f.mustExec(`UPDATE request_logs SET rejection_stage='flow',rejection_reason='user_rpm',request_method='POST',request_path='/v1/chat/completions',caller_result_class='failed',caller_status=429,caller_error_code='rate_limited',attempt_count=0,uncached_input_tokens=0,cache_write_input_tokens=0,cache_read_input_tokens=0,output_tokens=0,usage_unknown=0 WHERE logical_request_id=?`, f.charityID)
	ctx := context.Background()
	owner, err := f.repo.ListUser(ctx, logUserOne, ListFilter{Phase: "pre_handler"})
	if err != nil || len(owner.Data) != 1 {
		t.Fatal(owner, err)
	}
	row, ok := owner.Data[0].(UserCharityLogRow)
	if !ok || row.Phase != "pre_handler" || *row.RejectionReason != "user_rpm" || *row.RequestMethod != "POST" {
		t.Fatal("missing refusal fields", owner)
	}
	admin, err := f.repo.ListAdmin(ctx, ListFilter{Phase: "pre_handler"})
	if err != nil || len(admin.Data) != 1 || admin.Data[0].ID != f.charityID {
		t.Fatal(admin, err)
	}
	steward, err := f.repo.ListSteward(ctx, 999, ListFilter{Phase: "pre_handler"}, allowLogStewardRead{})
	if err != nil || len(steward.Data) != 1 || steward.Data[0].Phase != "pre_handler" {
		t.Fatal(steward, err)
	}
	ordinary, err := f.repo.ListUser(ctx, logUserOne, ListFilter{Phase: "handler"})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range ordinary.Data {
		switch v := r.(type) {
		case UserSelfLogRow:
			if v.Phase != "handler" {
				t.Fatal(v)
			}
		case UserCharityLogRow:
			if v.Phase != "handler" {
				t.Fatal(v)
			}
		}
	}
	exported, err := f.repo.ExportAdmin(ctx, ListFilter{Phase: "pre_handler"})
	if err != nil || len(exported) != 1 {
		t.Fatal(exported, err)
	}
	body, err := MarshalAdminCSV(exported)
	if err != nil || !strings.Contains(string(body), "pre_handler,flow,user_rpm,POST,/v1/chat/completions") {
		t.Fatal(string(body), err)
	}
	for _, query := range []string{"phase=", "phase=unknown", "phase=handler&phase=pre_handler"} {
		if _, err := parseListFilter(query, "user", false); err == nil {
			t.Fatal("accepted invalid phase", query)
		}
	}
}

func TestSafeRejectionDetailAllRolesAndExports(t *testing.T) {
	f := newNumberedLogFixture(t)
	f.mustExec(`UPDATE request_logs SET rejection_stage='preflight',rejection_reason='invalid_request',request_method='POST',request_path='/v1/chat/completions',caller_result_class='failed',caller_status=400,caller_error_code='invalid_request',attempt_count=0,uncached_input_tokens=0,cache_write_input_tokens=0,cache_read_input_tokens=0,output_tokens=0,usage_unknown=0 WHERE logical_request_id=?`, f.charityID)
	want := requestattempt.NewDetail("stream", "expected a boolean or null")
	for _, diag := range []string{requestattempt.EncodeDetail(want), "", `raw error PRIVATE_INPUT`, `{"field":"PRIVATE_FIELD","reason":"PRIVATE_INPUT"}`} {
		f.mustExec(`UPDATE request_logs SET error_diag=? WHERE logical_request_id=?`, diag, f.charityID)
		ctx := context.Background()
		admin, err := f.repo.ListAdmin(ctx, ListFilter{Phase: "pre_handler"})
		if err != nil || len(admin.Data) != 1 {
			t.Fatal(admin, err)
		}
		expected := diag == requestattempt.EncodeDetail(want)
		got := admin.Data[0].RejectionDetail
		if expected && (got == nil || *got != *want) || !expected && got != nil {
			t.Fatal("invalid projection", got)
		}
		detail, err := f.repo.GetAdmin(ctx, f.charityID, AttemptFilter{})
		if err != nil {
			t.Fatal(err)
		}
		steward, err := f.repo.GetSteward(ctx, 999, f.charityID, AttemptFilter{}, allowLogStewardRead{})
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range []*requestattempt.RejectionDetail{detail.Request.RejectionDetail, steward.Request.RejectionDetail} {
			if expected && (d == nil || *d != *want) || !expected && d != nil {
				t.Fatal("read guessed a cause", d)
			}
		}
		exported, err := f.repo.ExportSteward(ctx, 999, ListFilter{Phase: "pre_handler"}, allowLogStewardRead{})
		if err != nil {
			t.Fatal(err)
		}
		csv, err := MarshalAdminCSV(exported)
		if err != nil {
			t.Fatal(err)
		}
		if expected && !strings.Contains(string(csv), "stream,expected a boolean or null") || strings.Contains(string(csv), "PRIVATE_") {
			t.Fatal(string(csv))
		}
		user, err := f.repo.GetUser(ctx, logUserOne, f.charityID, AttemptFilter{})
		if err != nil {
			t.Fatal(err)
		}
		body, _ := json.Marshal(user)
		if strings.Contains(string(body), "rejection_detail") != expected || strings.Contains(string(body), "PRIVATE_") {
			t.Fatal("unsafe or missing owner diagnostic", string(body))
		}
		ownerPage, err := f.repo.ListUser(ctx, logUserOne, ListFilter{Phase: "pre_handler"})
		if err != nil {
			t.Fatal(err)
		}
		ownerRows, err := f.repo.ExportUser(ctx, logUserOne, ListFilter{Phase: "pre_handler"})
		if err != nil {
			t.Fatal(err)
		}
		ownerJSON, err := MarshalUserJSON(ownerRows)
		if err != nil {
			t.Fatal(err)
		}
		ownerCSV, err := MarshalUserCSV(ownerRows)
		if err != nil {
			t.Fatal(err)
		}
		pageJSON, _ := json.Marshal(ownerPage)
		for _, projection := range []string{string(pageJSON), string(ownerJSON), string(ownerCSV)} {
			if strings.Contains(projection, want.Reason) != expected || strings.Contains(projection, "PRIVATE_") {
				t.Fatal("unsafe or missing owner export", projection)
			}
		}
		if _, err := f.repo.GetUser(ctx, logUserTwo, f.charityID, AttemptFilter{}); !errors.Is(err, ErrNotFound) {
			t.Fatal("cross-account diagnostic", err)
		}
	}
}

func TestHandlerErrorDiagnosticDoesNotBecomeUserRejectionDetail(t *testing.T) {
	f := newLogFixture(t)
	f.mustExec("UPDATE request_logs SET error_diag=? WHERE logical_request_id=?", requestattempt.EncodeDetail(requestattempt.NewDetail("stream", "expected a boolean")), f.charityID)
	detail, err := f.repo.GetUser(context.Background(), logUserOne, f.charityID, AttemptFilter{})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(detail)
	if strings.Contains(string(body), "rejection_detail") {
		t.Fatal("handler error reclassified", string(body))
	}
}

func TestUndispatchedResourceDetailKeepsHandlerPhaseAndPrivacy(t *testing.T) {
	f := newLogFixture(t)
	want := requestattempt.NewDetail("model", "no usable model resource is available for this request")
	for _, tc := range []struct {
		code, source string
		attempts     int
		visible      bool
	}{
		{"unbound_model", "platform", 0, true},
		{"rate_limited", "platform", 0, true},
		{"unbound_model", "platform", 1, false},
		{"unbound_model", "upstream", 0, false},
		{"internal_error", "platform", 0, false},
	} {
		f.mustExec(`UPDATE request_logs SET caller_result_class='failed',caller_status=503,caller_error_code=?,error_source=?,attempt_count=?,error_diag=? WHERE logical_request_id=?`, tc.code, tc.source, tc.attempts, requestattempt.EncodeDetail(want), f.charityID)
		admin, err := f.repo.GetAdmin(t.Context(), f.charityID, AttemptFilter{})
		if err != nil {
			t.Fatal(err)
		}
		steward, err := f.repo.GetSteward(t.Context(), 999, f.charityID, AttemptFilter{}, allowLogStewardRead{})
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range []RejectionFields{admin.Request.RejectionFields, steward.Request.RejectionFields} {
			if row.Phase != "handler" || row.RejectionStage != nil || (row.RejectionDetail != nil) != tc.visible {
				t.Fatal("wrong detail boundary or phase", tc, row)
			}
			if tc.visible && *row.RejectionDetail != *want {
				t.Fatal("changed safe detail", row)
			}
		}
		owner, err := f.repo.GetUser(t.Context(), logUserOne, f.charityID, AttemptFilter{})
		if err != nil {
			t.Fatal(err)
		}
		body, _ := json.Marshal(owner)
		if strings.Contains(string(body), want.Reason) != tc.visible || !strings.Contains(string(body), `"phase":"handler"`) {
			t.Fatal("wrong owner detail", string(body))
		}
		if _, err := f.repo.GetUser(t.Context(), logUserTwo, f.charityID, AttemptFilter{}); !errors.Is(err, ErrNotFound) {
			t.Fatal("cross-account diagnostic", err)
		}
	}
}
