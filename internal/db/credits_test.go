package db

import (
	"bytes"
	"context"
	"errors"
	"math"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/credits"
	"github.com/waiting-here/NonbiriAPI/internal/dbtest"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

const testLedgerNow = 1_700_000_000

func ledgerTime() time.Time { return time.Unix(testLedgerNow, 0) }

func newCreditsTestStore(t *testing.T) *Store {
	t.Helper()
	st := openTestStore(t, filepath.Join(t.TempDir(), "credits.db"))
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func userBalancesOf(t *testing.T, st *Store, uid int64) (int64, int64) {
	t.Helper()
	var c, d int64
	if err := st.DB().QueryRow(`SELECT credits, donation_credit FROM users WHERE id=?`, uid).Scan(&c, &d); err != nil {
		t.Fatalf("read balances: %v", err)
	}
	return c, d
}

func countLedger(t *testing.T, st *Store, opID string) int {
	t.Helper()
	var n int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM credit_ledger WHERE operation_id=?`, opID).Scan(&n); err != nil {
		t.Fatalf("count ledger: %v", err)
	}
	return n
}

func TestFreshUsersGetZeroEconomyColumns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "economy-columns.db")
	key := bytes.Repeat([]byte{0x29}, secret.MasterKeyBytes)
	vault, err := secret.New(key)
	clear(key)
	if err != nil {
		t.Fatalf("create test secret vault: %v", err)
	}
	defer vault.Close()
	dbtest.EnsureOwnerOnlyParent(t, path)
	st, err := Open(path, vault)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	uid := seedUserRaw(t, st, "economy-zero")
	c, d := userBalancesOf(t, st, uid)
	if c != 0 || d != 0 {
		t.Fatalf("fresh balances = (%d, %d), want (0, 0)", c, d)
	}
	if _, err := st.ApplyAdminCreditAdjustment(context.Background(), AdminCreditAdjustment{
		TargetUserID: uid, ActorUserID: seedUserRaw(t, st, "actor"),
		OperationID: "op-open-again", Reason: "seed", CreditsSet: true, CreditsDelta: 5,
		CreatedAt: ledgerTime(),
	}); err != nil {
		t.Fatalf("seed adjustment: %v", err)
	}
	// Reopening the same current database must be idempotent: no startup error,
	// no reset of stored balances.
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st2, err := Open(path, vault)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer st2.Close()
	if c, d := userBalancesOf(t, st2, uid); c != 5 || d != 0 {
		t.Fatalf("balances after reopen = (%d, %d), want (5, 0)", c, d)
	}
}

func TestApplyCreditOperationAdjustsAndAudits(t *testing.T) {
	st := newCreditsTestStore(t)
	uid := seedUserRaw(t, st, "ledger-basic")
	actor := seedUserRaw(t, st, "ledger-actor")

	res, err := st.ApplyCreditOperation(context.Background(), CreditOperation{
		Kind: LedgerAdminAdjustment, UserID: uid, ActorUserID: actor,
		OperationID: "adj-1", CreditsDelta: 500, DonationCreditDelta: 25,
		Reason: "compensation", CreatedAt: ledgerTime(),
	})
	if err != nil || !res.Applied {
		t.Fatalf("apply = (%+v, %v)", res, err)
	}
	if res.CreditsAfter != 500 || res.DonationCreditAfter != 25 {
		t.Fatalf("after values = %+v", res)
	}
	if c, d := userBalancesOf(t, st, uid); c != 500 || d != 25 {
		t.Fatalf("balances after apply = (%d, %d)", c, d)
	}

	// Negative delta driving credits below zero is frozen behavior.
	res, err = st.ApplyCreditOperation(context.Background(), CreditOperation{
		Kind: LedgerCharitySettlement, UserID: uid,
		OperationID: "sys.settlement.s1", CreditsDelta: -900, CreatedAt: ledgerTime(),
	})
	if err != nil || !res.Applied || res.CreditsAfter != -400 {
		t.Fatalf("settlement to negative = (%+v, %v)", res, err)
	}
	if c, _ := userBalancesOf(t, st, uid); c != -400 {
		t.Fatalf("negative balance not persisted: %d", c)
	}
}

func TestApplyCreditOperationReplayReturnsFirstResult(t *testing.T) {
	st := newCreditsTestStore(t)
	uid := seedUserRaw(t, st, "replay")

	first, err := st.ApplyCreditOperation(context.Background(), CreditOperation{
		Kind: LedgerAdminAdjustment, UserID: uid, ActorUserID: uid,
		OperationID: "retry-me", CreditsDelta: 100, Reason: "first", CreatedAt: ledgerTime(),
	})
	if err != nil || !first.Applied {
		t.Fatalf("first apply = (%+v, %v)", first, err)
	}
	// A retry with a DIFFERENT delta but the same operation id must return the
	// first result and write nothing.
	second, err := st.ApplyCreditOperation(context.Background(), CreditOperation{
		Kind: LedgerAdminAdjustment, UserID: uid, ActorUserID: uid,
		OperationID: "retry-me", CreditsDelta: 999999, Reason: "retry", CreatedAt: ledgerTime(),
	})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if second.Applied {
		t.Fatal("replay reported Applied=true")
	}
	if second.CreditsAfter != 100 || second.CreditsDelta != 100 {
		t.Fatalf("replay result = %+v, want the first application's snapshot", second)
	}
	if c, _ := userBalancesOf(t, st, uid); c != 100 {
		t.Fatalf("balance after replay = %d, want 100", c)
	}
	if n := countLedger(t, st, "retry-me"); n != 1 {
		t.Fatalf("ledger rows for replayed operation = %d, want 1", n)
	}
}

func TestApplyCreditOperationNamespaceIsolation(t *testing.T) {
	st := newCreditsTestStore(t)
	uid := seedUserRaw(t, st, "namespace")

	sysOp := CreditOperation{Kind: LedgerCharityReserve, UserID: uid, OperationID: "plain-id", CreatedAt: ledgerTime()}
	if _, err := st.ApplyCreditOperation(context.Background(), sysOp); !errors.Is(err, ErrConflict) {
		t.Fatalf("system kind without sys. prefix = %v, want ErrConflict", err)
	}
	clientOp := CreditOperation{Kind: LedgerAdminAdjustment, UserID: uid, ActorUserID: uid,
		OperationID: "sys.stolen", Reason: "x", CreatedAt: ledgerTime()}
	if _, err := st.ApplyCreditOperation(context.Background(), clientOp); !errors.Is(err, ErrConflict) {
		t.Fatalf("client op in system namespace = %v, want ErrConflict", err)
	}
	for _, bad := range []string{"", "with space", "exotic/char", string(make([]byte, 129))} {
		if _, err := st.ApplyCreditOperation(context.Background(), CreditOperation{
			Kind: LedgerAdminAdjustment, UserID: uid, ActorUserID: uid,
			OperationID: bad, Reason: "x", CreatedAt: ledgerTime(),
		}); !errors.Is(err, ErrConflict) {
			t.Fatalf("invalid operation id %q = %v, want ErrConflict", bad, err)
		}
	}
	if _, err := st.ApplyCreditOperation(context.Background(), CreditOperation{
		Kind: CreditLedgerKind("mystery"), UserID: uid,
		OperationID: "sys.x", CreatedAt: ledgerTime(),
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("unknown kind = %v, want ErrConflict", err)
	}
}

func TestApplyCreditOperationDonationFloorAndOverflow(t *testing.T) {
	st := newCreditsTestStore(t)
	uid := seedUserRaw(t, st, "floor")
	if _, err := st.DB().Exec(`UPDATE users SET donation_credit=10 WHERE id=?`, uid); err != nil {
		t.Fatal(err)
	}
	// Donation result below zero is rejected before any write.
	if _, err := st.ApplyCreditOperation(context.Background(), CreditOperation{
		Kind: LedgerDonorReward, UserID: uid,
		OperationID: "sys.reward.neg", DonationCreditDelta: -11, CreatedAt: ledgerTime(),
	}); !errors.Is(err, ErrDonationCreditNegative) {
		t.Fatalf("donation floor = %v, want ErrDonationCreditNegative", err)
	}
	if _, d := userBalancesOf(t, st, uid); d != 10 {
		t.Fatalf("donation balance changed on rejected op: %d", d)
	}
	if n := countLedger(t, st, "sys.reward.neg"); n != 0 {
		t.Fatal("rejected operation left a ledger row")
	}
	// Exactly reaching zero is allowed.
	if _, err := st.ApplyCreditOperation(context.Background(), CreditOperation{
		Kind: LedgerDonorReward, UserID: uid,
		OperationID: "sys.reward.zero", DonationCreditDelta: -10, CreatedAt: ledgerTime(),
	}); err != nil {
		t.Fatalf("donation to exactly zero: %v", err)
	}
	// Overflow fails closed and leaves no partial state: seed the balance to
	// MaxInt64, then any positive delta overflows.
	if _, err := st.DB().Exec(`UPDATE users SET credits=? WHERE id=?`, int64(math.MaxInt64), uid); err != nil {
		t.Fatal(err)
	}
	_, err := st.ApplyCreditOperation(context.Background(), CreditOperation{
		Kind: LedgerAdminAdjustment, UserID: uid, ActorUserID: uid,
		OperationID: "overflow-1", CreditsDelta: 1,
		Reason: "big", CreatedAt: ledgerTime(),
	})
	if !errors.Is(err, credits.ErrOverflow) {
		t.Fatalf("overflow error = %v, want ErrOverflow", err)
	}
	if c, _ := userBalancesOf(t, st, uid); c != math.MaxInt64 {
		t.Fatalf("failed overflow changed balance: %d", c)
	}
	if n := countLedger(t, st, "overflow-1"); n != 0 {
		t.Fatal("overflowed operation left a ledger row")
	}
}

func TestApplyCreditOperationOwnershipErrors(t *testing.T) {
	st := newCreditsTestStore(t)
	admin, err := st.EnsureAdminUser("root")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ApplyCreditOperation(context.Background(), CreditOperation{
		Kind: LedgerAdminAdjustment, UserID: admin.ID, ActorUserID: admin.ID,
		OperationID: "on-admin", Reason: "x", CreditsDelta: 1, CreatedAt: ledgerTime(),
	}); !errors.Is(err, ErrAdminProtected) {
		t.Fatalf("admin target = %v, want ErrAdminProtected", err)
	}
	if _, err := st.ApplyCreditOperation(context.Background(), CreditOperation{
		Kind: LedgerCharityRelease, UserID: 424242,
		OperationID: "sys.rel.missing", CreditsDelta: 1, CreatedAt: ledgerTime(),
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing target = %v, want ErrNotFound", err)
	}
}

func TestDBFailureRollsBackBalanceAndLedger(t *testing.T) {
	st := newCreditsTestStore(t)
	uid := seedUserRaw(t, st, "rollback")
	// Temporary trigger that aborts any transaction touching credit_ledger at
	// commit time via a deferred-FK violation — same technique as the secret
	// commit-rollback tests.
	schema := `
CREATE TABLE ledger_commit_guard (
  id INTEGER PRIMARY KEY,
  user_id INTEGER NOT NULL REFERENCES users(id) DEFERRABLE INITIALLY DEFERRED
);
CREATE TRIGGER fail_ledger_commit
AFTER INSERT ON credit_ledger
BEGIN
  INSERT INTO ledger_commit_guard (user_id) VALUES (9223372036854775807);
END;`
	if _, err := st.DB().Exec(schema); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ApplyCreditOperation(context.Background(), CreditOperation{
		Kind: LedgerAdminAdjustment, UserID: uid, ActorUserID: uid,
		OperationID: "doomed-op", Reason: "x", CreditsDelta: 77, CreatedAt: ledgerTime(),
	}); err == nil {
		t.Fatal("commit failure unexpectedly succeeded")
	}
	if c, _ := userBalancesOf(t, st, uid); c != 0 {
		t.Fatalf("balance leaked past failed commit: %d", c)
	}
	if n := countLedger(t, st, "doomed-op"); n != 0 {
		t.Fatal("failed commit left a ledger row")
	}
	// The same operation must apply cleanly once the failure trigger is gone.
	if _, err := st.DB().Exec(`DROP TRIGGER fail_ledger_commit`); err != nil {
		t.Fatal(err)
	}
	res, err := st.ApplyCreditOperation(context.Background(), CreditOperation{
		Kind: LedgerAdminAdjustment, UserID: uid, ActorUserID: uid,
		OperationID: "doomed-op", Reason: "x", CreditsDelta: 77, CreatedAt: ledgerTime(),
	})
	if err != nil || !res.Applied || res.CreditsAfter != 77 {
		t.Fatalf("retry after failure = (%+v, %v)", res, err)
	}
}

func TestReserveCreditsConditionalDebit(t *testing.T) {
	st := newCreditsTestStore(t)
	uid := seedUserRaw(t, st, "reserve")
	if _, err := st.DB().Exec(`UPDATE users SET credits=1000 WHERE id=?`, uid); err != nil {
		t.Fatal(err)
	}

	res, err := st.ReserveCredits(context.Background(), CreditReserveInput{
		UserID: uid, Amount: 400, ReservationID: 11,
		OperationID: "sys.reserve.r1", CreatedAt: ledgerTime(),
	})
	if err != nil || !res.Applied || res.CreditsAfter != 600 {
		t.Fatalf("reserve = (%+v, %v)", res, err)
	}

	// Insufficient balance refuses with nothing written.
	if _, err := st.ReserveCredits(context.Background(), CreditReserveInput{
		UserID: uid, Amount: 601, OperationID: "sys.reserve.r2", CreatedAt: ledgerTime(),
	}); !errors.Is(err, ErrInsufficientCredits) {
		t.Fatalf("over-reserve = %v, want ErrInsufficientCredits", err)
	}
	if c, _ := userBalancesOf(t, st, uid); c != 600 {
		t.Fatalf("failed reserve changed balance: %d", c)
	}
	if n := countLedger(t, st, "sys.reserve.r2"); n != 0 {
		t.Fatal("failed reserve wrote a ledger row")
	}

	// Zero-amount reserve never rejects a negative balance.
	if _, err := st.DB().Exec(`UPDATE users SET credits=-50 WHERE id=?`, uid); err != nil {
		t.Fatal(err)
	}
	res, err = st.ReserveCredits(context.Background(), CreditReserveInput{
		UserID: uid, Amount: 0, OperationID: "sys.reserve.free", CreatedAt: ledgerTime(),
	})
	if err != nil || !res.Applied || res.CreditsAfter != -50 {
		t.Fatalf("zero reserve = (%+v, %v), want applied with unchanged negative balance", res, err)
	}

	// Release refunds exactly once; replays return the first release.
	rel, err := st.ReleaseReservation(context.Background(), CreditReserveInput{
		UserID: uid, Amount: 400, ReservationID: 11,
		OperationID: "sys.release.r1", CreatedAt: ledgerTime(),
	})
	if err != nil || rel.CreditsAfter != 350 {
		t.Fatalf("release = (%+v, %v)", rel, err)
	}
	rel2, err := st.ReleaseReservation(context.Background(), CreditReserveInput{
		UserID: uid, Amount: 400, ReservationID: 11,
		OperationID: "sys.release.r1", CreatedAt: ledgerTime(),
	})
	if err != nil || rel2.Applied || rel2.CreditsAfter != 350 {
		t.Fatalf("release replay = (%+v, %v), want no-op first-result", rel2, err)
	}
}

func TestConcurrentReservesNeverOverdraw(t *testing.T) {
	st := newCreditsTestStore(t)
	uid := seedUserRaw(t, st, "concurrent-reserve")
	const balance = 1_000
	if _, err := st.DB().Exec(`UPDATE users SET credits=? WHERE id=?`, balance, uid); err != nil {
		t.Fatal(err)
	}

	const goroutines = 16
	const amount = 300 // total demand 4800 >> balance
	var wg sync.WaitGroup
	var mu sync.Mutex
	okCount, insufficient := 0, 0
	start := make(chan struct{})
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, err := st.ReserveCredits(context.Background(), CreditReserveInput{
				UserID: uid, Amount: amount,
				OperationID: "sys.reserve.concurrent." + itoa(i),
				CreatedAt:   ledgerTime(),
			})
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				okCount++
			case errors.Is(err, ErrInsufficientCredits):
				insufficient++
			default:
				t.Errorf("unexpected reserve error: %v", err)
			}
		}(i)
	}
	close(start)
	wg.Wait()

	final, _ := userBalancesOf(t, st, uid)
	if okCount+insufficient != goroutines {
		t.Fatalf("outcomes = %d ok + %d insufficient, want %d total", okCount, insufficient, goroutines)
	}
	if final != balance-int64(okCount)*amount {
		t.Fatalf("final balance = %d, want %d (ok=%d)", final, balance-int64(okCount)*amount, okCount)
	}
	if final < 0 {
		t.Fatalf("concurrent reserves overdrawn the guarded balance: %d", final)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [20]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(b[pos:])
}

func TestDeleteUserCascadesCreditLedger(t *testing.T) {
	st := newCreditsTestStore(t)
	uid := seedUserRaw(t, st, "delete-ledger")
	if _, err := st.ApplyAdminCreditAdjustment(context.Background(), AdminCreditAdjustment{
		TargetUserID: uid, ActorUserID: uid, OperationID: "before-delete",
		Reason: "seed", CreditsSet: true, CreditsDelta: 42, CreatedAt: ledgerTime(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteUserAccount(context.Background(), uid); err != nil {
		t.Fatalf("delete account: %v", err)
	}
	if n := countRows(t, st, `SELECT COUNT(*) FROM credit_ledger`); n != 0 {
		t.Fatalf("credit_ledger rows after account delete = %d, want 0", n)
	}
}

func TestExportCreditLedgerBoundedAndOwned(t *testing.T) {
	st := newCreditsTestStore(t)
	uid := seedUserRaw(t, st, "export-ledger")
	other := seedUserRaw(t, st, "other-ledger")
	for i, owner := range []int64{uid, other, uid} {
		if _, err := st.ApplyAdminCreditAdjustment(context.Background(), AdminCreditAdjustment{
			TargetUserID: owner, ActorUserID: uid,
			OperationID: "export-seed-" + itoa(i), Reason: "seed",
			CreditsSet: true, CreditsDelta: int64(i + 1), CreatedAt: ledgerTime(),
		}); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := st.ListExportCreditLedger(context.Background(), uid, ExportCollectionLimit)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("owned ledger rows = %d, want 2", len(rows))
	}
	if rows[0].Kind != string(LedgerAdminAdjustment) || rows[0].CreditsDelta != "1" ||
		rows[0].CreditsAfter != "1" || rows[0].ActorUserID == nil || *rows[0].ActorUserID != uid {
		t.Fatalf("row shape = %+v", rows[0])
	}
	// A limit below the owned row count fails closed instead of truncating.
	if _, err := st.ListExportCreditLedger(context.Background(), uid, 1); !errors.Is(err, ErrExportLimit) {
		t.Fatalf("over-bound export = %v, want ErrExportLimit", err)
	}
	rowsOther, err := st.ListExportCreditLedger(context.Background(), other, ExportCollectionLimit)
	if err != nil || len(rowsOther) != 1 {
		t.Fatalf("other user's rows = (%d, %v), want 1 owned only", len(rowsOther), err)
	}
}
