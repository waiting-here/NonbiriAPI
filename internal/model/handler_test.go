package model

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nonbiriapi/internal/auth"
	"nonbiriapi/internal/host"
	"nonbiriapi/internal/httperr"
)

// newTestHandler builds a handler over a real service backed by a seeded user,
// with identity fixed to that user's id.
func newTestHandler(t *testing.T) (http.Handler, *testService, int64) {
	t.Helper()
	ts := newTestService(t)
	uid := ts.seedUser(t, "u1")
	h := NewHandler(HandlerDeps{Service: ts.svc, Identity: staticIdentity(uid)})
	return h, ts, uid
}

func staticIdentity(uid int64) IdentityResolver {
	return func(*http.Request) (int64, error) { return uid, nil }
}

func failingIdentity() IdentityResolver {
	return func(*http.Request) (int64, error) { return 0, errors.New("no session") }
}

func zeroIdentity() IdentityResolver {
	return func(*http.Request) (int64, error) { return 0, nil }
}

func doRequest(t *testing.T, h http.Handler, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, target, r)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

type errEnvelope struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Diag    string `json:"diag,omitempty"`
	} `json:"error"`
}

func decodeErrEnvelope(t *testing.T, body []byte) errEnvelope {
	t.Helper()
	var env errEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode error envelope: %v; body=%q", err, body)
	}
	return env
}

func decodeMap(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("decode json: %v; body=%q", err, body)
	}
	return m
}

func assertNoStore(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
}

func assertErr(t *testing.T, rec *httptest.ResponseRecorder, wantStatus int, wantCode string) {
	t.Helper()
	if rec.Code != wantStatus {
		t.Errorf("status = %d, want %d; body=%s", rec.Code, wantStatus, rec.Body.String())
	}
	env := decodeErrEnvelope(t, rec.Body.Bytes())
	if env.Error.Code != wantCode {
		t.Errorf("code = %q, want %q", env.Error.Code, wantCode)
	}
	assertNoStore(t, rec)
}

// --- identity ---------------------------------------------------------------

func TestHandlerRejectsMissingIdentity(t *testing.T) {
	ts := newTestService(t)
	h := NewHandler(HandlerDeps{Service: ts.svc, Identity: nil})
	rec := doRequest(t, h, http.MethodGet, "/api/models", "")
	assertErr(t, rec, http.StatusUnauthorized, httperr.CodeUnauthorized)
}

func TestHandlerRejectsFailingIdentity(t *testing.T) {
	ts := newTestService(t)
	h := NewHandler(HandlerDeps{Service: ts.svc, Identity: failingIdentity()})
	rec := doRequest(t, h, http.MethodGet, "/api/models", "")
	assertErr(t, rec, http.StatusUnauthorized, httperr.CodeUnauthorized)
}

func TestHandlerRejectsNonPositiveIdentity(t *testing.T) {
	ts := newTestService(t)
	h := NewHandler(HandlerDeps{Service: ts.svc, Identity: zeroIdentity()})
	rec := doRequest(t, h, http.MethodGet, "/api/models", "")
	assertErr(t, rec, http.StatusUnauthorized, httperr.CodeUnauthorized)
}

// --- model CRUD status / code / no-store ------------------------------------

