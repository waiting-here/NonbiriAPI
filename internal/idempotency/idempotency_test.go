package idempotency_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbtest"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

const testKey = "abcdefghijklmnopqrstuv"

func openStore(t *testing.T) *db.Store {
	t.Helper()
	vault, err := secret.New(make([]byte, secret.MasterKeyBytes))
	if err != nil {
		t.Fatalf("new vault: %v", err)
	}
	t.Cleanup(func() { _ = vault.Close() })
	path := filepath.Join(t.TempDir(), "idempotency.db")
	dbtest.EnsureOwnerOnlyParent(t, path)
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatalf("open generation two store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func actorAndDigest(t *testing.T, route string, body any) ([32]byte, [32]byte) {
	t.Helper()
	actor, err := idempotency.ActorScopeHash("user", "42")
	if err != nil {
		t.Fatalf("actor hash: %v", err)
	}
	encoded, err := idempotency.CanonicalJSON(body)
	if err != nil {
		t.Fatalf("canonical body: %v", err)
	}
	digest, err := idempotency.RequestDigest(idempotency.DigestInput{
		ActorScopeHash:  actor,
		Method:          "PATCH",
		Route:           route,
		PathResourceIDs: []string{"17", "23"},
		Query:           "mode=exact",
		Body:            encoded,
	})
	if err != nil {
		t.Fatalf("request digest: %v", err)
	}
	return actor, digest
}

func beginTx(t *testing.T, store *db.Store) *sql.Tx {
	t.Helper()
	tx, err := store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin transaction: %v", err)
	}
	return tx
}

