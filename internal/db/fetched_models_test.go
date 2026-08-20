package db

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// helper: create a key (enabled by default) for userID on ep with the given
// plaintext, returning the key id and its stored ciphertext.
func mustCreateTestKey(t *testing.T, st *Store, userID, endpointID int64, plaintext string, enabled bool) (int64, string) {
	t.Helper()
	k, err := st.CreateEndpointKey(context.Background(), userID, endpointID, []byte(plaintext), "head", "tail", "note", enabled, 1)
	if err != nil {
		t.Fatalf("create key: %v", err)
	}
	ciphertext, err := st.GetEndpointKeyCiphertext(context.Background(), userID, endpointID, k.ID)
	if err != nil {
		t.Fatalf("read stored ciphertext: %v", err)
	}
	return k.ID, ciphertext
}

// TestGetEndpointKeyFetchStateOwnership asserts the three-id ownership join:
// missing key, wrong endpoint, and cross-user paths are indistinguishable
// ErrNotFound, and the projection carries the enabled flags.
func TestGetEndpointKeyFetchStateOwnership(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "fetchstate.db"))
	defer st.Close()
	alice := seedTestUser(t, st, "alice", nil)
	bob := seedTestUser(t, st, "bob", nil)
	ep := mustCreateTestEndpoint(t, st, alice, "https://alice.example/v1/")
	keyID, _ := mustCreateTestKey(t, st, alice, ep.ID, "sk-state", true)

	state, err := st.GetEndpointKeyFetchState(context.Background(), alice, ep.ID, keyID)
	if err != nil {
		t.Fatalf("owner state: %v", err)
	}
	if state.ConnectorType != "openai-compatible" || state.BaseURL != "https://alice.example/v1/" ||
		!state.EndpointEnabled || !state.KeyEnabled {
		t.Errorf("state = %+v", state)
	}

	for name, args := range map[string][3]int64{
		"missing key":    {alice, ep.ID, keyID + 999},
		"wrong endpoint": {alice, ep.ID + 999, keyID},
		"cross user":     {bob, ep.ID, keyID},
		"cross user+ep":  {bob, ep.ID + 999, keyID},
		"zero key":       {alice, ep.ID, 0},
		"zero endpoint":  {alice, 0, keyID},
	} {
		_, err := st.GetEndpointKeyFetchState(context.Background(), args[0], args[1], args[2])
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("%s: err=%v, want not_found", name, err)
		}
	}

	// Disabled endpoint/key reflected in the projection.
	if _, err := st.UpdateEndpointKey(context.Background(), alice, ep.ID, keyID, nil, boolPtr(false), 2); err != nil {
		t.Fatalf("disable key: %v", err)
	}
	state, err = st.GetEndpointKeyFetchState(context.Background(), alice, ep.ID, keyID)
	if err != nil {
		t.Fatalf("state after disable: %v", err)
	}
	if state.KeyEnabled {
		t.Errorf("key still enabled after disable")
	}
	if !state.EndpointEnabled {
		t.Errorf("endpoint unexpectedly disabled")
	}
}

// TestGetEndpointKeyCiphertextOwnership asserts the ciphertext accessor is
// ownership-scoped and returns the stored envelope verbatim.
func TestGetEndpointKeyCiphertextOwnership(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "ciph.db"))
	defer st.Close()
	alice := seedTestUser(t, st, "alice", nil)
	bob := seedTestUser(t, st, "bob", nil)
	ep := mustCreateTestEndpoint(t, st, alice, "https://alice.example/v1/")
	keyID, ciphertext := mustCreateTestKey(t, st, alice, ep.ID, "sk-ciphertext-access", true)

	got, err := st.GetEndpointKeyCiphertext(context.Background(), alice, ep.ID, keyID)
	if err != nil {
		t.Fatalf("owner ciphertext: %v", err)
	}
	if got != ciphertext {
		t.Errorf("ciphertext = %q, want stored envelope verbatim", got)
	}
	// The envelope decrypts to the original plaintext (and is clearable).
	pt, err := st.secrets.OpenForContext(got, testEndpointKeyContext(t, alice, ep, keyID))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if !bytes.Equal(pt, []byte("sk-ciphertext-access")) {
		t.Errorf("plaintext = %q", pt)
	}
	clear(pt)

	for name, args := range map[string][3]int64{
		"missing key":    {alice, ep.ID, keyID + 999},
		"wrong endpoint": {alice, ep.ID + 999, keyID},
		"cross user":     {bob, ep.ID, keyID},
	} {
		if _, err := st.GetEndpointKeyCiphertext(context.Background(), args[0], args[1], args[2]); !errors.Is(err, ErrNotFound) {
			t.Errorf("%s: err=%v, want not_found", name, err)
		}
	}
}

