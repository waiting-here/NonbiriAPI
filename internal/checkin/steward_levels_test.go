package checkin

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

func TestStewardCheckinHTTPBothAssetsAndModes(t *testing.T) {
	for _, level := range []int{5, 6} {
		for _, mode := range []string{db.CheckinModeEnabled, db.CheckinModeLevelGated} {
			for _, route := range []string{Route, GameRoute} {
				t.Run(fmt.Sprintf("level=%d/%s%s", level, mode, route), func(t *testing.T) {
					f := newCheckinFixture(t)
					f.configure(mode, "0", 1250, 1250, 9000)
					configureGameCheckin(f, mode, 1250, 1250, 9000)
					user := f.seedUser("steward-checkin")
					if _, err := f.database.Exec(`UPDATE users SET level=? WHERE id=?`, level, user); err != nil {
						t.Fatal(err)
					}
					routes := &capturedUserRoutes{}
					if err := RegisterRoutes(routes, f.service); err != nil {
						t.Fatal(err)
					}
					status := invokeCheckinRoute(t, routes, http.MethodGet, route, nil, user)
					if status.Code != http.StatusOK || !strings.Contains(status.Body.String(), `"enabled":true`) {
						t.Fatalf("status=%d %s", status.Code, status.Body.String())
					}
					award := invokeCheckinRoute(t, routes, http.MethodPost, route, nil, user)
					if award.Code != http.StatusOK || !strings.Contains(award.Body.String(), `"award":"1.25"`) {
						t.Fatalf("award=%d %s", award.Code, award.Body.String())
					}
					table := "checkins"
					asset := "general"
					if route == GameRoute {
						table = "game_checkins"
						asset = "game"
					}
					operations := f.scalar(`SELECT count(*) FROM credit_operations`)
					duplicate := invokeCheckinRoute(t, routes, http.MethodPost, route, nil, user)
					if duplicate.Code != http.StatusConflict || f.scalar("SELECT count(*) FROM "+table+" WHERE user_id=?", user) != 1 {
						t.Fatal("duplicate consumed another daily slot")
					}
					f.setConfig("credits_cap_milli", "1000")
					f.setConfig("game_credits_cap_milli", "1000")
					f.clock.Add(86400)
					capped := invokeCheckinRoute(t, routes, http.MethodPost, route, nil, user)
					if capped.Code == http.StatusOK || capped.Code >= 500 {
						t.Fatalf("cap=%d %s", capped.Code, capped.Body.String())
					}
					if operations != f.scalar(`SELECT count(*) FROM credit_operations`) || f.scalar("SELECT count(*) FROM "+table+" WHERE user_id=?", user) != 1 {
						t.Fatal("cap wrote ledger or daily facts")
					}
					if f.u128(`SELECT balance_mag FROM credit_accounts WHERE user_id=? AND asset_type=?`, user, asset).String() != "1250" {
						t.Fatal("balance changed after refusal")
					}
				})
			}
		}
	}
}
