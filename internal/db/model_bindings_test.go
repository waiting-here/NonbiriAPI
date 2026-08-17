package db

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func newBindingsTestStore(t *testing.T) *Store {
	t.Helper()
	st := openTestStore(t, filepath.Join(t.TempDir(), "bindings.db"))
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// TestBindingCreateListOrder verifies the happy path: bindings are created
// against a cached upstream id and listed ordered by (ord, id).
func TestBindingCreateListOrder(t *testing.T) {
	st := newBindingsTestStore(t)
	uid := seedUserRaw(t, st, "u1")
	ctx := context.Background()

	ep := seedEndpointRaw(t, st, uid, true)
	key := seedEndpointKeyRaw(t, st, ep, true)
	seedFetchedModelRaw(t, st, key, "up-a")
	seedFetchedModelRaw(t, st, key, "up-b")
	seedFetchedModelRaw(t, st, key, "up-c")
	m := seedModelRaw(t, st, uid, "p", "m")

	// Create out of order; ord 0 default is exercised by b2.
	b3, err := st.CreateBinding(ctx, uid, m, key, "up-c", 5, testNow)
	if err != nil {
		t.Fatalf("create binding c: %v", err)
	}
	b1, err := st.CreateBinding(ctx, uid, m, key, "up-a", 1, testNow)
	if err != nil {
		t.Fatalf("create binding a: %v", err)
	}
	b2, err := st.CreateBinding(ctx, uid, m, key, "up-b", 1, testNow)
	if err != nil {
		t.Fatalf("create binding b: %v", err)
	}
	if b1.Ord != 1 || b2.Ord != 1 || b3.Ord != 5 {
		t.Fatalf("created ords = %d/%d/%d", b1.Ord, b2.Ord, b3.Ord)
	}

	list, err := st.ListBindings(ctx, uid, m)
	if err != nil {
		t.Fatalf("list bindings: %v", err)
	}
	// Expected order: ord 1 with lower id first (b1 before b2), then ord 5.
	want := []int64{b1.ID, b2.ID, b3.ID}
	if len(list) != 3 {
		t.Fatalf("list length = %d, want 3", len(list))
	}
	for i, w := range want {
		if list[i].ID != w {
			t.Fatalf("list[%d].ID = %d, want %d (ord,id order violated)", i, list[i].ID, w)
		}
	}
	if list[0].EndpointKeyID != key || list[0].UpstreamModelID != "up-a" || list[0].Ord != 1 {
		t.Fatalf("list[0] = %+v", list[0])
	}
}

// TestBindingCreateRejectsCrossUser verifies every cross-user shape yields
// ErrNotFound: another user's model, another user's key, and a same-user
// model with another user's key.
func TestBindingCreateRejectsCrossUser(t *testing.T) {
	st := newBindingsTestStore(t)
	alice := seedUserRaw(t, st, "alice")
	bob := seedUserRaw(t, st, "bob")
	ctx := context.Background()

	aliceEP := seedEndpointRaw(t, st, alice, true)
	aliceKey := seedEndpointKeyRaw(t, st, aliceEP, true)
	seedFetchedModelRaw(t, st, aliceKey, "up-a")
	aliceModel := seedModelRaw(t, st, alice, "p", "m")

	bobEP := seedEndpointRaw(t, st, bob, true)
	bobKey := seedEndpointKeyRaw(t, st, bobEP, true)
	seedFetchedModelRaw(t, st, bobKey, "up-a")
	bobModel := seedModelRaw(t, st, bob, "p", "m")

	// Bob's model, Alice's key.
	if _, err := st.CreateBinding(ctx, bob, bobModel, aliceKey, "up-a", 0, testNow); !errors.Is(err, ErrNotFound) {
		t.Fatalf("bob model + alice key err = %v, want ErrNotFound", err)
	}
	// Alice's model, Bob's key.
	if _, err := st.CreateBinding(ctx, alice, aliceModel, bobKey, "up-a", 0, testNow); !errors.Is(err, ErrNotFound) {
		t.Fatalf("alice model + bob key err = %v, want ErrNotFound", err)
	}
	// Bob's model with Bob's key is fine (sanity).
	if _, err := st.CreateBinding(ctx, bob, bobModel, bobKey, "up-a", 0, testNow); err != nil {
		t.Fatalf("bob self binding: %v", err)
	}
	// Alice's list must not show Bob's binding.
	list, err := st.ListBindings(ctx, alice, aliceModel)
	if err != nil || len(list) != 0 {
		t.Fatalf("alice list = %+v err=%v, want empty", list, err)
	}
	// A nonexistent model id is indistinguishable.
	if _, err := st.CreateBinding(ctx, alice, 99999, aliceKey, "up-a", 0, testNow); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing model err = %v, want ErrNotFound", err)
	}
	// A nonexistent key id is indistinguishable.
	if _, err := st.CreateBinding(ctx, alice, aliceModel, 99999, "up-a", 0, testNow); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing key err = %v, want ErrNotFound", err)
	}
}

