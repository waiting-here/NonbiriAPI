package observability

import (
	"context"
	"database/sql"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// Performance keeps bounded, anonymous latency histograms in memory. It never
// stores request paths, identities, headers, bodies or individual samples.
type Performance struct {
	mu     sync.Mutex
	series map[performanceKey]*performanceSeries
	http   HTTPPerformance
}

type performanceKey struct{ route, method string }
type performanceSeries struct {
	count, failed, canceled, active, peak int64
	total, first                          latencyHistogram
	activeSince                           time.Time
	activeTime                            time.Duration
}

// Accumulate concurrent elapsed time in this window, including unfinished work.
func (s *performanceSeries) accrue(now time.Time) {
	if !s.activeSince.IsZero() {
		s.activeTime += time.Duration(s.active) * now.Sub(s.activeSince)
	}
	s.activeSince = now
}

var latencyBounds = [...]time.Duration{
	time.Millisecond, 2 * time.Millisecond, 5 * time.Millisecond,
	10 * time.Millisecond, 20 * time.Millisecond, 50 * time.Millisecond,
	100 * time.Millisecond, 200 * time.Millisecond, 500 * time.Millisecond,
	time.Second, 2 * time.Second, 5 * time.Second, 10 * time.Second,
	30 * time.Second, time.Minute, 2 * time.Minute, 5 * time.Minute,
	10 * time.Minute, 20 * time.Minute,
}

type latencyHistogram struct {
	buckets [len(latencyBounds) + 1]int64
	max     time.Duration
}

func (h *latencyHistogram) add(elapsed time.Duration) {
	i := sort.Search(len(latencyBounds), func(i int) bool { return elapsed <= latencyBounds[i] })
	h.buckets[i]++
	h.max = max(h.max, elapsed)
}

// Percentiles are bucket upper bounds, including the observed maximum for
// the overflow bucket. They are not exact individual request measurements.
func (h latencyHistogram) percentile(count, percent int64) int64 {
	target := (count*percent + 99) / 100
	if target == 0 {
		return 0
	}
	var seen int64
	for i, n := range h.buckets {
		seen += n
		if seen >= target {
			if i == len(latencyBounds) {
				return h.max.Milliseconds()
			}
			return latencyBounds[i].Milliseconds()
		}
	}
	return 0
}

type PerformanceRow struct {
	Route        string `json:"route"`
	Method       string `json:"method,omitempty"`
	Completed    int64  `json:"completed"`
	ServerErrors int64  `json:"server_errors,omitempty"`
	Failures     int64  `json:"failures,omitempty"`
	Canceled     int64  `json:"canceled,omitempty"`
	Active       int64  `json:"active"`
	Peak         int64  `json:"peak"`
	ActiveMS     int64  `json:"active_ms"`
	P50MS        int64  `json:"p50_ms"`
	P95MS        int64  `json:"p95_ms"`
	P99MS        int64  `json:"p99_ms"`
	MaxMS        int64  `json:"max_ms"`
	FirstP95MS   int64  `json:"first_response_p95_ms,omitempty"`
}

type HTTPPerformance struct {
	Completed int64 `json:"completed"`
	Active    int64 `json:"active"`
	Peak      int64 `json:"peak"`
}

type PoolPerformance struct {
	InUse  int   `json:"in_use"`
	Open   int   `json:"open"`
	Waits  int64 `json:"waits"`
	WaitMS int64 `json:"wait_ms"`
}

type PoolPerformanceWindow struct {
	Writer PoolPerformance `json:"writer"`
	Reader PoolPerformance `json:"reader"`
}

type PerformanceWindow struct {
	HTTP     HTTPPerformance       `json:"http"`
	Pools    PoolPerformanceWindow `json:"db_pools"`
	WindowMS int64                 `json:"window_ms"`
	Rows     []PerformanceRow      `json:"routes"`
	DBInUse  int                   `json:"db_in_use"`
	DBOpen   int                   `json:"db_open"`
	DBWaits  int64                 `json:"db_waits"`
	DBWaitMS int64                 `json:"db_wait_ms"`
}

type performanceContextKey struct{}

func NewPerformance() *Performance {
	return &Performance{series: make(map[performanceKey]*performanceSeries)}
}

func (p *Performance) begin(key performanceKey) func(time.Duration, time.Duration, bool, bool) {
	p.mu.Lock()
	s := p.series[key]
	if s == nil {
		s = &performanceSeries{}
		p.series[key] = s
	}
	s.accrue(time.Now())
	s.active++
	s.peak = max(s.peak, s.active)
	if key.method != "" {
		p.http.Active++
		p.http.Peak = max(p.http.Peak, p.http.Active)
	}
	p.mu.Unlock()
	return func(total, first time.Duration, failed, canceled bool) {
		p.mu.Lock()
		s.accrue(time.Now())
		s.active--
		s.count++
		if key.method != "" {
			p.http.Active--
			p.http.Completed++
		}
		if failed {
			s.failed++
		}
		if canceled {
			s.canceled++
		}
		s.total.add(total)
		if key.method != "" {
			s.first.add(first)
		}
		p.mu.Unlock()
	}
}

func (p *Performance) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		finish := p.begin(performanceKey{performanceRoute(r.URL.Path), performanceMethod(r.Method)})
		writer := &performanceWriter{ResponseWriter: w, started: started}
		returned := false
		defer func() {
			elapsed := time.Since(started)
			first := writer.first
			if !writer.responded {
				first = elapsed
			}
			finish(elapsed, first, !returned || writer.status >= 500, r.Context().Err() != nil)
		}()
		root := "http." + performanceMethod(r.Method) + "." + performanceRoute(r.URL.Path)
		ctx := context.WithValue(p.Context(r.Context()), connectionRootKey{}, root)
		next.ServeHTTP(writer, r.WithContext(ctx))
		returned = true
	})
}

