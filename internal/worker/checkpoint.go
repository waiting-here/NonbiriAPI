// Package worker provides the shared Generation 2 checkpoint, retry and
// process-local lease primitives. It persists only scheduling metadata; no
// request body, secret or free-form identity snapshot is accepted.
package worker

import (
	"context"
	"database/sql"
	"errors"
	"math"
)

type ErrorClass string

const (
	ErrorNone      ErrorClass = ""
	ErrorDBBusy    ErrorClass = "db_busy"
	ErrorRetryable ErrorClass = "internal_retryable"
	ErrorInvariant ErrorClass = "invariant_violation"
)

var (
	ErrInvalidCheckpoint = errors.New("worker: invalid checkpoint")
)

const (
	maxUnix        = int64(253402300799)
	maxAttempts    = int64(math.MaxInt32)
	initialBackoff = int64(30)
	maximumBackoff = int64(3600)
)

// Checkpoint is the exact worker_checkpoints projection. Cursor is a frozen,
// domain-defined opaque cursor; this package never interprets or enriches it.
type Checkpoint struct {
	WorkerKey     string
	Cursor        string
	Generation    int64
	AttemptCount  int64
	NextAttemptAt int64
	LastError     ErrorClass
	UpdatedAt     int64
}

// Load returns one checkpoint.
func Load(ctx context.Context, query interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, workerKey string) (Checkpoint, error) {
	if ctx == nil || query == nil || workerKey == "" {
		return Checkpoint{}, ErrInvalidCheckpoint
	}
	var checkpoint Checkpoint
	checkpoint.WorkerKey = workerKey
	var errorClass string
	err := query.QueryRowContext(ctx, `
SELECT cursor_text,generation,attempt_count,next_attempt_at,last_error_class,updated_at
FROM worker_checkpoints WHERE worker_key=?`, workerKey).Scan(
		&checkpoint.Cursor, &checkpoint.Generation, &checkpoint.AttemptCount,
		&checkpoint.NextAttemptAt, &errorClass, &checkpoint.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Checkpoint{}, sql.ErrNoRows
	}
	if err != nil {
		return Checkpoint{}, err
	}
	checkpoint.LastError = ErrorClass(errorClass)
	if !validCheckpoint(checkpoint) {
		return Checkpoint{}, ErrInvalidCheckpoint
	}
	return checkpoint, nil
}

// Initialize inserts the initial checkpoint or returns the authoritative
// existing row. It never rewinds an existing cursor or generation.
func Initialize(ctx context.Context, tx *sql.Tx, initial Checkpoint) (Checkpoint, error) {
	if ctx == nil || tx == nil || !validCheckpoint(initial) || initial.AttemptCount != 0 || initial.LastError != ErrorNone {
		return Checkpoint{}, ErrInvalidCheckpoint
	}
	_, err := tx.ExecContext(ctx, `
INSERT INTO worker_checkpoints(
 worker_key,cursor_text,generation,attempt_count,next_attempt_at,last_error_class,updated_at)
VALUES(?,?,?,0,?,'',?) ON CONFLICT(worker_key) DO NOTHING`,
		initial.WorkerKey, initial.Cursor, initial.Generation, initial.NextAttemptAt, initial.UpdatedAt)
	if err != nil {
		return Checkpoint{}, err
	}
	return Load(ctx, tx, initial.WorkerKey)
}

// CompareAndSet writes next iff every persisted field still equals previous.
// This is the checkpoint/retry linearization point used after a batch commit.
func CompareAndSet(ctx context.Context, tx *sql.Tx, previous, next Checkpoint) (bool, error) {
	if ctx == nil || tx == nil || !validCheckpoint(previous) || !validCheckpoint(next) ||
		previous.WorkerKey != next.WorkerKey || next.Generation < previous.Generation {
		return false, ErrInvalidCheckpoint
	}
	result, err := tx.ExecContext(ctx, `
UPDATE worker_checkpoints
SET cursor_text=?,generation=?,attempt_count=?,next_attempt_at=?,last_error_class=?,updated_at=?
WHERE worker_key=? AND cursor_text=? AND generation=? AND attempt_count=?
 AND next_attempt_at=? AND last_error_class=? AND updated_at=?`,
		next.Cursor, next.Generation, next.AttemptCount, next.NextAttemptAt, string(next.LastError), next.UpdatedAt,
		previous.WorkerKey, previous.Cursor, previous.Generation, previous.AttemptCount,
		previous.NextAttemptAt, string(previous.LastError), previous.UpdatedAt)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return count == 1, nil
}

