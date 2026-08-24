// Package runtime owns the process-local scheduler for game settlement
// recovery. It deliberately delegates every economic transition to db's
// central GameSettlementService; this package only selects due rows, records a
// bounded safe retry class, and supplies lifecycle maintenance hooks.
package runtime

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

const DefaultRecoveryInterval = 30 * time.Second

type RecoveryWorkerConfig struct {
	Settlement *db.GameSettlementService
	Store      *db.Store
	Now        func() time.Time
	Interval   time.Duration
}

// RecoverySummary contains only bounded counters; no settlement ids or raw
// errors are exposed to logs or API callers.
type RecoverySummary struct {
	Due     int
	Settled int
	Retried int
	Skipped int
}

// RecoveryWorker is intentionally stateless between ticks. Pending state and
// retry backoff are authoritative in SQLite, so a restart resumes from the
// same next_attempt_at values without an in-memory queue.
type RecoveryWorker struct {
	settlement         *db.GameSettlementService
	store              *db.Store
	now                func() time.Time
	interval           time.Duration
	listDue            func(context.Context, time.Time, int) ([]db.DueGameSettlement, error)
	settleDue          func(context.Context, string) (db.FishingRound, error)
	recordRetryFailure func(context.Context, string, time.Time, db.GameRetryErrorClass) error
}

// Worker is a compatibility alias for callers that prefer the shorter name.
type Worker = RecoveryWorker

func NewRecoveryWorker(config RecoveryWorkerConfig) (*RecoveryWorker, error) {
	if config.Settlement == nil {
		return nil, errors.New("game runtime: settlement service is required")
	}
	if config.Store == nil {
		return nil, errors.New("game runtime: store is required")
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.Interval <= 0 {
		config.Interval = DefaultRecoveryInterval
	}
	return &RecoveryWorker{settlement: config.Settlement, store: config.Store,
		now: config.Now, interval: config.Interval,
		listDue:            config.Settlement.ListDueGameSettlements,
		settleDue:          config.Settlement.SettleDueGame,
		recordRetryFailure: config.Settlement.RecordGameRetryFailure}, nil
}

// New is a small constructor alias for integration code.
func New(config RecoveryWorkerConfig) (*RecoveryWorker, error) {
	return NewRecoveryWorker(config)
}

// RecoverDue performs one bounded due batch. A manual settle racing this call
// is harmless: the central transition returns the terminal replay and no
// second ledger operation is applied. Failed rows remain reserved and receive
// only the safe retry class/backoff metadata.
func (w *RecoveryWorker) RecoverDue(ctx context.Context) (RecoverySummary, error) {
	if w == nil || w.settlement == nil || w.now == nil || ctx == nil {
		return RecoverySummary{}, errors.New("game runtime: worker unavailable")
	}
	now := w.now()
	if now.IsZero() {
		return RecoverySummary{}, errors.New("game runtime: clock is invalid")
	}
	if w.listDue == nil || w.settleDue == nil || w.recordRetryFailure == nil {
		return RecoverySummary{}, errors.New("game runtime: worker operations unavailable")
	}
	due, err := w.listDue(ctx, now, db.GameRecoveryBatchMax)
	if err != nil {
		return RecoverySummary{}, fmt.Errorf("game recovery list: %w", err)
	}
	summary := RecoverySummary{Due: len(due)}
	for _, item := range due {
		if err := ctx.Err(); err != nil {
			return summary, err
		}
		_, settleErr := w.settleDue(ctx, item.SettlementID)
		if settleErr == nil {
			summary.Settled++
			continue
		}
		if errors.Is(settleErr, db.ErrNotFound) {
			// Account deletion may have cascaded the row between due-list and
			// transition. This is an expected terminal no-op.
			summary.Skipped++
			continue
		}
		class := db.ClassifyGameRetryError(settleErr)
		if retryErr := w.recordRetryFailure(ctx, item.SettlementID, now, class); retryErr != nil {
			if errors.Is(retryErr, db.ErrNotFound) {
				summary.Skipped++
				continue
			}
			// A failed retry write must not be reported as a successful retry:
			// the row may still be due and the next scheduled tick is the only
			// retry path. Returning the error also lets synchronous startup fail
			// closed while RunTicker keeps the process alive.
			if errors.Is(retryErr, context.Canceled) || errors.Is(retryErr, context.DeadlineExceeded) {
				return summary, retryErr
			}
			return summary, fmt.Errorf("game recovery retry metadata: %w", retryErr)
		}
		summary.Retried++
	}
	return summary, nil
}

// SweepRetention runs the bounded terminal-only sweep using one caller-owned
// now snapshot. Recovery and retention are separate methods so maintenance
// can always invoke RecoverDue before this method.
func (w *RecoveryWorker) SweepRetention(ctx context.Context, now time.Time) (int64, bool, error) {
	if w == nil || w.store == nil {
		return 0, false, errors.New("game runtime: worker unavailable")
	}
	return w.store.CleanupFishingRetention(ctx, now)
}

// Run performs the required startup due batch synchronously, then listens on a
// bounded ticker. It returns cleanly on shutdown; no goroutine is owned after
// Run returns.
func (w *RecoveryWorker) Run(ctx context.Context) error {
	if w == nil || ctx == nil {
		return errors.New("game runtime: worker unavailable")
	}
	if _, err := w.RecoverDue(ctx); err != nil {
		return err
	}
	return w.RunTicker(ctx)
}

// RunTicker starts only the recurring scheduler after the caller has already
// completed the required synchronous startup RecoverDue call. Integration
// code can therefore finish the first due batch before opening the HTTP
// listener while still using the same bounded ticker loop. Calling Run remains
// the convenient all-in-one form for standalone use and performs that startup
// batch itself.
func (w *RecoveryWorker) RunTicker(ctx context.Context) error {
	if w == nil || ctx == nil {
		return errors.New("game runtime: worker unavailable")
	}
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if _, err := w.RecoverDue(ctx); err != nil {
				if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
					return nil
				}
				// Recovery is resumable from SQLite; a transient list failure
				// must not terminate the process or create a tight retry loop.
			}
		}
	}
}

// ForgetUser is a lifecycle hook for account deletion. There is no per-user
// in-memory queue to purge: pending rows are atomically cascaded by the delete
// transaction, and a late due-list item is handled as ErrNotFound.
func (w *RecoveryWorker) ForgetUser(_ int64) {}
