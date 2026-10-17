package logapi

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

func newLogQueryBenchmarkDatabase(tb testing.TB, count int) *sql.DB {
	tb.Helper()
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		tb.Fatal(err)
	}
	database.SetMaxOpenConns(1)
	tb.Cleanup(func() { _ = database.Close() })
	statements := []string{
		`CREATE TABLE request_logs(id INTEGER PRIMARY KEY,started_at INTEGER NOT NULL,completed_at INTEGER,model TEXT NOT NULL,route_kind TEXT NOT NULL,error_diag TEXT NOT NULL)`,
		`CREATE INDEX idx_request_logs_started ON request_logs(started_at,id)`,
		`CREATE INDEX idx_request_logs_retention ON request_logs(completed_at,id)`,
		`CREATE TABLE request_attempts(request_log_id INTEGER NOT NULL,attempt_seq INTEGER NOT NULL,endpoint_key_id_snapshot INTEGER,canonical_base_url TEXT NOT NULL,upstream_model_id TEXT NOT NULL,diag TEXT NOT NULL)`,
		`CREATE UNIQUE INDEX idx_request_attempts_log_seq ON request_attempts(request_log_id,attempt_seq)`,
		fmt.Sprintf(`WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<%d)
INSERT INTO request_logs SELECT x,1800000000+x/10,1800000001+x/10,printf('provider/model-%%03d',x%%100),CASE WHEN x%%2=1 THEN 'charity_chat_completions' ELSE 'openai_chat_completions' END,printf('%%0256d',x) FROM n`, count),
		`INSERT INTO request_attempts SELECT l.id,s.n,l.id%1000,printf('https://endpoint-%03d.example/v1',l.id%100),printf('model-%02d',l.id%10),printf('%0256d',l.id) FROM request_logs l CROSS JOIN (SELECT 1 n UNION ALL SELECT 2 UNION ALL SELECT 3) s`,
	}
	for _, statement := range statements {
		if _, err := database.Exec(statement); err != nil {
			tb.Fatal(err)
		}
	}
	return database
}

func addLogAttemptBenchmarkIndexes(tb testing.TB, database *sql.DB) {
	tb.Helper()
	for _, statement := range []string{
		`CREATE INDEX idx_attempt_key_filter ON request_attempts(endpoint_key_id_snapshot,request_log_id)`,
		`CREATE INDEX idx_attempt_url_filter ON request_attempts(canonical_base_url,request_log_id)`,
		`CREATE INDEX idx_attempt_model_filter ON request_attempts(upstream_model_id,request_log_id)`,
	} {
		if _, err := database.Exec(statement); err != nil {
			tb.Fatal(err)
		}
	}
}

func benchmarkAttemptWhere(mask int, correlated bool) (string, []any) {
	where := `WHERE (l.completed_at IS NULL OR l.completed_at>?)`
	args := []any{int64(1_799_000_000)}
	for _, filter := range []struct {
		bit       int
		column    string
		predicate string
		value     any
	}{
		{1, "endpoint_key_id_snapshot", endpointKeyPredicate, int64(1)},
		{2, "canonical_base_url", endpointURLPredicate, "https://endpoint-001.example/v1"},
		{4, "upstream_model_id", upstreamModelPredicate, "model-01"},
	} {
		if mask&filter.bit == 0 {
			continue
		}
		if correlated {
			where += ` AND EXISTS(SELECT 1 FROM request_attempts a WHERE a.request_log_id=l.id AND a.` + filter.column + `=?)`
		} else {
			where += filter.predicate
		}
		args = append(args, filter.value)
	}
	return where, args
}

