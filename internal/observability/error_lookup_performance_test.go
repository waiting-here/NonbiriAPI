package observability

import (
	"context"
	"database/sql"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/diagnostic"
	"github.com/waiting-here/NonbiriAPI/internal/upstreamerror"
)

const legacyErrorLookupQuery = "SELECT count(*) FROM request_error_bodies WHERE request_log_id IS ? AND task_id IS ? AND operation_id IS ? AND attempt_seq=? AND event_seq=?"

func errorLookupPlan(t testing.TB, database *sql.DB, query string, args ...any) string {
	t.Helper()
	rows, err := database.Query("EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var result []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		result = append(result, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return strings.Join(result, "; ")
}

func TestErrorLookupUsesSubjectIndex(t *testing.T) {
	r, _ := newObservedDatabase(t)
	var version string
	if err := r.db.QueryRow("SELECT sqlite_version()").Scan(&version); err != nil {
		t.Fatal(err)
	}
	t.Log("SQLite", version)
	for _, query := range []struct {
		name, sql string
		args      []any
	}{
		{"request", errorBodyRequestExistsQuery, []any{int64(1), 1, 1}},
		{"task", errorBodyTaskExistsQuery, []any{"img_fixture", 1, 1}},
		{"operation", errorBodyOperationExistsQuery, []any{"op_fixture", 1, 1}},
	} {
		plan := errorLookupPlan(t, r.db, query.sql, query.args...)
		t.Log(query.name, plan)
		if !strings.Contains(plan, "USING COVERING INDEX idx_request_errors_"+query.name) || strings.Contains(plan, "SCAN request_error_bodies") {
			t.Fatalf("duplicate lookup scans retained history: %s", plan)
		}
	}
}

// These opt-in measurements keep the complete schema and production driver.
// Fixture construction is excluded; complete record timings include commits.
func TestErrorRecordingScale(t *testing.T) {
	if os.Getenv("NONBIRI_ERROR_LOOKUP_SCALE") != "1" {
		t.Skip("set NONBIRI_ERROR_LOOKUP_SCALE=1 for paired error-lookup measurements")
	}
	for _, size := range []int{10_000, 100_000} {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			r, user := newObservedDatabase(t)
			now := r.now().Unix()
			tx, err := r.db.BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			for _, stmt := range []struct {
				query string
				args  []any
			}{
				{`CREATE TEMP TABLE error_seed AS WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<?) SELECT x,'req_'||printf('%021d',x)||'A' AS request_id FROM n`, []any{size}},
				{`INSERT INTO logical_requests(id,user_id,route_kind,model_snapshot,state,attempt_limit,caller_result_class,caller_status,accounting_state,settlement_destination,ledger_rows_remaining,created_at,terminal_at) SELECT request_id,?,'openai_chat_completions','fixture-model','terminal',1,'failed',503,'none','user',zeroblob(16),?,? FROM error_seed`, []any{user, now - 1, now - 1}},
				{`INSERT INTO request_logs(logical_request_id,user_id,route_kind,started_at,completed_at,caller_result_class,caller_status,status_code) SELECT request_id,?,'openai_chat_completions',?,?,'failed',503,503 FROM error_seed`, []any{user, now - 1, now - 1}},
				{`INSERT INTO request_error_bodies(request_log_id,attempt_seq,event_seq,http_status,content_type,body,bytes_saved,truncated,save_state,created_at,expires_at) SELECT l.id,1,1,503,'text/plain',zeroblob(512),512,0,'saved',?,? FROM error_seed s JOIN request_logs l ON l.logical_request_id=s.request_id`, []any{now - 1, now + RetentionSeconds - 1}},
				{`UPDATE observability_state SET raw_body_bytes=(SELECT SUM(bytes_saved) FROM request_error_bodies) WHERE id=1`, nil},
				{`DROP TABLE error_seed`, nil},
			} {
				if _, err := tx.ExecContext(t.Context(), stmt.query, stmt.args...); err != nil {
					t.Fatal(err)
				}
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			if _, err := r.db.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
				t.Fatal(err)
			}
			request, logID := observedLog(t, r, user, now)
			measure := func(repeats int, query string, args ...any) time.Duration {
				t.Helper()
				ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
				defer cancel()
				start := time.Now()
				for range repeats {
					var found int
					if err := r.db.QueryRowContext(ctx, query, args...).Scan(&found); err != nil || found != 0 {
						t.Fatalf("lookup=%d error=%v", found, err)
					}
				}
				return time.Since(start) / time.Duration(repeats)
			}
			next := errorBodyRequestExistsQuery
			// Fixed batches resolve sub-millisecond lookups on coarse host clocks.
			var before, after []time.Duration
			for pass := 0; pass < 5; pass++ {
				a := measure(8, legacyErrorLookupQuery, logID, nil, nil, 1, 1)
				b := measure(128, next, logID, 1, 1)
				if pass == 0 {
					t.Logf("lookup first legacy=%s indexed=%s", a, b)
				} else {
					before, after = append(before, a), append(after, b)
				}
			}
			sort.Slice(before, func(i, j int) bool { return before[i] < before[j] })
			sort.Slice(after, func(i, j int) bool { return after[i] < after[j] })
			t.Logf("lookup warm median legacy=%s indexed=%s", before[len(before)/2], after[len(after)/2])
			var records []time.Duration
			for seq := int64(1); seq <= 5; seq++ {
				ctx := diagnostic.WithRecorder(t.Context())
				var duration time.Duration
				var recordErr error
				captured := false
				ctx = upstreamerror.WithCapture(ctx, func(ctx context.Context, event upstreamerror.Event) {
					captured = true
					start := time.Now()
					recordErr = r.recordError(ctx, DiagnosticRef{RequestID: request, AttemptSeq: 1}, seq, event)
					duration = time.Since(start)
				})
				upstreamerror.CaptureEvent(ctx, 503, "text/plain", []byte(strings.Repeat("fixture ", 64)))
				if !captured || recordErr != nil {
					t.Fatal("record failed", captured, recordErr)
				}
				if seq == 1 {
					t.Logf("record committed first=%s", duration)
				} else {
					records = append(records, duration)
				}
			}
			sort.Slice(records, func(i, j int) bool { return records[i] < records[j] })
			t.Logf("record committed warm median=%s", records[len(records)/2])
			if got := scalar(t, r.db, "SELECT count(*) FROM request_error_bodies WHERE request_log_id=?", logID); got != 5 {
				t.Fatalf("recorded=%d", got)
			}
			if got := scalar(t, r.db, "SELECT raw_body_bytes FROM observability_state"); got != int64(size+5)*512 {
				t.Fatalf("saved bytes=%d", got)
			}
			var pageSize, pages int64
			if err := r.db.QueryRow("PRAGMA page_size").Scan(&pageSize); err != nil {
				t.Fatal(err)
			}
			if err := r.db.QueryRow("PRAGMA page_count").Scan(&pages); err != nil {
				t.Fatal(err)
			}
			t.Logf("database MiB=%.2f history=%d body_bytes=512", float64(pageSize*pages)/(1<<20), size)
		})
	}
}
