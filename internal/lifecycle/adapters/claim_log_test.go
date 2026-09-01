package adapters

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
)

type adapterTestClaims struct {
	state *adapterTestState
}

func (source adapterTestClaims) PrepareAccountDeletion(
	ctx context.Context,
	tx *sql.Tx,
	_ int64,
	now int64,
) error {
	return source.state.prepareDelete(ctx, tx, "claims", now)
}

type adapterTestRequestLogs struct {
	state *adapterTestState
}

func (source adapterTestRequestLogs) PrepareLifecycleAccountDeletion(
	ctx context.Context,
	tx *sql.Tx,
	_ int64,
	now int64,
) error {
	return source.state.prepareDelete(ctx, tx, "logs", now)
}

func TestClaimLogDeleteAdapterPreservesSlotOrderAndRollback(t *testing.T) {
	database := newAdapterTestDatabase(t)
	tx, err := database.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	state := &adapterTestState{expectedTx: tx, expectedNow: 1_700_000_890, deleteFailure: "logs"}
	adapter := &ClaimLogDeleteAdapter{
		claims: adapterTestClaims{state: state},
		logs:   adapterTestRequestLogs{state: state},
	}
	finalizer, err := adapter.PrepareDelete(context.Background(), tx, lifecycle.DeleteRequest{
		UserID: 21, DecisionNow: state.expectedNow,
	})
	if !errors.Is(err, lifecycle.ErrUnavailable) || finalizer != nil {
		_ = tx.Rollback()
		t.Fatalf("claim/log delete finalizer=%v error=%v", finalizer, err)
	}
	if len(state.deleteCalls) != 2 || state.deleteCalls[0] != "claims" || state.deleteCalls[1] != "logs" {
		_ = tx.Rollback()
		t.Fatalf("claim/log calls=%v", state.deleteCalls)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	var rows int
	if err := database.QueryRow(`SELECT count(*) FROM delete_steps`).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("rolled-back claim/log rows=%d err=%v", rows, err)
	}
}

func TestClaimLogDeleteAdapterRejectsIncompleteOwnerSet(t *testing.T) {
	adapter := &ClaimLogDeleteAdapter{}
	if _, err := adapter.PrepareDelete(context.Background(), nil, lifecycle.DeleteRequest{
		UserID: 1, DecisionNow: 1,
	}); !errors.Is(err, lifecycle.ErrInvalid) {
		t.Fatalf("invalid claim/log error=%v", err)
	}
}
