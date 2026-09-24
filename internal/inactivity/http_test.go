package inactivity

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/waiting-here/NonbiriAPI/internal/auth"
	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/host"
	"github.com/waiting-here/NonbiriAPI/internal/maintenance"
	"github.com/waiting-here/NonbiriAPI/internal/resources"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type testProvider struct{}

func (testProvider) AuthorizationURL(context.Context, auth.DiscordAuthorizeRequest) (string, error) {
	return "https://identity.example.invalid", nil
}
func (testProvider) Exchange(context.Context, string, string) (auth.DiscordLogin, error) {
	return auth.DiscordLogin{}, auth.ErrProviderUnauthorized
}

type testMaintenance struct{}

func (testMaintenance) State() (maintenance.State, bool) { return maintenance.State{}, true }

type sessionAuthority struct {
	authority *authz.Authorizer
	last      context.Context
}

func (a *sessionAuthority) AuthorizeAdmin(ctx context.Context, tx *sql.Tx, id int64) error {
	actor, ok := auth.ActorFromContext(ctx)
	if !ok || actor.Kind != authz.ActorAdminSession || actor.UserID != id {
		return authz.ErrUnauthorized
	}
	a.last = ctx
	_, err := a.authority.Authorize(ctx, tx, actor, authz.Requirement{Role: authz.RoleAdministrator})
	return err
}

