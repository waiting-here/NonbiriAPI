package logapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/auth"
	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/host"
	"github.com/waiting-here/NonbiriAPI/internal/maintenance"
	"github.com/waiting-here/NonbiriAPI/internal/observability"
	"github.com/waiting-here/NonbiriAPI/internal/upstreamerror"
)

type refuseDiagnosticRead struct{}

func (refuseDiagnosticRead) AuthorizeStewardRead(context.Context, *sql.Tx, int64) error {
	return ErrForbidden
}

func TestManagementDiagnosticRoutesKeepUserDTOsClean(t *testing.T) {
	env := newLifecycleLogDatabase(t)
	database := env.store.DB()
	userID := seedLifecycleLogUser(t, database, "70019", false)
	now := int64(1_800_000_000)
	env.repo.now = func() time.Time { return time.Unix(now, 0) }
	id, err := db.GenerateOpaqueID("req_")
	if err != nil {
		t.Fatal(err)
	}
	insertLifecycleRequestLog(t, database, id, userID, "failed", 502, "upstream", now-1, now, 1, 0)
	observed, err := observability.NewRepository(database)
	if err != nil {
		t.Fatal(err)
	}
	ctx := observability.WithSource(context.Background(), observability.Source{EffectiveIP: "192.0.2.91", IPQuality: "direct_peer", UserAgent: "private-client"})
	tx, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err = observed.RecordSourceTx(ctx, tx, id, userID, "self", now); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	ctx = observed.ErrorScope(ctx, observability.DiagnosticRef{RequestID: id, AttemptSeq: 1})
	upstreamerror.CaptureEvent(ctx, 503, "application/json", []byte(`{"error":"private-original-payload"}`))
	clock := func() time.Time { return time.Unix(now, 0) }
	final := &diagnosticTestFinal{authority: authz.New(authz.Options{Now: clock})}
	runtime, err := auth.NewRuntime(auth.RuntimeConfig{Store: env.store, Provider: diagnosticTestProvider{}, DiscordClientID: "test-client", UserSiteBaseURL: "https://example.invalid", AdminUsername: "operator", AdminPassword: "fictional test password", CredentialKeyDeriver: env.vault, Authorizer: final.authority, Maintenance: diagnosticTestMaintenance{}, Now: clock})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	if err = RegisterAdminDiagnosticRoutes(runtime, env.repo, final); err != nil {
		t.Fatal(err)
	}
	handler := runtime.AdminHandler()
	login := httptest.NewRequest("POST", "https://admin.example.invalid/admin/api/login", strings.NewReader(`{"username":"operator","password":"fictional test password"}`))
	login.Header.Set("Content-Type", "application/json")
	login = login.WithContext(host.WithStation(login.Context(), host.StationAdmin))
	loginResponse := httptest.NewRecorder()
	handler.ServeHTTP(loginResponse, login)
	var cookie *http.Cookie
	for _, item := range loginResponse.Result().Cookies() {
		if item.Name == auth.AdminSessionCookieName {
			cookie = item
		}
	}
	if loginResponse.Code != 200 || cookie == nil {
		t.Fatalf("admin login failed: %d %s", loginResponse.Code, loginResponse.Body.String())
	}
	api := &diagnosticAPI{repository: env.repo, admin: final}
	request := httptest.NewRequest("GET", "https://admin.example.invalid/admin/api/logs/"+id+"/attempts/1/errors/1", nil)
	request.SetPathValue("id", id)
	request.SetPathValue("seq", "1")
	request.SetPathValue("event", "1")
	request.AddCookie(cookie)
	request = request.WithContext(host.WithStation(request.Context(), host.StationAdmin))
	writer := httptest.NewRecorder()
	handler.ServeHTTP(writer, request)
	request = request.WithContext(context.WithoutCancel(final.lastContext))
	var body observability.ErrorBody
	if writer.Code != 200 || json.Unmarshal(writer.Body.Bytes(), &body) != nil || body.Body != `{"error":"private-original-payload"}` {
		t.Fatalf("raw read: %d %s", writer.Code, writer.Body.String())
	}
	if writer.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("raw response can be cached")
	}
	userRequest := httptest.NewRequest("GET", "https://example.invalid/api/logs/"+id, nil)
	userRequest.SetPathValue("id", id)
	writer = httptest.NewRecorder()
	userAPI, _ := NewHTTPAPI(env.repo, nil)
	userAPI.userDetail(writer, userRequest, UserPrincipal{UserID: userID})
	if writer.Code != 200 {
		t.Fatalf("user read: %d %s", writer.Code, writer.Body.String())
	}
	for _, private := range []string{"private-original-payload", "private-client", "192.0.2.91", "source", "errors"} {
		if strings.Contains(writer.Body.String(), private) {
			t.Fatalf("user DTO exposed %s", private)
		}
	}
	denied := &diagnosticAPI{repository: env.repo, steward: refuseDiagnosticRead{}}
	writer = httptest.NewRecorder()
	denied.diagnostic(writer, request, userID)
	if writer.Code != 403 || strings.Contains(writer.Body.String(), "private-original") {
		t.Fatal("unauthorized management read")
	}
	env.repo.now = func() time.Time { return time.Unix(now+31*86400, 0) }
	writer = httptest.NewRecorder()
	api.diagnostic(writer, request, 0)
	if writer.Code != 404 {
		t.Fatalf("expired diagnostic visible: %d", writer.Code)
	}
	held := &requestLogHeldReadStub{allow: true}
	if err = env.repo.AttachAdminHeldReadAuthorizer(held); err != nil {
		t.Fatal(err)
	}
	writer = httptest.NewRecorder()
	api.diagnostic(writer, request, 0)
	if writer.Code != 200 || held.calls != 1 {
		t.Fatalf("held diagnostic inaccessible: %d", writer.Code)
	}
	missing := request.WithContext(context.Background())
	writer = httptest.NewRecorder()
	api.diagnostic(writer, missing, 0)
	if writer.Code != 403 {
		t.Fatal("missing administrator actor was accepted")
	}
	writer = httptest.NewRecorder()
	api.diagnostic(writer, request, userID)
	if writer.Code != 403 {
		t.Fatal("mismatched administrator identity was accepted")
	}
	actor, present := auth.ActorFromContext(request.Context())
	if !present {
		t.Fatal("real actor missing")
	}
	if _, err = database.Exec(`UPDATE sessions SET cred_gen='replacement' WHERE token_hash=?`, actor.SessionTokenHash); err != nil {
		t.Fatal(err)
	}
	writer = httptest.NewRecorder()
	api.diagnostic(writer, request, 0)
	if writer.Code != 403 {
		t.Fatal("changed session generation was accepted")
	}
	if _, err = database.Exec(`DELETE FROM sessions WHERE token_hash=?`, actor.SessionTokenHash); err != nil {
		t.Fatal(err)
	}
	writer = httptest.NewRecorder()
	api.diagnosticCapacity(writer, request, 0)
	if writer.Code != 403 {
		t.Fatal("revoked session read capacity")
	}
	writer = httptest.NewRecorder()
	api.accessObservations(writer, request, 0, true)
	if writer.Code != 403 {
		t.Fatal("revoked session read access observations")
	}
}