// TestBindingCreateRejectsDisabledResources verifies disabled keys and
// disabled endpoints never enter the candidate set.
func TestBindingCreateRejectsDisabledResources(t *testing.T) {
	st := newBindingsTestStore(t)
	uid := seedUserRaw(t, st, "u1")
	ctx := context.Background()

	epOK := seedEndpointRaw(t, st, uid, true)
	keyOK := seedEndpointKeyRaw(t, st, epOK, true)
	seedFetchedModelRaw(t, st, keyOK, "up-a")

	epDisabled := seedEndpointRaw(t, st, uid, false)
	keyOnDisabled := seedEndpointKeyRaw(t, st, epDisabled, true)
	seedFetchedModelRaw(t, st, keyOnDisabled, "up-a")

	epOn := seedEndpointRaw(t, st, uid, true)
	keyDisabled := seedEndpointKeyRaw(t, st, epOn, false)
	seedFetchedModelRaw(t, st, keyDisabled, "up-a")

	m := seedModelRaw(t, st, uid, "p", "m")

	if _, err := st.CreateBinding(ctx, uid, m, keyDisabled, "up-a", 0, testNow); !errors.Is(err, ErrNotFound) {
		t.Fatalf("disabled key err = %v, want ErrNotFound", err)
	}
	if _, err := st.CreateBinding(ctx, uid, m, keyOnDisabled, "up-a", 0, testNow); !errors.Is(err, ErrNotFound) {
		t.Fatalf("key on disabled endpoint err = %v, want ErrNotFound", err)
	}
	if _, err := st.CreateBinding(ctx, uid, m, keyOK, "up-a", 0, testNow); err != nil {
		t.Fatalf("enabled key binding: %v", err)
	}
}

// TestBindingCreateRejectsUncachedUpstream verifies the fetched-cache gate:
// the upstream id must exist in the key's cache, never an arbitrary string.
func TestBindingCreateRejectsUncachedUpstream(t *testing.T) {
	st := newBindingsTestStore(t)
	uid := seedUserRaw(t, st, "u1")
	ctx := context.Background()

	ep := seedEndpointRaw(t, st, uid, true)
	key := seedEndpointKeyRaw(t, st, ep, true)
	seedFetchedModelRaw(t, st, key, "up-a")
	m := seedModelRaw(t, st, uid, "p", "m")

	if _, err := st.CreateBinding(ctx, uid, m, key, "up-b", 0, testNow); !errors.Is(err, ErrNotFound) {
		t.Fatalf("uncached upstream err = %v, want ErrNotFound", err)
	}
	// A cached id with a different key is not cached for that key.
	ep2 := seedEndpointRaw(t, st, uid, true)
	key2 := seedEndpointKeyRaw(t, st, ep2, true)
	seedFetchedModelRaw(t, st, key2, "up-c")
	if _, err := st.CreateBinding(ctx, uid, m, key2, "up-a", 0, testNow); !errors.Is(err, ErrNotFound) {
		t.Fatalf("upstream cached on other key err = %v, want ErrNotFound", err)
	}
}

// TestBindingCreateDuplicateConflict verifies the unique triple: the same
// (model, key, upstream) twice is a conflict; different upstream ids on the
// same key and the same upstream on different keys are fine.
func TestBindingCreateDuplicateConflict(t *testing.T) {
	st := newBindingsTestStore(t)
	uid := seedUserRaw(t, st, "u1")
	ctx := context.Background()

	ep := seedEndpointRaw(t, st, uid, true)
	key := seedEndpointKeyRaw(t, st, ep, true)
	seedFetchedModelRaw(t, st, key, "up-a")
	seedFetchedModelRaw(t, st, key, "up-b")
	ep2 := seedEndpointRaw(t, st, uid, true)
	key2 := seedEndpointKeyRaw(t, st, ep2, true)
	seedFetchedModelRaw(t, st, key2, "up-a")
	m := seedModelRaw(t, st, uid, "p", "m")

	if _, err := st.CreateBinding(ctx, uid, m, key, "up-a", 0, testNow); err != nil {
		t.Fatalf("create first: %v", err)
	}
	if _, err := st.CreateBinding(ctx, uid, m, key, "up-a", 3, testNow); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate triple err = %v, want ErrConflict", err)
	}
	// Same key, different upstream: fine.
	if _, err := st.CreateBinding(ctx, uid, m, key, "up-b", 1, testNow); err != nil {
		t.Fatalf("same key different upstream: %v", err)
	}
	// Same upstream, different key: fine.
	if _, err := st.CreateBinding(ctx, uid, m, key2, "up-a", 0, testNow); err != nil {
		t.Fatalf("same upstream different key: %v", err)
	}
}