// TimeTransaction measures a transaction's work after acquisition. Register it
// before deferred rollback and finish it immediately after commit. Game callers
// begin timing after their writer lock and decision clock have been acquired.
func TimeTransaction(ctx context.Context, operation string) func() {
	p, _ := ctx.Value(performanceContextKey{}).(*Performance)
	if p == nil {
		return func() {}
	}
	switch operation {
	case "session", "catalog", "blackjack_state", "duel_state":
	default:
		return func() {}
	}
	started := time.Now()
	finish := p.begin(performanceKey{"transaction." + operation, ""})
	return sync.OnceFunc(func() { finish(time.Since(started), 0, false, ctx.Err() != nil) })
}

func (p *Performance) Drain() []PerformanceRow {
	rows, _ := p.drain()
	return rows
}

func (p *Performance) drain() ([]PerformanceRow, HTTPPerformance) {
	p.mu.Lock()
	defer p.mu.Unlock()
	rows := make([]PerformanceRow, 0, len(p.series))
	now := time.Now()
	for key, s := range p.series {
		if s.count == 0 && s.peak == 0 {
			continue
		}
		s.accrue(now)
		row := PerformanceRow{
			Route: key.route, Method: key.method, Completed: s.count,
			ServerErrors: s.failed, Canceled: s.canceled, Active: s.active, Peak: s.peak,
			ActiveMS: s.activeTime.Milliseconds(),
			P50MS:    s.total.percentile(s.count, 50), P95MS: s.total.percentile(s.count, 95),
			P99MS: s.total.percentile(s.count, 99), MaxMS: s.total.max.Milliseconds(),
			FirstP95MS: s.first.percentile(s.count, 95),
		}
		if key.method == "" {
			row.Failures, row.ServerErrors = s.failed, 0
		}
		rows = append(rows, row)
		*s = performanceSeries{active: s.active, peak: s.active, activeSince: now}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Route == rows[j].Route {
			return rows[i].Method < rows[j].Method
		}
		return rows[i].Route < rows[j].Route
	})
	http := p.http
	p.http = HTTPPerformance{Active: http.Active, Peak: http.Active}
	return rows, http
}

// Run emits one aggregate per minute. DB waiting is a pool-wide delta and
// cannot be attributed to an individual route or request from this report.
func (p *Performance) Run(ctx context.Context, stats func() (sql.DBStats, sql.DBStats), emit func(PerformanceWindow)) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	previousWriter, previousReader := stats()
	since := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			now := time.Now()
			writer, reader := stats()
			rows, http := p.drain()
			delta := func(current, previous sql.DBStats) PoolPerformance {
				return PoolPerformance{InUse: current.InUse, Open: current.OpenConnections,
					Waits:  current.WaitCount - previous.WaitCount,
					WaitMS: (current.WaitDuration - previous.WaitDuration).Milliseconds()}
			}
			pools := PoolPerformanceWindow{Writer: delta(writer, previousWriter), Reader: delta(reader, previousReader)}
			emit(PerformanceWindow{
				WindowMS: now.Sub(since).Milliseconds(), Rows: rows, HTTP: http, Pools: pools,
				DBInUse: writer.InUse + reader.InUse, DBOpen: writer.OpenConnections + reader.OpenConnections,
				DBWaits:  pools.Writer.Waits + pools.Reader.Waits,
				DBWaitMS: ((writer.WaitDuration - previousWriter.WaitDuration) + (reader.WaitDuration - previousReader.WaitDuration)).Milliseconds(),
			})
			previousWriter, previousReader, since = writer, reader, now
		}
	}
}

type performanceWriter struct {
	http.ResponseWriter
	started   time.Time
	first     time.Duration
	status    int
	responded bool
}

func (w *performanceWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *performanceWriter) WriteHeader(status int) {
	if status >= 200 && !w.responded {
		w.responded, w.status, w.first = true, status, time.Since(w.started)
	}
	w.ResponseWriter.WriteHeader(status)
}
func (w *performanceWriter) Write(body []byte) (int, error) {
	if !w.responded {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(body)
}
func (w *performanceWriter) Flush() {
	_ = w.FlushError()
}
func (w *performanceWriter) FlushError() error {
	if !w.responded {
		w.WriteHeader(http.StatusOK)
	}
	return http.NewResponseController(w.ResponseWriter).Flush()
}

func performanceMethod(method string) string {
	switch method {
	case "GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS":
		return method
	default:
		return "OTHER"
	}
}

func performanceRoute(path string) string {
	switch path {
	case "/healthz", "/readyz":
		return "health"
	case "/api/session", "/api/me", "/api/usage", "/api/config", "/api/homepage",
		"/api/home/game-summary", "/api/games", "/api/checkin", "/api/checkin/game",
		"/api/charity/models", "/api/models", "/api/caller-key", "/api/announcements":
		return path
	case "/v1/chat/completions", "/v1/responses", "/v1/messages", "/v1/embeddings",
		"/v1/images/generations", "/v1/decisions", "/v1/models":
		return path
	}
	if strings.HasPrefix(path, "/api/games/") {
		game, _, _ := strings.Cut(strings.TrimPrefix(path, "/api/games/"), "/")
		switch game {
		case "blackjack", "bidding", "likes", "linklink", "rps", "fishing", "lake-notes", "gacha", "steady-catch", "gwent":
			if strings.HasSuffix(path, "/state") {
				return "game." + game + ".state"
			}
			return "game." + game
		}
		return "game.other"
	}
	for _, prefix := range []string{"/admin/api/", "/api/", "/v1/", "/assets/"} {
		if strings.HasPrefix(path, prefix) {
			return prefix + "other"
		}
	}
	return "page.other"
}
