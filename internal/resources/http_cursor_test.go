package resources

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/idempotency"
	"github.com/waiting-here/NonbiriAPI/internal/strictjson"
)

type resourceTestRegistrar struct {
	handlers map[string]AuthorizedUserHandler
	failKey  string
}

func (registrar *resourceTestRegistrar) RegisterUserRoute(method, pattern string, handler AuthorizedUserHandler) error {
	if registrar.handlers == nil {
		registrar.handlers = make(map[string]AuthorizedUserHandler)
	}
	key := method + " " + pattern
	if key == registrar.failKey {
		return errors.New("registration refused")
	}
	if handler == nil {
		return errors.New("nil handler")
	}
	if _, duplicate := registrar.handlers[key]; duplicate {
		return errors.New("duplicate route")
	}
	registrar.handlers[key] = handler
	return nil
}

func resourceHTTPCall(t *testing.T, handler AuthorizedUserHandler, principal UserPrincipal, method, target, body, idempotencyValue string, pathValues map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, "https://user.example"+target, strings.NewReader(body))
	if idempotencyValue != "" {
		request.Header.Set("Idempotency-Key", idempotencyValue)
	}
	for name, value := range pathValues {
		request.SetPathValue(name, value)
	}
	recorder := httptest.NewRecorder()
	handler(recorder, request, principal)
	return recorder
}

func TestRegisterRoutesAndCallerKeyHTTPContract(t *testing.T) {
	environment := newResourceTestEnvironment(t)
	userID := environment.seedUser(t, "caller-http-owner")
	registrar := &resourceTestRegistrar{}
	if err := RegisterRoutes(registrar, environment.repository); err != nil {
		t.Fatalf("RegisterRoutes: %v", err)
	}
	if len(registrar.handlers) != 26 {
		t.Fatalf("registered routes = %d, want 26", len(registrar.handlers))
	}
	for _, key := range []string{
		"GET /api/endpoints", "DELETE /api/endpoints/{id}/keys/{keyId}",
		"POST /api/endpoints/{id}/keys/{keyId}/models/refresh",
		"PATCH /api/endpoints/{id}/keys/{keyId}/models/manual/{entryId}",
		"POST /api/models/{id}/bindings/batch", "PUT /api/models/{id}/bindings/order",
		"GET /api/caller-key", "POST /api/caller-key/regenerate",
	} {
		if registrar.handlers[key] == nil {
			t.Fatalf("route %q was not registered", key)
		}
	}

	principal := UserPrincipal{UserID: userID}
	get := resourceHTTPCall(t, registrar.handlers["GET /api/caller-key"], principal, http.MethodGet, "/api/caller-key", "", "", nil)
	if get.Code != http.StatusOK || get.Header().Get("X-Nonbiri-CallerKey-Generation") != "0" || strings.TrimSpace(get.Body.String()) != "null" {
		t.Fatalf("initial CallerKey HTTP = %d %q generation=%q", get.Code, get.Body.String(), get.Header().Get("X-Nonbiri-CallerKey-Generation"))
	}

	regenerate := resourceHTTPCall(t, registrar.handlers["POST /api/caller-key/regenerate"], principal,
		http.MethodPost, "/api/caller-key/regenerate", `{"expected_generation":"0"}`, resourceTestKey('H'), nil)
	if regenerate.Code != http.StatusOK {
		t.Fatalf("CallerKey regenerate HTTP = %d %s", regenerate.Code, regenerate.Body.String())
	}
	var secret CallerKeySecret
	if err := json.Unmarshal(regenerate.Body.Bytes(), &secret); err != nil || !strings.HasPrefix(secret.Secret, callerKeyPrefix) || secret.Metadata.Generation != "1" {
		t.Fatalf("CallerKey regenerate body = %#v, %v", secret, err)
	}
	var replayRows int
	if err := environment.store.DB().QueryRow(`SELECT count(*) FROM idempotency_records`).Scan(&replayRows); err != nil || replayRows != 0 {
		t.Fatalf("CallerKey exception replay rows = %d, %v", replayRows, err)
	}
	metadata := resourceHTTPCall(t, registrar.handlers["GET /api/caller-key"], principal, http.MethodGet, "/api/caller-key", "", "", nil)
	if metadata.Code != http.StatusOK || metadata.Header().Get("X-Nonbiri-CallerKey-Generation") != "1" || strings.TrimSpace(metadata.Body.String()) == "null" {
		t.Fatalf("active CallerKey HTTP = %d %q generation=%q", metadata.Code, metadata.Body.String(), metadata.Header().Get("X-Nonbiri-CallerKey-Generation"))
	}
}

