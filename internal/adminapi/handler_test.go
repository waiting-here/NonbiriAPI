package adminapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/auth"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/egress"
	"github.com/waiting-here/NonbiriAPI/internal/flowcontrol"
	"github.com/waiting-here/NonbiriAPI/internal/host"
	"github.com/waiting-here/NonbiriAPI/internal/maintenance"
	"github.com/waiting-here/NonbiriAPI/internal/ratelimit"
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

// recordingApplier records apply/revert calls and can fail either step.
type recordingApplier struct {
	applied   []string
	reverted  []string
	applyErr  error
	revertErr error
}

func (a *recordingApplier) ApplySiteConfig(_ context.Context, key, value string) error {
	a.applied = append(a.applied, key+"="+value)
	return a.applyErr
}

func (a *recordingApplier) RevertSiteConfig(_ context.Context, key, value string) error {
	a.reverted = append(a.reverted, key+"="+value)
	return a.revertErr
}

type env struct {
	store *db.Store
	admin *db.User
}

func newEnv(t *testing.T) *env {
	t.Helper()
	key := bytes.Repeat([]byte{0x6d}, secret.MasterKeyBytes)
	vault, err := secret.New(key)
	if err != nil {
		t.Fatalf("secret.New: %v", err)
	}
	t.Cleanup(func() { _ = vault.Close() })
	store, err := db.Open(filepath.Join(t.TempDir(), "adminapi.db"), vault)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	admin, err := store.EnsureAdminUser("root")
	if err != nil {
		t.Fatalf("EnsureAdminUser: %v", err)
	}
	return &env{store: store, admin: admin}
}

func (e *env) seedUser(t *testing.T, discordID string) *db.User {
	t.Helper()
	u, err := e.store.CreateUser(discordID, "user-"+discordID, "")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	return u
}

func (e *env) mount(t *testing.T, runtime RuntimeApplier) http.Handler {
	t.Helper()
	adminAuth, err := auth.NewAdminAuth(auth.AdminAuthConfig{
		Store: e.store, Username: "root", Password: "root-pw", SiteBaseURL: "https://example.com",
	})
	if err != nil {
		t.Fatalf("NewAdminAuth: %v", err)
	}
	t.Cleanup(func() { _ = adminAuth.Close() })
	return auth.AdminSessionMiddleware(adminAuth, NewHandler(HandlerDeps{Store: e.store, Runtime: runtime}))
}

func (e *env) mountUser(t *testing.T, next http.Handler) http.Handler {
	t.Helper()
	userAuth, err := auth.NewUserAuth(auth.UserAuthConfig{
		Store: e.store, Provider: stubProvider{}, ClientID: "client-id",
		SiteBaseURL: "https://example.com",
	})
	if err != nil {
		t.Fatalf("NewUserAuth: %v", err)
	}
	return auth.UserSessionMiddleware(userAuth, next)
}

func (e *env) mountCallerKey(t *testing.T, next http.Handler) http.Handler {
	t.Helper()
	return auth.CallerKeyMiddleware(e.store, next)
}

func (e *env) adminCookie(t *testing.T) *http.Cookie {
	t.Helper()
	token, _, err := e.store.CreateAdminSession(e.admin.ID)
	if err != nil {
		t.Fatalf("CreateAdminSession: %v", err)
	}
	return &http.Cookie{Name: auth.AdminSessionCookieName, Value: token}
}

func (e *env) userCookie(t *testing.T, userID int64) *http.Cookie {
	t.Helper()
	token, _, err := e.store.CreateUserSession(userID)
	if err != nil {
		t.Fatalf("CreateUserSession: %v", err)
	}
	return &http.Cookie{Name: auth.UserSessionCookieName, Value: token}
}

func stationRequest(method, target string, station host.Station, body []byte) *http.Request {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	r := httptest.NewRequest(method, target, reader)
	r.RemoteAddr = "198.51.100.20:4000"
	return r.WithContext(host.WithStation(r.Context(), station))
}

func do(t *testing.T, h http.Handler, r *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func withCookie(r *http.Request, cookie *http.Cookie) *http.Request {
	r.AddCookie(cookie)
	return r
}

func assertNoStore(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
}

func assertErr(t *testing.T, rec *httptest.ResponseRecorder, wantStatus int, wantCode string) {
	t.Helper()
	if rec.Code != wantStatus {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, wantStatus, rec.Body.String())
	}
	assertNoStore(t, rec)
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("error envelope: %v; body=%s", err, rec.Body.String())
	}
	if envelope.Error.Code != wantCode {
		t.Errorf("error code = %q, want %q", envelope.Error.Code, wantCode)
	}
}

func decodeJSON(t *testing.T, rec *httptest.ResponseRecorder, dst any) {
	t.Helper()
	if rec.Code != http.StatusOK && rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 2xx; body=%s", rec.Code, rec.Body.String())
	}
	if rec.Code == http.StatusNoContent {
		return
	}
	if err := json.Unmarshal(rec.Body.Bytes(), dst); err != nil {
		t.Fatalf("json: %v; body=%s", err, rec.Body.String())
	}
}

func adminGet(t *testing.T, e *env, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := do(t, e.mount(t, nil), withCookie(stationRequest(http.MethodGet, path, host.StationAdmin, nil), e.adminCookie(t)))
	assertNoStore(t, rec)
	return rec
}

func adminPatch(t *testing.T, e *env, runtime RuntimeApplier, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	rec := do(t, e.mount(t, runtime), withCookie(stationRequest(http.MethodPatch, path, host.StationAdmin, raw), e.adminCookie(t)))
	assertNoStore(t, rec)
	return rec
}

func adminPost(t *testing.T, e *env, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var raw []byte
	if body != nil {
		var err error
		raw, err = json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
	}
	rec := do(t, e.mount(t, nil), withCookie(stationRequest(http.MethodPost, path, host.StationAdmin, raw), e.adminCookie(t)))
	assertNoStore(t, rec)
	return rec
}

// --- isolation --------------------------------------------------------------

func TestAdminControlsAreAdminSessionOnly(t *testing.T) {
	e := newEnv(t)
	u := e.seedUser(t, "discord-u1")

	userHandler := e.mountUser(t, NewHandler(HandlerDeps{Store: e.store}))
	callerHandler := e.mountCallerKey(t, NewHandler(HandlerDeps{Store: e.store}))

	for _, path := range []string{
		"/admin/api/users",
		"/admin/api/users/1",
		"/admin/api/site-config",
	} {
		// No identity at all on the admin station.
		rec := do(t, e.mount(t, nil), stationRequest(http.MethodGet, path, host.StationAdmin, nil))
		assertErr(t, rec, http.StatusUnauthorized, "unauthorized")

		// A user session on the admin station is refused by the admin
		// middleware before the handler runs (401; the user cookie can never
		// become an admin principal).
		rec = do(t, e.mount(t, nil), withCookie(stationRequest(http.MethodGet, path, host.StationAdmin, nil), e.userCookie(t, u.ID)))
		assertErr(t, rec, http.StatusUnauthorized, "unauthorized")

		// A user session routed through the user middleware is refused by the
		// handler's own station check (the user station is not the admin station).
		rec = do(t, userHandler, withCookie(stationRequest(http.MethodGet, path, host.StationUser, nil), e.userCookie(t, u.ID)))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s via user station: status=%d, want 403; body=%s", path, rec.Code, rec.Body.String())
		}

		// A caller key can never authorize admin routes: the middleware
		// resolves a real caller-key principal, and the handler refuses it as
		// the wrong identity kind.
		gen, err := e.store.RegenerateCallerKey(u.ID)
		if err != nil {
			t.Fatalf("RegenerateCallerKey: %v", err)
		}
		keyReq := stationRequest(http.MethodGet, path, host.StationUser, nil)
		keyReq.Header.Set("Authorization", "Bearer "+gen.Secret)
		rec = do(t, callerHandler, keyReq)
		assertErr(t, rec, http.StatusForbidden, "forbidden")
	}

	// Wrong station for an otherwise valid admin session is refused.
	rec := do(t, e.mount(t, nil), withCookie(stationRequest(http.MethodGet, "/admin/api/users", host.StationUser, nil), e.adminCookie(t)))
	assertErr(t, rec, http.StatusForbidden, "forbidden")
}

