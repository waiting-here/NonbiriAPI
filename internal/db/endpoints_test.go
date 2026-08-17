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

// seedTestUser inserts a users row and returns its id. A non-nil endpointLimit
// sets the per-user cap override; nil leaves it at the global default.
func seedTestUser(t *testing.T, st *Store, discordID string, endpointLimit *int) int64 {
	t.Helper()
	var limitArg any
	if endpointLimit != nil {
		limitArg = *endpointLimit
	}
	res, err := st.DB().Exec(
		`INSERT INTO users (discord_id, username, endpoint_limit, created_at, updated_at) VALUES (?, ?, ?, 1, 1)`,
		discordID, "tester", limitArg)
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("seed user last insert id: %v", err)
	}
	return id
}

func setTestGlobalLimit(t *testing.T, st *Store, raw string) {
	t.Helper()
	if _, err := st.DB().Exec(
		`INSERT INTO site_config (key, value, updated_at) VALUES ('default_endpoint_limit', ?, 1)`, raw); err != nil {
		t.Fatalf("set global limit: %v", err)
	}
}

func mustCreateTestEndpoint(t *testing.T, st *Store, userID int64, baseURL string) Endpoint {
	t.Helper()
	ep, err := st.CreateEndpoint(context.Background(), userID, "openai-compatible", baseURL, "", true, 1)
	if err != nil {
		t.Fatalf("CreateEndpoint: %v", err)
	}
	return ep
}

// TestCreateEndpointOwnershipInSQL asserts every read/write path filters by
// user_id in SQL: a cross-user id is not_found and never returns another user's
// row.
func TestCreateEndpointOwnershipInSQL(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "own.db"))
	defer st.Close()
	alice := seedTestUser(t, st, "alice", nil)
	bob := seedTestUser(t, st, "bob", nil)

	aEP := mustCreateTestEndpoint(t, st, alice, "https://alice.example/v1/")
	bEP := mustCreateTestEndpoint(t, st, bob, "https://bob.example/v1/")

	// Cross-user get is not_found and does not return bob's row.
	if _, err := st.GetEndpoint(context.Background(), bob, aEP.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("bob get alice endpoint: err=%v, want not_found", err)
	}
	got, err := st.GetEndpoint(context.Background(), alice, bEP.ID)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("alice get bob endpoint: err=%v, want not_found (got %+v)", err, got)
	}

	// List is scoped: alice sees only her endpoint.
	aList, err := st.ListEndpoints(context.Background(), alice)
	if err != nil {
		t.Fatalf("alice list: %v", err)
	}
	if len(aList) != 1 || aList[0].ID != aEP.ID || aList[0].UserID != alice {
		t.Errorf("alice list = %+v, want only her endpoint", aList)
	}

	// Cross-user update is not_found and does not mutate alice's row.
	if _, err := st.UpdateEndpoint(context.Background(), bob, aEP.ID, strPtrDB("https://bob.example/v2/"), nil, nil, 2); !errors.Is(err, ErrNotFound) {
		t.Errorf("bob update alice endpoint: err=%v, want not_found", err)
	}
	still, err := st.GetEndpoint(context.Background(), alice, aEP.ID)
	if err != nil {
		t.Fatalf("alice re-get: %v", err)
	}
	if still.BaseURL != aEP.BaseURL {
		t.Errorf("alice endpoint base_url mutated by cross-user update: %q", still.BaseURL)
	}

	// Cross-user delete is not_found and does not drop alice's row.
	if err := st.DeleteEndpoint(context.Background(), bob, aEP.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("bob delete alice endpoint: err=%v, want not_found", err)
	}
	if _, err := st.GetEndpoint(context.Background(), alice, aEP.ID); err != nil {
		t.Errorf("alice endpoint disappeared after bob's failed delete: %v", err)
	}
}

// TestCreateEndpointSameBaseURLAllowed asserts the schema imposes no unique
// constraint on (user_id, base_url): several endpoints may share a canonical
// base_url.
func TestCreateEndpointSameBaseURLAllowed(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "sameurl.db"))
	defer st.Close()
	uid := seedTestUser(t, st, "u", nil)
	const url = "https://example.com/v1/"
	for i := 0; i < 3; i++ {
		mustCreateTestEndpoint(t, st, uid, url)
	}
	eps, err := st.ListEndpoints(context.Background(), uid)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(eps) != 3 {
		t.Fatalf("got %d endpoints sharing base_url, want 3", len(eps))
	}
}

