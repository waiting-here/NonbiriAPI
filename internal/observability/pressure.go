package observability

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"sync"
	"time"
)

const pressureSampleInterval = 5 * time.Second
const pressureFlushInterval = 30 * time.Second
const pressureWindow = 24 * time.Hour
const pressureBucket = time.Minute
const pressureFlushBudget = 100 * time.Millisecond
const maxPressureSamples = int(pressureWindow/pressureBucket) + 1
const pressureFlushBatch = 16

type PressureReading struct {
	RPM, RPMIntervalPeak, RPMLimit                         int
	Concurrency, ConcurrencyIntervalPeak, ConcurrencyLimit int
}

type PressureMetric struct {
	Current int `json:"current"`
	Limit   int `json:"limit"`
	Peak24h int `json:"peak_24h"`
	PeakAll int `json:"peak_all"`
}

type PressureSnapshot struct {
	RPM           PressureMetric `json:"rpm"`
	Concurrency   PressureMetric `json:"concurrency"`
	ObservedAt    int64          `json:"observed_at"`
	StartedAt     int64          `json:"started_at"`
	SampleSeconds int            `json:"sample_seconds"`
}

type pressureSample struct {
	at               int64
	rpm, concurrency int
}

// PressureMonitor keeps a bounded, process-wide series. Requests only change
// their existing admission counters; one worker samples and persists totals.
type PressureMonitor struct {
	db     *sql.DB
	source func() PressureReading
	mu     sync.RWMutex
	value  PressureSnapshot
	points []pressureSample
	// Only Run writes samples and flushes. Readers never access the database.
	flushedAt int64
}

func NewPressureMonitor(ctx context.Context, database *sql.DB, source func() PressureReading) (*PressureMonitor, error) {
	if database == nil || source == nil {
		return nil, errors.New("pressure monitor dependencies are required")
	}
	p := &PressureMonitor{db: database, source: source}
	now := time.Now().Unix()
	if _, err := database.ExecContext(ctx, `INSERT INTO global_pressure_state(id,started_at,rpm_peak,concurrency_peak) VALUES(1,?,0,0) ON CONFLICT(id) DO NOTHING`, now); err != nil {
		return nil, err
	}
	if err := database.QueryRowContext(ctx, `SELECT started_at,rpm_peak,concurrency_peak FROM global_pressure_state WHERE id=1`).Scan(&p.value.StartedAt, &p.value.RPM.PeakAll, &p.value.Concurrency.PeakAll); err != nil {
		return nil, err
	}
	cutoff := time.Unix(now, 0).Truncate(pressureBucket).Add(-pressureWindow).Unix()
	rows, err := database.QueryContext(ctx, `SELECT observed_at,rpm,concurrency FROM global_pressure_samples WHERE observed_at>=? AND observed_at<=? ORDER BY observed_at DESC LIMIT ?`, cutoff, now, maxPressureSamples)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var point pressureSample
		if err := rows.Scan(&point.at, &point.rpm, &point.concurrency); err != nil {
			return nil, err
		}
		p.points = append(p.points, point)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for left, right := 0, len(p.points)-1; left < right; left, right = left+1, right-1 {
		p.points[left], p.points[right] = p.points[right], p.points[left]
	}
	if len(p.points) > 0 {
		p.flushedAt = p.points[len(p.points)-1].at
	}
	p.value.SampleSeconds = int(pressureSampleInterval / time.Second)
	return p, nil
}

func (p *PressureMonitor) Snapshot() PressureSnapshot {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.value
}

