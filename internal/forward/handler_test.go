package forward

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/auth"
	"github.com/waiting-here/NonbiriAPI/internal/config"
	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbtest"
	"github.com/waiting-here/NonbiriAPI/internal/egress"
	"github.com/waiting-here/NonbiriAPI/internal/endpoint"
	"github.com/waiting-here/NonbiriAPI/internal/host"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

type forwardResolver struct{}

func (forwardResolver) LookupNetIP(ctx context.Context, _ string, _ string) ([]netip.Addr, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
}

type countingCodec struct {
	vault *secret.Vault
	opens atomic.Int32
}

func (c *countingCodec) Seal(plaintext []byte) (string, error) { return c.vault.Seal(plaintext) }
func (c *countingCodec) Open(ciphertext string) ([]byte, error) {
	c.opens.Add(1)
	return c.vault.Open(ciphertext)
}
func (c *countingCodec) SealForContext(plaintext []byte, credentialContext secret.EndpointKeyContext) (string, error) {
	return c.vault.SealForContext(plaintext, credentialContext)
}
func (c *countingCodec) OpenForContext(ciphertext string, credentialContext secret.EndpointKeyContext) ([]byte, error) {
	c.opens.Add(1)
	return c.vault.OpenForContext(ciphertext, credentialContext)
}

type forwardFixture struct {
	store    *db.Store
	codec    *countingCodec
	stack    *egress.Stack
	registry *endpoint.Registry
	handler  http.Handler
	service  *Service
}

type fixtureUser struct {
	id  int64
	key string
}

type fixtureRoute struct {
	modelID   int64
	endpoint  int64
	keyID     int64
	bindingID int64
	cipher    string
	fullName  string
}

func newForwardFixture(t *testing.T, allowed []string, hooks Hooks, selector Selector, stackMutate func(*egress.StackOptions)) *forwardFixture {
	t.Helper()
	return newForwardFixtureCfg(t, allowed, hooks, selector, stackMutate, BackoffConfig{Base: time.Millisecond, Max: 5 * time.Millisecond})
}

// newForwardFixtureBackoff builds a fixture with a caller-supplied backoff so
// retry/cancel tests can use a wait large enough to observe or interrupt.
func newForwardFixtureBackoff(t *testing.T, allowed []string, hooks Hooks, selector Selector, backoff BackoffConfig) *forwardFixture {
	t.Helper()
	return newForwardFixtureCfg(t, allowed, hooks, selector, nil, backoff)
}

func newForwardFixtureCfg(t *testing.T, allowed []string, hooks Hooks, selector Selector, stackMutate func(*egress.StackOptions), backoff BackoffConfig) *forwardFixture {
	t.Helper()
	master := bytes.Repeat([]byte{0x6a}, secret.MasterKeyBytes)
	vault, err := secret.New(master)
	clear(master)
	if err != nil {
		t.Fatal(err)
	}
	codec := &countingCodec{vault: vault}
	dbPath := filepath.Join(t.TempDir(), "forward.db")
	dbtest.EnsureOwnerOnlyParent(t, dbPath)
	store, err := db.Open(dbPath, codec)
	if err != nil {
		_ = vault.Close()
		t.Fatal(err)
	}
	options := egress.StackOptions{
		AllowedOrigins: allowed,
		Resolver:       forwardResolver{},
		RequestTimeout: 3 * time.Second,
		Concurrency:    egress.ConcurrencyLimits{Global: 8, PerEndpoint: 4},
	}
	if stackMutate != nil {
		stackMutate(&options)
	}
	stack, err := egress.NewStack(options)
	if err != nil {
		_ = store.Close()
		_ = vault.Close()
		t.Fatal(err)
	}
	if err := stack.AddSelfOrigins(context.Background(), &config.Config{
		SiteBaseURL: "https://gateway.example",
		UserHost:    "gateway.example",
		AdminHost:   "admin.gateway.example",
		ListenAddr:  "127.0.0.1:1",
	}); err != nil {
		_ = store.Close()
		_ = vault.Close()
		t.Fatal(err)
	}
	registry := endpoint.NewRegistry()
	adapter, err := openai.NewAdapter(openai.AdapterConfig{Stack: stack})
	if err != nil {
		t.Fatal(err)
	}
	runner, err := NewSecureRunner(SecureRunnerConfig{
		Repository: store,
		Secrets:    codec,
		Registry:   registry,
		Adapters:   []Adapter{adapter},
	})
	if err != nil {
		t.Fatal(err)
	}
	safetyIdentifierKey := derivedSafetyIdentifierKey(t, vault)
	service, err := NewService(ServiceConfig{
		Repository:          store,
		Selector:            selector,
		Runner:              runner,
		Hooks:               hooks,
		Backoff:             backoff,
		SafetyIdentifierKey: safetyIdentifierKey,
	})
	clear(safetyIdentifierKey)
	if err != nil {
		t.Fatal(err)
	}
	exit := NewHandler(HandlerDeps{Service: service, Identity: CallerIdentity})
	wrapped := auth.CallerKeyMiddleware(store, exit)
	fixture := &forwardFixture{store: store, codec: codec, stack: stack, registry: registry, handler: wrapped, service: service}
	t.Cleanup(func() {
		_ = service.Close()
		stack.CloseIdleConnections()
		_ = store.Close()
		_ = vault.Close()
	})
	return fixture
}

