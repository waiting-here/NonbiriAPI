package lifecycle

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/auth"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbtest"
	"github.com/waiting-here/NonbiriAPI/internal/elevation"
	"github.com/waiting-here/NonbiriAPI/internal/host"
	"github.com/waiting-here/NonbiriAPI/internal/httpmw"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

// stubDiscordProvider satisfies auth.DiscordProvider for lifecycle tests; the
// OAuth round-trip is not exercised here (users are seeded directly).
type stubDiscordProvider struct{}

func (stubDiscordProvider) AuthorizationURL(_ context.Context, req auth.DiscordAuthorizeRequest) (string, error) {
	return "https://discord.example/authorize?state=" + req.State, nil
}
func (stubDiscordProvider) Exchange(_ context.Context, _, _ string) (auth.DiscordLogin, error) {
	return auth.DiscordLogin{}, errors.New("not used")
}

type harness struct {
	t            *testing.T
	store        *db.Store
	vault        *secret.Vault
	elevation    *elevation.Manager
	userAuth     *auth.UserAuth
	adminAuth    *auth.AdminAuth
	svc          *Service
	adminUser    *db.User
	adminToken   string
	userSessions map[int64]string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	key := bytes.Repeat([]byte{0x31}, secret.MasterKeyBytes)
	vault, err := secret.New(key)
	clear(key)
	if err != nil {
		t.Fatalf("secret.New: %v", err)
	}
	dbPath := filepath.Join(t.TempDir(), "lifecycle.db")
	dbtest.EnsureOwnerOnlyParent(t, dbPath)
	st, err := db.Open(dbPath, vault)
	if err != nil {
		_ = vault.Close()
		t.Fatalf("db.Open: %v", err)
	}
	elevKey := bytes.Repeat([]byte{0x52}, 32)
	mgr, err := elevation.NewManagerWithKey(elevKey, time.Minute)
	if err != nil {
		t.Fatalf("elevation.NewManagerWithKey: %v", err)
	}
	provider := stubDiscordProvider{}
	userAuth, err := auth.NewUserAuth(auth.UserAuthConfig{
		Store: st, Provider: provider, ClientID: "client-id", Elevation: mgr,
		SiteBaseURL: "https://example.com",
		RegistrationGate: func(context.Context) (auth.RegistrationGate, error) {
			return auth.RegistrationGate{}, nil
		},
	})
	if err != nil {
		t.Fatalf("NewUserAuth: %v", err)
	}
	adminAuth, err := auth.NewAdminAuth(auth.AdminAuthConfig{
		Store: st, Username: "root", Password: "correct-password",
		SiteBaseURL: "https://example.com",
	})
	if err != nil {
		t.Fatalf("NewAdminAuth: %v", err)
	}
	svc, err := NewService(Config{
		Store: st, Elevation: mgr, AdminVerifier: adminAuth,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	admin, err := st.EnsureAdminUser("root")
	if err != nil {
		t.Fatalf("EnsureAdminUser: %v", err)
	}
	adminToken, _, err := st.CreateAdminSession(admin.ID)
	if err != nil {
		t.Fatalf("CreateAdminSession: %v", err)
	}
	h := &harness{
		t: t, store: st, vault: vault, elevation: mgr,
		userAuth: userAuth, adminAuth: adminAuth, svc: svc,
		adminUser: admin, adminToken: adminToken, userSessions: make(map[int64]string),
	}
	t.Cleanup(func() {
		_ = svc.Close()
		_ = userAuth.Close()
		_ = adminAuth.Close()
		_ = mgr.Close()
		_ = st.Close()
		_ = vault.Close()
	})
	return h
}

func (h *harness) seedUser(discordID string) *db.User {
	h.t.Helper()
	u, err := h.store.CreateDiscordUser(discordID, "user-"+discordID, "")
	if err != nil {
		h.t.Fatalf("CreateDiscordUser: %v", err)
	}
	return u
}

func (h *harness) userSessionCookie(userID int64) *http.Cookie {
	h.t.Helper()
	if token := h.userSessions[userID]; token != "" {
		return &http.Cookie{Name: auth.UserSessionCookieName, Value: token}
	}
	token, _, err := h.store.CreateUserSession(userID)
	if err != nil {
		h.t.Fatalf("CreateUserSession: %v", err)
	}
	h.userSessions[userID] = token
	return &http.Cookie{Name: auth.UserSessionCookieName, Value: token}
}

func (h *harness) issueUser(userID int64) string {
	h.t.Helper()
	session := h.userSessionCookie(userID).Value
	token, _, err := h.elevation.IssueBound(userID, elevation.KindUser, db.SessionHash(session))
	if err != nil {
		h.t.Fatalf("issue user elevation: %v", err)
	}
	return token
}

func (h *harness) issueUserKind(userID int64, kind elevation.Kind) string {
	h.t.Helper()
	session := h.userSessionCookie(userID).Value
	token, _, err := h.elevation.IssueBound(userID, kind, db.SessionHash(session))
	if err != nil {
		h.t.Fatalf("issue user-kind elevation: %v", err)
	}
	return token
}

func (h *harness) issueAdmin() string {
	h.t.Helper()
	token, _, err := h.elevation.IssueBound(h.adminUser.ID, elevation.KindAdmin, db.SessionHash(h.adminToken))
	if err != nil {
		h.t.Fatalf("issue admin elevation: %v", err)
	}
	return token
}

func (h *harness) userDeleteHandler() http.Handler {
	return h.userAuth.Middleware(httpmw.API(http.HandlerFunc(h.svc.DeleteOwnAccountHandler)))
}

func (h *harness) adminDeleteHandler() http.Handler {
	// The delete handler reads the target id via r.PathValue, which is set by
	// ServeMux pattern matching. Route through a single-route mux so direct
	// httptest calls resolve {id} exactly as the production Mount does.
	mux := http.NewServeMux()
	mux.Handle("DELETE /admin/api/users/{id}", h.adminAuth.Middleware(httpmw.API(http.HandlerFunc(h.svc.DeleteUserHandler))))
	return mux
}

func (h *harness) adminElevateHandler() http.Handler {
	return h.adminAuth.Middleware(httpmw.API(http.HandlerFunc(h.svc.ElevateAdminHandler)))
}

func stationReq(method, target string, station host.Station, body string) *http.Request {
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, target, nil)
	} else {
		r = httptest.NewRequest(method, target, strings.NewReader(body))
	}
	r.Header.Set("Content-Type", "application/json")
	r.RemoteAddr = "198.51.100.20:4000"
	return r.WithContext(host.WithStation(r.Context(), station))
}

