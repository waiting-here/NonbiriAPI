package logapi

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
	"github.com/waiting-here/NonbiriAPI/internal/requestkind"
)

func logFilterKinds() []RouteKind {
	kinds := []RouteKind{RouteDiscovery}
	for _, operation := range contract.AllOperations() {
		kinds = append(kinds, requestkind.ForOperation(operation, false), requestkind.ForOperation(operation, true))
	}
	return kinds
}

func newOperationFilterFixture(t *testing.T) *logFixture {
	t.Helper()
	f := newNumberedLogFixture(t)
	f.mustExec("DELETE FROM request_logs")
	for i, kind := range logFilterKinds() {
		for j, user := range []int64{logUserOne, logUserOne, logUserTwo} {
			id := int64(1000 + i*3 + j)
			f.insertLog(id, logOpaqueID("req_", uint64(id)), user, "[Public] MODEL_%", string(kind), string(ResultSuccess), 200, nil, 2000, 2001, [4]int64{}, 0, 0)
		}
	}
	return f
}

type routeKindRow struct {
	ID        string    `json:"id"`
	RouteKind RouteKind `json:"route_kind"`
}

func routeKindRows(t *testing.T, value any) []routeKindRow {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var rows []routeKindRow
	if err = json.Unmarshal(body, &rows); err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestOperationFilterAllOperationsAcrossRolesAndExports(t *testing.T) {
	f := newOperationFilterFixture(t)
	for _, operation := range append(contract.AllOperations(), OperationDiscovery) {
		t.Run(string(operation), func(t *testing.T) {
			filter := ListFilter{Operation: operation, Page: &pagination.Request{Page: 99, Size: 10}}
			user, err := f.repo.ListUser(t.Context(), logUserOne, filter)
			if err != nil {
				t.Fatal(err)
			}
			wantUser, wantAdmin := int64(4), int64(6)
			if operation == OperationDiscovery {
				wantUser, wantAdmin = 2, 3
			}
			requireLogPagination(t, user.Pagination, 1, 10, wantUser)
			admin, err := f.repo.ListAdmin(t.Context(), filter)
			if err != nil {
				t.Fatal(err)
			}
			requireLogPagination(t, admin.Pagination, 1, 10, wantAdmin)
			steward, err := f.repo.ListSteward(t.Context(), 999, filter, allowLogStewardRead{})
			if err != nil {
				t.Fatal(err)
			}
			requireManagementLogEqual(t, admin, steward)
			for _, row := range routeKindRows(t, admin.Data) {
				if !operationMatches(row.RouteKind, operation) {
					t.Fatal("another kind leaked", row)
				}
			}
			requireNoJSONKeys(t, user, "user_id", "caller_identity", "origin_user_id")
			filter.Page = nil
			userExport, err := f.repo.ExportUser(t.Context(), logUserOne, filter)
			if err != nil {
				t.Fatal(err)
			}
			adminExport, err := f.repo.ExportAdmin(t.Context(), filter)
			if err != nil {
				t.Fatal(err)
			}
			stewardExport, err := f.repo.ExportSteward(t.Context(), 999, filter, allowLogStewardRead{})
			if err != nil {
				t.Fatal(err)
			}
			slices.Reverse(userExport)
			slices.Reverse(adminExport)
			slices.Reverse(stewardExport)
			if !slices.Equal(routeKindRows(t, userExport), routeKindRows(t, user.Data)) || !slices.Equal(routeKindRows(t, adminExport), routeKindRows(t, admin.Data)) || !slices.Equal(routeKindRows(t, stewardExport), routeKindRows(t, admin.Data)) {
				t.Fatal("export filter or order differs from list")
			}
		})
	}
}

func TestOperationFilterBindsEveryRoleCursorAndIntersectsOtherFilters(t *testing.T) {
	f := newOperationFilterFixture(t)
	for _, role := range []string{"user", "admin", "steward"} {
		t.Run(role, func(t *testing.T) {
			read := func(filter ListFilter) ([]routeKindRow, *string, error) {
				switch role {
				case "user":
					page, err := f.repo.ListUser(t.Context(), logUserOne, filter)
					return routeKindRows(t, page.Data), page.NextCursor, err
				case "admin":
					page, err := f.repo.ListAdmin(t.Context(), filter)
					return routeKindRows(t, page.Data), page.NextCursor, err
				default:
					page, err := f.repo.ListSteward(t.Context(), 999, filter, allowLogStewardRead{})
					return routeKindRows(t, page.Data), page.NextCursor, err
				}
			}
			filter := ListFilter{Operation: "responses", Limit: 1}
			first, next, err := read(filter)
			if err != nil || len(first) != 1 || next == nil {
				t.Fatal(first, next, err)
			}
			filter.Cursor = *next
			second, _, err := read(filter)
			if err != nil || len(second) != 1 || second[0] == first[0] || !operationMatches(second[0].RouteKind, filter.Operation) {
				t.Fatal("same-filter cursor failed", second, err)
			}
			seen := map[string]bool{first[0].ID: true}
			sawSelf, sawCharity := first[0].RouteKind.IsSelf(), first[0].RouteKind.IsCharity()
			cursor := next
			for cursor != nil {
				filter.Cursor = *cursor
				rows, following, err := read(filter)
				if err != nil || len(rows) != 1 || seen[rows[0].ID] || !operationMatches(rows[0].RouteKind, filter.Operation) {
					t.Fatal("merged cursor traversal", rows, err)
				}
				seen[rows[0].ID] = true
				sawSelf = sawSelf || rows[0].RouteKind.IsSelf()
				sawCharity = sawCharity || rows[0].RouteKind.IsCharity()
				cursor = following
				if len(seen) > 6 {
					t.Fatal("cursor did not terminate")
				}
			}
			want := 6
			if role == "user" {
				want = 4
			}
			if len(seen) != want || !sawSelf || !sawCharity {
				t.Fatal("cursor lost one service path", seen)
			}
			filter.Cursor = *next
			for _, changed := range []contract.Operation{"", OperationDiscovery, "embeddings"} {
				filter.Operation = changed
				if _, _, err := read(filter); !errors.Is(err, ErrInvalid) {
					t.Fatal("cursor crossed request kind", changed, err)
				}
			}
		})
	}
	from, to, status := int64(2000), int64(2001), 200
	model := "model_%"
	filter := ListFilter{Operation: "responses", CharityModel: &model, From: &from, To: &to, Status: &status, Phase: "handler", UserID: new(logUserOne), Page: &pagination.Request{Page: 99, Size: 10}}
	page, err := f.repo.ListAdmin(t.Context(), filter)
	if err != nil {
		t.Fatal(err)
	}
	requireLogPagination(t, page.Pagination, 1, 10, 2)
	filter.Page = nil
	for _, changed := range []contract.Operation{OperationDiscovery} {
		filter.Operation = changed
		rows, err := f.repo.ExportAdmin(t.Context(), filter)
		if err != nil || len(rows) != 0 {
			t.Fatal("non-charity kind and charity model did not intersect", rows, err)
		}
	}
	filter = ListFilter{Operation: "responses", CharityModel: &model, Page: &pagination.Request{Page: 1, Size: 10}}
	page, err = f.repo.ListAdmin(t.Context(), filter)
	if err != nil {
		t.Fatal(err)
	}
	requireLogPagination(t, page.Pagination, 1, 10, 3)
	filter.Status = new(404)
	page, err = f.repo.ListAdmin(t.Context(), filter)
	if err != nil {
		t.Fatal(err)
	}
	requireLogPagination(t, page.Pagination, 1, 10, 0)
	if page.Data == nil || len(page.Data) != 0 {
		t.Fatal("no matches must return empty array")
	}
}

func TestOperationFilterHTTPClosedValuesAndExportParity(t *testing.T) {
	f := newOperationFilterFixture(t)
	user, steward := &logUserTestRegistrar{}, &logUserTestRegistrar{}
	admin := &logAdminTestRegistrar{}
	if err := RegisterUserRoutes(user, f.repo); err != nil {
		t.Fatal(err)
	}
	if err := RegisterStewardRoutes(steward, f.repo, allowLogStewardRead{}); err != nil {
		t.Fatal(err)
	}
	if err := RegisterAdminRoutes(admin, f.repo); err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"user", "admin", "steward"} {
		for _, suffix := range []string{"", "/export.json", "/export.csv"} {
			path := "/api/logs" + suffix
			call := func(query string) *httptest.ResponseRecorder {
				if role == "admin" {
					path = "/admin/api/logs" + suffix
					return callLogAdminHandler(t, admin.handlers["GET "+path], path+"?"+query, "", nil)
				}
				registrar := user
				if role == "steward" {
					path, registrar = "/api/steward/logs"+suffix, steward
				}
				return callLogUserHandler(t, registrar.handlers["GET "+path], logUserOne, path+"?"+query, "", nil)
			}
			response := call("operation=responses")
			if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "charity_responses") || !strings.Contains(response.Body.String(), "openai_responses") || strings.Contains(response.Body.String(), "model_discovery") || strings.Contains(response.Body.String(), "openai_chat_completions") {
				t.Fatalf("%s%s filter: %d %s", role, suffix, response.Code, response.Body)
			}
			if response := call("route_kind=charity_responses"); response.Code != http.StatusBadRequest {
				t.Fatal("route_kind query alias accepted", response.Code)
			}
			for _, invalid := range []string{"", "unknown", "Responses", "responses&operation=responses", "responses&operation=model_discovery", "%ff", url.QueryEscape(" responses"), url.QueryEscape("responses\x00"), "charity_responses", "openai_responses"} {
				if response := call("operation=" + invalid); response.Code != http.StatusBadRequest {
					t.Fatalf("%s%s invalid %q: %d", role, suffix, invalid, response.Code)
				}
			}
		}
		if _, err := normalizeListFilter(ListFilter{Operation: "unknown"}, role); !errors.Is(err, ErrInvalid) {
			t.Fatal("repository accepted unknown kind", role, err)
		}
	}
}

