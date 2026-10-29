package activities

import (
	"context"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

func TestActivityReaderQuoteAndAdminWhileWriterHeld(t *testing.T) {
	f := newActivityFixture(t, 1800000000)
	enableLoan(t, f)
	user, _ := f.seedUser("reader", false)
	f.fundUser(user, 20000000)
	tx, err := f.store.DB().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	quote, err := f.repository.QuoteLoan(ctx, user, "1")
	if err != nil || quote.GeneralAfter != "7000" {
		t.Fatalf("quote: %+v %v", quote, err)
	}
	if _, err = f.repository.GetActivitiesConfig(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = f.repository.GetAdminThursday(ctx, f.adminID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.repository.ListLoans(ctx, user, pagination.Default()); err != nil {
		t.Fatal(err)
	}
	if n := f.store.ReadDB().Stats().InUse; n != 0 {
		t.Fatalf("reader retained: %d", n)
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	result, _, err := f.repository.Borrow(ctx, user, loanMutation(f, quote.QuoteToken), quote.QuoteToken)
	if err != nil || result.Value.GeneralAfter != quote.GeneralAfter {
		t.Fatalf("writer revalidation: %+v %v", result, err)
	}
}
