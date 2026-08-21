package db

// Usage accounting repository: one atomic transaction writes a metadata-only
// request log row together with the user-level accumulator updates, and the
// narrow read projections back the user and administrator usage screens.
//
// Accounting semantics (the caller is the forwarding stage, which fires the
// usage hook only for committed attempts — at least one response body byte
// written to the client):
//
//   - the log row is inserted with INSERT ... SELECT FROM users, so a
//     concurrent account deletion makes the whole write an atomic no-op:
//     a late callback never fails and never resurrects an account (it also
//     never partially commits);
//   - a duplicate AttemptID is a no-op through the unique partial index, so a
//     retried or duplicated hook delivery cannot double-insert or
//     double-accumulate (explicit at-most-once semantics);
//   - the accumulator UPDATE is bounded by the current totals; an overflow
//     matches zero rows and fails the transaction closed, so no partial
//     accounting state is ever committed;
//   - every persisted value is validated non-negative and bounded, and every
//     diagnostic goes through diagnostic.Bound as the final sink policy
//     before it is stored.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/diagnostic"
)

// Usage accounting bounds. All persisted numbers are non-negative and
// bounded; the repository fails closed (rolls back) rather than persist an
// unbounded or overflowed value.
const (
	// MaxAttemptIDLen bounds the opaque attempt correlation id in bytes.
	MaxAttemptIDLen = 128
	// maxLogModelRunes bounds the platform model full name as stored.
	maxLogModelRunes = 512
	// maxLogUpstreamModelRunes bounds the upstream model identifier as stored.
	maxLogUpstreamModelRunes = 512
	// MaxTokenDelta bounds one request's token fields (a hostile or broken
	// upstream cannot overflow an accumulator through a single request).
	MaxTokenDelta = int64(1) << 40
	// MaxDurationMs bounds one request's measured duration in milliseconds.
	MaxDurationMs = int64(1) << 30
	// maxLogStatus bounds the stored client-facing status code.
	maxLogStatus = 599
	// MaxLogPageLimit bounds one request-log page (frozen contract: default
	// page size 20, clamp [1,100], shared by the user and admin log screens).
	MaxLogPageLimit = 100
	// MaxCleanupBatch bounds one retention-cleanup delete batch.
	MaxCleanupBatch = 1000
	// MaxUsageByUserLimit bounds the per-user usage aggregation page.
	MaxUsageByUserLimit = 1000
	// maxLogErrorCodeLen bounds the stable error code.
	maxLogErrorCodeLen = 64
	// accumulatorCeiling caps each per-user accumulator so that
	// current+delta can never exceed math.MaxInt64 (SQLite would otherwise
	// silently convert an overflowing sum to a floating-point value).
	accumulatorCeiling = math.MaxInt64
)

// stableErrorCodes is the closed set of error codes persisted in
// request_logs. The forwarding stage emits only these; an unknown code is a
// programming error and fails closed instead of persisting it.
var stableErrorCodes = map[string]bool{"": true, "upstream": true, "internal": true}

// RequestLogInput is the metadata-only input of one accounted request. Token
// values use the neutral mutually exclusive four-bucket form (all non-negative);
// the legacy prompt/completion/total mirror columns are derived here, never
// accepted separately, so there is exactly one source of truth. No
// request/response content, credential, base URL, header, or ciphertext may
// ever be passed here; the repository applies diagnostic.Bound to ErrorDiag
// as the final sink policy.
type RequestLogInput struct {
	AttemptID             string
	UserID                int64
	Model                 string // platform model full_name ("" when unknown)
	EndpointKeyID         int64  // 0 when no key was selected or it was deleted
	UpstreamModelID       string
	StatusCode            int
	DurationMs            int64
	StartedAt             time.Time
	CompletedAt           time.Time
	UncachedInputTokens   int64
	CacheWriteInputTokens int64
	CacheReadInputTokens  int64
	OutputTokens          int64
	UsageUnknown          bool
	ErrorCode             string
	ErrorDiag             string
}

