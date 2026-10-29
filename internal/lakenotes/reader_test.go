package lakenotes

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/authz"
)

func TestLakeReaderQuoteAndCurrentSessionAuthority(t *testing.T) {
	f := newFixture(t)
	f.enable(t)
	f.profile(t)
	writer, err := f.database.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Rollback()
	ctx, cancel := context.WithTimeout(f.ctx(f.user), 2*time.Second)
	defer cancel()
	quote, err := f.service.Quote(ctx, f.user, QuoteInput{GeneralToCoins, "2"})
	if err != nil || quote.SourceAmount != "14" {
		t.Fatalf("quote: %+v %v", quote, err)
	}
	adminCtx, adminCancel := context.WithTimeout(f.ctx(f.admin), 2*time.Second)
	defer adminCancel()
	if _, err = f.service.Periods(adminCtx, 1, 20); err != nil {
		t.Fatal(err)
	}
	if n := f.service.readDatabase.Stats().InUse; n != 0 {
		t.Fatalf("reader retained: %d", n)
	}
	if err = writer.Rollback(); err != nil {
		t.Fatal(err)
	}
	if _, err = f.database.Exec("DELETE FROM sessions WHERE user_id=?", f.user); err != nil {
		t.Fatal(err)
	}
	if _, err = f.service.Quote(ctx, f.user, QuoteInput{GeneralToCoins, "2"}); !errors.Is(err, authz.ErrUnauthorized) {
		t.Fatalf("revoked session quote: %v", err)
	}
}
