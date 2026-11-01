package logapi

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strings"
	"testing"

	contract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
	"github.com/waiting-here/NonbiriAPI/internal/requestkind"
)

func seedCharityQueryRows(tb testing.TB, database *sql.DB, count int, withLogicalRequests bool) {
	tb.Helper()
	operations := contract.AllOperations()
	var routes strings.Builder
	fmt.Fprintf(&routes, "CASE x%%%d ", 2*len(operations))
	for i, operation := range operations {
		fmt.Fprintf(&routes, "WHEN %d THEN '%s' WHEN %d THEN '%s' ", i, requestkind.ForOperation(operation, true), i+len(operations), requestkind.ForOperation(operation, false))
	}
	routes.WriteString("END")
	if withLogicalRequests {
		_, err := database.Exec(fmt.Sprintf(`WITH RECURSIVE n(x) AS (VALUES(0) UNION ALL SELECT x+1 FROM n WHERE x<%d)
INSERT INTO logical_requests(id,route_kind,model_snapshot,state,attempt_limit,caller_result_class,caller_status,accounting_state,settlement_destination,ledger_rows_remaining,created_at,terminal_at)
SELECT printf('req_%%021dA',x+1),%s,CASE WHEN (x/32)%%100=2 THEN 'provider/literal%%_model' ELSE printf('provider/model-%%03d',(x/32)%%100) END,'terminal',1,'success',200,'none','user',zeroblob(16),1800000000+x/32,1800000001+x/32 FROM n`, count-1, routes.String()))
		if err != nil {
			tb.Fatal(err)
		}
	}
	_, err := database.Exec(fmt.Sprintf(`WITH RECURSIVE n(x) AS (VALUES(0) UNION ALL SELECT x+1 FROM n WHERE x<%d)
INSERT INTO request_logs(id,logical_request_id,started_at,completed_at,model,route_kind,error_diag,caller_result_class,caller_status,status_code)
SELECT x+1,printf('req_%%021dA',x+1),1800000000+x/32,1800000001+x/32,
 CASE WHEN (x/32)%%100=2 THEN 'provider/literal%%_model' ELSE printf('provider/model-%%03d',(x/32)%%100) END,
 %s,printf('%%0256d',x),'success',200,200 FROM n`, count-1, routes.String()))
	if err != nil {
		tb.Fatal(err)
	}
}

func newCharityQueryBenchmarkDatabase(tb testing.TB, count int) *sql.DB {
	tb.Helper()
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		tb.Fatal(err)
	}
	database.SetMaxOpenConns(1)
	tb.Cleanup(func() { _ = database.Close() })
	_, err = database.Exec(`CREATE TABLE request_logs(
id INTEGER PRIMARY KEY,logical_request_id TEXT NOT NULL,started_at INTEGER NOT NULL,
completed_at INTEGER,model TEXT NOT NULL,route_kind TEXT NOT NULL,error_diag TEXT NOT NULL,
caller_result_class TEXT,caller_status INTEGER,status_code INTEGER);
CREATE INDEX idx_request_logs_started ON request_logs(started_at,id);
CREATE INDEX idx_request_logs_retention ON request_logs(completed_at,id);`)
	if err != nil {
		tb.Fatal(err)
	}
	seedCharityQueryRows(tb, database, count, false)
	return database
}

func charityQueryWhere(pattern string, from, to int64) (string, []any) {
	where := `WHERE (l.completed_at IS NULL OR l.completed_at>?)` + charityModelPredicate
	args := []any{int64(1_799_000_000), pattern}
	if from != 0 {
		where += " AND l.started_at>=?"
		args = append(args, from)
	}
	if to != 0 {
		where += " AND l.started_at<?"
		args = append(args, to)
	}
	return where, args
}

