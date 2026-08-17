package endpoint

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/egress"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

// testService bundles a Service with its real collaborators so tests assert
// end-to-end behavior (ownership through SQL, egress canonicalization, AES
// envelope persistence) while still injecting fakes (hooks).
type testService struct {
	svc    *Service
	store  *db.Store
	vault  *secret.Vault
	policy *egress.EgressPolicy
	hook   *recordingHook
	path   string
}

var testUserSeq atomic.Int64

func newTestService(t *testing.T) *testService {
	t.Helper()
	key := bytes.Repeat([]byte{0x71}, secret.MasterKeyBytes)
	vault, err := secret.New(key)
	clear(key)
	if err != nil {
		t.Fatalf("create test vault: %v", err)
	}
	t.Cleanup(func() { _ = vault.Close() })
	path := filepath.Join(t.TempDir(), "endpoint.db")
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatalf("open test store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	policy, err := egress.NewEgressPolicy(nil)
	if err != nil {
		t.Fatalf("create egress policy: %v", err)
	}
	hook := &recordingHook{}
	svc := NewService(ServiceDeps{
		Repo:       store,
		URLs:       policy,
		Secrets:    vault,
		Connectors: NewRegistry(),
		Hook:       hook,
		Now:        func() int64 { return 1 },
	})
	return &testService{svc: svc, store: store, vault: vault, policy: policy, hook: hook, path: path}
}

// seedUser inserts a users row and returns its id. A non-nil endpointLimit
// sets the per-user cap override; nil leaves it at the global default.
func (ts *testService) seedUser(t *testing.T, endpointLimit *int) int64 {
	t.Helper()
	seq := testUserSeq.Add(1)
	var limitArg any
	if endpointLimit != nil {
		limitArg = *endpointLimit
	}
	res, err := ts.store.DB().Exec(
		`INSERT INTO users (discord_id, username, endpoint_limit, created_at, updated_at) VALUES (?, ?, ?, 1, 1)`,
		fmt.Sprintf("u%d", seq), "tester", limitArg)
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("seed user last insert id: %v", err)
	}
	return id
}

func (ts *testService) setGlobalLimit(t *testing.T, raw string) {
	t.Helper()
	if _, err := ts.store.DB().Exec(
		`INSERT INTO site_config (key, value, updated_at) VALUES ('default_endpoint_limit', ?, 1)`, raw); err != nil {
		t.Fatalf("set global limit: %v", err)
	}
}

func (ts *testService) seedModel(t *testing.T, userID int64, provider, model string) int64 {
	t.Helper()
	res, err := ts.store.DB().Exec(
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

// recordingHook records every FetchModels invocation and optionally returns a
// fixed error. It is safe for concurrent use.
type recordingHook struct {
	mu    sync.Mutex
	calls []hookCall
	err   error
}

type hookCall struct {
	userID, endpointID, keyID int64
}

func (h *recordingHook) FetchModels(_ context.Context, userID, endpointID, keyID int64) error {
	h.mu.Lock()
	h.calls = append(h.calls, hookCall{userID, endpointID, keyID})
	err := h.err
	h.mu.Unlock()
	return err
}

func (h *recordingHook) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.calls)
}

func (h *recordingHook) last() hookCall {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.calls) == 0 {
		return hookCall{}
	}
	return h.calls[len(h.calls)-1]
}

func mustCreateEndpoint(t *testing.T, ts *testService, userID int64, baseURL string) db.Endpoint {
	t.Helper()
	ep, err := ts.svc.CreateEndpoint(context.Background(), userID, "", baseURL, nil, nil)
	if err != nil {
		t.Fatalf("create endpoint: %v", err)
	}
	return ep
}

func mustCreateKey(t *testing.T, ts *testService, userID, endpointID int64, secretPlaintext string, enabled bool) db.EndpointKey {
	t.Helper()
	en := enabled
	k, err := ts.svc.CreateEndpointKey(context.Background(), userID, endpointID, []byte(secretPlaintext), nil, &en)
	if err != nil {
		t.Fatalf("create endpoint key: %v", err)
	}
	return k
}

// --- connector + base_url ---------------------------------------------------

func TestCreateEndpointDefaultsConnectorAndCanonicalizesBaseURL(t *testing.T) {
	ts := newTestService(t)
	uid := ts.seedUser(t, nil)

	messy := " HTTPS://user:secret@EXAMPLE.COM.:443//api///v1/?token=x#fragment "
	want, err := ts.policy.ValidateBaseURL(messy)
	if err != nil {
		t.Fatalf("reference canonicalize: %v", err)
	}
	ep, err := ts.svc.CreateEndpoint(context.Background(), uid, "", messy, nil, nil)
	if err != nil {
		t.Fatalf("create endpoint: %v", err)
	}
	if ep.ConnectorType != string(ConnectorOpenAICompatible) {
		t.Errorf("connector = %q, want %q", ep.ConnectorType, ConnectorOpenAICompatible)
	}
	if ep.BaseURL != want {
		t.Errorf("base_url = %q, want canonical %q", ep.BaseURL, want)
	}
	if !ep.Enabled {
		t.Errorf("enabled = false, want default true")
	}
}

func TestCreateEndpointAcceptsExplicitOpenAICompatible(t *testing.T) {
	ts := newTestService(t)
	uid := ts.seedUser(t, nil)
	ep, err := ts.svc.CreateEndpoint(context.Background(), uid, "openai-compatible", "https://example.com/v1/", nil, nil)
	if err != nil {
		t.Fatalf("create endpoint: %v", err)
	}
	if ep.ConnectorType != "openai-compatible" {
		t.Fatalf("connector = %q", ep.ConnectorType)
	}
}

