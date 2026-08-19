package logapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/waiting-here/NonbiriAPI/internal/auth"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/host"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

// stubProvider satisfies auth.DiscordProvider for building a real UserAuth
// service; the tests never exchange an OAuth code.
type stubProvider struct{}

func (stubProvider) AuthorizationURL(_ context.Context, _ auth.DiscordAuthorizeRequest) (string, error) {
	return "https://discord.example/authorize", nil
}

func (stubProvider) Exchange(_ context.Context, _, _ string) (auth.DiscordLogin, error) {
	return auth.DiscordLogin{}, fmt.Errorf("stub provider: no exchange")
}

type testEnv struct {
	store *db.Store
	user  *db.User
	admin *db.User
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	key := bytes.Repeat([]byte{0x5c}, secret.MasterKeyBytes)
	vault, err := secret.New(key)
	if err != nil {
		t.Fatalf("secret.New: %v", err)
	}
	t.Cleanup(func() { _ = vault.Close() })
	store, err := db.Open(filepath.Join(t.TempDir(), "logapi.db"), vault)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	user, err := store.CreateUser("discord-u1", "user1", "")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	admin, err := store.EnsureAdminUser("admin")
	if err != nil {
		t.Fatalf("EnsureAdminUser: %v", err)
	}
	return &testEnv{store: store, user: user, admin: admin}
}

func seedSecondUser(t *testing.T, env *testEnv) *db.User {
	t.Helper()
	user, err := env.store.CreateUser("discord-u2", "user2", "")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	return user
}

// seedLog records one metadata-only accounted request through the real
// repository path.
func seedLog(t *testing.T, env *testEnv, userID int64, attempt, model string, status int, unix int64, diag string) {
	t.Helper()
	input := db.RequestLogInput{
		AttemptID:        attempt,
		UserID:           userID,
		Model:            model,
		StatusCode:       status,
		DurationMs:       7,
		StartedAt:        time.Unix(unix, 0).UTC(),
		CompletedAt:      time.Unix(unix, 0).UTC().Add(7 * time.Millisecond),
		PromptTokens:     1,
		CompletionTokens: 2,
		TotalTokens:      3,
	}
	if diag != "" {
		input.ErrorCode = "upstream"
		input.ErrorDiag = diag
	}
	if err := env.store.RecordRequest(context.Background(), input); err != nil {
		t.Fatalf("RecordRequest(%s): %v", attempt, err)
	}
}

// --- mount helpers (real session middlewares) ------------------------------

func newUserMount(t *testing.T, env *testEnv) http.Handler {
	t.Helper()
	userAuth, err := auth.NewUserAuth(auth.UserAuthConfig{
		Store: env.store, Provider: stubProvider{}, ClientID: "client-id",
		SiteBaseURL: "https://example.com",
	})
	if err != nil {
		t.Fatalf("NewUserAuth: %v", err)
	}
	return auth.UserSessionMiddleware(userAuth, NewHandler(HandlerDeps{Store: env.store}))
}

func newAdminMount(t *testing.T, env *testEnv) http.Handler {
	t.Helper()
	adminAuth, err := auth.NewAdminAuth(auth.AdminAuthConfig{
		Store: env.store, Username: "admin", Password: "admin-pw", SiteBaseURL: "https://example.com",
	})
	if err != nil {
		t.Fatalf("NewAdminAuth: %v", err)
	}
	t.Cleanup(func() { _ = adminAuth.Close() })
	return auth.AdminSessionMiddleware(adminAuth, NewHandler(HandlerDeps{Store: env.store}))
}

func newCallerKeyMount(t *testing.T, env *testEnv) http.Handler {
	t.Helper()
	return auth.CallerKeyMiddleware(env.store, NewHandler(HandlerDeps{Store: env.store}))
}

// --- request / response helpers --------------------------------------------

func stationRequest(method, target string, station host.Station) *http.Request {
	r := httptest.NewRequest(method, target, nil)
	r.RemoteAddr = "198.51.100.20:4000"
	return r.WithContext(host.WithStation(r.Context(), station))
}