func TestAccessQueryClosedFilters(t *testing.T) {
	now := int64(1_800_000_000)
	for _, raw := range []string{"page_size=101", "status_class=6", "key_generation=3", "user_id=0", "from=1&to=2", "page_size=1&page_size=2", "unknown=2"} {
		if _, err := parseAccessFilter(raw, now); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	filter, err := parseAccessFilter("user_id=7&key_generation=0&status_class=4&path_kind=models&page_size=100", now)
	if err != nil || filter.KeyGeneration == nil || *filter.KeyGeneration != 0 || filter.StatusClass != 4 || filter.PathKind != "models" {
		t.Fatalf("filter %+v %v", filter, err)
	}
}

type diagnosticTestProvider struct{}

func (diagnosticTestProvider) AuthorizationURL(context.Context, auth.DiscordAuthorizeRequest) (string, error) {
	return "https://identity.example.invalid", nil
}
func (diagnosticTestProvider) Exchange(context.Context, string, string) (auth.DiscordLogin, error) {
	return auth.DiscordLogin{}, auth.ErrProviderUnauthorized
}

type diagnosticTestMaintenance struct{}

func (diagnosticTestMaintenance) State() (maintenance.State, bool) { return maintenance.State{}, true }

type diagnosticTestFinal struct {
	authority   *authz.Authorizer
	lastContext context.Context
}

func (f *diagnosticTestFinal) AuthorizeAdmin(ctx context.Context, tx *sql.Tx, userID int64) error {
	actor, present := auth.ActorFromContext(ctx)
	if !present || actor.Kind != authz.ActorAdminSession || actor.UserID != userID {
		return authz.ErrUnauthorized
	}
	f.lastContext = ctx
	_, err := f.authority.Authorize(ctx, tx, actor, authz.Requirement{Role: authz.RoleAdministrator})
	return err
}