func TestCreateEndpointRejectsUnknownConnectorAndPersistsNothing(t *testing.T) {
	ts := newTestService(t)
	uid := ts.seedUser(t, nil)
	before, err := ts.store.CountEndpoints(context.Background(), uid)
	if err != nil {
		t.Fatalf("count before: %v", err)
	}
	for _, ct := range []string{"anthropic-compatible", "dify-app", "openai", "OpenAI-Compatible"} {
		if _, err := ts.svc.CreateEndpoint(context.Background(), uid, ct, "https://example.com/v1/", nil, nil); err == nil {
			t.Errorf("CreateEndpoint with connector %q unexpectedly succeeded", ct)
		}
	}
	after, err := ts.store.CountEndpoints(context.Background(), uid)
	if err != nil {
		t.Fatalf("count after: %v", err)
	}
	if after != before {
		t.Fatalf("unknown connector created a row: before=%d after=%d", before, after)
	}
}

func TestCreateEndpointRejectsUnsafeBaseURL(t *testing.T) {
	ts := newTestService(t)
	uid := ts.seedUser(t, nil)
	for _, raw := range []string{
		"http://127.0.0.1:8080",
		"http://10.0.0.1",
		"http://169.254.169.254/latest/meta-data",
		"http://100.100.100.200/latest/meta-data",
		"http://[::1]:8080",
		"http://[fe80::1]/",
		"http://localhost",
		"localhost",
		"ftp://example.com",
		"file:///etc/passwd",
		"https://exa mple.com",
		"",
		"   ",
	} {
		_, err := ts.svc.CreateEndpoint(context.Background(), uid, "", raw, nil, nil)
		if err == nil {
			t.Errorf("CreateEndpoint(%q) accepted an unsafe base URL", raw)
			continue
		}
		if !errors.Is(err, ErrInvalidRequest) {
			t.Errorf("CreateEndpoint(%q) err = %v, want ErrInvalidRequest", raw, err)
		}
	}
}

func TestCreateEndpointSameBaseURLCoexists(t *testing.T) {
	ts := newTestService(t)
	uid := ts.seedUser(t, nil)
	const url = "https://example.com/v1/"
	ep1 := mustCreateEndpoint(t, ts, uid, url)
	ep2 := mustCreateEndpoint(t, ts, uid, url)
	if ep1.ID == ep2.ID {
		t.Fatal("two endpoints with the same base_url got the same id")
	}
	eps, err := ts.svc.ListEndpoints(context.Background(), uid)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(eps) != 2 {
		t.Fatalf("expected 2 endpoints sharing base_url, got %d", len(eps))
	}
}

func TestTrailingSlashAndPathPrefixNotMerged(t *testing.T) {
	ts := newTestService(t)
	uid := ts.seedUser(t, nil)
	without := mustCreateEndpoint(t, ts, uid, "https://example.com/v1")
	with := mustCreateEndpoint(t, ts, uid, "https://example.com/v1/")
	if without.BaseURL == with.BaseURL {
		t.Fatalf("trailing-slash distinction lost: both %q", without.BaseURL)
	}
	eps, err := ts.svc.ListEndpoints(context.Background(), uid)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(eps) != 2 {
		t.Fatalf("expected 2 endpoints (no false merge), got %d", len(eps))
	}
}

// --- ownership matrix -------------------------------------------------------

