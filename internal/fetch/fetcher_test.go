package fetch

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/config"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbtest"
	"github.com/waiting-here/NonbiriAPI/internal/egress"
	"github.com/waiting-here/NonbiriAPI/internal/endpoint"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

// testResolver answers every hostname with a fixed public address, so
// AddSelfOrigins can register the fake station names without touching the
// real resolver (the upstream itself is dialed as an IP literal and never
// consults this resolver).
type testResolver struct{ addrs []netip.Addr }

func (r *testResolver) LookupNetIP(ctx context.Context, _ string, _ string) ([]netip.Addr, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return append([]netip.Addr(nil), r.addrs...), nil
}

// fetchFixture bundles a Fetcher with a real egress Stack, a real Store, and
// a configurable httptest upstream, so tests exercise the shared outbound
// boundary (DNS/SSRF/redirect/proxy/timeout/gate/cancel) end to end.
type fetchFixture struct {
	t        *testing.T
	store    *db.Store
	vault    *secret.Vault
	stack    *egress.Stack
	fetcher  *Fetcher
	upstream *httptest.Server

	handlerMu  sync.Mutex
	handler    http.HandlerFunc
	hits       atomic.Int64
	seedSeq    atomic.Int64
	lastAuth   atomic.Value // string
	lastAccept atomic.Value // string

	releaseMu sync.Mutex
	released  bool
	releaseCh chan struct{}
}

