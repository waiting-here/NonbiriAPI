package db

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/egress"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

func testEndpointKeyContext(t *testing.T, userID int64, endpoint Endpoint, keyID int64) secret.EndpointKeyContext {
	t.Helper()
	_, origin, err := egress.CanonicalEndpointTarget(endpoint.BaseURL)
	if err != nil {
		t.Fatalf("canonical endpoint target: %v", err)
	}
	credentialContext, err := secret.NewEndpointKeyContext(userID, endpoint.ID, keyID, origin)
	if err != nil {
		t.Fatalf("endpoint key context: %v", err)
	}
	return credentialContext
}

// TestCreateEndpointKeyOwnershipAtomicInsert asserts the INSERT...SELECT
// ownership guard: a key is created only when the endpoint exists and belongs
// to the caller; otherwise ErrNotFound and zero rows (no read-then-write race).
func TestCreateEndpointKeyOwnershipAtomicInsert(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "keyown.db"))
	defer st.Close()
	alice := seedTestUser(t, st, "alice", nil)
	bob := seedTestUser(t, st, "bob", nil)
	aEP := mustCreateTestEndpoint(t, st, alice, "https://alice.example/v1/")

	plaintext := []byte("sk-test")

	// Missing endpoint id.
	if _, err := st.CreateEndpointKey(context.Background(), alice, aEP.ID+999, bytes.Clone(plaintext), "h", "t", "note", true, 1); !errors.Is(err, ErrNotFound) {
		t.Errorf("create key on missing endpoint: err=%v, want not_found", err)
	}
	// Cross-user endpoint.
	if _, err := st.CreateEndpointKey(context.Background(), bob, aEP.ID, bytes.Clone(plaintext), "h", "t", "note", true, 1); !errors.Is(err, ErrNotFound) {
		t.Errorf("create key on alice endpoint as bob: err=%v, want not_found", err)
	}
	// No key rows inserted by the failed attempts.
	var n int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM endpoint_keys`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Errorf("failed creates inserted %d key rows", n)
	}

	// Owner success.
	k, err := st.CreateEndpointKey(context.Background(), alice, aEP.ID, bytes.Clone(plaintext), "h", "t", "note", true, 1)
	if err != nil {
		t.Fatalf("owner create key: %v", err)
	}
	if k.EndpointID != aEP.ID || k.DisplayHead != "h" || k.DisplayTail != "t" {
		t.Errorf("created key = %+v", k)
	}
}

// TestCreateEndpointKeyPersistsCiphertextAndDisplay asserts the row stores the
// envelope verbatim (never plaintext) plus the display fragments, and that the
// ciphertext round-trips through the codec.
func TestCreateEndpointKeyPersistsCiphertextAndDisplay(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "keypersist.db"))
	defer st.Close()
	uid := seedTestUser(t, st, "u", nil)
	ep := mustCreateTestEndpoint(t, st, uid, "https://example.com/v1/")

	plaintext := []byte("sk-repo-roundtrip-XYZW")
	k, err := st.CreateEndpointKey(context.Background(), uid, ep.ID, bytes.Clone(plaintext), "head", "tail", "my note", true, 1)
	if err != nil {
		t.Fatalf("create key: %v", err)
	}

	var stored, head, tail, note string
	if err := st.DB().QueryRow(`SELECT encrypted_secret, display_head, display_tail, note FROM endpoint_keys WHERE id=?`, k.ID).
		Scan(&stored, &head, &tail, &note); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if !strings.HasPrefix(stored, "nbsec:v2:aes-256-gcm:") {
		t.Fatal("encrypted_secret is not a contextual envelope")
	}
	if stored == string(plaintext) || strings.Contains(stored, string(plaintext)) {
		t.Fatal("encrypted_secret contains plaintext")
	}
	if head != "head" || tail != "tail" || note != "my note" {
		t.Errorf("display/note = (%q,%q,%q), want (head,tail,my note)", head, tail, note)
	}
	opened, err := st.secrets.OpenForContext(stored, testEndpointKeyContext(t, uid, ep, k.ID))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !bytes.Equal(opened, plaintext) {
		t.Fatalf("opened = %q, want %q", opened, plaintext)
	}
	clear(opened)
}

// TestListEndpointKeysExcludesCiphertext asserts the list path selects only
// metadata and display fragments: the returned EndpointKey values carry no
// ciphertext (the struct has no such field) and no plaintext.
func TestListEndpointKeysExcludesCiphertext(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "keylist.db"))
	defer st.Close()
	uid := seedTestUser(t, st, "u", nil)
	ep := mustCreateTestEndpoint(t, st, uid, "https://example.com/v1/")

	plaintext := []byte("sk-list-repo-12345678")
	if _, err := st.CreateEndpointKey(context.Background(), uid, ep.ID, bytes.Clone(plaintext), "h", "t", "note", true, 1); err != nil {
		t.Fatalf("create key: %v", err)
	}

	keys, err := st.ListEndpointKeys(context.Background(), uid, ep.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(keys) != 1 {
		t.Fatalf("got %d keys, want 1", len(keys))
	}
	// EndpointKey has no ciphertext field by construction; assert no field
	// carries the plaintext.
	for _, field := range []string{keys[0].DisplayHead, keys[0].DisplayTail, keys[0].Note} {
		if strings.Contains(field, string(plaintext)) {
			t.Errorf("listed key field %q contains plaintext", field)
		}
	}
}

// TestListEndpointKeysCrossUserEmpty asserts the repo-level contract: a
// cross-user list yields an empty slice (the service layer adds the
// not_found pre-check on top of this).
func TestListEndpointKeysCrossUserEmpty(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "keyxuser.db"))
	defer st.Close()
	alice := seedTestUser(t, st, "alice", nil)
	bob := seedTestUser(t, st, "bob", nil)
	aEP := mustCreateTestEndpoint(t, st, alice, "https://alice.example/v1/")

	if _, err := st.CreateEndpointKey(context.Background(), alice, aEP.ID, []byte("sk-x"), "h", "t", "", true, 1); err != nil {
		t.Fatalf("create key: %v", err)
	}

	bobKeys, err := st.ListEndpointKeys(context.Background(), bob, aEP.ID)
	if err != nil {
		t.Fatalf("bob list: %v", err)
	}
	if len(bobKeys) != 0 {
		t.Errorf("bob saw %d keys on alice endpoint, want 0", len(bobKeys))
	}
}

// TestListEnabledEndpointKeysFiltersDisabled asserts disabling a key removes it
// from the enabled candidate set immediately.
func TestListEnabledEndpointKeysFiltersDisabled(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "keyenabled.db"))
	defer st.Close()
	uid := seedTestUser(t, st, "u", nil)
	ep := mustCreateTestEndpoint(t, st, uid, "https://example.com/v1/")

	on, err := st.CreateEndpointKey(context.Background(), uid, ep.ID, []byte("sk-a"), "a", "a", "", true, 1)
	if err != nil {
		t.Fatalf("create on: %v", err)
	}
	off, err := st.CreateEndpointKey(context.Background(), uid, ep.ID, []byte("sk-b"), "b", "b", "", false, 1)
	if err != nil {
		t.Fatalf("create off: %v", err)
	}

	enabled, err := st.ListEnabledEndpointKeys(context.Background(), uid, ep.ID)
	if err != nil {
		t.Fatalf("list enabled: %v", err)
	}
	if len(enabled) != 1 || enabled[0].ID != on.ID {
		t.Errorf("enabled = %+v, want only the enabled key", enabled)
	}
	if _, err := st.UpdateEndpointKey(context.Background(), uid, ep.ID, on.ID, nil, boolPtrDB(false), 2); err != nil {
		t.Fatalf("disable on: %v", err)
	}
	enabled, err = st.ListEnabledEndpointKeys(context.Background(), uid, ep.ID)
	if err != nil {
		t.Fatalf("list enabled after disable: %v", err)
	}
	if len(enabled) != 0 {
		t.Errorf("after disabling, enabled = %+v, want empty", enabled)
	}
	_ = off
}

// TestUpdateEndpointKeyOwnershipAndEndpointScoped asserts the update is gated
// by user AND endpoint: cross-user and same-user wrong-endpoint paths are
// not_found, and the ciphertext is never mutated.
func TestUpdateEndpointKeyOwnershipAndEndpointScoped(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "keyupd.db"))
	defer st.Close()
	alice := seedTestUser(t, st, "alice", nil)
	bob := seedTestUser(t, st, "bob", nil)
	aEP := mustCreateTestEndpoint(t, st, alice, "https://alice.example/v1/")
	bEP := mustCreateTestEndpoint(t, st, bob, "https://bob.example/v1/")

	aKey, err := st.CreateEndpointKey(context.Background(), alice, aEP.ID, []byte("sk-alice"), "h", "t", "", true, 1)
	if err != nil {
		t.Fatalf("create alice key: %v", err)
	}

	// Cross-user update is not_found.
	if _, err := st.UpdateEndpointKey(context.Background(), bob, aEP.ID, aKey.ID, strPtrDB("x"), nil, 2); !errors.Is(err, ErrNotFound) {
		t.Errorf("bob update alice key: err=%v, want not_found", err)
	}
	// Same-user wrong-endpoint path is not_found.
	if _, err := st.UpdateEndpointKey(context.Background(), alice, bEP.ID, aKey.ID, strPtrDB("x"), nil, 2); !errors.Is(err, ErrNotFound) {
		t.Errorf("alice update own key via wrong endpoint path: err=%v, want not_found", err)
	}
	// Owner update via correct endpoint succeeds and does not touch ciphertext.
	var before string
	st.DB().QueryRow(`SELECT encrypted_secret FROM endpoint_keys WHERE id=?`, aKey.ID).Scan(&before)
	updated, err := st.UpdateEndpointKey(context.Background(), alice, aEP.ID, aKey.ID, strPtrDB("rotated"), boolPtrDB(false), 3)
	if err != nil {
		t.Fatalf("owner update: %v", err)
	}
	if updated.Note != "rotated" || updated.Enabled {
		t.Errorf("updated = %+v", updated)
	}
	var after string
	st.DB().QueryRow(`SELECT encrypted_secret FROM endpoint_keys WHERE id=?`, aKey.ID).Scan(&after)
	if before != after {
		t.Errorf("UpdateEndpointKey altered ciphertext: before=%q after=%q", before, after)
	}
}

// TestDeleteEndpointKeyOwnershipAndCascade asserts deletion is gated by user AND
// endpoint and cascades to fetched_models and model_bindings immediately.
func TestDeleteEndpointKeyOwnershipAndCascade(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "keydel.db"))
	defer st.Close()
	alice := seedTestUser(t, st, "alice", nil)
	bob := seedTestUser(t, st, "bob", nil)
	aEP := mustCreateTestEndpoint(t, st, alice, "https://alice.example/v1/")
	bEP := mustCreateTestEndpoint(t, st, bob, "https://bob.example/v1/")

	aKey, err := st.CreateEndpointKey(context.Background(), alice, aEP.ID, []byte("sk-alice"), "h", "t", "", true, 1)
	if err != nil {
		t.Fatalf("create alice key: %v", err)
	}
	// Seed a model and cache/binding rows for the key.
	res, err := st.DB().Exec(`INSERT INTO models (user_id, provider, model, full_name, route_strategy, created_at, updated_at) VALUES (?, 'openai', 'gpt', 'openai/gpt', 'ordered', 1, 1)`, alice)
	if err != nil {
		t.Fatalf("seed model: %v", err)
	}
	modelID, _ := res.LastInsertId()
	if _, err := st.DB().Exec(`INSERT INTO fetched_models (endpoint_key_id, upstream_model_id, provider, fetched_at, status) VALUES (?, 'gpt', 'openai', 1, 'ok')`, aKey.ID); err != nil {
		t.Fatalf("seed fetched_models: %v", err)
	}
	if _, err := st.DB().Exec(`INSERT INTO model_bindings (model_id, endpoint_key_id, upstream_model_id, ord, created_at) VALUES (?, ?, 'gpt', 0, 1)`, modelID, aKey.ID); err != nil {
		t.Fatalf("seed model_bindings: %v", err)
	}

	// Cross-user delete is not_found and changes nothing.
	if err := st.DeleteEndpointKey(context.Background(), bob, aEP.ID, aKey.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("bob delete alice key: err=%v, want not_found", err)
	}
	// Same-user wrong-endpoint path is not_found.
	if err := st.DeleteEndpointKey(context.Background(), alice, bEP.ID, aKey.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("alice delete own key via wrong endpoint path: err=%v, want not_found", err)
	}
	// Owner delete cascades.
	if err := st.DeleteEndpointKey(context.Background(), alice, aEP.ID, aKey.ID); err != nil {
		t.Fatalf("owner delete: %v", err)
	}
	for _, table := range []string{"endpoint_keys", "fetched_models", "model_bindings"} {
		var n int
		if err := st.DB().QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if n != 0 {
			t.Errorf("cascade left %d rows in %s, want 0", n, table)
		}
	}
	// Endpoint and model survive.
	var eps, models int
	st.DB().QueryRow(`SELECT COUNT(*) FROM endpoints WHERE id=?`, aEP.ID).Scan(&eps)
	st.DB().QueryRow(`SELECT COUNT(*) FROM models WHERE id=?`, modelID).Scan(&models)
	if eps != 1 || models != 1 {
		t.Errorf("delete key dropped endpoint(%d) or model(%d), want both retained", eps, models)
	}
}

// TestEndpointKeyQueriesAreParameterized asserts SQL-special characters in the
// note are stored verbatim and never interpreted.
func TestEndpointKeyQueriesAreParameterized(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "keysqli.db"))
	defer st.Close()
	uid := seedTestUser(t, st, "u", nil)
	ep := mustCreateTestEndpoint(t, st, uid, "https://example.com/v1/")

	injection := "'; DROP TABLE endpoint_keys; --"
	k, err := st.CreateEndpointKey(context.Background(), uid, ep.ID, []byte("sk"), "h", "t", injection, true, 1)
	if err != nil {
		t.Fatalf("create with injection note: %v", err)
	}
	if k.Note != injection {
		t.Errorf("injection note not stored verbatim: %q", k.Note)
	}
	var n int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM endpoint_keys`).Scan(&n); err != nil {
		t.Fatalf("endpoint_keys query failed (injection may have dropped it): %v", err)
	}
	if n != 1 {
		t.Errorf("endpoint_keys count = %d, want 1", n)
	}
}

