package usage_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"nonbiriapi/internal/db"
	"nonbiriapi/internal/secret"
	"nonbiriapi/internal/usage"
)

// quietLogger keeps the retention sweeps' summary lines out of the test log.
func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newRetentionStore(t *testing.T) *db.Store {
	t.Helper()
	master := bytes.Repeat([]byte{0x6b}, secret.MasterKeyBytes)
	vault, err := secret.New(master)
	clear(master)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = vault.Close() })
	store, err := db.Open(filepath.Join(t.TempDir(), "retention.db"), vault)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func seedRetentionUser(t *testing.T, store *db.Store, discordID string) int64 {
	t.Helper()
	now := time.Now().Unix()
	res, err := store.DB().Exec(`INSERT INTO users (discord_id, username, created_at, updated_at) VALUES (?, ?, ?, ?)`, discordID, discordID, now, now)
	if err != nil {
		t.Fatalf("seed user %s: %v", discordID, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("seed user %s last id: %v", discordID, err)
	}
	return id
}

func seedRetentionLog(t *testing.T, store *db.Store, userID int64, attemptID string, startedAtUnix int64) {
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

func countRetentionLogs(t *testing.T, store *db.Store) int {
	t.Helper()
	var n int
	if err := store.DB().QueryRow(`SELECT COUNT(*) FROM request_logs`).Scan(&n); err != nil {
		t.Fatalf("count request logs: %v", err)
	}
	return n
}

func TestCleanupDefaultCutoffThirtyDays(t *testing.T) {
	store := newRetentionStore(t)
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	service, err := usage.NewService(usage.Config{Logger: quietLogger(), Store: store, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	userID := seedRetentionUser(t, store, "u-boundary")

	// Cutoff = now - 30d; rows strictly before it are deleted.
	seedRetentionLog(t, store, userID, "older-than-30d", now.Add(-31*24*time.Hour).Unix())
	seedRetentionLog(t, store, userID, "just-before-cutoff", now.Add(-30*24*time.Hour).Unix()-1)
	seedRetentionLog(t, store, userID, "exactly-30d", now.Add(-30*24*time.Hour).Unix()) // kept: not strictly before
	seedRetentionLog(t, store, userID, "29d", now.Add(-29*24*time.Hour).Unix())
	seedRetentionLog(t, store, userID, "now", now.Unix())

	summary, err := service.CleanupRequestLogs(context.Background())
	if err != nil {
		t.Fatalf("CleanupRequestLogs: %v", err)
	}
	if summary.Deleted != 2 {
		t.Fatalf("deleted = %d, want 2", summary.Deleted)
	}
	if summary.Batches != 1 {
		t.Fatalf("batches = %d, want 1", summary.Batches)
	}
	wantCutoff := now.Add(-usage.DefaultRetention)
	if !summary.Cutoff.Equal(wantCutoff) {
		t.Fatalf("cutoff = %v, want %v", summary.Cutoff, wantCutoff)
	}
	if countRetentionLogs(t, store) != 3 {
		t.Fatalf("log count = %d, want 3", countRetentionLogs(t, store))
	}
}

func TestCleanupEmptyAndFutureRows(t *testing.T) {
	store := newRetentionStore(t)
	now := time.Now().UTC()
	service, err := usage.NewService(usage.Config{Logger: quietLogger(), Store: store, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}

	// Empty table: clean no-op (one empty batch is expected: the loop only
	// stops by observing a batch below the size bound, never by counting).
	summary, err := service.CleanupRequestLogs(context.Background())
	if err != nil {
		t.Fatalf("empty table: %v", err)
	}
	if summary.Deleted != 0 || summary.StoppedEarly {
		t.Fatalf("empty table summary = %+v", summary)
	}

	// Only future rows: nothing is eligible.
	userID := seedRetentionUser(t, store, "u-future")
	seedRetentionLog(t, store, userID, "future", now.Add(7*24*time.Hour).Unix())
	summary, err = service.CleanupRequestLogs(context.Background())
	if err != nil {
		t.Fatalf("future rows: %v", err)
	}
	if summary.Deleted != 0 || countRetentionLogs(t, store) != 1 {
		t.Fatalf("future rows summary = %+v, count = %d", summary, countRetentionLogs(t, store))
	}
}

func TestCleanupMultiBatchLoop(t *testing.T) {
	store := newRetentionStore(t)
	now := time.Now().UTC()
	service, err := usage.NewService(usage.Config{Logger: quietLogger(),
		Store:             store,
		Now:               func() time.Time { return now },
		CleanupBatchSize:  100,
		CleanupMaxBatches: 1000,
	})
	if err != nil {
		t.Fatal(err)
	}
	userID := seedRetentionUser(t, store, "u-multibatch")

	tx, err := store.DB().Begin()
	if err != nil {
		t.Fatal(err)
	}
	const total = 750
	for i := 0; i < total; i++ {
		if _, err := tx.Exec(`
INSERT INTO request_logs (attempt_id, user_id, started_at, completed_at)
VALUES (?, ?, ?, ?)`, fmt.Sprintf("mb-%d", i), userID, now.Add(-40*24*time.Hour).Unix(), now.Add(-40*24*time.Hour).Unix()); err != nil {
			t.Fatalf("seed batch row: %v", err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	summary, err := service.CleanupRequestLogs(context.Background())
	if err != nil {
		t.Fatalf("CleanupRequestLogs: %v", err)
	}
	if summary.Deleted != total {
		t.Fatalf("deleted = %d, want %d", summary.Deleted, total)
	}
	if summary.Batches != 8 { // 7 full batches of 100 + 1 partial of 50
		t.Fatalf("batches = %d, want 8", summary.Batches)
	}
	if summary.StoppedEarly {
		t.Fatal("full sweep must not stop early")
	}
	if countRetentionLogs(t, store) != 0 {
		t.Fatal("rows remain after full sweep")
	}
}

func TestCleanupIdempotentRerun(t *testing.T) {
	store := newRetentionStore(t)
	now := time.Now().UTC()
	service, err := usage.NewService(usage.Config{Logger: quietLogger(), Store: store, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	userID := seedRetentionUser(t, store, "u-idem")
	seedRetentionLog(t, store, userID, "idem-1", now.Add(-40*24*time.Hour).Unix())
	seedRetentionLog(t, store, userID, "idem-2", now.Add(-40*24*time.Hour).Unix())

	first, err := service.CleanupRequestLogs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if first.Deleted != 2 {
		t.Fatalf("first run deleted = %d", first.Deleted)
	}
	second, err := service.CleanupRequestLogs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if second.Deleted != 0 {
		t.Fatalf("second run = %+v, want empty sweep", second)
	}
	if countRetentionLogs(t, store) != 0 {
		t.Fatal("rows remain")
	}
}

func TestCleanupPreservesUserAccumulators(t *testing.T) {
	store := newRetentionStore(t)
	now := time.Now().UTC()
	service, err := usage.NewService(usage.Config{Logger: quietLogger(), Store: store, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	userID := seedRetentionUser(t, store, "u-acc")
	old := now.Add(-60 * 24 * time.Hour)
	for i := 0; i < 2; i++ {
		input := db.RequestLogInput{
			AttemptID:    fmt.Sprintf("acc-%d", i),
			UserID:       userID,
			Model:        "opaque/provider/model",
			StatusCode:   200,
			StartedAt:    old,
			CompletedAt:  old,
			PromptTokens: 5,
		}
		if err := store.RecordRequest(context.Background(), input); err != nil {
			t.Fatalf("RecordRequest: %v", err)
		}
	}

	summary, err := service.CleanupRequestLogs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if summary.Deleted != 2 || countRetentionLogs(t, store) != 0 {
		t.Fatalf("summary = %+v, count = %d", summary, countRetentionLogs(t, store))
	}
	// Log cleanup must never recompute or decrement the accumulators.
	totals, err := store.GetUserUsage(context.Background(), userID)
	if err != nil {
		t.Fatal(err)
	}
	if totals.TotalRequests != 2 || totals.TotalPromptTokens != 10 {
		t.Fatalf("accumulators changed by cleanup: %+v", totals)
	}
}

func TestCleanupMaxBatchesStopsEarlyAndResumes(t *testing.T) {
	store := newRetentionStore(t)
	now := time.Now().UTC()
	service, err := usage.NewService(usage.Config{Logger: quietLogger(),
		Store:             store,
		Now:               func() time.Time { return now },
		CleanupBatchSize:  10,
		CleanupMaxBatches: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	userID := seedRetentionUser(t, store, "u-maxb")
	for i := 0; i < 100; i++ {
		seedRetentionLog(t, store, userID, fmt.Sprintf("mx-%d", i), now.Add(-40*24*time.Hour).Unix())
	}

	first, err := service.CleanupRequestLogs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if first.Deleted != 30 || first.Batches != 3 || !first.StoppedEarly {
		t.Fatalf("first run = %+v, want 30 deleted / 3 batches / stopped early", first)
	}
	if countRetentionLogs(t, store) != 70 {
		t.Fatalf("count after early stop = %d, want 70", countRetentionLogs(t, store))
	}

	// Later rounds resume and finish the sweep (each round is bounded to
	// maxBatches=3 batches of 10).
	second, err := service.CleanupRequestLogs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if second.Deleted != 30 || second.Batches != 3 || !second.StoppedEarly {
		t.Fatalf("second run = %+v, want 30 deleted / 3 batches / stopped early", second)
	}
	third, err := service.CleanupRequestLogs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if third.Deleted != 30 || third.Batches != 3 || !third.StoppedEarly {
		t.Fatalf("third run = %+v, want 30 deleted / 3 batches / stopped early", third)
	}
	fourth, err := service.CleanupRequestLogs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if fourth.Deleted != 10 || fourth.StoppedEarly {
		t.Fatalf("fourth run = %+v, want 10 deleted and finished", fourth)
	}
	if countRetentionLogs(t, store) != 0 {
		t.Fatal("rows remain")
	}
}

func TestCleanupMaxDurationStopsEarly(t *testing.T) {
	store := newRetentionStore(t)
	now := time.Now().UTC()
	service, err := usage.NewService(usage.Config{Logger: quietLogger(),
		Store:              store,
		Now:                func() time.Time { return now },
		CleanupBatchSize:   10,
		CleanupMaxDuration: time.Nanosecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	userID := seedRetentionUser(t, store, "u-maxt")
	for i := 0; i < 100; i++ {
		seedRetentionLog(t, store, userID, fmt.Sprintf("mt-%d", i), now.Add(-40*24*time.Hour).Unix())
	}

	summary, err := service.CleanupRequestLogs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// The wall-clock bound must stop the run before the full sweep; at most
	// one batch may sneak in when the first check races the clock.
	if summary.Deleted >= 100 || countRetentionLogs(t, store) == 0 {
		t.Fatalf("summary = %+v, want stopped early with less than the full sweep", summary)
	}
}

func TestCleanupCancelledContext(t *testing.T) {
	store := newRetentionStore(t)
	now := time.Now().UTC()
	service, err := usage.NewService(usage.Config{Logger: quietLogger(), Store: store, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	userID := seedRetentionUser(t, store, "u-cancel")
	seedRetentionLog(t, store, userID, "cx-1", now.Add(-40*24*time.Hour).Unix())

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	summary, err := service.CleanupRequestLogs(ctx)
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if summary.Deleted != 0 || !summary.StoppedEarly {
		t.Fatalf("summary = %+v", summary)
	}
	if countRetentionLogs(t, store) != 1 {
		t.Fatal("cancelled run must not delete rows")
	}
}

func TestCleanupCountSeam(t *testing.T) {
	store := newRetentionStore(t)
	now := time.Now().UTC()
	service, err := usage.NewService(usage.Config{Logger: quietLogger(), Store: store, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	userID := seedRetentionUser(t, store, "u-count")
	seedRetentionLog(t, store, userID, "cs-1", now.Add(-40*24*time.Hour).Unix())
	seedRetentionLog(t, store, userID, "cs-2", now.Add(-40*24*time.Hour).Unix())
	seedRetentionLog(t, store, userID, "cs-3", now.Add(-1*24*time.Hour).Unix())

	n, err := service.CountRequestLogsBefore(context.Background(), now.Add(-30*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("count = %d, want 2", n)
	}
}

func TestCleanupConcurrentWithRecordRequest(t *testing.T) {
	store := newRetentionStore(t)
	now := time.Now().UTC()
	service, err := usage.NewService(usage.Config{Logger: quietLogger(), Store: store, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	userID := seedRetentionUser(t, store, "u-conc")
	old := now.Add(-40 * 24 * time.Hour)

	const writers = 8
	const perWriter = 25
	var failures atomic.Int64
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				input := db.RequestLogInput{
					AttemptID:    fmt.Sprintf("conc-%d-%d", w, i),
					UserID:       userID,
					Model:        "opaque/provider/model",
					StatusCode:   200,
					StartedAt:    old,
					CompletedAt:  old,
					PromptTokens: 1,
				}
				if err := store.RecordRequest(context.Background(), input); err != nil {
					failures.Add(1)
				}
			}
		}(w)
	}
	// Two overlapping cleanup rounds, as a scheduler firing twice would do.
	// They run a fixed number of sweeps while the writers are still going;
	// the deterministic final sweep happens after the writers finish.
	var cleanupErrs atomic.Int64
	var cwg sync.WaitGroup
	for round := 0; round < 2; round++ {
		cwg.Add(1)
		go func() {
			defer cwg.Done()
			for i := 0; i < 30; i++ {
				if _, err := service.CleanupRequestLogs(context.Background()); err != nil {
					cleanupErrs.Add(1)
				}
			}
		}()
	}
	wg.Wait()
	cwg.Wait()

	if failures.Load() != 0 {
		t.Fatalf("RecordRequest failures = %d", failures.Load())
	}
	if cleanupErrs.Load() != 0 {
		t.Fatalf("cleanup failures = %d", cleanupErrs.Load())
	}
	// Deterministic final sweep: every log row (all older than 30 days) must
	// be gone, while the accumulators keep every accounted request.
	for i := 0; i < 10 && countRetentionLogs(t, store) > 0; i++ {
		if _, err := service.CleanupRequestLogs(context.Background()); err != nil {
			t.Fatalf("final sweep: %v", err)
		}
	}
	totals, err := store.GetUserUsage(context.Background(), userID)
	if err != nil {
		t.Fatal(err)
	}
	if totals.TotalRequests != writers*perWriter {
		t.Fatalf("total_requests = %d, want %d", totals.TotalRequests, writers*perWriter)
	}
	if countRetentionLogs(t, store) != 0 {
		t.Fatalf("log count = %d, want 0", countRetentionLogs(t, store))
	}
}

func TestCleanupAfterStoreCloseFailsStable(t *testing.T) {
	master := bytes.Repeat([]byte{0x6b}, secret.MasterKeyBytes)
	vault, err := secret.New(master)
	clear(master)
	if err != nil {
		t.Fatal(err)
	}
	defer vault.Close()
	store, err := db.Open(filepath.Join(t.TempDir(), "closed.db"), vault)
	if err != nil {
		t.Fatal(err)
	}
	service, err := usage.NewService(usage.Config{Logger: quietLogger(), Store: store})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CleanupRequestLogs(context.Background()); err == nil {
		t.Fatal("cleanup against a closed store must fail with a stable error")
	}
}
