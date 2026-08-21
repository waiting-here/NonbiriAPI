package endpoint_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/config"
	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbtest"
	"github.com/waiting-here/NonbiriAPI/internal/egress"
	"github.com/waiting-here/NonbiriAPI/internal/endpoint"
	"github.com/waiting-here/NonbiriAPI/internal/fetch"
	"github.com/waiting-here/NonbiriAPI/internal/forward"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

type countingFetchHook struct {
	inner endpoint.FetchHook
	calls atomic.Int32
}

func (h *countingFetchHook) FetchModels(ctx context.Context, userID, endpointID, keyID int64) error {
	h.calls.Add(1)
	return h.inner.FetchModels(ctx, userID, endpointID, keyID)
}

type publicOriginResolver struct{}

func (publicOriginResolver) LookupNetIP(ctx context.Context, _, _ string) ([]netip.Addr, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
}

// portRoutingDialer lets tests use public-looking origins while routing vetted
// public IP dials to isolated local listeners. The egress resolver and origin
// checks still run before this dial boundary.
type portRoutingDialer struct {
	byPort map[string]string
}

func (d portRoutingDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	target := d.byPort[port]
	if target == "" {
		return nil, fmt.Errorf("unexpected test dial port")
	}
	var dialer net.Dialer
	return dialer.DialContext(ctx, network, target)
}

func serverPortAndAddress(t *testing.T, server *httptest.Server) (string, string) {
	t.Helper()
	parsed, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	_, port, err := net.SplitHostPort(parsed.Host)
	if err != nil {
		t.Fatal(err)
	}
	return port, parsed.Host
}

func waitForCondition(t *testing.T, condition func() bool, description string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", description)
}

