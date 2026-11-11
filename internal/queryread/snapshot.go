package queryread

import (
	"context"
	"database/sql"
	"time"
)

// Snapshot includes pool acquisition, authorization, count, page and projection
// in one budget. Readers cannot promote to writers. Domain-specific lazy expiry
// and mutation paths must use their existing write transaction instead.
func Snapshot[T any](ctx context.Context, reader *sql.DB, budget time.Duration, authorize func(context.Context, *sql.Tx) error, read func(context.Context, *sql.Tx) (T, error)) (T, error) {
	var zero T
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	tx, err := reader.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return zero, err
	}
	defer tx.Rollback()
	if err := authorize(ctx, tx); err != nil {
		return zero, err
	}
	result, err := read(ctx, tx)
	if err != nil {
		return zero, err
	}
	if err := tx.Commit(); err != nil {
		return zero, err
	}
	return result, nil
}
