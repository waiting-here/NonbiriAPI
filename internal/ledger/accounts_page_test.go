package ledger

import (
	"errors"
	"testing"
)

func TestUserAssetAccountsKeepPageOwnershipAndAsset(t *testing.T) {
	store := openLedgerTestStore(t)
	tx := beginLedgerTestTx(t, store.DB())
	first, general := seedLedgerUser(t, tx, "wallet-page-first")
	second, _ := seedLedgerUser(t, tx, "wallet-page-second")
	game, err := CreateUserAssetAccount(t.Context(), tx, first, Game, ledgerTestNow)
	if err != nil {
		t.Fatal(err)
	}
	accounts, err := UserAssetAccounts(t.Context(), tx, []int64{second, first, first}, General)
	if err != nil || len(accounts) != 2 || accounts[first].ID != general.ID || accounts[second].UserID != second {
		t.Fatal(accounts, err)
	}
	games, err := UserAssetAccounts(t.Context(), tx, []int64{first}, Game)
	if err != nil || len(games) != 1 || games[first].ID != game.ID {
		t.Fatal(games, err)
	}
	if _, err := UserAssetAccounts(t.Context(), tx, []int64{first, second + 100}, General); !errors.Is(err, ErrNotFound) {
		t.Fatal("missing wallet", err)
	}
	empty, err := UserAssetAccounts(t.Context(), tx, nil, General)
	if err != nil || len(empty) != 0 {
		t.Fatal(empty, err)
	}
}