func (f *forwardFixture) addUser(t *testing.T, name string) fixtureUser {
	t.Helper()
	user, err := f.store.CreateUser("discord-"+name, name, "")
	if err != nil {
		t.Fatal(err)
	}
	key, err := f.store.SetCallerKey(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	return fixtureUser{id: user.ID, key: key}
}

func (f *forwardFixture) addRoute(t *testing.T, userID int64, baseURL, provider, model, upstreamModel, upstreamSecret string, ord int64) fixtureRoute {
	t.Helper()
	canonical, err := f.stack.ValidateBaseURL(baseURL)
	if err != nil {
		t.Fatalf("ValidateBaseURL: %v", err)
	}
	endpointRow, err := f.store.CreateEndpoint(context.Background(), userID, string(endpoint.ConnectorOpenAICompatible), canonical, "", true, time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	keyRow, err := f.store.CreateEndpointKey(context.Background(), userID, endpointRow.ID, []byte(upstreamSecret), "head", "tail", "", true, time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := f.store.GetEndpointKeyCiphertext(context.Background(), userID, endpointRow.ID, keyRow.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.ReplaceFetchedModels(context.Background(), userID, endpointRow.ID, keyRow.ID, []db.FetchedModel{{
		EndpointKeyID: keyRow.ID, UpstreamModelID: upstreamModel, Provider: "upstream", Status: "ok",
	}}, time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	modelRow, err := f.store.CreateModel(context.Background(), userID, provider, model, "ordered", false, time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	binding, err := f.store.CreateBinding(context.Background(), userID, modelRow.ID, keyRow.ID, upstreamModel, ord, time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	return fixtureRoute{
		modelID: modelRow.ID, endpoint: endpointRow.ID, keyID: keyRow.ID,
		bindingID: binding.ID, cipher: ciphertext, fullName: provider + "/" + model,
	}
}

func callerRequest(method, path, key, body string) *http.Request {
	request := httptest.NewRequest(method, "https://gateway.example"+path, strings.NewReader(body))
	request = request.WithContext(host.WithStation(request.Context(), host.StationUser))
	if key != "" {
		request.Header.Set("Authorization", "Bearer "+key)
	}
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	return request
}

func performCaller(handler http.Handler, request *http.Request) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func responseCode(t *testing.T, recorder *httptest.ResponseRecorder) string {
	t.Helper()
	var envelope httperr.Envelope
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode error envelope: %v body=%s", err, recorder.Body.String())
	}
	return envelope.Error.Code
}

func forwardCompletion(content string) string {
	body := map[string]any{
		"id": "chatcmpl-forward", "object": "chat.completion", "created": int64(1), "model": "upstream/model",
		"choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": content}, "finish_reason": "stop"}},
		"usage":   map[string]any{"prompt_tokens": 2, "completion_tokens": 3, "total_tokens": 5},
	}
	encoded, _ := json.Marshal(body)
	return string(encoded)
}

func TestCallerKeyOnlyModelsOwnershipAndNoDecrypt(t *testing.T) {
	var upstreamHits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		upstreamHits.Add(1)
		http.Error(writer, "should not be called", http.StatusInternalServerError)
	}))
	defer upstream.Close()
	fixture := newForwardFixture(t, []string{upstream.URL}, Hooks{}, nil, nil)
	alice := fixture.addUser(t, "alice")
	bob := fixture.addUser(t, "bob")
	aliceRoute := fixture.addRoute(t, alice.id, upstream.URL, "provider/part", "model", "UPSTREAM-ONLY-MARKER", "sk-alice", 0)
	_ = fixture.addRoute(t, bob.id, upstream.URL, "hidden", "model", "hidden-upstream", "sk-bob", 0)
	fixture.codec.opens.Store(0)

	recorder := performCaller(fixture.handler, callerRequest(http.MethodGet, "/v1/models", alice.key, ""))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if recorder.Header().Get("Cache-Control") != "no-store" || fixture.codec.opens.Load() != 0 || upstreamHits.Load() != 0 {
		t.Fatalf("cache=%q opens=%d hits=%d", recorder.Header().Get("Cache-Control"), fixture.codec.opens.Load(), upstreamHits.Load())
	}
	body := recorder.Body.String()
	if !strings.Contains(body, aliceRoute.fullName) || strings.Contains(body, "hidden/model") || strings.Contains(body, "UPSTREAM-ONLY-MARKER") || strings.Contains(body, "sk-alice") {
		t.Fatalf("model list ownership/upstream isolation failed: %s", body)
	}

	for _, request := range []*http.Request{
		callerRequest(http.MethodGet, "/v1/models", "", ""),
		callerRequest(http.MethodGet, "/v1/models", "nbk_invalid", ""),
		callerRequest(http.MethodGet, "/v1/models", "", ""),
	} {
		request.AddCookie(&http.Cookie{Name: "nonbiri_session", Value: "browser-session"})
		response := performCaller(fixture.handler, request)
		if response.Code != http.StatusUnauthorized || responseCode(t, response) != httperr.CodeUnauthorized {
			t.Fatalf("non-caller identity status=%d body=%s", response.Code, response.Body.String())
		}
	}

	if err := fixture.store.BanUser(alice.id, "banned"); err != nil {
		t.Fatal(err)
	}
	banned := performCaller(fixture.handler, callerRequest(http.MethodGet, "/v1/models", alice.key, ""))
	if banned.Code != http.StatusUnauthorized || responseCode(t, banned) != httperr.CodeUnauthorized {
		t.Fatalf("banned key status=%d body=%s", banned.Code, banned.Body.String())
	}
}

