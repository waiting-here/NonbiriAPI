package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/auth"
	"github.com/waiting-here/NonbiriAPI/internal/backend"
	"github.com/waiting-here/NonbiriAPI/internal/connector"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbfixture"
	"github.com/waiting-here/NonbiriAPI/internal/egress"
	"github.com/waiting-here/NonbiriAPI/internal/elevation"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	"github.com/waiting-here/NonbiriAPI/internal/lifecycle"
	"github.com/waiting-here/NonbiriAPI/internal/resourcebridge"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
	"github.com/waiting-here/NonbiriAPI/internal/stewardautomation"
)

type automationFixture struct {
	app                      *application
	store                    *db.Store
	handler                  http.Handler
	caller, baseURL, modelID string
	userID                   int64
	mu                       sync.Mutex
	mode                     string
	discoveries              int
	started                  chan struct{}
}

type automationCreated struct {
	EndpointID string `json:"endpoint_id"`
	DonationID string `json:"donation_id"`
	Keys       []struct {
		EndpointKeyID string `json:"endpoint_key_id"`
		DonationKeyID string `json:"donation_key_id"`
	} `json:"keys"`
}

func newAutomationFixture(t *testing.T) *automationFixture {
	t.Helper()
	f := &automationFixture{started: make(chan struct{}, 16)}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		anthropic := strings.HasPrefix(r.Header.Get("X-Api-Key"), "donated-fixture-")
		if r.Method != http.MethodGet || r.URL.Path != "/v1/models" || !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer donated-fixture-") && !anthropic {
			w.WriteHeader(400)
			return
		}
		f.mu.Lock()
		mode := f.mode
		f.discoveries++
		f.mu.Unlock()
		select {
		case f.started <- struct{}{}:
		default:
		}
		w.Header().Set("Content-Type", "application/json")
		if anthropic && mode == "" {
			_, _ = w.Write([]byte(`{"data":[{"id":"exact/model-id","display_name":"catalog note","created_at":"2025-01-01T00:00:00Z","type":"model"}],"has_more":false,"first_id":"exact/model-id","last_id":"exact/model-id"}`))
			return
		}
		switch mode {
		case "demote":
			if _, err := f.store.DB().Exec(`UPDATE users SET level=4 WHERE id=?`, f.userID); err != nil {
				w.WriteHeader(500)
				return
			}
			_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"exact/model-id","object":"model","owned_by":"catalog-note"}]}`))
			return
		case "block":
			<-r.Context().Done()
			return
		case "fail":
			w.WriteHeader(401)
			_, _ = w.Write([]byte(`{"error":{"message":"private upstream diagnostic donated-fixture-secret"}}`))
			return
		case "empty":
			_, _ = w.Write([]byte(`{"object":"list","data":[]}`))
			return
		default:
			_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"exact/model-id","object":"model","owned_by":"catalog-note"}]}`))
		}
	}))
	t.Cleanup(upstream.Close)
	f.baseURL = upstream.URL + "/v1"
	vault, err := secret.New(bytes.Repeat([]byte{0x53}, secret.MasterKeyBytes))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = vault.Close() })
	path := filepath.Join(t.TempDir(), "automation.sqlite")
	dbfixture.Materialize(t, path)
	f.store, err = db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.store.Close() })
	f.app, err = buildApplication(auditConfig(), f.store, vault)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.app.Close() })
	login := testApplicationRequest(t, f.app.handler, "POST", auditAdminHost, "/admin/api/login", `{"username":"operator","password":"correct horse battery staple"}`, nil, map[string]string{"Content-Type": "application/json"})
	if login.Code != 200 {
		t.Fatalf("login: %d %s", login.Code, login.Body.String())
	}
	cookie := responseCookieNamed(t, login, auth.AdminSessionCookieName)
	opened := testApplicationRequest(t, f.app.handler, "POST", auditAdminHost, "/admin/api/maintenance/disable", `{"expected_revision":"1","reason":"integration fixture"}`, []*http.Cookie{cookie}, map[string]string{"Origin": "http://" + auditAdminHost, "Idempotency-Key": strings.Repeat("O", 22)})
	if opened.Code != 200 {
		t.Fatalf("open: %d %s", opened.Code, opened.Body.String())
	}
	f.caller = seedRootCallerIdentity(t, f.store)
	if err := f.store.DB().QueryRow(`SELECT user_id FROM caller_keys WHERE generation=1`).Scan(&f.userID); err != nil {
		t.Fatal(err)
	}
	f.exec(t, `UPDATE users SET level=6 WHERE id=?`, f.userID)
	f.exec(t, `UPDATE site_config SET value='1' WHERE key IN ('donation_accept_enabled','charity_enabled')`)
	f.exec(t, `UPDATE site_config SET value='1000' WHERE key IN ('default_endpoint_limit','default_endpoint_key_limit')`)
	tx, err := f.store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.CreateUserAccount(context.Background(), tx, f.userID, time.Now().Unix()); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if _, err := ledger.CreateUserAssetAccount(context.Background(), tx, f.userID, ledger.Game, time.Now().Unix()); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	f.modelID = strconv.FormatInt(f.exec(t, `INSERT INTO charity_models(provider,model,full_name,enabled,pricing_mode,revision,binding_revision,created_at,updated_at) VALUES('fixture','automation','[公益]fixture/automation',1,'per_request',1,1,?,?)`, time.Now().Unix(), time.Now().Unix()), 10)
	f.exec(t, `INSERT INTO charity_model_access(model_id,allowed_level_mask,public_description) VALUES(?,63,'')`, f.modelID)
	stack, err := egress.NewStack(egress.StackOptions{AllowedOrigins: []string{upstream.URL}, RequestTimeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stack.CloseIdleConnections)
	if err := stack.AddSelfOrigins(context.Background(), auditConfig()); err != nil {
		t.Fatal(err)
	}
	local, err := backend.NewLocal(stack)
	if err != nil {
		t.Fatal(err)
	}
	bridge, err := resourcebridge.New(resourcebridge.Config{Store: f.store, Vault: vault, Claims: f.app.claims, Backend: local})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = bridge.Close() })
	roles := &roleFinalTxAuthorizer{authorizer: f.app.authorizer}
	repository, err := resources.New(resources.Config{Store: f.store, Connectors: connector.NewDefaultRegistry(), BaseURLs: stack, Secrets: bridge, KeyDeletion: f.app.charity, KeyCreation: f.app.reports, Projection: f.app.issues.Sources(), DiscoveryRail: bridge, DiscoveryWorker: f.app.discoveryWorker, CursorKeys: vault, FinalAuth: f.app.authRuntime, AdminFinalAuth: roles})
	if err != nil {
		t.Fatal(err)
	}
	service, err := stewardautomation.New(stewardautomation.Config{Database: f.store.DB(), Authorizer: f.app.authorizer, Resources: repository, Donations: f.app.donations, Charity: f.app.charityRouting})
	if err != nil {
		t.Fatal(err)
	}
	automation, err := newStewardAutomationHandler(service, repository, f.app.forward.lifecycle, f.app.gate)
	if err != nil {
		t.Fatal(err)
	}
	mux, err := generationTwoMux(auditConfig(), f.store, f.app.authRuntime, f.app.forward.handler, automation)
	if err != nil {
		t.Fatal(err)
	}
	f.handler, err = stationBoundary(auditConfig(), mux)
	if err != nil {
		t.Fatal(err)
	}
	// Stop shared workers before closing the fixture's discovery bridge.
	t.Cleanup(func() { _ = f.app.Close() })
	return f
}

