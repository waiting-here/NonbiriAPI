package model

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbtest"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

// testService bundles a Service with a real Store so tests assert end-to-end
// behavior (ownership through SQL, atomic candidate validation) without any
// fakes in the persistence path.
type testService struct {
	svc   *Service
	store *db.Store
}

func newTestService(t *testing.T) *testService {
	t.Helper()
	key := bytes.Repeat([]byte{0x42}, secret.MasterKeyBytes)
	vault, err := secret.New(key)
	clear(key)
	if err != nil {
		t.Fatalf("create test vault: %v", err)
	}
	t.Cleanup(func() { _ = vault.Close() })
	dbPath := filepath.Join(t.TempDir(), "model.db")
	dbtest.EnsureOwnerOnlyParent(t, dbPath)
	store, err := db.Open(dbPath, vault)
	if err != nil {
		t.Fatalf("open test store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	svc := NewService(store)
	svc.now = func() int64 { return 1 }
	return &testService{svc: svc, store: store}
}

// seedUser inserts a users row and returns its id.
func (ts *testService) seedUser(t *testing.T, discordID string) int64 {
	t.Helper()
	res, err := ts.store.DB().Exec(
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

// seedEndpointKeyAndCache creates an enabled endpoint + enabled key + fetched
// cache rows for userID and returns the key id. The encrypted_secret is a
// fixture string: this rail never reads it.
func (ts *testService) seedEndpointKeyAndCache(t *testing.T, userID int64, upstreamIDs ...string) int64 {
	t.Helper()
	res, err := ts.store.DB().Exec(
		`INSERT INTO endpoints (user_id, connector_type, base_url, enabled, created_at, updated_at) VALUES (?, 'openai-compatible', 'https://example.com', 1, 1, 1)`,
		userID)
	if err != nil {
		t.Fatalf("seed endpoint: %v", err)
	}
	epID, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("seed endpoint last insert id: %v", err)
	}
	res, err = ts.store.DB().Exec(
		`INSERT INTO endpoint_keys (endpoint_id, encrypted_secret, display_head, display_tail, enabled, created_at, updated_at) VALUES (?, 'fixture-ciphertext', 'abcd', 'wxyz', 1, 1, 1)`,
		epID)
	if err != nil {
		t.Fatalf("seed endpoint key: %v", err)
	}
	keyID, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("seed endpoint key last insert id: %v", err)
	}
	for _, id := range upstreamIDs {
		if _, err := ts.store.DB().Exec(
			`INSERT INTO fetched_models (endpoint_key_id, upstream_model_id, provider, fetched_at, status) VALUES (?, ?, '', 1, 'ok')`,
			keyID, id); err != nil {
			t.Fatalf("seed fetched model: %v", err)
		}
	}
	return keyID
}

// TestServiceCreateModelValidationMatrix exercises the name-part boundary:
// empty, control characters, ASCII/Unicode leading and trailing whitespace,
// over-length, invalid UTF-8; and the exact acceptance of 64 runes and '/'.
func TestServiceCreateModelValidationMatrix(t *testing.T) {
	ts := newTestService(t)
	uid := ts.seedUser(t, "u1")
	ctx := context.Background()

	invalid := []struct {
		name, provider, model string
	}{
		{"empty provider", "", "m"},
		{"empty model", "p", ""},
		{"control in provider", "p\x01", "m"},
		{"DEL in model", "p", "m\x7f"},
		{"leading ASCII space", " p", "m"},
		{"trailing ASCII space", "p", "m "},
		{"leading tab", "\tp", "m"},
		{"trailing newline", "p", "m\n"},
		{"leading Unicode space", "\u00a0p", "m"},
		{"trailing Unicode space", "p", "m\u2003"},
		{"full-width space edge", "\u3000p", "m"},
		{"invalid UTF-8", "p\xff\xfe", "m"},
		{"over-length provider", strings.Repeat("a", 65), "m"},
		{"over-length model", "p", strings.Repeat("b", 65)},
	}
	for _, tc := range invalid {
		if _, err := ts.svc.CreateModel(ctx, uid, tc.provider, tc.model, nil, nil); !errors.Is(err, ErrInvalidRequest) {
			t.Errorf("%s: err = %v, want ErrInvalidRequest", tc.name, err)
		}
	}

	valid := []struct {
		name, provider, model string
	}{
		{"exactly 64 runes", strings.Repeat("a", 64), "m"},
		{"slash in provider", "openai/org-a", "gpt-4o"},
		{"slash in model", "openai", "models/gpt/x"},
		{"interior spaces allowed", "open ai", "gpt 4"},
		{"unicode interior", "模型", "gpt-4o"},
	}
	for _, tc := range valid {
		if _, err := ts.svc.CreateModel(ctx, uid, tc.provider, tc.model, nil, nil); err != nil {
			t.Errorf("%s: unexpected error %v", tc.name, err)
		}
	}
}

// TestServiceCharityPrefixReserved verifies the frozen §K rule: a provider
// starting with the reserved charity prefix is refused at create and update
// time, while near-miss forms and the model half stay allowed.
func TestServiceCharityPrefixReserved(t *testing.T) {
	ts := newTestService(t)
	uid := ts.seedUser(t, "u1")
	ctx := context.Background()

	m, err := ts.svc.CreateModel(ctx, uid, "openai", "gpt-4o", nil, nil)
	if err != nil {
		t.Fatalf("seed model: %v", err)
	}

	rejected := []string{"[公益]donor", "[公益]x/y"}
	for _, provider := range rejected {
		if _, err := ts.svc.CreateModel(ctx, uid, provider, "m", nil, nil); !errors.Is(err, ErrCharityPrefixReserved) {
			t.Errorf("create provider %q: err = %v, want ErrCharityPrefixReserved", provider, err)
		} else if !errors.Is(err, ErrInvalidRequest) {
			t.Errorf("create provider %q: err = %v, must wrap ErrInvalidRequest", provider, err)
		}
		newProvider := provider
		if _, err := ts.svc.UpdateModel(ctx, uid, m.ID, &newProvider, nil, nil, nil); !errors.Is(err, ErrCharityPrefixReserved) {
			t.Errorf("update provider %q: err = %v, want ErrCharityPrefixReserved", provider, err)
		}
	}

	// Near misses and non-provider halves are not affected.
	allowed := []struct{ name, provider, model string }{
		{"prefix inside provider", "x[公益]y", "m"},
		{"prefix in model half", "openai", "[公益]m"},
		{"partial bracket", "[公益", "m"},
	}
	for _, tc := range allowed {
		if _, err := ts.svc.CreateModel(ctx, uid, tc.provider, tc.model, nil, nil); err != nil {
			t.Errorf("%s: unexpected error %v", tc.name, err)
		}
	}

	// The stored model is unchanged after the refused updates.
	got, err := ts.svc.GetModel(ctx, uid, m.ID)
	if err != nil {
		t.Fatalf("get model: %v", err)
	}
	if got.Provider != "openai" || got.Model != "gpt-4o" {
		t.Fatalf("refused update mutated row: provider=%q model=%q", got.Provider, got.Model)
	}
}

// TestServiceRouteStrategyStrictness verifies the strategy default and the
// strict ordered|random acceptance.
func TestServiceRouteStrategyStrictness(t *testing.T) {
	ts := newTestService(t)
	uid := ts.seedUser(t, "u1")
	ctx := context.Background()

	m, err := ts.svc.CreateModel(ctx, uid, "p", "m", nil, nil)
	if err != nil || m.RouteStrategy != "ordered" {
		t.Fatalf("default strategy = %+v err=%v, want ordered", m, err)
	}
	if _, err := ts.svc.CreateModel(ctx, uid, "p2", "m2", strPtr("random"), nil); err != nil {
		t.Fatalf("random strategy: %v", err)
	}
	for _, bad := range []string{"weighted", "ROUND_ROBIN", "Ordered", "random "} {
		if _, err := ts.svc.CreateModel(ctx, uid, "p3", "m3", strPtr(bad), nil); !errors.Is(err, ErrInvalidRequest) {
			t.Errorf("strategy %q: err = %v, want ErrInvalidRequest", bad, err)
		}
	}
	// Update with an unknown strategy is also refused.
	if _, err := ts.svc.UpdateModel(ctx, uid, m.ID, nil, nil, strPtr("weighted"), nil); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("update strategy err = %v, want ErrInvalidRequest", err)
	}
}

// TestServiceFullNameCollisionCrossSplit verifies the service surfaces the
// conflict for field-split full-name collisions and allows cross-user reuse.
func TestServiceFullNameCollisionCrossSplit(t *testing.T) {
	ts := newTestService(t)
	alice := ts.seedUser(t, "alice")
	bob := ts.seedUser(t, "bob")
	ctx := context.Background()

	if _, err := ts.svc.CreateModel(ctx, alice, "a/b", "c", nil, nil); err != nil {
		t.Fatalf("create a/b + c: %v", err)
	}
	if _, err := ts.svc.CreateModel(ctx, alice, "a", "b/c", nil, nil); !errors.Is(err, db.ErrConflict) {
		t.Fatalf("split collision err = %v, want ErrConflict", err)
	}
	bobFirst, err := ts.svc.CreateModel(ctx, bob, "a", "b/c", nil, nil)
	if err != nil {
		t.Fatalf("cross-user same full name: %v", err)
	}
	bobSecond, err := ts.svc.CreateModel(ctx, bob, "x", "y", nil, nil)
	if err != nil {
		t.Fatalf("bob second model: %v", err)
	}
	// Update bob's second model into the same full name as his first:
	// field-split collision.
	if _, err := ts.svc.UpdateModel(ctx, bob, bobSecond.ID, strPtr("a/b"), strPtr("c"), nil, nil); !errors.Is(err, db.ErrConflict) {
		t.Fatalf("update split collision err = %v, want ErrConflict", err)
	}
	// bobFirst is untouched.
	if _, err := ts.svc.GetModel(ctx, bob, bobFirst.ID); err != nil {
		t.Fatalf("bob first model should survive: %v", err)
	}
}

// TestServiceBindingValidation verifies upstream id and ord boundaries at the
// service layer.
func TestServiceBindingValidation(t *testing.T) {
	ts := newTestService(t)
	uid := ts.seedUser(t, "u1")
	ctx := context.Background()

	m, err := ts.svc.CreateModel(ctx, uid, "p", "m", nil, nil)
	if err != nil {
		t.Fatalf("create model: %v", err)
	}
	key := ts.seedEndpointKeyAndCache(t, uid, "up-a")

	for _, tc := range []struct {
		name string
		up   string
		ord  *int64
	}{
		{"empty upstream", "", nil},
		{"control upstream", "up\x00a", nil},
		{"leading space upstream", " up-a", nil},
		{"trailing unicode space upstream", "up-a\u00a0", nil},
		{"over-length upstream", strings.Repeat("u", 513), nil},
		{"negative ord", "up-a", int64Ptr(-1)},
		{"over-max ord", "up-a", int64Ptr(MaxOrd + 1)},
	} {
		if _, err := ts.svc.CreateBinding(ctx, uid, m.ID, key, tc.up, tc.ord); !errors.Is(err, ErrInvalidRequest) {
			t.Errorf("%s: err = %v, want ErrInvalidRequest", tc.name, err)
		}
	}

	// Exact max ord and a long-but-valid upstream id are accepted (upstream
	// must be cached; seed it).
	ts.store.DB().Exec(`INSERT INTO fetched_models (endpoint_key_id, upstream_model_id, provider, fetched_at, status) VALUES (?, ?, '', 1, 'ok')`, key, strings.Repeat("u", 512))
	if _, err := ts.svc.CreateBinding(ctx, uid, m.ID, key, strings.Repeat("u", 512), int64Ptr(MaxOrd)); err != nil {
		t.Fatalf("max bounds binding: %v", err)
	}
}

// TestServiceBindingCandidateGates verifies the service-level gates: uncached
// upstream, disabled key, disabled endpoint, and cross-user resources all
// surface as not_found with no distinguishing detail.
func TestServiceBindingCandidateGates(t *testing.T) {
	ts := newTestService(t)
	alice := ts.seedUser(t, "alice")
	bob := ts.seedUser(t, "bob")
	ctx := context.Background()

	mA, err := ts.svc.CreateModel(ctx, alice, "p", "m", nil, nil)
	if err != nil {
		t.Fatalf("create model: %v", err)
	}
	mB, err := ts.svc.CreateModel(ctx, bob, "p", "m", nil, nil)
	if err != nil {
		t.Fatalf("create bob model: %v", err)
	}
	keyA := ts.seedEndpointKeyAndCache(t, alice, "up-a")
	keyB := ts.seedEndpointKeyAndCache(t, bob, "up-a")

	// Uncached upstream.
	if _, err := ts.svc.CreateBinding(ctx, alice, mA.ID, keyA, "up-zz", nil); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("uncached err = %v, want ErrNotFound", err)
	}
	// Cross-user key.
	if _, err := ts.svc.CreateBinding(ctx, alice, mA.ID, keyB, "up-a", nil); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("cross-user key err = %v, want ErrNotFound", err)
	}
	// Cross-user model.
	if _, err := ts.svc.CreateBinding(ctx, alice, mB.ID, keyA, "up-a", nil); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("cross-user model err = %v, want ErrNotFound", err)
	}
	// Disabled key.
	if _, err := ts.store.DB().Exec(`UPDATE endpoint_keys SET enabled=0 WHERE id=?`, keyA); err != nil {
		t.Fatalf("disable key: %v", err)
	}
	if _, err := ts.svc.CreateBinding(ctx, alice, mA.ID, keyA, "up-a", nil); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("disabled key err = %v, want ErrNotFound", err)
	}
	if _, err := ts.store.DB().Exec(`UPDATE endpoint_keys SET enabled=1 WHERE id=?`, keyA); err != nil {
		t.Fatalf("re-enable key: %v", err)
	}
	// Disabled endpoint.
	if _, err := ts.store.DB().Exec(`UPDATE endpoints SET enabled=0 WHERE id=(SELECT endpoint_id FROM endpoint_keys WHERE id=?)`, keyA); err != nil {
		t.Fatalf("disable endpoint: %v", err)
	}
	if _, err := ts.svc.CreateBinding(ctx, alice, mA.ID, keyA, "up-a", nil); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("disabled endpoint err = %v, want ErrNotFound", err)
	}
}

