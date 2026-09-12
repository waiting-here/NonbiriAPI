package adminusers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

func TestAdminGameBalanceAdjustmentKeepsGeneralAndDonationCredit(t *testing.T) {
	fixture := newAdminUsersFixture(t)
	userID := fixture.seedUser("game-adjustment", false)
	path := fmt.Sprintf("https://admin.example/admin/api/users/%d", userID)
	body := `{"mode":"economy","expected_revision":"1","target":"game_balance","direction":"decrease","amount":"0.001","reason":"Game credit correction"}`
	key := "ABCDEFGHIJKLMNOPQRSTG1"
	response := fixture.request(http.MethodPatch, routeUser, path, body, userID, key)
	if response.Code != http.StatusOK {
		t.Fatalf("adjustment: %d %s", response.Code, response.Body.String())
	}
	var user AdminUser
	if err := json.Unmarshal(response.Body.Bytes(), &user); err != nil {
		t.Fatal(err)
	}
	if user.Balance != "0" || user.GameBalance != "-0.001" || user.DonationCredit != "0" || user.Revision != "2" {
		t.Fatalf("wallets: %+v", user)
	}
	replay := fixture.request(http.MethodPatch, routeUser, path, body, userID, key)
	if replay.Code != http.StatusOK || !bytes.Equal(replay.Body.Bytes(), response.Body.Bytes()) {
		t.Fatalf("replay: %d %s", replay.Code, replay.Body.String())
	}
	var operations, generalEntries, gameEntries int
	if err := fixture.store.DB().QueryRow(`SELECT COUNT(*) FROM credit_operations WHERE kind='admin_user_adjustment'`).Scan(&operations); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.DB().QueryRow(`SELECT COUNT(*) FROM credit_entries WHERE asset_type='general'`).Scan(&generalEntries); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.DB().QueryRow(`SELECT COUNT(*) FROM credit_entries WHERE asset_type='game'`).Scan(&gameEntries); err != nil {
		t.Fatal(err)
	}
	if operations != 1 || generalEntries != 0 || gameEntries != 2 {
		t.Fatalf("ledger: operations=%d general=%d game=%d", operations, generalEntries, gameEntries)
	}
}
