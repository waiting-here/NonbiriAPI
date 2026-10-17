package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"sync"
	"testing"
	"time"
)

// This fixture preserves SQLite constraints while constructing anonymous,
// zero-cost terminal history. It contains no imported production records.
func seedCatalogLoadHistory(t *testing.T, seed *embeddingHTTPFixture, now int64) {
	t.Helper()
	for ordinal := 1; ordinal <= 2; ordinal++ {
		name := fmt.Sprintf("synthetic-binding-%d", ordinal)
		seed.exec(t, `INSERT INTO model_pair_catalog(endpoint_key_id,normalized_model_id,automatic_supports,manual_supports,automatic_revision,pair_revision,updated_at) VALUES(?,?,0,1,0,1,?)`, seed.keyID, name, now)
		limit := 220
		if ordinal == 2 {
			limit = 160
		}
		seed.exec(t, `INSERT INTO charity_model_bindings(charity_model_id,donation_key_id,endpoint_key_id,upstream_model_id,ord,created_at,updated_at)
SELECT charity_model_id,donation_key_id,endpoint_key_id,?,?,?,? FROM charity_model_bindings WHERE ord=0 ORDER BY charity_model_id LIMIT ?`, name, ordinal, now, now, limit)
	}
	tx, err := seed.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	insert, err := tx.Prepare(`INSERT INTO game_fishing_batches(id,user_id,bait,count,unit_price_milli,entry_total_milli,payout_total_milli,operation_id,request_hash,state,ledger_rows_remaining,next_attempt_at,created_at)
VALUES(?,?,'worm',?,0,0,0,?,zeroblob(32),'reserved',x'00000000000000000000000000000001',?,?)`)
	if err != nil {
		t.Fatal(err)
	}
	defer insert.Close()
	outcomes, err := tx.Prepare(`WITH RECURSIVE n(i) AS (VALUES(0) UNION ALL SELECT i+1 FROM n WHERE i+1<?)
INSERT INTO game_fishing_outcomes(batch_id,ordinal,species_key,tier,size_cm,payout_milli) SELECT ?,i,'boot','junk',0,0 FROM n`)
	if err != nil {
		t.Fatal(err)
	}
	defer outcomes.Close()
	settle, err := tx.Prepare(`UPDATE game_fishing_batches SET state='committed',settled_at=?,revealed_at=?,next_attempt_at=NULL,ledger_rows_remaining=zeroblob(16) WHERE id=?`)
	if err != nil {
		t.Fatal(err)
	}
	defer settle.Close()
	for i := range 70000 {
		id, operation := fmt.Sprintf("fb_%021dA", i), fmt.Sprintf("op_%021dA", i)
		count := 1
		if i%2 == 0 {
			count = 10
		}
		settled := now - 86400 - int64(i)
		if _, err := insert.Exec(id, seed.userID, count, operation, settled, settled); err != nil {
			t.Fatal(err)
		}
		if _, err := outcomes.Exec(count, id); err != nil {
			t.Fatal(err)
		}
		if _, err := settle.Exec(settled, settled, id); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

// TestApplicationCatalogLoad offers requests against a fixed wall-clock
// schedule. Slow responses cannot lower the planned arrival rate. Latency
// targets are evaluated in the release report, never as machine-speed tests.
func TestApplicationCatalogLoad(t *testing.T) {
	if os.Getenv("NONBIRI_LOAD_TEST") != "1" {
		t.Skip("set NONBIRI_LOAD_TEST=1 to run the synthetic load experiment")
	}
	t.Setenv("NONBIRI_LOAD_SCALE", "production")
	for _, tc := range []struct{ rps, concurrent int }{{15, 25}, {30, 50}} {
		t.Run(fmt.Sprintf("rps_%d_concurrent_%d", tc.rps, tc.concurrent), func(t *testing.T) {
			u := &loadUpstream{release: make(chan struct{}), mode: "short"}
			f := newLoadFixture(t, u)
			ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
			defer cancel()
			routes := []string{"/v1/models", "/api/charity/models", "/api/charity/models?view=catalog&page=1&page_size=50", "/api/session", "/api/me", "/api/games/blackjack/state"}
			for _, route := range routes {
				s := f.get(ctx, route, f.users[0])
				if s.status != 200 || s.err != "" {
					t.Fatalf("warm %s: %+v", route, s)
				}
			}
			runtime.GC()
			before := loadReadMemory()
			writerBefore, readerBefore := f.store.StatsByPool()
			measurements, schedule := &loadMeasurements{}, &loadMeasurements{}
			var wg sync.WaitGroup
			start := time.Now()
			const seconds = 30
			planned := seconds*tc.rps + tc.concurrent
			for i := range planned {
				burst := i >= seconds*tc.rps
				due := start.Add(time.Duration(i) * time.Second / time.Duration(tc.rps))
				if burst {
					due = start.Add(seconds * time.Second)
				}
				if delay := time.Until(due); delay > 0 {
					time.Sleep(delay)
				}
				wg.Add(1)
				go func(i int, due time.Time, burst bool) {
					defer wg.Done()
					phase, route := "scheduled", routes[i%len(routes)]
					if burst {
						phase, route = "burst", routes[(i-seconds*tc.rps)%3]
					}
					schedule.add(loadSample{route: "submission_lag", phase: phase, duration: time.Since(due)})
					s := f.get(ctx, route, f.users[i%len(f.users)])
					s.phase = phase
					measurements.add(s)
				}(i, due, burst)
			}
			wg.Wait()
			writerAfter, readerAfter := f.store.StatsByPool()
			completed, admitted, rejected := 0, 0, 0
			for route, p := range measurements.summaries() {
				completed += p.Count
				admitted += p.Status[200]
				rejected += p.Status[429] + p.Status[503]
				if len(p.Errors) != 0 || p.Status[200] != p.Count {
					t.Errorf("directory/page failure: %s %+v", route, p)
				}
			}
			report := map[string]any{
				"rps": tc.rps, "directory_concurrency": tc.concurrent, "planned": planned, "submitted": completed, "completed": completed, "admitted": admitted, "rejected": rejected,
				"models": 220, "bindings": 600, "fishing_batches": 70000, "fishing_outcomes": 385000,
				"elapsed_seconds": time.Since(start).Seconds(), "pages": measurements.summaries(), "schedule": schedule.summaries(),
				"writer_waits": writerAfter.WaitCount - writerBefore.WaitCount, "writer_wait_ms": float64(writerAfter.WaitDuration-writerBefore.WaitDuration) / float64(time.Millisecond),
				"reader_waits": readerAfter.WaitCount - readerBefore.WaitCount, "reader_wait_ms": float64(readerAfter.WaitDuration-readerBefore.WaitDuration) / float64(time.Millisecond),
				"memory_before": before, "memory_after": loadReadMemory(), "go": runtime.Version(), "os": runtime.GOOS, "arch": runtime.GOARCH, "gomaxprocs": runtime.GOMAXPROCS(0),
			}
			encoded, err := json.Marshal(report)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("CATALOG_LOAD_RESULT %s", encoded)
		})
	}
}
