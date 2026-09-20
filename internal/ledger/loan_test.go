package ledger

import (
	"context"
	"errors"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

func TestLoanAllowsDebtOnceAndReplaysWithoutDoublePosting(t *testing.T) {
	ctx := context.Background()
	store := openLedgerTestStore(t)
	tx := beginLedgerTestTx(t, store.DB())
	user, general := seedLedgerUser(t, tx, "borrower")
	game, err := CreateUserAssetAccount(ctx, tx, user, Game, ledgerTestNow)
	if err != nil {
		t.Fatal(err)
	}
	ext, err := CodedAccount(ctx, tx, "external")
	if err != nil {
		t.Fatal(err)
	}
	gameExt, err := CodedAssetAccount(ctx, tx, "external", Game)
	if err != nil {
		t.Fatal(err)
	}
	meta := func() Meta {
		return Meta{OperationID: mustLedgerID(t, "op_"), ActorUserID: user, CreatedAt: ledgerTestNow}
	}
	plan, err := NewActivityLoan(meta(), AccountPair{general.ID, game.ID}, AccountPair{ext.ID, gameExt.ID}, AmountFromMilli(9000000), AmountFromMilli(13000000))
	if err != nil {
		t.Fatal(err)
	}
	first, err := Apply(ctx, tx, plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Entries) != 4 || first.Kind != KindActivityLoan {
		t.Fatal(first)
	}
	replay, err := Apply(ctx, tx, plan)
	if err != nil || replay.OperationID != first.OperationID || replay.LedgerSeq != first.LedgerSeq {
		t.Fatal("replay", replay, err)
	}
	general, err = UserAccount(ctx, tx, user)
	if err != nil || general.Balance.Decimal() != "-13000000" {
		t.Fatal(general, err)
	}
	game, err = UserAssetAccount(ctx, tx, user, Game)
	if err != nil || game.Balance.Decimal() != "9000000" {
		t.Fatal(game, err)
	}
	next, err := NewActivityLoan(meta(), AccountPair{general.ID, game.ID}, AccountPair{ext.ID, gameExt.ID}, AmountFromMilli(9000000), AmountFromMilli(13000000))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(ctx, tx, next); !errors.Is(err, ErrInsufficientBalance) {
		t.Fatal("negative borrower accepted", err)
	}
	if err := db.ValidateAssetLedger(ctx, tx); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := tx.QueryRow(`SELECT count(*) FROM credit_operations WHERE kind='activity_loan'`).Scan(&count); err != nil || count != 1 {
		t.Fatal("partial or duplicate loan", count, err)
	}
}

func TestDonationAchievementUsesNonzeroLedgerOrder(t *testing.T) {
	ctx := context.Background()
	store := openLedgerTestStore(t)
	tx := beginLedgerTestTx(t, store.DB())
	user, wallet := seedLedgerUser(t, tx, "achievement")
	ext, err := CodedAccount(ctx, tx, "external")
	if err != nil {
		t.Fatal(err)
	}
	var last int64
	for _, delta := range []int64{100000, 100000, -100000, 0} {
		plan, err := NewAdminUserAdjustment(Meta{OperationID: mustLedgerID(t, "op_"), ActorUserID: user, CreatedAt: ledgerTestNow}, wallet.ID, ext.ID, Amount{}, user, AmountFromMilli(delta), "adjustment")
		if err != nil {
			t.Fatal(err)
		}
		result, err := Apply(ctx, tx, plan)
		if err != nil {
			t.Fatal(err)
		}
		if delta != 0 {
			last = result.LedgerSeq
		}
		var at int64
		var seqRaw []byte
		if err := tx.QueryRow(`SELECT donation_credit_achieved_at,donation_credit_achieved_seq FROM users WHERE id=?`, user).Scan(&at, &seqRaw); err != nil {
			t.Fatal(err)
		}
		seq, err := db.DecodeU128(seqRaw)
		if err != nil || seq.Big().Int64() != last || at != ledgerTestNow {
			t.Fatal(at, seq, err)
		}
	}
}
