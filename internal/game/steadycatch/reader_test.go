package steadycatch

import (
	"context"
	"testing"
	"time"
)

func TestCatchReadersWhileWriterHeld(t *testing.T) {
	f := newFixture(t)
	tx, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	if _, err = f.s.Leaderboard(ctx, f.user, "7d"); err != nil {
		t.Fatal(err)
	}
	if err = f.s.Module().ValidatePersistedState(ctx); err != nil {
		t.Fatal(err)
	}
	if n := f.s.readDatabase.Stats().InUse; n != 0 {
		t.Fatalf("reader retained: %d", n)
	}
}