// TestServiceDraftAndBindingLifecycle verifies the zero-binding draft model
// (created, listed with binding_count 0, deleted) and the binding lifecycle
// through the service.
func TestServiceDraftAndBindingLifecycle(t *testing.T) {
	ts := newTestService(t)
	uid := ts.seedUser(t, "u1")
	ctx := context.Background()

	// Draft model: zero bindings is legal.
	m, err := ts.svc.CreateModel(ctx, uid, "draft", "model", nil, nil)
	if err != nil {
		t.Fatalf("create draft: %v", err)
	}
	if m.BindingCount != 0 {
		t.Fatalf("draft binding_count = %d, want 0", m.BindingCount)
	}
	bindings, err := ts.svc.ListBindings(ctx, uid, m.ID)
	if err != nil || len(bindings) != 0 {
		t.Fatalf("draft bindings = %+v err=%v, want empty", bindings, err)
	}

	// Bind one cached upstream.
	key := ts.seedEndpointKeyAndCache(t, uid, "up-a", "up-b")
	b, err := ts.svc.CreateBinding(ctx, uid, m.ID, key, "up-a", int64Ptr(3))
	if err != nil {
		t.Fatalf("create binding: %v", err)
	}
	if b.Ord != 3 {
		t.Fatalf("binding ord = %d, want 3", b.Ord)
	}
	list, err := ts.svc.ListModels(ctx, uid)
	if err != nil || len(list) != 1 || list[0].BindingCount != 1 {
		t.Fatalf("list after binding = %+v err=%v, want binding_count 1", list, err)
	}

	// Update the binding (upstream swap within the cache, ord change).
	if _, err := ts.svc.UpdateBinding(ctx, uid, m.ID, b.ID, int64Ptr(1), strPtr("up-b")); err != nil {
		t.Fatalf("update binding: %v", err)
	}
	// The old triple is free again; the new one conflicts.
	if _, err := ts.svc.CreateBinding(ctx, uid, m.ID, key, "up-b", nil); !errors.Is(err, db.ErrConflict) {
		t.Fatalf("duplicate after update err = %v, want ErrConflict", err)
	}
	// Delete the binding: back to a draft.
	if err := ts.svc.DeleteBinding(ctx, uid, m.ID, b.ID); err != nil {
		t.Fatalf("delete binding: %v", err)
	}
	list, err = ts.svc.ListModels(ctx, uid)
	if err != nil || list[0].BindingCount != 0 {
		t.Fatalf("list after unbind = %+v err=%v, want binding_count 0", list, err)
	}
	// Delete the draft model.
	if err := ts.svc.DeleteModel(ctx, uid, m.ID); err != nil {
		t.Fatalf("delete draft: %v", err)
	}
	if _, err := ts.svc.GetModel(ctx, uid, m.ID); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("get deleted draft err = %v, want ErrNotFound", err)
	}
	// Cross-user binding list is indistinguishable from a missing model.
	if _, err := ts.svc.ListBindings(ctx, uid+1000, m.ID); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("cross-user list err = %v, want ErrNotFound", err)
	}
}