func wantCode(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status=%d want=%d body=%s", rec.Code, want, rec.Body.String())
	}
}

func wantNoStore(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("Cache-Control=%q want no-store", cc)
	}
}

func TestDeleteRetirementBoundaryCommitAbortAndMalformed(t *testing.T) {
	h := newHarness(t)
	var mu sync.Mutex
	events := make([]string, 0, 12)
	begin := func(userID int64) (func() bool, func() bool, error) {
		if current, err := h.store.GetUserByID(userID); err != nil || current == nil {
			return nil, nil, errors.New("target must exist when retirement begins")
		}
		mu.Lock()
		events = append(events, "begin:"+strconv.FormatInt(userID, 10))
		mu.Unlock()
		var done atomic.Bool
		terminal := func(kind string) func() bool {
			return func() bool {
				if !done.CompareAndSwap(false, true) {
					return false
				}
				mu.Lock()
				events = append(events, kind+":"+strconv.FormatInt(userID, 10))
				mu.Unlock()
				return true
			}
		}
		return terminal("commit"), terminal("abort"), nil
	}
	beginDeletion := func(userID int64) (func() bool, func() bool, error) {
		mu.Lock()
		events = append(events, "delete-begin:"+strconv.FormatInt(userID, 10))
		mu.Unlock()
		var done atomic.Bool
		terminal := func(kind string) func() bool {
			return func() bool {
				if !done.CompareAndSwap(false, true) {
					return false
				}
				mu.Lock()
				events = append(events, kind+":"+strconv.FormatInt(userID, 10))
				mu.Unlock()
				return true
			}
		}
		return terminal("delete-commit"), terminal("delete-abort"), nil
	}
	svc, err := NewService(Config{
		Store: h.store, Elevation: h.elevation, AdminVerifier: h.adminAuth,
		BeginUserRetirement: begin,
		BeginUserDeletion:   beginDeletion,
		PreDeleteUser: func(userID int64) {
			mu.Lock()
			events = append(events, "predelete:"+strconv.FormatInt(userID, 10))
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close() })

	self := h.seedUser("retire-self")
	selfToken := h.issueUser(self.ID)
	selfBinding := db.SessionHash(h.userSessions[self.ID])
	if err := svc.DeleteOwnAccountBound(context.Background(), self, selfToken, selfBinding); err != nil {
		t.Fatalf("self delete: %v", err)
	}
	adminTarget := h.seedUser("retire-admin")
	adminToken := h.issueAdmin()
	if err := svc.DeleteUserAsAdminBound(context.Background(), h.adminUser, adminTarget.ID, adminToken, db.SessionHash(h.adminToken)); err != nil {
		t.Fatalf("admin delete: %v", err)
	}

	// A DB failure after begin/pre-delete must Abort and leave the account
	// recoverable. A canceled context deterministically fails the transaction.
	failing := h.seedUser("retire-failure")
	failingToken := h.issueAdmin()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := svc.DeleteUserAsAdminBound(ctx, h.adminUser, failing.ID, failingToken, db.SessionHash(h.adminToken)); err == nil {
		t.Fatal("canceled delete unexpectedly succeeded")
	}
	if current, err := h.store.GetUserByID(failing.ID); err != nil || current == nil {
		t.Fatalf("failed delete was not recoverable: current=%+v err=%v", current, err)
	}

	mu.Lock()
	joined := strings.Join(events, ",")
	mu.Unlock()
	for _, want := range []string{
		"begin:" + strconv.FormatInt(self.ID, 10) + ",delete-begin:" + strconv.FormatInt(self.ID, 10) + ",predelete:" + strconv.FormatInt(self.ID, 10) + ",delete-commit:" + strconv.FormatInt(self.ID, 10) + ",commit:" + strconv.FormatInt(self.ID, 10),
		"begin:" + strconv.FormatInt(adminTarget.ID, 10) + ",delete-begin:" + strconv.FormatInt(adminTarget.ID, 10) + ",predelete:" + strconv.FormatInt(adminTarget.ID, 10) + ",delete-commit:" + strconv.FormatInt(adminTarget.ID, 10) + ",commit:" + strconv.FormatInt(adminTarget.ID, 10),
		"begin:" + strconv.FormatInt(failing.ID, 10) + ",delete-begin:" + strconv.FormatInt(failing.ID, 10) + ",predelete:" + strconv.FormatInt(failing.ID, 10) + ",delete-abort:" + strconv.FormatInt(failing.ID, 10) + ",abort:" + strconv.FormatInt(failing.ID, 10),
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("events=%s missing %s", joined, want)
		}
	}

	for _, tc := range []struct {
		name  string
		begin func(int64) (func() bool, func() bool, error)
	}{
		{"begin error", func(int64) (func() bool, func() bool, error) { return nil, nil, errors.New("blocked") }},
		{"malformed", func(int64) (func() bool, func() bool, error) { return nil, nil, nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			target := h.seedUser("retire-invalid-" + tc.name)
			invalidSvc, err := NewService(Config{
				Store: h.store, Elevation: h.elevation, AdminVerifier: h.adminAuth,
				BeginUserRetirement: tc.begin,
			})
			if err != nil {
				t.Fatal(err)
			}
			defer invalidSvc.Close()
			token := h.issueAdmin()
			if err := invalidSvc.DeleteUserAsAdminBound(context.Background(), h.adminUser, target.ID, token, db.SessionHash(h.adminToken)); err == nil {
				t.Fatal("invalid retirement dependency accepted")
			}
			if current, err := h.store.GetUserByID(target.ID); err != nil || current == nil {
				t.Fatalf("invalid dependency mutated DB current=%+v err=%v", current, err)
			}
		})
	}

	for _, tc := range []struct {
		name  string
		begin func(int64) (func() bool, func() bool, error)
	}{
		{"delete begin error", func(int64) (func() bool, func() bool, error) {
			return nil, nil, errors.New("delete state unavailable")
		}},
		{"delete malformed", func(int64) (func() bool, func() bool, error) { return nil, nil, nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			target := h.seedUser("retire-invalid-" + tc.name)
			invalidSvc, err := NewService(Config{
				Store: h.store, Elevation: h.elevation, AdminVerifier: h.adminAuth,
				BeginUserDeletion: tc.begin,
			})
			if err != nil {
				t.Fatal(err)
			}
			defer invalidSvc.Close()
			token := h.issueAdmin()
			if err := invalidSvc.DeleteUserAsAdminBound(context.Background(), h.adminUser, target.ID, token, db.SessionHash(h.adminToken)); err == nil {
				t.Fatal("invalid deletion dependency accepted")
			}
			if current, err := h.store.GetUserByID(target.ID); err != nil || current == nil {
				t.Fatalf("invalid deletion dependency mutated DB current=%+v err=%v", current, err)
			}
		})
	}
}

