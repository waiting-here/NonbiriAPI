package db

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

// seedRequestLog writes one metadata-only log row directly (the repository
// path under test is the cleanup, not RecordRequest).
func seedRequestLog(t *testing.T, store *Store, userID int64, attemptID string, startedAtUnix int64) {
	t.Helper()
	if _, err := store.DB().Exec(`
INSERT INTO request_logs (attempt_id, user_id, model, endpoint_key_id, upstream_model_id,
	status_code, duration_ms, started_at, completed_at, prompt_tokens, completion_tokens,
	total_tokens, usage_unknown, error_code, error_diag)
VALUES (?, ?, '', NULL, '', 200, 0, ?, ?, 0, 0, 0, 0, '', '')`,
		attemptID, userID, startedAtUnix, startedAtUnix); err != nil {
		t.Fatalf("seed request log: %v", err)
	}
}

func TestDeleteRequestLogsBeforeStrictBoundary(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "retention.db"))
	defer store.Close()
	userID := seedUsageUser(t, store, "u-retention")

	cutoff := int64(1700000000)
	seedRequestLog(t, store, userID, "old-1", cutoff-100000) // strictly before: deleted
	seedRequestLog(t, store, userID, "old-2", cutoff-1)      // strictly before: deleted
	seedRequestLog(t, store, userID, "exact", cutoff)        // equal to cutoff: kept
	seedRequestLog(t, store, userID, "future", cutoff+3600)  // after: kept

	deleted, err := store.DeleteRequestLogsBefore(context.Background(), cutoff, 10)
	if err != nil {
		t.Fatalf("DeleteRequestLogsBefore: %v", err)
	}
	if deleted != 2 {
		t.Fatalf("deleted = %d, want 2", deleted)
	}
	// The rows at or after the cutoff must survive; the query projection has
	// no attempt id, so verify through started_at and count instead.
	if countRequestLogs(t, store) != 2 {
		t.Fatalf("log count = %d, want 2 (rows strictly before cutoff only)", countRequestLogs(t, store))
	}
}

func TestDeleteRequestLogsBeforeBatchLimitAndMultiRound(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "retention.db"))
	defer store.Close()
	userID := seedUsageUser(t, store, "u-batch")
	cutoff := int64(1700000000)

	const total = 105
	for i := 0; i < total; i++ {
		seedRequestLog(t, store, userID, fmt.Sprintf("batch-%d", i), cutoff-1)
	}

	rounds := []int64{50, 50, 5, 0}
	for round, want := range rounds {
		deleted, err := store.DeleteRequestLogsBefore(context.Background(), cutoff, 50)
		if err != nil {
			t.Fatalf("round %d: %v", round, err)
		}
		if deleted != want {
			t.Fatalf("round %d deleted = %d, want %d", round, deleted, want)
		}
	}
	if countRequestLogs(t, store) != 0 {
		t.Fatal("rows remain after full sweep")
	}
}

func TestDeleteRequestLogsBeforeIdempotentAndEmptyTable(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "retention.db"))
	defer store.Close()

	// Empty table: a clean no-op.
	deleted, err := store.DeleteRequestLogsBefore(context.Background(), 1700000000, 50)
	if err != nil {
		t.Fatalf("empty table: %v", err)
	}
	if deleted != 0 {
		t.Fatalf("empty table deleted = %d", deleted)
	}

	// Rerunning after a full sweep is a no-op too.
	userID := seedUsageUser(t, store, "u-idem")
	seedRequestLog(t, store, userID, "idem-1", 1699000000)
	if _, err := store.DeleteRequestLogsBefore(context.Background(), 1700000000, 10); err != nil {
		t.Fatalf("first sweep: %v", err)
	}
	deleted, err = store.DeleteRequestLogsBefore(context.Background(), 1700000000, 10)
	if err != nil {
		t.Fatalf("second sweep: %v", err)
	}
	if deleted != 0 {
		t.Fatalf("second sweep deleted = %d, want 0", deleted)
	}
}