func (i RequestLogInput) validate() error {
	if i.UserID <= 0 {
		return fmt.Errorf("usage: user id is required")
	}
	if !validAttemptID(i.AttemptID) {
		return fmt.Errorf("usage: attempt id is invalid")
	}
	if !validOptionalStoredText(i.Model, maxLogModelRunes) {
		return fmt.Errorf("usage: model is invalid")
	}
	if !validOptionalStoredText(i.UpstreamModelID, maxLogUpstreamModelRunes) {
		return fmt.Errorf("usage: upstream model id is invalid")
	}
	if i.EndpointKeyID < 0 {
		return fmt.Errorf("usage: endpoint key id is invalid")
	}
	if i.StatusCode < 0 || i.StatusCode > maxLogStatus {
		return fmt.Errorf("usage: status code is out of range")
	}
	if i.DurationMs < 0 || i.DurationMs > MaxDurationMs {
		return fmt.Errorf("usage: duration is out of range")
	}
	if i.StartedAt.IsZero() {
		return fmt.Errorf("usage: started at is required")
	}
	if i.CompletedAt.Before(i.StartedAt) {
		return fmt.Errorf("usage: completed at precedes started at")
	}
	for _, tokens := range []int64{i.UncachedInputTokens, i.CacheWriteInputTokens, i.CacheReadInputTokens, i.OutputTokens} {
		if tokens < 0 || tokens > MaxTokenDelta {
			return fmt.Errorf("usage: token value is out of range")
		}
	}
	if i.UsageUnknown && (i.UncachedInputTokens != 0 || i.CacheWriteInputTokens != 0 || i.CacheReadInputTokens != 0 || i.OutputTokens != 0) {
		return fmt.Errorf("usage: unknown usage cannot carry token values")
	}
	if len(i.ErrorCode) > maxLogErrorCodeLen || !stableErrorCodes[i.ErrorCode] {
		return fmt.Errorf("usage: error code is not stable")
	}
	return nil
}

