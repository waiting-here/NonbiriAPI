package alertapi

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
	store, err := db.Open(filepath.Join(t.TempDir(), "alertapi.db"), vault)
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

// seedAlert records one alert through the real flood-safe repository hook.
func seedAlert(t *testing.T, env *testEnv, kind, ref string, subjectUserID int64) int64 {
	t.Helper()
	inserted, err := env.store.RecordAdminAlertBounded(context.Background(), db.AdminAlertInput{
		Kind:          kind,
		Message:       "message " + ref,
		Ref:           ref,
		SubjectUserID: subjectUserID,
		CreatedAt:     time.Now(),
	})
	if err != nil {
		t.Fatalf("RecordAdminAlertBounded(%s/%s): %v", kind, ref, err)
	}
	if !inserted {
		t.Fatalf("RecordAdminAlertBounded(%s/%s): unexpectedly suppressed", kind, ref)
	}
	var id int64
	if err := env.store.DB().QueryRow(`SELECT id FROM admin_alerts WHERE ref = ? ORDER BY id DESC LIMIT 1`, ref).Scan(&id); err != nil {
		t.Fatalf("read back alert id: %v", err)
	}
	return id
}

// --- mount helpers (real session middlewares) ------------------------------

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

func adminCookie(t *testing.T, env *testEnv) *http.Cookie {
	t.Helper()
	token, _, err := env.store.CreateAdminSession(env.admin.ID)
	if err != nil {
		t.Fatalf("CreateAdminSession: %v", err)
	}
	return &http.Cookie{Name: auth.AdminSessionCookieName, Value: token}
}

func userCookie(t *testing.T, env *testEnv) *http.Cookie {
	t.Helper()
	token, _, err := env.store.CreateUserSession(env.user.ID)
	if err != nil {
		t.Fatalf("CreateUserSession: %v", err)
	}
	return &http.Cookie{Name: auth.UserSessionCookieName, Value: token}
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

func decodeAlerts(t *testing.T, rec *httptest.ResponseRecorder) []map[string]any {
	t.Helper()
	var rows []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
		t.Fatalf("decode alerts: %v; body=%s", err, rec.Body.String())
	}
	return rows
}

func decodeAlert(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var row map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &row); err != nil {
		t.Fatalf("decode alert: %v; body=%s", err, rec.Body.String())
	}
	return row
}

func alertKeys() []string {
	return []string{"id", "kind", "message", "ref", "subject_user_id", "created_at", "resolved", "resolved_at"}
}

// --- GET /admin/api/alerts -------------------------------------------------

func TestAdminAlertsAuthIsolation(t *testing.T) {
	env := newTestEnv(t)
	seedAlert(t, env, db.AlertKindFetchFailed, "auth-1", 0)
	raw := NewHandler(HandlerDeps{Store: env.store})

	// No identity at all.
	rec := do(t, raw, stationRequest(http.MethodGet, "/admin/api/alerts", host.StationAdmin))
	assertErr(t, rec, http.StatusUnauthorized, httperr.CodeUnauthorized)

	// The handler never reads a session cookie by itself (middleware absent).
	r := stationRequest(http.MethodGet, "/admin/api/alerts", host.StationAdmin)
	r.AddCookie(adminCookie(t, env))
	rec = do(t, raw, r)
	assertErr(t, rec, http.StatusUnauthorized, httperr.CodeUnauthorized)

	// A header-carried identity never authorizes.
	r = stationRequest(http.MethodGet, "/admin/api/alerts", host.StationAdmin)
	r.Header.Set("X-User-Id", fmt.Sprintf("%d", env.user.ID))
	rec = do(t, raw, r)
	assertErr(t, rec, http.StatusUnauthorized, httperr.CodeUnauthorized)

	// User-station context is refused (the edge would have blocked the path).
	rec = do(t, raw, stationRequest(http.MethodGet, "/admin/api/alerts", host.StationUser))
	assertErr(t, rec, http.StatusForbidden, httperr.CodeForbidden)

	// A valid user session mounted on the admin station is refused before any
	// row is read.
	r = stationRequest(http.MethodGet, "/admin/api/alerts", host.StationAdmin)
	r.AddCookie(userCookie(t, env))
	rec = do(t, newUserMount(t, env), r)
	assertErr(t, rec, http.StatusForbidden, httperr.CodeForbidden)

	// A caller-key principal is not an admin session.
	generation, err := env.store.RegenerateCallerKey(env.user.ID)
	if err != nil {
		t.Fatalf("RegenerateCallerKey: %v", err)
	}
	r = stationRequest(http.MethodGet, "/admin/api/alerts", host.StationAdmin)
	r.Header.Set("Authorization", "Bearer "+generation.Secret)
	rec = do(t, newCallerKeyMount(t, env), r)
	assertErr(t, rec, http.StatusForbidden, httperr.CodeForbidden)

	// A real admin session succeeds.
	r = stationRequest(http.MethodGet, "/admin/api/alerts", host.StationAdmin)
	r.AddCookie(adminCookie(t, env))
	rec = do(t, newAdminMount(t, env), r)
	assertOK(t, rec)
}