func TestSelfServiceDeleteSuccess(t *testing.T) {
	h := newHarness(t)
	user := h.seedUser("self-1")
	// Seed linked data to prove cascade through the handler path.
	if _, err := h.store.RegenerateCallerKey(user.ID); err != nil {
		t.Fatalf("regen key: %v", err)
	}
	token := h.issueUser(user.ID)
	req := stationReq(http.MethodPost, "https://example.com/api/account/delete", host.StationUser,
		`{"confirm":"DELETE"}`)
	req.AddCookie(h.userSessionCookie(user.ID))
	req.Header.Set(elevatedTokenHeader, token)
	rec := httptest.NewRecorder()
	h.userDeleteHandler().ServeHTTP(rec, req)
	wantCode(t, rec, http.StatusNoContent)
	wantNoStore(t, rec)
	// User gone.
	if got, err := h.store.GetUserByID(user.ID); err != nil || got != nil {
		t.Fatalf("user still present: %v %v", got, err)
	}
	// Session cookie cleared.
	cleared := false
	for _, c := range rec.Result().Cookies() {
		if c.Name == auth.UserSessionCookieName && c.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Fatalf("user session cookie not cleared")
	}
}

func TestSelfServiceDeleteRejectsMissingElevatedToken(t *testing.T) {
	h := newHarness(t)
	user := h.seedUser("self-2")
	req := stationReq(http.MethodPost, "https://example.com/api/account/delete", host.StationUser,
		`{"confirm":"DELETE"}`)
	req.AddCookie(h.userSessionCookie(user.ID))
	rec := httptest.NewRecorder()
	h.userDeleteHandler().ServeHTTP(rec, req)
	wantCode(t, rec, http.StatusForbidden)
	if !strings.Contains(rec.Body.String(), `"code":"elevated_required"`) {
		t.Fatalf("body=%s", rec.Body.String())
	}
	// User still present (no state change).
	if got, _ := h.store.GetUserByID(user.ID); got == nil {
		t.Fatalf("user vanished without elevation")
	}
}

