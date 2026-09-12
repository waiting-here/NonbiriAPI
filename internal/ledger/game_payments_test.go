package ledger

import (
	"context"
	"errors"
	"testing"
)

func TestGameEntryPostsOnlyAvailableWallets(t *testing.T) {
	for _, tc := range []struct {
		name                                         string
		general, game, cost, afterGeneral, afterGame int64
		insufficient                                 bool
	}{
		{"mixed", 10, 4, 8, 6, 0, false},
		{"general debt", -10, 4, 3, -10, 1, false},
		{"game debt", 10, -4, 3, 7, -4, false},
		{"free with debt", -10, -4, 0, -10, -4, false},
		{"insufficient", 2, 4, 8, 2, 4, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := openLedgerTestStore(t)
			ctx := context.Background()
			tx := beginLedgerTestTx(t, store.DB())
			userID, general := seedLedgerUser(t, tx, "game-payment")
			game, err := CreateUserAssetAccount(ctx, tx, userID, Game, ledgerTestNow)
			if err != nil {
				t.Fatal(err)
			}
			external, err := CodedAccount(ctx, tx, "external")
			if err != nil {
				t.Fatal(err)
			}
			gameExternal, err := CodedAssetAccount(ctx, tx, "external", Game)
			if err != nil {
				t.Fatal(err)
			}
			platform, err := CodedAccount(ctx, tx, "platform")
			if err != nil {
				t.Fatal(err)
			}
			gamePlatform, err := CodedAssetAccount(ctx, tx, "platform", Game)
			if err != nil {
				t.Fatal(err)
			}
			meta := func() Meta {
				return Meta{OperationID: mustLedgerID(t, "op_"), ActorUserID: userID, CreatedAt: ledgerTestNow}
			}
			fund, err := NewAdminUserAdjustment(meta(), general.ID, external.ID, AmountFromMilli(tc.general), 0, Amount{}, "fund wallet")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := Apply(ctx, tx, fund); err != nil {
				t.Fatal(err)
			}
			fund, err = NewAdminGameAdjustment(meta(), game.ID, gameExternal.ID, AmountFromMilli(tc.game), "fund wallet")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := Apply(ctx, tx, fund); err != nil {
				t.Fatal(err)
			}
			payment, err := SplitGamePayment(AmountFromMilli(tc.cost), AmountFromMilli(tc.general), AmountFromMilli(tc.game))
			if tc.insufficient {
				if !errors.Is(err, ErrInsufficientBalance) {
					t.Fatalf("payment error=%v", err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				plan, err := NewLinkLinkEntryWithPayment(meta(), mustLedgerID(t, "ll_"), AccountPair{general.ID, game.ID}, AccountPair{platform.ID, gamePlatform.ID}, payment)
				if err != nil {
					t.Fatal(err)
				}
				first, err := Apply(ctx, tx, plan)
				if err != nil || len(first.Entries) != 4 {
					t.Fatalf("entry result=%+v err=%v", first, err)
				}
				for _, entry := range first.Entries {
					if entry.AccountKind == AccountUser {
						want := payment.General
						if entry.Asset == Game {
							want = payment.Game
						}
						if entry.Delta.Decimal() != negate(want).Decimal() {
							t.Fatalf("wrong debit=%+v", entry)
						}
					}
				}
				replay, err := Apply(ctx, tx, plan)
				if err != nil || replay.LedgerSeq != first.LedgerSeq {
					t.Fatalf("replay=%+v err=%v", replay, err)
				}
			}
			for id, want := range map[int64]int64{general.ID: tc.afterGeneral, game.ID: tc.afterGame} {
				account, err := ReadAccount(ctx, tx, id)
				if err != nil || account.Balance.Decimal() != AmountFromMilli(want).Decimal() {
					t.Fatalf("balance=%+v want=%d err=%v", account, want, err)
				}
			}
			if err := ValidateRecovery(ctx, tx); err != nil {
				t.Fatal(err)
			}
		})
	}
}
