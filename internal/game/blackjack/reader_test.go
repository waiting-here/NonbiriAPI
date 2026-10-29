package blackjack_test

import (
	"context"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/game/blackjack"
)

func TestBlackjackHistoryAndValidationWhileWriterHeld(t *testing.T) {
	f := newFixture(t, 1)
	f.db.SetMaxOpenConns(1)
	tx, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	if _, err = f.s.History(ctx, f.users[0], blackjack.PageInput{Limit: 20}); err != nil {
		t.Fatal(err)
	}
	if err = f.s.ValidatePersistedState(ctx); err != nil {
		t.Fatal(err)
	}
	if n := f.readDatabase.Stats().InUse; n != 0 {
		t.Fatalf("reader retained: %d", n)
	}
}
