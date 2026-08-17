package auth

// PATCH /api/me tests: the session-only self-service profile update. Only
// lang and the user's own rpm_limit are accepted; rpm_limit is clamped to the
// global per-user cap (or restored to the default with an explicit null);
// endpoint_limit / ban state / usage / body user id are never accepted.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
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

func TestPatchMeLangAndRPMLimit(t *testing.T) {
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

	// RPM below the cap stores the value.
	rec = patchMeDirect(t, service, st, user.ID, map[string]any{"rpm_limit": 25})
	if rec.Code != http.StatusOK {
		t.Fatalf("rpm patch status=%d body=%s", rec.Code, rec.Body.String())
	}
	updated, err := st.GetUserByID(user.ID)
	if err != nil || updated.RPMLimit == nil || *updated.RPMLimit != 25 {
		t.Fatalf("rpm stored = %+v err=%v", updated, err)
	}

	// RPM above the cap is clamped to the cap server-side.
	rec = patchMeDirect(t, service, st, user.ID, map[string]any{"rpm_limit": 100})
	if rec.Code != http.StatusOK {
		t.Fatalf("clamp patch status=%d body=%s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("envelope: %v", err)
	}
	if envelope.User.RPMLimit == nil || *envelope.User.RPMLimit != 40 {
		t.Fatalf("clamped rpm = %+v, want 40", envelope.User.RPMLimit)
	}

	// An explicit null restores the global default.
	rec = patchMeDirect(t, service, st, user.ID, map[string]any{"rpm_limit": nil})
	if rec.Code != http.StatusOK {
		t.Fatalf("null patch status=%d body=%s", rec.Code, rec.Body.String())
	}
	updated, err = st.GetUserByID(user.ID)
	if err != nil || updated.RPMLimit != nil {
		t.Fatalf("rpm after null = %+v err=%v, want nil", updated, err)
	}
	if updated.Lang != "en" {
		t.Fatalf("lang regressed: %q", updated.Lang)
	}
}

func TestPatchMeCapResolverOverride(t *testing.T) {
	st := authTestStore(t)
	if _, err := st.CreateUser("discord-1", "alice", ""); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	user, err := st.GetUserByDiscordID("discord-1")
	if err != nil {
		t.Fatalf("user: %v", err)
	}
	service := newTestUserAuth(t, st, &fakeDiscordProvider{login: DiscordLogin{
		Identity: DiscordIdentity{ID: "discord-1", Username: "alice"},
	}}, nil)
	service.userRPMLimitCap = func(context.Context) (int, error) { return 42, nil }
	rec := patchMeDirect(t, service, st, user.ID, map[string]any{"rpm_limit": 500})
	if rec.Code != http.StatusOK {
		t.Fatalf("patch status=%d body=%s", rec.Code, rec.Body.String())
	}
	var envelope userEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("envelope: %v", err)
	}
	if envelope.User.RPMLimit == nil || *envelope.User.RPMLimit != 42 {
		t.Fatalf("resolver-clamped rpm = %+v, want 42", envelope.User.RPMLimit)
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
		map[string]any{"rpm_limit": 0},
		map[string]any{"rpm_limit": -5},
		map[string]any{"rpm_limit": 1.5},
		map[string]any{"rpm_limit": "30"},
		map[string]any{"endpoint_limit": 10},
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
