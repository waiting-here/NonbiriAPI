package logapi

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

// This opt-in measurement uses one real SQLite database and consumes every
// projected row. Ordinary tests keep the smaller deterministic plan checks.
func TestLogOperationFilterScale(t *testing.T) {
	if os.Getenv("NONBIRI_LOG_OPERATION_SCALE") != "1" {
		t.Skip("opt-in database scale measurement")
	}
	f := newLifecycleLogDatabase(t)
	database := f.store.DB()
	owner := seedLifecycleLogUser(t, database, "operation-scale-owner", false)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := database.Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec("DROP INDEX idx_request_logs_operation_started")
	previous := 0
	const repeats = 10
	for _, size := range []int{10_000, 100_000} {
		seedOperationScale(t, database, previous+1, size, owner, false)
		previous = size
		baseline := map[string][]int64{}
		for _, indexed := range []bool{false, true} {
			if indexed {
				before := operationAllocatedBytes(t, database)
				start := time.Now()
				exec(db.RequestLogOperationIndexSQL)
				elapsed := time.Since(start)
				after := operationAllocatedBytes(t, database)
				t.Logf("build rows=%d elapsed=%s allocated_delta_bytes=%d", size, elapsed, after-before)
			}
			exec("ANALYZE request_logs")
			for _, operation := range []string{"chat_completions", "responses", "model_discovery"} {
				type queryShape struct {
					name, extra string
					args        []any
				}
				shapes := []queryShape{
					{"all", "", nil},
					{"date", " AND l.started_at>=? AND l.started_at<?", []any{int64(1_800_000_000 + size/20), int64(1_800_000_000 + size/10)}},
					{"owner", " AND l.user_id=?", []any{owner}},
					{"origin", " AND l.origin_user_id=?", []any{owner}},
				}
				if operation == "responses" {
					tx, err := database.BeginTx(t.Context(), &sql.TxOptions{ReadOnly: true})
					if err != nil {
						t.Fatal(err)
					}
					model := "needle-model"
					predicate, modelArgs, err := charityModelFilter(t.Context(), tx, ListFilter{Operation: "responses", CharityModel: &model})
					if err != nil {
						tx.Rollback()
						t.Fatal(err)
					}
					if err = tx.Rollback(); err != nil {
						t.Fatal(err)
					}
					shapes = append(shapes, queryShape{"charity_model", predicate, modelArgs})
				}
				for _, shape := range shapes {
					where := "WHERE (l.completed_at IS NULL OR l.completed_at>?) AND " + logOperationSQL + "=?" + shape.extra
					args := append([]any{int64(1_799_000_000), operation}, shape.args...)
					if shape.name == "charity_model" {
						where = "WHERE (l.completed_at IS NULL OR l.completed_at>?)" + shape.extra
						args = append([]any{int64(1_799_000_000)}, shape.args...)
					}
					countQuery := "SELECT COUNT(*) FROM request_logs l " + where
					var count int64
					start := time.Now()
					for range repeats {
						if err := database.QueryRow(countQuery, args...).Scan(&count); err != nil {
							t.Fatal(err)
						}
					}
					t.Logf("count rows=%d indexed=%v operation=%s shape=%s matches=%d repeat=%d total=%s", size, indexed, operation, shape.name, count, repeats, time.Since(start))
					t.Log("count plan:", charityQueryPlan(t, database, countQuery, args))
					if indexed && operation == "responses" && shape.name == "all" {
						assertOperationQueryDoesNotReadTable(t, database, countQuery, args)
						assertOperationQueryDoesNotReadTable(t, database, "SELECT l.id FROM request_logs l "+where+" ORDER BY l.started_at DESC,l.id DESC LIMIT 20 OFFSET 20", args)
					}
					for _, page := range []int64{1, max(1, (count+19)/20)} {
						var ids []int64
						start = time.Now()
						var query string
						var pageArgs []any
						for range repeats {
							var err error
							query, pageArgs, _, err = logListPageQuery(t.Context(), database, commonListColumns+","+callerIdentityColumns+",l.user_id", callerIdentityJoin, where, args, &pagination.Request{Page: page, Size: 20}, 20)
							if err != nil {
								t.Fatal(err)
							}
							ids = consumeOperationScalePage(t, database, query, pageArgs)
						}
						key := fmt.Sprintf("%s/%s/%d", operation, shape.name, page)
						if !indexed {
							baseline[key] = ids
						} else if !slices.Equal(baseline[key], ids) {
							t.Fatal("index changed page", key)
						}
						t.Logf("full_page rows=%d indexed=%v operation=%s shape=%s page=%d returned=%d repeat=%d count_plus_rows_total=%s", size, indexed, operation, shape.name, page, len(ids), repeats, time.Since(start))
						t.Log("page plan:", charityQueryPlan(t, database, query, pageArgs))
					}
				}
			}
			// Each phase commits; the index pays insert, terminal update and delete
			// maintenance. This is a bounded batch, not a production throughput claim.
			start := time.Now()
			seedOperationScale(t, database, size+1, size+1000, owner, true)
			t.Logf("write rows=%d indexed=%v phase=insert_logical_and_logs batch=1000 elapsed=%s", size, indexed, time.Since(start))
			start = time.Now()
			exec(`UPDATE logical_requests SET state='terminal',terminal_at=created_at+1,caller_result_class='success',caller_status=200 WHERE id>=?`, fmt.Sprintf("req_%021dA", size+1))
			t.Logf("write rows=%d indexed=%v phase=terminal_logical_and_synced_logs batch=1000 elapsed=%s", size, indexed, time.Since(start))
			start = time.Now()
			exec("DELETE FROM request_logs WHERE id>?", size)
			t.Logf("write rows=%d indexed=%v phase=delete_logs batch=1000 elapsed=%s", size, indexed, time.Since(start))
			exec("DELETE FROM logical_requests WHERE id>=?", fmt.Sprintf("req_%021dA", size+1))
		}
		exec("DROP INDEX idx_request_logs_operation_started")
	}
}