func do(t *testing.T, h http.Handler, r *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func userCookie(t *testing.T, env *testEnv) *http.Cookie {
	t.Helper()
	token, _, err := env.store.CreateUserSession(env.user.ID)
	if err != nil {
		t.Fatalf("CreateUserSession: %v", err)
	}
	return &http.Cookie{Name: auth.UserSessionCookieName, Value: token}
}

func adminCookie(t *testing.T, env *testEnv) *http.Cookie {
	t.Helper()
	token, _, err := env.store.CreateAdminSession(env.admin.ID)
	if err != nil {
		t.Fatalf("CreateAdminSession: %v", err)
	}
	return &http.Cookie{Name: auth.AdminSessionCookieName, Value: token}
}

func assertNoStore(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
}

func assertOK(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	assertNoStore(t, rec)
}

func assertErr(t *testing.T, rec *httptest.ResponseRecorder, wantStatus int, wantCode string) {
	t.Helper()
	if rec.Code != wantStatus {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, wantStatus, rec.Body.String())
	}
	assertNoStore(t, rec)
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("error envelope: %v; body=%s", err, rec.Body.String())
	}
	if env.Error.Code != wantCode {
		t.Errorf("error code = %q, want %q", env.Error.Code, wantCode)
	}
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func assertJSONKeys(t *testing.T, body []byte, wantKeys ...string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("decode json: %v; body=%s", err, body)
	}
	if len(m) != len(wantKeys) {
		t.Fatalf("json keys = %v, want %v; body=%s", keysOf(m), wantKeys, body)
	}
	for _, k := range wantKeys {
		if _, ok := m[k]; !ok {
			t.Fatalf("json key %q missing; keys=%v", k, keysOf(m))
		}
	}
	return m
}