func consumeCharityQueryPage(tb testing.TB, database *sql.DB, query string, args []any) []int64 {
	tb.Helper()
	rows, err := database.Query(query, args...)
	if err != nil {
		tb.Fatal(err)
	}
	defer rows.Close()
	ids := []int64{}
	for rows.Next() {
		var id, started int64
		var model, diagnostic string
		if err := rows.Scan(&id, &started, &model, &diagnostic); err != nil {
			tb.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		tb.Fatal(err)
	}
	return ids
}

func BenchmarkLogCharityModelFilter(b *testing.B) {
	database := newCharityQueryBenchmarkDatabase(b, 160_000)
	for _, mode := range []string{"previous_index", "covering_page_index"} {
		ddl := db.CharityLogCoveringIndexSQL
		if mode == "previous_index" {
			ddl = strings.Replace(ddl, ",id,started_at)", ",id)", 1)
		}
		if _, err := database.Exec("DROP INDEX IF EXISTS idx_request_logs_charity_model;" + ddl); err != nil {
			b.Fatal(err)
		}
		b.Run(mode, func(b *testing.B) {
			for _, tc := range []struct {
				name, pattern string
				total         int64
			}{
				{"common", "model-001", 800}, {"broad", "provider/", 80_000}, {"absent", "not-present", 0},
			} {
				b.Run(tc.name, func(b *testing.B) {
					where, args := charityQueryWhere(tc.pattern, 0, 0)
					countSQL := "SELECT COUNT(*) FROM request_logs l " + where
					b.Run("count", func(b *testing.B) {
						for b.Loop() {
							var count int64
							if err := database.QueryRow(countSQL, args...).Scan(&count); err != nil || count != tc.total {
								b.Fatalf("count=%d want=%d err=%v", count, tc.total, err)
							}
						}
					})
					query, pageArgs, metadata, err := logListPageQuery(context.Background(), database, "l.id,l.started_at,l.model,l.error_diag", "", where, args, &pagination.Request{Page: 1, Size: 50}, 50)
					if err != nil {
						b.Fatal(err)
					}
					if metadata.TotalItems != fmt.Sprint(tc.total) {
						b.Fatal(metadata)
					}
					b.Run("page", func(b *testing.B) {
						for b.Loop() {
							if ids := consumeCharityQueryPage(b, database, query, pageArgs); len(ids) != int(min(tc.total, 50)) {
								b.Fatalf("page rows=%d", len(ids))
							}
						}
					})
				})
			}
		})
	}
}

func charityQueryPlan(t *testing.T, database *sql.DB, query string, args []any) string {
	t.Helper()
	rows, err := database.Query("EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan strings.Builder
	for rows.Next() {
		var id, parent, unused int
		var step string
		if err := rows.Scan(&id, &parent, &unused, &step); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&plan, "%d/%d %s\n", id, parent, step)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return plan.String()
}

func TestCharityLogCountAndManagementPageUseCoveringIndex(t *testing.T) {
	fixture := newLifecycleLogDatabase(t)
	database := fixture.store.DB()
	const count = 6400
	seedCharityQueryRows(t, database, count, true)
	var routes int
	if err := database.QueryRow("SELECT COUNT(DISTINCT route_kind) FROM request_logs WHERE route_kind IN " + charityRouteKindsSQL).Scan(&routes); err != nil || routes != 16 {
		t.Fatalf("fixture charity routes=%d err=%v", routes, err)
	}
	for _, tc := range []struct {
		name, pattern  string
		from, to, page int64
	}{
		{"common", "MODEL-001", 0, 0, 1},
		{"broad", "provider/", 0, 0, 999},
		{"absent", "not-present", 0, 0, 999},
		{"literal wildcards", "%_", 0, 0, 1},
		{"date boundaries", "provider/", 1_800_000_100, 1_800_000_101, 1},
		{"date excludes common", "model-001", 1_800_000_102, 1_800_000_199, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			expected := []int64{}
			for id := int64(1); id <= count; id++ {
				x := id - 1
				if x%32 >= 16 {
					continue
				}
				started := 1_800_000_000 + x/32
				model := fmt.Sprintf("provider/model-%03d", (x/32)%100)
				if (x/32)%100 == 2 {
					model = "provider/literal%_model"
				}
				if !strings.Contains(strings.ToLower(model), strings.ToLower(tc.pattern)) || tc.from != 0 && started < tc.from || tc.to != 0 && started >= tc.to {
					continue
				}
				expected = append(expected, id)
			}
			slices.Reverse(expected)
			where, args := charityQueryWhere(tc.pattern, tc.from, tc.to)
			countSQL := "SELECT COUNT(*) FROM request_logs l " + where
			var total int64
			if err := database.QueryRow(countSQL, args...).Scan(&total); err != nil || total != int64(len(expected)) {
				t.Fatalf("count=%d expected=%d err=%v", total, len(expected), err)
			}
			countPlan := charityQueryPlan(t, database, countSQL, args)
			if tc.from == 0 && tc.to == 0 && !strings.Contains(countPlan, "COVERING INDEX idx_request_logs_charity_model") {
				t.Fatal("count lost covering index", countPlan)
			}
			query, pageArgs, metadata, err := logListPageQuery(t.Context(), database, commonListColumns+","+callerIdentityColumns+",l.user_id", callerIdentityJoin, where, args, &pagination.Request{Page: tc.page, Size: 10}, 10)
			if err != nil {
				t.Fatal(err)
			}
			page := min(tc.page, max(int64(1), (total+9)/10))
			requireLogPagination(t, metadata, page, 10, total)
			plan := charityQueryPlan(t, database, query, pageArgs)
			if total > 0 && tc.from == 0 && tc.to == 0 && !strings.Contains(plan, "COVERING INDEX idx_request_logs_charity_model") {
				t.Fatal("management page reads the wide table while filtering", plan)
			}
			if total == 0 && !strings.Contains(query, "LIMIT 0") {
				t.Fatal("empty authorized count still scans its selection")
			}
			rows, err := database.Query(query, pageArgs...)
			if err != nil {
				t.Fatal(err)
			}
			actual := []int64{}
			for rows.Next() {
				var userID sql.NullInt64
				record, _, err := scanManagementCommon(rows, &userID)
				if err != nil {
					rows.Close()
					t.Fatal(err)
				}
				actual = append(actual, record.rowID)
			}
			if err := rows.Err(); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			rows.Close()
			start := min(int((page-1)*10), len(expected))
			want := expected[start:min(start+10, len(expected))]
			if !slices.Equal(actual, want) {
				t.Fatalf("page ids=%v expected=%v", actual, want)
			}
		})
	}
}
