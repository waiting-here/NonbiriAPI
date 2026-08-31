package logapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type logUserTestRegistrar struct {
	handlers map[string]AuthorizedUserHandler
	failKey  string
}

func (registrar *logUserTestRegistrar) RegisterUserRoute(method, pattern string, handler AuthorizedUserHandler) error {
	if registrar.handlers == nil {
		registrar.handlers = make(map[string]AuthorizedUserHandler)
	}
	key := method + " " + pattern
	if key == registrar.failKey {
		return errors.New("registration refused")
	}
	if handler == nil {
		return errors.New("nil handler")
	}
	if _, exists := registrar.handlers[key]; exists {
		return errors.New("duplicate route")
	}
	registrar.handlers[key] = handler
	return nil
}

type logAdminTestRegistrar struct {
	handlers map[string]http.Handler
	failKey  string
}

func (registrar *logAdminTestRegistrar) RegisterAdminRoute(method, pattern string, handler http.Handler) error {
	if registrar.handlers == nil {
		registrar.handlers = make(map[string]http.Handler)
	}
	key := method + " " + pattern
	if key == registrar.failKey {
		return errors.New("registration refused")
	}
	if handler == nil {
		return errors.New("nil handler")
	}
	if _, exists := registrar.handlers[key]; exists {
		return errors.New("duplicate route")
	}
	registrar.handlers[key] = handler
	return nil
}

type logStewardTestAuthorizer struct {
	mu      sync.Mutex
	results []error
	calls   []int64
}

func (authorizer *logStewardTestAuthorizer) AuthorizeStewardRead(_ context.Context, tx *sql.Tx, userID int64) error {
	if tx == nil {
		return errors.New("missing final read transaction")
	}
	authorizer.mu.Lock()
	defer authorizer.mu.Unlock()
	authorizer.calls = append(authorizer.calls, userID)
	if len(authorizer.results) == 0 {
		return nil
	}
	result := authorizer.results[0]
	authorizer.results = authorizer.results[1:]
	return result
}

func (authorizer *logStewardTestAuthorizer) callCount() int {
	authorizer.mu.Lock()
	defer authorizer.mu.Unlock()
	return len(authorizer.calls)
}

func callLogUserHandler(t *testing.T, handler AuthorizedUserHandler, userID int64, target, body string, pathValues map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "https://user.example"+target, strings.NewReader(body))
	for key, value := range pathValues {
		request.SetPathValue(key, value)
	}
	recorder := httptest.NewRecorder()
	handler(recorder, request, UserPrincipal{UserID: userID})
	return recorder
}