func operationMatches(kind RouteKind, operation contract.Operation) bool {
	if operation == OperationDiscovery {
		return kind == RouteDiscovery
	}
	return kind.Operation() == operation
}

func TestOperationCharityModelBoundedLookupKeepsExactKind(t *testing.T) {
	for _, tc := range []struct {
		names    int
		pattern  string
		want     int
		complete bool
	}{
		{100, "historic", 100, true}, {4097, "historic-04096", 1, false},
	} {
		t.Run(fmt.Sprintf("%d", tc.names), func(t *testing.T) {
			database := indexedCharityQueryDatabase(t, 1)
			if _, err := database.Exec(`DELETE FROM request_logs;
WITH RECURSIVE n(x) AS (VALUES(0) UNION ALL SELECT x+1 FROM n WHERE x<?),
kinds(kind,offset) AS (VALUES('charity_responses',0),('charity_embeddings',10000),('openai_responses',20000))
INSERT INTO request_logs(id,logical_request_id,started_at,completed_at,model,route_kind,error_diag)
SELECT x+1+offset,printf('req_%021dA',x+1+offset),1800000000,1800000001,printf('historic-%05d',x),kind,'' FROM n CROSS JOIN kinds`, tc.names-1); err != nil {
				t.Fatal(err)
			}
			tx, err := database.BeginTx(t.Context(), &sql.TxOptions{ReadOnly: true})
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			_, complete, err := matchingCharityModelNames(t.Context(), tx, tc.pattern, "charity_responses")
			if err != nil || complete != tc.complete {
				t.Fatal("bounded lookup completion", complete, err)
			}
			predicate, args, err := charityModelFilter(t.Context(), tx, ListFilter{Operation: "responses", CharityModel: &tc.pattern})
			if err != nil {
				t.Fatal(err)
			}
			ids := queryLogIDs(t, tx, "SELECT l.id FROM request_logs l WHERE 1=1"+predicate+" ORDER BY l.id", args...)
			if len(ids) != tc.want {
				t.Fatal("fallback count", len(ids), tc.want)
			}
			for _, id := range ids {
				if id > int64(tc.names) {
					t.Fatal("fallback admitted another kind", id)
				}
			}
		})
	}
}

