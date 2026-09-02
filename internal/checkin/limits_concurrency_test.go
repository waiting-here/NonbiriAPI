package checkin

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"math/big"
	"sync"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

func TestTimezoneConfigurationMatchesCanonicalResolver(t *testing.T) {
	for _, test := range []struct {
		name    string
		offset  string
		enabled bool
	}{
		{name: "canonical UTC", offset: "0", enabled: true},
		{name: "trimmed canonical value", offset: " 30 ", enabled: true},
		{name: "explicit plus rejected", offset: "+30"},
		{name: "leading zero rejected", offset: "030"},
		{name: "non half hour rejected", offset: "15"},
		{name: "above east bound rejected", offset: "870"},
		{name: "below west bound rejected", offset: "-750"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newCheckinFixture(t)
			fixture.configure(db.CheckinModeEnabled, test.offset, 1, 1, 100)
			userID := fixture.seedUser("timezone")
			status, err := fixture.service.Status(context.Background(), userID)
			if err != nil {
				t.Fatalf("status: %v", err)
			}
			if status.Enabled != test.enabled {
				t.Fatalf("enabled = %v, want %v", status.Enabled, test.enabled)
			}
			if !test.enabled {
				if _, err := fixture.service.Checkin(context.Background(), userID); !errors.Is(err, ErrFeatureDisabled) {
					t.Fatalf("POST error = %v", err)
				}
			}
		})
	}
}

func TestExistingProductActivityDoesNotDoubleCountDistinctUser(t *testing.T) {
	fixture := newCheckinFixture(t)
	fixture.configure(db.CheckinModeEnabled, "0", 10, 10, 100)
	userID := fixture.seedUser("already-active")
	day := db.SiteDayKey(fixture.clock.Load(), 0)
	if _, err := fixture.database.Exec(`INSERT INTO user_activity_daily(
		day,user_id,product_active,checkins,updated_at
	) VALUES(?,?,1,0,?)`, day, userID, fixture.clock.Load()); err != nil {
		t.Fatal(err)
	}
	insertSiteActivity(t, fixture.database, day, 1, db.U128{}, mustU128(t, "1"), fixture.clock.Load())
	if _, err := fixture.service.Checkin(context.Background(), userID); err != nil {
		t.Fatalf("check in: %v", err)
	}
	if got := fixture.u128(`SELECT checkins FROM site_activity_daily WHERE day=?`, day); got.Cmp(big.NewInt(1)) != 0 {
		t.Fatalf("site check-ins = %s", got)
	}
	if got := fixture.u128(`SELECT distinct_product_users FROM site_activity_daily WHERE day=?`, day); got.Cmp(big.NewInt(1)) != 0 {
		t.Fatalf("distinct product users = %s, want 1", got)
	}
	if got := fixture.scalar(`SELECT checkins FROM user_activity_daily WHERE day=? AND user_id=?`, day, userID); got != 1 {
		t.Fatalf("user check-ins = %d", got)
	}
}

func TestTwoUsersIncrementExistingSiteU128Counters(t *testing.T) {
	fixture := newCheckinFixture(t)
	fixture.configure(db.CheckinModeEnabled, "0", 10, 10, 100)
	firstUser := fixture.seedUser("site-first")
	secondUser := fixture.seedUser("site-second")
	if _, err := fixture.service.Checkin(context.Background(), firstUser); err != nil {
		t.Fatalf("first user: %v", err)
	}
	if _, err := fixture.service.Checkin(context.Background(), secondUser); err != nil {
		t.Fatalf("second user: %v", err)
	}
	day := db.SiteDayKey(fixture.clock.Load(), 0)
	if got := fixture.u128(`SELECT checkins FROM site_activity_daily WHERE day=?`, day); got.Cmp(big.NewInt(2)) != 0 {
		t.Fatalf("site check-ins = %s, want 2", got)
	}
	if got := fixture.u128(`SELECT distinct_product_users FROM site_activity_daily WHERE day=?`, day); got.Cmp(big.NewInt(2)) != 0 {
		t.Fatalf("site distinct users = %s, want 2", got)
	}
	if got := fixture.scalar(`SELECT SUM(checkins) FROM user_activity_daily WHERE day=?`, day); got != 2 {
		t.Fatalf("user check-in total = %d, want 2", got)
	}
}