// newFetchFixture starts an upstream server whose behavior is swapped per
// test via setHandler. The egress stack allowlists only the upstream origin.
func newFetchFixture(t *testing.T, mutate func(*FetcherConfig, *egress.StackOptions)) *fetchFixture {
	t.Helper()
	f := &fetchFixture{t: t, releaseCh: make(chan struct{})}
	f.upstream = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.hits.Add(1)
		if r.Header.Get("Authorization") != "" {
			f.lastAuth.Store(r.Header.Get("Authorization"))
		}
		f.lastAccept.Store(r.Header.Get("Accept"))
		f.handlerMu.Lock()
		h := f.handler
		f.handlerMu.Unlock()
		if h != nil {
			h(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[]}`))
	}))
	t.Cleanup(func() {
		f.release()
		f.upstream.Close()
	})

	vaultKey := bytes.Repeat([]byte{0x5a}, secret.MasterKeyBytes)
	vault, err := secret.New(vaultKey)
	clear(vaultKey)
	if err != nil {
		t.Fatalf("vault: %v", err)
	}
	t.Cleanup(func() { _ = vault.Close() })
	f.vault = vault

	path := filepath.Join(t.TempDir(), "fetch.db")
	dbtest.EnsureOwnerOnlyParent(t, path)
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatalf("db open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	f.store = store

	stackOpts := egress.StackOptions{
		AllowedOrigins: []string{f.upstream.URL},
		RequestTimeout: 5 * time.Second,
		Resolver: &testResolver{
			addrs: []netip.Addr{netip.MustParseAddr("93.184.216.34")},
		},
	}
	cfg := FetcherConfig{
		Store:    store,
		Secrets:  vault,
		Registry: endpoint.NewRegistry(),
		Now:      func() int64 { return 1000 },
	}
	if mutate != nil {
		mutate(&cfg, &stackOpts)
	}
	stack, err := egress.NewStack(stackOpts)
	if err != nil {
		t.Fatalf("egress stack: %v", err)
	}
	t.Cleanup(stack.CloseIdleConnections)
	if err := stack.AddSelfOrigins(context.Background(), &config.Config{
		SiteBaseURL: "https://gateway.example",
		UserHost:    "gateway.example",
		AdminHost:   "admin.gateway.example",
		ListenAddr:  "127.0.0.1:1",
	}); err != nil {
		t.Fatalf("self origins: %v", err)
	}
	f.stack = stack
	cfg.Stack = stack

	fetcher, err := NewFetcher(cfg)
	if err != nil {
		t.Fatalf("fetcher: %v", err)
	}
	t.Cleanup(func() { _ = fetcher.Close() })
	f.fetcher = fetcher
	return f
}

// setHandler swaps the upstream behavior.
func (f *fetchFixture) setHandler(h http.HandlerFunc) {
	f.handlerMu.Lock()
	f.handler = h
	f.handlerMu.Unlock()
}

// blockHandler blocks every request until the fixture is released.
func (f *fetchFixture) blockHandler(w http.ResponseWriter, r *http.Request) {
	select {
	case <-f.releaseCh:
	case <-r.Context().Done():
	}
}

func (f *fetchFixture) release() {
	f.releaseMu.Lock()
	defer f.releaseMu.Unlock()
	if !f.released {
		close(f.releaseCh)
		f.released = true
	}
}

// seedCombo creates a user, an endpoint on the upstream URL, and an enabled
// key, returning (userID, endpointID, keyID).
func (f *fetchFixture) seedCombo(t *testing.T, baseURL string) (int64, int64, int64) {
	t.Helper()
	uid := f.seedUser(t, "user-"+fmt.Sprint(f.seedSeq.Add(1)))
	return f.seedComboForUser(t, uid, baseURL)
}

// seedComboForUser creates an endpoint on the upstream URL and an enabled key
// for an existing user, returning (userID, endpointID, keyID).
func (f *fetchFixture) seedComboForUser(t *testing.T, uid int64, baseURL string) (int64, int64, int64) {
	t.Helper()
	ep, err := f.store.CreateEndpoint(context.Background(), uid, "openai-compatible", baseURL, "", true, 1)
	if err != nil {
		t.Fatalf("create endpoint: %v", err)
	}
	k, err := f.store.CreateEndpointKey(context.Background(), uid, ep.ID, []byte("sk-upstream-secret-0123456789"), "sk-up", "6789", "note", true, 1)
	if err != nil {
		t.Fatalf("create key: %v", err)
	}
	return uid, ep.ID, k.ID
}

// seedCombos creates n distinct (endpoint, key) combos for one user on the
// upstream URL, returning the (userID, endpointID, keyID) triples.
func (f *fetchFixture) seedCombos(t *testing.T, uid int64, n int) [][3]int64 {
	t.Helper()
	out := make([][3]int64, n)
	for i := 0; i < n; i++ {
		u, ep, k := f.seedComboForUser(t, uid, f.upstream.URL)
		out[i] = [3]int64{u, ep, k}
	}
	return out
}

func (f *fetchFixture) seedUser(t *testing.T, discordID string) int64 {
	t.Helper()
	res, err := f.store.DB().Exec(
		`INSERT INTO users (discord_id, username, created_at, updated_at) VALUES (?, 'tester', 1, 1)`, discordID)
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("user id: %v", err)
	}
	return id
}

func (f *fetchFixture) cacheRows(t *testing.T, uid, epID, keyID int64) []db.FetchedModel {
	t.Helper()
	rows, err := f.store.ListFetchedModels(context.Background(), uid, epID, keyID)
	if err != nil {
		t.Fatalf("list cache: %v", err)
	}
	return rows
}

func (f *fetchFixture) flag(t *testing.T, uid, epID int64) (failed bool, at int64) {
	t.Helper()
	ep, err := f.store.GetEndpoint(context.Background(), uid, epID)
	if err != nil {
		t.Fatalf("get endpoint: %v", err)
	}
	return ep.ModelFetchFailed, ep.ModelFetchFailedAt
}

func (f *fetchFixture) issueCount(t *testing.T, uid int64) int {
	t.Helper()
	var n int
	if err := f.store.DB().QueryRow(`SELECT COUNT(*) FROM user_issues WHERE user_id=?`, uid).Scan(&n); err != nil {
		t.Fatalf("count issues: %v", err)
	}
	return n
}

func (f *fetchFixture) issueMessage(t *testing.T, uid int64) string {
	t.Helper()
	var msg string
	if err := f.store.DB().QueryRow(`SELECT message FROM user_issues WHERE user_id=? ORDER BY id DESC LIMIT 1`, uid).Scan(&msg); err != nil {
		t.Fatalf("read issue: %v", err)
	}
	return msg
}

func waitFor(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() && time.Now().Before(deadline) {
		time.Sleep(3 * time.Millisecond)
	}
	if !cond() {
		t.Fatalf("timeout waiting for %s", what)
	}
}

func boolPtr(v bool) *bool { return &v }

func modelsHandler(models ...string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var b strings.Builder
		b.WriteString(`{"object":"list","data":[`)
		for i, m := range models {
			if i > 0 {
				b.WriteByte(',')
			}
			fmt.Fprintf(&b, `{"id":%q,"object":"model","owned_by":"system"}`, m)
		}
		b.WriteString(`]}`)
		_, _ = w.Write([]byte(b.String()))
	}
}

// TestFetchSuccessReplacesCacheAndClearsFlag is the happy path through the
// real stack: the upstream sees the Bearer key and Accept header, the cache
// is replaced, the flag is cleared, and no issue is written.
func TestFetchSuccessReplacesCacheAndClearsFlag(t *testing.T) {
	f := newFetchFixture(t, nil)
	f.setHandler(modelsHandler("gpt-4o", "gpt-4o-mini"))
	uid, epID, keyID := f.seedCombo(t, f.upstream.URL)

	// Pre-mark the endpoint failed to prove success clears it.
	if _, err := f.store.DB().Exec(`UPDATE endpoints SET model_fetch_failed=1, model_fetch_failed_at=1 WHERE id=?`, epID); err != nil {
		t.Fatalf("pre-flag: %v", err)
	}

	if err := f.fetcher.fetchOne(context.Background(), uid, epID, keyID); err != nil {
		t.Fatalf("fetchOne: %v", err)
	}
	rows := f.cacheRows(t, uid, epID, keyID)
	if len(rows) != 2 || rows[0].UpstreamModelID != "gpt-4o" || rows[1].UpstreamModelID != "gpt-4o-mini" {
		t.Errorf("cache = %+v", rows)
	}
	for _, m := range rows {
		if m.Provider != "system" || m.Status != "ok" || m.FetchedAt != 1000 {
			t.Errorf("row = %+v", m)
		}
	}
	failed, at := f.flag(t, uid, epID)
	if failed || at != 0 {
		t.Errorf("flag after success = (%v, %d), want cleared", failed, at)
	}
	if got := f.issueCount(t, uid); got != 0 {
		t.Errorf("issues = %d, want 0", got)
	}
	if got := f.lastAuth.Load(); got != "Bearer sk-upstream-secret-0123456789" {
		t.Errorf("authorization = %q", got)
	}
	if got := f.lastAccept.Load(); got != "application/json" {
		t.Errorf("accept = %q", got)
	}
	if f.hits.Load() != 1 {
		t.Errorf("upstream hits = %d, want 1", f.hits.Load())
	}
}

// TestFetchFailureStatusClearsFlagsAndWritesBoundedIssue asserts a 500 with
// an upstream error body clears the prior cache, sets the flag, and writes a
// bounded issue that carries neither the key nor the full body.
func TestFetchFailureStatusClearsFlagsAndWritesBoundedIssue(t *testing.T) {
	f := newFetchFixture(t, nil)
	f.setHandler(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"message":"upstream exploded with a very long explanation ` +
			strings.Repeat("x", 2000) + `"}}`))
	})
	uid, epID, keyID := f.seedCombo(t, f.upstream.URL)
	if err := f.store.ReplaceFetchedModels(context.Background(), uid, epID, keyID,
		[]db.FetchedModel{{EndpointKeyID: keyID, UpstreamModelID: "gpt-4o", Provider: "s", Status: "ok"}}, 1); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	if err := f.fetcher.fetchOne(context.Background(), uid, epID, keyID); err != nil {
		t.Fatalf("fetchOne: %v", err)
	}
	if rows := f.cacheRows(t, uid, epID, keyID); len(rows) != 0 {
		t.Errorf("cache after failure = %+v, want empty", rows)
	}
	failed, at := f.flag(t, uid, epID)
	if !failed || at != 1000 {
		t.Errorf("flag = (%v, %d), want (true, 1000)", failed, at)
	}
	if got := f.issueCount(t, uid); got != 1 {
		t.Fatalf("issues = %d, want 1", got)
	}
	msg := f.issueMessage(t, uid)
	if strings.Contains(msg, "sk-upstream-secret") {
		t.Errorf("issue leaks the key: %q", msg)
	}
	if strings.Contains(msg, strings.Repeat("x", 2000)) {
		t.Errorf("issue contains the full upstream body")
	}
	if !strings.Contains(msg, "upstream returned status 500") {
		t.Errorf("issue = %q, want status summary", msg)
	}
	if len(msg) > maxIssueDiagBytes+64 {
		t.Errorf("issue length %d exceeds diag bound", len(msg))
	}
}

