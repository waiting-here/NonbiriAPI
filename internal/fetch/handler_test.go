package fetch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/egress"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
)

// handlerFixture wraps a fetchFixture with the mounted HTTP handler. The
// identity resolver reads X-Test-Identity so tests can switch callers.
type handlerFixture struct {
	*fetchFixture
	handler http.Handler
}

func newHandlerFixture(t *testing.T) *handlerFixture {
	t.Helper()
	inner := newFetchFixture(t, nil)
	h := NewHandler(HandlerDeps{
		Fetcher: inner.fetcher,
		Store:   inner.store,
		Identity: func(r *http.Request) (int64, error) {
			var uid int64
			if _, err := fmt.Sscanf(r.Header.Get("X-Test-Identity"), "%d", &uid); err != nil || uid <= 0 {
				return 0, errors.New("no identity")
			}
			return uid, nil
		},
	})
	return &handlerFixture{fetchFixture: inner, handler: h}
}

func doFetchRequest(t *testing.T, h http.Handler, method, path, identity string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	if identity != "" {
		req.Header.Set("X-Test-Identity", identity)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func decodeFetchBody(t *testing.T, rec *httptest.ResponseRecorder, dst any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), dst); err != nil {
		t.Fatalf("body %q is not JSON: %v", rec.Body.String(), err)
	}
}

// TestHandlerListModels asserts GET models returns the cache metadata for an
// owned combo, 404 for cross-user/missing/wrong-endpoint combos, and 401
// without identity.
func TestHandlerListModels(t *testing.T) {
	f := newHandlerFixture(t)
	uid := f.seedUser(t, "list-owner")
	ep, err := f.store.CreateEndpoint(context.Background(), uid, "openai-compatible", f.upstream.URL, "", true, 1)
	if err != nil {
		t.Fatalf("create endpoint: %v", err)
	}
	key, err := f.store.CreateEndpointKey(context.Background(), uid, ep.ID, []byte("sk-list"), "h", "t", "n", true, 1)
	if err != nil {
		t.Fatalf("create key: %v", err)
	}
	if err := f.store.ReplaceFetchedModels(context.Background(), uid, ep.ID, key.ID,
		[]db.FetchedModel{{EndpointKeyID: key.ID, UpstreamModelID: "gpt-4o", Provider: "system", Status: "ok", FetchedAt: 42}}, 42); err != nil {
		t.Fatalf("seed cache: %v", err)
	}
	bob := f.seedUser(t, "list-bob")

	path := fmt.Sprintf("/api/endpoints/%d/keys/%d/models", ep.ID, key.ID)
	rec := doFetchRequest(t, f.handler, http.MethodGet, path, fmt.Sprint(uid))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("cache-control = %q", got)
	}
	var list []map[string]any
	decodeFetchBody(t, rec, &list)
	if len(list) != 1 {
		t.Fatalf("list = %v", list)
	}
	if list[0]["upstream_model_id"] != "gpt-4o" || list[0]["provider"] != "system" ||
		list[0]["status"] != "ok" || list[0]["fetched_at"] != float64(42) {
		t.Errorf("row = %v", list[0])
	}

	// Cross-user / wrong endpoint / missing key: indistinguishable 404.
	for name, idPath := range map[string]string{
		"cross user":     fmt.Sprintf("/api/endpoints/%d/keys/%d/models", ep.ID, key.ID),
		"wrong endpoint": fmt.Sprintf("/api/endpoints/%d/keys/%d/models", ep.ID+999, key.ID),
		"missing key":    fmt.Sprintf("/api/endpoints/%d/keys/%d/models", ep.ID, key.ID+999),
	} {
		rec := doFetchRequest(t, f.handler, http.MethodGet, idPath, fmt.Sprint(bob))
		assertFetchErr(t, rec, http.StatusNotFound, httperr.CodeNotFound)
		_ = name
	}

	// Empty cache for an owned combo: 200 [].
	key2, err := f.store.CreateEndpointKey(context.Background(), uid, ep.ID, []byte("sk-list-2"), "h", "t", "n", true, 2)
	if err != nil {
		t.Fatalf("create key2: %v", err)
	}
	rec = doFetchRequest(t, f.handler, http.MethodGet,
		fmt.Sprintf("/api/endpoints/%d/keys/%d/models", ep.ID, key2.ID), fmt.Sprint(uid))
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Errorf("empty combo: status=%d body=%s", rec.Code, rec.Body.String())
	}

	// Unauthenticated / invalid id.
	rec = doFetchRequest(t, f.handler, http.MethodGet, path, "")
	assertFetchErr(t, rec, http.StatusUnauthorized, httperr.CodeUnauthorized)
	rec = doFetchRequest(t, f.handler, http.MethodGet, "/api/endpoints/abc/keys/1/models", fmt.Sprint(uid))
	assertFetchErr(t, rec, http.StatusBadRequest, httperr.CodeInvalidRequest)
}

