package issues

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nonbiriapi/internal/auth"
	"nonbiriapi/internal/db"
	"nonbiriapi/internal/host"
	"nonbiriapi/internal/httperr"
	"nonbiriapi/internal/secret"
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
	userB *db.User
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
	store, err := db.Open(filepath.Join(t.TempDir(), "issues.db"), vault)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	user, err := store.CreateUser("discord-u1", "user1", "")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	userB, err := store.CreateUser("discord-u2", "user2", "")
	if err != nil {
		t.Fatalf("CreateUser userB: %v", err)
	}
	admin, err := store.EnsureAdminUser("admin")
	if err != nil {
		t.Fatalf("EnsureAdminUser: %v", err)
	}
	return &testEnv{store: store, user: user, userB: userB, admin: admin}
}

// seedIssue writes one issue through the real repository path.
func seedIssue(t *testing.T, env *testEnv, userID int64, kind, message, ref string, unix int64) int64 {
	t.Helper()
	inserted, err := env.store.RecordUserIssue(context.Background(), db.UserIssueInput{
		UserID: userID, Kind: kind, Message: message, Ref: ref,
		CreatedAt: time.Unix(unix, 0),
	})
	if err != nil {
		t.Fatalf("RecordUserIssue: %v", err)
	}
	if !inserted {
		t.Fatalf("issue not inserted")
	}
	var id int64
	if err := env.store.DB().QueryRow(
		`SELECT id FROM user_issues WHERE user_id=? AND kind=? ORDER BY id DESC LIMIT 1`,
		userID, kind).Scan(&id); err != nil {
		t.Fatalf("read seeded issue id: %v", err)
	}
	return id
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
	t.Cleanup(func() { _ = userAuth.Close() })
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

func decodeIssues(t *testing.T, rec *httptest.ResponseRecorder) (items []map[string]any, hasMore bool) {
	t.Helper()
	var body struct {
		Data    []map[string]any `json:"data"`
		HasMore bool             `json:"has_more"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode issue list: %v; body=%s", err, rec.Body.String())
	}
	return body.Data, body.HasMore
}

// --- auth isolation --------------------------------------------------------

func TestIssuesAuthIsolation(t *testing.T) {
	env := newTestEnv(t)
	seedIssue(t, env, env.user.ID, "k", "m", "r", 1700000001)
	raw := NewHandler(HandlerDeps{Store: env.store})

	// No identity at all.
	rec := do(t, raw, stationRequest(http.MethodGet, "/api/issues", host.StationUser))
	assertErr(t, rec, http.StatusUnauthorized, httperr.CodeUnauthorized)

	// The handler never reads a session cookie by itself (middleware absent).
	r := stationRequest(http.MethodGet, "/api/issues", host.StationUser)
	r.AddCookie(userCookie(t, env))
	rec = do(t, raw, r)
	assertErr(t, rec, http.StatusUnauthorized, httperr.CodeUnauthorized)

	// A header-carried user id never authorizes.
	r = stationRequest(http.MethodGet, "/api/issues", host.StationUser)
	r.Header.Set("X-User-Id", fmt.Sprintf("%d", env.user.ID))
	rec = do(t, raw, r)
	assertErr(t, rec, http.StatusUnauthorized, httperr.CodeUnauthorized)

	// A caller-key principal is not a user session.
	generation, err := env.store.RegenerateCallerKey(env.user.ID)
	if err != nil {
		t.Fatalf("RegenerateCallerKey: %v", err)
	}
	r = stationRequest(http.MethodGet, "/api/issues", host.StationUser)
	r.Header.Set("Authorization", "Bearer "+generation.Secret)
	rec = do(t, newCallerKeyMount(t, env), r)
	assertErr(t, rec, http.StatusUnauthorized, httperr.CodeUnauthorized)

	// An admin session cannot reach the user route (admin middleware station
	// split refuses before any identity is consulted).
	r = stationRequest(http.MethodGet, "/api/issues", host.StationAdmin)
	r.AddCookie(adminCookie(t, env))
	rec = do(t, newAdminMount(t, env), r)
	assertErr(t, rec, http.StatusForbidden, httperr.CodeForbidden)

	// A user session on the wrong station is refused too.
	r = stationRequest(http.MethodGet, "/api/issues", host.StationAdmin)
	r.AddCookie(userCookie(t, env))
	rec = do(t, newUserMount(t, env), r)
	assertErr(t, rec, http.StatusForbidden, httperr.CodeForbidden)
}

func TestIssuesResolveAuthIsolation(t *testing.T) {
	env := newTestEnv(t)
	id := seedIssue(t, env, env.user.ID, "k", "m", "r", 1700000001)
	raw := NewHandler(HandlerDeps{Store: env.store})

	rec := do(t, raw, stationRequest(http.MethodPost, "/api/issues/"+fmt.Sprint(id)+"/resolve", host.StationUser))
	assertErr(t, rec, http.StatusUnauthorized, httperr.CodeUnauthorized)

	generation, err := env.store.RegenerateCallerKey(env.user.ID)
	if err != nil {
		t.Fatalf("RegenerateCallerKey: %v", err)
	}
	r := stationRequest(http.MethodPost, "/api/issues/"+fmt.Sprint(id)+"/resolve", host.StationUser)
	r.Header.Set("Authorization", "Bearer "+generation.Secret)
	rec = do(t, newCallerKeyMount(t, env), r)
	assertErr(t, rec, http.StatusUnauthorized, httperr.CodeUnauthorized)
}

// --- listing ---------------------------------------------------------------

func TestIssuesListOwnershipAndPagination(t *testing.T) {
	env := newTestEnv(t)
	// Interleaved seeds across two users: ordering and cursors must never
	// leak userB's issues into user's pages.
	idA1 := seedIssue(t, env, env.user.ID, "ka1", "ma1", "ra", 1700000001)
	seedIssue(t, env, env.userB.ID, "kb1", "mb1", "rb", 1700000002)
	idA2 := seedIssue(t, env, env.user.ID, "ka2", "ma2", "ra", 1700000003)
	seedIssue(t, env, env.userB.ID, "kb2", "mb2", "rb", 1700000004)
	idA3 := seedIssue(t, env, env.user.ID, "ka3", "ma3", "ra", 1700000005)

	mount := newUserMount(t, env)

	r := stationRequest(http.MethodGet, "/api/issues?limit=2", host.StationUser)
	r.AddCookie(userCookie(t, env))
	rec := do(t, mount, r)
	assertOK(t, rec)
	items, hasMore := decodeIssues(t, rec)
	if !hasMore {
		t.Fatalf("page 1 has_more=false, want true")
	}
	if len(items) != 2 {
		t.Fatalf("page 1 items=%d, want 2", len(items))
	}
	if items[0]["id"] != float64(idA3) || items[1]["id"] != float64(idA2) {
		t.Fatalf("page 1 wrong order: %v", items)
	}
	if items[0]["kind"] != "ka3" || items[0]["message"] != "ma3" || items[0]["ref"] != "ra" {
		t.Fatalf("page 1 wrong fields: %v", items[0])
	}
	if items[0]["created_at"] != float64(1700000005) {
		t.Fatalf("created_at=%v", items[0]["created_at"])
	}

	// Page 2 via the keyset cursor.
	r = stationRequest(http.MethodGet,
		fmt.Sprintf("/api/issues?limit=2&before_id=%d", int64(items[1]["id"].(float64))),
		host.StationUser)
	r.AddCookie(userCookie(t, env))
	rec = do(t, mount, r)
	assertOK(t, rec)
	items, hasMore = decodeIssues(t, rec)
	if hasMore {
		t.Fatalf("page 2 has_more=true, want false")
	}
	if len(items) != 1 || items[0]["id"] != float64(idA1) {
		t.Fatalf("page 2 wrong: %v", items)
	}
}

func TestIssuesListResolvedFilter(t *testing.T) {
	env := newTestEnv(t)
	idOpen := seedIssue(t, env, env.user.ID, "ko", "mo", "r", 1700000001)
	idDone := seedIssue(t, env, env.user.ID, "kd", "md", "r", 1700000002)
	if err := env.store.ResolveUserIssue(context.Background(), env.user.ID, idDone, 1700000010); err != nil {
		t.Fatalf("seed resolve: %v", err)
	}
	mount := newUserMount(t, env)

	r := stationRequest(http.MethodGet, "/api/issues?resolved=false", host.StationUser)
	r.AddCookie(userCookie(t, env))
	rec := do(t, mount, r)
	assertOK(t, rec)
	items, _ := decodeIssues(t, rec)
	if len(items) != 1 || items[0]["id"] != float64(idOpen) || items[0]["resolved"] != false {
		t.Fatalf("unresolved filter wrong: %v", items)
	}
	if _, present := items[0]["resolved_at"]; present {
		t.Fatalf("unresolved issue carries resolved_at")
	}

	r = stationRequest(http.MethodGet, "/api/issues?resolved=true", host.StationUser)
	r.AddCookie(userCookie(t, env))
	rec = do(t, mount, r)
	assertOK(t, rec)
	items, _ = decodeIssues(t, rec)
	if len(items) != 1 || items[0]["id"] != float64(idDone) || items[0]["resolved"] != true {
		t.Fatalf("resolved filter wrong: %v", items)
	}
	if items[0]["resolved_at"] != float64(1700000010) {
		t.Fatalf("resolved_at=%v", items[0]["resolved_at"])
	}

	// No filter: both.
	r = stationRequest(http.MethodGet, "/api/issues", host.StationUser)
	r.AddCookie(userCookie(t, env))
	rec = do(t, mount, r)
	assertOK(t, rec)
	items, _ = decodeIssues(t, rec)
	if len(items) != 2 {
		t.Fatalf("no filter items=%d, want 2", len(items))
	}
}

func TestIssuesListEmpty(t *testing.T) {
	env := newTestEnv(t)
	mount := newUserMount(t, env)
	r := stationRequest(http.MethodGet, "/api/issues", host.StationUser)
	r.AddCookie(userCookie(t, env))
	rec := do(t, mount, r)
	assertOK(t, rec)
	items, hasMore := decodeIssues(t, rec)
	if len(items) != 0 || hasMore {
		t.Fatalf("empty list wrong: items=%d has_more=%v", len(items), hasMore)
	}
}

func TestIssuesListStrictParams(t *testing.T) {
	env := newTestEnv(t)
	mount := newUserMount(t, env)

	cases := []string{
		"?resolved=yes",
		"?resolved=1",
		"?resolved=TRUE",
		"?resolved=true&resolved=false",
		"?before_id=0",
		"?before_id=-1",
		"?before_id=abc",
		"?before_id=1&before_id=2",
		"?limit=abc",
		"?limit=1&limit=2",
		"?unknown=1",
		"?limit=2&extra=1",
		"?" + strings.Repeat("x", 9000),
	}
	for _, q := range cases {
		r := stationRequest(http.MethodGet, "/api/issues"+q, host.StationUser)
		r.AddCookie(userCookie(t, env))
		rec := do(t, mount, r)
		assertErr(t, rec, http.StatusBadRequest, httperr.CodeInvalidRequest)
	}

	// Clamping: limit=0 and oversized limits are accepted and clamped (never
	// rejected, mirroring the log-query parser).
	for _, q := range []string{"?limit=0", "?limit=99999", "?limit=0&resolved=false"} {
		r := stationRequest(http.MethodGet, "/api/issues"+q, host.StationUser)
		r.AddCookie(userCookie(t, env))
		rec := do(t, mount, r)
		assertOK(t, rec)
	}
}

// --- resolve ---------------------------------------------------------------

func TestIssuesResolveOwnershipAndIdempotent(t *testing.T) {
	env := newTestEnv(t)
	idMine := seedIssue(t, env, env.user.ID, "km", "mm", "r", 1700000001)
	idTheirs := seedIssue(t, env, env.userB.ID, "kt", "mt", "r", 1700000002)
	mount := newUserMount(t, env)

	// Cross-user resolve: indistinguishable not_found, and no mutation.
	r := stationRequest(http.MethodPost, "/api/issues/"+fmt.Sprint(idTheirs)+"/resolve", host.StationUser)
	r.AddCookie(userCookie(t, env))
	rec := do(t, mount, r)
	assertErr(t, rec, http.StatusNotFound, httperr.CodeNotFound)
	var resolved int
	if err := env.store.DB().QueryRow(`SELECT resolved FROM user_issues WHERE id=?`, idTheirs).Scan(&resolved); err != nil {
		t.Fatalf("read theirs: %v", err)
	}
	if resolved != 0 {
		t.Fatalf("cross-user resolve mutated the issue")
	}

	// Missing id and bad id.
	r = stationRequest(http.MethodPost, "/api/issues/999999/resolve", host.StationUser)
	r.AddCookie(userCookie(t, env))
	rec = do(t, mount, r)
	assertErr(t, rec, http.StatusNotFound, httperr.CodeNotFound)
	for _, bad := range []string{"abc", "0", "-1", "1.5"} {
		r = stationRequest(http.MethodPost, "/api/issues/"+bad+"/resolve", host.StationUser)
		r.AddCookie(userCookie(t, env))
		rec = do(t, mount, r)
		assertErr(t, rec, http.StatusBadRequest, httperr.CodeInvalidRequest)
	}

	// Own resolve: 204, then idempotent repeat still 204.
	r = stationRequest(http.MethodPost, "/api/issues/"+fmt.Sprint(idMine)+"/resolve", host.StationUser)
	r.AddCookie(userCookie(t, env))
	rec = do(t, mount, r)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
	assertNoStore(t, rec)
	r = stationRequest(http.MethodPost, "/api/issues/"+fmt.Sprint(idMine)+"/resolve", host.StationUser)
	r.AddCookie(userCookie(t, env))
	rec = do(t, mount, r)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("repeat status = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}

	// The resolved issue now appears in the resolved filter.
	r = stationRequest(http.MethodGet, "/api/issues?resolved=true", host.StationUser)
	r.AddCookie(userCookie(t, env))
	rec = do(t, mount, r)
	assertOK(t, rec)
	items, _ := decodeIssues(t, rec)
	if len(items) != 1 || items[0]["id"] != float64(idMine) {
		t.Fatalf("resolved list wrong: %v", items)
	}
}

// --- banned / sanitization -------------------------------------------------

func TestIssuesDeniedWhenBanned(t *testing.T) {
	env := newTestEnv(t)
	seedIssue(t, env, env.user.ID, "k", "m", "r", 1700000001)
	// The session must be minted while the user is still active; banning
	// afterwards invalidates it at resolution time (the session query checks
	// ban state at the same serialization point as idle renewal).
	cookie := userCookie(t, env)
	if err := env.store.BanUser(env.user.ID, "abuse"); err != nil {
		t.Fatalf("BanUser: %v", err)
	}
	mount := newUserMount(t, env)
	r := stationRequest(http.MethodGet, "/api/issues", host.StationUser)
	r.AddCookie(cookie)
	rec := do(t, mount, r)
	assertErr(t, rec, http.StatusUnauthorized, httperr.CodeUnauthorized)
}

// TestIssuesProjectionIsPlainText verifies the wire projection carries only
// bounded, sanitized, secret-free text: control characters and CR/LF are
// gone, over-long values are bounded, and nothing else is projected.
func TestIssuesProjectionIsPlainText(t *testing.T) {
	env := newTestEnv(t)
	// Raw SQL bypasses the write boundary on purpose: the read sink must
	// still bound and normalize the projection.
	long := strings.Repeat("x", 4096)
	if _, err := env.store.DB().Exec(`
INSERT INTO user_issues (user_id, kind, message, ref, created_at)
VALUES (?, ?, ?, ?, ?)`,
		env.user.ID, "k\nl", "line1\nline2\tline3\x00secret-ish", long, 1700000001); err != nil {
		t.Fatalf("raw insert: %v", err)
	}
	mount := newUserMount(t, env)
	r := stationRequest(http.MethodGet, "/api/issues", host.StationUser)
	r.AddCookie(userCookie(t, env))
	rec := do(t, mount, r)
	assertOK(t, rec)
	items, _ := decodeIssues(t, rec)
	if len(items) != 1 {
		t.Fatalf("items=%d, want 1", len(items))
	}
	// Check the decoded values (the raw JSON body legitimately ends with the
	// encoder's trailing newline, so control-character checks belong on the
	// decoded strings).
	kind := items[0]["kind"].(string)
	if kind != "k l" {
		t.Fatalf("kind not normalized: %q", kind)
	}
	msg := items[0]["message"].(string)
	ref := items[0]["ref"].(string)
	for _, s := range []string{kind, msg, ref} {
		for _, r := range s {
			if r == '\n' || r == '\r' || r == '\t' || r < 0x20 || r == 0x7f {
				t.Fatalf("projection contains forbidden rune %q in %q", r, s)
			}
		}
	}
	if len(msg) > 1024 {
		t.Fatalf("message not bounded: %d", len(msg))
	}
	if len(ref) > 256 {
		t.Fatalf("ref not bounded: %d", len(ref))
	}
	// The wire shape is exactly the contracted field set.
	var keys map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &keys); err != nil {
		t.Fatalf("decode top level: %v", err)
	}
	if len(keys) != 2 || keys["data"] == nil || keys["has_more"] == nil {
		t.Fatalf("top-level keys = %v, want data + has_more", keys)
	}
}