func callLogAdminHandler(t *testing.T, handler http.Handler, target, body string, pathValues map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "https://admin.example"+target, strings.NewReader(body))
	for key, value := range pathValues {
		request.SetPathValue(key, value)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func TestLogRouteRegistrationClosedSetsAndFailures(t *testing.T) {
	fixture := newLogFixture(t)
	authorizer := &logStewardTestAuthorizer{}
	user := &logUserTestRegistrar{}
	if err := RegisterUserRoutes(user, fixture.repo); err != nil {
		t.Fatalf("RegisterUserRoutes: %v", err)
	}
	wantUser := []string{"GET /api/logs", "GET /api/logs/{id}"}
	if len(user.handlers) != len(wantUser) {
		t.Fatalf("user route count = %d", len(user.handlers))
	}
	for _, key := range wantUser {
		if user.handlers[key] == nil {
			t.Fatalf("missing user route %q", key)
		}
	}

	steward := &logUserTestRegistrar{}
	if err := RegisterStewardRoutes(steward, fixture.repo, authorizer); err != nil {
		t.Fatalf("RegisterStewardRoutes: %v", err)
	}
	wantSteward := []string{"GET /api/steward/logs", "GET /api/steward/logs/{id}"}
	if len(steward.handlers) != len(wantSteward) {
		t.Fatalf("steward route count = %d", len(steward.handlers))
	}
	for _, key := range wantSteward {
		if steward.handlers[key] == nil {
			t.Fatalf("missing steward route %q", key)
		}
	}
	for key := range steward.handlers {
		if strings.Contains(key, "export") {
			t.Fatalf("steward export route registered: %q", key)
		}
	}

	admin := &logAdminTestRegistrar{}
	if err := RegisterAdminRoutes(admin, fixture.repo); err != nil {
		t.Fatalf("RegisterAdminRoutes: %v", err)
	}
	wantAdmin := []string{
		"GET /admin/api/logs", "GET /admin/api/logs/{id}",
		"GET /admin/api/logs/export.csv", "GET /admin/api/logs/export.json",
	}
	if len(admin.handlers) != len(wantAdmin) {
		t.Fatalf("admin route count = %d", len(admin.handlers))
	}
	for _, key := range wantAdmin {
		if admin.handlers[key] == nil {
			t.Fatalf("missing admin route %q", key)
		}
	}

	if _, err := NewHTTPAPI(nil, nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("NewHTTPAPI nil repository = %v", err)
	}
	if err := RegisterUserRoutes(nil, fixture.repo); !errors.Is(err, ErrInvalid) {
		t.Fatalf("nil user registrar = %v", err)
	}
	if err := RegisterStewardRoutes(&logUserTestRegistrar{}, fixture.repo, nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("nil steward authorizer = %v", err)
	}
	if err := RegisterAdminRoutes(nil, fixture.repo); !errors.Is(err, ErrInvalid) {
		t.Fatalf("nil admin registrar = %v", err)
	}
	failing := &logAdminTestRegistrar{failKey: "GET /admin/api/logs/export.json"}
	if err := RegisterAdminRoutes(failing, fixture.repo); err == nil {
		t.Fatal("admin registration failure was ignored")
	}
}

func TestUserAndAdminHTTPPrivacyStrictQueriesAndExports(t *testing.T) {
	fixture := newLogFixture(t)
	user := &logUserTestRegistrar{}
	admin := &logAdminTestRegistrar{}
	if err := RegisterUserRoutes(user, fixture.repo); err != nil {
		t.Fatal(err)
	}
	if err := RegisterAdminRoutes(admin, fixture.repo); err != nil {
		t.Fatal(err)
	}

	list := callLogUserHandler(t, user.handlers["GET /api/logs"], logUserOne,
		"/api/logs?status=422&limit=1", "", nil)
	if list.Code != http.StatusOK || list.Header().Get("Cache-Control") != "no-store" ||
		list.Header().Get("Content-Type") != "application/json; charset=utf-8" {
		t.Fatalf("user list response = %d headers=%v body=%s", list.Code, list.Header(), list.Body.String())
	}
	noLogSentinel(t, list.Body.String(), "RAW-REQUEST-BODY", "RAW-AUTH", "RAW-UPSTREAM")

	self := callLogUserHandler(t, user.handlers["GET /api/logs/{id}"], logUserOne,
		"/api/logs/"+fixture.selfID, "", map[string]string{"id": fixture.selfID})
	if self.Code != http.StatusOK || !strings.Contains(self.Body.String(), `"attempts"`) {
		t.Fatalf("self detail = %d %s", self.Code, self.Body.String())
	}
	charity := callLogUserHandler(t, user.handlers["GET /api/logs/{id}"], logUserOne,
		"/api/logs/"+fixture.charityID, "", map[string]string{"id": fixture.charityID})
	if charity.Code != http.StatusOK || strings.Contains(charity.Body.String(), `"attempts"`) ||
		!strings.Contains(charity.Body.String(), `"caller_safe_result"`) {
		t.Fatalf("charity detail = %d %s", charity.Code, charity.Body.String())
	}
	foreign := callLogUserHandler(t, user.handlers["GET /api/logs/{id}"], logUserTwo,
		"/api/logs/"+fixture.selfID, "", map[string]string{"id": fixture.selfID})
	if foreign.Code != http.StatusNotFound {
		t.Fatalf("foreign detail = %d %s", foreign.Code, foreign.Body.String())
	}

	for _, target := range []string{
		"/api/logs?limit=01", "/api/logs?limit=1&limit=2", "/api/logs?unknown=1",
		"/api/logs?endpoint_base_url=https%3A%2F%2Fexample.com", "/api/logs?from=10&to=10",
	} {
		response := callLogUserHandler(t, user.handlers["GET /api/logs"], logUserOne, target, "", nil)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("strict user query %q = %d %s", target, response.Code, response.Body.String())
		}
	}
	withBody := callLogUserHandler(t, user.handlers["GET /api/logs"], logUserOne, "/api/logs", "x", nil)
	if withBody.Code != http.StatusBadRequest {
		t.Fatalf("GET body status = %d %s", withBody.Code, withBody.Body.String())
	}

	jsonExport := callLogAdminHandler(t, admin.handlers["GET /admin/api/logs/export.json"],
		"/admin/api/logs/export.json", "", nil)
	if jsonExport.Code != http.StatusOK || jsonExport.Header().Get("Content-Disposition") != `attachment; filename="nonbiri-logs.json"` ||
		jsonExport.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("JSON export = %d headers=%v body=%s", jsonExport.Code, jsonExport.Header(), jsonExport.Body.String())
	}
	csvExport := callLogAdminHandler(t, admin.handlers["GET /admin/api/logs/export.csv"],
		"/admin/api/logs/export.csv", "", nil)
	if csvExport.Code != http.StatusOK || csvExport.Header().Get("Content-Disposition") != `attachment; filename="nonbiri-logs.csv"` {
		t.Fatalf("CSV export = %d headers=%v body=%s", csvExport.Code, csvExport.Header(), csvExport.Body.String())
	}
	invalidExport := callLogAdminHandler(t, admin.handlers["GET /admin/api/logs/export.json"],
		"/admin/api/logs/export.json?limit=1", "", nil)
	if invalidExport.Code != http.StatusBadRequest {
		t.Fatalf("export limit = %d %s", invalidExport.Code, invalidExport.Body.String())
	}
	noLogSentinel(t, jsonExport.Body.String(), "RAW-REQUEST-BODY", "RAW-AUTH", "RAW-COOKIE",
		"RAW-DISCORD", "RAW-PRIVATE-NOTE", "RAW-CIPHERTEXT", "RAW-UPSTREAM", "SELF-LOGICAL-MODEL")
}