// TestServiceGuardsNilService verifies nil-receiver and nil-repo guards.
func TestServiceGuardsNilService(t *testing.T) {
	ctx := context.Background()
	if _, err := (*Service)(nil).CreateModel(ctx, 1, "p", "m", nil, nil); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("nil service create err = %v", err)
	}
	if _, err := (*Service)(nil).GetModel(ctx, 1, 1); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("nil service get err = %v", err)
	}
	if err := (*Service)(nil).DeleteModel(ctx, 1, 1); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("nil service delete err = %v", err)
	}
	svc := NewService(nil)
	if _, err := svc.CreateModel(ctx, 1, "p", "m", nil, nil); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("nil repo create err = %v", err)
	}
	if err := svc.DeleteBinding(ctx, 1, 1, 1); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("nil repo delete binding err = %v", err)
	}
	// Non-positive ids behave like missing resources.
	if _, err := svc.CreateBinding(ctx, 0, 1, 1, "up-a", nil); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("zero user err = %v", err)
	}
}

func strPtr(s string) *string { return &s }
func int64Ptr(n int64) *int64 { return &n }
func boolPtr(b bool) *bool    { return &b }

// TestServiceSilentRetrySwitch verifies the retry switch defaults false, is
// persisted on create, and is strictly a bool on create/update.
func TestServiceSilentRetrySwitch(t *testing.T) {
	ts := newTestService(t)
	uid := ts.seedUser(t, "retry-svc")
	ctx := context.Background()

	def, err := ts.svc.CreateModel(ctx, uid, "p", "def", nil, nil)
	if err != nil || def.SilentRetry {
		t.Fatalf("default create = %+v err=%v", def, err)
	}
	on, err := ts.svc.CreateModel(ctx, uid, "p", "on", nil, boolPtr(true))
	if err != nil || !on.SilentRetry {
		t.Fatalf("create on = %+v err=%v", on, err)
	}
	off, err := ts.svc.CreateModel(ctx, uid, "p", "off", nil, boolPtr(false))
	if err != nil || off.SilentRetry {
		t.Fatalf("create off = %+v err=%v", off, err)
	}

	got, err := ts.svc.GetModel(ctx, uid, on.ID)
	if err != nil || !got.SilentRetry {
		t.Fatalf("get on = %+v err=%v", got, err)
	}

	flipped, err := ts.svc.UpdateModel(ctx, uid, def.ID, nil, nil, nil, boolPtr(true))
	if err != nil || !flipped.SilentRetry {
		t.Fatalf("flip on = %+v err=%v", flipped, err)
	}
	cleared, err := ts.svc.UpdateModel(ctx, uid, def.ID, nil, nil, nil, boolPtr(false))
	if err != nil || cleared.SilentRetry {
		t.Fatalf("flip off = %+v err=%v", cleared, err)
	}
	// nil silentRetry leaves the switch unchanged (still false here).
	unchanged, err := ts.svc.UpdateModel(ctx, uid, def.ID, nil, nil, nil, nil)
	if err != nil || unchanged.SilentRetry {
		t.Fatalf("noop silentRetry = %+v err=%v", unchanged, err)
	}
}

