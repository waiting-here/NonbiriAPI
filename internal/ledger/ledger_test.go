package ledger

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"math/big"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/db"
)

func TestImmediateReserveConsumeReplayAndRecovery(t *testing.T) {
	store := openLedgerTestStore(t)
	ctx := context.Background()
	tx := beginLedgerTestTx(t, store.DB())
	userID, wallet := seedLedgerUser(t, tx, "flow")
	external, err := CodedAccount(ctx, tx, "external")
	if err != nil {
		t.Fatal(err)
	}
	platform, err := CodedAccount(ctx, tx, "platform")
	if err != nil {
		t.Fatal(err)
	}
	forward, err := CodedAccount(ctx, tx, "forward_reserve")
	if err != nil {
		t.Fatal(err)
	}

	fundMeta := Meta{OperationID: mustLedgerID(t, "op_"), ActorUserID: userID, CreatedAt: ledgerTestNow}
	fund, err := NewAdminUserAdjustment(fundMeta, wallet.ID, external.ID, AmountFromMilli(1000), 0, Amount{}, "test funding")
	if err != nil {
		t.Fatal(err)
	}
	fundResult, err := Apply(ctx, tx, fund)
	if err != nil || fundResult.LedgerSeq != 1 {
		t.Fatalf("fund = (%+v,%v)", fundResult, err)
	}

	requestID := mustLedgerID(t, "req_")
	ref, err := LogicalRequestReservation(requestID)
	if err != nil {
		t.Fatal(err)
	}
	one := mustU128(t, "1")
	if err := Reserve(ctx, tx, ref, one, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
INSERT INTO logical_requests(
 id,user_id,route_kind,model_snapshot,state,attempt_limit,accounting_state,
 account_reserved_milli,settlement_destination,ledger_rows_remaining,created_at)
VALUES(?,?,'openai_chat_completions','model','accepted',1,'reserved',100,'user',?,?)`,
			requestID, userID, db.EncodeU128(one), ledgerTestNow)
		return err
	}); err != nil {
		t.Fatalf("reserve future row: %v", err)
	}
	outstanding, err := RecoverNonterminal(ctx, tx)
	if err != nil || len(outstanding) != 1 || outstanding[0].Domain != "logical_request" ||
		outstanding[0].ResourceID != requestID || !outstanding[0].Ref.equal(ref) || outstanding[0].Rows.Big().Cmp(one.Big()) != 0 {
		t.Fatalf("recover accepted request = (%+v,%v)", outstanding, err)
	}
	reserveMeta := Meta{OperationID: mustLedgerID(t, "op_"), ActorUserID: userID, CreatedAt: ledgerTestNow + 1}
	reservePlan, err := NewForwardReserve(reserveMeta, requestID, wallet.ID, forward.ID, AmountFromMilli(100))
	if err != nil {
		t.Fatal(err)
	}
	reserveResult, err := Apply(ctx, tx, reservePlan)
	if err != nil || reserveResult.LedgerSeq != 2 {
		t.Fatalf("forward reserve = (%+v,%v)", reserveResult, err)
	}
	if err := ValidateRecovery(ctx, tx); err != nil {
		t.Fatalf("validate accepted request: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit reserve: %v", err)
	}

	tx = beginLedgerTestTx(t, store.DB())
	destination, err := UserSettlementDestination(wallet.ID)
	if err != nil {
		t.Fatal(err)
	}
	settleMeta := Meta{OperationID: mustLedgerID(t, "op_"), ActorUserID: userID, CreatedAt: ledgerTestNow + 2}
	settlePlan, err := NewForwardSettle(settleMeta, requestID, forward.ID, platform.ID, destination, AmountFromMilli(100), AmountFromMilli(80))
	if err != nil {
		t.Fatal(err)
	}
	settled, err := ConsumeReserved(ctx, tx, ref, settlePlan, func(ctx context.Context, tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `
UPDATE logical_requests
SET state='terminal',caller_result_class='success',caller_status=200,
 accounting_state='committed',account_reserved_milli=0,ledger_rows_remaining=?,terminal_at=?
WHERE id=? AND state IN ('accepted','running') AND ledger_rows_remaining=?`,
			db.EncodeU128(db.U128{}), ledgerTestNow+2, requestID, db.EncodeU128(one))
		if err != nil {
			return err
		}
		count, err := result.RowsAffected()
		if err != nil || count != 1 {
			return ErrConflict
		}
		return nil
	})
	if err != nil || settled.LedgerSeq != 3 {
		t.Fatalf("settle = (%+v,%v)", settled, err)
	}
	if err := ValidateRecovery(ctx, tx); err != nil {
		t.Fatalf("validate settled request: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit settle: %v", err)
	}

	// A different operation ID with the same immutable source returns the
	// persisted result and never invokes the terminal callback again.
	tx = beginLedgerTestTx(t, store.DB())
	replayMeta := settleMeta
	replayMeta.OperationID = mustLedgerID(t, "op_")
	replayPlan, err := NewForwardSettle(replayMeta, requestID, forward.ID, platform.ID, destination, AmountFromMilli(100), AmountFromMilli(999))
	if err != nil {
		t.Fatal(err)
	}
	called := false
	replayed, err := ConsumeReserved(ctx, tx, ref, replayPlan, func(context.Context, *sql.Tx) error {
		called = true
		return errors.New("must not run")
	})
	if err != nil || called || replayed.OperationID != settled.OperationID || replayed.LedgerSeq != settled.LedgerSeq {
		t.Fatalf("source replay = (%+v,%v), called=%v", replayed, err, called)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}

	tx = beginLedgerTestTx(t, store.DB())
	account, err := ReadAccount(ctx, tx, wallet.ID)
	if err != nil || account.Balance.Decimal() != "920" {
		t.Fatalf("wallet = (%s,%v), want 920", account.Balance.Decimal(), err)
	}
	account, err = ReadAccount(ctx, tx, forward.ID)
	if err != nil || account.Balance.Decimal() != "0" {
		t.Fatalf("forward reserve = (%s,%v), want 0", account.Balance.Decimal(), err)
	}
	account, err = ReadAccount(ctx, tx, platform.ID)
	if err != nil || account.Balance.Decimal() != "80" {
		t.Fatalf("platform = (%s,%v), want 80", account.Balance.Decimal(), err)
	}
	capacity, err := ReadCapacity(ctx, tx)
	if err != nil || capacity.LastLedgerSeq != 3 || capacity.ReservedFutureRows.Big().Sign() != 0 {
		t.Fatalf("capacity = (%+v,%v)", capacity, err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
}

func TestRecoveryRejectsCapacityAndSequenceMismatch(t *testing.T) {
	store := openLedgerTestStore(t)
	ctx := context.Background()
	tx := beginLedgerTestTx(t, store.DB())
	userID, _ := seedLedgerUser(t, tx, "recovery-mismatch")
	requestID := mustLedgerID(t, "req_")
	ref, err := LogicalRequestReservation(requestID)
	if err != nil {
		t.Fatal(err)
	}
	one := mustU128(t, "1")
	if err := Reserve(ctx, tx, ref, one, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
INSERT INTO logical_requests(
 id,user_id,route_kind,model_snapshot,state,attempt_limit,accounting_state,
 account_reserved_milli,settlement_destination,ledger_rows_remaining,created_at)
VALUES(?,?,'openai_chat_completions','model','accepted',1,'reserved',0,'user',?,?)`,
			requestID, userID, db.EncodeU128(one), ledgerTestNow)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE credit_capacity SET reserved_future_rows=? WHERE id=1`, db.EncodeU128(db.U128{})); err != nil {
		t.Fatal(err)
	}
	if err := ValidateRecovery(ctx, tx); !errors.Is(err, ErrInvariant) {
		t.Fatalf("capacity mismatch error = %v", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE credit_capacity SET reserved_future_rows=?,last_ledger_seq=1 WHERE id=1`, db.EncodeU128(one)); err != nil {
		t.Fatal(err)
	}
	if err := ValidateRecovery(ctx, tx); !errors.Is(err, ErrInvariant) {
		t.Fatalf("sequence mismatch error = %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
}

func TestRecoveryBatchesLedgerHistory(t *testing.T) {
	store := openLedgerTestStore(t)
	ctx := context.Background()
	tx := beginLedgerTestTx(t, store.DB())
	userID, wallet := seedLedgerUser(t, tx, "recovery-batches")
	external, _ := CodedAccount(ctx, tx, "external")
	const operationCount = 205
	for i := 0; i < operationCount; i++ {
		plan, err := NewAntiAbusePenalty(
			Meta{OperationID: mustLedgerID(t, "op_"), ActorUserID: userID, CreatedAt: ledgerTestNow + int64(i)},
			wallet.ID, external.ID, Amount{}, "zero audit")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Apply(ctx, tx, plan); err != nil {
			t.Fatalf("operation %d: %v", i, err)
		}
	}
	if err := ValidateRecovery(ctx, tx); err != nil {
		t.Fatalf("batched recovery: %v", err)
	}
	capacity, err := ReadCapacity(ctx, tx)
	if err != nil || capacity.LastLedgerSeq != operationCount {
		t.Fatalf("capacity = (%+v,%v)", capacity, err)
	}
	_ = tx.Rollback()
}

func TestReservationAndReleaseReplaysAreExact(t *testing.T) {
	store := openLedgerTestStore(t)
	ctx := context.Background()
	tx := beginLedgerTestTx(t, store.DB())
	userID, _ := seedLedgerUser(t, tx, "release")
	requestID := mustLedgerID(t, "req_")
	ref, _ := LogicalRequestReservation(requestID)
	one := mustU128(t, "1")
	insert := func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
INSERT INTO logical_requests(
 id,user_id,route_kind,model_snapshot,state,attempt_limit,accounting_state,
 account_reserved_milli,settlement_destination,ledger_rows_remaining,created_at)
VALUES(?,?,'openai_chat_completions','model','accepted',1,'reserved',0,'user',?,?)
ON CONFLICT(id) DO NOTHING`, requestID, userID, db.EncodeU128(one), ledgerTestNow)
		return err
	}
	if err := Reserve(ctx, tx, ref, one, insert); err != nil {
		t.Fatal(err)
	}
	if err := Reserve(ctx, tx, ref, one, insert); err != nil {
		t.Fatalf("reserve replay: %v", err)
	}
	release := func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
UPDATE logical_requests
SET state='terminal',caller_result_class='cancelled',accounting_state='released',
 account_reserved_milli=0,ledger_rows_remaining=?,terminal_at=?
WHERE id=? AND state='accepted'`, db.EncodeU128(db.U128{}), ledgerTestNow+1, requestID)
		return err
	}
	if err := ReleaseReserved(ctx, tx, ref, one, release); err != nil {
		t.Fatal(err)
	}
	if err := ReleaseReserved(ctx, tx, ref, one, release); err != nil {
		t.Fatalf("release replay: %v", err)
	}
	if err := ValidateRecovery(ctx, tx); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestRollbackLeavesNoHalfLedger(t *testing.T) {
	store := openLedgerTestStore(t)
	ctx := context.Background()
	tx := beginLedgerTestTx(t, store.DB())
	userID, wallet := seedLedgerUser(t, tx, "rollback")
	external, _ := CodedAccount(ctx, tx, "external")
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	tx = beginLedgerTestTx(t, store.DB())
	plan, err := NewCheckinAward(Meta{OperationID: mustLedgerID(t, "op_"), ActorUserID: userID, CreatedAt: ledgerTestNow}, wallet.ID, external.ID, AmountFromMilli(7))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(ctx, tx, plan); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}

	tx = beginLedgerTestTx(t, store.DB())
	var operations int
	if err := tx.QueryRow(`SELECT count(*) FROM credit_operations`).Scan(&operations); err != nil {
		t.Fatal(err)
	}
	account, err := ReadAccount(ctx, tx, wallet.ID)
	capacity, capacityErr := ReadCapacity(ctx, tx)
	if operations != 0 || err != nil || account.Balance.Sign() != 0 || capacityErr != nil || capacity.LastLedgerSeq != 0 {
		t.Fatalf("rollback state ops=%d account=%+v err=%v capacity=%+v err=%v", operations, account, err, capacity, capacityErr)
	}
	_ = tx.Rollback()
}

