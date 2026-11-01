package logapi

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

func TestCharityModelFilterUsesLiteralHistoricalNameAcrossManagementReads(t *testing.T) {
	f := newNumberedLogFixture(t)
	ctx := context.Background()
	const name = "[Public] Channel_12%/Opus"
	// Personal requests with the same name must not appear in this search.
	f.mustExec("UPDATE request_logs SET model=?", name)
	f.mustExec("UPDATE request_logs SET route_kind='charity_embeddings' WHERE logical_request_id=?", f.otherID)
	f.mustExec("UPDATE request_logs SET model='[Public] ChannelX12other/Opus' WHERE logical_request_id=?", f.deletedID)
	f.mustExec("UPDATE request_logs SET user_id=NULL WHERE logical_request_id=?", f.charityID)
	query := "channel_12%/oPus"
	filter := ListFilter{CharityModel: &query}
	rows, err := f.repo.ListAdmin(ctx, filter)
	if err != nil || len(rows.Data) != 2 {
		t.Fatalf("filtered rows = %+v, %v", rows, err)
	}
	for _, row := range rows.Data {
		if row.ID != f.charityID && row.ID != f.otherID {
			t.Fatalf("unrelated request matched: %s", row.ID)
		}
	}
	steward, err := f.repo.ListSteward(ctx, 999, filter, allowLogStewardRead{})
	if err != nil {
		t.Fatal(err)
	}
	requireManagementLogEqual(t, rows, steward)
	exported, err := f.repo.ExportAdmin(ctx, filter)
	if err != nil {
		t.Fatal(err)
	}
	slices.Reverse(exported)
	requireManagementLogEqual(t, rows.Data, exported)
	slices.Reverse(exported)
	stewardExport, err := f.repo.ExportSteward(ctx, 999, filter, allowLogStewardRead{})
	if err != nil {
		t.Fatal(err)
	}
	requireManagementLogEqual(t, exported, stewardExport)
	filter.Page = &pagination.Request{Page: 99, Size: 10}
	page, err := f.repo.ListAdmin(ctx, filter)
	if err != nil {
		t.Fatal(err)
	}
	requireLogPagination(t, page.Pagination, 1, 10, 2)
	filter.Page = nil
	filter.Limit = 1
	first, err := f.repo.ListAdmin(ctx, filter)
	if err != nil || first.NextCursor == nil {
		t.Fatalf("cursor: %+v %v", first, err)
	}
	filter.Cursor = *first.NextCursor
	next, err := f.repo.ListAdmin(ctx, filter)
	if err != nil || len(next.Data) != 1 || next.Data[0].ID == first.Data[0].ID {
		t.Fatalf("next: %+v %v", next, err)
	}
	other := "different"
	filter.CharityModel = &other
	if _, err := f.repo.ListAdmin(ctx, filter); !errors.Is(err, ErrInvalid) {
		t.Fatalf("cursor changed filter: %v", err)
	}
	if _, err := f.repo.ListUser(ctx, logUserOne, ListFilter{CharityModel: &query}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("user filter: %v", err)
	}
}

func TestCharityModelDateAndStatusIntersectionAcrossManagementReads(t *testing.T) {
	f := newNumberedLogFixture(t)
	query := "MODEL_%"
	f.mustExec("UPDATE request_logs SET model='model_%'")
	f.mustExec("UPDATE request_logs SET route_kind='charity_embeddings' WHERE logical_request_id=?", f.otherID)
	for _, tc := range []struct {
		name     string
		from, to int64
		status   int
		want     []string
	}{
		{"exclusive end", 200, 400, 0, []string{f.charityID}},
		{"inclusive start", 400, 401, 0, []string{f.otherID}},
		{"matching status", 200, 401, 200, []string{f.charityID}},
		{"status intersection", 200, 401, 201, nil},
		{"outside range", 600, 700, 200, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			filter := ListFilter{CharityModel: &query, From: &tc.from, To: &tc.to,
				Page: &pagination.Request{Page: 1, Size: 20}}
			if tc.status != 0 {
				filter.Status = &tc.status
			}
			admin, err := f.repo.ListAdmin(t.Context(), filter)
			if err != nil {
				t.Fatal(err)
			}
			ids := make([]string, 0, len(admin.Data))
			for _, row := range admin.Data {
				ids = append(ids, row.ID)
			}
			if !slices.Equal(ids, tc.want) {
				t.Fatalf("filtered ids=%v want=%v", ids, tc.want)
			}
			requireLogPagination(t, admin.Pagination, 1, 20, int64(len(tc.want)))
			steward, err := f.repo.ListSteward(t.Context(), 999, filter, allowLogStewardRead{})
			if err != nil {
				t.Fatal(err)
			}
			requireManagementLogEqual(t, admin, steward)
			filter.Page = nil
			exported, err := f.repo.ExportAdmin(t.Context(), filter)
			if err != nil {
				t.Fatal(err)
			}
			requireManagementLogEqual(t, admin.Data, exported)
			stewardExport, err := f.repo.ExportSteward(t.Context(), 999, filter, allowLogStewardRead{})
			if err != nil {
				t.Fatal(err)
			}
			requireManagementLogEqual(t, exported, stewardExport)
		})
	}
}

func TestCharityModelFilterHTTPListExportAndValidation(t *testing.T) {
	f := newLogFixture(t)
	f.mustExec("UPDATE request_logs SET model='[Public] Opus_5% test' WHERE logical_request_id=?", f.charityID)
	admin := &logAdminTestRegistrar{}
	if err := RegisterAdminRoutes(admin, f.repo); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"", "/export.csv", "/export.json"} {
		path := "/admin/api/logs" + suffix
		response := callLogAdminHandler(t, admin.handlers["GET "+path], path+"?charity_model="+url.QueryEscape("OPUS_5%"), "", nil)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), f.charityID) || strings.Contains(response.Body.String(), f.selfID) {
			t.Fatalf("filter %s: %d %s", path, response.Code, response.Body.String())
		}
		for _, value := range []string{"", strings.Repeat("x", 513), "%ff", "a&charity_model=b"} {
			response := callLogAdminHandler(t, admin.handlers["GET "+path], path+"?charity_model="+value, "", nil)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("invalid filter status = %d", response.Code)
			}
		}
	}
	if _, err := parseListFilter("charity_model=x", "user", false); !errors.Is(err, ErrInvalid) {
		t.Fatalf("user HTTP filter: %v", err)
	}
}
