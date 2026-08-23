// Package usage_test exercises the hook consumer end to end: a real forward
// pipeline (CallerKey middleware, ownership routing, secure runner, egress
// stack, OpenAI-compatible adapter) fires the attempt/usage hooks into the
// usage service, which persists metadata-only request logs and user
// accumulators in one transaction.
package usage_test

import (
	"bytes"
	"context"
	"fmt"
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
	"github.com/waiting-here/NonbiriAPI/internal/backend"
	"github.com/waiting-here/NonbiriAPI/internal/config"
	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbtest"
	"github.com/waiting-here/NonbiriAPI/internal/egress"
	"github.com/waiting-here/NonbiriAPI/internal/endpoint"
	"github.com/waiting-here/NonbiriAPI/internal/forward"
	"github.com/waiting-here/NonbiriAPI/internal/host"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
	"github.com/waiting-here/NonbiriAPI/internal/usage"
)

// mustLocalBackend wraps the shared stack in the single production Backend so
// adapter tests exercise the same delegation path as production wiring.
func mustLocalBackend(t *testing.T, stack *egress.Stack) *backend.LocalBackend {
	t.Helper()
	local, err := backend.NewLocal(stack)
	if err != nil {
		t.Fatal(err)
	}
	return local
}

type usageResolver struct{}

func (usageResolver) LookupNetIP(ctx context.Context, _ string, _ string) ([]netip.Addr, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
}

type usageFixture struct {
	store   *db.Store
	vault   *secret.Vault
	stack   *egress.Stack
	handler http.Handler
	service *usage.Service
}

