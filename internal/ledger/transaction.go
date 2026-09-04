package ledger

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync/atomic"

	sqlite "modernc.org/sqlite"
)

const maxUnix = int64(253402300799)

var savepointSequence atomic.Uint64

func validUnix(value int64) bool { return value >= 0 && value <= maxUnix }

func withSavepoint(ctx context.Context, tx *sql.Tx, fn func() error) error {
	if ctx == nil || tx == nil || fn == nil {
		return ErrInvalidPlan
	}
	name := fmt.Sprintf("ledger_%d", savepointSequence.Add(1))
	if _, err := tx.ExecContext(ctx, "SAVEPOINT "+name); err != nil {
		return classifySQLError("open savepoint", err)
	}
	if err := fn(); err != nil {
		_, rollbackErr := tx.ExecContext(ctx, "ROLLBACK TO "+name)
		_, releaseErr := tx.ExecContext(ctx, "RELEASE "+name)
		if rollbackErr != nil || releaseErr != nil {
			return fmt.Errorf("%w: savepoint rollback after %v", ErrInvariant, err)
		}
		return err
	}
	if _, err := tx.ExecContext(ctx, "RELEASE "+name); err != nil {
		return classifySQLError("release savepoint", err)
	}
	return nil
}

func classifySQLError(operation string, err error) error {
	if err == nil {
		return nil
	}
	var sqliteErr *sqlite.Error
	if errors.As(err, &sqliteErr) {
		code := sqliteErr.Code() & 0xff
		if code == 5 || code == 6 { // SQLITE_BUSY or SQLITE_LOCKED.
			return fmt.Errorf("%w: %s", ErrRetryable, operation)
		}
	}
	return fmt.Errorf("%s: %w", operation, err)
}
