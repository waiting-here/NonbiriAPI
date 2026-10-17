package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/auth"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbfixture"
	"github.com/waiting-here/NonbiriAPI/internal/egress"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

// TestApplicationSyntheticLoad is opt-in: it uses the real application, a
// private SQLite database, synthetic identities and a loopback-only upstream.
// Run with NONBIRI_LOAD_TEST=1 go test ./internal/app -run
// '^TestApplicationSyntheticLoad$' -count=1 -v -timeout=10m.
// The default workload holds model streams for eight seconds while six page
// probes each issue at most 2.5 requests/second. This is an interference
// experiment, not a claim about the number of supported browser sessions.
func TestApplicationSyntheticLoad(t *testing.T) {
	if os.Getenv("NONBIRI_LOAD_TEST") != "1" {
		t.Skip("set NONBIRI_LOAD_TEST=1 to run the synthetic load experiment")
	}
	hold := 8 * time.Second
	if raw := os.Getenv("NONBIRI_LOAD_HOLD"); raw != "" {
		var err error
		hold, err = time.ParseDuration(raw)
		if err != nil || hold < time.Second || hold > time.Minute {
			t.Fatal("NONBIRI_LOAD_HOLD must be between 1s and 1m")
		}
	}
	cases := []struct {
		name string
		n    int
		mode string
	}{
		{"pages_only", 0, "stream"},
		{"stream_50", 50, "stream"},
		{"stream_100", 100, "stream"},
		{"stream_200", 200, "stream"},
		{"short_200", 200, "short"},
		{"slow_cancel_200", 200, "slow_cancel"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { runSyntheticLoad(t, tc.n, tc.mode, hold) })
	}
}

type loadIdentity struct {
	id      int64
	caller  string
	session string
}

type loadUpstream struct {
	release  chan struct{}
	once     sync.Once
	entered  atomic.Int64
	active   atomic.Int64
	peak     atomic.Int64
	canceled atomic.Int64
	mode     string
}

func (u *loadUpstream) finish() { u.once.Do(func() { close(u.release) }) }

func (u *loadUpstream) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/chat/completions") {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	var input struct{ Stream bool }
	if json.NewDecoder(r.Body).Decode(&input) != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	u.entered.Add(1)
	active := u.active.Add(1)
	loadRaisePeak(&u.peak, active)
	defer u.active.Add(-1)
	if !input.Stream {
		select {
		case <-time.After(50 * time.Millisecond):
		case <-r.Context().Done():
			u.canceled.Add(1)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"synthetic","object":"chat.completion","created":1,"model":"private-model","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1024,"completion_tokens":2,"total_tokens":1026}}`)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	content := "synthetic token"
	if u.mode == "slow_cancel" {
		content = strings.Repeat("x", 16<<10)
	}
	chunk := `data: {"id":"synthetic","object":"chat.completion.chunk","created":1,"model":"private-model","choices":[{"index":0,"delta":{"content":"` + content + `"},"finish_reason":null}]}` + "\n\n"
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := io.WriteString(w, chunk); err != nil {
			return
		}
		w.(http.Flusher).Flush()
		select {
		case <-u.release:
			_, _ = io.WriteString(w, "data: {\"id\":\"synthetic\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"private-model\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":1024,\"completion_tokens\":2,\"total_tokens\":1026}}\n\ndata: [DONE]\n\n")
			return
		case <-r.Context().Done():
			u.canceled.Add(1)
			return
		case <-ticker.C:
		}
	}
}

type loadFixture struct {
	store   *db.Store
	app     *application
	server  *httptest.Server
	client  *http.Client
	users   []loadIdentity
	inbound atomic.Int64
	peak    atomic.Int64
}