func TestBalanceWidthAndNonnegativeAccountsFailAtomically(t *testing.T) {
	store := openLedgerTestStore(t)
	ctx := context.Background()
	tx := beginLedgerTestTx(t, store.DB())
	userID, wallet := seedLedgerUser(t, tx, "balance-boundary")
	external, _ := CodedAccount(ctx, tx, "external")
	maxBig := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 127), big.NewInt(1))
	maxAmount, err := AmountFromBig(maxBig)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE credit_accounts SET balance_sign=1,balance_mag=? WHERE id=?`, db.EncodeU128(maxAmount.value.Mag), wallet.ID); err != nil {
		t.Fatal(err)
	}
	overflow, _ := NewCheckinAward(
		Meta{OperationID: mustLedgerID(t, "op_"), ActorUserID: userID, CreatedAt: ledgerTestNow},
		wallet.ID, external.ID, AmountFromMilli(1))
	if _, err := Apply(ctx, tx, overflow); !errors.Is(err, ErrInvariant) {
		t.Fatalf("SM128 overflow error = %v", err)
	}
	var poolAccountID int64
	if err := tx.QueryRowContext(ctx, `SELECT account_id FROM shared_pools WHERE pool_type='welfare'`).Scan(&poolAccountID); err != nil {
		t.Fatal(err)
	}
	poolUnderflow, _ := NewAdminPoolAdjustment(
		Meta{OperationID: mustLedgerID(t, "op_"), ActorUserID: userID, CreatedAt: ledgerTestNow + 1},
		poolAccountID, external.ID, AmountFromMilli(-1), "underflow")
	if _, err := Apply(ctx, tx, poolUnderflow); !errors.Is(err, ErrInsufficientBalance) {
		t.Fatalf("pool underflow error = %v", err)
	}
	account, err := ReadAccount(ctx, tx, wallet.ID)
	if err != nil || account.Balance.Big().Cmp(maxBig) != 0 {
		t.Fatalf("wallet after overflow rollback = (%s,%v)", account.Balance.Decimal(), err)
	}
	pool, err := ReadAccount(ctx, tx, poolAccountID)
	if err != nil || !pool.Balance.IsZero() {
		t.Fatalf("pool after underflow rollback = (%s,%v)", pool.Balance.Decimal(), err)
	}
	var operations int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM credit_operations`).Scan(&operations); err != nil {
		t.Fatal(err)
	}
	capacity, err := ReadCapacity(ctx, tx)
	if err != nil || operations != 0 || capacity.LastLedgerSeq != 0 {
		t.Fatalf("failed writes operations=%d capacity=%+v err=%v", operations, capacity, err)
	}
	_ = tx.Rollback()
}

