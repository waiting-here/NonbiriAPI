package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/auth"
	"github.com/waiting-here/NonbiriAPI/internal/config"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbfixture"
	"github.com/waiting-here/NonbiriAPI/internal/egress"
	"github.com/waiting-here/NonbiriAPI/internal/ledger"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

const imageFixtureSecret = "synthetic-image-credential"
const imageFixtureModel = "synthetic-private-image-model"

type imageBrowserUser struct {
	ID     string       `json:"id"`
	Level  int          `json:"level"`
	Cookie *http.Cookie `json:"cookie"`
}
type imageBrowserFixture struct {
	t             *testing.T
	cfg           *config.Config
	vault         *secret.Vault
	store         *db.Store
	path          string
	app           atomic.Pointer[application]
	offset        atomic.Int64
	sequence      atomic.Int64
	mu            sync.Mutex
	upstream      *httptest.Server
	upstreamState *imageFixtureUpstream
	adminCookie   *http.Cookie
	users         []imageBrowserUser
}

func (f *imageBrowserFixture) now() time.Time {
	return time.Now().Add(time.Duration(f.offset.Load()) * time.Second)
}
func (f *imageBrowserFixture) open() error {
	store, err := db.Open(f.path, f.vault)
	if err != nil {
		return err
	}
	stack, err := egress.NewStack(egress.StackOptions{AllowedOrigins: []string{f.upstream.URL}})
	if err != nil {
		_ = store.Close()
		return err
	}
	app, err := buildApplicationWithRuntimeOptions(f.cfg, store, f.vault,
		applicationRuntimeOptions{Egress: stack, ActivityNow: f.now})
	if err != nil {
		_ = store.Close()
		return err
	}
	f.store = store
	f.app.Store(app)
	return nil
}
func (f *imageBrowserFixture) closeApplication() error {
	app := f.app.Swap(nil)
	if app != nil {
		if err := app.Close(); err != nil {
			return err
		}
	}
	if f.store != nil {
		return f.store.Close()
	}
	return nil
}

