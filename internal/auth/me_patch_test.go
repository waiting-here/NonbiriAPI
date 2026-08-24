package auth

// PATCH /api/me tests: the session-only self-service profile update. Only
// lang and game_profile_public are accepted; endpoint_limit / rpm_limit /
// concurrency_limit / ban state / usage / body user id are never accepted.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/host"
)

func patchMeDirect(t *testing.T, service *UserAuth, st *db.Store, userID int64, body any) *httptest.ResponseRecorder {
	t.Helper()
	token, _, err := st.CreateUserSession(userID)
	if err != nil {
		t.Fatalf("CreateUserSession: %v", err)
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	r := stationRequest(http.MethodPatch, "https://example.com/api/me", host.StationUser, bytes.NewReader(raw))
	r.AddCookie(&http.Cookie{Name: UserSessionCookieName, Value: token})
	rec := httptest.NewRecorder()
	service.PatchMe(rec, r)
	return rec
}

func meStoreWithCap(t *testing.T, cap int) (*db.Store, *UserAuth) {
	t.Helper()
	st := authTestStore(t)
	service := newTestUserAuth(t, st, &fakeDiscordProvider{login: DiscordLogin{
		Identity: DiscordIdentity{ID: "discord-1", Username: "alice"},
	}}, nil)
	if _, err := st.CreateUser("discord-1", "alice", ""); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if err := st.SetSiteConfigValue("default_rpm_per_user", fmt.Sprint(cap)); err != nil {
		t.Fatalf("SetSiteConfigValue: %v", err)
	}
	return st, service
}

func TestPatchMeLang(t *testing.T) {
	st, service := meStoreWithCap(t, 40)
	user, err := st.GetUserByDiscordID("discord-1")
	if err != nil || user == nil {
		t.Fatalf("user: %v", err)
	}

	// Lang update.
	rec := patchMeDirect(t, service, st, user.ID, map[string]any{"lang": "en"})
	if rec.Code != http.StatusOK {
		t.Fatalf("lang patch status=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("cache-control=%q", rec.Header().Get("Cache-Control"))
	}
	var envelope userEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("envelope: %v body=%s", err, rec.Body.String())
	}
	if envelope.User.Lang != "en" || envelope.User.ID != user.ID {
		t.Fatalf("lang patch user = %+v", envelope.User)
	}

	// lang=zh also accepted.
	rec = patchMeDirect(t, service, st, user.ID, map[string]any{"lang": "zh"})
	if rec.Code != http.StatusOK {
		t.Fatalf("lang=zh status=%d body=%s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("envelope: %v body=%s", err, rec.Body.String())
	}
	if envelope.User.Lang != "zh" {
		t.Fatalf("lang=zh user = %+v", envelope.User)
	}
}

func TestPatchMeGameProfilePrivacyIsAuthoritativeAndStrict(t *testing.T) {
	st, service := meStoreWithCap(t, 40)
	user, err := st.GetUserByDiscordID("discord-1")
	if err != nil || user == nil {
		t.Fatalf("user: %v", err)
	}

	for _, raw := range []string{
		`{"game_profile_public":true,"game_profile_public":false}`,
		`{"game_profile_public":null}`,
		`{"game_profile_public":1}`,
	} {
		token, _, err := st.CreateUserSession(user.ID)
		if err != nil {
			t.Fatal(err)
		}
		r := stationRequest(http.MethodPatch, "https://example.com/api/me", host.StationUser, bytes.NewReader([]byte(raw)))
		r.AddCookie(&http.Cookie{Name: UserSessionCookieName, Value: token})
		rec := httptest.NewRecorder()
		service.PatchMe(rec, r)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("raw=%s status=%d body=%s", raw, rec.Code, rec.Body.String())
		}
	}

	// The preference may be updated together with lang and is reflected by
	// both the response projection and the authoritative database row.
	token, _, err := st.CreateUserSession(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	r := stationRequest(http.MethodPatch, "https://example.com/api/me", host.StationUser,
		bytes.NewReader([]byte(`{"lang":"en","game_profile_public":true}`)))
	r.AddCookie(&http.Cookie{Name: UserSessionCookieName, Value: token})
	rec := httptest.NewRecorder()
	service.PatchMe(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("privacy patch status=%d body=%s", rec.Code, rec.Body.String())
	}
	var envelope userEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if !envelope.User.GameProfilePublic || envelope.User.Lang != "en" {
		t.Fatalf("privacy response=%+v", envelope.User)
	}
	stored, err := st.GetUserByID(user.ID)
	if err != nil || stored == nil || !stored.GameProfilePublic {
		t.Fatalf("stored privacy=%v err=%v", stored, err)
	}
}

func TestPatchMeRejectsForbiddenFieldsAndMalformedBodies(t *testing.T) {
	st, service := meStoreWithCap(t, 60)
	user, err := st.GetUserByDiscordID("discord-1")
	if err != nil || user == nil {
		t.Fatalf("user: %v", err)
	}

	for _, body := range []any{
		map[string]any{"lang": "fr"},
		map[string]any{"lang": ""},
		// rpm_limit is admin-set only; self-service updates are rejected.
		map[string]any{"rpm_limit": 25},
		map[string]any{"rpm_limit": nil},
		map[string]any{"endpoint_limit": 10},
		map[string]any{"concurrency_limit": 5},
		map[string]any{"concurrency_limit": nil},
		map[string]any{"is_banned": true},
		map[string]any{"admin": true},
		map[string]any{"usage": 1},
		map[string]any{"id": 5},
		map[string]any{"user_id": 5},
		map[string]any{},
	} {
		rec := patchMeDirect(t, service, st, user.ID, body)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("body %v: status=%d want 400; body=%s", body, rec.Code, rec.Body.String())
		}
		var env struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil || env.Error.Code != "invalid_request" {
			t.Fatalf("body %v: code=%q err=%v", body, env.Error.Code, err)
		}
	}

	// Trailing tokens are rejected.
	r := stationRequest(http.MethodPatch, "https://example.com/api/me", host.StationUser, bytes.NewReader([]byte(`{"lang":"zh"} {}`)))
	token, _, err := st.CreateUserSession(user.ID)
	if err != nil {
		t.Fatalf("CreateUserSession: %v", err)
	}
	r.AddCookie(&http.Cookie{Name: UserSessionCookieName, Value: token})
	rec := httptest.NewRecorder()
	service.PatchMe(rec, r)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("trailing token status=%d body=%s", rec.Code, rec.Body.String())
	}

	// Empty body and missing session.
	r = stationRequest(http.MethodPatch, "https://example.com/api/me", host.StationUser, nil)
	rec = httptest.NewRecorder()
	service.PatchMe(rec, r)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no session status=%d body=%s", rec.Code, rec.Body.String())
	}

	// An admin session cookie can never satisfy the user profile route.
	admin, err := st.EnsureAdminUser("root")
	if err != nil {
		t.Fatalf("EnsureAdminUser: %v", err)
	}
	adminToken, _, err := st.CreateAdminSession(admin.ID)
	if err != nil {
		t.Fatalf("CreateAdminSession: %v", err)
	}
	r = stationRequest(http.MethodPatch, "https://example.com/api/me", host.StationUser, bytes.NewReader([]byte(`{"lang":"zh"}`)))
	r.AddCookie(&http.Cookie{Name: AdminSessionCookieName, Value: adminToken})
	rec = httptest.NewRecorder()
	service.PatchMe(rec, r)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("admin cookie status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestStrictProfileJSONBoundsDepthAndFields(t *testing.T) {
	deep := strings.Repeat("[", maxStrictJSONDepth+1) + "0" + strings.Repeat("]", maxStrictJSONDepth+1)
	if err := scanStrictJSON(strings.NewReader(deep)); err == nil {
		t.Fatal("strict profile decoder accepted over-deep value")
	}
	var fields strings.Builder
	fields.WriteString("{")
	for i := 0; i <= maxStrictJSONFields; i++ {
		if i > 0 {
			fields.WriteByte(',')
		}
		fmt.Fprintf(&fields, "\"x%d\":0", i)
	}
	fields.WriteString("}")
	if err := scanStrictJSON(strings.NewReader(fields.String())); err == nil {
		t.Fatal("strict profile decoder accepted over-budget fields")
	}
}

