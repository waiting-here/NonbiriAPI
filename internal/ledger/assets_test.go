package ledger

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

func TestGamePaymentUsesAvailableAssetsIndependently(t *testing.T) {
	for _, tc := range []struct {
		name                                       string
		cost, general, game, wantGeneral, wantGame int64
		insufficient                               bool
	}{
		{"game first", 8, 20, 10, 0, 8, false},
		{"mixed", 12, 20, 10, 2, 10, false},
		{"general debt", 8, -20, 10, 0, 8, false},
		{"game debt", 8, 20, -10, 8, 0, false},
		{"zero with debt", 0, -20, -10, 0, 0, false},
		{"insufficient general", 12, -20, 10, 0, 0, true},
		{"both insufficient", 12, 1, 10, 0, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payment, err := SplitGamePayment(AmountFromMilli(tc.cost), AmountFromMilli(tc.general), AmountFromMilli(tc.game))
			if tc.insufficient {
				if !errors.Is(err, ErrInsufficientBalance) {
					t.Fatalf("payment error = %v", err)
				}
			} else if err != nil || payment.General.Big().Int64() != tc.wantGeneral || payment.Game.Big().Int64() != tc.wantGame {
				t.Fatalf("payment = %s/%s, %v", payment.General.Decimal(), payment.Game.Decimal(), err)
			}
		})
	}
}

func TestDualAssetsConserveAndDeleteIndependently(t *testing.T) {
	store := openLedgerTestStore(t)
	ctx := context.Background()
	tx := beginLedgerTestTx(t, store.DB())
	user, general := seedLedgerUser(t, tx, "dual-assets")
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
	fund, err := NewAdminUserAdjustment(meta(), general.ID, external.ID, AmountFromMilli(-10), 0, Amount{}, "adjust")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(ctx, tx, fund); err != nil {
		t.Fatal(err)
	}
	award, err := NewGameCheckinAward(meta(), game.ID, gameExternal.ID, AmountFromMilli(20))
	if err != nil {
		t.Fatal(err)
	}
	first, err := Apply(ctx, tx, award)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := Apply(ctx, tx, award)
	if err != nil || replay.LedgerSeq != first.LedgerSeq {
		t.Fatalf("award replay: %+v, %v", replay, err)
	}
	for _, entry := range first.Entries {
		if entry.Asset != Game {
			t.Fatal("award lost game asset")
		}
	}
	if _, err := tx.Exec(`UPDATE credit_entries SET asset_type='general' WHERE operation_id=?`, first.OperationID); err == nil {
		t.Fatal("entry asset mutated")
	}
	readGeneral, err := UserAccount(ctx, tx, user)
	if err != nil || readGeneral.ID != general.ID || readGeneral.Balance.Decimal() != "-10" {
		t.Fatalf("general wrapper = %+v, %v", readGeneral, err)
	}

	invalid, err := operationPlan(meta(), KindAdminUserAdjustment)
	if err != nil {
		t.Fatal(err)
	}
	invalid = invalid.add(userRole(general.ID), AmountFromMilli(-1)).
		add(roleForAsset(userRole(game.ID), Game), AmountFromMilli(1))
	if _, err := Apply(ctx, tx, invalid); !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("cross-asset cancellation accepted: %v", err)
	}
	if err := ValidateRecovery(ctx, tx); err != nil {
		t.Fatal(err)
	}

	zero, err := NewWalletsDeleteZero(meta(), AccountPair{general.ID, game.ID}, AccountPair{external.ID, gameExternal.ID})
	if err != nil {
		t.Fatal(err)
	}
	result, err := Apply(ctx, tx, zero)
	if err != nil || len(result.Entries) != 4 {
		t.Fatalf("two-wallet retirement: %+v, %v", result, err)
	}
	for _, id := range []int64{general.ID, game.ID} {
		account, err := ReadAccount(ctx, tx, id)
		if err != nil || !account.Balance.IsZero() {
			t.Fatalf("wallet remains: %+v, %v", account, err)
		}
	}
	if _, err := tx.Exec(`DELETE FROM users WHERE id=?`, user); err != nil {
		t.Fatal(err)
	}
	retired, err := loadResult(ctx, tx, result.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	assets := map[Asset]int{}
	for _, entry := range retired.Entries {
		assets[entry.Asset]++
		if entry.AccountKind == AccountUser && entry.AccountID != 0 {
			t.Fatal("user entry retained account identity")
		}
	}
	if assets[General] != 2 || assets[Game] != 2 {
		t.Fatalf("retired assets = %v", assets)
	}
	if err := ValidateRecovery(ctx, tx); err != nil {
		t.Fatal(err)
	}
}

func TestOnboardingHoldHasIndependentCapacity(t *testing.T) {
	store := openLedgerTestStore(t)
	ctx := context.Background()
	tx := beginLedgerTestTx(t, store.DB())
	user, _ := seedLedgerUser(t, tx, "onboarding-capacity")
	queueID := mustLedgerID(t, "rpsq_")
	account, err := CreateRPSQueueAccount(ctx, tx, queueID, ledgerTestNow)
	if err != nil {
		t.Fatal(err)
	}
	queueRef, _ := RPSQueueReservation(queueID)
	one := mustU128(t, "1")
	if err := Reserve(ctx, tx, queueRef, one, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO game_rps_queue(id,user_id,account_id,mode,revision,reservation_operation_id,reserved,ledger_rows_remaining,device_token_hash,source_ip_hash,deadline,created_at,rules_version)
VALUES(?,?,?,'quick',?,?,?,?,?,?,?, ?,2)`, queueID, user, account.ID, db.EncodeU128(one), mustLedgerID(t, "op_"), db.EncodeU128(one), db.EncodeU128(one), make([]byte, 32), make([]byte, 32), ledgerTestNow+60, ledgerTestNow)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	holdID := mustLedgerID(t, "goh_")
	hold, err := GameOnboardingHold(holdID)
	if err != nil {
		t.Fatal(err)
	}
	if err := Reserve(ctx, tx, hold, one, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO game_onboarding_holds(id,user_id,game_key,task_key,ledger_rows_remaining,created_at,rps_queue_id)
VALUES(?,?,'rps','quick',?,?,?)`, holdID, user, db.EncodeU128(one), ledgerTestNow, queueID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	capacity, err := ReadCapacity(ctx, tx)
	if err != nil || capacity.ReservedFutureRows.Decimal() != "2" {
		t.Fatalf("independent holds: %+v, %v", capacity, err)
	}
	if _, err := tx.Exec(`DELETE FROM game_rps_queue WHERE id=?`, queueID); err == nil {
		t.Fatal("parent deletion silently discarded capacity")
	}
	if err := ReleaseReserved(ctx, tx, hold, one, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM game_onboarding_holds WHERE id=?`, holdID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	capacity, err = ReadCapacity(ctx, tx)
	if err != nil || capacity.ReservedFutureRows.Decimal() != "1" {
		t.Fatalf("release changed queue capacity: %+v, %v", capacity, err)
	}
	if err := ValidateRecovery(ctx, tx); err != nil {
		t.Fatal(err)
	}
}