func TestForwardRequestSafetyHooksAndSecretSinks(t *testing.T) {
	const upstreamSecret = "sk-forward-never-leak"
	const promptMarker = "PRIVATE-PROMPT-CONTENT-MARKER"
	var capturedBodiesMu sync.Mutex
	capturedBodies := make(map[string][]byte)
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Cookie") != "" || request.Header.Get("X-Forwarded-For") != "" || request.Header.Get("X-Caller-Header") != "" {
			t.Errorf("caller headers reached upstream: %v", request.Header)
		}
		body, _ := io.ReadAll(request.Body)
		var root map[string]json.RawMessage
		_ = json.Unmarshal(body, &root)
		var safety string
		_ = json.Unmarshal(root["safety_identifier"], &safety)
		capturedBodiesMu.Lock()
		capturedBodies[request.Header.Get("Authorization")] = append([]byte(nil), body...)
		capturedBodiesMu.Unlock()
		if safety == "forged" {
			t.Error("caller safety_identifier reached upstream")
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.Header().Set("Set-Cookie", "bad=1")
		_, _ = io.WriteString(writer, forwardCompletion("safe completion"))
	}))
	defer upstream.Close()

	var attemptsMu sync.Mutex
	var attempts []AttemptRecord
	var usages []UsageRecord
	fixture := newForwardFixture(t, []string{upstream.URL}, Hooks{
		Attempt: func(record AttemptRecord) {
			attemptsMu.Lock()
			defer attemptsMu.Unlock()
			attempts = append(attempts, record)
		},
		Usage: func(record UsageRecord) {
			attemptsMu.Lock()
			defer attemptsMu.Unlock()
			usages = append(usages, record)
		},
	}, nil, nil)
	alice := fixture.addUser(t, "safety-alice")
	bob := fixture.addUser(t, "safety-bob")
	aliceRoute := fixture.addRoute(t, alice.id, upstream.URL, "opaque/provider", "model", "upstream/model", upstreamSecret, 0)
	_ = fixture.addRoute(t, bob.id, upstream.URL, "opaque/provider", "model", "upstream/model", "sk-bob-different", 0)

	requestBody := `{"model":"opaque/provider/model","messages":[{"role":"user","content":"` + promptMarker + `"}],"stream":false,"safety_identifier":"forged","unknown":{"kept":true}}`
	aliceRequest := callerRequest(http.MethodPost, "/v1/chat/completions", alice.key, requestBody)
	aliceRequest.Header.Set("Cookie", "caller-cookie=private")
	aliceRequest.Header.Set("X-Forwarded-For", "127.0.0.1")
	aliceRequest.Header.Set("X-Caller-Header", "not-forwarded")
	aliceResponse := performCaller(fixture.handler, aliceRequest)
	if aliceResponse.Code != http.StatusOK || !strings.Contains(aliceResponse.Body.String(), "safe completion") {
		t.Fatalf("alice status=%d body=%s", aliceResponse.Code, aliceResponse.Body.String())
	}
	bobResponse := performCaller(fixture.handler, callerRequest(http.MethodPost, "/v1/chat/completions", bob.key, requestBody))
	if bobResponse.Code != http.StatusOK {
		t.Fatalf("bob status=%d body=%s", bobResponse.Code, bobResponse.Body.String())
	}
	if aliceResponse.Header().Get("Set-Cookie") != "" || aliceResponse.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("unsafe/cache headers=%v", aliceResponse.Header())
	}

	capturedBodiesMu.Lock()
	aliceUpstream := capturedBodies["Bearer "+upstreamSecret]
	bobUpstream := capturedBodies["Bearer sk-bob-different"]
	capturedBodiesMu.Unlock()
	for userID, body := range map[int64][]byte{alice.id: aliceUpstream, bob.id: bobUpstream} {
		var root map[string]json.RawMessage
		if err := json.Unmarshal(body, &root); err != nil {
			t.Fatalf("captured body user=%d: %v %s", userID, err, body)
		}
		var safety, model string
		_ = json.Unmarshal(root["safety_identifier"], &safety)
		_ = json.Unmarshal(root["model"], &model)
		wantSafety := expectedSafetyIdentifier(t, fixture.codec.vault, userID)
		if safety != wantSafety || safety == "forged" || safety == strconv.FormatInt(userID, 10) || model != "upstream/model" || !bytes.Contains(root["unknown"], []byte("true")) {
			t.Fatalf("authority user=%d safety=%q want=%q model=%q body=%s", userID, safety, wantSafety, model, body)
		}
		assertSafetyIdentifierFormat(t, safety)
		if bytes.Count(body, []byte(`"safety_identifier"`)) != 1 || strings.Contains(safety, "nbu_v1_") {
			t.Fatalf("non-canonical safety identifier emission user=%d body=%s", userID, body)
		}
	}
	aliceSafety := expectedSafetyIdentifier(t, fixture.codec.vault, alice.id)
	bobSafety := expectedSafetyIdentifier(t, fixture.codec.vault, bob.id)
	if aliceSafety == bobSafety || aliceSafety != expectedSafetyIdentifier(t, fixture.codec.vault, alice.id) {
		t.Fatal("safety identifiers are not stable and user-distinct")
	}

	attemptsMu.Lock()
	attemptJSON, _ := json.Marshal(attempts)
	usageJSON, _ := json.Marshal(usages)
	attemptsMu.Unlock()
	for _, forbidden := range []string{upstreamSecret, aliceRoute.cipher, promptMarker, "forged", requestBody} {
		if strings.Contains(string(attemptJSON), forbidden) || strings.Contains(string(usageJSON), forbidden) || strings.Contains(aliceResponse.Body.String(), forbidden) {
			t.Fatalf("hook/response leaked %q attempts=%s usage=%s response=%s", forbidden, attemptJSON, usageJSON, aliceResponse.Body.String())
		}
	}
	if len(attempts) != 2 || len(usages) != 2 || !usages[0].Usage.Present {
		t.Fatalf("attempts=%+v usages=%+v", attempts, usages)
	}
	var persisted int
	for _, table := range []string{"request_logs", "user_issues", "admin_alerts"} {
		if err := fixture.store.DB().QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&persisted); err != nil || persisted != 0 {
			t.Fatalf("table %s count=%d err=%v", table, persisted, err)
		}
	}
}