func TestAdminAlertsListShapeAndFilter(t *testing.T) {
	env := newTestEnv(t)
	a1 := seedAlert(t, env, db.AlertKindFetchFailed, "list-1", env.user.ID)
	a2 := seedAlert(t, env, db.AlertKindForwardError, "list-2", 0)
	a3 := seedAlert(t, env, db.AlertKindRegistrationRejected, "list-3", env.user.ID)

	h := newAdminMount(t, env)
	get := func(query string) []map[string]any {
		t.Helper()
		r := stationRequest(http.MethodGet, "/admin/api/alerts"+query, host.StationAdmin)
		r.AddCookie(adminCookie(t, env))
		rec := do(t, h, r)
		assertOK(t, rec)
		return decodeAlerts(t, rec)
	}

	// All alerts, newest first, exact contract shape.
	rows := get("")
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want 3: %+v", len(rows), rows)
	}
	if int64(rows[0]["id"].(float64)) != a3 || int64(rows[1]["id"].(float64)) != a2 || int64(rows[2]["id"].(float64)) != a1 {
		t.Fatalf("order = [%v %v %v], want [%d %d %d]", rows[0]["id"], rows[1]["id"], rows[2]["id"], a3, a2, a1)
	}
	for _, row := range rows {
		for _, key := range alertKeys() {
			if _, ok := row[key]; !ok {
				t.Fatalf("key %q missing: %v", key, row)
			}
		}
		if len(row) != len(alertKeys()) {
			t.Fatalf("row keys = %v, want %v", row, alertKeys())
		}
		if row["resolved"] != false {
			t.Errorf("row %v: resolved should be false", row)
		}
	}
	// Subject-bound rows carry the subject id; site-wide rows carry null.
	if rows[0]["subject_user_id"].(float64) != float64(env.user.ID) {
		t.Errorf("subject-bound subject_user_id = %v", rows[0]["subject_user_id"])
	}
	if rows[1]["subject_user_id"] != nil {
		t.Errorf("site-wide subject_user_id = %v, want null", rows[1]["subject_user_id"])
	}
	// Unresolved alerts carry null resolved_at.
	if rows[0]["resolved_at"] != nil {
		t.Errorf("unresolved resolved_at = %v, want null", rows[0]["resolved_at"])
	}

	// resolved=false filter: only pending alerts.
	rows = get("?resolved=false")
	if len(rows) != 3 {
		t.Fatalf("resolved=false rows = %d, want 3", len(rows))
	}

	// Resolve a2 via the repository, then the filter shows it only on the
	// resolved=true page.
	if _, err := env.store.SetAdminAlertResolved(context.Background(), a2, true, time.Now().Unix()); err != nil {
		t.Fatalf("SetAdminAlertResolved: %v", err)
	}
	rows = get("?resolved=true")
	if len(rows) != 1 || int64(rows[0]["id"].(float64)) != a2 || rows[0]["resolved"] != true {
		t.Fatalf("resolved=true rows = %+v", rows)
	}
	if rows[0]["resolved_at"] == nil {
		t.Errorf("resolved alert resolved_at = nil, want a unix timestamp")
	}
	rows = get("?resolved=false")
	if len(rows) != 2 {
		t.Fatalf("resolved=false rows = %d, want 2", len(rows))
	}

	// An empty table yields an empty array, not an object or null.
	if _, err := env.store.DB().Exec(`DELETE FROM admin_alerts`); err != nil {
		t.Fatalf("clear alerts: %v", err)
	}
	r := stationRequest(http.MethodGet, "/admin/api/alerts", host.StationAdmin)
	r.AddCookie(adminCookie(t, env))
	rec := do(t, h, r)
	assertOK(t, rec)
	if got := strings.TrimSpace(rec.Body.String()); got != "[]" {
		t.Fatalf("empty list body = %s, want []", got)
	}
}