// TestHandlerRefresh asserts POST refresh returns 202 immediately, performs
// the fetch on the pool, and honors ownership (404) and disabled (404) gates
// without upstream work.
func TestHandlerRefresh(t *testing.T) {
	f := newHandlerFixture(t)
	f.setHandler(modelsHandler("gpt-4o"))
	uid, epID, keyID := f.seedCombo(t, f.upstream.URL)
	bob := f.seedUser(t, "refresh-bob")

	refreshPath := fmt.Sprintf("/api/endpoints/%d/keys/%d/models/refresh", epID, keyID)
	rec := doFetchRequest(t, f.handler, http.MethodPost, refreshPath, fmt.Sprint(uid))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("refresh status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("cache-control = %q", got)
	}
	waitFor(t, func() bool { return f.hits.Load() == 1 }, "refresh fetch hit")
	rows, err := f.store.ListFetchedModels(context.Background(), uid, epID, keyID)
	if err != nil || len(rows) != 1 || rows[0].UpstreamModelID != "gpt-4o" {
		t.Errorf("cache after refresh = %+v err=%v", rows, err)
	}

	// Cross-user refresh: 404, no upstream work.
	rec = doFetchRequest(t, f.handler, http.MethodPost, refreshPath, fmt.Sprint(bob))
	assertFetchErr(t, rec, http.StatusNotFound, httperr.CodeNotFound)
	if got := f.hits.Load(); got != 1 {
		t.Errorf("cross-user refresh hit upstream: hits=%d", got)
	}

	// Disabled key refresh: 404, no upstream work.
	if _, err := f.store.UpdateEndpointKey(context.Background(), uid, epID, keyID, nil, boolPtr(false), 3); err != nil {
		t.Fatalf("disable key: %v", err)
	}
	rec = doFetchRequest(t, f.handler, http.MethodPost, refreshPath, fmt.Sprint(uid))
	assertFetchErr(t, rec, http.StatusNotFound, httperr.CodeNotFound)
	if got := f.hits.Load(); got != 1 {
		t.Errorf("disabled refresh hit upstream: hits=%d", got)
	}

	// Missing key: 404.
	rec = doFetchRequest(t, f.handler, http.MethodPost,
		fmt.Sprintf("/api/endpoints/%d/keys/%d/models/refresh", epID, keyID+999), fmt.Sprint(uid))
	assertFetchErr(t, rec, http.StatusNotFound, httperr.CodeNotFound)

	// Unauthenticated: 401.
	rec = doFetchRequest(t, f.handler, http.MethodPost, refreshPath, "")
	assertFetchErr(t, rec, http.StatusUnauthorized, httperr.CodeUnauthorized)
}