// --- resource caps (service layer) -----------------------------------------

// setModelLimit upserts the default_model_limit site_config row so a test can
// adjust the cap mid-run (verifying runtime application).
func (ts *testService) setModelLimit(t *testing.T, raw string) {
	t.Helper()
	if _, err := ts.store.DB().Exec(
		`INSERT INTO site_config (key, value, updated_at) VALUES ('default_model_limit', ?, 1)
		 ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`, raw); err != nil {
		t.Fatalf("set model limit: %v", err)
	}
}

// setBindingLimit upserts the default_binding_limit site_config row.
func (ts *testService) setBindingLimit(t *testing.T, raw string) {
	t.Helper()
	if _, err := ts.store.DB().Exec(
		`INSERT INTO site_config (key, value, updated_at) VALUES ('default_binding_limit', ?, 1)
		 ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`, raw); err != nil {
		t.Fatalf("set binding limit: %v", err)
	}
}

// TestServiceModelCap covers the per-user platform-model cap through the
// service layer: the cap-th create succeeds, one over is a *db.CapError
// carrying resource + limit, a runtime site-config change takes effect
// immediately, and counts do not cross users.
func TestServiceModelCap(t *testing.T) {
	ts := newTestService(t)
	uid := ts.seedUser(t, "u1")
	ctx := context.Background()

	cap, err := ts.store.ModelCap(ctx)
	if err != nil {
		t.Fatalf("ModelCap: %v", err)
	}
	if cap != db.DefaultModelLimit {
		t.Errorf("unset cap = %d, want %d", cap, db.DefaultModelLimit)
	}

	ts.setModelLimit(t, "3")
	cap, _ = ts.store.ModelCap(ctx)
	if cap != 3 {
		t.Fatalf("adjusted cap = %d, want 3", cap)
	}
	for i := 0; i < cap; i++ {
		if _, err := ts.svc.CreateModel(ctx, uid, fmt.Sprintf("p%d", i), "m", nil, nil); err != nil {
			t.Fatalf("create model %d: %v", i, err)
		}
	}

	_, err = ts.svc.CreateModel(ctx, uid, "p-extra", "m", nil, nil)
	var capErr *db.CapError
	if !errors.As(err, &capErr) {
		t.Fatalf("over-cap err = %v, want *db.CapError", err)
	}
	if capErr.Resource != db.ResourceModel || capErr.Limit != cap {
		t.Errorf("capErr = %+v, want resource=%q limit=%d", capErr, db.ResourceModel, cap)
	}

	ts.setModelLimit(t, "5")
	if _, err := ts.svc.CreateModel(ctx, uid, "p-more", "m", nil, nil); err != nil {
		t.Fatalf("create after cap raise: %v", err)
	}
	count, _ := ts.store.CountModels(ctx, uid)
	if count != cap+1 {
		t.Errorf("count after cap raise = %d, want %d", count, cap+1)
	}

	// Counts do not cross users.
	bob := ts.seedUser(t, "bob")
	if c2, _ := ts.store.CountModels(ctx, bob); c2 != 0 {
		t.Errorf("bob count = %d, want 0", c2)
	}
}

