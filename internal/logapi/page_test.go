package logapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

func newNumberedLogFixture(t *testing.T) *logFixture {
	t.Helper()
	f := newLogFixture(t)
	f.mustExec(`ALTER TABLE users ADD COLUMN is_banned INTEGER NOT NULL DEFAULT 0`)
	f.mustExec(`ALTER TABLE users ADD COLUMN banned_until INTEGER`)
	f.mustExec(`INSERT INTO users(id,is_admin,username,discord_id) VALUES(101,0,'caller','caller-identity'),(202,0,'other','other-identity'),(1,1,'admin',NULL)`)
	for id := int64(100); id < 123; id++ {
		f.insertLog(id, logOpaqueID("req_", uint64(id)), logUserOne, "paged", string(RouteOpenAIChat),
			string(ResultSuccess), 201, nil, 1000, 1010, [4]int64{}, 0, 0)
	}
	return f
}

func requireLogPagination(t *testing.T, got *pagination.Metadata, page int64, size int, total int64) {
	t.Helper()
	pages := max(int64(1), (total+int64(size)-1)/int64(size))
	want := pagination.Metadata{Page: strconv.FormatInt(page, 10), PageSize: size,
		TotalItems: strconv.FormatInt(total, 10), TotalPages: strconv.FormatInt(pages, 10)}
	if got == nil || *got != want {
		t.Fatalf("pagination = %+v, want %+v", got, want)
	}
}

func TestLogNumberedListsCountOnlyAuthorizedFilteredRows(t *testing.T) {
	f := newNumberedLogFixture(t)
	ctx, status := context.Background(), 201
	for _, size := range []int{10, 20, 50, 100} {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			filter := ListFilter{Status: &status, Page: &pagination.Request{Page: pagination.MaxPage, Size: size}}
			page, err := f.repo.ListUser(ctx, logUserOne, filter)
			if err != nil {
				t.Fatal(err)
			}
			last := (int64(23) + int64(size) - 1) / int64(size)
			requireLogPagination(t, page.Pagination, last, size, 23)
			wantLength := 23 - int(last-1)*size
			if len(page.Data) != wantLength || page.NextCursor != nil {
				t.Fatalf("page = %+v", page)
			}
			for i, item := range page.Data {
				row, ok := item.(UserSelfLogRow)
				if !ok || row.ID != logOpaqueID("req_", uint64(100+wantLength-1-i)) {
					t.Fatalf("unstable order: %+v", item)
				}
			}
			admin, err := f.repo.ListAdmin(ctx, filter)
			if err != nil {
				t.Fatal(err)
			}
			requireLogPagination(t, admin.Pagination, last, size, 23)
			steward, err := f.repo.ListSteward(ctx, logUserOne, filter, allowLogStewardRead{})
			if err != nil {
				t.Fatal(err)
			}
			requireLogPagination(t, steward.Pagination, last, size, 23)
			if len(admin.Data) != wantLength || len(steward.Data) != wantLength || admin.NextCursor != nil || steward.NextCursor != nil {
				t.Fatal("role page differed")
			}
			requireNoJSONKeys(t, steward, "model", "endpoint_note", "key_note")
			noLogSentinel(t, page, "OTHER-", "RAW-")
		})
	}
	other, err := f.repo.ListUser(ctx, logUserTwo, ListFilter{Page: &pagination.Request{Page: 2, Size: 10}})
	if err != nil {
		t.Fatal(err)
	}
	requireLogPagination(t, other.Pagination, 1, 10, 1)
	if len(other.Data) != 1 || other.Data[0].(UserSelfLogRow).ID != f.otherID {
		t.Fatal("owner boundary changed")
	}
	missing := "not-present"
	empty, err := f.repo.ListUser(ctx, logUserOne, ListFilter{Model: &missing, Page: &pagination.Request{Page: 2, Size: 20}})
	if err != nil {
		t.Fatal(err)
	}
	requireLogPagination(t, empty.Pagination, 1, 20, 0)
	if empty.Data == nil || len(empty.Data) != 0 {
		t.Fatal("empty page must be an array")
	}
	legacy, err := f.repo.ListUser(ctx, logUserOne, ListFilter{Limit: 10})
	if err != nil || legacy.Pagination != nil || legacy.NextCursor == nil {
		t.Fatalf("legacy list = %+v, %v", legacy, err)
	}
}