func TestResourceBoundFailuresRollbackWholeCheckin(t *testing.T) {
	t.Run("ledger sequence MAX", func(t *testing.T) {
		fixture := newCheckinFixture(t)
		fixture.configure(db.CheckinModeEnabled, "0", 7, 7, 100)
		userID := fixture.seedUser("capacity")
		if _, err := fixture.database.Exec(`UPDATE credit_capacity SET last_ledger_seq=? WHERE id=1`, int64(math.MaxInt64)); err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.service.Checkin(context.Background(), userID); !errors.Is(err, ErrResourceLimit) {
			t.Fatalf("error = %v", err)
		}
		assertNoCheckinSideEffects(t, fixture, userID)
		if got := fixture.scalar(`SELECT COUNT(*) FROM site_config WHERE key=?`, timezoneLockKey); got != 0 {
			t.Fatalf("rolled-back check-in left timezone lock count %d", got)
		}
	})

	t.Run("user daily count MAX", func(t *testing.T) {
		fixture := newCheckinFixture(t)
		fixture.configure(db.CheckinModeEnabled, "0", 7, 7, 100)
		userID := fixture.seedUser("user-count")
		day := db.SiteDayKey(fixture.clock.Load(), 0)
		if _, err := fixture.database.Exec(`INSERT INTO user_activity_daily(
			day,user_id,product_active,checkins,updated_at
		) VALUES(?,?,1,?,?)`, day, userID, int64(math.MaxInt64), fixture.clock.Load()); err != nil {
			t.Fatal(err)
		}
		insertSiteActivity(t, fixture.database, day, 1, mustU128(t, "340282366920938463463374607431768211454"), mustU128(t, "1"), fixture.clock.Load())
		beforeSequence := fixture.scalar(`SELECT last_ledger_seq FROM credit_capacity WHERE id=1`)
		if _, err := fixture.service.Checkin(context.Background(), userID); !errors.Is(err, ErrResourceLimit) {
			t.Fatalf("error = %v", err)
		}
		if fixture.scalar(`SELECT COUNT(*) FROM checkins WHERE user_id=?`, userID) != 0 ||
			fixture.scalar(`SELECT last_ledger_seq FROM credit_capacity WHERE id=1`) != beforeSequence ||
			fixture.balance(userID).Sign() != 0 {
			t.Fatal("user count overflow did not roll back domain and ledger")
		}
		if got := fixture.scalar(`SELECT checkins FROM user_activity_daily WHERE day=? AND user_id=?`, day, userID); got != math.MaxInt64 {
			t.Fatalf("user count changed to %d", got)
		}
	})

	t.Run("site check-in U128 MAX", func(t *testing.T) {
		fixture := newCheckinFixture(t)
		fixture.configure(db.CheckinModeEnabled, "0", 7, 7, 100)
		userID := fixture.seedUser("site-count")
		day := db.SiteDayKey(fixture.clock.Load(), 0)
		maximum := mustU128(t, "340282366920938463463374607431768211455")
		insertSiteActivity(t, fixture.database, day, 0, maximum, db.U128{}, fixture.clock.Load())
		beforeSequence := fixture.scalar(`SELECT last_ledger_seq FROM credit_capacity WHERE id=1`)
		if _, err := fixture.service.Checkin(context.Background(), userID); !errors.Is(err, ErrResourceLimit) {
			t.Fatalf("error = %v", err)
		}
		if fixture.scalar(`SELECT COUNT(*) FROM checkins WHERE user_id=?`, userID) != 0 ||
			fixture.scalar(`SELECT COUNT(*) FROM user_activity_daily WHERE user_id=?`, userID) != 0 ||
			fixture.scalar(`SELECT last_ledger_seq FROM credit_capacity WHERE id=1`) != beforeSequence ||
			fixture.balance(userID).Sign() != 0 {
			t.Fatal("site U128 overflow did not roll back all prior writes")
		}
		if got := fixture.u128(`SELECT checkins FROM site_activity_daily WHERE day=?`, day); got.Cmp(maximum.Big()) != 0 {
			t.Fatalf("site count changed to %s", got)
		}
	})

	t.Run("site distinct U128 MAX", func(t *testing.T) {
		fixture := newCheckinFixture(t)
		fixture.configure(db.CheckinModeEnabled, "0", 7, 7, 100)
		userID := fixture.seedUser("distinct-count")
		day := db.SiteDayKey(fixture.clock.Load(), 0)
		maximum := mustU128(t, "340282366920938463463374607431768211455")
		insertSiteActivity(t, fixture.database, day, 1, db.U128{}, maximum, fixture.clock.Load())
		if _, err := fixture.service.Checkin(context.Background(), userID); !errors.Is(err, ErrResourceLimit) {
			t.Fatalf("error = %v", err)
		}
		if fixture.scalar(`SELECT COUNT(*) FROM checkins WHERE user_id=?`, userID) != 0 ||
			fixture.scalar(`SELECT COUNT(*) FROM user_activity_daily WHERE user_id=?`, userID) != 0 ||
			fixture.balance(userID).Sign() != 0 {
			t.Fatal("distinct U128 overflow did not roll back all prior writes")
		}
	})
}