// TestReplaceFetchedModelsAtomicSwap asserts a successful fetch replaces the
// combo's rows atomically and clears the endpoint failure flag, without
// touching sibling keys or other users.
func TestReplaceFetchedModelsAtomicSwap(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "replace.db"))
	defer st.Close()
	alice := seedTestUser(t, st, "alice", nil)
	bob := seedTestUser(t, st, "bob", nil)
	ep := mustCreateTestEndpoint(t, st, alice, "https://alice.example/v1/")
	keyA, _ := mustCreateTestKey(t, st, alice, ep.ID, "sk-a", true)
	keyB, _ := mustCreateTestKey(t, st, alice, ep.ID, "sk-b", true)
	bobEp := mustCreateTestEndpoint(t, st, bob, "https://bob.example/v1/")
	bobKey, _ := mustCreateTestKey(t, st, bob, bobEp.ID, "sk-bob", true)

	seed := func(kid int64, names ...string) []FetchedModel {
		var out []FetchedModel
		for _, n := range names {
			out = append(out, FetchedModel{EndpointKeyID: kid, UpstreamModelID: n, Provider: "system", Status: "ok"})
		}
		return out
	}

	// Seed keyA and mark the endpoint failed (simulating a prior failure).
	if err := st.FailFetch(context.Background(), alice, ep.ID, keyA, "model_fetch_failed", "model fetch failed", "1", 100); err != nil {
		t.Fatalf("seed fail: %v", err)
	}
	epRow, err := st.GetEndpoint(context.Background(), alice, ep.ID)
	if err != nil || !epRow.ModelFetchFailed || epRow.ModelFetchFailedAt != 100 {
		t.Fatalf("seed flag: ep=%+v err=%v", epRow, err)
	}

	// Replace keyA; keyB stays empty; bob untouched.
	if err := st.ReplaceFetchedModels(context.Background(), alice, ep.ID, keyA, seed(keyA, "gpt-4o", "gpt-4o-mini"), 200); err != nil {
		t.Fatalf("replace: %v", err)
	}
	listA, err := st.ListFetchedModels(context.Background(), alice, ep.ID, keyA)
	if err != nil {
		t.Fatalf("list keyA: %v", err)
	}
	if len(listA) != 2 || listA[0].UpstreamModelID != "gpt-4o" || listA[1].UpstreamModelID != "gpt-4o-mini" {
		t.Errorf("keyA rows = %+v", listA)
	}
	for _, m := range listA {
		if m.EndpointKeyID != keyA || m.Provider != "system" || m.Status != "ok" || m.FetchedAt != 200 {
			t.Errorf("keyA row = %+v", m)
		}
	}
	listB, err := st.ListFetchedModels(context.Background(), alice, ep.ID, keyB)
	if err != nil || len(listB) != 0 {
		t.Errorf("keyB rows = %+v err=%v, want empty", listB, err)
	}
	listBob, err := st.ListFetchedModels(context.Background(), bob, bobEp.ID, bobKey)
	if err != nil || len(listBob) != 0 {
		t.Errorf("bob rows = %+v err=%v, want empty", listBob, err)
	}
	epRow, err = st.GetEndpoint(context.Background(), alice, ep.ID)
	if err != nil || epRow.ModelFetchFailed || epRow.ModelFetchFailedAt != 0 {
		t.Errorf("flag not cleared: ep=%+v err=%v", epRow, err)
	}

	// Second replace swaps atomically.
	if err := st.ReplaceFetchedModels(context.Background(), alice, ep.ID, keyA, seed(keyA, "o1"), 300); err != nil {
		t.Fatalf("replace again: %v", err)
	}
	listA, err = st.ListFetchedModels(context.Background(), alice, ep.ID, keyA)
	if err != nil || len(listA) != 1 || listA[0].UpstreamModelID != "o1" || listA[0].FetchedAt != 300 {
		t.Errorf("keyA after second replace = %+v err=%v", listA, err)
	}
}