// TestHandlerRefreshFrequencyLimit asserts the per-user refresh frequency
// limiter returns 429 and never triggers upstream work beyond the admitted
// fetches.
func TestHandlerRefreshFrequencyLimit(t *testing.T) {
	f := newFetchFixture(t, func(cfg *FetcherConfig, _ *egress.StackOptions) {
		cfg.RefreshPerUserPerMinute = 1
	})
	var uid int64
	h := NewHandler(HandlerDeps{
		Fetcher:  f.fetcher,
		Store:    f.store,
		Identity: func(r *http.Request) (int64, error) { return uid, nil },
	})
	f.setHandler(f.blockHandler)
	uid = f.seedUser(t, "rl-user")

	_, ep1, k1 := f.seedComboForUser(t, uid, f.upstream.URL)
	_, ep2, k2 := f.seedComboForUser(t, uid, f.upstream.URL)

	refresh := func(ep, k int64) *httptest.ResponseRecorder {
		return doFetchRequest(t, h, http.MethodPost,
			fmt.Sprintf("/api/endpoints/%d/keys/%d/models/refresh", ep, k), "")
	}

	// First refresh consumes the single per-user slot.
	if rec := refresh(ep1, k1); rec.Code != http.StatusAccepted {
		t.Fatalf("first refresh = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
	waitFor(t, func() bool { return f.hits.Load() == 1 }, "first refresh hit")

	// A second refresh within the window is rate-limited; the combo is owned
	// and healthy, so the 429 is purely the frequency bound.
	for i := 0; i < 2; i++ {
		rec := refresh(ep2, k2)
		assertFetchErr(t, rec, http.StatusTooManyRequests, httperr.CodeRateLimited)
	}
	if got := f.hits.Load(); got != 1 {
		t.Errorf("rate-limited refreshes hit upstream: hits=%d", got)
	}

	f.release()
	waitFor(t, func() bool { return f.fetcher.pool.Pending() == 0 }, "pool drain")
	if got := f.hits.Load(); got != 1 {
		t.Errorf("hits after release = %d, want 1 (only the admitted fetch ran)", got)
	}
}

// TestHandlerRefreshQueueBound asserts the bounded pool surfaces as 429 when
// the queue is full, without unbounded goroutines.
func TestHandlerRefreshQueueBound(t *testing.T) {
	f := newFetchFixture(t, func(cfg *FetcherConfig, _ *egress.StackOptions) {
		cfg.Workers = 1
		cfg.QueueSize = 1
	})
	var uid int64
	h := NewHandler(HandlerDeps{
		Fetcher:  f.fetcher,
		Store:    f.store,
		Identity: func(r *http.Request) (int64, error) { return uid, nil },
	})
	f.setHandler(f.blockHandler)
	uid = f.seedUser(t, "q-user")

	_, ep1, k1 := f.seedComboForUser(t, uid, f.upstream.URL)
	_, ep2, k2 := f.seedComboForUser(t, uid, f.upstream.URL)
	_, ep3, k3 := f.seedComboForUser(t, uid, f.upstream.URL)

	refresh := func(ep, k int64) *httptest.ResponseRecorder {
		return doFetchRequest(t, h, http.MethodPost,
			fmt.Sprintf("/api/endpoints/%d/keys/%d/models/refresh", ep, k), "")
	}

	if rec := refresh(ep1, k1); rec.Code != http.StatusAccepted {
		t.Fatalf("first refresh = %d, want 202", rec.Code)
	}
	waitFor(t, func() bool { return f.hits.Load() == 1 }, "first refresh hit")
	if rec := refresh(ep2, k2); rec.Code != http.StatusAccepted {
		t.Fatalf("second refresh = %d, want 202 (queued)", rec.Code)
	}
	// Queue (size 1) is full: one running, one queued.
	rec := refresh(ep3, k3)
	assertFetchErr(t, rec, http.StatusTooManyRequests, httperr.CodeRateLimited)
	if got := f.hits.Load(); got != 1 {
		t.Errorf("busy refresh hit upstream: hits=%d", got)
	}

	f.release()
	waitFor(t, func() bool { return f.hits.Load() == 2 }, "both admitted fetches complete")
}

// TestHandlerRefreshClosedServiceUnavailable asserts a refresh during
// shutdown fails closed with 503, not a 202 that never fetches.
func TestHandlerRefreshClosedServiceUnavailable(t *testing.T) {
	f := newFetchFixture(t, nil)
	var uid int64
	h := NewHandler(HandlerDeps{
		Fetcher:  f.fetcher,
		Store:    f.store,
		Identity: func(r *http.Request) (int64, error) { return uid, nil },
	})
	uid, epID, keyID := f.seedCombo(t, f.upstream.URL)
	if err := f.fetcher.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	rec := doFetchRequest(t, h, http.MethodPost,
		fmt.Sprintf("/api/endpoints/%d/keys/%d/models/refresh", epID, keyID), "")
	assertFetchErr(t, rec, http.StatusServiceUnavailable, httperr.CodeServiceUnavailable)
}

func assertFetchErr(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, status, rec.Body.String())
	}
	if code == "" {
		return
	}
	var env httperr.Envelope
	decodeFetchBody(t, rec, &env)
	if env.Error.Code != code {
		t.Errorf("code = %q, want %q; body=%s", env.Error.Code, code, rec.Body.String())
	}
}