func TestLogNumberedAttemptsRemainRoleSafeAndIndependentlyPaged(t *testing.T) {
	f := newNumberedLogFixture(t)
	ctx, requestID := context.Background(), logOpaqueID("req_", 100)
	for seq := int64(1); seq <= 23; seq++ {
		f.insertAttempt(100, seq, 202, 302, "https://snapshot.example/v1", "openai-compatible", "upstream", string(ResultResponse), 200, nil, nil, [4]int64{}, 0, 1001, 1002)
	}
	f.mustExec(`UPDATE request_logs SET attempt_count=23 WHERE id=100`)
	for _, size := range []int{10, 20, 50, 100} {
		filter := AttemptFilter{Page: &pagination.Request{Page: 999, Size: size}}
		result, err := f.repo.GetUser(ctx, logUserOne, requestID, filter)
		if err != nil {
			t.Fatal(err)
		}
		detail := result.(UserSelfLogDetail)
		last := (int64(23) + int64(size) - 1) / int64(size)
		requireLogPagination(t, detail.AttemptPagination, last, size, 23)
		if detail.Attempts.Pagination != nil || detail.Attempts.NextCursor != nil {
			t.Fatal("attempt envelope is not backward compatible")
		}
		if len(detail.Attempts.Data) != 23-int(last-1)*size {
			t.Fatal("wrong attempt window")
		}
		for i, attempt := range detail.Attempts.Data {
			if attempt.AttemptSeq != strconv.Itoa(int(last-1)*size+i+1) || attempt.KeyNote != "" || attempt.EndpointNote != "" {
				t.Fatalf("attempt order or owner notes: %+v", attempt)
			}
		}
		admin, err := f.repo.GetAdmin(ctx, requestID, filter)
		if err != nil {
			t.Fatal(err)
		}
		requireLogPagination(t, admin.AttemptPagination, last, size, 23)
		steward, err := f.repo.GetSteward(ctx, logUserOne, requestID, filter, allowLogStewardRead{})
		if err != nil {
			t.Fatal(err)
		}
		requireLogPagination(t, steward.AttemptPagination, last, size, 23)
		requireNoJSONKeys(t, steward, "model", "endpoint_note", "key_note")
		noLogSentinel(t, detail, "OTHER-", "RAW-")
	}
	filter := AttemptFilter{Page: &pagination.Request{Page: 1, Size: 10}}
	if _, err := f.repo.GetUser(ctx, logUserTwo, requestID, filter); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign detail = %v", err)
	}
	// The public charity detail must not even count attempts. Removing the
	// private table makes an accidental COUNT or projection fail this request.
	f.mustExec(`DROP TABLE request_attempts`)
	charity, err := f.repo.GetUser(ctx, logUserOne, f.charityID, filter)
	if err != nil {
		t.Fatal(err)
	}
	requireNoJSONKeys(t, charity, "attempts", "attempt_count", "attempt_pagination", "pagination", "endpoint_key_id")
}

