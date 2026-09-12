package ledger

import (
	"context"
	"errors"
	"testing"
)

func TestHistoryAndExportKeepBothAssetsWithinOneOperation(t *testing.T) {
	store := openLedgerTestStore(t)
	ctx := context.Background()
	tx := beginLedgerTestTx(t, store.DB())
	defer tx.Rollback()
	user, general := seedHistoryUser(t, tx, "wallet-history")
	game, err := UserAssetAccount(ctx, tx, user, Game)
	if err != nil {
		t.Fatal(err)
	}
	extGeneral, err := CodedAccount(ctx, tx, "external")
	if err != nil {
		t.Fatal(err)
	}
	extGame, err := CodedAssetAccount(ctx, tx, "external", Game)
	if err != nil {
		t.Fatal(err)
	}
	apply := func(plan Plan, err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Apply(ctx, tx, plan); err != nil {
			t.Fatal(err)
		}
	}
	generalID := mustLedgerID(t, "op_")
	apply(NewCheckinAward(Meta{OperationID: generalID, ActorUserID: user, CreatedAt: ledgerTestNow}, general.ID, extGeneral.ID, AmountFromMilli(1500)))
	apply(NewGameCheckinAward(Meta{OperationID: mustLedgerID(t, "op_"), ActorUserID: user, CreatedAt: ledgerTestNow}, game.ID, extGame.ID, AmountFromMilli(2500)))
	before, err := UserHistory(ctx, tx, user, ledgerTestNow, HistoryFilter{Page: 1, PageSize: 10, Asset: "all"})
	if err != nil || before.CurrentBalance != "1.5" || before.GameBalance != "2.5" {
		t.Fatalf("wallets: %+v %v", before, err)
	}
	mixedID := mustLedgerID(t, "op_")
	apply(NewWalletsDeleteZero(Meta{OperationID: mixedID, ActorUserID: user, CreatedAt: ledgerTestNow}, AccountPair{General: general.ID, Game: game.ID}, AccountPair{General: extGeneral.ID, Game: extGame.ID}))
	all, err := UserHistory(ctx, tx, user, ledgerTestNow, HistoryFilter{Page: 1, PageSize: 10, Asset: "all"})
	if err != nil || all.Total != "4" || len(all.Data) != 4 || all.Anchor == nil || *all.Anchor != mixedID {
		t.Fatalf("all: %+v %v", all, err)
	}
	if all.Data[0].OperationID != mixedID || all.Data[1].OperationID != mixedID ||
		all.Data[0].Asset == all.Data[1].Asset || all.Data[0].Line == all.Data[1].Line {
		t.Fatalf("mixed lines: %+v", all.Data)
	}
	for _, asset := range []string{"", "general", "game"} {
		page, err := UserHistory(ctx, tx, user, ledgerTestNow, HistoryFilter{Page: 1, PageSize: 10, Asset: asset, Anchor: mixedID})
		if err != nil || page.Total != "2" || len(page.Data) != 2 {
			t.Fatalf("filter %q: %+v %v", asset, page, err)
		}
		want := General
		if asset == "game" {
			want = Game
		}
		for _, entry := range page.Data {
			if entry.Asset != want {
				t.Fatalf("wrong asset: %+v", entry)
			}
		}
	}
	if _, err := UserHistory(ctx, tx, user, ledgerTestNow, HistoryFilter{Page: 1, PageSize: 10, Asset: "game", Anchor: generalID}); !errors.Is(err, ErrInvalidHistory) {
		t.Fatalf("foreign-asset anchor: %v", err)
	}
	entries, err := ExportUserEntries(ctx, tx, user, 4)
	if err != nil || len(entries) != 4 {
		t.Fatalf("export: %+v %v", entries, err)
	}
	counts := map[Asset]int{}
	for _, entry := range entries {
		counts[entry.Asset]++
	}
	if counts[General] != 2 || counts[Game] != 2 {
		t.Fatalf("export asset counts: %v", counts)
	}
	if _, err := ExportUserEntries(ctx, tx, user, 3); !errors.Is(err, ErrExportTooLarge) {
		t.Fatalf("combined export limit: %v", err)
	}
}
