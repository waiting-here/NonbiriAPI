package runtime

import (
	"context"
	"errors"
	"math/big"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/game"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

func TestFishingMixedPaymentAndOriginalRelease(t *testing.T) {
	for _, tc := range []struct {
		name                                 string
		general, game, wantGeneral, wantGame int64
		release, insufficient                bool
	}{
		{"mixed", 2_000_000, 1_000_000, 500_000, 0, false, false},
		{"general-debt", -1_000, 3_000_000, -1_000, 500_000, false, false},
		{"game-debt", 3_000_000, -1_000, 500_000, -1_000, false, false},
		{"insufficient", 1_000_000, 1_000_000, 1_000_000, 1_000_000, false, true},
		{"release", 2_000_000, 1_000_000, 2_000_000, 1_000_000, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newGameFixture(t, &scriptedSource{})
			user := fixture.seedUser("payment-"+tc.name, tc.general)
			ctx := context.Background()
			tx, err := fixture.database.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			wallet, err := ledger.UserAssetAccount(ctx, tx, user, ledger.Game)
			if err != nil {
				t.Fatal(err)
			}
			external, err := ledger.CodedAssetAccount(ctx, tx, "external", ledger.Game)
			if err != nil {
				t.Fatal(err)
			}
			plan, err := ledger.NewAdminGameAdjustment(ledger.Meta{OperationID: fixture.mustID("op_"), ActorUserID: fixture.adminID, CreatedAt: fixture.clock.Load()}, wallet.ID, external.ID, ledger.AmountFromMilli(tc.game), "game wallet funding")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := ledger.Apply(ctx, tx, plan); err != nil {
				t.Fatal(err)
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			if tc.release {
				fixture.service.beforeSettlement = func(string) error { return errInjected }
			}
			input := StartInput{UserID: user, Bait: "worm", Count: 1, IdempotencyKey: validTestKey(9901)}
			result, pending, err := fixture.service.startFishing(ctx, input, 2)
			if tc.insufficient {
				if !errors.Is(err, ErrInsufficientCredits) || result != nil || pending != nil || fixture.scalar(`SELECT COUNT(*) FROM game_fishing_batches`) != 0 {
					t.Fatalf("insufficient=%+v %+v %v", result, pending, err)
				}
			} else if tc.release {
				if err != nil || result != nil || pending == nil || pending.RulesVersion != 2 || pending.Payment != (game.Payment{General: "1500", Game: "1000"}) {
					t.Fatalf("pending=%+v err=%v", pending, err)
				}
				tx, err := fixture.database.BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				if err := fixture.service.Lifecycle().PrepareDeleteTx(ctx, tx, user, fixture.clock.Load()); err != nil {
					_ = tx.Rollback()
					t.Fatal(err)
				}
				if err := tx.Commit(); err != nil {
					t.Fatal(err)
				}
				fixture.service.beforeSettlement = nil
				if _, err := fixture.service.settle(ctx, pending.BatchID, user, fixture.clock.Load(), false); !errors.Is(err, ErrNotFound) {
					t.Fatalf("late settlement=%v", err)
				}
			} else {
				if err != nil || pending != nil || result == nil || result.RulesVersion != 2 {
					t.Fatalf("result=%+v pending=%+v err=%v", result, pending, err)
				}
				if tc.name == "mixed" && result.Payment != (game.Payment{General: "1500", Game: "1000"}) {
					t.Fatalf("payment=%+v", result.Payment)
				}
				if result.GameBalance != game.FormatAmount(tc.wantGame) {
					t.Fatalf("game balance=%s", result.GameBalance)
				}
				replay, _, err := fixture.service.startFishing(ctx, input, 2)
				if err != nil || replay == nil || !replay.IdempotentReplay || replay.Payment != result.Payment {
					t.Fatalf("replay=%+v err=%v", replay, err)
				}
			}
			if !tc.release && !tc.insufficient {
				tc.wantGeneral += 1_000_000
				if fixture.scalar("SELECT COUNT(*) FROM game_onboarding_completions") != 1 || fixture.scalar("SELECT COUNT(*) FROM credit_operations WHERE kind='game_onboarding_reward'") != 1 {
					t.Fatal("normal batch did not award exactly once")
				}
			} else if fixture.scalar("SELECT COUNT(*) FROM game_onboarding_completions") != 0 {
				t.Fatal("uncompleted fishing awarded")
			}
			if fixture.scalar("SELECT COUNT(*) FROM game_onboarding_holds") != 0 {
				t.Fatal("finished batch retained a hold")
			}
			tx, err = fixture.database.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			for _, want := range []struct {
				asset  ledger.Asset
				amount int64
			}{{ledger.General, tc.wantGeneral}, {ledger.Game, tc.wantGame}} {
				actual, err := ledger.UserAssetAccount(ctx, tx, user, want.asset)
				if err != nil || actual.Balance.Big().Cmp(big.NewInt(want.amount)) != 0 {
					t.Fatalf("wallet %s=%+v err=%v", want.asset, actual, err)
				}
			}
			if err := ledger.ValidateRecovery(ctx, tx); err != nil {
				t.Fatal(err)
			}
		})
	}
}
