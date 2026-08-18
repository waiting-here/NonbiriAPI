package endpoint

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/httperr"
)

// newTestHandler builds a handler over a real service backed by a seeded
// user, with identity fixed to that user's id.
func newTestHandler(t *testing.T) (http.Handler, *testService) {
	t.Helper()
	ts := newTestService(t)
	uid := ts.seedUser(t, nil)
	h := NewHandler(HandlerDeps{Service: ts.svc, Identity: staticIdentity(uid)})
	return h, ts
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
	rec := doRequest(t, h, http.MethodGet, "/api/endpoints", "")
	assertErr(t, rec, http.StatusUnauthorized, httperr.CodeUnauthorized)
}

func TestHandlerRejectsFailingIdentity(t *testing.T) {
	ts := newTestService(t)
	h := NewHandler(HandlerDeps{Service: ts.svc, Identity: failingIdentity()})
	rec := doRequest(t, h, http.MethodGet, "/api/endpoints", "")
	assertErr(t, rec, http.StatusUnauthorized, httperr.CodeUnauthorized)
}

func TestHandlerRejectsNonPositiveIdentity(t *testing.T) {
	ts := newTestService(t)
	h := NewHandler(HandlerDeps{Service: ts.svc, Identity: zeroIdentity()})
	rec := doRequest(t, h, http.MethodGet, "/api/endpoints", "")
	assertErr(t, rec, http.StatusUnauthorized, httperr.CodeUnauthorized)
}

// --- endpoint CRUD status / code / no-store ---------------------------------