func (p *PressureMonitor) sample(now time.Time) {
	reading := p.source()
	at := now.Unix()
	p.mu.Lock()
	defer p.mu.Unlock()
	// A wall-clock correction must not create an unbounded duplicate series.
	if at < p.value.ObservedAt {
		return
	}
	bucketAt := now.Truncate(pressureBucket).Unix()
	point := pressureSample{at: bucketAt, rpm: max(reading.RPM, reading.RPMIntervalPeak), concurrency: max(reading.Concurrency, reading.ConcurrencyIntervalPeak)}
	if len(p.points) > 0 && p.points[len(p.points)-1].at == bucketAt {
		point.rpm = max(point.rpm, p.points[len(p.points)-1].rpm)
		point.concurrency = max(point.concurrency, p.points[len(p.points)-1].concurrency)
		p.points[len(p.points)-1] = point
	} else {
		p.points = append(p.points, point)
	}
	// Keep the overlapping edge minute; this conservatively overestimates the
	// 24-hour peak by at most one minute rather than discarding a recent burst.
	cutoff := bucketAt - int64(pressureWindow/time.Second)
	first := 0
	for first < len(p.points) && p.points[first].at < cutoff {
		first++
	}
	first = max(first, len(p.points)-maxPressureSamples)
	if first > 0 {
		copy(p.points, p.points[first:])
		p.points = p.points[:len(p.points)-first]
	}
	rpmPeak, concurrencyPeak := 0, 0
	for _, point := range p.points {
		rpmPeak = max(rpmPeak, point.rpm)
		concurrencyPeak = max(concurrencyPeak, point.concurrency)
	}
	p.value.RPM = PressureMetric{Current: reading.RPM, Limit: reading.RPMLimit, Peak24h: rpmPeak, PeakAll: max(p.value.RPM.PeakAll, rpmPeak)}
	p.value.Concurrency = PressureMetric{Current: reading.Concurrency, Limit: reading.ConcurrencyLimit, Peak24h: concurrencyPeak, PeakAll: max(p.value.Concurrency.PeakAll, concurrencyPeak)}
	p.value.ObservedAt = at
}

func (p *PressureMonitor) flush(ctx context.Context) error {
	p.mu.RLock()
	value := p.value
	points := make([]pressureSample, 0, pressureFlushBatch)
	for _, point := range p.points {
		if point.at >= p.flushedAt {
			points = append(points, point)
			if len(points) == pressureFlushBatch {
				break
			}
		}
	}
	p.mu.RUnlock()
	if value.ObservedAt == 0 {
		return nil
	}
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE global_pressure_state SET rpm_peak=max(rpm_peak,?),concurrency_peak=max(concurrency_peak,?) WHERE id=1`, value.RPM.PeakAll, value.Concurrency.PeakAll); err != nil {
		return err
	}
	for _, point := range points {
		if _, err := tx.ExecContext(ctx, `INSERT INTO global_pressure_samples(observed_at,rpm,concurrency) VALUES(?,?,?) ON CONFLICT(observed_at) DO UPDATE SET rpm=max(rpm,excluded.rpm),concurrency=max(concurrency,excluded.concurrency)`, point.at, point.rpm, point.concurrency); err != nil {
			return err
		}
	}
	cutoff := time.Unix(value.ObservedAt, 0).Truncate(pressureBucket).Add(-pressureWindow).Unix()
	if _, err := tx.ExecContext(ctx, `DELETE FROM global_pressure_samples WHERE observed_at<? OR observed_at>?`, cutoff, value.ObservedAt); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if len(points) > 0 {
		// Repeat the last minute because new samples may raise its peak.
		p.flushedAt = points[len(points)-1].at
	}
	return nil
}

// Run is called once, after both shared admission counters have been wired.
// Cancellation flushes the last samples with a separate, bounded context.
func (p *PressureMonitor) Run(ctx context.Context) {
	p.sample(time.Now())
	sampleTicker := time.NewTicker(pressureSampleInterval)
	flushTicker := time.NewTicker(pressureFlushInterval)
	defer sampleTicker.Stop()
	defer flushTicker.Stop()
	failed := false
	flush := func() {
		flushContext, cancel := context.WithTimeout(context.Background(), pressureFlushBudget)
		defer cancel()
		if err := p.flush(flushContext); err != nil {
			if !failed {
				slog.Warn("global pressure samples could not be saved")
			}
			failed = true
		} else {
			failed = false
		}
	}
	for {
		select {
		case <-ctx.Done():
			flush()
			return
		case now := <-sampleTicker.C:
			p.sample(now)
		case <-flushTicker.C:
			flush()
		}
	}
}