func TestAccountDeleteUsesCurrentSignedBalanceAndFKSetNull(t *testing.T) {
	store := openLedgerTestStore(t)
	ctx := context.Background()
	tx := beginLedgerTestTx(t, store.DB())
	userID, wallet := seedLedgerUser(t, tx, "delete")
	external, _ := CodedAccount(ctx, tx, "external")
	fund, _ := NewAdminUserAdjustment(Meta{OperationID: mustLedgerID(t, "op_"), ActorUserID: userID, CreatedAt: ledgerTestNow}, wallet.ID, external.ID, AmountFromMilli(55), 0, Amount{}, "fund")
	if _, err := Apply(ctx, tx, fund); err != nil {
		t.Fatal(err)
	}
	deleteMeta := Meta{OperationID: mustLedgerID(t, "op_"), ActorUserID: userID, CreatedAt: ledgerTestNow + 1}
	deletePlan, _ := NewAccountDeleteZero(deleteMeta, wallet.ID, external.ID)
	deleted, err := Apply(ctx, tx, deletePlan)
	if err != nil || len(deleted.Entries) != 2 || deleted.Entries[0].Delta.Decimal() != "-55" {
		t.Fatalf("delete operation = (%+v,%v)", deleted, err)
	}
	account, err := ReadAccount(ctx, tx, wallet.ID)
	if err != nil || account.Balance.Sign() != 0 {
		t.Fatalf("zeroed wallet = (%+v,%v)", account, err)
	}
	if _, err := tx.Exec(`DELETE FROM credit_accounts WHERE id=?`, wallet.ID); err != nil {
		t.Fatalf("delete zero wallet: %v", err)
	}
	var nullCount int
	if err := tx.QueryRow(`SELECT count(*) FROM credit_entries WHERE account_id IS NULL`).Scan(&nullCount); err != nil {
		t.Fatal(err)
	}
	var deltaSign int
	var deltaMag []byte
	if err := tx.QueryRow(`SELECT delta_sign,delta_mag FROM credit_entries WHERE operation_id=? AND line_no=0`, deleteMeta.OperationID).Scan(&deltaSign, &deltaMag); err != nil {
		t.Fatal(err)
	}
	delta, err := amountFromParts(deltaSign, deltaMag)
	if nullCount != 2 || err != nil || delta.Decimal() != "-55" {
		t.Fatalf("SET NULL history count=%d delta=%s err=%v", nullCount, delta.Decimal(), err)
	}
	replayPlan, _ := NewAccountDeleteZero(deleteMeta, wallet.ID, external.ID)
	replayed, err := Apply(ctx, tx, replayPlan)
	if err != nil || replayed.OperationID != deleted.OperationID || replayed.LedgerSeq != deleted.LedgerSeq {
		t.Fatalf("delete replay after account removal = (%+v,%v)", replayed, err)
	}
	_ = tx.Rollback()
}