func newUsageFixture(t *testing.T, allowed []string, mutateConfig func(*usage.Config), mutateStack func(*egress.StackOptions)) *usageFixture {
	t.Helper()
	master := bytes.Repeat([]byte{0x6b}, secret.MasterKeyBytes)
	vault, err := secret.New(master)
	clear(master)
	if err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(t.TempDir(), "usage.db")
	dbtest.EnsureOwnerOnlyParent(t, dbPath)
	store, err := db.Open(dbPath, vault)
	if err != nil {
		_ = vault.Close()
		t.Fatal(err)
	}
	options := egress.StackOptions{
		AllowedOrigins: allowed,
		Resolver:       usageResolver{},
		RequestTimeout: 3 * time.Second,
		Concurrency:    egress.ConcurrencyLimits{Global: 8, PerEndpoint: 4},
	}
	if mutateStack != nil {
		mutateStack(&options)
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
	adapter, err := openai.NewAdapter(openai.AdapterConfig{Backend: mustLocalBackend(t, stack)})
	if err != nil {
		t.Fatal(err)
	}
	safetyIdentifierKey, err := vault.DeriveSubkey([]byte(forward.SafetyIdentifierSubkeyInfo))
	if err != nil {
		t.Fatal(err)
	}
	safetyIdentifierFactory, err := forward.NewSafetyIdentifierFactory(safetyIdentifierKey)
	clear(safetyIdentifierKey)
	if err != nil {
		t.Fatal(err)
	}
	runner, err := forward.NewSecureRunner(forward.SecureRunnerConfig{
		Repository:        store,
		Secrets:           vault,
		Registry:          registry,
		Adapters:          []forward.Adapter{adapter},
		SafetyIdentifiers: safetyIdentifierFactory,
	})
	if err != nil {
		t.Fatal(err)
	}
	usageConfig := usage.Config{Store: store}
	if mutateConfig != nil {
		mutateConfig(&usageConfig)
	}
	usageService, err := usage.NewService(usageConfig)
	if err != nil {
		t.Fatal(err)
	}
	service, err := forward.NewService(forward.ServiceConfig{
		Repository: store,
		Runner:     runner,
		Hooks: forward.Hooks{
			Attempt: usageService.HandleAttempt,
			Usage:   usageService.HandleUsage,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	exit := forward.NewHandler(forward.HandlerDeps{Service: service, Identity: forward.CallerIdentity})
	wrapped := auth.CallerKeyMiddleware(store, exit)
	fixture := &usageFixture{store: store, vault: vault, stack: stack, handler: wrapped, service: usageService}
	t.Cleanup(func() {
		_ = service.Close()
		_ = safetyIdentifierFactory.Close()
		stack.CloseIdleConnections()
		_ = store.Close()
		_ = vault.Close()
	})
	return fixture
}

type fixtureUser struct {
	id  int64
	key string
}

func (f *usageFixture) addUser(t *testing.T, name string) fixtureUser {
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

func (f *usageFixture) addRoute(t *testing.T, userID int64, baseURL, provider, model, upstreamModel, upstreamSecret string) {
	t.Helper()
	ctx := context.Background()
	canonical, err := f.stack.ValidateBaseURL(baseURL)
	if err != nil {
		t.Fatalf("ValidateBaseURL: %v", err)
	}
	endpointRow, err := f.store.CreateEndpoint(ctx, userID, string(endpoint.ConnectorOpenAICompatible), canonical, "", true, time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	keyRow, err := f.store.CreateEndpointKey(ctx, userID, endpointRow.ID, []byte(upstreamSecret), "head", "tail", "", true, time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.ReplaceFetchedModels(ctx, userID, endpointRow.ID, keyRow.ID, []db.FetchedModel{{
		EndpointKeyID: keyRow.ID, UpstreamModelID: upstreamModel, Provider: "upstream", Status: "ok",
	}}, time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	modelRow, err := f.store.CreateModel(ctx, userID, provider, model, "ordered", false, time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.CreateBinding(ctx, userID, modelRow.ID, keyRow.ID, upstreamModel, 0, time.Now().Unix()); err != nil {
		t.Fatal(err)
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

func completionBody(content string, withUsage bool) string {
	usageField := ""
	if withUsage {
		usageField = `,"usage":{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5}`
	}
	return `{"id":"chatcmpl-usage","object":"chat.completion","created":1,"model":"upstream/model",` +
		`"choices":[{"index":0,"message":{"role":"assistant","content":` + strconv.Quote(content) + `},"finish_reason":"stop"}]` +
		usageField + `}`
}

func streamChunk(content string) string {
	return `{"id":"chatcmpl-usage","object":"chat.completion.chunk","created":1,"model":"upstream/model","choices":[{"index":0,"delta":{"content":` + strconv.Quote(content) + `}}]}`
}

func streamUsageChunk() string {
	return `{"id":"chatcmpl-usage","object":"chat.completion.chunk","created":1,"model":"upstream/model","choices":[],"usage":{"prompt_tokens":4,"completion_tokens":6,"total_tokens":10}}`
}

func serveForwardSSE(writer http.ResponseWriter, frames ...string) {
	writer.Header().Set("Content-Type", "text/event-stream")
	writer.WriteHeader(http.StatusOK)
	writer.(http.Flusher).Flush()
	for _, frame := range frames {
		_, _ = io.WriteString(writer, frame)
		writer.(http.Flusher).Flush()
	}
}

// logRows returns every persisted request-log row concatenated for leak
// scans.
func logRows(t *testing.T, store *db.Store) []string {
	t.Helper()
	rows, err := store.DB().Query(`SELECT model || '|' || COALESCE(upstream_model_id,'') || '|' || COALESCE(error_code,'') || '|' || COALESCE(error_diag,'') FROM request_logs`)
	if err != nil {
		t.Fatalf("query logs: %v", err)
	}
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatalf("scan log: %v", err)
		}
		lines = append(lines, line)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate logs: %v", err)
	}
	return lines
}

func assertLogsLeakFree(t *testing.T, store *db.Store, markers ...string) {
	t.Helper()
	for _, line := range logRows(t, store) {
		for _, marker := range markers {
			if strings.Contains(line, marker) {
				t.Fatalf("request_logs leaked %q in %q", marker, line)
			}
		}
	}
}

func assertUserTotals(t *testing.T, store *db.Store, userID int64, requests, prompt, completion, unknown int64) {
	t.Helper()
	totals, err := store.GetUserUsage(context.Background(), userID)
	if err != nil {
		t.Fatalf("GetUserUsage: %v", err)
	}
	if totals.TotalRequests != requests || totals.TotalPromptTokens != prompt ||
		totals.TotalCompletionTokens != completion || totals.TotalUnknownUsageRequests != unknown {
		t.Fatalf("user %d totals = %+v, want requests=%d prompt=%d completion=%d unknown=%d",
			userID, totals, requests, prompt, completion, unknown)
	}
}

func TestUsageNonStreamValidUsage(t *testing.T) {
	const upstreamSecret = "sk-usage-valid-never-leak"
	const promptMarker = "PRIVATE-VALID-MARKER"
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, completionBody("safe", true))
	}))
	defer upstream.Close()
	fixture := newUsageFixture(t, []string{upstream.URL}, nil, nil)
	user := fixture.addUser(t, "valid")
	fixture.addRoute(t, user.id, upstream.URL, "p", "m", "upstream/model", upstreamSecret)

	body := `{"model":"p/m","messages":[{"role":"user","content":"` + promptMarker + `"}]}`
	response := performCaller(fixture.handler, callerRequest(http.MethodPost, "/v1/chat/completions", user.key, body))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	assertUserTotals(t, fixture.store, user.id, 1, 2, 3, 0)

	logs, _, err := fixture.store.QueryUserRequestLogs(context.Background(), user.id, db.UserLogQuery{PageSize: 10})
	if err != nil || len(logs) != 1 {
		t.Fatalf("logs = %+v err=%v", logs, err)
	}
	log := logs[0]
	if log.Model != "p/m" || log.EndpointKeyID == 0 || log.UpstreamModelID != "upstream/model" ||
		log.EndpointBaseURL != upstream.URL ||
		log.StatusCode != 200 || log.PromptTokens != 2 || log.CompletionTokens != 3 || log.TotalTokens != 5 ||
		log.UsageUnknown || log.ErrorCode != "" || log.StartedAt.IsZero() || log.CompletedAt.Before(log.StartedAt) {
		t.Fatalf("log row = %+v", log)
	}
	// The dispatch-time canonical base-URL snapshot is owner-visible metadata
	// per the frozen log contract; only secrets and request content must stay
	// out of the persisted rows.
	assertLogsLeakFree(t, fixture.store, upstreamSecret, promptMarker)
}

func TestUsageNonStreamWithoutUsageCountsUnknown(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, completionBody("no usage", false))
	}))
	defer upstream.Close()
	fixture := newUsageFixture(t, []string{upstream.URL}, nil, nil)
	user := fixture.addUser(t, "nousage")
	fixture.addRoute(t, user.id, upstream.URL, "p", "m", "upstream/model", "sk-nousage")

	response := performCaller(fixture.handler, callerRequest(http.MethodPost, "/v1/chat/completions", user.key,
		`{"model":"p/m","messages":[]}`))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	assertUserTotals(t, fixture.store, user.id, 1, 0, 0, 1)
	logs, _, _ := fixture.store.QueryUserRequestLogs(context.Background(), user.id, db.UserLogQuery{PageSize: 10})
	if len(logs) != 1 || !logs[0].UsageUnknown || logs[0].TotalTokens != 0 || logs[0].StatusCode != 200 {
		t.Fatalf("logs = %+v", logs)
	}
}

func TestUsagePrecommitFailureNotPersisted(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		http.Error(writer, "boom with secret detail", http.StatusServiceUnavailable)
	}))
	defer upstream.Close()
	fixture := newUsageFixture(t, []string{upstream.URL}, nil, nil)
	user := fixture.addUser(t, "precommit")
	fixture.addRoute(t, user.id, upstream.URL, "p", "m", "upstream/model", "sk-precommit")

	response := performCaller(fixture.handler, callerRequest(http.MethodPost, "/v1/chat/completions", user.key,
		`{"model":"p/m","messages":[]}`))
	if response.Code != http.StatusBadGateway {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	assertUserTotals(t, fixture.store, user.id, 0, 0, 0, 0)
	if rows := logRows(t, fixture.store); len(rows) != 0 {
		t.Fatalf("pre-commit failure persisted log rows: %v", rows)
	}
}