// TestReplaceFetchedModelsOwnership asserts a cross-user/wrong-endpoint
// replace writes nothing and yields not_found.
func TestReplaceFetchedModelsOwnership(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "replaceown.db"))
	defer st.Close()
	alice := seedTestUser(t, st, "alice", nil)
	bob := seedTestUser(t, st, "bob", nil)
	ep := mustCreateTestEndpoint(t, st, alice, "https://alice.example/v1/")
	keyID, _ := mustCreateTestKey(t, st, alice, ep.ID, "sk-a", true)

	seed := []FetchedModel{{EndpointKeyID: keyID, UpstreamModelID: "gpt-4o", Provider: "system", Status: "ok"}}
	if err := st.ReplaceFetchedModels(context.Background(), bob, ep.ID, keyID, seed, 1); !errors.Is(err, ErrNotFound) {
		t.Errorf("cross-user replace: err=%v, want not_found", err)
	}
	if err := st.ReplaceFetchedModels(context.Background(), alice, ep.ID+999, keyID, seed, 1); !errors.Is(err, ErrNotFound) {
		t.Errorf("wrong-endpoint replace: err=%v, want not_found", err)
	}
	var n int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM fetched_models`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Errorf("failed replaces wrote %d rows", n)
	}
}

// TestFailFetchClearsCacheFlagsAndIssues asserts the atomic failure record:
// combo cache cleared, endpoint flagged with timestamp, bounded issue row
// written for the owning user only.
func TestFailFetchClearsCacheFlagsAndIssues(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "fail.db"))
	defer st.Close()
	alice := seedTestUser(t, st, "alice", nil)
	bob := seedTestUser(t, st, "bob", nil)
	ep := mustCreateTestEndpoint(t, st, alice, "https://alice.example/v1/")
	keyA, _ := mustCreateTestKey(t, st, alice, ep.ID, "sk-a", true)
	keyB, _ := mustCreateTestKey(t, st, alice, ep.ID, "sk-b", true)

	seed := []FetchedModel{{EndpointKeyID: keyA, UpstreamModelID: "gpt-4o", Provider: "system", Status: "ok"}}
	if err := st.ReplaceFetchedModels(context.Background(), alice, ep.ID, keyA, seed, 1); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	message := "model fetch failed: upstream returned status 500"
	if err := st.FailFetch(context.Background(), alice, ep.ID, keyA, "model_fetch_failed", message, "7", 50); err != nil {
		t.Fatalf("fail fetch: %v", err)
	}

	listA, err := st.ListFetchedModels(context.Background(), alice, ep.ID, keyA)
	if err != nil || len(listA) != 0 {
		t.Errorf("keyA cache after fail = %+v err=%v, want empty", listA, err)
	}
	epRow, err := st.GetEndpoint(context.Background(), alice, ep.ID)
	if err != nil || !epRow.ModelFetchFailed || epRow.ModelFetchFailedAt != 50 {
		t.Errorf("flag after fail: ep=%+v err=%v", epRow, err)
	}

	var kind, msg, ref string
	var issueUser, createdAt int64
	if err := st.DB().QueryRow(`SELECT user_id, kind, message, ref, created_at FROM user_issues`).
		Scan(&issueUser, &kind, &msg, &ref, &createdAt); err != nil {
		t.Fatalf("read issue: %v", err)
	}
	if issueUser != alice || kind != "model_fetch_failed" || msg != message || ref != "7" || createdAt != 50 {
		t.Errorf("issue = (%d, %q, %q, %q, %d)", issueUser, kind, msg, ref, createdAt)
	}

	// Sibling key cache and bob's state are untouched.
	listB, err := st.ListFetchedModels(context.Background(), alice, ep.ID, keyB)
	if err != nil || len(listB) != 0 {
		t.Errorf("keyB = %+v err=%v", listB, err)
	}
	bobEp := mustCreateTestEndpoint(t, st, bob, "https://bob.example/v1/")
	if _, err := st.GetEndpoint(context.Background(), bob, bobEp.ID); err != nil || endpointFlagged(t, st, bob, bobEp.ID) {
		t.Errorf("bob endpoint unexpectedly flagged")
	}

	// Success restores: replace clears flag and cache is usable again.
	if err := st.ReplaceFetchedModels(context.Background(), alice, ep.ID, keyA, seed, 60); err != nil {
		t.Fatalf("restore: %v", err)
	}
	epRow, err = st.GetEndpoint(context.Background(), alice, ep.ID)
	if err != nil || epRow.ModelFetchFailed || epRow.ModelFetchFailedAt != 0 {
		t.Errorf("flag after restore: ep=%+v err=%v", epRow, err)
	}
}

// TestFailFetchOwnershipAndLateCallback asserts a cross-user fail writes
// nothing, and a fail against a deleted user no-ops atomically (no issue row,
// no crash) instead of failing the transaction.
func TestFailFetchOwnershipAndLateCallback(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "failown.db"))
	defer st.Close()
	alice := seedTestUser(t, st, "alice", nil)
	bob := seedTestUser(t, st, "bob", nil)
	ep := mustCreateTestEndpoint(t, st, alice, "https://alice.example/v1/")
	keyID, _ := mustCreateTestKey(t, st, alice, ep.ID, "sk-a", true)

	// Cross-user: not_found, no flag, no issue, no cache change.
	if err := st.FailFetch(context.Background(), bob, ep.ID, keyID, "model_fetch_failed", "x", "1", 1); !errors.Is(err, ErrNotFound) {
		t.Errorf("cross-user fail: err=%v, want not_found", err)
	}
	var n int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM user_issues`).Scan(&n); err != nil || n != 0 {
		t.Errorf("cross-user fail wrote %d issues (err=%v)", n, err)
	}
	if endpointFlagged(t, st, alice, ep.ID) {
		t.Errorf("cross-user fail flagged alice's endpoint")
	}

	// Deleted key: a late failure for a removed combo must not flag the
	// surviving endpoint or create an orphan issue.
	lateKey, _ := mustCreateTestKey(t, st, alice, ep.ID, "sk-late", true)
	if _, err := st.DB().Exec(`DELETE FROM endpoint_keys WHERE id=?`, lateKey); err != nil {
		t.Fatalf("delete key: %v", err)
	}
	if err := st.FailFetch(context.Background(), alice, ep.ID, lateKey, "model_fetch_failed", "late key", "1", 2); !errors.Is(err, ErrNotFound) {
		t.Errorf("deleted-key fail: err=%v, want not_found", err)
	}
	if endpointFlagged(t, st, alice, ep.ID) {
		t.Errorf("deleted-key fail flagged alice's endpoint")
	}
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM user_issues`).Scan(&n); err != nil || n != 0 {
		t.Errorf("deleted-key fail wrote %d issues (err=%v)", n, err)
	}

	// Deleted user: the endpoint cascade removes the combo, so the fail is a
	// no-op ErrNotFound and no issue row is written (atomic suppression).
	if _, err := st.DB().Exec(`DELETE FROM users WHERE id=?`, alice); err != nil {
		t.Fatalf("delete user: %v", err)
	}
	if err := st.FailFetch(context.Background(), alice, ep.ID, keyID, "model_fetch_failed", "late", "1", 2); !errors.Is(err, ErrNotFound) {
		t.Errorf("late fail: err=%v, want not_found", err)
	}
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM user_issues`).Scan(&n); err != nil || n != 0 {
		t.Errorf("late fail wrote %d issues (err=%v)", n, err)
	}
}