// --- users list -------------------------------------------------------------

func TestAdminUsersList(t *testing.T) {
	e := newEnv(t)
	users := make([]*db.User, 0, 25)
	for i := 0; i < 25; i++ {
		users = append(users, e.seedUser(t, fmt.Sprintf("discord-%02d", i)))
	}
	if err := e.store.BanUser(users[0].ID, "spam"); err != nil {
		t.Fatalf("BanUser: %v", err)
	}

	// Default page: page_size clamp and has_more.
	rec := adminGet(t, e, "/admin/api/users?page=1&page_size=10")
	var page struct {
		Data    []userResp `json:"data"`
		HasMore bool       `json:"has_more"`
	}
	decodeJSON(t, rec, &page)
	if len(page.Data) != 10 || !page.HasMore {
		t.Fatalf("page 1: len=%d hasMore=%v", len(page.Data), page.HasMore)
	}
	for _, row := range page.Data {
		if row.DiscordID == "" || row.Username == "" || row.ID <= 0 {
			t.Fatalf("bad row: %+v", row)
		}
	}

	// Banned filter.
	rec = adminGet(t, e, "/admin/api/users?is_banned=true")
	decodeJSON(t, rec, &page)
	if len(page.Data) != 1 || !page.Data[0].IsBanned || page.Data[0].BannedReason != "spam" {
		t.Fatalf("banned filter: %+v", page.Data)
	}

	// Unknown / repeated / malformed parameters are rejected.
	for _, query := range []string{
		"?page=1&page_size=10&unknown=1",
		"?page=1&page=2",
		"?page_size=0",
		"?page=0",
		"?is_banned=maybe",
		"?page_size=abc",
	} {
		rec := adminGet(t, e, "/admin/api/users"+query)
		assertErr(t, rec, http.StatusBadRequest, "invalid_request")
	}

	// No caller-key or session material ever appears in the projection.
	body := rec.Body.String()
	for _, forbidden := range []string{"key_hash", "nbk_", "display_head", "\"secret\""} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("list leaked %q: %s", forbidden, body)
		}
	}
}

func TestAdminUsersSearchFilter(t *testing.T) {
	e := newEnv(t)
	alice1 := e.seedUser(t, "alice-1")
	alice2 := e.seedUser(t, "alice-2")
	e.seedUser(t, "bob")
	if err := e.store.BanUser(alice1.ID, "spam"); err != nil {
		t.Fatalf("BanUser: %v", err)
	}

	list := func(query string) []userResp {
		t.Helper()
		rec := adminGet(t, e, "/admin/api/users"+query)
		var page struct {
			Data    []userResp `json:"data"`
			HasMore bool       `json:"has_more"`
		}
		decodeJSON(t, rec, &page)
		return page.Data
	}
	has := func(rows []userResp, want int64) bool {
		for _, r := range rows {
			if r.ID == want {
				return true
			}
		}
		return false
	}

	// No q: all three users.
	if rows := list(""); len(rows) != 3 {
		t.Fatalf("no q: len=%d, want 3", len(rows))
	}

	// q filters by username and discord_id substring.
	rows := list("?q=alice")
	if len(rows) != 2 || !has(rows, alice1.ID) || !has(rows, alice2.ID) {
		t.Fatalf("q=alice: want alice-1+alice-2, got %d rows", len(rows))
	}

	// q stacks with is_banned.
	rows = list("?q=alice&is_banned=true")
	if len(rows) != 1 || rows[0].ID != alice1.ID {
		t.Fatalf("q=alice banned: want only alice-1, got %d rows", len(rows))
	}
	rows = list("?q=alice&is_banned=false")
	if len(rows) != 1 || rows[0].ID != alice2.ID {
		t.Fatalf("q=alice active: want only alice-2, got %d rows", len(rows))
	}

	// Empty q is accepted (no filter).
	if rows := list("?q="); len(rows) != 3 {
		t.Fatalf("q= empty: len=%d, want 3", len(rows))
	}

	// Unknown parameter is still rejected even when q is present.
	rec := adminGet(t, e, "/admin/api/users?q=alice&unknown=1")
	assertErr(t, rec, http.StatusBadRequest, "invalid_request")

	// Invalid q values are rejected: over-long, control character, NUL.
	for _, query := range []string{
		"?q=" + strings.Repeat("x", 129),
		"?q=ab%0Acd",
		"?q=ab%00cd",
	} {
		rec := adminGet(t, e, "/admin/api/users"+query)
		assertErr(t, rec, http.StatusBadRequest, "invalid_request")
	}

	// LIKE metacharacters in q are escaped: a literal "%" matches only the
	// user whose identity contains a literal %.
	pct, err := e.store.CreateUser("discord-pct", "100%done", "")
	if err != nil {
		t.Fatalf("CreateUser pct: %v", err)
	}
	rows = list("?q=100%25done") // %25 decodes to a literal %
	if len(rows) != 1 || rows[0].ID != pct.ID {
		t.Fatalf("q=100%%done: want only the literal-%% user, got %d rows", len(rows))
	}
	rows = list("?q=%25") // bare "%" -> only the literal-% user
	if len(rows) != 1 || rows[0].ID != pct.ID {
		t.Fatalf("q=%%: want only the literal-%% user, got %d rows", len(rows))
	}
}

func TestAdminUserDetail(t *testing.T) {
	e := newEnv(t)
	u := e.seedUser(t, "discord-u1")
	el, rl := 7, 9
	if _, err := e.store.UpdateUserLimits(u.ID, db.UserLimitPatch{
		EndpointLimitSet: true, EndpointLimit: &el,
		RPMLimitSet: true, RPMLimit: &rl,
	}); err != nil {
		t.Fatalf("UpdateUserLimits: %v", err)
	}
	rec := adminGet(t, e, fmt.Sprintf("/admin/api/users/%d", u.ID))
	var row userResp
	decodeJSON(t, rec, &row)
	if row.ID != u.ID || row.EndpointLimit == nil || *row.EndpointLimit != 7 ||
		row.RPMLimit == nil || *row.RPMLimit != 9 || row.DiscordID != "discord-u1" {
		t.Fatalf("detail row = %+v", row)
	}
	if row.TotalRequests != 0 {
		t.Fatalf("usage totals = %+v", row.TotalRequests)
	}

	// Missing user and the administrator row are indistinguishable not_found.
	for _, id := range []string{"999999", fmt.Sprint(e.admin.ID), "abc", "-3"} {
		rec := adminGet(t, e, "/admin/api/users/"+id)
		assertErr(t, rec, http.StatusNotFound, "not_found")
	}
}

// --- user patch -------------------------------------------------------------

