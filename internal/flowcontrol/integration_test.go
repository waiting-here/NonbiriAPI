package flowcontrol_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/auth"
	"github.com/waiting-here/NonbiriAPI/internal/config"
	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/egress"
	"github.com/waiting-here/NonbiriAPI/internal/endpoint"
	"github.com/waiting-here/NonbiriAPI/internal/flowcontrol"
	"github.com/waiting-here/NonbiriAPI/internal/forward"
	"github.com/waiting-here/NonbiriAPI/internal/host"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/ratelimit"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

type fixedResolver struct{}

func (fixedResolver) LookupNetIP(ctx context.Context, _ string, _ string) ([]netip.Addr, error) {
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

type integrationFixture struct {
	store      *db.Store
	codec      *countingCodec
	stack      *egress.Stack
	controller *flowcontrol.Controller
	handler    http.Handler
	upstream   *httptest.Server
	hits       *atomic.Int32
}

func upstreamCompletion(content string) string {
	body := map[string]any{
		"id": "chatcmpl-flowcontrol", "object": "chat.completion", "created": int64(1),
		"model": "upstream/model",
		"choices": []any{map[string]any{"index": 0,
			"message": map[string]any{"role": "assistant", "content": content}, "finish_reason": "stop"}},
		"usage": map[string]any{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2},
	}
	encoded, _ := json.Marshal(body)
	return string(encoded)
}

// newIntegrationFixture mounts: CallerKey auth -> flow-control middleware ->
// forward exit, with a real egress Stack as the only outbound boundary. The
// controller is wired to the store-backed per-user limit resolver.
func newIntegrationFixture(t *testing.T, rpmConfig ratelimit.RPMConfig) *integrationFixture {
	t.Helper()
	var hits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, upstreamCompletion("metered completion"))
	}))
	t.Cleanup(upstream.Close)

	master := bytes.Repeat([]byte{0x6d}, secret.MasterKeyBytes)
	vault, err := secret.New(master)
	clear(master)
	if err != nil {
		t.Fatal(err)
	}
	codec := &countingCodec{vault: vault}
	store, err := db.Open(filepath.Join(t.TempDir(), "flowcontrol.db"), codec)
	if err != nil {
		_ = vault.Close()
		t.Fatal(err)
	}
	stack, err := egress.NewStack(egress.StackOptions{
		AllowedOrigins: []string{upstream.URL},
		Resolver:       fixedResolver{},
		RequestTimeout: 3 * time.Second,
		Concurrency:    egress.ConcurrencyLimits{Global: 8, PerEndpoint: 4},
	})
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
		t.Fatal(err)
	}
	registry := endpoint.NewRegistry()
	adapter, err := openai.NewAdapter(openai.AdapterConfig{Stack: stack})
	if err != nil {
		t.Fatal(err)
	}
	runner, err := forward.NewSecureRunner(forward.SecureRunnerConfig{
		Repository: store,
		Secrets:    codec,
		Registry:   registry,
		Adapters:   []forward.Adapter{adapter},
	})
	if err != nil {
		t.Fatal(err)
	}
	service, err := forward.NewService(forward.ServiceConfig{Repository: store, Runner: runner})
	if err != nil {
		t.Fatal(err)
	}
	exit := forward.NewHandler(forward.HandlerDeps{Service: service, Identity: forward.CallerIdentity})
	controller, err := flowcontrol.New(flowcontrol.Config{RPM: rpmConfig, UserLimits: flowcontrol.DBUserLimitResolver(store)})
	if err != nil {
		t.Fatal(err)
	}
	middleware, err := flowcontrol.NewMiddleware(controller, forward.CallerIdentity)
	if err != nil {
		t.Fatal(err)
	}
	wrapped := auth.CallerKeyMiddleware(store, middleware.Wrap(exit))
	t.Cleanup(func() {
		controller.Close()
		stack.CloseIdleConnections()
		_ = store.Close()
		_ = vault.Close()
	})
	return &integrationFixture{store: store, codec: codec, stack: stack, controller: controller, handler: wrapped, upstream: upstream, hits: &hits}
}

