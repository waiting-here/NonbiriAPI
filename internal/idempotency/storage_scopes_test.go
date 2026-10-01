package idempotency_test

import (
	"context"
	"errors"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
)

func TestStorageScopesUseCanonicalReplayAndRecovery(t *testing.T) {
	store := openStore(t)
	ctx := context.Background()
	actor, digest := actorAndDigest(t, "/api/operations/{id}", struct {
		Revision string `json:"revision"`
	}{Revision: "1"})
	_, changed := actorAndDigest(t, "/api/operations/{id}", struct {
		Revision string `json:"revision"`
	}{Revision: "2"})
	scopes := []idempotency.Scope{idempotency.ScopeLakeNotes, idempotency.ScopePersonalAutomation}
	for _, scope := range scopes {
		input := idempotency.BeginInput{
			Scope: scope, ActorHash: actor, Key: testKey, RequestHash: digest,
			DecisionNow: maintenanceDecisionNow,
		}
		tx := beginTx(t, store)
		decision, err := idempotency.Begin(ctx, tx, input)
		if err != nil || decision.Kind != idempotency.Proceed {
			t.Fatalf("%s acceptance = (%+v, %v)", scope, decision, err)
		}
		if err := idempotency.Complete(ctx, tx, decision, 201, []byte(`{"revision":"1"}`)); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		tx = beginTx(t, store)
		replay, err := idempotency.Begin(ctx, tx, input)
		if err != nil || replay.Kind != idempotency.Replay || replay.HTTPStatus != 201 || string(replay.ResponseBody) != `{"revision":"1"}` {
			t.Fatalf("%s replay = (%+v, %v)", scope, replay, err)
		}
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		input.RequestHash = changed
		tx = beginTx(t, store)
		if _, err := idempotency.Begin(ctx, tx, input); !errors.Is(err, idempotency.ErrConflict) {
			t.Fatalf("%s changed payload error = %v", scope, err)
		}
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		input.RequestHash = digest
		input.Key = testKey + "_pending"
		tx = beginTx(t, store)
		if pending, err := idempotency.Begin(ctx, tx, input); err != nil || pending.Kind != idempotency.Proceed {
			t.Fatalf("%s pending acceptance = (%+v, %v)", scope, pending, err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		tx = beginTx(t, store)
		if _, err := idempotency.Begin(ctx, tx, input); !errors.Is(err, idempotency.ErrInProgress) {
			t.Fatalf("%s pending replay error = %v", scope, err)
		}
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
	}
	maintenance := idempotency.NewMaintenance(store.DB())
	result, err := maintenance.Recover(ctx, maintenanceDecisionNow, 100, maintenanceDeadline())
	if err != nil || result != (idempotency.MaintenanceResult{Processed: 2}) {
		t.Fatalf("recovery = (%+v, %v)", result, err)
	}
	for _, scope := range scopes {
		tx := beginTx(t, store)
		replay, err := idempotency.Begin(ctx, tx, idempotency.BeginInput{
			Scope: scope, ActorHash: actor, Key: testKey, RequestHash: digest,
			DecisionNow: maintenanceDecisionNow + idempotency.ReplayWindowSeconds - 1,
		})
		if err != nil || replay.Kind != idempotency.Replay || replay.HTTPStatus != 201 {
			t.Fatalf("%s recovery preserved replay = (%+v, %v)", scope, replay, err)
		}
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
	}
	result, err = maintenance.Retain(ctx, maintenanceDecisionNow+idempotency.ReplayWindowSeconds, 100, maintenanceDeadline())
	if err != nil || result != (idempotency.MaintenanceResult{Processed: 2}) {
		t.Fatalf("expiry boundary = (%+v, %v)", result, err)
	}
}
