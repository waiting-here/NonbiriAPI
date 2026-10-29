package ledger

import (
	"context"
	"errors"
	"testing"
)

func TestSavepointCleanupPreservesCancellationAndOuterRollback(t *testing.T) {
	for _, test := range []struct {
		name     string
		cause    error
		preserve bool
	}{
		{"cancelled", context.Canceled, true},
		{"deadline", context.DeadlineExceeded, true},
		{"retryable", ErrRetryable, false},
		{"capacity", ErrCapacityExhausted, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := openLedgerTestStore(t)
			tx := beginLedgerTestTx(t, store.DB())
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var userID int64
			err := withSavepoint(ctx, tx, func() error {
				userID, _ = seedLedgerUser(t, tx, "cancelled-savepoint")
				cancel()
				return test.cause
			})
			if !errors.Is(err, ErrInvariant) || errors.Is(err, test.cause) != test.preserve {
				t.Fatalf("unsafe cleanup lost its classification or cancellation: %v", err)
			}
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			var remaining int
			if err := store.DB().QueryRow(`SELECT COUNT(*) FROM users WHERE id=?`, userID).Scan(&remaining); err != nil || remaining != 0 {
				t.Fatalf("outer rollback retained user: count=%d err=%v", remaining, err)
			}
		})
	}
}