func TestHTTPStrictDecodeControlReplayAndDowngrade(t *testing.T) {
	environment := newResourceTestEnvironment(t)
	userID := environment.seedUser(t, "strict-http-owner")
	registrar := &resourceTestRegistrar{}
	if err := RegisterRoutes(registrar, environment.repository); err != nil {
		t.Fatalf("RegisterRoutes: %v", err)
	}
	principal := UserPrincipal{UserID: userID}
	create := registrar.handlers["POST /api/endpoints"]
	validBody := `{"connector_type":"openai-compatible","base_url":"https://example.com/v1","note":"note","enabled":true}`
	for index, body := range []string{
		`{"connector_type":"openai-compatible","base_url":"https://example.com/v1","note":"note","enabled":true,"unknown":1}`,
		`{"connector_type":"openai-compatible","connector_type":"anthropic-compatible","base_url":"https://example.com/v1","note":"note","enabled":true}`,
		`{"connector_type":"openai-compatible","base_url":"https://example.com/v1","note":null,"enabled":true}`,
		validBody + `{}`,
	} {
		response := resourceHTTPCall(t, create, principal, http.MethodPost, "/api/endpoints", body, resourceTestKey(byte('J'+index)), nil)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("strict body %d status = %d, body=%s", index, response.Code, response.Body.String())
		}
	}
	tooLarge := `{"connector_type":"` + strings.Repeat("x", idempotency.MaxControlBodyBytes+1) + `"}`
	oversized := resourceHTTPCall(t, create, principal, http.MethodPost, "/api/endpoints", tooLarge, resourceTestKey('N'), nil)
	if oversized.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized status = %d, body=%s", oversized.Code, oversized.Body.String())
	}
	withQuery := resourceHTTPCall(t, create, principal, http.MethodPost, "/api/endpoints?extra=1", validBody, resourceTestKey('O'), nil)
	if withQuery.Code != http.StatusBadRequest {
		t.Fatalf("unexpected mutation query status = %d", withQuery.Code)
	}
	var endpointRows int
	if err := environment.store.DB().QueryRow(`SELECT count(*) FROM endpoints`).Scan(&endpointRows); err != nil || endpointRows != 0 {
		t.Fatalf("strict rejects wrote endpoints = %d, %v", endpointRows, err)
	}

	idempotencyValue := resourceTestKey('P')
	first := resourceHTTPCall(t, create, principal, http.MethodPost, "/api/endpoints", validBody, idempotencyValue, nil)
	if first.Code != http.StatusCreated {
		t.Fatalf("valid endpoint create = %d %s", first.Code, first.Body.String())
	}
	replay := resourceHTTPCall(t, create, principal, http.MethodPost, "/api/endpoints", validBody, idempotencyValue, nil)
	if replay.Code != first.Code || replay.Body.String() != first.Body.String() {
		t.Fatalf("endpoint replay differs: first=%d %q replay=%d %q", first.Code, first.Body.String(), replay.Code, replay.Body.String())
	}
	environment.authorizer.deny.Store(true)
	denied := resourceHTTPCall(t, create, principal, http.MethodPost, "/api/endpoints", validBody, idempotencyValue, nil)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("downgraded replay status = %d, body=%s", denied.Code, denied.Body.String())
	}
	environment.authorizer.deny.Store(false)

	differentBody := strings.Replace(validBody, `"note":"note"`, `"note":"different"`, 1)
	conflictingReplay := resourceHTTPCall(t, create, principal, http.MethodPost, "/api/endpoints", differentBody, idempotencyValue, nil)
	if conflictingReplay.Code != http.StatusConflict {
		t.Fatalf("same-route digest conflict status = %d, body=%s", conflictingReplay.Code, conflictingReplay.Body.String())
	}
	createModel := registrar.handlers["POST /api/models"]
	crossRoute := resourceHTTPCall(t, createModel, principal, http.MethodPost, "/api/models",
		`{"provider":"logical","model":"main"}`, idempotencyValue, nil)
	if crossRoute.Code != http.StatusConflict {
		t.Fatalf("cross-route idempotency conflict status = %d, body=%s", crossRoute.Code, crossRoute.Body.String())
	}
	var modelRows int
	if err := environment.store.DB().QueryRow(`SELECT count(*) FROM models`).Scan(&modelRows); err != nil || modelRows != 0 {
		t.Fatalf("conflicting replay wrote models = %d, %v", modelRows, err)
	}
}

