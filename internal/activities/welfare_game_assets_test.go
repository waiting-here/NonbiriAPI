package activities

import (
	"context"
	"database/sql"
	"errors"
	"math/big"
	"net/http"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
)

func TestWelfareGameHoldsEnterAndLeaveAssetSnapshot(t *testing.T) {
	fixture := newActivityFixture(t, 1_804_000_000)
	userID, _ := fixture.seedUser("game-holds", false)
	zero := db.EncodeU128(db.U128{})
	one := db.EncodeU128(oneU128())

	fishingID := mustActivityID(t, "fb_")
	if _, err := fixture.store.DB().Exec(`
INSERT INTO game_fishing_batches(
 id,user_id,bait,count,unit_price_milli,entry_total_milli,payout_total_milli,
 operation_id,request_hash,state,ledger_rows_remaining,attempt_count,next_attempt_at,
 last_error_class,retry_exhausted,created_at,settled_at,revealed_at)
VALUES(?,?,'worm',1,20,20,0,?,?, 'reserved',?,0,?,NULL,0,?,NULL,NULL)`,
		fishingID, userID, mustActivityID(t, "op_"), make([]byte, 32), one,
		fixture.clock.Load()+120, fixture.clock.Load()); err != nil {
		t.Fatal(err)
	}
	assertWelfareAssets(t, fixture, userID, "20")
	if _, err := fixture.store.DB().Exec(`DELETE FROM game_fishing_batches WHERE id=?`, fishingID); err != nil {
		t.Fatal(err)
	}
	assertWelfareAssets(t, fixture, userID, "0")

	queueID := mustActivityID(t, "rpsq_")
	tx, err := fixture.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	queueAccount, err := ledger.CreateRPSQueueAccount(context.Background(), tx, queueID, fixture.clock.Load())
	if err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if _, err := tx.Exec(`
INSERT INTO game_rps_queue(
 id,user_id,account_id,mode,revision,reservation_operation_id,reserved,
 ledger_rows_remaining,device_token_hash,source_ip_hash,deadline,created_at)
VALUES(?,?,?,'quick',?,?,?,?,?,?,?,?)`, queueID, userID, queueAccount.ID, one,
		mustActivityID(t, "op_"), db.EncodeU128(mustActivityU128(t, "30")), one,
		make([]byte, 32), make([]byte, 32), fixture.clock.Load()+60, fixture.clock.Load()); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	assertWelfareAssets(t, fixture, userID, "30")
	if _, err := fixture.store.DB().Exec(`DELETE FROM game_rps_queue WHERE id=?`, queueID); err != nil {
		t.Fatal(err)
	}
	assertWelfareAssets(t, fixture, userID, "0")

	sessionID := mustActivityID(t, "rps_")
	tx, err = fixture.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	sessionAccount, err := ledger.CreateRPSSessionAccount(context.Background(), tx, sessionID, fixture.clock.Load())
	if err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	forty := db.EncodeU128(mustActivityU128(t, "40"))
	if _, err := tx.Exec(`
INSERT INTO game_rps_sessions(
 id,account_id,mode,rules_version,state,phase,revision,phase_seq,identity_epoch,cut_seq,
 ledger_rows_remaining,base_milli,platform_bp,welfare_bp,thursday_bp,gesture_seconds,
 dealer_seconds,follower_seconds,player_pool,permanent_multiplier,current_plan_multiplier,
 base_round_count,paid_tie_count,free_tie_count,paid_pool_streak,free_pool_streak,
 platform_cut_total,welfare_cut_total,thursday_cut_total,welfare_carry_total,
 phase_deadline,recent_first_seq,recent_last_seq,terminal_retry_attempt_count,started_at)
VALUES(?,?,'quick',1,'started','gesture',?,?,?,?,?,5,0,0,0,20,15,15,?,?,?,
 ?,?,?,?,?,?,?,?,?,?,?,?, ?,?)`,
		sessionID, sessionAccount.ID, one, one, one, zero, zero, forty, one, one,
		zero, zero, zero, zero, zero, zero, zero, zero, zero,
		fixture.clock.Load()+20, zero, zero, zero, fixture.clock.Load()); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if _, err := tx.Exec(`
INSERT INTO game_rps_seats(
 session_id,seat_no,user_id,deletion_state,starting_balance,current_balance,
 current_round_input,current_all_in,total_input,total_returned,rock_count,
 scissors_count,paper_count,timeout_count)
VALUES(?,0,?,'active',?,?,?,0,?,?,?,?,?,?)`, sessionID, userID, forty, forty, zero,
		make([]byte, 32), make([]byte, 32), zero, zero, zero, zero); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	assertWelfareAssets(t, fixture, userID, "40")
	if _, err := fixture.store.DB().Exec(`DELETE FROM game_rps_sessions WHERE id=?`, sessionID); err != nil {
		t.Fatal(err)
	}
	assertWelfareAssets(t, fixture, userID, "0")
}