func TestEndpointOwnershipMatrix(t *testing.T) {
	ts := newTestService(t)
	alice := ts.seedUser(t, nil)
	bob := ts.seedUser(t, nil)

	aEP := mustCreateEndpoint(t, ts, alice, "https://alice.example/v1/")
	bEP := mustCreateEndpoint(t, ts, bob, "https://bob.example/v1/")
	aKey := mustCreateKey(t, ts, alice, aEP.ID, "sk-alice-secret-AAAA", true)
	bKey := mustCreateKey(t, ts, bob, bEP.ID, "sk-bob-secret-BBBB", true)

	// Bob cannot see or touch Alice's endpoint.
	if _, err := ts.svc.GetEndpoint(context.Background(), bob, aEP.ID); !errors.Is(err, db.ErrNotFound) {
		t.Errorf("bob get alice endpoint: err=%v, want not_found", err)
	}
	if _, err := ts.svc.UpdateEndpoint(context.Background(), bob, aEP.ID, strPtr("https://bob.example/v2/"), nil, nil, nil); !errors.Is(err, db.ErrNotFound) {
		t.Errorf("bob patch alice endpoint: err=%v, want not_found", err)
	}
	if err := ts.svc.DeleteEndpoint(context.Background(), bob, aEP.ID); !errors.Is(err, db.ErrNotFound) {
		t.Errorf("bob delete alice endpoint: err=%v, want not_found", err)
	}

	// Bob cannot list keys on Alice's endpoint: a cross-user endpoint id is
	// not_found, not an empty list that hints the endpoint exists.
	if _, err := ts.svc.ListEndpointKeys(context.Background(), bob, aEP.ID); !errors.Is(err, db.ErrNotFound) {
		t.Errorf("bob list keys on alice endpoint: err=%v, want not_found", err)
	}

	// Bob cannot add a key to Alice's endpoint.
	if _, err := ts.svc.CreateEndpointKey(context.Background(), bob, aEP.ID, []byte("sk-bob-inject"), nil, nil); !errors.Is(err, db.ErrNotFound) {
		t.Errorf("bob add key to alice endpoint: err=%v, want not_found", err)
	}

	// Bob cannot patch or delete Alice's key, even via Alice's endpoint path.
	if _, err := ts.svc.UpdateEndpointKey(context.Background(), bob, aEP.ID, aKey.ID, strPtr("note"), nil); !errors.Is(err, db.ErrNotFound) {
		t.Errorf("bob patch alice key: err=%v, want not_found", err)
	}
	if err := ts.svc.DeleteEndpointKey(context.Background(), bob, aEP.ID, aKey.ID); !errors.Is(err, db.ErrNotFound) {
		t.Errorf("bob delete alice key: err=%v, want not_found", err)
	}

	// Bob cannot address Alice's key through his own endpoint's path.
	if _, err := ts.svc.UpdateEndpointKey(context.Background(), bob, bEP.ID, aKey.ID, strPtr("note"), nil); !errors.Is(err, db.ErrNotFound) {
		t.Errorf("bob patch alice key via bob endpoint path: err=%v, want not_found", err)
	}
	if err := ts.svc.DeleteEndpointKey(context.Background(), bob, bEP.ID, aKey.ID); !errors.Is(err, db.ErrNotFound) {
		t.Errorf("bob delete alice key via bob endpoint path: err=%v, want not_found", err)
	}

	// Alice cannot address Bob's key via Alice's own endpoint path (same-user
	// wrong-endpoint path is not_found, not a leak).
	if _, err := ts.svc.UpdateEndpointKey(context.Background(), alice, aEP.ID, bKey.ID, strPtr("note"), nil); !errors.Is(err, db.ErrNotFound) {
		t.Errorf("alice patch bob key via alice endpoint path: err=%v, want not_found", err)
	}
	if err := ts.svc.DeleteEndpointKey(context.Background(), alice, aEP.ID, bKey.ID); !errors.Is(err, db.ErrNotFound) {
		t.Errorf("alice delete bob key via alice endpoint path: err=%v, want not_found", err)
	}

	// Cross-user list never returns the other user's endpoint.
	aList, err := ts.svc.ListEndpoints(context.Background(), alice)
	if err != nil {
		t.Fatalf("alice list: %v", err)
	}
	for _, ep := range aList {
		if ep.UserID != alice {
			t.Errorf("alice list returned endpoint owned by %d", ep.UserID)
		}
	}
	bList, err := ts.svc.ListEndpoints(context.Background(), bob)
	if err != nil {
		t.Fatalf("bob list: %v", err)
	}
	if len(bList) != 1 || bList[0].ID != bEP.ID {
		t.Errorf("bob list = %v, want only bob's endpoint", bList)
	}

	// Alice still owns her endpoint and key after Bob's failed attempts.
	got, err := ts.svc.GetEndpoint(context.Background(), alice, aEP.ID)
	if err != nil {
		t.Fatalf("alice get own endpoint after attacks: %v", err)
	}
	if got.BaseURL != aEP.BaseURL {
		t.Errorf("alice endpoint base_url changed under attack: %q vs %q", got.BaseURL, aEP.BaseURL)
	}
	aKeys, err := ts.svc.ListEndpointKeys(context.Background(), alice, aEP.ID)
	if err != nil {
		t.Fatalf("alice list own keys: %v", err)
	}
	if len(aKeys) != 1 || aKeys[0].ID != aKey.ID {
		t.Errorf("alice keys = %v, want only her key", aKeys)
	}
}

// --- endpoint cap -----------------------------------------------------------

func TestEndpointCapMinOfGlobalAndUser(t *testing.T) {
	cases := []struct {
		name        string
		global      string // raw site_config value; "" means unset
		userLimit   *int
		wantCap     int
		attempts    int // how many creates to attempt
		wantSucceed int // expected successes
		wantCapErr  int // expected ErrEndpointCap results
	}{
		{"global default when unset", "", nil, db.DefaultEndpointLimit, 5, 5, 0},
		{"global overrides default", "3", nil, 3, 5, 3, 2},
		{"user override below global", "5", intPtr(2), 2, 5, 2, 3},
		{"global below user override", "5", intPtr(10), 5, 7, 5, 2},
		{"user zero blocks endpoints", "5", intPtr(0), 0, 3, 0, 3},
		{"negative user override ignored", "2", intPtr(-1), 2, 4, 2, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ts := newTestService(t)
			if tc.global != "" {
				ts.setGlobalLimit(t, tc.global)
			}
			uid := ts.seedUser(t, tc.userLimit)

			cap, err := ts.store.EndpointCap(context.Background(), uid)
			if err != nil {
				t.Fatalf("EndpointCap: %v", err)
			}
			if cap != tc.wantCap {
				t.Errorf("cap = %d, want %d", cap, tc.wantCap)
			}

			succeed, capErr := 0, 0
			for i := 0; i < tc.attempts; i++ {
				_, err := ts.svc.CreateEndpoint(context.Background(), uid, "",
					fmt.Sprintf("https://example.com/ep%d/", i), nil, nil)
				switch {
				case err == nil:
					succeed++
				case errors.Is(err, db.ErrEndpointCap):
					capErr++
				default:
					t.Fatalf("create %d: err=%v, want cap or nil", i, err)
				}
			}
			if succeed != tc.wantSucceed {
				t.Errorf("succeeded creates = %d, want %d", succeed, tc.wantSucceed)
			}
			if capErr != tc.wantCapErr {
				t.Errorf("cap errors = %d, want %d", capErr, tc.wantCapErr)
			}
			count, err := ts.store.CountEndpoints(context.Background(), uid)
			if err != nil {
				t.Fatalf("count: %v", err)
			}
			if count != tc.wantSucceed {
				t.Errorf("final count = %d, want %d", count, tc.wantSucceed)
			}
		})
	}
}