func TestResourceJSONFieldLimitIsPerObject(t *testing.T) {
	selections := make([]bindingSelectionCanonical, maxBindingBatch)
	for index := range selections {
		selections[index] = bindingSelectionCanonical{
			EndpointKeyID: strconv.Itoa(index + 1), UpstreamModelID: "model",
		}
	}
	payload, err := json.Marshal(addBindingsCanonical{
		ExpectedBindingRevision: "0", Selections: selections,
	})
	if err != nil {
		t.Fatalf("marshal max binding batch: %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, "https://user.example/api/models/1/bindings/batch", strings.NewReader(string(payload)))
	recorder := httptest.NewRecorder()
	var decoded addBindingsRequest
	if _, ok := decodeStrictObject(recorder, request, &decoded); !ok || len(decoded.Selections.Value) != maxBindingBatch {
		t.Fatalf("decode max binding batch = ok %t, status %d, selections %d", ok, recorder.Code, len(decoded.Selections.Value))
	}

	tooManyFields := make(map[string]int, strictjson.MaxFields+1)
	for index := 0; index <= strictjson.MaxFields; index++ {
		tooManyFields[strconv.Itoa(index)] = index
	}
	oversizedObject, err := json.Marshal(tooManyFields)
	if err != nil {
		t.Fatalf("marshal oversized object: %v", err)
	}
	if err := validateResourceJSONObject(oversizedObject); !errors.Is(err, errInvalidResourceJSON) {
		t.Fatalf("oversized object error = %v, want invalid resource JSON", err)
	}
	if err := validateResourceJSONObject([]byte(`{"value":"\ud800"}`)); !errors.Is(err, errInvalidResourceJSON) {
		t.Fatalf("isolated surrogate error = %v, want invalid resource JSON", err)
	}
}

func TestSignedCursorScopeOwnerTamperAndExpiry(t *testing.T) {
	environment := newResourceTestEnvironment(t)
	codec := environment.repository.cursors
	atoms := []db.CursorAtom{{Kind: db.CursorUint, Uint: 42}, {Kind: db.CursorText, Text: "cursor-value"}}
	token, err := codec.encode("cursor-test", "owner-1", uint64(resourceTestNow+10), atoms)
	if err != nil || len(token) > maxCursorBytes {
		t.Fatalf("encode cursor = %q, %v", token, err)
	}
	decoded, err := codec.decode(token, "cursor-test", "owner-1", uint64(resourceTestNow), db.CursorUint, db.CursorText)
	if err != nil || !reflect.DeepEqual(decoded, atoms) {
		t.Fatalf("decode cursor = %#v, %v", decoded, err)
	}
	for _, test := range []struct {
		name, scope, owner string
		now                uint64
	}{
		{name: "scope", scope: "other", owner: "owner-1", now: uint64(resourceTestNow)},
		{name: "owner", scope: "cursor-test", owner: "owner-2", now: uint64(resourceTestNow)},
		{name: "expiry", scope: "cursor-test", owner: "owner-1", now: uint64(resourceTestNow + 10)},
	} {
		if _, err := codec.decode(token, test.scope, test.owner, test.now, db.CursorUint, db.CursorText); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("%s cursor error = %v, want invalid_request", test.name, err)
		}
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		t.Fatalf("decode raw cursor: %v", err)
	}
	raw[len(raw)-1] ^= 1
	tampered := base64.RawURLEncoding.EncodeToString(raw)
	if _, err := codec.decode(tampered, "cursor-test", "owner-1", uint64(resourceTestNow), db.CursorUint, db.CursorText); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("tampered cursor error = %v, want invalid_request", err)
	}
}

func TestOversizedIdempotentProjectionFailsClosed(t *testing.T) {
	environment := newResourceTestEnvironment(t)
	userID := environment.seedUser(t, "response-limit-owner")
	tx, err := environment.repository.beginAuthorizedTx(context.Background(), userID)
	if err != nil {
		t.Fatalf("begin authorized transaction: %v", err)
	}
	defer tx.Rollback()
	mutation := resourceTestMutation(t, resourceTestKey('R'), http.MethodPost, routeEndpoints, nil, struct{}{})
	decision, err := beginControlMutation(context.Background(), tx, userID, mutation, resourceTestNow)
	if err != nil {
		t.Fatalf("begin response-limit mutation: %v", err)
	}
	if _, err := finishJSONMutation(context.Background(), tx, decision, http.StatusOK,
		map[string]string{"value": strings.Repeat("x", idempotency.MaxResponseBytes)}); !errors.Is(err, ErrResourceLimit) {
		t.Fatalf("oversized idempotent projection error = %v, want resource limit", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback response-limit transaction: %v", err)
	}
	var replayRows int
	if err := environment.store.DB().QueryRow(`SELECT count(*) FROM idempotency_records`).Scan(&replayRows); err != nil || replayRows != 0 {
		t.Fatalf("oversized projection replay rows = %d, %v", replayRows, err)
	}
}
