package main

import (
	"context"
	"database/sql"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/antiabuse"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbfixture"
	"github.com/waiting-here/NonbiriAPI/internal/flowcontrol"
	"github.com/waiting-here/NonbiriAPI/internal/forward"
	"github.com/waiting-here/NonbiriAPI/internal/httpmw"
	"github.com/waiting-here/NonbiriAPI/internal/lifecyclegate"
	"github.com/waiting-here/NonbiriAPI/internal/ratelimit"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

type scopeCaller struct{ id int64 }

func (s scopeCaller) ResolveCallerKey(context.Context, string) (resources.CallerIdentity, error) {
	return resources.CallerIdentity{UserID: s.id, Generation: 1}, nil
}

type noShortRejection struct{ t *testing.T }

func (s noShortRejection) RecordCharityRejectionTx(context.Context, *sql.Tx, int64, string, string, int, int, int64) error {
	s.t.Fatal("RPM denial unexpectedly reached short-request accounting")
	return nil
}

type publicScopeFixture struct {
	store        *db.Store
	userID       int64
	handler      http.Handler
	forwardCalls *atomic.Int64
}

func newPublicScopeFixture(t *testing.T, rpm ratelimit.RPMConfig, downstreamStatus int) publicScopeFixture {
	t.Helper()
	vault, err := secret.New(make([]byte, secret.MasterKeyBytes))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = vault.Close() })
	path := filepath.Join(t.TempDir(), "scope.sqlite")
	dbfixture.Materialize(t, path)
	store, err := db.Open(path, vault)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	zero := db.EncodeU128(db.U128{})
	now := time.Now().Unix()
	result, err := store.DB().Exec(`INSERT INTO users(username,donation_credit_mag,total_requests,total_uncached_input_tokens,total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,total_unknown_usage_requests,revision,created_at,updated_at)
VALUES('Test account',?,?,?,?,?,?,?,?,?,?)`, zero, zero, zero, zero, zero, zero, zero, zero, now, now)
	if err != nil {
		t.Fatal(err)
	}
	userID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().Exec(`INSERT INTO caller_keys(user_id,generation,key_hash,key_created_at,updated_at) VALUES(?,1,randomblob(32),?,?)`, userID, now, now); err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]string{antiabuse.KeyRPMBanThreshold: "2", antiabuse.KeyRPMBanDurationSeconds: "90"} {
		if _, err := store.DB().Exec(`UPDATE site_config SET value=? WHERE key=?`, value, key); err != nil {
			t.Fatal(err)
		}
	}
	var abuse *antiabuse.Service
	flow, err := flowcontrol.New(flowcontrol.Config{RPM: rpm, UserLimits: flowcontrol.DBUserLimitResolver(store), OnDenied: func(ctx context.Context, id int64, reason ratelimit.RPMReason) {
		applyPublicRPMDenial(ctx, id, reason, abuse)
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = flow.Close() })
	abuse, err = antiabuse.NewService(antiabuse.ServiceConfig{Database: store.DB(), Rejections: noShortRejection{t}, BeginUserRetirement: func(_ context.Context, id int64) (antiabuse.Retirement, error) { return flow.BeginUserRetirement(id) }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = abuse.Close() })
	lifecycle, err := lifecyclegate.New(lifecyclegate.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lifecycle.Close() })
	caller, err := forward.NewCallerKeyMiddleware(scopeCaller{userID}, lifecycle)
	if err != nil {
		t.Fatal(err)
	}
	forwardCalls := &atomic.Int64{}
	metered, err := publicFlowHandler(flow, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		forwardCalls.Add(1)
		w.WriteHeader(downstreamStatus)
		_, _ = w.Write([]byte("ok"))
	}))
	if err != nil {
		t.Fatal(err)
	}
	return publicScopeFixture{store: store, userID: userID, handler: caller.Wrap(metered), forwardCalls: forwardCalls}
}

// In-memory bodies are already available. Real transport deadlines are
// separately exercised by the forwarding package's HTTP server tests.
type scopeRecorder struct{ *httptest.ResponseRecorder }

func (*scopeRecorder) SetReadDeadline(time.Time) error { return nil }