// decodeLogs unpacks the {data, has_more} log-list envelope.
func decodeLogs(t *testing.T, rec *httptest.ResponseRecorder) ([]map[string]any, bool) {
	t.Helper()
	var body struct {
		Data    []map[string]any `json:"data"`
		HasMore bool             `json:"has_more"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode logs: %v; body=%s", err, rec.Body.String())
	}
	return body.Data, body.HasMore
}

// --- user usage -------------------------------------------------------------

func TestMeUsageAuthIsolation(t *testing.T) {
	env := newTestEnv(t)
	seedLog(t, env, env.user.ID, "attempt-me-1", "p1/m1", 200, 1700000001, "")
	raw := NewHandler(HandlerDeps{Store: env.store})

	// No identity at all.
	rec := do(t, raw, stationRequest(http.MethodGet, "/api/me/usage", host.StationUser))
	assertErr(t, rec, http.StatusUnauthorized, httperr.CodeUnauthorized)

	// The handler never reads a session cookie by itself (middleware absent).
	r := stationRequest(http.MethodGet, "/api/me/usage", host.StationUser)
	r.AddCookie(userCookie(t, env))
	rec = do(t, raw, r)
	assertErr(t, rec, http.StatusUnauthorized, httperr.CodeUnauthorized)

	// A header-carried user id never authorizes.
	r = stationRequest(http.MethodGet, "/api/me/usage", host.StationUser)
	r.Header.Set("X-User-Id", fmt.Sprintf("%d", env.user.ID))
	rec = do(t, raw, r)
	assertErr(t, rec, http.StatusUnauthorized, httperr.CodeUnauthorized)

	// A caller-key principal is not a user session.
	generation, err := env.store.RegenerateCallerKey(env.user.ID)
	if err != nil {
		t.Fatalf("RegenerateCallerKey: %v", err)
	}
	r = stationRequest(http.MethodGet, "/api/me/usage", host.StationUser)
	r.Header.Set("Authorization", "Bearer "+generation.Secret)
	rec = do(t, newCallerKeyMount(t, env), r)
	assertErr(t, rec, http.StatusUnauthorized, httperr.CodeUnauthorized)

	// An admin session cannot reach the user route (admin middleware station
	// gate on the user station).
	r = stationRequest(http.MethodGet, "/api/me/usage", host.StationUser)
	r.AddCookie(adminCookie(t, env))
	rec = do(t, newAdminMount(t, env), r)
	assertErr(t, rec, http.StatusForbidden, httperr.CodeForbidden)

	// Wrong station context is refused before any identity is consulted.
	rec = do(t, raw, stationRequest(http.MethodGet, "/api/me/usage", host.StationAdmin))
	assertErr(t, rec, http.StatusForbidden, httperr.CodeForbidden)
}

func TestMeUsageShape(t *testing.T) {
	env := newTestEnv(t)
	seedLog(t, env, env.user.ID, "attempt-shape-1", "p1/m1", 200, 1700000002, "")
	h := newUserMount(t, env)

	r := stationRequest(http.MethodGet, "/api/me/usage", host.StationUser)
	r.AddCookie(userCookie(t, env))
	rec := do(t, h, r)
	assertOK(t, rec)

	body := assertJSONKeys(t, rec.Body.Bytes(), "total_requests", "total_prompt_tokens", "total_completion_tokens", "total_unknown_usage_requests")
	for key, want := range map[string]float64{
		"total_requests":               1,
		"total_prompt_tokens":          1,
		"total_completion_tokens":      2,
		"total_unknown_usage_requests": 0,
	} {
		if got, ok := body[key].(float64); !ok || got != want {
			t.Errorf("%s = %v, want %v", key, body[key], want)
		}
	}

	// A second request accumulates server-side.
	seedLog(t, env, env.user.ID, "attempt-shape-2", "p1/m1", 200, 1700000003, "")
	rec = do(t, h, r)
	assertOK(t, rec)
	body = assertJSONKeys(t, rec.Body.Bytes(), "total_requests", "total_prompt_tokens", "total_completion_tokens", "total_unknown_usage_requests")
	if body["total_requests"].(float64) != 2 {
		t.Errorf("total_requests after second log = %v, want 2", body["total_requests"])
	}
}

// --- admin logs -------------------------------------------------------------

func TestAdminLogsAuthIsolation(t *testing.T) {
	env := newTestEnv(t)
	seedLog(t, env, env.user.ID, "attempt-log-auth-1", "p1/m1", 200, 1700000010, "")
	raw := NewHandler(HandlerDeps{Store: env.store})

	// No identity.
	rec := do(t, raw, stationRequest(http.MethodGet, "/admin/api/logs", host.StationAdmin))
	assertErr(t, rec, http.StatusUnauthorized, httperr.CodeUnauthorized)

	// User-station context is refused (the edge would have blocked the path).
	rec = do(t, raw, stationRequest(http.MethodGet, "/admin/api/logs", host.StationUser))
	assertErr(t, rec, http.StatusForbidden, httperr.CodeForbidden)

	// A valid user session mounted on the admin station is refused before any
	// row is read.
	userAuth, err := auth.NewUserAuth(auth.UserAuthConfig{
		Store: env.store, Provider: stubProvider{}, ClientID: "client-id",
		SiteBaseURL: "https://example.com",
	})
	if err != nil {
		t.Fatalf("NewUserAuth: %v", err)
	}
	userMounted := auth.UserSessionMiddleware(userAuth, NewHandler(HandlerDeps{Store: env.store}))
	r := stationRequest(http.MethodGet, "/admin/api/logs", host.StationAdmin)
	r.AddCookie(userCookie(t, env))
	rec = do(t, userMounted, r)
	assertErr(t, rec, http.StatusForbidden, httperr.CodeForbidden)

	// A real admin session succeeds.
	r = stationRequest(http.MethodGet, "/admin/api/logs", host.StationAdmin)
	r.AddCookie(adminCookie(t, env))
	rec = do(t, newAdminMount(t, env), r)
	assertOK(t, rec)
}

func TestAdminLogsFilters(t *testing.T) {
	env := newTestEnv(t)
	user2 := seedSecondUser(t, env)
	// user1: A(200,p1/m1,100), B(429,p1/m1,200), C(200,p2/m2,300); user2: D(200,p1/m1,400)
	seedLog(t, env, env.user.ID, "attempt-f-a", "p1/m1", 200, 1700000100, "")
	seedLog(t, env, env.user.ID, "attempt-f-b", "p1/m1", 429, 1700000200, "")
	seedLog(t, env, env.user.ID, "attempt-f-c", "p2/m2", 200, 1700000300, "")
	seedLog(t, env, user2.ID, "attempt-f-d", "p1/m1", 200, 1700000400, "")

	h := newAdminMount(t, env)

	get := func(query string) []map[string]any {
		t.Helper()
		r := stationRequest(http.MethodGet, "/admin/api/logs"+query, host.StationAdmin)
		r.AddCookie(adminCookie(t, env))
		rec := do(t, h, r)
		assertOK(t, rec)
		rows, _ := decodeLogs(t, rec)
		return rows
	}
	ids := func(rows []map[string]any) []int64 {
		out := make([]int64, 0, len(rows))
		for _, row := range rows {
			out = append(out, int64(row["id"].(float64)))
		}
		return out
	}
	all := func(rows []map[string]any) bool { return len(rows) == 4 && len(ids(rows)) == 4 }

	// No filter: newest first across users.
	rows := get("")
	if !all(rows) || ids(rows)[0] != 4 || ids(rows)[3] != 1 {
		t.Fatalf("unfiltered rows = %v", ids(rows))
	}

	// user_id filter never leaks the other user's rows.
	rows = get(fmt.Sprintf("?user_id=%d", env.user.ID))
	if len(rows) != 3 {
		t.Fatalf("user filter rows = %d, want 3", len(rows))
	}
	for _, row := range rows {
		if int64(row["user_id"].(float64)) != env.user.ID {
			t.Errorf("cross-user row leaked: %v", row)
		}
	}
	rows = get(fmt.Sprintf("?user_id=%d", user2.ID))
	if len(rows) != 1 || ids(rows)[0] != 4 {
		t.Fatalf("user2 filter rows = %v", ids(rows))
	}

	// model filter is an exact match.
	rows = get("?model=p1%2Fm1")
	if len(rows) != 3 || !idsContains(ids(rows), 1, 2, 4) {
		t.Fatalf("model filter rows = %v", ids(rows))
	}

	// status filter.
	rows = get("?status=429")
	if len(rows) != 1 || ids(rows)[0] != 2 {
		t.Fatalf("status filter rows = %v", ids(rows))
	}

	// time range: started_at >= from, started_at < to.
	rows = get("?from=1700000200")
	if len(rows) != 3 || !idsContains(ids(rows), 2, 3, 4) {
		t.Fatalf("from filter rows = %v", ids(rows))
	}
	rows = get("?to=1700000300")
	if len(rows) != 2 || !idsContains(ids(rows), 1, 2) {
		t.Fatalf("to filter rows = %v", ids(rows))
	}
	rows = get("?from=1700000200&to=1700000300")
	if len(rows) != 1 || ids(rows)[0] != 2 {
		t.Fatalf("range filter rows = %v", ids(rows))
	}

	// Combined filters.
	rows = get(fmt.Sprintf("?user_id=%d&model=p1%%2Fm1&status=200", env.user.ID))
	if len(rows) != 1 || ids(rows)[0] != 1 {
		t.Fatalf("combined filter rows = %v", ids(rows))
	}
}

func idsContains(ids []int64, want ...int64) bool {
	for _, w := range want {
		found := false
		for _, id := range ids {
			if id == w {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return len(ids) == len(want)
}

func TestAdminLogsPagination(t *testing.T) {
	env := newTestEnv(t)
	base := int64(1700001000)
	for i := 1; i <= 7; i++ {
		seedLog(t, env, env.user.ID, fmt.Sprintf("attempt-pag-%d", i), "p1/m1", 200, base+int64(i), "")
	}
	h := newAdminMount(t, env)

	get := func(query string) []int64 {
		t.Helper()
		r := stationRequest(http.MethodGet, "/admin/api/logs"+query, host.StationAdmin)
		r.AddCookie(adminCookie(t, env))
		rec := do(t, h, r)
		assertOK(t, rec)
		rows, _ := decodeLogs(t, rec)
		out := make([]int64, 0, len(rows))
		for _, row := range rows {
			out = append(out, int64(row["id"].(float64)))
		}
		return out
	}

	// Offset walk with page_size=3 covers every row exactly once, newest first.
	var walked []int64
	for n := 1; ; n++ {
		page := get(fmt.Sprintf("?page=%d&page_size=3", n))
		if len(page) == 0 {
			break
		}
		for i := 1; i < len(page); i++ {
			if page[i] >= page[i-1] {
				t.Fatalf("page not strictly descending: %v", page)
			}
		}
		walked = append(walked, page...)
		if len(page) < 3 {
			break
		}
	}
	if len(walked) != 7 || !idsContains(walked, 1, 2, 3, 4, 5, 6, 7) {
		t.Fatalf("walked ids = %v, want all 7 exactly once", walked)
	}

	// page_size=1000 clamps to the page bound and returns the full first page.
	if page := get("?page_size=1000"); len(page) != 7 {
		t.Fatalf("page_size=1000 page = %v", page)
	}
}

func TestAdminLogsPageLimitBound(t *testing.T) {
	env := newTestEnv(t)
	for i := 0; i < 205; i++ {
		seedLog(t, env, env.user.ID, fmt.Sprintf("attempt-bulk-%d", i), "p1/m1", 200, 1700002000+int64(i), "")
	}
	h := newAdminMount(t, env)
	r := stationRequest(http.MethodGet, "/admin/api/logs?page_size=1000", host.StationAdmin)
	r.AddCookie(adminCookie(t, env))
	rec := do(t, h, r)
	assertOK(t, rec)
	rows, hasMore := decodeLogs(t, rec)
	if len(rows) != db.MaxLogPageLimit || !hasMore {
		t.Fatalf("page rows = %d hasMore=%v, want %d/true", len(rows), hasMore, db.MaxLogPageLimit)
	}
}

func TestAdminLogsInvalidParams(t *testing.T) {
	env := newTestEnv(t)
	seedLog(t, env, env.user.ID, "attempt-param-1", "p1/m1", 200, 1700000050, "")
	h := newAdminMount(t, env)

	request := func(query string) *httptest.ResponseRecorder {
		t.Helper()
		r := stationRequest(http.MethodGet, "/admin/api/logs"+query, host.StationAdmin)
		r.AddCookie(adminCookie(t, env))
		return do(t, h, r)
	}

	bad := []string{
		"?unknown=1",
		"?user_id=1&user_id=2",
		"?user_id=abc",
		"?user_id=0",
		"?user_id=-3",
		"?user_id=1%20OR%201=1",
		"?status=99",
		"?status=600",
		"?status=abc",
		"?from=-1",
		"?from=abc",
		"?from=20&to=10",
		"?to=abc",
		"?page=0",
		"?page=-1",
		"?page=abc",
		"?page=1&page=2",
		"?page_size=0",
		"?page_size=-1",
		"?page_size=abc",
		"?page_size=1&page_size=2",
		"?model=",
		"?model=ab%0Acd",
		"?model=" + strings.Repeat("x", 600),
		"?model=" + strings.Repeat("x", 9000), // RawQuery over the bound
	}
	for _, query := range bad {
		rec := request(query)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("query %q: status = %d, want 400; body=%s", query, rec.Code, rec.Body.String())
			continue
		}
		assertErr(t, rec, http.StatusBadRequest, httperr.CodeInvalidRequest)
	}

	// Injection-shaped values are parameterized: never an error, never rows.
	rec := request("?model=p1%2Fm1%27%20OR%201%3D1--")
	assertOK(t, rec)
	if strings.Contains(rec.Body.String(), "1=1") {
		t.Fatalf("injection leaked into SQL or response: %s", rec.Body.String())
	}
	rows, _ := decodeLogs(t, rec)
	if len(rows) != 0 {
		t.Fatalf("injection model matched rows: %v", rows)
	}
}

func TestAdminLogsDiagBounded(t *testing.T) {
	env := newTestEnv(t)
	// A hostile/broken diagnostic far beyond the bound, with control
	// characters and invalid UTF-8; the repository bounds and sanitizes it.
	rawDiag := strings.Repeat("x", 9000) + "\n\t\r" + string([]byte{0xff, 0xfe}) + strings.Repeat("y", 9000)
	seedLog(t, env, env.user.ID, "attempt-diag-1", "p1/m1", 502, 1700000060, rawDiag)

	h := newAdminMount(t, env)
	r := stationRequest(http.MethodGet, "/admin/api/logs", host.StationAdmin)
	r.AddCookie(adminCookie(t, env))
	rec := do(t, h, r)
	assertOK(t, rec)

	rows, _ := decodeLogs(t, rec)
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	diag, _ := rows[0]["error_diag"].(string)
	if len(diag) > 4096 {
		t.Errorf("error_diag length = %d, want <= 4096", len(diag))
	}
	if !utf8.ValidString(diag) {
		t.Errorf("error_diag is not valid UTF-8")
	}
	if strings.ContainsAny(diag, "\r\n\t") {
		t.Errorf("error_diag contains line-forgery control characters")
	}
	if rows[0]["error_code"] != "upstream" {
		t.Errorf("error_code = %v, want upstream", rows[0]["error_code"])
	}
}

// --- admin usage ------------------------------------------------------------

func TestAdminUsageSiteAndByUser(t *testing.T) {
	env := newTestEnv(t)
	user2 := seedSecondUser(t, env)
	seedLog(t, env, env.user.ID, "attempt-u-a", "p1/m1", 200, 1700000100, "")
	seedLog(t, env, env.user.ID, "attempt-u-b", "p1/m1", 429, 1700000101, "")
	seedLog(t, env, user2.ID, "attempt-u-c", "p2/m2", 200, 1700000102, "")
	// The administrator's own accounted row must never enter aggregates.
	seedLog(t, env, env.admin.ID, "attempt-u-admin", "p-admin/m", 200, 1700000103, "")

	h := newAdminMount(t, env)
	get := func(query string) *httptest.ResponseRecorder {
		t.Helper()
		r := stationRequest(http.MethodGet, "/admin/api/usage"+query, host.StationAdmin)
		r.AddCookie(adminCookie(t, env))
		return do(t, h, r)
	}

	// Site-wide totals exclude the administrator row.
	rec := get("")
	assertOK(t, rec)
	body := assertJSONKeys(t, rec.Body.Bytes(), "total_requests", "total_prompt_tokens", "total_completion_tokens", "total_unknown_usage_requests")
	if body["total_requests"].(float64) != 3 {
		t.Errorf("site total_requests = %v, want 3 (admin row excluded)", body["total_requests"])
	}
	if body["total_prompt_tokens"].(float64) != 3 || body["total_completion_tokens"].(float64) != 6 || body["total_unknown_usage_requests"].(float64) != 0 {
		t.Errorf("site totals = %v", body)
	}

	// Explicit group_by=site keeps the same shape.
	rec = get("?group_by=site")
	assertOK(t, rec)
	assertJSONKeys(t, rec.Body.Bytes(), "total_requests", "total_prompt_tokens", "total_completion_tokens", "total_unknown_usage_requests")

	// By-user: ordered by total requests descending, administrator excluded.
	rec = get("?group_by=user")
	assertOK(t, rec)
	var byUser struct {
		Users []map[string]any `json:"users"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &byUser); err != nil {
		t.Fatalf("decode by-user usage: %v", err)
	}
	if len(byUser.Users) != 2 {
		t.Fatalf("by-user rows = %d, want 2: %+v", len(byUser.Users), byUser.Users)
	}
	first, second := byUser.Users[0], byUser.Users[1]
	if int64(first["user_id"].(float64)) != env.user.ID || first["total_requests"].(float64) != 2 {
		t.Errorf("first by-user row = %v", first)
	}
	if int64(second["user_id"].(float64)) != user2.ID || second["total_requests"].(float64) != 1 {
		t.Errorf("second by-user row = %v", second)
	}
	if _, ok := first["username"]; ok {
		t.Errorf("by-user row carries an unexpected field: %v", first)
	}

	// limit clamps into [1, MaxUsageByUserLimit].
	rec = get("?group_by=user&limit=1")
	assertOK(t, rec)
	if err := json.Unmarshal(rec.Body.Bytes(), &byUser); err != nil {
		t.Fatalf("decode limited by-user usage: %v", err)
	}
	if len(byUser.Users) != 1 {
		t.Fatalf("limited by-user rows = %d, want 1", len(byUser.Users))
	}

	// Invalid selectors.
	for _, query := range []string{"?group_by=bogus", "?group_by=user&limit=abc", "?group_by=user&group_by=site", "?foo=1"} {
		rec := get(query)
		assertErr(t, rec, http.StatusBadRequest, httperr.CodeInvalidRequest)
	}
}