// --- endpoint-key cap -------------------------------------------------------

// setTestEndpointKeyLimit upserts the default_endpoint_key_limit site_config
// row so a test can adjust the cap mid-run (verifying runtime application).
func setTestEndpointKeyLimit(t *testing.T, st *Store, raw string) {
	t.Helper()
	if _, err := st.DB().Exec(
		`INSERT INTO site_config (key, value, updated_at) VALUES ('default_endpoint_key_limit', ?, 1)
		 ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`, raw); err != nil {
		t.Fatalf("set endpoint key limit: %v", err)
	}
}

func secretFixture(label string) []byte {
	return []byte("sk-" + label)
}

// mustCreateTestEndpointKey creates a key on endpointID owned by userID and
// fails the test on any error. Used to fill a parent up to its cap.
func mustCreateTestEndpointKey(t *testing.T, st *Store, userID, endpointID int64, label string) EndpointKey {
	t.Helper()
	k, err := st.CreateEndpointKey(context.Background(), userID, endpointID, secretFixture(label), "h", "t", "", true, 1)
	if err != nil {
		t.Fatalf("CreateEndpointKey %s: %v", label, err)
	}
	return k
}

// TestCreateEndpointKeyCap covers the per-endpoint key-count cap: the default
// when unset, the cap-th create succeeds, the next is a *CapError wrapping
// ErrEndpointKeyCap with the resource name and exact effective cap, no extra
// row is written, a runtime site-config change takes effect immediately,
// counts do not cross endpoints, and a cross-user caller neither leaks nor
// bypasses the ownership guard.
func TestCreateEndpointKeyCap(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "keycap.db"))
	defer st.Close()
	alice := seedTestUser(t, st, "alice", nil)
	aEP := mustCreateTestEndpoint(t, st, alice, "https://alice.example/v1/")

	// Default cap when the site_config key is unset.
	cap, err := st.EndpointKeyCap(context.Background())
	if err != nil {
		t.Fatalf("EndpointKeyCap: %v", err)
	}
	if cap != DefaultEndpointKeyLimit {
		t.Errorf("unset cap = %d, want %d", cap, DefaultEndpointKeyLimit)
	}

	// A small cap is applied at runtime; fill to cap so the cap-th succeeds.
	setTestEndpointKeyLimit(t, st, "3")
	cap, _ = st.EndpointKeyCap(context.Background())
	if cap != 3 {
		t.Fatalf("adjusted cap = %d, want 3", cap)
	}
	for i := 0; i < cap; i++ {
		mustCreateTestEndpointKey(t, st, alice, aEP.ID, fmt.Sprintf("k%d", i))
	}
	count, err := st.CountEndpointKeys(context.Background(), alice, aEP.ID)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != cap {
		t.Fatalf("count = %d, want %d", count, cap)
	}

	// One over the cap is refused with a *CapError carrying the limit.
	_, err = st.CreateEndpointKey(context.Background(), alice, aEP.ID, secretFixture("extra"), "h", "t", "", true, 1)
	var capErr *CapError
	if !errors.As(err, &capErr) {
		t.Fatalf("over-cap err = %v, want *CapError", err)
	}
	if !errors.Is(err, ErrEndpointKeyCap) {
		t.Errorf("over-cap err does not match ErrEndpointKeyCap: %v", err)
	}
	if capErr.Resource != ResourceEndpointKey || capErr.Limit != cap {
		t.Errorf("capErr = %+v, want resource=%q limit=%d", capErr, ResourceEndpointKey, cap)
	}
	count, _ = st.CountEndpointKeys(context.Background(), alice, aEP.ID)
	if count != cap {
		t.Errorf("count after refused create = %d, want %d (no row written)", count, cap)
	}

	// Raising the cap at runtime lets another key in (no restart).
	setTestEndpointKeyLimit(t, st, "5")
	mustCreateTestEndpointKey(t, st, alice, aEP.ID, "more")
	count, _ = st.CountEndpointKeys(context.Background(), alice, aEP.ID)
	if count != cap+1 {
		t.Errorf("count after cap raise = %d, want %d", count, cap+1)
	}

	// Counts do not cross endpoints: a second endpoint starts at 0.
	aEP2 := mustCreateTestEndpoint(t, st, alice, "https://alice.example/v2/")
	if c2, _ := st.CountEndpointKeys(context.Background(), alice, aEP2.ID); c2 != 0 {
		t.Errorf("second endpoint count = %d, want 0", c2)
	}
	mustCreateTestEndpointKey(t, st, alice, aEP2.ID, "other-ep")
	if c2, _ := st.CountEndpointKeys(context.Background(), alice, aEP2.ID); c2 != 1 {
		t.Errorf("second endpoint count after one add = %d, want 1", c2)
	}

	// A cross-user caller counts 0 on alice's endpoint (no count leak) and the
	// ownership guard still refuses the create as ErrNotFound.
	bob := seedTestUser(t, st, "bob", nil)
	if cross, _ := st.CountEndpointKeys(context.Background(), bob, aEP.ID); cross != 0 {
		t.Errorf("bob counting alice endpoint = %d, want 0", cross)
	}
	if _, err := st.CreateEndpointKey(context.Background(), bob, aEP.ID, secretFixture("x"), "h", "t", "", true, 1); !errors.Is(err, ErrNotFound) {
		t.Errorf("bob create on alice endpoint: err=%v, want ErrNotFound", err)
	}
}

