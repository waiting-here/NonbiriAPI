// Independent audit: forwarding exit contract over real HTTP with a mock
// upstream. The fixture below is built from scratch (no reuse of the internal
// package fixture) so the ownership, stream-termination, cancellation,
// backpressure, usage, and routing boundaries are exercised through the same
// exported constructors production uses.
package forward_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
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
	"github.com/waiting-here/NonbiriAPI/internal/egress"
	"github.com/waiting-here/NonbiriAPI/internal/endpoint"
	"github.com/waiting-here/NonbiriAPI/internal/forward"
	"github.com/waiting-here/NonbiriAPI/internal/host"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

type auditFixture struct {
	store    *db.Store
	vault    *secret.Vault
	stack    *egress.Stack
	handler  http.Handler
	service  *forward.Service
	hooks    forward.Hooks
	registry *endpoint.Registry
}

type auditUser struct {
	id  int64
	key string
}

func auditChunk(content string) string {
	return `{"id":"c","object":"chat.completion.chunk","created":1,"model":"up/model","choices":[{"index":0,"delta":{"content":` + strconv.Quote(content) + `}}]}`
}

const auditUsageChunk = `{"id":"c","object":"chat.completion.chunk","created":1,"model":"up/model","choices":[],"usage":{"prompt_tokens":3,"completion_tokens":7,"total_tokens":10}}`

func newAuditFixture(t *testing.T, upstreamURLs []string, hooks forward.Hooks) *auditFixture {
	t.Helper()
	master := bytes.Repeat([]byte{0x3c}, secret.MasterKeyBytes)
	vault, err := secret.New(master)
	clear(master)
	if err != nil {
		t.Fatal(err)
	}
	store, err := db.Open(filepath.Join(t.TempDir(), "audit-forward.db"), vault)
	if err != nil {
		_ = vault.Close()
		t.Fatal(err)
	}
	stack, err := egress.NewStack(egress.StackOptions{
		AllowedOrigins: upstreamURLs,
		RequestTimeout: 10 * time.Second,
		Concurrency:    egress.ConcurrencyLimits{Global: 8, PerEndpoint: 4},
	})
	if err != nil {
		_ = store.Close()
		_ = vault.Close()
		t.Fatal(err)
	}
	if err := stack.AddSelfOrigins(context.Background(), &config.Config{
		SiteBaseURL: "https://127.0.0.1", UserHost: "127.0.0.1", AdminHost: "127.0.0.2",
		ListenAddr: "127.0.0.1:1",
	}); err != nil {
		t.Fatal(err)
	}
	registry := endpoint.NewRegistry()
	adapter, err := openai.NewAdapter(openai.AdapterConfig{Stack: stack})
	if err != nil {
		t.Fatal(err)
	}
	runner, err := forward.NewSecureRunner(forward.SecureRunnerConfig{
		Repository: store, Secrets: vault, Registry: registry, Adapters: []forward.Adapter{adapter},
	})
	if err != nil {
		t.Fatal(err)
	}
	service, err := forward.NewService(forward.ServiceConfig{
		Repository: store, Runner: runner, Hooks: hooks,
		Backoff: forward.BackoffConfig{Base: 5 * time.Millisecond, Max: 10 * time.Millisecond},
	})
	if err != nil {
		t.Fatal(err)
	}
	exit := forward.NewHandler(forward.HandlerDeps{Service: service, Identity: forward.CallerIdentity})
	handler := auth.CallerKeyMiddleware(store, exit)
	fixture := &auditFixture{store: store, vault: vault, stack: stack, handler: handler, service: service, hooks: hooks, registry: registry}
	t.Cleanup(func() {
		stack.CloseIdleConnections()
		_ = store.Close()
		_ = vault.Close()
	})
	return fixture
}

