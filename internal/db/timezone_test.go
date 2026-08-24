package db

// Tests for the authoritative site-timezone resolver and its immutability
// guard: unset vs explicit-UTC distinction, fail-closed reads, validation,
// the atomic freeze once temporal data exists, and day-key arithmetic.

import (
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestSiteTimezoneUnsetFailsClosed(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "tz-unset.db"))
	defer st.Close()

	if _, err := st.SiteTimezoneOffsetMinutes(); !errors.Is(err, ErrTimezoneUnavailable) {
		t.Fatalf("unset offset = %v, want ErrTimezoneUnavailable", err)
	}
	if _, err := st.SiteDayKeyAt(time.Now().Unix()); !errors.Is(err, ErrTimezoneUnavailable) {
		t.Fatalf("day key with unset offset = %v, want ErrTimezoneUnavailable", err)
	}
}

func TestSiteTimezoneExplicitZeroIsNotUnset(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "tz-zero.db"))
	defer st.Close()

	if err := st.SetSiteTimezoneOffsetMinutes(0); err != nil {
		t.Fatalf("SetSiteTimezoneOffsetMinutes(0): %v", err)
	}
	offset, err := st.SiteTimezoneOffsetMinutes()
	if err != nil || offset != 0 {
		t.Fatalf("explicit UTC = (%d, %v), want (0, nil)", offset, err)
	}
	// The stored row exists now: an unset key would not.
	values, err := st.GetAllSiteConfigValues()
	if err != nil {
		t.Fatalf("GetAllSiteConfigValues: %v", err)
	}
	if values[SiteTimezoneKey] != "0" {
		t.Fatalf("stored value = %q, want \"0\"", values[SiteTimezoneKey])
	}
}

func TestSiteTimezoneCorruptStoredValueFailsClosed(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "tz-corrupt.db"))
	defer st.Close()
	for _, corrupt := range []string{"abc", "45", "1000", "", "+30"} {
		if _, err := st.DB().Exec(`INSERT INTO site_config (key, value, updated_at) VALUES (?, ?, 0)
			ON CONFLICT(key) DO UPDATE SET value=excluded.value`, SiteTimezoneKey, corrupt); err != nil {
			t.Fatalf("seed corrupt value %q: %v", corrupt, err)
		}
		if _, err := st.SiteTimezoneOffsetMinutes(); !errors.Is(err, ErrTimezoneUnavailable) {
			t.Fatalf("corrupt value %q = %v, want ErrTimezoneUnavailable", corrupt, err)
		}
	}
}

func TestSetSiteTimezoneValidation(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "tz-valid.db"))
	defer st.Close()

	for _, invalid := range []int{15, 45, -45, 345, 721, -721, 841, 100000, -100000} {
		if err := st.SetSiteTimezoneOffsetMinutes(invalid); !errors.Is(err, ErrConflict) {
			t.Fatalf("invalid offset %d = %v, want ErrConflict", invalid, err)
		}
	}
	for _, valid := range []int{-720, -60, 0, 30, 330, 840} {
		if err := st.SetSiteTimezoneOffsetMinutes(valid); err != nil {
			t.Fatalf("valid offset %d rejected: %v", valid, err)
		}
		if got, err := st.SiteTimezoneOffsetMinutes(); err != nil || got != valid {
			t.Fatalf("round-trip %d = (%d, %v)", valid, got, err)
		}
	}
}

// TestSiteTimezoneFrozenOnceDataExists verifies the guard is atomic and
// permanent: the first checkin/activity row freezes the offset forever, even
// after every row is later removed (retention), because a lock marker was
// written in the same transaction that first observed the data.
func TestSiteTimezoneFrozenOnceDataExists(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "tz-freeze.db"))
	defer st.Close()

	// No freeze table exists yet: setting works.
	if err := st.SetSiteTimezoneOffsetMinutes(60); err != nil {
		t.Fatalf("initial set: %v", err)
	}

	// One check-in row (the schema-created table, real FK owner) is temporal
	// data.
	user, err := st.CreateUser("discord-tz-freeze", "tz-freeze", "")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if _, err := st.DB().Exec(`INSERT INTO checkins (user_id, day, award, operation_id, created_at)
		VALUES (?, 0, 1, ?, 0)`, user.ID, "sys.checkin."+fmt.Sprint(user.ID)+".0"); err != nil {
		t.Fatalf("insert checkin: %v", err)
	}
	if err := st.SetSiteTimezoneOffsetMinutes(120); !errors.Is(err, ErrConflict) {
		t.Fatalf("change over data = %v, want ErrConflict", err)
	}
	// Even writing the identical value is refused once frozen.
	if err := st.SetSiteTimezoneOffsetMinutes(60); !errors.Is(err, ErrConflict) {
		t.Fatalf("same-value write over data = %v, want ErrConflict", err)
	}
	// Retention-style cleanup must not unfreeze the key.
	if _, err := st.DB().Exec(`DELETE FROM checkins`); err != nil {
		t.Fatalf("clear checkins: %v", err)
	}
	if err := st.SetSiteTimezoneOffsetMinutes(120); !errors.Is(err, ErrConflict) {
		t.Fatalf("change after cleanup = %v, want ErrConflict", err)
	}
	if got, err := st.SiteTimezoneOffsetMinutes(); err != nil || got != 60 {
		t.Fatalf("frozen offset changed: (%d, %v)", got, err)
	}
}