func TestAdminUserPatch(t *testing.T) {
	e := newEnv(t)
	u := e.seedUser(t, "discord-u1")

	rec := adminPatch(t, e, nil, fmt.Sprintf("/admin/api/users/%d", u.ID), map[string]any{"endpoint_limit": 12, "rpm_limit": 30, "lang": "en"})
	var row userResp
	decodeJSON(t, rec, &row)
	if row.EndpointLimit == nil || *row.EndpointLimit != 12 || row.RPMLimit == nil || *row.RPMLimit != 30 || row.Lang != "en" {
		t.Fatalf("patched row = %+v", row)
	}
	if row.Username != "user-discord-u1" {
		t.Fatalf("unrelated fields changed: %+v", row)
	}

	// NULL restores the global default.
	rec = adminPatch(t, e, nil, fmt.Sprintf("/admin/api/users/%d", u.ID), map[string]any{"endpoint_limit": nil, "rpm_limit": nil})
	decodeJSON(t, rec, &row)
	if row.EndpointLimit != nil || row.RPMLimit != nil || row.Lang != "en" {
		t.Fatalf("null restore row = %+v", row)
	}

	// rpm_limit above the global cap is rejected (cap = default_rpm_per_user
	// or the ratelimit default 60).
	if err := e.store.SetSiteConfigValue("default_rpm_per_user", "40"); err != nil {
		t.Fatalf("SetSiteConfigValue: %v", err)
	}
	rec = adminPatch(t, e, nil, fmt.Sprintf("/admin/api/users/%d", u.ID), map[string]any{"rpm_limit": 41})
	assertErr(t, rec, http.StatusBadRequest, "invalid_request")
	rec = adminPatch(t, e, nil, fmt.Sprintf("/admin/api/users/%d", u.ID), map[string]any{"rpm_limit": 40})
	decodeJSON(t, rec, &row)
	if row.RPMLimit == nil || *row.RPMLimit != 40 {
		t.Fatalf("cap-accepted row = %+v", row)
	}

	// Value/field strictness.
	for _, body := range []any{
		map[string]any{"rpm_limit": 0},
		map[string]any{"rpm_limit": -1},
		map[string]any{"endpoint_limit": -1},
		map[string]any{"endpoint_limit": 10001},
		map[string]any{"lang": "fr"},
		map[string]any{"is_banned": true},
		map[string]any{"user_id": 3},
		map[string]any{"usage": 1},
		map[string]any{},
		map[string]any{"endpoint_limit": "10"},
	} {
		rec := adminPatch(t, e, nil, fmt.Sprintf("/admin/api/users/%d", u.ID), body)
		assertErr(t, rec, http.StatusBadRequest, "invalid_request")
	}

	// Trailing JSON tokens and oversized bodies are rejected.
	raw := `{"lang":"zh"} {}`
	rec = do(t, e.mount(t, nil), withCookie(stationRequest(http.MethodPatch, "/admin/api/users/"+fmt.Sprint(u.ID), host.StationAdmin, []byte(raw)), e.adminCookie(t)))
	assertErr(t, rec, http.StatusBadRequest, "invalid_request")
	big := `{"lang":"` + strings.Repeat("a", 17000) + `"}`
	rec = do(t, e.mount(t, nil), withCookie(stationRequest(http.MethodPatch, "/admin/api/users/"+fmt.Sprint(u.ID), host.StationAdmin, []byte(big)), e.adminCookie(t)))
	assertErr(t, rec, http.StatusRequestEntityTooLarge, "payload_too_large")

	// Missing user -> not_found; administrator row -> forbidden.
	rec = adminPatch(t, e, nil, "/admin/api/users/999999", map[string]any{"lang": "zh"})
	assertErr(t, rec, http.StatusNotFound, "not_found")
	rec = adminPatch(t, e, nil, fmt.Sprintf("/admin/api/users/%d", e.admin.ID), map[string]any{"lang": "zh"})
	assertErr(t, rec, http.StatusForbidden, "forbidden")
}

// --- ban / unban ------------------------------------------------------------

func TestAdminBanUnbanInvalidatesSessionsAndCallerKey(t *testing.T) {
	e := newEnv(t)
	u := e.seedUser(t, "discord-u1")
	gen, err := e.store.RegenerateCallerKey(u.ID)
	if err != nil {
		t.Fatalf("RegenerateCallerKey: %v", err)
	}
	sessionTokens := make([]string, 0, 2)
	for i := 0; i < 2; i++ {
		token, _, err := e.store.CreateUserSession(u.ID)
		if err != nil {
			t.Fatalf("CreateUserSession: %v", err)
		}
		sessionTokens = append(sessionTokens, token)
	}

	rec := adminPost(t, e, fmt.Sprintf("/admin/api/users/%d/ban", u.ID), map[string]any{"reason": "abuse"})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("ban status=%d body=%s", rec.Code, rec.Body.String())
	}
	// Same-transaction invalidation: the banned user's sessions and caller
	// key are gone; only the admin's own session row remains.
	user, err := e.store.GetUserByID(u.ID)
	if err != nil || user == nil || !user.IsBanned || user.BannedReason != "abuse" {
		t.Fatalf("banned user = %+v err=%v", user, err)
	}
	for _, token := range sessionTokens {
		if got, err := e.store.AuthenticateUserSession(token); err != nil || got != nil {
			t.Fatalf("banned user session still resolves: %v err=%v", got, err)
		}
	}
	if n, err := e.store.SessionRowCount(); err != nil || n != 1 {
		t.Fatalf("sessions after ban = %d err=%v, want 1 (admin session only)", n, err)
	}
	if key, err := e.store.GetCallerKey(u.ID); err != nil || key != nil {
		t.Fatalf("caller key after ban = %v err=%v, want nil", key, err)
	}
	// Request-time auth fails: the platform caller key no longer resolves.
	if got, err := e.store.GetUserByCallerKey(gen.Secret); err != nil || got != nil {
		t.Fatalf("banned key still resolves: %v err=%v", got, err)
	}
	if token, _, err := e.store.CreateUserSession(u.ID); err != db.ErrBanned || token != "" {
		t.Fatalf("session mint after ban: token=%q err=%v, want ErrBanned", token, err)
	}

	// Ban of a missing user -> not_found; administrator row -> forbidden.
	rec = adminPost(t, e, "/admin/api/users/999999/ban", map[string]any{})
	assertErr(t, rec, http.StatusNotFound, "not_found")
	rec = adminPost(t, e, fmt.Sprintf("/admin/api/users/%d/ban", e.admin.ID), map[string]any{})
	assertErr(t, rec, http.StatusForbidden, "forbidden")

	// Unban clears the ban; sessions/keys are not recreated, and the user can
	// log in again normally.
	rec = adminPost(t, e, fmt.Sprintf("/admin/api/users/%d/unban", u.ID), nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("unban status=%d body=%s", rec.Code, rec.Body.String())
	}
	user, err = e.store.GetUserByID(u.ID)
	if err != nil || user == nil || user.IsBanned || user.BannedReason != "" {
		t.Fatalf("unbanned user = %+v err=%v", user, err)
	}
	for _, token := range sessionTokens {
		if got, err := e.store.AuthenticateUserSession(token); err != nil || got != nil {
			t.Fatalf("pre-ban session resurrected after unban: %v err=%v", got, err)
		}
	}
	if key, _ := e.store.GetCallerKey(u.ID); key != nil {
		t.Fatalf("caller key after unban = %v, want nil (must regenerate)", key)
	}
	if _, _, err := e.store.CreateUserSession(u.ID); err != nil {
		t.Fatalf("session mint after unban: %v", err)
	}

	// A body on unban is rejected; invalid ban reason is invalid_request.
	rec = adminPost(t, e, fmt.Sprintf("/admin/api/users/%d/unban", u.ID), map[string]any{"x": 1})
	assertErr(t, rec, http.StatusBadRequest, "invalid_request")
	rec = adminPost(t, e, fmt.Sprintf("/admin/api/users/%d/ban", u.ID), map[string]any{"reason": "bad\x00reason"})
	assertErr(t, rec, http.StatusBadRequest, "invalid_request")
}

// --- site config ------------------------------------------------------------

