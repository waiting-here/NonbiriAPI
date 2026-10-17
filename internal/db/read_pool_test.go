package db

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestReadPoolPreservesSnapshotsWithoutBlockingWriter(t *testing.T) {
	store := openTestStore(t, filepath.Join(privateDBDir(t), "read-pool.sqlite"))
	reader := store.ReadDB()
	if reader == nil || reader == store.DB() || reader.Stats().MaxOpenConnections != readPoolConnections {
		t.Fatal("missing bounded independent read pool")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	tx, err := reader.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var before string
	if err := tx.QueryRowContext(ctx, "SELECT value FROM site_config WHERE key='site_name'").Scan(&before); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(ctx, "UPDATE site_config SET value=? WHERE key='site_name'", "committed beside a reader"); err != nil {
		t.Fatalf("read snapshot blocked writer: %v", err)
	}
	var snapshot, latest string
	if err := tx.QueryRowContext(ctx, "SELECT value FROM site_config WHERE key='site_name'").Scan(&snapshot); err != nil {
		t.Fatal(err)
	}
	if err := reader.QueryRowContext(ctx, "SELECT value FROM site_config WHERE key='site_name'").Scan(&latest); err != nil {
		t.Fatal(err)
	}
	if snapshot != before || latest != "committed beside a reader" {
		t.Fatalf("snapshot=%q before=%q latest=%q", snapshot, before, latest)
	}
	if _, err := tx.ExecContext(ctx, "UPDATE site_config SET value='forbidden' WHERE key='site_name'"); err == nil {
		t.Fatal("reader became a writer")
	}
}

func TestReadPoolBoundsAndReplacement(t *testing.T) {
	store := openTestStore(t, filepath.Join(privateDBDir(t), "bounded-reads.sqlite"))
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	connections := make([]*sql.Conn, 0, readPoolConnections)
	defer func() {
		for _, conn := range connections {
			_ = conn.Close()
		}
	}()
	for range readPoolConnections {
		conn, err := store.ReadDB().Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		connections = append(connections, conn)
	}
	waitCtx, stop := context.WithTimeout(ctx, 30*time.Millisecond)
	defer stop()
	if conn, err := store.ReadDB().Conn(waitCtx); !errors.Is(err, context.DeadlineExceeded) {
		if conn != nil {
			conn.Close()
		}
		t.Fatalf("exhausted reader pool returned %v", err)
	}
	if err := store.DB().PingContext(ctx); err != nil {
		t.Fatalf("exhausted reader pool consumed writer: %v", err)
	}
	stats := store.Stats()
	if stats.MaxOpenConnections != readPoolConnections+1 || stats.WaitCount != store.DB().Stats().WaitCount+store.ReadDB().Stats().WaitCount || stats.WaitDuration < 20*time.Millisecond {
		t.Fatalf("combined stats lost reader waits: %+v", stats)
	}
	for _, conn := range connections {
		_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		_ = conn.Close()
	}
	connections = nil
	conn, err := store.ReadDB().Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	for _, pragma := range []struct {
		name string
		want int
	}{{"query_only", 1}, {"foreign_keys", 1}, {"busy_timeout", 5000}} {
		var value int
		if err := conn.QueryRowContext(ctx, "PRAGMA "+pragma.name).Scan(&value); err != nil || value != pragma.want {
			t.Fatalf("replacement %s=%d, error=%v", pragma.name, value, err)
		}
	}
	if _, err := conn.ExecContext(ctx, "PRAGMA query_only=OFF"); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, "UPDATE site_config SET value='forbidden' WHERE key='site_name'"); err == nil {
		t.Fatal("read-only URI accepted writes after query_only was disabled")
	}
}

func TestStoreRetainsOwnershipUntilReaderCloses(t *testing.T) {
	path := filepath.Join(privateDBDir(t), "reader-close.sqlite")
	store := openTestStore(t, path)
	conn, err := store.ReadDB().Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	err = store.CloseContext(ctx)
	var deadline *CloseDeadlineError
	if !errors.As(err, &deadline) || deadline.OpenConnections < 1 || deadline.OpenConnections != store.Stats().OpenConnections {
		t.Fatalf("close released active reader: %v", err)
	}
	if owner, err := acquireDatabaseOwner(path); !errors.Is(err, ErrDatabaseInUse) {
		if owner != nil {
			owner.release()
		}
		t.Fatalf("reader lost database ownership: %v", err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if store.Stats().OpenConnections != 0 {
		t.Fatal("reader connections survived close")
	}
	owner, err := acquireDatabaseOwner(path)
	if err != nil {
		t.Fatal(err)
	}
	owner.release()
}
