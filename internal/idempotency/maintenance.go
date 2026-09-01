package idempotency

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

const maximumMaintenanceBatch = 100

// MaintenanceResult is the closed, bounded outcome consumed by lifecycle.
// More reports whether another row matching the same frozen decision time
// remained after this transaction committed.
type MaintenanceResult struct {
	Processed int
	More      bool
}

// Maintenance owns the all-scope idempotency recovery and retention queries.
// Mutation services continue to own replay authorization and domain work.
type Maintenance struct {
	database *sql.DB
}

func NewMaintenance(database *sql.DB) *Maintenance {
	return &Maintenance{database: database}
}

// Recover removes visible in-progress rows and records whose fixed replay
// window has expired. A legitimate mutation completes its accepted row in the
// same transaction, so a committed accepted row cannot represent replayable
// domain success. Completed rows inside their 24-hour window are untouched.
func (maintenance *Maintenance) Recover(
	ctx context.Context,
	decisionNow int64,
	limit int,
	budgetDeadline time.Time,
) (MaintenanceResult, error) {
	return maintenance.deleteBatch(ctx, decisionNow, limit, budgetDeadline, maintenanceRecovery)
}

// Retain removes records at the exact fixed-window boundary across every
// canonical scope. Domain completion never refreshes expires_at.
func (maintenance *Maintenance) Retain(
	ctx context.Context,
	decisionNow int64,
	limit int,
	budgetDeadline time.Time,
) (MaintenanceResult, error) {
	return maintenance.deleteBatch(ctx, decisionNow, limit, budgetDeadline, maintenanceRetention)
}

type maintenanceOperation uint8

const (
	maintenanceRecovery maintenanceOperation = iota + 1
	maintenanceRetention
)

func (maintenance *Maintenance) deleteBatch(
	ctx context.Context,
	decisionNow int64,
	limit int,
	budgetDeadline time.Time,
	operation maintenanceOperation,
) (MaintenanceResult, error) {
	if maintenance == nil || maintenance.database == nil || ctx == nil ||
		decisionNow < 0 || decisionNow > maxUnixSecond ||
		limit < 1 || limit > maximumMaintenanceBatch || budgetDeadline.IsZero() {
		return MaintenanceResult{}, errors.New("idempotency maintenance input is invalid")
	}

	workerCtx, cancel := context.WithDeadline(ctx, budgetDeadline)
	defer cancel()
	tx, err := maintenance.database.BeginTx(workerCtx, nil)
	if err != nil {
		return MaintenanceResult{}, fmt.Errorf("begin idempotency maintenance: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var result sql.Result
	switch operation {
	case maintenanceRecovery:
		result, err = tx.ExecContext(workerCtx, `
DELETE FROM idempotency_records
WHERE rowid IN (
 SELECT rowid FROM idempotency_records
 WHERE state='accepted' OR expires_at<=?
 ORDER BY CASE WHEN state='accepted' THEN 0 ELSE 1 END,
          expires_at,scope,actor_scope_hash,key_hash
 LIMIT ?
)`, decisionNow, limit)
	case maintenanceRetention:
		result, err = tx.ExecContext(workerCtx, `
DELETE FROM idempotency_records
WHERE rowid IN (
 SELECT rowid FROM idempotency_records
 WHERE expires_at<=?
 ORDER BY expires_at,scope,actor_scope_hash,key_hash
 LIMIT ?
)`, decisionNow, limit)
	default:
		return MaintenanceResult{}, errors.New("idempotency maintenance operation is invalid")
	}
	if err != nil {
		return MaintenanceResult{}, fmt.Errorf("delete idempotency maintenance batch: %w", err)
	}
	processed, err := result.RowsAffected()
	if err != nil {
		return MaintenanceResult{}, fmt.Errorf("observe idempotency maintenance batch: %w", err)
	}
	if processed < 0 || processed > int64(limit) {
		return MaintenanceResult{}, ErrState
	}

	var more int
	switch operation {
	case maintenanceRecovery:
		err = tx.QueryRowContext(workerCtx, `SELECT EXISTS(
 SELECT 1 FROM idempotency_records
 WHERE state='accepted' OR expires_at<=?
)`, decisionNow).Scan(&more)
	case maintenanceRetention:
		err = tx.QueryRowContext(workerCtx, `SELECT EXISTS(
 SELECT 1 FROM idempotency_records WHERE expires_at<=?
)`, decisionNow).Scan(&more)
	}
	if err != nil {
		return MaintenanceResult{}, fmt.Errorf("check remaining idempotency maintenance work: %w", err)
	}
	if more != 0 && more != 1 {
		return MaintenanceResult{}, ErrState
	}
	if err := tx.Commit(); err != nil {
		return MaintenanceResult{}, fmt.Errorf("commit idempotency maintenance: %w", err)
	}
	return MaintenanceResult{Processed: int(processed), More: more == 1}, nil
}