func newLoadFixture(t *testing.T, upstream *loadUpstream) *loadFixture {
	t.Helper()
	u := httptest.NewServer(http.HandlerFunc(upstream.serve))
	t.Cleanup(u.Close)
	t.Cleanup(upstream.finish)
	vault, err := secret.New(bytes.Repeat([]byte{0x63}, secret.MasterKeyBytes))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = vault.Close() })
	path := filepath.Join(t.TempDir(), "synthetic-load.sqlite")
	dbfixture.Materialize(t, path, vault)
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	seed := &embeddingHTTPFixture{store: store}
	seed.caller = seedRootCallerIdentity(t, store)
	if err := store.DB().QueryRow(`SELECT user_id FROM caller_keys WHERE generation=1`).Scan(&seed.userID); err != nil {
		t.Fatal(err)
	}
	seed.seedModels(t, vault, u.URL+"/pool0/v1", "openai-compatible")
	f := &loadFixture{store: store}
	f.users = append(f.users, loadIdentity{id: seed.userID, caller: seed.caller})
	now := time.Now().Unix()
	zero := make([]byte, 16)
	for index := 1; index < 40; index++ {
		id := seed.exec(t, `INSERT INTO users(discord_id,username,donation_credit_mag,total_requests,total_uncached_input_tokens,total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,total_unknown_usage_requests,revision,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, fmt.Sprintf("synthetic-%d", index), "Synthetic user", zero, zero, zero, zero, zero, zero, zero, zero, now, now)
		body := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{byte(index)}, 32))
		caller := "nbk_" + body
		digest := sha256.Sum256([]byte(caller))
		seed.exec(t, `INSERT INTO caller_keys(user_id,generation,key_hash,display_head,display_tail,key_created_at,updated_at) VALUES(?,1,?,?,?,?,?)`, id, digest[:], body[:4], body[len(body)-4:], now, now)
		base := fmt.Sprintf("%s/pool%d/v1", u.URL, index%4)
		endpoint := seed.exec(t, `INSERT INTO endpoints(user_id,connector_type,base_url,note,enabled,revision,created_at,updated_at) VALUES(?,'openai-compatible',?,'Synthetic endpoint',1,1,?,?)`, id, base, now, now)
		contextID, fingerprint := make([]byte, 16), make([]byte, 32)
		contextID[0], fingerprint[0] = byte(index), byte(index)
		keyContext, err := secret.NewGenerationTwoEndpointKeyContext(contextID)
		if err != nil {
			t.Fatal(err)
		}
		envelope, err := vault.SealForGenerationTwoContext([]byte("synthetic-upstream-key"), keyContext)
		if err != nil {
			t.Fatal(err)
		}
		secretID := seed.exec(t, `INSERT INTO endpoint_key_secrets(context_id,canonical_base_url,connector_type,encrypted_secret,created_at) VALUES(?,?,'openai-compatible',?,?)`, contextID, base, envelope, now)
		key := seed.exec(t, `INSERT INTO endpoint_keys(endpoint_id,secret_ref_id,secret_fingerprint,display_head,display_tail,note,enabled,force_store_false,revision,created_at,updated_at) VALUES(?,?,?,'head','tail','Synthetic key',1,1,1,?,?)`, endpoint, secretID, fingerprint, now, now)
		seed.exec(t, `INSERT INTO model_discovery_evidence(endpoint_key_id,state,revision,safe_class,safe_diag,fetched_count) VALUES(?,'unknown',1,'none','',0)`, key)
		seed.exec(t, `INSERT INTO model_pair_catalog(endpoint_key_id,normalized_model_id,automatic_supports,manual_supports,automatic_revision,pair_revision,updated_at) VALUES(?,'private-model',0,1,0,1,?)`, key, now)
		model := seed.exec(t, `INSERT INTO models(user_id,provider,model,full_name,route_strategy,silent_retry,flatten_tool_calls,revision,binding_revision,model_types,created_at,updated_at) VALUES(?,'provider','self','provider/self','ordered',0,1,1,1,3,?,?)`, id, now, now)
		seed.exec(t, `INSERT INTO model_bindings(model_id,endpoint_key_id,upstream_model_id,ord,created_at,updated_at) VALUES(?,?,'private-model',0,?,?)`, model, key, now, now)
		f.users = append(f.users, loadIdentity{id: id, caller: caller})
	}
	modelCount := 50
	if os.Getenv("NONBIRI_LOAD_SCALE") == "production" {
		modelCount = 220
	}
	for index := 2; index < modelCount; index++ {
		model := seed.exec(t, `INSERT INTO charity_models(provider,model,full_name,enabled,pricing_mode,request_user_price,request_donor_reward,revision,binding_revision,model_types,created_at,updated_at) VALUES('synthetic',?,?,1,'per_request',3000,1250,1,1,3,?,?)`, fmt.Sprintf("model%d", index), fmt.Sprintf("[公益]synthetic/model%d", index), now, now)
		seed.exec(t, `INSERT INTO charity_model_access(model_id,allowed_level_mask,public_description) VALUES(?,31,'')`, model)
		seed.exec(t, `INSERT INTO charity_routing_settings(model_id,revision,affinity_ttl_seconds) VALUES(?,1,300)`, model)
		seed.exec(t, `INSERT INTO charity_model_bindings(charity_model_id,donation_key_id,endpoint_key_id,upstream_model_id,ord,created_at,updated_at) SELECT ?,donation_key_id,endpoint_key_id,upstream_model_id,0,?,? FROM charity_model_bindings LIMIT 1`, model, now, now)
	}
	if modelCount == 220 {
		seedCatalogLoadHistory(t, seed, now)
	}
	stack, err := egress.NewStack(egress.StackOptions{AllowedOrigins: []string{u.URL}})
	if err != nil {
		t.Fatal(err)
	}
	f.app, err = buildApplicationWithRuntimeOptions(context.Background(), auditConfig(), store, vault, applicationRuntimeOptions{Egress: stack})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.app.Close() })
	login := testApplicationRequest(t, f.app.handler, "POST", auditAdminHost, "/admin/api/login", `{"username":"operator","password":"correct horse battery staple"}`, nil, map[string]string{"Content-Type": "application/json"})
	admin := []*http.Cookie{responseCookieNamed(t, login, auth.AdminSessionCookieName)}
	headers := map[string]string{"Content-Type": "application/json", "Origin": "http://" + auditAdminHost, "Idempotency-Key": strings.Repeat("L", 22)}
	disable := testApplicationRequest(t, f.app.handler, "POST", auditAdminHost, "/admin/api/maintenance/disable", `{"expected_revision":"1","reason":"Synthetic load validation"}`, admin, headers)
	if disable.Code != 200 {
		t.Fatalf("disable maintenance: %d %s", disable.Code, disable.Body.String())
	}
	headers["Idempotency-Key"] = strings.Repeat("M", 22)
	patch := testApplicationRequest(t, f.app.handler, "PATCH", auditAdminHost, "/admin/api/games/config", `{"expected_revision":"1","master_enabled":true,"blackjack":{"enabled":true}}`, admin, headers)
	if patch.Code != 200 {
		t.Fatalf("enable game: %d %s", patch.Code, patch.Body.String())
	}
	ctx := context.Background()
	tx, err := store.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for index := range f.users {
		user := &f.users[index]
		if _, err := f.app.authRuntime.IdentityContinuity().BindUserTx(ctx, tx, user.id); err != nil {
			t.Fatal(err)
		}
		general, err := ledger.CreateUserAccount(ctx, tx, user.id, now)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ledger.CreateUserAssetAccount(ctx, tx, user.id, ledger.Game, now); err != nil {
			t.Fatal(err)
		}
		if index > 0 {
			external, err := ledger.CodedAccount(ctx, tx, "external")
			if err != nil {
				t.Fatal(err)
			}
			op, err := db.GenerateOpaqueID("op_")
			if err != nil {
				t.Fatal(err)
			}
			plan, err := ledger.NewAdminUserAdjustment(ledger.Meta{OperationID: op, ActorUserID: user.id, CreatedAt: now}, general.ID, external.ID, ledger.AmountFromMilli(1000000), 0, ledger.Amount{}, "Synthetic funding")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := ledger.Apply(ctx, tx, plan); err != nil {
				t.Fatal(err)
			}
		}
		user.session = fmt.Sprintf("synthetic_load_session_%032d", index)
		digest := sha256.Sum256([]byte(user.session))
		if _, err := tx.Exec(`INSERT INTO sessions(token_hash,user_id,last_seen_at,expires_at,absolute_expires_at,created_at,cred_gen) VALUES(?,?,?,?,?,?,?)`, hex.EncodeToString(digest[:]), user.id, now, now+3600, now+7200, now, "synthetic-session-generation"); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/chat/completions" {
			loadRaisePeak(&f.peak, f.inbound.Add(1))
			defer f.inbound.Add(-1)
		}
		f.app.handler.ServeHTTP(w, r)
	}))
	t.Cleanup(f.server.Close)
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.MaxIdleConnsPerHost = 256
	transport.MaxIdleConns = 256
	f.client = &http.Client{Transport: transport, Timeout: 90 * time.Second}
	t.Cleanup(transport.CloseIdleConnections)
	return f
}

type loadSample struct {
	route, phase string
	duration     time.Duration
	status       int
	err          string
}

type loadMeasurements struct {
	mu      sync.Mutex
	samples []loadSample
}

func (m *loadMeasurements) add(sample loadSample) {
	m.mu.Lock()
	m.samples = append(m.samples, sample)
	m.mu.Unlock()
}

type loadPercentiles struct {
	Count  int            `json:"count"`
	P50MS  float64        `json:"p50_ms"`
	P95MS  float64        `json:"p95_ms"`
	P99MS  float64        `json:"p99_ms"`
	MaxMS  float64        `json:"max_ms"`
	Status map[int]int    `json:"status"`
	Errors map[string]int `json:"errors,omitempty"`
}

func (m *loadMeasurements) summaries() map[string]loadPercentiles {
	m.mu.Lock()
	defer m.mu.Unlock()
	groups := map[string][]loadSample{}
	for _, sample := range m.samples {
		key := sample.phase + " " + sample.route
		groups[key] = append(groups[key], sample)
	}
	out := map[string]loadPercentiles{}
	for key, samples := range groups {
		sort.Slice(samples, func(i, j int) bool { return samples[i].duration < samples[j].duration })
		percentile := func(q float64) float64 {
			return float64(samples[int(math.Ceil(q*float64(len(samples))))-1].duration) / float64(time.Millisecond)
		}
		p := loadPercentiles{Count: len(samples), P50MS: percentile(.5), P95MS: percentile(.95), P99MS: percentile(.99), MaxMS: percentile(1), Status: map[int]int{}, Errors: map[string]int{}}
		for _, sample := range samples {
			p.Status[sample.status]++
			if sample.err != "" {
				p.Errors[sample.err]++
			}
		}
		out[key] = p
	}
	return out
}

type loadMemory struct {
	Goroutines int    `json:"goroutines"`
	HeapBytes  uint64 `json:"heap_bytes"`
	TotalAlloc uint64 `json:"total_alloc"`
	PauseNS    uint64 `json:"gc_pause_ns"`
}

func loadReadMemory() loadMemory {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	return loadMemory{runtime.NumGoroutine(), mem.HeapAlloc, mem.TotalAlloc, mem.PauseTotalNs}
}

func loadRaisePeak(peak *atomic.Int64, value int64) {
	for old := peak.Load(); value > old; old = peak.Load() {
		if peak.CompareAndSwap(old, value) {
			return
		}
	}
}

func runSyntheticLoad(t *testing.T, n int, mode string, hold time.Duration) {
	u := &loadUpstream{release: make(chan struct{}), mode: mode}
	f := newLoadFixture(t, u)
	rpm, limits, err := loadRuntimeLimits(context.Background(), f.store)
	if err != nil {
		t.Fatal(err)
	}
	if limits != egress.DefaultConcurrencyLimits() {
		t.Fatalf("fixture defaults changed: %+v", limits)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Second)
	defer cancel()
	var phase atomic.Int32
	phaseName := func() string { return []string{"ramp", "held", "completion", "recovered"}[phase.Load()] }
	pages, models := &loadMeasurements{}, &loadMeasurements{}
	routes := []string{"/api/session", "/api/me", "/api/games", "/api/games/blackjack/state", "/api/charity/models?view=catalog&page=1&page_size=50", "/api/home/game-summary"}
	// Warm the same paths before measuring; an invalid probe is a setup error.
	for _, route := range routes {
		s := f.get(ctx, route, f.users[0])
		if s.status != 200 || s.err != "" {
			t.Fatalf("warm %s: %+v", route, s)
		}
	}
	runtime.GC()
	before := loadReadMemory()
	stats := f.store.Stats()
	start := time.Now()
	arrivalInterval := 10 * time.Millisecond
	if mode == "short" {
		arrivalInterval = 0
	}
	poolPhases := map[string]map[string]float64{}
	previousStats, previousTime := stats, start
	previousWriter, previousReader := f.store.StatsByPool()
	recordPoolPhase := func(name string) {
		current, at := f.store.Stats(), time.Now()
		writer, reader := f.store.StatsByPool()
		poolPhases[name] = map[string]float64{
			"elapsed_ms":        float64(at.Sub(previousTime)) / float64(time.Millisecond),
			"writer_wait_count": float64(writer.WaitCount - previousWriter.WaitCount),
			"reader_wait_count": float64(reader.WaitCount - previousReader.WaitCount),
			"writer_wait_ms":    float64(writer.WaitDuration-previousWriter.WaitDuration) / float64(time.Millisecond),
			"reader_wait_ms":    float64(reader.WaitDuration-previousReader.WaitDuration) / float64(time.Millisecond),
			"wait_count":        float64(current.WaitCount - previousStats.WaitCount),
			"wait_ms":           float64(current.WaitDuration-previousStats.WaitDuration) / float64(time.Millisecond),
		}
		previousStats, previousTime = current, at
		previousWriter, previousReader = writer, reader
	}
	pageCtx, stopPages := context.WithCancel(ctx)
	var pageWG sync.WaitGroup
	for index, route := range routes {
		pageWG.Add(1)
		go func(index int, route string) {
			defer pageWG.Done()
			ticker := time.NewTicker(400 * time.Millisecond)
			defer ticker.Stop()
			for seq := index; ; seq++ {
				select {
				case <-pageCtx.Done():
					return
				case <-ticker.C:
				}
				p := phaseName()
				s := f.get(ctx, route, f.users[seq%len(f.users)])
				if endPhase := phaseName(); endPhase != p {
					p += "->" + endPhase
				}
				s.phase = p
				pages.add(s)
			}
		}(index, route)
	}
	var modelWG sync.WaitGroup
	cancels := make([]context.CancelFunc, n)
	for index := range n {
		requestCtx, requestCancel := context.WithCancel(ctx)
		cancels[index] = requestCancel
		defer requestCancel()
		modelWG.Add(1)
		go func(index int) {
			defer modelWG.Done()
			if wait := time.Until(start.Add(time.Duration(index) * arrivalInterval)); wait > 0 {
				timer := time.NewTimer(wait)
				defer timer.Stop()
				select {
				case <-timer.C:
				case <-ctx.Done():
					return
				}
			}
			f.model(requestCtx, index, mode, models)
		}(index)
	}
	// A short fixed ramp is included in page summaries; the held snapshot
	// reports actual work that reached dispatch, not merely offered requests.
	time.Sleep(3 * time.Second)
	recordPoolPhase("ramp")
	phase.Store(1)
	var dispatched int
	if err := f.store.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM dispatch_claims WHERE state='dispatched'`).Scan(&dispatched); err != nil {
		t.Fatal(err)
	}
	enteredAtHold, activeAtHold := u.entered.Load(), u.active.Load()
	heldMemory := loadReadMemory()
	if mode == "slow_cancel" {
		time.Sleep(hold / 2)
		for index, requestCancel := range cancels {
			if index%5 == 0 {
				requestCancel()
			}
		}
		time.Sleep(hold - hold/2)
	} else {
		time.Sleep(hold)
	}
	recordPoolPhase("held")
	phase.Store(2)
	releaseAt := time.Now()
	u.finish()
	modelsDone := make(chan struct{})
	go func() { modelWG.Wait(); close(modelsDone) }()
	select {
	case <-modelsDone:
	case <-ctx.Done():
		t.Fatal("model requests did not drain within the experiment budget")
	}
	// HTTP clients can see terminal bytes before the server commits its final
	// ledger and audit facts. Wait for handler exit before checking recovery.
	for f.inbound.Load() != 0 || u.active.Load() != 0 {
		select {
		case <-ctx.Done():
			t.Fatal("handlers did not drain")
		case <-time.After(10 * time.Millisecond):
		}
	}
	drain := time.Since(releaseAt)
	recordPoolPhase("completion")
	phase.Store(3)
	time.Sleep(2 * time.Second)
	stopPages()
	pageWG.Wait()
	recordPoolPhase("recovered")
	afterStats := f.store.Stats()
	f.client.CloseIdleConnections()
	f.app.egress.CloseIdleConnections()
	runtime.GC()
	after := loadReadMemory()
	var activeClaims, activeRequests, reserved int
	for _, check := range []struct {
		sql string
		n   *int
	}{
		{`SELECT COUNT(*) FROM dispatch_claims WHERE state IN ('claimed','dispatched')`, &activeClaims},
		{`SELECT COUNT(*) FROM logical_requests WHERE state<>'terminal'`, &activeRequests},
		{`SELECT COUNT(*) FROM donation_usage_reservations WHERE state='reserved'`, &reserved},
	} {
		if err := f.store.DB().QueryRowContext(ctx, check.sql).Scan(check.n); err != nil {
			t.Fatal(err)
		}
	}
	if activeClaims+activeRequests+reserved != 0 {
		t.Errorf("unrecovered database state: claims=%d requests=%d reservations=%d", activeClaims, activeRequests, reserved)
	}
	failures := map[string]int{}
	rows, err := f.store.DB().QueryContext(ctx, `SELECT COALESCE(rejection_stage,''),COALESCE(rejection_reason,''),COALESCE(caller_error_code,''),COUNT(*) FROM logical_requests WHERE caller_result_class='failed' GROUP BY 1,2,3`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var stage, reason, code string
		var count int
		if err := rows.Scan(&stage, &reason, &code, &count); err != nil {
			t.Fatal(err)
		}
		failures[stage+":"+reason+":"+code] = count
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	tx, err := f.store.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	err = ledger.ValidateRecovery(ctx, tx)
	_ = tx.Rollback()
	if err != nil {
		t.Errorf("ledger recovery: %v", err)
	}
	pageSummary := pages.summaries()
	for route, summary := range pageSummary {
		if len(summary.Errors) != 0 || len(summary.Status) != 1 || summary.Status[200] != summary.Count {
			t.Errorf("page request failed: %s %+v", route, summary)
		}
	}
	modelSummary := models.summaries()
	completed, canceled := 0, 0
	for key, summary := range modelSummary {
		if !strings.HasPrefix(key, "complete ") {
			continue
		}
		completed += summary.Count
		canceled += summary.Errors["canceled"]
		for kind := range summary.Errors {
			if kind != "http_429" && kind != "http_503" && (kind != "canceled" || mode != "slow_cancel") {
				t.Errorf("unexpected model error: %s %+v", key, summary)
			}
		}
		for status := range summary.Status {
			if status != 200 && status != 429 && status != 503 && !(status == 0 && mode == "slow_cancel") {
				t.Errorf("unexpected model status: %s %+v", key, summary)
			}
		}
	}
	wantCanceled := 0
	if mode == "slow_cancel" {
		wantCanceled = (n + 4) / 5
	}
	if completed != n || canceled != wantCanceled {
		t.Errorf("model recovery: completed=%d/%d canceled=%d/%d", completed, n, canceled, wantCanceled)
	}
	for key := range failures {
		if !strings.HasPrefix(key, "flow:") {
			t.Errorf("unexpected non-admission failure: %s", key)
		}
	}
	report := map[string]any{
		"mode": mode, "offered": n, "users": len(f.users), "scale": os.Getenv("NONBIRI_LOAD_SCALE"), "input_bytes": 4096, "arrival_interval_ms": arrivalInterval.Milliseconds(),
		"hold_seconds": hold.Seconds(), "elapsed_seconds": time.Since(start).Seconds(), "drain_ms": float64(drain) / float64(time.Millisecond),
		"go": runtime.Version(), "os": runtime.GOOS, "arch": runtime.GOARCH, "gomaxprocs": runtime.GOMAXPROCS(0),
		"egress_limits": limits, "global_rpm": rpm.GlobalLimit, "per_user_rpm": rpm.PerUserLimit,
		"inbound_peak": f.peak.Load(), "upstream_peak": u.peak.Load(), "upstream_entered": u.entered.Load(), "upstream_canceled": u.canceled.Load(),
		"held_dispatched": dispatched, "held_upstream_entered": enteredAtHold, "held_upstream_active": activeAtHold,
		"db_max_connections": afterStats.MaxOpenConnections, "db_wait_count": afterStats.WaitCount - stats.WaitCount,
		"db_wait_ms":    float64(afterStats.WaitDuration-stats.WaitDuration) / float64(time.Millisecond),
		"memory_before": before, "memory_held": heldMemory, "memory_after_gc": after,
		"pages": pageSummary, "models": modelSummary, "db_phases": poolPhases, "failure_facts": failures,
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("LOAD_RESULT %s", encoded)
}

func (f *loadFixture) get(ctx context.Context, path string, user loadIdentity) loadSample {
	start := time.Now()
	r, err := http.NewRequestWithContext(ctx, "GET", f.server.URL+path, nil)
	if err != nil {
		return loadSample{route: path, err: "request"}
	}
	r.Host = auditUserHost
	r.AddCookie(&http.Cookie{Name: auth.UserSessionCookieName, Value: user.session})
	if path == "/v1/models" {
		r.Header.Set("Authorization", "Bearer "+user.caller)
	}
	response, err := f.client.Do(r)
	if err != nil {
		return loadSample{route: path, duration: time.Since(start), err: "transport"}
	}
	_, err = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	s := loadSample{route: path, duration: time.Since(start), status: response.StatusCode}
	if err != nil {
		s.err = "body"
	}
	return s
}

func (f *loadFixture) model(ctx context.Context, index int, mode string, metrics *loadMeasurements) {
	model, route := "provider/self", "self"
	if index%3 == 0 {
		model, route = "[公益]provider/per_request", "charity"
	}
	stream := mode != "short"
	body, _ := json.Marshal(map[string]any{"model": model, "stream": stream, "messages": []map[string]string{{"role": "user", "content": strings.Repeat("x", 4096)}}, "max_tokens": 128})
	start := time.Now()
	r, err := http.NewRequestWithContext(ctx, "POST", f.server.URL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		metrics.add(loadSample{route: route, phase: "complete", err: "request"})
		return
	}
	r.Host = auditUserHost
	r.Header.Set("Authorization", "Bearer "+f.users[index%len(f.users)].caller)
	r.Header.Set("Content-Type", "application/json")
	response, err := f.client.Do(r)
	if err != nil {
		kind := "transport"
		if ctx.Err() != nil {
			kind = "canceled"
		}
		metrics.add(loadSample{route: route, phase: "complete", duration: time.Since(start), err: kind})
		return
	}
	defer response.Body.Close()
	first := true
	var tail string
	buffer := make([]byte, 32<<10)
	if mode == "slow_cancel" && index%5 == 0 {
		buffer = buffer[:1024]
	}
	for {
		n, readErr := response.Body.Read(buffer)
		if n > 0 {
			if first {
				metrics.add(loadSample{route: route, phase: "first_byte", duration: time.Since(start), status: response.StatusCode})
				first = false
			}
			tail += string(buffer[:n])
			if len(tail) > 128 {
				tail = tail[len(tail)-128:]
			}
			if mode == "slow_cancel" && index%5 == 0 {
				select {
				case <-ctx.Done():
				case <-time.After(20 * time.Millisecond):
				}
			}
		}
		if readErr != nil {
			err = readErr
			break
		}
	}
	s := loadSample{route: route, phase: "complete", duration: time.Since(start), status: response.StatusCode}
	if ctx.Err() != nil {
		s.err = "canceled"
	} else if err != io.EOF {
		s.err = "body"
	} else if response.StatusCode == 200 && stream && !strings.Contains(tail, "data: [DONE]") {
		s.err = "protocol_incomplete"
	} else if response.StatusCode != 200 {
		s.err = fmt.Sprintf("http_%d", response.StatusCode)
	}
	metrics.add(s)
}