func TestHandlerEndpointCRUDStatusAndCodes(t *testing.T) {
	h, _ := newTestHandler(t)

	// Create -> 201, no-store, canonicalized base_url, default connector.
	rec := doRequest(t, h, http.MethodPost, "/api/endpoints", `{"base_url":" HTTPS://EXAMPLE.com.:443/v1///"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	assertNoStore(t, rec)
	m := decodeMap(t, rec.Body.Bytes())
	if m["connector_type"] != "openai-compatible" {
		t.Errorf("connector_type = %v, want openai-compatible", m["connector_type"])
	}
	if m["base_url"] != "https://example.com:443/v1/" {
		t.Errorf("base_url = %v, want canonical", m["base_url"])
	}
	if m["enabled"] != true {
		t.Errorf("enabled = %v, want default true", m["enabled"])
	}
	epID := jsonNumber(m["id"])
	if epID <= 0 {
		t.Fatalf("id = %v, want positive", m["id"])
	}

	// Get -> 200.
	rec = doRequest(t, h, http.MethodGet, "/api/endpoints/"+itoa(epID), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("get status = %d, want 200", rec.Code)
	}
	assertNoStore(t, rec)

	// List -> 200 array.
	rec = doRequest(t, h, http.MethodGet, "/api/endpoints", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200", rec.Code)
	}
	assertNoStore(t, rec)
	var list []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("list body is not a JSON array: %v; body=%s", err, rec.Body.String())
	}
	if len(list) != 1 {
		t.Fatalf("list has %d endpoints, want 1", len(list))
	}

	// Patch -> 200.
	rec = doRequest(t, h, http.MethodPatch, "/api/endpoints/"+itoa(epID), `{"note":"updated"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	assertNoStore(t, rec)

	// Patch with connector_type -> 400 (immutable).
	rec = doRequest(t, h, http.MethodPatch, "/api/endpoints/"+itoa(epID), `{"connector_type":"openai-compatible"}`)
	assertErr(t, rec, http.StatusBadRequest, httperr.CodeInvalidRequest)

	// Delete -> 204, no-store.
	rec = doRequest(t, h, http.MethodDelete, "/api/endpoints/"+itoa(epID), "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204", rec.Code)
	}
	assertNoStore(t, rec)

	// Get / Delete / Patch after delete -> 404 not_found, no-store.
	for _, m := range []string{http.MethodGet, http.MethodDelete, http.MethodPatch} {
		rec := doRequest(t, h, m, "/api/endpoints/"+itoa(epID), `{"note":"x"}`)
		assertErr(t, rec, http.StatusNotFound, httperr.CodeNotFound)
	}
}

func TestHandlerGetUnknownEndpointNotFound(t *testing.T) {
	h, _ := newTestHandler(t)
	rec := doRequest(t, h, http.MethodGet, "/api/endpoints/9999", "")
	assertErr(t, rec, http.StatusNotFound, httperr.CodeNotFound)
}

func TestHandlerInvalidPathID(t *testing.T) {
	h, _ := newTestHandler(t)
	for _, target := range []string{"/api/endpoints/abc", "/api/endpoints/0", "/api/endpoints/-1"} {
		rec := doRequest(t, h, http.MethodGet, target, "")
		assertErr(t, rec, http.StatusBadRequest, httperr.CodeInvalidRequest)
	}
}

func TestHandlerEndpointCapResourceLimitExceeded(t *testing.T) {
	h, ts := newTestHandler(t)
	ts.setGlobalLimit(t, "1")
	rec := doRequest(t, h, http.MethodPost, "/api/endpoints", `{"base_url":"https://example.com/a/"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("first create = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	// Second endpoint -> 422 resource_limit_exceeded carrying limit + resource.
	rec = doRequest(t, h, http.MethodPost, "/api/endpoints", `{"base_url":"https://example.com/b/"}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("second create = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}
	var env struct {
		Error struct {
			Code     string `json:"code"`
			Limit    int    `json:"limit"`
			Resource string `json:"resource"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rec.Body.String())
	}
	if env.Error.Code != httperr.CodeResourceLimitExceeded {
		t.Errorf("code = %q, want %q", env.Error.Code, httperr.CodeResourceLimitExceeded)
	}
	if env.Error.Limit != 1 {
		t.Errorf("limit = %d, want 1", env.Error.Limit)
	}
	if env.Error.Resource != "endpoint" {
		t.Errorf("resource = %q, want endpoint", env.Error.Resource)
	}
	assertNoStore(t, rec)
}

func TestHandlerRejectsUnsafeBaseURL(t *testing.T) {
	h, _ := newTestHandler(t)
	rec := doRequest(t, h, http.MethodPost, "/api/endpoints", `{"base_url":"http://127.0.0.1:8080"}`)
	assertErr(t, rec, http.StatusBadRequest, httperr.CodeInvalidRequest)
}

func TestHandlerRejectsUnknownConnector(t *testing.T) {
	h, _ := newTestHandler(t)
	rec := doRequest(t, h, http.MethodPost, "/api/endpoints", `{"base_url":"https://example.com/v1/","connector_type":"anthropic-compatible"}`)
	assertErr(t, rec, http.StatusBadRequest, httperr.CodeInvalidRequest)
}

func TestHandlerRejectsBadEndpointRequestBodies(t *testing.T) {
	h, _ := newTestHandler(t)
	cases := []struct {
		name   string
		method string
		target string
		body   string
		code   string
	}{
		{"malformed json", http.MethodPost, "/api/endpoints", `{"base_url":`, httperr.CodeInvalidRequest},
		{"unknown field", http.MethodPost, "/api/endpoints", `{"base_url":"https://example.com/v1/","extra":1}`, httperr.CodeInvalidRequest},
		{"trailing content", http.MethodPost, "/api/endpoints", `{"base_url":"https://example.com/v1/"}{}`, httperr.CodeInvalidRequest},
		{"empty body", http.MethodPost, "/api/endpoints", ``, httperr.CodeInvalidRequest},
		{"empty base_url", http.MethodPost, "/api/endpoints", `{"base_url":""}`, httperr.CodeInvalidRequest},
		{"oversized body", http.MethodPost, "/api/endpoints", `{"base_url":"https://example.com/v1/","note":"` + strings.Repeat("n", MaxBodyBytes) + `"}`, httperr.CodePayloadTooLarge},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := doRequest(t, h, tc.method, tc.target, tc.body)
			wantStatus := http.StatusBadRequest
			if tc.code == httperr.CodePayloadTooLarge {
				wantStatus = http.StatusRequestEntityTooLarge
			}
			assertErr(t, rec, wantStatus, tc.code)
		})
	}
}

// --- endpoint key CRUD + secret never echoed --------------------------------

func TestHandlerKeyCRUDAndSecretNeverEchoed(t *testing.T) {
	h, _ := newTestHandler(t)
	rec := doRequest(t, h, http.MethodPost, "/api/endpoints", `{"base_url":"https://example.com/v1/"}`)
	epID := jsonNumber(decodeMap(t, rec.Body.Bytes())["id"])

	// Create key -> 201; response carries display fragments, never the secret.
	secret := "sk-handler-secret-12345678"
	rec = doRequest(t, h, http.MethodPost, "/api/endpoints/"+itoa(epID)+"/keys", `{"secret":"`+secret+`"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create key status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	assertNoStore(t, rec)
	body := rec.Body.String()
	if strings.Contains(body, secret) {
		t.Fatalf("create key response echoes plaintext secret: %s", body)
	}
	km := decodeMap(t, rec.Body.Bytes())
	for _, forbidden := range []string{"secret", "encrypted_secret", "encrypted", "ciphertext"} {
		if _, ok := km[forbidden]; ok {
			t.Errorf("key response has forbidden field %q", forbidden)
		}
	}
	if km["display_head"] == nil || km["display_tail"] == nil {
		t.Errorf("key response missing display fragments: %v", km)
	}

	// List keys -> 200; no secret in array body.
	rec = doRequest(t, h, http.MethodGet, "/api/endpoints/"+itoa(epID)+"/keys", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list keys status = %d, want 200", rec.Code)
	}
	assertNoStore(t, rec)
	if strings.Contains(rec.Body.String(), secret) {
		t.Fatalf("list keys body echoes plaintext secret: %s", rec.Body.String())
	}
	var arr []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &arr); err != nil {
		t.Fatalf("decode key array: %v", err)
	}
	if len(arr) != 1 {
		t.Fatalf("got %d keys, want 1", len(arr))
	}
	keyID := jsonNumber(arr[0]["id"])
	for _, forbidden := range []string{"secret", "encrypted_secret", "encrypted", "ciphertext"} {
		if _, ok := arr[0][forbidden]; ok {
			t.Errorf("list key has forbidden field %q", forbidden)
		}
	}

	// Patch key -> 200; still no secret.
	rec = doRequest(t, h, http.MethodPatch, "/api/endpoints/"+itoa(epID)+"/keys/"+itoa(keyID), `{"note":"rotated"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch key status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	assertNoStore(t, rec)
	if strings.Contains(rec.Body.String(), secret) {
		t.Fatalf("patch key response echoes secret: %s", rec.Body.String())
	}

	// Delete key -> 204, no-store.
	rec = doRequest(t, h, http.MethodDelete, "/api/endpoints/"+itoa(epID)+"/keys/"+itoa(keyID), "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete key status = %d, want 204", rec.Code)
	}
	assertNoStore(t, rec)

	// Key CRUD against a missing endpoint -> 404.
	rec = doRequest(t, h, http.MethodPost, "/api/endpoints/9999/keys", `{"secret":"sk-x-1234567890"}`)
	assertErr(t, rec, http.StatusNotFound, httperr.CodeNotFound)
}

func TestHandlerRejectsBadKeyRequestBodies(t *testing.T) {
	h, _ := newTestHandler(t)
	rec := doRequest(t, h, http.MethodPost, "/api/endpoints", `{"base_url":"https://example.com/v1/"}`)
	epID := jsonNumber(decodeMap(t, rec.Body.Bytes())["id"])
	base := "/api/endpoints/" + itoa(epID) + "/keys"

	cases := []struct {
		name   string
		body   string
		code   string
		status int
	}{
		{"empty secret", `{"secret":""}`, httperr.CodeInvalidRequest, http.StatusBadRequest},
		{"missing secret", `{"note":"x"}`, httperr.CodeInvalidRequest, http.StatusBadRequest},
		{"unknown field", `{"secret":"sk-1234567890","extra":1}`, httperr.CodeInvalidRequest, http.StatusBadRequest},
		{"malformed", `{"secret":`, httperr.CodeInvalidRequest, http.StatusBadRequest},
		{"trailing", `{"secret":"sk-1234567890"}{}`, httperr.CodeInvalidRequest, http.StatusBadRequest},
		{"oversized", `{"secret":"` + strings.Repeat("k", MaxBodyBytes) + `"}`, httperr.CodePayloadTooLarge, http.StatusRequestEntityTooLarge},
		{"control char note", `{"secret":"sk-1234567890","note":"a\nb"}`, httperr.CodeInvalidRequest, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := doRequest(t, h, http.MethodPost, base, tc.body)
			assertErr(t, rec, tc.status, tc.code)
		})
	}
}

func TestHandlerCrossUserKeyAndEndpointNotFound(t *testing.T) {
	// Alice owns an endpoint; Bob cannot read its keys or add a key.
	ts := newTestService(t)
	alice := ts.seedUser(t, nil)
	bob := ts.seedUser(t, nil)
	aEP := mustCreateEndpoint(t, ts, alice, "https://alice.example/v1/")

	bobHandler := NewHandler(HandlerDeps{Service: ts.svc, Identity: staticIdentity(bob)})
	rec := doRequest(t, bobHandler, http.MethodGet, "/api/endpoints/"+itoa(aEP.ID)+"/keys", "")
	assertErr(t, rec, http.StatusNotFound, httperr.CodeNotFound)
	rec = doRequest(t, bobHandler, http.MethodPost, "/api/endpoints/"+itoa(aEP.ID)+"/keys", `{"secret":"sk-bob-inject-12345"}`)
	assertErr(t, rec, http.StatusNotFound, httperr.CodeNotFound)
}

func TestHandlerEndpointKeyCapResourceLimitExceeded(t *testing.T) {
	h, ts := newTestHandler(t)
	ts.setEndpointKeyLimit(t, "1")

	// Create an endpoint to attach keys to.
	rec := doRequest(t, h, http.MethodPost, "/api/endpoints", `{"base_url":"https://example.com/v1/"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create endpoint = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	epID := jsonNumber(decodeMap(t, rec.Body.Bytes())["id"])

	// First key -> 201.
	rec = doRequest(t, h, http.MethodPost, "/api/endpoints/"+itoa(epID)+"/keys", `{"secret":"sk-one-12345678"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("first key = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}

	// Second key -> 422 resource_limit_exceeded carrying limit + resource.
	rec = doRequest(t, h, http.MethodPost, "/api/endpoints/"+itoa(epID)+"/keys", `{"secret":"sk-two-12345678"}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("second key = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}
	var env struct {
		Error struct {
			Code     string `json:"code"`
			Limit    int    `json:"limit"`
			Resource string `json:"resource"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rec.Body.String())
	}
	if env.Error.Code != httperr.CodeResourceLimitExceeded {
		t.Errorf("code = %q, want %q", env.Error.Code, httperr.CodeResourceLimitExceeded)
	}
	if env.Error.Limit != 1 {
		t.Errorf("limit = %d, want 1", env.Error.Limit)
	}
	if env.Error.Resource != "endpoint_key" {
		t.Errorf("resource = %q, want endpoint_key", env.Error.Resource)
	}
	assertNoStore(t, rec)
}

// --- helpers ----------------------------------------------------------------

func jsonNumber(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int64:
		return n
	case int:
		return int64(n)
	}
	return 0
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