func TestUsageStreamDoneUsage(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		serveForwardSSE(writer,
			"data: "+streamChunk("hello")+"\n\n",
			"data: "+streamUsageChunk()+"\n\n",
			"data: [DONE]\n\n")
	}))
	defer upstream.Close()
	fixture := newUsageFixture(t, []string{upstream.URL}, nil, nil)
	user := fixture.addUser(t, "streamdone")
	fixture.addRoute(t, user.id, upstream.URL, "p", "m", "upstream/model", "sk-stream-done")

	response := performCaller(fixture.handler, callerRequest(http.MethodPost, "/v1/chat/completions", user.key,
		`{"model":"p/m","messages":[],"stream":true}`))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "data: [DONE]") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	assertUserTotals(t, fixture.store, user.id, 1, 4, 6, 0)
	logs, _, _ := fixture.store.QueryUserRequestLogs(context.Background(), user.id, db.UserLogQuery{PageSize: 10})
	if len(logs) != 1 || logs[0].UsageUnknown || logs[0].TotalTokens != 10 || logs[0].StatusCode != 200 {
		t.Fatalf("logs = %+v", logs)
	}
}

func TestUsageStreamAbnormalEOFAfterCommit(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		serveForwardSSE(writer, "data: "+streamChunk("partial")+"\n\n")
	}))
	defer upstream.Close()
	fixture := newUsageFixture(t, []string{upstream.URL}, nil, nil)
	user := fixture.addUser(t, "streamtrunc")
	fixture.addRoute(t, user.id, upstream.URL, "p", "m", "upstream/model", "sk-stream-trunc")

	response := performCaller(fixture.handler, callerRequest(http.MethodPost, "/v1/chat/completions", user.key,
		`{"model":"p/m","messages":[],"stream":true}`))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	assertUserTotals(t, fixture.store, user.id, 1, 0, 0, 1)
	logs, _, _ := fixture.store.QueryUserRequestLogs(context.Background(), user.id, db.UserLogQuery{PageSize: 10})
	if len(logs) != 1 || !logs[0].UsageUnknown || logs[0].TotalTokens != 0 ||
		logs[0].StatusCode != 200 || logs[0].ErrorCode != "upstream" || logs[0].EndpointBaseURL != upstream.URL {
		t.Fatalf("logs = %+v", logs)
	}
	assertLogsLeakFree(t, fixture.store, "sk-stream-trunc")
}

