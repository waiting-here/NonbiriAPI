package activities

import (
	"context"
	"database/sql"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

func TestWelfareLogicalHoldConversionCountsExactlyOnce(t *testing.T) {
	fixture := newActivityFixture(t, 1_800_600_000)
	userID, _ := fixture.seedUser("logical-hold", false)
	fixture.fundUser(userID, 100)
	requestID, err := db.GenerateOpaqueID("req_")
	if err != nil {
		t.Fatal(err)
	}
	reserveOperation := fixture.operationID()
	tx, err := fixture.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	ref, _ := ledger.LogicalRequestReservation(requestID)
	err = ledger.Reserve(context.Background(), tx, ref, oneU128(), func(ctx context.Context, callbackTx *sql.Tx) error {
		_, err := callbackTx.ExecContext(ctx, `
INSERT INTO logical_requests(
 id,user_id,route_kind,model_snapshot,state,attempt_limit,accounting_state,
 account_reserved_milli,settlement_destination,ledger_rows_remaining,created_at)
VALUES(?,?,'openai_chat_completions','','accepted',1,'reserved',40,'user',?,?)`,
			requestID, userID, db.EncodeU128(oneU128()), fixture.clock.Load())
		return err
	})
	if err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	wallet, _ := ledger.UserAccount(context.Background(), tx, userID)
	reserve, _ := ledger.CodedAccount(context.Background(), tx, "forward_reserve")
	plan, _ := ledger.NewForwardReserve(ledger.Meta{OperationID: reserveOperation, ActorUserID: userID, CreatedAt: fixture.clock.Load()}, requestID, wallet.ID, reserve.ID, ledger.AmountFromMilli(40))
	if _, err := ledger.Apply(context.Background(), tx, plan); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	assertWelfareAssets(t, fixture, userID, "100")

	tx, err = fixture.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	wallet, _ = ledger.UserAccount(context.Background(), tx, userID)
	reserve, _ = ledger.CodedAccount(context.Background(), tx, "forward_reserve")
	releasePlan, _ := ledger.NewForwardRelease(ledger.Meta{OperationID: fixture.operationID(), ActorUserID: userID, CreatedAt: fixture.clock.Load()}, requestID, reserve.ID, wallet.ID, ledger.AmountFromMilli(40))
	_, err = ledger.ConsumeReserved(context.Background(), tx, ref, releasePlan, func(ctx context.Context, callbackTx *sql.Tx) error {
		_, err := callbackTx.ExecContext(ctx, `UPDATE logical_requests SET accounting_state='released',account_reserved_milli=0,ledger_rows_remaining=? WHERE id=? AND accounting_state='reserved'`, db.EncodeU128(db.U128{}), requestID)
		return err
	})
	if err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	assertWelfareAssets(t, fixture, userID, "100")
	validateLedgerRecovery(t, fixture.store.DB())
}

func assertWelfareAssets(t *testing.T, fixture *activityFixture, userID int64, want string) {
	t.Helper()
	tx, err := fixture.store.DB().BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	assets, _, err := welfareAssetsTx(context.Background(), tx, userID)
	if err != nil || assets.String() != want {
		t.Fatalf("welfare assets=%v err=%v want=%s", assets, err, want)
	}
}
