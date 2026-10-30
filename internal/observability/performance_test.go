package observability

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func TestPerformanceBoundedAnonymousRoutes(t *testing.T) {
	p := NewPerformance()
	h := p.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		TimeTransaction(r.Context(), "untrusted-user-value")()
		w.WriteHeader(http.StatusNoContent)
	}))
	for i := range 1000 {
		for _, prefix := range []string{"/api/", "/admin/api/", "/api/games/gwent/", "/private/"} {
			r := httptest.NewRequest("PRIVATE-METHOD", fmt.Sprintf("%sprivate-user-%d?secret=private-token", prefix, i), nil)
			r.Header.Set("Authorization", "private-credential")
			h.ServeHTTP(httptest.NewRecorder(), r)
		}
	}
	rows := p.Drain()
	if len(rows) != 4 {
		t.Fatalf("unbounded route series: %d", len(rows))
	}
	data, err := json.Marshal(rows)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "private") || strings.Contains(string(data), "PRIVATE") {
		t.Fatalf("request data leaked: %s", data)
	}
	for _, row := range rows {
		if row.Completed != 1000 || row.Method != "OTHER" || row.Active != 0 {
			t.Fatalf("incorrect aggregate: %+v", row)
		}
	}
}

func TestPerformanceHistogramAndInFlightWindow(t *testing.T) {
	p := NewPerformance()
	key := performanceKey{"test", "GET"}
	long := p.begin(key)
	for _, duration := range []time.Duration{time.Millisecond, 2 * time.Millisecond, 3 * time.Millisecond, 40 * time.Minute} {
		p.begin(key)(duration, time.Millisecond, false, false)
	}
	row := p.Drain()[0]
	if row.Completed != 4 || row.Active != 1 || row.Peak != 2 || row.P50MS != 2 || row.P95MS != 2400000 || row.FirstP95MS != 1 {
		t.Fatalf("first window: %+v", row)
	}
	long(time.Second, time.Millisecond, true, true)
	row = p.Drain()[0]
	if row.Completed != 1 || row.Peak != 1 || row.Active != 0 || row.ServerErrors != 1 || row.Canceled != 1 || row.P99MS != 1000 {
		t.Fatalf("second window: %+v", row)
	}
	if rows := p.Drain(); len(rows) != 0 {
		t.Fatalf("empty window: %+v", rows)
	}
}

func TestPerformanceConcurrentDrain(t *testing.T) {
	p := NewPerformance()
	var workers sync.WaitGroup
	for range 20 {
		workers.Go(func() {
			for range 100 {
				p.begin(performanceKey{"test", "GET"})(time.Millisecond, time.Millisecond, false, false)
			}
		})
	}
	var completed int64
	for range 50 {
		for _, row := range p.Drain() {
			completed += row.Completed
		}
	}
	workers.Wait()
	for _, row := range p.Drain() {
		completed += row.Completed
		if row.Active != 0 {
			t.Fatalf("active requests leaked: %+v", row)
		}
	}
	if completed != 2000 {
		t.Fatalf("completed=%d", completed)
	}
}

func TestPerformancePreservesStreamingCancellationAndPanic(t *testing.T) {
	p := NewPerformance()
	ctx, cancel := context.WithCancel(context.Background())
	rec := httptest.NewRecorder()
	p.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		end := TimeTransaction(r.Context(), "session")
		end()
		end() // A deferred rollback after commit must not record twice.
		if err := http.NewResponseController(w).Flush(); err != nil {
			t.Fatal(err)
		}
		if !rec.Flushed {
			t.Fatal("stream flush was swallowed")
		}
		_, _ = w.Write([]byte("event\n"))
		cancel()
	})).ServeHTTP(rec, httptest.NewRequest("GET", "/api/me", nil).WithContext(ctx))
	rows := p.Drain()
	if len(rows) != 2 || rows[0].Canceled != 1 || rows[1].Completed != 1 || rows[1].FirstP95MS != 0 || rec.Body.String() != "event\n" {
		t.Fatalf("stream/transaction results: %+v", rows)
	}
	func() {
		defer func() {
			if recover() != "test panic" {
				t.Error("handler panic was swallowed or replaced")
			}
		}()
		p.Wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("test panic") })).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/me", nil))
	}()
	if row := p.Drain()[0]; row.ServerErrors != 1 || row.Active != 0 {
		t.Fatalf("panic accounting: %+v", row)
	}
}

