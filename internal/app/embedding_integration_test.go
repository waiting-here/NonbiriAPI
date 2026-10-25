package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/auth"
	"github.com/waiting-here/NonbiriAPI/internal/backend"
	"github.com/waiting-here/NonbiriAPI/internal/connectionstore/testseed"
	"github.com/waiting-here/NonbiriAPI/internal/connector"
	connectorcontract "github.com/waiting-here/NonbiriAPI/internal/connector/contract"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbfixture"
	"github.com/waiting-here/NonbiriAPI/internal/egress"
	"github.com/waiting-here/NonbiriAPI/internal/httpmw"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	"github.com/waiting-here/NonbiriAPI/internal/ratelimit"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

type embeddingHTTPFixture struct {
	store                  *db.Store
	app                    *application
	forward                *publicForwardRuntime
	server                 *httptest.Server
	caller                 string
	userCookies            []*http.Cookie
	userID, donorID, keyID int64
	mu                     sync.Mutex
	upstreamRequests       []map[string]json.RawMessage
}

// This uses the production CallerKey verifier, routing repositories, claim
// rail, ledger and LocalBackend. Only the upstream HTTP server is simulated.
func newEmbeddingHTTPFixture(t *testing.T, custom ...http.HandlerFunc) *embeddingHTTPFixture {
	t.Helper()
	return newEmbeddingHTTPFixtureWithConfig(t, nil, custom...)
}

func newEmbeddingHTTPFixtureWithConfig(t *testing.T, configure func(*db.Store, string), custom ...http.HandlerFunc) *embeddingHTTPFixture {
	t.Helper()
	connectorType := "openai-compatible"
	if len(custom) != 0 {
		connectorType = "ai-sdk-gateway-v3"
	}
	return newModelHTTPFixture(t, configure, connectorType, custom...)
}

func newModelHTTPFixture(t *testing.T, configure func(*db.Store, string), connectorType string, custom ...http.HandlerFunc) *embeddingHTTPFixture {
	return newModelHTTPFixtureWithEgress(t, configure, connectorType, nil, custom...)
}

