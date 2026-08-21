package db

// Check-in repository tests: the unset-vs-explicit-UTC distinction, the
// half-hour timezone day boundary, the mode/level/cap admission matrix, the
// same-instant UNIQUE race (exactly one winner, one ledger row, one activity
// contribution), the random-award distribution, the durable timezone freeze,
// the read-only status projection (no lazy level promotion), corrupt
// configuration fail-closed, and the export projection.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func newCheckinTestStore(t *testing.T) *Store {
	t.Helper()
	st := openTestStore(t, filepath.Join(t.TempDir(), "checkins.db"))
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func newCheckinUser(t *testing.T, st *Store, name string) *User {
	t.Helper()
	user, err := st.CreateUser("discord-"+name, name, "")
	if err != nil {
		t.Fatalf("CreateUser(%s): %v", name, err)
	}
	return user
}

// setCheckinConfig writes raw site_config rows the way a manually operated or
// corrupted database could hold them; legitimate writes go through the
// validated administrator path.
func setCheckinConfig(t *testing.T, st *Store, key, value string) {
	t.Helper()
	if _, err := st.DB().Exec(`INSERT INTO site_config (key, value, updated_at) VALUES (?, ?, 0)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value); err != nil {
		t.Fatalf("set site_config %s: %v", key, err)
	}
}

func enableCheckinAt(t *testing.T, st *Store, mode string, offsetMinutes int) {
	t.Helper()
	if err := st.SetSiteTimezoneOffsetMinutes(offsetMinutes); err != nil {
		t.Fatalf("set timezone %d: %v", offsetMinutes, err)
	}
	setCheckinConfig(t, st, CheckinModeKey, mode)
}

func setCheckinCredits(t *testing.T, st *Store, uid, value int64) {
	t.Helper()
	if _, err := st.DB().Exec(`UPDATE users SET credits=? WHERE id=?`, value, uid); err != nil {
		t.Fatalf("set credits: %v", err)
	}
}

func setCheckinManualLevel(t *testing.T, st *Store, uid int64, level int) {
	t.Helper()
	value := level
	if _, err := st.DB().Exec(`UPDATE users SET level=? WHERE id=?`, value, uid); err != nil {
		t.Fatalf("set manual level: %v", err)
	}
}

func countCheckinRows(t *testing.T, st *Store, query string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := st.DB().QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", query, err)
	}
	return n
}

// TestCheckinUnsetTimezoneIndistinguishableFromDisabled pins the frozen rule:
// while the site timezone is unset, an enabled mode behaves exactly like a
// disabled one (the same sentinel), and an explicit 0 (UTC) is a distinct,
// fully working configuration.
func TestCheckinUnsetTimezoneIndistinguishableFromDisabled(t *testing.T) {
	st := newCheckinTestStore(t)
	user := newCheckinUser(t, st, "tz")
	now := time.Unix(1700000000, 0).UTC()
	ctx := context.Background()

	// Unset timezone + enabled mode: disabled sentinel, nothing written.
	setCheckinConfig(t, st, CheckinModeKey, CheckinModeEnabled)
	if _, err := st.Checkin(ctx, user.ID, now); !errors.Is(err, ErrCheckinDisabled) {
		t.Fatalf("unset timezone err = %v, want ErrCheckinDisabled", err)
	}
	// Unset timezone + disabled mode: byte-identical sentinel (no distinction).
	setCheckinConfig(t, st, CheckinModeKey, CheckinModeDisabled)
	errDisabled := func() error {
		_, err := st.Checkin(ctx, user.ID, now)
		return err
	}()
	setCheckinConfig(t, st, CheckinModeKey, CheckinModeEnabled)
	errUnset := func() error {
		_, err := st.Checkin(ctx, user.ID, now)
		return err
	}()
	if errDisabled == nil || errUnset == nil || errDisabled.Error() != errUnset.Error() {
		t.Fatalf("unset-timezone error %v differs from disabled error %v", errUnset, errDisabled)
	}
	if n := countCheckinRows(t, st, `SELECT COUNT(*) FROM checkins`); n != 0 {
		t.Fatalf("checkins rows = %d, want 0", n)
	}
	// The status projection reports unavailable without a reason.
	status, err := st.CheckinStatus(ctx, user.ID, now)
	if err != nil || status.Enabled {
		t.Fatalf("status while unset = (%+v, %v), want enabled=false", status, err)
	}

	// Explicit 0 (UTC) is configured: the same configuration now succeeds.
	if err := st.SetSiteTimezoneOffsetMinutes(0); err != nil {
		t.Fatalf("set UTC: %v", err)
	}
	res, err := st.Checkin(ctx, user.ID, now)
	if err != nil {
		t.Fatalf("checkin at explicit UTC: %v", err)
	}
	if res.Day != SiteDayKey(now.Unix(), 0) {
		t.Fatalf("day key = %d, want %d", res.Day, SiteDayKey(now.Unix(), 0))
	}
	status, err = st.CheckinStatus(ctx, user.ID, now)
	if err != nil || !status.Enabled || !status.CheckedInToday {
		t.Fatalf("status after checkin = (%+v, %v)", status, err)
	}
}

// TestCheckinHalfHourBoundary pins the half-hour-offset day semantics: with
// offset +30 the local midnight falls at 23:30 UTC, so 23:15 UTC and 23:45 UTC
// of the same UTC day are DIFFERENT site-local days, while 23:45 UTC and
// 00:15 UTC of the next day are the SAME local day (a whole-hour offset would
// not produce this split).
func TestCheckinHalfHourBoundary(t *testing.T) {
	st := newCheckinTestStore(t)
	user := newCheckinUser(t, st, "halfhour")
	enableCheckinAt(t, st, CheckinModeEnabled, 30)
	ctx := context.Background()

	day := int64(1700000000/86400) * 86400 // a UTC midnight
	// Local midnight with +30 is 23:30 UTC of the previous UTC day.
	at2315 := time.Unix(day-45*60, 0).UTC() // 23:15 UTC of UTC-day D-1
	at2345 := time.Unix(day-15*60, 0).UTC() // 23:45 UTC of UTC-day D-1
	at0015 := time.Unix(day+15*60, 0).UTC() // 00:15 UTC of UTC-day D

	key2315 := SiteDayKey(at2315.Unix(), 30)
	key2345 := SiteDayKey(at2345.Unix(), 30)
	key0015 := SiteDayKey(at0015.Unix(), 30)
	if key2315 == key2345 {
		t.Fatalf("23:15 and 23:45 UTC share day key %d under +30", key2315)
	}
	if key2345 != key0015 {
		t.Fatalf("23:45 UTC and next-day 00:15 UTC differ (%d vs %d) under +30", key2345, key0015)
	}
	if want := day - 30*60; key2345 != want {
		t.Fatalf("day key = %d, want local midnight %d", key2345, want)
	}

	// 23:15 UTC lands on the earlier local day: first success.
	res, err := st.Checkin(ctx, user.ID, at2315)
	if err != nil || res.Day != key2315 {
		t.Fatalf("checkin 23:15 = (%+v, %v)", res, err)
	}
	// 23:45 UTC crosses the half-hour boundary into the next local day: the
	// second check-in of that UTC day succeeds.
	res, err = st.Checkin(ctx, user.ID, at2345)
	if err != nil || res.Day != key2345 {
		t.Fatalf("checkin 23:45 = (%+v, %v)", res, err)
	}
	// 00:15 UTC of the next UTC day is the SAME local day: refused, day
	// already consumed by the 23:45 check-in.
	if _, err := st.Checkin(ctx, user.ID, at0015); !errors.Is(err, ErrAlreadyCheckedIn) {
		t.Fatalf("checkin 00:15 err = %v, want ErrAlreadyCheckedIn", err)
	}
	if n := countCheckinRows(t, st, `SELECT COUNT(*) FROM checkins WHERE user_id=?`, user.ID); n != 2 {
		t.Fatalf("checkins rows = %d, want 2", n)
	}
}

// TestCheckinModeLevelCapMatrix walks the admission matrix: the three modes
// against manual/effective levels and cap configurations, including the
// day-not-consumed guarantee of a cap refusal and the level>=3 bypass. The
// timezone is configured once (the first check-in freezes it for the store's
// lifetime); later cases only flip the mode/cap keys.
func TestCheckinModeLevelCapMatrix(t *testing.T) {
	st := newCheckinTestStore(t)
	ctx := context.Background()
	now := time.Unix(1700000000, 0).UTC()
	if err := st.SetSiteTimezoneOffsetMinutes(0); err != nil {
		t.Fatalf("set UTC: %v", err)
	}

	// Award determinism for the matrix: pin min=max so balances are exact.
	setCheckinAwardPair(t, st, 1000, 1000)
	setMode := func(mode string) {
		t.Helper()
		setCheckinConfig(t, st, CheckinModeKey, mode)
	}
	setCap := func(v string) {
		t.Helper()
		setCheckinConfig(t, st, CreditsCapMilliKey, v)
	}

	mustCheckin := func(t *testing.T, st *Store, uid int64) *CheckinResult {
		t.Helper()
		res, err := st.Checkin(ctx, uid, now)
		if err != nil {
			t.Fatalf("checkin: %v", err)
		}
		return res
	}

	t.Run("DisabledModeRefusesEveryone", func(t *testing.T) {
		setMode(CheckinModeDisabled)
		l1 := newCheckinUser(t, st, "dis1")
		l5 := newCheckinUser(t, st, "dis5")
		setCheckinManualLevel(t, st, l5.ID, 5)
		for _, uid := range []int64{l1.ID, l5.ID} {
			if _, err := st.Checkin(ctx, uid, now); !errors.Is(err, ErrCheckinDisabled) {
				t.Fatalf("disabled mode err = %v, want ErrCheckinDisabled", err)
			}
		}
	})

	t.Run("EnabledCapZeroNeverRejectsEvenNegative", func(t *testing.T) {
		setMode(CheckinModeEnabled)
		setCap("0")
		u := newCheckinUser(t, st, "neg")
		setCheckinCredits(t, st, u.ID, -5_000_000)
		res := mustCheckin(t, st, u.ID)
		if res.CreditsAfter != -5_000_000+1000 {
			t.Fatalf("credits after = %d, want %d", res.CreditsAfter, -5_000_000+1000)
		}
	})

	t.Run("CapRejectsWithoutConsumingDay", func(t *testing.T) {
		setMode(CheckinModeEnabled)
		setCap("100")
		u := newCheckinUser(t, st, "cap")
		setCheckinCredits(t, st, u.ID, 100)
		if _, err := st.Checkin(ctx, u.ID, now); !errors.Is(err, ErrCheckinCapReached) {
			t.Fatalf("cap err = %v, want ErrCheckinCapReached", err)
		}
		if n := countCheckinRows(t, st, `SELECT COUNT(*) FROM checkins WHERE user_id=?`, u.ID); n != 0 {
			t.Fatalf("cap refusal consumed the day: rows=%d", n)
		}
		// The administrator lowers the balance: the SAME day now succeeds.
		setCheckinCredits(t, st, u.ID, 50)
		mustCheckin(t, st, u.ID)
	})

	t.Run("CapRejectsAtAndAbove", func(t *testing.T) {
		setMode(CheckinModeEnabled)
		setCap("100")
		at := newCheckinUser(t, st, "capAt")
		setCheckinCredits(t, st, at.ID, 100)
		if _, err := st.Checkin(ctx, at.ID, now); !errors.Is(err, ErrCheckinCapReached) {
			t.Fatalf("credits==cap err = %v", err)
		}
		above := newCheckinUser(t, st, "capAbove")
		setCheckinCredits(t, st, above.ID, 101)
		if _, err := st.Checkin(ctx, above.ID, now); !errors.Is(err, ErrCheckinCapReached) {
			t.Fatalf("credits>cap err = %v", err)
		}
	})

	t.Run("LevelBypassesCap", func(t *testing.T) {
		setMode(CheckinModeEnabled)
		setCap("100")
		for _, level := range []int{3, 5} {
			u := newCheckinUser(t, st, fmt.Sprintf("bypass%d", level))
			setCheckinManualLevel(t, st, u.ID, level)
			setCheckinCredits(t, st, u.ID, 1_000_000)
			mustCheckin(t, st, u.ID)
		}
		// Level 2 does not bypass.
		u2 := newCheckinUser(t, st, "bypass2")
		setCheckinManualLevel(t, st, u2.ID, 2)
		setCheckinCredits(t, st, u2.ID, 1_000_000)
		if _, err := st.Checkin(ctx, u2.ID, now); !errors.Is(err, ErrCheckinCapReached) {
			t.Fatalf("level 2 err = %v, want ErrCheckinCapReached", err)
		}
	})

	t.Run("LevelGatedDeniesBelow3AllowsAtOrAbove", func(t *testing.T) {
		setMode(CheckinModeLevelGated)
		setCap("0")
		for _, level := range []int{1, 2} {
			u := newCheckinUser(t, st, fmt.Sprintf("gated%d", level))
			setCheckinManualLevel(t, st, u.ID, level)
			if _, err := st.Checkin(ctx, u.ID, now); !errors.Is(err, ErrCheckinDisabled) {
				t.Fatalf("level_gated level %d err = %v, want the disabled sentinel", level, err)
			}
		}
		for _, level := range []int{3, 5} {
			u := newCheckinUser(t, st, fmt.Sprintf("gatedok%d", level))
			setCheckinManualLevel(t, st, u.ID, level)
			mustCheckin(t, st, u.ID)
		}
	})

	t.Run("AwardMayExceedCap", func(t *testing.T) {
		setMode(CheckinModeEnabled)
		setCap("100")
		u := newCheckinUser(t, st, "exceed")
		setCheckinCredits(t, st, u.ID, 0)
		res := mustCheckin(t, st, u.ID)
		if res.CreditsAfter != 1000 || res.CreditsAfter <= 100 {
			t.Fatalf("award truncated at cap: after=%d", res.CreditsAfter)
		}
		// The balance above the cap does not retroactively invalidate the
		// committed check-in (no truncation, no clawback).
		if n := countCheckinRows(t, st, `SELECT COUNT(*) FROM checkins WHERE user_id=? AND award=1000`, u.ID); n != 1 {
			t.Fatalf("stored award rows = %d, want 1", n)
		}
	})
}

// setCheckinAwardPair stores a raw pair directly (tests needing deterministic
// awards; the validated path is covered by the administrator tests).
func setCheckinAwardPair(t *testing.T, st *Store, min, max int64) {
	t.Helper()
	setCheckinConfig(t, st, CheckinAwardMinMilliKey, fmt.Sprintf("%d", min))
	setCheckinConfig(t, st, CheckinAwardMaxMilliKey, fmt.Sprintf("%d", max))
}

// TestCheckinCorruptConfigFailsClosed pins the fail-closed rules for a
// manually corrupted configuration: an unknown mode or an invalid award range
// is the disabled sentinel (never a fabricated draw), while a corrupt cap
// falls back to the documented default like the read projection.
func TestCheckinCorruptConfigFailsClosed(t *testing.T) {
	st := newCheckinTestStore(t)
	ctx := context.Background()
	now := time.Unix(1700000000, 0).UTC()
	if err := st.SetSiteTimezoneOffsetMinutes(0); err != nil {
		t.Fatalf("set UTC: %v", err)
	}

	cases := []struct {
		name string
		fn   func(t *testing.T)
	}{
		{"UnknownMode", func(t *testing.T) {
			setCheckinConfig(t, st, CheckinModeKey, "sometimes")
			u := newCheckinUser(t, st, "corruptMode")
			if _, err := st.Checkin(ctx, u.ID, now); !errors.Is(err, ErrCheckinDisabled) {
				t.Fatalf("unknown mode err = %v", err)
			}
			if s, err := st.CheckinStatus(ctx, u.ID, now); err != nil || s.Enabled {
				t.Fatalf("unknown mode status = (%+v, %v)", s, err)
			}
		}},
		{"MinGreaterThanMax", func(t *testing.T) {
			setCheckinConfig(t, st, CheckinModeKey, CheckinModeEnabled)
			setCheckinAwardPair(t, st, 500, 100)
			u := newCheckinUser(t, st, "corruptRange")
			if _, err := st.Checkin(ctx, u.ID, now); !errors.Is(err, ErrCheckinDisabled) {
				t.Fatalf("min>max err = %v", err)
			}
		}},
		{"NegativeBound", func(t *testing.T) {
			setCheckinConfig(t, st, CheckinModeKey, CheckinModeEnabled)
			setCheckinAwardPair(t, st, 0, 100)
			setCheckinConfig(t, st, CheckinAwardMinMilliKey, "-5")
			u := newCheckinUser(t, st, "corruptNeg")
			if _, err := st.Checkin(ctx, u.ID, now); !errors.Is(err, ErrCheckinDisabled) {
				t.Fatalf("negative min err = %v", err)
			}
		}},
		{"NonCanonicalBound", func(t *testing.T) {
			setCheckinConfig(t, st, CheckinModeKey, CheckinModeEnabled)
			setCheckinAwardPair(t, st, 0, 100)
			setCheckinConfig(t, st, CheckinAwardMaxMilliKey, "1e3")
			u := newCheckinUser(t, st, "corruptCanon")
			if _, err := st.Checkin(ctx, u.ID, now); !errors.Is(err, ErrCheckinDisabled) {
				t.Fatalf("non-canonical max err = %v", err)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, tc.fn)
	}

	// A corrupt cap reads as the documented default: a zero-balance user
	// below the default cap still checks in (the status projection shows the
	// same value the gate enforces).
	setCheckinConfig(t, st, CheckinModeKey, CheckinModeEnabled)
	setCheckinAwardPair(t, st, 10, 10)
	setCheckinConfig(t, st, CreditsCapMilliKey, "not-a-number")
	u := newCheckinUser(t, st, "corruptCap")
	status, err := st.CheckinStatus(ctx, u.ID, now)
	if err != nil || !status.Enabled || status.CreditsCapMilli != DefaultCreditsCapMilli {
		t.Fatalf("corrupt cap status = (%+v, %v)", status, err)
	}
	if _, err := st.Checkin(ctx, u.ID, now); err != nil {
		t.Fatalf("checkin under default cap: %v", err)
	}
}

// TestCheckinConcurrencySingleWinner races 100 same-instant check-ins of one
// user: the UNIQUE constraint must admit exactly one winner with exactly one
// ledger row and exactly one activity contribution; everyone else gets the
// already-checked-in sentinel and nothing is double-applied.
func TestCheckinConcurrencySingleWinner(t *testing.T) {
	st := newCheckinTestStore(t)
	user := newCheckinUser(t, st, "race")
	enableCheckinAt(t, st, CheckinModeEnabled, 60)
	setCheckinAwardPair(t, st, 100, 100)
	now := time.Unix(1700000000, 0).UTC()
	ctx := context.Background()

	const goroutines = 100
	var wg sync.WaitGroup
	var mu sync.Mutex
	successes, already, others := 0, 0, 0
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := st.Checkin(ctx, user.ID, now)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				successes++
				if res.AwardMilli != 100 || res.CreditsAfter != 100 {
					t.Errorf("winner result = %+v", res)
				}
			case errors.Is(err, ErrAlreadyCheckedIn):
				already++
			default:
				others++
				t.Errorf("unexpected race error: %v", err)
			}
		}()
	}
	wg.Wait()
	if successes != 1 || already != goroutines-1 || others != 0 {
		t.Fatalf("successes=%d already=%d others=%d, want 1/%d/0", successes, already, goroutines-1, others)
	}
	if n := countCheckinRows(t, st, `SELECT COUNT(*) FROM checkins WHERE user_id=?`, user.ID); n != 1 {
		t.Fatalf("checkins rows = %d, want 1", n)
	}
	if n := countCheckinRows(t, st,
		`SELECT COUNT(*) FROM credit_ledger WHERE user_id=? AND kind='checkin_award'`, user.ID); n != 1 {
		t.Fatalf("ledger rows = %d, want 1", n)
	}
	if n := countCheckinRows(t, st,
		`SELECT credits FROM users WHERE id=?`, user.ID); n != 100 {
		t.Fatalf("credits = %d, want exactly one award applied", n)
	}
	day := SiteDayKey(now.Unix(), 60)
	if n := countCheckinRows(t, st,
		`SELECT checkins FROM user_activity_daily WHERE user_id=? AND day=?`, user.ID, day); n != 1 {
		t.Fatalf("activity checkins = %d, want 1", n)
	}
	if n := countCheckinRows(t, st,
		`SELECT checkins FROM site_activity_daily WHERE day=?`, day); n != 1 {
		t.Fatalf("site activity checkins = %d, want 1", n)
	}
}

// TestCheckinAwardDistribution exercises the random draw directly: inclusive
// endpoints, exact constants, every value of a tiny range observed, bucket
// counts within generous bounds on a wide range (never flakes), and the
// fail-closed invalid ranges.
func TestCheckinAwardDistribution(t *testing.T) {
	// min == max is an exact constant.
	for _, v := range []int64{0, 1, 60_000_000} {
		got, err := randomAwardMilli(v, v)
		if err != nil || got != v {
			t.Fatalf("constant draw [%d,%d] = (%d, %v)", v, v, got, err)
		}
	}
	// Invalid ranges fail closed.
	for _, pair := range [][2]int64{{5, 4}, {-1, 5}, {-3, -1}} {
		if _, err := randomAwardMilli(pair[0], pair[1]); err == nil {
			t.Fatalf("range %v accepted", pair)
		}
	}
	// Endpoints of small ranges are both reachable (inclusive bounds).
	seen := map[int64]bool{}
	for i := 0; i < 4000; i++ {
		v, err := randomAwardMilli(7, 8)
		if err != nil {
			t.Fatalf("draw [7,8]: %v", err)
		}
		if v != 7 && v != 8 {
			t.Fatalf("draw [7,8] = %d", v)
		}
		seen[v] = true
	}
	if !seen[7] || !seen[8] {
		t.Fatalf("inclusive endpoints not observed: %v", seen)
	}
	// A three-value range observes every value over 2000 draws.
	seen = map[int64]bool{}
	for i := 0; i < 2000; i++ {
		v, err := randomAwardMilli(40, 42)
		if err != nil {
			t.Fatalf("draw [40,42]: %v", err)
		}
		seen[v] = true
	}
	if len(seen) != 3 {
		t.Fatalf("range [40,42] observed %v, want all three values", seen)
	}
	// Wide-range bucket sanity: 10 equal buckets over [0, 1e6), 40k draws,
	// each bucket within ±25% of the expected 4000 (binomial sigma ≈ 60, so
	// the bound is ~16σ and cannot flake).
	const buckets = 10
	const draws = 40000
	counts := make([]int, buckets)
	for i := 0; i < draws; i++ {
		v, err := randomAwardMilli(0, 1_000_000-1)
		if err != nil {
			t.Fatalf("wide draw: %v", err)
		}
		counts[int(v)/100000]++
	}
	expected := draws / buckets
	for b, c := range counts {
		lo, hi := expected*3/4, expected*5/4
		if c < lo || c > hi {
			t.Fatalf("bucket %d count %d outside [%d, %d]", b, c, lo, hi)
		}
	}
}

// TestCheckinRandomnessAcrossDays verifies the repository itself draws inside
// the configured range over many distinct (user, day) check-ins.
func TestCheckinRandomnessAcrossDays(t *testing.T) {
	st := newCheckinTestStore(t)
	user := newCheckinUser(t, st, "draws")
	enableCheckinAt(t, st, CheckinModeEnabled, 0)
	setCheckinAwardPair(t, st, 1_000, 1_002)
	ctx := context.Background()
	seen := map[int64]bool{}
	for i := 0; i < 30; i++ {
		now := time.Unix(1700000000+int64(i)*86400, 0).UTC()
		res, err := st.Checkin(ctx, user.ID, now)
		if err != nil {
			t.Fatalf("checkin %d: %v", i, err)
		}
		if res.AwardMilli < 1000 || res.AwardMilli > 1002 {
			t.Fatalf("award %d outside [1000,1002]", res.AwardMilli)
		}
		seen[res.AwardMilli] = true
	}
	if len(seen) < 2 {
		t.Fatalf("30 draws produced a single value %v (suspiciously non-random)", seen)
	}
}

// TestCheckinFreezeDurableAfterRetentionCleanup pins the new freeze behavior:
// the FIRST temporal row (activity or check-in) durably writes the freeze
// marker, so emptying every freeze table afterwards — simulating retention
// cleanup — can never make the offset mutable again.
func TestCheckinFreezeDurableAfterRetentionCleanup(t *testing.T) {
	st := newCheckinTestStore(t)
	if err := st.SetSiteTimezoneOffsetMinutes(90); err != nil {
		t.Fatalf("set timezone: %v", err)
	}
	ctx := context.Background()

	// The first temporal row is an activity write (console write path).
	admin, err := st.EnsureAdminUser("root")
	if err != nil {
		t.Fatalf("ensure admin: %v", err)
	}
	if err := st.SetSiteConfigValueWithActivity(ctx, "site_name", "Frozen Site", admin.ID,
		time.Unix(1700000000, 0).UTC()); err != nil {
		t.Fatalf("console write: %v", err)
	}
	if n := countCheckinRows(t, st, `SELECT COUNT(*) FROM site_config WHERE key=?`, timezoneLockKey); n != 1 {
		t.Fatalf("freeze marker missing after first activity row")
	}

	// Simulate retention cleanup emptying every freeze table.
	if _, err := st.DB().Exec(`DELETE FROM user_activity_daily`); err != nil {
		t.Fatalf("clear user activity: %v", err)
	}
	if _, err := st.DB().Exec(`DELETE FROM site_activity_daily`); err != nil {
		t.Fatalf("clear site activity: %v", err)
	}
	if err := st.SetSiteTimezoneOffsetMinutes(0); !errors.Is(err, ErrConflict) {
		t.Fatalf("offset mutable after retention cleanup: err = %v, want ErrConflict", err)
	}

	// The check-in rail also freezes on its own first row (fresh database
	// shape: delete the marker and repeat with a check-in).
	st2 := newCheckinTestStore(t)
	if err := st2.SetSiteTimezoneOffsetMinutes(-30); err != nil {
		t.Fatalf("set timezone 2: %v", err)
	}
	u2 := newCheckinUser(t, st2, "freeze2")
	setCheckinConfig(t, st2, CheckinModeKey, CheckinModeEnabled)
	if _, err := st2.Checkin(ctx, u2.ID, time.Unix(1700000000, 0).UTC()); err != nil {
		t.Fatalf("checkin 2: %v", err)
	}
	if n := countCheckinRows(t, st2, `SELECT COUNT(*) FROM site_config WHERE key=?`, timezoneLockKey); n != 1 {
		t.Fatalf("freeze marker missing after first checkin row")
	}
	if _, err := st2.DB().Exec(`DELETE FROM checkins`); err != nil {
		t.Fatalf("clear checkins: %v", err)
	}
	if _, err := st2.DB().Exec(`DELETE FROM user_activity_daily`); err != nil {
		t.Fatalf("clear activity: %v", err)
	}
	if _, err := st2.DB().Exec(`DELETE FROM site_activity_daily`); err != nil {
		t.Fatalf("clear site activity: %v", err)
	}
	if err := st2.SetSiteTimezoneOffsetMinutes(0); !errors.Is(err, ErrConflict) {
		t.Fatalf("offset mutable after checkin cleanup: err = %v, want ErrConflict", err)
	}
}

// TestCheckinStatusNeverPersistsLevelPromotion pins the read-only status
// rule: under level_gated the projection computes the effective level WITHOUT
// the lazy auto-level promotion (the persisted mark is untouched), while the
// write path's in-transaction resolution does promote.
func TestCheckinStatusNeverPersistsLevelPromotion(t *testing.T) {
	st := newCheckinTestStore(t)
	user := newCheckinUser(t, st, "peek")
	enableCheckinAt(t, st, CheckinModeLevelGated, 0)
	ctx := context.Background()
	now := time.Unix(1700000000, 0).UTC()

	// Qualify for level 3 through the donation threshold.
	admin, err := st.EnsureAdminUser("root")
	if err != nil {
		t.Fatalf("ensure admin: %v", err)
	}
	if err := st.SetLevelThresholdMilli(ctx, LevelThreshold3Key, 100, admin.ID, now); err != nil {
		t.Fatalf("set threshold: %v", err)
	}
	if _, err := st.DB().Exec(`UPDATE users SET donation_credit=100 WHERE id=?`, user.ID); err != nil {
		t.Fatalf("set donation credit: %v", err)
	}

	// The status read is a pure read: available, but no promotion persisted.
	status, err := st.CheckinStatus(ctx, user.ID, now)
	if err != nil || !status.Enabled {
		t.Fatalf("status = (%+v, %v), want enabled (peeked level 3)", status, err)
	}
	var auto int
	if err := st.DB().QueryRow(`SELECT auto_level FROM users WHERE id=?`, user.ID).Scan(&auto); err != nil || auto != 1 {
		t.Fatalf("auto_level after status = (%d, %v), want 1 (no lazy promotion)", auto, err)
	}

	// The write path resolves inside its transaction: promotion lands.
	if _, err := st.Checkin(ctx, user.ID, now); err != nil {
		t.Fatalf("checkin: %v", err)
	}
	if err := st.DB().QueryRow(`SELECT auto_level FROM users WHERE id=?`, user.ID).Scan(&auto); err != nil || auto != 3 {
		t.Fatalf("auto_level after checkin = (%d, %v), want 3", auto, err)
	}
}

// TestCheckinExportRows covers the export projection: the site-local date
// rendering, canonical award strings, ownership scoping, the fail-closed
// bounds, and the unset-offset impossibility (rows cannot exist without a
// configured offset; a corrupted state fails closed).
func TestCheckinExportRows(t *testing.T) {
	st := newCheckinTestStore(t)
	enableCheckinAt(t, st, CheckinModeEnabled, 30)
	user := newCheckinUser(t, st, "export")
	other := newCheckinUser(t, st, "exportOther")
	setCheckinAwardPair(t, st, 500, 500)
	ctx := context.Background()

	// Two check-ins on consecutive site-local days (offset +30: local
	// midnight at 23:30 UTC).
	first := time.Unix(1700006400, 0).UTC()  // inside some local day D
	second := time.Unix(1700092800, 0).UTC() // +86400s: local day D+1
	r1, err := st.Checkin(ctx, user.ID, first)
	if err != nil {
		t.Fatalf("checkin 1: %v", err)
	}
	r2, err := st.Checkin(ctx, user.ID, second)
	if err != nil {
		t.Fatalf("checkin 2: %v", err)
	}
	if r2.Day-r1.Day != 86400 {
		t.Fatalf("consecutive day keys differ by %d, want 86400", r2.Day-r1.Day)
	}
	if _, err := st.Checkin(ctx, other.ID, first); err != nil {
		t.Fatalf("other checkin: %v", err)
	}

	rows, err := st.ListExportCheckins(ctx, user.ID, ExportCollectionLimit)
	if err != nil {
		t.Fatalf("export checkins: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("export rows = %d, want 2 (ownership-scoped)", len(rows))
	}
	// Day-key arithmetic cross-check: the rendered date equals the local
	// calendar date of the day key under the frozen +30 offset.
	for i, res := range []*CheckinResult{r1, r2} {
		want := time.Unix(res.Day+30*60, 0).UTC().Format("2006-01-02")
		if rows[i].Day != want {
			t.Fatalf("row %d day = %q, want %q", i, rows[i].Day, want)
		}
		if rows[i].AwardMilli != "500" {
			t.Fatalf("row %d award = %q, want \"500\"", i, rows[i].AwardMilli)
		}
	}

	// Fail-closed bounds.
	if _, err := st.ListExportCheckins(ctx, user.ID, 0); !errors.Is(err, ErrExportLimit) {
		t.Fatalf("limit 0 err = %v, want ErrExportLimit", err)
	}
	if _, err := st.ListExportCheckins(ctx, user.ID, ExportCollectionLimit+1); !errors.Is(err, ErrExportLimit) {
		t.Fatalf("over-bound err = %v, want ErrExportLimit", err)
	}
	if _, err := st.ListExportCheckins(ctx, 0, 10); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing user err = %v, want ErrNotFound", err)
	}
}

// TestCheckinDeleteCascadesAndRecomputesSiteRollup verifies the lifecycle:
// deleting a check-in-heavy account removes its rows by cascade and the
// affected site-day rollup no longer counts its contributions.
func TestCheckinDeleteCascadesAndRecomputesSiteRollup(t *testing.T) {
	st := newCheckinTestStore(t)
	enableCheckinAt(t, st, CheckinModeEnabled, 0)
	heavy := newCheckinUser(t, st, "heavy")
	light := newCheckinUser(t, st, "light")
	setCheckinAwardPair(t, st, 10, 10)
	ctx := context.Background()

	var lastDay int64 = -1
	for i := 0; i < 5; i++ {
		now := time.Unix(1700000000+int64(i)*86400, 0).UTC()
		if _, err := st.Checkin(ctx, heavy.ID, now); err != nil {
			t.Fatalf("heavy checkin %d: %v", i, err)
		}
		lastDay = SiteDayKey(now.Unix(), 0)
	}
	firstDay := SiteDayKey(1700000000, 0)
	if _, err := st.Checkin(ctx, light.ID, time.Unix(1700000000, 0).UTC()); err != nil {
		t.Fatalf("light checkin: %v", err)
	}
	if n := countCheckinRows(t, st, `SELECT checkins FROM site_activity_daily WHERE day=?`, firstDay); n != 2 {
		t.Fatalf("site rollup checkins (first day) = %d, want 2", n)
	}
	if n := countCheckinRows(t, st, `SELECT checkins FROM site_activity_daily WHERE day=?`, lastDay); n != 1 {
		t.Fatalf("site rollup checkins (last day) = %d, want 1", n)
	}

	if err := st.DeleteUserAccount(ctx, heavy.ID); err != nil {
		t.Fatalf("delete account: %v", err)
	}
	if n := countCheckinRows(t, st, `SELECT COUNT(*) FROM checkins WHERE user_id=?`, heavy.ID); n != 0 {
		t.Fatalf("checkins survived delete: %d", n)
	}
	if n := countCheckinRows(t, st, `SELECT COUNT(*) FROM credit_ledger WHERE user_id=?`, heavy.ID); n != 0 {
		t.Fatalf("ledger survived delete: %d", n)
	}
	// The deleted account leaves no contribution behind on any affected day.
	if n := countCheckinRows(t, st, `SELECT checkins FROM site_activity_daily WHERE day=?`, firstDay); n != 1 {
		t.Fatalf("site rollup after delete = %d, want 1", n)
	}
}

// TestCheckinAdminExcludedAndMissingUser pins the identity edges: the
// environment administrator is excluded by the level resolver and a missing
// user is not found; both propagate untouched (no fabricated check-in).
func TestCheckinAdminExcludedAndMissingUser(t *testing.T) {
	st := newCheckinTestStore(t)
	enableCheckinAt(t, st, CheckinModeLevelGated, 0)
	admin, err := st.EnsureAdminUser("root")
	if err != nil {
		t.Fatalf("ensure admin: %v", err)
	}
	ctx := context.Background()
	now := time.Unix(1700000000, 0).UTC()
	if _, err := st.Checkin(ctx, admin.ID, now); !errors.Is(err, ErrLevelAdminExcluded) {
		t.Fatalf("admin checkin err = %v, want ErrLevelAdminExcluded", err)
	}
	if _, err := st.Checkin(ctx, 999999, now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing user err = %v, want ErrNotFound", err)
	}
	// level_gated peeks the level on the status path: the resolver exclusion
	// propagates.
	if _, err := st.CheckinStatus(ctx, admin.ID, now); !errors.Is(err, ErrLevelAdminExcluded) {
		t.Fatalf("admin status err = %v, want ErrLevelAdminExcluded", err)
	}
	// Enabled mode never peeks the level: the balances read reports the
	// administrator protection sentinel instead (fail closed either way).
	setCheckinConfig(t, st, CheckinModeKey, CheckinModeEnabled)
	if _, err := st.CheckinStatus(ctx, admin.ID, now); !errors.Is(err, ErrAdminProtected) {
		t.Fatalf("admin status (enabled) err = %v, want ErrAdminProtected", err)
	}
}

// TestSetCheckinAwardBoundPairValidation covers the administrator pair write:
// both directions of the min<=max cross-validation, same-transaction behavior
// (a rejected combination writes nothing), and the console-write activity
// hook.
func TestSetCheckinAwardBoundPairValidation(t *testing.T) {
	st := newCheckinTestStore(t)
	admin, err := st.EnsureAdminUser("root")
	if err != nil {
		t.Fatalf("ensure admin: %v", err)
	}
	ctx := context.Background()
	at := time.Unix(1700000000, 0).UTC()

	// Unknown key and negative value are refused outright.
	if err := st.SetCheckinAwardBoundMilli(ctx, "checkin_award_mid_milli", 1, admin.ID, at); !errors.Is(err, ErrConflict) {
		t.Fatalf("unknown key err = %v", err)
	}
	if err := st.SetCheckinAwardBoundMilli(ctx, CheckinAwardMinMilliKey, -1, admin.ID, at); !errors.Is(err, ErrConflict) {
		t.Fatalf("negative err = %v", err)
	}

	// While both keys are unset they cross-validate against the documented
	// defaults (40M/60M): a max below the default min is rejected, and the
	// rejection writes nothing.
	if err := st.SetCheckinAwardBoundMilli(ctx, CheckinAwardMaxMilliKey, 1000, admin.ID, at); !errors.Is(err, ErrConflict) {
		t.Fatalf("max below default min err = %v", err)
	}
	var stored string
	if err := st.DB().QueryRow(`SELECT value FROM site_config WHERE key=?`, CheckinAwardMaxMilliKey).Scan(&stored); err != sql.ErrNoRows {
		t.Fatalf("rejected max write leaked stored=%q err=%v", stored, err)
	}

	// Build a stored pair, then attack it from both directions.
	if err := st.SetCheckinAwardBoundMilli(ctx, CheckinAwardMinMilliKey, 100, admin.ID, at); err != nil {
		t.Fatalf("set min: %v", err)
	}
	if err := st.SetCheckinAwardBoundMilli(ctx, CheckinAwardMaxMilliKey, 1000, admin.ID, at); err != nil {
		t.Fatalf("set max: %v", err)
	}
	if err := st.SetCheckinAwardBoundMilli(ctx, CheckinAwardMinMilliKey, 1001, admin.ID, at); !errors.Is(err, ErrConflict) {
		t.Fatalf("min>max err = %v", err)
	}
	if err := st.DB().QueryRow(`SELECT value FROM site_config WHERE key=?`, CheckinAwardMinMilliKey).Scan(&stored); err != nil || stored != "100" {
		t.Fatalf("rejected min rewrite leaked stored=(%q, %v)", stored, err)
	}
	if err := st.SetCheckinAwardBoundMilli(ctx, CheckinAwardMaxMilliKey, 99, admin.ID, at); !errors.Is(err, ErrConflict) {
		t.Fatalf("max<min err = %v", err)
	}
	if err := st.DB().QueryRow(`SELECT value FROM site_config WHERE key=?`, CheckinAwardMaxMilliKey).Scan(&stored); err != nil || stored != "1000" {
		t.Fatalf("rejected max rewrite leaked stored=(%q, %v)", stored, err)
	}

	// The successful writes recorded the console-write activity only once a
	// timezone is configured (unset: the activity half self-skips). Repeat
	// with a configured timezone to observe the activity hook.
	if err := st.SetSiteTimezoneOffsetMinutes(0); err != nil {
		t.Fatalf("set UTC: %v", err)
	}
	if err := st.SetCheckinAwardBoundMilli(ctx, CheckinAwardMaxMilliKey, 2000, admin.ID, at); err != nil {
		t.Fatalf("set max 2: %v", err)
	}
	day := SiteDayKey(at.Unix(), 0)
	if n := countCheckinRows(t, st,
		`SELECT console_writes FROM user_activity_daily WHERE user_id=? AND day=?`, admin.ID, day); n != 1 {
		t.Fatalf("console writes = %d, want 1", n)
	}
	// A rejected write records no activity contribution.
	if err := st.SetCheckinAwardBoundMilli(ctx, CheckinAwardMaxMilliKey, 50, admin.ID, at); !errors.Is(err, ErrConflict) {
		t.Fatalf("rejected rewrite err = %v", err)
	}
	if n := countCheckinRows(t, st,
		`SELECT console_writes FROM user_activity_daily WHERE user_id=? AND day=?`, admin.ID, day); n != 1 {
		t.Fatalf("console writes after rejected write = %d, want still 1", n)
	}
}