func TestSyntheticNullStatusHTTPProjectionForAllRoles(t *testing.T) {
	fixture := newLogFixture(t)
	user := &logUserTestRegistrar{}
	admin := &logAdminTestRegistrar{}
	steward := &logUserTestRegistrar{}
	if err := RegisterUserRoutes(user, fixture.repo); err != nil {
		t.Fatal(err)
	}
	if err := RegisterAdminRoutes(admin, fixture.repo); err != nil {
		t.Fatal(err)
	}
	if err := RegisterStewardRoutes(steward, fixture.repo, allowLogStewardRead{}); err != nil {
		t.Fatal(err)
	}

	userResponse := callLogUserHandler(t, user.handlers["GET /api/logs/{id}"], logUserOne,
		"/api/logs/"+fixture.selfID, "", map[string]string{"id": fixture.selfID})
	adminResponse := callLogAdminHandler(t, admin.handlers["GET /admin/api/logs/{id}"],
		"/admin/api/logs/"+fixture.selfID, "", map[string]string{"id": fixture.selfID})
	stewardResponse := callLogUserHandler(t, steward.handlers["GET /api/steward/logs/{id}"], 999,
		"/api/steward/logs/"+fixture.selfID, "", map[string]string{"id": fixture.selfID})
	for role, response := range map[string]*httptest.ResponseRecorder{
		"user": userResponse, "admin": adminResponse, "steward": stewardResponse,
	} {
		if response.Code != http.StatusOK {
			t.Fatalf("%s detail = %d %s", role, response.Code, response.Body.String())
		}
	}

	var userDetail UserSelfLogDetail
	if err := json.Unmarshal(userResponse.Body.Bytes(), &userDetail); err != nil || len(userDetail.Attempts.Data) != 2 ||
		userDetail.Attempts.Data[1].ResultKind != ResultSynthetic || userDetail.Attempts.Data[1].StatusCode != nil {
		t.Fatalf("user synthetic NULL = (%+v,%v)", userDetail, err)
	}
	var adminDetail AdminLogDetail
	if err := json.Unmarshal(adminResponse.Body.Bytes(), &adminDetail); err != nil || len(adminDetail.Attempts.Data) != 2 ||
		adminDetail.Attempts.Data[1].ResultKind != ResultSynthetic || adminDetail.Attempts.Data[1].StatusCode != nil {
		t.Fatalf("admin synthetic NULL = (%+v,%v)", adminDetail, err)
	}
	var stewardDetail StewardLogDetail
	if err := json.Unmarshal(stewardResponse.Body.Bytes(), &stewardDetail); err != nil || len(stewardDetail.Attempts.Data) != 2 ||
		stewardDetail.Attempts.Data[1].ResultKind != ResultSynthetic || stewardDetail.Attempts.Data[1].StatusCode != nil {
		t.Fatalf("steward synthetic NULL = (%+v,%v)", stewardDetail, err)
	}
}

