package worker

import (
	"context"
	"database/sql"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbfixture"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

func openWorkerStore(t *testing.T) *db.Store {
	t.Helper()
	vault, err := secret.New(make([]byte, secret.MasterKeyBytes))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = vault.Close() })
	path := filepath.Join(t.TempDir(), "worker.db")
	dbfixture.Materialize(t, path)
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func beginWorkerTx(t *testing.T, database *sql.DB) *sql.Tx {
	t.Helper()
	tx, err := database.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback() })
	return tx
}

func TestCheckpointAdvanceRetryCASAndDue(t *testing.T) {
	store := openWorkerStore(t)
	ctx := context.Background()
	tx := beginWorkerTx(t, store.DB())
	initial := Checkpoint{WorkerKey: "ledger-recovery", Cursor: "", Generation: 1, NextAttemptAt: 100, UpdatedAt: 90}
	current, err := Initialize(ctx, tx, initial)
	if err != nil || current != initial {
		t.Fatalf("initialize = (%+v,%v)", current, err)
	}
	next, err := Advance(current, "cursor-1", 1, 200, 101)
	if err != nil {
		t.Fatal(err)
	}
	applied, err := CompareAndSet(ctx, tx, current, next)
	if err != nil || !applied {
		t.Fatalf("advance CAS = (%v,%v)", applied, err)
	}
	if applied, err := CompareAndSet(ctx, tx, current, next); err != nil || applied {
		t.Fatalf("stale CAS = (%v,%v)", applied, err)
	}
	retry, err := Retry(next, ErrorDBBusy, 150)
	if err != nil || retry.AttemptCount != 1 || retry.NextAttemptAt != 180 || retry.LastError != ErrorDBBusy {
		t.Fatalf("retry = (%+v,%v)", retry, err)
	}
	if applied, err := CompareAndSet(ctx, tx, next, retry); err != nil || !applied {
		t.Fatalf("retry CAS = (%v,%v)", applied, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	due, err := Due(ctx, store.DB(), 179, 100)
	if err != nil || len(due) != 0 {
		t.Fatalf("due before boundary = (%v,%v)", due, err)
	}
	due, err = Due(ctx, store.DB(), 180, 100)
	if err != nil || len(due) != 1 || due[0].WorkerKey != initial.WorkerKey {
		t.Fatalf("due at boundary = (%v,%v)", due, err)
	}
}

func TestRetryBackoffAndInvariantBlock(t *testing.T) {
	want := []int64{30, 60, 120, 240, 480, 960, 1920, 3600, 3600, 3600}
	for i, expected := range want {
		if got := RetryDelay(int64(i + 1)); got != expected {
			t.Fatalf("RetryDelay(%d)=%d want %d", i+1, got, expected)
		}
	}
	checkpoint := Checkpoint{WorkerKey: "w", Generation: 1, NextAttemptAt: 0, UpdatedAt: 0}
	blocked, err := Retry(checkpoint, ErrorInvariant, 10)
	if err != nil || blocked.NextAttemptAt != maxUnix || blocked.LastError != ErrorInvariant {
		t.Fatalf("blocked retry = (%+v,%v)", blocked, err)
	}
}

func TestLeaseRegistryIsProcessLocalAndGenerationSafe(t *testing.T) {
	registry := &LeaseRegistry{}
	first, ok := registry.TryAcquire("ledger")
	if !ok || first == nil {
		t.Fatal("first lease not acquired")
	}
	const contenders = 64
	var winners atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < contenders; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if lease, ok := registry.TryAcquire("ledger"); ok {
				winners.Add(1)
				lease.Release()
			}
		}()
	}
	wg.Wait()
	if winners.Load() != 0 {
		t.Fatalf("contenders acquired held lease: %d", winners.Load())
	}
	first.Release()
	first.Release()
	second, ok := registry.TryAcquire("ledger")
	if !ok {
		t.Fatal("lease not reacquired")
	}
	// Releasing the stale first token again cannot clear the new token.
	first.Release()
	if _, ok := registry.TryAcquire("ledger"); ok {
		t.Fatal("stale release cleared newer lease")
	}
	second.Release()
}
