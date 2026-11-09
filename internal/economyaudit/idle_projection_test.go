package economyaudit

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

func TestIdleAuditProjectionUsesReader(t *testing.T) {
	f := newAuditFixture(t)
	f.fund(t, 1, auditNow-1)
	f.project(t)
	held, err := f.database.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Rollback()
	before := f.database.Stats().WaitCount
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	if ready, err := f.service.Advance(ctx); err != nil || !ready {
		t.Fatal("idle projector waited for writer", ready, err)
	}
	if f.database.Stats().WaitCount != before || f.service.readDB.Stats().InUse != 0 {
		t.Fatal("idle projector acquired writer or retained reader")
	}
	if err := held.Rollback(); err != nil {
		t.Fatal(err)
	}
	canceled, stop := context.WithCancel(t.Context())
	stop()
	if _, err := f.service.Advance(canceled); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled probe", err)
	}
	if ready, err := f.service.Advance(t.Context()); err != nil || !ready {
		t.Fatal("reader not reusable", ready, err)
	}
	// The read fast path must preserve checkpoint and timezone invariants.
	if _, err := f.database.Exec("UPDATE site_config SET value='0' WHERE key='site_timezone_offset_minutes'"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.Advance(t.Context()); !errors.Is(err, ErrInvariant) {
		t.Fatal("idle projection skipped frozen timezone check", err)
	}
}

func TestAuditProjectionRechecksAfterWriterWait(t *testing.T) {
	f := newAuditFixture(t)
	f.fund(t, 1, auditNow-2)
	f.project(t)
	f.fund(t, 1, auditNow-1)
	held, err := f.database.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Rollback()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	before := f.database.Stats().WaitCount
	done := make(chan error, 1)
	go func() {
		ready, err := f.service.Advance(ctx)
		if err == nil && !ready {
			err = errors.New("projection remained pending")
		}
		done <- err
	}()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for f.database.Stats().WaitCount == before {
		select {
		case <-ctx.Done():
			t.Fatal("projection did not reach writer", ctx.Err())
		case <-ticker.C:
		}
	}
	if f.service.readDB.Stats().InUse != 0 {
		t.Fatal("probe retained reader while waiting for writer")
	}
	if ready, err := AdvanceTx(ctx, held, auditNow); err != nil || !ready {
		t.Fatal("competing projection", ready, err)
	}
	if err := held.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	got, err := f.service.Summary(f.ctx, f.admin, auditFilter(ledger.General))
	if err != nil || got.Metadata.LedgerSeq != "2" || got.Metadata.ProjectedSeq != "2" || got.Inventory == nil || got.Inventory.Net != "2000" || got.Flows.Issued != "2000" || got.Reconciliation.Status != "matched" {
		t.Fatalf("projection applied twice: %+v, %v", got, err)
	}
}