func TestAdminAlertsPagination(t *testing.T) {
	env := newTestEnv(t)
	for i := 1; i <= 7; i++ {
		seedAlert(t, env, db.AlertKindFetchFailed, fmt.Sprintf("pag-%d", i), 0)
	}
	h := newAdminMount(t, env)

	get := func(query string) []int64 {
		t.Helper()
		r := stationRequest(http.MethodGet, "/admin/api/alerts"+query, host.StationAdmin)
		r.AddCookie(adminCookie(t, env))
		rec := do(t, h, r)
		assertOK(t, rec)
		rows := decodeAlerts(t, rec)
		out := make([]int64, 0, len(rows))
		for _, row := range rows {
			out = append(out, int64(row["id"].(float64)))
		}
		return out
	}

	// Keyset walk with limit=3 covers every alert exactly once, newest first.
	var walked []int64
	var cursor int64
	for {
		query := "?limit=3"
		if cursor > 0 {
			query += fmt.Sprintf("&before_id=%d", cursor)
		}
		page := get(query)
		if len(page) == 0 {
			break
		}
		for i := 1; i < len(page); i++ {
			if page[i] >= page[i-1] {
				t.Fatalf("page not strictly descending: %v", page)
			}
		}
		walked = append(walked, page...)
		cursor = page[len(page)-1]
	}
	if len(walked) != 7 {
		t.Fatalf("walked %d alerts, want 7: %v", len(walked), walked)
	}
	seen := map[int64]bool{}
	for _, id := range walked {
		if seen[id] {
			t.Fatalf("alert %d walked twice", id)
		}
		seen[id] = true
	}

	// limit clamps into [1, db.MaxAdminAlertsPageLimit].
	if page := get("?limit=0"); len(page) != 1 || page[0] != 7 {
		t.Fatalf("limit=0 page = %v", page)
	}
	if page := get("?limit=1000"); len(page) != 7 {
		t.Fatalf("limit=1000 page = %v", page)
	}
}

func TestAdminAlertsInvalidParams(t *testing.T) {
	env := newTestEnv(t)
	seedAlert(t, env, db.AlertKindFetchFailed, "param-1", 0)
	h := newAdminMount(t, env)

	request := func(query string) *httptest.ResponseRecorder {
		t.Helper()
		r := stationRequest(http.MethodGet, "/admin/api/alerts"+query, host.StationAdmin)
		r.AddCookie(adminCookie(t, env))
		return do(t, h, r)
	}

	bad := []string{
		"?unknown=1",
		"?resolved=maybe",
		"?resolved=1",
		"?resolved=true&resolved=false",
		"?resolved=",
		"?before_id=abc",
		"?before_id=0",
		"?before_id=-1",
		"?before_id=1&before_id=2",
		"?limit=abc",
		"?limit=1&limit=2",
		"?resolved=true&before_id=1%20OR%201=1",
	}
	for _, query := range bad {
		rec := request(query)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("query %q: status = %d, want 400; body=%s", query, rec.Code, rec.Body.String())
			continue
		}
		assertErr(t, rec, http.StatusBadRequest, httperr.CodeInvalidRequest)
	}

	// Injection-shaped before_id is a parse failure (never SQL, never rows):
	// the stable invalid_request envelope is returned with no echo.
	rec := request("?before_id=1%27%20OR%201%3D1--")
	assertErr(t, rec, http.StatusBadRequest, httperr.CodeInvalidRequest)
	if strings.Contains(rec.Body.String(), "1=1") {
		t.Fatalf("injection leaked into the response: %s", rec.Body.String())
	}
}

// --- POST /admin/api/alerts/{id}/resolve -----------------------------------