// TestServiceBindingCap covers the per-model binding-count cap through the
// service layer: the cap-th add succeeds, one over is a *db.CapError carrying
// resource + limit, a runtime site-config change takes effect immediately,
// and counts do not cross models.
func TestServiceBindingCap(t *testing.T) {
	ts := newTestService(t)
	uid := ts.seedUser(t, "u1")
	ctx := context.Background()
	key := ts.seedEndpointKeyAndCache(t, uid, "up-0", "up-1", "up-2", "up-3", "up-4")

	ts.setBindingLimit(t, "3")
	cap, _ := ts.store.BindingCap(ctx)
	if cap != 3 {
		t.Fatalf("adjusted cap = %d, want 3", cap)
	}
	m, err := ts.svc.CreateModel(ctx, uid, "p", "m", nil, nil)
	if err != nil {
		t.Fatalf("create model: %v", err)
	}
	for i := 0; i < cap; i++ {
		if _, err := ts.svc.CreateBinding(ctx, uid, m.ID, key, fmt.Sprintf("up-%d", i), nil); err != nil {
			t.Fatalf("create binding %d: %v", i, err)
		}
	}

	_, err = ts.svc.CreateBinding(ctx, uid, m.ID, key, fmt.Sprintf("up-%d", cap), nil)
	var capErr *db.CapError
	if !errors.As(err, &capErr) {
		t.Fatalf("over-cap err = %v, want *db.CapError", err)
	}
	if capErr.Resource != db.ResourceBinding || capErr.Limit != cap {
		t.Errorf("capErr = %+v, want resource=%q limit=%d", capErr, db.ResourceBinding, cap)
	}

	ts.setBindingLimit(t, "5")
	if _, err := ts.svc.CreateBinding(ctx, uid, m.ID, key, fmt.Sprintf("up-%d", cap), nil); err != nil {
		t.Fatalf("create after cap raise: %v", err)
	}
	count, _ := ts.store.CountBindings(ctx, uid, m.ID)
	if count != cap+1 {
		t.Errorf("count after cap raise = %d, want %d", count, cap+1)
	}

	// Counts do not cross models.
	m2, err := ts.svc.CreateModel(ctx, uid, "p2", "m", nil, nil)
	if err != nil {
		t.Fatalf("create model 2: %v", err)
	}
	if c2, _ := ts.store.CountBindings(ctx, uid, m2.ID); c2 != 0 {
		t.Errorf("second model count = %d, want 0", c2)
	}
}
