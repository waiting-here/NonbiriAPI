package logapi

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/auth"
	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/host"
	"github.com/waiting-here/NonbiriAPI/internal/observability"
)

type diagnosticReadFixture struct {
	*lifecycleLogDatabase
	reader    *sql.DB
	actor     context.Context
	final     *diagnosticTestFinal
	requestID string
}

type pressureReadFixture struct{}

func (pressureReadFixture) Snapshot() observability.PressureSnapshot {
	return observability.PressureSnapshot{ObservedAt: 1_800_000_000, RPM: observability.PressureMetric{Current: 3, Limit: 600}}
}

func newDiagnosticReadFixture(t *testing.T) diagnosticReadFixture {
	t.Helper()
	env := newLifecycleLogDatabase(t)
	const now = int64(1_800_000_000)
	clock := func() time.Time { return time.Unix(now, 0) }
	var sequence int
	var name, path string
	if err := env.store.DB().QueryRow(`PRAGMA database_list`).Scan(&sequence, &name, &path); err != nil {
		t.Fatal(err)
	}
	reader, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?mode=ro&_pragma=query_only(1)")
	if err != nil {
		t.Fatal(err)
	}
	reader.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = reader.Close() })
	env.repo, err = NewRepositoryWithReader(env.store.DB(), reader, env.vault, logReadAuthFunc(func(context.Context, *sql.Tx, string, int64) error { return nil }))
	if err != nil {
		t.Fatal(err)
	}
	env.repo.now = clock
	final := &diagnosticTestFinal{authority: authz.New(authz.Options{Now: clock})}
	runtime, err := auth.NewRuntime(auth.RuntimeConfig{Store: env.store, Provider: diagnosticTestProvider{}, DiscordClientID: "test-client", UserSiteBaseURL: "https://example.invalid", AdminUsername: "operator", AdminPassword: "fictional test password", CredentialKeyDeriver: env.vault, Authorizer: final.authority, Maintenance: diagnosticTestMaintenance{}, Now: clock})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	if err := RegisterAdminDiagnosticRoutes(runtime, env.repo, final); err != nil {
		t.Fatal(err)
	}
	handler := runtime.AdminHandler()
	login := httptest.NewRequest(http.MethodPost, "https://admin.example.invalid/admin/api/login", strings.NewReader(`{"username":"operator","password":"fictional test password"}`))
	login.Header.Set("Content-Type", "application/json")
	login = login.WithContext(host.WithStation(login.Context(), host.StationAdmin))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, login)
	if response.Code != http.StatusOK {
		t.Fatalf("login: %d %s", response.Code, response.Body.String())
	}
	request := httptest.NewRequest(http.MethodGet, "https://admin.example.invalid/admin/api/logs/diagnostic-capacity", nil)
	for _, cookie := range response.Result().Cookies() {
		request.AddCookie(cookie)
	}
	request = request.WithContext(host.WithStation(request.Context(), host.StationAdmin))
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || final.lastContext == nil {
		t.Fatalf("trusted actor: %d %s", response.Code, response.Body.String())
	}
	user := seedLifecycleLogUser(t, env.store.DB(), "70021", false)
	id := logOpaqueID("req_", 70021)
	insertLifecycleRequestLog(t, env.store.DB(), id, user, "success", 200, nil, now-1, now, 1, 0)
	return diagnosticReadFixture{env, reader, context.WithoutCancel(final.lastContext), final, id}
}

func (f diagnosticReadFixture) call(api *diagnosticAPI, ctx context.Context, route string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, "https://admin.example.invalid/diagnostic", nil).WithContext(ctx)
	request.SetPathValue("id", f.requestID)
	response := httptest.NewRecorder()
	actorID := int64(0)
	if api.steward != nil {
		actorID = 6
	}
	switch route {
	case "pressure":
		api.pressure(response, request, actorID, pressureReadFixture{})
	case "capacity":
		api.diagnosticCapacity(response, request, actorID)
	case "access":
		api.accessObservations(response, request, actorID, false)
	case "numbered_access":
		request.URL.RawQuery = "page=1&page_size=20"
		api.accessObservations(response, request, actorID, false)
	case "summary":
		api.accessObservations(response, request, actorID, true)
	case "source":
		api.diagnostic(response, request, actorID)
	}
	return response
}

