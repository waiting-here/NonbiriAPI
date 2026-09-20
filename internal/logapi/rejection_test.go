package logapi

import (
	"context"
	"strings"
	"testing"
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