func TestControlMutationReplayConflictAndExpiryBoundary(t *testing.T) {
	store := openStore(t)
	actor, digest := actorAndDigest(t, "/api/endpoints/{id}/keys/{keyId}", struct {
		Enabled bool `json:"enabled"`
	}{Enabled: true})

	tx := beginTx(t, store)
	decision, err := idempotency.Begin(context.Background(), tx, idempotency.BeginInput{
		Scope: idempotency.ScopeControlMutation, ActorHash: actor, Key: testKey,
		RequestHash: digest, DecisionNow: 100,
	})
	if err != nil || decision.Kind != idempotency.Proceed {
		t.Fatalf("first acceptance = (%v, %v), want proceed", decision.Kind, err)
	}
	if err := idempotency.Complete(context.Background(), tx, decision, 200, []byte(`{"revision":"2"}`)); err != nil {
		t.Fatalf("complete first request: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit first request: %v", err)
	}

	tx = beginTx(t, store)
	replay, err := idempotency.Begin(context.Background(), tx, idempotency.BeginInput{
		Scope: idempotency.ScopeControlMutation, ActorHash: actor, Key: testKey,
		RequestHash: digest, DecisionNow: 100 + idempotency.ReplayWindowSeconds - 1,
	})
	if err != nil {
		t.Fatalf("same request replay: %v", err)
	}
	if replay.Kind != idempotency.Replay || replay.HTTPStatus != 200 || string(replay.ResponseBody) != `{"revision":"2"}` {
		t.Fatalf("replay = %+v", replay)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback replay transaction: %v", err)
	}

	_, differentRoute := actorAndDigest(t, "/api/models/{id}", struct {
		Enabled bool `json:"enabled"`
	}{Enabled: true})
	tx = beginTx(t, store)
	_, err = idempotency.Begin(context.Background(), tx, idempotency.BeginInput{
		Scope: idempotency.ScopeControlMutation, ActorHash: actor, Key: testKey,
		RequestHash: differentRoute, DecisionNow: 100 + idempotency.ReplayWindowSeconds - 1,
	})
	if !errors.Is(err, idempotency.ErrConflict) {
		t.Fatalf("different route error = %v, want conflict", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback conflict transaction: %v", err)
	}

	_, differentBody := actorAndDigest(t, "/api/endpoints/{id}/keys/{keyId}", struct {
		Enabled bool `json:"enabled"`
	}{Enabled: false})
	tx = beginTx(t, store)
	_, err = idempotency.Begin(context.Background(), tx, idempotency.BeginInput{
		Scope: idempotency.ScopeControlMutation, ActorHash: actor, Key: testKey,
		RequestHash: differentBody, DecisionNow: 100 + idempotency.ReplayWindowSeconds - 1,
	})
	if !errors.Is(err, idempotency.ErrConflict) {
		t.Fatalf("different body error = %v, want conflict", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback body conflict transaction: %v", err)
	}

	tx = beginTx(t, store)
	fresh, err := idempotency.Begin(context.Background(), tx, idempotency.BeginInput{
		Scope: idempotency.ScopeControlMutation, ActorHash: actor, Key: testKey,
		RequestHash: differentBody, DecisionNow: 100 + idempotency.ReplayWindowSeconds,
	})
	if err != nil || fresh.Kind != idempotency.Proceed {
		t.Fatalf("expiry boundary acceptance = (%v, %v), want proceed", fresh.Kind, err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback expiry transaction: %v", err)
	}
}

func TestRequestDigestBindsEveryControlMutationDimension(t *testing.T) {
	actor, err := idempotency.ActorScopeHash("user", "42")
	if err != nil {
		t.Fatal(err)
	}
	body, err := idempotency.CanonicalJSON(struct {
		Revision string `json:"revision"`
	}{Revision: "7"})
	if err != nil {
		t.Fatal(err)
	}
	base := idempotency.DigestInput{
		ActorScopeHash: actor, Method: "PATCH", Route: "/api/models/{id}",
		PathResourceIDs: []string{"17"}, Query: "view=full", Body: body,
	}
	want, err := idempotency.RequestDigest(base)
	if err != nil {
		t.Fatal(err)
	}

	otherActor, err := idempotency.ActorScopeHash("user", "43")
	if err != nil {
		t.Fatal(err)
	}
	changes := []idempotency.DigestInput{
		{ActorScopeHash: otherActor, Method: base.Method, Route: base.Route, PathResourceIDs: base.PathResourceIDs, Query: base.Query, Body: base.Body},
		{ActorScopeHash: actor, Method: "DELETE", Route: base.Route, PathResourceIDs: base.PathResourceIDs, Query: base.Query, Body: base.Body},
		{ActorScopeHash: actor, Method: base.Method, Route: "/api/endpoints/{id}", PathResourceIDs: base.PathResourceIDs, Query: base.Query, Body: base.Body},
		{ActorScopeHash: actor, Method: base.Method, Route: base.Route, PathResourceIDs: []string{"18"}, Query: base.Query, Body: base.Body},
		{ActorScopeHash: actor, Method: base.Method, Route: base.Route, PathResourceIDs: base.PathResourceIDs, Query: "view=compact", Body: base.Body},
		{ActorScopeHash: actor, Method: base.Method, Route: base.Route, PathResourceIDs: base.PathResourceIDs, Query: base.Query, Body: []byte(`{"revision":"8"}`)},
	}
	for i, changed := range changes {
		got, err := idempotency.RequestDigest(changed)
		if err != nil {
			t.Fatalf("change %d: %v", i, err)
		}
		if got == want {
			t.Fatalf("change %d did not alter request digest", i)
		}
	}
}

func TestKeyAndScopeValidation(t *testing.T) {
	for _, key := range []string{"short", "abcdefghijklmnopqrstu!", "abcdefghijklmnopqrstu界"} {
		if _, err := idempotency.KeyHash(key); err == nil {
			t.Fatalf("invalid key %q accepted", key)
		}
	}
	store := openStore(t)
	actor, digest := actorAndDigest(t, "/api/models/{id}", struct{}{})
	tx := beginTx(t, store)
	_, err := idempotency.Begin(context.Background(), tx, idempotency.BeginInput{
		Scope: "unknown", ActorHash: actor, Key: testKey, RequestHash: digest, DecisionNow: 0,
	})
	if err == nil {
		t.Fatal("unknown scope accepted")
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback unknown scope transaction: %v", err)
	}
}

func TestEmptyResponseRemainsReplayableBlob(t *testing.T) {
	store := openStore(t)
	actor, digest := actorAndDigest(t, "/api/endpoints/{id}", struct{}{})
	tx := beginTx(t, store)
	decision, err := idempotency.Begin(context.Background(), tx, idempotency.BeginInput{
		Scope: idempotency.ScopeControlMutation, ActorHash: actor, Key: testKey,
		RequestHash: digest, DecisionNow: 0,
	})
	if err != nil {
		t.Fatalf("accept empty response request: %v", err)
	}
	if err := idempotency.Complete(context.Background(), tx, decision, 204, nil); err != nil {
		t.Fatalf("complete empty response request: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit empty response request: %v", err)
	}

	tx = beginTx(t, store)
	replay, err := idempotency.Begin(context.Background(), tx, idempotency.BeginInput{
		Scope: idempotency.ScopeControlMutation, ActorHash: actor, Key: testKey,
		RequestHash: digest, DecisionNow: 1,
	})
	if err != nil {
		t.Fatalf("replay empty response request: %v", err)
	}
	if replay.Kind != idempotency.Replay || replay.HTTPStatus != 204 || len(replay.ResponseBody) != 0 {
		t.Fatalf("empty replay = %+v", replay)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback empty replay transaction: %v", err)
	}
}
