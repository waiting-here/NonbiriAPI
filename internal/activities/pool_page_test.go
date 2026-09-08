package activities

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

func seedClosedPoolPages(t *testing.T, f *activityFixture, count int) {
	t.Helper()
	tx, err := f.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`PRAGMA defer_foreign_keys=ON`); err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(`WITH RECURSIVE n(i) AS (VALUES(1) UNION ALL SELECT i+1 FROM n WHERE i<?)
INSERT INTO credit_accounts(kind,code,balance_sign,balance_mag,created_at,updated_at)
SELECT 'pool','pool:'||printf('pol_%021dA',i),0,zeroblob(16),i*604800,i*604800+86400 FROM n`, count)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(`WITH RECURSIVE n(i) AS (VALUES(1) UNION ALL SELECT i+1 FROM n WHERE i<?)
INSERT INTO shared_pools(id,pool_type,period_id,account_id,state,revision,created_at,closed_at)
SELECT printf('pol_%021dA',i),'thursday',printf('thu_%021dA',i),a.id,'closed',1,0,i*604800+86400
FROM n JOIN credit_accounts a ON a.code='pool:'||printf('pol_%021dA',i)`, count)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(`WITH RECURSIVE n(i) AS (VALUES(1) UNION ALL SELECT i+1 FROM n WHERE i<?)
INSERT INTO thursday_periods(id,period_key,state,revision,opens_at,closes_at,entry_milli,per_user_limit,
platform_bp,welfare_bp,next_pool_bp,current_pool_id,next_pool_id,ledger_rows_remaining,frozen_pool_mag,
frozen_contribution_count,eligible_contribution_count,platform_cut_mag,welfare_cut_mag,next_cut_mag,payout_total_mag,rollover_mag,created_at,started_settlement_at,terminal_at)
SELECT printf('thu_%021dA',i),date(i*604800,'unixepoch'),'settled',1,i*604800,i*604800+86400,1,1,0,0,0,
printf('pol_%021dA',i),(SELECT id FROM shared_pools WHERE pool_type='thursday' AND period_id IS NULL),
zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),i*604800,i*604800+86400,i*604800+86400 FROM n`, count)
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestPoolNumberedPagesCountWindowAndStableOrder(t *testing.T) {
	f := newActivityFixture(t, 8_000_000_000)
	const count = 10017
	seedClosedPoolPages(t, f, count)
	start := time.Now()
	for _, size := range []int{10, 20, 50, 100} {
		requested := pagination.Request{Page: pagination.MaxPage, Size: size}
		page, err := f.repository.ListPools(context.Background(), PoolListQuery{AdminID: f.adminID, State: PoolStateClosed, PoolType: PoolTypeThursday, Page: &requested})
		if err != nil {
			t.Fatal(err)
		}
		want, _, _ := requested.Window(count)
		if page.Pagination == nil || *page.Pagination != want || page.NextCursor != nil || len(page.Data) != count%size || page.Data[len(page.Data)-1].ID != fmt.Sprintf("pol_%021dA", count) {
			t.Fatalf("pool window=%+v rows=%d", page.Pagination, len(page.Data))
		}
		for _, pool := range page.Data {
			if pool.Balance != "0" || pool.State != PoolStateClosed || pool.PeriodID == nil {
				t.Fatalf("pool projection=%+v", pool)
			}
		}
	}
	t.Logf("%d closed pools, four numbered windows: %s", count, time.Since(start))
	rows, err := f.store.DB().Query(`EXPLAIN QUERY PLAN SELECT id FROM shared_pools WHERE state='closed' ORDER BY created_at,id LIMIT 20 OFFSET 10000`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	plan := ""
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan += detail + ";"
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan, "idx_shared_pools_created") || strings.Contains(plan, "TEMP B-TREE") {
		t.Fatalf("unindexed pool window: %s", plan)
	}
}

type deniedPoolPageAuth struct {
	calls int
	actor int64
}

func (a *deniedPoolPageAuth) AuthorizeAdmin(_ context.Context, _ *sql.Tx, actor int64) error {
	a.calls++
	a.actor = actor
	return authz.ErrForbidden
}

func TestPoolPageHTTPStrictParsingAndFinalAuthorization(t *testing.T) {
	f := newActivityFixture(t, 1_800_400_000)
	service, _ := NewService(ServiceConfig{Repository: f.repository})
	api := httpAPI{service: service}
	request := func(query string, adminID int64) *httptest.ResponseRecorder {
		r := httptest.NewRecorder()
		api.listPools(r, httptest.NewRequest(http.MethodGet, routeAdminPools+"?"+query, nil), AdminPrincipal{UserID: adminID})
		return r
	}
	for _, size := range []int{10, 20, 50, 100} {
		got := request(fmt.Sprintf("state=closed&page=999&page_size=%d", size), f.adminID)
		var page Page[Pool]
		if got.Code != 200 || json.Unmarshal(got.Body.Bytes(), &page) != nil {
			t.Fatalf("empty pool page: %d %s", got.Code, got.Body)
		}
		want, _, _ := (pagination.Request{Page: 1, Size: size}).Window(0)
		if page.Pagination == nil || *page.Pagination != want || len(page.Data) != 0 || page.NextCursor != nil {
			t.Fatalf("empty pool metadata: %+v", page)
		}
	}
	for _, query := range []string{"page=0", "page=01", "page=-1", "page=2147483648", "page_size=5", "page_size=20&page_size=50", "page=1&cursor=", "page=1&limit=1", "page=1&unexpected=1", "page=1&pool_type=bad", "page=1&state=bad"} {
		if got := request(query, f.adminID); got.Code != 400 {
			t.Fatalf("invalid %s: %d %s", query, got.Code, got.Body)
		}
	}
	if got := request("page=1", 0); got.Code != 401 {
		t.Fatalf("missing admin=%d", got.Code)
	}
	auth := &deniedPoolPageAuth{}
	f.repository.adminFinalAuth = auth
	for _, query := range []string{"page=1", "limit=20"} {
		got := request(query, f.adminID)
		if got.Code != 403 || strings.Contains(got.Body.String(), "pagination") {
			t.Fatalf("denied pool page: %d %s", got.Code, got.Body)
		}
	}
	if auth.calls != 2 || auth.actor != f.adminID {
		t.Fatalf("final auth=%+v", auth)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	requested := pagination.Default()
	if _, err := f.repository.ListPools(ctx, PoolListQuery{AdminID: f.adminID, Page: &requested}); err == nil {
		t.Fatal("cancelled pool page accepted")
	}
}
