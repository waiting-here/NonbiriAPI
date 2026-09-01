package adapters

import (
	"context"
	"errors"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
)

func TestAuthAndResourceDeleteAdaptersKeepFrozenSlotsIndependent(t *testing.T) {
	database := newAdapterTestDatabase(t)
	tx, err := database.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	state := &adapterTestState{
		expectedTx: tx, expectedNow: 1_700_000_789,
		finalizer: &adapterTestFinalizer{},
	}
	request := lifecycle.DeleteRequest{UserID: 17, DecisionNow: state.expectedNow}

	authAdapter := &AuthDeleteAdapter{identity: adapterTestIdentity{state}}
	finalizer, err := authAdapter.PrepareDelete(context.Background(), tx, request)
	if err != nil || finalizer != state.finalizer {
		_ = tx.Rollback()
		t.Fatalf("auth delete finalizer=%v error=%v", finalizer, err)
	}
	if state.finalizer.aborted || state.finalizer.committed {
		_ = tx.Rollback()
		t.Fatalf("auth finalizer changed before coordinator decision: %+v", state.finalizer)
	}

	state.deleteFailure = "resources"
	resourceAdapter := &ResourceDeleteAdapter{resources: adapterTestResources{state}}
	resourceFinalizer, err := resourceAdapter.PrepareDelete(context.Background(), tx, request)
	if !errors.Is(err, lifecycle.ErrUnavailable) || resourceFinalizer != nil {
		_ = tx.Rollback()
		t.Fatalf("resource delete finalizer=%v error=%v", resourceFinalizer, err)
	}
	if state.finalizer.aborted || state.finalizer.committed {
		_ = tx.Rollback()
		t.Fatalf("later adapter improperly controlled auth finalizer: %+v", state.finalizer)
	}
	wantCalls := []string{"identity", "resources"}
	if len(state.deleteCalls) != len(wantCalls) {
		_ = tx.Rollback()
		t.Fatalf("delete calls=%v", state.deleteCalls)
	}
	for index := range wantCalls {
		if state.deleteCalls[index] != wantCalls[index] {
			_ = tx.Rollback()
			t.Fatalf("delete calls=%v", state.deleteCalls)
		}
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	var rows int
	if err := database.QueryRow(`SELECT count(*) FROM delete_steps`).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("rolled-back delete rows=%d err=%v", rows, err)
	}
}

func TestDeleteSlotAdaptersRejectInvalidCalls(t *testing.T) {
	request := lifecycle.DeleteRequest{UserID: 1, DecisionNow: 1}
	if _, err := (&AuthDeleteAdapter{}).PrepareDelete(context.Background(), nil, request); !errors.Is(err, lifecycle.ErrInvalid) {
		t.Fatalf("auth invalid error=%v", err)
	}
	if _, err := (&ResourceDeleteAdapter{}).PrepareDelete(context.Background(), nil, request); !errors.Is(err, lifecycle.ErrInvalid) {
		t.Fatalf("resource invalid error=%v", err)
	}
}