func TestSelfServiceDeleteRejectsWrongConfirm(t *testing.T) {
	h := newHarness(t)
	user := h.seedUser("self-3")
	token := h.issueUser(user.ID)
	req := stationReq(http.MethodPost, "https://example.com/api/account/delete", host.StationUser,
		`{"confirm":"no"}`)
	req.AddCookie(h.userSessionCookie(user.ID))
	req.Header.Set(elevatedTokenHeader, token)
	rec := httptest.NewRecorder()
	h.userDeleteHandler().ServeHTTP(rec, req)
	wantCode(t, rec, http.StatusBadRequest)
	// The token must NOT be burned by a wrong confirm (confirm is checked first).
	if err := h.elevation.ConsumeBound(token, user.ID, elevation.KindUser, db.SessionHash(h.userSessionCookie(user.ID).Value)); err != nil {
		t.Fatalf("token burned by wrong confirm: %v", err)
	}
}

func TestSelfServiceDeleteRejectsExpiredToken(t *testing.T) {
	h := newHarness(t)
	user := h.seedUser("self-4")
	token := h.issueUser(user.ID)
	if err := h.elevation.SetClock(func() time.Time { return time.Now().Add(time.Hour) }); err != nil {
		t.Fatalf("SetClock: %v", err)
	}
	req := stationReq(http.MethodPost, "https://example.com/api/account/delete", host.StationUser,
		`{"confirm":"DELETE"}`)
	req.AddCookie(h.userSessionCookie(user.ID))
	req.Header.Set(elevatedTokenHeader, token)
	rec := httptest.NewRecorder()
	h.userDeleteHandler().ServeHTTP(rec, req)
	wantCode(t, rec, http.StatusForbidden)
	if got, _ := h.store.GetUserByID(user.ID); got == nil {
		t.Fatalf("user vanished with expired token")
	}
}

