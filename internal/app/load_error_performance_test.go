package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	"github.com/waiting-here/NonbiriAPI/internal/observability"
)

// TestApplicationErrorHistoryMixedLoad keeps work and concurrency fixed across
// history sizes. It exercises actual HTTP admission, dispatch and settlement.
func TestApplicationErrorHistoryMixedLoad(t *testing.T) {
	if os.Getenv("NONBIRI_ERROR_MIXED_LOAD") != "1" {
		t.Skip("set NONBIRI_ERROR_MIXED_LOAD=1 for the application error-history experiment")
	}
	t.Setenv("NONBIRI_LOAD_SCALE", "")
	const perKind, concurrency = 60, 8
	const catalog = "/api/charity/models?view=catalog&page=1&page_size=50"
	const session = "/api/session"
	for _, history := range []int{10_000, 100_000} {
		t.Run(fmt.Sprint(history), func(t *testing.T) {
			upstream := &loadUpstream{release: make(chan struct{}), mode: "error_mix"}
			f := newLoadFixture(t, upstream)
			ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
			defer cancel()
			seedErrorLoadHistory(t, f, history)
			for _, route := range []string{catalog, session} {
				if sample := f.get(ctx, route, f.users[0]); sample.status != 200 || sample.err != "" {
					t.Fatalf("warm page: %+v", sample)
				}
			}
			for _, fail := range []bool{false, true} {
				sample := errorLoadModel(ctx, f, f.users[0], fail)
				want := http.StatusOK
				if fail {
					want = http.StatusServiceUnavailable
				}
				if sample.status != want || sample.err != "" {
					t.Fatalf("warm model: %+v", sample)
				}
			}
			waitErrorLoadHandlers(t, ctx, f, upstream)
			var upperLog, bodiesBefore, bytesBefore int64
			for _, check := range []struct {
				query string
				value *int64
			}{
				{"SELECT COALESCE(MAX(id),0) FROM request_logs", &upperLog},
				{"SELECT count(*) FROM request_error_bodies", &bodiesBefore},
				{"SELECT raw_body_bytes FROM observability_state WHERE id=1", &bytesBefore},
			} {
				if err := f.store.ReadDB().QueryRowContext(ctx, check.query).Scan(check.value); err != nil {
					t.Fatal(err)
				}
			}
			// Keep writer spans independent of the application's minute drain.
			// Context roots and the actual SQLite driver remain unchanged.
			writerMetrics := observability.NewPerformance()
			f.store.SetConnectionObserver(writerMetrics)
			defer f.store.SetConnectionObserver(f.app.performance)
			runtime.GC()
			writerMetrics.Drain()
			writerBefore, readerBefore := f.store.StatsByPool()
			enteredBefore, failedBefore := upstream.entered.Load(), upstream.failed.Load()
			measurements, schedule := &loadMeasurements{}, &loadMeasurements{}
			jobs := make(chan int, perKind*4)
			var workers sync.WaitGroup
			start := time.Now()
			for range concurrency {
				workers.Go(func() {
					for job := range jobs {
						kind, ordinal := job%4, job/4
						user := f.users[ordinal%len(f.users)]
						var sample loadSample
						dispatched := time.Now()
						switch kind {
						case 0, 1:
							sample = errorLoadModel(ctx, f, user, kind == 0)
						case 2:
							sample = f.get(ctx, catalog, user)
						case 3:
							sample = f.get(ctx, session, user)
						}
						schedule.add(loadSample{route: sample.route, duration: dispatched.Sub(start)})
						measurements.add(sample)
					}
				})
			}
			for job := range perKind * 4 {
				jobs <- job
			}
			close(jobs)
			workers.Wait()
			waitErrorLoadHandlers(t, ctx, f, upstream)
			elapsed := time.Since(start)
			writerAfter, readerAfter := f.store.StatsByPool()
			occupancy := writerMetrics.Drain()
			f.store.SetConnectionObserver(f.app.performance)

			summaries := measurements.overallSummaries()
			for route, expected := range map[string]int{
				"model_error": 503, "model_success": 200, catalog: 200, session: 200,
			} {
				summary := summaries[route]
				if summary.Count != perKind || len(summary.Status) != 1 || summary.Status[expected] != perKind || len(summary.Errors) != 0 {
					t.Errorf("unexpected result %s: %+v", route, summary)
				}
				if route == "model_error" && summary.Codes[httperr.CodeUpstream] != perKind {
					t.Errorf("upstream error identity changed: %+v", summary)
				}
			}
			entered := upstream.entered.Load() - enteredBefore
			failed := upstream.failed.Load() - failedBefore
			if entered != 2*perKind || failed != perKind {
				t.Errorf("upstream attempts=%d errors=%d", entered, failed)
			}
			var bodiesAfter, bytesAfter, exactBodies, terminal int64
			for _, check := range []struct {
				query string
				args  []any
				value *int64
			}{
				{"SELECT count(*) FROM request_error_bodies", nil, &bodiesAfter},
				{"SELECT raw_body_bytes FROM observability_state WHERE id=1", nil, &bytesAfter},
				{`SELECT count(*) FROM request_error_bodies WHERE request_log_id>? AND attempt_seq=1 AND event_seq=1 AND http_status=503 AND bytes_saved=512 AND length(body)=512 AND save_state='saved'`, []any{upperLog}, &exactBodies},
				{`SELECT count(*) FROM logical_requests r JOIN request_logs l ON l.logical_request_id=r.id WHERE l.id>? AND r.state='terminal' AND r.accounting_state='committed' AND r.account_reserved_milli=0 AND r.ledger_rows_remaining=zeroblob(16)`, []any{upperLog}, &terminal},
			} {
				if err := f.store.ReadDB().QueryRowContext(ctx, check.query, check.args...).Scan(check.value); err != nil {
					t.Fatal(err)
				}
			}
			if bodiesAfter-bodiesBefore != perKind || bytesAfter-bytesBefore != perKind*loadErrorBodyBytes || exactBodies != perKind || terminal != 2*perKind {
				t.Errorf("durable outcome: bodies=%d bytes=%d exact=%d terminal=%d", bodiesAfter-bodiesBefore, bytesAfter-bytesBefore, exactBodies, terminal)
			}
			outcomes := map[string]int64{}
			rows, err := f.store.ReadDB().QueryContext(ctx, `SELECT r.caller_result_class,r.accounting_state,count(*) FROM logical_requests r JOIN request_logs l ON l.logical_request_id=r.id WHERE l.id>? GROUP BY 1,2`, upperLog)
			if err != nil {
				t.Fatal(err)
			}
			for rows.Next() {
				var result, accounting string
				var count int64
				if err := rows.Scan(&result, &accounting, &count); err != nil {
					rows.Close()
					t.Fatal(err)
				}
				outcomes[result+"/"+accounting] = count
			}
			err = rows.Err()
			closeErr := rows.Close()
			if err != nil || closeErr != nil {
				t.Fatal(err, closeErr)
			}
			if len(outcomes) != 2 || outcomes["success/committed"] != perKind || outcomes["failed/committed"] != perKind {
				t.Errorf("terminal accounting: %+v", outcomes)
			}
			var pending int
			if err := f.store.ReadDB().QueryRowContext(ctx, `SELECT count(*) FROM dispatch_claims WHERE state IN ('claimed','dispatched')`).Scan(&pending); err != nil || pending != 0 {
				t.Errorf("pending dispatch claims=%d error=%v", pending, err)
			}
			tx, err := f.store.DB().BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			err = ledger.ValidateRecovery(ctx, tx)
			_ = tx.Rollback()
			if err != nil {
				t.Errorf("ledger recovery: %v", err)
			}
			var holdMS, transactionMS int64
			for _, row := range occupancy {
				if strings.HasPrefix(row.Route, "occupancy.hold.") {
					holdMS += row.ActiveMS
				}
				if strings.HasPrefix(row.Route, "occupancy.transaction.") {
					transactionMS += row.ActiveMS
				}
			}
			report := map[string]any{
				"variant": os.Getenv("NONBIRI_ERROR_LOAD_VARIANT"), "history": history, "body_bytes": loadErrorBodyBytes,
				"per_kind": perKind, "concurrency": concurrency, "elapsed_ms": float64(elapsed) / float64(time.Millisecond),
				"http": summaries, "queue_delay": schedule.overallSummaries(),
				"upstream_attempts": entered, "upstream_errors": failed,
				"stored_bodies": bodiesAfter - bodiesBefore, "stored_bytes": bytesAfter - bytesBefore, "terminal_accounting": outcomes,
				"writer_waits": writerAfter.WaitCount - writerBefore.WaitCount, "writer_wait_ms": float64(writerAfter.WaitDuration-writerBefore.WaitDuration) / float64(time.Millisecond),
				"reader_waits": readerAfter.WaitCount - readerBefore.WaitCount, "reader_wait_ms": float64(readerAfter.WaitDuration-readerBefore.WaitDuration) / float64(time.Millisecond),
				"writer_hold_ms": holdMS, "writer_transaction_ms": transactionMS, "writer_occupancy": occupancy,
				"go": runtime.Version(), "os": runtime.GOOS, "arch": runtime.GOARCH, "gomaxprocs": runtime.GOMAXPROCS(0),
			}
			encoded, err := json.Marshal(report)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("ERROR_HISTORY_LOAD_RESULT %s", encoded)
		})
	}
}

