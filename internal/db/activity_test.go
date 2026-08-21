package db

// Tests for the daily product-activity aggregation: day-key arithmetic under
// a half-hour fixed offset (no DST), day boundaries, atomic user+site
// rollups, at-most-once hook delivery, concurrent first-active distinct
// counting, k-anonymity data, deletion recomputation and late-write
// linearization, bounded retention cleanup, and the unset-timezone no-op.

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// setOffset configures an explicit site timezone offset for the store.
func setOffset(t *testing.T, st *Store, minutes int) {
	t.Helper()
	if err := st.SetSiteTimezoneOffsetMinutes(minutes); err != nil {
		t.Fatalf("SetSiteTimezoneOffsetMinutes(%d): %v", minutes, err)
	}
}

// mustDayKey resolves the day key for a unix instant or fails the test.
func mustDayKey(t *testing.T, st *Store, unix int64) int64 {
	t.Helper()
	day, err := st.SiteDayKeyAt(unix)
	if err != nil {
		t.Fatalf("SiteDayKeyAt(%d): %v", unix, err)
	}
	return day
}

func TestSiteDayKeyHalfHourOffset(t *testing.T) {
	// Offset +05:30 (a 30-minute multiple that is not a whole hour). Local
	// midnight is UTC 18:30 of the previous day; the day key is exactly that
	// local midnight's unix seconds.
	const offset = int64(330)
	wantMidnight := time.Date(2024, 3, 14, 18, 30, 0, 0, time.UTC).Unix()
	for _, tc := range []struct {
		instant time.Time
		want    int64
	}{
		{time.Date(2024, 3, 14, 18, 29, 59, 0, time.UTC), wantMidnight - 86400}, // one second before local midnight
		{time.Date(2024, 3, 14, 18, 30, 0, 0, time.UTC), wantMidnight},          // exactly local midnight
		{time.Date(2024, 3, 15, 0, 0, 0, 0, time.UTC), wantMidnight},            // UTC midnight is mid-day locally
		{time.Date(2024, 3, 15, 12, 0, 0, 0, time.UTC), wantMidnight},           // local noon
		{time.Date(2024, 3, 15, 18, 30, 0, 0, time.UTC), wantMidnight + 86400},  // next local midnight
	} {
		if got := SiteDayKey(tc.instant.Unix(), offset); got != tc.want {
			t.Fatalf("SiteDayKey(%s)=%d, want %d", tc.instant, got, tc.want)
		}
	}
}

func TestActivityDayBoundaryUTC2330And0030(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "activity-boundary.db"))
	defer st.Close()
	setOffset(t, st, 60) // +01:00: local day flips at UTC 23:00.

	before := time.Date(2024, 6, 10, 22, 30, 0, 0, time.UTC).Unix() // local 23:30
	after := time.Date(2024, 6, 10, 23, 30, 0, 0, time.UTC).Unix()  // local 00:30 next day
	dayBefore := mustDayKey(t, st, before)
	dayAfter := mustDayKey(t, st, after)
	if dayAfter != dayBefore+86400 {
		t.Fatalf("UTC 23:30/00:30 straddle: %d vs %d", dayBefore, dayAfter)
	}

	uid := seedUserRaw(t, st, "boundary-user")
	if err := st.RecordActivity(context.Background(), uid, time.Unix(before, 0), ActivityDelta{APIRequests: 1}); err != nil {
		t.Fatalf("RecordActivity(before): %v", err)
	}
	if err := st.RecordActivity(context.Background(), uid, time.Unix(after, 0), ActivityDelta{APIRequests: 1}); err != nil {
		t.Fatalf("RecordActivity(after): %v", err)
	}
	// The two events must land in two different user rows (one per local day).
	n := countRows(t, st, `SELECT COUNT(*) FROM user_activity_daily WHERE user_id=?`, uid)
	if n != 2 {
		t.Fatalf("user_activity_daily rows=%d, want 2 (day boundary split)", n)
	}
	if got := countRows(t, st, `SELECT COUNT(*) FROM site_activity_daily`); got != 2 {
		t.Fatalf("site_activity_daily rows=%d, want 2", got)
	}
}

func TestFixedOffsetHasNoDSTShift(t *testing.T) {
	// A fixed offset is pure arithmetic: consecutive local days are always
	// exactly 86400s apart, including across dates where many real zones
	// switch to summer time.
	const offset = int64(120)
	start := time.Date(2024, 3, 29, 12, 0, 0, 0, time.UTC).Unix()
	prev := SiteDayKey(start, offset)
	for i := 1; i <= 7; i++ {
		cur := SiteDayKey(start+int64(i)*86400, offset)
		if cur-prev != 86400 {
			t.Fatalf("day %d: gap %d, want 86400 (fixed offset has no DST)", i, cur-prev)
		}
		prev = cur
	}
}

