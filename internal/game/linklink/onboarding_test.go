package linklink

import (
	"context"
	"strconv"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

func TestOnboardingRewardsOnlySuccessfulSpecificationsOnce(t *testing.T) {
	fixture := newFixture(t)
	user, binding := fixture.seedUser("newcomer", testFunding)
	ctx := context.Background()
	tx, err := fixture.database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.CreateUserAssetAccount(ctx, tx, user, ledger.Game, fixture.clock.Load()); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	earned := int64(0)
	for index, spec := range []string{"6x8", "8x8", "10x10", "6x8"} {
		started, err := fixture.service.start(ctx, StartInput{UserID: user, Spec: spec, IdempotencyKey: fixture.key(9920 + index*2)}, 2)
		if err != nil || started.State == nil {
			t.Fatal(started, err)
		}
		if _, err := fixture.service.RenewLease(ctx, LeaseInput{UserID: user, SessionBinding: binding, SessionID: started.State.SessionID, LeaseID: fixture.mustID("gle_")}); err != nil {
			t.Fatal(err)
		}
		definition, _ := resolveSpec(spec)
		nearComplete := validBoardWithActive(definition, map[Coordinate]byte{{Row: 0, Col: 0}: 1, {Row: 0, Col: 1}: 1})
		revision := fixture.replaceBoard(started.State.SessionID, nearComplete, definition.totalPairs()-1)
		input := MatchInput{UserID: user, SessionBinding: binding, SessionID: started.State.SessionID, ExpectedRevision: revision.Decimal(),
			First: Coordinate{Row: 0, Col: 0}, Second: Coordinate{Row: 0, Col: 1}, IdempotencyKey: fixture.key(9921 + index*2)}
		result, err := fixture.service.Match(ctx, input)
		if err != nil || result.Summary == nil || result.Summary.TerminalReason != TerminalCompleted {
			t.Fatal(result, err)
		}
		if index < 3 {
			earned += int64(index+1) * 1_000_000
		}
		if fixture.balance(user) != strconv.FormatInt(testFunding-int64(index+1)*1000+earned, 10) {
			t.Fatal("entry and independent award balance")
		}
		if replay, err := fixture.service.Match(ctx, input); err != nil || !replay.IdempotentReplay {
			t.Fatal("terminal replay", err)
		}
	}
	if earned != 6_000_000 || fixture.scalar("SELECT COUNT(*) FROM game_onboarding_completions WHERE user_id=?", user) != 3 ||
		fixture.scalar("SELECT COUNT(*) FROM game_onboarding_holds") != 0 {
		t.Fatal("specification rewards")
	}
	tx, err = fixture.database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := ledger.ValidateRecovery(ctx, tx); err != nil {
		t.Fatal(err)
	}
}
