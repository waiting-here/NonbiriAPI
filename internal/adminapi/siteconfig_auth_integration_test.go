package adminapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/auth"
	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbtest"
	"github.com/waiting-here/NonbiriAPI/internal/host"
	"github.com/waiting-here/NonbiriAPI/internal/httperr"
	"github.com/waiting-here/NonbiriAPI/internal/maintenance"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

type siteConfigAuthProvider struct{}

func (siteConfigAuthProvider) AuthorizationURL(context.Context, auth.DiscordAuthorizeRequest) (string, error) {
	return "https://identity.example/authorize", nil
}

func (siteConfigAuthProvider) Exchange(context.Context, string, string) (auth.DiscordLogin, error) {
	return auth.DiscordLogin{}, auth.ErrProviderUnauthorized
}

type siteConfigEnabledMaintenanceGate struct{}

func (siteConfigEnabledMaintenanceGate) State() (maintenance.State, bool) {
	return maintenance.State{Enabled: true, Revision: 7, ChangedAt: siteConfigTestNow}, true
}

type siteConfigAuthFinalAuthorizer struct {
	authorizer *authz.Authorizer
	mu         sync.Mutex
	demote     bool
}

func (authorizer *siteConfigAuthFinalAuthorizer) setDemote(value bool) {
	authorizer.mu.Lock()
	authorizer.demote = value
	authorizer.mu.Unlock()
}

func (authorizer *siteConfigAuthFinalAuthorizer) AuthorizeAdmin(ctx context.Context, tx *sql.Tx, adminID int64) error {
	actor, ok := auth.ActorFromContext(ctx)
	if !ok || actor.Kind != authz.ActorAdminSession || actor.UserID != adminID {
		return authz.ErrUnauthorized
	}
	authorizer.mu.Lock()
	demote := authorizer.demote
	authorizer.mu.Unlock()
	if demote {
		// The schema deliberately makes the singleton administrator bit
		// immutable. This injected live-role decision exercises the repository's
		// final-transaction denial seam without weakening that invariant.
		return authz.ErrForbidden
	}
	_, err := authorizer.authorizer.Authorize(ctx, tx, actor, authz.Requirement{Role: authz.RoleAdministrator})
	return err
}

type siteConfigAuthRegistrar struct {
	runtime *auth.Runtime
}

func (registrar siteConfigAuthRegistrar) RegisterAdminRoute(method, pattern string, handler SiteConfigAuthorizedAdminHandler) error {
	if registrar.runtime == nil || handler == nil {
		return errors.New("invalid auth registration")
	}
	adapter := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		actor, ok := auth.ActorFromContext(request.Context())
		if !ok || actor.Kind != authz.ActorAdminSession || actor.UserID <= 0 {
			writeSiteConfigError(writer, ErrSiteConfigUnauthorized)
			return
		}
		handler(writer, request, SiteConfigAdminPrincipal{UserID: actor.UserID})
	})
	return registrar.runtime.RegisterAdminRoute(method, pattern, adapter)
}

type siteConfigAuthFixture struct {
	runtime         *auth.Runtime
	handler         http.Handler
	store           *db.Store
	finalAuthorizer *siteConfigAuthFinalAuthorizer
}

