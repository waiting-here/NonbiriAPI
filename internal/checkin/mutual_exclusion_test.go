package checkin

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"sync"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/continuity"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

func mutualFixture(t *testing.T) (*checkinFixture, int64) {
	t.Helper()
	f := newCheckinFixture(t)
	f.configure(db.CheckinModeEnabled, "330", 1000, 1000, 0)
	configureGameCheckin(f, db.CheckinModeEnabled, 2000, 2000, 0)
	f.setConfig(mutuallyExclusiveKey, "1")
	return f, f.seedUser("daily-choice")
}

func TestMutuallyExclusiveCheckinBothOrdersAndSiteDay(t *testing.T) {
	for _, first := range []ledger.Asset{ledger.General, ledger.Game} {
		t.Run(string(first), func(t *testing.T) {
			f, user := mutualFixture(t)
			day := db.SiteDayKey(f.clock.Load(), 330)
			f.clock.Store(day + 86400 - 1)
			second := otherCheckinAsset(first)
			for _, asset := range []ledger.Asset{first, second} {
				status, err := f.service.StatusForAsset(context.Background(), user, asset)
				if err != nil || !status.Enabled || !status.MutuallyExclusive || status.CheckedInToday || status.BlockedByOtherCheckin {
					t.Fatalf("initial %s status = %+v, %v", asset, status, err)
				}
			}
			if _, err := f.service.CheckinForAsset(context.Background(), user, first); err != nil {
				t.Fatal(err)
			}
			for _, asset := range []ledger.Asset{first, second} {
				status, err := f.service.StatusForAsset(context.Background(), user, asset)
				if err != nil || status.CheckedInToday != (asset == first) || status.BlockedByOtherCheckin != (asset == second) {
					t.Fatalf("claimed %s status = %+v, %v", asset, status, err)
				}
			}
			before := f.scalar(`SELECT COUNT(*) FROM credit_operations`)
			if _, err := f.service.CheckinForAsset(context.Background(), user, second); !errors.Is(err, ErrOtherCheckedIn) {
				t.Fatalf("opposite check-in = %v", err)
			}
			if got := f.scalar(`SELECT COUNT(*) FROM credit_operations`); got != before {
				t.Fatal("blocked check-in changed ledger")
			}
			f.clock.Add(1)
			if _, err := f.service.CheckinForAsset(context.Background(), user, second); err != nil {
				t.Fatalf("new site day did not release choice: %v", err)
			}
		})
	}
}

func TestMutuallyExclusiveCheckinConcurrentAssetsCommitOneAward(t *testing.T) {
	f, user := mutualFixture(t)
	const calls = 12
	results := make([]error, calls)
	start := make(chan struct{})
	var wait sync.WaitGroup
	for i := range results {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			asset := ledger.General
			if i%2 != 0 {
				asset = ledger.Game
			}
			_, results[i] = f.service.CheckinForAsset(context.Background(), user, asset)
		}()
	}
	close(start)
	wait.Wait()
	winners := 0
	for _, err := range results {
		if err == nil {
			winners++
		} else if !errors.Is(err, ErrAlreadyCheckedIn) && !errors.Is(err, ErrOtherCheckedIn) {
			t.Fatalf("unexpected concurrent result: %v", err)
		}
	}
	if winners != 1 || f.scalar(`SELECT (SELECT COUNT(*) FROM checkins)+(SELECT COUNT(*) FROM game_checkins)`) != 1 {
		t.Fatalf("cross-asset winners = %d", winners)
	}
	if f.scalar(`SELECT COUNT(*) FROM credit_operations WHERE kind='checkin_award'`) != 1 {
		t.Fatal("choice did not commit exactly one award")
	}
	if f.scalar(`SELECT checkins+game_checkins FROM user_activity_daily WHERE user_id=?`, user) != 1 || f.scalar(`SELECT COUNT(*) FROM identity_continuity_facts WHERE kind IN ('checkin_general','checkin_game')`) != 1 {
		t.Fatal("choice changed more than one daily eligibility or activity count")
	}
}

