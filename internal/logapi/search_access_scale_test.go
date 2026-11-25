package logapi

import (
	"database/sql"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

const errorIndexName = "idx_request_logs_caller_error_started"

// Run on a private file using the complete project schema, log sync triggers,
// journal settings and engine. Index candidates share the same retained rows.
func TestLogSearchAccessScale(t *testing.T) {
	if os.Getenv("NONBIRI_LOG_SEARCH_SCALE") != "1" {
		t.Skip("opt-in SQLite log search measurements")
	}
	for _, size := range []int{10_000, 100_000} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			f := newLifecycleLogDatabase(t)
			database := f.store.DB()
			owner := seedLifecycleLogUser(t, database, "search-scale-owner", false)
			seedLifecycleLogUser(t, database, "", true)
			f.repo.readDB = f.store.ReadDB()
			f.repo.now = func() time.Time { return time.Unix(1_800_000_000+int64(size/100)+requestLogRetentionSeconds, 0) }
			var sqliteVersion string
			var stat4 int
			if err := database.QueryRow("SELECT sqlite_version(),sqlite_compileoption_used('ENABLE_STAT4')").Scan(&sqliteVersion, &stat4); err != nil {
				t.Fatal(err)
			}
			t.Logf("SQLite=%s stat4=%d", sqliteVersion, stat4)
			searchExec(t, database, "DROP INDEX IF EXISTS "+errorIndexName)
			insertSearchHistory(t, database, 1, size, owner)
			searchExec(t, database, finishSearchHistory, 1, size)
			baselines := make(map[string][]searchHit)
			for _, withStats := range []bool{false, true} {
				const create = "CREATE INDEX " + errorIndexName + " ON request_logs("
				const partial = ") WHERE caller_error_code IS NOT NULL"
				for _, candidate := range []struct{ name, ddl string }{
					{"baseline", ""},
					{"ordered", create + "caller_error_code,started_at,id" + partial},
					{"covering", create + "caller_error_code,started_at,id,completed_at" + partial},
					{"model_covering", db.RequestLogCallerErrorIndexSQL},
				} {
					mode := candidate.name
					if candidate.ddl != "" {
						start := time.Now()
						searchExec(t, database, candidate.ddl)
						var bytes int64
						build := time.Since(start)
						if err := database.QueryRow("SELECT sum(pgsize) FROM dbstat WHERE name=?", errorIndexName).Scan(&bytes); err != nil {
							t.Fatal(err)
						}
						t.Logf("build mode=%s elapsed=%s bytes=%d", mode, build, bytes)
					}
					if withStats {
						start := time.Now()
						searchExec(t, database, "ANALYZE request_logs")
						t.Logf("analyze rows=%d mode=%s elapsed=%s", size, mode, time.Since(start))
					}
					measureSearchReads(t, database, size, owner, mode, withStats, baselines)
					if !withStats {
						measureSearchWrites(t, database, size, owner, mode)
						if size == 100_000 && mode != "ordered" {
							measureSearchMixed(t, f, size, owner, mode)
						}
					}
					if mode != "baseline" {
						searchExec(t, database, "DROP INDEX "+errorIndexName)
					}
				}
			}
		})
	}
}

func searchExec(t *testing.T, database *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := database.ExecContext(t.Context(), query, args...); err != nil {
		t.Fatal(err)
	}
}