func TestDiagnosticPureReadsAvoidWriterAndHeldSourceKeepsWriter(t *testing.T) {
	f := newDiagnosticReadFixture(t)
	ctx, cancel := context.WithTimeout(f.actor, 2*time.Second)
	defer cancel()
	writer, err := f.store.DB().Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	apis := []*diagnosticAPI{{repository: f.repo, admin: f.final}, {repository: f.repo, steward: allowLogStewardRead{}}}
	for _, api := range apis {
		for _, route := range []string{"pressure", "capacity", "access", "numbered_access", "summary"} {
			response := f.call(api, ctx, route)
			if response.Code != http.StatusOK {
				t.Fatalf("pure %s read waited for writer: %d %s", route, response.Code, response.Body.String())
			}
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.DB().Exec(`CREATE TABLE held_read_audit(request_log_id INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if err := f.repo.AttachAdminHeldReadAuthorizer(logHeldAuditWriter{}); err != nil {
		t.Fatal(err)
	}
	reader, err := f.reader.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	for _, api := range apis {
		// Expiration makes the steward path consult its held-read audit hook.
		f.repo.now = func() time.Time { return time.Unix(1_800_000_000+40*86400, 0) }
		response := f.call(api, ctx, "source")
		if response.Code != http.StatusOK {
			t.Fatalf("held source lost writer: %d %s", response.Code, response.Body.String())
		}
	}
	var count int
	if err := f.store.DB().QueryRow(`SELECT COUNT(*) FROM held_read_audit`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("held audit count=%d, %v", count, err)
	}
}

type diagnosticFailureAuthorizer struct {
	failure error
	after   func()
}

func (a diagnosticFailureAuthorizer) AuthorizeAdmin(context.Context, *sql.Tx, int64) error {
	if a.after != nil {
		a.after()
	}
	return a.failure
}

func (a diagnosticFailureAuthorizer) AuthorizeStewardRead(ctx context.Context, tx *sql.Tx, id int64) error {
	return a.AuthorizeAdmin(ctx, tx, id)
}

func TestDiagnosticReadsPreserveMFAAndContextFailures(t *testing.T) {
	f := newDiagnosticReadFixture(t)
	for _, role := range []string{"admin", "steward"} {
		for _, route := range []string{"pressure", "capacity", "access", "summary", "source"} {
			for _, failure := range []error{context.Canceled, context.DeadlineExceeded, authz.ErrMFARequired, authz.ErrForbidden} {
				t.Run(role+"/"+route+"/"+failure.Error(), func(t *testing.T) {
					authority := diagnosticFailureAuthorizer{failure: failure}
					api := &diagnosticAPI{repository: f.repo, admin: authority}
					if role == "steward" {
						api.steward = authority
					}
					response := f.call(api, f.actor, route)
					status, code := http.StatusServiceUnavailable, "service_unavailable"
					if errors.Is(failure, authz.ErrMFARequired) {
						status, code = http.StatusForbidden, "mfa_required"
					} else if errors.Is(failure, authz.ErrForbidden) {
						status, code = http.StatusForbidden, "forbidden"
					}
					if response.Code != status || !strings.Contains(response.Body.String(), `"code":"`+code+`"`) {
						t.Fatalf("failure classification: %d %s", response.Code, response.Body.String())
					}
				})
			}
			// Cancel after authorization succeeds to exercise SQL/transaction
			// error translation, rather than only pre-authorizer failures.
			ctx, cancel := context.WithCancel(f.actor)
			authority := diagnosticFailureAuthorizer{after: cancel}
			api := &diagnosticAPI{repository: f.repo, admin: authority}
			if role == "steward" {
				api.steward = authority
			}
			response := f.call(api, ctx, route)
			cancel()
			if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), `"code":"service_unavailable"`) {
				t.Fatalf("%s %s SQL cancellation: %d %s", role, route, response.Code, response.Body.String())
			}
		}
	}
}