func TestEndpointOriginChangeCannotExfiltrateStoredCredential(t *testing.T) {
	const upstreamSecret = "sk-origin-attack-regression-12345678"
	var oldModelHits atomic.Int32
	var oldChatHits atomic.Int32
	var attackerHits atomic.Int32
	var authMu sync.Mutex
	var oldAuthorizations []string
	var attackerAuthorizations []string

	oldServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authMu.Lock()
		oldAuthorizations = append(oldAuthorizations, r.Header.Get("Authorization"))
		authMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/models":
			oldModelHits.Add(1)
			_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"upstream-model","owned_by":"upstream"}]}`))
		case "/v1/chat/completions":
			oldChatHits.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":      "chatcmpl-origin-boundary",
				"object":  "chat.completion",
				"created": int64(1),
				"model":   "upstream-model",
				"choices": []any{map[string]any{
					"index": 0,
					"message": map[string]any{
						"role": "assistant", "content": "ok",
					},
					"finish_reason": "stop",
				}},
				"usage": map[string]any{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(oldServer.Close)
	attackerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attackerHits.Add(1)
		authMu.Lock()
		attackerAuthorizations = append(attackerAuthorizations, r.Header.Get("Authorization"))
		authMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[]}`))
	}))
	t.Cleanup(attackerServer.Close)

	oldPort, oldAddress := serverPortAndAddress(t, oldServer)
	attackerPort, attackerAddress := serverPortAndAddress(t, attackerServer)
	oldBaseURL := "http://old-upstream.example:" + oldPort + "/v1"
	attackerBaseURL := "http://attacker.example:" + attackerPort + "/v1"

	master := bytes.Repeat([]byte{0x63}, secret.MasterKeyBytes)
	vault, err := secret.New(master)
	clear(master)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = vault.Close() })
	dbPath := filepath.Join(t.TempDir(), "origin-boundary.db")
	dbtest.EnsureOwnerOnlyParent(t, dbPath)
	store, err := db.Open(dbPath, vault)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	stack, err := egress.NewStack(egress.StackOptions{
		Resolver: publicOriginResolver{},
		Dialer: portRoutingDialer{byPort: map[string]string{
			oldPort:      oldAddress,
			attackerPort: attackerAddress,
		}},
		RequestTimeout: 3 * time.Second,
		Concurrency:    egress.ConcurrencyLimits{Global: 8, PerEndpoint: 4},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stack.CloseIdleConnections)
	if err := stack.AddSelfOrigins(context.Background(), &config.Config{
		SiteBaseURL: "https://gateway.example",
		UserHost:    "gateway.example",
		AdminHost:   "admin.gateway.example",
		ListenAddr:  "127.0.0.1:1",
	}); err != nil {
		t.Fatal(err)
	}

	registry := endpoint.NewRegistry()
	fetcher, err := fetch.NewFetcher(fetch.FetcherConfig{
		Store: store, Stack: stack, Secrets: vault, Registry: registry,
		Workers: 1, QueueSize: 4, Now: func() int64 { return 10 },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fetcher.Close() })

	user, err := store.CreateUser("discord-origin-boundary", "tester", "")
	if err != nil {
		t.Fatal(err)
	}
	fetchHook := &countingFetchHook{inner: fetcher}
	endpointService := endpoint.NewService(endpoint.ServiceDeps{
		Repo: store, URLs: stack, Connectors: registry, Hook: fetchHook,
		Now: func() int64 { return 10 },
	})
	ep, err := endpointService.CreateEndpoint(context.Background(), user.ID,
		string(endpoint.ConnectorOpenAICompatible), oldBaseURL, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	key, err := endpointService.CreateEndpointKey(context.Background(), user.ID, ep.ID,
		[]byte(upstreamSecret), nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	waitForCondition(t, func() bool {
		var count int
		err := store.DB().QueryRow(`SELECT COUNT(*) FROM fetched_models WHERE endpoint_key_id=?`, key.ID).Scan(&count)
		return err == nil && count == 1 && oldModelHits.Load() == 1
	}, "initial model fetch")

	handler := endpoint.NewHandler(endpoint.HandlerDeps{
		Service: endpointService,
		Identity: func(*http.Request) (int64, error) {
			return user.ID, nil
		},
	})
	payload, err := json.Marshal(map[string]string{"base_url": attackerBaseURL})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPatch, "/api/endpoints/"+strconv.FormatInt(ep.ID, 10), bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusConflict {
		t.Fatalf("origin PATCH status=%d body=%s", response.Code, response.Body.String())
	}
	var envelope httperr.Envelope
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Error.Code != httperr.CodeConflict {
		t.Fatalf("origin PATCH code=%q", envelope.Error.Code)
	}
	if calls := fetchHook.calls.Load(); calls != 1 {
		t.Fatalf("rejected origin PATCH changed fetch submissions to %d", calls)
	}
	for _, forbidden := range []string{upstreamSecret, oldBaseURL, attackerBaseURL} {
		if strings.Contains(response.Body.String(), forbidden) {
			t.Fatalf("origin conflict response exposed target material: %s", response.Body.String())
		}
	}
	storedEndpoint, err := store.GetEndpoint(context.Background(), user.ID, ep.ID)
	if err != nil {
		t.Fatal(err)
	}
	if storedEndpoint.BaseURL != oldBaseURL {
		t.Fatalf("stored endpoint moved to %q", storedEndpoint.BaseURL)
	}

	model, err := store.CreateModel(context.Background(), user.ID, "provider", "model", "ordered", false, 11)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateBinding(context.Background(), user.ID, model.ID, key.ID, "upstream-model", 0, 11); err != nil {
		t.Fatal(err)
	}
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
	forwardService, err := forward.NewService(forward.ServiceConfig{
		Repository: store,
		Runner:     runner,
		Backoff:    forward.BackoffConfig{Base: -1, Max: -1},
	})
	if err != nil {
		t.Fatal(err)
	}
	chatRequest, err := openai.DecodeChatRequest(strings.NewReader(
		`{"model":"provider/model","messages":[{"role":"user","content":"hello"}]}`), openai.MaxRequestBodyBytes)
	if err != nil {
		t.Fatal(err)
	}
	defer chatRequest.Clear()
	forwardResponse := httptest.NewRecorder()
	result, err := forwardService.Forward(context.Background(), forwardResponse, user.ID, chatRequest)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Success || oldChatHits.Load() != 1 {
		t.Fatalf("forward result=%+v old_chat_hits=%d body=%s", result, oldChatHits.Load(), forwardResponse.Body.String())
	}

	if got := attackerHits.Load(); got != 0 {
		t.Fatalf("attacker received %d requests", got)
	}
	authMu.Lock()
	defer authMu.Unlock()
	if len(attackerAuthorizations) != 0 {
		t.Fatalf("attacker received Authorization headers: %d", len(attackerAuthorizations))
	}
	if len(oldAuthorizations) != 2 {
		t.Fatalf("old origin Authorization count=%d, want 2", len(oldAuthorizations))
	}
	for i, authorization := range oldAuthorizations {
		if authorization != "Bearer "+upstreamSecret {
			t.Fatalf("old origin Authorization[%d]=%q", i, authorization)
		}
	}
}