func TestPatchMeRejectsStrictBoundsAtHTTPBoundary(t *testing.T) {
	st, service := meStoreWithCap(t, 60)
	user, err := st.GetUserByDiscordID("discord-1")
	if err != nil || user == nil {
		t.Fatalf("user: %v", err)
	}
	deep := strings.Repeat("[", maxStrictJSONDepth+1) + "0" + strings.Repeat("]", maxStrictJSONDepth+1)
	var fields strings.Builder
	fields.WriteString("{")
	for i := 0; i <= maxStrictJSONFields; i++ {
		if i > 0 {
			fields.WriteByte(',')
		}
		fmt.Fprintf(&fields, "\"x%d\":0", i)
	}
	fields.WriteString("}")
	for _, raw := range []string{deep, fields.String()} {
		token, _, err := st.CreateUserSession(user.ID)
		if err != nil {
			t.Fatal(err)
		}
		r := stationRequest(http.MethodPatch, "https://example.com/api/me", host.StationUser, strings.NewReader(raw))
		r.AddCookie(&http.Cookie{Name: UserSessionCookieName, Value: token})
		rec := httptest.NewRecorder()
		service.PatchMe(rec, r)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("raw length=%d status=%d body=%s", len(raw), rec.Code, rec.Body.String())
		}
	}
}

func TestPatchMeRouteRegisteredOnHandlerTree(t *testing.T) {
	st, service := meStoreWithCap(t, 60)
	user, err := st.GetUserByDiscordID("discord-1")
	if err != nil || user == nil {
		t.Fatalf("user: %v", err)
	}
	token, _, err := st.CreateUserSession(user.ID)
	if err != nil {
		t.Fatalf("CreateUserSession: %v", err)
	}
	r := stationRequest(http.MethodPatch, "https://example.com/api/me", host.StationUser, bytes.NewReader([]byte(`{"lang":"zh"}`)))
	r.AddCookie(&http.Cookie{Name: UserSessionCookieName, Value: token})
	rec := httptest.NewRecorder()
	service.Handler().ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("registered route status=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("cache-control=%q", rec.Header().Get("Cache-Control"))
	}
	// GET /api/me still works unchanged.
	r = stationRequest(http.MethodGet, "https://example.com/api/me", host.StationUser, nil)
	r.AddCookie(&http.Cookie{Name: UserSessionCookieName, Value: token})
	rec = httptest.NewRecorder()
	service.Handler().ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/me status=%d body=%s", rec.Code, rec.Body.String())
	}
}