func TestMutuallyExclusiveCheckinPolicyChangeAndZeroAward(t *testing.T) {
	f, user := mutualFixture(t)
	f.setConfig(mutuallyExclusiveKey, "0")
	f.configure(db.CheckinModeEnabled, "330", 0, 0, 0)
	if result, err := f.service.Checkin(context.Background(), user); err != nil || result.Award != "0" {
		t.Fatalf("zero award = %+v, %v", result, err)
	}
	f.setConfig(mutuallyExclusiveKey, "1")
	if _, err := f.service.CheckinForAsset(context.Background(), user, ledger.Game); !errors.Is(err, ErrOtherCheckedIn) {
		t.Fatalf("prior successful zero award did not consume choice: %v", err)
	}
	f.setConfig(mutuallyExclusiveKey, "0")
	if _, err := f.service.CheckinForAsset(context.Background(), user, ledger.Game); err != nil {
		t.Fatalf("disabling choice did not restore independent eligibility: %v", err)
	}
	f.setConfig(mutuallyExclusiveKey, "1")
	if f.scalar(`SELECT (SELECT COUNT(*) FROM checkins)+(SELECT COUNT(*) FROM game_checkins)`) != 2 {
		t.Fatal("enabling choice removed existing awards")
	}
}

func TestMutuallyExclusiveCheckinFailureDoesNotConsumeChoice(t *testing.T) {
	f, user := mutualFixture(t)
	before := f.scalar(`SELECT last_ledger_seq FROM credit_capacity WHERE id=1`)
	if _, err := f.database.Exec(`UPDATE credit_capacity SET last_ledger_seq=? WHERE id=1`, int64(math.MaxInt64)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.Checkin(context.Background(), user); !errors.Is(err, ErrResourceLimit) {
		t.Fatalf("injected failure = %v", err)
	}
	if f.scalar(`SELECT COUNT(*) FROM identity_continuity_facts WHERE kind IN ('checkin_general','checkin_game')`) != 0 {
		t.Fatal("failed award retained eligibility")
	}
	if _, err := f.database.Exec(`UPDATE credit_capacity SET last_ledger_seq=? WHERE id=1`, before); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.CheckinForAsset(context.Background(), user, ledger.Game); err != nil {
		t.Fatal(err)
	}
}

func TestMutuallyExclusiveCheckinPreservedIdentityEligibility(t *testing.T) {
	f, user := mutualFixture(t)
	tx, err := f.database.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	day, _, err := readSiteDayAndConfig(context.Background(), tx, f.clock.Load(), checkinSource{mode: checkinModeKey, minimum: awardMinimumKey, maximum: awardMaximumKey, cap: balanceCapKey})
	if err != nil {
		t.Fatal(err)
	}
	expires := day.activityDay + 86400
	if ok, err := continuity.ClaimEligibilityTx(context.Background(), tx, user, continuity.CheckinGeneral, "v1", day.siteDate, f.clock.Load(), &expires); err != nil || !ok {
		t.Fatalf("preserved eligibility = %v, %v", ok, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	oldUser := user
	if _, err := f.database.Exec(`DELETE FROM users WHERE id=?`, oldUser); err != nil {
		t.Fatal(err)
	}
	user = f.seedUser("daily-choice")
	if user == oldUser {
		t.Fatal("new account reused the deleted account ID")
	}
	status, err := f.service.StatusForAsset(context.Background(), user, ledger.Game)
	if err != nil || !status.BlockedByOtherCheckin || status.CheckedInToday {
		t.Fatalf("preserved status = %+v, %v", status, err)
	}
	if _, err := f.service.CheckinForAsset(context.Background(), user, ledger.Game); !errors.Is(err, ErrOtherCheckedIn) {
		t.Fatalf("preserved claim = %v", err)
	}
	if f.scalar(`SELECT COUNT(*) FROM checkins WHERE user_id=?`, user) != 0 {
		t.Fatal("fixture unexpectedly has old account check-in rows")
	}
}

func TestMutuallyExclusiveCheckinHTTPAndDisabledMode(t *testing.T) {
	f, user := mutualFixture(t)
	routes := &capturedUserRoutes{}
	if err := RegisterRoutes(routes, f.service); err != nil {
		t.Fatal(err)
	}
	if response := invokeCheckinRoute(t, routes, http.MethodPost, Route, nil, user); response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	response := invokeCheckinRoute(t, routes, http.MethodGet, GameRoute, nil, user)
	var status map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if response.Code != 200 || status["mutually_exclusive"] != true || status["blocked_by_other_checkin"] != true || status["checked_in_today"] != false {
		t.Fatal(response.Body.String())
	}
	if response := invokeCheckinRoute(t, routes, http.MethodPost, GameRoute, nil, user); response.Code != 409 {
		t.Fatalf("duplicate status = %d %s", response.Code, response.Body.String())
	}
	configureGameCheckin(f, db.CheckinModeDisabled, 2000, 2000, 0)
	response = invokeCheckinRoute(t, routes, http.MethodGet, GameRoute, nil, user)
	if response.Code != 200 || response.Body.String() != "{\"enabled\":false,\"mutually_exclusive\":true}\n" {
		t.Fatalf("disabled status = %d %s", response.Code, response.Body.String())
	}
}
