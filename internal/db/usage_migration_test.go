package db

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/dbtest"
	_ "modernc.org/sqlite"
)

// legacyUsageSchema recreates the pre-four-bucket (alpha.1) shape of the two
// usage-bearing tables so the in-place migration path is exercised for real.
func legacyUsageSchema(t *testing.T, path string) {
	t.Helper()
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open legacy database: %v", err)
	}
	defer raw.Close()
	if _, err := raw.Exec(`PRAGMA journal_mode=WAL; PRAGMA foreign_keys=ON;`); err != nil {
		t.Fatalf("legacy pragmas: %v", err)
	}
	schema := `
CREATE TABLE IF NOT EXISTS users (
	id                           INTEGER PRIMARY KEY AUTOINCREMENT,
	discord_id                   TEXT UNIQUE,
	username                     TEXT NOT NULL DEFAULT '',
	is_admin                     INTEGER NOT NULL DEFAULT 0,
	total_requests               INTEGER NOT NULL DEFAULT 0,
	total_prompt_tokens          INTEGER NOT NULL DEFAULT 0,
	total_completion_tokens      INTEGER NOT NULL DEFAULT 0,
	total_unknown_usage_requests INTEGER NOT NULL DEFAULT 0,
	created_at                   INTEGER NOT NULL,
	updated_at                   INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS request_logs (
	id                INTEGER PRIMARY KEY AUTOINCREMENT,
	attempt_id        TEXT,
	user_id           INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	model             TEXT NOT NULL DEFAULT '',
	endpoint_key_id   INTEGER,
	upstream_model_id TEXT NOT NULL DEFAULT '',
	status_code       INTEGER NOT NULL DEFAULT 0,
	duration_ms       INTEGER NOT NULL DEFAULT 0,
	started_at        INTEGER NOT NULL,
	completed_at      INTEGER NOT NULL DEFAULT 0,
	prompt_tokens     INTEGER NOT NULL DEFAULT 0,
	completion_tokens INTEGER NOT NULL DEFAULT 0,
	total_tokens      INTEGER NOT NULL DEFAULT 0,
	usage_unknown     INTEGER NOT NULL DEFAULT 0,
	error_code        TEXT NOT NULL DEFAULT '',
	error_diag        TEXT NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_request_logs_attempt ON request_logs(attempt_id) WHERE attempt_id IS NOT NULL;
`
	if _, err := raw.Exec(schema); err != nil {
		t.Fatalf("create legacy schema: %v", err)
	}
	seed := `
INSERT INTO users (id, discord_id, username, total_requests, total_prompt_tokens, total_completion_tokens, total_unknown_usage_requests, created_at, updated_at)
VALUES (1, 'legacy-user', 'legacy', 3, 120, 45, 1, 1700000000, 1700000000);
INSERT INTO users (id, discord_id, username, total_requests, created_at, updated_at)
VALUES (2, 'zero-user', 'zero', 0, 1700000000, 1700000000);
INSERT INTO request_logs (attempt_id, user_id, model, status_code, started_at, completed_at, prompt_tokens, completion_tokens, total_tokens, usage_unknown)
VALUES ('legacy-attempt-1', 1, 'p/m', 200, 1700000000, 1700000005, 100, 40, 140, 0),
       ('legacy-attempt-2', 1, 'p/m', 200, 1700000100, 1700000101, 20, 5, 25, 0),
       (NULL, 1, 'p/m', 502, 1700000200, 1700000200, 0, 0, 0, 1);`
	if _, err := raw.Exec(seed); err != nil {
		t.Fatalf("seed legacy rows: %v", err)
	}
}

