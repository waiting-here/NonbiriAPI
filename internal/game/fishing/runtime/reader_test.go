package runtime

import (
	"context"
	"testing"
	"time"
)

func TestFishingStateAndRecoveryReadsWhileWriterHeld(t *testing.T) {
	f := newGameFixture(t, &scriptedSource{})
	user := f.seedUser("reader", fixtureFunding)
	tx, err := f.database.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	if _, err = f.service.FishingState(ctx, user); err != nil {
		t.Fatal(err)
	}
	if _, err = f.service.FishingLeaderboard(ctx, user, "single"); err != nil {
		t.Fatal(err)
	}
	if err = f.service.validateFishingRecoveryState(ctx); err != nil {
		t.Fatal(err)
	}
	if n := f.store.ReadDB().Stats().InUse; n != 0 {
		t.Fatalf("reader retained: %d", n)
	}
}