type publicScopeBody struct {
	io.Reader
	beforeRead func()
	reads      int
}

func (b *publicScopeBody) Read(p []byte) (int, error) {
	b.reads++
	if b.beforeRead != nil {
		b.beforeRead()
		b.beforeRead = nil
	}
	return b.Reader.Read(p)
}

func (*publicScopeBody) Close() error { return nil }

func (f publicScopeFixture) request(body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "https://gateway.example/v1/chat/completions", strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer nbk_test")
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(&scopeRecorder{w}, r)
	return w
}

func TestPublicSelfRPMDenialDoesNotBanAccount(t *testing.T) {
	f := newPublicScopeFixture(t, ratelimit.RPMConfig{GlobalLimit: 100, PerUserLimit: 1}, http.StatusOK)
	for index := 0; index < 3; index++ {
		w := f.request(`{"model":"private/model","messages":[{"role":"user","content":"x"}]}`)
		want := http.StatusTooManyRequests
		if index == 0 {
			want = http.StatusOK
		}
		if w.Code != want {
			t.Fatalf("request %d status=%d body=%s", index, w.Code, w.Body.String())
		}
	}
	var banned, auto int
	if err := f.store.DB().QueryRow(`SELECT is_banned,auto_banned FROM users WHERE id=?`, f.userID).Scan(&banned, &auto); err != nil {
		t.Fatal(err)
	}
	if banned != 0 || auto != 0 {
		t.Fatalf("self-use RPM denial changed account ban state: banned=%d automatic=%d", banned, auto)
	}
	if f.forwardCalls.Load() != 1 {
		t.Fatalf("denial entered downstream: calls=%d", f.forwardCalls.Load())
	}
}

func TestPublicRPMBanCountsOnlyDecodedCharityAndUserLimit(t *testing.T) {
	for _, tc := range []struct {
		name, body   string
		global, user int
		downstream   int
		ban          bool
	}{
		{name: "charity", body: `{"model":"[公益]care/model","messages":[]}`, global: 100, user: 1, downstream: 200, ban: true},
		{name: "duplicate model", body: `{"model":"[公益]care/model","model":"private/model","messages":[]}`, global: 100, user: 1, downstream: 200},
		{name: "missing model", body: `{"messages":[]}`, global: 100, user: 1, downstream: 200},
		{name: "truncated body", body: `{"model":"[公益]care/model"`, global: 100, user: 1, downstream: 200},
		{name: "trailing object", body: `{"model":"[公益]care/model","messages":[]} {}`, global: 100, user: 1, downstream: 200},
		{name: "escaped charity name", body: `{"model":"\u005b公益\u005dcare/model","messages":[]}`, global: 100, user: 1, downstream: 200, ban: true},
		{name: "global limit", body: `{"model":"[公益]care/model","messages":[]}`, global: 1, user: 100, downstream: 200},
		{name: "downstream key or upstream 429", body: `{"model":"[公益]care/model","messages":[]}`, global: 100, user: 100, downstream: 429},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newPublicScopeFixture(t, ratelimit.RPMConfig{GlobalLimit: tc.global, PerUserLimit: tc.user}, tc.downstream)
			for i := 0; i < 3; i++ {
				w := f.request(tc.body)
				want := http.StatusTooManyRequests
				if i == 0 {
					want = tc.downstream
				}
				if w.Code != want {
					t.Fatalf("request %d: %d %s", i, w.Code, w.Body.String())
				}
			}
			var banned, automatic bool
			var generation int64
			var hash []byte
			if err := f.store.DB().QueryRow(`SELECT u.is_banned,u.auto_banned,c.generation,c.key_hash FROM users u JOIN caller_keys c ON c.user_id=u.id WHERE u.id=?`, f.userID).Scan(&banned, &automatic, &generation, &hash); err != nil {
				t.Fatal(err)
			}
			if banned != tc.ban || automatic != tc.ban {
				t.Fatalf("ban=%v automatic=%v want=%v", banned, automatic, tc.ban)
			}
			if tc.ban && (generation != 2 || hash != nil) || !tc.ban && (generation != 1 || len(hash) != 32) {
				t.Fatal("caller-key revocation crossed policy scope")
			}
			if tc.downstream == 429 && f.forwardCalls.Load() != 3 || tc.downstream != 429 && f.forwardCalls.Load() != 1 {
				t.Fatalf("downstream calls=%d", f.forwardCalls.Load())
			}
			var ledgerRows, logs int
			if err := f.store.DB().QueryRow(`SELECT (SELECT COUNT(*) FROM credit_operations),(SELECT COUNT(*) FROM request_logs)`).Scan(&ledgerRows, &logs); err != nil {
				t.Fatal(err)
			}
			if ledgerRows != 0 || logs != 0 {
				t.Fatal("RPM denial wrote accounting or request logs")
			}
		})
	}
}