// --- admin overview ---------------------------------------------------------

func seedEndpointRows(t *testing.T, env *testEnv, userID int64, baseURL string, keyCount int) int64 {
	t.Helper()
	now := time.Now().Unix()
	res, err := env.store.DB().Exec(`INSERT INTO endpoints (user_id, connector_type, base_url, note, enabled, created_at, updated_at) VALUES (?, 'openai-compatible', ?, 'note '||?, 1, ?, ?)`,
		userID, baseURL, baseURL, now, now)
	if err != nil {
		t.Fatalf("seed endpoint: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("seed endpoint last id: %v", err)
	}
	for i := 0; i < keyCount; i++ {
		if _, err := env.store.DB().Exec(`INSERT INTO endpoint_keys (endpoint_id, encrypted_secret, display_head, display_tail, created_at, updated_at) VALUES (?, 'nbsec:v1:aes-256-gcm:test-secret', 'abcd', 'wxyz', ?, ?)`,
			id, now, now); err != nil {
			t.Fatalf("seed endpoint key: %v", err)
		}
	}
	return id
}

func TestAdminOverviewEndpoints(t *testing.T) {
	env := newTestEnv(t)
	user2 := seedSecondUser(t, env)
	e1 := seedEndpointRows(t, env, env.user.ID, "https://one.example", 2)
	e2 := seedEndpointRows(t, env, user2.ID, "https://two.example", 1)
	e3 := seedEndpointRows(t, env, env.user.ID, "https://three.example", 0)

	h := newAdminMount(t, env)
	r := stationRequest(http.MethodGet, "/admin/api/overview/endpoints", host.StationAdmin)
	r.AddCookie(adminCookie(t, env))
	rec := do(t, h, r)
	assertOK(t, rec)

	var rows []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
		t.Fatalf("decode overview: %v; body=%s", err, rec.Body.String())
	}
	if len(rows) != 3 {
		t.Fatalf("overview rows = %d, want 3: %+v", len(rows), rows)
	}
	want := []struct {
		id       int64
		userID   int64
		keyCount float64
		enabled  bool
	}{
		{id: e1, userID: env.user.ID, keyCount: 2, enabled: true},
		{id: e2, userID: user2.ID, keyCount: 1, enabled: true},
		{id: e3, userID: env.user.ID, keyCount: 0, enabled: true},
	}
	for i, row := range rows {
		if int64(row["id"].(float64)) != want[i].id || int64(row["user_id"].(float64)) != want[i].userID ||
			row["key_count"].(float64) != want[i].keyCount || row["enabled"].(bool) != want[i].enabled ||
			row["connector_type"] != "openai-compatible" || row["base_url"] == "" || row["note"] == "" ||
			row["created_at"].(float64) == 0 || row["updated_at"].(float64) == 0 {
			t.Errorf("row %d = %v", i, row)
		}
	}

	// No secret, ciphertext, or display fragment anywhere in the response.
	lower := strings.ToLower(rec.Body.String())
	for _, forbidden := range []string{"secret", "cipher", "nbsec", "encrypted", "abcd", "wxyz"} {
		if strings.Contains(lower, forbidden) {
			t.Errorf("overview response contains %q: %s", forbidden, rec.Body.String())
		}
	}
}

