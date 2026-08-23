package db

// Level system tests: threshold chain validation (same-transaction),
// authoritative effective-level resolution with the lazy CAS auto promotion
// and the no-downgrade high-water mark, the manual override/reset, the
// administrator exclusion and concurrent promotion convergence.

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func newLevelsTestStore(t *testing.T) *Store {
	t.Helper()
	st := openTestStore(t, filepath.Join(t.TempDir(), "levels.db"))
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// setThreshold wraps Store.SetLevelThresholdMilli with the environment
// administrator as the actor; the actor/time pair mirrors the admin PATCH
// path (the console-write activity half self-skips while the site timezone
// is unset, which is the state of every plain level test store).
func setThreshold(t *testing.T, st *Store, key string, value int64) error {
	t.Helper()
	admin, err := st.EnsureAdminUser("root")
	if err != nil {
		t.Fatalf("ensure admin: %v", err)
	}
	return st.SetLevelThresholdMilli(context.Background(), key, value, admin.ID, time.Now())
}

func setDonationCredit(t *testing.T, st *Store, uid int64, value int64) {
	t.Helper()
	if _, err := st.DB().Exec(`UPDATE users SET donation_credit=? WHERE id=?`, value, uid); err != nil {
		t.Fatalf("set donation credit: %v", err)
	}
}

func autoLevelOf(t *testing.T, st *Store, uid int64) int {
	t.Helper()
	var auto int
	if err := st.DB().QueryRow(`SELECT auto_level FROM users WHERE id=?`, uid).Scan(&auto); err != nil {
		t.Fatalf("read auto_level: %v", err)
	}
	return auto
}

func storedLevelIsNull(t *testing.T, st *Store, uid int64) bool {
	t.Helper()
	var isNull bool
	if err := st.DB().QueryRow(`SELECT level IS NULL FROM users WHERE id=?`, uid).Scan(&isNull); err != nil {
		t.Fatalf("read level null flag: %v", err)
	}
	return isNull
}

// TestLevelThresholdChainValidation pins the enabled-chain rule: enabled
// thresholds must be strictly increasing in level order; a disabled (0)
// middle level is allowed and produces a skip; a rejected combination leaves
// nothing written (no half-chain).
func TestLevelThresholdChainValidation(t *testing.T) {
	st := newLevelsTestStore(t)

	// All disabled is valid (the default state).
	if err := setThreshold(t, st, LevelThreshold2Key, 0); err != nil {
		t.Fatalf("all-disabled t2: %v", err)
	}
	// Skip: t2 disabled, t3+t4 enabled increasing.
	if err := setThreshold(t, st, LevelThreshold3Key, 100); err != nil {
		t.Fatalf("skip t3: %v", err)
	}
	if err := setThreshold(t, st, LevelThreshold4Key, 500); err != nil {
		t.Fatalf("skip t4: %v", err)
	}
	snapshot, err := st.LevelThresholdsSnapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if snapshot != (LevelThresholds{Level2: 0, Level3: 100, Level4: 500}) {
		t.Fatalf("snapshot = %+v", snapshot)
	}

	// Equal enabled thresholds are not strictly increasing.
	if err := setThreshold(t, st, LevelThreshold2Key, 100); err == nil {
		t.Fatalf("t2 == enabled t3 must be rejected")
	}
	// Decreasing chain is rejected...
	if err := setThreshold(t, st, LevelThreshold2Key, 600); err == nil {
		t.Fatalf("t2 > t4 must be rejected")
	}
	// ...and the rejections left no half-write: t2 is still disabled.
	snapshot, err = st.LevelThresholdsSnapshot()
	if err != nil {
		t.Fatalf("snapshot after rejects: %v", err)
	}
	if snapshot.Level2 != 0 {
		t.Fatalf("rejected write leaked: snapshot = %+v", snapshot)
	}

	// Unknown key and negative value are refused outright.
	if err := setThreshold(t, st, "level_threshold_9_milli", 1); !errors.Is(err, ErrConflict) {
		t.Fatalf("unknown key err = %v, want ErrConflict", err)
	}
	if err := setThreshold(t, st, LevelThreshold2Key, -1); !errors.Is(err, ErrConflict) {
		t.Fatalf("negative err = %v, want ErrConflict", err)
	}

	// An unset key, an explicitly stored "0", and a corrupted row all read as
	// disabled — identical to each other and never a promotion.
	fresh := newLevelsTestStore(t)
	unset, err := fresh.LevelThresholdsSnapshot()
	if err != nil {
		t.Fatalf("unset snapshot: %v", err)
	}
	if unset != (LevelThresholds{}) {
		t.Fatalf("unset snapshot = %+v, want all-zero", unset)
	}
	if err := fresh.SetSiteConfigValue(LevelThreshold2Key, "0"); err != nil {
		t.Fatalf("raw zero: %v", err)
	}
	if err := fresh.SetSiteConfigValue(LevelThreshold3Key, "not-a-number"); err != nil {
		t.Fatalf("raw corrupt: %v", err)
	}
	explicit, err := fresh.LevelThresholdsSnapshot()
	if err != nil {
		t.Fatalf("explicit snapshot: %v", err)
	}
	if explicit != (LevelThresholds{}) {
		t.Fatalf("explicit-zero/corrupt snapshot = %+v, want all-zero", explicit)
	}
}

// TestResolveEffectiveLevelLazyPromotionAndNoDowngrade covers the automatic
// path: thresholds drive a lazy persisted promotion; raising a threshold later
// or negatively correcting donation_credit never lowers the high-water mark.
func TestResolveEffectiveLevelLazyPromotionAndNoDowngrade(t *testing.T) {
	st := newLevelsTestStore(t)
	ctx := context.Background()
	uid := seedUserRaw(t, st, "discord-auto")

	// No thresholds configured: every user stays at 1.
	level, err := st.ResolveEffectiveLevel(ctx, uid)
	if err != nil || level != 1 {
		t.Fatalf("resolve with no thresholds = (%d, %v), want (1, nil)", level, err)
	}

	// Enable t2: below the threshold stays 1, at/above promotes to 2 and
	// persists the high-water mark.
	if err := setThreshold(t, st, LevelThreshold2Key, 100); err != nil {
		t.Fatalf("set t2: %v", err)
	}
	setDonationCredit(t, st, uid, 50)
	if level, _ := st.ResolveEffectiveLevel(ctx, uid); level != 1 {
		t.Fatalf("below threshold level = %d, want 1", level)
	}
	setDonationCredit(t, st, uid, 100)
	if level, _ := st.ResolveEffectiveLevel(ctx, uid); level != 2 {
		t.Fatalf("at threshold level = %d, want 2", level)
	}
	if got := autoLevelOf(t, st, uid); got != 2 {
		t.Fatalf("persisted auto_level = %d, want 2", got)
	}

	// Raising the threshold afterwards does not demote (high-water mark).
	if err := setThreshold(t, st, LevelThreshold2Key, 1000); err != nil {
		t.Fatalf("raise t2: %v", err)
	}
	if level, _ := st.ResolveEffectiveLevel(ctx, uid); level != 2 {
		t.Fatalf("after raise level = %d, want 2 (no downgrade)", level)
	}
	// A negative donation_credit correction does not demote either.
	setDonationCredit(t, st, uid, 10)
	if level, _ := st.ResolveEffectiveLevel(ctx, uid); level != 2 {
		t.Fatalf("after correction level = %d, want 2 (no downgrade)", level)
	}

	// Skip promotion: t3 disabled, t4 enabled — a qualifying user jumps 1→4.
	uid2 := seedUserRaw(t, st, "discord-skip")
	if err := setThreshold(t, st, LevelThreshold4Key, 100000); err != nil {
		t.Fatalf("set t4: %v", err)
	}
	setDonationCredit(t, st, uid2, 200000)
	if level, _ := st.ResolveEffectiveLevel(ctx, uid2); level != 4 {
		t.Fatalf("skip level = %d, want 4", level)
	}
	if got := autoLevelOf(t, st, uid2); got != 4 {
		t.Fatalf("skip persisted auto_level = %d, want 4", got)
	}
	// Automatic levels stop at 4 even with an enormous balance: level 5 is
	// manual-only by construction (there is no threshold key for it).
	if level, _ := st.ResolveEffectiveLevel(ctx, uid2); level > MaxAutoLevel {
		t.Fatalf("auto level %d exceeds MaxAutoLevel", level)
	}
}

// TestResolveEffectiveLevelManualOverride pins the manual path: a non-NULL
// override wins, does not disturb the auto high-water mark, can be reset to
// NULL, and never applies to the administrator row.
func TestResolveEffectiveLevelManualOverride(t *testing.T) {
	st := newLevelsTestStore(t)
	ctx := context.Background()
	uid := seedUserRaw(t, st, "discord-manual")

	// Promote the auto level to 2 first so the reset has something to return to.
	if err := setThreshold(t, st, LevelThreshold2Key, 100); err != nil {
		t.Fatalf("set t2: %v", err)
	}
	setDonationCredit(t, st, uid, 500)
	if level, _ := st.ResolveEffectiveLevel(ctx, uid); level != 2 {
		t.Fatalf("auto level = %d, want 2", level)
	}

	// Manual override to every legal value wins over the auto level...
	for _, manual := range []int{1, 2, 3, 4, 5} {
		updated, err := st.SetUserManualLevel(uid, &manual)
		if err != nil {
			t.Fatalf("set manual %d: %v", manual, err)
		}
		if updated.Level == nil || *updated.Level != manual {
			t.Fatalf("stored manual = %+v, want %d", updated.Level, manual)
		}
		if level, _ := st.ResolveEffectiveLevel(ctx, uid); level != manual {
			t.Fatalf("effective with manual %d = %d", manual, level)
		}
		// ...and leaves the auto high-water mark untouched.
		if got := autoLevelOf(t, st, uid); got != 2 {
			t.Fatalf("manual %d disturbed auto_level = %d, want 2", manual, got)
		}
	}

	// A manual override also suppresses lazy auto promotions while in force.
	if err := setThreshold(t, st, LevelThreshold4Key, 200); err != nil {
		t.Fatalf("set t4: %v", err)
	}
	five := 5
	if _, err := st.SetUserManualLevel(uid, &five); err != nil {
		t.Fatalf("set manual 5: %v", err)
	}
	if level, _ := st.ResolveEffectiveLevel(ctx, uid); level != 5 {
		t.Fatalf("manual 5 effective = %d", level)
	}
	if got := autoLevelOf(t, st, uid); got != 2 {
		t.Fatalf("manual 5 disturbed auto_level = %d, want 2", got)
	}

	// Reset to NULL: the persisted high-water mark becomes effective again...
	if _, err := st.SetUserManualLevel(uid, nil); err != nil {
		t.Fatalf("reset manual: %v", err)
	}
	if !storedLevelIsNull(t, st, uid) {
		t.Fatalf("reset left a non-NULL level")
	}
	// ...and the next resolution re-evaluates the thresholds (donation=500,
	// t4=200 enabled) so the lazy promotion resumes from the mark.
	if level, _ := st.ResolveEffectiveLevel(ctx, uid); level != 4 {
		t.Fatalf("after reset effective = %d, want 4 (resumed promotion)", level)
	}

	// Out-of-range overrides are refused.
	for _, bad := range []int{0, 6, -1} {
		if _, err := st.SetUserManualLevel(uid, &bad); !errors.Is(err, ErrConflict) {
			t.Fatalf("manual %d err = %v, want ErrConflict", bad, err)
		}
	}
	// A missing user is not found; the administrator row is protected.
	if _, err := st.SetUserManualLevel(999999, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing user err = %v, want ErrNotFound", err)
	}
	admin, err := st.EnsureAdminUser("root")
	if err != nil {
		t.Fatalf("ensure admin: %v", err)
	}
	if _, err := st.SetUserManualLevel(admin.ID, &five); !errors.Is(err, ErrAdminProtected) {
		t.Fatalf("admin manual err = %v, want ErrAdminProtected", err)
	}
	if _, err := st.ResolveEffectiveLevel(ctx, admin.ID); !errors.Is(err, ErrLevelAdminExcluded) {
		t.Fatalf("admin resolve err = %v, want ErrLevelAdminExcluded", err)
	}

	// Level and AutoLevel are part of the standard user projection (scan
	// synchronization): the refreshed row carries both.
	fresh, err := st.GetUserByID(uid)
	if err != nil || fresh == nil {
		t.Fatalf("reload user: %v", err)
	}
	if fresh.Level != nil || fresh.AutoLevel != 4 {
		t.Fatalf("projection = (level %+v, auto %d), want (nil, 4)", fresh.Level, fresh.AutoLevel)
	}
}

// TestResolveEffectiveLevelConcurrentConvergence runs N concurrent
// resolutions of the same under-promoted user and asserts they all converge
// on the target level with exactly one persisted high-water value (no lost or
// duplicated promotion). The store's single connection serializes the
// conditional UPDATEs; the CAS predicate is the correctness core.
func TestResolveEffectiveLevelConcurrentConvergence(t *testing.T) {
	st := newLevelsTestStore(t)
	ctx := context.Background()
	uid := seedUserRaw(t, st, "discord-concurrent")
	if err := setThreshold(t, st, LevelThreshold2Key, 100); err != nil {
		t.Fatalf("set t2: %v", err)
	}
	if err := setThreshold(t, st, LevelThreshold4Key, 300); err != nil {
		t.Fatalf("set t4: %v", err)
	}
	setDonationCredit(t, st, uid, 1000) // qualifies for 4

	const workers = 16
	results := make([]int, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(slot int) {
			defer wg.Done()
			level, err := st.ResolveEffectiveLevel(ctx, uid)
			if err != nil {
				t.Errorf("worker %d: %v", slot, err)
				return
			}
			results[slot] = level
		}(i)
	}
	wg.Wait()
	for i, level := range results {
		if level != 4 {
			t.Fatalf("worker %d resolved %d, want 4 (converged target)", i, level)
		}
	}
	if got := autoLevelOf(t, st, uid); got != 4 {
		t.Fatalf("persisted auto_level = %d, want exactly 4", got)
	}
}

// TestLevelThresholdWriteRecordsConsoleActivity pins the console-write
// semantics of the threshold repository path: once the site timezone is
// configured, a threshold write records the administrator's console write as
// product activity in the SAME transaction, exactly like the generic
// site-config path (frozen §2.5). A rejected chain writes no activity either.
func TestLevelThresholdWriteRecordsConsoleActivity(t *testing.T) {
	st := newLevelsTestStore(t)
	admin, err := st.EnsureAdminUser("root")
	if err != nil {
		t.Fatalf("ensure admin: %v", err)
	}
	// Explicit UTC: no checkin/activity data exists yet, so the freeze rule
	// allows the first configuration.
	if err := st.SetSiteTimezoneOffsetMinutes(0); err != nil {
		t.Fatalf("set timezone: %v", err)
	}

	if err := st.SetLevelThresholdMilli(context.Background(), LevelThreshold3Key, 100, admin.ID, time.Now()); err != nil {
		t.Fatalf("set t3: %v", err)
	}
	var consoleWrites int
	var productActive bool
	if err := st.DB().QueryRow(
		`SELECT console_writes, product_active FROM user_activity_daily WHERE user_id=?`, admin.ID,
	).Scan(&consoleWrites, &productActive); err != nil {
		t.Fatalf("read activity: %v", err)
	}
	if consoleWrites != 1 || !productActive {
		t.Fatalf("activity = (%d, %v), want (1, true)", consoleWrites, productActive)
	}

	// A rejected chain writes neither the value nor an activity row.
	before := consoleWrites
	if err := st.SetLevelThresholdMilli(context.Background(), LevelThreshold2Key, 500, admin.ID, time.Now()); !errors.Is(err, ErrConflict) {
		t.Fatalf("decreasing chain err = %v, want ErrConflict", err)
	}
	if err := st.DB().QueryRow(
		`SELECT console_writes FROM user_activity_daily WHERE user_id=?`, admin.ID,
	).Scan(&consoleWrites); err != nil || consoleWrites != before {
		t.Fatalf("rejected write activity = (%d, %v), want unchanged %d", consoleWrites, err, before)
	}
}

// TestLevelCapabilityConstants pins the capability mapping and helper: the
// constants are the frozen thresholds and EffectiveLevelAtLeast is the only
// sanctioned server-side comparison.
func TestLevelCapabilityConstants(t *testing.T) {
	if LevelBanner != 2 || LevelCheckinBypass != 3 || LevelSteward != 5 {
		t.Fatalf("capability constants = (%d, %d, %d)", LevelBanner, LevelCheckinBypass, LevelSteward)
	}
	if !EffectiveLevelAtLeast(5, LevelSteward) || EffectiveLevelAtLeast(4, LevelSteward) {
		t.Fatalf("steward helper boundary is wrong")
	}
	if !EffectiveLevelAtLeast(3, LevelCheckinBypass) || EffectiveLevelAtLeast(2, LevelCheckinBypass) {
		t.Fatalf("checkin helper boundary is wrong")
	}
	if !EffectiveLevelAtLeast(2, LevelBanner) || EffectiveLevelAtLeast(1, LevelBanner) {
		t.Fatalf("banner helper boundary is wrong")
	}
}
