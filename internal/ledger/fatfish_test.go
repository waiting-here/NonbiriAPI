package ledger

import (
	"context"
	"errors"
	"math/big"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

func TestFatFishWideCreditsConserveReplayAndRetire(t *testing.T) {
	store := openLedgerTestStore(t)
	ctx := context.Background()
	tx := beginLedgerTestTx(t, store.DB())
	defer tx.Rollback()
	user, wallet := seedLedgerUser(t, tx, "wide-fish")
	game, err := CreateUserAssetAccount(ctx, tx, user, Game, ledgerTestNow)
	if err != nil {
		t.Fatal(err)
	}
	external, err := CodedAccount(ctx, tx, "external")
	if err != nil {
		t.Fatal(err)
	}
	platform, err := CodedAccount(ctx, tx, "platform")
	if err != nil {
		t.Fatal(err)
	}
	meta := func(actor int64) Meta {
		return Meta{OperationID: mustLedgerID(t, "op_"), ActorUserID: actor, CreatedAt: ledgerTestNow}
	}
	apply := func(plan Plan, err error) Result {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		result, err := Apply(ctx, tx, plan)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range result.Entries {
			if entry.Asset != General {
				t.Fatalf("unexpected asset %s", entry.Asset)
			}
		}
		return result
	}
	wide, err := AmountFromBig(new(big.Int).Lsh(big.NewInt(1), 100))
	if err != nil {
		t.Fatal(err)
	}
	rewardMeta := meta(0)
	reward := apply(NewFatFishReward(rewardMeta, external.ID, wallet.ID, wide))
	unlock := apply(NewFatFishUnlock(meta(user), wallet.ID, platform.ID, wide))
	if unlock.Kind != KindFatFishUnlock || reward.Kind != KindFatFishReward {
		t.Fatal("wrong operation kind")
	}
	apply(NewFatFishRefund(meta(0), platform.ID, wallet.ID, wide))
	ticket := apply(NewFatFishTicket(meta(user), wallet.ID, platform.ID, wide))
	refundMeta := meta(0)
	refund := apply(NewFatFishRefund(refundMeta, platform.ID, wallet.ID, wide))
	replay := apply(NewFatFishRefund(refundMeta, platform.ID, wallet.ID, wide))
	if replay.LedgerSeq != refund.LedgerSeq {
		t.Fatal("refund charged twice")
	}
	if ticket.Kind != KindFatFishTicket || refund.Kind != KindFatFishRefund {
		t.Fatal("wrong operation kind")
	}
	for _, kind := range []Kind{KindFatFishUnlock, KindFatFishTicket, KindFatFishReward, KindFatFishRefund} {
		if classification := ClassifyForAudit(kind, ""); !classification.Known || classification.Channel != "fat_fish" {
			t.Fatalf("unclassified %s", kind)
		}
	}
	page, err := UserHistory(ctx, tx, user, ledgerTestNow, HistoryFilter{Category: "fat_fish", Page: 1, PageSize: 20})
	if err != nil || page.Total != "5" {
		t.Fatalf("history = %+v, %v", page, err)
	}
	gameAfter, err := ReadAccount(ctx, tx, game.ID)
	if err != nil || !gameAfter.Balance.IsZero() {
		t.Fatalf("other asset changed: %+v %v", gameAfter, err)
	}
	if err := ValidateRecovery(ctx, tx); err != nil {
		t.Fatal(err)
	}
	apply(NewAccountDeleteZero(meta(user), wallet.ID, external.ID))
	if _, err := tx.ExecContext(ctx, `DELETE FROM credit_accounts WHERE id=?`, wallet.ID); err != nil {
		t.Fatal(err)
	}
	replayedReward := apply(NewFatFishReward(rewardMeta, external.ID, wallet.ID, wide))
	if replayedReward.LedgerSeq != reward.LedgerSeq {
		t.Fatal("retired reward was reissued")
	}
	for _, entry := range replayedReward.Entries {
		if entry.AccountKind == AccountUser && entry.AccountID != 0 {
			t.Fatal("retired account was restored")
		}
	}
}

func TestFatFishPaymentBoundsAndAccountRoles(t *testing.T) {
	store := openLedgerTestStore(t)
	ctx := context.Background()
	tx := beginLedgerTestTx(t, store.DB())
	defer tx.Rollback()
	user, wallet := seedLedgerUser(t, tx, "fish-bounds")
	other, otherWallet := seedLedgerUser(t, tx, "fish-other")
	game, err := CreateUserAssetAccount(ctx, tx, user, Game, ledgerTestNow)
	if err != nil {
		t.Fatal(err)
	}
	external, err := CodedAccount(ctx, tx, "external")
	if err != nil {
		t.Fatal(err)
	}
	platform, err := CodedAccount(ctx, tx, "platform")
	if err != nil {
		t.Fatal(err)
	}
	meta := func(actor int64) Meta {
		return Meta{OperationID: mustLedgerID(t, "op_"), ActorUserID: actor, CreatedAt: ledgerTestNow}
	}
	checkFailure := func(plan Plan, err, want error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		before, err := ReadCapacity(ctx, tx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Apply(ctx, tx, plan); !errors.Is(err, want) {
			t.Fatalf("apply error = %v, want %v", err, want)
		}
		after, err := ReadCapacity(ctx, tx)
		if err != nil || after != before {
			t.Fatalf("failed operation changed capacity: %+v -> %+v, %v", before, after, err)
		}
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM credit_operations WHERE id=?`, plan.spec.meta.OperationID).Scan(&count); err != nil || count != 0 {
			t.Fatalf("failed header exists: %d, %v", count, err)
		}
	}
	plan, err := NewFatFishTicket(meta(user), wallet.ID, platform.ID, AmountFromMilli(1))
	checkFailure(plan, err, ErrInsufficientBalance)
	plan, err = NewFatFishUnlock(meta(user), game.ID, platform.ID, Amount{})
	checkFailure(plan, err, ErrInvalidPlan)
	plan, err = NewFatFishUnlock(meta(user), otherWallet.ID, platform.ID, Amount{})
	checkFailure(plan, err, ErrInvalidPlan)
	plan, err = NewFatFishRefund(meta(0), external.ID, wallet.ID, Amount{})
	checkFailure(plan, err, ErrInvalidPlan)
	if _, err := NewFatFishReward(meta(other), external.ID, wallet.ID, Amount{}); !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("non-system reward = %v", err)
	}
	if _, err := NewFatFishTicket(meta(user), wallet.ID, platform.ID, AmountFromMilli(-1)); !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("negative ticket = %v", err)
	}
	penalty, err := NewAntiAbusePenalty(meta(user), wallet.ID, external.ID, AmountFromMilli(1), "penalty")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(ctx, tx, penalty); err != nil {
		t.Fatal(err)
	}
	zero, err := NewFatFishTicket(meta(user), wallet.ID, platform.ID, Amount{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(ctx, tx, zero); err != nil {
		t.Fatalf("free ticket with debt = %v", err)
	}
	max := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 127), big.NewInt(1))
	maximum, err := AmountFromBig(max)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE credit_accounts SET balance_sign=1,balance_mag=? WHERE id=?`, db.EncodeU128(maximum.value.Mag), wallet.ID); err != nil {
		t.Fatal(err)
	}
	plan, err = NewFatFishReward(meta(0), external.ID, wallet.ID, AmountFromMilli(1))
	checkFailure(plan, err, ErrInvariant)
	walletAfter, err := ReadAccount(ctx, tx, wallet.ID)
	if err != nil || walletAfter.Balance.Big().Cmp(max) != 0 {
		t.Fatalf("overflow mutated wallet: %+v, %v", walletAfter, err)
	}
}
