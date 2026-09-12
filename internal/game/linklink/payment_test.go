package linklink

import (
	"context"
	"math/big"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/game"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

func TestMixedEntryKeepsEachAssetInItsPlatformAccount(t *testing.T) {
	fixture := newFixture(t)
	user, binding := fixture.seedUser("mixed-entry", 400)
	ctx := context.Background()
	tx, err := fixture.database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	wallet, err := ledger.CreateUserAssetAccount(ctx, tx, user, ledger.Game, fixture.clock.Load())
	if err != nil {
		t.Fatal(err)
	}
	external, err := ledger.CodedAssetAccount(ctx, tx, "external", ledger.Game)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := ledger.NewAdminGameAdjustment(ledger.Meta{OperationID: fixture.mustID("op_"), ActorUserID: fixture.adminID, CreatedAt: fixture.clock.Load()}, wallet.ID, external.ID, ledger.AmountFromMilli(700), "game wallet funding")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Apply(ctx, tx, plan); err != nil {
		t.Fatal(err)
	}
	beforeGeneral, err := ledger.CodedAssetAccount(ctx, tx, "platform", ledger.General)
	if err != nil {
		t.Fatal(err)
	}
	beforeGame, err := ledger.CodedAssetAccount(ctx, tx, "platform", ledger.Game)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	input := StartInput{UserID: user, Spec: game.LinkLinkSpec6x8, IdempotencyKey: fixture.key(9901)}
	started, err := fixture.service.start(ctx, input, 2)
	if err != nil || started.State == nil || started.State.Payment != (game.Payment{General: "0.3", Game: "0.7"}) {
		t.Fatalf("start=%+v err=%v", started, err)
	}
	replay, err := fixture.service.start(ctx, input, 2)
	if err != nil || !replay.IdempotentReplay || replay.State.Payment != started.State.Payment {
		t.Fatalf("replay=%+v err=%v", replay, err)
	}
	summary, err := fixture.service.Abandon(ctx, AbandonInput{UserID: user, SessionBinding: binding, SessionID: started.State.SessionID, ExpectedRevision: "1", Confirmation: true, IdempotencyKey: fixture.key(9902)})
	if err != nil || summary.Payment != started.State.Payment || summary.RulesVersion != 2 {
		t.Fatalf("summary=%+v err=%v", summary, err)
	}
	if fixture.scalar("SELECT COUNT(*) FROM game_onboarding_holds") != 0 || fixture.scalar("SELECT COUNT(*) FROM game_onboarding_completions") != 0 {
		t.Fatal("abandoned game retained a reward or hold")
	}
	tx, err = fixture.database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for _, want := range []struct {
		asset ledger.Asset
		base  ledger.Amount
		delta int64
	}{{ledger.General, beforeGeneral.Balance, 300}, {ledger.Game, beforeGame.Balance, 700}} {
		actual, err := ledger.CodedAssetAccount(ctx, tx, "platform", want.asset)
		if err != nil || new(big.Int).Sub(actual.Balance.Big(), want.base.Big()).Cmp(big.NewInt(want.delta)) != 0 {
			t.Fatalf("platform %s=%+v err=%v", want.asset, actual, err)
		}
	}
	remaining, err := ledger.UserAccount(ctx, tx, user)
	if err != nil || remaining.Balance.Big().Cmp(big.NewInt(100)) != 0 {
		t.Fatal("general wallet charge", err)
	}
	if err := ledger.ValidateRecovery(ctx, tx); err != nil {
		t.Fatal(err)
	}
}
