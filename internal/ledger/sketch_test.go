package ledger

import (
	"context"
	"database/sql"
	"errors"
	"math/big"
	"sync"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

func TestActivityPlansConserveEachCurrencyAndRejectFractions(t *testing.T) {
	meta := Meta{OperationID: mustLedgerID(t, "op_"), ActorUserID: 1, CreatedAt: ledgerTestNow}
	plan, err := NewActivityExchange(meta, 1, 2, 3, 4, SketchPaper, AmountFromMilli(12345000), AmountFromMilli(3000))
	if err != nil {
		t.Fatal(err)
	}
	if err := validatePlan(plan); err != nil {
		t.Fatal(err)
	}
	if err := validateConservation(plan.spec.kind, plan.spec.entries); err != nil {
		t.Fatal(err)
	}
	if len(plan.spec.entries) != 4 || plan.spec.entries[0].delta.Decimal() != "-12345000" || plan.spec.entries[3].delta.Decimal() != "3000" {
		t.Fatal("exchange changed asset amounts")
	}
	for _, asset := range []Asset{SketchPaper, SketchBrush} {
		for _, quantity := range []int64{0, 1, 999, 1001, -1000} {
			if _, err := NewActivityExchange(meta, 1, 2, 3, 4, asset, AmountFromMilli(1), AmountFromMilli(quantity)); !errors.Is(err, ErrInvalidPlan) {
				t.Fatalf("accepted quantity %d", quantity)
			}
		}
	}
	if _, err := NewActivityExchange(meta, 1, 2, 3, 4, Game, AmountFromMilli(1), AmountFromMilli(1000)); err == nil {
		t.Fatal("activity exchange accepted game asset")
	}
	// A global zero sum cannot compensate a deficit in a different asset.
	bad := []entrySpec{{role: roleForAsset(userRole(1), SketchPaper), delta: AmountFromMilli(-1000)}, {role: roleForAsset(userRole(2), SketchBrush), delta: AmountFromMilli(1000)}}
	if validateConservation(KindActivityExchange, bad) == nil {
		t.Fatal("cross-asset conservation accepted")
	}
}

func TestImageFinancialPlansUseOneTaskReservation(t *testing.T) {
	id := mustLedgerID(t, "img_")
	meta := Meta{OperationID: mustLedgerID(t, "op_"), ActorUserID: 1, CreatedAt: ledgerTestNow}
	price := SketchPayment{Paper: AmountFromMilli(4000), Brush: AmountFromMilli(2000)}
	wallets, escrow, external := SketchAccounts{1, 2}, SketchAccounts{3, 4}, SketchAccounts{5, 6}
	reserve, err := NewImageReserve(meta, id, wallets, escrow, price)
	if err != nil {
		t.Fatal(err)
	}
	if reserve.spec.capacity.consume != nil || len(reserve.spec.requireNonnegative) != 2 {
		t.Fatal("invalid precharge shape")
	}
	meta.ActorUserID = 0
	for _, build := range []func() (Plan, error){
		func() (Plan, error) { return NewImageSettle(meta, id, escrow, external, price) },
		func() (Plan, error) { return NewImageRefund(meta, id, escrow, wallets, price) },
		func() (Plan, error) { return NewImageDeleteFinalize(meta, id, escrow, external, price) },
	} {
		plan, err := build()
		if err != nil {
			t.Fatal(err)
		}
		if err := validatePlan(plan); err != nil {
			t.Fatal(err)
		}
		if err := validateConservation(plan.spec.kind, plan.spec.entries); err != nil {
			t.Fatal(err)
		}
		if plan.spec.sourceID != id || plan.spec.sourceType != sourceImageTask || plan.spec.capacity.consume == nil || plan.spec.capacity.consume.kind != reservationImageTask {
			t.Fatal("terminal operation lost task reservation")
		}
		if plan.spec.entries[0].delta.Decimal() != "-4000" || plan.spec.entries[2].delta.Decimal() != "-2000" {
			t.Fatal("partial terminal charge")
		}
	}
	if _, err := NewImageReserve(meta, id, wallets, escrow, price); err == nil {
		t.Fatal("precharge without user accepted")
	}
	if _, err := NewImageRefund(Meta{OperationID: meta.OperationID, ActorUserID: 1, CreatedAt: meta.CreatedAt}, id, escrow, wallets, price); err == nil {
		t.Fatal("user-authored refund accepted")
	}
}

func TestActivityAssetsCannotEnterGameEscrows(t *testing.T) {
	for _, asset := range []Asset{SketchPaper, SketchBrush} {
		if _, err := CreateRPSQueueAssetAccount(context.Background(), nil, mustLedgerID(t, "rpsq_"), asset, ledgerTestNow); !errors.Is(err, ErrInvalidPlan) {
			t.Fatal("RPS accepted activity asset")
		}
		if _, err := CreateBlackjackPaymentAccount(context.Background(), nil, mustLedgerID(t, "bjp_"), asset, ledgerTestNow); !errors.Is(err, ErrInvalidPlan) {
			t.Fatal("blackjack accepted activity asset")
		}
		if _, err := CodedAssetAccount(context.Background(), nil, "platform", asset); !errors.Is(err, ErrNotFound) {
			t.Fatal("activity platform adjustment exposed")
		}
	}
}

func TestActivityExchangeReplayAndAllWalletDeletion(t *testing.T) {
	ctx := context.Background()
	store := openLedgerTestStore(t)
	tx := beginLedgerTestTx(t, store.DB())
	user, general := seedLedgerUser(t, tx, "sketch")
	game, err := CreateUserAssetAccount(ctx, tx, user, Game, ledgerTestNow)
	if err != nil {
		t.Fatal(err)
	}
	wallets, err := CreateSketchAccounts(ctx, tx, user, ledgerTestNow)
	if err != nil {
		t.Fatal(err)
	}
	get := func(code string, asset Asset) Account {
		a, err := CodedAssetAccount(ctx, tx, code, asset)
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	extGeneral, extGame, extPaper, extBrush := get("external", General), get("external", Game), get("external", SketchPaper), get("external", SketchBrush)
	meta := func() Meta {
		return Meta{OperationID: mustLedgerID(t, "op_"), ActorUserID: user, CreatedAt: ledgerTestNow}
	}
	fund, err := NewAdminUserAdjustment(meta(), general.ID, extGeneral.ID, AmountFromMilli(100000000), 0, Amount{}, "funding")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(ctx, tx, fund); err != nil {
		t.Fatal(err)
	}
	for _, pair := range []struct {
		asset            Asset
		wallet, external int64
	}{{SketchPaper, wallets.Paper, extPaper.ID}, {SketchBrush, wallets.Brush, extBrush.ID}} {
		plan, err := NewActivityExchange(meta(), general.ID, extGeneral.ID, pair.wallet, pair.external, pair.asset, AmountFromMilli(1000000), AmountFromMilli(3000))
		if err != nil {
			t.Fatal(err)
		}
		first, err := Apply(ctx, tx, plan)
		if err != nil {
			t.Fatal(err)
		}
		again, err := Apply(ctx, tx, plan)
		if err != nil || again.LedgerSeq != first.LedgerSeq {
			t.Fatal("exchange replay duplicated")
		}
	}
	entries, err := ExportUserEntries(ctx, tx, user, 100)
	if err != nil {
		t.Fatal(err)
	}
	found := map[Asset]bool{}
	for _, entry := range entries {
		if entry.Asset.IsActivity() {
			found[entry.Asset] = true
			if entry.Delta != "3" {
				t.Fatalf("activity export = %s", entry.Delta)
			}
		}
	}
	if !found[SketchPaper] || !found[SketchBrush] {
		t.Fatal("activity export omitted an asset")
	}
	plan, err := NewAssetWalletsDeleteZero(meta(), []AssetWallet{{General, general.ID, extGeneral.ID}, {Game, game.ID, extGame.ID}, {SketchPaper, wallets.Paper, extPaper.ID}, {SketchBrush, wallets.Brush, extBrush.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(ctx, tx, plan); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{general.ID, game.ID, wallets.Paper, wallets.Brush} {
		a, err := ReadAccount(ctx, tx, id)
		if err != nil || !a.Balance.IsZero() {
			t.Fatalf("wallet not zero: %v", err)
		}
	}
	if err := db.ValidateAssetLedger(ctx, tx); err != nil {
		t.Fatal(err)
	}
}

func TestInactivityDecayDoesNotRequireUntouchedDebtToBePositive(t *testing.T) {
	ctx := context.Background()
	store := openLedgerTestStore(t)
	tx := beginLedgerTestTx(t, store.DB())
	user, general := seedLedgerUser(t, tx, "decay")
	game, err := CreateUserAssetAccount(ctx, tx, user, Game, ledgerTestNow)
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
	meta := func() Meta {
		return Meta{OperationID: mustLedgerID(t, "op_"), ActorUserID: user, CreatedAt: ledgerTestNow}
	}
	debt, err := NewAdminUserAdjustment(meta(), general.ID, external.ID, AmountFromMilli(-1000), 0, Amount{}, "debt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(ctx, tx, debt); err != nil {
		t.Fatal(err)
	}
	fund, err := NewAdminGameAdjustment(meta(), game.ID, gameExternal.ID, AmountFromMilli(10000), "funding")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(ctx, tx, fund); err != nil {
		t.Fatal(err)
	}
	request := meta()
	request.ActorUserID = 0
	plan, err := NewInactivityDecay(request, AccountPair{general.ID, game.ID}, AccountPair{external.ID, gameExternal.ID}, Payment{Game: AmountFromMilli(3000)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(ctx, tx, plan); err != nil {
		t.Fatal(err)
	}
	got, _ := ReadAccount(ctx, tx, general.ID)
	if got.Balance.Big().Cmp(big.NewInt(-1000)) != 0 {
		t.Fatal("untouched debt changed")
	}
	got, _ = ReadAccount(ctx, tx, game.ID)
	if got.Balance.Decimal() != "7000" {
		t.Fatal("decay changed wrong amount")
	}
}

func TestAuditClassificationsPreserveAmbiguousAndDeletedFlows(t *testing.T) {
	if c := ClassifyForAudit(Kind("future_kind"), ""); c.Known || c.Channel != "unclassified" {
		t.Fatal("unknown kind hidden")
	}
	if c := ClassifyForAudit(KindDuelTerminal, "bid_test"); !c.Known || c.Channel != "bidding" {
		t.Fatal("bidding channel lost")
	}
	if c := ClassifyForAudit(KindDuelTerminal, "lik_test"); !c.Known || c.Channel != "likes" {
		t.Fatal("likes channel lost")
	}
	if ClassifyForAudit(KindDuelTerminal, "unknown").Known {
		t.Fatal("unknown duel channel guessed")
	}
	if c := ClassifyForAudit(KindImageRefund, ""); c.Behavior != "refund" || !c.Known {
		t.Fatal("refund classification lost")
	}
}

func TestImageRefundAndSettlementCompeteForOneReservedTerminal(t *testing.T) {
	ctx := context.Background()
	store := openLedgerTestStore(t)
	tx := beginLedgerTestTx(t, store.DB())
	user, general := seedLedgerUser(t, tx, "image-terminal")
	wallets, err := CreateSketchAccounts(ctx, tx, user, ledgerTestNow)
	if err != nil {
		t.Fatal(err)
	}
	get := func(code string, asset Asset) int64 {
		a, e := CodedAssetAccount(ctx, tx, code, asset)
		if e != nil {
			t.Fatal(e)
		}
		return a.ID
	}
	external := SketchAccounts{get("external", SketchPaper), get("external", SketchBrush)}
	escrow := SketchAccounts{get("image_activity_reserve", SketchPaper), get("image_activity_reserve", SketchBrush)}
	generalExternal := get("external", General)
	meta := func() Meta {
		return Meta{OperationID: mustLedgerID(t, "op_"), ActorUserID: user, CreatedAt: ledgerTestNow}
	}
	fund, err := NewAdminUserAdjustment(meta(), general.ID, generalExternal, AmountFromMilli(1000000), 0, Amount{}, "funding")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(ctx, tx, fund); err != nil {
		t.Fatal(err)
	}
	for _, v := range []struct {
		asset            Asset
		wallet, external int64
	}{{SketchPaper, wallets.Paper, external.Paper}, {SketchBrush, wallets.Brush, external.Brush}} {
		plan, err := NewActivityExchange(meta(), general.ID, generalExternal, v.wallet, v.external, v.asset, AmountFromMilli(1000), AmountFromMilli(5000))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Apply(ctx, tx, plan); err != nil {
			t.Fatal(err)
		}
	}
	id := mustLedgerID(t, "img_")
	ref, err := ImageTaskReservation(id)
	if err != nil {
		t.Fatal(err)
	}
	one := mustU128(t, "1")
	if err := Reserve(ctx, tx, ref, one, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO image_activity_tasks(id,user_id,state,ledger_rows_remaining,created_at,updated_at) VALUES(?,?,'dispatched',?,?,?)`, id, user, db.EncodeU128(one), ledgerTestNow, ledgerTestNow)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	price := SketchPayment{Paper: AmountFromMilli(2000), Brush: AmountFromMilli(1000)}
	reserve, err := NewImageReserve(meta(), id, wallets, escrow, price)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(ctx, tx, reserve); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	request := meta()
	request.ActorUserID = 0
	settle, err := NewImageSettle(request, id, escrow, external, price)
	if err != nil {
		t.Fatal(err)
	}
	request = meta()
	request.ActorUserID = 0
	refund, err := NewImageRefund(request, id, escrow, wallets, price)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, attempt := range []struct {
		plan  Plan
		state string
	}{{settle, "succeeded"}, {refund, "failed"}} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tx, err := store.DB().BeginTx(ctx, nil)
			if err != nil {
				results <- err
				return
			}
			defer tx.Rollback()
			_, err = ConsumeReserved(ctx, tx, ref, attempt.plan, func(ctx context.Context, tx *sql.Tx) error {
				result, err := tx.ExecContext(ctx, `UPDATE image_activity_tasks SET state=?,ledger_rows_remaining=?,updated_at=? WHERE id=? AND state='dispatched' AND ledger_rows_remaining=?`, attempt.state, make([]byte, 16), ledgerTestNow, id, db.EncodeU128(one))
				if err != nil {
					return err
				}
				n, err := result.RowsAffected()
				if err != nil {
					return err
				}
				if n != 1 {
					return ErrConflict
				}
				return nil
			})
			if err == nil {
				err = tx.Commit()
			}
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	won := 0
	for err := range results {
		if err == nil {
			won++
		} else if !errors.Is(err, ErrInvalidReservation) {
			t.Fatal(err)
		}
	}
	if won != 1 {
		t.Fatalf("terminal winners=%d", won)
	}
	tx = beginLedgerTestTx(t, store.DB())
	if err := ValidateRecovery(ctx, tx); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{escrow.Paper, escrow.Brush} {
		a, err := ReadAccount(ctx, tx, id)
		if err != nil || !a.Balance.IsZero() {
			t.Fatal("terminal left an escrow balance", a, err)
		}
	}
	var terminalCount int
	if err := tx.QueryRow(`SELECT count(*) FROM credit_operations WHERE source_type='image_task' AND source_id=? AND kind<>'image_reserve'`, id).Scan(&terminalCount); err != nil || terminalCount != 1 {
		t.Fatal("duplicate image terminal", terminalCount, err)
	}
}
