package fatfish

import (
	"context"
	"testing"
	"time"
)

func TestFatfishReadersWhileWriterHeld(t *testing.T) {
	f := newFishFixture(t)
	tx, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	if _, err = f.s.Levels(ctx, f.admin, 1); err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.ListPeriods(ctx, f.user, 1); err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.CurrentPlaytest(ctx, f.admin); err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.History(ctx, f.user, 20, 1); err != nil {
		t.Fatal(err)
	}
	if n := f.config.ReadDB.Stats().InUse; n != 0 {
		t.Fatalf("reader retained: %d", n)
	}
}