func seedModelRows(t *testing.T, env *testEnv, userID int64, provider, model string, silentRetry bool, bindingCount int) (int64, int64) {
	t.Helper()
	now := time.Now().Unix()
	// One endpoint + key so bindings have a legal FK target.
	epRes, err := env.store.DB().Exec(`INSERT INTO endpoints (user_id, connector_type, base_url, created_at, updated_at) VALUES (?, 'openai-compatible', 'https://upstream.example', ?, ?)`,
		userID, now, now)
	if err != nil {
		t.Fatalf("seed model endpoint: %v", err)
	}
	epID, _ := epRes.LastInsertId()
	keyRes, err := env.store.DB().Exec(`INSERT INTO endpoint_keys (endpoint_id, encrypted_secret, created_at, updated_at) VALUES (?, 'nbsec:v1:aes-256-gcm:test-secret', ?, ?)`,
		epID, now, now)
	if err != nil {
		t.Fatalf("seed model key: %v", err)
	}
	keyID, _ := keyRes.LastInsertId()

	retryInt := 0
	if silentRetry {
		retryInt = 1
	}
	modelRes, err := env.store.DB().Exec(`INSERT INTO models (user_id, provider, model, full_name, route_strategy, silent_retry, created_at, updated_at) VALUES (?, ?, ?, ?, 'random', ?, ?, ?)`,
		userID, provider, model, provider+"/"+model, retryInt, now, now)
	if err != nil {
		t.Fatalf("seed model: %v", err)
	}
	modelID, _ := modelRes.LastInsertId()
	for i := 0; i < bindingCount; i++ {
		if _, err := env.store.DB().Exec(`INSERT INTO model_bindings (model_id, endpoint_key_id, upstream_model_id, ord, created_at) VALUES (?, ?, ?, ?, ?)`,
			modelID, keyID, fmt.Sprintf("upstream/model-%d", i), i, now); err != nil {
			t.Fatalf("seed binding: %v", err)
		}
	}
	return modelID, keyID
}

