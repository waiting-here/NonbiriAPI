package linklink

import (
	"context"
	"testing"
	"time"
)

func TestLinkLinkReadersWhileWriterHeld(t *testing.T) {
	f := newFixture(t)
	user, _ := f.seedUser("reader", 10000)
	tx, err := f.database.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	if _, err = f.service.Leaderboard(ctx, user, "6x8", "7d"); err != nil {
		t.Fatal(err)
	}
	if _, err = f.service.ActiveCounts(ctx); err != nil {
		t.Fatal(err)
	}
	if err = f.service.validatePersistedSessions(ctx); err != nil {
		t.Fatal(err)
	}
	if n := f.store.ReadDB().Stats().InUse; n != 0 {
		t.Fatalf("reader retained: %d", n)
	}
}