func TestForwardOwnershipDisabledCacheAndExactFullNameNeverDial(t *testing.T) {
	var hits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, forwardCompletion("ok"))
	}))
	defer upstream.Close()
	fixture := newForwardFixture(t, []string{upstream.URL}, Hooks{}, nil, nil)
	alice := fixture.addUser(t, "owner-alice")
	bob := fixture.addUser(t, "owner-bob")
	route := fixture.addRoute(t, alice.id, upstream.URL, "a/b", "c", "upstream/model", "sk-owner", 0)
	_ = fixture.addRoute(t, bob.id, upstream.URL, "hidden", "model", "upstream/model", "sk-hidden", 0)

	for _, model := range []string{"hidden/model", "a/b", "b/c", "a/c"} {
		body := `{"model":` + strconv.Quote(model) + `,"messages":[]}`
		response := performCaller(fixture.handler, callerRequest(http.MethodPost, "/v1/chat/completions", alice.key, body))
		if response.Code != http.StatusNotFound || responseCode(t, response) != httperr.CodeNotFound {
			t.Fatalf("model %q status=%d body=%s", model, response.Code, response.Body.String())
		}
	}
	if hits.Load() != 0 {
		t.Fatalf("ownership/full-name failures dialed upstream %d times", hits.Load())
	}

	if _, err := fixture.store.DB().Exec(`UPDATE endpoint_keys SET enabled=0 WHERE id=?`, route.keyID); err != nil {
		t.Fatal(err)
	}
	body := `{"model":"a/b/c","messages":[]}`
	disabled := performCaller(fixture.handler, callerRequest(http.MethodPost, "/v1/chat/completions", alice.key, body))
	if disabled.Code != http.StatusServiceUnavailable || responseCode(t, disabled) != httperr.CodeUnboundModel || hits.Load() != 0 {
		t.Fatalf("disabled status=%d body=%s hits=%d", disabled.Code, disabled.Body.String(), hits.Load())
	}
	if _, err := fixture.store.DB().Exec(`UPDATE endpoint_keys SET enabled=1 WHERE id=?`, route.keyID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.DB().Exec(`DELETE FROM fetched_models WHERE endpoint_key_id=?`, route.keyID); err != nil {
		t.Fatal(err)
	}
	uncached := performCaller(fixture.handler, callerRequest(http.MethodPost, "/v1/chat/completions", alice.key, body))
	if uncached.Code != http.StatusServiceUnavailable || responseCode(t, uncached) != httperr.CodeUnboundModel || hits.Load() != 0 {
		t.Fatalf("uncached status=%d body=%s hits=%d", uncached.Code, uncached.Body.String(), hits.Load())
	}
}