func TestDeleteRequestLogsBeforeCancelledContextRollsBack(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "retention.db"))
	defer store.Close()
	userID := seedUsageUser(t, store, "u-cancel")
	cutoff := int64(1700000000)
	for i := 0; i < 10; i++ {
		seedRequestLog(t, store, userID, fmt.Sprintf("cancel-%d", i), cutoff-1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.DeleteRequestLogsBefore(ctx, cutoff, 50); err == nil {
		t.Fatal("cancelled context must fail the delete")
	}
	// Nothing may be committed on failure: the transaction rolled back.
	if n := countRequestLogs(t, store); n != 10 {
		t.Fatalf("log count after failed delete = %d, want 10 (rollback)", n)
	}
}

func TestDeleteRequestLogsBeforeKeepsAccumulatorsAndOtherTables(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "retention.db"))
	defer store.Close()
	userID := seedUsageUser(t, store, "u-keep")
	keyID := seedUsageKey(t, store, userID)
	input := usageInput(userID, keyID) // started_at 1700000000
	if err := store.RecordRequest(context.Background(), input); err != nil {
		t.Fatalf("RecordRequest: %v", err)
	}

	now := time.Now().Unix()
	if _, err := store.DB().Exec(`INSERT INTO user_issues (user_id, kind, message, created_at) VALUES (?, 'test', 'keep', ?)`, userID, now); err != nil {
		t.Fatalf("seed user issue: %v", err)
	}
	if _, err := store.DB().Exec(`INSERT INTO admin_alerts (kind, message, created_at) VALUES ('test', 'keep', ?)`, now); err != nil {
		t.Fatalf("seed admin alert: %v", err)
	}

	deleted, err := store.DeleteRequestLogsBefore(context.Background(), 1700000001, 10)
	if err != nil {
		t.Fatalf("DeleteRequestLogsBefore: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("deleted = %d, want 1", deleted)
	}
	if countRequestLogs(t, store) != 0 {
		t.Fatal("log rows remain")
	}

	// The users accumulator must survive cleanup untouched.
	totals, err := store.GetUserUsage(context.Background(), userID)
	if err != nil {
		t.Fatalf("GetUserUsage: %v", err)
	}
	if totals.TotalRequests != 1 || totals.TotalPromptTokens != 2 {
		t.Fatalf("accumulators changed by cleanup: %+v", totals)
	}
	// Other lifecycle tables are out of the retention policy's scope.
	var issues, alerts int
	if err := store.DB().QueryRow(`SELECT COUNT(*) FROM user_issues`).Scan(&issues); err != nil {
		t.Fatal(err)
	}
	if err := store.DB().QueryRow(`SELECT COUNT(*) FROM admin_alerts`).Scan(&alerts); err != nil {
		t.Fatal(err)
	}
	if issues != 1 || alerts != 1 {
		t.Fatalf("issues/alerts touched: issues=%d alerts=%d", issues, alerts)
	}
}

func TestCountRequestLogsBefore(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "retention.db"))
	defer store.Close()
	userID := seedUsageUser(t, store, "u-count")

	cutoff := int64(1700000000)
	seedRequestLog(t, store, userID, "cnt-1", cutoff-2)
	seedRequestLog(t, store, userID, "cnt-2", cutoff-1)
	seedRequestLog(t, store, userID, "cnt-3", cutoff)   // equal: not counted
	seedRequestLog(t, store, userID, "cnt-4", cutoff+1) // after: not counted

	n, err := store.CountRequestLogsBefore(context.Background(), cutoff)
	if err != nil {
		t.Fatalf("CountRequestLogsBefore: %v", err)
	}
	if n != 2 {
		t.Fatalf("count = %d, want 2", n)
	}
	if _, err := store.CountRequestLogsBefore(context.Background(), 0); err == nil {
		t.Fatal("zero cutoff must be rejected")
	}
}

func TestDeleteRequestLogsBeforeInvalidArguments(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "retention.db"))
	defer store.Close()
	if _, err := store.DeleteRequestLogsBefore(context.Background(), 0, 10); err == nil {
		t.Fatal("zero cutoff must be rejected")
	}
	if _, err := store.DeleteRequestLogsBefore(context.Background(), 1700000000, 0); err == nil {
		t.Fatal("zero limit must be rejected")
	}
	if _, err := store.DeleteRequestLogsBefore(context.Background(), 1700000000, -1); err == nil {
		t.Fatal("negative limit must be rejected")
	}
}

