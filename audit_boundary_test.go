package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/waiting-here/NonbiriAPI/internal/activities"
	"github.com/waiting-here/NonbiriAPI/internal/auth"
	"github.com/waiting-here/NonbiriAPI/internal/authz"
	"github.com/waiting-here/NonbiriAPI/internal/config"
	"github.com/waiting-here/NonbiriAPI/internal/db"
	"github.com/waiting-here/NonbiriAPI/internal/dbtest"
	"github.com/waiting-here/NonbiriAPI/internal/secret"
)

const (
	auditUserHost  = "127.0.0.1"
	auditAdminHost = "127.0.0.2"
)

func auditConfig() *config.Config {
	return &config.Config{
		ListenAddr:          "127.0.0.1:18999",
		UserHost:            auditUserHost,
		AdminHost:           auditAdminHost,
		SiteBaseURL:         "https://" + auditUserHost,
		AdminUsername:       "operator",
		AdminPassword:       "correct horse battery staple",
		DiscordClientID:     "test-client",
		DiscordClientSecret: "test-secret",
		TrustedProxyCIDRs:   []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")},
	}
}

func testApplicationRequest(t *testing.T, handler http.Handler, method, hostName, path, body string, cookies []*http.Cookie, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var request *http.Request
	if body == "" {
		request = httptest.NewRequest(method, "http://wire.invalid"+path, nil)
	} else {
		request = httptest.NewRequest(method, "http://wire.invalid"+path, strings.NewReader(body))
	}
	request.Host = hostName
	request.RemoteAddr = "198.51.100.20:4242"
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func responseCookieNamed(t *testing.T, response *httptest.ResponseRecorder, name string) *http.Cookie {
	t.Helper()
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == name && cookie.Value != "" {
			return cookie
		}
	}
	t.Fatalf("response did not set %s: status=%d body=%s", name, response.Code, response.Body.String())
	return nil
}