// TestListFetchedModelsOrderingAndEmpty asserts ordering by upstream_model_id
// and the empty-vs-notfound distinction.
func TestListFetchedModelsOrderingAndEmpty(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "list.db"))
	defer st.Close()
	alice := seedTestUser(t, st, "alice", nil)
	ep := mustCreateTestEndpoint(t, st, alice, "https://alice.example/v1/")
	keyID, _ := mustCreateTestKey(t, st, alice, ep.ID, "sk-a", true)

	empty, err := st.ListFetchedModels(context.Background(), alice, ep.ID, keyID)
	if err != nil || len(empty) != 0 {
		t.Errorf("empty combo = %+v err=%v, want empty slice", empty, err)
	}
	// "b" sorts before "a" if order were wrong: exercise the ORDER BY.
	seed := []FetchedModel{
		{EndpointKeyID: keyID, UpstreamModelID: "zeta", Provider: "p1", Status: "ok"},
		{EndpointKeyID: keyID, UpstreamModelID: "alpha", Provider: "p2", Status: "ok"},
	}
	if err := st.ReplaceFetchedModels(context.Background(), alice, ep.ID, keyID, seed, 1); err != nil {
		t.Fatalf("seed: %v", err)
	}
	list, err := st.ListFetchedModels(context.Background(), alice, ep.ID, keyID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 || list[0].UpstreamModelID != "alpha" || list[1].UpstreamModelID != "zeta" {
		t.Errorf("list order = %+v", list)
	}
	if _, err := st.ListFetchedModels(context.Background(), alice, ep.ID+9, keyID); !errors.Is(err, ErrNotFound) {
		t.Errorf("wrong endpoint list: err=%v, want not_found", err)
	}
}