// TestEndpointCapReadsMinOfGlobalAndUser covers the cap computation directly:
// unset global falls back to DefaultEndpointLimit; the effective cap is the min
// of the global default and a non-negative per-user override.
func TestEndpointCapReadsMinOfGlobalAndUser(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "cap.db"))
	defer st.Close()
	uid := seedTestUser(t, st, "u", nil)

	// Unset global -> default.
	cap, err := st.EndpointCap(context.Background(), uid)
	if err != nil {
		t.Fatalf("cap unset: %v", err)
	}
	if cap != DefaultEndpointLimit {
		t.Errorf("unset cap = %d, want %d", cap, DefaultEndpointLimit)
	}

	// Global override.
	setTestGlobalLimit(t, st, "4")
	cap, err = st.EndpointCap(context.Background(), uid)
	if err != nil {
		t.Fatalf("cap global: %v", err)
	}
	if cap != 4 {
		t.Errorf("global cap = %d, want 4", cap)
	}

	// Per-user override below global wins.
	uid2 := seedTestUser(t, st, "u2", intPtrDB(2))
	cap, err = st.EndpointCap(context.Background(), uid2)
	if err != nil {
		t.Fatalf("cap user2: %v", err)
	}
	if cap != 2 {
		t.Errorf("user override cap = %d, want 2", cap)
	}

	// Per-user override above global is clamped to global.
	uid3 := seedTestUser(t, st, "u3", intPtrDB(10))
	cap, err = st.EndpointCap(context.Background(), uid3)
	if err != nil {
		t.Fatalf("cap user3: %v", err)
	}
	if cap != 4 {
		t.Errorf("user override above global cap = %d, want 4 (global)", cap)
	}

	// Zero override blocks all endpoints.
	uid4 := seedTestUser(t, st, "u4", intPtrDB(0))
	cap, err = st.EndpointCap(context.Background(), uid4)
	if err != nil {
		t.Fatalf("cap user4: %v", err)
	}
	if cap != 0 {
		t.Errorf("zero override cap = %d, want 0", cap)
	}

	// Negative override is treated as unset (global default).
	uid5 := seedTestUser(t, st, "u5", intPtrDB(-1))
	cap, err = st.EndpointCap(context.Background(), uid5)
	if err != nil {
		t.Fatalf("cap user5: %v", err)
	}
	if cap != 4 {
		t.Errorf("negative override cap = %d, want 4 (global, negative ignored)", cap)
	}
}

// TestEndpointCapRejectsInvalidSiteConfig asserts a malformed global default
// surfaces as ErrInvalidSiteConfig (and never silently allows unbounded
// creation).
func TestEndpointCapRejectsInvalidSiteConfig(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "badcfg.db"))
	defer st.Close()
	uid := seedTestUser(t, st, "u", nil)
	for _, raw := range []string{"not-a-number", "-5", "  "} {
		t.Run(raw, func(t *testing.T) {
			// Each subtest needs its own row since site_config key is PK.
			st.DB().Exec(`DELETE FROM site_config WHERE key='default_endpoint_limit'`)
			setTestGlobalLimit(t, st, raw)
			_, err := st.CreateEndpoint(context.Background(), uid, "openai-compatible", "https://example.com/v1/", "", true, 1)
			if !errors.Is(err, ErrInvalidSiteConfig) {
				t.Errorf("CreateEndpoint with global %q: err=%v, want ErrInvalidSiteConfig", raw, err)
			}
		})
	}
}