func TestSelfServiceDeleteRejectsKindMismatch(t *testing.T) {
	h := newHarness(t)
	user := h.seedUser("self-5")
	// An admin-kind token must not authorize a user delete.
	token := h.issueUserKind(user.ID, elevation.KindAdmin)
	req := stationReq(http.MethodPost, "https://example.com/api/account/delete", host.StationUser,
		`{"confirm":"DELETE"}`)
	req.AddCookie(h.userSessionCookie(user.ID))
	req.Header.Set(elevatedTokenHeader, token)
	rec := httptest.NewRecorder()
	h.userDeleteHandler().ServeHTTP(rec, req)
	wantCode(t, rec, http.StatusForbidden)
	if got, _ := h.store.GetUserByID(user.ID); got == nil {
		t.Fatalf("user vanished via admin-kind token")
	}
}

func TestSelfServiceDeleteReplayForbidden(t *testing.T) {
	h := newHarness(t)
	user := h.seedUser("self-6")
	token := h.issueUser(user.ID)
	// First delete consumes the token and removes the account.
	req1 := stationReq(http.MethodPost, "https://example.com/api/account/delete", host.StationUser,
		`{"confirm":"DELETE"}`)
	req1.AddCookie(h.userSessionCookie(user.ID))
	req1.Header.Set(elevatedTokenHeader, token)
	h.userDeleteHandler().ServeHTTP(httptest.NewRecorder(), req1)
	// A second session for the same (now deleted) user cannot exist; emulate a
	// replay by issuing a fresh session for a different user and reusing the
	// token: it must be refused because the token was already consumed.
	other := h.seedUser("self-6b")
	req2 := stationReq(http.MethodPost, "https://example.com/api/account/delete", host.StationUser,
		`{"confirm":"DELETE"}`)
	req2.AddCookie(h.userSessionCookie(other.ID))
	req2.Header.Set(elevatedTokenHeader, token)
	rec := httptest.NewRecorder()
	h.userDeleteHandler().ServeHTTP(rec, req2)
	wantCode(t, rec, http.StatusForbidden)
	if got, _ := h.store.GetUserByID(other.ID); got == nil {
		t.Fatalf("other user vanished via replayed token")
	}
}

func TestSelfServiceDeleteRequiresUserSession(t *testing.T) {
	h := newHarness(t)
	user := h.seedUser("self-7")
	token := h.issueUser(user.ID)
	// No session cookie.
	req := stationReq(http.MethodPost, "https://example.com/api/account/delete", host.StationUser,
		`{"confirm":"DELETE"}`)
	req.Header.Set(elevatedTokenHeader, token)
	rec := httptest.NewRecorder()
	h.userDeleteHandler().ServeHTTP(rec, req)
	wantCode(t, rec, http.StatusUnauthorized)
	// Admin session crossing into the user station is refused by the middleware.
	req2 := stationReq(http.MethodPost, "https://example.com/api/account/delete", host.StationUser,
		`{"confirm":"DELETE"}`)
	req2.AddCookie(&http.Cookie{Name: auth.AdminSessionCookieName, Value: h.adminToken})
	req2.Header.Set(elevatedTokenHeader, token)
	rec2 := httptest.NewRecorder()
	h.userDeleteHandler().ServeHTTP(rec2, req2)
	wantCode(t, rec2, http.StatusUnauthorized)
}

