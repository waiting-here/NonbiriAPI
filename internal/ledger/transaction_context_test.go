package ledger

import (
	"context"
	"errors"
	"testing"
)

func TestSavepointCleanupPreservesCancellationAndOuterRollback(t *testing.T) {
	store := openLedgerTestStore(t)
	tx := beginLedgerTestTx(t, store.DB())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var userID int64
	err := withSavepoint(ctx, tx, func() error {
		userID, _ = seedLedgerUser(t, tx, "cancelled-savepoint")
		cancel()
		return ctx.Err()
	})
	if !errors.Is(err, ErrInvariant) || !errors.Is(err, context.Canceled) {
		t.Fatalf("cleanup must preserve the primary cancellation and unsafe savepoint: %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	var remaining int
	if err := store.DB().QueryRow(`SELECT COUNT(*) FROM users WHERE id=?`, userID).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatalf("outer rollback retained user: count=%d err=%v", remaining, err)
	}
}