func TestUsageStreamCanceledAfterCommit(t *testing.T) {
	starts := make(chan struct{}, 1)
	release := make(chan struct{})
	defer close(release)
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		serveForwardSSE(writer, "data: "+streamChunk("partial")+"\n\n")
		starts <- struct{}{}
		select {
		case <-request.Context().Done():
		case <-release:
		}
	}))
	defer upstream.Close()
	fixture := newUsageFixture(t, []string{upstream.URL}, nil, func(options *egress.StackOptions) {
		options.RequestTimeout = 30 * time.Second
	})
	user := fixture.addUser(t, "streamcancel")
	fixture.addRoute(t, user.id, upstream.URL, "p", "m", "upstream/model", "sk-stream-cancel")

	request := callerRequest(http.MethodPost, "/v1/chat/completions", user.key, `{"model":"p/m","messages":[],"stream":true}`)
	ctx, cancel := context.WithCancel(request.Context())
	request = request.WithContext(ctx)
	recorder := newLockedRecorder()
	done := make(chan struct{})
	go func() {
		fixture.handler.ServeHTTP(recorder, request)
		close(done)
	}()
	select {
	case <-starts:
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal("stream did not start")
	}
	// Wait until the first chunk is actually written to the client side, so
	// the attempt is provably committed before the cancellation races it.
	deadline := time.Now().Add(2 * time.Second)
	for !strings.Contains(recorder.String(), "partial") && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !strings.Contains(recorder.String(), "partial") {
		cancel()
		t.Fatal("committed chunk never reached the client side")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not return after cancellation")
	}
	if strings.Contains(recorder.String(), "[DONE]") {
		t.Fatalf("canceled stream wrote a terminal frame: %q", recorder.String())
	}
	assertUserTotals(t, fixture.store, user.id, 1, 0, 0, 1)
	logs, _, _ := fixture.store.QueryUserRequestLogs(context.Background(), user.id, db.UserLogQuery{PageSize: 10})
	if len(logs) != 1 || !logs[0].UsageUnknown || logs[0].TotalTokens != 0 || logs[0].StatusCode != 200 {
		t.Fatalf("logs = %+v", logs)
	}
}