func TestAccountDeleteClearsNegativeWallet(t *testing.T) {
	store := openLedgerTestStore(t)
	ctx := context.Background()
	tx := beginLedgerTestTx(t, store.DB())
	userID, wallet := seedLedgerUser(t, tx, "delete-negative")
	external, _ := CodedAccount(ctx, tx, "external")
	penalty, _ := NewAntiAbusePenalty(
		Meta{OperationID: mustLedgerID(t, "op_"), ActorUserID: userID, CreatedAt: ledgerTestNow},
		wallet.ID, external.ID, AmountFromMilli(10), "penalty")
	if _, err := Apply(ctx, tx, penalty); err != nil {
		t.Fatal(err)
	}
	deletePlan, _ := NewAccountDeleteZero(
		Meta{OperationID: mustLedgerID(t, "op_"), ActorUserID: userID, CreatedAt: ledgerTestNow + 1},
		wallet.ID, external.ID)
	deleted, err := Apply(ctx, tx, deletePlan)
	if err != nil || len(deleted.Entries) != 2 || deleted.Entries[0].Delta.Decimal() != "10" || deleted.Entries[1].Delta.Decimal() != "-10" {
		t.Fatalf("negative delete operation = (%+v,%v)", deleted, err)
	}
	walletAfter, walletErr := ReadAccount(ctx, tx, wallet.ID)
	externalAfter, externalErr := ReadAccount(ctx, tx, external.ID)
	if walletErr != nil || externalErr != nil || !walletAfter.Balance.IsZero() || !externalAfter.Balance.IsZero() {
		t.Fatalf("negative delete balances wallet=(%s,%v) external=(%s,%v)",
			walletAfter.Balance.Decimal(), walletErr, externalAfter.Balance.Decimal(), externalErr)
	}
	_ = tx.Rollback()
}

