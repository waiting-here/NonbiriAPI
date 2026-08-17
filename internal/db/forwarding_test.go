package db

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func seedForwardKey(t *testing.T, store *Store, endpointID int64, plaintext string, enabled bool) (int64, string) {
	t.Helper()
	ciphertext, err := store.secrets.Seal([]byte(plaintext))
	if err != nil {
		t.Fatalf("seal key: %v", err)
	}
	enabledInt := 0
	if enabled {
		enabledInt = 1
	}
	result, err := store.DB().Exec(`
INSERT INTO endpoint_keys (endpoint_id, encrypted_secret, enabled, created_at, updated_at)
VALUES (?, ?, ?, 1, 1)`, endpointID, ciphertext, enabledInt)
	if err != nil {
		t.Fatalf("insert key: %v", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("key id: %v", err)
	}
	return id, ciphertext
}

func seedForwardBinding(t *testing.T, store *Store, modelID, keyID int64, upstream string, ord int64) int64 {
	t.Helper()
	result, err := store.DB().Exec(`
INSERT INTO model_bindings (model_id, endpoint_key_id, upstream_model_id, ord, created_at)
VALUES (?, ?, ?, ?, 1)`, modelID, keyID, upstream, ord)
	if err != nil {
		t.Fatalf("insert binding: %v", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("binding id: %v", err)
	}
	return id
}

func TestForwardProjectionOwnershipAndUsability(t *testing.T) {
	store := newModelsTestStore(t)
	ctx := context.Background()
	alice := seedUserRaw(t, store, "forward-alice")
	bob := seedUserRaw(t, store, "forward-bob")
	modelID := seedModelRaw(t, store, alice, "provider/with/slash", "model")
	_ = seedModelRaw(t, store, bob, "provider/with/slash", "model")

	validEndpoint := seedEndpointRaw(t, store, alice, true)
	validKey, _ := seedForwardKey(t, store, validEndpoint, "valid-secret", true)
	seedFetchedModelRaw(t, store, validKey, "upstream-late")
	seedFetchedModelRaw(t, store, validKey, "upstream-first")
	lateBinding := seedForwardBinding(t, store, modelID, validKey, "upstream-late", 9)
	firstBinding := seedForwardBinding(t, store, modelID, validKey, "upstream-first", 1)

	disabledEndpoint := seedEndpointRaw(t, store, alice, false)
	disabledEndpointKey, _ := seedForwardKey(t, store, disabledEndpoint, "disabled-endpoint-secret", true)
	seedFetchedModelRaw(t, store, disabledEndpointKey, "disabled-endpoint-model")
	seedForwardBinding(t, store, modelID, disabledEndpointKey, "disabled-endpoint-model", 0)

	disabledKey, _ := seedForwardKey(t, store, validEndpoint, "disabled-key-secret", false)
	seedFetchedModelRaw(t, store, disabledKey, "disabled-key-model")
	seedForwardBinding(t, store, modelID, disabledKey, "disabled-key-model", 0)

	uncachedKey, _ := seedForwardKey(t, store, validEndpoint, "uncached-secret", true)
	seedForwardBinding(t, store, modelID, uncachedKey, "not-in-cache", 0)

	badStatusKey, _ := seedForwardKey(t, store, validEndpoint, "bad-status-secret", true)
	if _, err := store.DB().Exec(`
INSERT INTO fetched_models (endpoint_key_id, upstream_model_id, provider, fetched_at, status)
VALUES (?, 'bad-status-model', '', 1, 'failed')`, badStatusKey); err != nil {
		t.Fatalf("insert bad status cache: %v", err)
	}
	seedForwardBinding(t, store, modelID, badStatusKey, "bad-status-model", 0)

	bobEndpoint := seedEndpointRaw(t, store, bob, true)
	bobKey, _ := seedForwardKey(t, store, bobEndpoint, "bob-secret", true)
	seedFetchedModelRaw(t, store, bobKey, "bob-upstream")
	seedForwardBinding(t, store, modelID, bobKey, "bob-upstream", 0)

	route, err := store.ResolveForwardRoute(ctx, alice, "provider/with/slash/model", 16)
	if err != nil {
		t.Fatalf("ResolveForwardRoute: %v", err)
	}
	if route.ModelID != modelID || route.UserID != alice || route.FullName != "provider/with/slash/model" {
		t.Fatalf("route = %+v", route)
	}
	if len(route.Candidates) != 2 {
		t.Fatalf("usable candidates = %+v, want exactly two", route.Candidates)
	}
	if route.Candidates[0].BindingID != firstBinding || route.Candidates[1].BindingID != lateBinding {
		t.Fatalf("candidate order = %+v", route.Candidates)
	}
	if _, err := store.ResolveForwardRoute(ctx, alice, "provider/with/slash/model", 1); !errors.Is(err, ErrForwardProjectionLimit) {
		t.Fatalf("candidate limit error=%v", err)
	}
	for _, candidate := range route.Candidates {
		if candidate.EndpointID != validEndpoint || candidate.EndpointKeyID != validKey {
			t.Fatalf("unusable or cross-owner candidate escaped SQL filter: %+v", candidate)
		}
	}

	for _, test := range []struct {
		user int64
		name string
	}{
		{bob, "provider/with/slash/model"},
		{alice, "provider/with/slash"},
		{alice, "with/slash/model"},
	} {
		route, err := store.ResolveForwardRoute(ctx, test.user, test.name, 16)
		if test.user == bob && test.name == "provider/with/slash/model" {
			if err != nil || route.UserID != bob || len(route.Candidates) != 0 {
				t.Fatalf("bob route = %+v err=%v", route, err)
			}
			continue
		}
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("ResolveForwardRoute(%d,%q) error=%v, want not found", test.user, test.name, err)
		}
	}
}

func TestForwardTargetRevalidatesAndKeepsCiphertextDedicated(t *testing.T) {
	store := newModelsTestStore(t)
	ctx := context.Background()
	alice := seedUserRaw(t, store, "target-alice")
	bob := seedUserRaw(t, store, "target-bob")
	modelID := seedModelRaw(t, store, alice, "p", "m")
	endpointID := seedEndpointRaw(t, store, alice, true)
	keyID, ciphertext := seedForwardKey(t, store, endpointID, "target-secret", true)
	seedFetchedModelRaw(t, store, keyID, "upstream")
	bindingID := seedForwardBinding(t, store, modelID, keyID, "upstream", 0)

	target, err := store.GetForwardTarget(ctx, alice, "p/m", bindingID)
	if err != nil {
		t.Fatalf("GetForwardTarget: %v", err)
	}
	if target.EndpointID != endpointID || target.EndpointKeyID != keyID || target.UpstreamModelID != "upstream" {
		t.Fatalf("target metadata = %+v", target)
	}
	if formatted := fmt.Sprintf("%v %#v %+v", target, target, target); strings.Contains(formatted, ciphertext) {
		t.Fatalf("target formatting leaked ciphertext: %s", formatted)
	}
	if got := target.TakeEncryptedSecret(); got != ciphertext {
		t.Fatal("target did not transfer the stored ciphertext")
	}
	if got := target.TakeEncryptedSecret(); got != "" {
		t.Fatal("target transferred its ciphertext more than once")
	}
	for _, args := range []struct {
		user    int64
		name    string
		binding int64
	}{
		{bob, "p/m", bindingID},
		{alice, "p/other", bindingID},
		{alice, "p/m", bindingID + 999},
	} {
		if _, err := store.GetForwardTarget(ctx, args.user, args.name, args.binding); !errors.Is(err, ErrNotFound) {
			t.Fatalf("cross/stale target error=%v, want not found", err)
		}
	}

	if _, err := store.DB().Exec(`UPDATE endpoint_keys SET enabled=0 WHERE id=?`, keyID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetForwardTarget(ctx, alice, "p/m", bindingID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("disabled key target error=%v, want not found", err)
	}
	if _, err := store.DB().Exec(`UPDATE endpoint_keys SET enabled=1 WHERE id=?`, keyID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().Exec(`DELETE FROM fetched_models WHERE endpoint_key_id=?`, keyID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetForwardTarget(ctx, alice, "p/m", bindingID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("uncached target error=%v, want not found", err)
	}
}

func TestCallerModelsProjectionAndLimits(t *testing.T) {
	store := newModelsTestStore(t)
	ctx := context.Background()
	alice := seedUserRaw(t, store, "list-alice")
	bob := seedUserRaw(t, store, "list-bob")
	seedModelRaw(t, store, alice, "p", "one")
	seedModelRaw(t, store, alice, "q/r", "two")
	seedModelRaw(t, store, bob, "hidden", "model")

	models, err := store.ListCallerModels(ctx, alice, 2)
	if err != nil {
		t.Fatalf("ListCallerModels: %v", err)
	}
	if len(models) != 2 || models[0].FullName != "p/one" || models[1].FullName != "q/r/two" {
		t.Fatalf("models = %+v", models)
	}
	if _, err := store.ListCallerModels(ctx, alice, 1); !errors.Is(err, ErrForwardProjectionLimit) {
		t.Fatalf("list limit error=%v", err)
	}

	route, err := store.ResolveForwardRoute(ctx, alice, "p/one", 1)
	if err != nil || len(route.Candidates) != 0 {
		t.Fatalf("draft route=%+v err=%v", route, err)
	}
}
