package imageactivity

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestIdleWorkerDoesNotWaitForWriter(t *testing.T) {
	f := newFixture(t)
	tx, err := f.database.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	before := f.database.Stats().WaitCount
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	if err := f.service.Step(ctx); err != nil {
		t.Fatalf("idle worker waited for writer: %v", err)
	}
	if f.database.Stats().WaitCount != before {
		t.Fatal("idle worker attempted to acquire the writer")
	}
	if n := f.service.config.ReadDatabase.Stats().InUse; n != 0 {
		t.Fatalf("idle probe retained %d readers", n)
	}
}

func TestQueueExpiryProbePreservesDeadlineAndRetry(t *testing.T) {
	f := newFixture(t)
	f.configure(t)
	task := f.submit(t, f.user, 1)
	row, err := f.service.readTask(t.Context(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := f.database.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	if err := f.service.expireQueued(ctx, row.queueDeadline-1); err != nil {
		t.Fatalf("future deadline acquired writer: %v", err)
	}
	cancelled, stop := context.WithCancel(t.Context())
	stop()
	if err := f.service.expireQueued(cancelled, row.queueDeadline); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled expiry: %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := f.service.expireQueued(t.Context(), row.queueDeadline); err != nil {
			t.Fatal(err)
		}
	}
	result, err := f.service.GetTask(f.ctx(f.user), f.user, task.ID)
	if err != nil || result.Status != "cancelled" || result.ErrorCode == nil || *result.ErrorCode != "queue_timeout" || result.Refund != (Price{"2", "1"}) {
		t.Fatalf("deadline refund: %+v %v", result, err)
	}
	f.checkLedger(t)
}