func TestWelfareWidePositiveAssetsAreIneligibleAndClaimConflictsWithoutWrites(t *testing.T) {
	fixture := newActivityFixture(t, 1_804_000_100)
	userID, wallet := fixture.seedUser("wide-positive-assets", false)
	fixture.fundUser(userID, db.MaxMoneyMilli)
	fixture.fundUser(userID, 1)
	wideTotal := big.NewInt(db.MaxMoneyMilli + 1)
	assertWelfareAssets(t, fixture, userID, wideTotal.String())
	validateLedgerRecovery(t, fixture.store.DB())
	fixture.setActivityConfig(true, true, false, db.MaxMoneyMilli, 100)
	projection, err := fixture.repository.GetActivities(context.Background(), userID)
	if err != nil || projection.Welfare.State != WelfareStateIneligible {
		t.Fatalf("wide positive projection=%+v err=%v", projection.Welfare, err)
	}

	before := captureWelfareMutationStorage(t, fixture, wallet.ID)
	if _, _, err := fixture.repository.ClaimWelfare(context.Background(), userID,
		fixture.control(http.MethodPost, routeWelfareClaims, nil)); !errors.Is(err, ErrConflict) {
		t.Fatalf("wide positive claim error=%v", err)
	}
	after := captureWelfareMutationStorage(t, fixture, wallet.ID)
	if after != before {
		t.Fatalf("ineligible claim wrote state\n before=%+v\n  after=%+v", before, after)
	}
	validateLedgerRecovery(t, fixture.store.DB())
}

func TestWelfareWideNegativeWalletCanClaimAndConservesBalance(t *testing.T) {
	fixture := newActivityFixture(t, 1_804_000_200)
	userID, wallet := fixture.seedUser("wide-negative-wallet", false)
	fixture.fundUser(userID, -db.MaxMoneyMilli)
	fixture.fundUser(userID, -1000)
	var welfarePoolID string
	var welfarePoolAccountID int64
	if err := fixture.store.DB().QueryRow(`
SELECT id,account_id FROM shared_pools WHERE pool_type='welfare' AND state='open'`).Scan(
		&welfarePoolID, &welfarePoolAccountID); err != nil {
		t.Fatal(err)
	}
	fixture.fundPool(welfarePoolID, 1000)
	fixture.setActivityConfig(true, true, false, 0, 100)

	projection, err := fixture.repository.GetActivities(context.Background(), userID)
	if err != nil || projection.Welfare.State != WelfareStateAvailable {
		t.Fatalf("wide negative projection=%+v err=%v", projection.Welfare, err)
	}
	beforeWallet := readActivityAccountBalance(t, fixture.store.DB(), wallet.ID)
	beforePool := readActivityAccountBalance(t, fixture.store.DB(), welfarePoolAccountID)
	if beforeWallet.Cmp(new(big.Int).Neg(big.NewInt(db.MaxMoneyMilli+1000))) != 0 || beforePool.Cmp(big.NewInt(1000)) != 0 {
		t.Fatalf("unexpected pre-claim balances wallet=%s pool=%s", beforeWallet, beforePool)
	}

	claimed, facts, err := fixture.repository.ClaimWelfare(context.Background(), userID,
		fixture.control(http.MethodPost, routeWelfareClaims, nil))
	if err != nil {
		t.Fatal(err)
	}
	afterWallet := readActivityAccountBalance(t, fixture.store.DB(), wallet.ID)
	afterPool := readActivityAccountBalance(t, fixture.store.DB(), welfarePoolAccountID)
	walletDelta := new(big.Int).Sub(afterWallet, beforeWallet)
	poolDelta := new(big.Int).Sub(afterPool, beforePool)
	if claimed.Value.Awarded != "0.1" || claimed.Value.Balance != formatMilliPoints(afterWallet) ||
		claimed.Value.PoolBalance != "0.9" || !facts.Global || walletDelta.Cmp(big.NewInt(100)) != 0 ||
		poolDelta.Cmp(big.NewInt(-100)) != 0 || new(big.Int).Add(walletDelta, poolDelta).Sign() != 0 ||
		new(big.Int).Abs(afterWallet).Cmp(big.NewInt(db.MaxMoneyMilli)) <= 0 {
		t.Fatalf("wide negative claim=%+v facts=%+v wallet %s->%s pool %s->%s",
			claimed.Value, facts, beforeWallet, afterWallet, beforePool, afterPool)
	}
	validateLedgerRecovery(t, fixture.store.DB())
}