// TestBindingUpdate verifies ord/upstream updates: cache existence for the new
// upstream, duplicate-triple conflict, cross-user and wrong-path not_found,
// and the disabled-key gate on updates.
func TestBindingUpdate(t *testing.T) {
	st := newBindingsTestStore(t)
	alice := seedUserRaw(t, st, "alice")
	bob := seedUserRaw(t, st, "bob")
	ctx := context.Background()

	aliceEP := seedEndpointRaw(t, st, alice, true)
	aliceKey := seedEndpointKeyRaw(t, st, aliceEP, true)
	seedFetchedModelRaw(t, st, aliceKey, "up-a")
	seedFetchedModelRaw(t, st, aliceKey, "up-b")
	aliceModel := seedModelRaw(t, st, alice, "p", "m")

	bobEP := seedEndpointRaw(t, st, bob, true)
	bobKey := seedEndpointKeyRaw(t, st, bobEP, true)
	seedFetchedModelRaw(t, st, bobKey, "up-a")
	bobModel := seedModelRaw(t, st, bob, "p", "m")

	b, err := st.CreateBinding(ctx, alice, aliceModel, aliceKey, "up-a", 0, testNow)
	if err != nil {
		t.Fatalf("create binding: %v", err)
	}

	// Ord update.
	ord := int64(7)
	upd, err := st.UpdateBinding(ctx, alice, aliceModel, b.ID, &ord, nil, testNow)
	if err != nil || upd.Ord != 7 || upd.UpstreamModelID != "up-a" {
		t.Fatalf("ord update = %+v err=%v", upd, err)
	}

	// Upstream update to another cached id.
	upstream := "up-b"
	upd, err = st.UpdateBinding(ctx, alice, aliceModel, b.ID, nil, &upstream, testNow)
	if err != nil || upd.UpstreamModelID != "up-b" || upd.Ord != 7 {
		t.Fatalf("upstream update = %+v err=%v", upd, err)
	}

	// Upstream update to an uncached id: not_found.
	uncached := "up-zzz"
	if _, err := st.UpdateBinding(ctx, alice, aliceModel, b.ID, nil, &uncached, testNow); !errors.Is(err, ErrNotFound) {
		t.Fatalf("uncached upstream update err = %v, want ErrNotFound", err)
	}

	// Cross-user update.
	if _, err := st.UpdateBinding(ctx, bob, bobModel, b.ID, &ord, nil, testNow); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-user update err = %v, want ErrNotFound", err)
	}
	// Alice's model id with a binding id that exists on Bob's model path: the
	// binding id is scoped to the model id in the SQL, so not_found.
	bobBinding, err := st.CreateBinding(ctx, bob, bobModel, bobKey, "up-a", 0, testNow)
	if err != nil {
		t.Fatalf("bob create binding: %v", err)
	}
	if _, err := st.UpdateBinding(ctx, alice, aliceModel, bobBinding.ID, &ord, nil, testNow); !errors.Is(err, ErrNotFound) {
		t.Fatalf("wrong model path update err = %v, want ErrNotFound", err)
	}

	// Duplicate triple after update: create a second binding with "up-a"
	// (b currently holds "up-b"), then updating b back to "up-a" conflicts.
	if _, err := st.CreateBinding(ctx, alice, aliceModel, aliceKey, "up-a", 2, testNow); err != nil {
		t.Fatalf("create second binding: %v", err)
	}
	backToA := "up-a"
	if _, err := st.UpdateBinding(ctx, alice, aliceModel, b.ID, nil, &backToA, testNow); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate triple update err = %v, want ErrConflict", err)
	}

	// Disabled key gate: disabling the key makes even an ord-only update fail
	// (a real change is required so the gate is exercised).
	if _, err := st.DB().Exec(`UPDATE endpoint_keys SET enabled=0 WHERE id=?`, aliceKey); err != nil {
		t.Fatalf("disable key: %v", err)
	}
	changedOrd := int64(9)
	if _, err := st.UpdateBinding(ctx, alice, aliceModel, b.ID, &changedOrd, nil, testNow); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update on disabled key err = %v, want ErrNotFound", err)
	}
}