func TestAdminOverviewModels(t *testing.T) {
	env := newTestEnv(t)
	user2 := seedSecondUser(t, env)
	m1, _ := seedModelRows(t, env, env.user.ID, "prov1", "mod1", true, 2)
	m2, _ := seedModelRows(t, env, user2.ID, "prov2", "mod2", false, 0)

	h := newAdminMount(t, env)
	r := stationRequest(http.MethodGet, "/admin/api/overview/models", host.StationAdmin)
	r.AddCookie(adminCookie(t, env))
	rec := do(t, h, r)
	assertOK(t, rec)

	var rows []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
		t.Fatalf("decode overview: %v; body=%s", err, rec.Body.String())
	}
	if len(rows) != 2 {
		t.Fatalf("overview rows = %d, want 2: %+v", len(rows), rows)
	}
	first, second := rows[0], rows[1]
	// Contract shape: exactly these keys, no updated_at.
	assertJSONKeys(t, mustJSON(t, first), "id", "user_id", "provider", "model", "full_name", "route_strategy", "silent_retry", "binding_count", "created_at")
	if int64(first["id"].(float64)) != m1 || int64(first["user_id"].(float64)) != env.user.ID ||
		first["provider"] != "prov1" || first["model"] != "mod1" || first["full_name"] != "prov1/mod1" ||
		first["route_strategy"] != "random" || first["silent_retry"].(bool) != true || first["binding_count"].(float64) != 2 {
		t.Errorf("first model row = %v", first)
	}
	if int64(second["id"].(float64)) != m2 || int64(second["user_id"].(float64)) != user2.ID ||
		second["full_name"] != "prov2/mod2" || second["silent_retry"].(bool) != false || second["binding_count"].(float64) != 0 {
		t.Errorf("second model row = %v", second)
	}
	if _, ok := first["updated_at"]; ok {
		t.Errorf("model overview must not carry updated_at per contract: %v", first)
	}

	// Overview routes take no query parameters.
	r2 := stationRequest(http.MethodGet, "/admin/api/overview/models?limit=5", host.StationAdmin)
	r2.AddCookie(adminCookie(t, env))
	rec = do(t, h, r2)
	assertErr(t, rec, http.StatusBadRequest, httperr.CodeInvalidRequest)
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