// This opt-in fixture serves the actual application, database, sessions and
// embedded assets. Its control server and synthetic upstream exist only in tests.
func TestImageBrowserFixture(t *testing.T) {
	statePath := os.Getenv("NONBIRI_IMAGE_BROWSER_STATE")
	if statePath == "" {
		t.Skip("browser fixture is opt-in")
	}
	vault, err := secret.New(bytes.Repeat([]byte{0x37}, secret.MasterKeyBytes))
	if err != nil {
		t.Fatal(err)
	}
	defer vault.Close()
	f := &imageBrowserFixture{t: t, vault: vault, cfg: auditConfig(),
		path: filepath.Join(t.TempDir(), "picture-book.db")}
	dbfixture.Materialize(t, f.path)
	f.upstreamState = newImageFixtureUpstream(t)
	f.upstream = httptest.NewServer(f.upstreamState)
	defer f.upstream.Close()
	proxy := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		app := f.app.Load()
		if app == nil {
			http.Error(w, "restarting", http.StatusServiceUnavailable)
			return
		}
		app.handler.ServeHTTP(w, r)
	})
	users := httptest.NewUnstartedServer(proxy)
	admins := httptest.NewUnstartedServer(proxy)
	f.cfg.UserHost = users.Listener.Addr().String()
	_, adminPort, err := net.SplitHostPort(admins.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	f.cfg.AdminHost = net.JoinHostPort("localhost", adminPort)
	f.cfg.ListenAddr, f.cfg.SiteBaseURL = f.cfg.UserHost, "http://"+f.cfg.UserHost
	f.cfg.DBPath = f.path
	if err := f.open(); err != nil {
		t.Fatal(err)
	}
	users.Start()
	admins.Start()
	admins.URL = "http://" + f.cfg.AdminHost
	defer func() {
		if err := f.closeApplication(); err != nil {
			t.Error(err)
		}
		users.Close()
		admins.Close()
	}()
	f.initialize()
	stop := make(chan struct{})
	var stopOnce sync.Once
	controlToken := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x65}, 32))
	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+controlToken {
			http.Error(w, "unauthorized", 401)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method", 405)
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		var input struct {
			Seconds int64 `json:"seconds"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1024)).Decode(&input); err != nil {
			http.Error(w, "invalid control body", 400)
			return
		}
		switch r.URL.Path {
		case "/advance":
			if input.Seconds < 1 || input.Seconds > 3600 {
				http.Error(w, "range", 400)
				return
			}
			f.offset.Add(input.Seconds)
		case "/release":
			f.upstreamState.release()
		case "/restart":
			if err := f.closeApplication(); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			if err := f.open(); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
		case "/stats":
			f.upstreamState.mu.Lock()
			defer f.upstreamState.mu.Unlock()
			writeImageFixtureJSON(w, map[string]any{"submissions": f.upstreamState.submissions,
				"polls": f.upstreamState.polls})
			return
		case "/shutdown":
			stopOnce.Do(func() { close(stop) })
		default:
			http.NotFound(w, r)
			return
		}
		writeImageFixtureJSON(w, map[string]bool{"ok": true})
	}))
	defer control.Close()
	state := map[string]any{
		"user_url": users.URL, "admin_url": admins.URL, "control_url": control.URL,
		"control_token": controlToken, "users": f.users, "admin_cookie": f.adminCookie,
		"private_markers": []string{imageFixtureSecret, imageFixtureModel, "synthetic-private-metadata", "synthetic-job-"},
	}
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(statePath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-stop:
	case <-time.After(8 * time.Minute):
		t.Fatal("browser fixture was not shut down")
	}
}

func (f *imageBrowserFixture) request(method, path string, body any, cookie *http.Cookie, admin bool) *httptest.ResponseRecorder {
	f.t.Helper()
	host := f.cfg.UserHost
	if admin {
		host = f.cfg.AdminHost
	}
	cookies := []*http.Cookie{}
	if cookie != nil {
		cookies = append(cookies, cookie)
	}
	return testApplicationRequest(f.t, f.app.Load().handler, method, host, path,
		wireBody(f.t, body), cookies, map[string]string{
			"Content-Type": "application/json", "Origin": "http://" + host,
			"Idempotency-Key": fmt.Sprintf("%022d", f.sequence.Add(1))})
}
func (f *imageBrowserFixture) call(method, path string, body any, cookie *http.Cookie, admin bool) map[string]any {
	f.t.Helper()
	out := f.request(method, path, body, cookie, admin)
	if out.Code != http.StatusOK {
		f.t.Fatalf("%s %s: %d %s", method, path, out.Code, out.Body.String())
	}
	var result map[string]any
	if err := json.Unmarshal(out.Body.Bytes(), &result); err != nil {
		f.t.Fatal(err)
	}
	return result
}
func (f *imageBrowserFixture) initialize() {
	t := f.t
	login := f.request("POST", "/admin/api/login", map[string]any{
		"username": f.cfg.AdminUsername, "password": f.cfg.AdminPassword}, nil, true)
	if login.Code != 200 {
		t.Fatalf("login: %d %s", login.Code, login.Body.String())
	}
	f.adminCookie = responseCookieNamed(t, login, auth.AdminSessionCookieName)
	f.call("POST", "/admin/api/maintenance/disable", map[string]any{
		"expected_revision": "1", "reason": "Browser acceptance fixture"}, f.adminCookie, true)
	var adminID int64
	if err := f.store.DB().QueryRow("SELECT id FROM users WHERE is_admin=1").Scan(&adminID); err != nil {
		t.Fatal(err)
	}
	for index, level := range []int{1, 1, 5, 6} {
		f.users = append(f.users, f.seedUser(index, level, adminID))
	}
	const prefix = "/admin/api/limited-activities/picture-book"
	current := f.call("GET", prefix, nil, f.adminCookie, true)
	f.call("PUT", prefix, map[string]any{
		"expected_revision": current["revision"], "visible": false, "paused": false,
		"starts_at": f.now().Unix() - 60, "ends_at": f.now().Unix() + 86400,
		"module_config": map[string]any{"paper_price": "1000", "brush_price": "10000", "brush_cap": "100"},
	}, f.adminCookie, true)
	upstream := f.call("GET", prefix+"/upstream", nil, f.adminCookie, true)
	mapping := map[string]any{"model_pointer": "/model",
		"parameters": map[string]any{"prompt": "/prompt", "n": "/n", "size": "/canvas"},
		"constants":  []any{}}
	adapter := map[string]any{
		"discovery": map[string]any{"method": "GET", "path": "/v1/models",
			"items_pointer": "/data", "id_pointer": "/id", "metadata_pointer": "/metadata"},
		"submit": map[string]any{"method": "POST", "path": "/v1/images/generations", "mapping": mapping, "receipt": map[string]any{"indicator_pointer": "/ticket/accepted", "indicator_value": true}},
		"poll":   map[string]any{"method": "GET", "path": "/jobs/{task_id}"},
		"response": map[string]any{"task_id_pointer": "/ticket/reference", "state_pointer": "/phase",
			"working_states": []string{"working"}, "success_states": []string{"complete"},
			"failure_states": []string{"failed"}, "images_pointer": "/pictures", "base64_pointer": "/encoded"},
	}
	f.call("PUT", prefix+"/upstream", map[string]any{
		"expected_revision": upstream["revision"], "base_url": f.upstream.URL,
		"secret": map[string]any{"mode": "replace", "value": imageFixtureSecret},
		"rpm":    10000, "concurrency": 1, "per_user_limit": 2, "global_limit": 100,
		"queue_timeout_seconds": 1800, "execution_timeout_seconds": 1800,
		"memory_budget_mib": 512, "image_origins": []string{}, "adapter": adapter,
	}, f.adminCookie, true)
	refresh := f.call("POST", prefix+"/models/refresh", map[string]any{}, f.adminCookie, true)
	operationID := refresh["operation"].(map[string]any)["id"].(string)
	deadline := time.Now().Add(20 * time.Second)
	for {
		op := f.call("GET", prefix+"/models/refresh/"+operationID, nil, f.adminCookie, true)
		if op["state"] == "succeeded" {
			break
		}
		if op["state"] == "failed" || time.Now().After(deadline) {
			t.Fatalf("model refresh failed: %v", op)
		}
		time.Sleep(50 * time.Millisecond)
	}
	models := f.call("GET", prefix+"/models", nil, f.adminCookie, true)["data"].([]any)
	if len(models) != 1 {
		t.Fatalf("unexpected catalog size %d", len(models))
	}
	model := models[0].(map[string]any)
	f.call("PUT", prefix+"/models/"+model["id"].(string), map[string]any{
		"expected_revision": model["revision"], "display_name": "Browser canvas",
		"description": "<img src=x onerror=alert(1)> is displayed as text.",
		"enabled":     true, "price": map[string]string{"paper": "2", "brush": "1"},
		"parameters": []any{
			map[string]any{"key": "prompt", "supported": true, "required": true, "type": "string",
				"min_length": 1, "max_length": 65536, "length_unit": "utf8_bytes"},
			map[string]any{"key": "n", "supported": true, "required": false, "type": "integer",
				"minimum": 1, "maximum": 4, "step": 1, "default": 1},
			map[string]any{"key": "size", "supported": true, "required": true, "type": "string",
				"length_unit": "utf8_bytes", "default": "80x144", "dimensions": map[string]any{
					"format": "width_height", "width": map[string]int{"minimum": 48, "maximum": 240, "step": 16},
					"height": map[string]int{"minimum": 80, "maximum": 272, "step": 32}}},
		}, "combinations": []any{}, "mapping": map[string]any{
			"model_pointer": "", "parameters": map[string]any{}, "constants": []any{}},
	}, f.adminCookie, true)
	for _, user := range f.users {
		for asset, quantity := range map[string]string{"sketch_paper": "100", "sketch_brush": "20"} {
			f.call("POST", "/api/limited-activities/picture-book/exchange",
				map[string]string{"asset": asset, "quantity": quantity}, user.Cookie, false)
		}
	}
}

func (f *imageBrowserFixture) seedUser(index, level int, admin int64) imageBrowserUser {
	t := f.t
	ctx := context.Background()
	now := f.now().Unix()
	zero := db.EncodeU128(db.U128{})
	one, err := db.ParseU128Decimal("1")
	if err != nil {
		t.Fatal(err)
	}
	result, err := f.store.DB().Exec(`INSERT INTO users(discord_id,username,level,donation_credit_mag,total_requests,total_uncached_input_tokens,total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,total_unknown_usage_requests,revision,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		fmt.Sprintf("image-fixture-%d", index), fmt.Sprintf("Canvas participant %d", index), level,
		zero, zero, zero, zero, zero, zero, zero, db.EncodeU128(one), now, now)
	if err != nil {
		t.Fatal(err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.DB().Exec("INSERT INTO caller_keys(user_id,generation,updated_at) VALUES(?,0,?)", id, now); err != nil {
		t.Fatal(err)
	}
	token := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{byte(71 + index)}, 32))
	digest := sha256.Sum256([]byte(token))
	tx, err := f.store.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO sessions(token_hash,user_id,last_seen_at,expires_at,absolute_expires_at,created_at,cred_gen) VALUES(?,?,?,?,?,?,?)`,
		hex.EncodeToString(digest[:]), id, now, now+3600, now+7200, now, "image-fixture-generation"); err != nil {
		t.Fatal(err)
	}
	wallet, err := ledger.CreateUserAssetAccount(ctx, tx, id, ledger.General, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.CreateUserAssetAccount(ctx, tx, id, ledger.Game, now); err != nil {
		t.Fatal(err)
	}
	external, err := ledger.CodedAssetAccount(ctx, tx, "external", ledger.General)
	if err != nil {
		t.Fatal(err)
	}
	operation, err := db.GenerateOpaqueID("op_")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := ledger.NewAdminUserAdjustment(ledger.Meta{OperationID: operation, ActorUserID: admin, CreatedAt: now},
		wallet.ID, external.ID, ledger.AmountFromMilli(1_000_000_000), 0, ledger.Amount{}, "Image browser funding")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Apply(ctx, tx, plan); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return imageBrowserUser{ID: strconv.FormatInt(id, 10), Level: level,
		Cookie: &http.Cookie{Name: auth.UserSessionCookieName, Value: token}}
}

type imageFixtureJob struct {
	count    int
	scenario string
	released bool
}
type imageFixtureUpstream struct {
	mu          sync.Mutex
	jobs        map[string]*imageFixtureJob
	png         string
	submissions int
	polls       int
}

func newImageFixtureUpstream(t *testing.T) *imageFixtureUpstream {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			img.Set(x, y, color.NRGBA{R: uint8(20 + x*20), G: uint8(50 + y*20), B: 160, A: 255})
		}
	}
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, img); err != nil {
		t.Fatal(err)
	}
	return &imageFixtureUpstream{jobs: map[string]*imageFixtureJob{},
		png: base64.StdEncoding.EncodeToString(buffer.Bytes())}
}
func writeImageFixtureJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(value)
}
func (u *imageFixtureUpstream) release() {
	u.mu.Lock()
	defer u.mu.Unlock()
	for _, job := range u.jobs {
		job.released = true
	}
}
func (u *imageFixtureUpstream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer "+imageFixtureSecret {
		http.Error(w, "wrong synthetic credential", 401)
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	switch {
	case r.URL.Path == "/v1/models" && r.Method == http.MethodGet:
		writeImageFixtureJSON(w, map[string]any{"data": []any{map[string]any{
			"id": imageFixtureModel, "metadata": map[string]string{"private": "synthetic-private-metadata"},
		}}})
	case r.URL.Path == "/v1/images/generations" && r.Method == http.MethodPost:
		var input struct {
			Model  string `json:"model"`
			Prompt string `json:"prompt"`
			N      int    `json:"n"`
			Canvas string `json:"canvas"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&input); err != nil ||
			input.Model != imageFixtureModel || input.N < 1 || input.N > 4 || input.Canvas == "" {
			http.Error(w, "invalid synthetic input", 400)
			return
		}
		u.submissions++
		if strings.HasPrefix(input.Prompt, "synchronous") {
			images := []any{}
			for range input.N {
				images = append(images, map[string]string{"encoded": u.png})
			}
			writeImageFixtureJSON(w, map[string]any{"pictures": images})
			return
		}
		id := fmt.Sprintf("synthetic-job-%d", u.submissions)
		scenario := "success"
		for _, candidate := range []string{"hold", "partial", "fail"} {
			if strings.HasPrefix(input.Prompt, candidate) {
				scenario = candidate
				break
			}
		}
		u.jobs[id] = &imageFixtureJob{count: input.N, scenario: scenario}
		writeImageFixtureJSON(w, map[string]any{"ticket": map[string]any{"accepted": true, "reference": id}})
	case strings.HasPrefix(r.URL.Path, "/jobs/") && r.Method == http.MethodGet:
		u.polls++
		id := strings.TrimPrefix(r.URL.Path, "/jobs/")
		job, ok := u.jobs[id]
		if !ok {
			http.NotFound(w, r)
			return
		}
		state, count := "complete", job.count
		if job.scenario == "hold" && !job.released {
			state, count = "working", 0
		}
		if job.scenario == "fail" {
			state, count = "failed", 0
		}
		if job.scenario == "partial" {
			count = max(1, count/2)
		}
		images := []any{}
		for range count {
			images = append(images, map[string]string{"encoded": u.png})
		}
		writeImageFixtureJSON(w, map[string]any{"phase": state, "pictures": images})
	default:
		http.NotFound(w, r)
	}
}