func assertFreshSafeApplication(t *testing.T, app *application) {
	t.Helper()
	for _, tc := range []struct {
		name, host, path string
		want             int
	}{
		{name: "user health", host: auditUserHost, path: "/healthz", want: http.StatusOK},
		{name: "admin health", host: auditAdminHost, path: "/healthz", want: http.StatusOK},
		{name: "public bootstrap", host: auditUserHost, path: "/api/config", want: http.StatusOK},
		{name: "public bootstrap rejects query", host: auditUserHost, path: "/api/config?unexpected=1", want: http.StatusBadRequest},
		{name: "endpoint API mounted behind maintenance", host: auditUserHost, path: "/api/endpoints", want: http.StatusServiceUnavailable},
		{name: "donation API mounted behind maintenance", host: auditUserHost, path: "/api/donations", want: http.StatusServiceUnavailable},
		{name: "charity capability mounted behind maintenance", host: auditUserHost, path: "/api/charity/models", want: http.StatusServiceUnavailable},
		{name: "activities API mounted behind maintenance", host: auditUserHost, path: "/api/activities", want: http.StatusServiceUnavailable},
		{name: "announcements API mounted behind maintenance", host: auditUserHost, path: "/api/announcements", want: http.StatusServiceUnavailable},
		{name: "issues API mounted behind maintenance", host: auditUserHost, path: "/api/issues?state=current", want: http.StatusServiceUnavailable},
		{name: "game API mounted behind maintenance", host: auditUserHost, path: "/api/games", want: http.StatusServiceUnavailable},
		{name: "Debug API mounted behind maintenance", host: auditUserHost, path: "/api/debug/session", want: http.StatusServiceUnavailable},
		{name: "log API mounted behind maintenance", host: auditUserHost, path: "/api/logs", want: http.StatusServiceUnavailable},
		{name: "steward maintenance mounted behind maintenance", host: auditUserHost, path: "/api/steward/maintenance", want: http.StatusServiceUnavailable},
		{name: "account events require a session before continuation", host: auditUserHost, path: "/api/events", want: http.StatusUnauthorized},
		{name: "export rejects wrong method", host: auditUserHost, path: "/api/account/export", want: http.StatusMethodNotAllowed},
		{name: "caller models mounted behind maintenance", host: auditUserHost, path: "/v1/models", want: http.StatusServiceUnavailable},
		{name: "caller chat mounted behind maintenance", host: auditUserHost, path: "/v1/chat/completions", want: http.StatusServiceUnavailable},
		{name: "admin bootstrap requires session", host: auditAdminHost, path: "/admin/api/config", want: http.StatusUnauthorized},
		{name: "admin catalog requires session", host: auditAdminHost, path: "/admin/api/site-config", want: http.StatusUnauthorized},
		{name: "admin users require session", host: auditAdminHost, path: "/admin/api/users", want: http.StatusUnauthorized},
		{name: "admin alerts require session", host: auditAdminHost, path: "/admin/api/alerts", want: http.StatusUnauthorized},
		{name: "admin donation requires session", host: auditAdminHost, path: "/admin/api/donations", want: http.StatusUnauthorized},
		{name: "admin charity models require session", host: auditAdminHost, path: "/admin/api/charity-models", want: http.StatusUnauthorized},
		{name: "admin activities require session", host: auditAdminHost, path: "/admin/api/activities/thursday", want: http.StatusUnauthorized},
		{name: "admin announcements require session", host: auditAdminHost, path: "/admin/api/announcements", want: http.StatusUnauthorized},
		{name: "admin reports require session", host: auditAdminHost, path: "/admin/api/reports/badge", want: http.StatusUnauthorized},
		{name: "admin logs require session", host: auditAdminHost, path: "/admin/api/logs", want: http.StatusUnauthorized},
		{name: "admin maintenance requires session", host: auditAdminHost, path: "/admin/api/maintenance", want: http.StatusUnauthorized},
		{name: "admin games require session", host: auditAdminHost, path: "/admin/api/games/config", want: http.StatusUnauthorized},
		{name: "admin cannot reach user bootstrap", host: auditAdminHost, path: "/api/config", want: http.StatusNotFound},
		{name: "user cannot reach admin API", host: auditUserHost, path: "/admin/api/config", want: http.StatusNotFound},
		{name: "unknown host rejected", host: "198.51.100.10", path: "/", want: http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := testHTTPResponse(t, app.handler, http.MethodGet, tc.host, tc.path)
			if rec.Code != tc.want {
				t.Fatalf("status=%d want=%d body=%s", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
	publicReport := testApplicationRequest(t, app.handler, http.MethodPost, auditUserHost, "/api/reports/credential-theft", `{}`, nil, map[string]string{
		"Content-Type": "application/json",
		"Origin":       "http://" + auditUserHost,
	})
	if publicReport.Code != http.StatusServiceUnavailable {
		t.Fatalf("public report during maintenance status=%d body=%s", publicReport.Code, publicReport.Body.String())
	}

	for _, tc := range []struct {
		host, path string
	}{
		{host: auditUserHost, path: "/"},
		{host: auditAdminHost, path: "/settings/profile"},
	} {
		rec := testHTTPResponse(t, app.handler, http.MethodGet, tc.host, tc.path)
		if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/html") {
			t.Fatalf("SPA %s%s status=%d content-type=%q", tc.host, tc.path, rec.Code, rec.Header().Get("Content-Type"))
		}
		if rec.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("SPA cache-control=%q", rec.Header().Get("Cache-Control"))
		}
	}

	rec := testHTTPResponse(t, app.handler, http.MethodGet, auditUserHost, "/api/config")
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("public config body=%s err=%v", rec.Body.String(), err)
	}
	gotKeys := make([]string, 0, len(body))
	for key := range body {
		gotKeys = append(gotKeys, key)
	}
	sort.Strings(gotKeys)
	wantKeys := []string{
		"announcement_epoch",
		"legal_authoritative_locale",
		"legal_privacy_override_en",
		"legal_privacy_override_zh",
		"legal_terms_override_en",
		"legal_terms_override_zh",
		"maintenance_mode",
		"registration_open",
		"site_logo_url",
		"site_name",
	}
	if strings.Join(gotKeys, ",") != strings.Join(wantKeys, ",") {
		t.Fatalf("public config keys=%v want=%v body=%s", gotKeys, wantKeys, rec.Body.String())
	}
	if _, ok := body["default_locale"]; ok {
		t.Fatal("public config exposed deleted default_locale")
	}
	epoch, ok := body["announcement_epoch"].(string)
	if !ok || !strings.HasPrefix(epoch, "b1e_") || len(epoch) != 26 {
		t.Fatalf("announcement_epoch=%v", body["announcement_epoch"])
	}
	if _, ok := body["maintenance_mode"].(bool); !ok {
		t.Fatalf("maintenance_mode=%T", body["maintenance_mode"])
	}
	if _, ok := body["registration_open"].(bool); !ok {
		t.Fatalf("registration_open=%T", body["registration_open"])
	}

	bodyRequest := httptest.NewRequest(http.MethodGet, "http://wire.invalid/api/config", strings.NewReader("{}"))
	bodyRequest.Host = auditUserHost
	bodyRequest.RemoteAddr = "198.51.100.20:4242"
	bodyResponse := httptest.NewRecorder()
	app.handler.ServeHTTP(bodyResponse, bodyRequest)
	if bodyResponse.Code != http.StatusBadRequest {
		t.Fatalf("public config body status=%d want=%d body=%s", bodyResponse.Code, http.StatusBadRequest, bodyResponse.Body.String())
	}
}

func TestGenerationTwoFreshAndCurrentApplicationBoot(t *testing.T) {
	key := bytes.Repeat([]byte{0x5a}, secret.MasterKeyBytes)
	vault, err := secret.New(key)
	clear(key)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = vault.Close() }()

	dbPath := filepath.Join(t.TempDir(), "generation-two.db")
	dbtest.EnsureOwnerOnlyParent(t, dbPath)
	for pass := 0; pass < 2; pass++ {
		store, err := db.Open(dbPath, vault)
		if err != nil {
			t.Fatalf("pass %d db.Open: %v", pass, err)
		}
		app, err := buildApplication(auditConfig(), store, vault)
		if err != nil {
			_ = store.Close()
			t.Fatalf("pass %d buildApplication: %v", pass, err)
		}
		assertFreshSafeApplication(t, app)

		closed := make(chan error, 1)
		go func() { closed <- app.Close() }()
		select {
		case closeErr := <-closed:
			if closeErr != nil {
				t.Fatalf("pass %d app.Close: %v", pass, closeErr)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("pass %d app.Close blocked while stopping the idle discovery worker", pass)
		}
		if err := store.Close(); err != nil {
			t.Fatalf("pass %d store.Close: %v", pass, err)
		}
	}
}

func TestGenerationTwoRootAuthenticationAndMaintenanceWiring(t *testing.T) {
	key := bytes.Repeat([]byte{0x69}, secret.MasterKeyBytes)
	vault, err := secret.New(key)
	clear(key)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = vault.Close() }()

	dbPath := filepath.Join(t.TempDir(), "root-auth.db")
	dbtest.EnsureOwnerOnlyParent(t, dbPath)
	store, err := db.Open(dbPath, vault)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	app, err := buildApplication(auditConfig(), store, vault)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = app.Close() }()

	if app.authRuntime == nil || app.bridge == nil || app.claims == nil || app.resourceRepo == nil ||
		app.discoveryWorker == nil || app.donations == nil || app.charity == nil || app.charityRouting == nil ||
		app.announcements == nil || app.issues == nil || app.reports == nil || app.activities == nil ||
		app.activityRepo == nil || app.activityEvents == nil || app.adminConfig == nil || app.adminAlerts == nil ||
		app.adminUsers == nil || app.lifecycle == nil || app.lifecycleDone == nil ||
		app.debug == nil || app.logs == nil || app.accountEvents == nil || app.forward == nil || app.games == nil ||
		app.failures == nil || app.authorizer == nil ||
		app.elevation == nil || app.gate == nil || app.maintenance == nil || app.egress == nil {
		t.Fatal("root runtime omitted a required Generation 2 owner")
	}
	state, ready := app.gate.State()
	if !ready || !state.Enabled || app.registry == nil || !app.registry.Frozen() {
		t.Fatalf("maintenance startup state=%+v ready=%v registry=%v", state, ready, app.registry != nil && app.registry.Frozen())
	}
	maintenanceTx, err := store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if gateErr := (activityMutationGate{}).AuthorizeUserActivity(context.Background(), maintenanceTx, 1); !errors.Is(gateErr, activities.ErrMaintenance) {
		_ = maintenanceTx.Rollback()
		t.Fatalf("activity final-transaction maintenance gate err=%v", gateErr)
	}
	_ = maintenanceTx.Rollback()

	userSession := testApplicationRequest(t, app.handler, http.MethodGet, auditUserHost, "/api/session", "", nil, nil)
	if userSession.Code != http.StatusServiceUnavailable || !strings.Contains(userSession.Body.String(), `"code":"maintenance"`) {
		t.Fatalf("user session during maintenance status=%d body=%s", userSession.Code, userSession.Body.String())
	}
	oauthStart := testApplicationRequest(t, app.handler, http.MethodGet, auditUserHost, "/api/auth/discord/start", "", nil, nil)
	if oauthStart.Code != http.StatusServiceUnavailable || !strings.Contains(oauthStart.Body.String(), `"code":"maintenance"`) {
		t.Fatalf("OAuth start during maintenance status=%d body=%s", oauthStart.Code, oauthStart.Body.String())
	}
	export := testApplicationRequest(t, app.handler, http.MethodPost, auditUserHost, "/api/account/export", "", nil, nil)
	if export.Code != http.StatusServiceUnavailable || !strings.Contains(export.Body.String(), `"code":"maintenance"`) {
		t.Fatalf("account export during maintenance status=%d body=%s", export.Code, export.Body.String())
	}
	legalHolds := testApplicationRequest(t, app.handler, http.MethodGet, auditAdminHost, "/admin/api/legal-holds", "", nil, nil)
	if legalHolds.Code != http.StatusUnauthorized {
		t.Fatalf("legal hold route without admin authentication status=%d body=%s", legalHolds.Code, legalHolds.Body.String())
	}
	caller := testApplicationRequest(t, app.handler, http.MethodGet, auditUserHost, "/v1/models", "", nil, nil)
	if caller.Code != http.StatusServiceUnavailable || !strings.Contains(caller.Body.String(), `"code":"maintenance"`) {
		t.Fatalf("caller route during maintenance status=%d body=%s", caller.Code, caller.Body.String())
	}

	wrongLogin := testApplicationRequest(t, app.handler, http.MethodPost, auditAdminHost, "/admin/api/login",
		`{"username":"operator","password":"wrong password"}`, nil, map[string]string{"Content-Type": "application/json"})
	if wrongLogin.Code != http.StatusUnauthorized {
		t.Fatalf("wrong admin password status=%d body=%s", wrongLogin.Code, wrongLogin.Body.String())
	}
	login := testApplicationRequest(t, app.handler, http.MethodPost, auditAdminHost, "/admin/api/login",
		`{"username":"operator","password":"correct horse battery staple"}`, nil, map[string]string{"Content-Type": "application/json"})
	if login.Code != http.StatusOK {
		t.Fatalf("admin login during maintenance status=%d body=%s", login.Code, login.Body.String())
	}
	adminCookie := responseCookieNamed(t, login, auth.AdminSessionCookieName)
	adminSession := testApplicationRequest(t, app.handler, http.MethodGet, auditAdminHost, "/admin/api/session", "", []*http.Cookie{adminCookie}, nil)
	if adminSession.Code != http.StatusOK {
		t.Fatalf("admin session during maintenance status=%d body=%s", adminSession.Code, adminSession.Body.String())
	}
	adminThursday := testApplicationRequest(t, app.handler, http.MethodGet, auditAdminHost, "/admin/api/activities/thursday", "", []*http.Cookie{adminCookie}, nil)
	if adminThursday.Code != http.StatusOK || strings.TrimSpace(adminThursday.Body.String()) != `{"period":null}` {
		t.Fatalf("admin activities during maintenance status=%d body=%s", adminThursday.Code, adminThursday.Body.String())
	}
	adminMaintenance := testApplicationRequest(t, app.handler, http.MethodGet, auditAdminHost, "/admin/api/maintenance", "", []*http.Cookie{adminCookie}, nil)
	if adminMaintenance.Code != http.StatusOK || strings.TrimSpace(adminMaintenance.Body.String()) != `{"enabled":true,"revision":"1"}` {
		t.Fatalf("admin maintenance during maintenance status=%d body=%s", adminMaintenance.Code, adminMaintenance.Body.String())
	}
	for _, path := range []string{
		"/admin/api/config", "/admin/api/site-config", "/admin/api/site-config/catalog",
		"/admin/api/users", "/admin/api/usage?group_by=site", "/admin/api/activity",
		"/admin/api/overview/endpoints", "/admin/api/alerts",
	} {
		response := testApplicationRequest(t, app.handler, http.MethodGet, auditAdminHost, path, "", []*http.Cookie{adminCookie}, nil)
		if response.Code != http.StatusOK {
			t.Fatalf("administrator route during maintenance %s status=%d body=%s", path, response.Code, response.Body.String())
		}
	}

	elevated := testApplicationRequest(t, app.handler, http.MethodPost, auditAdminHost, "/admin/api/auth/elevate",
		`{"password":"correct horse battery staple"}`, []*http.Cookie{adminCookie}, map[string]string{
			"Content-Type": "application/json",
			"Origin":       "http://" + auditAdminHost,
		})
	if elevated.Code != http.StatusOK {
		t.Fatalf("admin elevation during maintenance status=%d body=%s", elevated.Code, elevated.Body.String())
	}
	var elevationResponse auth.ElevationResponse
	if err := json.Unmarshal(elevated.Body.Bytes(), &elevationResponse); err != nil || elevationResponse.Token == "" {
		t.Fatalf("admin elevation body=%s err=%v", elevated.Body.String(), err)
	}

	var adminUserID int64
	var tokenHash, credentialGeneration string
	if err := store.DB().QueryRow(`SELECT s.user_id,s.token_hash,s.cred_gen
FROM sessions s JOIN users u ON u.id=s.user_id WHERE u.is_admin=1`).Scan(&adminUserID, &tokenHash, &credentialGeneration); err != nil {
		t.Fatal(err)
	}
	actor := authz.Actor{
		Kind:              authz.ActorAdminSession,
		UserID:            adminUserID,
		SessionTokenHash:  tokenHash,
		SessionGeneration: credentialGeneration,
		ElevationToken:    elevationResponse.Token,
	}
	tx, err := store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	principal, authorizeErr := app.authorizer.Authorize(context.Background(), tx, actor, authz.Requirement{
		Role:           authz.RoleAdministrator,
		FreshElevation: true,
	})
	_ = tx.Rollback()
	if authorizeErr != nil || principal.UserID != adminUserID || principal.Role != authz.RoleAdministrator {
		t.Fatalf("shared final authorization principal=%+v err=%v", principal, authorizeErr)
	}
	replayTx, err := store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	_, replayErr := app.authorizer.Authorize(context.Background(), replayTx, actor, authz.Requirement{
		Role:           authz.RoleAdministrator,
		FreshElevation: true,
	})
	_ = replayTx.Rollback()
	if !errors.Is(replayErr, authz.ErrElevatedRequired) {
		t.Fatalf("elevation replay err=%v", replayErr)
	}

	disable := testApplicationRequest(t, app.handler, http.MethodPost, auditAdminHost, "/admin/api/maintenance/disable",
		`{"expected_revision":"1","reason":"root route authorization verification"}`, []*http.Cookie{adminCookie}, map[string]string{
			"Content-Type":    "application/json",
			"Origin":          "http://" + auditAdminHost,
			"Idempotency-Key": strings.Repeat("M", 22),
		})
	if disable.Code != http.StatusOK || strings.TrimSpace(disable.Body.String()) != `{"enabled":false,"revision":"2"}` {
		t.Fatalf("disable maintenance route=%d %s", disable.Code, disable.Body.String())
	}
	if app.gate.Enabled() {
		t.Fatal("maintenance gate remained enabled")
	}
	activityTx, err := store.DB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if gateErr := (activityMutationGate{}).AuthorizeUserActivity(context.Background(), activityTx, 1); gateErr != nil {
		_ = activityTx.Rollback()
		t.Fatalf("activity final-transaction gate after maintenance disable err=%v", gateErr)
	}
	_ = activityTx.Rollback()
	for _, path := range []string{"/api/endpoints", "/api/donations", "/api/charity/models", "/api/activities", "/api/announcements", "/api/issues?state=current", "/api/games", "/api/debug/session", "/api/logs", "/api/steward/maintenance", "/api/events"} {
		unauthenticated := testApplicationRequest(t, app.handler, http.MethodGet, auditUserHost, path, "", nil, nil)
		if unauthenticated.Code != http.StatusUnauthorized {
			t.Fatalf("unauthenticated mounted route %s status=%d body=%s", path, unauthenticated.Code, unauthenticated.Body.String())
		}
	}
	unauthenticatedModels := testApplicationRequest(t, app.handler, http.MethodGet, auditUserHost, "/v1/models", "", nil, nil)
	if unauthenticatedModels.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated caller models status=%d body=%s", unauthenticatedModels.Code, unauthenticatedModels.Body.String())
	}
	unauthenticatedChat := testApplicationRequest(t, app.handler, http.MethodPost, auditUserHost, "/v1/chat/completions", `{}`, nil, map[string]string{"Content-Type": "application/json"})
	if unauthenticatedChat.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated caller chat status=%d body=%s", unauthenticatedChat.Code, unauthenticatedChat.Body.String())
	}
	callerKey := seedRootCallerIdentity(t, store)
	authorizedModels := testApplicationRequest(t, app.handler, http.MethodGet, auditUserHost, "/v1/models", "", nil, map[string]string{
		"Authorization": "Bearer " + callerKey,
	})
	if authorizedModels.Code != http.StatusOK || !strings.Contains(authorizedModels.Body.String(), `"object":"list"`) {
		t.Fatalf("authorized caller models status=%d body=%s", authorizedModels.Code, authorizedModels.Body.String())
	}
	for _, path := range []string{"/admin/api/donations", "/admin/api/charity-models", "/admin/api/activities/thursday", "/admin/api/announcements", "/admin/api/reports/badge", "/admin/api/logs", "/admin/api/games/config"} {
		authorized := testApplicationRequest(t, app.handler, http.MethodGet, auditAdminHost, path, "", []*http.Cookie{adminCookie}, nil)
		if authorized.Code != http.StatusOK {
			t.Fatalf("authenticated administrator route %s status=%d body=%s", path, authorized.Code, authorized.Body.String())
		}
	}
	invalidPublicReport := testApplicationRequest(t, app.handler, http.MethodPost, auditUserHost, "/api/reports/credential-theft", `{}`, nil, map[string]string{
		"Content-Type": "application/json",
		"Origin":       "http://" + auditUserHost,
	})
	if invalidPublicReport.Code != http.StatusBadRequest {
		t.Fatalf("invalid public report after maintenance status=%d body=%s", invalidPublicReport.Code, invalidPublicReport.Body.String())
	}

	logout := testApplicationRequest(t, app.handler, http.MethodPost, auditAdminHost, "/admin/api/logout", "", []*http.Cookie{adminCookie}, map[string]string{
		"Origin": "http://" + auditAdminHost,
	})
	if logout.Code != http.StatusNoContent {
		t.Fatalf("admin logout during maintenance status=%d body=%s", logout.Code, logout.Body.String())
	}
	stale := testApplicationRequest(t, app.handler, http.MethodGet, auditAdminHost, "/admin/api/session", "", []*http.Cookie{adminCookie}, nil)
	if stale.Code != http.StatusUnauthorized {
		t.Fatalf("logged-out admin session status=%d body=%s", stale.Code, stale.Body.String())
	}
}