func (f *auditFixture) addUser(t *testing.T) auditUser {
	t.Helper()
	user, err := f.store.CreateUser("discord-audit-fwd", "forward-user", "")
	if err != nil {
		t.Fatal(err)
	}
	key, err := f.store.SetCallerKey(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	return auditUser{id: user.ID, key: key}
}

// addRoute wires one (endpoint, key, fetched model, platform model, binding)
// set; ord selects the ordered position and silentRetry the model switch.
// It returns the created platform model id so more bindings can be added to
// the same model with addBinding.
func (f *auditFixture) addRoute(t *testing.T, userID int64, baseURL, upstreamModel, upstreamSecret string, ord int64, silentRetry bool) int64 {
	t.Helper()
	model, err := f.store.CreateModel(context.Background(), userID, "provider", "model", "ordered", silentRetry, time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	f.addBinding(t, userID, model.ID, baseURL, upstreamModel, upstreamSecret, ord)
	return model.ID
}

// addBinding wires another (endpoint, key, fetched model, binding) onto an
// existing platform model at the given ordered position.
func (f *auditFixture) addBinding(t *testing.T, userID, modelID int64, baseURL, upstreamModel, upstreamSecret string, ord int64) {
	t.Helper()
	canonical, err := f.stack.ValidateBaseURL(baseURL)
	if err != nil {
		t.Fatalf("ValidateBaseURL: %v", err)
	}
	ep, err := f.store.CreateEndpoint(context.Background(), userID, string(endpoint.ConnectorOpenAICompatible), canonical, "", true, time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := f.vault.Seal([]byte(upstreamSecret))
	if err != nil {
		t.Fatal(err)
	}
	ek, err := f.store.CreateEndpointKey(context.Background(), userID, ep.ID, ciphertext, "head", "tail", "", true, time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.ReplaceFetchedModels(context.Background(), userID, ep.ID, ek.ID, []db.FetchedModel{{
		EndpointKeyID: ek.ID, UpstreamModelID: upstreamModel, Provider: "up", Status: "ok",
	}}, time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.CreateBinding(context.Background(), userID, modelID, ek.ID, upstreamModel, ord, time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
}

func (f *auditFixture) call(t *testing.T, user auditUser, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "https://127.0.0.1"+path, strings.NewReader(body))
	req = req.WithContext(host.WithStation(req.Context(), host.StationUser))
	req.Header.Set("Authorization", "Bearer "+user.key)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	return rec
}

func auditErrCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode envelope: %v body=%q", err, rec.Body.String())
	}
	return envelope.Error.Code
}

func TestAuditForwardStreamExplicitDoneAndUsage(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		flusher.Flush()
		_, _ = io.WriteString(w, "data: "+auditChunk("hello")+"\n\n")
		flusher.Flush()
		_, _ = io.WriteString(w, "data: "+auditUsageChunk+"\n\n")
		flusher.Flush()
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer upstream.Close()
	var usageMu sync.Mutex
	var usageRecords []forward.UsageRecord
	hook := forward.Hooks{Usage: func(record forward.UsageRecord) {
		usageMu.Lock()
		usageRecords = append(usageRecords, record)
		usageMu.Unlock()
	}}
	fixture := newAuditFixture(t, []string{upstream.URL}, hook)
	user := fixture.addUser(t)
	fixture.addRoute(t, user.id, upstream.URL, "up/model", "sk-secret-audit", 0, false)

	rec := fixture.call(t, user, "/v1/chat/completions", `{"model":"provider/model","messages":[],"stream":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.HasSuffix(strings.TrimSpace(body), "data: [DONE]") {
		t.Fatalf("stream did not end with explicit DONE: %q", body)
	}
	if !strings.Contains(body, "hello") {
		t.Fatalf("chunk missing: %q", body)
	}
	if strings.Contains(body, "sk-secret-audit") {
		t.Fatal("stream leaked the upstream secret")
	}
	usageMu.Lock()
	defer usageMu.Unlock()
	if len(usageRecords) != 1 || !usageRecords[0].Usage.Present || usageRecords[0].Usage.TotalTokens != 10 || usageRecords[0].UsageUnknown {
		t.Fatalf("usage records=%+v", usageRecords)
	}
}

func TestAuditForwardAbnormalEOFAfterCommit(t *testing.T) {
	// The upstream commits one chunk and then dies without [DONE]. The client
	// must receive an SSE error frame (never a synthesized success DONE) and
	// the usage hook must record usage_unknown.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		_, _ = io.WriteString(w, "data: "+auditChunk("partial")+"\n\n")
		w.(http.Flusher).Flush()
	}))
	defer upstream.Close()
	var usageRecords []forward.UsageRecord
	fixture := newAuditFixture(t, []string{upstream.URL}, forward.Hooks{Usage: func(record forward.UsageRecord) {
		usageRecords = append(usageRecords, record)
	}})
	user := fixture.addUser(t)
	fixture.addRoute(t, user.id, upstream.URL, "up/model", "sk-secret-audit", 0, false)

	rec := fixture.call(t, user, "/v1/chat/completions", `{"model":"provider/model","messages":[],"stream":true}`)
	body := rec.Body.String()
	if !strings.Contains(body, "partial") {
		t.Fatalf("committed chunk missing: %q", body)
	}
	if strings.Contains(body, "data: [DONE]") {
		t.Fatalf("abnormal EOF synthesized a success DONE: %q", body)
	}
	if !strings.Contains(body, `"error"`) {
		t.Fatalf("abnormal EOF did not produce an SSE error frame: %q", body)
	}
	if len(usageRecords) != 1 || !usageRecords[0].UsageUnknown || usageRecords[0].Usage.Present {
		t.Fatalf("truncated stream usage record=%+v", usageRecords)
	}
}

func TestAuditForwardPreCommitUpstreamErrorNoRetryByDefault(t *testing.T) {
	// Upstream returns 500 before any body byte. Without silent_retry the
	// client gets a stable 502 and no attempt is repeated.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer upstream.Close()
	var attempts atomic.Int32
	var failovers atomic.Int32
	fixture := newAuditFixture(t, []string{upstream.URL}, forward.Hooks{
		Attempt:  func(forward.AttemptRecord) { attempts.Add(1) },
		Failover: func(forward.FailoverRecord) { failovers.Add(1) },
	})
	user := fixture.addUser(t)
	fixture.addRoute(t, user.id, upstream.URL, "up/model", "sk-secret-audit", 0, false)

	rec := fixture.call(t, user, "/v1/chat/completions", `{"model":"provider/model","messages":[]}`)
	if rec.Code != http.StatusBadGateway || auditErrCode(t, rec) != "upstream" {
		t.Fatalf("status=%d body=%q", rec.Code, rec.Body.String())
	}
	if attempts.Load() != 1 || failovers.Load() != 0 {
		t.Fatalf("attempts=%d failovers=%d", attempts.Load(), failovers.Load())
	}
}

func TestAuditForwardSilentRetryFailoverOrdered(t *testing.T) {
	// Two bindings: the first 500s before commit; with silent_retry on, the
	// ordered selector advances to the second binding which succeeds.
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer bad.Close()
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"ok","object":"chat.completion","created":1,"model":"up/model","choices":[{"index":0,"message":{"role":"assistant","content":"recovered"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	}))
	defer good.Close()

	var failovers atomic.Int32
	fixture := newAuditFixture(t, []string{bad.URL, good.URL}, forward.Hooks{Failover: func(forward.FailoverRecord) { failovers.Add(1) }})
	user := fixture.addUser(t)
	modelID := fixture.addRoute(t, user.id, bad.URL, "up/model", "sk-secret-audit", 0, true)
	fixture.addBinding(t, user.id, modelID, good.URL, "up/model", "sk-secret-audit-2", 1)

	rec := fixture.call(t, user, "/v1/chat/completions", `{"model":"provider/model","messages":[]}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "recovered") {
		t.Fatalf("status=%d body=%q", rec.Code, rec.Body.String())
	}
	if failovers.Load() != 1 {
		t.Fatalf("failovers=%d want 1", failovers.Load())
	}
}

func TestAuditForwardSafetyIdentifierInjected(t *testing.T) {
	received := make(chan map[string]any, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var payload map[string]any
		_ = json.Unmarshal(body, &payload)
		received <- payload
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"ok","object":"chat.completion","created":1,"model":"up/model","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	}))
	defer upstream.Close()
	fixture := newAuditFixture(t, []string{upstream.URL}, forward.Hooks{})
	user := fixture.addUser(t)
	fixture.addRoute(t, user.id, upstream.URL, "up/model", "sk-secret-audit", 0, false)

	rec := fixture.call(t, user, "/v1/chat/completions", `{"model":"provider/model","messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", rec.Code, rec.Body.String())
	}
	payload := <-received
	safety, ok := payload["safety_identifier"].(string)
	if !ok || safety == "" {
		t.Fatalf("safety_identifier missing: %v", payload)
	}
	if want := forward.SafetyIdentifier(user.id); safety != want {
		t.Fatalf("safety_identifier=%q want=%q", safety, want)
	}
	if strings.Contains(safety, strconv.FormatInt(user.id, 10)) {
		t.Fatalf("safety_identifier embeds the raw user id: %q", safety)
	}
}

func TestAuditForwardStreamOptionsInjected(t *testing.T) {
	received := make(chan map[string]any, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var payload map[string]any
		_ = json.Unmarshal(body, &payload)
		received <- payload
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "data: "+auditChunk("x")+"\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer upstream.Close()
	fixture := newAuditFixture(t, []string{upstream.URL}, forward.Hooks{})
	user := fixture.addUser(t)
	fixture.addRoute(t, user.id, upstream.URL, "up/model", "sk-secret-audit", 0, false)

	rec := fixture.call(t, user, "/v1/chat/completions", `{"model":"provider/model","messages":[],"stream":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
	payload := <-received
	opts, ok := payload["stream_options"].(map[string]any)
	if !ok || opts["include_usage"] != true {
		t.Fatalf("stream_options.include_usage not injected: %v", payload)
	}
}

func TestAuditForwardClientCancelPropagatesUpstream(t *testing.T) {
	upstreamCanceled := make(chan struct{}, 1)
	release := make(chan struct{})
	var once sync.Once
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		_, _ = io.WriteString(w, "data: "+auditChunk("partial")+"\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-r.Context().Done():
			once.Do(func() { close(upstreamCanceled) })
		case <-release:
		}
	}))
	defer func() { close(release); upstream.Close() }()
	fixture := newAuditFixture(t, []string{upstream.URL}, forward.Hooks{})
	user := fixture.addUser(t)
	fixture.addRoute(t, user.id, upstream.URL, "up/model", "sk-secret-audit", 0, false)

	req := httptest.NewRequest(http.MethodPost, "https://127.0.0.1/v1/chat/completions",
		strings.NewReader(`{"model":"provider/model","messages":[],"stream":true}`))
	req = req.WithContext(host.WithStation(req.Context(), host.StationUser))
	req.Header.Set("Authorization", "Bearer "+user.key)
	req.Header.Set("Content-Type", "application/json")
	ctx, cancel := context.WithCancel(req.Context())
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		fixture.handler.ServeHTTP(rec, req)
		close(done)
	}()
	// Wait until the upstream got the first committed chunk, then cancel.
	select {
	case <-upstreamCanceled:
		t.Fatal("upstream canceled before client cancellation")
	case <-time.After(500 * time.Millisecond):
	}
	cancel()
	select {
	case <-upstreamCanceled:
	case <-time.After(3 * time.Second):
		t.Fatal("upstream request was not canceled after client disconnect")
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("handler did not return after client cancellation")
	}
}

func TestAuditForwardBackpressureSlowClient(t *testing.T) {
	// A slow reader must not deadlock the stream and the upstream must be
	// able to finish once the client drains.
	const chunks = 400
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		for i := 0; i < chunks; i++ {
			_, _ = io.WriteString(w, "data: "+auditChunk(strconv.Itoa(i))+"\n\n")
			flusher.Flush()
		}
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer upstream.Close()
	fixture := newAuditFixture(t, []string{upstream.URL}, forward.Hooks{})
	user := fixture.addUser(t)
	fixture.addRoute(t, user.id, upstream.URL, "up/model", "sk-secret-audit", 0, false)

	// Wrap the recorder with a one-byte-at-a-time slow sink.
	slow := &slowWriter{inner: httptest.NewRecorder()}
	req := httptest.NewRequest(http.MethodPost, "https://127.0.0.1/v1/chat/completions",
		strings.NewReader(`{"model":"provider/model","messages":[],"stream":true}`))
	req = req.WithContext(host.WithStation(req.Context(), host.StationUser))
	req.Header.Set("Authorization", "Bearer "+user.key)
	req.Header.Set("Content-Type", "application/json")
	fixture.handler.ServeHTTP(slow, req)
	if !strings.HasSuffix(strings.TrimSpace(slow.inner.Body.String()), "data: [DONE]") {
		t.Fatalf("slow-client stream did not finish with DONE (bytes=%d)", slow.inner.Body.Len())
	}
	if !strings.Contains(slow.inner.Body.String(), strconv.Itoa(chunks-1)) {
		t.Fatalf("slow-client stream lost trailing chunks")
	}
}

// slowWriter drains at one byte per write with a small sleep, forcing the
// streaming adapter to pace itself.
type slowWriter struct {
	inner *httptest.ResponseRecorder
	once  sync.Once
}

func (s *slowWriter) Header() http.Header  { return s.inner.Header() }
func (s *slowWriter) WriteHeader(code int) { s.inner.WriteHeader(code) }
func (s *slowWriter) Flush()               { s.once.Do(func() { time.Sleep(50 * time.Millisecond) }) }
func (s *slowWriter) Write(p []byte) (int, error) {
	for i := 0; i < len(p); i++ {
		time.Sleep(200 * time.Microsecond)
		if _, err := s.inner.Write(p[i : i+1]); err != nil {
			return 0, err
		}
	}
	return len(p), nil
}

func TestAuditForwardUnboundModelAndNotFound(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()
	fixture := newAuditFixture(t, []string{upstream.URL}, forward.Hooks{})
	user := fixture.addUser(t)
	fixture.addRoute(t, user.id, upstream.URL, "up/model", "sk-secret-audit", 0, false)

	// Unknown model: stable 404.
	rec := fixture.call(t, user, "/v1/chat/completions", `{"model":"missing/model","messages":[]}`)
	if rec.Code != http.StatusNotFound || auditErrCode(t, rec) != httperr.CodeNotFound {
		t.Fatalf("unknown model status=%d body=%q", rec.Code, rec.Body.String())
	}
	// A draft model without bindings: stable 503 unbound_model.
	draft, err := fixture.store.CreateModel(context.Background(), user.id, "draft", "model", "ordered", false, time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	_ = draft
	rec = fixture.call(t, user, "/v1/chat/completions", `{"model":"draft/model","messages":[]}`)
	if rec.Code != http.StatusServiceUnavailable || auditErrCode(t, rec) != httperr.CodeUnboundModel {
		t.Fatalf("unbound model status=%d body=%q", rec.Code, rec.Body.String())
	}
}

func TestAuditForwardContentTypeStrictness(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()
	fixture := newAuditFixture(t, []string{upstream.URL}, forward.Hooks{})
	user := fixture.addUser(t)
	fixture.addRoute(t, user.id, upstream.URL, "up/model", "sk-secret-audit", 0, false)

	for _, contentType := range []string{
		"text/plain",
		"application/json; charset=iso-8859-1",
		"application/x-www-form-urlencoded",
		"application/json; boundary=x",
	} {
		req := httptest.NewRequest(http.MethodPost, "https://127.0.0.1/v1/chat/completions",
			strings.NewReader(`{"model":"provider/model","messages":[]}`))
		req = req.WithContext(host.WithStation(req.Context(), host.StationUser))
		req.Header.Set("Authorization", "Bearer "+user.key)
		req.Header.Set("Content-Type", contentType)
		rec := httptest.NewRecorder()
		fixture.handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("content-type %q status=%d body=%q", contentType, rec.Code, rec.Body.String())
		}
	}
	// A single UTF-8 JSON content type is accepted.
	req := httptest.NewRequest(http.MethodPost, "https://127.0.0.1/v1/chat/completions",
		strings.NewReader(`{"model":"missing/model","messages":[]}`))
	req = req.WithContext(host.WithStation(req.Context(), host.StationUser))
	req.Header.Set("Authorization", "Bearer "+user.key)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	rec := httptest.NewRecorder()
	fixture.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("utf-8 json status=%d body=%q", rec.Code, rec.Body.String())
	}
}