func TestAdminElevateIssuesUsableToken(t *testing.T) {
	h := newHarness(t)
	// Step 1: elevate with the correct password.
	req := stationReq(http.MethodPost, "https://admin.example.com/admin/api/auth/elevate", host.StationAdmin,
		`{"password":"correct-password"}`)
	req.AddCookie(&http.Cookie{Name: auth.AdminSessionCookieName, Value: h.adminToken})
	rec := httptest.NewRecorder()
	h.adminElevateHandler().ServeHTTP(rec, req)
	wantCode(t, rec, http.StatusOK)
	wantNoStore(t, rec)
	var resp struct {
		Token     string `json:"token"`
		ExpiresAt string `json:"expires_at"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || resp.Token == "" || resp.ExpiresAt == "" {
		t.Fatalf("elevate response=%s err=%v", rec.Body.String(), err)
	}
	// Step 2: the issued token authorizes an admin delete.
	target := h.seedUser("admin-target-1")
	delReq := stationReq(http.MethodDelete, "https://admin.example.com/admin/api/users/"+strconv.FormatInt(target.ID, 10), host.StationAdmin, "")
	delReq.AddCookie(&http.Cookie{Name: auth.AdminSessionCookieName, Value: h.adminToken})
	delReq.Header.Set(elevatedTokenHeader, resp.Token)
	delRec := httptest.NewRecorder()
	h.adminDeleteHandler().ServeHTTP(delRec, delReq)
	wantCode(t, delRec, http.StatusNoContent)
	if got, _ := h.store.GetUserByID(target.ID); got != nil {
		t.Fatalf("target still present after admin delete")
	}
}

func TestAdminElevateRejectsWrongPassword(t *testing.T) {
	h := newHarness(t)
	req := stationReq(http.MethodPost, "https://admin.example.com/admin/api/auth/elevate", host.StationAdmin,
		`{"password":"wrong"}`)
	req.AddCookie(&http.Cookie{Name: auth.AdminSessionCookieName, Value: h.adminToken})
	rec := httptest.NewRecorder()
	h.adminElevateHandler().ServeHTTP(rec, req)
	wantCode(t, rec, http.StatusForbidden)
}

func TestAdminDeleteProtectsAdminRow(t *testing.T) {
	h := newHarness(t)
	token := h.issueAdmin()
	req := stationReq(http.MethodDelete, "https://admin.example.com/admin/api/users/"+strconv.FormatInt(h.adminUser.ID, 10), host.StationAdmin, "")
	req.AddCookie(&http.Cookie{Name: auth.AdminSessionCookieName, Value: h.adminToken})
	req.Header.Set(elevatedTokenHeader, token)
	rec := httptest.NewRecorder()
	h.adminDeleteHandler().ServeHTTP(rec, req)
	wantCode(t, rec, http.StatusForbidden)
	if got, _ := h.store.GetUserByID(h.adminUser.ID); got == nil || !got.IsAdmin {
		t.Fatalf("admin row missing or demoted after protected delete: %v", got)
	}
}

func TestAdminDeleteNotFound(t *testing.T) {
	h := newHarness(t)
	token := h.issueAdmin()
	req := stationReq(http.MethodDelete, "https://admin.example.com/admin/api/users/999999", host.StationAdmin, "")
	req.AddCookie(&http.Cookie{Name: auth.AdminSessionCookieName, Value: h.adminToken})
	req.Header.Set(elevatedTokenHeader, token)
	rec := httptest.NewRecorder()
	h.adminDeleteHandler().ServeHTTP(rec, req)
	wantCode(t, rec, http.StatusNotFound)
}

func TestAdminDeleteRejectsMissingElevatedToken(t *testing.T) {
	h := newHarness(t)
	target := h.seedUser("admin-target-2")
	req := stationReq(http.MethodDelete, "https://admin.example.com/admin/api/users/"+strconv.FormatInt(target.ID, 10), host.StationAdmin, "")
	req.AddCookie(&http.Cookie{Name: auth.AdminSessionCookieName, Value: h.adminToken})
	rec := httptest.NewRecorder()
	h.adminDeleteHandler().ServeHTTP(rec, req)
	wantCode(t, rec, http.StatusForbidden)
	if got, _ := h.store.GetUserByID(target.ID); got == nil {
		t.Fatalf("target vanished without elevation")
	}
}

func TestAdminDeleteRequiresAdminSession(t *testing.T) {
	h := newHarness(t)
	target := h.seedUser("admin-target-3")
	token := h.issueAdmin()
	// User session crossing into the admin station is refused by the middleware.
	req := stationReq(http.MethodDelete, "https://admin.example.com/admin/api/users/"+strconv.FormatInt(target.ID, 10), host.StationAdmin, "")
	req.AddCookie(h.userSessionCookie(target.ID))
	req.Header.Set(elevatedTokenHeader, token)
	rec := httptest.NewRecorder()
	h.adminDeleteHandler().ServeHTTP(rec, req)
	wantCode(t, rec, http.StatusUnauthorized)
}

func TestAdminElevateRequiresAdminSession(t *testing.T) {
	h := newHarness(t)
	target := h.seedUser("admin-target-4")
	req := stationReq(http.MethodPost, "https://admin.example.com/admin/api/auth/elevate", host.StationAdmin,
		`{"password":"correct-password"}`)
	req.AddCookie(h.userSessionCookie(target.ID))
	rec := httptest.NewRecorder()
	h.adminElevateHandler().ServeHTTP(rec, req)
	wantCode(t, rec, http.StatusUnauthorized)
}

func TestMethodAndStationGuards(t *testing.T) {
	h := newHarness(t)
	user := h.seedUser("self-8")
	token := h.issueUser(user.ID)
	// Wrong method on user delete.
	req := stationReq(http.MethodGet, "https://example.com/api/account/delete", host.StationUser, "")
	req.AddCookie(h.userSessionCookie(user.ID))
	req.Header.Set(elevatedTokenHeader, token)
	rec := httptest.NewRecorder()
	h.userDeleteHandler().ServeHTTP(rec, req)
	wantCode(t, rec, http.StatusMethodNotAllowed)
	// User-station delete handler refuses admin station.
	req2 := stationReq(http.MethodPost, "https://admin.example.com/api/account/delete", host.StationAdmin,
		`{"confirm":"DELETE"}`)
	req2.AddCookie(&http.Cookie{Name: auth.AdminSessionCookieName, Value: h.adminToken})
	req2.Header.Set(elevatedTokenHeader, token)
	rec2 := httptest.NewRecorder()
	h.userDeleteHandler().ServeHTTP(rec2, req2)
	wantCode(t, rec2, http.StatusForbidden)
}

func TestUnknownFieldsAndTrailingTokensRejected(t *testing.T) {
	h := newHarness(t)
	user := h.seedUser("self-9")
	token := h.issueUser(user.ID)
	// Unknown field.
	req := stationReq(http.MethodPost, "https://example.com/api/account/delete", host.StationUser,
		`{"confirm":"DELETE","extra":1}`)
	req.AddCookie(h.userSessionCookie(user.ID))
	req.Header.Set(elevatedTokenHeader, token)
	rec := httptest.NewRecorder()
	h.userDeleteHandler().ServeHTTP(rec, req)
	wantCode(t, rec, http.StatusBadRequest)
	// Trailing token.
	req2 := stationReq(http.MethodPost, "https://example.com/api/account/delete", host.StationUser,
		`{"confirm":"DELETE"}{"more":1}`)
	req2.AddCookie(h.userSessionCookie(user.ID))
	req2.Header.Set(elevatedTokenHeader, token)
	rec2 := httptest.NewRecorder()
	h.userDeleteHandler().ServeHTTP(rec2, req2)
	wantCode(t, rec2, http.StatusBadRequest)
}

// TestConcurrentDeleteAtMostOneWinner fires many concurrent self-delete
// requests with a single shared elevation token and a single shared session.
// Exactly one must win (204): the elevation consume is single-use. Losers see
// either 403 (token replay, they reached the consume before the winner's delete
// committed) or 401 (the winner's delete cascaded the session away before they
// authenticated). No other status code may appear; the user is gone at the end.
func TestConcurrentDeleteAtMostOneWinner(t *testing.T) {
	h := newHarness(t)
	user := h.seedUser("self-race")
	token := h.issueUser(user.ID)
	cookie := h.userSessionCookie(user.ID)
	handler := h.userDeleteHandler()

	const racers = 64
	var wins atomic.Int64
	var forbidden atomic.Int64
	var unauthorized atomic.Int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := stationReq(http.MethodPost, "https://example.com/api/account/delete", host.StationUser,
				`{"confirm":"DELETE"}`)
			req.AddCookie(cookie)
			req.Header.Set(elevatedTokenHeader, token)
			rec := httptest.NewRecorder()
			<-start
			handler.ServeHTTP(rec, req)
			switch rec.Code {
			case http.StatusNoContent:
				wins.Add(1)
			case http.StatusForbidden:
				forbidden.Add(1)
			case http.StatusUnauthorized:
				unauthorized.Add(1)
			default:
				h.t.Errorf("unexpected status=%d body=%s", rec.Code, rec.Body.String())
			}
		}()
	}
	close(start)
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("wins=%d, want exactly 1 (single-use violated)", wins.Load())
	}
	if got := forbidden.Load() + unauthorized.Load(); got != racers-1 {
		t.Fatalf("losers=%d (forbidden=%d unauthorized=%d), want %d", got, forbidden.Load(), unauthorized.Load(), racers-1)
	}
	if got, _ := h.store.GetUserByID(user.ID); got != nil {
		t.Fatalf("user still present after race")
	}
}