func (f *automationFixture) exec(t *testing.T, query string, args ...any) int64 {
	t.Helper()
	result, err := f.store.DB().Exec(query, args...)
	if err != nil {
		t.Fatal(err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func (f *automationFixture) count(t *testing.T, table string) int {
	t.Helper()
	var n int
	if err := f.store.DB().QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func (f *automationFixture) input(count int) map[string]any {
	keys := make([]map[string]any, count)
	for i := range keys {
		keys[i] = map[string]any{"secret": fmt.Sprintf("donated-fixture-%d", i)}
	}
	return map[string]any{"endpoint": map[string]any{"connector_type": "openai-compatible", "base_url": f.baseURL}, "description": "fixture donation", "discord_public_thanks": false, "keys": keys}
}

func (f *automationFixture) call(t *testing.T, path, key string, input any) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	return testApplicationRequest(t, f.handler, "POST", auditUserHost, path, string(body), nil, map[string]string{"Content-Type": "application/json", "Authorization": "Bearer " + f.caller, "Idempotency-Key": key})
}

func (f *automationFixture) create(t *testing.T, key string, input any) automationCreated {
	t.Helper()
	response := f.call(t, stewardautomation.DonationsPath, key, input)
	if response.Code != 201 {
		t.Fatalf("create: %d %s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "fixture-secret") || strings.Contains(response.Body.String(), "display_head") {
		t.Fatal("creation leaked credential material")
	}
	var out automationCreated
	if err := json.Unmarshal(response.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func bindingBody(model string, manual bool, created automationCreated) map[string]any {
	ids := make([]string, len(created.Keys))
	for i, key := range created.Keys {
		ids[i] = key.DonationKeyID
	}
	return map[string]any{"charity_model_id": model, "donation_key_ids": ids, "upstream_model_id": "exact/model-id", "manual": manual}
}

func TestStewardAutomationAtomicCreationAndManualBindings(t *testing.T) {
	f := newAutomationFixture(t)
	input := f.input(2)
	keys := input["keys"].([]map[string]any)
	expires := time.Now().Unix() + 86400
	keys[0]["note"] = "private key note"
	keys[0]["safe_note"] = "management note"
	keys[0]["max_concurrency"], keys[0]["max_rpm"] = 3, 7
	keys[0]["force_store_false"] = true
	keys[0]["authorized_expires_at"], keys[0]["expires_at"] = expires, expires-100
	keys[0]["price_limit"], keys[0]["calls_limit"], keys[0]["tokens_limit"] = "10", "30", "900"
	keys[0]["token_reserve"] = 42
	keys[0]["recurring_limits"] = []map[string]any{{"mode": "reset", "interval": "1h", "alignment": "first_success", "time_zone": "Asia/Shanghai", "metric": "tokens", "limit": "300"}}
	input["review_note"] = "approved by fixture steward"
	input["discord_public_thanks"] = true
	idem := strings.Repeat("A", 22)
	created := f.create(t, idem, input)
	if len(created.Keys) != 2 || f.count(t, "donations") != 1 || f.count(t, "donation_reviews") != 1 || f.count(t, "donation_quota_rules") != 1 {
		t.Fatal("bundle did not create one approved donation")
	}
	var owner, reviewer int64
	var role, status string
	var thanks bool
	if err := f.store.DB().QueryRow(`SELECT user_id,reviewed_by_user_id,reviewed_by_role,status,discord_public_thanks FROM donations WHERE id=?`, created.DonationID).Scan(&owner, &reviewer, &role, &status, &thanks); err != nil {
		t.Fatal(err)
	}
	if owner != f.userID || reviewer != f.userID || role != "level6" || status != "approved" {
		t.Fatalf("audit actor = %d/%d/%s/%s", owner, reviewer, role, status)
	}
	if !thanks {
		t.Fatal("explicit public-thanks consent was not retained")
	}
	var note, safeNote string
	var reserve, effective, authorized, concurrency, rpm int64
	if err := f.store.DB().QueryRow(`SELECT k.note,dk.safe_note,dk.token_reserve,dk.expires_at,dk.authorized_expires_at,l.max_concurrency,l.max_rpm FROM donation_keys dk JOIN endpoint_keys k ON k.id=dk.endpoint_key_id JOIN endpoint_key_limits l ON l.endpoint_key_id=k.id WHERE dk.id=?`, created.Keys[0].DonationKeyID).Scan(&note, &safeNote, &reserve, &effective, &authorized, &concurrency, &rpm); err != nil {
		t.Fatal(err)
	}
	if note != "private key note" || safeNote != "management note" || reserve != 42 || authorized != expires || effective != expires-100 || concurrency != 3 || rpm != 7 {
		t.Fatal("optional settings were not retained")
	}
	replay := f.create(t, idem, input)
	if replay.DonationID != created.DonationID || f.count(t, "endpoints") != 1 || f.count(t, "endpoint_key_secrets") != 2 {
		t.Fatal("replay duplicated resources")
	}
	input["description"] = "changed"
	if response := f.call(t, stewardautomation.DonationsPath, idem, input); response.Code != 409 {
		t.Fatalf("changed replay: %d %s", response.Code, response.Body.String())
	}
	bound := f.call(t, stewardautomation.BindingsPath, "", bindingBody(f.modelID, true, created))
	if bound.Code != 200 || f.count(t, "charity_model_bindings") != 2 {
		t.Fatalf("manual bind: %d %s", bound.Code, bound.Body.String())
	}
	f.exec(t, `UPDATE model_catalog_entries SET provider='preserved note' WHERE endpoint_key_id=? AND source_type='manual'`, created.Keys[0].EndpointKeyID)
	var revision int64
	if err := f.store.DB().QueryRow(`SELECT binding_revision FROM charity_models WHERE id=?`, f.modelID).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	repeated := f.call(t, stewardautomation.BindingsPath, "", bindingBody(f.modelID, true, created))
	if repeated.Code != 200 {
		t.Fatalf("repeat bind: %d %s", repeated.Code, repeated.Body.String())
	}
	var after int64
	var provider string
	if err := f.store.DB().QueryRow(`SELECT binding_revision FROM charity_models WHERE id=?`, f.modelID).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if err := f.store.DB().QueryRow(`SELECT provider FROM model_catalog_entries WHERE endpoint_key_id=? AND source_type='manual'`, created.Keys[0].EndpointKeyID).Scan(&provider); err != nil {
		t.Fatal(err)
	}
	if after != revision || provider != "preserved note" {
		t.Fatal("repeat changed binding order/revision or catalog note")
	}
	for i, key := range created.Keys {
		var ord int
		if err := f.store.DB().QueryRow(`SELECT ord FROM charity_model_bindings WHERE donation_key_id=?`, key.DonationKeyID).Scan(&ord); err != nil || ord != i {
			t.Fatalf("binding input order %d: %d %v", i, ord, err)
		}
	}
}

func TestStewardAutomationRollsBackLateFailures(t *testing.T) {
	f := newAutomationFixture(t)
	for _, table := range []string{"donation_reviews", "donation_quota_rules"} {
		t.Run(table, func(t *testing.T) {
			f.exec(t, `CREATE TRIGGER automation_failure BEFORE INSERT ON `+table+` BEGIN SELECT RAISE(ABORT,'private donated-fixture-secret'); END`)
			input := f.input(2)
			input["keys"].([]map[string]any)[1]["recurring_limits"] = []map[string]any{{"mode": "sliding", "interval": "day", "time_zone": "UTC", "metric": "calls", "limit": "3"}}
			response := f.call(t, stewardautomation.DonationsPath, strings.Repeat("F", 22), input)
			if response.Code < 400 || strings.Contains(response.Body.String(), "donated-fixture-secret") {
				t.Fatalf("unsafe failure: %d %s", response.Code, response.Body.String())
			}
			for _, name := range []string{"endpoints", "endpoint_keys", "endpoint_key_secrets", "model_discovery_evidence", "donations", "donation_keys", "donation_key_memberships", "donation_reviews", "donation_quota_rules", "donation_quota_epochs"} {
				if f.count(t, name) != 0 {
					t.Fatalf("rollback retained %s", name)
				}
			}
			f.exec(t, `DROP TRIGGER automation_failure`)
		})
	}
	created := f.create(t, strings.Repeat("F", 22), f.input(2))
	if len(created.Keys) != 2 {
		t.Fatal("retry after rollback failed")
	}
}

func TestStewardAutomationFreshDiscoveryCannotUseOldOrManualCatalog(t *testing.T) {
	f := newAutomationFixture(t)
	created := f.create(t, strings.Repeat("D", 22), f.input(2))
	for _, manual := range []bool{true, false} {
		response := f.call(t, stewardautomation.BindingsPath, "", bindingBody(f.modelID, manual, created))
		if response.Code != 200 {
			t.Fatalf("binding: %d %s", response.Code, response.Body.String())
		}
	}
	for _, test := range []struct{ mode, code string }{{"empty", "model_not_found"}, {"fail", "discovery_failed"}} {
		f.mu.Lock()
		f.mode = test.mode
		before := f.discoveries
		f.mu.Unlock()
		response := f.call(t, stewardautomation.BindingsPath, "", bindingBody(f.modelID, false, created))
		if response.Code != 422 || strings.Count(response.Body.String(), test.code) != 2 || strings.Contains(response.Body.String(), "private upstream") {
			t.Fatalf("fresh discovery failure: %d %s", response.Code, response.Body.String())
		}
		f.mu.Lock()
		after := f.discoveries
		f.mu.Unlock()
		if after-before != 2 || f.count(t, "charity_model_bindings") != 2 {
			t.Fatal("did not discover each key or destroyed prior bindings")
		}
	}
}

func TestStewardAutomationCancellationStopsDiscoveryAndLateBinding(t *testing.T) {
	f := newAutomationFixture(t)
	created := f.create(t, strings.Repeat("C", 22), f.input(2))
	f.mu.Lock()
	f.mode = "block"
	f.mu.Unlock()
	body, _ := json.Marshal(bindingBody(f.modelID, false, created))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := httptest.NewRequest("POST", "http://wire.invalid"+stewardautomation.BindingsPath, bytes.NewReader(body)).WithContext(ctx)
	r.Host = auditUserHost
	r.RemoteAddr = "198.51.100.20:4242"
	r.Header.Set("Authorization", "Bearer "+f.caller)
	done := make(chan struct{})
	go func() { f.handler.ServeHTTP(&scopeRecorder{httptest.NewRecorder()}, r); close(done) }()
	select {
	case <-f.started:
	case <-time.After(3 * time.Second):
		t.Fatal("discovery did not start")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("request did not cancel")
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		var n int
		if err := f.store.DB().QueryRow(`SELECT count(*) FROM model_discovery_evidence WHERE state='checking'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("discovery did not finish cleanup")
		}
		time.Sleep(time.Millisecond)
	}
	f.mu.Lock()
	count := f.discoveries
	f.mu.Unlock()
	if count != 1 || f.count(t, "charity_model_bindings") != 0 {
		t.Fatal("cancellation dispatched a later key or bound a late result")
	}
}

func TestStewardAutomationBoundsDefaultsAndEntryAuthority(t *testing.T) {
	f := newAutomationFixture(t)
	for _, test := range []struct {
		name string
		edit func(map[string]any)
	}{
		{"no connector", func(in map[string]any) { delete(in["endpoint"].(map[string]any), "connector_type") }},
		{"null enabled", func(in map[string]any) { in["endpoint"].(map[string]any)["enabled"] = nil }},
		{"channel field", func(in map[string]any) { in["endpoint"].(map[string]any)["channel_id"] = "channel" }},
		{"ownership field", func(in map[string]any) { in["ownership_authorized"] = true }},
		{"missing thanks", func(in map[string]any) { delete(in, "discord_public_thanks") }},
		{"null thanks", func(in map[string]any) { in["discord_public_thanks"] = nil }},
		{"duplicate secrets", func(in map[string]any) {
			in["keys"].([]map[string]any)[1]["secret"] = in["keys"].([]map[string]any)[0]["secret"]
		}},
		{"too many keys", func(in map[string]any) { in["keys"] = f.input(101)["keys"] }},
		{"bad limit", func(in map[string]any) { in["keys"].([]map[string]any)[1]["calls_limit"] = "-1" }},
		{"expiry expansion", func(in map[string]any) {
			k := in["keys"].([]map[string]any)[1]
			k["authorized_expires_at"] = time.Now().Unix() + 3600
			k["expires_at"] = nil
		}},
		{"private URL", func(in map[string]any) { in["endpoint"].(map[string]any)["base_url"] = "http://127.0.0.3:9999/v1" }},
		{"foreign connector", func(in map[string]any) { in["endpoint"].(map[string]any)["connector_type"] = "unknown" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := f.input(2)
			test.edit(input)
			response := f.call(t, stewardautomation.DonationsPath, strings.Repeat("N", 22), input)
			if response.Code != 400 {
				t.Fatalf("invalid input: %d %s", response.Code, response.Body.String())
			}
			if f.count(t, "endpoints") != 0 {
				t.Fatal("invalid bundle left resources")
			}
		})
	}
	for _, test := range []struct {
		path, method, host, authorization, body string
		want                                    int
	}{
		{stewardautomation.DonationsPath, "POST", auditUserHost, "", `{}`, 401},
		{stewardautomation.DonationsPath, "GET", auditUserHost, f.caller, `{}`, 405},
		{stewardautomation.DonationsPath, "POST", auditAdminHost, f.caller, `{}`, 404},
		{"/api/steward/automation/%64onations", "POST", auditUserHost, f.caller, `{}`, 404},
		{stewardautomation.DonationsPath + "?x=1", "POST", auditUserHost, f.caller, `{}`, 400},
		{stewardautomation.DonationsPath, "POST", auditUserHost, f.caller, `{"keys":[],"keys":[]}`, 400},
		{stewardautomation.DonationsPath, "POST", auditUserHost, f.caller, strings.Repeat(" ", 256*1024+1), 413},
		{"/api/endpoints", "POST", auditUserHost, f.caller, `{}`, 401},
	} {
		response := testApplicationRequest(t, f.handler, test.method, test.host, test.path, test.body, []*http.Cookie{{Name: auth.UserSessionCookieName, Value: "irrelevant-cookie"}}, map[string]string{"Authorization": "Bearer " + test.authorization, "Idempotency-Key": strings.Repeat("N", 22), "Origin": "http://" + auditUserHost})
		if response.Code != test.want {
			t.Fatalf("%s %s: %d %s", test.method, test.path, response.Code, response.Body.String())
		}
	}
	f.exec(t, `UPDATE users SET level=4 WHERE id=?`, f.userID)
	if response := f.call(t, stewardautomation.DonationsPath, strings.Repeat("N", 22), f.input(1)); response.Code != 403 {
		t.Fatalf("non-steward: %d %s", response.Code, response.Body.String())
	}
	f.exec(t, `UPDATE users SET level=6,is_banned=1,banned_until=NULL WHERE id=?`, f.userID)
	if response := f.call(t, stewardautomation.DonationsPath, strings.Repeat("N", 22), f.input(1)); response.Code != 401 {
		t.Fatalf("banned: %d %s", response.Code, response.Body.String())
	}
	f.exec(t, `UPDATE users SET is_banned=0 WHERE id=?`, f.userID)
	input := f.input(100)
	for _, key := range input["keys"].([]map[string]any) {
		key["note"] = "batch fixture"
		key["max_rpm"] = 0
	}
	created := f.create(t, strings.Repeat("B", 22), input)
	if len(created.Keys) != 100 {
		t.Fatal("100-key batch rejected")
	}
	var enabled, physical, policy int
	var expires sql.NullInt64
	if err := f.store.DB().QueryRow(`SELECT dk.enabled,k.enabled,k.force_store_false,dk.expires_at FROM donation_keys dk JOIN endpoint_keys k ON k.id=dk.endpoint_key_id WHERE dk.id=?`, created.Keys[0].DonationKeyID).Scan(&enabled, &physical, &policy, &expires); err != nil {
		t.Fatal(err)
	}
	if enabled != 1 || physical != 1 || policy != 0 || expires.Valid {
		t.Fatal("defaults differ from contract")
	}
	var thanks bool
	if err := f.store.DB().QueryRow(`SELECT discord_public_thanks FROM donations WHERE id=?`, created.DonationID).Scan(&thanks); err != nil || thanks {
		t.Fatalf("explicit public-thanks refusal was not retained: %t %v", thanks, err)
	}
	login := testApplicationRequest(t, f.handler, http.MethodPost, auditAdminHost, "/admin/api/login", `{"username":"operator","password":"correct horse battery staple"}`, nil, map[string]string{"Content-Type": "application/json"})
	cookie := responseCookieNamed(t, login, auth.AdminSessionCookieName)
	closed := testApplicationRequest(t, f.handler, http.MethodPost, auditAdminHost, "/admin/api/maintenance/enable", `{"expected_revision":"2","reason":"integration fixture","confirmation":true}`, []*http.Cookie{cookie}, map[string]string{"Origin": "http://" + auditAdminHost, "Idempotency-Key": strings.Repeat("M", 22)})
	if closed.Code != 200 {
		t.Fatalf("maintenance: %d %s", closed.Code, closed.Body.String())
	}
	for _, path := range []string{stewardautomation.DonationsPath, stewardautomation.BindingsPath} {
		if response := f.call(t, path, strings.Repeat("M", 22), f.input(1)); response.Code != 503 {
			t.Fatalf("maintenance admission %s: %d %s", path, response.Code, response.Body.String())
		}
	}
}

func TestStewardAutomationResourcesFollowExportAndAccountDeletion(t *testing.T) {
	f := newAutomationFixture(t)
	input := f.input(2)
	input["keys"].([]map[string]any)[0]["recurring_limits"] = []map[string]any{{"mode": "reset", "interval": "1h", "alignment": "first_success", "time_zone": "Asia/Shanghai", "metric": "tokens", "limit": "300"}}
	created := f.create(t, strings.Repeat("L", 22), input)
	if response := f.call(t, stewardautomation.BindingsPath, "", bindingBody(f.modelID, true, created)); response.Code != 200 {
		t.Fatalf("bind before export: %d %s", response.Code, response.Body.String())
	}
	models := testApplicationRequest(t, f.handler, http.MethodGet, auditUserHost, "/v1/models", "", nil, map[string]string{"Authorization": "Bearer " + f.caller})
	if models.Code != 200 || !strings.Contains(models.Body.String(), "[公益]fixture/automation") {
		t.Fatalf("created bindings unavailable: %d %s", models.Code, models.Body.String())
	}
	// Seed only the external login result; the normal session, elevation,
	// export and deletion handlers remain in the production path.
	rawSession := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x72}, 32))
	digest := sha256.Sum256([]byte(rawSession))
	binding := fmt.Sprintf("%x", digest)
	now := time.Now().Unix()
	f.exec(t, `INSERT INTO sessions(token_hash,user_id,last_seen_at,expires_at,absolute_expires_at,created_at,cred_gen) VALUES(?,?,?,?,?,?,?)`, binding, f.userID, now, now+3600, now+7200, now, "automation-fixture-session")
	cookie := &http.Cookie{Name: auth.UserSessionCookieName, Value: rawSession}
	lifecycleCall := func(path, body string) *httptest.ResponseRecorder {
		t.Helper()
		token, _, err := f.app.authRuntime.ElevationManager().IssueBound(f.userID, elevation.KindUser, binding)
		if err != nil {
			t.Fatal(err)
		}
		return testApplicationRequest(t, f.handler, http.MethodPost, auditUserHost, path, body, []*http.Cookie{cookie}, map[string]string{"Origin": "http://" + auditUserHost, "Content-Type": "application/json", "X-Elevated-Token": token})
	}
	exported := lifecycleCall("/api/account/export", "")
	if exported.Code != 200 {
		t.Fatalf("export: %d %s", exported.Code, exported.Body.String())
	}
	var document lifecycle.ExportDocument
	if err := json.Unmarshal(exported.Body.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	if document.SchemaVersion != 10 || len(document.Endpoints) != 1 || len(document.Endpoints[0].Keys) != 2 ||
		len(document.Donations) != 1 || document.Donations[0].ID != created.DonationID || document.Donations[0].Status != "approved" ||
		len(document.Donations[0].Keys) != 2 || len(document.Donations[0].Keys[0].RecurringLimits) != 1 || len(document.CatalogPairs) != 2 {
		t.Fatal("automation resources are missing from the existing export")
	}
	for _, key := range input["keys"].([]map[string]any) {
		if strings.Contains(exported.Body.String(), key["secret"].(string)) {
			t.Fatal("export leaked the complete upstream key")
		}
	}
	deleted := lifecycleCall("/api/account/delete", `{"confirm":"DELETE"}`)
	if deleted.Code != 204 {
		t.Fatalf("delete: %d %s", deleted.Code, deleted.Body.String())
	}
	for _, table := range []string{"endpoints", "endpoint_keys", "model_catalog_entries", "model_discovery_evidence", "model_pair_catalog", "donation_key_memberships", "charity_model_bindings"} {
		if f.count(t, table) != 0 {
			t.Fatalf("account deletion retained private or active resources in %s", table)
		}
	}
	var orphaned int
	if err := f.store.DB().QueryRow(`SELECT count(*) FROM endpoint_key_secrets WHERE orphaned_at IS NOT NULL`).Scan(&orphaned); err != nil || orphaned != 2 {
		t.Fatalf("removed keys were not marked for secret cleanup: %d %v", orphaned, err)
	}
	// The established secret lifecycle retains unreferenced ciphertext for
	// one hour, then removes it through bounded maintenance.
	if _, err := f.app.claims.MaintainOrphanSecretsAt(context.Background(), time.Now().Unix()+3601, 100, time.Now().Add(5*time.Second)); err != nil {
		t.Fatal(err)
	}
	if f.count(t, "endpoint_key_secrets") != 0 {
		t.Fatal("secret cleanup retained expired orphan ciphertext")
	}
	var retained int
	if err := f.store.DB().QueryRow(`SELECT count(*) FROM donations WHERE id=? AND user_id IS NULL AND reviewed_by_user_id IS NULL AND status='deleted' AND description='' AND review_note=''`, created.DonationID).Scan(&retained); err != nil || retained != 1 {
		t.Fatalf("donation was not deidentified: %d %v", retained, err)
	}
	if response := f.call(t, stewardautomation.DonationsPath, strings.Repeat("L", 22), input); response.Code != 401 {
		t.Fatalf("deleted caller replay: %d %s", response.Code, response.Body.String())
	}
}

func TestStewardAutomationOwnKeysOnlyAndLiveDemotion(t *testing.T) {
	f := newAutomationFixture(t)
	own := f.create(t, strings.Repeat("I", 22), f.input(2))
	zero := make([]byte, 16)
	foreignID := f.exec(t, `INSERT INTO users(discord_id,username,level,donation_credit_mag,total_requests,total_uncached_input_tokens,total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,total_unknown_usage_requests,revision,created_at,updated_at) VALUES('foreign-fixture','foreign fixture',6,?,?,?,?,?,?,?,?,?,?)`, zero, zero, zero, zero, zero, zero, zero, zero, time.Now().Unix(), time.Now().Unix())
	keyBody := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x62}, 32))
	foreignCaller := "nbk_" + keyBody
	hash := sha256.Sum256([]byte(foreignCaller))
	f.exec(t, `INSERT INTO caller_keys(user_id,generation,key_hash,display_head,display_tail,key_created_at,updated_at) VALUES(?,1,?,?,?,1,1)`, foreignID, hash[:], keyBody[:4], keyBody[len(keyBody)-4:])
	caller := f.caller
	f.caller = foreignCaller
	foreign := f.create(t, strings.Repeat("J", 22), f.input(1))
	f.caller = caller
	mixed := map[string]any{"charity_model_id": f.modelID, "donation_key_ids": []string{own.Keys[0].DonationKeyID, foreign.Keys[0].DonationKeyID, "999999", own.Keys[1].DonationKeyID}, "upstream_model_id": "exact/model-id", "manual": true}
	response := f.call(t, stewardautomation.BindingsPath, "", mixed)
	if response.Code != 200 || strings.Count(response.Body.String(), `"status":"success"`) != 2 || strings.Count(response.Body.String(), `"code":"not_found"`) != 2 {
		t.Fatalf("partial ownership: %d %s", response.Code, response.Body.String())
	}
	var count int
	if err := f.store.DB().QueryRow(`SELECT count(*) FROM model_catalog_entries WHERE endpoint_key_id=?`, foreign.Keys[0].EndpointKeyID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("foreign catalog changed: %d %v", count, err)
	}
	f.mu.Lock()
	f.mode = "demote"
	before := f.discoveries
	f.mu.Unlock()
	response = f.call(t, stewardautomation.BindingsPath, "", bindingBody(f.modelID, false, own))
	if response.Code != 422 || strings.Count(response.Body.String(), `"code":"forbidden"`) != 2 {
		t.Fatalf("demoted during discovery: %d %s", response.Code, response.Body.String())
	}
	f.mu.Lock()
	after := f.discoveries
	f.mu.Unlock()
	if after-before != 1 {
		t.Fatal("demotion did not stop later upstream requests")
	}
}

func TestStewardAutomationAnthropicEndpointAndDiscovery(t *testing.T) {
	f := newAutomationFixture(t)
	input := f.input(1)
	input["endpoint"].(map[string]any)["connector_type"] = "anthropic-compatible"
	created := f.create(t, strings.Repeat("H", 22), input)
	response := f.call(t, stewardautomation.BindingsPath, "", bindingBody(f.modelID, false, created))
	if response.Code != 200 || f.count(t, "charity_model_bindings") != 1 {
		t.Fatalf("Anthropic discovery: %d %s", response.Code, response.Body.String())
	}
	input["keys"].([]map[string]any)[0]["force_store_false"] = true
	response = f.call(t, stewardautomation.DonationsPath, strings.Repeat("L", 22), input)
	if response.Code != 400 || f.count(t, "endpoints") != 1 {
		t.Fatalf("incompatible storage policy: %d %s", response.Code, response.Body.String())
	}
}
