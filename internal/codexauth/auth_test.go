package codexauth

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/config"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/egress"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
	_ "modernc.org/sqlite"
)

type resolver struct{}

func (resolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
}

type fixture struct {
	m         *Manager
	v         *secret.Vault
	now       atomic.Int64
	refreshes atomic.Int64
	denied    atomic.Bool
	poll      func(http.ResponseWriter, *http.Request)
	refreshFn func(http.ResponseWriter, *http.Request)
	usageFn   func(http.ResponseWriter, *http.Request)
}

func jwt(value any) string {
	raw, _ := json.Marshal(value)
	return "e30." + base64.RawURLEncoding.EncodeToString(raw) + ".fixture"
}
func (f *fixture) tokens(w http.ResponseWriter, access, refresh string) {
	_ = json.NewEncoder(w).Encode(map[string]any{"id_token": jwt(map[string]any{"sub": "subject", "email": "operator@example.invalid", "https://api.openai.com/auth": map[string]string{"chatgpt_account_id": "workspace"}}), "access_token": jwt(map[string]any{"exp": f.now.Load() + 3600, "jti": access}), "refresh_token": refresh})
}
func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{}
	f.now.Store(1800000000)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/backend-api/wham/usage":
			if f.usageFn != nil {
				f.usageFn(w, r)
			} else {
				http.NotFound(w, r)
			}
		case "/api/accounts/deviceauth/usercode":
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["client_id"] != ClientID {
				t.Error("incorrect client")
			}
			_, _ = w.Write([]byte(`{"device_auth_id":"private-device-id","user_code":"ABCD-EFGH","interval":"1"}`))
		case "/api/accounts/deviceauth/token":
			if f.poll != nil {
				f.poll(w, r)
				return
			}
			_, _ = w.Write([]byte(`{"authorization_code":"one-time-code","code_verifier":"verifier"}`))
		case "/oauth/token":
			if r.Header.Get("Content-Type") == "application/json" {
				f.refreshes.Add(1)
				if f.refreshFn != nil {
					f.refreshFn(w, r)
					return
				}
				var body map[string]string
				_ = json.NewDecoder(r.Body).Decode(&body)
				if body["resource"] != "" || body["client_id"] != ClientID || body["refresh_token"] != "refresh-original" || body["grant_type"] != "refresh_token" {
					t.Error("incorrect refresh")
				}
				f.tokens(w, "rotated", "refresh-rotated")
				return
			}
			_ = r.ParseForm()
			if r.Form.Get("code") != "one-time-code" || r.Form.Get("code_verifier") != "verifier" || r.Form.Get("redirect_uri") != f.m.client.issuer+"/deviceauth/callback" || r.Form.Get("resource") != "" {
				t.Error("incorrect exchange")
			}
			f.tokens(w, "original", "refresh-original")
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	stack, err := egress.NewStack(egress.StackOptions{AllowedOrigins: []string{server.URL}, Resolver: resolver{}})
	if err != nil {
		t.Fatal(err)
	}
	if err = stack.AddSelfOrigins(context.Background(), &config.Config{SiteBaseURL: "https://self.invalid", UserHost: "self.invalid", AdminHost: "admin.invalid", ListenAddr: "127.0.0.1:41997"}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stack.CloseIdleConnections)
	database, err := sql.Open("sqlite", filepath.ToSlash(filepath.Join(t.TempDir(), "auth.sqlite"))+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	database.SetMaxOpenConns(3)
	t.Cleanup(func() { database.Close() })
	if _, err = database.Exec(db.CodexSessionSQL); err != nil {
		t.Fatal(err)
	}
	f.v, err = secret.New([]byte(strings.Repeat("x", 32)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.v.Close() })
	f.m, err = New(Config{DB: database, ReadDB: database, Secrets: f.v, Stack: stack, Now: func() time.Time { return time.Unix(f.now.Load(), 0) }, AdminAuthorize: func(context.Context, *sql.Tx) error {
		if f.denied.Load() {
			return errors.New("denied")
		}
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	f.m.client.issuer = server.URL
	f.m.client.usageURL = server.URL + "/backend-api/wham/usage"
	t.Cleanup(func() { f.m.Close() })
	return f
}
func waitStatus(t *testing.T, f *fixture, want string) Status {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		s, err := f.m.Status(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if s.Login != nil && s.Login.State == want {
			return s
		}
		time.Sleep(time.Millisecond * 5)
	}
	t.Fatalf("login did not reach %s", want)
	return Status{}
}
func TestDeviceLoginAndSerialRefresh(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	start, err := f.m.Begin(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if start.Login.UserCode != "ABCD-EFGH" || start.Login.VerificationURL != VerificationURL {
		t.Fatalf("bad device status: %+v", start)
	}
	s := waitStatus(t, f, "succeeded")
	if !s.Connected || s.Revision != 1 || s.Login.UserCode != "" {
		t.Fatalf("bad connected status: %+v", s)
	}
	var stored string
	if err = f.m.config.DB.QueryRow("SELECT encrypted_credentials FROM agent_codex_session").Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stored, "refresh-original") {
		t.Fatal("plaintext stored")
	}
	if _, err = f.v.OpenChatGPT(stored, "credentials"); err == nil {
		t.Fatal("shared secret purpose")
	}
	f.now.Add(3541)
	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			token, account, revision, e := f.m.AccessTokenSnapshot(ctx)
			if e != nil || account != "workspace" || revision != 1 || len(token) == 0 {
				t.Errorf("refresh: %s %d %v", account, revision, e)
			}
		}()
	}
	wg.Wait()
	if f.refreshes.Load() != 1 {
		t.Fatalf("refresh count %d", f.refreshes.Load())
	}
}
func TestCancelAndDisconnectRejectLateLogin(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	entered, release := make(chan struct{}), make(chan struct{})
	f.poll = func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		_, _ = w.Write([]byte(`{"authorization_code":"one-time-code","code_verifier":"verifier"}`))
	}
	s, err := f.m.Begin(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	<-entered
	if _, err = f.m.Cancel(ctx, s.Login.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.m.Disconnect(ctx, 0); err != nil {
		t.Fatal(err)
	}
	close(release)
	f.m.workers.Wait()
	s, err = f.m.Status(ctx)
	if err != nil || s.Connected || s.Revision != 1 {
		t.Fatalf("late login revived: %+v %v", s, err)
	}
}
func TestDisconnectCancelsRequestsAndRefresh(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, err := f.m.Begin(ctx, 0); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, f, "succeeded")
	bound, release, err := f.m.RequestContext(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	f.now.Add(3541)
	entered, unblock := make(chan struct{}), make(chan struct{})
	defer close(unblock)
	f.refreshFn = func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-unblock
		f.tokens(w, "late", "refresh-late")
	}
	done := make(chan error, 1)
	go func() { _, _, _, e := f.m.AccessTokenSnapshot(ctx); done <- e }()
	<-entered
	if _, err = f.m.Disconnect(ctx, 1); err != nil {
		t.Fatal(err)
	}
	// Request cancellation propagates through context.AfterFunc. Wait for its
	// notification rather than assuming that callback has already been scheduled.
	select {
	case <-bound.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("request not cancelled")
	}
	if !errors.Is(bound.Err(), context.Canceled) {
		t.Fatalf("request cancellation: %v", bound.Err())
	}
	// The refresh must stop while the server response is still blocked.
	select {
	case err = <-done:
		if !errors.Is(err, ErrDisconnected) {
			t.Fatalf("late refresh: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("refresh did not stop after disconnect")
	}
	if _, _, err = f.m.RequestContext(ctx, 1); !errors.Is(err, ErrDisconnected) {
		t.Fatalf("old generation reused: %v", err)
	}
	s, _ := f.m.Status(ctx)
	if s.Connected || s.Revision != 2 {
		t.Fatalf("revived: %+v", s)
	}
}
func TestRejectedLoginAndRevokedRefreshAreSafe(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.poll = func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		_, _ = w.Write([]byte(`{"error":"access_denied","error_description":"private-device-id"}`))
	}
	if _, err := f.m.Begin(ctx, 0); err != nil {
		t.Fatal(err)
	}
	s := waitStatus(t, f, "failed")
	raw, _ := json.Marshal(s)
	if strings.Contains(string(raw), "private-device-id") || strings.Contains(string(raw), "ABCD-EFGH") {
		t.Fatal("auth material exposed")
	}
	f.poll = nil
	if _, err := f.m.Begin(ctx, 0); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, f, "succeeded")
	f.refreshFn = func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		_, _ = w.Write([]byte(`{"error":{"code":"refresh_token_reused"},"error_description":"refresh-original"}`))
	}
	f.now.Add(3541)
	if _, _, _, err := f.m.AccessTokenSnapshot(ctx); !errors.Is(err, ErrDisconnected) {
		t.Fatalf("revocation: %v", err)
	}
	s, _ = f.m.Status(ctx)
	if s.Connected {
		t.Fatal("revoked credentials retained")
	}
	f.denied.Store(true)
	if _, err := f.m.Status(ctx); err == nil {
		t.Fatal("unauthorized status")
	}
	if _, err := f.m.Begin(ctx, s.Revision); err == nil {
		t.Fatal("unauthorized login")
	}
	if _, err := f.m.Disconnect(ctx, s.Revision); err == nil {
		t.Fatal("unauthorized logout")
	}
}
func TestPendingLoginDoesNotSurviveRestart(t *testing.T) {
	f := newFixture(t)
	f.poll = func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(404) }
	s, err := f.m.Begin(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if s.Login == nil {
		t.Fatal("missing login")
	}
	if err = f.m.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := New(f.m.config)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	s, err = next.Status(context.Background())
	if err != nil || s.Login != nil || s.Connected {
		t.Fatalf("pending survived: %+v %v", s, err)
	}
}
func TestRefreshStorageFailureRetainsRotatedCredential(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, err := f.m.Begin(ctx, 0); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, f, "succeeded")
	f.now.Add(3541)
	if _, err := f.m.config.DB.Exec("CREATE TRIGGER fail_refresh BEFORE UPDATE ON agent_codex_session BEGIN SELECT RAISE(ABORT,'storage unavailable'); END"); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := f.m.AccessTokenSnapshot(ctx); err == nil {
		t.Fatal("storage failure ignored")
	}
	if _, err := f.m.config.DB.Exec("DROP TRIGGER fail_refresh"); err != nil {
		t.Fatal(err)
	}
	token, _, revision, err := f.m.AccessTokenSnapshot(ctx)
	if err != nil || len(token) == 0 || revision != 1 {
		t.Fatalf("rotated credential recovery: %v", err)
	}
	if f.refreshes.Load() != 1 {
		t.Fatalf("spent refresh token replayed %d times", f.refreshes.Load())
	}
}
func TestExpiredDevicePollClearsCode(t *testing.T) {
	f := newFixture(t)
	f.poll = func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(404) }
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	p := &pendingLogin{cancel: cancel, status: LoginStatus{ID: "synthetic", State: "pending", UserCode: "CODE"}}
	f.m.mu.Lock()
	f.m.login = p
	f.m.workers.Add(1)
	f.m.mu.Unlock()
	go f.m.completeLogin(ctx, p, deviceCode{DeviceID: "fixture", Code: "CODE", Interval: time.Second})
	s := waitStatus(t, f, "expired")
	if s.Login.UserCode != "" || s.Connected {
		t.Fatal("expired code retained")
	}
}
func TestRefreshRequiresNewAccessToken(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, err := f.m.Begin(ctx, 0); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, f, "succeeded")
	f.now.Add(3541)
	f.refreshFn = func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"refresh_token":"new-refresh","expires_in":3600}`))
	}
	if _, _, _, err := f.m.AccessTokenSnapshot(ctx); !errors.Is(err, ErrInvalid) {
		t.Fatalf("missing new access token accepted: %v", err)
	}
	s, err := load(ctx, f.m.config.ReadDB)
	if err != nil {
		t.Fatal(err)
	}
	if s.expires > f.now.Load()+60 {
		t.Fatal("expired access token was relabeled as fresh")
	}
}
func TestRefreshReadFailureRetainsRotatedCredential(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, err := f.m.Begin(ctx, 0); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, f, "succeeded")
	f.now.Add(3541)
	original := f.m.config.ReadDB
	unavailable, err := sql.Open("sqlite", filepath.ToSlash(filepath.Join(t.TempDir(), "closed.sqlite")))
	if err != nil {
		t.Fatal(err)
	}
	_ = unavailable.Close()
	f.refreshFn = func(w http.ResponseWriter, r *http.Request) {
		f.m.mu.Lock()
		f.m.config.ReadDB = unavailable
		f.m.mu.Unlock()
		f.tokens(w, "rotated-after-read-failure", "refresh-rotated")
	}
	if _, _, _, err = f.m.AccessTokenSnapshot(ctx); err == nil {
		t.Fatal("read failure ignored")
	}
	f.m.mu.Lock()
	f.m.config.ReadDB = original
	f.m.mu.Unlock()
	if _, _, _, err = f.m.AccessTokenSnapshot(ctx); err != nil {
		t.Fatalf("rotation recovery: %v", err)
	}
	if f.refreshes.Load() != 1 {
		t.Fatal("spent refresh token replayed after read failure")
	}
	s, err := load(ctx, original)
	if err != nil {
		t.Fatal(err)
	}
	c, err := f.m.open(s)
	if err != nil || c.RefreshToken != "refresh-rotated" {
		t.Fatal("rotated refresh token was lost")
	}
}