func TestRecordActivityAggregatesUserAndSite(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "activity-agg.db"))
	defer st.Close()
	setOffset(t, st, 330)

	alice := seedUserRaw(t, st, "agg-alice")
	bob := seedUserRaw(t, st, "agg-bob")
	at := time.Now().UTC()

	delta := ActivityDelta{
		APIRequests:           1,
		UncachedInputTokens:   100,
		CacheWriteInputTokens: 20,
		CacheReadInputTokens:  30,
		OutputTokens:          50,
	}
	for i := 0; i < 3; i++ {
		if err := st.RecordActivity(context.Background(), alice, at, delta); err != nil {
			t.Fatalf("RecordActivity(alice #%d): %v", i, err)
		}
	}
	if err := st.RecordActivity(context.Background(), bob, at, ActivityDelta{ConsoleWrites: 1}); err != nil {
		t.Fatalf("RecordActivity(bob): %v", err)
	}

	var ua userActivityRow
	if err := st.DB().QueryRow(`SELECT product_active, api_requests,
		uncached_input_tokens, cache_write_input_tokens, cache_read_input_tokens, output_tokens,
		checkins, console_writes, game_active, game_rounds
		FROM user_activity_daily WHERE user_id=?`, alice).
		Scan(&ua.ProductActive, &ua.APIRequests, &ua.Uncached, &ua.CacheWrite, &ua.CacheRead, &ua.Output,
			&ua.Checkins, &ua.ConsoleWrites, &ua.GameActive, &ua.GameRounds); err != nil {
		t.Fatalf("read user row: %v", err)
	}
	if ua.ProductActive != 1 || ua.APIRequests != 3 || ua.Uncached != 300 ||
		ua.CacheWrite != 60 || ua.CacheRead != 90 || ua.Output != 150 ||
		ua.Checkins != 0 || ua.ConsoleWrites != 0 || ua.GameActive != 0 || ua.GameRounds != 0 {
		t.Fatalf("user aggregate = %+v", ua)
	}

	var sa siteActivityRow
	if err := st.DB().QueryRow(`SELECT product_active, api_requests,
		uncached_input_tokens, cache_write_input_tokens, cache_read_input_tokens, output_tokens,
		checkins, console_writes, game_active, game_rounds, distinct_product_users
		FROM site_activity_daily`).
		Scan(&sa.ProductActive, &sa.APIRequests, &sa.Uncached, &sa.CacheWrite, &sa.CacheRead, &sa.Output,
			&sa.Checkins, &sa.ConsoleWrites, &sa.GameActive, &sa.GameRounds, &sa.Distinct); err != nil {
		t.Fatalf("read site row: %v", err)
	}
	if sa.ProductActive != 1 || sa.APIRequests != 3 || sa.Uncached != 300 ||
		sa.CacheWrite != 60 || sa.CacheRead != 90 || sa.Output != 150 ||
		sa.ConsoleWrites != 1 || sa.Distinct != 2 || sa.GameActive != 0 || sa.GameRounds != 0 {
		t.Fatalf("site aggregate = %+v", sa)
	}
}

type userActivityRow struct {
	ProductActive, APIRequests              int64
	Uncached, CacheWrite, CacheRead, Output int64
	Checkins, ConsoleWrites                 int64
	GameActive, GameRounds                  int64
}

type siteActivityRow struct {
	ProductActive, APIRequests              int64
	Uncached, CacheWrite, CacheRead, Output int64
	Checkins, ConsoleWrites                 int64
	GameActive, GameRounds                  int64
	Distinct                                int64
}