func TestPerformanceWindowPoolDeltasAndShutdown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		p := NewPerformance()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		windows := make(chan PerformanceWindow, 1)
		done := make(chan struct{})
		var stats atomic.Value
		stats.Store(sql.DBStats{WaitCount: 10, WaitDuration: time.Second})
		go func() {
			defer close(done)
			p.Run(ctx, func() (sql.DBStats, sql.DBStats) {
				current := stats.Load().(sql.DBStats)
				return current, sql.DBStats{WaitCount: current.WaitCount * 2, WaitDuration: current.WaitDuration * 2}
			}, func(window PerformanceWindow) { windows <- window })
		}()
		synctest.Wait()
		stats.Store(sql.DBStats{WaitCount: 13, WaitDuration: 1500 * time.Millisecond, InUse: 1, OpenConnections: 1})
		time.Sleep(time.Minute)
		window := <-windows
		if window.WindowMS != 60000 || window.DBWaits != 9 || window.DBWaitMS != 1500 || window.Pools.Reader.Waits != 6 || window.Pools.Reader.WaitMS != 1000 || window.DBInUse != 1 || window.DBOpen != 1 {
			t.Fatalf("pool delta: %+v", window)
		}
		cancel()
		<-done
	})
}

func TestCharityManagementPerformanceUsesBoundedRouteTemplates(t *testing.T) {
	p := NewPerformance()
	handler := p.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	templates := map[string]string{
		"/charity-models":                                         "/charity-models",
		"/charity-models/private-object":                          "/charity-models/{id}",
		"/charity-models/private-object/bindings":                 "/charity-models/{id}/bindings",
		"/charity-models/private-object/binding-candidates":       "/charity-models/{id}/binding-candidates",
		"/charity-models/private-object/bindings/batch-receipt":   "/charity-models/{id}/bindings/batch-receipt",
		"/charity-models/private-object/bindings/private-binding": "/charity-models/{id}/bindings/{id}",
		"/charity-price-templates":                                "/charity-price-templates",
		"/charity-price-templates/private-object":                 "/charity-price-templates/{id}",
	}
	for _, base := range []string{"/api/steward", "/admin/api"} {
		for path, want := range templates {
			if got := performanceRoute(base + path); got != base+want {
				t.Fatalf("%s: %s", base+path, got)
			}
			for i := range 100 {
				unique := strings.ReplaceAll(path, "private-object", fmt.Sprintf("private-object-%d", i))
				unique = strings.ReplaceAll(unique, "private-binding", fmt.Sprintf("private-binding-%d", i))
				request := httptest.NewRequest(http.MethodGet, base+unique+"?private-query=private-secret", nil)
				request.Header.Set("Authorization", "private-token")
				handler.ServeHTTP(httptest.NewRecorder(), request)
			}
		}
	}
	for _, path := range []string{"/api/steward/charity-models//bindings", "/api/steward/charity-models/1/private-tail", "/api/steward/charity-models/1/bindings/2/private-tail", "/api/steward/charity-price-templates/1/private-tail", "/api/steward/charity-models-private"} {
		if got := performanceRoute(path); got != "/api/other" {
			t.Fatalf("unregistered route=%s", got)
		}
	}
	rows := p.Drain()
	if len(rows) != 2*len(templates) {
		t.Fatalf("unexpected route cardinality: %d", len(rows))
	}
	encoded, err := json.Marshal(rows)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "private") {
		t.Fatalf("request values leaked: %s", encoded)
	}
	for _, row := range rows {
		if row.Completed != 100 {
			t.Fatalf("split aggregate: %+v", row)
		}
	}
}

func TestPerformanceSeparatesEventStreamsAndLeaderboards(t *testing.T) {
	for path, want := range map[string]string{
		"/api/events":                     "/api/events",
		"/api/games/fishing/leaderboard":  "game.fishing.leaderboard",
		"/api/games/fishing/state":        "game.fishing.state",
		"/api/games/fishing/private-name": "game.fishing",
		"/api/games/private/leaderboard":  "game.other",
		"/api/events/private":             "/api/other",
	} {
		if got := performanceRoute(path); got != want {
			t.Fatalf("%s: got %s want %s", path, got, want)
		}
	}
}