func TestSiteConfigRead(t *testing.T) {
	e := newEnv(t)
	rec := adminGet(t, e, "/admin/api/site-config")
	var out map[string]any
	decodeJSON(t, rec, &out)

	// Fresh install: known keys present with effective defaults, typed.
	if out["site_name"] != "" {
		t.Fatalf("site_name = %v", out["site_name"])
	}
	if v, ok := out["default_endpoint_limit"].(float64); !ok || v != 50 {
		t.Fatalf("default_endpoint_limit = %v", out["default_endpoint_limit"])
	}
	if v, ok := out["default_rpm_per_user"].(float64); !ok || v != 60 {
		t.Fatalf("default_rpm_per_user = %v", out["default_rpm_per_user"])
	}
	if v, ok := out["global_rpm"].(float64); !ok || v != 600 {
		t.Fatalf("global_rpm = %v", out["global_rpm"])
	}
	if v, ok := out["egress_global_concurrency"].(float64); !ok || v != 32 {
		t.Fatalf("egress_global_concurrency = %v", out["egress_global_concurrency"])
	}
	if v, ok := out["default_per_endpoint_concurrency"].(float64); !ok || v != 8 {
		t.Fatalf("default_per_endpoint_concurrency = %v", out["default_per_endpoint_concurrency"])
	}
	// Resource-count caps default to their implementation-time values when
	// unset and are typed as integers on read.
	if v, ok := out["default_endpoint_key_limit"].(float64); !ok || v != 20 {
		t.Fatalf("default_endpoint_key_limit = %v", out["default_endpoint_key_limit"])
	}
	if v, ok := out["default_model_limit"].(float64); !ok || v != 100 {
		t.Fatalf("default_model_limit = %v", out["default_model_limit"])
	}
	if v, ok := out["default_binding_limit"].(float64); !ok || v != 50 {
		t.Fatalf("default_binding_limit = %v", out["default_binding_limit"])
	}
	// The authoritative key set is closed: an unknown stored row is never
	// projected, even when manually inserted.
	if err := e.store.SetSiteConfigValue("nonsense_key", "x"); err != nil {
		t.Fatalf("SetSiteConfigValue: %v", err)
	}
	if err := e.store.SetSiteConfigValue("alert_prefs_warn_429", "true"); err != nil {
		t.Fatalf("SetSiteConfigValue: %v", err)
	}
	rec = adminGet(t, e, "/admin/api/site-config")
	decodeJSON(t, rec, &out)
	if _, ok := out["nonsense_key"]; ok {
		t.Fatalf("unknown key projected: %v", out)
	}
	if out["alert_prefs_warn_429"] != "true" {
		t.Fatalf("alert_prefs key missing: %v", out)
	}

	// Query parameters are not accepted.
	rec = adminGet(t, e, "/admin/api/site-config?x=1")
	assertErr(t, rec, http.StatusBadRequest, "invalid_request")
}

func TestSiteConfigResourceLimitPatchAndPersist(t *testing.T) {
	e := newEnv(t)
	applier := &recordingApplier{}

	// default_model_limit is a DB-only int key (min=1): a valid PATCH is
	// applied through the applier (a no-op for this key, but recorded) and
	// persisted; GET returns the typed value.
	rec := adminPatch(t, e, applier, "/admin/api/site-config/default_model_limit", map[string]any{"value": 200})
	var patched siteConfigPatchResp
	decodeJSON(t, rec, &patched)
	if patched.Key != "default_model_limit" || patched.Value != float64(200) {
		t.Fatalf("patch resp = %+v", patched)
	}
	if len(applier.applied) != 1 || applier.applied[0] != "default_model_limit=200" {
		t.Fatalf("applied = %v", applier.applied)
	}
	rec = adminGet(t, e, "/admin/api/site-config")
	var out map[string]any
	decodeJSON(t, rec, &out)
	if out["default_model_limit"] != float64(200) {
		t.Fatalf("default_model_limit after patch = %v", out["default_model_limit"])
	}

	// The bound is enforced: a value above the ceiling is invalid_request.
	rec = adminPatch(t, e, applier, "/admin/api/site-config/default_binding_limit", map[string]any{"value": 1000000})
	assertErr(t, rec, http.StatusBadRequest, "invalid_request")
}

func TestSiteConfigPatchTypedAndRuntimeApply(t *testing.T) {
	e := newEnv(t)
	applier := &recordingApplier{}

	// Text value.
	rec := adminPatch(t, e, applier, "/admin/api/site-config/site_name", map[string]any{"value": "Nonbiri"})
	var patched siteConfigPatchResp
	decodeJSON(t, rec, &patched)
	if patched.Key != "site_name" || patched.Value != "Nonbiri" {
		t.Fatalf("patch resp = %+v", patched)
	}
	// The real applier is a no-op for text keys; the recording applier
	// records every call, so reset it before counting runtime-applicable keys.
	applier.applied = nil

	// Integer value: applied to runtime, persisted, typed on read.
	rec = adminPatch(t, e, applier, "/admin/api/site-config/global_rpm", map[string]any{"value": 1200})
	decodeJSON(t, rec, &patched)
	if patched.Value != float64(1200) {
		t.Fatalf("patch resp = %+v", patched)
	}
	if len(applier.applied) != 1 || applier.applied[0] != "global_rpm=1200" {
		t.Fatalf("applied = %v", applier.applied)
	}
	rec = adminGet(t, e, "/admin/api/site-config")
	var out map[string]any
	decodeJSON(t, rec, &out)
	if out["global_rpm"] != float64(1200) {
		t.Fatalf("global_rpm after patch = %v", out["global_rpm"])
	}

	// Value type/range/control strictness.
	for _, tc := range []struct {
		key  string
		body any
	}{
		{"global_rpm", map[string]any{"value": "1200"}},                      // string for an int key
		{"global_rpm", map[string]any{"value": 0}},                           // below minimum
		{"global_rpm", map[string]any{"value": 4097}},                        // above the limiter ceiling
		{"global_rpm", map[string]any{"value": 1.5}},                         // non-integral
		{"global_rpm", map[string]any{"value": nil}},                         // null rejected
		{"site_name", map[string]any{"value": 42}},                           // number for a text key
		{"site_name", map[string]any{"value": ""}},                           // non-empty required
		{"site_name", map[string]any{"value": "a\nb"}},                       // control character
		{"default_locale", map[string]any{"value": "fr"}},                    // locale whitelist
		{"default_locale", map[string]any{"value": "zh"}},                    // valid (checked below)
		{"legal_authoritative_locale", map[string]any{"value": "fr"}},        // optional locale whitelist
		{"legal_authoritative_locale", map[string]any{"value": ""}},          // valid empty (checked below)
		{"legal_authoritative_locale", map[string]any{"value": "zh"}},        // valid (checked below)
		{"legal_terms_override_zh", map[string]any{"value": 42}},             // number for a multiline key
		{"legal_terms_override_zh", map[string]any{"value": "bad\x00value"}}, // disallowed control char
		{"legal_terms_override_zh", map[string]any{"value": "valid\nline"}},  // valid multiline (checked below)
		{"default_endpoint_limit", map[string]any{"value": -1}},
		{"default_endpoint_key_limit", map[string]any{"value": 0}}, // below min=1
		{"default_model_limit", map[string]any{"value": 0}},        // below min=1
		{"default_binding_limit", map[string]any{"value": 0}},      // below min=1
		{"alert_prefs_x", map[string]any{"value": "line\x00break"}},
	} {
		rec := adminPatch(t, e, applier, "/admin/api/site-config/"+tc.key, tc.body)
		if tc.key == "default_locale" && tc.body.(map[string]any)["value"] == "zh" {
			if rec.Code != http.StatusOK {
				t.Fatalf("valid locale rejected: status=%d body=%s", rec.Code, rec.Body.String())
			}
			continue
		}
		// Legal override / authoritative-locale valid cases that must succeed.
		if tc.key == "legal_authoritative_locale" {
			v := tc.body.(map[string]any)["value"]
			if v == "" || v == "zh" {
				if rec.Code != http.StatusOK {
					t.Fatalf("valid authoritative locale rejected: status=%d body=%s", rec.Code, rec.Body.String())
				}
				continue
			}
		}
		if tc.key == "legal_terms_override_zh" && tc.body.(map[string]any)["value"] == "valid\nline" {
			if rec.Code != http.StatusOK {
				t.Fatalf("valid multiline rejected: status=%d body=%s", rec.Code, rec.Body.String())
			}
			continue
		}
		assertErr(t, rec, http.StatusBadRequest, "invalid_request")
	}
	// Unknown key is strictly not_found.
	rec = adminPatch(t, e, applier, "/admin/api/site-config/not_a_key", map[string]any{"value": "x"})
	assertErr(t, rec, http.StatusNotFound, "not_found")
	// Overlong alert_prefs keys can never be stored and are not known.
	longKey := "alert_prefs_" + strings.Repeat("k", 200)
	rec = adminPatch(t, e, applier, "/admin/api/site-config/"+longKey, map[string]any{"value": "x"})
	assertErr(t, rec, http.StatusNotFound, "not_found")
	// Missing value / empty body.
	rec = adminPatch(t, e, applier, "/admin/api/site-config/site_name", map[string]any{})
	assertErr(t, rec, http.StatusBadRequest, "invalid_request")
	rec = adminPatch(t, e, applier, "/admin/api/site-config/site_name", nil)
	assertErr(t, rec, http.StatusBadRequest, "invalid_request")

	// A failed runtime apply fails closed: DB value unchanged, no revert.
	before := len(applier.applied)
	applier.applyErr = fmt.Errorf("boom")
	rec = adminPatch(t, e, applier, "/admin/api/site-config/global_rpm", map[string]any{"value": 900})
	assertErr(t, rec, http.StatusInternalServerError, "internal")
	applier.applyErr = nil
	if len(applier.applied) != before+1 || len(applier.reverted) != 0 {
		t.Fatalf("apply failure accounting: applied=%v reverted=%v", applier.applied, applier.reverted)
	}
	rec = adminGet(t, e, "/admin/api/site-config")
	decodeJSON(t, rec, &out)
	if out["global_rpm"] != float64(1200) {
		t.Fatalf("global_rpm after failed apply = %v, want 1200", out["global_rpm"])
	}
}