func TestSingleAttemptDoesNotSilentlyRetry(t *testing.T) {
	var firstHits, secondHits atomic.Int32
	first := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		firstHits.Add(1)
		writer.Header().Set("Content-Type", "application/json")
		http.Error(writer, "first failed with secret", http.StatusServiceUnavailable)
	}))
	defer first.Close()
	second := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		secondHits.Add(1)
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, forwardCompletion("second"))
	}))
	defer second.Close()
	fixture := newForwardFixture(t, []string{first.URL, second.URL}, Hooks{}, nil, nil)
	user := fixture.addUser(t, "no-retry")
	firstRoute := fixture.addRoute(t, user.id, first.URL, "p", "m", "upstream/model", "sk-first", 0)

	canonical, _ := fixture.stack.ValidateBaseURL(second.URL)
	secondEndpoint, err := fixture.store.CreateEndpoint(context.Background(), user.id, string(endpoint.ConnectorOpenAICompatible), canonical, "", true, 2)
	if err != nil {
		t.Fatal(err)
	}
	secondKey, err := fixture.store.CreateEndpointKey(context.Background(), user.id, secondEndpoint.ID, []byte("sk-second"), "", "", "", true, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.ReplaceFetchedModels(context.Background(), user.id, secondEndpoint.ID, secondKey.ID, []db.FetchedModel{{UpstreamModelID: "upstream/model"}}, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.CreateBinding(context.Background(), user.id, firstRoute.modelID, secondKey.ID, "upstream/model", 1, 2); err != nil {
		t.Fatal(err)
	}

	response := performCaller(fixture.handler, callerRequest(http.MethodPost, "/v1/chat/completions", user.key, `{"model":"p/m","messages":[]}`))
	if response.Code != http.StatusBadGateway || responseCode(t, response) != httperr.CodeUpstream {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "first failed with secret") || strings.Contains(response.Body.String(), "sk-first") || strings.Contains(response.Body.String(), firstRoute.cipher) {
		t.Fatalf("upstream failure leaked content or credential: %s", response.Body.String())
	}
	if firstHits.Load() != 1 || secondHits.Load() != 0 {
		t.Fatalf("unexpected retry first=%d second=%d", firstHits.Load(), secondHits.Load())
	}
}

func TestForwardSSRFAndUnknownConnectorFailClosedWithoutSensitiveDiagnostics(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, forwardCompletion("ok"))
	}))
	defer upstream.Close()
	fixture := newForwardFixture(t, []string{upstream.URL}, Hooks{}, nil, nil)
	user := fixture.addUser(t, "blocked")
	route := fixture.addRoute(t, user.id, upstream.URL, "p", "m", "upstream/model", "sk-blocked-secret", 0)
	body := `{"model":"p/m","messages":[]}`

	if _, err := fixture.store.DB().Exec(`UPDATE endpoints SET base_url='http://169.254.169.254/latest/meta-data' WHERE id=?`, route.endpoint); err != nil {
		t.Fatal(err)
	}
	blocked := performCaller(fixture.handler, callerRequest(http.MethodPost, "/v1/chat/completions", user.key, body))
	if blocked.Code != http.StatusInternalServerError || responseCode(t, blocked) != httperr.CodeInternal {
		t.Fatalf("blocked status=%d body=%s", blocked.Code, blocked.Body.String())
	}
	for _, forbidden := range []string{"169.254.169.254", "latest/meta-data", "sk-blocked-secret", route.cipher} {
		if strings.Contains(blocked.Body.String(), forbidden) {
			t.Fatalf("blocked response leaked %q: %s", forbidden, blocked.Body.String())
		}
	}

	if _, err := fixture.store.DB().Exec(`UPDATE endpoints SET base_url='https://gateway.example:443' WHERE id=?`, route.endpoint); err != nil {
		t.Fatal(err)
	}
	self := performCaller(fixture.handler, callerRequest(http.MethodPost, "/v1/chat/completions", user.key, body))
	if self.Code != http.StatusInternalServerError || responseCode(t, self) != httperr.CodeInternal || strings.Contains(self.Body.String(), "gateway.example") {
		t.Fatalf("self-origin status=%d body=%s", self.Code, self.Body.String())
	}

	if _, err := fixture.store.DB().Exec(`UPDATE endpoints SET base_url=?, connector_type='unknown-protocol' WHERE id=?`, upstream.URL, route.endpoint); err != nil {
		t.Fatal(err)
	}
	unknown := performCaller(fixture.handler, callerRequest(http.MethodPost, "/v1/chat/completions", user.key, body))
	if unknown.Code != http.StatusInternalServerError || responseCode(t, unknown) != httperr.CodeInternal {
		t.Fatalf("unknown connector status=%d body=%s", unknown.Code, unknown.Body.String())
	}

	const corruptCiphertext = "CIPHERTEXT-LEAK-MARKER"
	if _, err := fixture.store.DB().Exec(`UPDATE endpoints SET connector_type='openai-compatible' WHERE id=?`, route.endpoint); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.DB().Exec(`UPDATE endpoint_keys SET encrypted_secret=? WHERE id=?`, corruptCiphertext, route.keyID); err != nil {
		t.Fatal(err)
	}
	corrupt := performCaller(fixture.handler, callerRequest(http.MethodPost, "/v1/chat/completions", user.key, body))
	if corrupt.Code != http.StatusInternalServerError || responseCode(t, corrupt) != httperr.CodeInternal || strings.Contains(corrupt.Body.String(), corruptCiphertext) {
		t.Fatalf("corrupt credential status=%d body=%s", corrupt.Code, corrupt.Body.String())
	}
}

