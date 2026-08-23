package checkin

// HTTP-level tests for the user-station check-in routes: the exact disabled
// body (timezone-unset indistinguishable from every other disabled cause),
// the enabled status shape with string amounts, the POST success/error
// envelopes over the real repository, and the session/station boundary.

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/auth"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbtest"
	"github.com/waiting-here/NonbiriAPI/internal/host"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

// stubProvider satisfies auth.DiscordProvider; the tests never exchange an
// OAuth code.
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
	mount http.Handler
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	key := bytes.Repeat([]byte{0x5d}, secret.MasterKeyBytes)
	vault, err := secret.New(key)
	clear(key)
	if err != nil {
		t.Fatalf("secret.New: %v", err)
	}
	t.Cleanup(func() { _ = vault.Close() })
	dbPath := filepath.Join(t.TempDir(), "checkin.db")
	dbtest.EnsureOwnerOnlyParent(t, dbPath)
	store, err := db.Open(dbPath, vault)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	user, err := store.CreateUser("discord-c1", "checkin-user", "")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	userAuth, err := auth.NewUserAuth(auth.UserAuthConfig{
		Store: store, Provider: stubProvider{}, ClientID: "client-id",
		SiteBaseURL: "https://example.com",
	})
	if err != nil {
		t.Fatalf("NewUserAuth: %v", err)
	}
	env := &testEnv{store: store, user: user}
	env.mount = New(Deps{UserAuth: userAuth, Store: store}).Handler()
	return env
}

// setConfig writes raw site_config rows; legitimate writes go through the
// validated administrator path (covered in the adminapi package).
func setConfig(t *testing.T, env *testEnv, key, value string) {
	t.Helper()
	if _, err := env.store.DB().Exec(`INSERT INTO site_config (key, value, updated_at) VALUES (?, ?, 0)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value); err != nil {
		t.Fatalf("set site_config %s: %v", key, err)
	}
}

func enableCheckin(t *testing.T, env *testEnv, mode string) {
	t.Helper()
	if err := env.store.SetSiteTimezoneOffsetMinutes(0); err != nil {
		t.Fatalf("set UTC: %v", err)
	}
	setConfig(t, env, db.CheckinModeKey, mode)
	setConfig(t, env, db.CheckinAwardMinMilliKey, "100")
	setConfig(t, env, db.CheckinAwardMaxMilliKey, "100")
}

func setCredits(t *testing.T, env *testEnv, uid, value int64) {
	t.Helper()
	if _, err := env.store.DB().Exec(`UPDATE users SET credits=? WHERE id=?`, value, uid); err != nil {
		t.Fatalf("set credits: %v", err)
	}
}

func request(t *testing.T, env *testEnv, method, target string, body string, withCookie bool) *httptest.ResponseRecorder {
	t.Helper()
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	r := httptest.NewRequest(method, target, reader)
	r.RemoteAddr = "198.51.100.20:4000"
	r = r.WithContext(host.WithStation(r.Context(), host.StationUser))
	if withCookie {
		token, _, err := env.store.CreateUserSession(env.user.ID)
		if err != nil {
			t.Fatalf("CreateUserSession: %v", err)
		}
		r.AddCookie(&http.Cookie{Name: auth.UserSessionCookieName, Value: token})
	}
	rec := httptest.NewRecorder()
	env.mount.ServeHTTP(rec, r)
	return rec
}

func decodeErr(t *testing.T, rec *httptest.ResponseRecorder) (int, string, string, string) {
	t.Helper()
	var envelope struct {
		Error struct {
			Code    string `json:"code"`
			Source  string `json:"source"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode error envelope: %v; body=%s", err, rec.Body.String())
	}
	return rec.Code, envelope.Error.Code, envelope.Error.Source, envelope.Error.Message
}

func assertErr(t *testing.T, rec *httptest.ResponseRecorder, wantStatus int, wantCode string) {
	t.Helper()
	if rec.Code != wantStatus {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, wantStatus, rec.Body.String())
	}
	_, code, _, _ := decodeErr(t, rec)
	if code != wantCode {
		t.Fatalf("error code = %q, want %q", code, wantCode)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
}