// TestSiteConfigLegalOverrideAcceptsLargeDocument guards the multiline legal
// override keys. textMaxFor must return the per-key max (maxLegalOverrideBytes,
// 64 KiB) for kindMultilineText keys, not the generic maxSiteNameBytes fallback
// (256). A real privacy policy is several KiB; before the fix any value over
// 256 bytes was rejected as "invalid configuration value".
func TestSiteConfigLegalOverrideAcceptsLargeDocument(t *testing.T) {
	e := newEnv(t)
	applier := &recordingApplier{}

	// A representative multi-paragraph document, well over both the 256-byte
	// generic text bound (the old textMaxFor bug) and the 4096-byte identity
	// cap (the old GetSiteConfigValue bug), but under the admin body limit.
	doc := strings.Repeat("## Section\n\nA paragraph of privacy text.\n", 120) // ~7.7 KiB
	if len(doc) <= 4096 {
		t.Fatalf("test document too short: %d bytes", len(doc))
	}
	for _, k := range []string{
		"legal_privacy_override_zh", "legal_privacy_override_en",
		"legal_terms_override_zh", "legal_terms_override_en",
	} {
		rec := adminPatch(t, e, applier, "/admin/api/site-config/"+k, map[string]any{"value": doc})
		if rec.Code != http.StatusOK {
			t.Fatalf("PATCH %s (len=%d): status=%d body=%s", k, len(doc), rec.Code, rec.Body.String())
		}
	}

	// Re-saving an existing multiline override must succeed too: the read-back
	// of the previous value is what triggered the 500 before the
	// GetSiteConfigValue fix (it rejected newlines via validateIdentityText).
	rec := adminPatch(t, e, applier, "/admin/api/site-config/legal_privacy_override_zh", map[string]any{"value": "## Replacement\n\nShorter doc.\n"})
	if rec.Code != http.StatusOK {
		t.Fatalf("re-PATCH over existing multiline: status=%d body=%s", rec.Code, rec.Body.String())
	}

	// Values over the 16 KiB admin body limit are rejected by the HTTP layer
	// (413), independent of the 64 KiB config ceiling; the storage-layer
	// ceiling is covered by the db tests directly.
	overBody := strings.Repeat("x", 17*1024)
	rec = adminPatch(t, e, applier, "/admin/api/site-config/legal_privacy_override_zh", map[string]any{"value": overBody})
	assertErr(t, rec, http.StatusRequestEntityTooLarge, "payload_too_large")

	// The persisted value round-trips through the admin read path unchanged.
	rec = adminGet(t, e, "/admin/api/site-config")
	var out map[string]any
	decodeJSON(t, rec, &out)
	for _, k := range []string{"legal_privacy_override_en", "legal_terms_override_zh", "legal_terms_override_en"} {
		if out[k] != doc {
			t.Fatalf("%s round-trip = %v, want len=%d", k, out[k], len(doc))
		}
	}
	if out["legal_privacy_override_zh"] != "## Replacement\n\nShorter doc.\n" {
		t.Fatalf("legal_privacy_override_zh round-trip = %v", out["legal_privacy_override_zh"])
	}
}

func TestSiteConfigPatchPersistFailureRevertsRuntime(t *testing.T) {
	e := newEnv(t)
	applier := &recordingApplier{}

	// Seed a known value first so the previous-value read is non-empty.
	rec := adminPatch(t, e, applier, "/admin/api/site-config/global_rpm", map[string]any{"value": 500})
	if rec.Code != http.StatusOK {
		t.Fatalf("seed patch status=%d body=%s", rec.Code, rec.Body.String())
	}
	applier.applied = nil
	applier.reverted = nil

	// A persistence failure after a successful runtime apply reverts the
	// runtime singleton to the previous value (DB/runtime stay consistent).
	persistErr := fmt.Errorf("disk full")
	err := applyThenPersist(context.Background(), applier, "global_rpm", "700", "500", func() error {
		return persistErr
	})
	if err == nil || !errors.Is(err, persistErr) {
		t.Fatalf("applyThenPersist err = %v, want the persist error", err)
	}
	if len(applier.applied) != 1 || applier.applied[0] != "global_rpm=700" {
		t.Fatalf("applied = %v", applier.applied)
	}
	if len(applier.reverted) != 1 || applier.reverted[0] != "global_rpm=500" {
		t.Fatalf("reverted = %v, want global_rpm=500 (the previous value)", applier.reverted)
	}

	// A failed runtime apply leaves runtime and DB untouched (no revert, no
	// persist call).
	applier.applied = nil
	applier.reverted = nil
	applier.applyErr = fmt.Errorf("boom")
	persisted := false
	err = applyThenPersist(context.Background(), applier, "global_rpm", "900", "500", func() error {
		persisted = true
		return nil
	})
	if err == nil {
		t.Fatalf("applyThenPersist with apply error = nil, want error")
	}
	if persisted {
		t.Fatalf("persist ran after a failed apply")
	}
	if len(applier.applied) != 1 || len(applier.reverted) != 0 {
		t.Fatalf("apply-failure accounting: applied=%v reverted=%v", applier.applied, applier.reverted)
	}
	applier.applyErr = nil

	// No runtime applier: persistence proceeds without any runtime call.
	err = applyThenPersist(context.Background(), nil, "global_rpm", "800", "500", func() error {
		return nil
	})
	if err != nil {
		t.Fatalf("applyThenPersist without applier: %v", err)
	}
}

// recordingGate records the maintenance-gate Set calls and can fail neither;
// it stands in for internal/maintenance.Gate so the applier test does not couple
// to that package.
type recordingGate struct {
	enabled bool
}

func (g *recordingGate) Set(enabled bool) { g.enabled = enabled }