func newSiteConfigAuthFixture(t *testing.T) *siteConfigAuthFixture {
	t.Helper()
	masterKey := bytes.Repeat([]byte{0x73}, secret.MasterKeyBytes)
	vault, err := secret.New(masterKey)
	clear(masterKey)
	if err != nil {
		t.Fatalf("create auth integration vault: %v", err)
	}
	path := filepath.Join(t.TempDir(), "site-config-auth.db")
	dbtest.EnsureOwnerOnlyParent(t, path)
	store, err := db.Open(path, vault)
	if err != nil {
		_ = vault.Close()
		t.Fatalf("open auth integration store: %v", err)
	}
	clock := func() time.Time { return time.Unix(siteConfigTestNow, 0) }
	final := &siteConfigAuthFinalAuthorizer{authorizer: authz.New(authz.Options{Now: clock})}
	repository := newSiteConfigTestRepository(t, store, final)
	siteRuntime, err := NewSiteConfigRuntime(repository)
	if err != nil {
		_ = store.Close()
		_ = vault.Close()
		t.Fatalf("new site configuration runtime: %v", err)
	}
	authRuntime, err := auth.NewRuntime(auth.RuntimeConfig{
		Store: store, Provider: siteConfigAuthProvider{}, DiscordClientID: "client", UserSiteBaseURL: "https://user.example",
		AdminUsername: "operator", AdminPassword: "correct horse battery staple", CredentialKeyDeriver: vault,
		Authorizer: final.authorizer, Maintenance: siteConfigEnabledMaintenanceGate{}, Now: clock,
	})
	if err != nil {
		_ = store.Close()
		_ = vault.Close()
		t.Fatalf("new auth integration runtime: %v", err)
	}
	if err := RegisterSiteConfigRoutes(siteConfigAuthRegistrar{runtime: authRuntime}, siteRuntime); err != nil {
		_ = authRuntime.Close()
		_ = store.Close()
		_ = vault.Close()
		t.Fatalf("register site configuration with auth runtime: %v", err)
	}
	fixture := &siteConfigAuthFixture{runtime: authRuntime, handler: authRuntime.AdminHandler(), store: store, finalAuthorizer: final}
	t.Cleanup(func() {
		_ = authRuntime.Close()
		_ = store.Close()
		_ = vault.Close()
	})
	return fixture
}

func siteConfigAuthRequest(t *testing.T, handler http.Handler, station host.Station, method, path string, body []byte, cookies []*http.Cookie, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var request *http.Request
	if body == nil {
		request = httptest.NewRequest(method, "https://admin.example"+path, nil)
	} else {
		request = httptest.NewRequest(method, "https://admin.example"+path, bytes.NewReader(body))
	}
	request.RemoteAddr = "192.0.2.30:4321"
	request = request.WithContext(host.WithStation(request.Context(), station))
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func siteConfigAdminCookie(t *testing.T, recorder *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, cookie := range recorder.Result().Cookies() {
		if cookie.Name == auth.AdminSessionCookieName {
			return cookie
		}
	}
	t.Fatalf("admin login response lacks session cookie: %s", recorder.Body.String())
	return nil
}

func siteConfigWrongRoleCookie(t *testing.T, store *db.Store) *http.Cookie {
	t.Helper()
	tx, err := store.DB().Begin()
	if err != nil {
		t.Fatalf("begin wrong-role fixture: %v", err)
	}
	defer tx.Rollback()
	zero := db.EncodeU128(db.U128{})
	revision := db.U128{}
	revision[15] = 1
	result, err := tx.Exec(`INSERT INTO users(
		discord_id,username,avatar,guild_nick,guild_avatar_url,is_admin,is_banned,banned_reason,banned_until,auto_banned,
		charity_suspended_until,endpoint_limit,rpm_limit,concurrency_limit,game_profile_public,donation_credit_mag,level,auto_level,
		total_requests,total_uncached_input_tokens,total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,
		total_unknown_usage_requests,revision,lang,created_at,updated_at
	) VALUES(?,?,?,?,?,0,0,'',NULL,0,NULL,NULL,NULL,NULL,0,?,NULL,1,?,?,?,?,?,?,?,'',?,?)`,
		"discord-wrong-role", "ordinary-user", "", "", "", zero, zero, zero, zero, zero, zero, zero,
		db.EncodeU128(revision), siteConfigTestNow, siteConfigTestNow)
	if err != nil {
		t.Fatalf("insert wrong-role user: %v", err)
	}
	userID, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("read wrong-role user id: %v", err)
	}
	raw := strings.Repeat("u", 43)
	hash := sha256.Sum256([]byte(raw))
	if _, err := tx.Exec(`INSERT INTO sessions(
		token_hash,user_id,oauth_state,last_seen_at,expires_at,absolute_expires_at,created_at,cred_gen
	) VALUES(?,?,'',?,?,?,?,?)`, hex.EncodeToString(hash[:]), userID, siteConfigTestNow,
		siteConfigTestNow+86400, siteConfigTestNow+86400, siteConfigTestNow, "user-generation"); err != nil {
		t.Fatalf("insert wrong-role session: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit wrong-role fixture: %v", err)
	}
	return &http.Cookie{Name: auth.AdminSessionCookieName, Value: raw}
}