func TestStewardAuthorizationRecheckedBeforeEveryRead(t *testing.T) {
	fixture := newLogFixture(t)
	authorizer := &logStewardTestAuthorizer{results: []error{
		errors.New("not level five"), nil, nil, nil, errors.New("session replaced"), ErrUnavailable,
	}}
	registrar := &logUserTestRegistrar{}
	if err := RegisterStewardRoutes(registrar, fixture.repo, authorizer); err != nil {
		t.Fatal(err)
	}
	handler := registrar.handlers["GET /api/steward/logs"]

	// Authorization wins even when the body/query is malformed, so an old
	// cached principal cannot observe or execute any part of a steward read.
	for index, call := range []struct {
		target string
		body   string
		status int
	}{
		{"/api/steward/logs?unknown=1", "x", http.StatusForbidden},
		{"/api/steward/logs?status=422", "", http.StatusOK},
		{"/api/steward/logs", "", http.StatusForbidden},
		{"/api/steward/logs", "", http.StatusServiceUnavailable},
	} {
		response := callLogUserHandler(t, handler, 999, call.target, call.body, nil)
		if response.Code != call.status {
			t.Fatalf("steward call %d = %d %s", index, response.Code, response.Body.String())
		}
		noLogSentinel(t, response.Body.String(), "SELF-LOGICAL-MODEL", "OWNER-ENDPOINT-NOTE",
			"RAW-REQUEST-BODY", "RAW-AUTH", "RAW-UPSTREAM")
	}
	if authorizer.callCount() != 6 {
		t.Fatalf("authorizer calls = %d, want 6", authorizer.callCount())
	}
}

func TestLogQueryParsersRejectUnknownDuplicateAndNonCanonicalValues(t *testing.T) {
	for _, test := range []struct {
		query  string
		role   string
		export bool
	}{
		{"bad=%zz", "user", false}, {"status=099", "user", false}, {"status=600", "admin", false},
		{"from=+1", "admin", false}, {"to=-1", "admin", false}, {"user_id=01", "admin", false},
		{"model=x", "admin", false}, {"user_id=1", "steward", false}, {"cursor=x", "admin", true},
		{"limit=1", "admin", true}, {"error_code=A", "user", false},
	} {
		filter, err := parseListFilter(test.query, test.role, test.export)
		if err == nil {
			_, err = normalizeListFilter(filter, test.role)
		}
		if !errors.Is(err, ErrInvalid) {
			t.Fatalf("parseListFilter(%q,%q,%v) = %+v, %v", test.query, test.role, test.export, filter, err)
		}
	}
	for _, query := range []string{
		"attempt_cursor=", "attempt_limit=0", "attempt_limit=01", "attempt_limit=101",
		"attempt_limit=1&attempt_limit=2", "cursor=x", "bad=%zz",
	} {
		if _, err := parseAttemptFilter(query); !errors.Is(err, ErrInvalid) {
			t.Fatalf("parseAttemptFilter(%q) = %v", query, err)
		}
	}
}