// --- envelope fallback and nil store ---------------------------------------

func TestEnvelopeFallback(t *testing.T) {
	env := newTestEnv(t)
	raw := NewHandler(HandlerDeps{Store: env.store})

	rec := do(t, raw, stationRequest(http.MethodGet, "/admin/api/logs/extra", host.StationAdmin))
	assertErr(t, rec, http.StatusNotFound, httperr.CodeNotFound)
	rec = do(t, raw, stationRequest(http.MethodGet, "/api/me/usage/extra", host.StationUser))
	assertErr(t, rec, http.StatusNotFound, httperr.CodeNotFound)
	rec = do(t, raw, stationRequest(http.MethodGet, "/api/nonexistent", host.StationUser))
	assertErr(t, rec, http.StatusNotFound, httperr.CodeNotFound)
	rec = do(t, raw, stationRequest(http.MethodPost, "/admin/api/logs", host.StationAdmin))
	assertErr(t, rec, http.StatusMethodNotAllowed, httperr.CodeMethodNotAllowed)
	rec = do(t, raw, stationRequest(http.MethodPost, "/api/me/usage", host.StationUser))
	assertErr(t, rec, http.StatusMethodNotAllowed, httperr.CodeMethodNotAllowed)
}

func TestNilStoreFailsClosed(t *testing.T) {
	raw := NewHandler(HandlerDeps{})
	rec := do(t, raw, stationRequest(http.MethodGet, "/api/me/usage", host.StationUser))
	assertErr(t, rec, http.StatusServiceUnavailable, httperr.CodeServiceUnavailable)
	rec = do(t, raw, stationRequest(http.MethodGet, "/admin/api/logs", host.StationAdmin))
	assertErr(t, rec, http.StatusServiceUnavailable, httperr.CodeServiceUnavailable)
}

