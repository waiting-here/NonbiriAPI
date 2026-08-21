package db

// Daily product-activity aggregation behind the administrator activity
// screen: a per-user table (user_activity_daily) and its exact site-wide
// rollup (site_activity_daily), both keyed by the site-local day under the
// explicitly configured fixed offset (see timezone.go).
//
// Invariants:
//
//   - No day key is ever generated while the site timezone offset is unset:
//     every writer resolves the offset inside its own transaction and treats
//     ErrTimezoneUnavailable as "activity disabled" (a silent no-op for the
//     business operation, an explicit disabled status at the read boundary).
//   - The user row and the site rollup are always written in one transaction,
//     in the same transaction as the triggering business write where one
//     exists (committed request accounting, console configuration writes).
//     A late write against a deleted user is suppressed atomically by
//     INSERT ... SELECT ... WHERE EXISTS users — never by a read-then-write.
//   - A deleted account can never leave a contribution behind: account
//     deletion recomputes every affected day's site row from the surviving
//     user rows inside the deletion transaction (see account_delete.go).
//   - distinct_product_users is maintained incrementally but is only ever
//     exposed through QuerySiteActivity; the API boundary suppresses values
//     below ActivityKAnonymity to null so a small group is never conflated
//     with zero. No user-level drill-down exists on any read path.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"time"
)

const (
	// ActivityKAnonymity is the suppression threshold for distinct-user
	// counts: a day with fewer distinct active users reports null instead of
	// the true count. Non-distinct aggregates (requests, tokens, check-ins,
	// console writes) are never suppressed.
	ActivityKAnonymity = 5

	// ActivityRetentionDays is the frozen retention window for both activity
	// tables. Day keys older than this many site-local days are removed by
	// the bounded maintenance sweep.
	ActivityRetentionDays = 400

	// activityMaxCountDelta bounds one hook's non-token counter delta. Token
	// deltas reuse MaxTokenDelta from the usage accounting bounds.
	activityMaxCountDelta = int64(1) << 32

	// DefaultActivityCleanupBatchSize bounds one cleanup delete batch.
	DefaultActivityCleanupBatchSize = 500
	// DefaultActivityCleanupMaxBatches bounds one cleanup run's batch count.
	DefaultActivityCleanupMaxBatches = 100
)

// ActivityDelta is one trigger's contribution to the daily aggregation. All
// fields must be non-negative and bounded; a delta with no contribution is
// rejected. A successful check-in contributes Checkins=1 through the
// in-transaction helper in the same transaction as its business write (see
// checkins.go); game contributions stay reserved for a future rail.
type ActivityDelta struct {
	APIRequests           int64
	UncachedInputTokens   int64
	CacheWriteInputTokens int64
	CacheReadInputTokens  int64
	OutputTokens          int64
	Checkins              int64
	ConsoleWrites         int64
}

func (d ActivityDelta) validate() error {
	for _, count := range []int64{d.APIRequests, d.Checkins, d.ConsoleWrites} {
		if count < 0 || count > activityMaxCountDelta {
			return fmt.Errorf("activity: counter delta is out of range")
		}
	}
	for _, tokens := range []int64{d.UncachedInputTokens, d.CacheWriteInputTokens, d.CacheReadInputTokens, d.OutputTokens} {
		if tokens < 0 || tokens > MaxTokenDelta {
			return fmt.Errorf("activity: token delta is out of range")
		}
	}
	if d.APIRequests == 0 && d.Checkins == 0 && d.ConsoleWrites == 0 &&
		d.UncachedInputTokens == 0 && d.CacheWriteInputTokens == 0 &&
		d.CacheReadInputTokens == 0 && d.OutputTokens == 0 {
		return fmt.Errorf("activity: empty delta")
	}
	return nil
}

func (d ActivityDelta) active() bool {
	return d.APIRequests > 0 || d.Checkins > 0 || d.ConsoleWrites > 0
}

