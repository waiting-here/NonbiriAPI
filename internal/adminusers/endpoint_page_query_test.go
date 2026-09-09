package adminusers

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

func endpointQueryPlan(t *testing.T, tx *sql.Tx, query string, args []any) string {
	t.Helper()
	rows, err := tx.QueryContext(context.Background(), `EXPLAIN QUERY PLAN `+query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return strings.Join(plan, "; ")
}

func TestEndpointPagesAggregateOnlySelectedGroups(t *testing.T) {
	f := newAdminUsersFixture(t)
	seedAdminPageScale(t, f, 37)
	tx, err := f.service.beginListRead(context.Background(), f.adminID, true)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	requested := pagination.Request{Page: 3, Size: 10}
	for _, users := range []bool{false, true} {
		selection, args := endpointOverviewGroups, []any{"scale.example", "scale.example", ""}
		if users {
			selection, args = endpointOverviewUserGroups, []any{"https://shared.example/v1"}
		}
		countPlan := endpointQueryPlan(t, tx, `SELECT COUNT(*) FROM (`+selection+`)`, args)
		if strings.Contains(countPlan, "CORRELATED") || strings.Contains(countPlan, "endpoint_keys") || !strings.Contains(countPlan, "idx_endpoints_base_users") {
			t.Fatalf("group count reads children or misses its index: %s", countPlan)
		}
		query, args, _, err := endpointOverviewPageQuery(context.Background(), tx, "scale.example", "", &requested, requested.Size)
		window := "endpoint_page"
		if users {
			query, args, _, err = endpointOverviewUsersPageQuery(context.Background(), tx, "https://shared.example/v1", requested)
			window = "user_page"
		}
		if err != nil {
			t.Fatal(err)
		}
		plan := endpointQueryPlan(t, tx, query, args)
		windowScan, childRead := strings.Index(plan, "SCAN p"), strings.Index(plan, "CORRELATED")
		if !strings.Contains(plan, "MATERIALIZE "+window) || windowScan < 0 || childRead < windowScan || !strings.Contains(plan[windowScan:], "idx_endpoints_base_users (base_url=?") {
			t.Fatalf("child aggregation is not behind an indexed page window: %s", plan)
		}
		t.Logf("users=%t count=%s; page=%s", users, countPlan, plan)
	}
}

func TestEndpointPagesKeepWholeGroupTotalsAfterOffset(t *testing.T) {
	f := newAdminUsersFixture(t)
	const target = "https://window.example/zz"
	var first, last int64
	for index := 0; index < 21; index++ {
		id := f.seedUser(fmt.Sprintf("window-%02d", index), false)
		if index == 0 {
			first = id
		}
		last = id
		f.seedEndpoint(id, target, index%2 == 0, index%3)
	}
	f.seedEndpoint(first, target, false, 2)
	f.seedEndpoint(f.adminID, target, true, 5)
	for index := 0; index < 10; index++ {
		f.seedEndpoint(first, fmt.Sprintf("https://window.example/%02d", index), true, 3)
	}
	requested := pagination.Request{Page: 2, Size: 10}
	page, err := f.service.EndpointOverview(context.Background(), f.adminID, EndpointOverviewQuery{Q: "window.example", Page: &requested})
	if err != nil {
		t.Fatal(err)
	}
	requireAdminPage(t, page.Pagination, 2, 10, 11)
	if len(page.Data) != 1 || page.NextCursor != nil {
		t.Fatalf("page=%+v", page)
	}
	group := page.Data[0]
	if group.BaseURL != target || group.UserCount != "21" || group.EndpointCount != "22" || group.KeyCount != "23" || len(group.Users) != 3 || group.Users[0].KeyCount != "2" || group.Users[0].EndpointCount != "2" || group.Users[0].EnabledCount != "1" {
		t.Fatalf("window truncated group totals or included an admin: %+v", group)
	}
	requested.Page = 3
	nested, err := f.service.EndpointOverviewUsers(context.Background(), f.adminID, target, requested)
	if err != nil {
		t.Fatal(err)
	}
	requireAdminPage(t, nested.Pagination, 3, 10, 21)
	if len(nested.Data) != 1 || nested.Data[0].UserID != strconv.FormatInt(last, 10) || nested.Data[0].KeyCount != "2" || nested.Data[0].EndpointCount != "1" || nested.Data[0].EnabledCount != "1" {
		t.Fatalf("nested window=%+v", nested)
	}
	legacy, err := f.service.EndpointOverview(context.Background(), f.adminID, EndpointOverviewQuery{Q: "window.example", Limit: 10})
	if err != nil || len(legacy.Data) != 10 || legacy.NextCursor == nil || legacy.Pagination != nil {
		t.Fatalf("legacy first window=%+v error=%v", legacy, err)
	}
	legacy, err = f.service.EndpointOverview(context.Background(), f.adminID, EndpointOverviewQuery{Q: "window.example", Limit: 10, Cursor: *legacy.NextCursor})
	if err != nil || len(legacy.Data) != 1 || legacy.NextCursor != nil || legacy.Pagination != nil {
		t.Fatalf("legacy next window=%+v error=%v", legacy, err)
	}
	if got := legacy.Data[0]; got.BaseURL != target || got.UserCount != group.UserCount || got.EndpointCount != group.EndpointCount || got.KeyCount != group.KeyCount || len(got.Users) != 21 {
		t.Fatalf("legacy window changed the full preview or aggregate: %+v", got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := f.service.EndpointOverview(ctx, f.adminID, EndpointOverviewQuery{Page: &requested}); err == nil {
		t.Fatal("cancelled overview accepted")
	}
	if _, err := f.service.EndpointOverviewUsers(ctx, f.adminID, target, requested); err == nil {
		t.Fatal("cancelled nested page accepted")
	}
}
