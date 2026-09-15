package activities

import (
	"context"
	"database/sql"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

func TestWelfareIncludesReservedBlackjackGameCredits(t *testing.T) {
	f := newActivityFixture(t, 1_804_000_100)
	user, _ := f.seedUser("card-game-assets", false)
	f.fundGame(user, 5_000_000)
	assertWelfareAssets(t, f, user, "5000000")
	ctx := context.Background()
	tx, err := f.store.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	general, err := ledger.CreateUserAssetAccount(ctx, tx, user, ledger.General, f.clock.Load())
	if err != nil {
		t.Fatal(err)
	}
	game, err := ledger.UserAssetAccount(ctx, tx, user, ledger.Game)
	if err != nil {
		t.Fatal(err)
	}
	paymentID, entryID, operationID := mustActivityID(t, "bjp_"), mustActivityID(t, "bjq_"), mustActivityID(t, "op_")
	escrowGeneral, err := ledger.CreateBlackjackPaymentAccount(ctx, tx, paymentID, ledger.General, f.clock.Load())
	if err != nil {
		t.Fatal(err)
	}
	escrowGame, err := ledger.CreateBlackjackPaymentAccount(ctx, tx, paymentID, ledger.Game, f.clock.Load())
	if err != nil {
		t.Fatal(err)
	}
	ref, err := ledger.BlackjackReservation(paymentID)
	if err != nil {
		t.Fatal(err)
	}
	one := oneU128()
	err = ledger.Reserve(ctx, tx, ref, one, func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.Exec(`INSERT INTO game_blackjack_entries(id,user_id,state,stake_milli,platform_bp,welfare_bp,thursday_bp,created_at) VALUES(?,?,'waiting',5000000,100,100,100,?)`, entryID, user, f.clock.Load()); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO game_blackjack_payments(id,entry_id,kind,hand_no,amount_milli,game_paid_milli,general_account_id,game_account_id,reserve_operation_id,state,ledger_rows_remaining,created_at) VALUES(?,?,'base',0,5000000,5000000,?,?,?,'reserved',?,?)`, paymentID, entryID, escrowGeneral.ID, escrowGame.ID, operationID, db.EncodeU128(one), f.clock.Load())
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := ledger.NewBlackjackReserve(ledger.Meta{OperationID: operationID, ActorUserID: user, CreatedAt: f.clock.Load()}, paymentID, ledger.AccountPair{General: general.ID, Game: game.ID}, ledger.AccountPair{General: escrowGeneral.ID, Game: escrowGame.ID}, ledger.Payment{Game: ledger.AmountFromMilli(5_000_000)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Apply(ctx, tx, plan); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	// Moving all game credits into a queue reserve must not make the user poor.
	assertWelfareAssets(t, f, user, "5000000")
	if balance := readActivityAccountBalance(t, f.store.DB(), game.ID); balance.Sign() != 0 {
		t.Fatal(balance)
	}
	validateLedgerRecovery(t, f.store.DB())
}
