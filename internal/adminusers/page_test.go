package adminusers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

func requireAdminPage(t *testing.T, got *pagination.Metadata, page int64, size int, total int64) {
	t.Helper()
	want, _, err := (pagination.Request{Page: page, Size: size}).Window(total)
	if err != nil || got == nil || *got != want {
		t.Fatalf("pagination=%+v want=%+v error=%v", got, want, err)
	}
}

func seedAdminPageScale(t *testing.T, f *adminUsersFixture, count int) {
	t.Helper()
	tx, err := f.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	zero := db.EncodeU128(db.U128{})
	_, err = tx.Exec(`WITH RECURSIVE n(i) AS (VALUES(1) UNION ALL SELECT i+1 FROM n WHERE i<?)
INSERT INTO users(id,discord_id,username,is_admin,donation_credit_mag,total_requests,total_uncached_input_tokens,
total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,total_unknown_usage_requests,revision,created_at,updated_at)
SELECT i+1000,printf('scale-%05d',i),printf('scale-%05d',i),0,?,?,?,?,?,?,?,?,?,? FROM n`, count, zero, zero, zero, zero, zero, zero, zero, zero, adminUsersTestNow, adminUsersTestNow)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(`INSERT INTO credit_accounts(kind,user_id,code,balance_sign,balance_mag,created_at,updated_at)
SELECT 'user',id,NULL,0,?,?,? FROM users WHERE id>1000`, zero, adminUsersTestNow, adminUsersTestNow)
	if err != nil {
		t.Fatal(err)
	}
	for _, base := range []string{`printf('https://scale.example/%05d',id-1000)`, `'https://shared.example/v1'`} {
		_, err = tx.Exec(`INSERT INTO endpoints(user_id,connector_type,base_url,note,enabled,revision,created_at,updated_at)
SELECT id,'openai-compatible',`+base+`,'private note',1,1,?,? FROM users WHERE id>1000`, adminUsersTestNow, adminUsersTestNow)
		if err != nil {
			t.Fatal(err)
		}
	}
	_, err = tx.Exec(`WITH RECURSIVE n(i) AS (VALUES(0) UNION ALL SELECT i+1 FROM n WHERE i<?-1)
INSERT INTO site_activity_daily(day,product_active,api_requests,uncached_input_tokens,cache_write_input_tokens,
cache_read_input_tokens,output_tokens,checkins,console_writes,game_active,game_rounds,distinct_product_users,updated_at)
SELECT i*86400,1,?,?,?,?,?,?,?,1,?,CASE WHEN i=0 THEN X'00000000000000000000000000000004' ELSE X'00000000000000000000000000000005' END,? FROM n`, count, zero, zero, zero, zero, zero, zero, zero, zero, adminUsersTestNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(`UPDATE site_config SET value='1' WHERE key='activities_enabled'`); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestAdminNumberedPagesFullCollectionsAndNestedScale(t *testing.T) {
	f := newAdminUsersFixture(t)
	const total = 10017
	seedAdminPageScale(t, f, total)
	f.seedEndpoint(f.adminID, "https://shared.example/v1", true, 1)
	f.seedEndpoint(1001, "https://shared.example/v1", false, 2)
	started := time.Now()
	for _, size := range []int{10, 20, 50, 100} {
		requested := pagination.Request{Page: pagination.MaxPage, Size: size}
		wantCount := total % size
		users, err := f.service.ListUsers(context.Background(), f.adminID, UserListQuery{Q: "scale-", Page: &requested})
		if err != nil {
			t.Fatal(err)
		}
		requireAdminPage(t, users.Pagination, requested.Page, size, total)
		if len(users.Data) != wantCount || users.NextCursor != nil || users.Data[len(users.Data)-1].ID != "11017" {
			t.Fatalf("user window size=%d rows=%d", size, len(users.Data))
		}
		usage, err := f.service.UserUsage(context.Background(), f.adminID, PageQuery{Page: &requested})
		if err != nil {
			t.Fatal(err)
		}
		requireAdminPage(t, usage.Pagination, requested.Page, size, total)
		if len(usage.Data) != wantCount || usage.NextCursor != nil || usage.Data[len(usage.Data)-1].UserID != "11017" {
			t.Fatalf("usage window=%+v", usage.Pagination)
		}
		activity, err := f.service.Activity(context.Background(), f.adminID, PageQuery{Page: &requested})
		if err != nil {
			t.Fatal(err)
		}
		requireAdminPage(t, activity.Pagination, requested.Page, size, total)
		if len(activity.Data) != wantCount || activity.NextCursor != nil || activity.Data[len(activity.Data)-1].Day != 0 || activity.Data[len(activity.Data)-1].DistinctProductUsers != nil || activity.Data[0].DistinctProductUsers == nil {
			t.Fatalf("activity privacy/order window=%+v", activity.Pagination)
		}
		overview, err := f.service.EndpointOverview(context.Background(), f.adminID, EndpointOverviewQuery{Q: "scale.example", Page: &requested})
		if err != nil {
			t.Fatal(err)
		}
		requireAdminPage(t, overview.Pagination, requested.Page, size, total)
		if len(overview.Data) != wantCount || overview.NextCursor != nil || overview.Data[len(overview.Data)-1].BaseURL != "https://scale.example/10017" {
			t.Fatalf("overview window=%+v", overview.Pagination)
		}
		nested, err := f.service.EndpointOverviewUsers(context.Background(), f.adminID, "https://shared.example/v1", requested)
		if err != nil {
			t.Fatal(err)
		}
		requireAdminPage(t, nested.Pagination, requested.Page, size, total)
		if len(nested.Data) != wantCount || nested.NextCursor != nil || nested.Data[len(nested.Data)-1].UserID != "11017" {
			t.Fatalf("nested window=%+v", nested.Pagination)
		}
	}
	t.Logf("five complete collections, four page sizes, %d users: %s", total, time.Since(started))
	requested := pagination.Default()
	overview, err := f.service.EndpointOverview(context.Background(), f.adminID, EndpointOverviewQuery{Q: "shared.example", Page: &requested})
	if err != nil {
		t.Fatal(err)
	}
	if len(overview.Data) != 1 {
		t.Fatalf("shared groups=%d", len(overview.Data))
	}
	group := overview.Data[0]
	if group.UserCount != "10017" || group.EndpointCount != "10018" || group.KeyCount != "2" || len(group.Users) != 3 || group.Users[0].EndpointCount != "2" || group.Users[0].EnabledCount != "1" {
		t.Fatalf("full grouping/preview=%+v", group)
	}
	encoded, _ := json.Marshal(overview)
	if strings.Contains(string(encoded), "private") || strings.Contains(string(encoded), "sealed") {
		t.Fatal("private endpoint fields escaped the projection")
	}
	if _, err := f.service.EndpointOverview(context.Background(), f.adminID, EndpointOverviewQuery{Q: "shared.example"}); !errors.Is(err, ErrPayloadTooLarge) {
		t.Fatalf("legacy bound=%v", err)
	}
	for _, query := range []string{
		`SELECT e.base_url FROM endpoints e JOIN users u ON u.id=e.user_id AND u.is_admin=0 GROUP BY e.base_url ORDER BY e.base_url LIMIT 20 OFFSET 10000`,
		`SELECT e.user_id FROM endpoints e JOIN users u ON u.id=e.user_id AND u.is_admin=0 WHERE e.base_url='https://shared.example/v1' GROUP BY e.user_id ORDER BY e.user_id LIMIT 20 OFFSET 10000`,
	} {
		rows, err := f.store.DB().Query(`EXPLAIN QUERY PLAN ` + query)
		if err != nil {
			t.Fatal(err)
		}
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
		rows.Close()
		joined := strings.Join(plan, "; ")
		if !strings.Contains(joined, "idx_endpoints_base_users") || strings.Contains(joined, "TEMP B-TREE") {
			t.Fatalf("unindexed grouped page: %s", joined)
		}
	}
}

func TestAdminNumberedHTTPValidationAndEmptyWindows(t *testing.T) {
	f := newAdminUsersFixture(t)
	for _, entry := range []struct{ route, query string }{
		{routeUsers, "q=missing"}, {routeUsage, "group_by=user"}, {routeActivity, ""}, {routeEndpointOverview, "q=missing"}, {routeEndpointOverviewUsers, "base_url=https%3A%2F%2Fmissing.example"},
	} {
		separator := "?" + entry.query
		if entry.query != "" {
			separator += "&"
		}
		for _, size := range []int{10, 20, 50, 100} {
			target := "https://admin.example" + entry.route + separator + fmt.Sprintf("page=999&page_size=%d", size)
			recorder := f.request(http.MethodGet, entry.route, target, "", 0, "")
			var result struct {
				Data       []json.RawMessage    `json:"data"`
				Pagination *pagination.Metadata `json:"pagination"`
				NextCursor *string              `json:"next_cursor"`
			}
			if recorder.Code != 200 || json.Unmarshal(recorder.Body.Bytes(), &result) != nil {
				t.Fatalf("%s: %d %s", target, recorder.Code, recorder.Body)
			}
			requireAdminPage(t, result.Pagination, 1, size, 0)
			if len(result.Data) != 0 || result.NextCursor != nil {
				t.Fatalf("nonempty missing page: %s", recorder.Body)
			}
		}
		for _, bad := range []string{"page=0", "page=01", "page=2147483648", "page=1&page=2", "page_size=3", "page_size=10&page_size=20", "page=1&cursor=", "page=1&limit=20", "page_size=", "page=+1"} {
			recorder := f.request(http.MethodGet, entry.route, "https://admin.example"+entry.route+separator+bad, "", 0, "")
			if recorder.Code != 400 {
				t.Fatalf("%s %s status=%d", entry.route, bad, recorder.Code)
			}
		}
	}
	for _, params := range []string{"page=1", "page_size=20"} {
		got := f.request(http.MethodGet, routeUsage, "https://admin.example"+routeUsage+"?group_by=site&"+params, "", 0, "")
		if got.Code != 400 {
			t.Fatalf("site accepts numbered query: %d", got.Code)
		}
	}
	for _, query := range []string{"", "base_url=", "base_url=" + url.QueryEscape(strings.Repeat("a", 4097)), "base_url=%ff"} {
		got := f.request(http.MethodGet, routeEndpointOverviewUsers, "https://admin.example"+routeEndpointOverviewUsers+"?"+query, "", 0, "")
		if got.Code != 400 {
			t.Fatalf("invalid exact base URL status=%d", got.Code)
		}
	}
	longURL := "https://long.example/" + strings.Repeat("%2F", 1300)
	got := f.request(http.MethodGet, routeEndpointOverviewUsers, "https://admin.example"+routeEndpointOverviewUsers+"?base_url="+url.QueryEscape(longURL), "", 0, "")
	if got.Code != 200 {
		t.Fatalf("encoded valid URL length rejected: %d %s", got.Code, got.Body)
	}
}

func TestAdminNumberedAuthorizationPrecedesCounting(t *testing.T) {
	f := newAdminUsersFixture(t)
	// The activity count would fail if a denied read reaches domain SQL.
	if _, err := f.store.DB().Exec(`DROP TABLE site_activity_daily`); err != nil {
		t.Fatal(err)
	}
	f.auth.err = authz.ErrForbidden
	for _, entry := range []struct{ route, query string }{
		{routeUsers, ""}, {routeUsage, "group_by=user&"}, {routeActivity, ""}, {routeEndpointOverview, ""}, {routeEndpointOverviewUsers, "base_url=https%3A%2F%2Fmissing.example&"},
	} {
		got := f.request(http.MethodGet, entry.route, "https://admin.example"+entry.route+"?"+entry.query+"page=1", "", 0, "")
		if got.Code != http.StatusForbidden || strings.Contains(got.Body.String(), "pagination") {
			t.Fatalf("denied %s: %d %s", entry.route, got.Code, got.Body)
		}
	}
	if f.auth.calls != 5 {
		t.Fatalf("final authorization calls=%d", f.auth.calls)
	}
	f.auth.err = nil
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	requested := pagination.Default()
	_, err := f.service.ListUsers(ctx, f.adminID, UserListQuery{Page: &requested})
	if err == nil {
		t.Fatal("cancelled page accepted")
	}
	_, err = f.service.EndpointOverviewUsers(context.Background(), 0, "https://example.com", requested)
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("missing admin accepted: %v", err)
	}
}

type snapshotPageAdminAuth struct{ now int64 }

func (a snapshotPageAdminAuth) AuthorizeAdmin(ctx context.Context, tx *sql.Tx, userID int64) error {
	var admin, banned int
	var until sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT is_admin,is_banned,banned_until FROM users WHERE id=?`, userID).Scan(&admin, &banned, &until); err != nil {
		return authz.ErrUnauthorized
	}
	if admin != 1 || banned == 1 && (!until.Valid || until.Int64 > a.now) {
		return authz.ErrForbidden
	}
	return nil
}

func TestAdminNumberedLiveAuthorityAndBanTimeFiltering(t *testing.T) {
	f := newAdminUsersFixture(t)
	a := f.seedUser("permanent", false)
	b := f.seedUser("expired", false)
	if _, err := f.store.DB().Exec(`UPDATE users SET is_banned=1,banned_until=CASE WHEN id=? THEN ? ELSE NULL END WHERE id IN (?,?)`, b, adminUsersTestNow, a, b); err != nil {
		t.Fatal(err)
	}
	f.service.finalAuth = snapshotPageAdminAuth{now: adminUsersTestNow}
	requested := pagination.Default()
	yes := true
	page, err := f.service.ListUsers(context.Background(), f.adminID, UserListQuery{IsBanned: &yes, Page: &requested})
	if err != nil {
		t.Fatal(err)
	}
	requireAdminPage(t, page.Pagination, 1, 20, 1)
	if len(page.Data) != 1 || page.Data[0].ID != strconv.FormatInt(a, 10) {
		t.Fatal("ban filter/count disagree with decision time")
	}
	if _, err := f.store.DB().Exec(`UPDATE users SET is_banned=1 WHERE id=?`, f.adminID); err != nil {
		t.Fatal(err)
	}
	for _, actorID := range []int64{f.adminID, b, 999999} {
		r := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "https://admin.example"+routeUsers+"?page=1", nil)
		f.registrar.handler(http.MethodGet, routeUsers)(r, req, AdminPrincipal{UserID: actorID})
		if r.Code != 401 && r.Code != 403 {
			t.Fatalf("stale admin received page: %d %s", r.Code, r.Body)
		}
	}
}