func TestAdminAlertsResolve(t *testing.T) {
	env := newTestEnv(t)
	id := seedAlert(t, env, db.AlertKindFetchFailed, "resolve-1", 0)
	h := newAdminMount(t, env)

	post := func(target, body string) *httptest.ResponseRecorder {
		t.Helper()
		var reader *bytes.Reader
		if body == "" {
			reader = bytes.NewReader(nil)
		} else {
			reader = bytes.NewReader([]byte(body))
		}
		r := httptest.NewRequest(http.MethodPost, target, reader)
		r.RemoteAddr = "198.51.100.20:4000"
		r = r.WithContext(host.WithStation(r.Context(), host.StationAdmin))
		r.AddCookie(adminCookie(t, env))
		return do(t, h, r)
	}

	// A bare POST is a one-step ack (resolve).
	rec := post(fmt.Sprintf("/admin/api/alerts/%d/resolve", id), "")
	assertOK(t, rec)
	row := decodeAlert(t, rec)
	if row["resolved"] != true || row["resolved_at"] == nil || int64(row["id"].(float64)) != id {
		t.Fatalf("resolved row = %v", row)
	}
	for _, key := range alertKeys() {
		if _, ok := row[key]; !ok {
			t.Fatalf("key %q missing in resolve response: %v", key, row)
		}
	}

	// Repeating the same transition succeeds (idempotent).
	rec = post(fmt.Sprintf("/admin/api/alerts/%d/resolve", id), `{"resolved":true}`)
	assertOK(t, rec)
	if decodeAlert(t, rec)["resolved"] != true {
		t.Fatalf("repeat resolve failed: %s", rec.Body.String())
	}

	// Explicit reopen.
	rec = post(fmt.Sprintf("/admin/api/alerts/%d/resolve", id), `{"resolved":false}`)
	assertOK(t, rec)
	row = decodeAlert(t, rec)
	if row["resolved"] != false || row["resolved_at"] != nil {
		t.Fatalf("reopened row = %v", row)
	}

	// Empty object means resolve (default true).
	rec = post(fmt.Sprintf("/admin/api/alerts/%d/resolve", id), `{}`)
	assertOK(t, rec)
	if decodeAlert(t, rec)["resolved"] != true {
		t.Fatalf("empty object did not resolve: %s", rec.Body.String())
	}

	// Resolve is admin-session-only.
	r := stationRequest(http.MethodPost, fmt.Sprintf("/admin/api/alerts/%d/resolve", id), host.StationAdmin)
	rec = do(t, h, r)
	assertErr(t, rec, http.StatusUnauthorized, httperr.CodeUnauthorized)
}

func TestAdminAlertsResolveErrors(t *testing.T) {
	env := newTestEnv(t)
	id := seedAlert(t, env, db.AlertKindFetchFailed, "resolve-err", 0)
	h := newAdminMount(t, env)

	post := func(target, body string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(http.MethodPost, target, bytes.NewReader([]byte(body)))
		r.RemoteAddr = "198.51.100.20:4000"
		r = r.WithContext(host.WithStation(r.Context(), host.StationAdmin))
		r.AddCookie(adminCookie(t, env))
		return do(t, h, r)
	}
	target := fmt.Sprintf("/admin/api/alerts/%d/resolve", id)

	// Malformed ids never reach the repository.
	for _, badTarget := range []string{
		"/admin/api/alerts/abc/resolve",
		"/admin/api/alerts/0/resolve",
		"/admin/api/alerts/-5/resolve",
		"/admin/api/alerts/1.5/resolve",
	} {
		rec := post(badTarget, "")
		assertErr(t, rec, http.StatusBadRequest, httperr.CodeInvalidRequest)
	}

	// A valid id with no row is not_found.
	rec := post("/admin/api/alerts/999999/resolve", "")
	assertErr(t, rec, http.StatusNotFound, httperr.CodeNotFound)

	// Malformed bodies.
	badBodies := []string{
		`{"resolved":"yes"}`,
		`{"resolved":1}`,
		`{"resolved":`,
		`{"other":true}`,
		`{"resolved":true,"extra":1}`,
		`{"resolved":true} {"resolved":false}`,
		`[1,2]`,
		`not json`,
	}
	for _, body := range badBodies {
		rec := post(target, body)
		assertErr(t, rec, http.StatusBadRequest, httperr.CodeInvalidRequest)
	}

	// Over-long bodies are payload_too_large.
	rec = post(target, `{"resolved":`+strings.Repeat(" ", 5000)+`true}`)
	assertErr(t, rec, http.StatusRequestEntityTooLarge, httperr.CodePayloadTooLarge)

	// The alert is untouched by all failed attempts.
	r := stationRequest(http.MethodGet, "/admin/api/alerts", host.StationAdmin)
	r.AddCookie(adminCookie(t, env))
	rec = do(t, h, r)
	assertOK(t, rec)
	rows := decodeAlerts(t, rec)
	if len(rows) != 1 || rows[0]["resolved"] != false {
		t.Fatalf("alert state after failed attempts = %+v", rows)
	}

	// Method and path fallbacks stay bounded JSON envelopes.
	for _, fallback := range []struct{ method, target string }{
		{http.MethodGet, target},
		{http.MethodPost, "/admin/api/alerts"},
		{http.MethodDelete, target},
		{http.MethodPost, target + "/extra"},
		{http.MethodGet, "/admin/api/alerts/extra"},
	} {
		req := stationRequest(fallback.method, fallback.target, host.StationAdmin)
		req.AddCookie(adminCookie(t, env))
		rec := do(t, h, req)
		if rec.Code != http.StatusMethodNotAllowed && rec.Code != http.StatusNotFound {
			t.Errorf("%s %s: status = %d, want 404/405; body=%s", fallback.method, fallback.target, rec.Code, rec.Body.String())
		}
		assertNoStore(t, rec)
	}
}