// endpointFlagged reports whether the endpoint currently carries the
// fetch-failed flag; it is a tiny test helper.
func endpointFlagged(t *testing.T, st *Store, userID, endpointID int64) bool {
	t.Helper()
	ep, err := st.GetEndpoint(context.Background(), userID, endpointID)
	if err != nil {
		t.Fatalf("get endpoint: %v", err)
	}
	return ep.ModelFetchFailed
}

// boolPtr is a tiny helper for pointer-to-bool arguments.
func boolPtr(v bool) *bool { return &v }

// TestFailFetchMessageIsBounded documents the final persistence boundary:
// issue text is normalized and bounded even when a future caller bypasses the
// fetch rail's tighter diagnostic limit.
func TestFailFetchMessageIsBounded(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "msg.db"))
	defer st.Close()
	alice := seedTestUser(t, st, "alice", nil)
	ep := mustCreateTestEndpoint(t, st, alice, "https://alice.example/v1/")
	keyID, _ := mustCreateTestKey(t, st, alice, ep.ID, "sk-a", true)

	long := strings.Repeat("x", 4096) + "\nforged"
	if err := st.FailFetch(context.Background(), alice, ep.ID, keyID, "model_fetch_failed", long, "1", 1); err != nil {
		t.Fatalf("fail: %v", err)
	}
	var msg string
	if err := st.DB().QueryRow(`SELECT message FROM user_issues`).Scan(&msg); err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(msg) > 1024 || strings.ContainsAny(msg, "\r\n\t") {
		t.Errorf("message was not bounded/sanitized: len=%d value=%q", len(msg), msg)
	}
}