func TestLogNumberedReadsRecheckCurrentAuthorityAndCancellation(t *testing.T) {
	f := newNumberedLogFixture(t)
	ctx, filter := context.Background(), ListFilter{Page: &pagination.Request{Page: 1, Size: 10}}
	attempts := AttemptFilter{Page: filter.Page}
	f.mustExec(`UPDATE users SET is_banned=1 WHERE id=101`)
	if _, err := f.repo.ListUser(ctx, logUserOne, filter); !errors.Is(err, ErrForbidden) {
		t.Fatalf("banned list = %v", err)
	}
	if _, err := f.repo.GetUser(ctx, logUserOne, f.selfID, attempts); !errors.Is(err, ErrForbidden) {
		t.Fatalf("banned detail = %v", err)
	}
	f.mustExec(`UPDATE users SET banned_until=? WHERE id=101`, f.clock.Now().Unix())
	if _, err := f.repo.ListUser(ctx, logUserOne, filter); err != nil {
		t.Fatalf("expired ban: %v", err)
	}
	f.mustExec(`UPDATE users SET is_admin=0 WHERE id=1`)
	if _, err := f.repo.ListAdmin(ctx, filter); !errors.Is(err, ErrForbidden) {
		t.Fatalf("admin list = %v", err)
	}
	if _, err := f.repo.GetAdmin(ctx, f.selfID, attempts); !errors.Is(err, ErrForbidden) {
		t.Fatalf("admin detail = %v", err)
	}
	denied := &logStewardTestAuthorizer{results: []error{ErrForbidden, ErrForbidden}}
	if _, err := f.repo.ListSteward(ctx, logUserOne, filter, denied); !errors.Is(err, ErrForbidden) {
		t.Fatalf("lost L5 list = %v", err)
	}
	if _, err := f.repo.GetSteward(ctx, logUserOne, f.selfID, attempts, denied); !errors.Is(err, ErrForbidden) {
		t.Fatalf("lost L5 detail = %v", err)
	}
	if denied.callCount() != 2 {
		t.Fatal("authority was not checked per read")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := f.repo.ListUser(cancelled, logUserOne, filter); err == nil {
		t.Fatal("cancelled read succeeded")
	}
	f.mustExec(`DELETE FROM users WHERE id=101`)
	if _, err := f.repo.ListUser(ctx, logUserOne, filter); !errors.Is(err, ErrForbidden) {
		t.Fatalf("deleted owner list = %v", err)
	}
}

func TestLogNumberedHTTPStrictProtocolAndEnvelope(t *testing.T) {
	f := newNumberedLogFixture(t)
	users, admins, stewards := &logUserTestRegistrar{}, &logAdminTestRegistrar{}, &logUserTestRegistrar{}
	if err := RegisterUserRoutes(users, f.repo); err != nil {
		t.Fatal(err)
	}
	if err := RegisterAdminRoutes(admins, f.repo); err != nil {
		t.Fatal(err)
	}
	if err := RegisterStewardRoutes(stewards, f.repo, allowLogStewardRead{}); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{"page=0", "page=01", "page=2147483648", "page=1&page=2", "page_size=15", "page=1&cursor=", "page_size=10&limit=10", "page=1&unknown=yes"} {
		user := callLogUserHandler(t, users.handlers["GET /api/logs"], logUserOne, "/api/logs?"+query, "", nil)
		admin := callLogAdminHandler(t, admins.handlers["GET /admin/api/logs"], "/admin/api/logs?"+query, "", nil)
		steward := callLogUserHandler(t, stewards.handlers["GET /api/steward/logs"], logUserOne, "/api/steward/logs?"+query, "", nil)
		if user.Code != 400 || admin.Code != 400 || steward.Code != 400 {
			t.Fatalf("%s: %d/%d/%d", query, user.Code, admin.Code, steward.Code)
		}
	}
	for _, query := range []string{"attempt_page=0", "attempt_page=1&attempt_cursor=", "attempt_page_size=10&attempt_limit=10", "attempt_page=1&attempt_page=2", "page=1"} {
		response := callLogUserHandler(t, users.handlers["GET /api/logs/{id}"], logUserOne, "/api/logs/"+f.selfID+"?"+query, "", map[string]string{"id": f.selfID})
		if response.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d %s", query, response.Code, response.Body)
		}
	}
	response := callLogUserHandler(t, users.handlers["GET /api/logs"], logUserOne, "/api/logs?page=999&page_size=10&status=201", "", nil)
	var body struct {
		Data       []json.RawMessage    `json:"data"`
		NextCursor *string              `json:"next_cursor"`
		Pagination *pagination.Metadata `json:"pagination"`
	}
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &body) != nil {
		t.Fatalf("page response = %d %s", response.Code, response.Body)
	}
	requireLogPagination(t, body.Pagination, 3, 10, 23)
	if body.NextCursor != nil || len(body.Data) != 3 || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("page envelope changed")
	}
	for name, handler := range admins.handlers {
		if strings.Contains(name, "export") {
			response := callLogAdminHandler(t, handler, "/admin/api/logs/export?page=1", "", nil)
			if response.Code != 400 {
				t.Fatalf("export accepted page mode: %s %d", name, response.Code)
			}
		}
	}
}

func TestLogNumberedRetentionAndHeldOnlyKnownID(t *testing.T) {
	f := newLifecycleLogDatabase(t)
	user := seedLifecycleLogUser(t, f.store.DB(), "numbered-retention-owner", false)
	seedLifecycleLogUser(t, f.store.DB(), "", true)
	now := int64(1_800_000_000)
	f.repo.now = func() time.Time { return time.Unix(now, 0) }
	cutoff := now - requestLogRetentionSeconds
	expired, visible := logOpaqueID("req_", 700), logOpaqueID("req_", 701)
	insertLifecycleRequestLog(t, f.store.DB(), expired, user, "success", 200, nil, cutoff-10, cutoff, 10, 0)
	insertLifecycleRequestLog(t, f.store.DB(), visible, user, "success", 200, nil, cutoff, cutoff+1, 1, 0)
	hold := &requestLogHeldReadStub{allow: true}
	if err := f.repo.AttachAdminHeldReadAuthorizer(hold); err != nil {
		t.Fatal(err)
	}
	ctx, filter := context.Background(), ListFilter{Page: &pagination.Request{Page: 99, Size: 10}}
	page, err := f.repo.ListUser(ctx, user, filter)
	if err != nil {
		t.Fatal(err)
	}
	requireLogPagination(t, page.Pagination, 1, 10, 1)
	admin, err := f.repo.ListAdmin(ctx, filter)
	if err != nil {
		t.Fatal(err)
	}
	requireLogPagination(t, admin.Pagination, 1, 10, 1)
	steward, err := f.repo.ListSteward(ctx, user, filter, allowLogStewardRead{})
	if err != nil {
		t.Fatal(err)
	}
	requireLogPagination(t, steward.Pagination, 1, 10, 1)
	if hold.calls != 0 {
		t.Fatal("ordinary list consulted held-only facts")
	}
	attempts := AttemptFilter{Page: filter.Page}
	if _, err := f.repo.GetUser(ctx, user, expired, attempts); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := f.repo.GetSteward(ctx, user, expired, attempts, allowLogStewardRead{}); err != nil {
		t.Fatal(err)
	}
	detail, err := f.repo.GetAdmin(ctx, expired, attempts)
	if err != nil {
		t.Fatal(err)
	}
	requireLogPagination(t, detail.AttemptPagination, 1, 10, 0)
	if hold.calls != 2 {
		t.Fatal("known-ID hold check missing")
	}
	hold.allow = false
	if _, err := f.repo.GetAdmin(ctx, expired, attempts); !errors.Is(err, ErrNotFound) {
		t.Fatalf("released hold = %v", err)
	}
}