func validAttemptID(id string) bool {
	if id == "" || len(id) > MaxAttemptIDLen || !utf8.ValidString(id) {
		return false
	}
	for _, r := range id {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func validOptionalStoredText(value string, maxRunes int) bool {
	if value == "" {
		return true
	}
	if !utf8.ValidString(value) || utf8.RuneCountInString(value) > maxRunes {
		return false
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	first, _ := utf8.DecodeRuneInString(value)
	last, _ := utf8.DecodeLastRuneInString(value)
	return !unicode.IsSpace(first) && !unicode.IsSpace(last)
}

// RecordRequest persists one accounted request: a metadata-only log row plus
// the user-level accumulator updates, in a single transaction. It is
// idempotent for a repeated AttemptID and an atomic no-op for a deleted user
// (see the package comment). Any validation, constraint, or overflow failure
// rolls the whole transaction back: no partial state is ever committed.
func (s *Store) RecordRequest(ctx context.Context, input RequestLogInput) error {
	if err := input.validate(); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("record request: begin: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	startedUnix := input.StartedAt.Unix()
	completedUnix := input.CompletedAt.Unix()
	unknown := boolToInt(input.UsageUnknown)
	diag := diagnostic.Bound(input.ErrorDiag)

	// Derive the legacy compatibility mirrors from the four buckets. The
	// per-request bounds above (each bucket <= MaxTokenDelta) mean these sums
	// cannot overflow int64, but the checked adds keep the invariant explicit
	// and fail closed rather than ever persisting a wrapped value.
	promptMirror, ok := addNonNegative(input.UncachedInputTokens, input.CacheWriteInputTokens)
	if !ok {
		return fmt.Errorf("usage: input token mirror overflows")
	}
	promptMirror, ok = addNonNegative(promptMirror, input.CacheReadInputTokens)
	if !ok {
		return fmt.Errorf("usage: input token mirror overflows")
	}
	completionMirror := input.OutputTokens
	totalMirror, ok := addNonNegative(promptMirror, completionMirror)
	if !ok {
		return fmt.Errorf("usage: total token mirror overflows")
	}

	// INSERT ... SELECT FROM users atomically no-ops when the account has
	// already been deleted (late-callback linearization, same pattern as
	// admin_alerts). The endpoint_key id is checked against the live key set
	// inside the same statement so a key deleted during the upstream attempt
	// keeps the log row with a NULL reference instead of failing the FK.
	res, err := tx.ExecContext(ctx, `
INSERT INTO request_logs
	(attempt_id, user_id, model, endpoint_key_id, upstream_model_id, status_code, duration_ms,
	 started_at, completed_at, prompt_tokens, completion_tokens, total_tokens,
	 uncached_input_tokens, cache_write_input_tokens, cache_read_input_tokens, output_tokens,
	 usage_unknown, error_code, error_diag)
SELECT
       ?,
       ?,
       ?,
       CASE WHEN EXISTS (
           SELECT 1 FROM endpoint_keys ek
           JOIN endpoints e ON e.id = ek.endpoint_id
           WHERE ek.id = ? AND e.user_id = ?
       ) THEN ? ELSE NULL END,
       ?,
       ?,
       ?,
       ?,
       ?,
       ?,
       ?,
       ?,
       ?,
       ?,
       ?,
       ?,
       ?,
       ?,
       ?
FROM users WHERE id = ?`,
		input.AttemptID, input.UserID, input.Model,
		input.EndpointKeyID, input.UserID, input.EndpointKeyID,
		input.UpstreamModelID, input.StatusCode, input.DurationMs,
		startedUnix, completedUnix, promptMirror, completionMirror, totalMirror,
		input.UncachedInputTokens, input.CacheWriteInputTokens, input.CacheReadInputTokens, input.OutputTokens,
		unknown, input.ErrorCode, diag, input.UserID)
	if err != nil {
		if isConstraintError(err) {
			// A duplicate attempt correlation id means the same accounted
			// request was already persisted by an earlier delivery of this
			// hook; treat it as an idempotent no-op (nothing was inserted and
			// the accumulator update below never runs).
			if derr := classifyConflict(ctx, tx,
				`SELECT COUNT(*) FROM request_logs WHERE attempt_id = ?`, input.AttemptID); derr == ErrConflict {
				return nil
			}
		}
		return fmt.Errorf("insert request log: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("request log rows affected: %w", err)
	}
	if affected == 0 {
		// The user row was deleted before this transaction (late callback):
		// nothing was written and nothing accumulates. Rolling back is a
		// no-op; returning nil keeps late hook delivery quiet.
		return nil
	}

	// The user exists (the insert above succeeded); the accumulator update
	// must match exactly one row. The bounded predicate makes an overflow
	// match zero rows and fail the transaction closed. The four-bucket
	// accumulators are authoritative; the legacy prompt/completion columns
	// remain compatible mirrors (prompt = sum of the three input buckets).
	res2, err := tx.ExecContext(ctx, `
UPDATE users SET
	total_requests                 = total_requests + 1,
	total_uncached_input_tokens    = total_uncached_input_tokens + ?,
	total_cache_write_input_tokens = total_cache_write_input_tokens + ?,
	total_cache_read_input_tokens  = total_cache_read_input_tokens + ?,
	total_output_tokens            = total_output_tokens + ?,
	total_prompt_tokens            = total_prompt_tokens + ?,
	total_completion_tokens        = total_completion_tokens + ?,
	total_unknown_usage_requests   = total_unknown_usage_requests + ?,
	updated_at                     = ?
WHERE id = ?
  AND total_requests                    <= ?
  AND total_uncached_input_tokens       <= ?
  AND total_cache_write_input_tokens    <= ?
  AND total_cache_read_input_tokens     <= ?
  AND total_output_tokens               <= ?
  AND total_prompt_tokens               <= ?
  AND total_completion_tokens           <= ?
  AND total_unknown_usage_requests      <= ?`,
		input.UncachedInputTokens, input.CacheWriteInputTokens, input.CacheReadInputTokens, input.OutputTokens,
		promptMirror, completionMirror, unknown, completedUnix,
		input.UserID,
		accumulatorCeiling-1,
		accumulatorCeiling-input.UncachedInputTokens,
		accumulatorCeiling-input.CacheWriteInputTokens,
		accumulatorCeiling-input.CacheReadInputTokens,
		accumulatorCeiling-input.OutputTokens,
		accumulatorCeiling-promptMirror,
		accumulatorCeiling-completionMirror,
		accumulatorCeiling-int64(unknown))
	if err != nil {
		return fmt.Errorf("accumulate user usage: %w", err)
	}
	updated, err := res2.RowsAffected()
	if err != nil {
		return fmt.Errorf("accumulate user usage rows affected: %w", err)
	}
	if updated != 1 {
		return fmt.Errorf("user usage accumulator overflow; accounting failed closed")
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit request record: %w", err)
	}
	committed = true
	return nil
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

// addNonNegative adds two values already validated non-negative and reports
// whether the sum fits in int64.
func addNonNegative(a, b int64) (int64, bool) {
	sum := a + b
	if sum < a || sum < b {
		return 0, false
	}
	return sum, true
}

// ===== retention cleanup ====================================================
// The 30-day request-log retention policy (DEC-006, api-contract): log rows
// whose started_at is strictly older than a server-side cutoff are deleted in
// bounded single-transaction batches. The users accumulator columns are the
// server-authoritative totals and are never recomputed or decremented here
// (AggregateUsage never sums request_logs), so cleanup can never change the
// per-user or site-wide usage numbers. Only request_logs is touched:
// user_issues, admin_alerts, sessions, keys, endpoints and models are
// outside this policy.

// CountRequestLogsBefore returns the number of metadata-only log rows whose
// started_at is strictly before beforeUnix. It backs the dry-run/count seam
// for operators and the retention scheduler.
func (s *Store) CountRequestLogsBefore(ctx context.Context, beforeUnix int64) (int64, error) {
	if beforeUnix <= 0 {
		return 0, fmt.Errorf("count request logs before: cutoff is invalid")
	}
	var n int64
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM request_logs WHERE started_at < ?`, beforeUnix).Scan(&n); err != nil {
		return 0, fmt.Errorf("count request logs before: %w", err)
	}
	return n, nil
}

// DeleteRequestLogsBefore deletes at most limit metadata-only log rows with
// started_at strictly before beforeUnix, in a single transaction (one bounded
// batch so a large table is never swept in one write lock). It is idempotent
// — every surviving row still satisfies the predicate, so reruns converge to
// the same state — and it fails closed: any error rolls the transaction back
// and no partial deletion is ever committed. limit is clamped to 1..
// MaxCleanupBatch.
func (s *Store) DeleteRequestLogsBefore(ctx context.Context, beforeUnix int64, limit int) (int64, error) {
	if beforeUnix <= 0 {
		return 0, fmt.Errorf("delete request logs before: cutoff is invalid")
	}
	if limit < 1 {
		return 0, fmt.Errorf("delete request logs before: batch limit is invalid")
	}
	if limit > MaxCleanupBatch {
		limit = MaxCleanupBatch
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("delete request logs before: begin: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	res, err := tx.ExecContext(ctx,
		`DELETE FROM request_logs WHERE id IN (SELECT id FROM request_logs WHERE started_at < ? ORDER BY id LIMIT ?)`,
		beforeUnix, limit)
	if err != nil {
		return 0, fmt.Errorf("delete request logs before: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("delete request logs before: rows affected: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("delete request logs before: commit: %w", err)
	}
	committed = true
	return affected, nil
}

// UsageTotals is the server-authoritative per-user usage projection backing
// GET /api/me/usage and the admin usage screens. Metadata only. The four-bucket
// totals are authoritative; total_prompt/completion remain compatible mirrors.
type UsageTotals struct {
	TotalRequests              int64
	TotalUncachedInputTokens   int64
	TotalCacheWriteInputTokens int64
	TotalCacheReadInputTokens  int64
	TotalOutputTokens          int64
	TotalPromptTokens          int64
	TotalCompletionTokens      int64
	TotalUnknownUsageRequests  int64
}

// GetUserUsage returns the accumulator totals for userID, or ErrNotFound.
// Ownership-scoped by the user id; the caller decides which identity may read
// it.
func (s *Store) GetUserUsage(ctx context.Context, userID int64) (UsageTotals, error) {
	if userID <= 0 {
		return UsageTotals{}, ErrNotFound
	}
	var totals UsageTotals
	err := s.db.QueryRowContext(ctx, `
SELECT total_requests,
       total_uncached_input_tokens, total_cache_write_input_tokens,
       total_cache_read_input_tokens, total_output_tokens,
       total_prompt_tokens, total_completion_tokens, total_unknown_usage_requests
FROM users WHERE id = ?`, userID).
		Scan(&totals.TotalRequests,
			&totals.TotalUncachedInputTokens, &totals.TotalCacheWriteInputTokens,
			&totals.TotalCacheReadInputTokens, &totals.TotalOutputTokens,
			&totals.TotalPromptTokens, &totals.TotalCompletionTokens, &totals.TotalUnknownUsageRequests)
	if errors.Is(err, sql.ErrNoRows) {
		return UsageTotals{}, ErrNotFound
	}
	if err != nil {
		return UsageTotals{}, fmt.Errorf("read user usage: %w", err)
	}
	return totals, nil
}

// AggregateUsage returns site-wide accumulator totals across all normal
// (non-administrator) users. The users table is the server-authoritative
// accumulator set; request_logs is never summed, so retention cleanup or a
// re-insertion can never double-count a request.
func (s *Store) AggregateUsage(ctx context.Context) (UsageTotals, error) {
	var totals UsageTotals
	err := s.db.QueryRowContext(ctx, `
SELECT COALESCE(SUM(total_requests), 0),
       COALESCE(SUM(total_uncached_input_tokens), 0),
       COALESCE(SUM(total_cache_write_input_tokens), 0),
       COALESCE(SUM(total_cache_read_input_tokens), 0),
       COALESCE(SUM(total_output_tokens), 0),
       COALESCE(SUM(total_prompt_tokens), 0),
       COALESCE(SUM(total_completion_tokens), 0),
       COALESCE(SUM(total_unknown_usage_requests), 0)
FROM users WHERE is_admin = 0`).
		Scan(&totals.TotalRequests,
			&totals.TotalUncachedInputTokens, &totals.TotalCacheWriteInputTokens,
			&totals.TotalCacheReadInputTokens, &totals.TotalOutputTokens,
			&totals.TotalPromptTokens, &totals.TotalCompletionTokens, &totals.TotalUnknownUsageRequests)
	if err != nil {
		return UsageTotals{}, fmt.Errorf("aggregate usage: %w", err)
	}
	return totals, nil
}

// UserUsageRow is one user's accumulator totals for the by-user aggregation.
type UserUsageRow struct {
	UserID int64
	UsageTotals
}

// AggregateUsageByUser returns up to limit users' totals ordered by total
// requests descending (ties broken by user id), bounded by
// MaxUsageByUserLimit. It excludes the environment-owned administrator.
func (s *Store) AggregateUsageByUser(ctx context.Context, limit int) ([]UserUsageRow, error) {
	if limit <= 0 {
		limit = MaxUsageByUserLimit
	}
	if limit > MaxUsageByUserLimit {
		limit = MaxUsageByUserLimit
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id, total_requests,
       total_uncached_input_tokens, total_cache_write_input_tokens,
       total_cache_read_input_tokens, total_output_tokens,
       total_prompt_tokens, total_completion_tokens, total_unknown_usage_requests
FROM users WHERE is_admin = 0
ORDER BY total_requests DESC, id ASC
LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("aggregate usage by user: %w", err)
	}
	defer rows.Close()

	users := make([]UserUsageRow, 0, min(limit, 32))
	for rows.Next() {
		var row UserUsageRow
		if err := rows.Scan(
			&row.UserID,
			&row.TotalRequests,
			&row.TotalUncachedInputTokens, &row.TotalCacheWriteInputTokens,
			&row.TotalCacheReadInputTokens, &row.TotalOutputTokens,
			&row.TotalPromptTokens, &row.TotalCompletionTokens, &row.TotalUnknownUsageRequests,
		); err != nil {
			return nil, fmt.Errorf("scan usage by user: %w", err)
		}
		users = append(users, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate usage by user: %w", err)
	}
	return users, nil
}