func TestHandlerModelCRUDStatusAndCodes(t *testing.T) {
	h, _, _ := newTestHandler(t)

	// Create -> 201 with default strategy and zero binding count.
	rec := doRequest(t, h, http.MethodPost, "/api/models", `{"provider":"openai","model":"gpt-4o"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	assertNoStore(t, rec)
	body := decodeMap(t, rec.Body.Bytes())
	if body["full_name"] != "openai/gpt-4o" || body["route_strategy"] != "ordered" || body["binding_count"] != float64(0) {
		t.Fatalf("create body = %v", body)
	}
	id := int64(body["id"].(float64))

	// List -> 200.
	rec = doRequest(t, h, http.MethodGet, "/api/models", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d; body=%s", rec.Code, rec.Body.String())
	}
	assertNoStore(t, rec)

	// Get -> 200.
	rec = doRequest(t, h, http.MethodGet, fmt.Sprintf("/api/models/%d", id), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("get status = %d; body=%s", rec.Code, rec.Body.String())
	}
	assertNoStore(t, rec)

	// Patch -> 200 with recomputed full_name.
	rec = doRequest(t, h, http.MethodPatch, fmt.Sprintf("/api/models/%d", id), `{"provider":"anthropic","route_strategy":"random"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch status = %d; body=%s", rec.Code, rec.Body.String())
	}
	body = decodeMap(t, rec.Body.Bytes())
	if body["full_name"] != "anthropic/gpt-4o" || body["route_strategy"] != "random" {
		t.Fatalf("patch body = %v", body)
	}
	assertNoStore(t, rec)

	// No-op patch -> 200 unchanged.
	rec = doRequest(t, h, http.MethodPatch, fmt.Sprintf("/api/models/%d", id), `{}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("noop patch status = %d; body=%s", rec.Code, rec.Body.String())
	}

	// Delete -> 204.
	rec = doRequest(t, h, http.MethodDelete, fmt.Sprintf("/api/models/%d", id), "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d; body=%s", rec.Code, rec.Body.String())
	}
	assertNoStore(t, rec)

	// Get after delete -> 404 not_found.
	rec = doRequest(t, h, http.MethodGet, fmt.Sprintf("/api/models/%d", id), "")
	assertErr(t, rec, http.StatusNotFound, httperr.CodeNotFound)
}

func TestHandlerModelConflictAndValidationCodes(t *testing.T) {
	h, _, _ := newTestHandler(t)

	// Duplicate full name -> 409 conflict.
	doRequest(t, h, http.MethodPost, "/api/models", `{"provider":"a/b","model":"c"}`)
	rec := doRequest(t, h, http.MethodPost, "/api/models", `{"provider":"a","model":"b/c"}`)
	assertErr(t, rec, http.StatusConflict, httperr.CodeConflict)

	// Missing required fields -> 400 invalid_request.
	rec = doRequest(t, h, http.MethodPost, "/api/models", `{"provider":"","model":"m"}`)
	assertErr(t, rec, http.StatusBadRequest, httperr.CodeInvalidRequest)

	// Unknown route strategy -> 400.
	rec = doRequest(t, h, http.MethodPost, "/api/models", `{"provider":"p","model":"m","route_strategy":"weighted"}`)
	assertErr(t, rec, http.StatusBadRequest, httperr.CodeInvalidRequest)

	// Patch into a collision -> 409.
	doRequest(t, h, http.MethodPost, "/api/models", `{"provider":"x","model":"y"}`)
	rec = doRequest(t, h, http.MethodPatch, "/api/models/2", `{"provider":"a","model":"b/c"}`)
	assertErr(t, rec, http.StatusConflict, httperr.CodeConflict)

	// Unknown fields are rejected.
	rec = doRequest(t, h, http.MethodPost, "/api/models", `{"provider":"p","model":"m","connector_type":"openai-compatible"}`)
	assertErr(t, rec, http.StatusBadRequest, httperr.CodeInvalidRequest)

	// Trailing JSON tokens are rejected.
	rec = doRequest(t, h, http.MethodPost, "/api/models", `{"provider":"p","model":"m"} {"x":1}`)
	assertErr(t, rec, http.StatusBadRequest, httperr.CodeInvalidRequest)

	// Empty body is rejected.
	rec = doRequest(t, h, http.MethodPost, "/api/models", "")
	assertErr(t, rec, http.StatusBadRequest, httperr.CodeInvalidRequest)

	// Oversized body -> 413 payload_too_large.
	huge := `{"provider":"p","model":"` + strings.Repeat("a", MaxBodyBytes) + `"}`
	rec = doRequest(t, h, http.MethodPost, "/api/models", huge)
	assertErr(t, rec, http.StatusRequestEntityTooLarge, httperr.CodePayloadTooLarge)
}

func TestHandlerModelCrossUserNotFound(t *testing.T) {
	h, ts, _ := newTestHandler(t)
	other := ts.seedUser(t, "u2")
	// other creates a model; uid cannot see it.
	h2 := NewHandler(HandlerDeps{Service: ts.svc, Identity: staticIdentity(other)})
	rec := doRequest(t, h2, http.MethodPost, "/api/models", `{"provider":"secret","model":"model"}`)
	id := int64(decodeMap(t, rec.Body.Bytes())["id"].(float64))

	rec = doRequest(t, h, http.MethodGet, fmt.Sprintf("/api/models/%d", id), "")
	assertErr(t, rec, http.StatusNotFound, httperr.CodeNotFound)
	rec = doRequest(t, h, http.MethodPatch, fmt.Sprintf("/api/models/%d", id), `{"provider":"x"}`)
	assertErr(t, rec, http.StatusNotFound, httperr.CodeNotFound)
	rec = doRequest(t, h, http.MethodDelete, fmt.Sprintf("/api/models/%d", id), "")
	assertErr(t, rec, http.StatusNotFound, httperr.CodeNotFound)
	rec = doRequest(t, h, http.MethodGet, fmt.Sprintf("/api/models/%d/bindings", id), "")
	assertErr(t, rec, http.StatusNotFound, httperr.CodeNotFound)

	// Garbage path ids are invalid_request.
	rec = doRequest(t, h, http.MethodGet, "/api/models/not-a-number", "")
	assertErr(t, rec, http.StatusBadRequest, httperr.CodeInvalidRequest)
	rec = doRequest(t, h, http.MethodGet, "/api/models/0", "")
	assertErr(t, rec, http.StatusBadRequest, httperr.CodeInvalidRequest)

	// A non-existent id is indistinguishable.
	rec = doRequest(t, h, http.MethodGet, "/api/models/99999", "")
	assertErr(t, rec, http.StatusNotFound, httperr.CodeNotFound)
}

// --- bindings ---------------------------------------------------------------

func TestHandlerBindingsCRUD(t *testing.T) {
	h, ts, uid := newTestHandler(t)
	ctx := context.Background()

	m, err := ts.svc.CreateModel(ctx, uid, "p", "m", nil)
	if err != nil {
		t.Fatalf("create model: %v", err)
	}
	key := ts.seedEndpointKeyAndCache(t, uid, "up-a", "up-b")
	// Seed a recognizable ciphertext marker so we can assert it never leaks.
	if _, err := ts.store.DB().Exec(`UPDATE endpoint_keys SET encrypted_secret='LEAKMARK-ciphertext' WHERE id=?`, key); err != nil {
		t.Fatalf("mark ciphertext: %v", err)
	}

	// Create binding -> 201 with ord default 0.
	rec := doRequest(t, h, http.MethodPost, fmt.Sprintf("/api/models/%d/bindings", m.ID),
		`{"endpoint_key_id":`+fmt.Sprintf("%d", key)+`,"upstream_model_id":"up-a"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create binding status = %d; body=%s", rec.Code, rec.Body.String())
	}
	assertNoStore(t, rec)
	binding := decodeMap(t, rec.Body.Bytes())
	if binding["ord"] != float64(0) || binding["upstream_model_id"] != "up-a" {
		t.Fatalf("binding body = %v", binding)
	}
	bID := int64(binding["id"].(float64))

	// List bindings -> 200 array.
	rec = doRequest(t, h, http.MethodGet, fmt.Sprintf("/api/models/%d/bindings", m.ID), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list bindings status = %d; body=%s", rec.Code, rec.Body.String())
	}
	assertNoStore(t, rec)
	if !strings.Contains(rec.Body.String(), `"upstream_model_id":"up-a"`) {
		t.Fatalf("list bindings body = %s", rec.Body.String())
	}

	// Patch binding -> 200.
	rec = doRequest(t, h, http.MethodPatch, fmt.Sprintf("/api/models/%d/bindings/%d", m.ID, bID),
		`{"ord":4,"upstream_model_id":"up-b"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch binding status = %d; body=%s", rec.Code, rec.Body.String())
	}
	binding = decodeMap(t, rec.Body.Bytes())
	if binding["ord"] != float64(4) || binding["upstream_model_id"] != "up-b" {
		t.Fatalf("patched binding = %v", binding)
	}
	assertNoStore(t, rec)

	// Binding errors: uncached upstream -> 404; duplicate -> 409; disabled
	// key -> 404; negative ord -> 400.
	rec = doRequest(t, h, http.MethodPost, fmt.Sprintf("/api/models/%d/bindings", m.ID),
		`{"endpoint_key_id":`+fmt.Sprintf("%d", key)+`,"upstream_model_id":"up-zz"}`)
	assertErr(t, rec, http.StatusNotFound, httperr.CodeNotFound)
	rec = doRequest(t, h, http.MethodPost, fmt.Sprintf("/api/models/%d/bindings", m.ID),
		`{"endpoint_key_id":`+fmt.Sprintf("%d", key)+`,"upstream_model_id":"up-b"}`)
	assertErr(t, rec, http.StatusConflict, httperr.CodeConflict)
	rec = doRequest(t, h, http.MethodPost, fmt.Sprintf("/api/models/%d/bindings", m.ID),
		`{"endpoint_key_id":`+fmt.Sprintf("%d", key)+`,"upstream_model_id":"up-b","ord":-1}`)
	assertErr(t, rec, http.StatusBadRequest, httperr.CodeInvalidRequest)
	rec = doRequest(t, h, http.MethodPatch, fmt.Sprintf("/api/models/%d/bindings/%d", m.ID, bID), `{"ord":99999999}`)
	assertErr(t, rec, http.StatusBadRequest, httperr.CodeInvalidRequest)

	// Delete binding -> 204; re-delete -> 404.
	rec = doRequest(t, h, http.MethodDelete, fmt.Sprintf("/api/models/%d/bindings/%d", m.ID, bID), "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete binding status = %d; body=%s", rec.Code, rec.Body.String())
	}
	assertNoStore(t, rec)
	rec = doRequest(t, h, http.MethodDelete, fmt.Sprintf("/api/models/%d/bindings/%d", m.ID, bID), "")
	assertErr(t, rec, http.StatusNotFound, httperr.CodeNotFound)

	// Wrong binding id on the path -> 404.
	rec = doRequest(t, h, http.MethodPatch, fmt.Sprintf("/api/models/%d/bindings/99999", m.ID), `{"ord":1}`)
	assertErr(t, rec, http.StatusNotFound, httperr.CodeNotFound)

	// No response may contain the ciphertext marker or key fragments.
	for _, target := range []string{
		"/api/models",
		fmt.Sprintf("/api/models/%d", m.ID),
		fmt.Sprintf("/api/models/%d/bindings", m.ID),
	} {
		rec = doRequest(t, h, http.MethodGet, target, "")
		if strings.Contains(rec.Body.String(), "LEAKMARK") || strings.Contains(rec.Body.String(), "ciphertext") {
			t.Fatalf("secret material leaked in %s: %s", target, rec.Body.String())
		}
	}
}

// --- SessionIdentity --------------------------------------------------------

// stubProvider satisfies auth.DiscordProvider with no-ops; it is only needed
// to construct the user auth service for the session middleware.
type stubProvider struct{}

func (stubProvider) AuthorizationURL(context.Context, auth.DiscordAuthorizeRequest) (string, error) {
	return "https://example.test/authorize", nil
}
func (stubProvider) Exchange(context.Context, string, string) (auth.DiscordLogin, error) {
	return auth.DiscordLogin{}, errors.New("not used in this test")
}

// TestSessionIdentityIsSessionOnly proves the resolver wired for the user API
// accepts a browser user-session principal but never a caller-key (bearer)
// principal, even when the bearer is valid.
func TestSessionIdentityIsSessionOnly(t *testing.T) {
	ts := newTestService(t)
	uid := ts.seedUser(t, "u1")

	// Build a user station request context (the middleware requires it).
	withStation := func(r *http.Request) *http.Request {
		return r.WithContext(host.WithStation(r.Context(), host.StationUser))
	}

	// 1. No principal: rejected.
	req := httptest.NewRequest(http.MethodGet, "/api/models", nil)
	if _, err := SessionIdentity(req); err == nil {
		t.Fatal("SessionIdentity accepted a request without any principal")
	}
	if _, err := SessionIdentity(nil); err == nil {
		t.Fatal("SessionIdentity accepted a nil request")
	}

	// 2. Valid caller key (bearer): must be rejected even though a principal
	//    exists in context.
	key, err := ts.store.SetCallerKey(uid)
	if err != nil {
		t.Fatalf("set caller key: %v", err)
	}
	var gotSessionID int64
	gotSessionID = -1
	callerWrapped := auth.CallerKeyMiddleware(ts.store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := SessionIdentity(r)
		if err == nil {
			gotSessionID = id
		}
	}))
	req = httptest.NewRequest(http.MethodGet, "/api/models", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	callerWrapped.ServeHTTP(httptest.NewRecorder(), withStation(req))
	if gotSessionID != -1 {
		t.Fatalf("SessionIdentity accepted a caller-key principal (id=%d)", gotSessionID)
	}

	// 3. Browser user session: accepted.
	userAuth, err := auth.NewUserAuth(auth.UserAuthConfig{
		Store:       ts.store,
		Provider:    stubProvider{},
		ClientID:    "test-client",
		SiteBaseURL: "https://user.example.test",
	})
	if err != nil {
		t.Fatalf("new user auth: %v", err)
	}
	token, _, err := ts.store.CreateUserSession(uid)
	if err != nil {
		t.Fatalf("create user session: %v", err)
	}
	gotSessionID = -1
	sessionWrapped := auth.UserSessionMiddleware(userAuth, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := SessionIdentity(r)
		if err == nil {
			gotSessionID = id
		}
	}))
	req = httptest.NewRequest(http.MethodGet, "/api/models", nil)
	req.AddCookie(&http.Cookie{Name: auth.UserSessionCookieName, Value: token})
	sessionWrapped.ServeHTTP(httptest.NewRecorder(), withStation(req))
	if gotSessionID != uid {
		t.Fatalf("SessionIdentity with user session = %d, want %d", gotSessionID, uid)
	}
}

// TestHandlerWorksUnderUserSessionMiddleware proves the mountable integration:
// the handler behind the session middleware answers requests with the real
// SessionIdentity resolver.
func TestHandlerWorksUnderUserSessionMiddleware(t *testing.T) {
	ts := newTestService(t)
	uid := ts.seedUser(t, "u1")
	h := NewHandler(HandlerDeps{Service: ts.svc, Identity: SessionIdentity})

	userAuth, err := auth.NewUserAuth(auth.UserAuthConfig{
		Store:       ts.store,
		Provider:    stubProvider{},
		ClientID:    "test-client",
		SiteBaseURL: "https://user.example.test",
	})
	if err != nil {
		t.Fatalf("new user auth: %v", err)
	}
	token, _, err := ts.store.CreateUserSession(uid)
	if err != nil {
		t.Fatalf("create user session: %v", err)
	}

	stack := auth.UserSessionMiddleware(userAuth, h)
	req := httptest.NewRequest(http.MethodPost, "/api/models", strings.NewReader(`{"provider":"openai","model":"gpt-4o"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: auth.UserSessionCookieName, Value: token})
	rec := httptest.NewRecorder()
	stack.ServeHTTP(rec, req.WithContext(host.WithStation(req.Context(), host.StationUser)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("session-authenticated create status = %d; body=%s", rec.Code, rec.Body.String())
	}
	// The same stack without a session cookie is unauthorized.
	req2 := httptest.NewRequest(http.MethodGet, "/api/models", nil)
	rec2 := httptest.NewRecorder()
	stack.ServeHTTP(rec2, req2.WithContext(host.WithStation(req2.Context(), host.StationUser)))
	if rec2.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d; body=%s", rec2.Code, rec2.Body.String())
	}
	// And with a valid bearer key it is still unauthorized (management API
	// never accepts the platform caller credential).
	key, err := ts.store.SetCallerKey(uid)
	if err != nil {
		t.Fatalf("set caller key: %v", err)
	}
	req3 := httptest.NewRequest(http.MethodGet, "/api/models", nil)
	req3.Header.Set("Authorization", "Bearer "+key)
	rec3 := httptest.NewRecorder()
	stack.ServeHTTP(rec3, req3.WithContext(host.WithStation(req3.Context(), host.StationUser)))
	if rec3.Code != http.StatusUnauthorized {
		t.Fatalf("bearer-authenticated status = %d, want 401; body=%s", rec3.Code, rec3.Body.String())
	}
}