func TestForwardContextExchangeAndLegacyEnvelopeNeverDial(t *testing.T) {
	for _, attack := range []string{"user", "endpoint", "key", "origin", "legacy", "future", "oversized", "oversized-origin"} {
		attack := attack
		t.Run(attack, func(t *testing.T) {
			var hits atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				hits.Add(1)
				writer.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(writer, forwardCompletion("must-not-be-reached"))
			}))
			defer upstream.Close()
			fixture := newForwardFixture(t, []string{upstream.URL}, Hooks{}, nil, nil)
			user := fixture.addUser(t, "context-"+attack)
			route := fixture.addRoute(t, user.id, upstream.URL, "p", "m", "upstream/model", "context-secret-marker", 0)
			endpointRow, err := fixture.store.GetEndpoint(context.Background(), user.id, route.endpoint)
			if err != nil {
				t.Fatal(err)
			}
			_, origin, err := egress.CanonicalEndpointTarget(endpointRow.BaseURL)
			if err != nil {
				t.Fatal(err)
			}

			wrongUser, wrongEndpoint, wrongKey, wrongOrigin := user.id, route.endpoint, route.keyID, origin
			switch attack {
			case "user":
				wrongUser++
			case "endpoint":
				wrongEndpoint++
			case "key":
				wrongKey++
			case "origin":
				wrongOrigin = "https://forward-context-exchange.example:443"
			}
			var ciphertext string
			switch attack {
			case "legacy":
				ciphertext, err = fixture.codec.Seal([]byte("legacy-forward-fallback-marker"))
			case "future":
				ciphertext = strings.Replace(route.cipher, ":v2:", ":v7:", 1)
			case "oversized":
				ciphertext = strings.Repeat("A", 129<<10)
			case "oversized-origin":
				ciphertext = route.cipher
				if _, updateErr := fixture.store.DB().Exec(`UPDATE endpoints SET base_url=? WHERE id=?`, strings.Repeat("a", 4097), route.endpoint); updateErr != nil {
					t.Fatal(updateErr)
				}
			default:
				credentialContext, contextErr := secret.NewEndpointKeyContext(wrongUser, wrongEndpoint, wrongKey, wrongOrigin)
				if contextErr != nil {
					t.Fatal(contextErr)
				}
				ciphertext, err = fixture.codec.SealForContext([]byte("context-exchange-secret-marker"), credentialContext)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := fixture.store.DB().Exec(`UPDATE endpoint_keys SET encrypted_secret=? WHERE id=?`, ciphertext, route.keyID); err != nil {
				t.Fatal(err)
			}

			response := performCaller(fixture.handler, callerRequest(http.MethodPost, "/v1/chat/completions", user.key, `{"model":"p/m","messages":[]}`))
			if response.Code != http.StatusInternalServerError || responseCode(t, response) != httperr.CodeInternal {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if got := hits.Load(); got != 0 {
				t.Fatalf("wrong context dialed upstream %d times", got)
			}
			for _, marker := range []string{ciphertext, origin, wrongOrigin, "context-exchange", "legacy-forward", "credential unavailable"} {
				if strings.Contains(response.Body.String(), marker) {
					t.Fatal("credential failure exposed protected detail")
				}
			}
		})
	}
}