// TestCreateEndpointAtomicCapUnderConcurrency hammers CreateEndpoint from many
// goroutines against a small cap; the count-then-insert inside one transaction
// must never let the cap be breached. Run under -race.
func TestCreateEndpointAtomicCapUnderConcurrency(t *testing.T) {
	const cap = 8
	const workers = 64
	st := openTestStore(t, filepath.Join(t.TempDir(), "race.db"))
	defer st.Close()
	setTestGlobalLimit(t, st, strconv.Itoa(cap))
	uid := seedTestUser(t, st, "u", nil)

	var wg sync.WaitGroup
	var succ, capErr atomic.Int64
	start := make(chan struct{})
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		i := i
		go func() {
			defer wg.Done()
			<-start
			_, err := st.CreateEndpoint(context.Background(), uid, "openai-compatible",
				fmt.Sprintf("https://example.com/w%d/", i), "", true, 1)
			switch {
			case err == nil:
				succ.Add(1)
			case errors.Is(err, ErrEndpointCap):
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
	count, err := st.CountEndpoints(context.Background(), uid)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != cap {
		t.Errorf("final count = %d, want %d", count, cap)
	}
}

// TestUpdateEndpointFieldsAndUpdatedAt asserts partial updates touch only the
// supplied fields, bump updated_at, and return the canonical row.
func TestUpdateEndpointFieldsAndUpdatedAt(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "upd.db"))
	defer st.Close()
	uid := seedTestUser(t, st, "u", nil)
	ep := mustCreateTestEndpoint(t, st, uid, "https://example.com/v1/")

	// No-op update returns the row and verifies ownership.
	got, err := st.UpdateEndpoint(context.Background(), uid, ep.ID, nil, nil, nil, 99)
	if err != nil {
		t.Fatalf("noop update: %v", err)
	}
	if got.BaseURL != ep.BaseURL || got.UpdatedAt != ep.UpdatedAt {
		t.Errorf("noop update altered row: %+v", got)
	}
	// No-op update for a cross-user id is not_found.
	if _, err := st.UpdateEndpoint(context.Background(), uid+999, ep.ID, nil, nil, nil, 99); !errors.Is(err, ErrNotFound) {
		t.Errorf("noop update cross-user: err=%v, want not_found", err)
	}

	// Partial: note + enabled, base_url untouched.
	updated, err := st.UpdateEndpoint(context.Background(), uid, ep.ID, nil, strPtrDB("note 1"), boolPtrDB(false), 5)
	if err != nil {
		t.Fatalf("update note+enabled: %v", err)
	}
	if updated.Note != "note 1" || updated.Enabled {
		t.Errorf("updated = %+v", updated)
	}
	if updated.BaseURL != ep.BaseURL {
		t.Errorf("update altered base_url: %q vs %q", updated.BaseURL, ep.BaseURL)
	}
	if updated.UpdatedAt != 5 {
		t.Errorf("updated_at = %d, want 5", updated.UpdatedAt)
	}

	// base_url change persists.
	updated, err = st.UpdateEndpoint(context.Background(), uid, ep.ID, strPtrDB("https://example.com/v2/"), nil, nil, 6)
	if err != nil {
		t.Fatalf("update base_url: %v", err)
	}
	if updated.BaseURL != "https://example.com/v2/" || updated.UpdatedAt != 6 {
		t.Errorf("updated = %+v", updated)
	}
}

// TestEndpointQueriesAreParameterized asserts SQL-special characters in
// user-supplied strings are stored verbatim and never interpreted as SQL: the
// endpoints table survives and the row round-trips intact.
func TestEndpointQueriesAreParameterized(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "sqli.db"))
	defer st.Close()
	uid := seedTestUser(t, st, "u", nil)

	injection := "'; DROP TABLE endpoints; --"
	ep, err := st.CreateEndpoint(context.Background(), uid, "openai-compatible", injection, injection, true, 1)
	if err != nil {
		t.Fatalf("create with injection string: %v", err)
	}
	if ep.BaseURL != injection || ep.Note != injection {
		t.Errorf("injection string not stored verbatim: %+v", ep)
	}

	// The endpoints table must still exist and hold the row.
	got, err := st.GetEndpoint(context.Background(), uid, ep.ID)
	if err != nil {
		t.Fatalf("get after injection: %v", err)
	}
	if got.BaseURL != injection {
		t.Errorf("round-trip base_url = %q, want %q", got.BaseURL, injection)
	}
	var n int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM endpoints`).Scan(&n); err != nil {
		t.Fatalf("endpoints table query failed (injection may have dropped it): %v", err)
	}
	if n != 1 {
		t.Errorf("endpoints count = %d, want 1", n)
	}
}

// --- helpers shared with endpoint_keys_test.go ------------------------------

func strPtrDB(s string) *string { return &s }
func intPtrDB(i int) *int       { return &i }
func boolPtrDB(b bool) *bool    { return &b }