// RecordActivity applies one activity delta for userID at instant at in its
// own transaction. It is the standalone entry point for triggers whose
// business write does not already carry activity (and for the future check-in
// rail). When the site timezone offset is unset the call is a successful
// no-op: activity is disabled and the triggering operation must not fail
// because of it. A late call against a deleted user is an atomic no-op.
func (s *Store) RecordActivity(ctx context.Context, userID int64, at time.Time, delta ActivityDelta) error {
	if err := delta.validate(); err != nil {
		return err
	}
	if userID <= 0 || at.IsZero() {
		return fmt.Errorf("activity: user id and time are required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("record activity: begin: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	dayKey, err := siteDayKeyAtTx(tx, at.Unix())
	if errors.Is(err, ErrTimezoneUnavailable) {
		// Activity is disabled while no explicit offset is configured: skip
		// silently rather than bucket events under an implicit default.
		return nil
	}
	if err != nil {
		return fmt.Errorf("record activity: resolve day key: %w", err)
	}
	inserted, err := recordActivityTx(ctx, tx, userID, dayKey, delta, at.Unix())
	if err != nil {
		return fmt.Errorf("record activity: %w", err)
	}
	_ = inserted // false only when the user was concurrently deleted: a suppressed late write
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("record activity: commit: %w", err)
	}
	committed = true
	return nil
}

// recordActivityTx applies one delta inside the caller's transaction. It
// returns false when the write was suppressed because the user no longer
// exists (late-write linearization); in that case the site rollup is not
// touched either, so no orphan contribution can be created.
func recordActivityTx(ctx context.Context, tx *sql.Tx, userID, dayKey int64, delta ActivityDelta, nowUnix int64) (bool, error) {
	// Whether this is the first temporal row ever decides whether the write
	// must durably freeze the site timezone offset (see freezeTimezoneTx). The
	// probe runs before this transaction's own insert; the index-backed EXISTS
	// scan is O(1) on the hot path and only the first-ever row pays the lock
	// upsert after the insert lands.
	firstTemporalRow, err := timezoneTableEmptyTx(ctx, tx, "user_activity_daily")
	if err != nil {
		return false, err
	}
	// Whether the user was already product-active on this day decides whether
	// the site rollup's distinct counter increments. Reading it here is safe:
	// the single SQLite writer serializes whole transactions, so no other
	// transaction can interleave between this read and the writes below.
	var wasActive int
	err = tx.QueryRowContext(ctx,
		`SELECT product_active FROM user_activity_daily WHERE day = ? AND user_id = ?`,
		dayKey, userID).Scan(&wasActive)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, fmt.Errorf("read prior activity: %w", err)
	}
	distinctInc := int64(1)
	if err == nil && wasActive == 1 {
		distinctInc = 0
	}

	// Atomic conditional upsert: the INSERT ... SELECT contributes zero rows
	// once the user has been deleted, which both suppresses the late write and
	// skips the site rollup below.
	res, err := tx.ExecContext(ctx, `
INSERT INTO user_activity_daily
	(day, user_id, product_active, api_requests,
	 uncached_input_tokens, cache_write_input_tokens, cache_read_input_tokens, output_tokens,
	 checkins, console_writes, game_active, game_rounds, updated_at)
SELECT ?, ?, 1, ?, ?, ?, ?, ?, ?, ?, 0, 0, ?
WHERE EXISTS (SELECT 1 FROM users WHERE id = ?)
ON CONFLICT(day, user_id) DO UPDATE SET
	product_active            = 1,
	api_requests              = api_requests + excluded.api_requests,
	uncached_input_tokens     = uncached_input_tokens + excluded.uncached_input_tokens,
	cache_write_input_tokens  = cache_write_input_tokens + excluded.cache_write_input_tokens,
	cache_read_input_tokens   = cache_read_input_tokens + excluded.cache_read_input_tokens,
	output_tokens             = output_tokens + excluded.output_tokens,
	checkins                  = checkins + excluded.checkins,
	console_writes            = console_writes + excluded.console_writes,
	updated_at                = excluded.updated_at`,
		dayKey, userID, delta.APIRequests,
		delta.UncachedInputTokens, delta.CacheWriteInputTokens, delta.CacheReadInputTokens, delta.OutputTokens,
		delta.Checkins, delta.ConsoleWrites, nowUnix, userID)
	if err != nil {
		return false, fmt.Errorf("upsert user activity: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("upsert user activity rows affected: %w", err)
	}
	if affected == 0 {
		// The user was deleted before this transaction: suppressed late write.
		return false, nil
	}

	// Site rollup. On conflict the excluded.distinct_product_users value (the
	// increment computed above) is added to the stored counter; the WHERE
	// guard fails the update closed if any counter would overflow instead of
	// letting SQLite silently widen to floating point.
	res, err = tx.ExecContext(ctx, `
INSERT INTO site_activity_daily
	(day, product_active, api_requests,
	 uncached_input_tokens, cache_write_input_tokens, cache_read_input_tokens, output_tokens,
	 checkins, console_writes, game_active, game_rounds, distinct_product_users, updated_at)
VALUES (?, 1, ?, ?, ?, ?, ?, ?, ?, 0, 0, ?, ?)
ON CONFLICT(day) DO UPDATE SET
	product_active            = 1,
	api_requests              = api_requests + excluded.api_requests,
	uncached_input_tokens     = uncached_input_tokens + excluded.uncached_input_tokens,
	cache_write_input_tokens  = cache_write_input_tokens + excluded.cache_write_input_tokens,
	cache_read_input_tokens   = cache_read_input_tokens + excluded.cache_read_input_tokens,
	output_tokens             = output_tokens + excluded.output_tokens,
	checkins                  = checkins + excluded.checkins,
	console_writes            = console_writes + excluded.console_writes,
	distinct_product_users    = distinct_product_users + excluded.distinct_product_users,
	updated_at                = excluded.updated_at
WHERE api_requests                 <= ?
  AND uncached_input_tokens        <= ?
  AND cache_write_input_tokens     <= ?
  AND cache_read_input_tokens      <= ?
  AND output_tokens                <= ?
  AND checkins                     <= ?
  AND console_writes               <= ?
  AND distinct_product_users       <= ?`,
		dayKey, delta.APIRequests,
		delta.UncachedInputTokens, delta.CacheWriteInputTokens, delta.CacheReadInputTokens, delta.OutputTokens,
		delta.Checkins, delta.ConsoleWrites, distinctInc, nowUnix,
		math.MaxInt64-delta.APIRequests-1,
		math.MaxInt64-delta.UncachedInputTokens,
		math.MaxInt64-delta.CacheWriteInputTokens,
		math.MaxInt64-delta.CacheReadInputTokens,
		math.MaxInt64-delta.OutputTokens,
		math.MaxInt64-delta.Checkins,
		math.MaxInt64-delta.ConsoleWrites,
		math.MaxInt64-distinctInc)
	if err != nil {
		return true, fmt.Errorf("upsert site activity: %w", err)
	}
	affected, err = res.RowsAffected()
	if err != nil {
		return true, fmt.Errorf("upsert site activity rows affected: %w", err)
	}
	if affected != 1 {
		return true, fmt.Errorf("site activity aggregate overflow; write failed closed")
	}
	// The first temporal row ever durably freezes the site timezone offset in
	// this same transaction: retention cleanup must never be able to empty the
	// freeze tables and make the offset mutable again. Idempotent for every
	// later write (the probe above already short-circuited them).
	if firstTemporalRow {
		if err := freezeTimezoneTx(ctx, tx, nowUnix); err != nil {
			return true, err
		}
	}
	return true, nil
}

// distinctActivityDaysTx returns the (at most 400) site-local day keys the
// user has any activity row for, newest first. The bound matches the retention
// window: a user can never legitimately hold more days, and capping here keeps
// the deletion transaction finite even against a corrupted backlog.
func distinctActivityDaysTx(ctx context.Context, tx *sql.Tx, userID int64) ([]int64, error) {
	rows, err := tx.QueryContext(ctx, `
SELECT DISTINCT day FROM user_activity_daily WHERE user_id = ? ORDER BY day DESC LIMIT ?`,
		userID, ActivityRetentionDays)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var days []int64
	for rows.Next() {
		var day int64
		if err := rows.Scan(&day); err != nil {
			return nil, err
		}
		days = append(days, day)
	}
	return days, rows.Err()
}

// recomputeSiteActivityDayTx rebuilds one day's site_activity_daily row from
// the surviving per-user rows inside the caller's transaction. The counters
// become the exact sum over those rows; distinct_product_users counts the
// rows with product_active=1 (one row per user-day by primary key). A day
// with no remaining contributor loses its site row entirely, so a deleted
// account can never leave a contribution behind and no all-zero ghost day
// survives.
func recomputeSiteActivityDayTx(ctx context.Context, tx *sql.Tx, day int64) error {
	// Drop the stale rollup first when no per-user row remains; otherwise the
	// INSERT ... SELECT below would leave the old values in place.
	if _, err := tx.ExecContext(ctx, `
DELETE FROM site_activity_daily
WHERE day = ?
  AND NOT EXISTS (SELECT 1 FROM user_activity_daily uad WHERE uad.day = site_activity_daily.day)`, day); err != nil {
		return fmt.Errorf("drop empty site activity day: %w", err)
	}
	res, err := tx.ExecContext(ctx, `
INSERT INTO site_activity_daily
	(day, product_active, api_requests,
	 uncached_input_tokens, cache_write_input_tokens, cache_read_input_tokens, output_tokens,
	 checkins, console_writes, game_active, game_rounds, distinct_product_users, updated_at)
SELECT ?, MAX(product_active),
       COALESCE(SUM(api_requests), 0),
       COALESCE(SUM(uncached_input_tokens), 0),
       COALESCE(SUM(cache_write_input_tokens), 0),
       COALESCE(SUM(cache_read_input_tokens), 0),
       COALESCE(SUM(output_tokens), 0),
       COALESCE(SUM(checkins), 0),
       COALESCE(SUM(console_writes), 0),
       MAX(game_active),
       COALESCE(SUM(game_rounds), 0),
       COALESCE(SUM(product_active), 0),
       ?
FROM user_activity_daily WHERE day = ?
HAVING COUNT(*) > 0
ON CONFLICT(day) DO UPDATE SET
	product_active            = excluded.product_active,
	api_requests              = excluded.api_requests,
	uncached_input_tokens     = excluded.uncached_input_tokens,
	cache_write_input_tokens  = excluded.cache_write_input_tokens,
	cache_read_input_tokens   = excluded.cache_read_input_tokens,
	output_tokens             = excluded.output_tokens,
	checkins                  = excluded.checkins,
	console_writes            = excluded.console_writes,
	game_active               = excluded.game_active,
	game_rounds               = excluded.game_rounds,
	distinct_product_users    = excluded.distinct_product_users,
	updated_at                = excluded.updated_at`,
		day, time.Now().Unix(), day)
	if err != nil {
		return fmt.Errorf("recompute site activity day: %w", err)
	}
	if _, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("recompute site activity day rows affected: %w", err)
	}
	return nil
}