func TestDeleteRequestLogsBeforeLimitClamped(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "retention.db"))
	defer store.Close()
	userID := seedUsageUser(t, store, "u-clamp")
	cutoff := int64(1700000000)
	seedRequestLog(t, store, userID, "clamp-1", cutoff-1)
	seedRequestLog(t, store, userID, "clamp-2", cutoff-2)

	// A limit above MaxCleanupBatch is clamped, never rejected.
	deleted, err := store.DeleteRequestLogsBefore(context.Background(), cutoff, MaxCleanupBatch*10)
	if err != nil {
		t.Fatalf("clamped limit: %v", err)
	}
	if deleted != 2 {
		t.Fatalf("deleted = %d, want 2", deleted)
	}
}

func TestCleanupTerminalCharityReservationsRetentionAndBound(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "retention.db"))
	defer store.Close()
	userID := seedUsageUser(t, store, "u-charity-retention")
	cutoff := time.Now().Unix() - 400*24*60*60

	insertReservation := func(attempt, state string, createdAt int64, finalizedAt *int64) {
		t.Helper()
		if _, err := store.DB().Exec(`INSERT INTO charity_reservations
			(user_id, attempt_id, state, pricing_mode, discount_percent,
			 created_at, finalized_at, updated_at)
			VALUES (?, ?, ?, 'per_request', 100, ?, ?, ?)`,
			userID, attempt, state, createdAt, finalizedAt, createdAt); err != nil {
			t.Fatalf("insert charity reservation %s: %v", attempt, err)
		}
	}

	oldFinalized := cutoff - 1
	recentFinalized := cutoff
	insertReservation("old-committed", "committed", cutoff-10, &oldFinalized)
	insertReservation("old-released", "released", cutoff-9, &oldFinalized)
	insertReservation("inflight-reserved", "reserved", cutoff-8, nil)
	insertReservation("inflight-dispatched", "dispatched", cutoff-7, nil)
	insertReservation("recent-committed", "committed", cutoff, &recentFinalized)
	insertReservation("recent-released", "released", cutoff, &recentFinalized)

	// The cleanup has a hard bound of 50 batches × 200 rows. More than that
	// amount of eligible work must report remaining=true for the next sweep.
	for i := 0; i < 10001; i++ {
		insertReservation(fmt.Sprintf("bulk-old-%d", i), "committed", cutoff-100, &oldFinalized)
	}

	removed, remaining, err := store.CleanupTerminalCharityReservations(context.Background(), cutoff)
	if err != nil {
		t.Fatalf("first charity cleanup: %v", err)
	}
	if removed != 10000 || !remaining {
		t.Fatalf("first cleanup removed=%d remaining=%v, want 10000,true", removed, remaining)
	}

	removed, remaining, err = store.CleanupTerminalCharityReservations(context.Background(), cutoff)
	if err != nil {
		t.Fatalf("second charity cleanup: %v", err)
	}
	if removed != 3 || remaining {
		t.Fatalf("second cleanup removed=%d remaining=%v, want 3,false", removed, remaining)
	}

	var total, inflight, recent int
	if err := store.DB().QueryRow(`SELECT COUNT(*) FROM charity_reservations`).Scan(&total); err != nil {
		t.Fatal(err)
	}
	if err := store.DB().QueryRow(`SELECT COUNT(*) FROM charity_reservations WHERE state IN ('reserved','dispatched')`).Scan(&inflight); err != nil {
		t.Fatal(err)
	}
	if err := store.DB().QueryRow(`SELECT COUNT(*) FROM charity_reservations WHERE state IN ('committed','released') AND finalized_at >= ?`, cutoff).Scan(&recent); err != nil {
		t.Fatal(err)
	}
	if total != 4 || inflight != 2 || recent != 2 {
		t.Fatalf("remaining reservations total=%d inflight=%d recent=%d, want 4,2,2", total, inflight, recent)
	}
}
