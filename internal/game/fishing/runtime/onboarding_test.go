package runtime

import (
	"context"
	"math/big"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/game"
	builtinconfig "github.com/waiting-here/NonbiriAPI/internal/game/builtin/config"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

func TestFishingOnboardingAwardsEachBaitOnceAcrossBatchSizes(t *testing.T) {
	fixture := newGameFixture(t, &scriptedSource{})
	const funding int64 = 1_000_000_000
	user := fixture.seedUser("newcomer", funding)
	ctx := context.Background()
	expected := big.NewInt(funding)
	for index, bait := range []string{"worm", "lure", "premium", "worm"} {
		count := 1
		if index == 1 {
			count = 10
		}
		input := StartInput{UserID: user, Bait: bait, Count: count, IdempotencyKey: validTestKey(9910 + index)}
		result, pending, err := fixture.service.startFishing(ctx, input, 2)
		if err != nil || result == nil || pending != nil {
			t.Fatalf("bait=%s result=%+v pending=%+v err=%v", bait, result, pending, err)
		}
		entry, err := game.ParseAmount(result.EntryTotal)
		if err != nil {
			t.Fatal(err)
		}
		payout, err := game.ParseAmount(result.PayoutTotal)
		if err != nil {
			t.Fatal(err)
		}
		expected.Sub(expected, big.NewInt(entry))
		expected.Add(expected, big.NewInt(payout))
		if index < 3 {
			expected.Add(expected, big.NewInt(1_000_000))
		}
		replay, _, err := fixture.service.startFishing(ctx, input, 2)
		if err != nil || replay == nil || !replay.IdempotentReplay {
			t.Fatal("batch replay", err)
		}
		if err := fixture.service.AcknowledgeFishing(ctx, user, result.BatchID); err != nil {
			t.Fatal(err)
		}
	}
	if fixture.scalar("SELECT COUNT(*) FROM game_onboarding_completions WHERE user_id=?", user) != 3 ||
		fixture.scalar("SELECT SUM(award_milli) FROM game_onboarding_completions WHERE user_id=?", user) != 3_000_000 ||
		fixture.scalar("SELECT COUNT(*) FROM game_onboarding_holds") != 0 {
		t.Fatal("lifetime bait awards")
	}
	tx, err := fixture.database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	wallet, err := ledger.UserAccount(ctx, tx, user)
	if err != nil || wallet.Balance.Big().Cmp(expected) != 0 {
		t.Fatalf("actual wallet=%+v expected=%s err=%v", wallet, expected, err)
	}
	registry, err := builtinconfig.Registry()
	if err != nil {
		t.Fatal(err)
	}
	progress, err := registry.OnboardingProgress(ctx, tx, user)
	if err != nil || !progress["fishing"].AllCompleted || progress["linklink"].AllCompleted || progress["rps"].AllCompleted {
		t.Fatalf("progress=%+v err=%v", progress, err)
	}
	if err := ledger.ValidateRecovery(ctx, tx); err != nil {
		t.Fatal(err)
	}
}