func TestActivityConcurrentFirstActiveDistinctCount(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "activity-concurrent.db"))
	defer st.Close()
	setOffset(t, st, 0) // explicit UTC

	const users = 12
	const repeats = 5
	ids := make([]int64, users)
	for i := range ids {
		ids[i] = seedUserRaw(t, st, fmt.Sprintf("conc-%02d", i))
	}
	at := time.Now().UTC()
	var wg sync.WaitGroup
	errs := make(chan error, users*repeats)
	for _, id := range ids {
		for j := 0; j < repeats; j++ {
			wg.Add(1)
			go func(uid int64) {
				defer wg.Done()
				errs <- st.RecordActivity(context.Background(), uid, at, ActivityDelta{APIRequests: 1})
			}(id)
		}
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent RecordActivity: %v", err)
		}
	}
	var distinct int64
	if err := st.DB().QueryRow(`SELECT distinct_product_users FROM site_activity_daily`).Scan(&distinct); err != nil {
		t.Fatalf("read distinct: %v", err)
	}
	if distinct != users {
		t.Fatalf("distinct_product_users=%d, want %d (each user counted once)", distinct, users)
	}
	var requests int64
	if err := st.DB().QueryRow(`SELECT api_requests FROM site_activity_daily`).Scan(&requests); err != nil {
		t.Fatalf("read requests: %v", err)
	}
	if requests != users*repeats {
		t.Fatalf("api_requests=%d, want %d (every event counted)", requests, users*repeats)
	}
}

func TestRequestActivityDuplicateHookAtMostOnce(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "activity-dup.db"))
	defer st.Close()
	setOffset(t, st, 330)
	uid := seedUserRaw(t, st, "dup-user")

	input := RequestLogInput{
		AttemptID: "activity-dup-attempt", UserID: uid, Model: "p/m",
		StatusCode: 200, StartedAt: time.Unix(1700000000, 0).UTC(), CompletedAt: time.Unix(1700000001, 0).UTC(),
		UncachedInputTokens: 11, OutputTokens: 7,
		Activity: &ActivityDelta{APIRequests: 1, UncachedInputTokens: 11, OutputTokens: 7},
	}
	// The same committed hook delivered twice must not double-count anywhere.
	if err := st.RecordRequest(context.Background(), input); err != nil {
		t.Fatalf("RecordRequest #1: %v", err)
	}
	if err := st.RecordRequest(context.Background(), input); err != nil {
		t.Fatalf("RecordRequest #2: %v", err)
	}
	var requests, uncached, output int64
	if err := st.DB().QueryRow(`SELECT api_requests, uncached_input_tokens, output_tokens FROM user_activity_daily WHERE user_id=?`, uid).
		Scan(&requests, &uncached, &output); err != nil {
		t.Fatalf("read user activity: %v", err)
	}
	if requests != 1 || uncached != 11 || output != 7 {
		t.Fatalf("duplicate hook double-counted: requests=%d uncached=%d output=%d", requests, uncached, output)
	}
	var siteRequests, distinct int64
	if err := st.DB().QueryRow(`SELECT api_requests, distinct_product_users FROM site_activity_daily`).Scan(&siteRequests, &distinct); err != nil {
		t.Fatalf("read site activity: %v", err)
	}
	if siteRequests != 1 || distinct != 1 {
		t.Fatalf("site duplicate: requests=%d distinct=%d", siteRequests, distinct)
	}
}

func TestDeleteRecalculatesSiteActivityRows(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "activity-delete.db"))
	defer st.Close()
	setOffset(t, st, 330)

	keep1 := seedUserRaw(t, st, "del-keep1")
	keep2 := seedUserRaw(t, st, "del-keep2")
	gone := seedUserRaw(t, st, "del-gone")
	at := time.Now().UTC()

	if err := st.RecordActivity(context.Background(), keep1, at, ActivityDelta{APIRequests: 1, UncachedInputTokens: 10, OutputTokens: 5}); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordActivity(context.Background(), keep2, at, ActivityDelta{APIRequests: 1, CacheReadInputTokens: 3}); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordActivity(context.Background(), gone, at, ActivityDelta{APIRequests: 1, UncachedInputTokens: 999, OutputTokens: 999}); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteUserAccount(context.Background(), gone); err != nil {
		t.Fatalf("DeleteUserAccount: %v", err)
	}
	assertUserGone(t, st, gone)

	var sa siteActivityRow
	if err := st.DB().QueryRow(`SELECT product_active, api_requests,
		uncached_input_tokens, cache_write_input_tokens, cache_read_input_tokens, output_tokens,
		distinct_product_users FROM site_activity_daily`).
		Scan(&sa.ProductActive, &sa.APIRequests, &sa.Uncached, &sa.CacheWrite, &sa.CacheRead, &sa.Output, &sa.Distinct); err != nil {
		t.Fatalf("read site row after delete: %v", err)
	}
	if sa.APIRequests != 2 || sa.Uncached != 10 || sa.Output != 5 || sa.CacheRead != 3 || sa.Distinct != 2 {
		t.Fatalf("site row keeps deleted user's contribution: %+v", sa)
	}

	// Deleting every contributor removes the site day entirely (no ghost row).
	if err := st.DeleteUserAccount(context.Background(), keep1); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteUserAccount(context.Background(), keep2); err != nil {
		t.Fatal(err)
	}
	if got := countRows(t, st, `SELECT COUNT(*) FROM site_activity_daily`); got != 0 {
		t.Fatalf("site rows after deleting all contributors=%d, want 0", got)
	}
	if got := countRows(t, st, `SELECT COUNT(*) FROM user_activity_daily`); got != 0 {
		t.Fatalf("user activity rows survived cascade=%d, want 0", got)
	}
}