// SetSiteConfigValueWithActivity persists one site_config upsert and records
// the administrator's console write as product activity in the same
// transaction, so the rollup can never show a configuration change that did
// not commit or vice versa. While the site timezone offset is unset the
// configuration value is still persisted and only the activity recording is
// skipped (activity is disabled). Callers exempt keys whose write must not
// create temporal data — notably the timezone offset itself, whose first
// activity-derived freeze would otherwise contradict its own immutability
// window.
func (s *Store) SetSiteConfigValueWithActivity(ctx context.Context, key, value string, actorUserID int64, at time.Time) error {
	if err := validateSiteConfigText(key, maxSiteConfigKeyBytes, false, false); err != nil {
		return ErrConflict
	}
	if err := validateSiteConfigText(value, maxSiteConfigValueBytes, true, true); err != nil {
		return ErrConflict
	}
	if actorUserID <= 0 || at.IsZero() {
		return fmt.Errorf("set site configuration with activity: actor and time are required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("set site configuration: begin: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if _, err := tx.ExecContext(ctx, `INSERT INTO site_config (key, value, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`,
		key, value, at.Unix()); err != nil {
		return fmt.Errorf("set site configuration: %w", err)
	}
	dayKey, err := siteDayKeyAtTx(tx, at.Unix())
	if errors.Is(err, ErrTimezoneUnavailable) {
		// Activity disabled: keep the configuration write, skip the rollup.
	} else if err != nil {
		return fmt.Errorf("set site configuration: resolve day key: %w", err)
	} else if _, err := recordActivityTx(ctx, tx, actorUserID, dayKey, ActivityDelta{ConsoleWrites: 1}, at.Unix()); err != nil {
		return fmt.Errorf("set site configuration: record activity: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("set site configuration: commit: %w", err)
	}
	committed = true
	return nil
}

// SiteActivityDay is one site-local day's rollup as read by the
// administrator activity screen. DistinctProductUsers is the raw stored
// count; the k-anonymity suppression to null happens at the API boundary.
type SiteActivityDay struct {
	Day                   int64
	ProductActive         bool
	APIRequests           int64
	UncachedInputTokens   int64
	CacheWriteInputTokens int64
	CacheReadInputTokens  int64
	OutputTokens          int64
	Checkins              int64
	ConsoleWrites         int64
	GameActive            bool
	GameRounds            int64
	DistinctProductUsers  int64
	UpdatedAt             int64
}

// SiteActivityQuery is the bounded page request for the admin activity list.
// Days are returned newest first; Page is 1-based and PageSize defaults to 20
// clamped into [1, MaxLogPageLimit].
type SiteActivityQuery struct {
	Page     int
	PageSize int
}

// QuerySiteActivity returns at most one page of site-day rollups, newest
// first, plus whether another page follows. It never projects per-user data:
// there is no user dimension on any activity read path.
func (s *Store) QuerySiteActivity(ctx context.Context, query SiteActivityQuery) ([]SiteActivityDay, bool, error) {
	page := query.Page
	if page < 1 {
		page = 1
	}
	pageSize := query.PageSize
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > MaxLogPageLimit {
		pageSize = MaxLogPageLimit
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT day, product_active, api_requests,
       uncached_input_tokens, cache_write_input_tokens, cache_read_input_tokens, output_tokens,
       checkins, console_writes, game_active, game_rounds, distinct_product_users, updated_at
FROM site_activity_daily
ORDER BY day DESC
LIMIT ? OFFSET ?`, pageSize+1, (page-1)*pageSize)
	if err != nil {
		return nil, false, fmt.Errorf("query site activity: %w", err)
	}
	defer rows.Close()

	days := make([]SiteActivityDay, 0, min(pageSize, 32))
	for rows.Next() {
		var day SiteActivityDay
		var productActive, gameActive int
		if err := rows.Scan(&day.Day, &productActive, &day.APIRequests,
			&day.UncachedInputTokens, &day.CacheWriteInputTokens, &day.CacheReadInputTokens, &day.OutputTokens,
			&day.Checkins, &day.ConsoleWrites, &gameActive, &day.GameRounds, &day.DistinctProductUsers, &day.UpdatedAt); err != nil {
			return nil, false, fmt.Errorf("scan site activity: %w", err)
		}
		day.ProductActive = productActive == 1
		day.GameActive = gameActive == 1
		days = append(days, day)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("iterate site activity: %w", err)
	}
	hasMore := len(days) > pageSize
	if hasMore {
		days = days[:pageSize]
	}
	return days, hasMore, nil
}

// ActivityRetentionCutoffDay returns the newest day key that is still inside
// the retention window: rows whose day key is strictly below it are more than
// ActivityRetentionDays site-local days old. An unset timezone yields
// ErrTimezoneUnavailable; callers skip cleanup then, since no activity row
// can exist without a configured offset.
func (s *Store) ActivityRetentionCutoffDay(nowUnix int64) (int64, error) {
	offset, err := s.SiteTimezoneOffsetMinutes()
	if err != nil {
		return 0, err
	}
	return SiteDayKey(nowUnix-ActivityRetentionDays*86400, int64(offset)), nil
}

// CleanupActivityBefore deletes every activity row whose day key is strictly
// below beforeDay, in bounded single-transaction batches. Site-day rows are
// removed only after their last per-user row is gone, so a surviving site row
// always remains exactly derivable from user_activity_daily. The run stops at
// the batch-count bound and is resumable by the next scheduled sweep; it is
// idempotent because every surviving row still satisfies the predicate.
func (s *Store) CleanupActivityBefore(ctx context.Context, beforeDay int64) (deleted int64, stoppedEarly bool, err error) {
	return s.cleanupActivityBeforeBounded(ctx, beforeDay, DefaultActivityCleanupBatchSize, DefaultActivityCleanupMaxBatches)
}

// cleanupActivityBeforeBounded is the explicit-bounds seam for tests and
// targeted sweeps.
func (s *Store) cleanupActivityBeforeBounded(ctx context.Context, beforeDay int64, batchSize, maxBatches int) (int64, bool, error) {
	if beforeDay <= 0 {
		return 0, false, fmt.Errorf("cleanup activity before: cutoff day is invalid")
	}
	if batchSize < 1 || maxBatches < 1 {
		return 0, false, fmt.Errorf("cleanup activity before: batch bounds are invalid")
	}
	var deleted int64
	for batch := 0; batch < maxBatches; batch++ {
		if err := ctx.Err(); err != nil {
			return deleted, true, err
		}
		n, err := s.cleanupActivityBatch(ctx, beforeDay, batchSize)
		if err != nil {
			return deleted, false, err
		}
		deleted += n
		if n < int64(batchSize) {
			return deleted, false, nil
		}
	}
	return deleted, true, nil
}

// cleanupActivityBatch deletes one bounded batch of aged per-user rows plus
// every site-day row left without any per-user row, in one transaction.
func (s *Store) cleanupActivityBatch(ctx context.Context, beforeDay int64, limit int) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("cleanup activity: begin: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	res, err := tx.ExecContext(ctx, `
DELETE FROM user_activity_daily
WHERE day < ?
  AND (day, user_id) IN (SELECT day, user_id FROM user_activity_daily WHERE day < ? ORDER BY day, user_id LIMIT ?)`,
		beforeDay, beforeDay, limit)
	if err != nil {
		return 0, fmt.Errorf("cleanup activity: delete user rows: %w", err)
	}
	userDeleted, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("cleanup activity: user rows affected: %w", err)
	}
	res, err = tx.ExecContext(ctx, `
DELETE FROM site_activity_daily
WHERE day < ?
  AND NOT EXISTS (SELECT 1 FROM user_activity_daily uad WHERE uad.day = site_activity_daily.day)`,
		beforeDay)
	if err != nil {
		return 0, fmt.Errorf("cleanup activity: delete site rows: %w", err)
	}
	siteDeleted, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("cleanup activity: site rows affected: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("cleanup activity: commit: %w", err)
	}
	committed = true
	return userDeleted + siteDeleted, nil
}