func TestLogNumberedReadsAtTenThousandRowsUseOrderedIndex(t *testing.T) {
	f := newLifecycleLogDatabase(t)
	user := seedLifecycleLogUser(t, f.store.DB(), "numbered-scale-owner", false)
	other := seedLifecycleLogUser(t, f.store.DB(), "numbered-scale-other", false)
	seedLifecycleLogUser(t, f.store.DB(), "", true)
	f.repo.now = func() time.Time { return time.Unix(1_800_000_000, 0) }
	_, err := f.store.DB().Exec(`WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<10018)
INSERT INTO logical_requests(id,user_id,route_kind,model_snapshot,state,attempt_limit,caller_result_class,caller_status,
accounting_state,settlement_destination,ledger_rows_remaining,created_at,terminal_at)
SELECT printf('req_%021dA',x),CASE WHEN x=10018 THEN ? ELSE ? END,'openai_chat_completions','safe-model','terminal',1,
'success',200,'none','user',zeroblob(16),1799999000,1799999001 FROM n`, other, user)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.store.DB().Exec(`INSERT INTO request_logs(logical_request_id,user_id,started_at,completed_at,caller_result_class,caller_status,status_code)
SELECT id,user_id,created_at,terminal_at,caller_result_class,caller_status,caller_status FROM logical_requests`)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	for _, size := range []int{10, 20, 50, 100} {
		filter := ListFilter{Page: &pagination.Request{Page: pagination.MaxPage, Size: size}}
		page, err := f.repo.ListUser(context.Background(), user, filter)
		if err != nil {
			t.Fatal(err)
		}
		requireLogPagination(t, page.Pagination, (10017+int64(size)-1)/int64(size), size, 10017)
		if len(page.Data) == 0 || len(page.Data) > size {
			t.Fatal("unbounded user page")
		}
		admin, err := f.repo.ListAdmin(context.Background(), filter)
		if err != nil {
			t.Fatal(err)
		}
		requireLogPagination(t, admin.Pagination, (10018+int64(size)-1)/int64(size), size, 10018)
		steward, err := f.repo.ListSteward(context.Background(), user, filter, allowLogStewardRead{})
		if err != nil {
			t.Fatal(err)
		}
		requireLogPagination(t, steward.Pagination, (10018+int64(size)-1)/int64(size), size, 10018)
	}
	for _, scope := range []string{"WHERE l.user_id=? AND (l.completed_at IS NULL OR l.completed_at>?)", "WHERE (l.completed_at IS NULL OR l.completed_at>?)"} {
		args := []any{int64(1_800_000_000) - requestLogRetentionSeconds}
		index := "idx_request_logs_started"
		if strings.Contains(scope, "user_id") {
			args = append([]any{user}, args...)
			index = "idx_request_logs_user_started"
		}
		query := `SELECT ` + commonListColumns + `,l.user_id FROM request_logs l ` + scope + ` ORDER BY l.started_at DESC,l.id DESC LIMIT 10 OFFSET 10000`
		rows, err := f.store.DB().Query("EXPLAIN QUERY PLAN "+query, args...)
		if err != nil {
			t.Fatal(err)
		}
		var plan strings.Builder
		outerSort := false
		for rows.Next() {
			var id, parent, unused int
			var step string
			if err := rows.Scan(&id, &parent, &unused, &step); err != nil {
				t.Fatal(err)
			}
			fmt.Fprintf(&plan, "%d/%d %s\n", id, parent, step)
			// The per-request charge projection can sort its bounded settlement
			// entries. The long request collection itself must use its index.
			outerSort = outerSort || (parent == 0 && strings.Contains(step, "TEMP B-TREE"))
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		if !strings.Contains(plan.String(), index) || outerSort {
			t.Fatalf("unindexed page: %s", plan.String())
		}
		t.Logf("%s", plan.String())
	}
	t.Logf("four sizes across three roles with 10018 rows: %s", time.Since(started))
}
