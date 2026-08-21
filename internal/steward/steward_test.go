package steward

// Steward frame tests: the per-request live level-5 gate (grant/demote
// immediacy), the station boundary (admin host refused, admin session never
// accepted), the unregistered-sub-path envelope ordering (403 before 404),
// and the minimal identity handed to sub-handlers.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/waiting-here/NonbiriAPI/internal/auth"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbtest"
	"github.com/waiting-here/NonbiriAPI/internal/host"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

type stewardEnv struct {
	store *db.Store
	frame *Handler
	user  *db.User
}

func newStewardEnv(t *testing.T) *stewardEnv {
	t.Helper()
	key := bytes.Repeat([]byte{0x51}, secret.MasterKeyBytes)
	vault, err := secret.New(key)
	if err != nil {
		t.Fatalf("secret.New: %v", err)
	}
	t.Cleanup(func() { _ = vault.Close() })
	dbPath := t.TempDir() + "/steward.db"
	dbtest.EnsureOwnerOnlyParent(t, dbPath)
	store, err := db.Open(dbPath, vault)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	userAuth, err := auth.NewUserAuth(auth.UserAuthConfig{
		Store: store, Provider: stubProvider{}, ClientID: "client-id",
		SiteBaseURL: "https://example.com",
	})
	if err != nil {
		t.Fatalf("NewUserAuth: %v", err)
	}
	t.Cleanup(func() { _ = userAuth.Close() })
	user, err := store.CreateUser("discord-steward", "steward-user", "")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	return &stewardEnv{store: store, frame: New(Deps{UserAuth: userAuth, Store: store}), user: user}
}

type stubProvider struct{}

func (stubProvider) AuthorizationURL(context.Context, auth.DiscordAuthorizeRequest) (string, error) {
	return "https://discord.example/authorize", nil
}

func (stubProvider) Exchange(context.Context, string, string) (auth.DiscordLogin, error) {
	return auth.DiscordLogin{}, context.Canceled
}

func (e *stewardEnv) get(t *testing.T, path string, station host.Station, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "https://example.com"+path, nil)
	r = r.WithContext(host.WithStation(r.Context(), station))
	if cookie != nil {
		r.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	e.frame.Handler().ServeHTTP(rec, r)
	return rec
}

func (e *stewardEnv) userCookie(t *testing.T) *http.Cookie {
	t.Helper()
	token, _, err := e.store.CreateUserSession(e.user.ID)
	if err != nil {
		t.Fatalf("CreateUserSession: %v", err)
	}
	return &http.Cookie{Name: auth.UserSessionCookieName, Value: token}
}

func (e *stewardEnv) setLevel(t *testing.T, level *int) {
	t.Helper()
	if _, err := e.store.SetUserManualLevel(e.user.ID, level); err != nil {
		t.Fatalf("SetUserManualLevel(%+v): %v", level, err)
	}
}

func codeOf(t *testing.T, rec *httptest.ResponseRecorder) (int, string) {
	t.Helper()
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("envelope: %v body=%s", err, rec.Body.String())
	}
	return rec.Code, envelope.Error.Code
}

// TestStewardGateGrantAndInstantRevocation: below level 5 the prefix is
// forbidden; a manual level-5 grant opens it on the very next request of the
// SAME session; a demotion (manual reset) closes it again on the next request
// — no session refresh, no caching.
func TestStewardGateGrantAndInstantRevocation(t *testing.T) {
	e := newStewardEnv(t)
	cookie := e.userCookie(t)

	rec := e.get(t, "/api/steward/anything", host.StationUser, cookie)
	if code, c := codeOf(t, rec); code != http.StatusForbidden || c != "forbidden" {
		t.Fatalf("L1 request = (%d, %s), want 403 forbidden", code, c)
	}

	five := 5
	e.setLevel(t, &five)
	rec = e.get(t, "/api/steward/anything", host.StationUser, cookie)
	// Level 5 but an unregistered sub-path: the gate passed, the mux 404s.
	if code, c := codeOf(t, rec); code != http.StatusNotFound || c != "not_found" {
		t.Fatalf("L5 unregistered = (%d, %s), want 404 not_found", code, c)
	}

	// Demote to manual 4: same session, next request is already forbidden.
	four := 4
	e.setLevel(t, &four)
	rec = e.get(t, "/api/steward/anything", host.StationUser, cookie)
	if code, c := codeOf(t, rec); code != http.StatusForbidden || c != "forbidden" {
		t.Fatalf("demoted request = (%d, %s), want 403 forbidden", code, c)
	}

	// Reset to automatic: still forbidden (auto levels stop at 4).
	e.setLevel(t, nil)
	rec = e.get(t, "/api/steward/anything", host.StationUser, cookie)
	if code, _ := codeOf(t, rec); code != http.StatusForbidden {
		t.Fatalf("reset-to-auto request = %d, want 403", code)
	}
}