// TestSiteTimezoneFreezeVsSetRace runs offset writes against concurrent
// first-row inserts; every outcome must be one of {set succeeded while no
// data existed, refused with ErrConflict}, never a corrupted state or an
// unexpected error kind.
func TestSiteTimezoneFreezeVsSetRace(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "tz-race.db"))
	defer st.Close()
	user, err := st.CreateUser("discord-tz-race", "tz-race", "")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		i := 0
		for {
			select {
			case <-stop:
				return
			default:
			}
			if _, err := st.DB().Exec(
				`INSERT INTO checkins (user_id, day, award, operation_id, created_at) VALUES (?, ?, 1, ?, ?)`,
				user.ID, i, fmt.Sprintf("sys.checkin.%d.%d", user.ID, i), i); err != nil {
				t.Errorf("insert checkin: %v", err)
				return
			}
			if _, err := st.DB().Exec(`DELETE FROM checkins WHERE user_id=?`, user.ID); err != nil {
				t.Errorf("delete checkins: %v", err)
				return
			}
			i++
		}
	}()
	setOK, setConflict := 0, 0
	for i := 0; i < 40; i++ {
		switch err := st.SetSiteTimezoneOffsetMinutes(30 + (i%3)*30); {
		case err == nil:
			setOK++
		case errors.Is(err, ErrConflict):
			setConflict++
		default:
			t.Fatalf("unexpected set error: %v", err)
		}
	}
	close(stop)
	wg.Wait()
	if setOK == 0 && setConflict == 0 {
		t.Fatal("no outcomes recorded")
	}
	// Final state must be consistent: either no configured value or a valid one.
	if _, err := st.SiteTimezoneOffsetMinutes(); err != nil && !errors.Is(err, ErrTimezoneUnavailable) {
		t.Fatalf("final resolver error: %v", err)
	}
}

func TestSiteDayKeyArithmetic(t *testing.T) {
	cases := []struct {
		name   string
		unix   int64
		offset int64
		want   int64
	}{
		{"epoch utc", 0, 0, 0},
		// Instant 0 under UTC+1 is local 01:00 Jan 1; that day's local
		// midnight is unix -3600.
		{"epoch utc+1", 0, 60, -3600},
		// Instant 0 under UTC-1 is local 23:00 Dec 31; midnight of that
		// local day is unix -82800.
		{"epoch utc-1", 0, -60, -82800},
		{"local 02:00 utc+1 same day", 3600, 60, -3600},
		{"one second after local 02:00 utc+1", 3601, 60, -3600},
		{"one second before local midnight rollover utc+1", 82799, 60, -3600},
		{"local midnight rollover utc+1", 82800, 60, 82800},
		{"min offset utc-12 noon instant", 0, -720, -43200},
		{"max offset utc+14", 0, 840, -50400},
	}
	for _, tc := range cases {
		if got := SiteDayKey(tc.unix, tc.offset); got != tc.want {
			t.Errorf("%s: SiteDayKey(%d,%d)=%d want %d", tc.name, tc.unix, tc.offset, got, tc.want)
		}
	}
	// The key must equal the instant's own midnight when aligned.
	now := time.Now().Unix()
	key := SiteDayKey(now, 330)
	if key > now || now-key >= 86400 {
		t.Errorf("day key not within one day below the instant: now=%d key=%d", now, key)
	}
	if SiteDayKey(key, 330) != key {
		t.Errorf("day key not idempotent under its own offset")
	}
}

func TestSiteDayKeyAtUsesConfiguredOffset(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "tz-daykey.db"))
	defer st.Close()
	if err := st.SetSiteTimezoneOffsetMinutes(-120); err != nil {
		t.Fatalf("set offset: %v", err)
	}
	instant := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC).Unix()
	key, err := st.SiteDayKeyAt(instant)
	if err != nil {
		t.Fatalf("SiteDayKeyAt: %v", err)
	}
	if want := SiteDayKey(instant, -120); key != want {
		t.Fatalf("SiteDayKeyAt = %d want %d", key, want)
	}
}
