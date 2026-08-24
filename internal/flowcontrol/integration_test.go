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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/auth"
	"github.com/waiting-here/NonbiriAPI/internal/backend"
	"github.com/waiting-here/NonbiriAPI/internal/charityrouting"
	"github.com/waiting-here/NonbiriAPI/internal/config"
	"github.com/waiting-here/NonbiriAPI/internal/connector/openai"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbtest"
	"github.com/waiting-here/NonbiriAPI/internal/egress"
	"github.com/waiting-here/NonbiriAPI/internal/endpoint"
	"github.com/waiting-here/NonbiriAPI/internal/flowcontrol"
	"github.com/waiting-here/NonbiriAPI/internal/forward"
	"github.com/waiting-here/NonbiriAPI/internal/host"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/ratelimit"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
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
	store               *db.Store
	codec               *countingCodec
	stack               *egress.Stack
	controller          *flowcontrol.Controller
	handler             http.Handler
	upstream            *httptest.Server
	hits                *atomic.Int32
	blockUpstream       *atomic.Bool
	upstreamEntered     chan struct{}
	upstreamRelease     chan struct{}
	upstreamReleaseOnce *sync.Once
	charityCalls        *atomic.Int32
	deniedCalls         *atomic.Int32
	upstreamFailures    *atomic.Int32
}

func (f *integrationFixture) releaseBlockedUpstream() {
	if f != nil && f.upstreamReleaseOnce != nil {
		f.upstreamReleaseOnce.Do(func() { close(f.upstreamRelease) })
	}
}

type countingCharityRail struct {
	inner forward.CharityRail
	calls *atomic.Int32
}

func (r countingCharityRail) ListCallerModels(ctx context.Context) ([]db.CallerModel, error) {
	return r.inner.ListCallerModels(ctx)
}

func (r countingCharityRail) Forward(ctx context.Context, writer http.ResponseWriter, userID int64, request *openai.ChatRequest) (openai.AttemptResult, error) {
	r.calls.Add(1)
	return r.inner.Forward(ctx, writer, userID, request)
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
	var blockUpstream atomic.Bool
	var charityCalls atomic.Int32
	var deniedCalls atomic.Int32
	var upstreamFailures atomic.Int32
	upstreamEntered := make(chan struct{}, 1)
	upstreamRelease := make(chan struct{})
	upstreamReleaseOnce := &sync.Once{}
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		for upstreamFailures.Load() > 0 {
			current := upstreamFailures.Load()
			if current > 0 && upstreamFailures.CompareAndSwap(current, current-1) {
				writer.WriteHeader(http.StatusBadGateway)
				_, _ = writer.Write([]byte(`{"error":"temporary"}`))
				return
			}
		}
		if blockUpstream.Load() {
			select {
			case upstreamEntered <- struct{}{}:
			default:
			}
			<-upstreamRelease
		}
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
	dbPath := filepath.Join(t.TempDir(), "flowcontrol.db")
	dbtest.EnsureOwnerOnlyParent(t, dbPath)
	store, err := db.Open(dbPath, codec)
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
		CharityTargets:    store,
		Secrets:           codec,
		Registry:          registry,
		Adapters:          []forward.Adapter{adapter},
		SafetyIdentifiers: safetyIdentifierFactory,
	})
	if err != nil {
		t.Fatal(err)
	}
	service, err := forward.NewService(forward.ServiceConfig{
		Repository: store,
		Runner:     runner,
	})
	if err != nil {
		t.Fatal(err)
	}
	charityService, err := charityrouting.NewService(charityrouting.ServiceConfig{Store: store, Runner: runner})
	if err != nil {
		t.Fatal(err)
	}
	charityRail := countingCharityRail{inner: charityService, calls: &charityCalls}
	exit := forward.NewHandler(forward.HandlerDeps{Service: service, Charity: charityRail, Identity: forward.CallerIdentity})
	controller, err := flowcontrol.New(flowcontrol.Config{
		RPM: rpmConfig, UserLimits: flowcontrol.DBUserLimitResolver(store),
		OnDenied: func(context.Context, int64, ratelimit.RPMReason) { deniedCalls.Add(1) },
	})
	if err != nil {
		t.Fatal(err)
	}
	middleware, err := flowcontrol.NewMiddleware(controller, forward.CallerIdentity)
	if err != nil {
		t.Fatal(err)
	}
	wrapped := auth.CallerKeyMiddleware(store, middleware.Wrap(exit))
	t.Cleanup(func() {
		upstreamReleaseOnce.Do(func() { close(upstreamRelease) })
		_ = charityService.Close()
		_ = service.Close()
		_ = safetyIdentifierFactory.Close()
		controller.Close()
		stack.CloseIdleConnections()
		_ = store.Close()
		_ = vault.Close()
	})
	return &integrationFixture{
		store: store, codec: codec, stack: stack, controller: controller, handler: wrapped,
		upstream: upstream, hits: &hits, blockUpstream: &blockUpstream,
		upstreamEntered: upstreamEntered, upstreamRelease: upstreamRelease,
		upstreamReleaseOnce: upstreamReleaseOnce,
		charityCalls:        &charityCalls, deniedCalls: &deniedCalls, upstreamFailures: &upstreamFailures,
	}
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