// TestStewardStationBoundary: the prefix is user-station only. A request that
// arrives on the admin station (the root mux serves both hosts) is refused
// before any identity is consulted; an administrator session is never
// accepted either (the user-session middleware itself rejects it).
func TestStewardStationBoundary(t *testing.T) {
	e := newStewardEnv(t)
	five := 5
	e.setLevel(t, &five) // even a level-5 user gains nothing off-station

	userCookie := e.userCookie(t)
	for _, station := range []host.Station{host.StationAdmin, host.StationUnknown} {
		rec := e.get(t, "/api/steward/logs", station, userCookie)
		if code, c := codeOf(t, rec); code != http.StatusForbidden || c != "forbidden" {
			t.Fatalf("station %v = (%d, %s), want 403 forbidden", station, code, c)
		}
	}

	// An administrator session on the user station is not a steward identity.
	admin, err := e.store.EnsureAdminUser("root")
	if err != nil {
		t.Fatalf("EnsureAdminUser: %v", err)
	}
	adminToken, _, err := e.store.CreateAdminSession(admin.ID)
	if err != nil {
		t.Fatalf("CreateAdminSession: %v", err)
	}
	adminCookie := &http.Cookie{Name: auth.UserSessionCookieName, Value: adminToken}
	rec := e.get(t, "/api/steward/logs", host.StationUser, adminCookie)
	if rec.Code != http.StatusUnauthorized && rec.Code != http.StatusForbidden {
		t.Fatalf("admin session = %d, want 401/403", rec.Code)
	}

	// No session at all: unauthorized before the level gate.
	rec = e.get(t, "/api/steward/logs", host.StationUser, nil)
	if code, c := codeOf(t, rec); code != http.StatusUnauthorized || c != "unauthorized" {
		t.Fatalf("anonymous = (%d, %s), want 401 unauthorized", code, c)
	}
}

// TestStewardSubHandlerMinimalIdentity registers a business route through the
// frame and pins what the sub-handler receives: only the opaque steward
// identity (user id + role), never a user row or session material.
func TestStewardSubHandlerMinimalIdentity(t *testing.T) {
	e := newStewardEnv(t)
	var gotUserID int64
	var gotRole string
	e.frame.Handle(http.MethodGet, "/api/steward/probe", func(_ http.ResponseWriter, r *http.Request, p Principal) {
		gotUserID = p.UserID
		gotRole = p.Role
	})

	five := 5
	e.setLevel(t, &five)
	rec := e.get(t, "/api/steward/probe", host.StationUser, e.userCookie(t))
	if rec.Code != http.StatusOK {
		t.Fatalf("probe status=%d body=%s", rec.Code, rec.Body.String())
	}
	if gotUserID != e.user.ID || gotRole != RoleLevel5 {
		t.Fatalf("sub-handler identity = (%d, %q), want (%d, %q)", gotUserID, gotRole, e.user.ID, RoleLevel5)
	}
}

// TestStewardNilDepsFailClosed: a frame without its dependencies refuses
// everything with service_unavailable rather than letting a request through.
func TestStewardNilDepsFailClosed(t *testing.T) {
	h := New(Deps{})
	rec := httptest.NewRecorder()
	h.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/steward/logs", nil))
	if code, c := codeOf(t, rec); code != http.StatusServiceUnavailable || c != "service_unavailable" {
		t.Fatalf("nil deps = (%d, %s), want 503 service_unavailable", code, c)
	}
}
