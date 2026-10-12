package idempotency_test

import (
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
)

func TestRedemptionReplayLifetimeIsFrozenAndScopeLimited(t *testing.T) {
	store := openStore(t)
	actor, digest := actorAndDigest(t, "/api/rewards/redeem", nil)
	input := idempotency.BeginInput{Scope: idempotency.ScopeRewardRedemption, ActorHash: actor, Key: testKey, RequestHash: digest, DecisionNow: 100, ReplaySeconds: 2 * idempotency.ReplayWindowSeconds}
	tx := beginTx(t, store)
	d, err := idempotency.Begin(t.Context(), tx, input)
	if err != nil {
		t.Fatal(err)
	}
	if err := idempotency.Complete(t.Context(), tx, d, 200, []byte(`{"rejection":"unavailable"}`)); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	for _, now := range []int64{100 + idempotency.ReplayWindowSeconds + 1, 100 + input.ReplaySeconds - 1} {
		for _, currentWindow := range []int64{idempotency.ReplayWindowSeconds, db.MaxBanDurationSeconds} {
			attempt := input
			attempt.DecisionNow, attempt.ReplaySeconds = now, currentWindow
			tx := beginTx(t, store)
			d, err := idempotency.Begin(t.Context(), tx, attempt)
			if err != nil || d.Kind != idempotency.Replay {
				t.Fatalf("replay at %d with window %d: %v %v", now, currentWindow, d.Kind, err)
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
		}
	}
	var expires int64
	if err := store.DB().QueryRow("SELECT expires_at FROM idempotency_records WHERE scope='reward_redemption'").Scan(&expires); err != nil || expires != 100+input.ReplaySeconds {
		t.Fatalf("replay changed original expiry: %d %v", expires, err)
	}
	input.DecisionNow, input.ReplaySeconds = expires, idempotency.ReplayWindowSeconds
	tx = beginTx(t, store)
	d, err = idempotency.Begin(t.Context(), tx, input)
	if err != nil || d.Kind != idempotency.Proceed {
		t.Fatalf("exact expiry: %v %v", d.Kind, err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []idempotency.BeginInput{
		{Scope: idempotency.ScopeControlMutation, ReplaySeconds: 2 * idempotency.ReplayWindowSeconds},
		{Scope: idempotency.ScopeRewardRedemption, ReplaySeconds: idempotency.ReplayWindowSeconds - 1},
		{Scope: idempotency.ScopeRewardRedemption, ReplaySeconds: db.MaxBanDurationSeconds + 1},
	} {
		invalid.ActorHash, invalid.RequestHash, invalid.Key, invalid.DecisionNow = actor, digest, testKey, 100
		tx := beginTx(t, store)
		if _, err := idempotency.Begin(t.Context(), tx, invalid); err == nil {
			t.Fatalf("invalid lifetime accepted: %+v", invalid)
		}
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
	}
}