// TestFetchFailureBodyEchoingKeyIsWithheld asserts the diagnostic guard: an
// upstream that echoes the Authorization value back in its error body must
// not have that text surface in the issue.
func TestFetchFailureBodyEchoingKeyIsWithheld(t *testing.T) {
	f := newFetchFixture(t, nil)
	f.setHandler(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"error":"invalid key: ` + r.Header.Get("Authorization") + `"}`))
	})
	uid, epID, keyID := f.seedCombo(t, f.upstream.URL)

	if err := f.fetcher.fetchOne(context.Background(), uid, epID, keyID); err != nil {
		t.Fatalf("fetchOne: %v", err)
	}
	msg := f.issueMessage(t, uid)
	if strings.Contains(msg, "sk-upstream-secret") {
		t.Errorf("issue leaks the echoed key: %q", msg)
	}
}

// TestFetchRejectsRedirect asserts egress's redirect block surfaces as a
// bounded fetch failure.
func TestFetchRejectsRedirect(t *testing.T) {
	f := newFetchFixture(t, nil)
	f.setHandler(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, f.upstream.URL+"/elsewhere", http.StatusFound)
	})
	uid, epID, keyID := f.seedCombo(t, f.upstream.URL)

	if err := f.fetcher.fetchOne(context.Background(), uid, epID, keyID); err != nil {
		t.Fatalf("fetchOne: %v", err)
	}
	failed, _ := f.flag(t, uid, epID)
	if !failed {
		t.Errorf("redirect did not flag the endpoint")
	}
	if got := f.issueCount(t, uid); got != 1 {
		t.Errorf("issues = %d, want 1", got)
	}
	msg := f.issueMessage(t, uid)
	if strings.Contains(msg, f.upstream.URL) {
		t.Errorf("issue echoes the URL: %q", msg)
	}
}

// TestFetchRejectsSSRFWithoutAllowlist asserts a loopback endpoint without an
// allowlist entry fails at the egress client boundary (no dial, no upstream),
// and is recorded as a fetch failure.
func TestFetchRejectsSSRFWithoutAllowlist(t *testing.T) {
	f := newFetchFixture(t, func(cfg *FetcherConfig, opts *egress.StackOptions) {
		opts.AllowedOrigins = nil
	})
	// 127.0.0.1:9 is a loopback port that is certainly not an allowed origin.
	uid, epID, keyID := f.seedCombo(t, "http://127.0.0.1:9")

	if err := f.fetcher.fetchOne(context.Background(), uid, epID, keyID); err != nil {
		t.Fatalf("fetchOne: %v", err)
	}
	failed, _ := f.flag(t, uid, epID)
	if !failed {
		t.Errorf("loopback endpoint without allowlist was not flagged")
	}
	if f.hits.Load() != 0 {
		t.Errorf("upstream hits = %d, want 0 (no dial)", f.hits.Load())
	}
	if got := f.issueCount(t, uid); got != 1 {
		t.Errorf("issues = %d, want 1", got)
	}
}

// TestFetchIgnoresEnvironmentProxy asserts the shared stack never inherits
// HTTP(S)_PROXY: with a bogus proxy set, the upstream is still dialed
// directly.
func TestFetchIgnoresEnvironmentProxy(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	t.Setenv("http_proxy", "http://127.0.0.1:1")
	t.Setenv("https_proxy", "http://127.0.0.1:1")
	f := newFetchFixture(t, nil)
	f.setHandler(modelsHandler("gpt-4o"))
	uid, epID, keyID := f.seedCombo(t, f.upstream.URL)

	if err := f.fetcher.fetchOne(context.Background(), uid, epID, keyID); err != nil {
		t.Fatalf("fetchOne: %v", err)
	}
	if f.hits.Load() != 1 {
		t.Errorf("upstream hits = %d, want 1 (proxy must be ignored)", f.hits.Load())
	}
	if rows := f.cacheRows(t, uid, epID, keyID); len(rows) != 1 {
		t.Errorf("cache = %+v", rows)
	}
}

// TestFetchDisabledKeyIsNoOp asserts a disabled endpoint/key never dials the
// upstream, never flags, and never writes an issue; the existing cache is
// left alone.
func TestFetchDisabledKeyIsNoOp(t *testing.T) {
	f := newFetchFixture(t, nil)
	f.setHandler(modelsHandler("gpt-4o"))
	uid, epID, keyID := f.seedCombo(t, f.upstream.URL)
	if err := f.store.ReplaceFetchedModels(context.Background(), uid, epID, keyID,
		[]db.FetchedModel{{EndpointKeyID: keyID, UpstreamModelID: "stale", Provider: "s", Status: "ok"}}, 1); err != nil {
		t.Fatalf("seed cache: %v", err)
	}
	// Disable the endpoint itself.
	if _, _, err := f.store.UpdateEndpoint(context.Background(), uid, epID, nil, nil, boolPtr(false), 2); err != nil {
		t.Fatalf("disable endpoint: %v", err)
	}

	if err := f.fetcher.fetchOne(context.Background(), uid, epID, keyID); err != nil {
		t.Fatalf("fetchOne: %v", err)
	}
	if f.hits.Load() != 0 {
		t.Errorf("upstream hits = %d, want 0", f.hits.Load())
	}
	if rows := f.cacheRows(t, uid, epID, keyID); len(rows) != 1 || rows[0].UpstreamModelID != "stale" {
		t.Errorf("cache changed for disabled combo: %+v", rows)
	}
	failed, _ := f.flag(t, uid, epID)
	if failed {
		t.Errorf("disabled combo flagged the endpoint")
	}
	if got := f.issueCount(t, uid); got != 0 {
		t.Errorf("issues = %d, want 0", got)
	}
}

// TestFetchCrossUserAndWrongEndpointNoUpstream asserts ownership is enforced
// before any dial: another user or a wrong endpoint id never reaches the
// upstream and writes nothing.
func TestFetchCrossUserAndWrongEndpointNoUpstream(t *testing.T) {
	f := newFetchFixture(t, nil)
	f.setHandler(modelsHandler("gpt-4o"))
	uid, epID, keyID := f.seedCombo(t, f.upstream.URL)
	bob := f.seedUser(t, "bob")

	for name, args := range map[string][3]int64{
		"cross user":     {bob, epID, keyID},
		"wrong endpoint": {uid, epID + 999, keyID},
		"missing key":    {uid, epID, keyID + 999},
	} {
		if err := f.fetcher.fetchOne(context.Background(), args[0], args[1], args[2]); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if f.hits.Load() != 0 {
		t.Errorf("upstream hits = %d, want 0", f.hits.Load())
	}
	failed, _ := f.flag(t, uid, epID)
	if failed {
		t.Errorf("ownership failures flagged the endpoint")
	}
	if got := f.issueCount(t, uid); got != 0 {
		t.Errorf("issues = %d, want 0", got)
	}
}

// TestFetchTamperedEnvelopeFailsClosed asserts a corrupted ciphertext (wrong
// master key or garbage bytes) fails without touching the upstream and never
// leaks envelope material into the issue.
func TestFetchTamperedEnvelopeFailsClosed(t *testing.T) {
	f := newFetchFixture(t, nil)
	f.setHandler(modelsHandler("gpt-4o"))
	uid, epID, keyID := f.seedCombo(t, f.upstream.URL)
	// Corrupt the stored envelope in place.
	if _, err := f.store.DB().Exec(`UPDATE endpoint_keys SET encrypted_secret=? WHERE id=?`,
		"nbsec:v1:aes-256-gcm:AAAAAAAAAAAAAAAA:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", keyID); err != nil {
		t.Fatalf("corrupt envelope: %v", err)
	}

	if err := f.fetcher.fetchOne(context.Background(), uid, epID, keyID); err != nil {
		t.Fatalf("fetchOne: %v", err)
	}
	if f.hits.Load() != 0 {
		t.Errorf("upstream hits = %d, want 0", f.hits.Load())
	}
	failed, _ := f.flag(t, uid, epID)
	if !failed {
		t.Errorf("tampered envelope did not flag the endpoint")
	}
	msg := f.issueMessage(t, uid)
	if strings.Contains(msg, "nbsec") || strings.Contains(msg, "AAAA") {
		t.Errorf("issue leaks envelope material: %q", msg)
	}
	if msg != "model fetch failed: credential unavailable" {
		t.Errorf("issue = %q, want opaque credential summary", msg)
	}
}

func TestFetchContextExchangeAndLegacyEnvelopeNeverDial(t *testing.T) {
	for _, attack := range []string{"user", "endpoint", "key", "origin", "legacy", "future", "oversized", "oversized-origin"} {
		attack := attack
		t.Run(attack, func(t *testing.T) {
			f := newFetchFixture(t, nil)
			f.setHandler(modelsHandler("must-not-be-reached"))
			uid, endpointID, keyID := f.seedCombo(t, f.upstream.URL)
			state, err := f.store.GetEndpointKeyFetchState(context.Background(), uid, endpointID, keyID)
			if err != nil {
				t.Fatal(err)
			}
			_, origin, err := egress.CanonicalEndpointTarget(state.BaseURL)
			if err != nil {
				t.Fatal(err)
			}

			wrongUser, wrongEndpoint, wrongKey, wrongOrigin := uid, endpointID, keyID, origin
			switch attack {
			case "user":
				wrongUser++
			case "endpoint":
				wrongEndpoint++
			case "key":
				wrongKey++
			case "origin":
				wrongOrigin = "https://context-exchange.example:443"
			}
			var ciphertext string
			switch attack {
			case "legacy":
				ciphertext, err = f.vault.Seal([]byte("legacy-runtime-fallback-marker"))
			case "future":
				ciphertext, err = f.store.GetEndpointKeyCiphertext(context.Background(), uid, endpointID, keyID)
				ciphertext = strings.Replace(ciphertext, ":v2:", ":v8:", 1)
			case "oversized":
				ciphertext = strings.Repeat("A", 129<<10)
			case "oversized-origin":
				ciphertext, err = f.store.GetEndpointKeyCiphertext(context.Background(), uid, endpointID, keyID)
				if _, updateErr := f.store.DB().Exec(`UPDATE endpoints SET base_url=? WHERE id=?`, strings.Repeat("a", 4097), endpointID); updateErr != nil {
					t.Fatal(updateErr)
				}
			default:
				credentialContext, contextErr := secret.NewEndpointKeyContext(wrongUser, wrongEndpoint, wrongKey, wrongOrigin)
				if contextErr != nil {
					t.Fatal(contextErr)
				}
				ciphertext, err = f.vault.SealForContext([]byte("context-exchange-secret-marker"), credentialContext)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.store.DB().Exec(`UPDATE endpoint_keys SET encrypted_secret=? WHERE id=?`, ciphertext, keyID); err != nil {
				t.Fatal(err)
			}

			if err := f.fetcher.fetchOne(context.Background(), uid, endpointID, keyID); err != nil {
				t.Fatalf("fetchOne: %v", err)
			}
			if hits := f.hits.Load(); hits != 0 {
				t.Fatalf("wrong context dialed upstream %d times", hits)
			}
			if auth := f.lastAuth.Load(); auth != nil {
				t.Fatal("wrong context constructed an authorization header")
			}
			message := f.issueMessage(t, uid)
			if message != "model fetch failed: credential unavailable" {
				t.Fatalf("issue=%q, want opaque credential failure", message)
			}
			for _, marker := range []string{ciphertext, origin, wrongOrigin, "context-exchange", "legacy-runtime"} {
				if strings.Contains(message, marker) {
					t.Fatal("credential failure exposed protected context")
				}
			}
		})
	}
}

// TestFetchTruncatedBodyIsProtocolFailure asserts a response beyond
// MaxModelsBodyBytes fails the fetch (never a partial cache).
func TestFetchTruncatedBodyIsProtocolFailure(t *testing.T) {
	f := newFetchFixture(t, nil)
	f.setHandler(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"`))
		_, _ = w.Write(bytes.Repeat([]byte("a"), MaxModelsBodyBytes))
		_, _ = w.Write([]byte(`"}]}`))
	})
	uid, epID, keyID := f.seedCombo(t, f.upstream.URL)

	if err := f.fetcher.fetchOne(context.Background(), uid, epID, keyID); err != nil {
		t.Fatalf("fetchOne: %v", err)
	}
	failed, _ := f.flag(t, uid, epID)
	if !failed {
		t.Errorf("truncated body did not flag the endpoint")
	}
	if rows := f.cacheRows(t, uid, epID, keyID); len(rows) != 0 {
		t.Errorf("truncated body cached %d rows", len(rows))
	}
}