func TestForwardRequestValidationStableErrorsAndNoDial(t *testing.T) {
	var hits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
	}))
	defer upstream.Close()
	fixture := newForwardFixture(t, []string{upstream.URL}, Hooks{}, nil, nil)
	user := fixture.addUser(t, "validation")
	_ = fixture.addRoute(t, user.id, upstream.URL, "p", "m", "upstream/model", "sk-validation", 0)

	tests := []struct {
		name   string
		body   string
		code   string
		status int
	}{
		{"empty", "", httperr.CodeInvalidRequest, http.StatusBadRequest},
		{"array", `[]`, httperr.CodeInvalidRequest, http.StatusBadRequest},
		{"invalid", `{`, httperr.CodeInvalidRequest, http.StatusBadRequest},
		{"trailing", `{"model":"p/m"}{}`, httperr.CodeInvalidRequest, http.StatusBadRequest},
		{"missing model", `{"messages":[]}`, httperr.CodeInvalidRequest, http.StatusBadRequest},
		{"stream type", `{"model":"p/m","stream":"yes"}`, httperr.CodeInvalidRequest, http.StatusBadRequest},
		{"model control", "{\"model\":\"p/m\\n\"}", httperr.CodeInvalidRequest, http.StatusBadRequest},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := callerRequest(http.MethodPost, "/v1/chat/completions", user.key, test.body)
			if test.body == "" {
				request.Header.Set("Content-Type", "application/json")
			}
			response := performCaller(fixture.handler, request)
			if response.Code != test.status || responseCode(t, response) != test.code {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}

	oversized := `{"model":"p/m","x":"` + strings.Repeat("x", int(openai.MaxRequestBodyBytes)) + `"}`
	overResponse := performCaller(fixture.handler, callerRequest(http.MethodPost, "/v1/chat/completions", user.key, oversized))
	if overResponse.Code != http.StatusRequestEntityTooLarge || responseCode(t, overResponse) != httperr.CodePayloadTooLarge {
		t.Fatalf("oversize status=%d body=%s", overResponse.Code, overResponse.Body.String())
	}
	badContentType := callerRequest(http.MethodPost, "/v1/chat/completions", user.key, `{"model":"p/m"}`)
	badContentType.Header.Set("Content-Type", "text/plain")
	contentResponse := performCaller(fixture.handler, badContentType)
	if contentResponse.Code != http.StatusBadRequest || responseCode(t, contentResponse) != httperr.CodeInvalidRequest {
		t.Fatalf("content type status=%d body=%s", contentResponse.Code, contentResponse.Body.String())
	}
	encodedRequest := callerRequest(http.MethodPost, "/v1/chat/completions", user.key, `{"model":"p/m"}`)
	encodedRequest.Header.Set("Content-Encoding", "gzip")
	encodedResponse := performCaller(fixture.handler, encodedRequest)
	if encodedResponse.Code != http.StatusBadRequest || responseCode(t, encodedResponse) != httperr.CodeInvalidRequest {
		t.Fatalf("content encoding status=%d body=%s", encodedResponse.Code, encodedResponse.Body.String())
	}
	duplicateType := callerRequest(http.MethodPost, "/v1/chat/completions", user.key, `{"model":"p/m"}`)
	duplicateType.Header.Add("Content-Type", "text/plain")
	duplicateResponse := performCaller(fixture.handler, duplicateType)
	if duplicateResponse.Code != http.StatusBadRequest || responseCode(t, duplicateResponse) != httperr.CodeInvalidRequest {
		t.Fatalf("duplicate content type status=%d body=%s", duplicateResponse.Code, duplicateResponse.Body.String())
	}
	if hits.Load() != 0 {
		t.Fatalf("invalid requests dialed upstream %d times", hits.Load())
	}
}