func TestUsageBucketMigrationBackfillsLegacyRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	dbtest.EnsureOwnerOnlyParent(t, path)
	legacyUsageSchema(t, path)

	store := openTestStore(t, path)
	defer store.Close()

	// users: legacy prompt/completion totals land in uncached/output with
	// cache buckets zeroed; a zero-history user stays all-zero.
	var uncached, write, read, output, prompt, completion int64
	if err := store.DB().QueryRow(`SELECT total_uncached_input_tokens, total_cache_write_input_tokens, total_cache_read_input_tokens, total_output_tokens, total_prompt_tokens, total_completion_tokens FROM users WHERE id = 1`).
		Scan(&uncached, &write, &read, &output, &prompt, &completion); err != nil {
		t.Fatalf("read migrated user: %v", err)
	}
	if uncached != 120 || write != 0 || read != 0 || output != 45 || prompt != 120 || completion != 45 {
		t.Fatalf("user buckets = %d/%d/%d/%d mirrors=%d/%d", uncached, write, read, output, prompt, completion)
	}
	if err := store.DB().QueryRow(`SELECT total_uncached_input_tokens, total_cache_write_input_tokens, total_cache_read_input_tokens, total_output_tokens FROM users WHERE id = 2`).
		Scan(&uncached, &write, &read, &output); err != nil {
		t.Fatalf("read zero user: %v", err)
	}
	if uncached != 0 || write != 0 || read != 0 || output != 0 {
		t.Fatalf("zero user buckets = %d/%d/%d/%d", uncached, write, read, output)
	}

	// request_logs: same conservative projection; the unknown row stays zeroed.
	rows := []struct{ u, w, r, o, p, c, tt int64 }{}
	for _, id := range []int64{1, 2, 3} {
		var row struct {
			u, w, r, o, p, c, tt int64
		}
		if err := store.DB().QueryRow(`SELECT uncached_input_tokens, cache_write_input_tokens, cache_read_input_tokens, output_tokens, prompt_tokens, completion_tokens, total_tokens FROM request_logs WHERE id = ?`, id).
			Scan(&row.u, &row.w, &row.r, &row.o, &row.p, &row.c, &row.tt); err != nil {
			t.Fatalf("read migrated log %d: %v", id, err)
		}
		rows = append(rows, row)
	}
	if rows[0] != (struct{ u, w, r, o, p, c, tt int64 }{100, 0, 0, 40, 100, 40, 140}) ||
		rows[1] != (struct{ u, w, r, o, p, c, tt int64 }{20, 0, 0, 5, 20, 5, 25}) ||
		rows[2] != (struct{ u, w, r, o, p, c, tt int64 }{0, 0, 0, 0, 0, 0, 0}) {
		t.Fatalf("migrated logs = %+v", rows)
	}

	// New accounting after migration keeps buckets and mirrors consistent.
	userID := int64(1)
	input := RequestLogInput{
		AttemptID:             "post-migration-attempt",
		UserID:                userID,
		Model:                 "p/m",
		StatusCode:            200,
		StartedAt:             time.Unix(1700000300, 0).UTC(),
		CompletedAt:           time.Unix(1700000301, 0).UTC(),
		UncachedInputTokens:   4,
		CacheWriteInputTokens: 6,
		CacheReadInputTokens:  8,
		OutputTokens:          10,
	}
	if err := store.RecordRequest(context.Background(), input); err != nil {
		t.Fatalf("post-migration RecordRequest: %v", err)
	}
	totals, err := store.GetUserUsage(context.Background(), userID)
	if err != nil {
		t.Fatal(err)
	}
	if totals.TotalUncachedInputTokens != 124 || totals.TotalCacheWriteInputTokens != 6 || totals.TotalCacheReadInputTokens != 8 || totals.TotalOutputTokens != 55 ||
		totals.TotalPromptTokens != 138 || totals.TotalCompletionTokens != 55 {
		t.Fatalf("post-migration totals = %+v", totals)
	}

	// Reopening must be idempotent: no double backfill, defaults intact.
	reopened := openTestStore(t, path)
	defer reopened.Close()
	if err := reopened.DB().QueryRow(`SELECT total_uncached_input_tokens, total_output_tokens FROM users WHERE id = 1`).Scan(&uncached, &output); err != nil {
		t.Fatal(err)
	}
	if uncached != 124 || output != 55 {
		t.Fatalf("reopen changed backfill: uncached=%d output=%d", uncached, output)
	}
}