// TestRuntimeApplierRealSingletons drives the real flowcontrol controller and
// the real egress gate through the applier, proving the apply path accepts
// every registry-valid value and fails closed on an unwired singleton.
func TestRuntimeApplierRealSingletons(t *testing.T) {
	controller, err := flowcontrol.New(flowcontrol.Config{RPM: ratelimit.DefaultRPMConfig()})
	if err != nil {
		t.Fatalf("flowcontrol.New: %v", err)
	}
	defer controller.Close()
	stack, err := egress.NewStack(egress.StackOptions{})
	if err != nil {
		t.Fatalf("egress.NewStack: %v", err)
	}
	defer stack.CloseIdleConnections()

	applier := NewRuntimeApplier(controller, stack, nil, nil)
	if err := applier.ApplySiteConfig(context.Background(), "global_rpm", "900"); err != nil {
		t.Fatalf("apply global_rpm: %v", err)
	}
	if limits := controller.Limits(); limits.GlobalLimit != 900 {
		t.Fatalf("global limit = %d, want 900", limits.GlobalLimit)
	}
	if err := applier.ApplySiteConfig(context.Background(), "default_rpm_per_user", "30"); err != nil {
		t.Fatalf("apply default_rpm_per_user: %v", err)
	}
	if limits := controller.Limits(); limits.PerUserLimit != 30 {
		t.Fatalf("per-user limit = %d, want 30", limits.PerUserLimit)
	}
	if err := applier.ApplySiteConfig(context.Background(), "egress_global_concurrency", "64"); err != nil {
		t.Fatalf("apply egress_global_concurrency: %v", err)
	}
	if limits := stack.ConcurrencyLimits(); limits.Global != 64 {
		t.Fatalf("egress global = %d, want 64", limits.Global)
	}
	if err := applier.ApplySiteConfig(context.Background(), "default_per_endpoint_concurrency", "16"); err != nil {
		t.Fatalf("apply default_per_endpoint_concurrency: %v", err)
	}
	if limits := stack.ConcurrencyLimits(); limits.PerEndpoint != 16 {
		t.Fatalf("egress per-endpoint = %d, want 16", limits.PerEndpoint)
	}
	// Text and alert-preference keys are no-ops.
	if err := applier.ApplySiteConfig(context.Background(), "site_name", "x"); err != nil {
		t.Fatalf("apply site_name: %v", err)
	}
	if err := applier.ApplySiteConfig(context.Background(), "alert_prefs_x", "y"); err != nil {
		t.Fatalf("apply alert_prefs_x: %v", err)
	}
	// Revert is the same operation with the earlier value.
	if err := applier.RevertSiteConfig(context.Background(), "global_rpm", "500"); err != nil {
		t.Fatalf("revert global_rpm: %v", err)
	}
	if limits := controller.Limits(); limits.GlobalLimit != 500 {
		t.Fatalf("global limit after revert = %d, want 500", limits.GlobalLimit)
	}
	// A registry-valid value above the limiter's event-store ceiling fails
	// closed with no state change.
	if err := applier.ApplySiteConfig(context.Background(), "global_rpm", "4097"); err == nil {
		t.Fatalf("apply global_rpm=4097: want error")
	}
	if limits := controller.Limits(); limits.GlobalLimit != 500 {
		t.Fatalf("global limit after failed apply = %d, want 500", limits.GlobalLimit)
	}
	// An unwired singleton fails closed on its own keys only.
	brokenRPM := NewRuntimeApplier(nil, stack, nil, nil)
	if err := brokenRPM.ApplySiteConfig(context.Background(), "global_rpm", "600"); err == nil {
		t.Fatalf("apply with nil rpm controller: want error")
	}
	if err := brokenRPM.ApplySiteConfig(context.Background(), "egress_global_concurrency", "32"); err != nil {
		t.Fatalf("apply with wired egress gate: %v", err)
	}
	brokenGate := NewRuntimeApplier(controller, nil, nil, nil)
	if err := brokenGate.ApplySiteConfig(context.Background(), "egress_global_concurrency", "32"); err == nil {
		t.Fatalf("apply with nil egress gate: want error")
	}
	if err := brokenGate.ApplySiteConfig(context.Background(), "global_rpm", "600"); err != nil {
		t.Fatalf("apply with wired rpm controller: %v", err)
	}
}

// TestRuntimeApplierMaintenanceGateLiveApplies covers the maintenance_mode
// live-apply branch: the canonical "1"/"0" flips the wired gate, a non-canonical
// value fails closed with no state change, a nil gate fails closed on its key
// only, and revert restores the previous state.
func TestRuntimeApplierMaintenanceGateLiveApplies(t *testing.T) {
	controller, err := flowcontrol.New(flowcontrol.Config{RPM: ratelimit.DefaultRPMConfig()})
	if err != nil {
		t.Fatalf("flowcontrol.New: %v", err)
	}
	defer controller.Close()
	stack, err := egress.NewStack(egress.StackOptions{})
	if err != nil {
		t.Fatalf("egress.NewStack: %v", err)
	}
	defer stack.CloseIdleConnections()

	gate := &recordingGate{}
	applier := NewRuntimeApplier(controller, stack, nil, gate)
	if gate.enabled {
		t.Fatalf("gate starts enabled")
	}
	if err := applier.ApplySiteConfig(context.Background(), KeyMaintenanceMode, "1"); err != nil {
		t.Fatalf("apply maintenance_mode=1: %v", err)
	}
	if !gate.enabled {
		t.Fatalf("gate not enabled after apply 1")
	}
	if err := applier.ApplySiteConfig(context.Background(), KeyMaintenanceMode, "0"); err != nil {
		t.Fatalf("apply maintenance_mode=0: %v", err)
	}
	if gate.enabled {
		t.Fatalf("gate still enabled after apply 0")
	}
	// Revert with the previous value restores it.
	if err := applier.RevertSiteConfig(context.Background(), KeyMaintenanceMode, "1"); err != nil {
		t.Fatalf("revert maintenance_mode=1: %v", err)
	}
	if !gate.enabled {
		t.Fatalf("gate not enabled after revert to 1")
	}
	// A non-canonical value fails closed and leaves the gate unchanged.
	for _, bad := range []string{"true", "false", "yes", "", "2"} {
		if err := applier.ApplySiteConfig(context.Background(), KeyMaintenanceMode, bad); err == nil {
			t.Fatalf("apply maintenance_mode=%q: want error", bad)
		}
	}
	if !gate.enabled {
		t.Fatalf("gate changed after failed apply")
	}
	// A nil gate fails closed on maintenance_mode only (other keys still work).
	noGate := NewRuntimeApplier(controller, stack, nil, nil)
	if err := noGate.ApplySiteConfig(context.Background(), KeyMaintenanceMode, "1"); err == nil {
		t.Fatalf("apply with nil gate: want error")
	}
	if err := noGate.ApplySiteConfig(context.Background(), "global_rpm", "600"); err != nil {
		t.Fatalf("apply global_rpm with nil gate: %v", err)
	}
}

// TestMaintenanceGatePersistFailureRevertsRuntime covers the DB/runtime
// consistency step for maintenance_mode specifically: a successful runtime apply
// (gate flips on) followed by a persistence failure must revert the runtime gate
// back to its previous value, so the database and the live gate cannot drift.
func TestMaintenanceGatePersistFailureRevertsRuntime(t *testing.T) {
	controller, err := flowcontrol.New(flowcontrol.Config{RPM: ratelimit.DefaultRPMConfig()})
	if err != nil {
		t.Fatalf("flowcontrol.New: %v", err)
	}
	defer controller.Close()
	stack, err := egress.NewStack(egress.StackOptions{})
	if err != nil {
		t.Fatalf("egress.NewStack: %v", err)
	}
	defer stack.CloseIdleConnections()
	gate := &recordingGate{}
	applier := NewRuntimeApplier(controller, stack, nil, gate)

	// Seed the gate off, then apply "1" with a persist that fails: the runtime
	// gate must revert to the previous (off) value, not stay on.
	if err := applier.ApplySiteConfig(context.Background(), KeyMaintenanceMode, "0"); err != nil {
		t.Fatalf("seed apply 0: %v", err)
	}
	if gate.enabled {
		t.Fatalf("gate not off after seed")
	}
	persistErr := fmt.Errorf("disk full")
	err = applyThenPersist(context.Background(), applier, KeyMaintenanceMode, "1", "0", func() error {
		return persistErr
	})
	if err == nil || !errors.Is(err, persistErr) {
		t.Fatalf("applyThenPersist err = %v, want the persist error", err)
	}
	if gate.enabled {
		t.Fatalf("gate stayed on after a persist failure; runtime did not revert to the previous off value")
	}

	// A failed runtime apply leaves the gate untouched (no persist call, no
	// revert), matching the global fail-closed contract.
	gate.enabled = true
	brokenApply := &runtimeApplier{maintenance: gate}
	// Force the maintenance branch to fail by feeding a non-canonical value.
	if err := applyThenPersist(context.Background(), brokenApply, KeyMaintenanceMode, "yes", "1", func() error {
		t.Fatalf("persist must not run after a failed apply")
		return nil
	}); err == nil {
		t.Fatalf("applyThenPersist with a non-canonical maintenance value: want error")
	}
	if !gate.enabled {
		t.Fatalf("gate changed after a failed apply")
	}
}