func TestTimezoneFreezeSurvivesTemporalRetention(t *testing.T) {
	fixture := newCheckinFixture(t)
	fixture.configure(db.CheckinModeEnabled, "0", 1, 1, 100)
	userID := fixture.seedUser("freeze")
	if _, err := fixture.service.Checkin(context.Background(), userID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.database.Exec(`DELETE FROM checkins`); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.database.Exec(`DELETE FROM user_activity_daily`); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.database.Exec(`DELETE FROM site_activity_daily`); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.SetSiteTimezoneOffsetMinutes(30); !errors.Is(err, db.ErrConflict) {
		t.Fatalf("timezone change after retention = %v, want conflict", err)
	}
	var value string
	if err := fixture.database.QueryRow(`SELECT value FROM site_config WHERE key=?`, timezoneLockKey).Scan(&value); err != nil || value != "1" {
		t.Fatalf("timezone lock = %q, err=%v", value, err)
	}
}

func TestConcurrentSameUserSameDayHasOneWinner(t *testing.T) {
	fixture := newCheckinFixture(t)
	fixture.configure(db.CheckinModeEnabled, "0", 1, 1, 100)
	userID := fixture.seedUser("concurrent")
	const workers = 12
	start := make(chan struct{})
	errorsByWorker := make([]error, workers)
	var wait sync.WaitGroup
	wait.Add(workers)
	for index := range workers {
		go func(index int) {
			defer wait.Done()
			<-start
			_, errorsByWorker[index] = fixture.service.Checkin(context.Background(), userID)
		}(index)
	}
	close(start)
	wait.Wait()
	winners, duplicates := 0, 0
	for _, err := range errorsByWorker {
		switch {
		case err == nil:
			winners++
		case errors.Is(err, ErrAlreadyCheckedIn):
			duplicates++
		default:
			t.Fatalf("unexpected concurrent error: %v", err)
		}
	}
	if winners != 1 || duplicates != workers-1 {
		t.Fatalf("winners=%d duplicates=%d", winners, duplicates)
	}
	if fixture.scalar(`SELECT COUNT(*) FROM checkins WHERE user_id=?`, userID) != 1 ||
		fixture.scalar(`SELECT COUNT(*) FROM credit_operations WHERE kind='checkin_award'`) != 1 ||
		fixture.scalar(`SELECT SUM(checkins) FROM user_activity_daily WHERE user_id=?`, userID) != 1 ||
		fixture.balance(userID).Cmp(big.NewInt(1)) != 0 {
		t.Fatal("concurrent losers changed committed check-in facts")
	}
	if got := fixture.u128(`SELECT checkins FROM site_activity_daily`); got.Cmp(big.NewInt(1)) != 0 {
		t.Fatalf("site check-ins = %s", got)
	}
}

func assertNoCheckinSideEffects(t *testing.T, fixture *checkinFixture, userID int64) {
	t.Helper()
	if fixture.scalar(`SELECT COUNT(*) FROM checkins WHERE user_id=?`, userID) != 0 ||
		fixture.scalar(`SELECT COUNT(*) FROM credit_operations WHERE kind='checkin_award'`) != 0 ||
		fixture.scalar(`SELECT COUNT(*) FROM user_activity_daily WHERE user_id=?`, userID) != 0 ||
		fixture.scalar(`SELECT COUNT(*) FROM site_activity_daily`) != 0 ||
		fixture.balance(userID).Sign() != 0 {
		t.Fatal("failed check-in left side effects")
	}
}

func insertSiteActivity(t *testing.T, database *sql.DB, day int64, productActive int, checkins, distinct db.U128, now int64) {
	t.Helper()
	zero := db.EncodeU128(db.U128{})
	if _, err := database.Exec(`INSERT INTO site_activity_daily(
		day,product_active,api_requests,uncached_input_tokens,cache_write_input_tokens,
		cache_read_input_tokens,output_tokens,checkins,console_writes,game_active,
		game_rounds,distinct_product_users,updated_at
	) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, day, productActive, zero, zero, zero, zero, zero,
		db.EncodeU128(checkins), zero, 0, zero, db.EncodeU128(distinct), now); err != nil {
		t.Fatalf("insert site activity: %v", err)
	}
}

func mustU128(t *testing.T, value string) db.U128 {
	t.Helper()
	parsed, err := db.ParseU128Decimal(value)
	if err != nil {
		t.Fatalf("parse U128 %s: %v", value, err)
	}
	return parsed
}