func TestTargetInvalidationBetweenSelectionAndDispatchNeverDials(t *testing.T) {
	var hits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
	}))
	defer upstream.Close()
	var fixture *forwardFixture
	selector := selectorFunc(func(_ context.Context, selection Selection) ([]int64, error) {
		candidate := selection.Candidates[0]
		if _, err := fixture.store.DB().Exec(`UPDATE endpoint_keys SET enabled=0 WHERE id=?`, candidate.EndpointKeyID); err != nil {
			return nil, err
		}
		return []int64{candidate.BindingID}, nil
	})
	fixture = newForwardFixture(t, []string{upstream.URL}, Hooks{}, selector, nil)
	user := fixture.addUser(t, "invalidation")
	_ = fixture.addRoute(t, user.id, upstream.URL, "p", "m", "upstream/model", "sk-invalidation", 0)
	fixture.codec.opens.Store(0)
	response := performCaller(fixture.handler, callerRequest(http.MethodPost, "/v1/chat/completions", user.key, `{"model":"p/m"}`))
	if response.Code != http.StatusBadGateway || responseCode(t, response) != httperr.CodeUpstream || hits.Load() != 0 || fixture.codec.opens.Load() != 0 {
		t.Fatalf("invalidation status=%d body=%s hits=%d opens=%d", response.Code, response.Body.String(), hits.Load(), fixture.codec.opens.Load())
	}
}

func TestSelectorCannotInjectUnprojectedBinding(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		t.Error("selector injection reached upstream")
	}))
	defer upstream.Close()
	selector := selectorFunc(func(context.Context, Selection) ([]int64, error) { return []int64{999999}, nil })
	fixture := newForwardFixture(t, []string{upstream.URL}, Hooks{}, selector, nil)
	user := fixture.addUser(t, "selector")
	_ = fixture.addRoute(t, user.id, upstream.URL, "p", "m", "upstream/model", "sk-selector", 0)
	response := performCaller(fixture.handler, callerRequest(http.MethodPost, "/v1/chat/completions", user.key, `{"model":"p/m"}`))
	if response.Code != http.StatusInternalServerError || responseCode(t, response) != httperr.CodeInternal {
		t.Fatalf("selector injection status=%d body=%s", response.Code, response.Body.String())
	}
}

type selectorFunc func(context.Context, Selection) ([]int64, error)

func (f selectorFunc) Select(ctx context.Context, selection Selection) ([]int64, error) {
	return f(ctx, selection)
}

func TestHandlerMethodAndIdentityBoundaries(t *testing.T) {
	fixture := newForwardFixture(t, nil, Hooks{}, nil, nil)
	for _, path := range []string{"/v1/models", "/v1/chat/completions"} {
		response := performCaller(fixture.handler, callerRequest(http.MethodDelete, path, "", ""))
		// Caller middleware authenticates before the method handler, avoiding a
		// method oracle for unauthenticated platform-exit requests.
		if response.Code != http.StatusUnauthorized || responseCode(t, response) != httperr.CodeUnauthorized {
			t.Fatalf("unauthenticated method status=%d body=%s", response.Code, response.Body.String())
		}
	}
	user := fixture.addUser(t, "method")
	response := performCaller(fixture.handler, callerRequest(http.MethodDelete, "/v1/models", user.key, ""))
	if response.Code != http.StatusMethodNotAllowed || responseCode(t, response) != httperr.CodeMethodNotAllowed {
		t.Fatalf("method status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestSecureRunnerConstructionAndTargetInvalidation(t *testing.T) {
	if _, err := NewSecureRunner(SecureRunnerConfig{}); err == nil {
		t.Fatal("empty runner config accepted")
	}
	if !errors.Is(ErrModelNotFound, ErrModelNotFound) {
		t.Fatal("sentinel sanity")
	}
}