func waitErrorLoadHandlers(t *testing.T, ctx context.Context, f *loadFixture, upstream *loadUpstream) {
	t.Helper()
	for f.inbound.Load() != 0 || upstream.active.Load() != 0 {
		select {
		case <-ctx.Done():
			t.Fatal("application handlers did not drain", ctx.Err())
		case <-time.After(time.Millisecond):
		}
	}
}

func errorLoadModel(ctx context.Context, f *loadFixture, user loadIdentity, fail bool) loadSample {
	label, prompt := "model_success", "fixture-ok:"
	if fail {
		label, prompt = "model_error", "fixture-error:"
	}
	body, _ := json.Marshal(map[string]any{
		"model": "provider/self", "stream": false, "max_tokens": 128,
		"messages": []map[string]string{{"role": "user", "content": prompt + strings.Repeat("x", 4096)}},
	})
	start := time.Now()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, f.server.URL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return loadSample{route: label, err: "request"}
	}
	request.Host = auditUserHost
	request.Header.Set("Authorization", "Bearer "+user.caller)
	request.Header.Set("Content-Type", "application/json")
	response, err := f.client.Do(request)
	if err != nil {
		return loadSample{route: label, duration: time.Since(start), err: "transport"}
	}
	raw, readErr := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	closeErr := response.Body.Close()
	sample := loadSample{route: label, duration: time.Since(start), status: response.StatusCode}
	if readErr != nil || closeErr != nil {
		sample.err = "body"
		return sample
	}
	var payload struct {
		Error   struct{ Code string }
		Choices []struct{ Message struct{ Content string } }
	}
	if json.Unmarshal(raw, &payload) != nil {
		sample.err = "json"
	} else if fail {
		sample.code = payload.Error.Code
	} else if len(payload.Choices) != 1 || payload.Choices[0].Message.Content != "ok" {
		sample.err = "completion"
	}
	return sample
}