// TestRuntimeApplierRevertMissingRowUsesCanonicalDefault covers the missing-row
// revert path: when the prior database row is absent the handler reads an
// empty previous, so a persist failure after a first-ever write must revert
// the runtime singleton to the frozen canonical default for the key — not fail
// on the empty string and leave the singleton in the just-applied state. This
// holds for every runtime key (bool and int), matching each key's frozen
// default rather than only patching the bool case.
func TestRuntimeApplierRevertMissingRowUsesCanonicalDefault(t *testing.T) {
	persistErr := errors.New("disk full")

	// maintenance_mode (bool): frozen canonical default is false ("0").
	gate := &recordingGate{}
	maintApplier := NewRuntimeApplier(nil, nil, nil, gate)
	if err := applyThenPersist(context.Background(), maintApplier, KeyMaintenanceMode, "1", "", func() error {
		return persistErr
	}); err == nil || !errors.Is(err, persistErr) {
		t.Fatalf("maintenance missing-row err = %v, want the persist error", err)
	}
	if gate.enabled {
		t.Fatalf("maintenance gate stayed on after a missing-row persist failure; want revert to canonical default false")
	}

	// global_rpm (int): frozen canonical default is DefaultRPMGlobalLimit. The
	// controller starts at that default; applying a new value then failing the
	// persist must revert to the same default, not the empty string.
	controller, err := flowcontrol.New(flowcontrol.Config{RPM: ratelimit.DefaultRPMConfig()})
	if err != nil {
		t.Fatalf("flowcontrol.New: %v", err)
	}
	defer controller.Close()
	if limits := controller.Limits(); limits.GlobalLimit != ratelimit.DefaultRPMGlobalLimit {
		t.Fatalf("controller default = %d, want %d", limits.GlobalLimit, ratelimit.DefaultRPMGlobalLimit)
	}
	rpmApplier := NewRuntimeApplier(controller, nil, nil, nil)
	if err := applyThenPersist(context.Background(), rpmApplier, KeyGlobalRPM, "1200", "", func() error {
		return persistErr
	}); err == nil || !errors.Is(err, persistErr) {
		t.Fatalf("rpm missing-row err = %v, want the persist error", err)
	}
	if limits := controller.Limits(); limits.GlobalLimit != ratelimit.DefaultRPMGlobalLimit {
		t.Fatalf("global limit after missing-row revert = %d, want canonical default %d", limits.GlobalLimit, ratelimit.DefaultRPMGlobalLimit)
	}
}

// TestParseCanonicalBoolByteExact covers that parseCanonicalBool accepts only
// the exact bytes "1"/"0". Surrounding whitespace (which an earlier TrimSpace
// would have silently accepted) is rejected, so a corrupted or hand-edited row
// can never flip the runtime singleton through a whitespace-padded value.
func TestParseCanonicalBoolByteExact(t *testing.T) {
	for _, ok := range []string{"0", "1"} {
		v, err := parseCanonicalBool(ok)
		if err != nil {
			t.Fatalf("parseCanonicalBool(%q) err = %v", ok, err)
		}
		if v != (ok == "1") {
			t.Fatalf("parseCanonicalBool(%q) = %v", ok, v)
		}
	}
	for _, bad := range []string{"", " ", " 1", "1 ", " 0 ", "\t1", "1\n", "true", "false", "TRUE", "yes", "2", "01"} {
		if _, err := parseCanonicalBool(bad); err == nil {
			t.Fatalf("parseCanonicalBool(%q) = nil, want error", bad)
		}
	}
}

// TestSiteConfigPatchConcurrentUpdatesKeepDBAndRuntimeConsistent verifies the
// per-handler serialization of the site-config read→apply→persist→revert step.
// Concurrent PATCHes to the same runtime key cannot interleave, so once they
// all settle the persisted database value and the live runtime singleton agree
// (no drift), regardless of which value won. This is stronger than asserting
// each response is merely a valid status code: it proves apply and persist
// orders cannot diverge under concurrency.
//
// The applier drives the real atomic maintenance.Gate (race-free under
// concurrent Set) and yields the scheduler right after the live apply. The
// yield widens the apply→persist window so concurrent patches exercise real
// scheduler interleaving under the race detector; the per-handler lock keeps
// each patch's read→apply→persist→revert step atomic, so once all patches
// settle the persisted database value and the live gate agree (no drift). The
// deterministic serialization test below pins one patch inside its apply to
// prove the per-handler lock is what prevents drift.
func TestSiteConfigPatchConcurrentUpdatesKeepDBAndRuntimeConsistent(t *testing.T) {
	e := newEnv(t)
	gate := maintenance.New()
	applier := &yieldingGateApplier{gate: gate}
	// Mount ONCE so every concurrent request shares the same Handler (and its
	// site-config serialization mutex); mounting per request would hand each
	// request a fresh Handler/mutex and defeat the test.
	h := e.mount(t, applier)
	cookie := e.adminCookie(t)

	bodyOn, _ := json.Marshal(map[string]any{"value": true})
	bodyOff, _ := json.Marshal(map[string]any{"value": false})
	patch := func(body []byte) {
		req := withCookie(stationRequest(http.MethodPatch, "/admin/api/site-config/"+KeyMaintenanceMode, host.StationAdmin, body), cookie)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("patch status=%d body=%s", rec.Code, rec.Body.String())
		}
	}
	// Seed a known starting state (row present, gate off) so the first revert
	// path is well-defined, then hammer concurrent toggles.
	patch(bodyOff)

	const goroutines, iters = 12, 12
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				if (g+i)%2 == 0 {
					patch(bodyOn)
				} else {
					patch(bodyOff)
				}
			}
		}(g)
	}
	wg.Wait()

	// After all concurrent updates settle, the persisted value and the live
	// gate must agree: the last serialized PATCH determined both, so they can
	// never drift apart regardless of which value won.
	dbVal, err := e.store.GetSiteConfigValue(KeyMaintenanceMode)
	if err != nil {
		t.Fatalf("get maintenance_mode: %v", err)
	}
	if dbVal != "0" && dbVal != "1" {
		t.Fatalf("persisted value not canonical: %q", dbVal)
	}
	if want := dbVal == "1"; gate.Enabled() != want {
		t.Fatalf("DB/runtime drift: db=%q gate=%v", dbVal, gate.Enabled())
	}
}

// yieldingGateApplier is a RuntimeApplier that drives the real atomic
// maintenance.Gate and yields the scheduler immediately after the live apply.
// It is a test double for the concurrency test: the yield widens the
// apply→persist window so concurrent patches interleave without the handler's
// serialization. It reuses the package's canonical parsers and the missing-row
// canonical-default fallback so its apply/revert match the real applier's
// semantics for maintenance_mode.
type yieldingGateApplier struct {
	gate *maintenance.Gate
}

func (a *yieldingGateApplier) ApplySiteConfig(_ context.Context, key, value string) error {
	if key != KeyMaintenanceMode {
		return nil
	}
	enabled, err := parseCanonicalBool(value)
	if err != nil {
		return err
	}
	a.gate.Set(enabled)
	runtime.Gosched()
	return nil
}

func (a *yieldingGateApplier) RevertSiteConfig(ctx context.Context, key, previous string) error {
	if previous == "" {
		previous = canonicalDefaultStored(key)
	}
	return a.ApplySiteConfig(ctx, key, previous)
}

