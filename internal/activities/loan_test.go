package activities

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

func enableLoan(t *testing.T, f *activityFixture) {
	t.Helper()
	f.setActivityConfig(true, false, false, 0, 0)
	enabled := true
	if _, _, err := f.repository.PatchActivitiesConfig(context.Background(), f.adminID, f.control(http.MethodPatch, routeAdminActivityConfig, map[string]any{"loan_enabled": true}), ActivitiesConfigPatch{ExpectedRevision: f.configRevision(), LoanEnabled: &enabled}); err != nil {
		t.Fatal(err)
	}
}

func loanMutation(f *activityFixture, token string) ControlMutation {
	return f.control(http.MethodPost, routeLoan, map[string]any{"quote_token": token})
}

func TestLoanExactTermsContinuousBorrowingAndPermanentNonce(t *testing.T) {
	f := newActivityFixture(t, 1800000000)
	enableLoan(t, f)
	user, _ := f.seedUser("borrower", false)
	f.fundUser(user, 20000000)
	ctx := context.Background()
	q, err := f.repository.QuoteLoan(ctx, user, "1")
	if err != nil || q.Disbursed != "9000" || q.Repayment != "13000" || q.Fee != "1000" || q.Interest != "3000" || q.GeneralAfter != "7000" || q.ExpiresAt != f.clock.Load()+60 {
		t.Fatal(q, err)
	}
	m := loanMutation(f, q.QuoteToken)
	first, _, err := f.repository.Borrow(ctx, user, m, q.QuoteToken)
	if err != nil || first.Status != 201 || first.Value.GameAfter != "9000" || first.Value.GeneralAfter != "7000" {
		t.Fatal(first, err)
	}
	if _, _, err := f.repository.Borrow(ctx, user, loanMutation(f, q.QuoteToken), q.QuoteToken); !errors.Is(err, ErrConflict) {
		t.Fatal("nonce reused with a new key", err)
	}
	f.clock.Add(61)
	replay, _, err := f.repository.Borrow(ctx, user, m, q.QuoteToken)
	if err != nil || !replay.Replayed || string(replay.Body) != string(first.Body) {
		t.Fatal("completed replay must precede expiry", replay, err)
	}
	q2, err := f.repository.QuoteLoan(ctx, user, "1")
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := f.repository.Borrow(ctx, user, loanMutation(f, q2.QuoteToken), q2.QuoteToken)
	if err != nil || second.Value.GeneralAfter != "-6000" || second.Value.GameAfter != "18000" {
		t.Fatal(second, err)
	}
	if _, err := f.repository.QuoteLoan(ctx, user, "1"); !errors.Is(err, ErrInsufficientCredits) {
		t.Fatal("negative balance eligible", err)
	}
	f.clock.Add(172801)
	if _, _, err := f.repository.Borrow(ctx, user, m, q.QuoteToken); !errors.Is(err, ErrConflict) {
		t.Fatal("expired idempotency accepted used quote", err)
	}
	// Later configuration cannot rewrite immutable history; disabling only blocks new loans.
	disabled := false
	if _, _, err := f.repository.PatchActivitiesConfig(ctx, f.adminID, f.control(http.MethodPatch, routeAdminActivityConfig, map[string]any{"loan_enabled": false}), ActivitiesConfigPatch{ExpectedRevision: f.configRevision(), LoanEnabled: &disabled}); err != nil {
		t.Fatal(err)
	}
	page, err := f.repository.ListLoans(ctx, user, pagination.Default())
	if err != nil || len(page.Data) != 2 || page.Data[0] != second.Value || page.Data[1] != first.Value || page.Pagination.TotalItems != "2" {
		t.Fatal(page, err)
	}
	tx, err := f.store.DB().Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	items, err := f.repository.ExportLoansTx(ctx, tx, user, 10000)
	if err != nil || len(items) != 2 {
		t.Fatal(items, err)
	}
	if _, err := f.repository.ExportLoansTx(ctx, tx, user, 1); !errors.Is(err, ErrResourceLimit) {
		t.Fatal("truncated loan export", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	validateLedgerRecovery(t, f.store.DB())
}

func TestLoanQuoteOwnershipExpiryConfigurationAndActualBalances(t *testing.T) {
	ctx := context.Background()
	f := newActivityFixture(t, 1800000000)
	enableLoan(t, f)
	user, _ := f.seedUser("first", false)
	peer, _ := f.seedUser("second", false)
	q, err := f.repository.QuoteLoan(ctx, user, "1")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.repository.Borrow(ctx, peer, loanMutation(f, q.QuoteToken), q.QuoteToken); !errors.Is(err, ErrInvalidRequest) {
		t.Fatal("cross-user quote accepted", err)
	}
	for _, token := range []string{q.QuoteToken + "x", strings.Repeat("x", 2049), "invalid.token"} {
		if _, _, err := f.repository.Borrow(ctx, user, loanMutation(f, token), token); !errors.Is(err, ErrInvalidRequest) {
			t.Fatal("forged quote accepted", err)
		}
	}
	f.clock.Add(60)
	if _, _, err := f.repository.Borrow(ctx, user, loanMutation(f, q.QuoteToken), q.QuoteToken); !errors.Is(err, ErrConflict) {
		t.Fatal("expired quote accepted", err)
	}
	q, err = f.repository.QuoteLoan(ctx, user, "1")
	if err != nil {
		t.Fatal(err)
	}
	b := "1.4"
	if _, _, err := f.repository.PatchActivitiesConfig(ctx, f.adminID, f.control(http.MethodPatch, routeAdminActivityConfig, map[string]any{"loan_b": b}), ActivitiesConfigPatch{ExpectedRevision: f.configRevision(), LoanB: &b}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.repository.Borrow(ctx, user, loanMutation(f, q.QuoteToken), q.QuoteToken); !errors.Is(err, ErrConflict) {
		t.Fatal("changed configuration accepted", err)
	}
	q, err = f.repository.QuoteLoan(ctx, user, "1")
	if err != nil {
		t.Fatal(err)
	}
	f.fundUser(user, 2000000)
	actual, _, err := f.repository.Borrow(ctx, user, loanMutation(f, q.QuoteToken), q.QuoteToken)
	if err != nil || actual.Value.GeneralBefore != "2000" || actual.Value.GeneralAfter != "-12000" || actual.Value.Repayment != "14000" {
		t.Fatal("snapshot improperly used as balance CAS", actual, err)
	}
	q, err = f.repository.QuoteLoan(ctx, peer, "1")
	if err != nil {
		t.Fatal(err)
	}
	zero, _, err := f.repository.Borrow(ctx, peer, loanMutation(f, q.QuoteToken), q.QuoteToken)
	if err != nil || zero.Value.GeneralBefore != "0" || zero.Value.GeneralAfter != "-14000" {
		t.Fatal("zero balance should qualify", zero, err)
	}
	validateLedgerRecovery(t, f.store.DB())
}

func TestLoanAtomicFailureConcurrentReplayAndDeletion(t *testing.T) {
	f := newActivityFixture(t, 1800000000)
	enableLoan(t, f)
	user, _ := f.seedUser("atomic", false)
	ctx := context.Background()
	q, err := f.repository.QuoteLoan(ctx, user, "1")
	if err != nil {
		t.Fatal(err)
	}
	m := loanMutation(f, q.QuoteToken)
	if _, err := f.store.DB().Exec(`CREATE TRIGGER fail_loan BEFORE INSERT ON activity_loans BEGIN SELECT RAISE(ABORT,'loan failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.repository.Borrow(ctx, user, m, q.QuoteToken); err == nil {
		t.Fatal("partial loan committed")
	}
	assertUserBalance(t, f.store.DB(), user, "0")
	var count int
	if err := f.store.DB().QueryRow(`SELECT count(*) FROM credit_operations WHERE kind='activity_loan'`).Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
	if _, err := f.store.DB().Exec(`DROP TRIGGER fail_loan`); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan MutationResult[LoanReceipt], 6)
	errs := make(chan error, 6)
	for range 6 {
		wg.Go(func() {
			result, _, err := f.repository.Borrow(ctx, user, m, q.QuoteToken)
			results <- result
			errs <- err
		})
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	id := ""
	for result := range results {
		if id == "" {
			id = result.Value.ID
		}
		if id != result.Value.ID {
			t.Fatal("duplicate immutable receipts")
		}
	}
	assertUserBalance(t, f.store.DB(), user, "-13000000")
	validateLedgerRecovery(t, f.store.DB())
	tx, err := f.store.DB().Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := f.repository.PrepareUserDeletion(ctx, tx, user, f.clock.Load()); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(`SELECT count(*) FROM activity_loans WHERE user_id=?`, user).Scan(&count); err != nil || count != 0 {
		t.Fatal("loan receipts survive deletion", count, err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	page, err := f.repository.ListLoans(ctx, user, pagination.Default())
	if err != nil || len(page.Data) != 1 {
		t.Fatal("deletion rollback lost receipt", page, err)
	}
}