func seedRootCallerIdentity(t *testing.T, store *db.Store) string {
	t.Helper()
	zero := make([]byte, 16)
	result, err := store.DB().Exec(`
INSERT INTO users(
 discord_id,username,donation_credit_mag,total_requests,total_uncached_input_tokens,
 total_cache_write_input_tokens,total_cache_read_input_tokens,total_output_tokens,
 total_unknown_usage_requests,revision,created_at,updated_at
) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
		"root-caller", "root caller", zero, zero, zero, zero, zero, zero, zero, zero, 1, 1)
	if err != nil {
		t.Fatalf("seed root caller user: %v", err)
	}
	userID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	body := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x4a}, 32))
	callerKey := "nbk_" + body
	digest := sha256.Sum256([]byte(callerKey))
	if _, err := store.DB().Exec(`
INSERT INTO caller_keys(user_id,generation,key_hash,display_head,display_tail,key_created_at,updated_at)
VALUES(?,1,?,?,?,?,?)`, userID, digest[:], body[:4], body[len(body)-4:], 1, 1); err != nil {
		t.Fatalf("seed root caller key: %v", err)
	}
	return callerKey
}

func TestApplicationCloseIsIdempotent(t *testing.T) {
	key := bytes.Repeat([]byte{0x7a}, secret.MasterKeyBytes)
	vault, err := secret.New(key)
	clear(key)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = vault.Close() }()
	dbPath := filepath.Join(t.TempDir(), "close.db")
	dbtest.EnsureOwnerOnlyParent(t, dbPath)
	store, err := db.Open(dbPath, vault)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	app, err := buildApplication(auditConfig(), store, vault)
	if err != nil {
		t.Fatal(err)
	}
	if err := app.Close(); err != nil {
		t.Fatal(err)
	}
	if err := app.Close(); err != nil {
		t.Fatal(err)
	}
	closed := testApplicationRequest(t, app.handler, http.MethodGet, auditAdminHost, "/admin/api/session", "", nil, nil)
	if closed.Code != http.StatusServiceUnavailable {
		t.Fatalf("closed authentication runtime status=%d body=%s", closed.Code, closed.Body.String())
	}
}
