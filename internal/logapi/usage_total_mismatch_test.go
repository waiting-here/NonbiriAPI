package logapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

type mismatchStewardAuthorizer struct{}

func (mismatchStewardAuthorizer) AuthorizeStewardRead(_ context.Context, _ *sql.Tx, actorID int64) error {
	if actorID == 6 {
		return nil
	}
	return ErrForbidden
}

func TestUsageTotalMismatchManagementProjectionFilterAndAuthority(t *testing.T) {
	f := newNumberedLogFixture(t)
	f.mustExec(`UPDATE request_logs SET usage_total_mismatch=1 WHERE id=1 OR (id>=100 AND id<=120)`)
	f.mustExec(`UPDATE request_attempts SET usage_total_mismatch=1 WHERE request_log_id=1 AND attempt_seq=1`)
	ctx := context.Background()
	yes, no := true, false
	trueFilter := ListFilter{UsageTotalMismatch: &yes, Page: &pagination.Request{Page: 1, Size: 20}}
	first, err := f.repo.ListAdmin(ctx, trueFilter)
	if err != nil {
		t.Fatal(err)
	}
	requireLogPagination(t, first.Pagination, 1, 20, 22)
	if len(first.Data) != 20 {
		t.Fatalf("first filtered page = %d", len(first.Data))
	}
	for _, row := range first.Data {
		if !row.UsageTotalMismatch {
			t.Fatalf("unmatched row escaped filter: %+v", row)
		}
	}
	trueFilter.Page.Page = 2
	second, err := f.repo.ListAdmin(ctx, trueFilter)
	if err != nil {
		t.Fatal(err)
	}
	requireLogPagination(t, second.Pagination, 2, 20, 22)
	if len(second.Data) != 2 || !second.Data[1].UsageTotalMismatch || second.Data[1].ID != f.selfID {
		t.Fatalf("second filtered page = %+v", second.Data)
	}
	steward, err := f.repo.ListSteward(ctx, 6, trueFilter, mismatchStewardAuthorizer{})
	if err != nil {
		t.Fatal(err)
	}
	requireLogPagination(t, steward.Pagination, 2, 20, 22)
	if len(steward.Data) != 2 || !steward.Data[1].UsageTotalMismatch {
		t.Fatalf("steward filtered page = %+v", steward.Data)
	}
	falseRows, err := f.repo.ListAdmin(ctx, ListFilter{UsageTotalMismatch: &no})
	if err != nil {
		t.Fatal(err)
	}
	if len(falseRows.Data) != 7 {
		t.Fatalf("false filter returned %d rows", len(falseRows.Data))
	}
	for _, row := range falseRows.Data {
		if row.UsageTotalMismatch {
			t.Fatalf("mismatched row escaped false filter: %+v", row)
		}
	}
	firstCursor, err := f.repo.ListAdmin(ctx, ListFilter{UsageTotalMismatch: &yes, Limit: 1})
	if err != nil || firstCursor.NextCursor == nil {
		t.Fatalf("filtered cursor = %+v, %v", firstCursor, err)
	}
	if _, err := f.repo.ListAdmin(ctx, ListFilter{UsageTotalMismatch: &no, Limit: 1, Cursor: *firstCursor.NextCursor}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("cursor crossed mismatch filter: %v", err)
	}
	if _, err := f.repo.ListSteward(ctx, 6, ListFilter{UsageTotalMismatch: &yes, Limit: 1, Cursor: *firstCursor.NextCursor}, mismatchStewardAuthorizer{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("cursor crossed role/actor: %v", err)
	}
	adminDetail, err := f.repo.GetAdmin(ctx, f.selfID, AttemptFilter{})
	if err != nil || !adminDetail.Request.UsageTotalMismatch || len(adminDetail.Attempts.Data) != 2 ||
		!adminDetail.Attempts.Data[0].UsageTotalMismatch || adminDetail.Attempts.Data[1].UsageTotalMismatch {
		t.Fatalf("admin attempt projection = %+v, %v", adminDetail, err)
	}
	stewardDetail, err := f.repo.GetSteward(ctx, 6, f.selfID, AttemptFilter{}, mismatchStewardAuthorizer{})
	if err != nil || !stewardDetail.Request.UsageTotalMismatch || !stewardDetail.Attempts.Data[0].UsageTotalMismatch {
		t.Fatalf("steward attempt projection = %+v, %v", stewardDetail, err)
	}
	user, err := f.repo.GetUser(ctx, logUserOne, f.selfID, AttemptFilter{})
	if err != nil {
		t.Fatal(err)
	}
	requireNoJSONKeys(t, user, "usage_total_mismatch")
	if _, err := f.repo.ListUser(ctx, logUserOne, ListFilter{UsageTotalMismatch: &yes}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("ordinary user accessed management filter: %v", err)
	}

	exported, err := f.repo.ExportAdmin(ctx, ListFilter{UsageTotalMismatch: &yes})
	if err != nil || len(exported) != 22 {
		t.Fatalf("admin export = %d, %v", len(exported), err)
	}
	stewardExport, err := f.repo.ExportSteward(ctx, 6, ListFilter{UsageTotalMismatch: &yes}, mismatchStewardAuthorizer{})
	if err != nil || len(stewardExport) != 22 {
		t.Fatalf("steward export = %d, %v", len(stewardExport), err)
	}
	jsonBody, err := MarshalAdminJSON(exported)
	if err != nil || !bytes.Contains(jsonBody, []byte(`"usage_total_mismatch":true`)) {
		t.Fatalf("JSON export marker missing: %v", err)
	}
	csvBody, err := MarshalAdminCSV(exported)
	if err != nil {
		t.Fatal(err)
	}
	csvRows, err := csv.NewReader(bytes.NewReader(csvBody)).ReadAll()
	if err != nil || len(csvRows) != 23 {
		t.Fatalf("CSV export = %d rows, %v", len(csvRows), err)
	}
	column := -1
	for i, name := range csvRows[0] {
		if name == "usage_total_mismatch" {
			column = i
		}
	}
	if column < 0 {
		t.Fatal("CSV mismatch header missing")
	}
	for _, row := range csvRows[1:] {
		if row[column] != "true" {
			t.Fatalf("CSV filtered marker = %q", row[column])
		}
	}

	adminRoutes := &logAdminTestRegistrar{}
	userRoutes := &logUserTestRegistrar{}
	stewardRoutes := &logUserTestRegistrar{}
	if err := RegisterAdminRoutes(adminRoutes, f.repo); err != nil {
		t.Fatal(err)
	}
	if err := RegisterUserRoutes(userRoutes, f.repo); err != nil {
		t.Fatal(err)
	}
	if err := RegisterStewardRoutes(stewardRoutes, f.repo, mismatchStewardAuthorizer{}); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"1", "TRUE", "", "true&usage_total_mismatch=false"} {
		resp := callLogAdminHandler(t, adminRoutes.handlers["GET /admin/api/logs"], "/admin/api/logs?usage_total_mismatch="+bad, "", nil)
		if resp.Code != http.StatusBadRequest {
			t.Fatalf("invalid admin filter %q = %d", bad, resp.Code)
		}
	}
	ordinary := callLogUserHandler(t, userRoutes.handlers["GET /api/logs"], logUserOne, "/api/logs?usage_total_mismatch=true", "", nil)
	if ordinary.Code != http.StatusBadRequest || strings.Contains(ordinary.Body.String(), f.selfID) {
		t.Fatalf("ordinary filter = %d %s", ordinary.Code, ordinary.Body.String())
	}
	for _, actorID := range []int64{6} {
		resp := callLogUserHandler(t, stewardRoutes.handlers["GET /api/steward/logs"], actorID, "/api/steward/logs?usage_total_mismatch=true", "", nil)
		if resp.Code != http.StatusOK {
			t.Fatalf("steward %d filter = %d %s", actorID, resp.Code, resp.Body.String())
		}
		var page Page[StewardLogRow]
		if err := json.Unmarshal(resp.Body.Bytes(), &page); err != nil || len(page.Data) != 22 {
			t.Fatalf("steward %d HTTP page = %d, %v", actorID, len(page.Data), err)
		}
	}
	denied := callLogUserHandler(t, stewardRoutes.handlers["GET /api/steward/logs"], 5, "/api/steward/logs?usage_total_mismatch=true", "", nil)
	if denied.Code != http.StatusForbidden || strings.Contains(denied.Body.String(), f.selfID) {
		t.Fatalf("ordinary steward read = %d %s", denied.Code, denied.Body.String())
	}
}