func TestWelfareAssetSourceValuesRemainStrict(t *testing.T) {
	for _, test := range []struct {
		name string
		add  func(*big.Int) error
	}{
		{name: "negative logical request hold", add: func(total *big.Int) error {
			return addWelfareNonnegativeInt(total, -1)
		}},
		{name: "negative fishing hold", add: func(total *big.Int) error {
			return addWelfareNonnegativeInt(total, -1)
		}},
		{name: "short RPS queue codec", add: func(total *big.Int) error {
			return addWelfareU128(total, make([]byte, 15))
		}},
		{name: "long RPS seat codec", add: func(total *big.Int) error {
			return addWelfareU128(total, make([]byte, 17))
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			total := big.NewInt(41)
			if err := test.add(total); !errors.Is(err, ErrInvariant) || total.Cmp(big.NewInt(41)) != 0 {
				t.Fatalf("hostile source total=%s err=%v", total, err)
			}
		})
	}
}

type welfareMutationStorage struct {
	lastLedgerSeq      int64
	reservedFutureRows string
	capacityRevision   string
	walletSign         int
	walletMagnitude    string
	operations         int
	entries            int
	claims             int
	idempotencyRecords int
}

func captureWelfareMutationStorage(t *testing.T, fixture *activityFixture, walletID int64) welfareMutationStorage {
	t.Helper()
	var state welfareMutationStorage
	if err := fixture.store.DB().QueryRow(`
SELECT last_ledger_seq,hex(reserved_future_rows),hex(revision) FROM credit_capacity WHERE id=1`).Scan(
		&state.lastLedgerSeq, &state.reservedFutureRows, &state.capacityRevision); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.DB().QueryRow(`SELECT balance_sign,hex(balance_mag) FROM credit_accounts WHERE id=?`, walletID).Scan(
		&state.walletSign, &state.walletMagnitude); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.DB().QueryRow(`
SELECT
 (SELECT COUNT(*) FROM credit_operations),
 (SELECT COUNT(*) FROM credit_entries),
 (SELECT COUNT(*) FROM welfare_claims),
 (SELECT COUNT(*) FROM idempotency_records)`).Scan(
		&state.operations, &state.entries, &state.claims, &state.idempotencyRecords); err != nil {
		t.Fatal(err)
	}
	return state
}

func readActivityAccountBalance(t *testing.T, database *sql.DB, accountID int64) *big.Int {
	t.Helper()
	tx, err := database.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	account, err := ledger.ReadAccount(context.Background(), tx, accountID)
	if err != nil {
		t.Fatal(err)
	}
	return account.Balance.Big()
}

func mustActivityID(t *testing.T, prefix string) string {
	t.Helper()
	id, err := db.GenerateOpaqueID(prefix)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func mustActivityU128(t *testing.T, decimal string) db.U128 {
	t.Helper()
	value, err := db.ParseU128Decimal(decimal)
	if err != nil {
		t.Fatal(err)
	}
	return value
}