// TestCheckinDisabledBodyExact pins the disabled shape: the ENTIRE body is
// {"enabled":false}, byte-identical for every refused cause.
func TestCheckinDisabledBodyExact(t *testing.T) {
	env := newTestEnv(t)

	// Timezone unset, mode enabled.
	setConfig(t, env, db.CheckinModeKey, db.CheckinModeEnabled)
	rec := request(t, env, http.MethodGet, "/api/checkin", "", true)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", rec.Code, rec.Body.String())
	}
	unsetBody := strings.TrimSpace(rec.Body.String())
	if unsetBody != `{"enabled":false}` {
		t.Fatalf("unset-timezone body = %s, want exactly {\"enabled\":false}", unsetBody)
	}

	// Explicitly disabled mode with a configured timezone: byte-identical.
	if err := env.store.SetSiteTimezoneOffsetMinutes(0); err != nil {
		t.Fatalf("set UTC: %v", err)
	}
	setConfig(t, env, db.CheckinModeKey, db.CheckinModeDisabled)
	rec = request(t, env, http.MethodGet, "/api/checkin", "", true)
	if strings.TrimSpace(rec.Body.String()) != unsetBody {
		t.Fatalf("disabled body differs from unset-timezone body")
	}

	// level_gated below the bypass level: byte-identical too.
	enableCheckin(t, env, db.CheckinModeLevelGated)
	rec = request(t, env, http.MethodGet, "/api/checkin", "", true)
	if strings.TrimSpace(rec.Body.String()) != unsetBody {
		t.Fatalf("level_gated body differs from unset-timezone body")
	}

	// POST refusals share ONE envelope (status/code/source/message) across
	// every disabled cause.
	setConfig(t, env, db.CheckinModeKey, db.CheckinModeDisabled)
	first := request(t, env, http.MethodPost, "/api/checkin", "", true)
	code1, source1, msg1 := "", "", ""
	{
		rec := first
		if rec.Code != http.StatusForbidden {
			t.Fatalf("disabled POST status = %d; body=%s", rec.Code, rec.Body.String())
		}
		_, code1, source1, msg1 = decodeErr(t, rec)
	}
	if code1 != httperr.CodeFeatureDisabled || source1 != "platform" || msg1 != "签到未启用" {
		t.Fatalf("disabled POST envelope = (%q, %q, %q)", code1, source1, msg1)
	}
	// Unset timezone produces the byte-identical envelope.
	if err := env.store.SetSiteTimezoneOffsetMinutes(0); err != nil {
		t.Fatalf("set UTC: %v", err)
	}
	setConfig(t, env, db.CheckinModeKey, db.CheckinModeEnabled)
	if _, err := env.store.DB().Exec(`DELETE FROM site_config WHERE key=?`, db.SiteTimezoneKey); err != nil {
		t.Fatalf("clear timezone: %v", err)
	}
	rec = request(t, env, http.MethodPost, "/api/checkin", "", true)
	if rec.Code != first.Code || rec.Body.String() != first.Body.String() {
		t.Fatalf("unset-timezone POST envelope %s differs from disabled %s", rec.Body.String(), first.Body.String())
	}
}