// lockedRecorder is a goroutine-safe response recorder: the adapter writes
// stream frames from its own goroutine while the test polls the body.
type lockedRecorder struct {
	mu     sync.Mutex
	header http.Header
	code   int
	body   bytes.Buffer
}

func newLockedRecorder() *lockedRecorder {
	return &lockedRecorder{header: make(http.Header)}
}

func (r *lockedRecorder) Header() http.Header {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.header
}

func (r *lockedRecorder) Write(body []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.code == 0 {
		r.code = http.StatusOK
	}
	return r.body.Write(body)
}

func (r *lockedRecorder) WriteHeader(code int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.code == 0 {
		r.code = code
	}
}

func (r *lockedRecorder) Flush() {}

func (r *lockedRecorder) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.body.String()
}

func TestUsageDuplicateHookDeliveryIsAtMostOnce(t *testing.T) {
	store := openRawUsageStore(t)
	defer store.Close()
	userID := seedRawUser(t, store)

	service, err := usage.NewService(usage.Config{Store: store})
	if err != nil {
		t.Fatal(err)
	}
	started := time.Unix(1700000000, 0).UTC()
	attempt := forward.AttemptRecord{
		AttemptID: "attempt-duplicate", UserID: userID, FullName: "p/m",
		EndpointKeyID: 7, UpstreamModelID: "upstream/model",
		StartedAt: started, Duration: 10 * time.Millisecond, ClientStatus: 200,
	}
	usageRecord := forward.UsageRecord{
		AttemptID: "attempt-duplicate", UserID: userID, FullName: "p/m", EndpointKeyID: 7,
		UpstreamModelID: "upstream/model",
		Usage:           openai.Usage{UncachedInputTokens: 2, OutputTokens: 3, Present: true},
	}
	service.HandleAttempt(attempt)
	// The same committed usage hook delivered twice must not double-accumulate.
	service.HandleUsage(usageRecord)
	service.HandleUsage(usageRecord)

	assertUserTotals(t, store, userID, 1, 2, 3, 0)
	logs, _, _ := store.QueryUserRequestLogs(context.Background(), userID, db.UserLogQuery{PageSize: 10})
	if len(logs) != 1 {
		t.Fatalf("duplicate hook delivery persisted %d rows", len(logs))
	}
}

func TestUsageOrphanUsageRecordFallsBackToZeroMetadata(t *testing.T) {
	store := openRawUsageStore(t)
	defer store.Close()
	userID := seedRawUser(t, store)

	service, err := usage.NewService(usage.Config{Store: store})
	if err != nil {
		t.Fatal(err)
	}
	service.HandleUsage(forward.UsageRecord{
		AttemptID: "attempt-orphan", UserID: userID, FullName: "p/m",
		Usage: openai.Usage{Present: false},
	})
	assertUserTotals(t, store, userID, 1, 0, 0, 1)
	logs, _, _ := store.QueryUserRequestLogs(context.Background(), userID, db.UserLogQuery{PageSize: 10})
	if len(logs) != 1 || logs[0].StatusCode != 0 || logs[0].DurationMs != 0 ||
		!logs[0].UsageUnknown || logs[0].Model != "" {
		t.Fatalf("orphan fallback log = %+v", logs)
	}
}

