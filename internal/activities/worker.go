package activities

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

const (
	defaultSettlementSweep      = 30 * time.Second
	initialSettlementRetryDelay = 30 * time.Second
	maximumSettlementRetryDelay = time.Hour
)

type SettlementWorker struct {
	service  *Service
	interval time.Duration
	closed   chan struct{}
	close    sync.Once
}

func NewSettlementWorker(service *Service, interval time.Duration) (*SettlementWorker, error) {
	if service == nil || service.repository == nil || interval < 0 {
		return nil, errors.New("activities: settlement worker dependencies are required")
	}
	if interval == 0 {
		interval = defaultSettlementSweep
	}
	return &SettlementWorker{service: service, interval: interval, closed: make(chan struct{})}, nil
}

// RecoverBeforeListener first proves global ledger/capacity recovery, then
// mechanically drains every persisted due activity checkpoint before a
// listener is exposed.
func (worker *SettlementWorker) RecoverBeforeListener(ctx context.Context) error {
	if worker == nil || worker.service == nil || ctx == nil {
		return ErrInvalidRequest
	}
	select {
	case <-worker.closed:
		return ErrClosed
	default:
	}
	tx, err := worker.service.repository.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return classifyDatabaseError("begin activity recovery validation", err)
	}
	if err := ledger.ValidateRecovery(ctx, tx); err != nil {
		tx.Rollback()
		return classifyLedgerError("validate activity recovery", err)
	}
	if err := tx.Commit(); err != nil {
		return classifyDatabaseError("commit activity recovery validation", err)
	}
	return worker.drain(ctx)
}

func (worker *SettlementWorker) drain(ctx context.Context) error {
	for {
		select {
		case <-worker.closed:
			return ErrClosed
		default:
		}
		result, err := worker.service.RunSettlementStep(ctx)
		if err != nil {
			return err
		}
		if !result.Changed {
			return nil
		}
	}
}

// Run is deliberately blocking and never creates a detached goroutine. The
// root owner chooses whether and where to run it and owns its lifetime.
func (worker *SettlementWorker) Run(ctx context.Context) error {
	if worker == nil || ctx == nil {
		return ErrInvalidRequest
	}
	retryDelay := initialSettlementRetryDelay
	for {
		err := worker.RecoverBeforeListener(ctx)
		if err == nil {
			break
		}
		if errors.Is(err, ErrClosed) {
			return nil
		}
		if !errors.Is(err, ErrRetryable) {
			return err
		}
		if err := worker.wait(ctx, retryDelay); err != nil {
			return err
		}
		retryDelay = nextSettlementRetryDelay(retryDelay)
	}

	delay := worker.interval
	retryDelay = initialSettlementRetryDelay
	for {
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			stopTimer(timer)
			return ctx.Err()
		case <-worker.closed:
			stopTimer(timer)
			return nil
		case <-timer.C:
			if err := worker.drain(ctx); err != nil {
				if errors.Is(err, ErrClosed) {
					return nil
				}
				if !errors.Is(err, ErrRetryable) {
					return err
				}
				delay = retryDelay
				retryDelay = nextSettlementRetryDelay(retryDelay)
				continue
			}
			delay = worker.interval
			retryDelay = initialSettlementRetryDelay
		}
	}
}

func (worker *SettlementWorker) wait(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer stopTimer(timer)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-worker.closed:
		return ErrClosed
	case <-timer.C:
		return nil
	}
}

func nextSettlementRetryDelay(current time.Duration) time.Duration {
	if current <= 0 {
		return initialSettlementRetryDelay
	}
	if current >= maximumSettlementRetryDelay/2 {
		return maximumSettlementRetryDelay
	}
	return current * 2
}

func stopTimer(timer *time.Timer) {
	if timer != nil && !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
}

func (worker *SettlementWorker) Close() {
	if worker != nil {
		worker.close.Do(func() { close(worker.closed) })
	}
}