func TestDonationCreditChangeIsAtomicAndReplaySafe(t *testing.T) {
	store := openLedgerTestStore(t)
	ctx := context.Background()
	tx := beginLedgerTestTx(t, store.DB())
	userID, wallet := seedLedgerUser(t, tx, "donation-credit")
	external, _ := CodedAccount(ctx, tx, "external")
	meta := Meta{OperationID: mustLedgerID(t, "op_"), ActorUserID: userID, CreatedAt: ledgerTestNow}
	plan, err := NewAdminUserAdjustment(meta, wallet.ID, external.ID, Amount{}, userID, AmountFromMilli(7), "donation credit")
	if err != nil {
		t.Fatal(err)
	}
	result, err := Apply(ctx, tx, plan)
	if err != nil || len(result.Entries) != 0 || result.DonationCreditAfter == nil || result.DonationCreditAfter.Decimal() != "7" {
		t.Fatalf("donation adjustment = (%+v,%v)", result, err)
	}
	replayed, err := Apply(ctx, tx, plan)
	if err != nil || replayed.LedgerSeq != result.LedgerSeq {
		t.Fatalf("donation replay = (%+v,%v)", replayed, err)
	}
	badMeta := Meta{OperationID: mustLedgerID(t, "op_"), ActorUserID: userID, CreatedAt: ledgerTestNow + 1}
	bad, _ := NewAdminUserAdjustment(badMeta, wallet.ID, external.ID, Amount{}, userID, AmountFromMilli(-8), "underflow")
	if _, err := Apply(ctx, tx, bad); !errors.Is(err, ErrInsufficientBalance) {
		t.Fatalf("underflow error = %v", err)
	}
	otherUserID, _ := seedLedgerUser(t, tx, "donation-credit-other")
	mismatchMeta := Meta{OperationID: mustLedgerID(t, "op_"), ActorUserID: userID, CreatedAt: ledgerTestNow + 2}
	mismatch, _ := NewAdminUserAdjustment(mismatchMeta, wallet.ID, external.ID, Amount{}, otherUserID, AmountFromMilli(1), "wrong wallet")
	if _, err := Apply(ctx, tx, mismatch); !errors.Is(err, ErrNotFound) {
		t.Fatalf("donation wallet mismatch error = %v", err)
	}
	var raw []byte
	if err := tx.QueryRow(`SELECT donation_credit_mag FROM users WHERE id=?`, userID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	credit, err := db.DecodeU128(raw)
	var count int
	if countErr := tx.QueryRow(`SELECT count(*) FROM credit_operations`).Scan(&count); countErr != nil {
		t.Fatal(countErr)
	}
	capacity, capacityErr := ReadCapacity(ctx, tx)
	if err != nil || credit.Decimal() != "7" || count != 1 || capacityErr != nil || capacity.LastLedgerSeq != 1 {
		t.Fatalf("post-underflow credit=%s count=%d capacity=%+v errors=(%v,%v)", credit.Decimal(), count, capacity, err, capacityErr)
	}
	_ = tx.Rollback()
}

func TestDatabaseBusyIsRetryableAndWritesNothing(t *testing.T) {
	store := openLedgerTestStore(t)
	store.DB().SetMaxOpenConns(2)
	ctx := context.Background()
	setup := beginLedgerTestTx(t, store.DB())
	userID, wallet := seedLedgerUser(t, setup, "busy")
	external, _ := CodedAccount(ctx, setup, "external")
	if err := setup.Commit(); err != nil {
		t.Fatal(err)
	}

	locker := beginLedgerTestTx(t, store.DB())
	if _, err := locker.Exec(`PRAGMA busy_timeout=0`); err != nil {
		t.Fatal(err)
	}
	if _, err := locker.Exec(`UPDATE credit_capacity SET revision=revision WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	contender := beginLedgerTestTx(t, store.DB())
	if _, err := contender.Exec(`PRAGMA busy_timeout=0`); err != nil {
		t.Fatal(err)
	}
	plan, _ := NewCheckinAward(Meta{OperationID: mustLedgerID(t, "op_"), ActorUserID: userID, CreatedAt: ledgerTestNow}, wallet.ID, external.ID, AmountFromMilli(1))
	if _, err := Apply(ctx, contender, plan); !errors.Is(err, ErrRetryable) {
		t.Fatalf("busy error = %v", err)
	}
	if err := contender.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := locker.Rollback(); err != nil {
		t.Fatal(err)
	}
	var operations int
	if err := store.DB().QueryRow(`SELECT count(*) FROM credit_operations`).Scan(&operations); err != nil {
		t.Fatal(err)
	}
	if operations != 0 {
		t.Fatalf("busy path wrote %d operations", operations)
	}
}

func TestCapacitySQLiteMaxBoundaries(t *testing.T) {
	for _, test := range []struct {
		name      string
		last      int64
		wantError bool
	}{
		{"last slot", math.MaxInt64 - 1, false},
		{"exhausted", math.MaxInt64, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := openLedgerTestStore(t)
			ctx := context.Background()
			tx := beginLedgerTestTx(t, store.DB())
			userID, wallet := seedLedgerUser(t, tx, "max-"+test.name)
			external, _ := CodedAccount(ctx, tx, "external")
			zero := db.EncodeU128(db.U128{})
			if _, err := tx.Exec(`UPDATE credit_capacity SET last_ledger_seq=?,reserved_future_rows=?,revision=? WHERE id=1`, test.last, zero, zero); err != nil {
				t.Fatal(err)
			}
			plan, _ := NewCheckinAward(Meta{OperationID: mustLedgerID(t, "op_"), ActorUserID: userID, CreatedAt: ledgerTestNow}, wallet.ID, external.ID, AmountFromMilli(1))
			result, err := Apply(ctx, tx, plan)
			if test.wantError {
				if !errors.Is(err, ErrCapacityExhausted) {
					t.Fatalf("error = %v", err)
				}
			} else if err != nil || result.LedgerSeq != math.MaxInt64 {
				t.Fatalf("result = (%+v,%v)", result, err)
			}
			_ = tx.Rollback()
		})
	}

	t.Run("future rows equal and exceed boundary", func(t *testing.T) {
		for _, rows := range []struct {
			value     string
			wantError bool
		}{{"1", false}, {"2", true}} {
			store := openLedgerTestStore(t)
			ctx := context.Background()
			tx := beginLedgerTestTx(t, store.DB())
			userID, _ := seedLedgerUser(t, tx, "future-"+rows.value)
			zero := db.EncodeU128(db.U128{})
			if _, err := tx.Exec(`UPDATE credit_capacity SET last_ledger_seq=?,reserved_future_rows=?,revision=? WHERE id=1`, int64(math.MaxInt64-1), zero, zero); err != nil {
				t.Fatal(err)
			}
			requestID := mustLedgerID(t, "req_")
			ref, _ := LogicalRequestReservation(requestID)
			called := false
			value := mustU128(t, rows.value)
			err := Reserve(ctx, tx, ref, value, func(ctx context.Context, tx *sql.Tx) error {
				called = true
				_, err := tx.ExecContext(ctx, `
INSERT INTO logical_requests(
 id,user_id,route_kind,model_snapshot,state,attempt_limit,accounting_state,
 account_reserved_milli,settlement_destination,ledger_rows_remaining,created_at)
VALUES(?,?,'openai_chat_completions','model','accepted',1,'reserved',0,'user',?,?)`,
					requestID, userID, db.EncodeU128(value), ledgerTestNow)
				return err
			})
			if rows.wantError {
				if !errors.Is(err, ErrCapacityExhausted) || called {
					t.Fatalf("reserve = %v called=%v", err, called)
				}
			} else if err != nil || !called {
				t.Fatalf("reserve = %v called=%v", err, called)
			} else {
				replayCalled := false
				err := Reserve(ctx, tx, ref, value, func(ctx context.Context, tx *sql.Tx) error {
					replayCalled = true
					_, err := tx.ExecContext(ctx, `UPDATE logical_requests SET id=id WHERE id=?`, requestID)
					return err
				})
				if err != nil || !replayCalled {
					t.Fatalf("reserve replay at MAX = %v called=%v", err, replayCalled)
				}
			}
			_ = tx.Rollback()
		}
	})
}

func TestRPSQueueReleaseRequiresExactAccountDrain(t *testing.T) {
	store := openLedgerTestStore(t)
	ctx := context.Background()
	tx := beginLedgerTestTx(t, store.DB())
	userID, wallet := seedLedgerUser(t, tx, "rps-release-drain")
	external, _ := CodedAccount(ctx, tx, "external")
	fund, _ := NewAdminUserAdjustment(
		Meta{OperationID: mustLedgerID(t, "op_"), ActorUserID: userID, CreatedAt: ledgerTestNow},
		wallet.ID, external.ID, AmountFromMilli(10), 0, Amount{}, "fund")
	if _, err := Apply(ctx, tx, fund); err != nil {
		t.Fatal(err)
	}
	queueID := mustLedgerID(t, "rpsq_")
	queueAccount, err := CreateRPSQueueAccount(ctx, tx, queueID, ledgerTestNow)
	if err != nil {
		t.Fatal(err)
	}
	one := mustU128(t, "1")
	ref, _ := RPSQueueReservation(queueID)
	reserveMeta := Meta{OperationID: mustLedgerID(t, "op_"), ActorUserID: userID, CreatedAt: ledgerTestNow + 1}
	if err := Reserve(ctx, tx, ref, one, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
INSERT INTO game_rps_queue(
 id,user_id,account_id,mode,revision,reservation_operation_id,reserved,
 ledger_rows_remaining,device_token_hash,source_ip_hash,deadline,created_at)
VALUES(?,?,?,'quick',?,?,?,?,?,?,?,?)`,
			queueID, userID, queueAccount.ID, db.EncodeU128(one), reserveMeta.OperationID,
			db.EncodeU128(mustU128(t, "10")), db.EncodeU128(one), make([]byte, 32), make([]byte, 32), ledgerTestNow+60, ledgerTestNow)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	reservePlan, _ := NewRPSQueueReserve(reserveMeta, queueID, wallet.ID, queueAccount.ID, AmountFromMilli(10))
	if _, err := Apply(ctx, tx, reservePlan); err != nil {
		t.Fatal(err)
	}
	releasePlan, _ := NewRPSQueueRelease(
		Meta{OperationID: mustLedgerID(t, "op_"), ActorUserID: userID, CreatedAt: ledgerTestNow + 2},
		queueID, queueAccount.ID, wallet.ID, AmountFromMilli(9))
	_, err = ConsumeReserved(ctx, tx, ref, releasePlan, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM game_rps_queue WHERE id=?`, queueID)
		return err
	})
	if !errors.Is(err, ErrInvariant) {
		t.Fatalf("under-drain error = %v", err)
	}
	queueBalance, err := ReadAccount(ctx, tx, queueAccount.ID)
	if err != nil || queueBalance.Balance.Decimal() != "10" {
		t.Fatalf("queue balance after rollback = (%s,%v)", queueBalance.Balance.Decimal(), err)
	}
	var queueRows, operations int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM game_rps_queue WHERE id=?`, queueID).Scan(&queueRows); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM credit_operations`).Scan(&operations); err != nil {
		t.Fatal(err)
	}
	if queueRows != 1 || operations != 2 {
		t.Fatalf("rollback queue rows=%d operations=%d", queueRows, operations)
	}
	if err := ValidateRecovery(ctx, tx); err != nil {
		t.Fatalf("recovery after under-drain rollback: %v", err)
	}
	_ = tx.Rollback()
}

func TestRPSSessionStartConsumesQueuesAndReservesSessionH(t *testing.T) {
	store := openLedgerTestStore(t)
	ctx := context.Background()
	tx := beginLedgerTestTx(t, store.DB())
	external, _ := CodedAccount(ctx, tx, "external")
	one := mustU128(t, "1")
	three := mustU128(t, "3")
	zero := db.U128{}
	type queueFixture struct {
		id          string
		account     Account
		userID      int64
		wallet      Account
		reserveMeta Meta
	}
	queues := make([]queueFixture, 0, 3)
	for i := 0; i < 3; i++ {
		userID, wallet := seedLedgerUser(t, tx, "rps-user-"+string(rune('a'+i)))
		fund, _ := NewAdminUserAdjustment(Meta{OperationID: mustLedgerID(t, "op_"), ActorUserID: userID, CreatedAt: ledgerTestNow}, wallet.ID, external.ID, AmountFromMilli(100), 0, Amount{}, "fund")
		if _, err := Apply(ctx, tx, fund); err != nil {
			t.Fatal(err)
		}
		queueID := mustLedgerID(t, "rpsq_")
		queueAccount, err := CreateRPSQueueAccount(ctx, tx, queueID, ledgerTestNow)
		if err != nil {
			t.Fatal(err)
		}
		reserveMeta := Meta{OperationID: mustLedgerID(t, "op_"), ActorUserID: userID, CreatedAt: ledgerTestNow + 1}
		ref, _ := RPSQueueReservation(queueID)
		if err := Reserve(ctx, tx, ref, one, func(ctx context.Context, tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `
INSERT INTO game_rps_queue(
 id,user_id,account_id,mode,revision,reservation_operation_id,reserved,
 ledger_rows_remaining,device_token_hash,source_ip_hash,deadline,created_at)
VALUES(?,?,?,'quick',?,?,?,?,?,?,?,?)`,
				queueID, userID, queueAccount.ID, db.EncodeU128(one), reserveMeta.OperationID,
				db.EncodeU128(mustU128(t, "10")), db.EncodeU128(one), make([]byte, 32), make([]byte, 32), ledgerTestNow+60, ledgerTestNow)
			return err
		}); err != nil {
			t.Fatalf("reserve queue: %v", err)
		}
		queuePlan, _ := NewRPSQueueReserve(reserveMeta, queueID, wallet.ID, queueAccount.ID, AmountFromMilli(10))
		if _, err := Apply(ctx, tx, queuePlan); err != nil {
			t.Fatalf("queue reserve operation: %v", err)
		}
		queues = append(queues, queueFixture{id: queueID, account: queueAccount, userID: userID, wallet: wallet, reserveMeta: reserveMeta})
	}

	sessionID := mustLedgerID(t, "rps_")
	sessionAccount, err := CreateRPSSessionAccount(ctx, tx, sessionID, ledgerTestNow+2)
	if err != nil {
		t.Fatal(err)
	}
	inputs := [3]RPSQueueInput{}
	for i, queue := range queues {
		inputs[i] = RPSQueueInput{QueueID: queue.id, AccountID: queue.account.ID, Amount: AmountFromMilli(10)}
	}
	startMeta := Meta{OperationID: mustLedgerID(t, "op_"), CreatedAt: ledgerTestNow + 2}
	startPlan, err := NewRPSSessionStart(startMeta, sessionID, sessionAccount.ID, three, inputs)
	if err != nil {
		t.Fatal(err)
	}
	primary := *startPlan.spec.capacity.consume
	started, err := ConsumeReserved(ctx, tx, primary, startPlan, func(ctx context.Context, tx *sql.Tx) error {
		for _, queue := range queues {
			if _, err := tx.ExecContext(ctx, `DELETE FROM game_rps_queue WHERE id=?`, queue.id); err != nil {
				return err
			}
		}
		_, err := tx.ExecContext(ctx, `
INSERT INTO game_rps_sessions(
 id,account_id,mode,rules_version,state,phase,revision,phase_seq,identity_epoch,cut_seq,
 ledger_rows_remaining,dealer_seat,base_milli,platform_bp,welfare_bp,thursday_bp,
 gesture_seconds,dealer_seconds,follower_seconds,player_pool,permanent_multiplier,
 pool_base_multiplier,current_plan_multiplier,dealer_raise,base_round_count,paid_tie_count,
 free_tie_count,paid_pool_streak,free_pool_streak,platform_cut_total,welfare_cut_total,
 thursday_cut_total,welfare_carry_total,reminder_state,phase_deadline,health_epoch,
 recent_events_blob,recent_first_seq,recent_last_seq,recent_event_count,
 terminal_operation_id,terminal_retry_attempt_count,terminal_next_retry_at,
 terminal_last_error_class,started_at,terminal_reason)
VALUES(?,?,'quick',1,?,?,?,?,?, ?,?,0,5,0,0,0,20,15,15,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?, ?,0,X'',?,?,0,?, ?,NULL,NULL,?,?)`,
			sessionID, sessionAccount.ID, "started", "gesture",
			db.EncodeU128(one), db.EncodeU128(one), db.EncodeU128(one), db.EncodeU128(zero), db.EncodeU128(three),
			db.EncodeU128(zero), db.EncodeU128(one), nil, db.EncodeU128(one), nil,
			db.EncodeU128(zero), db.EncodeU128(zero), db.EncodeU128(zero), db.EncodeU128(zero), db.EncodeU128(zero),
			db.EncodeU128(zero), db.EncodeU128(zero), db.EncodeU128(zero), db.EncodeU128(zero),
			"none", ledgerTestNow+20, db.EncodeU128(zero), db.EncodeU128(zero), nil,
			db.EncodeU128(zero), ledgerTestNow, nil)
		return err
	})
	if err != nil {
		t.Fatalf("session start: %v", err)
	}
	if len(started.Entries) != 4 {
		t.Fatalf("start entries = %d", len(started.Entries))
	}
	capacity, err := ReadCapacity(ctx, tx)
	if err != nil || capacity.ReservedFutureRows.Decimal() != "3" {
		t.Fatalf("capacity after start = (%+v,%v)", capacity, err)
	}
	if err := ValidateRecovery(ctx, tx); err != nil {
		t.Fatalf("RPS recovery invariant: %v", err)
	}
	var welfareAccountID int64
	if err := tx.QueryRowContext(ctx, `SELECT account_id FROM shared_pools WHERE pool_type='welfare'`).Scan(&welfareAccountID); err != nil {
		t.Fatal(err)
	}
	terminalPlan, err := NewRPSTerminal(
		Meta{OperationID: mustLedgerID(t, "op_"), CreatedAt: ledgerTestNow + 3},
		sessionID, sessionAccount.ID, external.ID, welfareAccountID,
		[]RPSTerminalPayout{{UserAccountID: queues[0].wallet.ID, Amount: AmountFromMilli(30)}}, Amount{}, Amount{})
	if err != nil {
		t.Fatal(err)
	}
	terminalRef := *terminalPlan.spec.capacity.consume
	terminal, err := ConsumeReserved(ctx, tx, terminalRef, terminalPlan, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM game_rps_sessions WHERE id=?`, sessionID)
		return err
	})
	if err != nil || len(terminal.Entries) != 3 {
		t.Fatalf("terminal consumes H remainder = (%+v,%v)", terminal, err)
	}
	capacity, err = ReadCapacity(ctx, tx)
	if err != nil || capacity.ReservedFutureRows.Big().Sign() != 0 {
		t.Fatalf("capacity after terminal = (%+v,%v)", capacity, err)
	}
	sessionBalance, err := ReadAccount(ctx, tx, sessionAccount.ID)
	if err != nil || !sessionBalance.Balance.IsZero() {
		t.Fatalf("session balance after terminal = (%s,%v)", sessionBalance.Balance.Decimal(), err)
	}
	replayPlan, err := NewRPSTerminal(
		Meta{OperationID: mustLedgerID(t, "op_"), CreatedAt: ledgerTestNow + 4},
		sessionID, sessionAccount.ID, external.ID, welfareAccountID,
		[]RPSTerminalPayout{{UserAccountID: queues[0].wallet.ID, Amount: AmountFromMilli(99)}}, Amount{}, Amount{})
	if err != nil {
		t.Fatal(err)
	}
	replayCalled := false
	replayed, err := ConsumeReserved(ctx, tx, *replayPlan.spec.capacity.consume, replayPlan, func(context.Context, *sql.Tx) error {
		replayCalled = true
		return errors.New("must not run")
	})
	if err != nil || replayCalled || replayed.OperationID != terminal.OperationID || replayed.LedgerSeq != terminal.LedgerSeq {
		t.Fatalf("terminal replay = (%+v,%v), called=%v", replayed, err, replayCalled)
	}
	if err := ValidateRecovery(ctx, tx); err != nil {
		t.Fatalf("RPS terminal recovery invariant: %v", err)
	}
	_ = tx.Rollback()
}

