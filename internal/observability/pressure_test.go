package observability

import (
	"context"
	"database/sql"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

func pressureDatabase(t *testing.T) *sql.DB {
	t.Helper()
	database, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "pressure.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	database.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = database.Close() })
	if _, err := database.Exec(db.GlobalPressureSchema); err != nil {
		t.Fatal(err)
	}
	return database
}

func TestPressureWindowAndRestart(t *testing.T) {
	database := pressureDatabase(t)
	reading := PressureReading{RPM: 3, RPMIntervalPeak: 19, RPMLimit: 600, Concurrency: 1, ConcurrencyIntervalPeak: 7, ConcurrencyLimit: 256}
	monitor, err := NewPressureMonitor(context.Background(), database, func() PressureReading { return reading })
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().Truncate(time.Minute).Add(-time.Minute)
	monitor.sample(at.Add(-time.Minute))
	monitor.sample(at)
	first := monitor.Snapshot()
	if first.RPM.Current != 3 || first.RPM.Peak24h != 19 || first.Concurrency.PeakAll != 7 {
		t.Fatalf("short interval peak lost: %+v", first)
	}
	if err := monitor.flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`CREATE TRIGGER protect_historical_pressure BEFORE UPDATE ON global_pressure_samples
WHEN OLD.observed_at < (SELECT max(observed_at) FROM global_pressure_samples)
BEGIN SELECT RAISE(ABORT,'old pressure minute rewritten'); END`); err != nil {
		t.Fatal(err)
	}
	restarted, err := NewPressureMonitor(context.Background(), database, func() PressureReading { return reading })
	if err != nil {
		t.Fatal(err)
	}
	reading = PressureReading{RPM: 2, RPMLimit: 10, Concurrency: 0, ConcurrencyLimit: 4}
	restarted.sample(at.Add(30 * time.Second))
	value := restarted.Snapshot()
	if value.StartedAt != first.StartedAt || value.RPM.PeakAll != 19 || value.RPM.Peak24h != 19 || value.RPM.Limit != 10 || value.Concurrency.Limit != 4 {
		t.Fatalf("restart or changed limit lost: %+v", value)
	}
	if err := restarted.flush(context.Background()); err != nil {
		t.Fatalf("restart rewrote historical minutes: %v", err)
	}
	restarted.sample(at.Add(pressureWindow + time.Minute))
	value = restarted.Snapshot()
	if value.RPM.Peak24h != 2 || value.Concurrency.Peak24h != 0 || value.RPM.PeakAll != 19 || value.Concurrency.PeakAll != 7 {
		t.Fatalf("24-hour boundary is incorrect: %+v", value)
	}
	if err := restarted.flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	var oldest int64
	if err := database.QueryRow(`SELECT min(observed_at) FROM global_pressure_samples`).Scan(&oldest); err != nil || oldest <= at.Unix() {
		t.Fatalf("expired samples retained: oldest=%d err=%v", oldest, err)
	}
}

func TestPressureFailedFlushRetriesWithoutLosingPeaks(t *testing.T) {
	database := pressureDatabase(t)
	reading := PressureReading{RPM: 8, RPMLimit: 100, Concurrency: 3, ConcurrencyLimit: 20}
	monitor, err := NewPressureMonitor(context.Background(), database, func() PressureReading { return reading })
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().Truncate(time.Minute)
	monitor.sample(at)
	if _, err := database.Exec(`CREATE TRIGGER fail_pressure BEFORE INSERT ON global_pressure_samples BEGIN SELECT RAISE(ABORT,'write unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	if err := monitor.flush(context.Background()); err == nil {
		t.Fatal("expected write failure")
	}
	if monitor.flushedAt != 0 {
		t.Fatal("failed flush advanced the cursor")
	}
	reading.RPM, reading.Concurrency = 1, 0
	monitor.sample(at.Add(time.Minute))
	if _, err := database.Exec(`DROP TRIGGER fail_pressure`); err != nil {
		t.Fatal(err)
	}
	if err := monitor.flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	var peak, count int
	if err := database.QueryRow(`SELECT rpm_peak,(SELECT count(*) FROM global_pressure_samples) FROM global_pressure_state`).Scan(&peak, &count); err != nil || peak != 8 || count != 2 {
		t.Fatalf("retry lost samples: peak=%d count=%d err=%v", peak, count, err)
	}
}

func TestPressureConcurrentReadersAndShutdownFlush(t *testing.T) {
	database := pressureDatabase(t)
	monitor, err := NewPressureMonitor(context.Background(), database, func() PressureReading {
		return PressureReading{RPM: 6, RPMLimit: 100, Concurrency: 2, ConcurrencyLimit: 10}
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	var readers sync.WaitGroup
	readers.Add(1)
	go func() {
		defer readers.Done()
		for range 1000 {
			_ = monitor.Snapshot()
		}
		cancel()
	}()
	monitor.Run(ctx)
	readers.Wait()
	var peak int
	if err := database.QueryRow(`SELECT rpm_peak FROM global_pressure_state`).Scan(&peak); err != nil || peak != 6 {
		t.Fatalf("shutdown did not save the last sample: peak=%d err=%v", peak, err)
	}
}

func TestPressureUnflushedSeriesStaysBounded(t *testing.T) {
	monitor := &PressureMonitor{source: func() PressureReading { return PressureReading{RPM: 1} }}
	at := time.Now()
	for i := range maxPressureSamples + 3 {
		monitor.sample(at.Add(time.Duration(i) * pressureBucket))
	}
	if len(monitor.points) > maxPressureSamples || monitor.Snapshot().RPM.PeakAll != 1 {
		t.Fatalf("unbounded unsaved series: %d", len(monitor.points))
	}
}

func TestPressureFlushBoundsBacklogAndDefersForBusyWriter(t *testing.T) {
	database := pressureDatabase(t)
	monitor, err := NewPressureMonitor(context.Background(), database, func() PressureReading {
		return PressureReading{RPM: 5, RPMLimit: 600}
	})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().Truncate(time.Minute)
	for i := range pressureFlushBatch * 3 {
		monitor.sample(at.Add(time.Duration(i) * time.Minute))
	}
	// Occupy the writer pool. Optional monitoring must give up promptly.
	writer, err := database.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), pressureFlushBudget)
	started := time.Now()
	err = monitor.flush(ctx)
	cancel()
	if closeErr := writer.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if err == nil || time.Since(started) > time.Second || monitor.flushedAt != 0 {
		t.Fatalf("busy writer was not deferred: elapsed=%v err=%v", time.Since(started), err)
	}
	if err := monitor.flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := database.QueryRow(`SELECT count(*) FROM global_pressure_samples`).Scan(&count); err != nil || count != pressureFlushBatch {
		t.Fatalf("unbounded backlog transaction: count=%d err=%v", count, err)
	}
	if monitor.Snapshot().RPM.PeakAll != 5 {
		t.Fatal("persistence failure affected in-memory statistics")
	}
}

func BenchmarkPressureSampleFullWindow(b *testing.B) {
	monitor := &PressureMonitor{source: func() PressureReading { return PressureReading{RPM: 600, Concurrency: 256} }}
	at := time.Now().Truncate(time.Minute)
	for i := range maxPressureSamples {
		monitor.sample(at.Add(time.Duration(i) * time.Minute))
	}
	at = at.Add(time.Duration(maxPressureSamples-1) * time.Minute)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		monitor.sample(at)
	}
}