func (f *integrationFixture) addSilentRetryRoute(t *testing.T, userID int64) {
	t.Helper()
	ctx := context.Background()
	canonical, err := f.stack.ValidateBaseURL(f.upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	endpointRow, err := f.store.CreateEndpoint(ctx, userID, string(endpoint.ConnectorOpenAICompatible), canonical, "", true, time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	modelRow, err := f.store.CreateModel(ctx, userID, "retry", "model", "ordered", true, time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		keyRow, err := f.store.CreateEndpointKey(ctx, userID, endpointRow.ID, []byte("sk-retry-"+strconv.Itoa(i)), "head", "tail", "", true, time.Now().Unix())
		if err != nil {
			t.Fatal(err)
		}
		if err := f.store.ReplaceFetchedModels(ctx, userID, endpointRow.ID, keyRow.ID, []db.FetchedModel{{
			EndpointKeyID: keyRow.ID, UpstreamModelID: "upstream/model", Provider: "retry", Status: "ok",
		}}, time.Now().Unix()); err != nil {
			t.Fatal(err)
		}
		if _, err := f.store.CreateBinding(ctx, userID, modelRow.ID, keyRow.ID, "upstream/model", int64(i), time.Now().Unix()); err != nil {
			t.Fatal(err)
		}
	}
}

func (f *integrationFixture) addCharityRoute(t *testing.T, consumerID int64) {
	t.Helper()
	ctx := context.Background()
	if err := f.store.SetSiteConfigValue("charity_enabled", "1"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.DB().Exec(`UPDATE users SET credits=10000 WHERE id=?`, consumerID); err != nil {
		t.Fatal(err)
	}
	donor, _, err := f.store.FindOrCreateDiscordUser("flow-charity-donor", "donor", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := f.stack.ValidateBaseURL(f.upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	endpointRow, err := f.store.CreateEndpoint(ctx, donor.ID, string(endpoint.ConnectorOpenAICompatible), canonical, "", true, 40)
	if err != nil {
		t.Fatal(err)
	}
	keyRow, err := f.store.CreateEndpointKey(ctx, donor.ID, endpointRow.ID, []byte("sk-flow-charity"), "head", "tail", "", true, 41)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.ReplaceFetchedModels(ctx, donor.ID, endpointRow.ID, keyRow.ID, []db.FetchedModel{{
		EndpointKeyID: keyRow.ID, UpstreamModelID: "upstream/model", Provider: "donor", Status: "ok",
	}}, 42); err != nil {
		t.Fatal(err)
	}
	donation, err := f.store.CreateDonation(ctx, db.CreateDonationInput{
		UserID: donor.ID, Description: "flow control charity fixture", Now: 43,
		Existing: &db.ExistingEndpointKeys{EndpointID: endpointRow.ID, KeyIDs: []int64{keyRow.ID}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.ApplyDonationReview(ctx, db.ReviewDecision{
		DonationID: donation.ID, Role: db.ReviewRoleAdmin, ReviewerID: donor.ID,
		Action: db.ReviewActionApprove, Now: 44,
	}); err != nil {
		t.Fatal(err)
	}
	var donationKeyID int64
	if err := f.store.DB().QueryRow(`SELECT id FROM donation_keys WHERE donation_id=?`, donation.ID).Scan(&donationKeyID); err != nil {
		t.Fatal(err)
	}
	modelRow, err := f.store.CreateCharityModel(ctx, db.CharityModel{
		Provider: "donor", Model: "charity", Enabled: true, PricingMode: db.CharityPricingPerRequest,
		RequestUserPrice: 500, RequestDonorReward: 100,
		UncachedUserPrice: 1_000_000, CacheWriteUserPrice: 1_000_000,
		CacheReadUserPrice: 1_000_000, OutputUserPrice: 1_000_000,
		UncachedDonorReward: 200_000, CacheWriteDonorReward: 200_000,
		CacheReadDonorReward: 200_000, OutputDonorReward: 200_000,
	}, 0, 45)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.CreateCharityBinding(ctx, modelRow.ID, donationKeyID, "upstream/model", 0, 46); err != nil {
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

type integrationCountingReader struct{ reads atomic.Int64 }

func (r *integrationCountingReader) Read([]byte) (int, error) {
	r.reads.Add(1)
	return 0, io.EOF
}

func TestConcurrencySharedAcrossRotatedCallerKeysPersonalAndCharity(t *testing.T) {
	config := ratelimit.RPMConfig{
		Window: time.Minute, GlobalLimit: 100, PerUserLimit: 100,
		MaxUserKeys: 1024, MaxEvents: 4096, MaxKeyBytes: 64,
	}
	fixture := newIntegrationFixture(t, config)
	userID, oldKey := fixture.addUser(t, "shared-concurrency")
	fixture.addRoute(t, userID)
	fixture.addCharityRoute(t, userID)
	one := 1
	if _, err := fixture.store.UpdateUserLimits(userID, db.UserLimitPatch{
		ConcurrencyLimitSet: true, ConcurrencyLimit: &one,
	}); err != nil {
		t.Fatal(err)
	}

	fixture.blockUpstream.Store(true)
	firstDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		recorder := httptest.NewRecorder()
		fixture.handler.ServeHTTP(recorder, integrationRequest(http.MethodPost, "/v1/chat/completions", oldKey, chatBody()))
		firstDone <- recorder
	}()
	select {
	case <-fixture.upstreamEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("personal request did not reach blocking upstream")
	}

	// Rotate while the already-authenticated old-key request is in flight.
	// The new key resolves to the same user id and therefore must share the
	// exact active counter rather than opening a second caller-key bucket.
	rotated, err := fixture.store.SetCallerKey(userID)
	if err != nil {
		t.Fatal(err)
	}
	var reservationsBefore int
	if err := fixture.store.DB().QueryRow(`SELECT COUNT(*) FROM charity_reservations`).Scan(&reservationsBefore); err != nil {
		t.Fatal(err)
	}
	creditsBefore, err := fixture.store.GetUserByID(userID)
	if err != nil {
		t.Fatal(err)
	}
	reads := &integrationCountingReader{}
	request := httptest.NewRequest(http.MethodPost, "https://gateway.example/v1/chat/completions", reads)
	request = request.WithContext(host.WithStation(request.Context(), host.StationUser))
	request.Header.Set("Authorization", "Bearer "+rotated)
	request.Header.Set("Content-Type", "application/json")
	second := httptest.NewRecorder()
	fixture.handler.ServeHTTP(second, request)
	if second.Code != http.StatusTooManyRequests || second.Header().Get("Retry-After") != "" {
		t.Fatalf("concurrency status=%d retry=%q body=%s", second.Code, second.Header().Get("Retry-After"), second.Body.String())
	}
	if reads.reads.Load() != 0 || fixture.charityCalls.Load() != 0 {
		t.Fatalf("denial parsed body or entered charity rail: reads=%d calls=%d", reads.reads.Load(), fixture.charityCalls.Load())
	}
	if fixture.codec.opens.Load() != 1 || fixture.hits.Load() != 1 || fixture.deniedCalls.Load() != 0 {
		t.Fatalf("denial side effects: decrypt=%d upstream=%d penalties=%d", fixture.codec.opens.Load(), fixture.hits.Load(), fixture.deniedCalls.Load())
	}
	var reservationsAfter int
	if err := fixture.store.DB().QueryRow(`SELECT COUNT(*) FROM charity_reservations`).Scan(&reservationsAfter); err != nil {
		t.Fatal(err)
	}
	creditsAfter, err := fixture.store.GetUserByID(userID)
	if err != nil {
		t.Fatal(err)
	}
	if reservationsAfter != reservationsBefore || creditsAfter.Credits != creditsBefore.Credits {
		t.Fatalf("charity economy changed: reservations %d->%d credits %d->%d",
			reservationsBefore, reservationsAfter, creditsBefore.Credits, creditsAfter.Credits)
	}

	fixture.blockUpstream.Store(false)
	fixture.releaseBlockedUpstream()
	select {
	case first := <-firstDone:
		if first.Code != http.StatusOK {
			t.Fatalf("first status=%d body=%s", first.Code, first.Body.String())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("first request did not finish")
	}
	// Once the one shared permit releases, the rotated key can reach the real
	// charity route and create exactly one economic reservation.
	charityBody := `{"model":"[公益]donor/charity","messages":[{"role":"user","content":"hello"}]}`
	third := httptest.NewRecorder()
	fixture.handler.ServeHTTP(third, integrationRequest(http.MethodPost, "/v1/chat/completions", rotated, charityBody))
	if third.Code != http.StatusOK || fixture.charityCalls.Load() != 1 {
		t.Fatalf("charity after release status=%d calls=%d body=%s", third.Code, fixture.charityCalls.Load(), third.Body.String())
	}
}

func TestSilentRetryHoldsExactlyOneConcurrencyPermit(t *testing.T) {
	config := ratelimit.RPMConfig{
		Window: time.Minute, GlobalLimit: 100, PerUserLimit: 100,
		MaxUserKeys: 1024, MaxEvents: 4096, MaxKeyBytes: 64,
	}
	fixture := newIntegrationFixture(t, config)
	userID, key := fixture.addUser(t, "silent-retry")
	fixture.addSilentRetryRoute(t, userID)
	one := 1
	if _, err := fixture.store.UpdateUserLimits(userID, db.UserLimitPatch{
		ConcurrencyLimitSet: true, ConcurrencyLimit: &one,
	}); err != nil {
		t.Fatal(err)
	}
	fixture.upstreamFailures.Store(1)
	fixture.blockUpstream.Store(true)
	body := `{"model":"retry/model","messages":[{"role":"user","content":"hello"}]}`
	firstDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		recorder := httptest.NewRecorder()
		fixture.handler.ServeHTTP(recorder, integrationRequest(http.MethodPost, "/v1/chat/completions", key, body))
		firstDone <- recorder
	}()
	select {
	case <-fixture.upstreamEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("silent retry did not reach its second attempt")
	}
	if fixture.hits.Load() != 2 || fixture.codec.opens.Load() != 2 {
		t.Fatalf("retry attempts hits=%d decryptions=%d", fixture.hits.Load(), fixture.codec.opens.Load())
	}
	second := httptest.NewRecorder()
	fixture.handler.ServeHTTP(second, integrationRequest(http.MethodPost, "/v1/chat/completions", key, body))
	if second.Code != http.StatusTooManyRequests || second.Header().Get("Retry-After") != "" {
		t.Fatalf("parallel request status=%d retry=%q body=%s", second.Code, second.Header().Get("Retry-After"), second.Body.String())
	}
	if fixture.hits.Load() != 2 || fixture.codec.opens.Load() != 2 || fixture.deniedCalls.Load() != 0 {
		t.Fatalf("parallel denial side effects hits=%d decryptions=%d penalties=%d",
			fixture.hits.Load(), fixture.codec.opens.Load(), fixture.deniedCalls.Load())
	}
	fixture.blockUpstream.Store(false)
	fixture.releaseBlockedUpstream()
	select {
	case first := <-firstDone:
		if first.Code != http.StatusOK {
			t.Fatalf("retry request status=%d body=%s", first.Code, first.Body.String())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("retry request did not finish")
	}
	third := httptest.NewRecorder()
	fixture.handler.ServeHTTP(third, integrationRequest(http.MethodPost, "/v1/chat/completions", key, body))
	if third.Code != http.StatusOK {
		t.Fatalf("permit did not release: status=%d body=%s", third.Code, third.Body.String())
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

// TestDBUserLimitOverride verifies users.rpm_limit is honored below or above
// the site default without being clamped, through the real handler path.
func TestDBUserLimitOverride(t *testing.T) {
	config := ratelimit.RPMConfig{
		Window:       time.Minute,
		GlobalLimit:  100,
		PerUserLimit: 3, // site default, not a ceiling
		MaxUserKeys:  1024,
		MaxEvents:    4096,
		MaxKeyBytes:  64,
	}
	fixture := newIntegrationFixture(t, config)
	withinID, withinKey := fixture.addUser(t, "clamp-within")
	overID, overKey := fixture.addUser(t, "override-over")
	fixture.addRoute(t, withinID)
	fixture.addRoute(t, overID)

	fixture.setUserRPMLimit(t, withinID, 2) // below default: honored
	fixture.setUserRPMLimit(t, overID, 5)   // above default: independently honored

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

	for i := 0; i < 5; i++ {
		recorder := httptest.NewRecorder()
		fixture.handler.ServeHTTP(recorder, integrationRequest(http.MethodPost, "/v1/chat/completions", overKey, chatBody()))
		if recorder.Code != http.StatusOK {
			t.Fatalf("over user request %d status=%d body=%s", i+1, recorder.Code, recorder.Body.String())
		}
	}
	recorder = httptest.NewRecorder()
	fixture.handler.ServeHTTP(recorder, integrationRequest(http.MethodPost, "/v1/chat/completions", overKey, chatBody()))
	if recorder.Code != http.StatusTooManyRequests {
		t.Fatalf("over user explicit-cap status=%d body=%s", recorder.Code, recorder.Body.String())
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