// TestFetchBadProtocolShapeFails asserts a 200 with malformed JSON (duplicate
// id) is a protocol failure: HTTP 200 alone is not success.
func TestFetchBadProtocolShapeFails(t *testing.T) {
	f := newFetchFixture(t, nil)
	f.setHandler(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"gpt-4o"},{"id":"gpt-4o"}]}`))
	})
	uid, epID, keyID := f.seedCombo(t, f.upstream.URL)

	if err := f.fetcher.fetchOne(context.Background(), uid, epID, keyID); err != nil {
		t.Fatalf("fetchOne: %v", err)
	}
	failed, _ := f.flag(t, uid, epID)
	if !failed {
		t.Errorf("bad shape did not flag the endpoint")
	}
	if rows := f.cacheRows(t, uid, epID, keyID); len(rows) != 0 {
		t.Errorf("bad shape cached %d rows", len(rows))
	}
}

// TestFetchCancellationIsNotRecordedAsFailure asserts a cancelled fetch aborts
// upstream work without flagging the endpoint or writing an issue: shutdown
// cancellation is not an upstream failure.
func TestFetchCancellationIsNotRecordedAsFailure(t *testing.T) {
	f := newFetchFixture(t, nil)
	f.setHandler(f.blockHandler)
	uid, epID, keyID := f.seedCombo(t, f.upstream.URL)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- f.fetcher.fetchOne(ctx, uid, epID, keyID)
	}()
	// Wait until the upstream handler is blocked, then cancel.
	waitFor(t, func() bool { return f.hits.Load() == 1 }, "upstream hit")
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("fetchOne: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("fetchOne did not return after cancellation")
	}
	failed, _ := f.flag(t, uid, epID)
	if failed {
		t.Errorf("cancelled fetch flagged the endpoint")
	}
	if got := f.issueCount(t, uid); got != 0 {
		t.Errorf("cancelled fetch wrote %d issues", got)
	}
}

// TestFetchConcurrencyGateSerializesPerEndpoint asserts the shared egress
// gate really bounds concurrent fetches for one canonical base URL.
func TestFetchConcurrencyGateSerializesPerEndpoint(t *testing.T) {
	f := newFetchFixture(t, func(cfg *FetcherConfig, opts *egress.StackOptions) {
		opts.Concurrency = egress.ConcurrencyLimits{Global: 1, PerEndpoint: 1}
	})
	f.setHandler(f.blockHandler)
	uid, epID, keyID := f.seedCombo(t, f.upstream.URL)
	uid2, epID2, keyID2 := f.seedCombo(t, f.upstream.URL)

	done1 := make(chan error, 1)
	done2 := make(chan error, 1)
	go func() { done1 <- f.fetcher.fetchOne(context.Background(), uid, epID, keyID) }()
	// Give the first fetch time to acquire the gate and hit the upstream.
	waitFor(t, func() bool { return f.hits.Load() == 1 }, "first upstream hit")
	go func() { done2 <- f.fetcher.fetchOne(context.Background(), uid2, epID2, keyID2) }()
	// The second must wait on the gate: give it time to fail if the gate were
	// not shared.
	time.Sleep(100 * time.Millisecond)
	if got := f.hits.Load(); got != 1 {
		t.Fatalf("hits while first blocked = %d, want 1 (gate not shared?)", got)
	}
	f.release()
	for i, done := range []chan error{done1, done2} {
		if err := <-done; err != nil {
			t.Fatalf("fetch %d: %v", i, err)
		}
	}
	if got := f.hits.Load(); got != 2 {
		t.Errorf("hits after release = %d, want 2", got)
	}
}

// TestFetcherHookAsyncAndBusy asserts the FetchHook contract: submission
// returns immediately while the fetch runs on the pool, and a full queue
// yields ErrPoolBusy instead of unbounded work.
func TestFetcherHookAsyncAndBusy(t *testing.T) {
	f := newFetchFixture(t, func(cfg *FetcherConfig, opts *egress.StackOptions) {
		cfg.Workers = 1
		cfg.QueueSize = 1
		opts.Concurrency = egress.ConcurrencyLimits{Global: 1, PerEndpoint: 1}
	})
	f.setHandler(f.blockHandler)
	uid, epID, keyID := f.seedCombo(t, f.upstream.URL)
	uid2, epID2, keyID2 := f.seedCombo(t, f.upstream.URL)
	uid3, epID3, keyID3 := f.seedCombo(t, f.upstream.URL)

	start := time.Now()
	if err := f.fetcher.FetchModels(context.Background(), uid, epID, keyID); err != nil {
		t.Fatalf("hook submit 1: %v", err)
	}
	waitFor(t, func() bool { return f.hits.Load() == 1 }, "first hit")
	if err := f.fetcher.FetchModels(context.Background(), uid2, epID2, keyID2); err != nil {
		t.Fatalf("hook submit 2: %v", err)
	}
	// Third submit: queue (size 1) is full (job1 running, job2 queued).
	if err := f.fetcher.FetchModels(context.Background(), uid3, epID3, keyID3); !errors.Is(err, ErrPoolBusy) {
		t.Errorf("hook submit 3: err=%v, want busy", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("hook submission took %v; must not block on upstream", elapsed)
	}
	// Merged submit while running.
	if err := f.fetcher.FetchModels(context.Background(), uid, epID, keyID); err != nil {
		t.Errorf("hook merged submit: %v", err)
	}
	f.release()
	// Both jobs complete eventually; nothing beyond the pool bounds runs.
	deadline := time.Now().Add(5 * time.Second)
	for f.hits.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := f.hits.Load(); got != 2 {
		t.Errorf("hits = %d, want 2 (bounded pool)", got)
	}
}

// TestFetchHookAfterCloseErrors asserts submission after Close fails closed.
func TestFetchHookAfterCloseErrors(t *testing.T) {
	f := newFetchFixture(t, nil)
	uid, epID, keyID := f.seedCombo(t, f.upstream.URL)
	if err := f.fetcher.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := f.fetcher.FetchModels(context.Background(), uid, epID, keyID); !errors.Is(err, ErrPoolClosed) {
		t.Errorf("submit after close: err=%v, want closed", err)
	}
	// Idempotent close.
	if err := f.fetcher.Close(); err != nil {
		t.Errorf("second close: %v", err)
	}
}

// TestFetcherValidation asserts construction fails closed without required
// collaborators.
func TestFetcherValidation(t *testing.T) {
	f := newFetchFixture(t, nil)
	if _, err := NewFetcher(FetcherConfig{Store: nil, Stack: f.stack, Secrets: f.vault, Registry: endpoint.NewRegistry()}); err == nil {
		t.Errorf("nil store accepted")
	}
	if _, err := NewFetcher(FetcherConfig{Store: f.store, Stack: nil, Secrets: f.vault, Registry: endpoint.NewRegistry()}); err == nil {
		t.Errorf("nil stack accepted")
	}
	if _, err := NewFetcher(FetcherConfig{Store: f.store, Stack: f.stack, Secrets: nil, Registry: endpoint.NewRegistry()}); err == nil {
		t.Errorf("nil secrets accepted")
	}
	if _, err := NewFetcher(FetcherConfig{Store: f.store, Stack: f.stack, Secrets: f.vault, Registry: nil}); err == nil {
		t.Errorf("nil registry accepted")
	}
}

// TestFetchMountPath asserts /v1/models is joined under a base URL with a
// mount path.
func TestFetchMountPath(t *testing.T) {
	f := newFetchFixture(t, nil)
	f.setHandler(modelsHandler("m1"))
	uid, epID, keyID := f.seedCombo(t, f.upstream.URL+"/openai/v1")
	if err := f.fetcher.fetchOne(context.Background(), uid, epID, keyID); err != nil {
		t.Fatalf("fetchOne: %v", err)
	}
	if rows := f.cacheRows(t, uid, epID, keyID); len(rows) != 1 || rows[0].UpstreamModelID != "m1" {
		t.Errorf("mount-path cache = %+v", rows)
	}
}

// TestFetchVaultClosedFailsClosed asserts a closed vault (process shutdown
// edge) fails the fetch without touching the upstream.
func TestFetchVaultClosedFailsClosed(t *testing.T) {
	f := newFetchFixture(t, nil)
	f.setHandler(modelsHandler("gpt-4o"))
	uid, epID, keyID := f.seedCombo(t, f.upstream.URL)
	// The fixture's cleanup closes the vault; simulate shutdown by closing it
	// now. Reopen behavior is out of scope: this only proves the fail path.
	if err := f.vault.Close(); err != nil {
		t.Fatalf("close vault: %v", err)
	}
	if err := f.fetcher.fetchOne(context.Background(), uid, epID, keyID); err != nil {
		t.Fatalf("fetchOne: %v", err)
	}
	if f.hits.Load() != 0 {
		t.Errorf("upstream hits = %d, want 0", f.hits.Load())
	}
	failed, _ := f.flag(t, uid, epID)
	if !failed {
		t.Errorf("closed vault did not flag the endpoint")
	}
}

// TestJoinModelsURLContract locks the endpoint base-URL contract: the base
// URL carries the full API mount including the version segment, so only the
// resource path is appended. A base already ending in /v1 must never produce
// /v1/v1/models.
func TestJoinModelsURLContract(t *testing.T) {
	cases := []struct {
		base, want string
	}{
		{"https://api.example.com/v1", "https://api.example.com/v1/models"},
		{"https://api.example.com/v1/", "https://api.example.com/v1/models"},
		{"https://api.example.com/api/v1", "https://api.example.com/api/v1/models"},
		{"https://api.example.com", "https://api.example.com/models"},
	}
	for _, c := range cases {
		if got := joinModelsURL(c.base); got != c.want {
			t.Errorf("joinModelsURL(%q) = %q, want %q", c.base, got, c.want)
		}
	}
}

// TestFetcherSharedAdmissionAutoAndManual asserts that automatic and manual
// refreshes share one per-user admission budget: a burst of automatic fetches
// exhausts the per-user RPM, after which both an extra automatic fetch and a
// manual refresh are denied for that user, while a different user (its own
// budget) is still admitted. The pool per-user cap is raised so only the shared
// RPM binds.
func TestFetcherSharedAdmissionAutoAndManual(t *testing.T) {
	f := newFetchFixture(t, func(cfg *FetcherConfig, opts *egress.StackOptions) {
		cfg.Workers = 4
		cfg.QueueSize = 32
		cfg.PerUserCap = 32 // isolate the shared RPM bound from the pool cap
		opts.Concurrency = egress.ConcurrencyLimits{Global: 100, PerEndpoint: 100}
	})
	f.setHandler(f.blockHandler) // keep admitted jobs running so slots stay taken

	alice := f.seedUser(t, "alice")
	aCombos := f.seedCombos(t, alice, 11)
	ctx := context.Background()

	// 10 automatic fetches consume the full per-user admission budget.
	for i := 0; i < 10; i++ {
		c := aCombos[i]
		if err := f.fetcher.FetchModels(ctx, c[0], c[1], c[2]); err != nil {
			t.Fatalf("auto fetch[%d]: %v", i, err)
		}
	}
	// Budget exhausted: the 11th automatic fetch is rate-limited.
	c11 := aCombos[10]
	if err := f.fetcher.FetchModels(ctx, c11[0], c11[1], c11[2]); !errors.Is(err, ErrRefreshRateLimited) {
		t.Errorf("11th auto fetch: err=%v, want ErrRefreshRateLimited (shared budget exhausted)", err)
	}
	// Manual refresh shares the same budget: it is also denied for alice.
	c0 := aCombos[0]
	if err := f.fetcher.RefreshManual(ctx, c0[0], c0[1], c0[2]); !errors.Is(err, ErrRefreshRateLimited) {
		t.Errorf("manual refresh after exhaustion: err=%v, want ErrRefreshRateLimited", err)
	}
	// A different user has its own budget: its first fetch is admitted.
	bob := f.seedUser(t, "bob")
	bCombos := f.seedCombos(t, bob, 1)
	bc := bCombos[0]
	if err := f.fetcher.FetchModels(ctx, bc[0], bc[1], bc[2]); err != nil {
		t.Errorf("bob fetch: err=%v, want nil (independent per-user budget)", err)
	}
}

// TestFetcherPerUserPoolCap asserts the pool's per-user pending+running cap is
// honored through the Fetcher: with the RPM raised so it does not bind, one
// user's distinct fetches are capped and the overflow is busy, while another
// user is unaffected.
func TestFetcherPerUserPoolCap(t *testing.T) {
	f := newFetchFixture(t, func(cfg *FetcherConfig, opts *egress.StackOptions) {
		cfg.Workers = 4
		cfg.QueueSize = 32
		cfg.PerUserCap = 4
		cfg.RefreshPerUserPerMinute = 100 // isolate the pool per-user cap from the RPM
		opts.Concurrency = egress.ConcurrencyLimits{Global: 100, PerEndpoint: 100}
	})
	f.setHandler(f.blockHandler)

	alice := f.seedUser(t, "alice")
	aCombos := f.seedCombos(t, alice, 5)
	ctx := context.Background()
	for i := 0; i < 4; i++ {
		c := aCombos[i]
		if err := f.fetcher.FetchModels(ctx, c[0], c[1], c[2]); err != nil {
			t.Fatalf("auto fetch[%d]: %v", i, err)
		}
	}
	c5 := aCombos[4]
	if err := f.fetcher.FetchModels(ctx, c5[0], c5[1], c5[2]); !errors.Is(err, ErrPoolBusy) {
		t.Errorf("5th auto fetch: err=%v, want ErrPoolBusy (per-user pool cap)", err)
	}
	// alice's cap does not block bob.
	bob := f.seedUser(t, "bob")
	bCombos := f.seedCombos(t, bob, 1)
	bc := bCombos[0]
	if err := f.fetcher.FetchModels(ctx, bc[0], bc[1], bc[2]); err != nil {
		t.Errorf("bob fetch: err=%v, want nil (independent per-user cap)", err)
	}
}