func TestUsageAttemptCacheEvictionBoundedAndOrphanFallback(t *testing.T) {
	store := openRawUsageStore(t)
	defer store.Close()
	userID := seedRawUser(t, store)

	now := time.Unix(1700000000, 0).UTC()
	var mu sync.Mutex
	service, err := usage.NewService(usage.Config{
		Store:              store,
		Now:                func() time.Time { mu.Lock(); defer mu.Unlock(); return now },
		MaxPendingAttempts: 2,
		PendingTTL:         time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		service.HandleAttempt(forward.AttemptRecord{
			AttemptID: fmt.Sprintf("attempt-evict-%d", i), UserID: userID,
			StartedAt: now, Duration: time.Millisecond, ClientStatus: 200,
		})
	}
	// The third attempt evicted the first; its usage hook falls back to
	// zeroed metadata but is still counted (commit boundary).
	service.HandleUsage(forward.UsageRecord{
		AttemptID: "attempt-evict-0", UserID: userID, Usage: openai.Usage{Present: false},
	})
	assertUserTotals(t, store, userID, 1, 0, 0, 1)

	// TTL expiry: a usage hook arriving after the TTL cannot correlate.
	mu.Lock()
	now = now.Add(2 * time.Hour)
	mu.Unlock()
	service.HandleAttempt(forward.AttemptRecord{
		AttemptID: "attempt-stale", UserID: userID,
		StartedAt: now, Duration: time.Millisecond, ClientStatus: 200,
	})
	service.HandleUsage(forward.UsageRecord{
		AttemptID: "attempt-evict-1", UserID: userID, Usage: openai.Usage{Present: false},
	})
	assertUserTotals(t, store, userID, 2, 0, 0, 2)
}

func TestUsageConcurrentRequestsNoLostAccounting(t *testing.T) {
	const workers = 16
	var hits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, completionBody("concurrent", true))
	}))
	defer upstream.Close()
	fixture := newUsageFixture(t, []string{upstream.URL}, nil, func(options *egress.StackOptions) {
		options.Concurrency = egress.ConcurrencyLimits{Global: workers, PerEndpoint: workers}
	})
	user := fixture.addUser(t, "concurrent")
	fixture.addRoute(t, user.id, upstream.URL, "p", "m", "upstream/model", "sk-concurrent")

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			response := performCaller(fixture.handler, callerRequest(http.MethodPost, "/v1/chat/completions", user.key,
				`{"model":"p/m","messages":[]}`))
			if response.Code != http.StatusOK {
				t.Errorf("status=%d", response.Code)
			}
		}()
	}
	wg.Wait()
	if got := hits.Load(); got != workers {
		t.Fatalf("upstream hits = %d, want %d", got, workers)
	}
	assertUserTotals(t, fixture.store, user.id, workers, 2*workers, 3*workers, 0)
	logs, _, _ := fixture.store.QueryUserRequestLogs(context.Background(), user.id, db.UserLogQuery{PageSize: 100})
	if len(logs) != workers {
		t.Fatalf("log rows = %d, want %d", len(logs), workers)
	}
	assertLogsLeakFree(t, fixture.store, "sk-concurrent")
}

