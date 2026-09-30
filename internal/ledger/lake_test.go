package ledger

import (
	"context"
	"errors"
	"math/big"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

func TestLakePlansBalanceAssetsAndAvailableWallet(t *testing.T) {
	store := openLedgerTestStore(t)
	tx := beginLedgerTestTx(t, store.DB())
	ctx := context.Background()
	user, general := seedLedgerUser(t, tx, "lake")
	game, e := CreateUserAssetAccount(ctx, tx, user, Game, ledgerTestNow)
	if e != nil {
		t.Fatal(e)
	}
	external, e := CodedAssetAccount(ctx, tx, "external", General)
	if e != nil {
		t.Fatal(e)
	}
	gameExternal, e := CodedAssetAccount(ctx, tx, "external", Game)
	if e != nil {
		t.Fatal(e)
	}
	fund, e := NewAdminUserAdjustment(Meta{mustLedgerID(t, "op_"), user, ledgerTestNow}, general.ID, external.ID, AmountFromMilli(1000), 0, Amount{}, "test funding")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = Apply(ctx, tx, fund); e != nil {
		t.Fatal(e)
	}
	entry, e := NewLakeEntry(Meta{mustLedgerID(t, "op_"), user, ledgerTestNow}, general.ID, external.ID, AmountFromMilli(250))
	if e != nil {
		t.Fatal(e)
	}
	posted, e := Apply(ctx, tx, entry)
	if e != nil || len(posted.Entries) != 2 {
		t.Fatal(posted, e)
	}
	spend, e := NewLakeExchange(Meta{mustLedgerID(t, "op_"), user, ledgerTestNow}, general.ID, external.ID, General, AmountFromMilli(-751))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = Apply(ctx, tx, spend); !errors.Is(e, ErrInsufficientBalance) {
		t.Fatal(e)
	}
	issue, e := NewLakeExchange(Meta{mustLedgerID(t, "op_"), user, ledgerTestNow}, game.ID, gameExternal.ID, Game, AmountFromMilli(250))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = Apply(ctx, tx, issue); e != nil {
		t.Fatal(e)
	}
	current, e := UserAccount(ctx, tx, user)
	if e != nil || current.Balance.Decimal() != "750" {
		t.Fatal(current, e)
	}
	if c := ClassifyForAudit(KindLakeEntry, ""); !c.Known || c.Channel != "lake_notes" || c.Behavior != "fee" {
		t.Fatal(c)
	}
	if c := ClassifyForAudit(KindLakeExchange, ""); !c.Known || c.Channel != "lake_notes" || c.Behavior != "exchange" {
		t.Fatal(c)
	}
	if e = ValidateHistoryFilter(HistoryFilter{Page: 1, PageSize: 20, Category: "lake_notes"}); e != nil {
		t.Fatal(e)
	}
}
func TestLakePlanRejectsWrongAssetAndPrimitiveOverflow(t *testing.T) {
	meta := Meta{mustLedgerID(t, "op_"), 1, ledgerTestNow}
	if _, e := NewLakeEntry(meta, 1, 2, Amount{}); !errors.Is(e, ErrInvalidPlan) {
		t.Fatal(e)
	}
	if _, e := NewLakeExchange(meta, 1, 2, SketchPaper, AmountFromMilli(1000)); !errors.Is(e, ErrInvalidPlan) {
		t.Fatal(e)
	}
	bigAmount, _ := AmountFromBig(new(big.Int).Add(big.NewInt(db.MaxMoneyMilli), big.NewInt(1)))
	for _, value := range []Amount{bigAmount, negate(bigAmount)} {
		if _, e := NewLakeExchange(meta, 1, 2, General, value); !errors.Is(e, ErrInvalidPlan) {
			t.Fatal(e)
		}
	}
}