// TestCheckinEnabledShapeAndPost verifies the enabled status shape field by
// field and the POST success/duplicate lifecycle.
func TestCheckinEnabledShapeAndPost(t *testing.T) {
	env := newTestEnv(t)
	enableCheckin(t, env, db.CheckinModeEnabled)
	setCredits(t, env, env.user.ID, -1500)

	rec := request(t, env, http.MethodGet, "/api/checkin", "", true)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET status = %d; body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	want := map[string]any{
		"enabled":           true,
		"checked_in_today":  false,
		"credits":           "-1500",
		"award_min_milli":   "100",
		"award_max_milli":   "100",
		"credits_cap_milli": strconv.FormatInt(db.DefaultCreditsCapMilli, 10),
	}
	if len(body) != len(want) {
		t.Fatalf("GET keys = %v, want exactly %v", body, want)
	}
	for k, v := range want {
		got, ok := body[k]
		if !ok || fmt.Sprint(got) != fmt.Sprint(v) {
			t.Fatalf("GET %s = (%v, %v), want %v", k, got, ok, v)
		}
	}

	// POST: success carries the drawn award and the new balance as strings.
	rec = request(t, env, http.MethodPost, "/api/checkin", "", true)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST status = %d; body=%s", rec.Code, rec.Body.String())
	}
	var done struct {
		AwardMilli string `json:"award_milli"`
		Credits    string `json:"credits"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &done); err != nil {
		t.Fatalf("decode POST: %v", err)
	}
	if done.AwardMilli != "100" || done.Credits != "-1400" {
		t.Fatalf("POST result = (%q, %q), want (100, -1400)", done.AwardMilli, done.Credits)
	}
	var storedAward int64
	if err := env.store.DB().QueryRow(`SELECT award FROM checkins WHERE user_id=?`, env.user.ID).Scan(&storedAward); err != nil || storedAward != 100 {
		t.Fatalf("stored award = (%d, %v)", storedAward, err)
	}

	// The status now reports today's check-in.
	rec = request(t, env, http.MethodGet, "/api/checkin", "", true)
	body = map[string]any{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode after POST: %v", err)
	}
	if body["checked_in_today"] != true || body["credits"] != "-1400" {
		t.Fatalf("status after POST = %v", body)
	}

	// The second POST of the day: 409 already_checked_in, nothing re-applied.
	rec = request(t, env, http.MethodPost, "/api/checkin", "", true)
	assertErr(t, rec, http.StatusConflict, httperr.CodeAlreadyCheckedIn)
	_, _, _, msg := decodeErr(t, rec)
	if msg != "今日已签到" {
		t.Fatalf("already message = %q", msg)
	}
	var credits int64
	if err := env.store.DB().QueryRow(`SELECT credits FROM users WHERE id=?`, env.user.ID).Scan(&credits); err != nil || credits != -1400 {
		t.Fatalf("credits after duplicate = (%d, %v), want -1400 (no double award)", credits, err)
	}
}

// TestCheckinCapEnvelope verifies the cap refusal envelope and that the day
// is not consumed.
func TestCheckinCapEnvelope(t *testing.T) {
	env := newTestEnv(t)
	enableCheckin(t, env, db.CheckinModeEnabled)
	setConfig(t, env, db.CreditsCapMilliKey, "100")
	setCredits(t, env, env.user.ID, 100)

	rec := request(t, env, http.MethodPost, "/api/checkin", "", true)
	assertErr(t, rec, http.StatusForbidden, httperr.CodeCheckinCapReached)
	_, code, source, msg := decodeErr(t, rec)
	if code != httperr.CodeCheckinCapReached || source != "platform" || !strings.Contains(msg, "悠哉积分已达签到上限") {
		t.Fatalf("cap envelope = (%q, %q, %q)", code, source, msg)
	}
	// The refusal consumed neither the day nor the balance.
	var rows int
	if err := env.store.DB().QueryRow(`SELECT COUNT(*) FROM checkins WHERE user_id=?`, env.user.ID).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("cap refusal wrote rows: (%d, %v)", rows, err)
	}
	// Lower the balance: the same day succeeds.
	setCredits(t, env, env.user.ID, 50)
	rec = request(t, env, http.MethodPost, "/api/checkin", "", true)
	if rec.Code != http.StatusOK {
		t.Fatalf("post-cap-lowered status = %d; body=%s", rec.Code, rec.Body.String())
	}
}

// TestCheckinBoundaryRules covers the auth/station boundary and the strict
// input rules (no query parameters, no body).
func TestCheckinBoundaryRules(t *testing.T) {
	env := newTestEnv(t)
	enableCheckin(t, env, db.CheckinModeEnabled)

	// No session: the stable 401 envelope on both routes.
	assertErr(t, request(t, env, http.MethodGet, "/api/checkin", "", false), http.StatusUnauthorized, httperr.CodeUnauthorized)
	assertErr(t, request(t, env, http.MethodPost, "/api/checkin", "", false), http.StatusUnauthorized, httperr.CodeUnauthorized)

	// Wrong station: refused before any identity is consulted.
	r := httptest.NewRequest(http.MethodGet, "/api/checkin", nil)
	r = r.WithContext(host.WithStation(r.Context(), host.StationAdmin))
	rec := httptest.NewRecorder()
	env.mount.ServeHTTP(rec, r)
	assertErr(t, rec, http.StatusForbidden, httperr.CodeForbidden)

	// Method mismatch: the stable 405 envelope (no such route for PUT).
	assertErr(t, request(t, env, http.MethodPut, "/api/checkin", "", true), http.StatusMethodNotAllowed, httperr.CodeMethodNotAllowed)

	// Query parameters are rejected on both routes.
	assertErr(t, request(t, env, http.MethodGet, "/api/checkin?day=1", "", true), http.StatusBadRequest, httperr.CodeInvalidRequest)
	assertErr(t, request(t, env, http.MethodPost, "/api/checkin?force=1", "", true), http.StatusBadRequest, httperr.CodeInvalidRequest)

	// A POST body is rejected: the client may never supply any part of the
	// check-in (award, day, id).
	assertErr(t, request(t, env, http.MethodPost, "/api/checkin", `{"award_milli":"9999999"}`, true), http.StatusBadRequest, httperr.CodeInvalidRequest)
	assertErr(t, request(t, env, http.MethodPost, "/api/checkin", `{}`, true), http.StatusBadRequest, httperr.CodeInvalidRequest)

	// Nothing above consumed the day.
	var rows int
	if err := env.store.DB().QueryRow(`SELECT COUNT(*) FROM checkins WHERE user_id=?`, env.user.ID).Scan(&rows); err != nil && err != sql.ErrNoRows {
		t.Fatalf("count checkins: %v", err)
	}
	if rows != 0 {
		t.Fatalf("boundary probes wrote %d checkin rows", rows)
	}
}
