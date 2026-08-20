package maintenance

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// recorderHandler records the paths that reach the next handler, so a test can
// assert which requests passed the gate. It is safe for concurrent use.
type recorderHandler struct {
	mu    sync.Mutex
	paths []string
}

func (h *recorderHandler) ServeHTTP(_ http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	h.paths = append(h.paths, r.URL.Path)
	h.mu.Unlock()
}

func (h *recorderHandler) snapshot() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]string, len(h.paths))
	copy(out, h.paths)
	return out
}

func (h *recorderHandler) reset() {
	h.mu.Lock()
	h.paths = nil
	h.mu.Unlock()
}

func (h *recorderHandler) empty() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.paths) == 0
}

func newRequest(method, path string) *http.Request {
	return httptest.NewRequest(method, path, nil)
}

func decodeErr(t *testing.T, rec *httptest.ResponseRecorder) (code, source string) {
	t.Helper()
	var env struct {
		Error struct {
			Code   string `json:"code"`
			Source string `json:"source"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode error envelope: %v; body=%s", err, rec.Body.String())
	}
	return env.Error.Code, env.Error.Source
}

func assertRefused(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d want 503; body=%s", rec.Code, rec.Body.String())
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("Cache-Control=%q want no-store", cc)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("Content-Type=%q", ct)
	}
	code, source := decodeErr(t, rec)
	if code != "service_unavailable" {
		t.Fatalf("code=%q want service_unavailable", code)
	}
	if source != "platform" {
		t.Fatalf("source=%q want platform", source)
	}
}

func assertPassed(t *testing.T, rec *httptest.ResponseRecorder, next *recorderHandler, wantPath string) {
	t.Helper()
	if rec.Code != http.StatusTeapot {
		t.Fatalf("next did not run: status=%d body=%s", rec.Code, rec.Body.String())
	}
	paths := next.snapshot()
	if len(paths) != 1 || paths[0] != wantPath {
		t.Fatalf("paths=%v want [%s]", paths, wantPath)
	}
}

// passedNext returns a handler that writes 418 so a test can distinguish
// "reached next" from "gate refused (503)".
func passedNext(h *recorderHandler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(w, r)
		w.WriteHeader(http.StatusTeapot)
	})
}

func TestGateDefaultsDisabledAndPassesThrough(t *testing.T) {
	gate := New()
	if gate.Enabled() {
		t.Fatalf("new gate is enabled; want disabled")
	}
	next := &recorderHandler{}
	h := GateMiddleware(gate, passedNext(next))
	for _, path := range []string{"/api/session", "/api/endpoints", "/v1/models", "/v1/chat/completions"} {
		next.reset()
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, newRequest(http.MethodGet, path))
		assertPassed(t, rec, next, path)
	}
}

func TestGateEnabledRefusesAllUserStationAPIExceptAllowlist(t *testing.T) {
	gate := New()
	gate.Set(true)
	if !gate.Enabled() {
		t.Fatalf("gate not enabled after Set(true)")
	}
	next := &recorderHandler{}
	h := GateMiddleware(gate, passedNext(next))

	// Every user-station /api/* and /v1/* path that is not in the allowlist is
	// refused with a stable 503 platform envelope; the next handler never runs.
	refused := []string{
		"/api", "/api/session", "/api/me", "/api/me/usage",
		"/api/auth/discord/start", "/api/auth/discord/callback", "/api/auth/elevate",
		"/api/caller-key", "/api/caller-key/regenerate",
		"/api/endpoints", "/api/endpoints/1", "/api/endpoints/1/keys",
		"/api/endpoints/1/keys/2/models", "/api/endpoints/1/keys/2/models/refresh",
		"/api/models", "/api/models/3", "/api/models/3/bindings",
		"/api/issues", "/api/issues/9",
		"/api/account/export", "/api/account/delete",
		"/v1", "/v1/models", "/v1/chat/completions",
	}
	for _, path := range refused {
		next.reset()
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, newRequest(http.MethodGet, path))
		assertRefused(t, rec)
		if !next.empty() {
			t.Fatalf("gate let %s through to next: %v", path, next.snapshot())
		}
	}
}

func TestGateEnabledAdmitsAllowlist(t *testing.T) {
	gate := New()
	gate.Set(true)
	next := &recorderHandler{}
	h := GateMiddleware(gate, passedNext(next))

	for _, method := range []string{http.MethodGet, http.MethodPost} {
		next.reset()
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, newRequest(method, "/api/auth/logout"))
		assertPassed(t, rec, next, "/api/auth/logout")
	}
	next.reset()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newRequest(http.MethodGet, "/api/config"))
	assertPassed(t, rec, next, "/api/config")

	// The allowlist is exact: /api/configX or /api/auth/logout/extra are NOT
	// the public-config / logout endpoints and must be refused.
	for _, path := range []string{"/api/configX", "/api/config/", "/api/auth/logout/extra", "/api/auth/logoutX"} {
		next.reset()
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, newRequest(http.MethodGet, path))
		assertRefused(t, rec)
	}
}

func TestGateToggleIsImmediateForInflightRequests(t *testing.T) {
	gate := New()
	next := &recorderHandler{}
	h := GateMiddleware(gate, passedNext(next))

	// Off: passes.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newRequest(http.MethodGet, "/api/session"))
	assertPassed(t, rec, next, "/api/session")

	// Flip on: the very next request is refused, even though a session had
	// just been admitted. No restart, no per-request DB read.
	gate.Set(true)
	next.reset()
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, newRequest(http.MethodGet, "/api/session"))
	assertRefused(t, rec)

	// Flip back off: normal operation resumes immediately.
	gate.Set(false)
	next.reset()
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, newRequest(http.MethodGet, "/api/session"))
	assertPassed(t, rec, next, "/api/session")
}

func TestGateConcurrentToggleIsRaceFree(t *testing.T) {
	gate := New()
	next := &recorderHandler{}
	h := GateMiddleware(gate, passedNext(next))
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				gate.Set(j%2 == 0)
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, newRequest(http.MethodGet, "/api/session"))
				// The response is always a valid terminal state: either the
				// next handler ran (418) or the gate refused (503). It can never
				// hang, panic, or produce a partial envelope.
				if rec.Code != http.StatusTeapot && rec.Code != http.StatusServiceUnavailable {
					t.Errorf("status=%d want 418 or 503", rec.Code)
				}
			}
		}(i)
	}
	wg.Wait()
}

func TestNilGateIsPassThrough(t *testing.T) {
	next := &recorderHandler{}
	h := GateMiddleware(nil, passedNext(next))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newRequest(http.MethodGet, "/api/session"))
	assertPassed(t, rec, next, "/api/session")

	// Enabled reports false for a nil gate.
	var nilGate *Gate
	if nilGate.Enabled() {
		t.Fatalf("nil gate Enabled()=true")
	}
	nilGate.Set(true) // must not panic
}

func TestIsUserStationAPIPathClassifies(t *testing.T) {
	for _, p := range []string{"/api", "/api/", "/api/x", "/v1", "/v1/", "/v1/models"} {
		if !isUserStationAPIPath(p) {
			t.Fatalf("isUserStationAPIPath(%q)=false want true", p)
		}
	}
	for _, p := range []string{"/", "/healthz", "/admin/api", "/admin/api/x"} {
		if isUserStationAPIPath(p) {
			t.Fatalf("isUserStationAPIPath(%q)=true want false", p)
		}
	}
}