// TestCreateEndpointKeyCapRejectsInvalidSiteConfig asserts a malformed cap
// surfaces as ErrInvalidSiteConfig (fail closed) and never writes a key.
func TestCreateEndpointKeyCapRejectsInvalidSiteConfig(t *testing.T) {
	for _, raw := range []string{"not-a-number", "-3", "  "} {
		raw := raw
		t.Run(raw, func(t *testing.T) {
			st := openTestStore(t, filepath.Join(t.TempDir(), "keycapcfg.db"))
			defer st.Close()
			setTestEndpointKeyLimit(t, st, raw)
			alice := seedTestUser(t, st, "alice", nil)
			aEP := mustCreateTestEndpoint(t, st, alice, "https://alice.example/v1/")
			_, err := st.CreateEndpointKey(context.Background(), alice, aEP.ID, secretFixture("x"), "h", "t", "", true, 1)
			if !errors.Is(err, ErrInvalidSiteConfig) {
				t.Errorf("CreateEndpointKey with global %q: err=%v, want ErrInvalidSiteConfig", raw, err)
			}
			count, _ := st.CountEndpointKeys(context.Background(), alice, aEP.ID)
			if count != 0 {
				t.Errorf("invalid site_config still produced %d keys", count)
			}
		})
	}
}

// TestCreateEndpointKeyAtomicCapUnderConcurrency hammers CreateEndpointKey
// from many goroutines against a small cap; the count-then-insert inside one
// transaction must never let the cap be breached. Run under -race.
func TestCreateEndpointKeyAtomicCapUnderConcurrency(t *testing.T) {
	const cap = 8
	const workers = 64
	st := openTestStore(t, filepath.Join(t.TempDir(), "keycaprace.db"))
	defer st.Close()
	setTestEndpointKeyLimit(t, st, strconv.Itoa(cap))
	alice := seedTestUser(t, st, "alice", nil)
	aEP := mustCreateTestEndpoint(t, st, alice, "https://alice.example/v1/")

	// Give each worker an independently owned plaintext slice because the
	// repository consumes and clears it.
	plaintexts := make([][]byte, workers)
	for i := 0; i < workers; i++ {
		plaintexts[i] = secretFixture(fmt.Sprintf("w%d", i))
	}

	var wg sync.WaitGroup
	var succ, capErr atomic.Int64
	start := make(chan struct{})
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		i := i
		go func() {
			defer wg.Done()
			<-start
			_, err := st.CreateEndpointKey(context.Background(), alice, aEP.ID, plaintexts[i], "h", "t", "", true, 1)
			switch {
			case err == nil:
				succ.Add(1)
			case errors.Is(err, ErrEndpointKeyCap):
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
	count, err := st.CountEndpointKeys(context.Background(), alice, aEP.ID)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != cap {
		t.Errorf("final count = %d, want %d", count, cap)
	}
}
