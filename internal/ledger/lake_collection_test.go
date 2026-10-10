package ledger

import (
	"context"
	"testing"
)

func TestLakeCollectionRewardDonationOnlyReplayAndHistory(t *testing.T) {
	store := openLedgerTestStore(t)
	tx := beginLedgerTestTx(t, store.DB())
	defer tx.Rollback()
	ctx := context.Background()
	user, general := seedLedgerUser(t, tx, "lake-collection")
	other, otherWallet := seedLedgerUser(t, tx, "lake-collection-other")
	if _, err := CreateUserAssetAccount(ctx, tx, other, Game, ledgerTestNow); err != nil {
		t.Fatal(err)
	}
	game, err := CreateUserAssetAccount(ctx, tx, user, Game, ledgerTestNow)
	if err != nil {
		t.Fatal(err)
	}
	meta := Meta{OperationID: mustLedgerID(t, "op_"), ActorUserID: user, CreatedAt: ledgerTestNow}
	plan, err := NewLakeCollectionReward(meta, general.ID)
	if err != nil {
		t.Fatal(err)
	}
	first, err := Apply(ctx, tx, plan)
	if err != nil || len(first.Entries) != 0 || first.DonationCreditAfter == nil || first.DonationCreditAfter.Decimal() != "50000" {
		t.Fatal(first, err)
	}
	replay, err := Apply(ctx, tx, plan)
	if err != nil || replay.LedgerSeq != first.LedgerSeq || replay.DonationCreditAfter.Decimal() != "50000" {
		t.Fatal(replay, err)
	}
	for _, account := range []int64{general.ID, game.ID, otherWallet.ID} {
		current, err := readAccount(ctx, tx, account)
		if err != nil || !current.Balance.IsZero() {
			t.Fatal(current, err)
		}
	}
	page, err := UserHistory(ctx, tx, user, ledgerTestNow, HistoryFilter{Page: 1, PageSize: 20, Category: "lake_notes"})
	if err != nil || len(page.Data) != 0 {
		t.Fatal(page, err)
	}
	foreign, err := UserHistory(ctx, tx, other, ledgerTestNow, HistoryFilter{Page: 1, PageSize: 20, Category: "lake_notes"})
	if err != nil || len(foreign.Data) != 0 {
		t.Fatal(foreign, err)
	}
	c := ClassifyForAudit(KindLakeCollectionReward, "")
	if !c.Known || c.Channel != "lake_notes" || c.Behavior != "reward" {
		t.Fatal(c)
	}
}