// --- log response shape -----------------------------------------------------

func TestAdminLogsRowShape(t *testing.T) {
	env := newTestEnv(t)
	now := time.Now().Unix()
	seedLog(t, env, env.user.ID, "attempt-shape-row", "p1/m1", 502, now-60, "bounded diag")

	h := newAdminMount(t, env)
	r := stationRequest(http.MethodGet, "/admin/api/logs", host.StationAdmin)
	r.AddCookie(adminCookie(t, env))
	rec := do(t, h, r)
	assertOK(t, rec)

	rows, _ := decodeLogs(t, rec)
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	assertJSONKeys(t, mustJSON(t, rows[0]), "id", "user_id", "model", "endpoint_key_id", "upstream_model_id",
		"status_code", "duration_ms", "started_at", "completed_at", "prompt_tokens",
		"completion_tokens", "total_tokens", "usage_unknown", "error_code", "error_diag")
	row := rows[0]
	if row["user_id"].(float64) != float64(env.user.ID) || row["model"] != "p1/m1" ||
		row["status_code"].(float64) != 502 || row["error_code"] != "upstream" ||
		row["duration_ms"].(float64) != 7 || row["usage_unknown"] != false {
		t.Errorf("row = %v", row)
	}
	// No content or credential fields exist in the shape.
	lower := strings.ToLower(rec.Body.String())
	for _, forbidden := range []string{"secret", "cipher", "nbsec", "header"} {
		if strings.Contains(lower, forbidden) {
			t.Errorf("log response contains %q: %s", forbidden, rec.Body.String())
		}
	}
}