func insertSearchHistory(t *testing.T, database *sql.DB, first, last int, owner int64) {
	t.Helper()
	tx, err := database.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(t.Context(), `WITH RECURSIVE s(n) AS (VALUES(?) UNION ALL SELECT n+1 FROM s WHERE n<?)
INSERT INTO logical_requests(id,user_id,route_kind,model_snapshot,state,attempt_limit,accounting_state,settlement_destination,ledger_rows_remaining,created_at)
SELECT printf('req_%021dA',n),CASE WHEN n%5<>0 THEN ? END,
 CASE n%3 WHEN 0 THEN 'openai_chat_completions' WHEN 1 THEN 'charity_chat_completions' ELSE 'charity_responses' END,
 CASE WHEN n%1009=0 THEN 'cold-model' ELSE 'hot-model' END,'accepted',1,'none','user',zeroblob(16),1800000000+n/10 FROM s`, first, last, owner)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.ExecContext(t.Context(), `INSERT INTO request_logs(id,logical_request_id,user_id,route_kind,model,started_at,error_diag)
SELECT CAST(substr(id,5,21) AS INTEGER),id,user_id,route_kind,model_snapshot,created_at,printf('%0256d',created_at)
FROM logical_requests WHERE id BETWEEN printf('req_%021dA',?) AND printf('req_%021dA',?)`, first, last)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

// Exercise pending-to-terminal sync, with 1/701 left pending, 1/997 rare
// errors, and roughly 1/17 frequent errors.
const finishSearchHistory = `UPDATE logical_requests SET state='terminal',terminal_at=created_at+1,
caller_error_code=CASE WHEN CAST(substr(id,5,21) AS INTEGER)%997=0 THEN 'invalid_request' WHEN CAST(substr(id,5,21) AS INTEGER)%17=0 THEN 'upstream' END,
caller_result_class=CASE WHEN CAST(substr(id,5,21) AS INTEGER)%997=0 OR CAST(substr(id,5,21) AS INTEGER)%17=0 THEN 'failed' ELSE 'success' END,
caller_status=CASE WHEN CAST(substr(id,5,21) AS INTEGER)%997=0 THEN 400 WHEN CAST(substr(id,5,21) AS INTEGER)%17=0 THEN 502 ELSE 200 END
WHERE id BETWEEN printf('req_%021dA',?) AND printf('req_%021dA',?) AND CAST(substr(id,5,21) AS INTEGER)%701<>0`

func measureSearchWrites(t *testing.T, database *sql.DB, size int, owner int64, mode string) {
	t.Helper()
	const batch = 200
	for run := range 5 {
		start := time.Now()
		insertSearchHistory(t, database, size+1, size+batch, owner)
		insert := time.Since(start)
		start = time.Now()
		searchExec(t, database, finishSearchHistory, size+1, size+batch)
		finish := time.Since(start)
		start = time.Now()
		searchExec(t, database, "DELETE FROM request_logs WHERE id>?", size)
		remove := time.Since(start)
		searchExec(t, database, "DELETE FROM logical_requests WHERE id>printf('req_%021dA',?)", size)
		t.Logf("write mode=%s run=%d batch=%d insert=%s finish=%s delete=%s", mode, run, batch, insert, finish, remove)
	}
}

type searchHit struct {
	id        int64
	timestamp sql.NullInt64 // page start or collected completion time
}

func measureSearchReads(t *testing.T, database *sql.DB, size int, owner int64, mode string, stats bool, baselines map[string][]searchHit) {
	t.Helper()
	const epoch = 1_800_000_000
	where := " FROM request_logs l WHERE (l.completed_at IS NULL OR l.completed_at>?)"
	charity := " AND l.route_kind IN " + charityRouteKindsSQL + " AND l.model IN (?)"
	for _, shape := range []struct {
		name, predicate string
		args            []any
	}{
		{"broad", "", nil},
		{"time", " AND l.started_at>=? AND l.started_at<?", []any{epoch + size/20, epoch + size/20 + 10}},
		{"rare", " AND l.caller_error_code=?", []any{"invalid_request"}},
		{"absent", " AND l.caller_error_code=?", []any{"unbound_model"}},
		{"frequent", " AND l.caller_error_code=?", []any{"upstream"}},
		{"owner_rare", " AND l.user_id=? AND l.caller_error_code=?", []any{owner, "invalid_request"}},
		{"hot", charity, []any{"hot-model"}},
		{"cold", charity, []any{"cold-model"}},
		{"hot_error_time", charity + " AND l.caller_error_code=? AND l.started_at>=?", []any{"hot-model", "invalid_request", epoch + size/20}},
		{"cold_error_time", charity + " AND l.caller_error_code=? AND l.started_at>=?", []any{"cold-model", "upstream", epoch + size/20}},
	} {
		// Materialize the same exact candidate sets across planner configurations.
		args := append([]any{epoch + size/100}, shape.args...)
		source := where + shape.predicate
		countQuery := "SELECT count(*)" + source
		var count int64
		if err := database.QueryRow(countQuery, args...).Scan(&count); err != nil {
			t.Fatal(err)
		}
		for _, op := range []string{"count", "first", "last", "all"} {
			query := countQuery
			values := slices.Clone(args)
			if op != "count" {
				columns := "l.id,l.started_at"
				if op == "all" {
					columns = "l.id,l.completed_at"
				}
				query = "SELECT " + columns + source + " ORDER BY l.started_at DESC,l.id DESC"
				if op != "all" {
					offset := int64(0)
					if op == "last" && count > 0 {
						offset = (count - 1) / 20 * 20
					}
					query += " LIMIT 20 OFFSET ?"
					values = append(values, offset)
				}
			}
			key := shape.name + "/" + op
			plan := strings.Join(strings.Fields(charityQueryPlan(t, database, query, values)), " ")
			repeats := 5
			if shape.name == "rare" && (op == "count" || op == "first") {
				repeats = 100
			}
			// Drop the SQLite page cache once. The OS file cache is uncontrolled.
			searchExec(t, database, "PRAGMA shrink_memory")
			var initial time.Duration
			var samples []time.Duration
			for run := 0; run <= repeats; run++ {
				start := time.Now()
				var hits []searchHit
				if op == "count" {
					var n int64
					if err := database.QueryRow(countQuery, values...).Scan(&n); err != nil {
						t.Fatal(err)
					}
					hits = []searchHit{{id: n}}
				} else {
					hits = readSearchHits(t, database, query, values)
				}
				elapsed := time.Since(start)
				want, exists := baselines[key]
				if !exists {
					baselines[key] = hits
				} else if !slices.Equal(hits, want) {
					t.Fatalf("different membership/order/completion for %s", key)
				}
				if op == "all" && int64(len(hits)) != count {
					t.Fatalf("count differs from consumed set for %s", key)
				}
				if run == 0 {
					initial = elapsed
				} else {
					samples = append(samples, elapsed)
				}
			}
			slices.Sort(samples)
			tails := ""
			if repeats >= 100 {
				tails = fmt.Sprintf(" p95=%s p99=%s", samples[94], samples[98])
			}
			t.Logf("read rows=%d mode=%s stats=%t shape=%s op=%s matches=%d first=%s repeats=%d median=%s%s plan=%s", size, mode, stats, shape.name, op, count, initial, repeats, samples[(repeats-1)/2], tails, plan)
		}
	}
}

// Run the synchronous repository page (authorization, count, ID page, full
// row projection) on the reader pool alongside 100 disjoint terminal batches.
// New log times lie outside the fixed filter so every page remains verifiable.
func measureSearchMixed(t *testing.T, f *lifecycleLogDatabase, size int, owner int64, mode string) {
	t.Helper()
	const samples, batch = 100, 20
	first := size + 100
	database := f.store.DB()
	insertSearchHistory(t, database, first, first+samples*batch-1, owner)
	start := time.Now()
	written := make(chan error, 1)
	var commits []time.Duration
	go func() {
		for i := range samples {
			begin := time.Now()
			_, err := database.ExecContext(t.Context(), finishSearchHistory, first+i*batch, first+(i+1)*batch-1)
			if err != nil {
				written <- err
				return
			}
			commits = append(commits, time.Since(begin))
		}
		written <- nil
	}()
	code := "invalid_request"
	to := int64(1_800_000_000 + size/10 + 1)
	filter := ListFilter{ErrorCode: &code, To: &to, Page: &pagination.Request{Page: 1, Size: 20}}
	var reads []time.Duration
	var readErr error
	for range samples {
		begin := time.Now()
		p, err := f.repo.ListAdmin(t.Context(), filter)
		if err != nil {
			readErr = err
			break
		}
		reads = append(reads, time.Since(begin))
		if p.Pagination == nil || p.Pagination.TotalItems != "90" || len(p.Data) != 20 || p.Data[0].ID != fmt.Sprintf("req_%021dA", 997*100) {
			readErr = fmt.Errorf("unexpected mixed workload page: metadata=%v rows=%d", p.Pagination, len(p.Data))
			break
		}
	}
	writeErr := <-written
	total := time.Since(start)
	if readErr != nil || writeErr != nil {
		t.Fatal(readErr, writeErr)
	}
	slices.Sort(commits)
	slices.Sort(reads)
	t.Logf("mixed mode=%s admin_rare_pages=%d batch_writes=%d rows_per_write=%d wall=%s read_p50=%s read_p95=%s read_p99=%s write_p50=%s write_p95=%s write_p99=%s", mode, samples, samples, batch, total, reads[49], reads[94], reads[98], commits[49], commits[94], commits[98])
	searchExec(t, database, "DELETE FROM request_logs WHERE id>?", size)
	searchExec(t, database, "DELETE FROM logical_requests WHERE id>printf('req_%021dA',?)", size)
}

func readSearchHits(t *testing.T, database *sql.DB, query string, args []any) []searchHit {
	t.Helper()
	rows, err := database.QueryContext(t.Context(), query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var hits []searchHit
	for rows.Next() {
		var hit searchHit
		if err := rows.Scan(&hit.id, &hit.timestamp); err != nil {
			t.Fatal(err)
		}
		hits = append(hits, hit)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return hits
}
