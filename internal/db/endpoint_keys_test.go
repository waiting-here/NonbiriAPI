package db

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// TestCreateEndpointKeyOwnershipAtomicInsert asserts the INSERT...SELECT
// ownership guard: a key is created only when the endpoint exists and belongs
// to the caller; otherwise ErrNotFound and zero rows (no read-then-write race).
func TestCreateEndpointKeyOwnershipAtomicInsert(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "keyown.db"))
	defer st.Close()
	alice := seedTestUser(t, st, "alice", nil)
	bob := seedTestUser(t, st, "bob", nil)
	aEP := mustCreateTestEndpoint(t, st, alice, "https://alice.example/v1/")

	ciphertext, err := st.secrets.Seal([]byte("sk-test"))
	if err != nil {
		t.Fatalf("seal: %v", err)
	}

	// Missing endpoint id.
	if _, err := st.CreateEndpointKey(context.Background(), alice, aEP.ID+999, ciphertext, "h", "t", "note", true, 1); !errors.Is(err, ErrNotFound) {
		t.Errorf("create key on missing endpoint: err=%v, want not_found", err)
	}
	// Cross-user endpoint.
	if _, err := st.CreateEndpointKey(context.Background(), bob, aEP.ID, ciphertext, "h", "t", "note", true, 1); !errors.Is(err, ErrNotFound) {
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
	k, err := st.CreateEndpointKey(context.Background(), alice, aEP.ID, ciphertext, "h", "t", "note", true, 1)
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
	ciphertext, err := st.secrets.Seal(plaintext)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	k, err := st.CreateEndpointKey(context.Background(), uid, ep.ID, ciphertext, "head", "tail", "my note", true, 1)
	if err != nil {
		t.Fatalf("create key: %v", err)
	}

	var stored, head, tail, note string
	if err := st.DB().QueryRow(`SELECT encrypted_secret, display_head, display_tail, note FROM endpoint_keys WHERE id=?`, k.ID).
		Scan(&stored, &head, &tail, &note); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if stored != ciphertext {
		t.Errorf("encrypted_secret not stored verbatim: got %q want %q", stored, ciphertext)
	}
	if stored == string(plaintext) || strings.Contains(stored, string(plaintext)) {
		t.Fatal("encrypted_secret contains plaintext")
	}
	if head != "head" || tail != "tail" || note != "my note" {
		t.Errorf("display/note = (%q,%q,%q), want (head,tail,my note)", head, tail, note)
	}
	opened, err := st.secrets.Open(stored)
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
	ciphertext, err := st.secrets.Seal(plaintext)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if _, err := st.CreateEndpointKey(context.Background(), uid, ep.ID, ciphertext, "h", "t", "note", true, 1); err != nil {
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

	ciphertext, err := st.secrets.Seal([]byte("sk-x"))
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if _, err := st.CreateEndpointKey(context.Background(), alice, aEP.ID, ciphertext, "h", "t", "", true, 1); err != nil {
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

	ct, _ := st.secrets.Seal([]byte("sk-a"))
	on, err := st.CreateEndpointKey(context.Background(), uid, ep.ID, ct, "a", "a", "", true, 1)
	if err != nil {
		t.Fatalf("create on: %v", err)
	}
	ct2, _ := st.secrets.Seal([]byte("sk-b"))
	off, err := st.CreateEndpointKey(context.Background(), uid, ep.ID, ct2, "b", "b", "", false, 1)
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

	ct, _ := st.secrets.Seal([]byte("sk-alice"))
	aKey, err := st.CreateEndpointKey(context.Background(), alice, aEP.ID, ct, "h", "t", "", true, 1)
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

	ct, _ := st.secrets.Seal([]byte("sk-alice"))
	aKey, err := st.CreateEndpointKey(context.Background(), alice, aEP.ID, ct, "h", "t", "", true, 1)
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

	ct, _ := st.secrets.Seal([]byte("sk"))
	injection := "'; DROP TABLE endpoint_keys; --"
	k, err := st.CreateEndpointKey(context.Background(), uid, ep.ID, ct, "h", "t", injection, true, 1)
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
