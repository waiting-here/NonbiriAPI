package inactivity

import (
	"context"
	"database/sql"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	"sync"
	"testing"
	"time"
)

func TestFourAssetBoundaryAndLifecycleRetention(t *testing.T) {
	e := fixture(t)
	id := e.user(t, "currencies", 1, 10000, 2000)
	ctx := context.Background()
	tx, err := e.store.DB().Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	wallets, err := ledger.CreateSketchAccounts(ctx, tx, id, testNow)
	if err != nil {
		t.Fatal(err)
	}
	general, err := ledger.UserAssetAccount(ctx, tx, id, ledger.General)
	if err != nil {
		t.Fatal(err)
	}
	ext, err := ledger.CodedAssetAccount(ctx, tx, "external", ledger.General)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		asset  ledger.Asset
		wallet int64
	}{{ledger.SketchPaper, wallets.Paper}, {ledger.SketchBrush, wallets.Brush}} {
		other, err := ledger.CodedAssetAccount(ctx, tx, "external", item.asset)
		if err != nil {
			t.Fatal(err)
		}
		op, err := db.GenerateOpaqueID("op_")
		if err != nil {
			t.Fatal(err)
		}
		plan, err := ledger.NewActivityExchange(ledger.Meta{OperationID: op, ActorUserID: id, CreatedAt: testNow}, general.ID, ext.ID, item.wallet, other.ID, item.asset, ledger.AmountFromMilli(1000), ledger.AmountFromMilli(3000))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = ledger.Apply(ctx, tx, plan); err != nil {
			t.Fatal(err)
		}
	}
	request, err := db.GenerateOpaqueID("req_")
	if err != nil {
		t.Fatal(err)
	}
	ref, err := ledger.LogicalRequestReservation(request)
	if err != nil {
		t.Fatal(err)
	}
	one, err := db.ParseU128Decimal("1")
	if err != nil {
		t.Fatal(err)
	}
	if err = ledger.Reserve(ctx, tx, ref, one, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO logical_requests(id,user_id,route_kind,model_snapshot,state,attempt_limit,accounting_state,account_reserved_milli,settlement_destination,ledger_rows_remaining,created_at) VALUES(?,?,'openai_chat_completions','test','accepted',1,'reserved',1000,'user',?,?)`, request, id, db.EncodeU128(one), testNow)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	reserve, err := ledger.CodedAccount(ctx, tx, "forward_reserve")
	if err != nil {
		t.Fatal(err)
	}
	op, err := db.GenerateOpaqueID("op_")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := ledger.NewForwardReserve(ledger.Meta{OperationID: op, ActorUserID: id, CreatedAt: testNow}, request, general.ID, reserve.ID, ledger.AmountFromMilli(1000))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ledger.Apply(ctx, tx, plan); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	e.policy(t, testPolicy())
	runBatch(t, e.s, testNow)
	if e.balance(t, id, ledger.General) != "6300" || e.balance(t, id, ledger.SketchPaper) != "3000" || e.balance(t, id, ledger.SketchBrush) != "3000" {
		t.Fatal("activity currencies changed or payment counted as activity")
	}
	if scalar(t, e.store.DB(), `SELECT count(*) FROM inactivity_runs WHERE id=ledger_operation_id`) != 0 {
		t.Fatal("receipt identity links to ledger after deidentification")
	}
	tx, err = e.store.DB().Begin()
	if err != nil {
		t.Fatal(err)
	}
	frozen, err := ledger.ReadAccount(ctx, tx, reserve.ID)
	if err != nil || frozen.Balance.Decimal() != "1000" {
		t.Fatal("frozen credits changed", err)
	}
	export, err := ExportTx(ctx, tx, id, 100)
	_ = tx.Rollback()
	if err != nil || export.Activity == nil || len(export.Runs) != 1 {
		t.Fatal(export, err)
	}
	retainAt := testNow + identityLife
	result, err := e.s.Retain(ctx, retainAt, 100, time.Now().Add(2*time.Second), nil)
	if err != nil || result.Deidentified != 1 {
		t.Fatal(result, err)
	}
	if scalar(t, e.store.DB(), `SELECT count(*) FROM inactivity_runs WHERE user_id IS NOT NULL OR ledger_operation_id IS NOT NULL`) != 0 {
		t.Fatal("identity retained")
	}
	held := func(context.Context, *sql.Tx, string, string, int64) (bool, error) { return true, nil }
	result, err = e.s.Retain(ctx, testNow+auditLife, 100, time.Now().Add(2*time.Second), held)
	if err != nil || result.Deleted != 0 {
		t.Fatal(result, err)
	}
	result, err = e.s.Retain(ctx, testNow+auditLife, 100, time.Now().Add(2*time.Second), nil)
	if err != nil || result.Deleted != 1 {
		t.Fatal(result, err)
	}
}

func TestDeletionAndWorkerCannotResurrectActivity(t *testing.T) {
	e := fixture(t)
	id := e.user(t, "retiring", 1, 0, 0)
	e.policy(t, testPolicy())
	ctx := context.Background()
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, err := e.s.Process(ctx, testNow, 100, time.Now().Add(2*time.Second))
		errs <- err
	}()
	go func() {
		defer wg.Done()
		tx, err := e.store.DB().Begin()
		if err != nil {
			errs <- err
			return
		}
		defer tx.Rollback()
		err = DeleteTx(ctx, tx, id)
		if err == nil {
			_, err = tx.Exec(`DELETE FROM users WHERE id=?`, id)
		}
		if err == nil {
			err = tx.Commit()
		}
		errs <- err
	}()
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	tx, err := e.store.DB().Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err = RecordActiveTx(ctx, tx, ActiveEvent{id, testNow, "api", true}); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if scalar(t, e.store.DB(), `SELECT count(*) FROM user_activity_state WHERE user_id=?`, id) != 0 || scalar(t, e.store.DB(), `SELECT count(*) FROM inactivity_runs WHERE user_id=?`, id) != 0 {
		t.Fatal("retired user recreated")
	}
}
