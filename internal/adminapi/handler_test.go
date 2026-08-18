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
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/auth"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/egress"
	"github.com/waiting-here/NonbiriAPI/internal/flowcontrol"
	"github.com/waiting-here/NonbiriAPI/internal/host"
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
		{"global_rpm", map[string]any{"value": "1200"}},   // string for an int key
		{"global_rpm", map[string]any{"value": 0}},        // below minimum
		{"global_rpm", map[string]any{"value": 4097}},     // above the limiter ceiling
		{"global_rpm", map[string]any{"value": 1.5}},      // non-integral
		{"global_rpm", map[string]any{"value": nil}},      // null rejected
		{"site_name", map[string]any{"value": 42}},        // number for a text key
		{"site_name", map[string]any{"value": ""}},        // non-empty required
		{"site_name", map[string]any{"value": "a\nb"}},    // control character
		{"default_locale", map[string]any{"value": "fr"}}, // locale whitelist
		{"default_locale", map[string]any{"value": "zh"}}, // valid (checked below)
		{"default_endpoint_limit", map[string]any{"value": -1}},
		{"alert_prefs_x", map[string]any{"value": "line\x00break"}},
	} {
		rec := adminPatch(t, e, applier, "/admin/api/site-config/"+tc.key, tc.body)
		if tc.key == "default_locale" && tc.body.(map[string]any)["value"] == "zh" {
			if rec.Code != http.StatusOK {
				t.Fatalf("valid locale rejected: status=%d body=%s", rec.Code, rec.Body.String())
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

	applier := NewRuntimeApplier(controller, stack, nil)
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
	brokenRPM := NewRuntimeApplier(nil, stack, nil)
	if err := brokenRPM.ApplySiteConfig(context.Background(), "global_rpm", "600"); err == nil {
		t.Fatalf("apply with nil rpm controller: want error")
	}
	if err := brokenRPM.ApplySiteConfig(context.Background(), "egress_global_concurrency", "32"); err != nil {
		t.Fatalf("apply with wired egress gate: %v", err)
	}
	brokenGate := NewRuntimeApplier(controller, nil, nil)
	if err := brokenGate.ApplySiteConfig(context.Background(), "egress_global_concurrency", "32"); err == nil {
		t.Fatalf("apply with nil egress gate: want error")
	}
	if err := brokenGate.ApplySiteConfig(context.Background(), "global_rpm", "600"); err != nil {
		t.Fatalf("apply with wired rpm controller: %v", err)
	}
}
