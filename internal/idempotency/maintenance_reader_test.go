package idempotency_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
)

func TestEmptyMaintenanceUsesReaderAndResumesForDueWork(t *testing.T) {
	for _, recovery := range []bool{false, true} {
		name := "retention"
		if recovery {
			name = "recovery"
		}
		t.Run(name, func(t *testing.T) {
			store := openStore(t)
			owner := idempotency.NewMaintenance(store.DB(), store.ReadDB())
			run := owner.Retain
			if recovery {
				run = owner.Recover
			}
			insertMaintenanceRecord(t, store.DB(), idempotency.ScopeControlMutation, "completed", maintenanceDecisionNow+1, 1)
			tx, err := store.DB().BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			result, err := run(ctx, maintenanceDecisionNow, 100, time.Now().Add(time.Second))
			if err != nil || result != (idempotency.MaintenanceResult{}) {
				t.Fatalf("idle sweep waited for writer: %+v, %v", result, err)
			}
			if used := store.ReadDB().Stats().InUse; used != 0 {
				t.Fatalf("reader connection retained: %d", used)
			}
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			result, err = run(t.Context(), maintenanceDecisionNow+1, 100, maintenanceDeadline())
			if err != nil || result != (idempotency.MaintenanceResult{Processed: 1}) {
				t.Fatalf("due work did not resume: %+v, %v", result, err)
			}
		})
	}
}

func TestMaintenanceReaderCancellationDoesNotConsumeWork(t *testing.T) {
	store := openStore(t)
	owner := idempotency.NewMaintenance(store.DB(), store.ReadDB())
	insertMaintenanceRecord(t, store.DB(), idempotency.ScopeControlMutation, "accepted", maintenanceDecisionNow+1, 1)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := owner.Recover(ctx, maintenanceDecisionNow, 100, maintenanceDeadline()); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled sweep: %v", err)
	}
	// Recovery includes unfinished rows regardless of their future expiry;
	// retention alone must leave these unexpired rows intact.
	if result, err := owner.Retain(t.Context(), maintenanceDecisionNow, 100, maintenanceDeadline()); err != nil || result.Processed != 0 {
		t.Fatalf("unexpired retention: %+v, %v", result, err)
	}
	if result, err := owner.Recover(t.Context(), maintenanceDecisionNow, 100, maintenanceDeadline()); err != nil || result.Processed != 1 {
		t.Fatalf("reader not reusable after cancellation: %+v, %v", result, err)
	}
}