func TestConcurrentShuffleExactOnce(t *testing.T) {
	store := openLedgerTestStore(t)
	store.DB().SetMaxOpenConns(12)
	ctx := context.Background()
	tx := beginLedgerTestTx(t, store.DB())
	userID, wallet := seedLedgerUser(t, tx, "shuffle")
	external, _ := CodedAccount(ctx, tx, "external")
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	const operations = 24
	plans := make([]Plan, operations)
	for i := range plans {
		plan, err := NewCheckinAward(Meta{OperationID: mustLedgerID(t, "op_"), ActorUserID: userID, CreatedAt: ledgerTestNow + int64(i)}, wallet.ID, external.ID, AmountFromMilli(1))
		if err != nil {
			t.Fatal(err)
		}
		plans[i] = plan
	}
	var wg sync.WaitGroup
	var failures atomic.Int64
	for _, plan := range plans {
		for duplicate := 0; duplicate < 2; duplicate++ {
			wg.Add(1)
			go func(plan Plan) {
				defer wg.Done()
				for attempt := 0; attempt < 100; attempt++ {
					tx, err := store.DB().BeginTx(ctx, nil)
					if err != nil {
						failures.Add(1)
						return
					}
					_, err = Apply(ctx, tx, plan)
					if err == nil {
						err = tx.Commit()
					} else {
						_ = tx.Rollback()
					}
					if err == nil {
						return
					}
					if !errors.Is(err, ErrRetryable) {
						failures.Add(1)
						return
					}
					time.Sleep(time.Duration(attempt%3+1) * time.Millisecond)
				}
				failures.Add(1)
			}(plan)
		}
	}
	wg.Wait()
	if failures.Load() != 0 {
		t.Fatalf("concurrent failures = %d", failures.Load())
	}

	tx = beginLedgerTestTx(t, store.DB())
	var count int
	if err := tx.QueryRow(`SELECT count(*) FROM credit_operations`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	account, err := ReadAccount(ctx, tx, wallet.ID)
	if err != nil || count != operations || account.Balance.Decimal() != "24" {
		t.Fatalf("count=%d balance=%s err=%v", count, account.Balance.Decimal(), err)
	}
	if err := ValidateRecovery(ctx, tx); err != nil {
		t.Fatalf("shuffle recovery: %v", err)
	}
	_ = tx.Rollback()
}