func TestOperationCharityModelCursorAndExportKeepExactKind(t *testing.T) {
	f := newOperationFilterFixture(t)
	model := "model_%"
	for _, role := range []string{"admin", "steward"} {
		filter := ListFilter{Operation: "responses", CharityModel: &model, Limit: 1}
		ids := []routeKindRow{}
		for {
			var rows []routeKindRow
			var next *string
			var err error
			if role == "admin" {
				var page Page[AdminLogRow]
				page, err = f.repo.ListAdmin(t.Context(), filter)
				rows, next = routeKindRows(t, page.Data), page.NextCursor
			} else {
				var page Page[StewardLogRow]
				page, err = f.repo.ListSteward(t.Context(), 999, filter, allowLogStewardRead{})
				rows, next = routeKindRows(t, page.Data), page.NextCursor
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(rows) != 1 || rows[0].RouteKind != "charity_responses" {
				t.Fatal("combined cursor admitted another kind", rows)
			}
			ids = append(ids, rows...)
			if next == nil {
				break
			}
			if len(ids) > 3 {
				t.Fatal("combined cursor did not terminate")
			}
			filter.Cursor = *next
		}
		if len(ids) != 3 {
			t.Fatal("combined cursor lost rows", ids)
		}
		filter.Cursor = ""
		filter.Limit = 0
		var exported []AdminLogRow
		var err error
		if role == "admin" {
			exported, err = f.repo.ExportAdmin(t.Context(), filter)
		} else {
			exported, err = f.repo.ExportSteward(t.Context(), 999, filter, allowLogStewardRead{})
		}
		if err != nil {
			t.Fatal(err)
		}
		slices.Reverse(exported)
		if !slices.Equal(ids, routeKindRows(t, exported)) {
			t.Fatal("combined export differs", role)
		}
	}
}
