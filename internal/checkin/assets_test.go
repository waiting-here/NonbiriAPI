package checkin

import (
	"context"
	"errors"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

func configureGameCheckin(f *checkinFixture, mode string, minimum, maximum, cap int64) {
	f.t.Helper()
	f.setConfig("game_checkin_mode", mode)
	f.setConfig("game_checkin_award_min_milli", big.NewInt(minimum).String())
	f.setConfig("game_checkin_award_max_milli", big.NewInt(maximum).String())
	f.setConfig("game_credits_cap_milli", big.NewInt(cap).String())
}

func TestTwoAssetCheckinsAreIndependentAndCountOneActiveUser(t *testing.T) {
	f := newCheckinFixture(t)
	f.configure(db.CheckinModeEnabled, "330", 1000, 1000, 1000)
	configureGameCheckin(f, db.CheckinModeEnabled, 2000, 2000, 1000)
	user := f.seedUser("two-wallets")
	start := make(chan struct{})
	var wait sync.WaitGroup
	results := make([]error, 6)
	for index := range results {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			asset := ledger.General
			if index%2 == 1 {
				asset = ledger.Game
			}
			_, results[index] = f.service.CheckinForAsset(context.Background(), user, asset)
		}()
	}
	close(start)
	wait.Wait()
	winners := map[ledger.Asset]int{}
	for index, err := range results {
		asset := ledger.General
		if index%2 == 1 {
			asset = ledger.Game
		}
		if err == nil {
			winners[asset]++
		} else if !errors.Is(err, ErrAlreadyCheckedIn) && !errors.Is(err, ErrBalanceCap) {
			t.Fatalf("unexpected concurrent result: %v", err)
		}
	}
	if winners[ledger.General] != 1 || winners[ledger.Game] != 1 {
		t.Fatalf("winners=%v", winners)
	}
	day := db.SiteDayKey(f.clock.Load(), 330)
	for _, column := range []string{"checkins", "game_checkins"} {
		if got := f.scalar("SELECT "+column+" FROM user_activity_daily WHERE day=? AND user_id=?", day, user); got != 1 {
			t.Fatalf("%s user count=%d", column, got)
		}
		if got := f.u128("SELECT "+column+" FROM site_activity_daily WHERE day=?", day); got.String() != "1" {
			t.Fatalf("%s site count=%s", column, got)
		}
	}
	if f.u128("SELECT distinct_product_users FROM site_activity_daily WHERE day=?", day).String() != "1" {
		t.Fatal("two checkins counted two distinct users")
	}
	tx, err := f.database.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for asset, want := range map[ledger.Asset]string{ledger.General: "1000", ledger.Game: "2000"} {
		wallet, err := ledger.UserAssetAccount(context.Background(), tx, user, asset)
		if err != nil || wallet.Balance.Decimal() != want {
			t.Fatalf("wallet %s=%s err=%v", asset, wallet.Balance.Decimal(), err)
		}
		var sum int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM credit_entries e JOIN credit_operations o ON o.id=e.operation_id WHERE e.account_id=? AND e.asset_type=? AND o.kind='checkin_award'`, wallet.ID, asset).Scan(&sum); err != nil || sum != 1 {
			t.Fatalf("asset entries=%d err=%v", sum, err)
		}
	}
	if err := ledger.ValidateRecovery(context.Background(), tx); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}

	f.clock.Add(24 * 60 * 60)
	for _, asset := range []ledger.Asset{ledger.General, ledger.Game} {
		if _, err := f.service.CheckinForAsset(context.Background(), user, asset); !errors.Is(err, ErrBalanceCap) {
			t.Fatalf("%s next-day cap=%v", asset, err)
		}
	}
	configureGameCheckin(f, db.CheckinModeEnabled, 0, 0, 0)
	result, err := f.service.CheckinForAsset(context.Background(), user, ledger.Game)
	if err != nil || result.Asset != ledger.Game || result.Award != "0" || result.Balance != "2" {
		t.Fatalf("uncapped zero game award=%+v err=%v", result, err)
	}
	if f.scalar("SELECT COUNT(*) FROM game_checkins WHERE user_id=?", user) != 2 {
		t.Fatal("second game day missing")
	}
}

func TestGameCheckinClosedWireAndEligibility(t *testing.T) {
	f := newCheckinFixture(t)
	f.configure(db.CheckinModeEnabled, "0", 1, 1, 0)
	user := f.seedUser("game-wire")
	routes := &capturedUserRoutes{}
	if err := RegisterRoutes(routes, f.service); err != nil {
		t.Fatal(err)
	}
	status := invokeCheckinRoute(t, routes, http.MethodGet, GameRoute, nil, user)
	if status.Code != 200 || strings.TrimSpace(status.Body.String()) != `{"enabled":false}` {
		t.Fatalf("disabled status=%d %s", status.Code, status.Body.String())
	}
	configureGameCheckin(f, db.CheckinModeLevelGated, 0, 0, 0)
	denied := invokeCheckinRoute(t, routes, http.MethodPost, GameRoute, nil, user)
	if denied.Code != 403 || f.scalar("SELECT COUNT(*) FROM game_checkins") != 0 {
		t.Fatalf("level denial=%d %s", denied.Code, denied.Body.String())
	}
	if _, err := f.database.Exec("UPDATE users SET level=3 WHERE id=?", user); err != nil {
		t.Fatal(err)
	}
	award := invokeCheckinRoute(t, routes, http.MethodPost, GameRoute, nil, user)
	if award.Code != 200 || !strings.Contains(award.Body.String(), `"asset_type":"game"`) || !strings.Contains(award.Body.String(), `"award":"0"`) {
		t.Fatalf("game award=%d %s", award.Code, award.Body.String())
	}
	duplicate := invokeCheckinRoute(t, routes, http.MethodPost, GameRoute, nil, user)
	if duplicate.Code != 409 {
		t.Fatalf("duplicate=%d %s", duplicate.Code, duplicate.Body.String())
	}
	if _, err := f.service.Checkin(context.Background(), user); err != nil {
		t.Fatalf("game did not leave general slot: %v", err)
	}
	if err := f.store.SetSiteTimezoneOffsetMinutes(30); !errors.Is(err, db.ErrConflict) {
		t.Fatalf("timezone not frozen: %v", err)
	}
}