// blockingGateApplier drives the real atomic maintenance.Gate and lets a test
// pin one patch inside its live apply (after the gate is flipped, before
// applyThenPersist reaches the persist step) so a second concurrent patch is
// forced to either wait on the handler's per-key lock (serialized) or read the
// stale pre-first-patch row and interleave its own apply/persist (drift). It
// reuses the package's canonical parsers and the missing-row canonical-default
// fallback so its apply/revert match the real applier's semantics for
// maintenance_mode. The coordination channels are consumed once by the next
// Apply call; a second Apply sees no channels and returns immediately.
type blockingGateApplier struct {
	gate *maintenance.Gate

	mu           sync.Mutex
	applyStarted chan struct{}
	releaseApply chan struct{}
	blockNext    bool
}

func (a *blockingGateApplier) configure(started, release chan struct{}) {
	a.mu.Lock()
	a.applyStarted = started
	a.releaseApply = release
	a.blockNext = true
	a.mu.Unlock()
}

func (a *blockingGateApplier) ApplySiteConfig(_ context.Context, key, value string) error {
	if key != KeyMaintenanceMode {
		return nil
	}
	enabled, err := parseCanonicalBool(value)
	if err != nil {
		return err
	}
	a.gate.Set(enabled)
	a.mu.Lock()
	started := a.applyStarted
	release := a.releaseApply
	block := a.blockNext
	a.blockNext = false
	a.applyStarted = nil
	a.releaseApply = nil
	a.mu.Unlock()
	if block && started != nil {
		close(started)
		if release != nil {
			<-release
		}
	}
	return nil
}

func (a *blockingGateApplier) RevertSiteConfig(ctx context.Context, key, previous string) error {
	if previous == "" {
		previous = canonicalDefaultStored(key)
	}
	return a.ApplySiteConfig(ctx, key, previous)
}

// TestSiteConfigPatchSerializesConcurrentUpdatesDeterministically proves the
// per-handler lock serializes concurrent patches to the same runtime key so
// the read→apply→persist→revert step cannot interleave. A blocking applier pins
// the first patch (on) inside its live apply — the gate is already flipped on
// but the persist step has not run, so the database still holds the seeded
// value. A second concurrent patch (off) must wait for the first to fully
// settle (apply + persist) before it can read the prior value; with the
// per-handler lock it cannot read the stale seeded row and interleave its own
// apply/persist, so the two never drift the database and the live gate apart.
//
// The assertion that the second patch has not completed while the first is
// pinned is the serialization proof: without the per-key lock the second
// patch would read the stale row, apply off, and persist off while the first
// is still pinned, then the first would persist on, leaving the database at
// "1" and the gate off (drift) — and the second would have completed during
// the pin, tripping this assertion. With the lock the second waits, the final
// state is the last serialized patch (off), and database and gate agree.
func TestSiteConfigPatchSerializesConcurrentUpdatesDeterministically(t *testing.T) {
	e := newEnv(t)
	gate := maintenance.New()
	blocker := &blockingGateApplier{gate: gate}
	// Mount ONCE so both patches share the same Handler (and its site-config
	// serialization mutex); mounting per request would hand each request a
	// fresh Handler/mutex and defeat the test.
	h := e.mount(t, blocker)
	cookie := e.adminCookie(t)

	// Seed a known row so the prior value is non-empty and deterministic.
	if err := e.store.SetSiteConfigValue(KeyMaintenanceMode, "0"); err != nil {
		t.Fatalf("seed: %v", err)
	}

	patch := func(value any) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]any{"value": value})
		req := withCookie(stationRequest(http.MethodPatch, "/admin/api/site-config/"+KeyMaintenanceMode, host.StationAdmin, body), cookie)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	// Pin the first patch (on) inside its live apply: the gate is flipped on,
	// then the applier blocks before applyThenPersist reaches the persist step.
	// While it is pinned the database still holds the seeded "0".
	started := make(chan struct{})
	release := make(chan struct{})
	blocker.configure(started, release)

	g1Err := make(chan error, 1)
	go func() {
		rec := patch(true)
		if rec.Code != http.StatusOK {
			g1Err <- fmt.Errorf("first patch status=%d body=%s", rec.Code, rec.Body.String())
			return
		}
		g1Err <- nil
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("first patch did not pin inside its live apply")
	}

	// Launch the second patch (off). With the per-handler lock it blocks at the
	// mutex while the first patch is pinned; without the lock it would proceed,
	// read the stale seeded row, and complete during the pin.
	g2Err := make(chan error, 1)
	go func() {
		rec := patch(false)
		if rec.Code != http.StatusOK {
			g2Err <- fmt.Errorf("second patch status=%d body=%s", rec.Code, rec.Body.String())
			return
		}
		g2Err <- nil
	}()

	select {
	case err := <-g2Err:
		t.Fatalf("second patch completed while the first was still pinned (lock did not serialize): %v", err)
	case <-time.After(50 * time.Millisecond):
		// The second patch is blocked behind the first patch's lock, as expected.
	}

	// Release the first patch: it persists on and unlocks; the second patch then
	// reads the now-persisted "1", applies off, and persists off.
	close(release)
	if err := <-g1Err; err != nil {
		t.Fatalf("first patch: %v", err)
	}
	if err := <-g2Err; err != nil {
		t.Fatalf("second patch: %v", err)
	}

	// After both serialized patches settle, the persisted value and the live
	// gate agree: the second (last) patch set both to off. Without the lock the
	// second patch would have read the stale "0" during the pin, applied off,
	// and persisted off, then the first would persist on, leaving the database
	// at "1" and the gate off (drift).
	dbVal, err := e.store.GetSiteConfigValue(KeyMaintenanceMode)
	if err != nil {
		t.Fatalf("get maintenance_mode: %v", err)
	}
	if dbVal != "0" {
		t.Fatalf("persisted value = %q, want 0 (last serialized patch was off)", dbVal)
	}
	if gate.Enabled() {
		t.Fatalf("DB/runtime drift: db=%q gate=true, want gate=false", dbVal)
	}
}

// TestSiteConfigBoolTogglePatch covers the maintenance_mode / registration_open
// toggles: a JSON bool is accepted and stored as the canonical "1"/"0"; the
// typed read path echoes a bool; non-bool values (string, number, null) are
// rejected. maintenance_mode is now a runtime key (applied to the maintenance
// gate), so the recording applier records its apply; registration_open is not.
func TestSiteConfigBoolTogglePatch(t *testing.T) {
	e := newEnv(t)
	applier := &recordingApplier{}

	rec := adminPatch(t, e, applier, "/admin/api/site-config/maintenance_mode", map[string]any{"value": true})
	var patched siteConfigPatchResp
	decodeJSON(t, rec, &patched)
	if patched.Key != "maintenance_mode" || patched.Value != true {
		t.Fatalf("maintenance_mode patch = %+v", patched)
	}
	// maintenance_mode is a runtime key: the recording applier observed the
	// live-apply of the canonical stored value ("1").
	if len(applier.applied) != 1 || applier.applied[0] != "maintenance_mode=1" {
		t.Fatalf("maintenance_mode runtime apply = %v", applier.applied)
	}
	applier.applied = nil

	rec = adminPatch(t, e, applier, "/admin/api/site-config/registration_open", map[string]any{"value": false})
	decodeJSON(t, rec, &patched)
	if patched.Key != "registration_open" || patched.Value != false {
		t.Fatalf("registration_open patch = %+v", patched)
	}

	rec = adminGet(t, e, "/admin/api/site-config")
	var out map[string]any
	decodeJSON(t, rec, &out)
	if out["maintenance_mode"] != true {
		t.Fatalf("maintenance_mode=%v want true", out["maintenance_mode"])
	}
	if out["registration_open"] != false {
		t.Fatalf("registration_open=%v want false", out["registration_open"])
	}

	for _, bad := range []any{"true", 1, 0, "yes", nil} {
		rec = adminPatch(t, e, applier, "/admin/api/site-config/maintenance_mode", map[string]any{"value": bad})
		assertErr(t, rec, http.StatusBadRequest, "invalid_request")
	}
}
