package idempotency_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
)

func insertMaintenanceReceipt(t *testing.T, database *sql.DB, expires int64, seed byte) {
	t.Helper()
	if _, err := database.Exec(`INSERT OR IGNORE INTO users(id,donation_credit_mag,total_requests,total_uncached_input_tokens,total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,total_unknown_usage_requests,revision,created_at,updated_at) VALUES(42,zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),zeroblob(16),1,1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO resource_operation_status(actor_scope_hash,key_hash,user_id,stage,status,result_json,created_at,expires_at) VALUES(?,?,42,'endpoint','recorded','{"endpoint_id":"17"}',?,?)`, maintenanceHash(seed), maintenanceHash(seed+32), expires-idempotency.ReplayWindowSeconds, expires); err != nil {
		t.Fatal(err)
	}
}

func maintenanceReceiptCount(t *testing.T, database *sql.DB) int {
	t.Helper()
	var count int
	if err := database.QueryRow(`SELECT count(*) FROM resource_operation_status`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestMaintenanceResourceReceiptsShareBoundedBatchAndExactExpiry(t *testing.T) {
	for _, recover := range []bool{false, true} {
		t.Run(map[bool]string{false: "retention", true: "recovery"}[recover], func(t *testing.T) {
			store := openStore(t)
			maintenance := idempotency.NewMaintenance(store.DB())
			run := maintenance.Retain
			if recover {
				run = maintenance.Recover
			}
			insertMaintenanceRecord(t, store.DB(), idempotency.ScopeControlMutation, "completed", maintenanceDecisionNow, 1)
			insertMaintenanceRecord(t, store.DB(), idempotency.ScopeModelDiscovery, "completed", maintenanceDecisionNow-1, 2)
			insertMaintenanceReceipt(t, store.DB(), maintenanceDecisionNow, 1)
			insertMaintenanceReceipt(t, store.DB(), maintenanceDecisionNow-1, 2)
			insertMaintenanceReceipt(t, store.DB(), maintenanceDecisionNow+1, 3)
			result, err := run(context.Background(), maintenanceDecisionNow, 3, maintenanceDeadline())
			if err != nil || result != (idempotency.MaintenanceResult{Processed: 3, More: true}) {
				t.Fatalf("mixed first batch = (%+v,%v)", result, err)
			}
			if maintenanceReceiptCount(t, store.DB()) != 2 {
				t.Fatal("projection deletion exceeded remaining batch capacity")
			}
			result, err = run(context.Background(), maintenanceDecisionNow, 3, maintenanceDeadline())
			if err != nil || result != (idempotency.MaintenanceResult{Processed: 1}) {
				t.Fatalf("last projection batch = (%+v,%v)", result, err)
			}
			if maintenanceReceiptCount(t, store.DB()) != 1 {
				t.Fatal("post-boundary receipt was removed")
			}
			result, err = run(context.Background(), maintenanceDecisionNow+1, 3, maintenanceDeadline())
			if err != nil || result != (idempotency.MaintenanceResult{Processed: 1}) || maintenanceReceiptCount(t, store.DB()) != 0 {
				t.Fatalf("next boundary batch = (%+v,%v)", result, err)
			}
		})
	}
}

func TestMaintenanceRecoveryPreservesUnexpiredReceiptAndRollbackIsAtomic(t *testing.T) {
	store := openStore(t)
	maintenance := idempotency.NewMaintenance(store.DB())
	insertMaintenanceRecord(t, store.DB(), idempotency.ScopeControlMutation, "accepted", maintenanceDecisionNow+1, 1)
	insertMaintenanceReceipt(t, store.DB(), maintenanceDecisionNow+1, 1)
	result, err := maintenance.Recover(context.Background(), maintenanceDecisionNow, 100, maintenanceDeadline())
	if err != nil || result != (idempotency.MaintenanceResult{Processed: 1}) || maintenanceReceiptCount(t, store.DB()) != 1 {
		t.Fatalf("live receipt recovery = (%+v,%v)", result, err)
	}
	insertMaintenanceRecord(t, store.DB(), idempotency.ScopeControlMutation, "completed", maintenanceDecisionNow, 2)
	insertMaintenanceReceipt(t, store.DB(), maintenanceDecisionNow, 2)
	if _, err := store.DB().Exec(`CREATE TRIGGER reject_receipt_cleanup BEFORE DELETE ON resource_operation_status BEGIN SELECT RAISE(ABORT,'receipt cleanup rejected'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := maintenance.Retain(context.Background(), maintenanceDecisionNow, 100, maintenanceDeadline()); err == nil {
		t.Fatal("cleanup failure succeeded")
	}
	if !maintenanceRecordExists(t, store.DB(), idempotency.ScopeControlMutation, 2) || maintenanceReceiptCount(t, store.DB()) != 2 {
		t.Fatal("failed mixed cleanup partially committed")
	}
}
