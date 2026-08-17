// Retention cleanup: the DEC-006 / api-contract 30-day request-log retention
// policy. The cleanup is an explicitly callable, bounded loop of
// single-transaction batch deletes; a later rail wires it to a startup
// scheduler without changing this package. It never touches user_issues,
// admin_alerts, export data, or the users accumulator columns (the
// server-authoritative usage totals survive cleanup by design), and it never
// writes log content — the only output is the bounded summary below.
//
// Safety: the loop is synchronous (no goroutines), checks the caller's
// context before every batch and propagates cancellation, and stops at the
// batch-count or wall-clock bound so a run can be resumed by the next
// scheduled round. The store serializes writers, so concurrent RecordRequest
// calls queue behind each batch, and a closed store fails the batch with a
// stable error.
package usage

import (
	"context"
	"fmt"
	"time"
)

const (
	// DefaultRetention is the frozen 30-day request-log retention window.
	DefaultRetention = 30 * 24 * time.Hour
	// DefaultCleanupBatchSize bounds one delete batch (rows per transaction).
	DefaultCleanupBatchSize = 500
	// DefaultCleanupMaxBatches bounds one cleanup run's batch count.
	DefaultCleanupMaxBatches = 1000
	// DefaultCleanupMaxDuration bounds one cleanup run's wall-clock time.
	DefaultCleanupMaxDuration = 30 * time.Second
)

// CleanupSummary reports one retention-cleanup run. Bounded metadata only;
// no log content is ever emitted.
type CleanupSummary struct {
	Cutoff       time.Time // server-side UTC cutoff; rows with started_at strictly before it were deleted
	Batches      int
	Deleted      int64
	StoppedEarly bool // the run hit a batch/time/cancel bound before the table was fully swept
}

// CleanupRequestLogs runs one bounded retention sweep with the default
// server-side cutoff (now - retention, UTC): log rows whose started_at is
// strictly older are deleted in bounded single-transaction batches. The
// cutoff is derived from the service clock, never from client input.
func (s *Service) CleanupRequestLogs(ctx context.Context) (CleanupSummary, error) {
	return s.CleanupRequestLogsBefore(ctx, s.now().UTC().Add(-s.cleanupRetention))
}

// CleanupRequestLogsBefore runs one bounded retention sweep against an
// explicit cutoff. This is an operator/scheduler seam for tests, dry-runs and
// targeted sweeps; the cutoff must be generated server-side (e.g. a fixed
// point-in-time) — client-supplied values must never reach this method.
// Rows with started_at strictly before the cutoff are deleted. The sweep
// stops early at the batch-count or wall-clock bound, or on context
// cancellation (returned wrapped); the summary reports what was done and a
// later round can continue.
func (s *Service) CleanupRequestLogsBefore(ctx context.Context, before time.Time) (CleanupSummary, error) {
	if s == nil || s.store == nil || s.now == nil {
		return CleanupSummary{}, fmt.Errorf("cleanup request logs: service is unavailable")
	}
	if ctx == nil {
		return CleanupSummary{}, fmt.Errorf("cleanup request logs: context is required")
	}
	if before.IsZero() {
		return CleanupSummary{}, fmt.Errorf("cleanup request logs: cutoff is required")
	}
	cutoff := before.UTC()
	summary := CleanupSummary{Cutoff: cutoff}
	started := time.Now()
	for batch := 0; batch < s.cleanupMaxBatches; batch++ {
		if err := ctx.Err(); err != nil {
			summary.StoppedEarly = true
			return summary, fmt.Errorf("cleanup request logs: %w", err)
		}
		if time.Since(started) >= s.cleanupMaxDuration {
			summary.StoppedEarly = true
			break
		}
		deleted, err := s.store.DeleteRequestLogsBefore(ctx, cutoff.Unix(), s.cleanupBatchSize)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				summary.StoppedEarly = true
				return summary, fmt.Errorf("cleanup request logs: %w", ctxErr)
			}
			return summary, fmt.Errorf("cleanup request logs: %w", err)
		}
		summary.Batches++
		summary.Deleted += deleted
		if deleted < int64(s.cleanupBatchSize) {
			break // the table is fully swept
		}
	}
	if summary.Batches == s.cleanupMaxBatches {
		summary.StoppedEarly = true
	}
	s.logger.Info("cleanup request logs",
		"cutoff_unix", cutoff.Unix(), "batches", summary.Batches,
		"deleted", summary.Deleted, "stopped_early", summary.StoppedEarly)
	return summary, nil
}

// CountRequestLogsBefore returns how many metadata-only log rows are strictly
// older than before (dry-run seam for operators and the scheduler).
func (s *Service) CountRequestLogsBefore(ctx context.Context, before time.Time) (int64, error) {
	if s == nil || s.store == nil {
		return 0, fmt.Errorf("cleanup request logs: service is unavailable")
	}
	if ctx == nil {
		return 0, fmt.Errorf("cleanup request logs: context is required")
	}
	if before.IsZero() {
		return 0, fmt.Errorf("cleanup request logs: cutoff is required")
	}
	return s.store.CountRequestLogsBefore(ctx, before.UTC().Unix())
}