// Advance builds the next successful checkpoint. It resets retry state and
// preserves all scheduling values supplied by the domain worker.
func Advance(previous Checkpoint, cursor string, generation, nextAttemptAt, now int64) (Checkpoint, error) {
	next := Checkpoint{
		WorkerKey: previous.WorkerKey, Cursor: cursor, Generation: generation,
		AttemptCount: 0, NextAttemptAt: nextAttemptAt, LastError: ErrorNone, UpdatedAt: now,
	}
	if !validCheckpoint(previous) || !validCheckpoint(next) || generation < previous.Generation {
		return Checkpoint{}, ErrInvalidCheckpoint
	}
	return next, nil
}

// Retry builds the next retry checkpoint with the frozen 30-second
// exponential backoff capped at one hour. invariant_violation is stable but
// blocked: its next-attempt remains at maxUnix instead of being auto-retried.
func Retry(previous Checkpoint, class ErrorClass, now int64) (Checkpoint, error) {
	if !validCheckpoint(previous) || class == ErrorNone || !validError(class) || now < 0 || now > maxUnix || previous.AttemptCount >= maxAttempts {
		return Checkpoint{}, ErrInvalidCheckpoint
	}
	attempt := previous.AttemptCount + 1
	nextAt := maxUnix
	if class != ErrorInvariant {
		delay := RetryDelay(attempt)
		if now <= maxUnix-delay {
			nextAt = now + delay
		}
	}
	next := previous
	next.AttemptCount = attempt
	next.NextAttemptAt = nextAt
	next.LastError = class
	next.UpdatedAt = now
	return next, nil
}

// RetryDelay returns seconds for the one-based attempt number.
func RetryDelay(attempt int64) int64 {
	if attempt <= 1 {
		return initialBackoff
	}
	delay := initialBackoff
	for i := int64(1); i < attempt && delay < maximumBackoff; i++ {
		if delay > maximumBackoff/2 {
			return maximumBackoff
		}
		delay *= 2
	}
	if delay > maximumBackoff {
		return maximumBackoff
	}
	return delay
}

// Due returns at most limit checkpoints due at now in stable scheduling order.
func Due(ctx context.Context, query interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, now int64, limit int) ([]Checkpoint, error) {
	if ctx == nil || query == nil || now < 0 || now > maxUnix || limit < 1 || limit > 100 {
		return nil, ErrInvalidCheckpoint
	}
	rows, err := query.QueryContext(ctx, `
SELECT worker_key,cursor_text,generation,attempt_count,next_attempt_at,last_error_class,updated_at
FROM worker_checkpoints WHERE next_attempt_at<=?
ORDER BY next_attempt_at,worker_key LIMIT ?`, now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Checkpoint
	for rows.Next() {
		var checkpoint Checkpoint
		var errorClass string
		if err := rows.Scan(&checkpoint.WorkerKey, &checkpoint.Cursor, &checkpoint.Generation,
			&checkpoint.AttemptCount, &checkpoint.NextAttemptAt, &errorClass, &checkpoint.UpdatedAt); err != nil {
			return nil, err
		}
		checkpoint.LastError = ErrorClass(errorClass)
		if !validCheckpoint(checkpoint) {
			return nil, ErrInvalidCheckpoint
		}
		out = append(out, checkpoint)
	}
	return out, rows.Err()
}

func validCheckpoint(checkpoint Checkpoint) bool {
	return checkpoint.WorkerKey != "" && checkpoint.Generation >= 0 && checkpoint.AttemptCount >= 0 && checkpoint.AttemptCount <= maxAttempts &&
		checkpoint.NextAttemptAt >= 0 && checkpoint.NextAttemptAt <= maxUnix && checkpoint.UpdatedAt >= 0 && checkpoint.UpdatedAt <= maxUnix &&
		validError(checkpoint.LastError) && (checkpoint.AttemptCount == 0) == (checkpoint.LastError == ErrorNone)
}

func validError(class ErrorClass) bool {
	switch class {
	case ErrorNone, ErrorDBBusy, ErrorRetryable, ErrorInvariant:
		return true
	default:
		return false
	}
}