func TestSiteConfigRoutesUseHostPasswordSessionGenerationAndFinalTxAuth(t *testing.T) {
	fixture := newSiteConfigAuthFixture(t)

	anonymous := siteConfigAuthRequest(t, fixture.handler, host.StationAdmin, http.MethodGet, RouteAdminBootstrapConfig, nil, nil, nil)
	assertSiteConfigHTTPError(t, anonymous, http.StatusUnauthorized, httperr.CodeUnauthorized)

	wrongPassword := siteConfigAuthRequest(t, fixture.handler, host.StationAdmin, http.MethodPost, "/admin/api/login",
		[]byte(`{"username":"operator","password":"wrong password"}`), nil, map[string]string{"Content-Type": "application/json"})
	assertSiteConfigHTTPError(t, wrongPassword, http.StatusUnauthorized, httperr.CodeUnauthorized)

	login := siteConfigAuthRequest(t, fixture.handler, host.StationAdmin, http.MethodPost, "/admin/api/login",
		[]byte(`{"username":"operator","password":"correct horse battery staple"}`), nil, map[string]string{"Content-Type": "application/json"})
	if login.Code != http.StatusOK {
		t.Fatalf("legal admin login status=%d body=%s", login.Code, login.Body.String())
	}
	cookie := siteConfigAdminCookie(t, login)

	// The fixture's committed maintenance state is enabled. Administrator
	// bootstrap/configuration routes remain readable and authenticated.
	legal := siteConfigAuthRequest(t, fixture.handler, host.StationAdmin, http.MethodGet, RouteAdminBootstrapConfig, nil, []*http.Cookie{cookie}, nil)
	if legal.Code != http.StatusOK {
		t.Fatalf("maintenance-period admin bootstrap status=%d body=%s", legal.Code, legal.Body.String())
	}
	wrongHost := siteConfigAuthRequest(t, fixture.handler, host.StationUser, http.MethodGet, RouteAdminBootstrapConfig, nil, []*http.Cookie{cookie}, nil)
	assertSiteConfigHTTPError(t, wrongHost, http.StatusForbidden, httperr.CodeForbidden)

	var adminID int64
	if err := fixture.store.DB().QueryRow(`SELECT id FROM users WHERE is_admin=1`).Scan(&adminID); err != nil {
		t.Fatalf("read administrator identity: %v", err)
	}
	wrongRoleCookie := siteConfigWrongRoleCookie(t, fixture.store)
	wrongRole := siteConfigAuthRequest(t, fixture.handler, host.StationAdmin, http.MethodGet, RouteAdminBootstrapConfig, nil, []*http.Cookie{wrongRoleCookie}, nil)
	assertSiteConfigHTTPError(t, wrongRole, http.StatusForbidden, httperr.CodeForbidden)

	initialRevision := siteConfigRevision(t, fixture.store)
	initialRecords := siteConfigIdempotencyCount(t, fixture.store)
	fixture.finalAuthorizer.setDemote(true)
	finalDemotion := siteConfigAuthRequest(t, fixture.handler, host.StationAdmin, http.MethodPatch, "/admin/api/site-config/site_name",
		[]byte(`{"value":"must roll back"}`), []*http.Cookie{cookie}, map[string]string{"Idempotency-Key": "site-config-final-demotion-01"})
	fixture.finalAuthorizer.setDemote(false)
	assertSiteConfigHTTPError(t, finalDemotion, http.StatusForbidden, httperr.CodeForbidden)
	var isAdmin int
	if err := fixture.store.DB().QueryRow(`SELECT is_admin FROM users WHERE id=?`, adminID).Scan(&isAdmin); err != nil {
		t.Fatalf("read role after final-transaction denial: %v", err)
	}
	if isAdmin != 1 || siteConfigRevision(t, fixture.store) != initialRevision || siteConfigIdempotencyCount(t, fixture.store) != initialRecords {
		t.Fatal("final-transaction demotion denial did not roll back every write")
	}

	if _, err := fixture.store.DB().Exec(`UPDATE sessions SET cred_gen='stale-generation' WHERE user_id=?`, adminID); err != nil {
		t.Fatalf("stale administrator credential generation: %v", err)
	}
	stale := siteConfigAuthRequest(t, fixture.handler, host.StationAdmin, http.MethodGet, RouteAdminBootstrapConfig, nil, []*http.Cookie{cookie}, nil)
	assertSiteConfigHTTPError(t, stale, http.StatusUnauthorized, httperr.CodeUnauthorized)
}