func (f *integrationFixture) addUser(t *testing.T, name string) (int64, string) {
	t.Helper()
	user, err := f.store.CreateUser("discord-"+name, name, "")
	if err != nil {
		t.Fatal(err)
	}
	key, err := f.store.SetCallerKey(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	return user.ID, key
}

func (f *integrationFixture) setUserRPMLimit(t *testing.T, userID int64, limit int) {
	t.Helper()
	if _, err := f.store.DB().Exec(`UPDATE users SET rpm_limit=? WHERE id=?`, limit, userID); err != nil {
		t.Fatal(err)
	}
}

func (f *integrationFixture) addRoute(t *testing.T, userID int64) {
	t.Helper()
	canonical, err := f.stack.ValidateBaseURL(f.upstream.URL)
	if err != nil {
		t.Fatalf("ValidateBaseURL: %v", err)
	}
	endpointRow, err := f.store.CreateEndpoint(context.Background(), userID, string(endpoint.ConnectorOpenAICompatible), canonical, "", true, time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	keyRow, err := f.store.CreateEndpointKey(context.Background(), userID, endpointRow.ID, []byte("sk-flowcontrol-upstream"), "head", "tail", "", true, time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.ReplaceFetchedModels(context.Background(), userID, endpointRow.ID, keyRow.ID, []db.FetchedModel{{
		EndpointKeyID: keyRow.ID, UpstreamModelID: "upstream/model", Provider: "upstream", Status: "ok",
	}}, time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	modelRow, err := f.store.CreateModel(context.Background(), userID, "provider", "model", "ordered", false, time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.CreateBinding(context.Background(), userID, modelRow.ID, keyRow.ID, "upstream/model", 0, time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
}

func integrationRequest(method, path, key, body string) *http.Request {
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

func chatBody() string {
	return `{"model":"provider/model","messages":[{"role":"user","content":"hello"}],"stream":false}`
}

func envelopeCode(t *testing.T, recorder *httptest.ResponseRecorder) string {
	t.Helper()
	var envelope httperr.Envelope
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode envelope: %v body=%s", err, recorder.Body.String())
	}
	return envelope.Error.Code
}

// TestDeniedRequestNeverReachesUpstreamOrVault drives the real forward exit:
// a request denied by the shared RPM window must not hit the upstream server
// and must not decrypt a credential, while admitted requests flow through the
// shared egress stack normally.
func TestDeniedRequestNeverReachesUpstreamOrVault(t *testing.T) {
	config := ratelimit.RPMConfig{
		Window:       time.Minute,
		GlobalLimit:  100,
		PerUserLimit: 2,
		MaxUserKeys:  1024,
		MaxEvents:    4096,
		MaxKeyBytes:  64,
	}
	fixture := newIntegrationFixture(t, config)
	userID, key := fixture.addUser(t, "metered")
	fixture.addRoute(t, userID)

	request := integrationRequest(http.MethodPost, "/v1/chat/completions", key, chatBody())
	recorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "metered completion") {
		t.Fatalf("first request status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	firstHits := fixture.hits.Load()
	if firstHits != 1 {
		t.Fatalf("upstream hits=%d", firstHits)
	}

	// Second request: admitted, committed (bytes written).
	request = integrationRequest(http.MethodPost, "/v1/chat/completions", key, chatBody())
	recorder = httptest.NewRecorder()
	fixture.handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("second request status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	// Third request: per-user window full -> 429 before any outbound work.
	opensBefore := fixture.codec.opens.Load()
	hitsBefore := fixture.hits.Load()
	request = integrationRequest(http.MethodPost, "/v1/chat/completions", key, chatBody())
	recorder = httptest.NewRecorder()
	fixture.handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusTooManyRequests || envelopeCode(t, recorder) != httperr.CodeRateLimited {
		t.Fatalf("denied status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if recorder.Header().Get("Retry-After") == "" {
		t.Fatal("denied response must carry Retry-After")
	}
	if fixture.codec.opens.Load() != opensBefore {
		t.Fatal("denied request must never decrypt a credential")
	}
	if fixture.hits.Load() != hitsBefore {
		t.Fatal("denied request must never reach the upstream")
	}
}

// TestModelsNeverMeteredAndUsersIsolated verifies /v1/models does not consume
// RPM budget and different users have independent per-user windows under one
// shared global window.
func TestModelsNeverMeteredAndUsersIsolated(t *testing.T) {
	config := ratelimit.RPMConfig{
		Window:       time.Minute,
		GlobalLimit:  100,
		PerUserLimit: 1,
		MaxUserKeys:  1024,
		MaxEvents:    4096,
		MaxKeyBytes:  64,
	}
	fixture := newIntegrationFixture(t, config)
	aliceID, aliceKey := fixture.addUser(t, "isolated-alice")
	bobID, bobKey := fixture.addUser(t, "isolated-bob")
	fixture.addRoute(t, aliceID)
	fixture.addRoute(t, bobID)

	// Alice consumes her single budget slot.
	recorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(recorder, integrationRequest(http.MethodPost, "/v1/chat/completions", aliceKey, chatBody()))
	if recorder.Code != http.StatusOK {
		t.Fatalf("alice status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	// /v1/models is never metered, even at the per-user cap.
	recorder = httptest.NewRecorder()
	fixture.handler.ServeHTTP(recorder, integrationRequest(http.MethodGet, "/v1/models", aliceKey, ""))
	if recorder.Code != http.StatusOK {
		t.Fatalf("models status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	// Bob is isolated from Alice's per-user window (global still has room).
	recorder = httptest.NewRecorder()
	fixture.handler.ServeHTTP(recorder, integrationRequest(http.MethodPost, "/v1/chat/completions", bobKey, chatBody()))
	if recorder.Code != http.StatusOK {
		t.Fatalf("bob status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	// Alice is still capped: her second chat request is denied.
	recorder = httptest.NewRecorder()
	fixture.handler.ServeHTTP(recorder, integrationRequest(http.MethodPost, "/v1/chat/completions", aliceKey, chatBody()))
	if recorder.Code != http.StatusTooManyRequests {
		t.Fatalf("alice second status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

// TestDBUserLimitClamp verifies users.rpm_limit is honored within the
// administrator ceiling and clamped above it, through the real handler path.
func TestDBUserLimitClamp(t *testing.T) {
	config := ratelimit.RPMConfig{
		Window:       time.Minute,
		GlobalLimit:  100,
		PerUserLimit: 3, // administrator ceiling
		MaxUserKeys:  1024,
		MaxEvents:    4096,
		MaxKeyBytes:  64,
	}
	fixture := newIntegrationFixture(t, config)
	withinID, withinKey := fixture.addUser(t, "clamp-within")
	overID, overKey := fixture.addUser(t, "clamp-over")
	fixture.addRoute(t, withinID)
	fixture.addRoute(t, overID)

	fixture.setUserRPMLimit(t, withinID, 2) // below ceiling: honored
	fixture.setUserRPMLimit(t, overID, 999) // above ceiling: clamped to 3

	for i := 0; i < 2; i++ {
		recorder := httptest.NewRecorder()
		fixture.handler.ServeHTTP(recorder, integrationRequest(http.MethodPost, "/v1/chat/completions", withinKey, chatBody()))
		if recorder.Code != http.StatusOK {
			t.Fatalf("within user request %d status=%d body=%s", i+1, recorder.Code, recorder.Body.String())
		}
	}
	recorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(recorder, integrationRequest(http.MethodPost, "/v1/chat/completions", withinKey, chatBody()))
	if recorder.Code != http.StatusTooManyRequests {
		t.Fatalf("within user over-cap status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	for i := 0; i < 3; i++ {
		recorder := httptest.NewRecorder()
		fixture.handler.ServeHTTP(recorder, integrationRequest(http.MethodPost, "/v1/chat/completions", overKey, chatBody()))
		if recorder.Code != http.StatusOK {
			t.Fatalf("over user request %d status=%d body=%s", i+1, recorder.Code, recorder.Body.String())
		}
	}
	recorder = httptest.NewRecorder()
	fixture.handler.ServeHTTP(recorder, integrationRequest(http.MethodPost, "/v1/chat/completions", overKey, chatBody()))
	if recorder.Code != http.StatusTooManyRequests {
		t.Fatalf("over user clamped status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	// Bounded Retry-After: never zero, never oversized, and the response
	// reveals no internal counters.
	retryAfter := recorder.Header().Get("Retry-After")
	seconds, err := strconv.Atoi(retryAfter)
	if err != nil || seconds < 1 || seconds > 3600 {
		t.Fatalf("Retry-After=%q err=%v", retryAfter, err)
	}
	if body := recorder.Body.String(); strings.Contains(body, "GlobalCount") || strings.Contains(body, "Count") {
		t.Fatalf("429 leaks counters: %s", body)
	}
}