// TestBindingDeleteAndCascade verifies binding delete ownership and the
// cascade paths: key delete, endpoint delete, and model delete all remove
// bindings immediately.
func TestBindingDeleteAndCascade(t *testing.T) {
	st := newBindingsTestStore(t)
	alice := seedUserRaw(t, st, "alice")
	bob := seedUserRaw(t, st, "bob")
	ctx := context.Background()

	aliceEP := seedEndpointRaw(t, st, alice, true)
	aliceKey := seedEndpointKeyRaw(t, st, aliceEP, true)
	seedFetchedModelRaw(t, st, aliceKey, "up-a")
	aliceModel := seedModelRaw(t, st, alice, "p", "m")

	bobModel := seedModelRaw(t, st, bob, "p", "m")

	b, err := st.CreateBinding(ctx, alice, aliceModel, aliceKey, "up-a", 0, testNow)
	if err != nil {
		t.Fatalf("create binding: %v", err)
	}
	// Cross-user delete.
	if err := st.DeleteBinding(ctx, bob, bobModel, b.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-user delete err = %v, want ErrNotFound", err)
	}
	// Wrong model path delete.
	if err := st.DeleteBinding(ctx, alice, bobModel, b.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("wrong path delete err = %v, want ErrNotFound", err)
	}
	// Legit delete.
	if err := st.DeleteBinding(ctx, alice, aliceModel, b.ID); err != nil {
		t.Fatalf("delete binding: %v", err)
	}
	if err := st.DeleteBinding(ctx, alice, aliceModel, b.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("re-delete err = %v, want ErrNotFound", err)
	}

	// Cascade on key delete.
	b2, err := st.CreateBinding(ctx, alice, aliceModel, aliceKey, "up-a", 0, testNow)
	if err != nil {
		t.Fatalf("recreate binding: %v", err)
	}
	if err := st.DeleteEndpointKey(ctx, alice, aliceEP, aliceKey); err != nil {
		t.Fatalf("delete key: %v", err)
	}
	if _, err := st.GetModel(ctx, alice, aliceModel); err != nil {
		t.Fatalf("model survives key delete: %v", err)
	}
	var n int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM model_bindings WHERE id=?`, b2.ID).Scan(&n); err != nil {
		t.Fatalf("count binding: %v", err)
	}
	if n != 0 {
		t.Fatalf("binding survived key delete (cascade), want 0")
	}

	// Cascade on endpoint delete.
	aliceEP2 := seedEndpointRaw(t, st, alice, true)
	aliceKey2 := seedEndpointKeyRaw(t, st, aliceEP2, true)
	seedFetchedModelRaw(t, st, aliceKey2, "up-a")
	if _, err := st.CreateBinding(ctx, alice, aliceModel, aliceKey2, "up-a", 0, testNow); err != nil {
		t.Fatalf("create binding 3: %v", err)
	}
	if err := st.DeleteEndpoint(ctx, alice, aliceEP2); err != nil {
		t.Fatalf("delete endpoint: %v", err)
	}
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM model_bindings WHERE model_id=?`, aliceModel).Scan(&n); err != nil {
		t.Fatalf("count bindings: %v", err)
	}
	if n != 0 {
		t.Fatalf("bindings survived endpoint delete (cascade), want 0")
	}

	// Cascade on model delete.
	aliceEP3 := seedEndpointRaw(t, st, alice, true)
	aliceKey3 := seedEndpointKeyRaw(t, st, aliceEP3, true)
	seedFetchedModelRaw(t, st, aliceKey3, "up-a")
	if _, err := st.CreateBinding(ctx, alice, aliceModel, aliceKey3, "up-a", 0, testNow); err != nil {
		t.Fatalf("create binding 4: %v", err)
	}
	if err := st.DeleteModel(ctx, alice, aliceModel); err != nil {
		t.Fatalf("delete model: %v", err)
	}
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM model_bindings`).Scan(&n); err != nil {
		t.Fatalf("count bindings: %v", err)
	}
	if n != 0 {
		t.Fatalf("bindings survived model delete (cascade), want 0")
	}
}

// TestBindingListCrossUserEmpty verifies the list is ownership-filtered in
// SQL even without the service-level model check.
func TestBindingListCrossUserEmpty(t *testing.T) {
	st := newBindingsTestStore(t)
	alice := seedUserRaw(t, st, "alice")
	bob := seedUserRaw(t, st, "bob")
	ctx := context.Background()

	aliceEP := seedEndpointRaw(t, st, alice, true)
	aliceKey := seedEndpointKeyRaw(t, st, aliceEP, true)
	seedFetchedModelRaw(t, st, aliceKey, "up-a")
	aliceModel := seedModelRaw(t, st, alice, "p", "m")

	if _, err := st.CreateBinding(ctx, alice, aliceModel, aliceKey, "up-a", 0, testNow); err != nil {
		t.Fatalf("create binding: %v", err)
	}
	list, err := st.ListBindings(ctx, bob, aliceModel)
	if err != nil || len(list) != 0 {
		t.Fatalf("bob list = %+v err=%v, want empty", list, err)
	}
}

// TestBindingConcurrentCreateConflict races two goroutines creating the same
// triple: exactly one wins, the other must see ErrConflict, and the final
// state has a single row. Also races ord patches against a reader.
func TestBindingConcurrentCreateConflict(t *testing.T) {
	st := newBindingsTestStore(t)
	uid := seedUserRaw(t, st, "u1")
	ctx := context.Background()

	ep := seedEndpointRaw(t, st, uid, true)
	key := seedEndpointKeyRaw(t, st, ep, true)
	seedFetchedModelRaw(t, st, key, "up-a")
	seedFetchedModelRaw(t, st, key, "up-b")
	m := seedModelRaw(t, st, uid, "p", "m")

	const workers = 8
	results := make(chan error, workers)
	for i := 0; i < workers; i++ {
		go func() {
			_, err := st.CreateBinding(ctx, uid, m, key, "up-a", int64(i), testNow)
			results <- err
		}()
	}
	successes := 0
	conflicts := 0
	for i := 0; i < workers; i++ {
		err := <-results
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrConflict):
			conflicts++
		default:
			t.Fatalf("unexpected concurrent create error: %v", err)
		}
	}
	if successes != 1 || conflicts != workers-1 {
		t.Fatalf("concurrent creates: successes=%d conflicts=%d, want 1/%d", successes, conflicts, workers-1)
	}
	var n int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM model_bindings WHERE model_id=?`, m).Scan(&n); err != nil {
		t.Fatalf("count bindings: %v", err)
	}
	if n != 1 {
		t.Fatalf("final binding count = %d, want 1", n)
	}

	// Concurrent ord patches on the single surviving binding must never
	// corrupt the row or deadlock; the final ord is one of the written values.
	ord := int64(0)
	_, err := st.CreateBinding(ctx, uid, m, key, "up-b", ord, testNow)
	if err != nil {
		t.Fatalf("create second binding: %v", err)
	}
	// Find the first binding id.
	b, err := bindingIDByUpstream(ctx, st, uid, m, "up-a")
	if err != nil {
		t.Fatalf("read binding: %v", err)
	}
	patchDone := make(chan error, workers)
	for i := 0; i < workers; i++ {
		go func(i int) {
			v := int64(i * 100)
			_, err := st.UpdateBinding(ctx, uid, m, b, &v, nil, testNow)
			patchDone <- err
		}(i)
	}
	for i := 0; i < workers; i++ {
		if err := <-patchDone; err != nil {
			t.Fatalf("concurrent patch error: %v", err)
		}
	}
	var finalOrd int64
	if err := st.DB().QueryRow(`SELECT ord FROM model_bindings WHERE id=?`, b).Scan(&finalOrd); err != nil {
		t.Fatalf("read final ord: %v", err)
	}
	if finalOrd%100 != 0 {
		t.Fatalf("final ord = %d, want a multiple of 100", finalOrd)
	}
}

// bindingIDByUpstream is a test-only helper returning the binding id of a
// model with the given upstream id.
func bindingIDByUpstream(ctx context.Context, st *Store, userID, modelID int64, upstream string) (int64, error) {
	var id int64
	err := st.db.QueryRowContext(ctx,
		`SELECT b.id FROM model_bindings b JOIN models m ON b.model_id=m.id WHERE b.model_id=? AND m.user_id=? AND b.upstream_model_id=?`,
		modelID, userID, upstream).Scan(&id)
	return id, err
}