func TestLogOperationCharityModelScale(t *testing.T) {
	if os.Getenv("NONBIRI_LOG_OPERATION_SCALE") != "1" {
		t.Skip("opt-in database scale measurement")
	}
	measureOperationCharityModel(t, 100_000, 10)
}

func seedOperationScale(t *testing.T, database *sql.DB, first, last int, owner int64, pending bool) {
	t.Helper()
	tx, err := database.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	// 90% chat, 9% responses and 1% discovery; both inference operations
	// alternate self/charity. Identity selects 10% across every operation.
	route := `CASE WHEN n%100=0 THEN 'model_discovery' WHEN n%10=0 THEN CASE WHEN (n/10)%2=0 THEN 'openai_responses' ELSE 'charity_responses' END ELSE CASE WHEN n%2=0 THEN 'openai_chat_completions' ELSE 'charity_chat_completions' END END`
	state, class, status, terminal := "'terminal'", "'success'", "200", "1800000001+n/10"
	if pending {
		state, class, status, terminal = "'accepted'", "NULL", "NULL", "NULL"
	}
	query := fmt.Sprintf(`WITH RECURSIVE seq(n) AS (VALUES(?) UNION ALL SELECT n+1 FROM seq WHERE n<?)
INSERT INTO logical_requests(id,user_id,route_kind,model_snapshot,state,attempt_limit,caller_result_class,caller_status,accounting_state,settlement_destination,ledger_rows_remaining,created_at,terminal_at)
SELECT printf('req_%%021dA',n),CASE WHEN (n/100)%%10=0 THEN ? END,%s,CASE WHEN n%%1000 IN (17,110) THEN 'needle-model' ELSE 'model' END,%s,1,%s,%s,'none','user',zeroblob(16),1800000000+n/10,%s FROM seq`, route, state, class, status, terminal)
	if _, err = tx.ExecContext(t.Context(), query, first, last, owner); err != nil {
		t.Fatal(err)
	}
	query = `INSERT INTO request_logs(id,logical_request_id,user_id,route_kind,model,started_at,completed_at,caller_result_class,caller_status,status_code,error_diag)
SELECT CAST(substr(id,5,21) AS INTEGER),id,user_id,route_kind,model_snapshot,created_at,terminal_at,caller_result_class,caller_status,COALESCE(caller_status,0),printf('%0256d',created_at) FROM logical_requests WHERE CAST(substr(id,5,21) AS INTEGER) BETWEEN ? AND ?`
	if _, err = tx.ExecContext(t.Context(), query, first, last); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func operationAllocatedBytes(t *testing.T, database *sql.DB) int64 {
	t.Helper()
	var pages, free, size int64
	for query, target := range map[string]*int64{"PRAGMA page_count": &pages, "PRAGMA freelist_count": &free, "PRAGMA page_size": &size} {
		if err := database.QueryRow(query).Scan(target); err != nil {
			t.Fatal(err)
		}
	}
	return (pages - free) * size
}

func consumeOperationScalePage(t *testing.T, database *sql.DB, query string, args []any) []int64 {
	t.Helper()
	rows, err := database.QueryContext(context.Background(), query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	ids := []int64{}
	for rows.Next() {
		var user sql.NullInt64
		record, _, err := scanManagementCommon(rows, &user)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, record.rowID)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		t.Fatal(err)
	}
	return ids
}