func TestDeleteVsLateActivityLinearized(t *testing.T) {
	for attempt := 0; attempt < 20; attempt++ {
		st := openTestStore(t, filepath.Join(t.TempDir(), fmt.Sprintf("activity-late-%02d.db", attempt)))
		setOffset(t, st, 330)
		uid := seedUserRaw(t, st, fmt.Sprintf("late-%02d", attempt))
		at := time.Now().UTC()
		if err := st.RecordActivity(context.Background(), uid, at, ActivityDelta{APIRequests: 1}); err != nil {
			t.Fatal(err)
		}
		delErr := st.DeleteUserAccount(context.Background(), uid)
		lateErr := st.RecordActivity(context.Background(), uid, at, ActivityDelta{APIRequests: 1, UncachedInputTokens: 42})
		if delErr != nil {
			t.Fatalf("attempt %d: delete: %v", attempt, delErr)
		}
		if lateErr != nil {
			t.Fatalf("attempt %d: late write must be a silent no-op, got %v", attempt, lateErr)
		}
		assertUserGone(t, st, uid)
		if got := countRows(t, st, `SELECT COUNT(*) FROM user_activity_daily WHERE user_id=?`, uid); got != 0 {
			t.Fatalf("attempt %d: late write resurrected user activity rows (%d)", attempt, got)
		}
		var uncapped int64
		if err := st.DB().QueryRow(`SELECT COALESCE(SUM(uncached_input_tokens),0) FROM site_activity_daily`).Scan(&uncapped); err != nil {
			t.Fatal(err)
		}
		if uncapped != 0 {
			t.Fatalf("attempt %d: site rollup kept deleted user contribution (%d)", attempt, uncapped)
		}
		st.Close()
	}
}

func TestActivityRetentionCleanupBoundedBatches(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "activity-retention.db"))
	defer st.Close()
	setOffset(t, st, 0)

	now := time.Now().UTC().Unix()
	oldDay := SiteDayKey(now-(ActivityRetentionDays+5)*86400, 0)
	newDay := SiteDayKey(now, 0)
	uid := seedUserRaw(t, st, "retention-user")
	for _, day := range []int64{oldDay - 86400, oldDay, newDay} {
		if _, err := st.DB().Exec(`INSERT INTO user_activity_daily
			(day, user_id, product_active, api_requests, updated_at) VALUES (?, ?, 1, 1, ?)`,
			day, uid, now); err != nil {
			t.Fatal(err)
		}
		if _, err := st.DB().Exec(`INSERT INTO site_activity_daily
			(day, product_active, api_requests, distinct_product_users, updated_at) VALUES (?, 1, 1, 1, ?)`,
			day, now); err != nil {
			t.Fatal(err)
		}
	}

	cutoff, err := st.ActivityRetentionCutoffDay(now)
	if err != nil {
		t.Fatal(err)
	}
	deleted, early, err := st.cleanupActivityBeforeBounded(context.Background(), cutoff, 1, 2)
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if deleted == 0 {
		t.Fatalf("deleted=0, want aged rows")
	}
	remainingOld := countRows(t, st, `SELECT COUNT(*) FROM user_activity_daily WHERE day < ?`, cutoff)
	if remainingOld != 0 {
		t.Fatalf("aged user rows remain: %d", remainingOld)
	}
	keptNew := countRows(t, st, `SELECT COUNT(*) FROM user_activity_daily WHERE day >= ?`, cutoff)
	if keptNew != 1 {
		t.Fatalf("fresh rows must survive, got %d", keptNew)
	}
	// Site rows whose last per-user row was cleaned go with them; the fresh
	// day's site row survives.
	if got := countRows(t, st, `SELECT COUNT(*) FROM site_activity_daily WHERE day < ?`, cutoff); got != 0 {
		t.Fatalf("aged site rows remain: %d", got)
	}
	if got := countRows(t, st, `SELECT COUNT(*) FROM site_activity_daily WHERE day >= ?`, cutoff); got != 1 {
		t.Fatalf("fresh site row missing: %d", got)
	}
	_ = early // bounded runs may stop early and resume on the next sweep

	// Idempotent rerun converges to the same state.
	if _, _, err := st.CleanupActivityBefore(context.Background(), cutoff); err != nil {
		t.Fatalf("rerun: %v", err)
	}
	if got := countRows(t, st, `SELECT COUNT(*) FROM user_activity_daily WHERE day < ?`, cutoff); got != 0 {
		t.Fatalf("rerun left aged rows: %d", got)
	}
}

