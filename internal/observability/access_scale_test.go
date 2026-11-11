package observability

import (
	"context"
	"encoding/json"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Opt-in paired measurements use the application's SQLite driver and complete
// schema, with ordinary foreign keys, checks and indexes enabled.
func TestAccessQueryScale(t *testing.T) {
	if os.Getenv("NONBIRI_ACCESS_QUERY_SCALE") != "1" {
		t.Skip("set NONBIRI_ACCESS_QUERY_SCALE=1 for paired access-query measurements")
	}
	for _, size := range []int{10_000, 100_000} {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			r, userID := newObservedDatabase(t)
			now := r.now().Unix()
			source, err := json.Marshal(Source{EffectiveIP: "192.0.2.72", IPQuality: "direct_peer", UserAgent: strings.Repeat("Example/1 ", 50)})
			if err != nil {
				t.Fatal(err)
			}
			tx, err := r.db.BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			statements := []struct {
				sql  string
				args []any
			}{
				{`CREATE TEMP TABLE access_seed AS WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<?)
 SELECT x, 'req_'||printf('%021d',x)||'A' AS request_id, ?-1-((?-x)*30*86400/?) AS at,
 CASE x%4 WHEN 0 THEN 'self' WHEN 1 THEN 'charity' WHEN 2 THEN 'unclassified' ELSE 'discovery' END AS kind FROM n`, []any{size, now, size, size}},
				{`INSERT INTO logical_requests(id,user_id,route_kind,model_snapshot,state,attempt_limit,caller_result_class,caller_status,accounting_state,settlement_destination,ledger_rows_remaining,created_at,terminal_at)
 SELECT request_id,?,'openai_chat_completions','scale-model','terminal',1,'failed',503,'none','user',zeroblob(16),at,at FROM access_seed`, []any{userID}},
				{`INSERT INTO request_logs(logical_request_id,user_id,route_kind,started_at,completed_at,caller_result_class,caller_status,status_code)
 SELECT request_id,?,'openai_chat_completions',at,at,'failed',503,503 FROM access_seed`, []any{userID}},
				{`INSERT INTO request_source_facts(request_log_id,user_id,kind,effective_ip,ip_quality,source_json,occurred_at)
 SELECT l.id,?,s.kind,'192.0.2.72','direct_peer',?,s.at FROM access_seed s JOIN request_logs l ON l.logical_request_id=s.request_id`, []any{userID, string(source)}},
				{`INSERT INTO audit_access_events(id,user_id,caller_key_user_id,caller_key_generation,path_kind,method,http_status,response_kind,effective_ip,ip_quality,source_json,occurred_at)
 SELECT 'aev_'||printf('%021d',x)||'A',?,?,3,CASE x%5 WHEN 0 THEN 'billing_usage' ELSE 'models' END,'GET',CASE x%97 WHEN 0 THEN 429 ELSE 200 END,'api_json','192.0.2.72','direct_peer',?,at FROM access_seed`, []any{userID, userID, string(source)}},
				{`UPDATE observability_state SET access_rows=(SELECT count(*) FROM audit_access_events) WHERE id=1`, nil},
				{`DROP TABLE access_seed`, nil},
			}
			started := time.Now()
			for _, statement := range statements {
				if _, err := tx.ExecContext(t.Context(), statement.sql, statement.args...); err != nil {
					t.Fatal(err)
				}
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			t.Logf("fixture rows=%d per table source_bytes=%d seed=%s", size, len(source), time.Since(started))
			for _, scope := range []struct {
				name            string
				from, to, owner int64
			}{
				{"global_24h", now - 86400, now, 0}, {"owner_24h", now - 86400, now, userID},
				{"global_30d", now - 30*86400, now, 0}, {"empty_window", now + 1, now + 61, 0},
			} {
				old := `SELECT count(*) FROM request_source_facts WHERE kind IN ('self','charity') AND occurred_at>=? AND occurred_at<?`
				next := accessGenerationCountQuery
				args := []any{scope.from, scope.to}
				if scope.owner > 0 {
					old += ` AND user_id=?`
					next += ` AND user_id=?`
					args = append(args, scope.owner)
				}
				measure := func(query string) (int64, time.Duration) {
					t.Helper()
					ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
					defer cancel()
					var count int64
					start := time.Now()
					if err := r.db.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
						t.Fatal(err)
					}
					return count, time.Since(start)
				}
				var before, after []time.Duration
				var want int64
				for pass := 0; pass < 4; pass++ {
					var a, b time.Duration
					var got int64
					want, a = measure(old)
					got, b = measure(next)
					if got != want {
						t.Fatalf("%s counts differ: old=%d new=%d", scope.name, want, got)
					}
					if pass == 0 {
						t.Logf("%s first old=%s new=%s count=%d", scope.name, a, b, want)
					} else {
						before = append(before, a)
						after = append(after, b)
					}
				}
				sort.Slice(before, func(i, j int) bool { return before[i] < before[j] })
				sort.Slice(after, func(i, j int) bool { return after[i] < after[j] })
				t.Logf("%s warm_median old=%s new=%s", scope.name, before[1], after[1])
				ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
				read, err := r.db.BeginTx(ctx, nil)
				if err != nil {
					cancel()
					t.Fatal(err)
				}
				filter := AccessFilter{From: scope.from, To: scope.to, UserID: scope.owner, Limit: 20, Page: 2147483647}
				start := time.Now()
				summary, err := SummarizeAccessTx(ctx, read, filter)
				if err != nil {
					read.Rollback()
					cancel()
					t.Fatal(err)
				}
				summaryTime := time.Since(start)
				start = time.Now()
				page, err := ListAccessNumberedTx(ctx, read, filter)
				if err != nil {
					read.Rollback()
					cancel()
					t.Fatal(err)
				}
				pageTime := time.Since(start)
				if err := read.Commit(); err != nil {
					cancel()
					t.Fatal(err)
				}
				cancel()
				if summary.GenerationRequests != want {
					t.Fatalf("summary count=%d want=%d", summary.GenerationRequests, want)
				}
				t.Logf("%s complete_summary=%s last_page=%s auth=%d page=%s total=%s", scope.name, summaryTime, pageTime, summary.AuthenticatedEvents, page.Page, page.TotalItems)
			}
			var violations int
			if err := r.db.QueryRow(`SELECT count(*) FROM pragma_foreign_key_check`).Scan(&violations); err != nil || violations != 0 {
				t.Fatalf("foreign keys: violations=%d error=%v", violations, err)
			}
		})
	}
}