func newModelHTTPFixtureWithEgress(t *testing.T, configure func(*db.Store, string), connectorType string, configureEgress func(*egress.StackOptions, string) string, custom ...http.HandlerFunc) *embeddingHTTPFixture {
	t.Helper()
	f := &embeddingHTTPFixture{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(custom) != 0 {
			custom[0](w, r)
			return
		}
		var body map[string]json.RawMessage
		if r.Method != "POST" || (r.URL.Path != "/v1/embeddings" && r.URL.Path != "/v1/chat/completions") || r.Header.Get("Authorization") != "Bearer embedding-fixture-secret" || json.NewDecoder(r.Body).Decode(&body) != nil {
			w.WriteHeader(400)
			return
		}
		f.mu.Lock()
		f.upstreamRequests = append(f.upstreamRequests, body)
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		var scenario, encoding string
		_ = json.Unmarshal(body["scenario"], &scenario)
		_ = json.Unmarshal(body["encoding_format"], &encoding)
		if scenario == "error" {
			w.WriteHeader(429)
			_, _ = io.WriteString(w, `{"error":{"message":"Capacity exhausted","code":"busy"}}`)
			return
		}
		if scenario == "bad-vector" {
			_, _ = io.WriteString(w, `{"object":"list","model":"private-model","data":[]}`)
			return
		}
		if r.URL.Path == "/v1/chat/completions" {
			_, _ = io.WriteString(w, `{"id":"chat-result","object":"chat.completion","created":1,"model":"private-model","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":0,"total_tokens":2}}`)
			return
		}
		count := 1
		if strings.HasPrefix(string(body["input"]), "[") {
			var items []json.RawMessage
			_ = json.Unmarshal(body["input"], &items)
			if len(items) > 0 && (items[0][0] == '[' || items[0][0] == '"') {
				count = len(items)
			}
		}
		data := make([]map[string]any, 0, count)
		for index := count - 1; index >= 0; index-- {
			var vector any = []float64{0.25, -0.5}
			if encoding == "base64" {
				vector = base64.StdEncoding.EncodeToString([]byte{0, 0, 128, 62, 0, 0, 0, 191})
			}
			data = append(data, map[string]any{"object": "embedding", "index": index, "embedding": vector, "private_diagnostic": "hidden-source"})
		}
		out := map[string]any{"object": "list", "model": "private-model", "data": data, "vendor": "hidden-source"}
		if scenario != "unknown" {
			tokens := 3
			if scenario == "over-reservation" {
				tokens = 2
			}
			if scenario == "zero" {
				tokens = 0
			}
			out["usage"] = map[string]int{"prompt_tokens": tokens, "total_tokens": tokens}
		}
		_ = json.NewEncoder(w).Encode(out)
	}))
	t.Cleanup(upstream.Close)
	vault, err := secret.New(bytes.Repeat([]byte{0x63}, secret.MasterKeyBytes))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = vault.Close() })
	path := filepath.Join(t.TempDir(), "embedding.sqlite")

	dbfixture.Materialize(t, path, vault)
	f.store, err = db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.store.Close() })
	cfg := auditConfig()
	base := upstream.URL + "/v1"
	egressOptions := egress.StackOptions{AllowedOrigins: []string{upstream.URL}, RequestTimeout: 5 * time.Second}
	if configureEgress != nil {
		base = configureEgress(&egressOptions, base)
	}
	if configure != nil {
		configure(f.store, base)
	}
	f.app, err = buildApplication(context.Background(), cfg, f.store, vault)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.app.Close() })
	login := testApplicationRequest(t, f.app.handler, "POST", auditAdminHost, "/admin/api/login", `{"username":"operator","password":"correct horse battery staple"}`, nil, map[string]string{"Content-Type": "application/json"})
	if login.Code != 200 {
		t.Fatalf("login %d %s", login.Code, login.Body.String())
	}
	cookie := responseCookieNamed(t, login, auth.AdminSessionCookieName)
	disable := testApplicationRequest(t, f.app.handler, "POST", auditAdminHost, "/admin/api/maintenance/disable", `{"expected_revision":"1","reason":"HTTP integration validation"}`, []*http.Cookie{cookie}, map[string]string{"Content-Type": "application/json", "Origin": "http://" + auditAdminHost, "Idempotency-Key": strings.Repeat("E", 22)})
	if disable.Code != 200 {
		t.Fatalf("disable %d %s", disable.Code, disable.Body.String())
	}
	f.caller = seedRootCallerIdentity(t, f.store)
	if err := f.store.DB().QueryRow(`SELECT user_id FROM caller_keys WHERE generation=1`).Scan(&f.userID); err != nil {
		t.Fatal(err)
	}
	f.seedModels(t, vault, base, connectorType)
	stack, err := egress.NewStack(egressOptions)
	if err != nil {
		t.Fatal(err)
	}
	if err := stack.AddSelfOrigins(context.Background(), auditConfig()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stack.CloseIdleConnections)
	local, err := backend.NewLocal(stack)
	if err != nil {
		t.Fatal(err)
	}
	invalidations := &userSessionInvalidationFanout{debug: f.app.debug}
	runtime, err := newPublicForwardRuntime(f.store, vault, f.app.adaptations, f.app.authRuntime.IdentityContinuity(), f.app.claims, f.app.charity, f.app.charityRouting, f.app.resourceRepo, connector.NewDefaultRegistry(), local, f.app.debug, f.app.gate, ratelimit.RPMConfig{GlobalLimit: 600, PerUserLimit: 600}, f.app.games.CancelUserDuelsTx, f.app.audits, invalidations.InvalidateUserAuthority)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	f.forward = runtime
	handler, err := stationBoundary(auditConfig(), f.app.audits.Wrap(httpmw.API(runtime.handler)))
	if err != nil {
		t.Fatal(err)
	}
	f.server = httptest.NewServer(handler)
	t.Cleanup(f.server.Close)
	return f
}