func TestHTTPFinalSessionAuthorityRevisionAndReplay(t *testing.T) {
	e := fixture(t)
	user := e.user(t, "review", 6, 1000, 1000)
	clock := func() time.Time { return time.Unix(testNow, 0) }
	final := &sessionAuthority{authority: authz.New(authz.Options{Now: clock})}
	e.s.auth = final
	runtime, err := auth.NewRuntime(auth.RuntimeConfig{Store: e.store, Provider: testProvider{}, DiscordClientID: "test", UserSiteBaseURL: "https://example.invalid", AdminUsername: "operator", AdminPassword: "fictional test password", CredentialKeyDeriver: e.vault, Authorizer: final.authority, Maintenance: testMaintenance{}, Now: clock})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	if err = RegisterAdminRoutes(runtime, e.s); err != nil {
		t.Fatal(err)
	}
	var userContext context.Context
	if err = runtime.RegisterUserRoute("GET", "/api/inactivity-policy/status", func(w http.ResponseWriter, r *http.Request, _ resources.UserPrincipal) {
		userContext = context.WithoutCancel(r.Context())
		e.s.StatusHTTP(w, r)
	}); err != nil {
		t.Fatal(err)
	}
	handler := runtime.AdminHandler()
	login := httptest.NewRequest("POST", "https://admin.example.invalid/admin/api/login", strings.NewReader(`{"username":"operator","password":"fictional test password"}`))
	login.Header.Set("Content-Type", "application/json")
	login = login.WithContext(host.WithStation(login.Context(), host.StationAdmin))
	writer := httptest.NewRecorder()
	handler.ServeHTTP(writer, login)
	var cookie *http.Cookie
	for _, c := range writer.Result().Cookies() {
		if c.Name == auth.AdminSessionCookieName {
			cookie = c
		}
	}
	if writer.Code != 200 || cookie == nil {
		t.Fatal("login", writer.Code, writer.Body.String())
	}
	call := func(method, path, body, key string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "https://admin.example.invalid"+path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		if key != "" {
			r.Header.Set("Idempotency-Key", key)
		}
		r.AddCookie(cookie)
		r = r.WithContext(host.WithStation(r.Context(), host.StationAdmin))
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	got := call("GET", "/admin/api/inactivity-policy", "", "")
	if got.Code != 200 || got.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(got.Code, got.Body.String())
	}
	var initial Configuration
	if err = json.Unmarshal(got.Body.Bytes(), &initial); err != nil {
		t.Fatal(err)
	}
	input := Update{initial.Revision, testPolicy()}
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	key := "inactivity-policy-save-request-0001"
	first := call("PUT", "/admin/api/inactivity-policy", string(raw), key)
	again := call("PUT", "/admin/api/inactivity-policy", string(raw), key)
	if first.Code != 200 || again.Code != 200 || first.Body.String() != again.Body.String() {
		t.Fatal(first.Code, first.Body.String(), again.Code, again.Body.String())
	}
	if scalar(t, e.store.DB(), `SELECT count(*) FROM inactivity_audits WHERE action='configure'`) != 1 {
		t.Fatal("replay rewrote audit")
	}
	if conflict := call("PUT", "/admin/api/inactivity-policy", string(raw), key+"2"); conflict.Code != 409 {
		t.Fatal("stale revision accepted", conflict.Code)
	}
	var current Configuration
	if err = json.Unmarshal(first.Body.Bytes(), &current); err != nil {
		t.Fatal(err)
	}
	if current.DecayGraceUntil != testNow+graceSeconds {
		t.Fatal("enable grace missing")
	}
	input.ExpectedRevision = current.Revision
	previewRaw, _ := json.Marshal(PreviewInput{Update: input, Limit: 1})
	preview := call("POST", "/admin/api/inactivity-policy/preview", string(previewRaw), "")
	if preview.Code != 200 {
		t.Fatal(preview.Code, preview.Body.String())
	}
	if scalar(t, e.store.DB(), `SELECT count(*) FROM inactivity_runs`) != 0 {
		t.Fatal("preview executed policy")
	}
	auditPage := call("GET", "/admin/api/inactivity-policy/audits?page_size=1", "", "")
	if auditPage.Code != 200 || !strings.Contains(auditPage.Body.String(), `"next_cursor":"`) {
		t.Fatal("audit page missing", auditPage.Code, auditPage.Body.String())
	}
	for _, bad := range []string{`{"expected_revision":"2","policy":{},"extra":1}`, `{"expected_revision":"2","expected_revision":"2","policy":{}}`} {
		if w := call("PUT", "/admin/api/inactivity-policy", bad, key); w.Code != 400 {
			t.Fatal("invalid body accepted", w.Code)
		}
	}
	if w := call("GET", "/admin/api/inactivity-policy/runs?page_size=101", "", ""); w.Code != 400 {
		t.Fatal("unbounded page accepted")
	}
	ctx := context.WithoutCancel(final.last)
	actor, ok := auth.ActorFromContext(ctx)
	if !ok {
		t.Fatal("actor missing")
	}
	if _, err = e.s.Get(context.Background()); !errors.Is(err, ErrForbidden) {
		t.Fatal("missing admin accepted", err)
	}
	if _, err = e.store.DB().Exec(`UPDATE sessions SET cred_gen='replaced' WHERE token_hash=?`, actor.SessionTokenHash); err != nil {
		t.Fatal(err)
	}
	if _, err = e.s.Get(ctx); !errors.Is(err, ErrForbidden) {
		t.Fatal("replaced session accepted", err)
	}
	if _, err = e.store.DB().Exec(`UPDATE sessions SET cred_gen=?,user_id=? WHERE token_hash=?`, actor.SessionGeneration, user, actor.SessionTokenHash); err != nil {
		t.Fatal(err)
	}
	if _, err = e.s.Put(ctx, input, key); !errors.Is(err, ErrForbidden) {
		t.Fatal("mismatched admin accepted", err)
	}
	if _, err = e.store.DB().Exec(`DELETE FROM sessions WHERE token_hash=?`, actor.SessionTokenHash); err != nil {
		t.Fatal(err)
	}
	if _, err = e.s.Runs(ctx, 0, 100); !errors.Is(err, ErrForbidden) {
		t.Fatal("revoked admin accepted", err)
	}
	if _, err = e.s.Audits(ctx, 0, 100); !errors.Is(err, ErrForbidden) {
		t.Fatal("revoked admin read policy audit", err)
	}
	token := strings.Repeat("A", 43)
	hash := sha256.Sum256([]byte(token))
	if _, err = e.store.DB().Exec(`INSERT INTO sessions(token_hash,user_id,last_seen_at,expires_at,absolute_expires_at,created_at,cred_gen) VALUES(?,?,?,?,?,?,?)`, hex.EncodeToString(hash[:]), user, testNow, testNow+600, testNow+1200, testNow, "test-generation"); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("GET", "https://example.invalid/api/inactivity-policy/status", nil)
	request.AddCookie(&http.Cookie{Name: auth.UserSessionCookieName, Value: token})
	request = request.WithContext(host.WithStation(request.Context(), host.StationUser))
	response := httptest.NewRecorder()
	runtime.UserHandler().ServeHTTP(response, request)
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"exempt_reason":"steward"`) {
		t.Fatal(response.Code, response.Body.String())
	}
	if _, err = e.s.Get(userContext); !errors.Is(err, ErrForbidden) {
		t.Fatal("level 6 edited policy", err)
	}
	if scalar(t, e.store.DB(), `SELECT activity_seq FROM user_activity_state WHERE user_id=?`, user) != 0 {
		t.Fatal("read refreshed activity")
	}
}