func TestPublicCharityRPMBanThroughHTTPServer(t *testing.T) {
	f := newPublicScopeFixture(t, ratelimit.RPMConfig{GlobalLimit: 100, PerUserLimit: 1}, http.StatusOK)
	server := httptest.NewServer(httpmw.API(f.handler))
	defer server.Close()
	client := server.Client()
	client.Timeout = 5 * time.Second
	for i := 0; i < 3; i++ {
		r, err := http.NewRequest(http.MethodPost, server.URL+"/v1/chat/completions", strings.NewReader(`{"model":"[公益]care/model","messages":[]}`))
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("Authorization", "Bearer nbk_test")
		response, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		_, readErr := io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
		if readErr != nil {
			t.Fatal(readErr)
		}
		want := http.StatusTooManyRequests
		if i == 0 {
			want = http.StatusOK
		}
		if response.StatusCode != want {
			t.Fatalf("response %d: %d", i, response.StatusCode)
		}
	}
	var banned bool
	if err := f.store.DB().QueryRow(`SELECT auto_banned FROM users WHERE id=?`, f.userID).Scan(&banned); err != nil {
		t.Fatal(err)
	}
	if !banned || f.forwardCalls.Load() != 1 {
		t.Fatal("real transport lost scoped RPM enforcement")
	}
}

func TestPublicRPMDenialRechecksAccountAndCancellation(t *testing.T) {
	for _, state := range []string{"banned", "deleted during body read", "cancelled"} {
		t.Run(state, func(t *testing.T) {
			f := newPublicScopeFixture(t, ratelimit.RPMConfig{GlobalLimit: 100, PerUserLimit: 1}, http.StatusOK)
			if got := f.request(`{"model":"provider/model","messages":[]}`).Code; got != 200 {
				t.Fatal(got)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			body := &publicScopeBody{Reader: strings.NewReader(`{"model":"[公益]care/model","messages":[]}`)}
			if state == "banned" {
				if _, err := f.store.DB().Exec(`UPDATE users SET is_banned=1 WHERE id=?`, f.userID); err != nil {
					t.Fatal(err)
				}
			} else if state == "deleted during body read" {
				body.beforeRead = func() {
					if _, err := f.store.DB().Exec(`DELETE FROM users WHERE id=?`, f.userID); err != nil {
						t.Fatal(err)
					}
				}
			} else {
				cancel()
			}
			r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", body).WithContext(ctx)
			r.Header.Set("Authorization", "Bearer nbk_test")
			w := httptest.NewRecorder()
			f.handler.ServeHTTP(&scopeRecorder{w}, r)
			if state == "banned" && w.Code != 401 || state == "deleted during body read" && w.Code != 429 {
				t.Fatalf("status=%d", w.Code)
			}
			if state != "deleted during body read" && body.reads != 0 {
				t.Fatal("ineligible account body was parsed")
			}
			var users, events int
			if err := f.store.DB().QueryRow(`SELECT (SELECT COUNT(*) FROM users WHERE auto_banned=1),(SELECT COUNT(*) FROM credit_operations)+(SELECT COUNT(*) FROM request_logs)`).Scan(&users, &events); err != nil {
				t.Fatal(err)
			}
			if users != 0 || events != 0 || f.forwardCalls.Load() != 1 {
				t.Fatal("retired or cancelled caller acquired new effects")
			}
		})
	}
}
