package dbtest

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

// WithoutWriter verifies that a synchronous read releases its reader and does
// not queue for the single writer connection.
func WithoutWriter(t testing.TB, writer, reader *sql.DB, read func(context.Context)) {
	t.Helper()
	if writer == reader || writer.Stats().MaxOpenConnections != 1 {
		t.Fatal("test requires distinct reader and single-connection writer pools")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	held, err := writer.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	before := writer.Stats().WaitCount
	var changes int64
	if err := held.QueryRowContext(ctx, "SELECT total_changes()").Scan(&changes); err != nil {
		t.Fatal(err)
	}
	read(ctx)
	if ctx.Err() != nil {
		t.Fatal(ctx.Err())
	}
	if writer.Stats().WaitCount != before {
		t.Fatal("pure read queued for the writer")
	}
	if reader.Stats().InUse != 0 {
		t.Fatal("pure read retained a reader connection")
	}
	var after int64
	if err := held.QueryRowContext(ctx, "SELECT total_changes()").Scan(&after); err != nil {
		t.Fatal(err)
	}
	if changes != after {
		t.Fatal("pure read changed persistent state")
	}
}