func TestEndpointCapRejectsInvalidSiteConfig(t *testing.T) {
	for _, raw := range []string{"not-a-number", "-3", "  "} {
		t.Run(raw, func(t *testing.T) {
			ts := newTestService(t)
			ts.setGlobalLimit(t, raw)
			uid := ts.seedUser(t, nil)
			_, err := ts.svc.CreateEndpoint(context.Background(), uid, "", "https://example.com/v1/", nil, nil)
			if err == nil {
				t.Fatalf("create succeeded under invalid site_config")
			}
			if !errors.Is(err, ErrInvalidRequest) {
				t.Errorf("err = %v, want ErrInvalidRequest (mapped from ErrInvalidSiteConfig)", err)
			}
			count, err := ts.store.CountEndpoints(context.Background(), uid)
			if err != nil {
				t.Fatalf("count: %v", err)
			}
			if count != 0 {
				t.Errorf("invalid site_config still produced %d endpoints", count)
			}
		})
	}
}

// TestEndpointCapAtomicUnderConcurrency hammers CreateEndpoint from many
// goroutines against a small cap and asserts the cap is never breached. Run
// under -race (scripts/race-check.sh) to confirm no data race.
func TestEndpointCapAtomicUnderConcurrency(t *testing.T) {
	const cap = 8
	const workers = 64
	ts := newTestService(t)
	ts.setGlobalLimit(t, strconv.Itoa(cap))
	uid := ts.seedUser(t, nil)

	var wg sync.WaitGroup
	var succ, capErr atomic.Int64
	start := make(chan struct{})
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		i := i
		go func() {
			defer wg.Done()
			<-start
			_, err := ts.svc.CreateEndpoint(context.Background(), uid, "",
				fmt.Sprintf("https://example.com/worker%d/", i), nil, nil)
			switch {
			case err == nil:
				succ.Add(1)
			case errors.Is(err, db.ErrEndpointCap):
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
	count, err := ts.store.CountEndpoints(context.Background(), uid)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != cap {
		t.Errorf("final count = %d, want %d", count, cap)
	}
}

// --- endpoint update / delete / cascade -------------------------------------

func TestUpdateEndpointConnectorImmutable(t *testing.T) {
	ts := newTestService(t)
	uid := ts.seedUser(t, nil)
	ep := mustCreateEndpoint(t, ts, uid, "https://example.com/v1/")
	if _, err := ts.svc.UpdateEndpoint(context.Background(), uid, ep.ID, nil, nil, nil, strPtr("openai-compatible")); err == nil {
		t.Fatal("UpdateEndpoint accepted a connector_type change")
	} else if !errors.Is(err, ErrConnectorImmutable) {
		t.Errorf("err = %v, want ErrConnectorImmutable", err)
	}
}

func TestUpdateEndpointRecanonicalizesBaseURL(t *testing.T) {
	ts := newTestService(t)
	uid := ts.seedUser(t, nil)
	ep := mustCreateEndpoint(t, ts, uid, "https://example.com/v1/")
	want, err := ts.policy.ValidateBaseURL(" HTTPS://API.Example.com:443/v2///")
	if err != nil {
		t.Fatalf("reference canonicalize: %v", err)
	}
	updated, err := ts.svc.UpdateEndpoint(context.Background(), uid, ep.ID, strPtr(" HTTPS://API.Example.com:443/v2///"), nil, nil, nil)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.BaseURL != want {
		t.Errorf("updated base_url = %q, want %q", updated.BaseURL, want)
	}
}

func TestUpdateEndpointTriggersFetchPerEnabledKey(t *testing.T) {
	ts := newTestService(t)
	uid := ts.seedUser(t, nil)
	ep := mustCreateEndpoint(t, ts, uid, "https://example.com/v1/")
	enabled := mustCreateKey(t, ts, uid, ep.ID, "sk-enabled-AAAAAAAA", true)
	mustCreateKey(t, ts, uid, ep.ID, "sk-disabled-BBBBBBBB", false)

	if got := ts.hook.count(); got != 1 {
		t.Fatalf("after key adds, hook calls = %d, want 1 (enabled only)", got)
	}
	if ts.hook.last().keyID != enabled.ID {
		t.Errorf("first hook call keyID = %d, want enabled key %d", ts.hook.last().keyID, enabled.ID)
	}

	if _, err := ts.svc.UpdateEndpoint(context.Background(), uid, ep.ID, nil, strPtr("edited note"), nil, nil); err != nil {
		t.Fatalf("update endpoint: %v", err)
	}
	if got := ts.hook.count(); got != 2 {
		t.Fatalf("after endpoint edit, hook calls = %d, want 2 (one per enabled key)", got)
	}
	last := ts.hook.last()
	if last.endpointID != ep.ID || last.keyID != enabled.ID {
		t.Errorf("post-edit hook call = %+v, want endpoint %d key %d", last, ep.ID, enabled.ID)
	}
}

func TestDeleteEndpointCascadesKeysCacheAndBindings(t *testing.T) {
	ts := newTestService(t)
	uid := ts.seedUser(t, nil)
	ep := mustCreateEndpoint(t, ts, uid, "https://example.com/v1/")
	k1 := mustCreateKey(t, ts, uid, ep.ID, "sk-cascade-11111111", true)
	k2 := mustCreateKey(t, ts, uid, ep.ID, "sk-cascade-22222222", true)
	modelID := ts.seedModel(t, uid, "openai", "gpt")
	d := ts.store.DB()

	// Seed cache and bindings for both keys.
	for _, kid := range []int64{k1.ID, k2.ID} {
		if _, err := d.Exec(`INSERT INTO fetched_models (endpoint_key_id, upstream_model_id, provider, fetched_at, status) VALUES (?, 'gpt', 'openai', 1, 'ok')`, kid); err != nil {
			t.Fatalf("seed fetched_models: %v", err)
		}
		if _, err := d.Exec(`INSERT INTO model_bindings (model_id, endpoint_key_id, upstream_model_id, ord, created_at) VALUES (?, ?, 'gpt', 0, 1)`, modelID, kid); err != nil {
			t.Fatalf("seed model_bindings: %v", err)
		}
	}

	if err := ts.svc.DeleteEndpoint(context.Background(), uid, ep.ID); err != nil {
		t.Fatalf("delete endpoint: %v", err)
	}
	for _, table := range []string{"endpoint_keys", "fetched_models", "model_bindings"} {
		var n int
		if err := d.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if n != 0 {
			t.Errorf("cascade left %d rows in %s, want 0", n, table)
		}
	}
	// The model itself remains (it is owned by the user, not the endpoint).
	var models int
	if err := d.QueryRow(`SELECT COUNT(*) FROM models WHERE user_id=?`, uid).Scan(&models); err != nil {
		t.Fatalf("count models: %v", err)
	}
	if models != 1 {
		t.Errorf("models = %d, want 1 (endpoint delete must not drop the model)", models)
	}
}

// --- endpoint key secret boundary -------------------------------------------

func TestCreateEndpointKeySealsAndStoresDisplayFragments(t *testing.T) {
	ts := newTestService(t)
	uid := ts.seedUser(t, nil)
	ep := mustCreateEndpoint(t, ts, uid, "https://example.com/v1/")

	plaintext := []byte("sk-test-1234567890-abcdef")
	// Pass a clone: the service clears the secret slice it receives (so no
	// plaintext outlives the call), which would zero the test's reference too.
	k, err := ts.svc.CreateEndpointKey(context.Background(), uid, ep.ID, bytes.Clone(plaintext), nil, nil)
	if err != nil {
		t.Fatalf("create key: %v", err)
	}
	if k.DisplayHead != "sk-t" {
		t.Errorf("display_head = %q, want first 4 runes", k.DisplayHead)
	}
	if k.DisplayTail != "cdef" {
		t.Errorf("display_tail = %q, want last 4 runes", k.DisplayTail)
	}

	// The persisted row carries the AES envelope, not the plaintext, and it
	// round-trips through the vault.
	var stored string
	if err := ts.store.DB().QueryRow(`SELECT encrypted_secret FROM endpoint_keys WHERE id=?`, k.ID).Scan(&stored); err != nil {
		t.Fatalf("read encrypted_secret: %v", err)
	}
	if stored == string(plaintext) || strings.Contains(stored, string(plaintext)) {
		t.Fatal("encrypted_secret contains plaintext")
	}
	opened, err := ts.vault.Open(stored)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !bytes.Equal(opened, plaintext) {
		t.Fatalf("opened = %q, want %q", opened, plaintext)
	}
	clear(opened)
}

func TestCreateEndpointKeyNoPlaintextInDatabaseFiles(t *testing.T) {
	ts := newTestService(t)
	uid := ts.seedUser(t, nil)
	ep := mustCreateEndpoint(t, ts, uid, "https://example.com/v1/")

	plaintext := []byte("db-leak-sentinel-9c4f17a3dddd")
	if _, err := ts.svc.CreateEndpointKey(context.Background(), uid, ep.ID, bytes.Clone(plaintext), nil, nil); err != nil {
		t.Fatalf("create key: %v", err)
	}
	if _, err := ts.store.DB().Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	assertDBFilesExclude(t, ts.path, plaintext)
}

func assertDBFilesExclude(t *testing.T, path string, plaintext []byte) {
	t.Helper()
	for _, candidate := range []string{path, path + "-wal", path + "-shm"} {
		data, err := os.ReadFile(candidate)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			t.Fatalf("read db artifact %s: %v", candidate, err)
		}
		contains := bytes.Contains(data, plaintext)
		clear(data)
		if contains {
			t.Fatalf("db artifact %s contains plaintext secret bytes", candidate)
		}
	}
}

func TestCreateEndpointKeyRejectsEmptyAndOversizedSecret(t *testing.T) {
	ts := newTestService(t)
	uid := ts.seedUser(t, nil)
	ep := mustCreateEndpoint(t, ts, uid, "https://example.com/v1/")

	if _, err := ts.svc.CreateEndpointKey(context.Background(), uid, ep.ID, []byte(""), nil, nil); err == nil {
		t.Error("empty secret accepted")
	} else if !errors.Is(err, ErrInvalidRequest) {
		t.Errorf("empty secret err = %v, want ErrInvalidRequest", err)
	}

	oversized := bytes.Repeat([]byte("k"), MaxSecretBytes+1)
	if _, err := ts.svc.CreateEndpointKey(context.Background(), uid, ep.ID, oversized, nil, nil); err == nil {
		t.Error("oversized secret accepted")
	} else if !errors.Is(err, ErrPayloadTooLarge) {
		t.Errorf("oversized secret err = %v, want ErrPayloadTooLarge", err)
	}
	for _, invalid := range [][]byte{[]byte("secret\x00value"), []byte("secret\nvalue"), {0xff, 0xfe}} {
		if _, err := ts.svc.CreateEndpointKey(context.Background(), uid, ep.ID, invalid, nil, nil); err == nil {
			t.Errorf("invalid secret %q accepted", invalid)
		} else if !errors.Is(err, ErrInvalidRequest) {
			t.Errorf("invalid secret %q err = %v, want ErrInvalidRequest", invalid, err)
		}
	}
	// No row should exist for any rejected create.
	var n int
	if err := ts.store.DB().QueryRow(`SELECT COUNT(*) FROM endpoint_keys`).Scan(&n); err != nil {
		t.Fatalf("count keys: %v", err)
	}
	if n != 0 {
		t.Errorf("rejected creates left %d key rows", n)
	}
}

func TestCreateEndpointKeyRejectsBadNote(t *testing.T) {
	ts := newTestService(t)
	uid := ts.seedUser(t, nil)
	ep := mustCreateEndpoint(t, ts, uid, "https://example.com/v1/")

	for _, note := range []string{
		"line\nbreak",
		"null\x00byte",
		"tab\there",
		strings.Repeat("n", MaxNoteRunes+1),
	} {
		if _, err := ts.svc.CreateEndpointKey(context.Background(), uid, ep.ID, []byte("sk-valid-secret"), strPtr(note), nil); err == nil {
			t.Errorf("bad note %q accepted", note)
		} else if !errors.Is(err, ErrInvalidRequest) {
			t.Errorf("bad note %q err = %v, want ErrInvalidRequest", note, err)
		}
	}
}

func TestCreateEndpointKeyTriggersFetchOnlyWhenEnabled(t *testing.T) {
	ts := newTestService(t)
	uid := ts.seedUser(t, nil)
	ep := mustCreateEndpoint(t, ts, uid, "https://example.com/v1/")

	mustCreateKey(t, ts, uid, ep.ID, "sk-disabled-CCCCCCCC", false)
	if got := ts.hook.count(); got != 0 {
		t.Fatalf("disabled key add triggered %d hook calls, want 0", got)
	}
	mustCreateKey(t, ts, uid, ep.ID, "sk-enabled-DDDDDDDD", true)
	if got := ts.hook.count(); got != 1 {
		t.Fatalf("enabled key add triggered %d hook calls, want 1", got)
	}
}

func TestCreateEndpointKeyCrossUserNotFound(t *testing.T) {
	ts := newTestService(t)
	alice := ts.seedUser(t, nil)
	bob := ts.seedUser(t, nil)
	aEP := mustCreateEndpoint(t, ts, alice, "https://alice.example/v1/")

	if _, err := ts.svc.CreateEndpointKey(context.Background(), bob, aEP.ID, []byte("sk-bob-inject"), nil, nil); !errors.Is(err, db.ErrNotFound) {
		t.Errorf("bob add key to alice endpoint: err=%v, want not_found", err)
	}
	if got := ts.hook.count(); got != 0 {
		t.Errorf("cross-user create triggered fetch hook %d times, want 0", got)
	}
	var n int
	if err := ts.store.DB().QueryRow(`SELECT COUNT(*) FROM endpoint_keys`).Scan(&n); err != nil {
		t.Fatalf("count keys: %v", err)
	}
	if n != 0 {
		t.Errorf("cross-user create inserted %d key rows", n)
	}
}

func TestListEndpointKeysExposesNoCiphertextOrPlaintext(t *testing.T) {
	ts := newTestService(t)
	uid := ts.seedUser(t, nil)
	ep := mustCreateEndpoint(t, ts, uid, "https://example.com/v1/")
	plaintext := []byte("sk-list-secret-EEEEEEEE")
	mustCreateKey(t, ts, uid, ep.ID, string(plaintext), true)

	keys, err := ts.svc.ListEndpointKeys(context.Background(), uid, ep.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(keys) != 1 {
		t.Fatalf("got %d keys, want 1", len(keys))
	}
	k := keys[0]
	// The EndpointKey struct has no ciphertext field; assert each field does not
	// carry the plaintext either.
	for _, field := range []string{k.DisplayHead, k.DisplayTail, k.Note} {
		if strings.Contains(field, string(plaintext)) {
			t.Errorf("key field %q contains plaintext", field)
		}
	}
}

func TestUpdateEndpointKeyNoteAndEnabledAndDisableFilter(t *testing.T) {
	ts := newTestService(t)
	uid := ts.seedUser(t, nil)
	ep := mustCreateEndpoint(t, ts, uid, "https://example.com/v1/")
	k := mustCreateKey(t, ts, uid, ep.ID, "sk-toggle-FFFFFFFFFF", true)

	updated, err := ts.svc.UpdateEndpointKey(context.Background(), uid, ep.ID, k.ID, strPtr("rotated note"), boolPtr(false))
	if err != nil {
		t.Fatalf("update key: %v", err)
	}
	if updated.Note != "rotated note" || updated.Enabled {
		t.Errorf("updated = %+v", updated)
	}

	enabled, err := ts.store.ListEnabledEndpointKeys(context.Background(), uid, ep.ID)
	if err != nil {
		t.Fatalf("list enabled: %v", err)
	}
	if len(enabled) != 0 {
		t.Errorf("disabled key still in enabled list: %d rows", len(enabled))
	}
	all, err := ts.svc.ListEndpointKeys(context.Background(), uid, ep.ID)
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(all) != 1 || all[0].Enabled {
		t.Errorf("disabled key missing from full list or still enabled: %+v", all)
	}
}

func TestUpdateEndpointKeyDoesNotMutateCiphertext(t *testing.T) {
	ts := newTestService(t)
	uid := ts.seedUser(t, nil)
	ep := mustCreateEndpoint(t, ts, uid, "https://example.com/v1/")
	k := mustCreateKey(t, ts, uid, ep.ID, "sk-immutable-GGGGGGGG", true)

	var before string
	if err := ts.store.DB().QueryRow(`SELECT encrypted_secret FROM endpoint_keys WHERE id=?`, k.ID).Scan(&before); err != nil {
		t.Fatalf("read before: %v", err)
	}
	if _, err := ts.svc.UpdateEndpointKey(context.Background(), uid, ep.ID, k.ID, strPtr("note 2"), boolPtr(false)); err != nil {
		t.Fatalf("update: %v", err)
	}
	var after string
	if err := ts.store.DB().QueryRow(`SELECT encrypted_secret FROM endpoint_keys WHERE id=?`, k.ID).Scan(&after); err != nil {
		t.Fatalf("read after: %v", err)
	}
	if before != after {
		t.Errorf("UpdateEndpointKey altered ciphertext: before=%q after=%q", before, after)
	}
}

func TestDeleteEndpointKeyCascadesCacheAndBindings(t *testing.T) {
	ts := newTestService(t)
	uid := ts.seedUser(t, nil)
	ep := mustCreateEndpoint(t, ts, uid, "https://example.com/v1/")
	k := mustCreateKey(t, ts, uid, ep.ID, "sk-cascade-key-HHHHHHHH", true)
	modelID := ts.seedModel(t, uid, "openai", "gpt")
	d := ts.store.DB()
	if _, err := d.Exec(`INSERT INTO fetched_models (endpoint_key_id, upstream_model_id, provider, fetched_at, status) VALUES (?, 'gpt', 'openai', 1, 'ok')`, k.ID); err != nil {
		t.Fatalf("seed fetched_models: %v", err)
	}
	if _, err := d.Exec(`INSERT INTO model_bindings (model_id, endpoint_key_id, upstream_model_id, ord, created_at) VALUES (?, ?, 'gpt', 0, 1)`, modelID, k.ID); err != nil {
		t.Fatalf("seed model_bindings: %v", err)
	}

	if err := ts.svc.DeleteEndpointKey(context.Background(), uid, ep.ID, k.ID); err != nil {
		t.Fatalf("delete key: %v", err)
	}
	for _, table := range []string{"endpoint_keys", "fetched_models", "model_bindings"} {
		var n int
		if err := d.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if n != 0 {
			t.Errorf("cascade left %d rows in %s, want 0", n, table)
		}
	}
	// Endpoint and model survive.
	var eps, models int
	if err := d.QueryRow(`SELECT COUNT(*) FROM endpoints WHERE id=?`, ep.ID).Scan(&eps); err != nil {
		t.Fatalf("count endpoints: %v", err)
	}
	if err := d.QueryRow(`SELECT COUNT(*) FROM models WHERE id=?`, modelID).Scan(&models); err != nil {
		t.Fatalf("count models: %v", err)
	}
	if eps != 1 || models != 1 {
		t.Errorf("delete key dropped endpoint(%d) or model(%d), want both retained", eps, models)
	}
}

// --- secret envelope integrity ----------------------------------------------

func TestSecretEnvelopeWrongKeyAndTamperFail(t *testing.T) {
	ts := newTestService(t)
	uid := ts.seedUser(t, nil)
	ep := mustCreateEndpoint(t, ts, uid, "https://example.com/v1/")
	plaintext := []byte("sk-integrity-IIIIIIII")
	k, err := ts.svc.CreateEndpointKey(context.Background(), uid, ep.ID, plaintext, nil, nil)
	if err != nil {
		t.Fatalf("create key: %v", err)
	}
	var ciphertext string
	if err := ts.store.DB().QueryRow(`SELECT encrypted_secret FROM endpoint_keys WHERE id=?`, k.ID).Scan(&ciphertext); err != nil {
		t.Fatalf("read ciphertext: %v", err)
	}

	// A vault built with a different master key cannot decrypt the envelope.
	otherKey := bytes.Repeat([]byte{0x29}, secret.MasterKeyBytes)
	otherVault, err := secret.New(otherKey)
	clear(otherKey)
	if err != nil {
		t.Fatalf("create other vault: %v", err)
	}
	defer otherVault.Close()
	if _, err := otherVault.Open(ciphertext); err == nil {
		t.Fatal("Open with a wrong master key unexpectedly succeeded")
	}

	// A single-byte tamper of the ciphertext portion must fail authentication.
	tampered := toggleLastBase64(ciphertext)
	if _, err := ts.vault.Open(tampered); err == nil {
		t.Fatal("Open accepted a tampered ciphertext")
	}
}

// --- hook failure isolation -------------------------------------------------

func TestHookFailureDoesNotRollbackCommittedKey(t *testing.T) {
	ts := newTestService(t)
	uid := ts.seedUser(t, nil)
	ep := mustCreateEndpoint(t, ts, uid, "https://example.com/v1/")

	ts.hook.err = errors.New("fetch backend down")
	k, err := ts.svc.CreateEndpointKey(context.Background(), uid, ep.ID, []byte("sk-hookfail-JJJJJJJJ"), nil, nil)
	if err != nil {
		t.Fatalf("CreateEndpointKey returned %v; a hook failure must not fail the committed save", err)
	}
	if ts.hook.count() != 1 {
		t.Fatalf("hook calls = %d, want 1 (hook invoked then failed)", ts.hook.count())
	}
	// The key row is committed despite the hook error.
	var n int
	if err := ts.store.DB().QueryRow(`SELECT COUNT(*) FROM endpoint_keys WHERE id=?`, k.ID).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("committed key row missing after hook failure: count=%d", n)
	}
}

// --- pure unit tests --------------------------------------------------------

func TestDisplayFragmentsEdgeCases(t *testing.T) {
	cases := []struct {
		secret string
		head   string
		tail   string
	}{
		{"", "", ""},
		{"a", "", ""}, // <=4: head would reveal all -> suppress
		{"ab", "", ""},
		{"abcd", "", ""},
		{"abcde", "abcd", ""}, // 5: head first4 (1 hidden), no tail
		{"abcdefgh", "abcd", ""},
		{"abcdefghi", "abcd", "fghi"}, // 9: head+tail, 1 hidden
		{"abcdefghij", "abcd", "ghij"},
		{"_sk-abcdefghijklmnopqrstuvwxyz0123456789_", "_sk-", "789_"},
	}
	for _, tc := range cases {
		head, tail := displayFragments([]byte(tc.secret))
		if head != tc.head || tail != tc.tail {
			t.Errorf("displayFragments(%q) = (%q,%q), want (%q,%q)", tc.secret, head, tail, tc.head, tc.tail)
		}
		// The fragments must never equal or fully re-cover the secret.
		if head+tail == tc.secret && tc.secret != "" {
			t.Errorf("displayFragments(%q) re-covers the whole secret", tc.secret)
		}
	}
}

func TestValidateBoundedTextRejectsControlCharsAndOversize(t *testing.T) {
	for _, bad := range []string{"a\x00b", "x\ty", "p\nq", "r\x7fs", strings.Repeat("z", MaxNoteRunes+1)} {
		if err := validateBoundedText(bad, MaxNoteRunes); err == nil {
			t.Errorf("validateBoundedText(%q) accepted", bad)
		}
	}
	// Clean bounded text passes.
	if err := validateBoundedText("a normal note", MaxNoteRunes); err != nil {
		t.Errorf("validateBoundedText clean note: %v", err)
	}
	if err := validateBoundedText("", MaxNoteRunes); err != nil {
		t.Errorf("validateBoundedText empty: %v", err)
	}
	// Multibyte rune count, not byte length, is the bound.
	mb := strings.Repeat("世", MaxNoteRunes)
	if err := validateBoundedText(mb, MaxNoteRunes); err != nil {
		t.Errorf("validateBoundedText max-rune multibyte: %v", err)
	}
	if err := validateBoundedText(mb+"世", MaxNoteRunes); err == nil {
		t.Errorf("validateBoundedText over-rune multibyte accepted")
	}
}

func TestNilServiceGuardsDoNotPanic(t *testing.T) {
	var s *Service
	if _, err := s.CreateEndpoint(context.Background(), 1, "", "https://example.com/v1/", nil, nil); err == nil {
		t.Error("nil Service CreateEndpoint accepted")
	}
	if _, err := s.ListEndpoints(context.Background(), 1); err == nil {
		t.Error("nil Service ListEndpoints accepted")
	}
	if _, err := s.GetEndpoint(context.Background(), 1, 1); !errors.Is(err, db.ErrNotFound) {
		t.Errorf("nil Service GetEndpoint err = %v, want not_found", err)
	}
	if _, err := s.UpdateEndpoint(context.Background(), 1, 1, nil, nil, nil, nil); !errors.Is(err, db.ErrNotFound) {
		t.Errorf("nil Service UpdateEndpoint err = %v, want not_found", err)
	}
	if err := s.DeleteEndpoint(context.Background(), 1, 1); !errors.Is(err, db.ErrNotFound) {
		t.Errorf("nil Service DeleteEndpoint err = %v, want not_found", err)
	}
	if _, err := s.CreateEndpointKey(context.Background(), 1, 1, []byte("x"), nil, nil); err == nil {
		t.Error("nil Service CreateEndpointKey accepted")
	}
	if _, err := s.ListEndpointKeys(context.Background(), 1, 1); !errors.Is(err, db.ErrNotFound) {
		t.Errorf("nil Service ListEndpointKeys err = %v, want not_found", err)
	}
	if _, err := s.UpdateEndpointKey(context.Background(), 1, 1, 1, nil, nil); !errors.Is(err, db.ErrNotFound) {
		t.Errorf("nil Service UpdateEndpointKey err = %v, want not_found", err)
	}
	if err := s.DeleteEndpointKey(context.Background(), 1, 1, 1); !errors.Is(err, db.ErrNotFound) {
		t.Errorf("nil Service DeleteEndpointKey err = %v, want not_found", err)
	}
}

// --- helpers ----------------------------------------------------------------

func strPtr(s string) *string { return &s }
func intPtr(i int) *int       { return &i }
func boolPtr(b bool) *bool    { return &b }

func toggleLastBase64(s string) string {
	if s == "" {
		return s
	}
	b := []byte(s)
	if b[len(b)-1] == 'A' {
		b[len(b)-1] = 'B'
	} else {
		b[len(b)-1] = 'A'
	}
	return string(b)
}