func TestUsageFailoverCommittedAttemptCountedOnce(t *testing.T) {
	const firstSecret = "sk-failover-first"
	const secondSecret = "sk-failover-second"
	first := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		http.Error(writer, "precommit failure", http.StatusServiceUnavailable)
	}))
	defer first.Close()
	second := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, completionBody("failover ok", true))
	}))
	defer second.Close()
	fixture := newUsageFixture(t, []string{first.URL, second.URL}, nil, nil)
	user := fixture.addUser(t, "failover")
	ctx := context.Background()
	canonicalFirst, err := fixture.stack.ValidateBaseURL(first.URL)
	if err != nil {
		t.Fatal(err)
	}
	canonicalSecond, err := fixture.stack.ValidateBaseURL(second.URL)
	if err != nil {
		t.Fatal(err)
	}
	endpointFirst, err := fixture.store.CreateEndpoint(ctx, user.id, string(endpoint.ConnectorOpenAICompatible), canonicalFirst, "", true, time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	endpointSecond, err := fixture.store.CreateEndpoint(ctx, user.id, string(endpoint.ConnectorOpenAICompatible), canonicalSecond, "", true, time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	keyFirst, err := fixture.store.CreateEndpointKey(ctx, user.id, endpointFirst.ID, []byte(firstSecret), "", "", "", true, time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	keySecond, err := fixture.store.CreateEndpointKey(ctx, user.id, endpointSecond.ID, []byte(secondSecret), "", "", "", true, time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	for _, keyRow := range []struct {
		key        db.EndpointKey
		endpointID int64
	}{{keyFirst, endpointFirst.ID}, {keySecond, endpointSecond.ID}} {
		if err := fixture.store.ReplaceFetchedModels(ctx, user.id, keyRow.endpointID, keyRow.key.ID, []db.FetchedModel{{
			EndpointKeyID: keyRow.key.ID, UpstreamModelID: "upstream/model", Provider: "upstream", Status: "ok",
		}}, time.Now().Unix()); err != nil {
			t.Fatal(err)
		}
	}
	modelRow, err := fixture.store.CreateModel(ctx, user.id, "p", "m", "ordered", true, time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.CreateBinding(ctx, user.id, modelRow.ID, keyFirst.ID, "upstream/model", 0, time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.CreateBinding(ctx, user.id, modelRow.ID, keySecond.ID, "upstream/model", 1, time.Now().Unix()); err != nil {
		t.Fatal(err)
	}

	response := performCaller(fixture.handler, callerRequest(http.MethodPost, "/v1/chat/completions", user.key,
		`{"model":"p/m","messages":[]}`))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	// Exactly one accounted request, with the second (committed) attempt's
	// metadata: the pre-commit failure is not counted.
	assertUserTotals(t, fixture.store, user.id, 1, 2, 3, 0)
	logs, _, _ := fixture.store.QueryUserRequestLogs(context.Background(), user.id, db.UserLogQuery{PageSize: 10})
	if len(logs) != 1 || logs[0].ErrorCode != "" || logs[0].UsageUnknown || logs[0].TotalTokens != 5 {
		t.Fatalf("logs = %+v", logs)
	}
	var keyID int64
	var committedBaseURL string
	if err := fixture.store.DB().QueryRow(`SELECT endpoint_key_id, endpoint_base_url FROM request_logs LIMIT 1`).Scan(&keyID, &committedBaseURL); err != nil {
		t.Fatalf("read key id: %v", err)
	}
	// The snapshot must reference the attempt that actually committed (the
	// second binding), not the failed pre-commit candidate.
	if committedBaseURL != second.URL {
		t.Fatalf("endpoint_base_url = %q, want committed %q", committedBaseURL, second.URL)
	}
	var secondKey int64
	if err := fixture.store.DB().QueryRow(`SELECT ek.id FROM endpoint_keys ek JOIN endpoints e ON e.id=ek.endpoint_id WHERE e.base_url=?`, second.URL).Scan(&secondKey); err != nil {
		t.Fatalf("resolve second key: %v", err)
	}
	if keyID != secondKey {
		t.Fatalf("log row references the wrong attempt's key: %d want %d", keyID, secondKey)
	}
	assertLogsLeakFree(t, fixture.store, firstSecret, secondSecret)
}

// --- raw store helpers (no egress stack needed) ---

func openRawUsageStore(t *testing.T) *db.Store {
	t.Helper()
	master := bytes.Repeat([]byte{0x7c}, secret.MasterKeyBytes)
	vault, err := secret.New(master)
	clear(master)
	if err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(t.TempDir(), "raw-usage.db")
	dbtest.EnsureOwnerOnlyParent(t, dbPath)
	store, err := db.Open(dbPath, vault)
	if err != nil {
		_ = vault.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = store.Close()
		_ = vault.Close()
	})
	return store
}

func seedRawUser(t *testing.T, store *db.Store) int64 {
	t.Helper()
	now := time.Now().Unix()
	res, err := store.DB().Exec(`INSERT INTO users (discord_id, username, created_at, updated_at) VALUES (?, 'raw', ?, ?)`,
		"discord-raw-"+strconv.FormatInt(now, 10), now, now)
	if err != nil {
		t.Fatal(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestUsageRecordsDailyActivity(t *testing.T) {
	store := openRawUsageStore(t)
	// An explicit offset must be configured before any day key is generated.
	if err := store.SetSiteTimezoneOffsetMinutes(330); err != nil {
		t.Fatal(err)
	}
	userID := seedRawUser(t, store)

	service, err := usage.NewService(usage.Config{Store: store})
	if err != nil {
		t.Fatal(err)
	}
	started := time.Unix(1700000000, 0).UTC()
	attempt := forward.AttemptRecord{
		AttemptID: "activity-attempt", UserID: userID, FullName: "p/m",
		StartedAt: started, Duration: 10 * time.Millisecond, ClientStatus: 200,
		Success: true,
	}
	usageRecord := forward.UsageRecord{
		AttemptID: "activity-attempt", UserID: userID, FullName: "p/m",
		Usage: openai.Usage{UncachedInputTokens: 2, CacheReadInputTokens: 4, OutputTokens: 3, Present: true},
	}
	service.HandleAttempt(attempt)
	// A duplicate delivery of the same committed hook must not double-count
	// the daily activity rollup either.
	service.HandleUsage(usageRecord)
	service.HandleUsage(usageRecord)

	var requests, uncached, cacheRead, output int64
	if err := store.DB().QueryRow(`SELECT api_requests, uncached_input_tokens,
		cache_read_input_tokens, output_tokens FROM user_activity_daily WHERE user_id=?`, userID).
		Scan(&requests, &uncached, &cacheRead, &output); err != nil {
		t.Fatalf("read user activity: %v", err)
	}
	if requests != 1 || uncached != 2 || cacheRead != 4 || output != 3 {
		t.Fatalf("user activity = (%d,%d,%d,%d), want (1,2,4,3)", requests, uncached, cacheRead, output)
	}
	var siteRequests, distinct int64
	if err := store.DB().QueryRow(`SELECT api_requests, distinct_product_users FROM site_activity_daily`).
		Scan(&siteRequests, &distinct); err != nil {
		t.Fatalf("read site activity: %v", err)
	}
	if siteRequests != 1 || distinct != 1 {
		t.Fatalf("site activity = (%d,%d), want (1,1)", siteRequests, distinct)
	}
}

// A committed-but-failed request (protocol failure after commit, e.g. an
// upstream error mid-stream) is accounted as a request but is never successful
// API activity per the frozen contract; neither is an orphan usage record with
// no attempt metadata (unknown success).
func TestUsageFailedRequestNotActivity(t *testing.T) {
	store := openRawUsageStore(t)
	if err := store.SetSiteTimezoneOffsetMinutes(330); err != nil {
		t.Fatal(err)
	}
	userID := seedRawUser(t, store)

	service, err := usage.NewService(usage.Config{Store: store})
	if err != nil {
		t.Fatal(err)
	}
	started := time.Unix(1700000000, 0).UTC()
	service.HandleAttempt(forward.AttemptRecord{
		AttemptID: "failed-attempt", UserID: userID, FullName: "p/m",
		StartedAt: started, Duration: 10 * time.Millisecond, ClientStatus: 502,
		StableErrorCode: "upstream", Success: false,
	})
	service.HandleUsage(forward.UsageRecord{
		AttemptID: "failed-attempt", UserID: userID, FullName: "p/m",
		Usage: openai.Usage{UncachedInputTokens: 5, OutputTokens: 5, Present: true},
	})
	// Orphan usage record: success unknown, must not fabricate activity.
	service.HandleUsage(forward.UsageRecord{
		AttemptID: "orphan-attempt", UserID: userID, FullName: "p/m",
		Usage: openai.Usage{UncachedInputTokens: 7, OutputTokens: 7, Present: true},
	})

	assertUserTotals(t, store, userID, 2, 12, 12, 0)
	rows, err := store.DB().Query(`SELECT COUNT(*) FROM user_activity_daily`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var n int
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Fatalf("failed/orphan requests recorded daily activity rows: %d", n)
		}
	}
}

func TestUsageActivitySkippedWithoutTimezone(t *testing.T) {
	store := openRawUsageStore(t)
	userID := seedRawUser(t, store)

	service, err := usage.NewService(usage.Config{Store: store})
	if err != nil {
		t.Fatal(err)
	}
	service.HandleUsage(forward.UsageRecord{
		AttemptID: "no-tz-attempt", UserID: userID,
		Usage: openai.Usage{UncachedInputTokens: 1, OutputTokens: 1, Present: true},
	})
	assertUserTotals(t, store, userID, 1, 1, 1, 0)
	rows, err := store.DB().Query(`SELECT COUNT(*) FROM user_activity_daily`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var n int
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Fatalf("activity rows written without a configured timezone: %d", n)
		}
	}
}
