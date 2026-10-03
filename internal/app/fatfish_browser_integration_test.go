package app

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/auth"
	"github.com/waiting-here/NonbiriAPI/internal/dbfixture"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

// The opt-in fixture uses actual routes, sessions, storage, randomness, browser
// bundles and server replay. It never contacts a configured external service.
func TestFatFishBrowserFixture(t *testing.T) {
	statePath := os.Getenv("NONBIRI_FISH_BROWSER_STATE")
	if statePath == "" {
		t.Skip("browser fixture is opt-in")
	}
	vault, err := secret.New(bytes.Repeat([]byte{0x39}, secret.MasterKeyBytes))
	if err != nil {
		t.Fatal(err)
	}
	defer vault.Close()
	f := &imageBrowserFixture{t: t, vault: vault, cfg: auditConfig(), path: filepath.Join(t.TempDir(), "fish.db")}
	dbfixture.Materialize(t, f.path)
	f.upstream = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("unexpected upstream request", r.Method, r.URL.Path)
		http.Error(w, "upstream disabled", http.StatusBadGateway)
	}))
	defer f.upstream.Close()
	proxy := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		handler := f.app.Load().handler
		f.mu.Unlock()
		// Account event streams remain open while independent requests proceed.
		handler.ServeHTTP(w, r)
	})
	users, admins := httptest.NewUnstartedServer(proxy), httptest.NewUnstartedServer(proxy)
	_, adminPort, err := net.SplitHostPort(admins.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	f.cfg.UserHost, f.cfg.AdminHost = users.Listener.Addr().String(), "localhost:"+adminPort
	f.cfg.ListenAddr, f.cfg.SiteBaseURL, f.cfg.DBPath = f.cfg.UserHost, "http://"+f.cfg.UserHost, f.path
	if err := f.open(); err != nil {
		t.Fatal(err)
	}
	users.Start()
	admins.Start()
	admins.URL = "http://" + f.cfg.AdminHost
	defer func() {
		users.Close()
		admins.Close()
		if err := f.closeApplication(); err != nil {
			t.Error(err)
		}
	}()
	login := f.request("POST", "/admin/api/login", map[string]any{"username": f.cfg.AdminUsername, "password": f.cfg.AdminPassword}, nil, true)
	if login.Code != http.StatusOK {
		t.Fatal("administrator fixture login failed")
	}
	f.adminCookie = responseCookieNamed(t, login, auth.AdminSessionCookieName)
	if _, err := f.store.DB().Exec("INSERT INTO site_config(key,value,updated_at) VALUES('site_timezone_offset_minutes','0',0) ON CONFLICT(key) DO UPDATE SET value='0'"); err != nil {
		t.Fatal(err)
	}
	f.call("POST", "/admin/api/maintenance/disable", map[string]string{"expected_revision": "1", "reason": "Browser acceptance fixture"}, f.adminCookie, true)
	var adminID int64
	if err := f.store.DB().QueryRow("SELECT id FROM users WHERE is_admin=1").Scan(&adminID); err != nil {
		t.Fatal(err)
	}
	for i, level := range []int{1, 1, 5, 6} {
		f.users = append(f.users, f.seedUser(i, level, adminID))
	}

	stop := make(chan struct{})
	var stopped sync.Once
	const controlToken = "synthetic-fish-browser-control"
	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+controlToken {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		switch r.URL.Path {
		case "/shutdown":
			stopped.Do(func() { close(stop) })
		case "/restart":
			if err := f.closeApplication(); err != nil {
				http.Error(w, "close failed", 500)
				return
			}
			if err := f.open(); err != nil {
				http.Error(w, "reopen failed", 500)
				return
			}
		case "/advance":
			var body struct {
				Seconds int64 `json:"seconds"`
			}
			if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&body) != nil || body.Seconds < 0 || body.Seconds > 7200 {
				http.Error(w, "invalid clock step", 400)
				return
			}
			f.offset.Add(body.Seconds)
		case "/snapshot":
			out := make(map[string]int64)
			queries := map[string]string{
				"ledger_seq":      "SELECT last_ledger_seq FROM credit_capacity WHERE id=1",
				"periods":         "SELECT count(*) FROM fatfish_periods",
				"progress":        "SELECT count(*) FROM fatfish_progress",
				"playtests":       "SELECT count(*) FROM fatfish_playtests",
				"verified_passes": "SELECT count(*) FROM fatfish_playtests WHERE passed=1 AND stars>=1",
				"versions":        "SELECT count(*) FROM fatfish_level_versions",
				"default_closed":  "SELECT count(*) FROM limited_activity_configs WHERE activity_key='fat-fish' AND visible=0 AND starts_at IS NULL AND ends_at IS NULL",
			}
			for key, query := range queries {
				var value int64
				if err := f.store.DB().QueryRow(query).Scan(&value); err != nil {
					http.Error(w, "snapshot failed", 500)
					return
				}
				out[key] = value
			}
			writeImageFixtureJSON(w, out)
			return
		default:
			http.NotFound(w, r)
			return
		}
		writeImageFixtureJSON(w, map[string]bool{"ok": true})
	}))
	defer control.Close()
	raw, err := json.Marshal(map[string]any{"user_url": users.URL, "admin_url": admins.URL,
		"control_url": control.URL, "control_token": controlToken, "users": f.users, "admin_cookie": f.adminCookie})
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
	case <-time.After(12 * time.Minute):
		t.Fatal("browser fixture was not shut down")
	}
}
