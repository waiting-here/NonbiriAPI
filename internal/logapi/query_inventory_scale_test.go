package logapi

import (
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

func TestOwnerLogQueryScale(t *testing.T) {
	if os.Getenv("NONBIRI_QUERY_SCALE") != "1" {
		t.Skip("opt-in full-schema query measurement")
	}
	f := newLifecycleLogDatabase(t)
	database := f.store.DB()
	owner := seedLifecycleLogUser(t, database, "owner-query-scale", false)
	previous := 0
	type result struct {
		ids      []int64
		metadata *pagination.Metadata
	}
	baseline := make(map[string]result)
	const priorIndex = `DROP INDEX idx_request_logs_user_started;
CREATE INDEX idx_request_logs_user_started ON request_logs(user_id,started_at,id);`
	for _, size := range []int{10_000, 100_000} {
		seedOperationScale(t, database, previous+1, size, owner, false)
		previous = size
		// Concentrate retained history in one owner, as a heavy user's list does.
		if _, err := database.Exec(`UPDATE logical_requests SET user_id=?`, owner); err != nil {
			t.Fatal(err)
		}
		for _, candidate := range []bool{false, true} {
			indexSQL := priorIndex
			if candidate {
				indexSQL = db.RequestLogOwnerIndexSQL
			}
			if _, err := database.Exec(indexSQL); err != nil {
				t.Fatal(err)
			}
			if _, err := database.Exec(`ANALYZE request_logs`); err != nil {
				t.Fatal(err)
			}
			for _, shape := range []struct {
				name, extra string
				args        []any
			}{
				{"all", "", nil},
				{"operation", " AND " + logOperationSQL + "=?", []any{"responses"}},
				{"empty-operation", " AND " + logOperationSQL + "=?", []any{"embeddings"}},
				{"status", " AND l.caller_status=?", []any{200}},
				{"empty-status", " AND l.caller_status=?", []any{503}},
				{"error", " AND l.caller_error_code=?", []any{"missing_error"}},
				{"model", " AND l.model=?", []any{"needle-model"}},
				{"old-time", " AND l.started_at>=? AND l.started_at<?", []any{int64(1800000000), int64(1800000010)}},
			} {
				where := `WHERE l.user_id=? AND (l.completed_at IS NULL OR l.completed_at>?)` + shape.extra
				args := append([]any{owner, int64(1799000000)}, shape.args...)
				for _, page := range []int64{1, 5000} {
					for run := range 2 {
						start := time.Now()
						query, values, metadata, err := logListPageQuery(t.Context(), database, commonListColumns+`,`+callerIdentityColumns+`,l.user_id`, callerIdentityJoin, where, args, &pagination.Request{Page: page, Size: 20}, 20)
						if err != nil {
							t.Fatal(err)
						}
						ids := consumeOperationScalePage(t, database, query, values)
						key := fmt.Sprintf("%d/%s/%d", size, shape.name, page)
						actual := result{ids, metadata}
						if !candidate && run == 0 {
							baseline[key] = actual
						} else if !reflect.DeepEqual(actual, baseline[key]) {
							t.Fatalf("result changed for %s: got %+v want %+v", key, actual, baseline[key])
						}
						t.Logf("rows=%d candidate=%v shape=%s page=%d run=%d returned=%d total=%s elapsed=%s", size, candidate, shape.name, page, run, len(ids), metadata.TotalItems, time.Since(start))
						if run == 0 && page == 1 {
							t.Log("count plan:", charityQueryPlan(t, database, `SELECT count(*) FROM request_logs l `+where, args))
						}
					}
				}
			}
			var bytes int64
			if err := database.QueryRow(`SELECT sum(pgsize) FROM dbstat WHERE name='idx_request_logs_user_started'`).Scan(&bytes); err != nil {
				t.Fatal(err)
			}
			start := time.Now()
			seedOperationScale(t, database, size+1, size+100, owner, false)
			t.Logf("rows=%d candidate=%v index_bytes=%d insert_100_one_transaction=%s", size, candidate, bytes, time.Since(start))
			if _, err := database.Exec(`DELETE FROM request_logs WHERE id>?; DELETE FROM logical_requests WHERE CAST(substr(id,5,21) AS INTEGER)>?`, size, size); err != nil {
				t.Fatal(err)
			}
		}
	}
}