func (f *embeddingHTTPFixture) exec(t *testing.T, statement string, args ...any) int64 {
	t.Helper()
	result, err := f.store.DB().Exec(statement, args...)
	if err != nil {
		t.Fatal(err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func (f *embeddingHTTPFixture) ownLogJSON(t *testing.T, id string) []byte {
	t.Helper()
	if len(f.userCookies) == 0 {
		const token = "model_http_log_session_01234567890123456789"
		digest := sha256.Sum256([]byte(token))
		now := time.Now().Unix()
		f.exec(t, `INSERT INTO sessions(token_hash,user_id,last_seen_at,expires_at,absolute_expires_at,created_at,cred_gen) VALUES(?,?,?,?,?,?,?)`, fmt.Sprintf("%x", digest), f.userID, now, now+3600, now+3600, now, "model-log-session")
		f.userCookies = []*http.Cookie{{Name: auth.UserSessionCookieName, Value: token}}
	}
	response := testApplicationRequest(t, f.app.handler, "GET", auditUserHost, "/api/logs/"+id, "", f.userCookies, nil)
	if response.Code != http.StatusOK || !json.Valid(response.Body.Bytes()) {
		t.Fatalf("own log read: %d %s", response.Code, response.Body)
	}
	return response.Body.Bytes()
}

func (f *embeddingHTTPFixture) seedModels(t *testing.T, vault *secret.Vault, base, connectorType string) {
	t.Helper()
	now := time.Now().Unix()
	zero := make([]byte, 16)
	one := make([]byte, 16)
	one[15] = 1
	f.donorID = f.exec(t, `INSERT INTO users(discord_id,username,donation_credit_mag,total_requests,total_uncached_input_tokens,total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,total_unknown_usage_requests,revision,created_at,updated_at) VALUES('embedding-donor','fixture donor',?,?,?,?,?,?,?,?,?,?)`, zero, zero, zero, zero, zero, zero, zero, zero, now, now)
	tx, err := f.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for _, user := range []int64{f.userID, f.donorID} {
		account, err := ledger.CreateUserAccount(context.Background(), tx, user, now)
		if err != nil {
			t.Fatal(err)
		}
		external, err := ledger.CodedAccount(context.Background(), tx, "external")
		if err != nil {
			t.Fatal(err)
		}
		id, err := db.GenerateOpaqueID("op_")
		if err != nil {
			t.Fatal(err)
		}
		plan, err := ledger.NewAdminUserAdjustment(ledger.Meta{OperationID: id, ActorUserID: user, CreatedAt: now}, account.ID, external.ID, ledger.AmountFromMilli(1000000), 0, ledger.Amount{}, "fixture funding")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ledger.Apply(context.Background(), tx, plan); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	f.exec(t, `UPDATE site_config SET value='1' WHERE key IN ('charity_enabled','donation_accept_enabled')`)
	f.exec(t, `UPDATE site_config SET value='1000' WHERE key='charity_min_chars'`)
	f.exec(t, `INSERT INTO site_config(key,value,updated_at) VALUES('charity_token_reserve_milli','100',?) ON CONFLICT(key) DO UPDATE SET value='100',updated_at=excluded.updated_at`, now)
	kind := connectorcontract.Type(connectorType)
	services, err := connectorcontract.LegacyNativeServices(kind)
	if err != nil {
		t.Fatal(err)
	}
	// This multi-service fixture explicitly authorizes the inference operations
	// exercised below; dedicated Responses/Decisions entries retain their native route.
	if kind == connectorcontract.TypeOpenAICompatible || kind == connectorcontract.TypeAISDKGatewayV3 || kind == connectorcontract.TypeAISDKGatewayV4 {
		services, err = connectorcontract.SupportedNativeServices(kind)
		if err != nil {
			t.Fatal(err)
		}
	}
	var format connectorcontract.RerankFormat
	if kind == connectorcontract.TypeOpenAICompatible {
		format = connectorcontract.RerankFormatGeneric
	}
	for index, user := range []int64{f.userID, f.donorID} {
		endpoint := appFixtureEndpoint(t, f.store, testseed.EndpointInput{OwnerUserID: user, Type: kind, BaseURL: base, Note: "fixture endpoint", NativeServices: services, RerankFormat: format, At: now})
		contextID, fingerprint := make([]byte, 16), make([]byte, 32)
		contextID[15] = byte(index + 1)
		fingerprint[31] = byte(index + 1)
		keyContext, err := secret.NewGenerationTwoEndpointKeyContext(contextID)
		if err != nil {
			t.Fatal(err)
		}
		envelope, err := vault.SealForGenerationTwoContext([]byte("embedding-fixture-secret"), keyContext)
		if err != nil {
			t.Fatal(err)
		}
		secretID := f.exec(t, `INSERT INTO endpoint_key_secrets(context_id,canonical_base_url,connector_type,encrypted_secret,created_at) VALUES(?,?,'`+connectorType+`',?,?)`, contextID, base, envelope, now)
		key := appFixtureKey(t, f.store, testseed.KeyInput{EndpointID: endpoint, SecretRefID: secretID, Fingerprint: fingerprint, DisplayHead: "head", DisplayTail: "tail", Note: "fixture key", NativeServices: services, ForceStoreFalse: true, At: now}, vault).KeyID
		appFixtureManualModel(t, f.store, key, "private-model", now)
		if index == 0 {
			model := f.exec(t, `INSERT INTO models(user_id,provider,model,full_name,route_strategy,silent_retry,flatten_tool_calls,revision,binding_revision,model_types,created_at,updated_at) VALUES(?,'provider','self','provider/self','ordered',0,1,1,1,3,?,?)`, user, now, now)
			appFixtureBinding(t, f.store, testseed.BindingInput{ModelID: model, KeyID: key, UpstreamModelID: "private-model", NativeServices: services, At: now}, false)
			continue
		}
		f.keyID = key
		donation := f.exec(t, `INSERT INTO donations(user_id,status,revision,description,review_note,reviewed_by_role,created_at,updated_at) VALUES(?,'approved',1,'fixture donation','','admin',?,?)`, user, now, now)
		donationKey := f.exec(t, `INSERT INTO donation_keys(donation_id,endpoint_key_id,display_head,display_tail,canonical_base_url,connector_type,price_used_mag,price_reserved_mag,calls_used,calls_reserved,tokens_used,tokens_reserved,token_reserve,enabled,failure_streak,streak_generation,next_claim_seq,next_fold_seq,safe_note,created_at,updated_at,source_endpoint_key_id,report_fingerprint) VALUES(?,?,'head','tail',?,'`+connectorType+`',?,?,?,?,?,?,5,1,?,?,?,?,'fixture',?,?,?,?)`, donation, key, base, zero, zero, zero, zero, zero, zero, zero, one, one, one, now, now, key, fingerprint)
		f.exec(t, `INSERT INTO donation_key_memberships(endpoint_key_id,donation_key_id,donation_id,created_at) VALUES(?,?,?,?)`, key, donationKey, donation, now)
		appFixtureApprovedUses(t, f.store, donationKey, now)
		appFixtureDonationManualModel(t, f.store, donationKey, "private-model", now)
		for _, mode := range []string{"per_request", "per_token"} {
			model := f.exec(t, `INSERT INTO charity_models(provider,model,full_name,enabled,pricing_mode,request_user_price,request_donor_reward,uncached_user_price,uncached_donor_reward,discount_percent,discount_enabled,flatten_tool_calls,revision,binding_revision,model_types,created_at,updated_at) VALUES('provider',?,?,1,?,3000,1250,4000000,2000000,80,1,1,1,1,3,?,?)`, mode, "[公益]provider/"+mode, mode, now, now)
			f.exec(t, `INSERT INTO charity_model_access(model_id,allowed_level_mask,public_description) VALUES(?,31,'')`, model)
			f.exec(t, `INSERT INTO charity_routing_settings(model_id,revision,affinity_ttl_seconds) VALUES(?,1,300)`, model)
			appFixtureBinding(t, f.store, testseed.BindingInput{ModelID: model, KeyID: donationKey, UpstreamModelID: "private-model", NativeServices: services, At: now}, true)
		}
	}
}

func (f *embeddingHTTPFixture) call(t *testing.T, model, input, encoding, scenario string) (int, []byte) {
	t.Helper()
	body := `{"model":"` + model + `","input":` + input + `,"encoding_format":"` + encoding + `","dimensions":2,"user":"caller-provided","store":true,"scenario":"` + scenario + `"}`
	return f.post(t, "/v1/embeddings", body)
}

func (f *embeddingHTTPFixture) post(t *testing.T, path, body string) (int, []byte) {
	t.Helper()
	r, err := http.NewRequest("POST", f.server.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	r.Host = auditUserHost
	r.Header.Set("Authorization", "Bearer "+f.caller)
	r.Header.Set("Content-Type", "application/json")
	response, err := f.server.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, data
}

func TestEmbeddingHTTPAccountingAndProjection(t *testing.T) {
	f := newEmbeddingHTTPFixture(t)
	for _, model := range []string{"provider/self", "[公益]provider/per_request", "[公益]provider/per_token"} {
		for _, encoding := range []string{"float", "base64"} {
			for _, input := range []string{`" "`, `["a","b"]`, `[1,2]`, `[[1],[2]]`} {
				t.Run(model+"/"+encoding+"/"+input, func(t *testing.T) {
					status, body := f.call(t, model, input, encoding, "")
					if status != 200 {
						t.Fatalf("status=%d body=%s", status, body)
					}
					var result struct {
						Model string
						Data  []struct {
							Index     int
							Embedding json.RawMessage
						}
						Usage map[string]int
					}
					if err := json.Unmarshal(body, &result); err != nil {
						t.Fatal(err)
					}
					count := 1
					if strings.HasPrefix(input, `["`) || strings.HasPrefix(input, `[[`) {
						count = 2
					}
					if result.Model != model || len(result.Data) != count || result.Data[0].Index != count-1 || result.Usage["prompt_tokens"] != 3 {
						t.Fatalf("result=%s", body)
					}
					if strings.Contains(string(body), "private-model") || strings.Contains(string(body), "hidden-source") {
						t.Fatal("private upstream output escaped")
					}
					f.assertLatest(t, model, 3, false, true)
				})
			}
		}
		for _, scenario := range []string{"zero", "unknown", "bad-vector", "error"} {
			t.Run(model+"/"+scenario, func(t *testing.T) {
				status, body := f.call(t, model, `["a","b"]`, "float", scenario)
				want := 200
				if scenario == "bad-vector" {
					want = 502
				}
				if scenario == "error" {
					want = 429
				}
				if status != want {
					t.Fatalf("status=%d want=%d body=%s", status, want, body)
				}
				f.assertLatest(t, model, 0, scenario != "zero", want == 200)
			})
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	identity := ""
	for _, r := range f.upstreamRequests {
		var user, model string
		_ = json.Unmarshal(r["user"], &user)
		_ = json.Unmarshal(r["model"], &model)
		if !strings.HasPrefix(user, "nbu_v3_") || identity != "" && identity != user || model != "private-model" || string(r["store"]) != "true" {
			t.Fatal("upstream request authority or policy drift")
		}
		identity = user
	}
	var violations int
	if err := f.store.DB().QueryRow(`SELECT COUNT(*) FROM logical_requests WHERE caller_error_code='content_too_short'`).Scan(&violations); err != nil || violations != 0 {
		t.Fatalf("short-content penalty=%d %v", violations, err)
	}
}

func (f *embeddingHTTPFixture) assertLatest(t *testing.T, model string, tokens int64, unknown, success bool) {
	t.Helper()
	var id, route, state string
	var input, output, cacheWrite, cacheRead int64
	var usageUnknown bool
	if err := f.store.DB().QueryRow(`SELECT logical_request_id,route_kind,uncached_input_tokens,cache_write_input_tokens,cache_read_input_tokens,output_tokens,usage_unknown FROM request_logs ORDER BY id DESC LIMIT 1`).Scan(&id, &route, &input, &cacheWrite, &cacheRead, &output, &usageUnknown); err != nil {
		t.Fatal(err)
	}
	wantRoute := "openai_embeddings"
	charity := strings.HasPrefix(model, "[公益]")
	if charity {
		wantRoute = "charity_embeddings"
	}
	if route != wantRoute || input != tokens || output != 0 || cacheWrite != 0 || cacheRead != 0 || usageUnknown != unknown {
		t.Fatalf("usage route=%s tokens=%d/%d/%d/%d unknown=%t", route, input, cacheWrite, cacheRead, output, usageUnknown)
	}
	if err := f.store.DB().QueryRow(`SELECT accounting_state FROM logical_requests WHERE id=?`, id).Scan(&state); err != nil || state != "committed" {
		t.Fatalf("dispatch accounting=%s %v", state, err)
	}
	var marks int
	if err := f.store.DB().QueryRow(`SELECT COUNT(*) FROM dispatch_response_starts m JOIN dispatch_claims c ON c.id=m.claim_id WHERE c.logical_request_id=?`, id).Scan(&marks); err != nil || (marks == 1) != success {
		t.Fatalf("success marks=%d %v", marks, err)
	}
	if charity {
		wantCharge, wantReward, wantCalls := int64(0), int64(0), int64(0)
		if success {
			wantCalls = 1
			if strings.HasSuffix(model, "per_request") {
				wantCharge = 2400
				if !unknown {
					wantReward = 1250
				}
			} else if unknown {
				wantCharge = 80
			} else {
				wantCharge = (tokens*4*80 + 99) / 100
				wantReward = tokens * 2
			}
		}
		var charge, reward, calls int64
		if err := f.store.DB().QueryRow(`SELECT cr.user_charge_milli,c.donor_reward_actual_milli,u.calls_actual FROM charity_reservations cr JOIN dispatch_claims c ON c.logical_request_id=cr.logical_request_id JOIN donation_usage_reservations u ON u.claim_id=c.id WHERE cr.logical_request_id=?`, id).Scan(&charge, &reward, &calls); err != nil {
			t.Fatal(err)
		}
		if charge != wantCharge || reward != wantReward || calls != wantCalls {
			t.Fatalf("money/calls=%d/%d/%d want=%d/%d/%d", charge, reward, calls, wantCharge, wantReward, wantCalls)
		}
	}
	encoded := f.ownLogJSON(t, id)
	if charity {
		for _, forbidden := range []string{"attempts", "attempt_count", "private-model", "fixture key", "fixture endpoint", "embedding-fixture-secret"} {
			if strings.Contains(string(encoded), forbidden) {
				t.Fatalf("charity log exposed %s", forbidden)
			}
		}
	}
	tx, err := f.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := ledger.ValidateRecovery(context.Background(), tx); err != nil {
		t.Fatal(fmt.Errorf("ledger consistency: %w", err))
	}
}

func TestCharityHTTPKnownUsageCanExceedReservation(t *testing.T) {
	for _, route := range []string{"chat", "embeddings"} {
		t.Run(route, func(t *testing.T) {
			f := newEmbeddingHTTPFixture(t)
			f.exec(t, `UPDATE site_config SET value='5' WHERE key='charity_token_reserve_milli'`)
			f.exec(t, `UPDATE site_config SET value='0' WHERE key='charity_min_chars'`)
			var status int
			var body []byte
			if route == "embeddings" {
				status, body = f.call(t, "[公益]provider/per_token", `"hello"`, "float", "over-reservation")
			} else {
				status, body = f.post(t, "/v1/chat/completions", `{"model":"[公益]provider/per_token","messages":[{"role":"user","content":"hello"}],"stream":false}`)
			}
			if status != 200 {
				t.Fatalf("status=%d body=%s", status, body)
			}
			var reserved, original, charge, reward, tokens int64
			if err := f.store.DB().QueryRow(`SELECT cr.user_reserved_milli,cr.original_charge_milli,cr.user_charge_milli,c.donor_reward_actual_milli,l.uncached_input_tokens FROM charity_reservations cr JOIN dispatch_claims c ON c.logical_request_id=cr.logical_request_id JOIN request_logs l ON l.logical_request_id=cr.logical_request_id`).Scan(&reserved, &original, &charge, &reward, &tokens); err != nil {
				t.Fatal(err)
			}
			if reserved != 5 || original != 8 || charge != 7 || reward != 4 || tokens != 2 {
				t.Fatalf("reserved/original/charge/reward/tokens=%d/%d/%d/%d/%d", reserved, original, charge, reward, tokens)
			}
			tx, err := f.store.DB().BeginTx(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			for user, want := range map[int64]string{f.userID: "999993", f.donorID: "1000004"} {
				account, err := ledger.UserAccount(context.Background(), tx, user)
				if err != nil || account.Balance.Decimal() != want {
					t.Fatalf("balance=%s want=%s err=%v", account.Balance.Decimal(), want, err)
				}
			}
			if err := ledger.ValidateRecovery(context.Background(), tx); err != nil {
				t.Fatal(err)
			}
		})
	}
}