func runLogQueryBenchmark(tb testing.TB, database *sql.DB, where string, args []any, page int64, correlated bool) int64 {
	tb.Helper()
	const order = ` ORDER BY l.started_at DESC,l.id DESC`
	requested := &pagination.Request{Page: page, Size: 50}
	var query string
	var metadata *pagination.Metadata
	var err error
	if correlated {
		query, args, metadata, err = logPageQuery(context.Background(), database, `SELECT l.id FROM request_logs l `+where,
			`SELECT COUNT(*) FROM request_logs l `+where, order, args, requested, 50)
	} else {
		query, args, metadata, err = logListPageQuery(context.Background(), database, `l.id`, "", where, args, requested, 50)
	}
	if err != nil {
		tb.Fatal(err)
	}
	rows, err := database.Query(query, args...)
	if err != nil {
		tb.Fatal(err)
	}
	defer rows.Close()
	var total int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			tb.Fatal(err)
		}
		total++
	}
	if err := rows.Err(); err != nil {
		tb.Fatal(err)
	}
	if total == 0 || metadata.TotalItems == "0" {
		tb.Fatal("benchmark filter unexpectedly empty")
	}
	return total
}

func BenchmarkLogAttemptFilters(b *testing.B) {
	database := newLogQueryBenchmarkDatabase(b, 200_000)
	for _, mode := range []string{"correlated", "sets", "indexed_sets"} {
		if mode == "indexed_sets" {
			addLogAttemptBenchmarkIndexes(b, database)
		}
		b.Run(mode, func(b *testing.B) {
			for mask := 1; mask < 8; mask++ {
				b.Run(fmt.Sprintf("filters_%d", mask), func(b *testing.B) {
					where, args := benchmarkAttemptWhere(mask, mode == "correlated")
					b.ResetTimer()
					for b.Loop() {
						runLogQueryBenchmark(b, database, where, args, 1, mode == "correlated")
					}
				})
			}
		})
	}
}

func BenchmarkLogCharityContainsCount(b *testing.B) {
	database := newLogQueryBenchmarkDatabase(b, 200_000)
	for _, mode := range []string{"table", "covering_partial"} {
		if mode == "covering_partial" {
			_, err := database.Exec(`CREATE INDEX idx_charity_model_count ON request_logs(route_kind,model,completed_at,id) WHERE route_kind IN ('charity_chat_completions','charity_embeddings','charity_images_generations','charity_decisions')`)
			if err != nil {
				b.Fatal(err)
			}
		}
		b.Run(mode, func(b *testing.B) {
			query := `SELECT COUNT(*) FROM request_logs l WHERE (l.completed_at IS NULL OR l.completed_at>?)` + charityModelPredicate
			for b.Loop() {
				var count int
				if err := database.QueryRow(query, 1_799_000_000, "model-001").Scan(&count); err != nil || count != 2000 {
					b.Fatalf("count = %d, %v", count, err)
				}
			}
		})
	}
}

func TestLogAttemptFilterPlanUsesIndependentIndexedSets(t *testing.T) {
	database := newLogQueryBenchmarkDatabase(t, 10_000)
	addLogAttemptBenchmarkIndexes(t, database)
	where, args := benchmarkAttemptWhere(7, false)
	queries := []string{`SELECT COUNT(*) FROM request_logs l ` + where}
	query, queryArgs, metadata, err := logListPageQuery(context.Background(), database, `l.id`, "", where, args, &pagination.Request{Page: 1, Size: 10}, 10)
	if err != nil {
		t.Fatal(err)
	}
	requireLogPagination(t, metadata, 1, 10, 10)
	queries = append(queries, query)
	for i, query := range queries {
		parameters := args
		if i == 1 {
			parameters = queryArgs
		}
		rows, err := database.Query("EXPLAIN QUERY PLAN "+query, parameters...)
		if err != nil {
			t.Fatal(err)
		}
		var plan strings.Builder
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			fmt.Fprintf(&plan, "%d/%d %s\n", id, parent, detail)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		for _, index := range []string{"idx_attempt_key_filter", "idx_attempt_url_filter", "idx_attempt_model_filter"} {
			if !strings.Contains(plan.String(), "COVERING INDEX "+index) {
				t.Fatalf("missing covering filter %s: %s", index, plan.String())
			}
		}
		if strings.Contains(plan.String(), "CORRELATED") {
			t.Fatalf("attempt filter still runs for each logical row: %s", plan.String())
		}
		t.Log(plan.String())
	}
}
