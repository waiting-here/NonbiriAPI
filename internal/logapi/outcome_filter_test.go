package logapi

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

func TestActualBackoffOutcomeUsesRecordedGatesBeforePaging(t *testing.T) {
	f := newNumberedLogFixture(t)
	ctx := context.Background()
	f.mustExec(`INSERT INTO request_attempt_backoffs(claim_id,request_log_id,reason,scope,retry_at,observed_only)
 SELECT 'gate-'||id,id,'retry_header','key',1001,0 FROM request_logs WHERE id BETWEEN 100 AND 110`)
	f.mustExec(`INSERT INTO request_attempt_backoffs VALUES('second-gate',100,'retry_header','key',1002,NULL,0),('advice',111,'retry_header','key',1002,NULL,1)`)
	filter := ListFilter{Outcome: "actual_backoff", Page: &pagination.Request{Page: 2, Size: 10}}
	admin, err := f.repo.ListAdmin(ctx, filter)
	if err != nil {
		t.Fatal(err)
	}
	requireLogPagination(t, admin.Pagination, 2, 10, 11)
	if len(admin.Data) != 1 || admin.Data[0].ID != logOpaqueID("req_", 100) {
		t.Fatalf("unexpected final page: %+v", admin.Data)
	}
	steward, err := f.repo.ListSteward(ctx, 6, filter, mismatchStewardAuthorizer{})
	if err != nil {
		t.Fatal(err)
	}
	requireLogPagination(t, steward.Pagination, 2, 10, 11)
	if _, err = f.repo.ListSteward(ctx, 5, filter, refuseDiagnosticRead{}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("denied reader: %v", err)
	}
	if _, err = f.repo.ListUser(ctx, logUserOne, filter); !errors.Is(err, ErrInvalid) {
		t.Fatalf("owner filter: %v", err)
	}
	if _, err = parseListFilter("outcome=actual_backoff", "user", false); !errors.Is(err, ErrInvalid) {
		t.Fatalf("owner route: %v", err)
	}
	rows, err := f.repo.ExportAdmin(ctx, ListFilter{Outcome: "actual_backoff"})
	if err != nil || len(rows) != 11 {
		t.Fatalf("export: %d, %v", len(rows), err)
	}
	for _, role := range []string{"admin", "steward", "user"} {
		parsed, err := parseListFilter("outcome=success", role, false)
		if err != nil || parsed.Outcome != "success" {
			t.Fatalf("outcome parse: %v, %v", parsed, err)
		}
	}
	plan := charityQueryPlan(t, f.db, "SELECT l.id FROM request_logs l WHERE l.completed_at>?"+actualBackoffFilter+" ORDER BY l.started_at DESC,l.id DESC LIMIT 10", []any{0})
	t.Log(plan)
	if strings.Contains(plan, "CORRELATED") || !strings.Contains(plan, "SEARCH l USING INTEGER PRIMARY KEY") {
		t.Fatalf("unbounded request probe: %s", plan)
	}
}

func TestOutcomesRespectDispatchEvidenceOwnershipAndCursorScope(t *testing.T) {
	f := newNumberedLogFixture(t)
	ctx := t.Context()
	const update = `UPDATE request_logs SET caller_result_class='failed',caller_status=418,caller_error_code='internal_error',
   error_source=?,attempt_count=? WHERE id=?`
	f.mustExec(update, "platform", 0, 100)
	f.mustExec(update, "platform", 1, 101)
	f.mustExec(update, "upstream", 0, 102)
	f.mustExec(update, "platform", 1, 103)
	f.mustExec(`UPDATE request_logs SET rejection_stage='flow',rejection_reason='user_rpm',attempt_count=0 WHERE id=103`)
	f.mustExec(`UPDATE request_logs SET user_id=?,origin_user_id=? WHERE id=101`, logUserTwo, logUserTwo)
	status := 418
	filter := ListFilter{Status: &status, Outcome: "before_dispatch", Page: &pagination.Request{Page: 1, Size: 10}}
	rejected, err := f.repo.ListAdmin(ctx, filter)
	if err != nil {
		t.Fatal(err)
	}
	requireLogPagination(t, rejected.Pagination, 1, 10, 2)
	if rejected.Data[0].ID != logOpaqueID("req_", 103) || rejected.Data[1].ID != logOpaqueID("req_", 100) {
		t.Fatal("unexpected rejection classification", rejected.Data)
	}
	filter.Outcome = "failed"
	owner, err := f.repo.ListUser(ctx, logUserOne, filter)
	if err != nil {
		t.Fatal(err)
	}
	requireLogPagination(t, owner.Pagination, 1, 10, 3)
	filter.Outcome = "success"
	succeeded, err := f.repo.ListUser(ctx, logUserOne, filter)
	if err != nil {
		t.Fatal(err)
	}
	requireLogPagination(t, succeeded.Pagination, 1, 10, 0)
	first, err := f.repo.ListAdmin(ctx, ListFilter{Outcome: "success", Limit: 1})
	if err != nil || first.NextCursor == nil {
		t.Fatal("missing cursor", err)
	}
	_, err = f.repo.ListAdmin(ctx, ListFilter{Outcome: "failed", Cursor: *first.NextCursor, Limit: 1})
	if !errors.Is(err, ErrInvalid) {
		t.Fatal("cursor accepted for a different outcome", err)
	}
	for _, raw := range []string{"outcome=", "outcome=all", "outcome=failed&outcome=success"} {
		if _, err := parseListFilter(raw, "admin", false); !errors.Is(err, ErrInvalid) {
			t.Fatal("invalid outcome accepted", raw, err)
		}
	}
}
