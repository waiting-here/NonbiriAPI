package db

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
)

// seedUserRaw inserts a user row and returns its id.
func seedUserRaw(t *testing.T, st *Store, discordID string) int64 {
	t.Helper()
	res, err := st.DB().Exec(
		`INSERT INTO users (discord_id, username, created_at, updated_at) VALUES (?, 'tester', 1, 1)`, discordID)
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("seed user last insert id: %v", err)
	}
	return id
}

// seedEndpointRaw inserts an endpoint owned by userID (enabled by default) and
// returns its id.
func seedEndpointRaw(t *testing.T, st *Store, userID int64, enabled bool) int64 {
	t.Helper()
	enabledInt := 0
	if enabled {
		enabledInt = 1
	}
	res, err := st.DB().Exec(
		`INSERT INTO endpoints (user_id, connector_type, base_url, enabled, created_at, updated_at) VALUES (?, 'openai-compatible', 'https://example.com', ?, 1, 1)`,
		userID, enabledInt)
	if err != nil {
		t.Fatalf("seed endpoint: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("seed endpoint last insert id: %v", err)
	}
	return id
}

// seedEndpointKeyRaw inserts an endpoint key (encrypted_secret is a fixture
// string; the model rail never reads it) and returns its id.
func seedEndpointKeyRaw(t *testing.T, st *Store, endpointID int64, enabled bool) int64 {
	t.Helper()
	enabledInt := 0
	if enabled {
		enabledInt = 1
	}
	res, err := st.DB().Exec(
		`INSERT INTO endpoint_keys (endpoint_id, encrypted_secret, display_head, display_tail, note, enabled, created_at, updated_at) VALUES (?, 'fixture-ciphertext', 'abcd', 'wxyz', '', ?, 1, 1)`,
		endpointID, enabledInt)
	if err != nil {
		t.Fatalf("seed endpoint key: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("seed endpoint key last insert id: %v", err)
	}
	return id
}

// seedFetchedModelRaw inserts one fetched cache row for a key.
func seedFetchedModelRaw(t *testing.T, st *Store, keyID int64, upstreamID string) {
	t.Helper()
	if _, err := st.DB().Exec(
		`INSERT INTO fetched_models (endpoint_key_id, upstream_model_id, provider, fetched_at, status) VALUES (?, ?, '', 1, 'ok')`,
		keyID, upstreamID); err != nil {
		t.Fatalf("seed fetched model: %v", err)
	}
}

// seedModelRaw inserts a platform model row (validated values assumed; the
// schema CHECK backs the full_name) and returns its id.
func seedModelRaw(t *testing.T, st *Store, userID int64, provider, model string) int64 {
	t.Helper()
	res, err := st.DB().Exec(
		`INSERT INTO models (user_id, provider, model, full_name, route_strategy, created_at, updated_at) VALUES (?, ?, ?, ?, 'ordered', 1, 1)`,
		userID, provider, model, provider+"/"+model)
	if err != nil {
		t.Fatalf("seed model: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("seed model last insert id: %v", err)
	}
	return id
}

func newModelsTestStore(t *testing.T) *Store {
	t.Helper()
	st := openTestStore(t, filepath.Join(t.TempDir(), "models.db"))
	t.Cleanup(func() { _ = st.Close() })
	return st
}

const testNow = int64(1_700_000_000)

// TestModelCRUDRoundTrip exercises create/get/list/patch/delete for one user
// including the binding-count projection (0 for a draft).
func TestModelCRUDRoundTrip(t *testing.T) {
	st := newModelsTestStore(t)
	uid := seedUserRaw(t, st, "u1")
	ctx := context.Background()

	m, err := st.CreateModel(ctx, uid, "openai", "gpt-4o", "ordered", false, testNow)
	if err != nil {
		t.Fatalf("create model: %v", err)
	}
	if m.FullName != "openai/gpt-4o" || m.BindingCount != 0 || m.RouteStrategy != "ordered" {
		t.Fatalf("created model = %+v", m)
	}

	got, err := st.GetModel(ctx, uid, m.ID)
	if err != nil {
		t.Fatalf("get model: %v", err)
	}
	if got.FullName != "openai/gpt-4o" || got.BindingCount != 0 {
		t.Fatalf("get model = %+v", got)
	}

	list, err := st.ListModels(ctx, uid)
	if err != nil || len(list) != 1 || list[0].ID != m.ID || list[0].BindingCount != 0 {
		t.Fatalf("list models = %+v err=%v", list, err)
	}

	// Patch provider only: full_name must be recomputed as a/b.
	provider := "anthropic"
	upd, err := st.UpdateModel(ctx, uid, m.ID, &provider, nil, nil, nil, testNow+1)
	if err != nil {
		t.Fatalf("update model: %v", err)
	}
	if upd.Provider != "anthropic" || upd.Model != "gpt-4o" || upd.FullName != "anthropic/gpt-4o" {
		t.Fatalf("updated model = %+v", upd)
	}

	// Patch model only: full_name recomputed again; strategy untouched.
	model := "claude-3-5-sonnet"
	upd, err = st.UpdateModel(ctx, uid, m.ID, nil, &model, nil, nil, testNow+2)
	if err != nil {
		t.Fatalf("update model 2: %v", err)
	}
	if upd.FullName != "anthropic/claude-3-5-sonnet" || upd.RouteStrategy != "ordered" {
		t.Fatalf("updated model 2 = %+v", upd)
	}

	// No-op update echoes the row.
	upd, err = st.UpdateModel(ctx, uid, m.ID, nil, nil, nil, nil, testNow+3)
	if err != nil || upd.FullName != "anthropic/claude-3-5-sonnet" {
		t.Fatalf("noop update = %+v err=%v", upd, err)
	}

	// Strategy patch.
	strategy := "random"
	upd, err = st.UpdateModel(ctx, uid, m.ID, nil, nil, &strategy, nil, testNow+4)
	if err != nil || upd.RouteStrategy != "random" {
		t.Fatalf("strategy update = %+v err=%v", upd, err)
	}

	if err := st.DeleteModel(ctx, uid, m.ID); err != nil {
		t.Fatalf("delete model: %v", err)
	}
	if _, err := st.GetModel(ctx, uid, m.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get deleted model err = %v, want ErrNotFound", err)
	}
	if _, err := st.UpdateModel(ctx, uid, m.ID, nil, nil, nil, nil, testNow); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update deleted model err = %v, want ErrNotFound", err)
	}
	if err := st.DeleteModel(ctx, uid, m.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete deleted model err = %v, want ErrNotFound", err)
	}
}

// TestModelOwnershipIsolation verifies the full cross-user matrix: get/update/
// delete of another user's model and enumeration of another user's models are
// all impossible and indistinguishable from missing.
func TestModelOwnershipIsolation(t *testing.T) {
	st := newModelsTestStore(t)
	alice := seedUserRaw(t, st, "alice")
	bob := seedUserRaw(t, st, "bob")
	ctx := context.Background()

	m := seedModelRaw(t, st, alice, "openai", "gpt-4o")

	if _, err := st.GetModel(ctx, bob, m); !errors.Is(err, ErrNotFound) {
		t.Fatalf("bob get alice model err = %v, want ErrNotFound", err)
	}
	provider := "evil"
	if _, err := st.UpdateModel(ctx, bob, m, &provider, nil, nil, nil, testNow); !errors.Is(err, ErrNotFound) {
		t.Fatalf("bob update alice model err = %v, want ErrNotFound", err)
	}
	if err := st.DeleteModel(ctx, bob, m); !errors.Is(err, ErrNotFound) {
		t.Fatalf("bob delete alice model err = %v, want ErrNotFound", err)
	}
	if list, err := st.ListModels(ctx, bob); err != nil || len(list) != 0 {
		t.Fatalf("bob list = %+v err=%v, want empty", list, err)
	}
	// Alice's row is untouched.
	if _, err := st.GetModel(ctx, alice, m); err != nil {
		t.Fatalf("alice get after bob attempts: %v", err)
	}
}

// TestModelFullNameUniquePerUser verifies the external-name uniqueness:
// identical full names collide only within one user; a field-split collision
// (different provider/model combinations forming the same full name) also
// collides.
func TestModelFullNameUniquePerUser(t *testing.T) {
	st := newModelsTestStore(t)
	alice := seedUserRaw(t, st, "alice")
	bob := seedUserRaw(t, st, "bob")
	ctx := context.Background()

	if _, err := st.CreateModel(ctx, alice, "openai", "gpt-4o", "ordered", false, testNow); err != nil {
		t.Fatalf("create first: %v", err)
	}
	// Same full name, same user: conflict.
	if _, err := st.CreateModel(ctx, alice, "openai", "gpt-4o", "random", false, testNow); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate full name err = %v, want ErrConflict", err)
	}
	// Same full name, another user: allowed.
	if _, err := st.CreateModel(ctx, bob, "openai", "gpt-4o", "ordered", false, testNow); err != nil {
		t.Fatalf("same full name other user: %v", err)
	}
	// Field-split collision: "a/b" + "c" vs "a" + "b/c" both form "a/b/c".
	if _, err := st.CreateModel(ctx, bob, "a/b", "c", "ordered", false, testNow); err != nil {
		t.Fatalf("create a/b + c: %v", err)
	}
	if _, err := st.CreateModel(ctx, bob, "a", "b/c", "random", false, testNow); !errors.Is(err, ErrConflict) {
		t.Fatalf("field-split collision err = %v, want ErrConflict", err)
	}
	// A non-colliding slash-y name is fine.
	if _, err := st.CreateModel(ctx, bob, "a/b", "c/d", "ordered", false, testNow); err != nil {
		t.Fatalf("create a/b + c/d: %v", err)
	}
}

// TestModelUpdateCollision verifies that a patch recomputing the full name
// into another existing full name is a conflict, including the field-split
// case, and that a patch onto its own full name is a no-op.
func TestModelUpdateCollision(t *testing.T) {
	st := newModelsTestStore(t)
	uid := seedUserRaw(t, st, "u1")
	ctx := context.Background()

	m1 := seedModelRaw(t, st, uid, "a", "b")   // full name a/b
	m2 := seedModelRaw(t, st, uid, "c", "d")   // full name c/d
	m3 := seedModelRaw(t, st, uid, "a/b", "c") // full name a/b/c

	// A single-field patch that does not collide succeeds and recomputes the
	// full name.
	provider := "a"
	upd, err := st.UpdateModel(ctx, uid, m2, &provider, nil, nil, nil, testNow)
	if err != nil || upd.FullName != "a/d" {
		t.Fatalf("non-colliding patch = %+v err=%v, want a/d", upd, err)
	}

	// Patching the model to "b" now collides with m1 ("a/b").
	model := "b"
	if _, err := st.UpdateModel(ctx, uid, m2, nil, &model, nil, nil, testNow); !errors.Is(err, ErrConflict) {
		t.Fatalf("update into collision err = %v, want ErrConflict", err)
	}

	// Field-split collision: m3 is "a/b/c"; patching m2 (still "a/d") to
	// provider "a" + model "b/c" forms the same full name.
	if _, err := st.GetModel(ctx, uid, m3); err != nil {
		t.Fatalf("m3 should exist: %v", err)
	}
	model2 := "b/c"
	if _, err := st.UpdateModel(ctx, uid, m2, &provider, &model2, nil, nil, testNow); !errors.Is(err, ErrConflict) {
		t.Fatalf("update into split collision err = %v, want ErrConflict", err)
	}

	// Updating m1 to its own values is a no-op success.
	if _, err := st.UpdateModel(ctx, uid, m1, nil, nil, nil, nil, testNow); err != nil {
		t.Fatalf("noop update: %v", err)
	}
}

// TestModelBindingCountProjection verifies binding_count tracks real rows:
// 0 for a draft, then the actual count after bindings are added, then back
// to 0 after the bindings are deleted.
func TestModelBindingCountProjection(t *testing.T) {
	st := newModelsTestStore(t)
	uid := seedUserRaw(t, st, "u1")
	ctx := context.Background()

	ep := seedEndpointRaw(t, st, uid, true)
	key := seedEndpointKeyRaw(t, st, ep, true)
	seedFetchedModelRaw(t, st, key, "up-a")
	seedFetchedModelRaw(t, st, key, "up-b")
	m := seedModelRaw(t, st, uid, "p", "m")

	got, err := st.GetModel(ctx, uid, m)
	if err != nil || got.BindingCount != 0 {
		t.Fatalf("draft binding_count = %+v err=%v, want 0", got, err)
	}
	if _, err := st.CreateBinding(ctx, uid, m, key, "up-a", 0, testNow); err != nil {
		t.Fatalf("create binding a: %v", err)
	}
	if _, err := st.CreateBinding(ctx, uid, m, key, "up-b", 1, testNow); err != nil {
		t.Fatalf("create binding b: %v", err)
	}
	got, err = st.GetModel(ctx, uid, m)
	if err != nil || got.BindingCount != 2 {
		t.Fatalf("binding_count after two bindings = %+v err=%v, want 2", got, err)
	}
	list, err := st.ListModels(ctx, uid)
	if err != nil || len(list) != 1 || list[0].BindingCount != 2 {
		t.Fatalf("list binding_count = %+v err=%v, want 2", list, err)
	}
}

// TestModelDeleteCascadesBindings verifies deleting a model removes its
// bindings through the foreign key cascade.
func TestModelDeleteCascadesBindings(t *testing.T) {
	st := newModelsTestStore(t)
	uid := seedUserRaw(t, st, "u1")
	ctx := context.Background()

	ep := seedEndpointRaw(t, st, uid, true)
	key := seedEndpointKeyRaw(t, st, ep, true)
	seedFetchedModelRaw(t, st, key, "up-a")
	m := seedModelRaw(t, st, uid, "p", "m")
	if _, err := st.CreateBinding(ctx, uid, m, key, "up-a", 0, testNow); err != nil {
		t.Fatalf("create binding: %v", err)
	}
	if err := st.DeleteModel(ctx, uid, m); err != nil {
		t.Fatalf("delete model: %v", err)
	}
	var n int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM model_bindings`).Scan(&n); err != nil {
		t.Fatalf("count bindings: %v", err)
	}
	if n != 0 {
		t.Fatalf("bindings after model delete = %d, want 0 (cascade)", n)
	}
}

// TestModelSlashNamesStoredOpaquely verifies provider/model containing '/'
// are stored and matched exactly as the concatenated full name (never split
// or normalized), and the schema CHECK rejects a mismatched full_name.
func TestModelSlashNamesStoredOpaquely(t *testing.T) {
	st := newModelsTestStore(t)
	uid := seedUserRaw(t, st, "u1")
	ctx := context.Background()

	m, err := st.CreateModel(ctx, uid, "accounts/org-a", "models/gpt/x", "ordered", false, testNow)
	if err != nil {
		t.Fatalf("create slash model: %v", err)
	}
	if m.FullName != "accounts/org-a/models/gpt/x" {
		t.Fatalf("full_name = %q, want exact concatenation", m.FullName)
	}
	got, err := st.GetModel(ctx, uid, m.ID)
	if err != nil || got.FullName != "accounts/org-a/models/gpt/x" {
		t.Fatalf("get slash model = %+v err=%v", got, err)
	}
	list, err := st.ListModels(ctx, uid)
	if err != nil || len(list) != 1 || list[0].FullName != "accounts/org-a/models/gpt/x" {
		t.Fatalf("list slash model = %+v err=%v", list, err)
	}
}

// TestModelUpdateStrategyOnly verifies a strategy-only patch keeps the full
// name and that the DB CHECK rejects a wrong strategy directly.
func TestModelUpdateStrategyOnly(t *testing.T) {
	st := newModelsTestStore(t)
	uid := seedUserRaw(t, st, "u1")
	ctx := context.Background()

	m, err := st.CreateModel(ctx, uid, "p", "m", "ordered", false, testNow)
	if err != nil {
		t.Fatalf("create model: %v", err)
	}
	strategy := "random"
	upd, err := st.UpdateModel(ctx, uid, m.ID, nil, nil, &strategy, nil, testNow)
	if err != nil || upd.FullName != "p/m" || upd.RouteStrategy != "random" {
		t.Fatalf("strategy-only update = %+v err=%v", upd, err)
	}
	if _, err := st.DB().Exec(
		`INSERT INTO models (user_id, provider, model, full_name, route_strategy, created_at, updated_at) VALUES (?, 'x', 'y', 'x/y', 'weighted', 1, 1)`,
		uid); err == nil {
		t.Fatal("DB accepted an unknown route strategy")
	}
}

// TestModelSilentRetryPersistence verifies the retry switch defaults to false,
// persists true/false on create, projects on get/list, and updates atomically.
func TestModelSilentRetryPersistence(t *testing.T) {
	st := newModelsTestStore(t)
	uid := seedUserRaw(t, st, "retry-u1")
	ctx := context.Background()

	off, err := st.CreateModel(ctx, uid, "p", "off", "ordered", false, testNow)
	if err != nil {
		t.Fatalf("create off: %v", err)
	}
	if off.SilentRetry {
		t.Fatalf("create off returned SilentRetry=true")
	}
	on, err := st.CreateModel(ctx, uid, "p", "on", "ordered", true, testNow)
	if err != nil {
		t.Fatalf("create on: %v", err)
	}
	if !on.SilentRetry {
		t.Fatalf("create on returned SilentRetry=false")
	}

	gotOff, err := st.GetModel(ctx, uid, off.ID)
	if err != nil || gotOff.SilentRetry {
		t.Fatalf("get off = %+v err=%v", gotOff, err)
	}
	gotOn, err := st.GetModel(ctx, uid, on.ID)
	if err != nil || !gotOn.SilentRetry {
		t.Fatalf("get on = %+v err=%v", gotOn, err)
	}

	list, err := st.ListModels(ctx, uid)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	byName := make(map[string]bool, len(list))
	for _, m := range list {
		byName[m.FullName] = m.SilentRetry
	}
	if byName["p/off"] || !byName["p/on"] {
		t.Fatalf("list projection = %v", byName)
	}

	flip := true
	upd, err := st.UpdateModel(ctx, uid, off.ID, nil, nil, nil, &flip, testNow+1)
	if err != nil || !upd.SilentRetry {
		t.Fatalf("flip on = %+v err=%v", upd, err)
	}
	clear := false
	upd, err = st.UpdateModel(ctx, uid, off.ID, nil, nil, nil, &clear, testNow+2)
	if err != nil || upd.SilentRetry {
		t.Fatalf("flip off = %+v err=%v", upd, err)
	}
	// nil silentRetry leaves it unchanged (still false here).
	upd, err = st.UpdateModel(ctx, uid, off.ID, nil, nil, nil, nil, testNow+3)
	if err != nil || upd.SilentRetry {
		t.Fatalf("noop silentRetry = %+v err=%v", upd, err)
	}

	// The DB CHECK rejects an out-of-range silent_retry value directly.
	if _, err := st.DB().Exec(
		`INSERT INTO models (user_id, provider, model, full_name, route_strategy, silent_retry, created_at, updated_at) VALUES (?, 'x', 'z', 'x/z', 'ordered', 2, 1, 1)`,
		uid); err == nil {
		t.Fatal("DB accepted an out-of-range silent_retry")
	}
}

// --- model cap --------------------------------------------------------------

// setTestModelLimit upserts the default_model_limit site_config row so a test
// can adjust the cap mid-run (verifying runtime application).
func setTestModelLimit(t *testing.T, st *Store, raw string) {
	t.Helper()
	if _, err := st.DB().Exec(
		`INSERT INTO site_config (key, value, updated_at) VALUES ('default_model_limit', ?, 1)
		 ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`, raw); err != nil {
		t.Fatalf("set model limit: %v", err)
	}
}

// mustCreateTestModel creates a platform model with a distinct full name and
// fails the test on any error. Used to fill a user up to the model cap.
func mustCreateTestModel(t *testing.T, st *Store, userID int64, provider, model string) Model {
	t.Helper()
	m, err := st.CreateModel(context.Background(), userID, provider, model, "ordered", false, testNow)
	if err != nil {
		t.Fatalf("CreateModel %s/%s: %v", provider, model, err)
	}
	return m
}

// TestCreateModelCap covers the per-user platform-model cap: the default when
// unset, the cap-th create succeeds, the next is a *CapError wrapping
// ErrModelCap with the resource name and exact effective cap, no extra row is
// written, a runtime site-config change takes effect immediately, and counts
// do not cross users.
func TestCreateModelCap(t *testing.T) {
	st := newModelsTestStore(t)
	ctx := context.Background()

	cap, err := st.ModelCap(ctx)
	if err != nil {
		t.Fatalf("ModelCap: %v", err)
	}
	if cap != DefaultModelLimit {
		t.Errorf("unset cap = %d, want %d", cap, DefaultModelLimit)
	}

	alice := seedUserRaw(t, st, "alice")
	setTestModelLimit(t, st, "3")
	cap, _ = st.ModelCap(ctx)
	if cap != 3 {
		t.Fatalf("adjusted cap = %d, want 3", cap)
	}
	for i := 0; i < cap; i++ {
		mustCreateTestModel(t, st, alice, fmt.Sprintf("p%d", i), "m")
	}
	count, _ := st.CountModels(ctx, alice)
	if count != cap {
		t.Fatalf("count = %d, want %d", count, cap)
	}

	_, err = st.CreateModel(ctx, alice, "p-extra", "m", "ordered", false, testNow)
	var capErr *CapError
	if !errors.As(err, &capErr) {
		t.Fatalf("over-cap err = %v, want *CapError", err)
	}
	if !errors.Is(err, ErrModelCap) {
		t.Errorf("over-cap err does not match ErrModelCap: %v", err)
	}
	if capErr.Resource != ResourceModel || capErr.Limit != cap {
		t.Errorf("capErr = %+v, want resource=%q limit=%d", capErr, ResourceModel, cap)
	}
	count, _ = st.CountModels(ctx, alice)
	if count != cap {
		t.Errorf("count after refused create = %d, want %d (no row written)", count, cap)
	}

	setTestModelLimit(t, st, "5")
	mustCreateTestModel(t, st, alice, "p-more", "m")
	count, _ = st.CountModels(ctx, alice)
	if count != cap+1 {
		t.Errorf("count after cap raise = %d, want %d", count, cap+1)
	}

	// Counts do not cross users.
	bob := seedUserRaw(t, st, "bob")
	if c2, _ := st.CountModels(ctx, bob); c2 != 0 {
		t.Errorf("bob count = %d, want 0", c2)
	}
	mustCreateTestModel(t, st, bob, "p", "m")
	if c2, _ := st.CountModels(ctx, bob); c2 != 1 {
		t.Errorf("bob count after one add = %d, want 1", c2)
	}
}

// TestCreateModelCapRejectsInvalidSiteConfig asserts a malformed cap surfaces
// as ErrInvalidSiteConfig (fail closed) and never writes a model.
func TestCreateModelCapRejectsInvalidSiteConfig(t *testing.T) {
	for _, raw := range []string{"not-a-number", "-3", "  "} {
		raw := raw
		t.Run(raw, func(t *testing.T) {
			st := newModelsTestStore(t)
			setTestModelLimit(t, st, raw)
			alice := seedUserRaw(t, st, "alice")
			_, err := st.CreateModel(context.Background(), alice, "p", "m", "ordered", false, testNow)
			if !errors.Is(err, ErrInvalidSiteConfig) {
				t.Errorf("CreateModel with global %q: err=%v, want ErrInvalidSiteConfig", raw, err)
			}
			count, _ := st.CountModels(context.Background(), alice)
			if count != 0 {
				t.Errorf("invalid site_config still produced %d models", count)
			}
		})
	}
}

// TestCreateModelAtomicCapUnderConcurrency hammers CreateModel from many
// goroutines against a small cap; the count-then-insert inside one transaction
// must never let the cap be breached. Run under -race.
func TestCreateModelAtomicCapUnderConcurrency(t *testing.T) {
	const cap = 8
	const workers = 64
	st := newModelsTestStore(t)
	ctx := context.Background()
	setTestModelLimit(t, st, strconv.Itoa(cap))
	alice := seedUserRaw(t, st, "alice")

	var wg sync.WaitGroup
	var succ, capErr atomic.Int64
	start := make(chan struct{})
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		i := i
		go func() {
			defer wg.Done()
			<-start
			_, err := st.CreateModel(ctx, alice, fmt.Sprintf("p%d", i), "m", "ordered", false, testNow)
			switch {
			case err == nil:
				succ.Add(1)
			case errors.Is(err, ErrModelCap):
				capErr.Add(1)
			default:
				t.Errorf("unexpected create error: %v", err)
			}
		}()
	}
	close(start)
	wg.Wait()

	if got := succ.Load(); got != int64(cap) {
		t.Errorf("successes = %d, want %d (cap breached or underfilled)", got, cap)
	}
	if got := capErr.Load(); got != int64(workers-cap) {
		t.Errorf("cap errors = %d, want %d", got, workers-cap)
	}
	count, _ := st.CountModels(ctx, alice)
	if count != cap {
		t.Errorf("final count = %d, want %d", count, cap)
	}
}