func TestActivityUnsetTimezoneNoOp(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "activity-unset.db"))
	defer st.Close()
	// No offset configured: writers must not generate day keys.
	uid := seedUserRaw(t, st, "unset-user")

	if err := st.RecordActivity(context.Background(), uid, time.Now().UTC(), ActivityDelta{APIRequests: 1}); err != nil {
		t.Fatalf("RecordActivity with unset timezone must be a successful no-op, got %v", err)
	}
	if err := st.RecordRequest(context.Background(), RequestLogInput{
		AttemptID: "unset-attempt", UserID: uid,
		StartedAt: time.Unix(1700000000, 0).UTC(), CompletedAt: time.Unix(1700000001, 0).UTC(),
		Activity: &ActivityDelta{APIRequests: 1},
	}); err != nil {
		t.Fatalf("RecordRequest with unset timezone: %v", err)
	}
	if err := st.SetSiteConfigValueWithActivity(context.Background(), "site_name", "x", uid, time.Now().UTC()); err != nil {
		t.Fatalf("config write with unset timezone: %v", err)
	}
	if got := countRows(t, st, `SELECT COUNT(*) FROM user_activity_daily`); got != 0 {
		t.Fatalf("user activity rows written without configured timezone: %d", got)
	}
	if got := countRows(t, st, `SELECT COUNT(*) FROM site_activity_daily`); got != 0 {
		t.Fatalf("site activity rows written without configured timezone: %d", got)
	}
	// The business writes still landed.
	if got := countRows(t, st, `SELECT COUNT(*) FROM request_logs`); got != 1 {
		t.Fatalf("request log rows=%d, want 1", got)
	}
	value, err := st.GetSiteConfigValue("site_name")
	if err != nil || value != "x" {
		t.Fatalf("config value=(%q, %v), want (\"x\", nil)", value, err)
	}
}

func TestConsoleWriteRecordsActivity(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "activity-console.db"))
	defer st.Close()
	setOffset(t, st, -390) // -06:30
	admin := seedUserRaw(t, st, "console-admin")

	ctx := context.Background()
	if err := st.SetSiteConfigValueWithActivity(ctx, "site_name", "first", admin, time.Now().UTC()); err != nil {
		t.Fatalf("config write: %v", err)
	}
	if err := st.SetSiteConfigValueWithActivity(ctx, "site_name", "second", admin, time.Now().UTC()); err != nil {
		t.Fatalf("second config write: %v", err)
	}
	var writes, distinct int64
	if err := st.DB().QueryRow(`SELECT console_writes, distinct_product_users FROM site_activity_daily`).Scan(&writes, &distinct); err != nil {
		t.Fatalf("read site row: %v", err)
	}
	if writes != 2 || distinct != 1 {
		t.Fatalf("console_writes=%d distinct=%d, want 2/1", writes, distinct)
	}
	// The first temporal-data writer durably freezes the site timezone offset
	// (same transaction as the business insert): console activity counts, so
	// retention cleanup can never make the offset mutable again.
	if got := countRows(t, st, `SELECT COUNT(*) FROM site_config WHERE key=?`, timezoneLockKey); got != 1 {
		t.Fatalf("timezone lock rows = %d, want 1 after the first activity row", got)
	}
}

func TestActivityInvalidDeltaRejected(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "activity-invalid.db"))
	defer st.Close()
	setOffset(t, st, 0)
	uid := seedUserRaw(t, st, "invalid-user")
	ctx := context.Background()
	if err := st.RecordActivity(ctx, uid, time.Now().UTC(), ActivityDelta{}); err == nil {
		t.Fatal("empty delta accepted")
	}
	if err := st.RecordActivity(ctx, uid, time.Now().UTC(), ActivityDelta{APIRequests: -1}); err == nil {
		t.Fatal("negative delta accepted")
	}
	if err := st.RecordActivity(ctx, uid, time.Now().UTC(), ActivityDelta{OutputTokens: MaxTokenDelta + 1}); err == nil {
		t.Fatal("unbounded token delta accepted")
	}
}