// --- output bounds and fail-closed -----------------------------------------

func TestAdminAlertsOutputBounded(t *testing.T) {
	env := newTestEnv(t)
	// A hostile/broken writer bypasses the write boundary and puts over-long,
	// control-character, invalid-UTF-8 text straight into the table; the
	// read-side sink must bound and sanitize it before it reaches the admin.
	dirty := strings.Repeat("x", 9000) + "\n\t\r" + string([]byte{0xff, 0xfe}) + strings.Repeat("y", 9000)
	now := time.Now().Unix()
	if _, err := env.store.DB().Exec(`INSERT INTO admin_alerts (kind, message, ref, created_at) VALUES (?, ?, ?, ?)`,
		dirty, dirty, dirty, now); err != nil {
		t.Fatalf("raw insert: %v", err)
	}

	h := newAdminMount(t, env)
	r := stationRequest(http.MethodGet, "/admin/api/alerts", host.StationAdmin)
	r.AddCookie(adminCookie(t, env))
	rec := do(t, h, r)
	assertOK(t, rec)

	rows := decodeAlerts(t, rec)
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	for _, field := range []string{"kind", "message", "ref"} {
		value, _ := rows[0][field].(string)
		if !utf8.ValidString(value) {
			t.Errorf("%s is not valid UTF-8", field)
		}
		if strings.ContainsAny(value, "\r\n\t") {
			t.Errorf("%s contains line-forgery control characters", field)
		}
	}
	if utf8.RuneCountInString(rows[0]["message"].(string)) > 1024 {
		t.Errorf("message not bounded: %d runes", utf8.RuneCountInString(rows[0]["message"].(string)))
	}

	// The response shape carries no credential/ciphertext/content field.
	lower := strings.ToLower(rec.Body.String())
	for _, forbidden := range []string{"secret", "cipher", "nbsec", "encrypted", "prompt", "header"} {
		if strings.Contains(lower, forbidden) {
			t.Errorf("alert response contains %q: %s", forbidden, rec.Body.String())
		}
	}
}

func TestNilStoreFailsClosed(t *testing.T) {
	raw := NewHandler(HandlerDeps{})
	rec := do(t, raw, stationRequest(http.MethodGet, "/admin/api/alerts", host.StationAdmin))
	assertErr(t, rec, http.StatusServiceUnavailable, httperr.CodeServiceUnavailable)
	rec = do(t, raw, stationRequest(http.MethodPost, "/admin/api/alerts/1/resolve", host.StationAdmin))
	assertErr(t, rec, http.StatusServiceUnavailable, httperr.CodeServiceUnavailable)
}

func TestEnvelopeFallback(t *testing.T) {
	env := newTestEnv(t)
	raw := NewHandler(HandlerDeps{Store: env.store})

	rec := do(t, raw, stationRequest(http.MethodGet, "/admin/api/alerts/extra", host.StationAdmin))
	assertErr(t, rec, http.StatusNotFound, httperr.CodeNotFound)
	rec = do(t, raw, stationRequest(http.MethodPost, "/admin/api/alerts", host.StationAdmin))
	assertErr(t, rec, http.StatusMethodNotAllowed, httperr.CodeMethodNotAllowed)
	rec = do(t, raw, stationRequest(http.MethodGet, "/admin/api/nonexistent", host.StationAdmin))
	assertErr(t, rec, http.StatusNotFound, httperr.CodeNotFound)
}