func seedErrorLoadHistory(t *testing.T, f *loadFixture, size int) {
	t.Helper()
	now := time.Now().Unix()
	tx, err := f.store.DB().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{`CREATE TEMP TABLE error_load_seed AS WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<?) SELECT x,'req_'||printf('%021d',x)||'A' AS request_id FROM n`, []any{size}},
		{`INSERT INTO logical_requests(id,user_id,route_kind,model_snapshot,state,attempt_limit,caller_result_class,caller_status,accounting_state,settlement_destination,ledger_rows_remaining,created_at,terminal_at) SELECT request_id,?,'openai_chat_completions','fixture-history','terminal',1,'failed',503,'none','user',zeroblob(16),?,? FROM error_load_seed`, []any{f.users[0].id, now - 1, now - 1}},
		{`INSERT INTO request_logs(logical_request_id,user_id,route_kind,started_at,completed_at,caller_result_class,caller_status,status_code) SELECT request_id,?,'openai_chat_completions',?,?,'failed',503,503 FROM error_load_seed`, []any{f.users[0].id, now - 1, now - 1}},
		{`INSERT INTO request_error_bodies(request_log_id,attempt_seq,event_seq,http_status,content_type,body,bytes_saved,truncated,save_state,created_at,expires_at) SELECT l.id,1,1,503,'text/plain',zeroblob(512),512,0,'saved',?,? FROM error_load_seed s JOIN request_logs l ON l.logical_request_id=s.request_id`, []any{now - 1, now + observability.RetentionSeconds - 1}},
		{`UPDATE observability_state SET raw_body_bytes=(SELECT SUM(bytes_saved) FROM request_error_bodies) WHERE id=1`, nil},
		{`DROP TABLE error_load_seed`, nil},
	} {
		if _, err := tx.ExecContext(t.Context(), statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.DB().Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		t.Fatal(err)
	}
}
